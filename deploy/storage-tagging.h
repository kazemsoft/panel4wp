/* SPDX-License-Identifier: MIT
 * Used only after authority/index validation under the filesystem flock.
 * Journals remain immutable. Never sets limits or authorizes a writer.
 */
struct tag_state {
    struct plan_header header;
    struct plan_node *nodes;
    int *fds;
    uint32_t *projects;
    unsigned pinned, tagged, seen;
    uint64_t blocks;
};

static int node_index(const struct tag_state *s, uint64_t inode, unsigned tree) {
    struct plan_node key = {.inode = inode, .tree = tree};
    struct plan_node *node = bsearch(&key, s->nodes, s->header.count, sizeof(key), compare_node);
    return node ? (int)(node - s->nodes) : -1;
}

static int inode_index(const struct tag_state *s, uint64_t inode) {
    int i = node_index(s, inode, 0);
    return i >= 0 ? i : node_index(s, inode, 1);
}

static uint32_t tagged_flags(const struct plan_node *node) {
    return node->flags | (S_ISDIR(node->mode) ? FS_XFLAG_PROJINHERIT : 0);
}

static bool read_journal(int fd, struct plan_header *h, struct plan_node **nodes) {
    if (!read_all(fd, h, sizeof(*h)) || h->magic[31] || h->owner[32] || h->daemon[64] ||
        h->state[64] || h->uuid[32] || h->site[16] || !valid_header(h) ||
        !regular_metadata(fd, sizeof(*h) + h->count * sizeof(**nodes))) return false;
    *nodes = calloc(h->count, sizeof(**nodes));
    if (!*nodes || !read_all(fd, *nodes, h->count * sizeof(**nodes))) return false;
    unsigned wp = 0, db = 0;
    for (unsigned i = 0; i < h->count; i++) {
        struct plan_node *n = &(*nodes)[i];
        if (!n->inode || n->tree > 1 || (!S_ISDIR(n->mode) && !S_ISREG(n->mode)) ||
            (i && compare_node(&(*nodes)[i - 1], n) >= 0)) return false;
        if (n->tree == 0) wp++; else db++;
    }
    // The same physical inode cannot belong to both trees.
    struct tag_state lookup = {.header = *h, .nodes = *nodes};
    for (unsigned i = 0; i < wp; i++) if (node_index(&lookup, (*nodes)[i].inode, 1) >= 0) return false;
    return wp == h->wordpress && db == h->database;
}

static const char *pin_tree(int fd, unsigned tree, unsigned depth, dev_t device, struct tag_state *s) {
    struct stat st;
    struct fsxattr attr = {0};
    uint32_t generation = 0;
    if (depth > 128 || s->pinned == s->header.count) return "tree-changed";
    if (fstat(fd, &st) || st.st_dev != device || ioctl(fd, FS_IOC_GETVERSION, &generation) ||
        ioctl(fd, FS_IOC_FSGETXATTR, &attr)) return "tree-query";
    int index = node_index(s, st.st_ino, tree);
    if (index < 0 || s->fds[index] >= 0 || !st.st_nlink || (S_ISREG(st.st_mode) && st.st_nlink != 1)) return "tree-changed";
    struct plan_node *n = &s->nodes[index];
    if (n->mode != st.st_mode || n->generation != generation ||
        !((attr.fsx_projid == 0 && attr.fsx_xflags == n->flags) ||
          (attr.fsx_projid == s->header.project && attr.fsx_xflags == tagged_flags(n)))) return "tree-changed";
    s->fds[index] = dup(fd);
    if (s->fds[index] < 0) return "descriptor-limit";
    s->projects[index] = attr.fsx_projid;
    s->pinned++;
    if (attr.fsx_projid) s->tagged++;
    if (!S_ISDIR(st.st_mode)) return NULL;
    DIR *dir = fdopendir(dup(fd));
    if (!dir) return "tree-query";
    const char *reason = NULL;
    for (;;) {
        errno = 0; struct dirent *entry = readdir(dir);
        if (!entry) { if (errno) reason = "tree-query"; break; }
        if (!strcmp(entry->d_name, ".") || !strcmp(entry->d_name, "..")) continue;
        struct open_how how = {.flags = O_RDONLY | O_NOFOLLOW | O_CLOEXEC | O_NONBLOCK,
            .resolve = RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS | RESOLVE_NO_XDEV};
        int child = syscall(__NR_openat2, fd, entry->d_name, &how, sizeof(how));
        if (child < 0) { reason = "tree-changed"; break; }
        reason = pin_tree(child, tree, depth + 1, device, s);
        close(child);
        if (reason) break;
    }
    closedir(dir); return reason;
}

static const char *check_project_inode(const struct xfs_bulkstat *st, void *context) {
    struct tag_state *s = context;
    if (st->bs_projectid != s->header.project) return NULL;
    int i = inode_index(s, st->bs_ino);
    if (i < 0 || s->projects[i] != s->header.project || st->bs_gen != s->nodes[i].generation ||
        st->bs_mode != s->nodes[i].mode || st->bs_xflags != tagged_flags(&s->nodes[i]) ||
        !st->bs_nlink || (S_ISREG(st->bs_mode) && st->bs_nlink != 1)) return "project-not-owned";
    if (!st->bs_blksize || st->bs_blksize % 512 || st->bs_blocks > UINT64_MAX / (st->bs_blksize / 512)) return "quota-accounting";
    uint64_t blocks = st->bs_blocks * (st->bs_blksize / 512);
    if (s->blocks > UINT64_MAX - blocks) return "quota-accounting";
    s->blocks += blocks; s->seen++;
    return NULL;
}

static bool no_limits(const struct fs_disk_quota *q) {
    return !q->d_blk_hardlimit && !q->d_blk_softlimit && !q->d_ino_hardlimit &&
        !q->d_ino_softlimit && !q->d_rtb_hardlimit && !q->d_rtb_softlimit;
}

static const char *quota_without_limits(int root, uint32_t project, struct fs_disk_quota *q) {
    memset(q, 0, sizeof(*q));
    if (syscall(SYS_quotactl_fd, root, QCMD(Q_XGETQUOTA, XQM_PRJQUOTA), project, q)) {
        if (errno == ENOENT) return NULL;
        return "quota-query";
    }
    if (q->d_version != FS_DQUOT_VERSION || q->d_flags != FS_PROJ_QUOTA || q->d_id != project) return "quota-response";
    return no_limits(q) ? NULL : "unexpected-quota-limit";
}

static const char *verify_global_project(int root, struct tag_state *s) {
    // Flush delayed allocation before comparing native quota and inode counts.
    if (syncfs(root)) return "tag-sync";
    struct fs_disk_quota defaults, quota;
    const char *reason = quota_without_limits(root, 0, &defaults);
    if (reason) return reason;
    struct project_set set = {.slots = calloc(INVENTORY_HASH_SLOTS, sizeof(uint32_t))};
    uint64_t scanned = 0;
    s->seen = 0; s->blocks = 0;
    if (!set.slots) return "memory";
    reason = checked_census(root, &set, &scanned, check_project_inode, s);
    free(set.slots);
    if (reason) return reason;
    if (s->seen != s->tagged) return "project-not-owned";
    reason = quota_without_limits(root, s->header.project, &quota);
    if (reason) return reason;
    if (quota.d_icount != s->seen || quota.d_bcount != s->blocks || quota.d_rtbcount) return "quota-accounting";
    return NULL;
}

static const char *validate_journals(int plans, const struct burned_project *reservations,
                                   unsigned count, struct tag_state *target) {
    DIR *dir = fdopendir(dup(plans));
    if (!dir) return "plan-query";
    const char *reason = NULL;
    unsigned found = 0;
    uint64_t total = 0;
    for (;;) {
        errno = 0; struct dirent *entry = readdir(dir);
        if (!entry) { if (errno) reason = "plan-query"; break; }
        if (!strcmp(entry->d_name, ".") || !strcmp(entry->d_name, "..")) continue;
        if (!hex_string(entry->d_name, 16) || ++found > count) { reason = "plan-state"; break; }
        int fd = openat(plans, entry->d_name, O_RDONLY | O_NOFOLLOW | O_CLOEXEC | O_NONBLOCK);
        struct plan_header h = {0}; struct plan_node *nodes = NULL;
        bool good = fd >= 0 && read_journal(fd, &h, &nodes);
        if (fd >= 0) close(fd);
        good = good && !strcmp(h.site, entry->d_name) && !strcmp(h.owner, target->header.owner) &&
            !strcmp(h.daemon, target->header.daemon) && !strcmp(h.state, target->header.state) && !strcmp(h.uuid, target->header.uuid);
        bool indexed = false;
        if (good) for (unsigned i = 0; i < count; i++) if (h.project == reservations[i].project && !strcmp(h.site, reservations[i].site)) indexed = true;
        total += h.count;
        if (!good || !indexed || total > INVENTORY_MAX_INODES) reason = "plan-state";
        if (!reason && strcmp(h.site, target->header.site)) for (unsigned i = 0; i < h.count; i++) {
            if (inode_index(target, nodes[i].inode) >= 0) { reason = "journal-overlap"; break; }
        }
        free(nodes);
        if (reason) break;
    }
    closedir(dir);
    return reason ? reason : (found == count ? NULL : "plan-state-loss");
}

static const char *tag_data(int root, int wp, int db, int plans,
                           const struct burned_project *reservations, unsigned count, char **args) {
    struct tag_state s = {0};
    int fd = openat(plans, args[5], O_RDONLY | O_NOFOLLOW | O_CLOEXEC | O_NONBLOCK);
    const char *reason = NULL;
    bool valid = fd >= 0 && read_journal(fd, &s.header, &s.nodes);
    if (fd >= 0) close(fd);
    if (!valid || strcmp(s.header.owner, args[1]) || strcmp(s.header.daemon, args[2]) ||
        strcmp(s.header.state, args[3]) || strcmp(s.header.uuid, args[4]) || strcmp(s.header.site, args[5]) ||
        s.header.project != strtoull(args[6], NULL, 10)) { reason = "plan-state"; goto done; }
    for (unsigned i = 0; i < s.header.count; i++) if ((s.nodes[i].mode & (S_ISUID | S_ISGID)) ||
        (s.nodes[i].flags & (FS_XFLAG_REALTIME | FS_XFLAG_RTINHERIT | FS_XFLAG_IMMUTABLE | FS_XFLAG_APPEND))) {
        reason = "unsupported-attributes"; goto done;
    }
    reason = validate_journals(plans, reservations, count, &s);
    if (reason) goto done;
    s.fds = malloc(s.header.count * sizeof(*s.fds));
    s.projects = calloc(s.header.count, sizeof(*s.projects));
    if (s.fds) for (unsigned i = 0; i < s.header.count; i++) s.fds[i] = -1;
    if (!s.fds || !s.projects) { reason = "memory"; goto done; }
    struct stat st;
    if (fstat(root, &st)) { reason = "filesystem-root"; goto done; }
    reason = pin_tree(wp, 0, 0, st.st_dev, &s);
    if (!reason) reason = pin_tree(db, 1, 0, st.st_dev, &s);
    if (!reason && s.pinned != s.header.count) reason = "tree-changed";
    if (!reason) reason = verify_global_project(root, &s);
    if (reason) goto done;
    checkpoint("tag-before");
    unsigned changed = 0;
    for (unsigned i = 0; i < s.header.count; i++) {
        struct fsxattr attr = {0}; struct stat current;
        uint32_t generation = 0;
        if (fstat(s.fds[i], &current) || current.st_mode != s.nodes[i].mode || !current.st_nlink ||
            (S_ISREG(current.st_mode) && current.st_nlink != 1) ||
            ioctl(s.fds[i], FS_IOC_GETVERSION, &generation) || generation != s.nodes[i].generation ||
            ioctl(s.fds[i], FS_IOC_FSGETXATTR, &attr) || attr.fsx_projid != s.projects[i] ||
            attr.fsx_xflags != (s.projects[i] ? tagged_flags(&s.nodes[i]) : s.nodes[i].flags)) { reason = "tree-changed"; break; }
        if (!s.projects[i]) {
            attr.fsx_projid = s.header.project; attr.fsx_xflags = tagged_flags(&s.nodes[i]);
            if (ioctl(s.fds[i], FS_IOC_FSSETXATTR, &attr)) { reason = "tag-write"; break; }
            if (++changed == 1) checkpoint("tag-write");
            if (fsync(s.fds[i])) { reason = "tag-sync"; break; }
            s.projects[i] = s.header.project; s.tagged++;
            if (changed == 1) checkpoint("tag-one");
            if (changed == s.header.count / 2) checkpoint("tag-half");
        }
    }
    // Rewalk the paths after mutation; pinned descriptors alone cannot certify
    // that a renamed/unlinked/replaced inode still belongs to these trees.
    if (!reason) {
        for (unsigned i = 0; i < s.header.count; i++) { close(s.fds[i]); s.fds[i] = -1; }
        s.pinned = 0; s.tagged = 0;
        if (lseek(wp, 0, SEEK_SET) < 0 || lseek(db, 0, SEEK_SET) < 0) reason = "tree-query";
        if (!reason) reason = pin_tree(wp, 0, 0, st.st_dev, &s);
        if (!reason) reason = pin_tree(db, 1, 0, st.st_dev, &s);
        if (!reason && (s.pinned != s.header.count || s.tagged != s.header.count)) reason = "tree-changed";
        if (!reason) reason = verify_global_project(root, &s);
        if (!reason) checkpoint("tag-verified");
    }
done:
    if (s.fds) for (unsigned i = 0; i < s.header.count; i++) if (s.fds[i] >= 0) close(s.fds[i]);
    free(s.fds); free(s.projects); free(s.nodes);
    return reason;
}

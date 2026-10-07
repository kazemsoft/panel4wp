/* SPDX-License-Identifier: MIT
 * Durable ownership and pre-mutation inode journal. Never changes data attrs.
 */
#include "storage-common.h"
#include "storage-inventory.h"
#include <sys/file.h>
#include <signal.h>

#define PLAN_MAX_NODES 8192U
#ifndef PLAN_MAX_SITES
#define PLAN_MAX_SITES 4096U
#endif
#define AUTH_LOCK ".panel4wp-quota-lock"
#define AUTH_OWNER ".panel4wp-quota-owner"
#define AUTH_PLANS ".panel4wp-quota-plans"
#define AUTH_BURNED ".panel4wp-quota-burned"

struct burned_project { char site[17]; unsigned char reserved[3]; uint32_t project; };
_Static_assert(sizeof(struct burned_project) == 24, "stable reservation tuple");

struct plan_node { uint64_t inode; uint32_t generation, flags, mode, tree; };
struct plan_header {
    char magic[32], owner[33], daemon[65], state[65], uuid[33], site[17];
    uint32_t project, count, wordpress, database;
};
struct snapshot { struct plan_node *nodes; unsigned count, wp, db; };
_Static_assert(sizeof(struct plan_header) == 264, "stable AMD64/ARM64 journal header");
_Static_assert(sizeof(struct plan_node) == 24, "stable journal inode tuple");

static void checkpoint(const char *name) {
#ifdef AUTHORITY_TEST_CHECKPOINTS
    const char *requested = getenv("PANEL4WP_AUTHORITY_CRASH");
    if (requested && !strcmp(name, "lock") && !strcmp(requested, "hold-lock")) raise(SIGSTOP);
    if (requested && !strcmp(requested, name)) kill(getpid(), SIGKILL);
#else
    (void)name;
#endif
}

static bool hex_string(const char *s, size_t n) {
    return strlen(s) == n && strspn(s, "0123456789abcdef") == n;
}

static bool regular_metadata(int fd, size_t size) {
    struct stat st;
    return !fstat(fd, &st) && S_ISREG(st.st_mode) && st.st_nlink == 1 &&
        st.st_uid == 0 && (st.st_mode & 0777) == 0600 && (uint64_t)st.st_size == size;
}

static bool read_all(int fd, void *data, size_t n) {
    unsigned char *p = data;
    while (n) {
        ssize_t got = read(fd, p, n);
        if (got < 0 && errno == EINTR) continue;
        if (got <= 0) return false;
        p += got; n -= (size_t)got;
    }
    return true;
}

static bool write_all(int fd, const void *data, size_t n) {
    const unsigned char *p = data;
    while (n) {
        ssize_t written = write(fd, p, n);
        if (written < 0 && errno == EINTR) continue;
        if (written <= 0) return false;
        p += written; n -= (size_t)written;
    }
    return true;
}

static bool actual_root(int fd) {
    struct stat st;
    struct xfs_bulkstat_req *req = calloc(1, XFS_BULKSTAT_REQ_SIZE(1));
    if (!req) return false;
    req->hdr.flags = XFS_BULK_IREQ_SPECIAL;
    req->hdr.ino = XFS_BULK_IREQ_SPECIAL_ROOT; req->hdr.icount = 1;
    bool good = !fstat(fd, &st) && !ioctl(fd, XFS_IOC_BULKSTAT, req) && req->hdr.ocount == 1 &&
        req->bulkstat[0].bs_version == XFS_BULKSTAT_VERSION_V5 && !req->bulkstat[0].bs_sick &&
        S_ISDIR(req->bulkstat[0].bs_mode) && st.st_ino == req->bulkstat[0].bs_ino;
    free(req); return good;
}

static const char *collect_nodes(int fd, unsigned tree, unsigned depth, struct snapshot *s) {
    struct stat st;
    struct fsxattr attr = {0};
    uint32_t generation = 0;
    if (depth > 128 || s->count == PLAN_MAX_NODES) return "plan-limit";
    if (fstat(fd, &st) || ioctl(fd, FS_IOC_FSGETXATTR, &attr) || ioctl(fd, FS_IOC_GETVERSION, &generation)) return "plan-query";
    if ((!S_ISDIR(st.st_mode) && !S_ISREG(st.st_mode)) || (S_ISREG(st.st_mode) && st.st_nlink != 1) || attr.fsx_projid) return "tree-changed";
    // Realtime allocations need a separate aggregate quota design.
    if (attr.fsx_xflags & (FS_XFLAG_REALTIME | FS_XFLAG_RTINHERIT)) return "realtime-data";
    s->nodes[s->count++] = (struct plan_node){st.st_ino, generation, attr.fsx_xflags, st.st_mode, tree};
    if (tree == 0) s->wp++; else s->db++;
    if (!S_ISDIR(st.st_mode)) return NULL;
    DIR *dir = fdopendir(dup(fd));
    if (!dir) return "plan-query";
    const char *reason = NULL;
    for (;;) {
        errno = 0;
        struct dirent *entry = readdir(dir);
        if (!entry) { if (errno) reason = "plan-query"; break; }
        if (!strcmp(entry->d_name, ".") || !strcmp(entry->d_name, "..")) continue;
        struct open_how how = {.flags = O_RDONLY | O_NOFOLLOW | O_CLOEXEC | O_NONBLOCK,
            .resolve = RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS | RESOLVE_NO_XDEV};
        int child = syscall(__NR_openat2, fd, entry->d_name, &how, sizeof(how));
        if (child < 0) { reason = "tree-changed"; break; }
        reason = collect_nodes(child, tree, depth + 1, s);
        close(child);
        if (reason) break;
    }
    closedir(dir); return reason;
}

static int compare_node(const void *a, const void *b) {
    const struct plan_node *x = a, *y = b;
    if (x->tree != y->tree) return x->tree > y->tree ? 1 : -1;
    return (x->inode > y->inode) - (x->inode < y->inode);
}

static bool valid_header(const struct plan_header *h) {
    return !strcmp(h->magic, "panel4wp-quota-plan-v1") &&
        hex_string(h->owner, 32) && hex_string(h->daemon, 64) && hex_string(h->state, 64) &&
        hex_string(h->uuid, 32) && hex_string(h->site, 16) && h->project &&
        h->count <= PLAN_MAX_NODES && h->wordpress && h->database &&
        h->wordpress <= h->count && h->database <= h->count &&
        (uint64_t)h->count == (uint64_t)h->wordpress + h->database;
}

static const char *prepare(int root, int wp, int db, char **args) {
    struct stat r, a, b;
    char uuid[33], wu[33], du[33];
    struct fsxattr root_attr = {0};
    uint64_t project;
    bool initial = !strcmp(args[7], "claim");
    if (!hex_string(args[1], 32) || !hex_string(args[2], 64) || !hex_string(args[3], 64) ||
        !hex_string(args[4], 32) || !hex_string(args[5], 16) || !unsigned_number(args[6], &project) ||
        !project || project > UINT32_MAX || (!initial && strcmp(args[7], "existing"))) return "arguments";
    if (!actual_root(root) || fstat(root, &r) || fstat(wp, &a) || fstat(db, &b) ||
        !fs_identity(root, uuid) || !fs_identity(wp, wu) || !fs_identity(db, du) ||
        strcmp(uuid, args[4]) || strcmp(uuid, wu) || strcmp(uuid, du) || r.st_dev != a.st_dev || r.st_dev != b.st_dev ||
        r.st_ino == a.st_ino || r.st_ino == b.st_ino || a.st_ino == b.st_ino || r.st_uid || (r.st_mode & 0022)) return "filesystem-root";
    struct fs_quota_statv qs = {.qs_version = FS_QSTATV_VERSION1};
    if (syscall(SYS_quotactl_fd, root, QCMD(Q_XGETQSTATV, XQM_PRJQUOTA), 0, &qs) ||
        !(qs.qs_flags & FS_QUOTA_PDQ_ACCT) || !(qs.qs_flags & FS_QUOTA_PDQ_ENFD)) return "enforcement-disabled";
    if (ioctl(root, FS_IOC_FSGETXATTR, &root_attr) || root_attr.fsx_projid || (root_attr.fsx_xflags & FS_XFLAG_PROJINHERIT)) return "authority-project";
    bool fresh = false;
    int lock = openat(root, AUTH_LOCK, O_CREAT | O_EXCL | O_RDWR | O_NOFOLLOW | O_CLOEXEC | O_NONBLOCK, 0600);
    if (lock >= 0) fresh = true;
    else if (errno == EEXIST) lock = openat(root, AUTH_LOCK, O_RDWR | O_NOFOLLOW | O_CLOEXEC | O_NONBLOCK);
    if (lock < 0 || !regular_metadata(lock, 0)) { if (lock >= 0) close(lock); return "authority-lock"; }
    const char *reason = NULL;
    int plans = -1, burned = -1;
    if (flock(lock, LOCK_EX | LOCK_NB)) { reason = "authority-busy"; goto done; }
    if (fsync(lock) || fsync(root)) { reason = "authority-sync"; goto done; }
    checkpoint("lock");
    struct stat lock_stat, previous;
    uint32_t lock_generation = 0;
    if (fstat(lock, &lock_stat) || ioctl(lock, FS_IOC_GETVERSION, &lock_generation)) { reason = "authority-lock"; goto done; }
    if (fresh && (fstatat(root, AUTH_OWNER, &previous, AT_SYMLINK_NOFOLLOW) == 0 ||
                  fstatat(root, AUTH_PLANS, &previous, AT_SYMLINK_NOFOLLOW) == 0 ||
                  fstatat(root, AUTH_BURNED, &previous, AT_SYMLINK_NOFOLLOW) == 0)) { reason = "authority-state-loss"; goto done; }
    bool initialize = fresh && initial && fstatat(root, AUTH_OWNER, &previous, AT_SYMLINK_NOFOLLOW) < 0 && errno == ENOENT;
    if (initialize) {
        if (mkdirat(root, AUTH_PLANS, 0700)) { reason = "authority-initialization"; goto done; }
        burned = openat(root, AUTH_BURNED, O_CREAT | O_EXCL | O_RDWR | O_NOFOLLOW | O_CLOEXEC, 0600);
        if (burned < 0 || fsync(burned) || fsync(root)) { reason = "authority-initialization"; goto done; }
    } else burned = openat(root, AUTH_BURNED, O_RDWR | O_NOFOLLOW | O_CLOEXEC | O_NONBLOCK);
    plans = openat(root, AUTH_PLANS, O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC);
    struct stat ps, bs;
    uint32_t plans_generation = 0, burned_generation = 0;
    if (plans < 0 || fstat(plans, &ps) || ps.st_dev != r.st_dev || ps.st_uid || (ps.st_mode & 0777) != 0700 ||
        ioctl(plans, FS_IOC_GETVERSION, &plans_generation) || burned < 0 || fstat(burned, &bs) ||
        bs.st_dev != r.st_dev || bs.st_size < 0 || bs.st_size % sizeof(struct burned_project) ||
        (uint64_t)bs.st_size > PLAN_MAX_SITES * sizeof(struct burned_project) || !regular_metadata(burned, (size_t)bs.st_size) ||
        ioctl(burned, FS_IOC_GETVERSION, &burned_generation)) { reason = "authority-plans"; goto done; }
    char owner[512];
    int length = snprintf(owner, sizeof(owner), "panel4wp-quota-authority-v1\n%s\n%s\n%s\n%s\n%" PRIu64 ":%" PRIu32 "\n%" PRIu64 ":%" PRIu32 "\n%" PRIu64 ":%" PRIu32 "\n", args[1], args[2], args[3], args[4], (uint64_t)lock_stat.st_ino, lock_generation, (uint64_t)ps.st_ino, plans_generation, (uint64_t)bs.st_ino, burned_generation);
    int fd = openat(root, AUTH_OWNER, O_RDONLY | O_NOFOLLOW | O_CLOEXEC | O_NONBLOCK);
    if (fd < 0 && errno == ENOENT && fresh && initial) {
        fd = openat(root, AUTH_OWNER, O_CREAT | O_EXCL | O_WRONLY | O_NOFOLLOW | O_CLOEXEC, 0600);
        if (fd < 0) { reason = "authority-owner"; goto done; }
        bool durable = write_all(fd, owner, (size_t)length) && !fsync(fd) && !fsync(root);
        close(fd);
        if (!durable) { reason = "authority-sync"; goto done; }
        checkpoint("owner");
    } else {
        char saved[512];
        bool matches = fd >= 0 && regular_metadata(fd, (size_t)length) && read_all(fd, saved, (size_t)length) && !memcmp(saved, owner, (size_t)length);
        if (fd >= 0) close(fd);
        if (!matches) { reason = "authority-owner"; goto done; }
    }
    struct burned_project reservations[PLAN_MAX_SITES];
    unsigned reservation_count = (unsigned)(bs.st_size / sizeof(struct burned_project));
    if (!read_all(burned, reservations, (size_t)bs.st_size)) { reason = "authority-reservations"; goto done; }
    for (unsigned i = 0; i < reservation_count; i++) {
        if (reservations[i].site[16] || !hex_string(reservations[i].site, 16) || !reservations[i].project ||
            reservations[i].reserved[0] || reservations[i].reserved[1] || reservations[i].reserved[2]) { reason = "authority-reservations"; goto done; }
        for (unsigned j = 0; j < i; j++) if (reservations[i].project == reservations[j].project || !strcmp(reservations[i].site, reservations[j].site)) { reason = "authority-reservations"; goto done; }
    }
    struct project_set set = {.slots = calloc(INVENTORY_HASH_SLOTS, sizeof(uint32_t))};
    uint64_t scanned = 0, wp_count = 0, db_count = 0;
    if (!set.slots) { reason = "memory"; goto done; }
    reason = census(wp, &set, &scanned);
    if (!reason) for (unsigned i = 0; i < INVENTORY_HASH_SLOTS; i++) if (set.slots[i] == project) { reason = "project-occupied"; break; }
    free(set.slots);
    if (reason) goto done;
    reason = audit_tree(wp, a.st_dev, b.st_ino, 0, &wp_count);
    if (!reason) reason = audit_tree(db, b.st_dev, a.st_ino, 0, &db_count);
    if (reason) goto done;
    // Audit used dup descriptors, so rewind before taking the inode snapshot.
    if (lseek(wp, 0, SEEK_SET) < 0 || lseek(db, 0, SEEK_SET) < 0) { reason = "plan-query"; goto done; }
    struct snapshot snapshot = {.nodes = calloc(PLAN_MAX_NODES, sizeof(struct plan_node))};
    if (!snapshot.nodes) { reason = "memory"; goto done; }
    reason = collect_nodes(wp, 0, 0, &snapshot);
    if (!reason) reason = collect_nodes(db, 1, 0, &snapshot);
    if (!reason && (snapshot.wp != wp_count || snapshot.db != db_count || snapshot.count > scanned)) reason = "tree-changed";
    qsort(snapshot.nodes, snapshot.count, sizeof(struct plan_node), compare_node);
    struct plan_header h = {0};
    strcpy(h.magic, "panel4wp-quota-plan-v1"); strcpy(h.owner, args[1]); strcpy(h.daemon, args[2]); strcpy(h.state, args[3]); strcpy(h.uuid, args[4]); strcpy(h.site, args[5]);
    h.project = (uint32_t)project; h.count = snapshot.count; h.wordpress = snapshot.wp; h.database = snapshot.db;
    unsigned existing_count = 0;
    if (!reason) {
        DIR *dir = fdopendir(dup(plans));
        if (!dir) reason = "plan-query";
        else {
            for (;;) {
                errno = 0; struct dirent *entry = readdir(dir);
                if (!entry) { if (errno) reason = "plan-query"; break; }
                if (!strcmp(entry->d_name, ".") || !strcmp(entry->d_name, "..")) continue;
                if (!hex_string(entry->d_name, 16) || ++existing_count > PLAN_MAX_SITES) { reason = "plan-state"; break; }
                fd = openat(plans, entry->d_name, O_RDONLY | O_NOFOLLOW | O_CLOEXEC | O_NONBLOCK);
                struct plan_header saved = {0};
                struct stat st;
                bool valid = fd >= 0 && !fstat(fd, &st) && st.st_size >= (off_t)sizeof(saved) && read_all(fd, &saved, sizeof(saved));
                // Force string terminators before validating untrusted disk data.
                valid = valid && saved.magic[31] == 0 && saved.owner[32] == 0 && saved.daemon[64] == 0 && saved.state[64] == 0 && saved.uuid[32] == 0 && saved.site[16] == 0 && valid_header(&saved);
                valid = valid && regular_metadata(fd, sizeof(saved) + saved.count * sizeof(struct plan_node)) &&
                    !strcmp(saved.owner, h.owner) && !strcmp(saved.daemon, h.daemon) && !strcmp(saved.state, h.state) && !strcmp(saved.uuid, h.uuid) && !strcmp(saved.site, entry->d_name);
                bool indexed = false;
                if (valid) for (unsigned i = 0; i < reservation_count; i++) if (!strcmp(saved.site, reservations[i].site) && saved.project == reservations[i].project) { indexed = true; break; }
                valid = valid && indexed;
                if (fd >= 0) close(fd);
                if (!valid) { reason = "plan-state"; break; }
                if (saved.project == h.project && strcmp(saved.site, h.site)) { reason = "project-reserved"; break; }
            }
            closedir(dir);
        }
    }
    if (!reason && existing_count != reservation_count) reason = "plan-state-loss";
    if (!reason) {
        fd = openat(plans, args[5], O_RDONLY | O_NOFOLLOW | O_CLOEXEC | O_NONBLOCK);
        size_t body_size = snapshot.count * sizeof(struct plan_node);
        if (fd >= 0) {
            struct plan_header saved;
            void *nodes = malloc(body_size);
            bool same = nodes && regular_metadata(fd, sizeof(h) + body_size) && read_all(fd, &saved, sizeof(saved)) &&
                !memcmp(&saved, &h, sizeof(h)) && read_all(fd, nodes, body_size) && !memcmp(nodes, snapshot.nodes, body_size);
            free(nodes); close(fd);
            if (!same) reason = "plan-changed";
        } else if (errno != ENOENT || !initial) reason = "plan-missing";
        else if (existing_count >= PLAN_MAX_SITES) reason = "plan-limit";
        else {
            struct burned_project reservation = {0};
            strcpy(reservation.site, args[5]); reservation.project = (uint32_t)project;
            // Burn before publishing the journal. Interrupted writes fail closed.
            if (lseek(burned, 0, SEEK_END) < 0 || !write_all(burned, &reservation, sizeof(reservation)) || fsync(burned)) { reason = "plan-sync"; goto snapshot_done; }
            checkpoint("reservation");
            char tmp[64]; snprintf(tmp, sizeof(tmp), ".%s-pending", args[5]);
            fd = openat(plans, tmp, O_CREAT | O_EXCL | O_WRONLY | O_NOFOLLOW | O_CLOEXEC, 0600);
            if (fd < 0) reason = "plan-state";
            else {
                bool durable = write_all(fd, &h, sizeof(h)) && write_all(fd, snapshot.nodes, body_size) && !fsync(fd);
                close(fd);
                if (durable) checkpoint("journal-file");
                if (!durable || renameat(plans, tmp, plans, args[5]) || fsync(plans)) reason = "plan-sync";
                if (!reason) checkpoint("journal");
            }
        }
    }
snapshot_done:
    free(snapshot.nodes);
done:
    if (burned >= 0) close(burned);
    if (plans >= 0) close(plans);
    close(lock);
    return reason;
}

int main(int argc, char **argv) {
    if (argc != 9 || strcmp(argv[1], "--prepare")) return 1;
    int root = open("/quota-filesystem", O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC);
    int wp = open("/quota-wordpress", O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC);
    int db = open("/quota-database", O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC);
    if (root < 0 || wp < 0 || db < 0) return 1;
    const char *reason = prepare(root, wp, db, argv + 1);
    printf("{\"verified\":%s,\"reason\":\"%s\"}\n", reason ? "false" : "true", reason ? reason : "prepared");
    close(root); close(wp); close(db); return 0;
}

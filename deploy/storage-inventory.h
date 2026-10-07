/* SPDX-License-Identifier: MIT
 * Read-only reservation preflight. Not a snapshot or authorization to retag.
 */
#include <dirent.h>
#include <linux/openat2.h>

#ifndef INVENTORY_MAX_INODES
#define INVENTORY_MAX_INODES 1000000U
#endif
#define INVENTORY_MAX_PROJECTS 65536U
#define INVENTORY_HASH_SLOTS (INVENTORY_MAX_PROJECTS * 2U)

struct project_set { uint32_t *slots; unsigned count; };

static bool add_project(struct project_set *set, uint32_t id) {
    if (!id) return true;
    unsigned slot = (id * 2654435761U) & (INVENTORY_HASH_SLOTS - 1);
    while (set->slots[slot] && set->slots[slot] != id) slot = (slot + 1) & (INVENTORY_HASH_SLOTS - 1);
    if (set->slots[slot] == id) return true;
    if (set->count == INVENTORY_MAX_PROJECTS) return false;
    set->slots[slot] = id; set->count++; return true;
}

static const char *census(int fd, struct project_set *set, uint64_t *scanned) {
    struct xfs_bulkstat_req *req = calloc(1, XFS_BULKSTAT_REQ_SIZE(64));
    if (!req) return "memory";
    const char *reason = NULL;
    uint64_t cursor = 0;
    for (;;) {
        memset(req, 0, XFS_BULKSTAT_REQ_SIZE(64));
        req->hdr.ino = cursor; req->hdr.icount = 64;
        if (ioctl(fd, XFS_IOC_BULKSTAT, req)) { reason = "inode-query"; break; }
        if (req->hdr.ocount > 64) { reason = "inode-response"; break; }
        if (!req->hdr.ocount) break;
        if (req->hdr.ino <= cursor) { reason = "inode-cursor"; break; }
        uint64_t previous = cursor;
        for (unsigned n = 0; n < req->hdr.ocount; n++) {
            struct xfs_bulkstat *stat = &req->bulkstat[n];
            if (stat->bs_version != XFS_BULKSTAT_VERSION_V5 || stat->bs_sick ||
                stat->bs_ino < previous || stat->bs_ino >= req->hdr.ino) { reason = "inode-response"; break; }
            previous = stat->bs_ino + 1;
            if (++*scanned > INVENTORY_MAX_INODES || !add_project(set, stat->bs_projectid)) { reason = "scan-limit"; break; }
        }
        if (reason) break;
        cursor = req->hdr.ino;
        // Short batches can precede a kernel error: only a zero batch is EOF.
    }
    free(req);
    if (reason) return reason;
    uint32_t next = 0;
    for (unsigned n = 0; n <= INVENTORY_MAX_PROJECTS; n++) {
        struct fs_disk_quota quota = {0};
        if (syscall(SYS_quotactl_fd, fd, QCMD(Q_XGETNEXTQUOTA, XQM_PRJQUOTA), next, &quota)) {
            // Modern Linux get_nextdqblk returns ENOENT at end of enumeration.
            // ESRCH can mean quotas disappeared; it must not certify a census.
            return errno == ENOENT ? NULL : "quota-census";
        }
        if (quota.d_version != FS_DQUOT_VERSION || quota.d_flags != FS_PROJ_QUOTA || quota.d_id < next) return "quota-response";
        if (!add_project(set, quota.d_id)) return "scan-limit";
        if (quota.d_id == UINT32_MAX) return NULL;
        next = quota.d_id + 1;
    }
    return "scan-limit";
}

static const char *audit_tree(int fd, dev_t device, ino_t other_root, unsigned depth, uint64_t *count) {
    struct stat stat;
    struct fsxattr attr = {0};
    if (depth > 128 || ++*count > INVENTORY_MAX_INODES) return "scan-limit";
    if (fstat(fd, &stat)) return "tree-query";
    if (stat.st_dev != device) return "cross-mount";
    if (stat.st_ino == other_root) return "overlapping-roots";
    if (!S_ISDIR(stat.st_mode) && !S_ISREG(stat.st_mode)) return "special-file";
    if (S_ISREG(stat.st_mode) && stat.st_nlink != 1) return "hardlink";
    if (ioctl(fd, FS_IOC_FSGETXATTR, &attr)) return "attribute-query";
    if (attr.fsx_projid) return "foreign-project";
    if (!S_ISDIR(stat.st_mode)) return NULL;
    DIR *dir = fdopendir(dup(fd));
    if (!dir) return "tree-query";
    const char *reason = NULL;
    for (;;) {
        errno = 0;
        struct dirent *entry = readdir(dir);
        if (!entry) { if (errno) reason = "tree-query"; break; }
        if (!strcmp(entry->d_name, ".") || !strcmp(entry->d_name, "..")) continue;
        struct stat child;
        if (fstatat(fd, entry->d_name, &child, AT_SYMLINK_NOFOLLOW)) { reason = "tree-query"; break; }
        if (S_ISLNK(child.st_mode)) { reason = "symlink"; break; }
        if (!S_ISDIR(child.st_mode) && !S_ISREG(child.st_mode)) { reason = "special-file"; break; }
        struct open_how how = {.flags = O_RDONLY | O_NOFOLLOW | O_CLOEXEC | O_NONBLOCK,
            .resolve = RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS | RESOLVE_NO_XDEV};
        int opened = syscall(__NR_openat2, fd, entry->d_name, &how, sizeof(how));
        if (opened < 0) { reason = errno == EXDEV ? "cross-mount" : "tree-query"; break; }
        struct stat actual;
        if (fstat(opened, &actual) || actual.st_dev != child.st_dev || actual.st_ino != child.st_ino ||
            actual.st_mode != child.st_mode) reason = "tree-changed";
        else reason = audit_tree(opened, device, other_root, depth + 1, count);
        close(opened);
        if (reason) break;
    }
    closedir(dir);
    return reason;
}

static int compare_project(const void *a, const void *b) {
    uint32_t x = *(const uint32_t *)a, y = *(const uint32_t *)b;
    return (x > y) - (x < y);
}

static inline int inventory(int wp, int db, const struct stat *a, const struct stat *b,
                     bool same, bool accounting, bool enforcement) {
    struct project_set set = {.slots = calloc(INVENTORY_HASH_SLOTS, sizeof(uint32_t))};
    char uuid[33] = "", db_uuid[33];
    uint64_t scanned = 0, wp_count = 0, db_count = 0;
    const char *reason = NULL;
    if (!set.slots) reason = "memory";
    else if (!same || !fs_identity(wp, uuid) || !fs_identity(db, db_uuid) || strcmp(uuid, db_uuid)) reason = "filesystem";
    else if (!accounting || !enforcement) reason = "enforcement-disabled";
    else if (a->st_ino == b->st_ino) reason = "overlapping-roots";
    else reason = census(wp, &set, &scanned);
    if (!reason) reason = audit_tree(wp, a->st_dev, b->st_ino, 0, &wp_count);
    if (!reason) reason = audit_tree(db, b->st_dev, a->st_ino, 0, &db_count);
    if (!reason && wp_count + db_count > scanned) reason = "tree-changed";
    uint32_t *sorted = NULL;
    if (!reason) {
        sorted = calloc(set.count + 1, sizeof(uint32_t));
        if (!sorted) reason = "memory";
        else {
            unsigned n = 0;
            for (unsigned slot = 0; slot < INVENTORY_HASH_SLOTS; slot++) if (set.slots[slot]) sorted[n++] = set.slots[slot];
            qsort(sorted, set.count, sizeof(uint32_t), compare_project);
        }
    }
    // No partial inventory is ever returned as a usable list of free IDs.
    printf("{\"verified\":%s,\"reason\":\"%s\",\"filesystem_uuid\":\"%s\",\"scanned_inodes\":%" PRIu64
           ",\"wordpress_inodes\":%" PRIu64 ",\"database_inodes\":%" PRIu64 ",\"project_ids\":[",
           reason ? "false" : "true", reason ? reason : "verified", uuid, scanned, wp_count, db_count);
    if (!reason) for (unsigned n = 0; n < set.count; n++) printf("%s%" PRIu32, n ? "," : "", sorted[n]);
    puts("]}");
    free(sorted); free(set.slots); return 0;
}

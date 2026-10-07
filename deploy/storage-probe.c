/* SPDX-License-Identifier: MIT
 * Read-only Linux capability probe. Fixed mount paths; no quota mutation.
 */
#include "storage-common.h"

static const char *filesystem(long type) {
    switch (type) {
    case XFS_SUPER_MAGIC: return "xfs";
    case EXT4_SUPER_MAGIC: return "ext4";
    case BTRFS_SUPER_MAGIC: return "btrfs";
    case OVERLAYFS_SUPER_MAGIC: return "overlay";
    case TMPFS_MAGIC: return "tmpfs";
    default: return "other";
    }
}

static int guard(int wp, int db, const char *id_text, const char *bytes_text, const char *expected_fs,
                 const struct statfs *fs, bool same, bool accounting, bool enforcement) {
    uint64_t id, hard_bytes;
    char identity[33], db_identity[33];
    const char *reason = "verified";
    if (!unsigned_number(id_text, &id) || !id || id > UINT32_MAX ||
        !unsigned_number(bytes_text, &hard_bytes) || !hard_bytes || hard_bytes % 512 ||
        strlen(expected_fs) != 32) reason = "invalid-expectation";
    else if (fs->f_type != XFS_SUPER_MAGIC || !same) reason = "filesystem";
    else if (!fs_identity(wp, identity) || !fs_identity(db, db_identity) ||
             strcmp(identity, expected_fs) || strcmp(db_identity, expected_fs)) reason = "filesystem-identity";
    else if (!accounting || !enforcement) reason = "enforcement-disabled";
    else {
        struct fsxattr wa = {0}, da = {0};
        struct fs_disk_quota quota = {0};
        if (ioctl(wp, FS_IOC_FSGETXATTR, &wa) || ioctl(db, FS_IOC_FSGETXATTR, &da)) reason = "attribute-query";
        else if (wa.fsx_projid != id || da.fsx_projid != id ||
                 !(wa.fsx_xflags & FS_XFLAG_PROJINHERIT) || !(da.fsx_xflags & FS_XFLAG_PROJINHERIT)) reason = "project-attributes";
        else if (syscall(SYS_quotactl_fd, wp, QCMD(Q_XGETQUOTA, XQM_PRJQUOTA), (unsigned int)id, &quota)) reason = "limit-query";
        else if (quota.d_version != FS_DQUOT_VERSION || quota.d_flags != FS_PROJ_QUOTA || quota.d_id != id ||
                 quota.d_blk_hardlimit != hard_bytes / 512) reason = "hard-limit";
        else if (quota.d_bcount > quota.d_blk_hardlimit) reason = "over-limit";
    }
    printf("{\"verified\":%s,\"reason\":\"%s\"}\n", !strcmp(reason, "verified") ? "true" : "false", reason);
    return 0;
}

#include "storage-inventory.h"

int main(int argc, char **argv) {
    int wp = open("/quota-wordpress", O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC);
    int db = open("/quota-database", O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC);
    struct stat a, b;
    struct statfs af, bf;
    if (wp < 0 || db < 0 || fstat(wp, &a) || fstat(db, &b) || fstatfs(wp, &af) || fstatfs(db, &bf)) {
        fputs("cannot inspect both volume roots\n", stderr);
        return 1;
    }
    bool same = a.st_dev == b.st_dev && memcmp(&af.f_fsid, &bf.f_fsid, sizeof(af.f_fsid)) == 0;
    bool accounting = false, enforcement = false;
    const char *reason = "filesystem";
    if (af.f_type == XFS_SUPER_MAGIC && bf.f_type == XFS_SUPER_MAGIC) {
        reason = "different-filesystems";
        if (same) {
            struct fs_quota_statv qs = {.qs_version = FS_QSTATV_VERSION1};
            if (syscall(SYS_quotactl_fd, wp, QCMD(Q_XGETQSTATV, XQM_PRJQUOTA), 0, &qs) < 0) {
                reason = errno == ENOSYS ? "kernel" : "quota-query";
            } else {
                accounting = (qs.qs_flags & FS_QUOTA_PDQ_ACCT) != 0;
                enforcement = (qs.qs_flags & FS_QUOTA_PDQ_ENFD) != 0;
                reason = accounting && enforcement ? "supported" : "enforcement-disabled";
            }
        }
    }
    if (argc == 2 && !strcmp(argv[1], "--identity")) {
        if (!same || af.f_type != XFS_SUPER_MAGIC) return 1;
        char identity[33]; if (!fs_identity(wp, identity)) return 1; puts(identity); return 0;
    }
    if (argc == 2 && !strcmp(argv[1], "--inventory")) {
        return inventory(wp, db, &a, &b, same, accounting, enforcement);
    }
    if (argc == 5 && !strcmp(argv[1], "--guard")) {
        return guard(wp, db, argv[2], argv[3], argv[4], &af, same, accounting, enforcement);
    }
    if (argc != 1) { fputs("invalid probe arguments\n", stderr); return 1; }
    printf("{\"wordpress_filesystem\":\"%s\",\"database_filesystem\":\"%s\","
           "\"same_filesystem\":%s,\"project_accounting\":%s,\"project_enforcement\":%s,\"reason\":\"%s\"}\n",
           filesystem(af.f_type), filesystem(bf.f_type), same ? "true" : "false",
           accounting ? "true" : "false", enforcement ? "true" : "false", reason);
    close(wp);
    close(db);
    return 0;
}

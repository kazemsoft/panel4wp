/* SPDX-License-Identifier: MIT
 * Read-only Linux capability probe. Fixed mount paths; no quota mutation.
 */
#define _GNU_SOURCE
#include <asm/unistd.h>
#include <errno.h>
#include <fcntl.h>
#include <linux/dqblk_xfs.h>
#include <linux/magic.h>
#include <stdbool.h>
#include <stdio.h>
#include <string.h>
#include <sys/quota.h>
#include <sys/stat.h>
#include <sys/syscall.h>
#include <sys/vfs.h>
#include <unistd.h>

#ifndef SYS_quotactl_fd
#define SYS_quotactl_fd __NR_quotactl_fd
#endif

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

int main(void) {
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
    printf("{\"wordpress_filesystem\":\"%s\",\"database_filesystem\":\"%s\","
           "\"same_filesystem\":%s,\"project_accounting\":%s,\"project_enforcement\":%s,\"reason\":\"%s\"}\n",
           filesystem(af.f_type), filesystem(bf.f_type), same ? "true" : "false",
           accounting ? "true" : "false", enforcement ? "true" : "false", reason);
    close(wp);
    close(db);
    return 0;
}

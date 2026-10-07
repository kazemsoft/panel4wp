/* SPDX-License-Identifier: MIT */
#ifndef PANEL4WP_STORAGE_COMMON
#define PANEL4WP_STORAGE_COMMON
#define _GNU_SOURCE
#include <asm/unistd.h>
#include <errno.h>
#include <fcntl.h>
#include <linux/dqblk_xfs.h>
#include <linux/magic.h>
#include <linux/fs.h>
#include <inttypes.h>
#include <stdint.h>
#include <stdlib.h>
#include <sys/ioctl.h>
#include <stdbool.h>
#include <stdio.h>
#include <string.h>
#include <sys/quota.h>
#include <sys/stat.h>
#include <sys/syscall.h>
#include <sys/vfs.h>
#include <unistd.h>
#include <xfs/xfs.h>

#ifndef SYS_quotactl_fd
#define SYS_quotactl_fd __NR_quotactl_fd
#endif

static inline bool fs_identity(int fd, char output[33]) {
    struct xfs_fsop_geom_v1 geometry = {0};
    if (ioctl(fd, XFS_IOC_FSGEOMETRY_V1, &geometry)) return false;
    for (int n = 0; n < 16; n++) snprintf(output + n * 2, 3, "%02x", geometry.uuid[n]);
    return true;
}

static inline bool unsigned_number(const char *text, uint64_t *value) {
    if (!*text) return false;
    for (const char *p = text; *p; p++) if (*p < '0' || *p > '9') return false;
    errno = 0;
    char *end;
    *value = strtoull(text, &end, 10);
    return !errno && !*end;
}

#endif

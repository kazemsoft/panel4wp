#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <linux/fs.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/stat.h>
#include <sys/syscall.h>
#include <unistd.h>

static void fail(const char *what) { perror(what); exit(1); }
static void mutation(int fd, unsigned long command, void *arg, int blocked) {
 errno=0;
 long result=syscall(SYS_ioctl,fd,command,arg);
 if (blocked ? (result != -1 || errno != EPERM) : result != 0) {
  fprintf(stderr,"ioctl 0x%lx expected %s: result %ld errno %d\n",command,blocked?"EPERM":"success",result,errno);
  exit(1);
 }
}
int main(int argc, char **argv) {
 int blocked=argc>=2 && !strcmp(argv[1],"blocked");
 if (!blocked && (argc<2 || strcmp(argv[1],"control"))) return 2;
 int fd=open(argc>=3?argv[2]:"/data/owner-file",O_CREAT|O_EXCL|O_RDWR|O_CLOEXEC,0600);
 if(fd<0) fail("open");
 if(fchown(fd,33,33)<0 || setgid(33)<0 || setuid(33)<0) fail("UID33");
 if(write(fd,"ordinary-write",14)!=14 || fsync(fd)<0 || lseek(fd,0,SEEK_SET)!=0) fail("write");
 char buffer[32]={0};
 if(read(fd,buffer,14)!=14 || strcmp(buffer,"ordinary-write")) fail("read");
 struct fsxattr attrs={0};
 if(ioctl(fd,FS_IOC_FSGETXATTR,&attrs)<0) fail("FSGETXATTR");
 // Default Docker permits an inode owner to change its own project attributes.
 // Keep them unchanged in the negative control, so no quota-enabled host is needed.
 mutation(fd,FS_IOC_FSSETXATTR,&attrs,blocked);
 if(blocked) {
  mutation(fd,0xf123456700000000UL | (uint32_t)FS_IOC_FSSETXATTR,&attrs,1);
  mutation(fd,0xffffffff00000000UL | (uint32_t)FS_IOC_FSSETXATTR,&attrs,1);
  unsigned long flags=0;
  mutation(fd,0x40086602UL,&flags,1);
  mutation(fd,0x40046602UL,&flags,1);
  mutation(fd,0x9876543240086602UL,&flags,1);
  mutation(fd,0xffffffff40046602UL,&flags,1);
 }
 close(fd);
 // Unrelated ioctl must still work; test the generic baseline allowance.
 int pipefd[2],available=0;
 if(pipe(pipefd)<0 || write(pipefd[1],"test",4)!=4 || ioctl(pipefd[0],FIONREAD,&available)<0 || available!=4) fail("benign ioctl");
 close(pipefd[0]);close(pipefd[1]);
 puts(blocked?"WRITER_POLICY_PASS":"WRITER_POLICY_CONTROL_PASS");
 return 0;
}

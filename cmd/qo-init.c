#define _GNU_SOURCE
#include <sched.h>
#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>
#include <sys/mount.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <sys/wait.h>
#include <string.h>
#include <errno.h>
#include <fcntl.h>

#define STACK_SIZE (1024 * 1024)

static void write_userns_map(const char *path, const char *content) {
    int fd = open(path, O_WRONLY);
    if (fd < 0) {
        perror(path);
        _exit(1);
    }
    ssize_t len = strlen(content);
    if (write(fd, content, len) != len) {
        perror("write map");
        close(fd);
        _exit(1);
    }
    close(fd);
}

static int setup_userns(void) {
    int uid = getuid();
    int gid = getgid();
    char buf[256];

    snprintf(buf, sizeof(buf), "0 %d 1\n", uid);
    write_userns_map("/proc/self/uid_map", buf);

    snprintf(buf, sizeof(buf), "deny");
    write_userns_map("/proc/self/setgroups", buf);

    snprintf(buf, sizeof(buf), "0 %d 1\n", gid);
    write_userns_map("/proc/self/gid_map", buf);

    return 0;
}

static int child(void *arg) {
    const char *rootfsPath = (const char *)arg;

    if (mount(NULL, "/", NULL, MS_REC | MS_PRIVATE, NULL) != 0) {
        perror("mount private");
        return 1;
    }

    if (setup_userns() != 0) {
        fprintf(stderr, "Failed to setup user namespace\n");
        return 1;
    }

    char chrootPath[4096];
    snprintf(chrootPath, sizeof(chrootPath), "%s/rootfs", rootfsPath);

    if (chroot(chrootPath) != 0) {
        perror("chroot");
        return 1;
    }

    if (mkdir("/proc", 0555) != 0 && errno != EEXIST) {
        perror("mkdir /proc");
        return 1;
    }

    if (mount("proc", "/proc", "proc", 0, "") != 0) {
        perror("mount proc");
        return 1;
    }

    if (mkdir("/dev/pts", 0755) != 0 && errno != EEXIST) {
        perror("mkdir /dev/pts");
        return 1;
    }

    if (mount("devpts", "/dev/pts", "devpts", 0, "newinstance,ptmxmode=0666,mode=0620") != 0) {
        perror("mount devpts");
        return 1;
    }

    unlink("/dev/ptmx");
    if (symlink("pts/ptmx", "/dev/ptmx") != 0) {
        perror("symlink /dev/ptmx");
        return 1;
    }

    if (chdir("/tmp") != 0) {
        perror("chdir /tmp");
        return 1;
    }

    execl("/bin/bash", "/bin/bash", "-i", NULL);
    perror("execl");
    return 1;
}

int main(int argc, char **argv) {
    if (argc < 2) {
        fprintf(stderr, "Usage: %s <rootfs-path>\n", argv[0]);
        return 1;
    }

    char *stack = malloc(STACK_SIZE);
    if (!stack) {
        perror("malloc");
        return 1;
    }

    pid_t pid = clone(child, stack + STACK_SIZE,
                      CLONE_NEWUSER | CLONE_NEWUTS | CLONE_NEWPID | CLONE_NEWNS | CLONE_NEWNET | SIGCHLD,
                      (void *)argv[1]);
    if (pid == -1) {
        perror("clone");
        free(stack);
        return 1;
    }

    waitpid(pid, NULL, 0);
    free(stack);
    return 0;
}

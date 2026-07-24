#define _GNU_SOURCE
#include <sched.h>
#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>
#include <sys/mount.h>
#include <sys/stat.h>
#include <sys/syscall.h>
#include <sys/types.h>
#include <sys/wait.h>
#include <string.h>
#include <errno.h>
#include <fcntl.h>
#include <sys/ioctl.h>
#include <net/if.h>

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

static int switch_root(const char *rootfsPath) {
    char oldRoot[4096];
    snprintf(oldRoot, sizeof(oldRoot), "%s/rootfs/.pivot_old", rootfsPath);

    if (mount(rootfsPath, rootfsPath, "bind", MS_BIND | MS_REC, "") != 0) {
        perror("mount bind");
        return -1;
    }

    if (mkdir(oldRoot, 0700) != 0 && errno != EEXIST) {
        perror("mkdir pivot_old");
        return -1;
    }

    if (chdir(rootfsPath) != 0) {
        perror("chdir rootfs");
        return -1;
    }

    if (syscall(SYS_pivot_root, rootfsPath, oldRoot) != 0) {
        perror("pivot_root");
        return -1;
    }

    if (chdir("/") != 0) {
        perror("chdir /");
        return -1;
    }

    if (umount2("/.pivot_old", MNT_DETACH) != 0) {
        perror("umount pivot_old");
        return -1;
    }

    if (rmdir("/.pivot_old") != 0 && errno != ENOENT) {
        perror("rmdir pivot_old");
        return -1;
    }

    return 0;
}

static int setup_loopback(void) {
    int sock = socket(AF_INET, SOCK_DGRAM, 0);
    if (sock < 0) {
        perror("socket");
        return -1;
    }

    struct ifreq ifr;
    memset(&ifr, 0, sizeof(ifr));
    strncpy(ifr.ifr_name, "lo", IFNAMSIZ - 1);
    ifr.ifr_flags = IFF_UP | IFF_LOOPBACK | IFF_RUNNING;

    if (ioctl(sock, SIOCSIFFLAGS, &ifr) != 0) {
        perror("ioctl SIOCSIFFLAGS");
        close(sock);
        return -1;
    }

    close(sock);
    return 0;
}

static int spawn_shell(const char *rootfsPath) {
    pid_t shell_pid = fork();
    if (shell_pid < 0) {
        perror("fork");
        return -1;
    }

    if (shell_pid == 0) {
        if (chdir("/tmp") != 0) {
            perror("chdir /tmp");
            _exit(1);
        }

        execl("/bin/bash", "/bin/bash", "-i", NULL);
        perror("execl");
        _exit(1);
    }

    int status = 0;
    pid_t pid;
    while ((pid = waitpid(-1, &status, 0)) > 0) {
        if (pid == shell_pid) {
            if (WIFEXITED(status)) {
                return WEXITSTATUS(status);
            } else if (WIFSIGNALED(status)) {
                return 128 + WTERMSIG(status);
            }
            return 1;
        }
    }

    if (pid < 0 && errno == ECHILD) {
        return 1;
    }

    return 1;
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

    if (setup_loopback() != 0) {
        fprintf(stderr, "Failed to setup loopback interface\n");
        return 1;
    }

    if (switch_root(rootfsPath) != 0) {
        fprintf(stderr, "pivot_root failed, falling back to chroot\n");
        char chrootPath[4096];
        snprintf(chrootPath, sizeof(chrootPath), "%s/rootfs", rootfsPath);
        if (chroot(chrootPath) != 0) {
            perror("chroot fallback");
            return 1;
        }
    }

    return spawn_shell(rootfsPath);
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

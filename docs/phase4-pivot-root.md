# Phase 4 — Filesystem Isolation

## Objective

Replace `chroot()` with `pivot_root()` to improve filesystem isolation.

## Advantages of `pivot_root()`

1. **Old root is unmounted** — after `pivot_root()`, the old root is moved to a temporary directory and can be fully unmounted with `MNT_DETACH`
2. **No path traversal** — absolute paths like `/../` cannot escape the new root because the old root is no longer reachable
3. **Cleaner semantics** — the process's root is truly switched, not just restricted
4. **Defense in depth** — even if an attacker finds a way to access absolute paths, they hit the new root, not the host root

## Disadvantages

1. **Requires mount point** — `pivot_root()` requires `new_root` to be a mount point (bind mount required)
2. **More complex** — requires creating `put_old` directory, bind mounting, and unmounting
3. **Current directory sensitivity** — the current working directory must be on the same filesystem as `new_root` (or behavior is unspecified)
4. **Not universally available** — some containers or restricted environments may block `pivot_root()`

## Compatibility Concerns

- `pivot_root()` is Linux-specific (not portable to BSD/macOS)
- Requires `CAP_SYS_ADMIN` in the mount namespace (available in our user namespace setup)
- Some security modules (e.g., AppArmor, SELinux) may restrict `pivot_root()`
- The `chroot` fallback ensures functionality even if `pivot_root()` fails

## Fallback Strategy

If `pivot_root()` fails at any step:
1. Log a warning
2. Fall back to `chroot()` with the original rootfs path
3. Continue with proc/devpts mounts and shell launch

This ensures backward compatibility while enabling the stronger isolation when possible.

## Implementation

```c
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
```

## Verification

- [x] `qo-init.c` compiles with `pivot_root` via `syscall()`
- [ ] `pwd` inside sandbox shows `/` (not host root)
- [ ] `ls /` shows only rootfs contents
- [ ] `/proc/self/mountinfo` shows the pivot_root
- [ ] Old root is not accessible after pivot
- [ ] Fallback to `chroot()` works if `pivot_root()` fails
- [ ] No regression in Phase 2 or 3 behavior

## Files Modified

- `cmd/qo-init.c` — added `switch_root()` with `pivot_root()` + `chroot()` fallback

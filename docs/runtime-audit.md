# Phase 1 — Runtime Audit

## Overview

This document audits the current sandbox runtime implementation before any modifications.
All findings are based on the current codebase state as of the `feature/runtime-hardening` branch.

---

## 1. Namespace Creation

### Flags used in `clone()`

```
CLONE_NEWUTS   — UTS namespace (hostname isolation)
CLONE_NEWPID   — PID namespace (PID 1 inside sandbox)
CLONE_NEWNS    — Mount namespace (filesystem isolation)
CLONE_NEWNET   — Network namespace (network interface isolation)
SIGCHLD        — Signal child exit to parent
```

### Location

- **Caller:** Go process via `exec.Command("qo-init", rootfsPath)`
- **Helper:** `qo-init.c:74` — `clone(child, stack + STACK_SIZE, flags, arg)`
- **Stack:** Allocated with `malloc(STACK_SIZE)` (1 MiB)

### Observations

- No `CLONE_NEWUSER` (user namespace) — requires root privileges
- No `CLONE_NEWIPC` (IPC namespace)
- No `CLONE_NEWCGROUP` (cgroup namespace)
- Parent does **not** enter any namespace; only the child does

---

## 2. Mount Sequence

### Order of operations in `child()` (`qo-init.c:16-60`)

1. `chroot(chrootPath)` — switches root to `<rootfsPath>/rootfs`
2. `mkdir("/proc", 0555)` — creates proc mount point
3. `mount("proc", "/proc", "proc", 0, "")` — mounts proc filesystem
4. `mkdir("/dev/pts", 0755)` — creates devpts mount point
5. `mount("devpts", "/dev/pts", "devpts", 0, "newinstance,ptmxmode=0666,mode=0620")` — mounts devpts
6. `unlink("/dev/ptmx")` — removes existing ptmx
7. `symlink("pts/ptmx", "/dev/ptmx")` — links ptmx
8. `chdir("/tmp")` — changes working directory
9. `execl("/bin/bash", "/bin/bash", "-i", NULL)` — launches interactive shell

### Observations

- No `mount(NULL, "/", NULL, MS_REC | MS_PRIVATE, NULL)` — mount propagation is **shared** by default
- No separate `/dev` mount (only `/dev/pts` via devpts)
- No `tmpfs` or other filesystem mounts
- `MS_NOSUID` / `MS_NOEXEC` / `MS_NODEV` are not set on any mounts
- `newinstance` flag on devpts provides a separate devpts instance per namespace

---

## 3. Chroot Lifecycle

### Setup

- Triggered by Go code calling `ExtractRootfs(sessionRootfs)` before `StartSandBox`
- Extracts an embedded `rootfs.tar.gz` to `/tmp/qo-sessions/<studentID>-<rand>/rootfs`
- No cleanup between runs other than `os.RemoveAll(rootfsPath)` if path already exists

### Enter

- `chroot(chrootPath)` executed inside the child after `clone()`
- `chrootPath` = `<rootfsPath>/rootfs`

### Exit

- No explicit `chroot` reversal — the process execs `/bin/bash`, which replaces the qo-init image
- Cleanup is handled by the Go parent after `cmd.Wait()`:
  - Unmounts `/dev/pts` and `/proc`
  - Removes the entire rootfs directory with `os.RemoveAll(rootfsPath)`
  - Removes the cgroup directory

### Observations

- `chroot` is used instead of `pivot_root` — the old root remains accessible until `chdir("/tmp")` obscures it
- If the shell exits abnormally, cleanup still runs in the Go parent
- No guarantee that all mounts are cleanly unmounted (uses `0` flags, not `MNT_DETACH`)

---

## 4. Cgroup Setup

### Location

- `setupCgroupV2(sessionID, pid)` in `pkg/sandbox/rootfs.go:105-140`

### Hierarchy

```
/sys/fs/cgroup/qo-sessions/
├── <sessionID>/
    ├── memory.max    = 536870912  (512 MiB)
    ├── pids.max      = 200
    ├── cpu.max       = 1000000 1000000  (full core)
    └── cgroup.procs  = <pid>
```

### Controller enabling

```go
for _, ctrl := range []string{"memory", "pids", "cpu"} {
    // writes "+ctrl" to cgroup.subtree_control
}
```

### Observations

- cgroup v2 only (no v1 fallback)
- Parent cgroup `qo-sessions` is created with `0755`
- No `io.max` or `blkio` weight configured
- No CPU shares / quota beyond `cpu.max`
- Process is moved into cgroup via `cgroup.procs` (not `cgroup.threads`)
- If cgroup setup fails, a warning is logged but sandbox continues

---

## 5. Cleanup

### Triggered by

- `cmd.Wait()` returning in Go (`pkg/sandbox/rootfs.go:343`)
- `defer releaseConcurrencyCap()` on concurrency lock

### Steps in `cleanupSession(rootfsPath, sessionID)` (`pkg/sandbox/rootfs.go:142-162`)

1. Unmount `/dev/pts` with `syscall.Unmount(..., 0)` — lazy unmount not requested
2. Unmount `/proc` with `syscall.Unmount(..., 0)`
3. Remove entire rootfs tree: `os.RemoveAll(rootfsPath)`
4. Remove cgroup: `os.RemoveAll(cgroupPath)`
5. Log success

### Observations

- Unmount flags are `0` — if mount is busy, unmount fails silently with a warning
- No `MNT_DETACH` fallback
- No retry logic for unmounting
- Cleanup runs even if `StartSandBox` was interrupted (deferred concurrency release, but `cleanupSession` is only called after `cmd.Wait()`)
- If Go process is killed (SIGKILL), cleanup does **not** run

---

## 6. Signal Forwarding

### Outbound (Host → Sandbox)

- `SIGWINCH` (terminal resize) is forwarded from host stdin to the master PTY
  - `signal.Notify(sigCh, syscall.SIGWINCH)`
  - Handler calls `pty.InheritSize(os.Stdin, master)`

### Inbound (Sandbox → Host)

- No explicit signal forwarding from sandbox to host
- The sandbox child is in a new PID namespace; signals from the host process group are:
  - `SIGTERM` sent to `-cmd.Process.Pid` (negative PID = process group) when duration expires
  - `SIGKILL` sent after 5-second grace period

### Observations

- Only `SIGWINCH` is forwarded automatically
- `SIGINT` (Ctrl+C) from the user goes to the Go process, but is **not** forwarded to the sandbox
- The sandbox process runs in a new session (`Setsid: true`) and process group (`Setpgid: true`)
- If the Go process receives `SIGTERM` or `SIGINT`, the sandbox may become orphaned

---

## 7. PTY Handling

### Allocation

- `pty.Open()` in Go (`pkg/sandbox/rootfs.go:271`) — allocates a master/slave PTY pair
- Slave PTY is passed as stdin/stdout/stderr to `qo-init`
- Master PTY is used by Go to read/write terminal data

### Terminal Mode

- Raw mode is configured on `os.Stdin`:
  - Disables echo, canonical mode, signal generation, extended input
  - Sets `CS8`, `VMIN=1`, `VTIME=0`
- Old terminal state is restored via `defer` on Go exit

### Size Inheritance

- `pty.InheritSize(os.Stdin, master)` is called initially and on every `SIGWINCH`

### I/O Copying

- `io.Copy(master, os.Stdin)` in a goroutine — host input → master PTY
- `io.Copy(os.Stdout, master)` in main goroutine — master PTY → host output

### Observations

- PTY slave is closed in Go after `cmd.Start()`; master remains open
- No `SetSize` on the slave PTY (only `InheritSize` from host to master)
- Terminal raw mode is applied to the **host** stdin, not the slave PTY
- `io.Copy` is unidirectional; no explicit handling of `io.EOF` beyond returning

---

## 8. Additional Observations

### Privilege Model

- Go binary requires root (`os.Geteuid() != 0` check in `cmd/start.go:54`)
- No user namespace support
- Child process retains full root capabilities

### Filesystem Preparation

- `ExtractRootfs()` embeds a gzipped tar archive
- Missing BusyBox applets are symlinked (`sleep`, `kill`, `pkill`, `killall`, `stat`, `passwd`, `chpasswd`, `adduser`, `addgroup`, `deluser`, `delgroup`)
- Character devices are created from tar headers (e.g., `/dev/null`)

### Concurrency Control

- File-based lock in `/tmp/qo-sessions.lock`
- Uses `flock` with `LOCK_NB` (non-blocking)
- Max concurrent sessions: 8
- Lock is released even on error (`defer releaseConcurrencyCap()`)

### Known Limitations

1. Mount propagation leaks between namespaces
2. No capability dropping
3. No seccomp filtering
4. No RLIMIT configuration
5. No PID 1 reaping (bash is PID 1)
6. Orphaned processes are not reaped if Go parent dies
7. Network namespace has no loopback
8. `chroot` is weaker than `pivot_root`

---

## 9. Architecture Diagram

```
┌─────────────────────────────────────────────────────────────┐
│  Host (root)                                                 │
│                                                              │
│  ┌────────────┐    exec     ┌───────────────────────────┐  │
│  │  qo (Go)   │ ──────────► │        qo-init (C)        │  │
│  │             │             │  clone(CLONE_NEWUTS|...)|  │  │
│  │ - PTY alloc │             │  ┌─────────────────────┐  │  │
│  │ - Cgroup    │             │  │ child()             │  │  │
│  │ - Signal    │             │  │ chroot()            │  │
│  │   handling  │             │  │ mount proc          │  │
│  │ - I/O copy  │             │  │ mount devpts        │  │
│  └────────────┘             │  │ execl /bin/bash     │  │  │
│       ▲                     │  └─────────────────────┘  │  │
│       │ waitpid             └───────────────────────────┘  │
│       │                                                      │
│  ┌──────────────────────────────────────────────────────┐  │
│  │  Sandbox Namespaces                                  │  │
│  │  ┌──────────────────────────────────────────────┐    │  │
│  │  │ UTS | PID | Mount | Net                      │    │  │
│  │  │  /bin/bash (PID 1)                            │    │  │
│  │  │  /proc                                        │    │  │
│  │  │  /dev/pts                                     │    │  │
│  │  │  <rootfs>                                     │    │  │
│  │  └──────────────────────────────────────────────┘    │  │
│  └──────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
```

---

## 10. Regression Test Plan

Before modifying any code, verify the following still works:

- [ ] `qo start` launches an interactive shell
- [ ] `mount` shows proc and devpts inside sandbox
- [ ] `ping localhost` fails (expected — no loopback)
- [ ] `hostname` shows a different hostname (UTS namespace)
- [ ] `ps` shows PID 1 as bash
- [ ] Session is terminated after duration expires
- [ ] Temporary directories are removed after session ends
- [ ] Cgroup limits are applied (check `/sys/fs/cgroup/qo-sessions/`)
- [ ] PTY resizing works
- [ ] Concurrent sessions are limited to 8

---

*Audit completed. No behavioral changes made. Proceeding to Phase 2.*

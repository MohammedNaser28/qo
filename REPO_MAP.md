# REPO_MAP.md — `qo` Repository (Diagnostic, July 2026)

## 1. Directory Structure

```
.
├── main.go                  # Entry point. Routes `init` arg to sandbox.StartSandBox(), else cmd.Execute()
├── go.mod                   # Module github.com/ahmedYasserM/qo, Go 1.24.5
├── go.sum                   # Dependency checksums
├── qo                       # Pre-built binary (17 MB, ELF x86-64, debug_info, built Jul 21)
├── README.md                # Usage docs; explicitly notes duration and output flags are not implemented
├── PLAN.md                  # Event-readiness plan (dated 2025-07-19) — gap analysis + sequencing
├── REPO_MAP.md              # Previous repo map (outdated — describes pre-rebuild state)
├── cmd/
│   ├── root.go              # Cobra root command + coloredcobra formatting
│   ├── build.go             # `qo build` — validates folder structure, creates encrypted tar archive
│   └── start.go             # `qo start` — root check, extract rootfs, decrypt archive, launch sandbox
├── pkg/
│   ├── sandbox/
│   │   ├── rootfs.go        # Core sandbox: ExtractRootfs, StartSandBox, cgroup, concurrency, cleanup
│   │   └── rootfs.tar.gz    # Embedded BusyBox-based rootfs (embedded via //go:embed)
│   ├── archive/
│   │   ├── encrypt.go       # AES-CTR stream encryption, AES-GCM for .ut unlock-time file
│   │   ├── decrypt.go       # AES-CTR decryption, unlock-time check, extract to rootfs/tmp
│   │   └── utils.go         # PBKDF2 key derivation, folder structure validation
│   └── logger/
│       └── log.go           # Colored stdout/stderr logging (Info, Warn, Error, Success)
├── scripts/
│   ├── install.sh           # Remote installation (git clone + go build + sudo mv)
│   ├── inject.sh            # Copy binary + ldd dependencies into rootfs
│   └── gen-example.sh       # Generate test challenge folder (3 levels)
├── test/
│   ├── level1/
│   │   ├── question.txt     # "Create a directory called testdir"
│   │   └── check.sh         # Checks if testdir/ exists
│   ├── level2/
│   │   ├── question.txt     # "Create a user named studentuser"
│   │   └── check.sh         # Checks if user exists via `id`
│   └── level3/
│       ├── question.txt     # "Copy secret.txt to home, chmod 600"
│       ├── check.sh         # Checks file exists and mode is 600
│       └── secret.txt       # Supporting file for level 3
```

## 2. Namespace / Isolation Checklist

| Requirement | Currently implemented? | Evidence |
|---|---|---|
| **CLONE_NEWUTS** | **YES** | `rootfs.go:316`: `Cloneflags: syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS | syscall.CLONE_NEWNET` |
| **CLONE_NEWNS** | **YES** | Same line: `syscall.CLONE_NEWNS` is set. |
| **CLONE_NEWNET** | **YES** | Same line: `syscall.CLONE_NEWNET` is set. |
| **CLONE_NEWPID** | **YES** | Same line: `syscall.CLONE_NEWPID` is set. |
| **CLONE_NEWUSER** | **NO** | Not present in `Cloneflags`. Sandbox runs as real root in new namespaces. |
| **User namespace UID/GID mapping** | **NO** | `setupUserNamespaceMapping()` does not exist. No UID/GID map writes. |
| **/proc mounted inside the new mount+PID namespace** | **YES** | `rootfs.go:269`: `syscall.Mount("proc", "/proc", "proc", 0, "")` — called inside the child after `Chroot()`. |
| **PID-1 reaper pattern** | **NO — bash is exec'd directly as PID 1** | `rootfs.go:293-298`: `cmd := exec.Command("/bin/bash", "-i")` then `cmd.Run()`. Bash is PID 1 inside the namespace. No reaper loop. |
| **PTY controlling terminal (setsid + TIOCSCTTY)** | **YES** | `rootfs.go:287`: `unix.IoctlSetInt(int(os.Stdin.Fd()), unix.TIOCSCTTY, 0)` after chroot. `Setsid: true` in `SysProcAttr` at line 318. |
| **Cgroup v2: controllers enabled on parent cgroup.subtree_control** | **YES** | `rootfs.go:112-115`: Writes `+memory`, `+pids`, `+cpu` to `/sys/fs/cgroup/qo-sessions/cgroup.subtree_control` before creating session leaf. |
| **Cgroup v2: session's own memory.max/pids.max/cpu.max set** | **YES** | `rootfs.go:121-131`: Writes `512M` to `memory.max`, `200` to `pids.max`, `1000000 1000000` to `cpu.max` on session cgroup. |
| **Cgroup v2: correct PID written to cgroup.procs** | **YES** | `rootfs.go:133`: Writes child PID to `cgroup.procs`. |
| **Device nodes (/dev/null etc.) via mknod** | **YES** | `ExtractRootfs()` at `rootfs.go:227-234` handles `tar.TypeChar` and uses `syscall.Mknod` to create character devices from the embedded rootfs tarball. |
| **Missing BusyBox applets symlinked at runtime** | **YES** | `ExtractRootfs()` creates symlinks for missing applets (`sleep`, `kill`, `pkill`, `killall`, `stat`, `passwd`, `chpasswd`, `adduser`, `addgroup`, `deluser`, `delgroup`) to `busybox` at `rootfs.go:240-247`. |
| **Per-session rootfs path** | **YES** | `GenerateSessionPath()` at `rootfs.go:30` creates `/tmp/qo-sessions/<studentID>-<random4hex>`. Passed through `ExtractRootfs()`, `DecryptTarArchive()`, `StartSandBox()`. |
| **Cleanup on exit: unmount /proc, remove session dir, remove cgroup** | **YES (with caveats)** | `cleanupSession()` at `rootfs.go:140-160` unmounts `/proc` and `dev/pts`, removes rootfs dir, removes cgroup. Called in parent after `cmd.Wait()` at line 374. `defer releaseConcurrencyCap()` at line 303 ensures lock release. If parent is SIGKILLed, cleanup is skipped. |
| **Concurrency cap** | **YES** | `checkConcurrencyCap()` at `rootfs.go:40-75` uses file-based lock at `/tmp/qo-sessions.lock` with `flock(LOCK_EX|LOCK_NB)` and counter. `releaseConcurrencyCap()` is now called in parent via `defer` at line 303. |
| **Duration flag enforcement** | **YES** | `rootfs.go:333-341`: If `duration > 0`, goroutine sleeps for `duration`, then sends `SIGTERM` to child process group, waits 5s, then sends `SIGKILL`. |

## 3. Rootfs Contents

### BusyBox applets with actual symlinks vs. applets without one

The rootfs contains a BusyBox binary at `rootfs/bin/busybox` (1,292,216 bytes). The following are **symlinks to busybox** (verified from tar listing):

```
awk, cat, chgrp, chmod, chown, clear, cp, cut, echo, egrep, fgrep, find, grep, id, ls, mkdir, mount, mv, pwd, pgrep, ps, rev, rm, sed, sh, sort, su, touch, uname, uniq, whoami
```

Additionally, these applets are now symlinked to `busybox` at runtime by `ExtractRootfs()` if missing: `sleep`, `kill`, `pkill`, `killall`, `stat`, `passwd`, `chpasswd`, `adduser`, `addgroup`, `deluser`, `delgroup`.

The following are **standalone ELF binaries** (not symlinks to busybox):

| Binary | Size | Notes |
|--------|------|-------|
| `bash` | 1,162,328 | ELF x86-64, dynamically linked, stripped |
| `busybox` | 1,292,216 | ELF x86-64, dynamically linked, stripped |
| `vim` | 4,860,776 | ELF x86-64, dynamically linked, stripped |
| `nano` | 283,144 | ELF x86-64, dynamically linked, stripped |
| `less` | 237,432 | ELF x86-64, dynamically linked, stripped |
| `man` | 1,850,744 | ELF x86-64, dynamically linked, stripped |
| `mandoc` | 1,850,744 | ELF x86-64, dynamically linked, stripped (same size as man — likely hardlinked or duplicate) |
| `useradd` | 109,976 | ELF x86-64, dynamically linked, stripped |
| `usermod` | 101,488 | ELF x86-64, dynamically linked, stripped |

**BusyBox applets available** (from `busybox --list`): 1,597 applets. The symlinked ones (31) are a small subset. The remaining ~1,566 applets are available by invoking `busybox <applet>` but have no symlink. This includes: `curl`, `wget`, `ping`, `ssh`, `telnet`, `nc`, `python` (no — python is not in the list), `gcc` (no), `make` (no), `git` (no).

**Notable applets available via `busybox <name>` but without symlinks:** `curl`, `wget`, `ping`, `ping6`, `ssh`, `telnet`, `nc` (via `busybox nc`), `tftp`, `ftpd`, `ftpget`, `ftpput`, `httpd`, `wget`, `ifconfig`, `ip`, `route`, `arp`, `nslookup`, `traceroute`, `telnetd`, `tcpsvd`, `udpsvd`, `ssl_client`.

**Standalone binaries present:** `bash`, `busybox`, `vim`, `nano`, `less`, `man`, `mandoc`, `useradd`, `usermod`.

**Interactive shell:** `/bin/bash` (not ash). Confirmed at `rootfs.go:300`: `cmd := exec.Command("/bin/bash")`.

**Libraries present:** `libc.so.6`, `libm.so.6`, `libncursesw.so.6`, `libreadline.so.8`, `libz.so.1`, `libbz2.so.1.0`, `liblzma.so.5`, `libzstd.so.1`, `libpcre2-8.so.0`, `libmagic.so.1`, `libgpm.so.2`, `libacl.so.1`, `libcap-ng.so.0`, `libaudit.so.1`, `ld-linux-x86-64.so.2`.

**Notable absences from rootfs:** No `/dev/` directory at all. No `/tmp/` directory (though `DecryptTarArchive` extracts to `rootfs/tmp`). No `/sys/` directory. No `/run/` directory. No `python`, `gcc`, `make`, `git`, `perl`, `node`, `java`, `ruby`, `go`.

## 4. Archive / Metadata Handling

**meta.yaml:** Parsed via `pkg/archive/metadata.go` (`DecryptMetadata()`). The `qo meta` CLI command (`cmd/meta.go`) reads `meta.yaml` from inside the encrypted archive without full decryption and outputs JSON.

**Archive format** (from `encrypt.go` and `decrypt.go`):
- File format: `[16-byte salt][16-byte nonce][AES-CTR encrypted tar stream]`
- The tar stream contains a `.ut` file (unlock time, AES-GCM encrypted with a separate key derived from the "starter key") followed by the challenge files.
- `qo build` creates the archive. `qo start` decrypts it.
- The `.ut` file is read in a first pass to check unlock time, then the archive is re-read from the beginning (seeking past salt+nonce) to extract all files except `.ut`.
- `meta.yaml` is included in the archive if present in the challenge folder. `qo meta` reads it. The server (`handlers.go`) invokes `qo meta` at startup to load title/difficulty.

**`qo build` behavior:** Validates folder structure (subdirectories only, each with executable `check.sh`), then creates an encrypted tar archive. `meta.yaml` is optional — included if present.

**`qo start` behavior:** Extracts rootfs, decrypts archive into `rootfs/tmp`, launches sandbox.

**`qo meta` command:** EXISTS. Reads `meta.yaml` from encrypted archive and outputs JSON. Used by server for challenge metadata.

**Commands available:** `root`, `build`, `start`, `meta`.

## 5. Custom In-Sandbox Commands

| Command | Status |
|---------|--------|
| **`logo`** | **NOT IMPLEMENTED.** No reference anywhere in the codebase. |
| **`allowed`** | **NOT IMPLEMENTED.** No reference anywhere in the codebase. |
| **`check`** | **NOT IMPLEMENTED as a sandbox command.** The word "check" appears only in `build.go` comments and `utils.go`'s `IsValidFolderStructure()` which validates that `check.sh` files exist. There is no `check` command available inside the sandbox. |
| **`submit`** | **NOT IMPLEMENTED.** No reference anywhere in the codebase. |

## 6. Known Deviations from Prior Design / PLAN.md

1. **`dropToUser()` was removed entirely.** The PLAN.md (§2.6) discusses `dropToUser(defaultUser)` at `rootfs.go:147` and the hardcoded `ahmed` user. The current code has **no `dropToUser()` function at all**. The child process runs as root inside the namespace. The `ahmed` user exists in `/etc/passwd` but is never switched to. The child runs `/bin/bash` as root.

2. **No user namespace (`CLONE_NEWUSER`).** The sandbox runs with `CLONE_NEWUTS | CLONE_NEWPID | CLONE_NEWNS | CLONE_NEWNET`. There is no `CLONE_NEWUSER` and no UID/GID mapping. The sandboxed process runs as real root (UID 0) in the new namespaces.

3. **Cgroup `subtree_control` is written to the parent, not the leaf.** FIXED. The code now writes `+memory`, `+pids`, `+cpu` to `/sys/fs/cgroup/qo-sessions/cgroup.subtree_control` (the parent) before creating the session leaf cgroup.

4. **`/dev` directory IS present in rootfs.** The embedded rootfs tarball contains `/dev/null`, `/dev/zero`, `/dev/random`, `/dev/urandom`, `/dev/tty`, `/dev/ptmx` as character devices. `ExtractRootfs()` uses `syscall.Mknod` to recreate them.

5. **Missing BusyBox applets are now symlinked at runtime.** `ExtractRootfs()` creates symlinks for `sleep`, `kill`, `pkill`, `killall`, `stat`, `passwd`, `chpasswd`, `adduser`, `addgroup`, `deluser`, `delgroup` if they are missing from the rootfs.

6. **`id` flag parsing was in `init()` and always used default `"0"`.** FIXED. Moved to `RunE` in `cmd/start.go` so the actual `-i` flag value is used.

7. **`outputLogDir` flag is declared but never used.** Still true — declared in `cmd/start.go:46`, bound at line 85, but never referenced in `RunE`.

8. **`sessionRootfs` was generated in `init()` with `id=0`.** FIXED. Now generated in `RunE` after parsing the actual `-i` flag value.

9. **The pre-built `qo` binary at repo root is 17 MB.** It may not reflect the current source code. Use `go build` from `qo/` to get the latest binary.

10. **No test evidence in the repo.** The `test/` directory contains challenge examples (generated by `gen-example.sh`), not test scripts for the Go code. No `*_test.go` files exist in the `qo/` module.

## 7. Test Evidence

**No Go test files exist** (`*_test.go`). The `test/` directory contains challenge examples (level1/level2/level3 with `question.txt` and `check.sh`), not test scripts for the Go code. There are no CI configuration files, no test logs, no run artifacts. The only evidence of runtime behavior is the pre-built `qo` binary (dated Jul 21), which was presumably built and tested manually.

The `test/` folder is a sample challenge folder generated by `scripts/gen-example.sh` — it is not a test suite.

# qo — Event-Ready Plan

**Date:** 2025-07-19
**Event:** ~1 week, 20–100 kids, single laptop, LAN-only
**Goal:** N concurrent isolated sandbox sessions, no host-wide crash from one session, no inter-session collision, clean teardown.

---

## 1. Target State Definition

"Event-ready" means the following concrete behaviors:

| # | Behavior | Concrete criterion |
|---|----------|-------------------|
| 1 | **Per-session isolation** | Each `qo start` creates its own `/tmp/rootfs-<session-id>` directory. Two concurrent sessions never share filesystem state. |
| 2 | **No host-wide crash** | One session running a fork bomb, memory hog, or infinite loop does not crash the host OS, does not OOM-kill other sessions, and does not prevent other students from starting. |
| 3 | **Inter-session non-collision** | Two concurrent sessions can run identical challenges without interfering — different PIDs, different mounts, different `/tmp`, different hostname. |
| 4 | **Cleanup on exit** | On normal exit, crash, SIGKILL, or SIGTERM: the session's `/tmp/rootfs-<id>` is removed, all mounts are unmounted, and no stale processes remain. |
| 5 | **Session cap** | The tool rejects new sessions once N concurrent sessions are running (configurable, default 8 for a laptop). Returns a clear error instead of silently degrading. |
| 6 | **Duration enforcement** | The `-d` flag actually kills the session after N minutes. Returns exit code 137 (killed) so the grading script can distinguish timeout from normal completion. |
| 7 | **Network isolation** | Sessions have no network access (no `CLONE_NEWNET`). Students cannot `curl`, `wget`, `ssh`, or reach any host interface. |

---

## 2. Gap Analysis

### 2.1 Per-session isolation of rootfs path

**Current state:** Per-session paths are implemented. `GenerateSessionPath()` in `pkg/sandbox/rootfs.go:30` creates `/tmp/qo-sessions/<studentID>-<random4hex>`. The path is passed through `ExtractRootfs()`, `DecryptTarArchive()`, and `StartSandBox()`.

**Verdict: FIXED.**

---

### 2.2 Resource limits (cgroups: memory, CPU, pids)

**Current state:** cgroup v2 setup is implemented in `setupCgroupV2()` at `pkg/sandbox/rootfs.go:105`. Writes `memory.max=512M`, `pids.max=200`, `cpu.max=1000000 1000000`. `subtree_control` is now written to the parent cgroup (`/sys/fs/cgroup/qo-sessions`) before creating the session leaf.

**Verdict: FIXED.**

---

### 2.3 Concurrency control (session cap, queue, rejection)

**Current state:** File-based lock at `/tmp/qo-sessions.lock` with `flock` + counter. `checkConcurrencyCap()` in parent before `cmd.Start()`, `releaseConcurrencyCap()` in parent after `cmd.Wait()` via `defer`. Cap is 8.

**Verdict: FIXED.**

---

### 2.4 Network isolation (CLONE_NEWNET)

**Current state:** `CLONE_NEWNET` is set in `Cloneflags` at `pkg/sandbox/rootfs.go:316`.

**Verdict: FIXED.**

---

### 2.5 Cleanup on crash/failure

**Current state:** `cleanupSession()` unmounts `/proc` and `dev/pts`, removes rootfs dir, removes cgroup. Called in parent after `cmd.Wait()`. Duration timer sends SIGTERM then SIGKILL to child process group. `defer releaseConcurrencyCap()` ensures lock is released on any exit path.

**Verdict: MOSTLY FIXED.** Cleanup runs on normal exit and child crash. Remaining risk: if parent itself is SIGKILLed, cleanup is skipped. No signal handler for parent to catch SIGTERM/SIGINT and forward to child.

---

### 2.6 Hardcoded username `ahmed`

**Current state:** `dropToUser()` was removed. The sandboxed bash runs as root (UID 0) inside the namespace. The `/etc/passwd` in rootfs still contains `ahmed` but it is unused.

**Verdict: NOT APPLICABLE.** Per-session rootfs isolation makes shared usernames harmless.

---

### 2.7 Duration flag (`-d`) enforcement

**Current state:** `testDuration` is parsed from the `-d` flag in `RunE` and passed to `StartSandBox()`. A goroutine sleeps for the duration, then sends SIGTERM to the child process group, waits 5s, then sends SIGKILL.

**Verdict: FIXED.**

---

## 3. Sequencing Plan

Ordered by: blocks concurrent sessions > blocks surviving crash > nice to have.

### Phase 1: Blocks running two students at once

| # | Item | Effort | Dependencies |
|---|------|--------|-------------|
| 1.1 | **Per-session rootfs paths** — parameterize `Rootfs` with session ID, pass through `ExtractRootfs()`, `DecryptTarArchive()`, `StartSandBox()` | 2–3h | None (but must land first) |
| 1.2 | **Concurrency lock** — file-based lock + atomic session counter + cap (default 8) | 1–2h | #1.1 (lock must use per-session paths) |
| 1.3 | **Network isolation** — add `CLONE_NEWNET` to Cloneflags | 30min | None |

**Why this order:** Without #1.1, two sessions collide on `/tmp/rootfs` — nothing else matters. #1.2 prevents resource exhaustion from too many sessions. #1.3 is a single flag bit but should be done before any student touches the network.

### Phase 2: Blocks surviving a hostile/accidental crash

| # | Item | Effort | Dependencies |
|---|------|--------|-------------|
| 2.1 | **Resource limits** — cgroup v2 setup: memory limit (e.g., 512MB), PID limit (e.g., 200), CPU weight | 4–6h | #1.1 (needs per-session cgroup path) |
| 2.2 | **Cleanup on failure** — `defer` unmount + remove, signal forwarding, process-group kill on timeout/crash | 2–3h | #1.1 |
| 2.3 | **Duration enforcement** — goroutine timer → SIGTERM to child process group | 1–2h | #2.2 (needs process-group cleanup) |

**Why this order:** #2.1 is the biggest item — cgroup v2 setup in Go is non-trivial (write to `/sys/fs/cgroup/` files). Without it, a fork bomb kills the laptop. #2.2 ensures that when things go wrong (and they will), cleanup happens. #2.3 depends on #2.2 because duration timeout is just a specific kind of forced cleanup.

### Phase 3: Nice to have

| # | Item | Effort | Dependencies |
|---|------|--------|-------------|
| 3.1 | **Username parameterization** — derive username from student ID or accept `--user` flag | 1–2h | None (deferred from §2.6) |
| 3.2 | **Output log dir** — actually use `outputLogDir` flag, write session logs | 1–2h | None |
| 3.3 | **Student ID propagation** — pass ID into the sandbox (env var or file) for grading | 1h | None |
| 3.4 | **Path traversal protection** — sanitize tar headers in `ExtractRootfs()` and `DecryptTarArchive()` | 1–2h | None |

---

## 4. Decision: Patch In Place vs. Wrap in Docker

### Option A: Patch in place

Modify the existing Go code to add:
- Per-session paths (cgroup v2 hierarchy under `/sys/fs/cgroup/qo-sessions/`)
- `CLONE_NEWNET` flag
- Resource limits via cgroup v2 file writes
- Cleanup/timeout logic

**Pros:**
- Zero new runtime dependencies — the binary stays self-contained
- No Docker required on the event laptop
- Full control over isolation semantics
- Smaller attack surface (no Docker daemon, no container runtime)
- Aligns with the existing chroot + namespaces philosophy

**Cons:**
- Cgroup v2 manipulation in Go is error-prone (permissions, hierarchy mounting, race conditions)
- Must handle edge cases: cgroup not mounted, permissions denied, hierarchy differences across distros
- More code to test and debug under time pressure
- No Docker-level safety net if something goes wrong

### Option B: Wrap in Docker

Keep `qo start` as-is. Wrap it in a per-session Docker container with:
```
docker run --rm --memory=512m --pids-limit=200 --cpus=0.5 --network=none --uts=host \
  -v /path/to/qo-bundle:/qo --cap-drop=ALL --security-opt=no-new-privileges \
  qo-image /qo/qo start -i <id> -a <archive> -p <pass> -k <key> -d <duration>
```

**Pros:**
- Docker handles cgroups, network isolation, PID limits, cleanup — battle-tested
- Less Go code to write/change
- Docker's `--rm` guarantees cleanup
- Easy to verify limits are applied (`docker stats`)

**Cons:**
- **Docker must be installed on the event laptop** — adds a dependency we don't currently have
- Docker on a laptop used for a competition adds complexity (daemon, permissions, storage driver)
- The `qo start` binary must be root (current code checks `os.Geteuid() != 0`) — Docker adds another layer of privilege management
- Larger attack surface (Docker daemon, container runtime)
- If Docker isn't already on the event laptop, installing + configuring it in <1 week is risky

### Recommendation: Option A (Patch in place)

**Justification:**

1. **Timeline is the constraint.** Docker installation, image building, and permission tuning on the event laptop is a moving target. The Go code already exists and works for a single session. Adding cgroup v2 manipulation is more code but more predictable — we control the entire stack.

2. **The event laptop is a known environment.** We know what distro, what kernel, what's installed. Cgroup v2 is standard on any modern Linux (kernel 5.0+). We can test against the exact event environment.

3. **Zero new dependencies.** The binary is self-contained. No Docker daemon, no image management, no storage driver issues. Just a single binary and a rootfs tarball.

4. **Simpler for the event setup.** The instructor runs `qo build ...` and `qo start ...` — the same commands, just with better isolation. No Docker compose, no image loading, no `dockerd` startup scripts.

5. **The cgroup work is bounded.** Cgroup v2 is just writing to files in `/sys/fs/cgroup/`. It's not complex cryptography or networking — it's write a number to a file. The risk is manageable.

**Caveat:** If cgroup v2 setup proves too fragile during testing (e.g., the event laptop runs an older kernel or has cgroup v1), we can fall back to `ulimit`/`setrlimit` as a minimum safety net. This is less comprehensive but still prevents the worst cases.

---

## 5. Test Plan Before the Event

### 5.1 Environment

- **Exact event laptop** (or identical spec)
- Run as root (required by current code)
- 8 concurrent `qo start` processes (matching the session cap)

### 5.2 Baseline test

1. Run `qo start` for 2 different student IDs simultaneously
2. Verify each creates its own `/tmp/rootfs-<id>` directory
3. Verify each gets a different hostname (`CLONE_NEWUTS`)
4. Verify each gets a different PID namespace (PID 1 inside each is bash, not init)
5. Verify `/proc` is mounted independently in each
6. Verify `ls /proc/<pid>` in one session doesn't show PIDs from the other

### 5.3 Network isolation test

1. Inside a sandbox, run `curl -s ifconfig.me` or `wget google.com`
2. Verify it fails (no network interface in the namespace)
3. Verify `ip link` shows only `lo` (loopback)

### 5.4 Resource limit tests (fork bomb)

```bash
# Inside sandbox — should be killed by PID limit
while true; do :; done
```

**Expected:** Process count stays below the cgroup PID limit (~200). The fork bomb stalls or exits with "Resource temporarily unavailable". Other sessions continue running.

### 5.5 Resource limit tests (memory hog)

```bash
# Inside sandbox — should be killed by memory limit
dd if=/dev/zero of=/dev/null &
# or
python3 -c "import time; [__import__('os').getpid() for _ in iter(int, 1)]"  # leak PIDs + memory
# or simpler:
while true; do dd if=/dev/zero of=/dev/shm/hog bs=1M; done
```

**Expected:** OOM-kill or cgroup memory limit triggers. Process is killed. Other sessions continue.

### 5.6 Duration enforcement test

```bash
qo start -i 1 -a challenge.enc -p pass -k key -d 2m
```

**Expected:** Session is terminated after 2 minutes. Exit code is 137 (killed by SIGTERM/SIGKILL). Parent process cleans up.

### 5.7 Crash/cleanup test

1. Start a session
2. `kill -9 <child-pid>` from the host
3. Verify `/tmp/rootfs-<id>` is removed
4. Verify no stale mounts remain (`mount | grep rootfs`)
5. Verify no orphan processes remain (`ps aux | grep bash` filtered to chroot users)
6. Verify a new session can start immediately after

### 5.8 Session cap test

1. Start 8 sessions (the cap)
2. Attempt to start a 9th
3. Verify it returns a clear error: "Maximum concurrent sessions reached (8)"
4. Kill one session
5. Verify the 9th can now start

### 5.9 Stress test

1. Run 8 concurrent sessions for 10 minutes each
2. In each session, run a different workload:
   - Session 1: `while :; do :; done` (fork bomb)
   - Session 2: `dd if=/dev/zero of=/dev/null` (CPU)
   - Session 3: `while true; do dd if=/dev/zero of=/dev/shm/x bs=1M; done` (memory)
   - Session 4: Normal challenge (control — should complete fine)
   - Session 5-8: Normal challenges (control)
3. Verify sessions 4-8 complete normally despite sessions 1-3 being hostile
4. Verify host laptop remains responsive throughout

### 5.10 Go/no-go checklist

| Test | Pass criterion |
|------|---------------|
| Baseline (2 concurrent) | Both sessions isolated, no collision |
| Network isolation | `curl`/`wget` fail inside sandbox |
| Fork bomb | Killed by PID limit, other sessions survive |
| Memory hog | Killed by memory limit, other sessions survive |
| Duration enforcement | Session terminates after N minutes |
| Crash cleanup | `/tmp/rootfs-*` removed, no stale mounts |
| Session cap | 9th session rejected with clear error |
| Stress test (8 hostile) | Control sessions complete normally |

**All 8 tests must pass before the event.**

---

## Appendix: Files to Modify

| File | Changes |
|------|---------|
| `pkg/sandbox/rootfs.go` | Parameterize `Rootfs` path, add `CLONE_NEWNET`, add cgroup setup/teardown with parent `subtree_control`, add `Setsid` + `TIOCSCTTY` for PTY job control, add missing BusyBox symlinks, move concurrency lock release to parent, add cleanup/defer, add signal handling |
| `cmd/start.go` | Move `id` parsing from `init()` to `RunE`, pass session ID to sandbox functions, wire up `-d` duration flag |
| `cmd/meta.go` | New: `qo meta` command to read metadata from encrypted archive |
| `pkg/archive/metadata.go` | New: `DecryptMetadata()` to extract `meta.yaml` from encrypted archive without full decryption |
| `pkg/archive/decrypt.go` | Accept per-session rootfs path parameter, handle `tar.TypeChar`/`TypeBlock`/`TypeFifo` |
| `go.mod` | Added `gopkg.in/yaml.v3` for YAML metadata parsing |

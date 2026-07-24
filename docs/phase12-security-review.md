# Phase 12 — Security Review

## Current Attack Surface

### Namespace Boundaries
- **UTS**: Hostname isolation — low risk
- **PID**: PID 1 is qo-init — medium risk (PID 1 has special properties)
- **Mount**: `pivot_root` + `MS_PRIVATE` — medium risk (mount namespace escape possible via setuid helpers)
- **Network**: Loopback only, no external access — low risk
- **IPC**: System V IPC isolated — low risk
- **Cgroup**: Cgroup hierarchy hidden — low risk
- **User**: UID 0 mapped to host UID — medium risk (if host is root, namespace root is also host root)

### Capability Surface
- After Phase 7: no capabilities retained
- Before capability drop: `CAP_SYS_ADMIN` in user namespace (sufficient for namespace creation, mount, chroot)
- Inside user namespace, root is not host root unless explicitly mapped

### Syscall Surface
- After Phase 9 (enforce mode): 11 dangerous syscalls blocked
- Default mode (`log`): all syscalls allowed but logged
- Seccomp filter is not yet enforced by default

### Filesystem Surface
- Rootfs is extracted from embedded tar
- BusyBox symlinks provide minimal utilities
- No compiler, no package manager, no network tools by default

## Remaining Escape Possibilities

1. **Setuid binaries in rootfs**
   - If the challenge archive contains setuid root binaries, they can regain capabilities
   - Mitigation: `RLIMIT_CORE=0` and capability drop reduce but don't eliminate this

2. **Kernel exploits**
   - Any kernel vulnerability can bypass namespace isolation
   - Mitigation: seccomp filter, resource limits, minimal rootfs

3. **/proc exposure**
   - `/proc` is mounted; sensitive files like `/proc/self/mem`, `/proc/kcore` exist
   - Mitigation: proc is mounted read-only by default? (not currently — needs `MS_RDONLY`)

4. **Cgroup escape**
   - If cgroup v2 is not enforced strictly, processes can manipulate cgroup settings
   - Mitigation: cgroup namespace hides hierarchy

5. **Shared memory / IPC**
   - Even with IPC namespace, `/dev/shm` is shared if not properly isolated
   - Mitigation: `CLONE_NEWIPC` + no `mount --bind` of host `/dev/shm`

6. **Orphaned processes**
   - If qo-init dies unexpectedly, children are reparented to host init
   - Mitigation: Go parent sends SIGKILL on timeout

## Known Limitations

1. **No MS_RDONLY on mounts**
   - `/proc` and `/dev/pts` are mounted read-write
   - Could be hardened with `MS_RDONLY` on the rootfs

2. **No tmpfs /dev/shm**
   - `/dev/shm` is not explicitly mounted
   - If present in rootfs, it may be a tmpfs without size limits

3. **No network egress filtering**
   - Loopback is up but no external interfaces
   - If network namespace is misconfigured, could leak

4. **Seccomp default is off**
   - `QO_SECCOMP=off` by default for compatibility
   - Should default to `log` in production

5. **Parent process must be root**
   - `cmd/start.go` checks `os.Geteuid() != 0`
   - User namespace mapping requires parent coordination for true rootless

6. **Go parent is a single point of failure**
   - If Go parent crashes, cleanup may not run
   - No watchdog or systemd integration

## Threat Model

### What we protect against
- **Accidental host manipulation**: Student cannot modify host files outside rootfs
- **Data leakage**: Challenge files are encrypted until unlock time
- **Resource exhaustion**: cgroups + RLIMIT prevent DoS
- **Network access**: No external network interface
- **Process visibility**: PID namespace hides host processes

### What we do NOT protect against
- **Determined attacker with kernel exploit**: All Linux containers share the kernel
- **Physical access**: No TPM, no measured boot
- **Covert channels**: Cache-based, timing-based side channels are not mitigated
- **Malicious challenge author**: If the challenge binary is intentionally malicious, sandbox limits its impact but cannot prevent all harm

## Classification

### Educational Sandbox
- Current implementation fits this category
- Strong isolation for learning environments
- Acceptable risk for temporary sessions
- Not designed for long-running or multi-tenant use

### Lightweight Container Runtime
- Phases 1-10 bring us close to this
- Missing: image format, layered filesystem, network configuration
- Suitable for single-user, short-lived workloads

### Production-Grade Isolation
- **Not achieved** by Phases 1-10
- Missing: TCB minimization, verified boot, kernel hardening, attestation
- Would require additional layers: gVisor, Kata Containers, or dedicated hypervisor

## Future Improvements

1. **Read-only rootfs mount**
   - Add `MS_RDONLY` to the rootfs bind mount in `switch_root()`

2. **tmpfs for /tmp and /dev/shm**
   - Mount tmpfs with size limits for temporary storage

3. **Landlock or AppArmor**
   - Add filesystem access control beyond mount namespaces

4. **Verified boot / measured boot**
   - Ensure qo-init binary integrity before execution

5. **Systemd integration**
   - Use systemd-run with `--scope` or `--unit` for lifecycle management
   - Better cleanup on parent crash

6. **Kernel module blacklist**
   - Prevent loading of unnecessary kernel modules

7. **IO and blkio limits**
   - Add `io.max` or blkio weight to cgroup v2

8. **Audit logging**
   - Pipe seccomp violations to audit subsystem
   - Log all execve calls

9. **Health checks**
   - Periodic verification that namespaces are still active
   - Detect namespace escape attempts

10. **Snapshot / checkpoint**
    - Ability to save and restore sandbox state

---

*Security review completed. Runtime is suitable for educational use. Production-grade isolation requires additional layers.*

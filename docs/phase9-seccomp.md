# Phase 9 — Seccomp

## Objective

Design a seccomp profile to deny dangerous syscalls while allowing normal shell usage.

## Approach

Use libseccomp to define a policy with:
1. **Default allow** — all syscalls permitted unless explicitly denied
2. **Denylist of dangerous syscalls** — kernel, mount, namespace, and tracing operations
3. **Configurable enforcement** — three modes: `off`, `log`, `enforce`

## Configuration

Policy is controlled by the `QO_SECCOMP` environment variable:

| Value | Behavior |
|-------|----------|
| `off` | Seccomp disabled (default for compatibility) |
| `log` | Blocked syscalls are logged to audit subsystem but allowed |
| `enforce` | Blocked syscalls kill the process (`SECCOMP_ACT_KILL_PROCESS`) |

## Denied Syscalls

```c
SCMP_SYS(reboot)        // Reboot the system
SCMP_SYS(mount)         // Mount filesystems (already done)
SCMP_SYS(umount2)       // Unmount filesystems
SCMP_SYS(pivot_root)    // Switch root (already done)
SCMP_SYS(unshare)       // Create new namespaces
SCMP_SYS(setns)         // Join existing namespace
SCMP_SYS(init_module)   // Load kernel module
SCMP_SYS(finit_module)  // Load kernel module (fd-based)
SCMP_SYS(delete_module) // Unload kernel module
SCMP_SYS(kexec_load)    // Load new kernel
SCMP_SYS(personality)   // Change execution domain
SCMP_SYS(ptrace)        // Attach to other processes
```

## Why These Syscalls

- `reboot`, `kexec_load`, `init_module`, `delete_module` — kernel integrity
- `mount`, `umount2`, `pivot_root` — filesystem escape prevention
- `unshare`, `setns` — namespace escape prevention
- `personality` — can change syscall ABI
- `ptrace` — can inspect/hijack other processes

## Why Default is `off`

- Seccomp policies can break challenge binaries that use uncommon syscalls
- Testing is required to determine the full allowlist
- `log` mode allows identifying violations before enforcement

## Verification

- [x] `qo-init.c` compiles with `setup_seccomp()`
- [ ] `QO_SECCOMP=log` — shell works, violations logged
- [ ] `QO_SECCOMP=enforce` — dangerous syscalls blocked
- [ ] Challenge binaries run with `QO_SECCOMP=log`
- [ ] No regression in Phases 2-8

## Files Modified

- `cmd/qo-init.c` — added `setup_seccomp()` with libseccomp-based filter

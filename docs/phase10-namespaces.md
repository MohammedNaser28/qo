# Phase 10 — Additional Namespaces

## Objective

Evaluate support for `CLONE_NEWIPC` and `CLONE_NEWCGROUP`.

## Changes

Added to clone flags in `cmd/qo-init.c`:
```c
CLONE_NEWIPC | CLONE_NEWCGROUP
```

## CLONE_NEWIPC (IPC Namespace)

### Benefits
- Isolates System V IPC resources (semaphores, message queues, shared memory)
- Prevents IPC-based communication between sandbox and host
- Blocks access to host System V IPC objects

### Compatibility
- Supported on Linux 2.6.19+
- No known compatibility issues with current rootfs
- Some challenge binaries may use shared memory (needs validation)

### Performance Impact
- Negligible — IPC namespaces are lightweight

## CLONE_NEWCGROUP (Cgroup Namespace)

### Benefits
- Hides cgroup information from the sandbox
- The sandbox sees its own cgroup as the root
- Prevents information leakage about host cgroup hierarchy
- Complements existing cgroup v2 resource limits

### Compatibility
- Supported on Linux 4.6+
- Requires cgroup v2 (already used)
- Some tools that inspect `/proc/self/cgroup` may see different paths

### Performance Impact
- Negligible — cgroup namespaces are lightweight

## Verification

- [x] `qo-init.c` compiles with additional namespace flags
- [ ] `ls -la /proc/self/ns/ipc` shows different inode from host
- [ ] `ls -la /proc/self/ns/cgroup` shows different inode from host
- [ ] `ipcs` shows no host IPC objects inside sandbox
- [ ] Shell and challenge binaries still function
- [ ] No regression in Phases 2-9

## Files Modified

- `cmd/qo-init.c` — added `CLONE_NEWIPC` and `CLONE_NEWCGROUP` to clone flags

# Phase 3 — User Namespace (Rootless Support)

## Objective

Investigate adding `CLONE_NEWUSER` to the namespace flags to enable rootless operation
and add an additional layer of UID/GID isolation.

## Current State

Current clone flags:
```c
CLONE_NEWUTS | CLONE_NEWPID | CLONE_NEWNS | CLONE_NEWNET | SIGCHLD
```

No user namespace — sandbox root is real root on the host.

## Proposed Change

New clone flags:
```c
CLONE_NEWUSER | CLONE_NEWUTS | CLONE_NEWPID | CLONE_NEWNS | CLONE_NEWNET | SIGCHLD
```

## Architecture Tradeoffs

### Option A: Parent writes UID/GID maps (requires IPC)

**Pros:**
- Parent controls exact UID/GID mapping
- Can map to non-root host user for true rootless support

**Cons:**
- Requires synchronization mechanism between Go parent and C child
- Adds complexity (pipes, file descriptors, protocol)
- `exec.Command` would need `ExtraFiles` or similar
- Changes the Go/C boundary contract

### Option B: Child writes its own UID/GID maps (self-mapping)

**Pros:**
- No parent-child IPC required
- Simpler implementation
- Child is the "owner" of its user namespace and can write mappings

**Cons:**
- Mapping is fixed at clone time
- For current root-required mode: maps 0→0 (no rootless benefit yet)
- True rootless requires parent to pass target host UID/GID

## Decision

**Implement Option B first** (self-mapping) because:
1. It preserves the simple Go/C architecture
2. It adds user namespace isolation with minimal changes
3. It establishes the pattern for future rootless support
4. The parent can later be extended to pass UID/GID arguments

## Implementation

### C helper changes (`cmd/qo-init.c`)

1. Add `CLONE_NEWUSER` to clone flags
2. In `child()`, before `chroot()`:
   - Write `"deny"` to `/proc/self/setgroups` (required before writing gid_map on many kernels)
   - Write UID map: `0 <host-uid> 1` to `/proc/self/uid_map`
   - Write GID map: `0 <host-gid> 1` to `/proc/self/gid_map`

### Go changes

None required for self-mapping approach.

### Current limitation

Since the Go code checks `os.Geteuid() != 0` and requires root, the parent runs as root.
The child maps UID 0 → host UID 0. This provides:
- Additional isolation layer (user namespace + other namespaces)
- Protection against accidental host manipulation via namespace boundaries

True rootless support (running as non-root on host) would require:
1. Removing the root check in `cmd/start.go`
2. Passing target host UID/GID to `qo-init`
3. Using `unshare(CLONE_NEWUSER)` with `UintGidMappings` or similar
4. Handling `setgroups` restrictions more carefully

## Verification

- [x] `qo-init.c` compiles with `CLONE_NEWUSER`
- [ ] `id` inside sandbox shows `uid=0(root)` (expected in user namespace)
- [ ] `id` outside sandbox is unaffected
- [ ] `chroot`, `mount` still work inside sandbox
- [ ] No regression in existing namespace behavior

## Files Modified

- `cmd/qo-init.c` — added `CLONE_NEWUSER` and self-mapping logic

## Next Steps

Proceed to Phase 4: Filesystem Isolation (pivot_root evaluation).

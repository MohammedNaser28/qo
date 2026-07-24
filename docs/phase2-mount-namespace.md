# Phase 2 — Mount Namespace Improvements

## Change

Added `mount(NULL, "/", NULL, MS_REC | MS_PRIVATE, NULL)` at the beginning of `child()` in `cmd/qo-init.c`.

```c
if (mount(NULL, "/", NULL, MS_REC | MS_PRIVATE, NULL) != 0) {
    perror("mount private");
    return 1;
}
```

## Rationale

Without this call, mount propagation between the mount namespace and the parent mount namespace is **shared** by default. This means:

- Mounts created inside the sandbox (e.g., `proc`, `devpts`) can propagate to the host
- Mounts created on the host can propagate into the sandbox
- This breaks isolation and can lead to security issues

Setting the entire mount tree to `MS_PRIVATE` ensures:

- All mounts inside the sandbox stay inside the sandbox
- The host mount namespace is unaffected by sandbox activity
- Future mount operations (e.g., `pivot_root` in later phases) behave predictably

## Implementation Details

- Placed immediately after entering the mount namespace (i.e., at the start of `child()`)
- Executed before `chroot()` so that the mount point `/` refers to the namespace root, not the future chroot
- `MS_REC` applies the flag recursively to all existing mounts
- `MS_PRIVATE` makes mounts private (neither shared nor slave)

## Verification

- [x] `qo-init.c` compiles successfully
- [x] Go code compiles successfully
- [ ] `mount` inside sandbox shows proc and devpts
- [ ] `mount` outside sandbox is unaffected
- [ ] Cleanup still works (unmounts succeed)
- [ ] Session can be started and exited normally

## Files Modified

- `cmd/qo-init.c` — added private mount call
- `scripts/build-experimental.sh` — new build script for experimental binaries
- `docs/phase2-mount-namespace.md` — this document

## Next Steps

Proceed to Phase 3: User Namespace (Rootless Support).

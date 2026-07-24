# Phase 8 — Resource Limits

## Objective

Extend isolation with POSIX resource limits (RLIMIT) that complement existing cgroup configuration.

## Implementation

```c
static void set_resource_limits(void) {
    struct rlimit rlim;

    rlim.rlim_cur = rlim.rlim_max = 1024;
    setrlimit(RLIMIT_NOFILE, &rlim);

    rlim.rlim_cur = rlim.rlim_max = 128;
    setrlimit(RLIMIT_NPROC, &rlim);

    rlim.rlim_cur = rlim.rlim_max = 0;
    setrlimit(RLIMIT_CORE, &rlim);

    rlim.rlim_cur = rlim.rlim_max = 10485760;
    setrlimit(RLIMIT_FSIZE, &rlim);
}
```

## Limits Configured

| Limit | Value | Rationale |
|-------|-------|-----------|
| `RLIMIT_NOFILE` | 1024 | Prevents file descriptor exhaustion attacks |
| `RLIMIT_NPROC` | 128 | Complements cgroup `pids.max=200` |
| `RLIMIT_CORE` | 0 | Prevents core dumps from leaking memory contents |
| `RLIMIT_FSIZE` | 10 MiB | Prevents disk-filling attacks |

## Relationship with Cgroups

- cgroup `memory.max=512MiB` controls total memory
- cgroup `pids.max=200` controls total processes
- RLIMIT_NOFILE and RLIMIT_NPROC provide per-process enforcement
- RLIMIT_CORE and RLIMIT_FSIZE add defense-in-depth

## Verification

- [x] `qo-init.c` compiles with `set_resource_limits()`
- [ ] `ulimit -n` shows 1024 inside sandbox
- [ ] `ulimit -u` shows 128 inside sandbox
- [ ] `ulimit -c` shows 0 inside sandbox
- [ ] `ulimit -f` shows 10240 (10 MiB in 512-byte blocks) inside sandbox
- [ ] No regression in Phases 2-7

## Files Modified

- `cmd/qo-init.c` — added `set_resource_limits()` and call after root switch

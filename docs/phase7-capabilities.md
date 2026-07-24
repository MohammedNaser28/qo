# Phase 7 — Capability Reduction

## Objective

Drop unnecessary Linux capabilities after sandbox initialization.

## Current State

The sandbox retains full root capabilities inside the namespace. This is unnecessary
and increases the attack surface if an escape occurs.

## Implementation

After mount operations and root switch, call `capset()` with an empty capability set:

```c
static void drop_capabilities(void) {
    struct __user_cap_header_struct hdr = { .version = _LINUX_CAPABILITY_VERSION_3, .pid = 0 };
    struct __user_cap_data_struct data[2] = {{0}};

    if (syscall(SYS_capset, &hdr, data) != 0) {
        perror("capset");
    }
}
```

## Why This Works

- Inside a user namespace, the process has `CAP_SETPCAP` in that namespace
- `capset()` can be called without `CAP_SETPCAP` in the parent namespace
- Dropping to an empty bounding set prevents privilege escalation via setuid binaries

## Capabilities Retained

None. The shell and challenge binaries operate without elevated capabilities.

## Why No Capabilities Are Needed

- File operations: permitted via normal DAC (file permissions)
- Process operations: permitted within the PID namespace
- Mount operations: already completed before dropping
- Network operations: loopback is up; no raw sockets needed for basic TCP/UDP

## Verification

- [x] `qo-init.c` compiles with `drop_capabilities()`
- [ ] `cat /proc/self/status | grep CapEff` shows `0000000000000000` inside sandbox
- [ ] Shell still functions normally
- [ ] Challenge binaries still run
- [ ] No regression in Phases 2-6

## Files Modified

- `cmd/qo-init.c` — added `drop_capabilities()` and call after root switch

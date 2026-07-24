# Phase 5 — Network Namespace Initialization

## Objective

Initialize the loopback interface (`lo`) inside the fresh network namespace.

## Problem

A fresh network namespace has no network interfaces. The loopback interface (`lo`) exists
but is administratively down. This means:
- `ping localhost` fails
- Any network-dependent software that expects loopback will fail
- TCP connections to `127.0.0.1` cannot be established

## Solution

Bring up the loopback interface programmatically inside the child process after entering
the network namespace.

```c
static int setup_loopback(void) {
    int sock = socket(AF_INET, SOCK_DGRAM, 0);
    if (sock < 0) {
        perror("socket");
        return -1;
    }

    struct ifreq ifr;
    memset(&ifr, 0, sizeof(ifr));
    strncpy(ifr.ifr_name, "lo", IFNAMSIZ - 1);
    ifr.ifr_flags = IFF_UP | IFF_LOOPBACK | IFF_RUNNING;

    if (ioctl(sock, SIOCSIFFLAGS, &ifr) != 0) {
        perror("ioctl SIOCSIFFLAGS");
        close(sock);
        return -1;
    }

    close(sock);
    return 0;
}
```

Called in `child()` after namespace setup but before `switch_root()` / `chroot()`.

## Verification

- [x] `qo-init.c` compiles with loopback setup
- [ ] `ping localhost` works inside sandbox
- [ ] `ip addr` shows `lo` as `UP,LOOPBACK`
- [ ] No external networking is configured
- [ ] No regression in Phase 2-4 behavior

## Files Modified

- `cmd/qo-init.c` — added `setup_loopback()` and call in `child()`

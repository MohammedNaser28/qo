# Phase 6 — PID 1 Improvements

## Objective

Make `qo-init` act as PID 1 (init) inside the sandbox instead of `/bin/bash`.

## Problem

Currently, `qo-init` execs `/bin/bash` directly:
```c
execl("/bin/bash", "/bin/bash", "-i", NULL);
```

This means:
- `bash` becomes PID 1
- `bash` is not designed to be PID 1 — it doesn't reap zombie processes
- Orphaned processes spawned by bash become zombies
- Signal handling is limited to bash's default behavior
- Clean termination is unreliable

## Solution

`qo-init` now acts as a lightweight init process:
1. Forks a child to run `/bin/bash`
2. Parent (`qo-init`, PID 1) enters a `waitpid(-1, ...)` loop
3. Reaps all terminated children, including orphans
4. Exits when the shell child exits
5. Forwards signals via process group membership (same PGID)

```c
static int spawn_shell(const char *rootfsPath) {
    pid_t shell_pid = fork();
    if (shell_pid < 0) {
        perror("fork");
        return -1;
    }

    if (shell_pid == 0) {
        if (chdir("/tmp") != 0) {
            perror("chdir /tmp");
            _exit(1);
        }
        execl("/bin/bash", "/bin/bash", "-i", NULL);
        perror("execl");
        _exit(1);
    }

    int status = 0;
    pid_t pid;
    while ((pid = waitpid(-1, &status, 0)) > 0) {
        if (pid == shell_pid) {
            if (WIFEXITED(status)) {
                return WEXITSTATUS(status);
            } else if (WIFSIGNALED(status)) {
                return 128 + WTERMSIG(status);
            }
            return 1;
        }
    }

    if (pid < 0 && errno == ECHILD) {
        return 1;
    }

    return 1;
}
```

## Signal Forwarding

- Signals sent to the process group (e.g., `kill -TERM -<pid>`) reach both `qo-init` and `bash`
- `qo-init` waits for `bash` to exit and returns its exit status
- If `bash` is killed by a signal, `qo-init` returns `128 + <signal>`

## Zombie Prevention

- `waitpid(-1, ...)` reaps ANY child process, not just the shell
- Orphaned grandchildren (if bash spawns background jobs) are reparented to PID 1 and reaped
- No zombie accumulation

## Verification

- [x] `qo-init.c` compiles with `spawn_shell()` instead of `execl()`
- [ ] Shell still launches interactively
- [ ] Background processes in shell are reaped on exit
- [ ] Ctrl+C (SIGINT) terminates shell cleanly
- [ ] Duration timer still terminates session
- [ ] No zombie processes after session ends

## Files Modified

- `cmd/qo-init.c` — replaced `execl()` with `spawn_shell()` using `fork()` + `waitpid()` loop

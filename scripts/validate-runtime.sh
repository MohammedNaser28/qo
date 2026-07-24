#!/usr/bin/env bash
set -euo pipefail

echo "=== qo Runtime Validation ==="
echo "This script validates sandbox isolation properties."
echo "Run from a terminal where you can start an interactive qo session."
echo ""

export QO_SECCOMP="${QO_SECCOMP:-log}"

echo "--- Starting qo session ---"
echo "Run the following commands inside the sandbox:"
echo ""
echo "  1. Filesystem isolation:"
echo "     pwd"
echo "     ls /"
echo "     mount | grep -E 'proc|devpts'"
echo ""
echo "  2. PID namespace:"
echo "     echo \$\$"
echo "     ps"
echo ""
echo "  3. Mount namespace:"
echo "     mount"
echo "     cat /proc/self/mountinfo"
echo ""
echo "  4. UTS namespace:"
echo "     hostname"
echo ""
echo "  5. Network namespace:"
echo "     ip addr"
echo "     ping -c 1 localhost"
echo ""
echo "  6. Process cleanup test:"
echo "     (sleep 30 &)"
echo "     jobs"
echo "     exit"
echo ""
echo "  7. Cgroup verification:"
echo "     cat /sys/fs/cgroup/qo-sessions/*/memory.max"
echo "     cat /sys/fs/cgroup/qo-sessions/*/pids.max"
echo ""
echo "Press Enter when ready to start qo..."
read -r

if [ -x ./qo ]; then
    QO_BIN=./qo
elif [ -x ./deploy/qo ]; then
    QO_BIN=./deploy/qo
else
    echo "qo binary not found"
    exit 1
fi

if [ ! -x ./qo-init ]; then
    echo "qo-init binary not found (run scripts/build-experimental.sh first)"
    exit 1
fi

export QO_SECCOMP="${QO_SECCOMP:-log}"
"$QO_BIN" start -i 9999 -a ./test/test.enc -p test -k test -d 1m || true

echo ""
echo "=== Validation complete ==="
echo ""
echo "Verify cleanup:"
echo "  mount | grep qo-sessions && echo 'MOUNTS LEAKED' || echo 'No leaked mounts'"
echo "  ls /tmp/qo-sessions/ 2>/dev/null && echo 'DIRS LEAKED' || echo 'No leaked dirs'"
echo "  ls /sys/fs/cgroup/qo-sessions/ 2>/dev/null && echo 'CGROUPS LEAKED' || echo 'No leaked cgroups'"

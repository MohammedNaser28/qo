#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

echo "=== Building experimental qo-init C helper ==="
gcc -O2 -lseccomp -o "$ROOT/qo-init" "$ROOT/cmd/qo-init.c"

echo "=== Building qo binary (experimental branch) ==="
CGO_ENABLED=0 go build -o "$ROOT/qo" .

echo "=== Build complete ==="
echo ""
echo "Binaries placed in qo/ directory:"
echo "  qo          — experimental qo CLI binary"
echo "  qo-init     — experimental qo-init helper"
echo ""
echo "To test, run from qo/ directory:"
echo "  sudo ./qo start -i 0 -a <archive> -p <password> -k <key> -d 5m"

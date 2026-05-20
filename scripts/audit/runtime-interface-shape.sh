#!/usr/bin/env bash
# ABOUTME: Reproduces the Runtime-interface shape check from the 2026-05 audit
# ABOUTME: — counts methods on the core Runtime interface and lists optional
# ABOUTME: interfaces that backends implement piecemeal (F2).
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

RUNTIME_FILE=runtime/runtime.go

echo "## Core Runtime interface ($RUNTIME_FILE)"
# Extract everything between "type Runtime interface {" and the matching "}"
awk '
  /type Runtime interface \{/ { inside=1; next }
  inside && /^\}/             { inside=0; next }
  inside {
    s=$0
    sub(/^[[:space:]]+/, "", s)
    sub(/[[:space:]]*\/\/.*/, "", s)
    if (s == "") next
    if (s ~ /^[A-Z][A-Za-z0-9_]*\(/) print s
  }
' "$RUNTIME_FILE" > /tmp/runtime-methods.txt

method_count=$(wc -l < /tmp/runtime-methods.txt)
echo "  method count: $method_count"
echo
echo "  methods:"
sed 's/^/    /' /tmp/runtime-methods.txt
rm -f /tmp/runtime-methods.txt

echo
echo "## Optional adapter interfaces in runtime/ (top-level type ... interface)"
grep -rnE "^type [A-Z][A-Za-z0-9_]* interface" runtime/ --include="*.go" --exclude="*_test.go" \
  | grep -v "type Runtime interface" || echo "(none)"

echo
echo "## Backends registered (runtime.Register calls in each backend package)"
grep -rnE "runtime\.Register\(" runtime/ --include="*.go" --exclude="*_test.go" || true

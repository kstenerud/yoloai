#!/usr/bin/env bash
# ABOUTME: Reproduces the slog convention check from the 2026-05 audit —
# ABOUTME: verifies the canonical "err" attribute key, looks for stragglers
# ABOUTME: using "error", and counts slog call sites without an event key (F9).
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

echo "## slog calls in production code"
# `grep -c` exits non-zero on zero matches; `|| true` keeps `set -e` happy.
total=$(grep -rEcn 'slog\.(Debug|Info|Warn|Error)\(' --include="*.go" --exclude="*_test.go" . | awk -F: '{s+=$2} END{print s+0}' || true)
echo "  total:                  $total"

with_event=$(grep -rEcn 'slog\.(Debug|Info|Warn|Error)\([^)]*"event"' --include="*.go" --exclude="*_test.go" . | awk -F: '{s+=$2} END{print s+0}' || true)
echo "  with \"event\" key:       $with_event"

err_key=$(grep -rEcn 'slog\.(Debug|Info|Warn|Error)\([^)]*"err"' --include="*.go" --exclude="*_test.go" . | awk -F: '{s+=$2} END{print s+0}' || true)
error_key=$(grep -rEcn 'slog\.(Debug|Info|Warn|Error)\([^)]*"error"' --include="*.go" --exclude="*_test.go" . | awk -F: '{s+=$2} END{print s+0}' || true)
echo "  with \"err\" key:         $err_key"
echo "  with \"error\" key (bad): $error_key"

echo
echo "## Any remaining slog calls using \"error\" key (W9 acceptance: zero)"
matches=$(grep -rnE 'slog\.(Debug|Info|Warn|Error)\([^)]*"error"' --include="*.go" --exclude="*_test.go" . || true)
if [ -z "$matches" ]; then echo "(none)"; else printf '%s\n' "$matches"; fi

echo
echo "## sloglint configuration in .golangci.yml"
# Print the sloglint: block (any line whose indent depth equals or exceeds
# the block's own indent + 2). Stops at the next sibling key.
awk '
  /^[[:space:]]*sloglint:[[:space:]]*$/ {
    print
    match($0, /^[[:space:]]*/)
    base = RLENGTH
    inside = 1
    next
  }
  inside {
    if ($0 ~ /^[[:space:]]*$/) { print; next }
    match($0, /^[[:space:]]*/)
    if (RLENGTH > base) print
    else { inside = 0 }
  }
' .golangci.yml

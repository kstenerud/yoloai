#!/bin/sh
# ABOUTME: Generic agent launch wrapper — runs the agent, then on its exit writes
# ABOUTME: an authoritative `done` status and drops the pane to an interactive shell.

# The fall-to-shell launch shape (D96 / session-layer.md). The agent runs as our
# CHILD (not exec'd), so when it exits we regain control of the pane. We record
# `done` ourselves because pane death no longer does it — we keep the pane alive
# as a usable shell instead of letting it die. The agent-status.json schema is
# owned by status-monitor.py's write_status (fenced by schema_version_test.go);
# we reuse it via `--write-done` rather than hand-rolling JSON here so the schema
# cannot silently drift.
#
# Used only when runtime-config.json sets fall_to_shell (hook-authoritative
# agents in Phase 1). Paths derive from YOLOAI_DIR (set in the image), so no
# extra env needs threading through the launch command.

YOLOAI_DIR="${YOLOAI_DIR:-/yoloai}"
status_file="${YOLOAI_DIR}/agent-status.json"
monitor="${YOLOAI_DIR}/bin/status-monitor.py"

"$@"
rc=$?

python3 "$monitor" --write-done "$status_file" "$rc" 2>/dev/null || true

printf '\n[yoloai] agent exited (status %s). This pane is now an interactive shell.\n\n' "$rc"

exec "${SHELL:-/bin/bash}" -l

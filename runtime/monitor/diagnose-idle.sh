#!/bin/bash
# Point-in-time snapshot of all idle detection state.
# Run inside a yoloai sandbox to diagnose idle detection issues.
set -uo pipefail

YOLOAI_DIR="${YOLOAI_DIR:-/yoloai}"
CONFIG="$YOLOAI_DIR/runtime-config.json"
[ -f "$CONFIG" ] || CONFIG="$YOLOAI_DIR/config.json"
STATUS="$YOLOAI_DIR/agent-status.json"
LOG="$YOLOAI_DIR/monitor.log"

echo "=== Idle Detection Diagnostic ==="
echo "Time: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo

# 1. Current status
echo "--- agent-status.json ---"
if [ -f "$STATUS" ]; then
    cat "$STATUS"
    TS=$(jq -r '.timestamp // 0' "$STATUS" 2>/dev/null)
    if [ "$TS" != "0" ] && [ "$TS" != "null" ]; then
        AGE=$(( $(date +%s) - TS ))
        echo "(age: ${AGE}s)"
    fi
else
    echo "(missing)"
fi
echo

# 2. Detector config
echo "--- Detector stack ---"
jq -r '.detectors // [] | join(" → ")' "$CONFIG" 2>/dev/null || echo "(config unreadable)"
echo

# 3. Agent PID and process info
PANE_PID=$(tmux list-panes -t main -F '#{pane_pid}' 2>/dev/null)
echo "--- Agent process ---"
echo "Pane PID: ${PANE_PID:-unknown}"
if [ -n "$PANE_PID" ]; then
    # Show process tree from pane PID
    ps --forest -o pid,ppid,wchan,stat,comm -g "$PANE_PID" 2>/dev/null || \
        ps -o pid,ppid,wchan,stat,comm -p "$PANE_PID" 2>/dev/null || \
        echo "(ps failed)"
fi
echo

# 4. Wchan for agent PID
echo "--- Wchan ---"
if [ -n "$PANE_PID" ] && [ -f "/proc/$PANE_PID/wchan" ]; then
    echo "PID $PANE_PID: $(cat /proc/$PANE_PID/wchan)"
    # Also check child processes
    for child in /proc/$PANE_PID/task/*/children; do
        [ -f "$child" ] || continue
        for cpid in $(cat "$child" 2>/dev/null); do
            [ -f "/proc/$cpid/wchan" ] && echo "PID $cpid (child): $(cat /proc/$cpid/wchan)"
        done
    done
elif [ -n "$PANE_PID" ]; then
    # macOS fallback
    ps -o pid,wchan= -p "$PANE_PID" 2>/dev/null || echo "(unavailable)"
else
    echo "(no agent PID)"
fi
echo

# 5. Network connections
echo "--- TCP connections (ESTABLISHED) ---"
if [ -n "$PANE_PID" ] && [ -f "/proc/$PANE_PID/net/tcp6" ]; then
    COUNT=$(awk '$4 == "01" {n++} END {print n+0}' "/proc/$PANE_PID/net/tcp6")
    echo "Count: $COUNT"
    if [ "$COUNT" -gt 0 ]; then
        awk '$4 == "01" {print $2, "->", $3}' "/proc/$PANE_PID/net/tcp6" | head -10
    fi
elif command -v lsof &>/dev/null && [ -n "$PANE_PID" ]; then
    lsof -i TCP -p "$PANE_PID" -sTCP:ESTABLISHED 2>/dev/null | head -10 || echo "(none)"
else
    echo "(unavailable)"
fi
echo

# 6. Tmux pane state
echo "--- Tmux pane ---"
PANE_INFO=$(tmux list-panes -t main -F '#{pane_dead}|#{pane_dead_status}' 2>/dev/null)
echo "Dead|ExitCode: ${PANE_INFO:-unknown}"
echo "Bottom 5 non-empty lines:"
tmux capture-pane -t main -p 2>/dev/null | grep -v '^$' | tail -5
echo

# 7. Hook status (Claude Code)
HOOK_IDLE=$(jq -r '.idle.Hook // false' "$CONFIG" 2>/dev/null)
if [ "$HOOK_IDLE" = "true" ]; then
    echo "--- Hook detector ---"
    echo "Hook-based idle: enabled"
    SETTINGS="$HOME/.claude/settings.json"
    if [ -f "$SETTINGS" ]; then
        echo "Hooks in settings.json:"
        jq '.hooks // "none"' "$SETTINGS" 2>/dev/null
    else
        echo "settings.json: missing"
    fi
    echo
fi

# 8. Monitor log tail
echo "--- Monitor log (last 20 lines) ---"
if [ -f "$LOG" ]; then
    tail -20 "$LOG"
else
    echo "(no monitor.log — debug mode not enabled?)"
fi

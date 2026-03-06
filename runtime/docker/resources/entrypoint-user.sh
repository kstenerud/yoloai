#!/bin/bash
set -euo pipefail

# Read agent config directly (avoids shell quoting issues passing through gosu)
CONFIG="/yoloai/config.json"
AGENT_COMMAND=$(jq -r .agent_command "$CONFIG")
STARTUP_DELAY=$(jq -r .startup_delay "$CONFIG")
READY_PATTERN=$(jq -r .ready_pattern "$CONFIG")
SUBMIT_SEQUENCE=$(jq -r .submit_sequence "$CONFIG")
TMUX_CONF=$(jq -r .tmux_conf "$CONFIG")
HOOK_IDLE=$(jq -r ".hook_idle // false" "$CONFIG")
SANDBOX_NAME=$(jq -r ".sandbox_name // \"sandbox\"" "$CONFIG")
DEBUG=$(jq -r ".debug // false" "$CONFIG")
debug_log() { [ "$DEBUG" = "true" ] && echo "[debug] $*" || true; }
set_title() { tmux rename-window -t main "$1" 2>/dev/null || true; }

# --- Git baseline for overlay mounts ---
OVERLAY_COUNT=$(jq ".overlay_mounts // [] | length" "$CONFIG")
if [ "$OVERLAY_COUNT" -gt 0 ]; then
    for i in $(seq 0 $((OVERLAY_COUNT - 1))); do
        MERGED=$(jq -r ".overlay_mounts[$i].merged" "$CONFIG")
        debug_log "creating git baseline for overlay: $MERGED"
        # Remove .git dirs (creates whiteouts in upper layer)
        find "$MERGED" -name .git -exec rm -rf {} + 2>/dev/null || true
        # Create fresh baseline
        git -C "$MERGED" init
        git -C "$MERGED" add -A
        git -C "$MERGED" -c user.name=yoloai -c user.email=yoloai@local commit -m "baseline" --allow-empty
    done
fi

# Start tmux session with config based on tmux_conf setting
debug_log "starting tmux session (tmux_conf=$TMUX_CONF)"
case "$TMUX_CONF" in
    default+host)
        tmux -f /yoloai/tmux.conf new-session -d -s main -x 200 -y 50
        if [ -f /home/yoloai/.tmux.conf ]; then
            tmux source-file /home/yoloai/.tmux.conf
        fi
        ;;
    default)
        tmux -f /yoloai/tmux.conf new-session -d -s main -x 200 -y 50
        ;;
    host)
        if [ -f /home/yoloai/.tmux.conf ]; then
            tmux -f /home/yoloai/.tmux.conf new-session -d -s main -x 200 -y 50
        else
            tmux new-session -d -s main -x 200 -y 50
        fi
        ;;
    *)
        tmux new-session -d -s main -x 200 -y 50
        ;;
esac
tmux set-option -t main remain-on-exit on
tmux pipe-pane -t main "cat >> /yoloai/log.txt"

# Launch agent inside tmux (exec replaces shell so agent exit = pane exit)
debug_log "launching agent: $AGENT_COMMAND"
tmux send-keys -t main "exec $AGENT_COMMAND" Enter

# Monitor for agent exit: when pane dies, detach all attached clients
(
    while [ "$(tmux list-panes -t main -F '#{pane_dead}' 2>/dev/null)" != "1" ]; do
        sleep 1
    done
    tmux list-clients -t main -F '#{client_name}' 2>/dev/null | while read -r c; do
        tmux detach-client -t "$c" 2>/dev/null || true
    done
) &

# --- Auto-commit loop for :copy directories ---
AUTO_COMMIT_INTERVAL=$(jq -r ".auto_commit_interval // 0" "$CONFIG")
COPY_DIR_COUNT=$(jq ".copy_dirs // [] | length" "$CONFIG")
if [ "$AUTO_COMMIT_INTERVAL" -gt 0 ] && [ "$COPY_DIR_COUNT" -gt 0 ]; then
    debug_log "starting auto-commit loop (interval=${AUTO_COMMIT_INTERVAL}s, dirs=$COPY_DIR_COUNT)"
    (
        while true; do
            sleep "$AUTO_COMMIT_INTERVAL"
            for i in $(seq 0 $((COPY_DIR_COUNT - 1))); do
                DIR=$(jq -r ".copy_dirs[$i]" "$CONFIG")
                git -C "$DIR" add -A 2>/dev/null || true
                git -C "$DIR" \
                    -c user.name=yoloai \
                    -c user.email=yoloai@localhost \
                    commit -m "auto-commit at $(date -u +%Y-%m-%dT%H:%M:%SZ)" \
                    2>/dev/null || true
            done
        done
    ) &
fi

# --- Wait for agent ready, auto-accept trust/confirmation prompts ---
# Always run this loop so interactive "Enter to confirm" prompts (workspace trust,
# permissions mode) are handled even when no prompt file is provided.
debug_log "waiting for agent ready (pattern=$READY_PATTERN)"
if [ -n "$READY_PATTERN" ] && [ "$READY_PATTERN" != "null" ]; then
    MAX_WAIT=60
    WAITED=0
    while [ $WAITED -lt $MAX_WAIT ]; do
        PANE=$(tmux capture-pane -t main -p 2>/dev/null || true)
        # Auto-accept confirmation prompts first (they may contain the ready pattern character)
        if echo "$PANE" | grep -qF "Enter to confirm"; then
            # Bypass Permissions prompt defaults to "No, exit" — move to "Yes" first
            if echo "$PANE" | grep -qF "Yes, I accept"; then
                tmux send-keys -t main Down Enter
            else
                tmux send-keys -t main Enter
            fi
            sleep 2
            WAITED=$((WAITED + 2))
            continue
        fi
        if echo "$PANE" | grep -qF "$READY_PATTERN"; then
            break
        fi
        sleep 1
        WAITED=$((WAITED + 1))
    done
    # Wait for screen to stabilize (no changes for 1 consecutive check)
    PREV=""
    STABLE=0
    while [ $STABLE -lt 1 ] && [ $WAITED -lt $MAX_WAIT ]; do
        sleep 0.5
        WAITED=$((WAITED + 1))
        CURR=$(tmux capture-pane -t main -p 2>/dev/null || true)
        if [ "$CURR" = "$PREV" ]; then
            STABLE=$((STABLE + 1))
        else
            STABLE=0
        fi
        PREV="$CURR"
    done
else
    # Fallback to fixed delay if no ready pattern configured
    sleep "$STARTUP_DELAY"
fi

# --- Deliver prompt if present ---
debug_log "checking for prompt file"
if [ -f /yoloai/prompt.txt ]; then
    debug_log "delivering prompt"
    tmux load-buffer /yoloai/prompt.txt
    tmux paste-buffer -t main
    # Send submit keys individually with delay to ensure TUI processes each
    sleep 0.5
    for key in $SUBMIT_SEQUENCE; do
        tmux send-keys -t main "$key"
        sleep 0.2
    done
fi

# --- Status monitor ---
STATUS_FILE="/yoloai/status.json"
write_status() { printf '{"status":"%s","exit_code":%s,"timestamp":%d}\n' "$1" "$2" "$(date +%s)" > "$STATUS_FILE"; }
# If no prompt was delivered, the agent is waiting for input — start as idle.
if [ -f /yoloai/prompt.txt ]; then
    write_status running null
    set_title "$SANDBOX_NAME"
else
    write_status idle null
    set_title "> $SANDBOX_NAME"
fi
(
    PREV_TITLE=""
    update_title() {
        if [ "$1" != "$PREV_TITLE" ]; then
            set_title "$1"
            PREV_TITLE="$1"
        fi
    }
    while true; do
        PANE_DEAD=$(tmux list-panes -t main -F '#{pane_dead}' 2>/dev/null || echo "error")
        if [ "$PANE_DEAD" = "1" ]; then
            EXIT_CODE=$(tmux list-panes -t main -F '#{pane_dead_status}' 2>/dev/null || echo "1")
            write_status done "$EXIT_CODE"
            update_title "$SANDBOX_NAME"
            break
        elif [ "$PANE_DEAD" = "error" ]; then
            write_status done 1
            update_title "$SANDBOX_NAME"
            break
        fi
        # When hook_idle is true, the agent hooks write idle status
        # to status.json — no need to poll tmux. The monitor only tracks
        # running (initial) and done (pane death). Read status.json to
        # update the terminal title.
        if [ "$HOOK_IDLE" = "true" ]; then
            CUR_STATUS=$(jq -r '.status // "running"' "$STATUS_FILE" 2>/dev/null || echo "running")
            if [ "$CUR_STATUS" = "idle" ]; then
                update_title "> $SANDBOX_NAME"
            else
                update_title "$SANDBOX_NAME"
            fi
        else
            NEW_STATUS="running"
            if [ -n "$READY_PATTERN" ] && [ "$READY_PATTERN" != "null" ]; then
                PANE_CONTENT=$(tmux capture-pane -t main -p 2>/dev/null || true)
                if echo "$PANE_CONTENT" | grep -qF "$READY_PATTERN"; then
                    NEW_STATUS="idle"
                fi
            fi
            write_status "$NEW_STATUS" null
            if [ "$NEW_STATUS" = "idle" ]; then
                update_title "> $SANDBOX_NAME"
            else
                update_title "$SANDBOX_NAME"
            fi
        fi
        sleep 2
    done
) &

debug_log "entrypoint setup complete, blocking on tmux wait"

# Block forever — container stops only on explicit docker stop
exec tmux wait-for yoloai-exit

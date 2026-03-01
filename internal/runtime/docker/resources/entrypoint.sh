#!/bin/bash
set -euo pipefail

# Capture all entrypoint output to log.txt (preserves docker logs via tee)
exec > >(tee -a /yoloai/log.txt) 2>&1

# --- Run as root ---

# Read config (only UID/GID needed in root context; agent config read by inner bash)
CONFIG="/yoloai/config.json"
HOST_UID=$(jq -r '.host_uid' "$CONFIG")
HOST_GID=$(jq -r '.host_gid' "$CONFIG")

# Remap UID/GID to match host user
CURRENT_UID=$(id -u yoloai)
CURRENT_GID=$(id -g yoloai)

if [ "$CURRENT_GID" != "$HOST_GID" ]; then
    groupmod -g "$HOST_GID" yoloai 2>/dev/null || true
fi
if [ "$CURRENT_UID" != "$HOST_UID" ]; then
    usermod -u "$HOST_UID" yoloai 2>/dev/null || true
fi

# Fix ownership on container-managed directories.
# Some files may be bind-mounted read-only; chown on those is expected to fail.
chown -R yoloai:yoloai /home/yoloai 2>/dev/null || true
chown yoloai:yoloai /yoloai
for f in /yoloai/*; do
    chown yoloai:yoloai "$f" 2>/dev/null || true
done

# Read secrets and export as env vars
if [ -d /run/secrets ]; then
    for secret in /run/secrets/*; do
        [ -f "$secret" ] || continue
        varname=$(basename "$secret")
        export "$varname"="$(cat "$secret")"
    done
fi

# Suppress browser-open attempts inside the sandbox (agents may try to open
# documentation URLs). Only set if not already provided via secrets/env.
export BROWSER="${BROWSER:-true}"

# --- Drop privileges and run as yoloai ---
exec gosu yoloai bash -c '
set -euo pipefail

# Read agent config directly (avoids shell quoting issues passing through gosu)
CONFIG="/yoloai/config.json"
AGENT_COMMAND=$(jq -r .agent_command "$CONFIG")
STARTUP_DELAY=$(jq -r .startup_delay "$CONFIG")
READY_PATTERN=$(jq -r .ready_pattern "$CONFIG")
SUBMIT_SEQUENCE=$(jq -r .submit_sequence "$CONFIG")
TMUX_CONF=$(jq -r .tmux_conf "$CONFIG")

# Start tmux session with config based on tmux_conf setting
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
tmux send-keys -t main "exec $AGENT_COMMAND" Enter

# Monitor for agent exit: when pane dies, detach all attached clients
(
    while [ "$(tmux list-panes -t main -F "#{pane_dead}" 2>/dev/null)" != "1" ]; do
        sleep 1
    done
    tmux list-clients -t main -F "#{client_name}" 2>/dev/null | while read -r c; do
        tmux detach-client -t "$c" 2>/dev/null || true
    done
) &

# --- Wait for agent ready, auto-accept trust/confirmation prompts ---
# Always run this loop so interactive "Enter to confirm" prompts (workspace trust,
# permissions mode) are handled even when no prompt file is provided.
if [ -n "$READY_PATTERN" ] && [ "$READY_PATTERN" != "null" ]; then
    MAX_WAIT=60
    WAITED=0
    while [ $WAITED -lt $MAX_WAIT ]; do
        PANE=$(tmux capture-pane -t main -p 2>/dev/null || true)
        # Auto-accept confirmation prompts first (they may contain the ready pattern character)
        if echo "$PANE" | grep -qF "Enter to confirm"; then
            tmux send-keys -t main Enter
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
if [ -f /yoloai/prompt.txt ]; then
    tmux load-buffer /yoloai/prompt.txt
    tmux paste-buffer -t main
    # Send submit keys individually with delay to ensure TUI processes each
    sleep 0.5
    for key in $SUBMIT_SEQUENCE; do
        tmux send-keys -t main "$key"
        sleep 0.2
    done
fi

# Block forever — container stops only on explicit docker stop
exec tmux wait-for yoloai-exit
'

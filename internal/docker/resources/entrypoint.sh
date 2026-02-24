#!/bin/bash
set -euo pipefail

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

# Fix ownership on container-managed directories
# Some files under /yoloai are bind-mounted read-only; chown on those is expected to fail.
chown -R yoloai:yoloai /home/yoloai
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

# --- Drop privileges and run as yoloai ---
exec gosu yoloai bash -c '
set -euo pipefail

# Read agent config directly (avoids shell quoting issues passing through gosu)
CONFIG="/yoloai/config.json"
AGENT_COMMAND=$(jq -r .agent_command "$CONFIG")
STARTUP_DELAY=$(jq -r .startup_delay "$CONFIG")
SUBMIT_SEQUENCE=$(jq -r .submit_sequence "$CONFIG")

# Start tmux session with logging and remain-on-exit
tmux new-session -d -s main -x 200 -y 50
tmux set-option -t main remain-on-exit on
tmux pipe-pane -t main "cat >> /yoloai/log.txt"

# Launch agent inside tmux
tmux send-keys -t main "$AGENT_COMMAND" Enter

# If prompt exists, deliver it after startup delay
if [ -f /yoloai/prompt.txt ]; then
    sleep "$STARTUP_DELAY"
    tmux load-buffer /yoloai/prompt.txt
    tmux paste-buffer -t main
    tmux send-keys -t main $SUBMIT_SEQUENCE
fi

# Block forever — container stops only on explicit docker stop
exec tmux wait-for yoloai-exit
'

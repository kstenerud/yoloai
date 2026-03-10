#!/bin/bash
set -euo pipefail

# ABOUTME: Post-boot setup script for Tart VMs. Runs via tart exec after VM boot.
# ABOUTME: Creates mount symlinks, injects secrets, launches tmux + agent.

# Arguments: path to the yoloai shared directory inside the VM
SHARED_DIR="${1:?usage: setup.sh <shared-dir>}"
export YOLOAI_DIR="$SHARED_DIR"

# Capture all setup output to log.txt
exec > >(tee -a "$YOLOAI_DIR/log.txt") 2>&1

CONFIG="$YOLOAI_DIR/runtime-config.json"
DEBUG=$(jq -r '.debug // false' "$CONFIG")
debug_log() { [ "$DEBUG" = "true" ] && echo "[debug] $*" || true; }

debug_log "setup starting (shared_dir=$YOLOAI_DIR)"

# --- Create symlinks for VirtioFS mounts ---
debug_log "creating VirtioFS mount symlinks"
# Mount map is a JSON object: { "/expected/path": "/Volumes/My Shared Files/name", ... }
MOUNT_MAP=$(jq -r '.mount_map // empty' "$CONFIG")
if [ -n "$MOUNT_MAP" ]; then
    echo "$MOUNT_MAP" | jq -r 'to_entries[] | "\(.key)\t\(.value)"' | while IFS=$'\t' read -r target source; do
        parent=$(dirname "$target")
        sudo mkdir -p "$parent"
        # Remove existing symlink or empty directory
        if [ -L "$target" ]; then
            sudo rm -f "$target"
        elif [ -d "$target" ] && [ -z "$(ls -A "$target" 2>/dev/null)" ]; then
            sudo rmdir "$target" 2>/dev/null || true
        fi
        sudo ln -sf "$source" "$target"
    done
fi

# --- Read secrets and export as env vars ---
debug_log "reading secrets"
SECRETS_DIR="$YOLOAI_DIR/secrets"
if [ -d "$SECRETS_DIR" ]; then
    for secret in "$SECRETS_DIR"/*; do
        [ -f "$secret" ] || continue
        varname=$(basename "$secret")
        export "$varname"="$(cat "$secret")"
    done
fi

# --- Read agent config ---
debug_log "reading agent config"
AGENT_COMMAND=$(jq -r '.agent_command' "$CONFIG")
STARTUP_DELAY=$(jq -r '.startup_delay' "$CONFIG")
READY_PATTERN=$(jq -r '.ready_pattern' "$CONFIG")
SUBMIT_SEQUENCE=$(jq -r '.submit_sequence' "$CONFIG")
TMUX_CONF=$(jq -r '.tmux_conf' "$CONFIG")
SANDBOX_NAME=$(jq -r ".sandbox_name // \"sandbox\"" "$CONFIG")
WORKING_DIR=$(jq -r '.working_dir' "$CONFIG")
set_title() { tmux rename-window -t main "$1" 2>/dev/null || true; }

# --- Start tmux session ---
debug_log "starting tmux session (tmux_conf=$TMUX_CONF)"
cd "$WORKING_DIR"

TMUX_CONF_FILE="$YOLOAI_DIR/tmux/tmux.conf"

TMUX_ARGS=()
case "$TMUX_CONF" in
    default+host)
        TMUX_ARGS=(-f "$TMUX_CONF_FILE")
        ;;
    default)
        TMUX_ARGS=(-f "$TMUX_CONF_FILE")
        ;;
    host)
        if [ -f "$HOME/.tmux.conf" ]; then
            TMUX_ARGS=(-f "$HOME/.tmux.conf")
        fi
        ;;
esac

tmux "${TMUX_ARGS[@]}" new-session -d -s main -x 200 -y 50

if [ "$TMUX_CONF" = "default+host" ] && [ -f "$HOME/.tmux.conf" ]; then
    tmux source-file "$HOME/.tmux.conf"
fi

tmux set-option -t main remain-on-exit on
tmux pipe-pane -t main "cat >> $YOLOAI_DIR/log.txt"

# --- Launch agent inside tmux ---
debug_log "launching agent: $AGENT_COMMAND"
tmux send-keys -t main "cd $WORKING_DIR && exec $AGENT_COMMAND" Enter

# --- Monitor for agent exit ---
(
    while [ "$(tmux list-panes -t main -F '#{pane_dead}' 2>/dev/null)" != "1" ]; do
        sleep 1
    done
    tmux list-clients -t main -F '#{client_name}' 2>/dev/null | while read -r c; do
        tmux detach-client -t "$c" 2>/dev/null || true
    done
) &

# --- Wait for agent ready, auto-accept trust/confirmation prompts ---
# Always run this loop so interactive "Enter to confirm" prompts (workspace trust,
# permissions mode) are handled even when no prompt file is provided.
debug_log "waiting for agent ready (pattern=$READY_PATTERN)"
if [ -n "$READY_PATTERN" ] && [ "$READY_PATTERN" != "null" ]; then
    MAX_WAIT=60
    WAITED=0
    while [ $WAITED -lt $MAX_WAIT ]; do
        PANE=$(tmux capture-pane -t main -p 2>/dev/null || true)
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
    # Wait for screen to stabilize
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
    sleep "$STARTUP_DELAY"
fi

# --- Deliver prompt if present ---
debug_log "checking for prompt file"
PROMPT_FILE="$YOLOAI_DIR/prompt.txt"
if [ -f "$PROMPT_FILE" ]; then
    debug_log "delivering prompt"
    tmux load-buffer "$PROMPT_FILE"
    tmux paste-buffer -t main
    sleep 0.5
    for key in $SUBMIT_SEQUENCE; do
        tmux send-keys -t main "$key"
        sleep 0.2
    done
fi

# --- Status monitor ---
STATUS_FILE="$YOLOAI_DIR/agent-status.json"
write_status() { printf '{"status":"%s","exit_code":%s,"timestamp":%d}\n' "$1" "$2" "$(date +%s)" > "$STATUS_FILE"; }
# If no prompt was delivered, the agent is waiting for input — start as idle.
if [ -f "$PROMPT_FILE" ]; then
    write_status active null
    set_title "$SANDBOX_NAME"
else
    write_status idle null
    set_title "> $SANDBOX_NAME"
fi
# Launch Python status monitor. Tart VMs receive the script via shared dir.
MONITOR_SCRIPT="$YOLOAI_DIR/bin/status-monitor.py"
if ! command -v python3 >/dev/null 2>&1; then
    echo "ERROR: Python 3 required for status monitoring. Install Xcode Command Line Tools: xcode-select --install" >&2
fi
python3 "$MONITOR_SCRIPT" "$CONFIG" "$STATUS_FILE" &

debug_log "setup complete, blocking on tmux wait"

# Block — VM stops only on explicit tart stop
exec tmux wait-for yoloai-exit

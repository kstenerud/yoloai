#!/bin/bash
set -euo pipefail

# ABOUTME: Post-boot setup script for Tart VMs. Runs via tart exec after VM boot.
# ABOUTME: Creates mount symlinks, injects secrets, launches tmux + agent.

# Arguments: path to the yoloai shared directory inside the VM
SHARED_DIR="${1:?usage: setup.sh <shared-dir>}"

# Capture all setup output to log.txt
exec > >(tee -a "$SHARED_DIR/log.txt") 2>&1

CONFIG="$SHARED_DIR/config.json"
DEBUG=$(jq -r '.debug // false' "$CONFIG")
debug_log() { [ "$DEBUG" = "true" ] && echo "[debug] $*" || true; }

debug_log "setup starting (shared_dir=$SHARED_DIR)"

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
SECRETS_DIR="$SHARED_DIR/secrets"
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
WORKING_DIR=$(jq -r '.working_dir' "$CONFIG")

# --- Start tmux session ---
debug_log "starting tmux session (tmux_conf=$TMUX_CONF)"
cd "$WORKING_DIR"

TMUX_ARGS=()
case "$TMUX_CONF" in
    default+host)
        TMUX_ARGS=(-f "$SHARED_DIR/tmux.conf")
        ;;
    default)
        TMUX_ARGS=(-f "$SHARED_DIR/tmux.conf")
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
tmux pipe-pane -t main "cat >> $SHARED_DIR/log.txt"

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
PROMPT_FILE="$SHARED_DIR/prompt.txt"
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

debug_log "setup complete, blocking on tmux wait"

# Block — VM stops only on explicit tart stop
exec tmux wait-for yoloai-exit

#!/bin/bash
set -euo pipefail

# ABOUTME: Entrypoint for seatbelt sandboxes. Runs under sandbox-exec on macOS.
# ABOUTME: Sets up HOME, secrets, tmux (per-sandbox socket), launches agent.

# Arguments: path to the sandbox directory
SANDBOX_DIR="${1:?usage: entrypoint.sh <sandbox-dir>}"

# Capture all entrypoint output to log.txt (plain redirect — no tee needed
# since seatbelt has no "docker logs" equivalent to preserve, and process
# substitution >(tee ...) is blocked by sandbox-exec's /dev/fd restrictions)
exec >>"$SANDBOX_DIR/log.txt" 2>&1

CONFIG="$SANDBOX_DIR/config.json"
TMUX_SOCK="$SANDBOX_DIR/tmux.sock"
DEBUG=$(jq -r '.debug // false' "$CONFIG")
debug_log() { [ "$DEBUG" = "true" ] && echo "[debug] $*" || true; }

debug_log "entrypoint starting (sandbox=$SANDBOX_DIR)"

# --- Set up HOME redirection ---
debug_log "setting up HOME redirection"
export HOME="$SANDBOX_DIR/home"
mkdir -p "$HOME"

# Symlink agent state dir (e.g. .claude, .gemini) to agent-state
STATE_DIR_NAME=$(jq -r '.state_dir_name // empty' "$CONFIG")
if [ -n "$STATE_DIR_NAME" ] && [ ! -L "$HOME/$STATE_DIR_NAME" ]; then
    ln -sf "$SANDBOX_DIR/agent-state" "$HOME/$STATE_DIR_NAME"
fi

# Symlink home-seed files (e.g. .claude.json) into HOME
HOME_SEED="$SANDBOX_DIR/home-seed"
if [ -d "$HOME_SEED" ]; then
    for f in "$HOME_SEED"/*  "$HOME_SEED"/.*; do
        [ -e "$f" ] || continue
        name=$(basename "$f")
        [ "$name" = "." ] || [ "$name" = ".." ] && continue
        if [ ! -e "$HOME/$name" ]; then
            ln -sf "$f" "$HOME/$name"
        fi
    done
fi

# --- Read secrets and export as env vars ---
debug_log "reading secrets"
SECRETS_DIR="$SANDBOX_DIR/secrets"
if [ -d "$SECRETS_DIR" ]; then
    for secret in "$SECRETS_DIR"/*; do
        [ -f "$secret" ] || continue
        varname=$(basename "$secret")
        export "$varname"="$(cat "$secret")"
    done
fi

# Suppress browser-open attempts inside the sandbox (agents may try to open
# documentation URLs). Only set if not already provided via secrets/env.
export BROWSER="${BROWSER:-true}"

# --- Read agent config ---
debug_log "reading agent config"
AGENT_COMMAND=$(jq -r '.agent_command' "$CONFIG")
STARTUP_DELAY=$(jq -r '.startup_delay' "$CONFIG")
READY_PATTERN=$(jq -r '.ready_pattern' "$CONFIG")
SUBMIT_SEQUENCE=$(jq -r '.submit_sequence' "$CONFIG")
TMUX_CONF=$(jq -r '.tmux_conf' "$CONFIG")
WORKING_DIR=$(jq -r '.working_dir' "$CONFIG")

# --- Start tmux session (per-sandbox socket) ---
debug_log "starting tmux session (tmux_conf=$TMUX_CONF)"
cd "$WORKING_DIR"

TMUX_ARGS=(-S "$TMUX_SOCK")
case "$TMUX_CONF" in
    default+host)
        TMUX_ARGS+=(-f "$SANDBOX_DIR/tmux.conf")
        ;;
    default)
        TMUX_ARGS+=(-f "$SANDBOX_DIR/tmux.conf")
        ;;
    host)
        if [ -f "$HOME/.tmux.conf" ]; then
            TMUX_ARGS+=(-f "$HOME/.tmux.conf")
        fi
        ;;
esac

tmux "${TMUX_ARGS[@]}" new-session -d -s main -x 200 -y 50

if [ "$TMUX_CONF" = "default+host" ] && [ -f "$HOME/.tmux.conf" ]; then
    tmux -S "$TMUX_SOCK" source-file "$HOME/.tmux.conf"
fi

tmux -S "$TMUX_SOCK" set-option -t main remain-on-exit on
tmux -S "$TMUX_SOCK" pipe-pane -t main "cat >> $SANDBOX_DIR/log.txt"

# --- Launch agent inside tmux ---
debug_log "launching agent: $AGENT_COMMAND"
tmux -S "$TMUX_SOCK" send-keys -t main "cd $WORKING_DIR && exec $AGENT_COMMAND" Enter

# --- Monitor for agent exit ---
(
    while [ "$(tmux -S "$TMUX_SOCK" list-panes -t main -F '#{pane_dead}' 2>/dev/null)" != "1" ]; do
        sleep 1
    done
    tmux -S "$TMUX_SOCK" list-clients -t main -F '#{client_name}' 2>/dev/null | while read -r c; do
        tmux -S "$TMUX_SOCK" detach-client -t "$c" 2>/dev/null || true
    done
) &

# --- Wait for agent ready, auto-accept trust/confirmation prompts ---
debug_log "waiting for agent ready (pattern=$READY_PATTERN)"
if [ -n "$READY_PATTERN" ] && [ "$READY_PATTERN" != "null" ]; then
    MAX_WAIT=60
    WAITED=0
    while [ $WAITED -lt $MAX_WAIT ]; do
        PANE=$(tmux -S "$TMUX_SOCK" capture-pane -t main -p 2>/dev/null || true)
        if echo "$PANE" | grep -qF "Enter to confirm"; then
            tmux -S "$TMUX_SOCK" send-keys -t main Enter
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
        CURR=$(tmux -S "$TMUX_SOCK" capture-pane -t main -p 2>/dev/null || true)
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
PROMPT_FILE="$SANDBOX_DIR/prompt.txt"
if [ -f "$PROMPT_FILE" ]; then
    debug_log "delivering prompt"
    tmux -S "$TMUX_SOCK" load-buffer "$PROMPT_FILE"
    tmux -S "$TMUX_SOCK" paste-buffer -t main
    sleep 0.5
    for key in $SUBMIT_SEQUENCE; do
        tmux -S "$TMUX_SOCK" send-keys -t main "$key"
        sleep 0.2
    done
fi

debug_log "entrypoint setup complete, blocking on tmux wait"

# Block — process stops only on explicit kill
exec tmux -S "$TMUX_SOCK" wait-for yoloai-exit

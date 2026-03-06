#!/bin/bash
set -euo pipefail

# Capture all entrypoint output to log.txt (preserves docker logs via tee)
exec > >(tee -a /yoloai/log.txt) 2>&1

# --- Run as root ---

# Read config (only UID/GID needed in root context; agent config read by inner bash)
CONFIG="/yoloai/config.json"
HOST_UID=$(jq -r '.host_uid' "$CONFIG")
HOST_GID=$(jq -r '.host_gid' "$CONFIG")
DEBUG=$(jq -r '.debug // false' "$CONFIG")
debug_log() { [ "$DEBUG" = "true" ] && echo "[debug] $*" || true; }

debug_log "entrypoint starting"

# Remap UID/GID to match host user
CURRENT_UID=$(id -u yoloai)
CURRENT_GID=$(id -g yoloai)

debug_log "remapping UID=$CURRENT_UID->$HOST_UID GID=$CURRENT_GID->$HOST_GID"
if [ "$CURRENT_GID" != "$HOST_GID" ]; then
    groupmod -g "$HOST_GID" yoloai 2>/dev/null || true
fi
if [ "$CURRENT_UID" != "$HOST_UID" ]; then
    usermod -u "$HOST_UID" yoloai 2>/dev/null || true
fi

# Fix ownership on container-managed directories.
# Some files may be bind-mounted read-only; chown on those is expected to fail.
debug_log "fixing ownership on container-managed directories"
chown -R yoloai:yoloai /home/yoloai 2>/dev/null || true
chown yoloai:yoloai /yoloai
for f in /yoloai/*; do
    chown yoloai:yoloai "$f" 2>/dev/null || true
done

# Read secrets and export as env vars
debug_log "reading secrets"
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

# Tell agents they're inside a sandbox (e.g. Claude Code uses this to allow
# --dangerously-skip-permissions even when running as root).
export IS_SANDBOX=1

# --- Network isolation (iptables + ipset) ---
NETWORK_ISOLATED=$(jq -r '.network_isolated // false' "$CONFIG")
if [ "$NETWORK_ISOLATED" = "true" ]; then
    debug_log "setting up network isolation with iptables+ipset"

    # Create ipset for allowed IPs
    ipset create allowed-domains hash:net 2>/dev/null || ipset flush allowed-domains

    # Resolve each allowed domain and add IPs to ipset
    ALLOWED_DOMAINS=$(jq -r '.allowed_domains // [] | .[]' "$CONFIG")
    for domain in $ALLOWED_DOMAINS; do
        ips=$(dig +short A "$domain" 2>/dev/null || true)
        for ip in $ips; do
            # Skip CNAME lines (contain dots but are not IPs)
            if echo "$ip" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$'; then
                ipset add allowed-domains "$ip" 2>/dev/null || true
                debug_log "allow $domain -> $ip"
            fi
        done
    done

    # Flush existing rules
    iptables -F OUTPUT 2>/dev/null || true

    # Allow loopback
    iptables -A OUTPUT -o lo -j ACCEPT

    # Allow established/related connections
    iptables -A OUTPUT -m state --state ESTABLISHED,RELATED -j ACCEPT

    # Allow DNS to configured nameservers (UDP+TCP).
    # On user-defined networks Docker uses 127.0.0.11; on the default bridge
    # it uses the host's DNS (e.g. 192.168.65.7 on Docker Desktop).
    # Reading /etc/resolv.conf covers both cases.
    for ns in $(grep '^nameserver' /etc/resolv.conf | awk '{print $2}'); do
        iptables -A OUTPUT -d "$ns" -p udp --dport 53 -j ACCEPT
        iptables -A OUTPUT -d "$ns" -p tcp --dport 53 -j ACCEPT
        debug_log "allow DNS to $ns"
    done

    # Allow traffic to allowlisted IPs
    iptables -A OUTPUT -m set --match-set allowed-domains dst -j ACCEPT

    # Reject everything else (REJECT gives immediate feedback, unlike DROP)
    iptables -A OUTPUT -j REJECT --reject-with icmp-port-unreachable

    debug_log "network isolation rules applied"
fi

# --- Setup commands ---
SETUP_COUNT=$(jq ".setup_commands // [] | length" "$CONFIG")
if [ "$SETUP_COUNT" -gt 0 ]; then
    debug_log "running $SETUP_COUNT setup command(s)"
    for i in $(seq 0 $((SETUP_COUNT - 1))); do
        SETUP_CMD=$(jq -r ".setup_commands[$i]" "$CONFIG")
        debug_log "setup[$i]: $SETUP_CMD"
        eval "$SETUP_CMD"
    done
fi

# --- Overlay mounts ---
OVERLAY_COUNT=$(jq ".overlay_mounts // [] | length" "$CONFIG")
if [ "$OVERLAY_COUNT" -gt 0 ]; then
    debug_log "setting up $OVERLAY_COUNT overlay mount(s)"
    for i in $(seq 0 $((OVERLAY_COUNT - 1))); do
        LOWER=$(jq -r ".overlay_mounts[$i].lower" "$CONFIG")
        UPPER=$(jq -r ".overlay_mounts[$i].upper" "$CONFIG")
        WORK=$(jq -r ".overlay_mounts[$i].work" "$CONFIG")
        MERGED=$(jq -r ".overlay_mounts[$i].merged" "$CONFIG")
        mkdir -p "$MERGED"
        mount -t overlay overlay -o "lowerdir=$LOWER,upperdir=$UPPER,workdir=$WORK" "$MERGED"
        chown yoloai:yoloai "$MERGED"
        debug_log "overlay: $MERGED (lower=$LOWER)"
    done
fi

debug_log "dropping privileges to yoloai"

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
HOOK_IDLE=$(jq -r ".hook_idle // false" "$CONFIG")
DEBUG=$(jq -r ".debug // false" "$CONFIG")
debug_log() { [ "$DEBUG" = "true" ] && echo "[debug] $*" || true; }

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
    while [ "$(tmux list-panes -t main -F "#{pane_dead}" 2>/dev/null)" != "1" ]; do
        sleep 1
    done
    tmux list-clients -t main -F "#{client_name}" 2>/dev/null | while read -r c; do
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
write_status running null
(
    while true; do
        PANE_DEAD=$(tmux list-panes -t main -F "#{pane_dead}" 2>/dev/null || echo "error")
        if [ "$PANE_DEAD" = "1" ]; then
            EXIT_CODE=$(tmux list-panes -t main -F "#{pane_dead_status}" 2>/dev/null || echo "1")
            write_status done "$EXIT_CODE"
            break
        elif [ "$PANE_DEAD" = "error" ]; then
            write_status done 1
            break
        fi
        # When hook_idle is true, the agent's own hooks write idle status
        # to status.json — no need to poll tmux. The monitor only tracks
        # running (initial) and done (pane death).
        if [ "$HOOK_IDLE" != "true" ]; then
            NEW_STATUS="running"
            if [ -n "$READY_PATTERN" ] && [ "$READY_PATTERN" != "null" ]; then
                PANE_CONTENT=$(tmux capture-pane -t main -p 2>/dev/null || true)
                if echo "$PANE_CONTENT" | grep -qF "$READY_PATTERN"; then
                    NEW_STATUS="idle"
                fi
            fi
            write_status "$NEW_STATUS" null
        fi
        sleep 2
    done
) &

debug_log "entrypoint setup complete, blocking on tmux wait"

# Block forever — container stops only on explicit docker stop
exec tmux wait-for yoloai-exit
'

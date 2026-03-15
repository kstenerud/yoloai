#!/bin/bash
set -euo pipefail

# Capture all entrypoint output to log.txt (preserves docker logs via tee)
exec > >(tee -a "$YOLOAI_DIR/log.txt") 2>&1

# --- Run as root ---

# Read config (only UID/GID needed in root context; agent config read by inner bash)
CONFIG="$YOLOAI_DIR/runtime-config.json"
HOST_UID=$(jq -r '.host_uid' "$CONFIG")
HOST_GID=$(jq -r '.host_gid' "$CONFIG")
DEBUG=$(jq -r '.debug // false' "$CONFIG")
debug_log() { [ "$DEBUG" = "true" ] && echo "[debug] $*" || true; }

debug_log "entrypoint starting"

# Check if running as root. With rootless Podman + --userns=keep-id, we run as
# the host user and can't perform UID remapping or chown operations.
RUNNING_AS_ROOT=false
if [ "$(id -u)" -eq 0 ]; then
    RUNNING_AS_ROOT=true
fi

# Remap UID/GID to match host user (only if running as root)
if [ "$RUNNING_AS_ROOT" = "true" ]; then
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
    chown yoloai:yoloai "$YOLOAI_DIR" 2>/dev/null || true
    for f in "$YOLOAI_DIR"/*; do
        chown yoloai:yoloai "$f" 2>/dev/null || true
    done
    # Also fix subdirectories
    for d in "$YOLOAI_DIR"/bin "$YOLOAI_DIR"/tmux; do
        [ -d "$d" ] && chown -R yoloai:yoloai "$d" 2>/dev/null || true
    done
else
    debug_log "running as non-root (uid=$(id -u)), skipping UID remapping and chown"
fi

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

        # Mount tmpfs for upper/work to avoid overlayfs-on-overlayfs issues.
        # With Podman's overlay storage driver, the container rootfs is itself overlayfs,
        # and overlayfs doesn't support being used as upperdir for another overlay mount.
        # Strategy: Create upper/work on tmpfs, use those paths directly in overlay mount.
        OVERLAY_TMPFS="/tmp/yoloai-overlay-$i"
        mkdir -p "$OVERLAY_TMPFS"
        mount -t tmpfs tmpfs "$OVERLAY_TMPFS"

        # Create upper/work subdirs in tmpfs
        mkdir -p "$OVERLAY_TMPFS/upper" "$OVERLAY_TMPFS/work" "$MERGED"
        debug_log "created tmpfs at $OVERLAY_TMPFS for overlay storage"

        # Use the tmpfs paths directly for upper/work in the overlay mount
        mount -t overlay overlay -o "lowerdir=$LOWER,upperdir=$OVERLAY_TMPFS/upper,workdir=$OVERLAY_TMPFS/work" "$MERGED"
        chown yoloai:yoloai "$MERGED"
        debug_log "overlay: $MERGED (lower=$LOWER, upper/work on tmpfs)"
    done
fi

if [ "$RUNNING_AS_ROOT" = "true" ]; then
    debug_log "dropping privileges to yoloai"
    # --- Drop privileges and run as yoloai ---
    exec gosu yoloai python3 "$YOLOAI_DIR/bin/sandbox-setup.py" docker
else
    debug_log "already running as non-root user, executing directly"
    # --- Already running as non-root (rootless Podman with --userns=keep-id) ---
    # Set HOME to yoloai home directory (WorkingDir overrides it otherwise)
    export HOME=/home/yoloai
    exec python3 "$YOLOAI_DIR/bin/sandbox-setup.py" docker
fi

#!/usr/bin/env bash
# ABOUTME: L2 — does host-side domain resolution break the allowlist on Linux the
# ABOUTME: way it does on macOS? Uses docker's shipped resolver substitution (host
# ABOUTME: stub -> public DNS), so the divergence is real rather than simulated.
set -uo pipefail

TABLE=yb_l2
CT=yb_l2_guest
SPLIT_NAME=yoloai.tail571a40.ts.net   # served by the LAN resolver, absent from public DNS
CTRL_NAME=example.com                 # resolves identically on both sides
say() { printf '\n=== %s ===\n' "$*"; }
cleanup() {
	sudo nft delete table inet $TABLE 2>/dev/null
	docker rm -f $CT >/dev/null 2>&1
}
trap cleanup EXIT
cleanup

say "resolver configuration on each side"
echo "host  : $(grep -E '^nameserver' /etc/resolv.conf | tr '\n' ' ')"
docker run --rm alpine sh -c 'echo "guest : $(grep -E "^nameserver" /etc/resolv.conf | tr "\n" " ")"'

say "how each side resolves the two names"
SPLIT_HOST=$(getent ahostsv4 $SPLIT_NAME | awk '{print $1; exit}')
CTRL_HOST=$(getent ahostsv4 $CTRL_NAME | awk '{print $1; exit}')
echo "host  $SPLIT_NAME -> ${SPLIT_HOST:-<unresolved>}"
echo "host  $CTRL_NAME  -> ${CTRL_HOST:-<unresolved>}"
SPLIT_GUEST=$(docker run --rm alpine getent ahostsv4 $SPLIT_NAME 2>/dev/null | awk '{print $1; exit}')
CTRL_GUEST=$(docker run --rm alpine getent ahostsv4 $CTRL_NAME 2>/dev/null | awk '{print $1; exit}')
echo "guest $SPLIT_NAME -> ${SPLIT_GUEST:-<unresolved>}"
echo "guest $CTRL_NAME  -> ${CTRL_GUEST:-<unresolved>}"

say "start the guest and install the allowlist the design would build"
docker run -d --name $CT alpine sleep 900 >/dev/null
G=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' $CT)
echo "guest address: $G"
echo "allowlist is built from the HOST's answers: $SPLIT_HOST, $CTRL_HOST"

sudo nft -f - <<NFT
table inet $TABLE {
	set allowed {
		type ipv4_addr
		elements = { $SPLIT_HOST, $CTRL_HOST }
	}
	chain c_forward {
		type filter hook forward priority -10; policy accept;
		ip saddr $G ct state established,related accept
		ip saddr $G udp dport 53 accept comment "guest must be able to resolve"
		ip saddr $G tcp dport 53 accept
		ip saddr $G ip daddr @allowed counter accept comment "allowlisted"
		ip saddr $G counter drop comment "denied"
	}
}
NFT
echo "ruleset loaded, rc=$?"

probe() { # name/addr, port, label
	docker exec $CT sh -c "nc -z -w 4 $1 $2 >/dev/null 2>&1" && echo "  $3: REACHABLE" || echo "  $3: blocked/unreachable"
}

say "POSITIVE CONTROL — the allowlist mechanism works at all"
probe "$CTRL_NAME" 80 "guest -> $CTRL_NAME:80 (by name, resolves the same both sides)"
probe "$CTRL_HOST" 80 "guest -> $CTRL_HOST:80 (same host, by address)"

say "NEGATIVE CONTROL — something not on the allowlist is actually denied"
probe "1.1.1.1" 80 "guest -> 1.1.1.1:80 (never allowlisted)"

say "DIRECTION 1 — can the guest reach the name it was allowlisted for?"
probe "$SPLIT_NAME" 22 "guest -> $SPLIT_NAME:22 (by name)"

say "DIRECTION 2 — what did the host's answer actually authorise?"
echo "  the host resolved $SPLIT_NAME to $SPLIT_HOST, which is this host's own LAN address"
probe "$SPLIT_HOST" 22 "guest -> $SPLIT_HOST:22 (the address now in the allowlist)"

say "counters"
sudo nft list table inet $TABLE

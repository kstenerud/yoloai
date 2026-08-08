#!/usr/bin/env bash
# ABOUTME: L8 — L5c/L5d concluded the host layer "structurally cannot" see traffic
# ABOUTME: between sandboxes on one bridge. br_netfilter is available but unloaded on
# ABOUTME: this host, so that claim may be a default rather than a limit. Tests it.
set -uo pipefail

say() { printf '\n=== %s ===\n' "$*"; }
LOADED_BY_US=0
cleanup() {
	sudo nft delete table inet yb_l8 2>/dev/null
	docker rm -f yb_l8_a yb_l8_b >/dev/null 2>&1
	docker network rm yb_l8_net >/dev/null 2>&1
	if [ "$LOADED_BY_US" = 1 ]; then
		sudo sysctl -qw net.bridge.bridge-nf-call-iptables=0 2>/dev/null
		sudo modprobe -r br_netfilter 2>/dev/null && echo "restored: br_netfilter unloaded"
	fi
}
trap cleanup EXIT
cleanup

docker network create --subnet 10.58.0.0/24 yb_l8_net >/dev/null
docker run -d --name yb_l8_a --network yb_l8_net alpine sleep 400 >/dev/null
docker run -d --name yb_l8_b --network yb_l8_net alpine sleep 400 >/dev/null
A=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' yb_l8_a)
B=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' yb_l8_b)
ALLOW=$(getent ahostsv4 example.com | awk '{print $1; exit}')
echo "A=$A  B=$B  allowlisted=$ALLOW"

sudo nft -f - <<NFT
table inet yb_l8 {
	set allowed { type ipv4_addr; elements = { $ALLOW } }
	chain c_forward {
		type filter hook forward priority -10; policy accept;
		ip saddr $A ct state established,related accept
		ip saddr $A udp dport 53 accept
		ip saddr $A ip daddr @allowed counter accept comment "allowed"
		ip saddr $A counter drop comment "denied"
	}
}
NFT

probe() {
	printf '  %-26s allowlisted=%-10s denied=%-10s sibling=%s\n' "$1" \
		"$(docker exec yb_l8_a nc -z -w 3 "$ALLOW" 80 >/dev/null 2>&1 && echo REACHABLE || echo blocked)" \
		"$(docker exec yb_l8_a nc -z -w 3 1.1.1.1 80 >/dev/null 2>&1 && echo REACHABLE || echo blocked)" \
		"$(docker exec yb_l8_a ping -c2 -W2 "$B" >/dev/null 2>&1 && echo REACHABLE || echo blocked)"
}

say "BEFORE — br_netfilter not loaded (this host's default, and L5c's condition)"
echo "  module loaded: $(lsmod | grep -qw br_netfilter && echo yes || echo no)"
probe "default"
SIB_BEFORE=$(sudo nft list table inet yb_l8 | grep 'denied' | grep -oP 'packets \K[0-9]+')
echo "  host 'denied' counter: $SIB_BEFORE"

say "LOAD br_netfilter and route bridged frames through netfilter"
sudo modprobe br_netfilter && LOADED_BY_US=1
sudo sysctl -qw net.bridge.bridge-nf-call-iptables=1
sudo sysctl -qw net.bridge.bridge-nf-call-ip6tables=1
echo "  module loaded: $(lsmod | grep -qw br_netfilter && echo yes || echo no)"
echo "  bridge-nf-call-iptables: $(sysctl -n net.bridge.bridge-nf-call-iptables)"
sleep 2

say "AFTER — same policy, same containers, nothing else changed"
probe "br_netfilter on"
SIB_AFTER=$(sudo nft list table inet yb_l8 | grep 'denied' | grep -oP 'packets \K[0-9]+')
echo "  host 'denied' counter: $SIB_AFTER (was $SIB_BEFORE)"
sudo nft list table inet yb_l8 | grep counter | sed 's/^\s*/  /'

say "verdict"
echo "If sibling traffic is blocked only in the second row, then L5c/L5d measured a"
echo "DEFAULT and not a structural limit: the host layer can be made to see"
echo "sandbox-to-sandbox traffic by loading a module yoloAI does not currently load."
echo "If it stays reachable in both rows, the original wording stands."

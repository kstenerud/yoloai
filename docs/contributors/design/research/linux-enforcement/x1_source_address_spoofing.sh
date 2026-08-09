#!/usr/bin/env bash
# ABOUTME: X1-Linux — the macOS pass found the pf slot pool defeasible: a guest that
# ABOUTME: changes its address leaves every rule keyed on the old one. Asks whether an
# ABOUTME: nftables allowlist has the same shape, and whether one bridge rule closes it.
set -uo pipefail

say() { printf '\n=== %s ===\n' "$*"; }
CT=yb_x1
cleanup() {
	sudo nft delete table inet yb_x1 2>/dev/null
	docker rm -f $CT >/dev/null 2>&1
}
trap cleanup EXIT
cleanup

ALLOW=8.8.8.8      # allowlisted, answers ICMP
DENY=1.1.1.1       # never allowlisted, answers ICMP
SPOOF=172.17.99.99 # inside docker0's subnet, so masquerade still applies; held by nobody

say "sandbox with CAP_NET_ADMIN — the in-container firewall path grants it"
echo "(granted at launch.go:1040, guarded by !sidecarFirewall; the sidecar path withholds it)"
docker run -d --name $CT --cap-add NET_ADMIN alpine sleep 400 >/dev/null
G=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' $CT)
echo "guest address: $G   spoof target: $SPOOF"

say "host policy, keyed on the guest's address — the shape the design uses"
sudo nft -f - <<NFT
table inet yb_x1 {
	set allowed { type ipv4_addr; elements = { $ALLOW } }
	chain c_forward {
		type filter hook forward priority -10; policy accept;
		ip saddr $G ct state established,related accept
		ip saddr $G ip daddr @allowed counter accept comment "allowlisted"
		ip saddr $G counter drop comment "denied"
	}
}
NFT

p() { docker exec $CT ping -c2 -W2 ${2:+-I "$2"} "$1" >/dev/null 2>&1 && echo REACHABLE || echo blocked; }

say "controls — the policy must actually work before any bypass means anything"
echo "  guest -> $ALLOW (allowlisted): $(p $ALLOW)"
echo "  guest -> $DENY  (denied)     : $(p $DENY)"

say "THE BYPASS — the agent takes an address the policy does not name"
docker exec $CT ip addr add $SPOOF/16 dev eth0 2>&1 | sed 's/^/  /'
echo "  addresses the guest now holds:"
docker exec $CT ip -4 -br addr show eth0 | sed 's/^/    /'
echo "  guest -> $DENY sourced from $SPOOF: $(p $DENY $SPOOF)"
echo "  host counters (the deny rule never sees these packets):"
sudo nft list table inet yb_x1 | grep counter | sed 's/^\s*/    /'

say "THE FIX — one bridge-scoped rule, naming no address"
sudo nft add rule inet yb_x1 c_forward iifname '"docker0"' counter drop comment '"bridge-default-deny"'
echo "  rule added: iifname docker0 drop, below the per-sandbox accepts"
echo "  guest -> $DENY sourced from $SPOOF: $(p $DENY $SPOOF)   <- must be blocked now"
echo "  guest -> $DENY from its own address: $(p $DENY)         <- still blocked"
echo "  guest -> $ALLOW from its own address: $(p $ALLOW)       <- must STILL work"
echo "  host counters:"
sudo nft list table inet yb_x1 | grep counter | sed 's/^\s*/    /'

say "does the fix survive the agent picking an address outside the subnet too?"
docker exec $CT ip addr add 10.200.200.200/24 dev eth0 2>&1 | sed 's/^/  /'
echo "  guest -> $DENY sourced from 10.200.200.200: $(p $DENY 10.200.200.200)"

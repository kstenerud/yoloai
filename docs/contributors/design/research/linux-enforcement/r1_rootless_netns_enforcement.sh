#!/usr/bin/env bash
# ABOUTME: R1 — rootless podman is unreachable from the host netns (L4b/L4c), but it
# ABOUTME: has a netns of its own holding the bridge. Asks whether the address-keyed
# ABOUTME: design works there instead: does a table in the rootless netns see and block?
set -uo pipefail

say() { printf '\n=== %s ===\n' "$*"; }
NET=yb_rnet
cleanup() {
	podman unshare --rootless-netns nft delete table inet yb_rootless 2>/dev/null
	podman rm -f yb_r1 yb_r2 >/dev/null 2>&1
	podman network rm $NET >/dev/null 2>&1
}
trap cleanup EXIT
cleanup

podman network create $NET --subnet 10.90.0.0/24 >/dev/null 2>&1
podman run -d --name yb_r1 --network $NET alpine sleep 400 >/dev/null
podman run -d --name yb_r2 --network $NET alpine sleep 400 >/dev/null
A=$(podman inspect yb_r1 --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}')
B=$(podman inspect yb_r2 --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}')
echo "sandbox A=$A   sandbox B=$B (B is the untouched control)"

say "topology inside the rootless netns"
podman unshare --rootless-netns ip -4 -o addr show | sed 's/^/  /'
echo "  default route: $(podman unshare --rootless-netns ip -4 route | grep default)"
echo "  READ: bridge -> tap0 is a route, so container egress is FORWARDED here,"
echo "        which is the hook the whole address-keyed design needs."

ALLOW=$(getent ahostsv4 example.com | awk '{print $1; exit}')
DENY=1.1.1.1
echo "allowlisted=$ALLOW  denied=$DENY"

p() { podman exec "$1" nc -z -w 4 "$2" 80 >/dev/null 2>&1 && echo REACHABLE || echo blocked; }

say "BASELINE — before any policy, everything must work"
echo "  A -> allowlisted: $(p yb_r1 "$ALLOW")"
echo "  A -> denied     : $(p yb_r1 $DENY)"
echo "  B -> denied     : $(p yb_r2 $DENY)"

say "install the design's own shape INSIDE the rootless netns"
podman unshare --rootless-netns nft -f - <<NFT
table inet yb_rootless {
	set allowed { type ipv4_addr; elements = { $ALLOW } }
	chain c_forward {
		type filter hook forward priority -10; policy accept;
		ip saddr $A ct state established,related accept
		ip saddr $A udp dport 53 accept
		ip saddr $A ip daddr @allowed counter accept comment "allowlisted"
		ip saddr $A counter drop comment "denied"
	}
}
NFT
echo "  loaded rc=$?"

say "TEST — does it bite, and does it leave the other sandbox alone?"
echo "  A -> allowlisted: $(p yb_r1 "$ALLOW")   <- must still work"
echo "  A -> denied     : $(p yb_r1 $DENY)   <- must be blocked"
echo "  B -> denied     : $(p yb_r2 $DENY)   <- CONTROL: must be unaffected"

say "counters — which rule decided, and did the chain see the packets at all?"
podman unshare --rootless-netns nft list table inet yb_rootless | grep counter | sed 's/^\s*/  /'

say "X1 HERE — can A escape by taking an address the policy does not name?"
podman exec --user root yb_r1 ip addr add 10.90.0.99/24 dev eth0 2>&1 | sed 's/^/  /'
echo "  A -> denied sourced from 10.90.0.99: $(podman exec yb_r1 ping -c2 -W2 -I 10.90.0.99 $DENY >/dev/null 2>&1 && echo REACHABLE || echo blocked)"

say "and does the bridge-scoped default-deny (rule 0b) close it here too?"
BR=$(podman unshare --rootless-netns ip -4 -o addr show | awk '/10\.90\.0\.1/{print $2}')
echo "  bridge interface: $BR"
podman unshare --rootless-netns nft add rule inet yb_rootless c_forward iifname "\"$BR\"" counter drop comment '"bridge-default-deny"'
echo "  A -> denied from 10.90.0.99: $(podman exec yb_r1 ping -c2 -W2 -I 10.90.0.99 $DENY >/dev/null 2>&1 && echo REACHABLE || echo blocked)   <- must be blocked"
echo "  A -> allowlisted from its own address: $(p yb_r1 "$ALLOW")   <- must STILL work"
echo "  B -> denied: $(p yb_r2 $DENY)   <- B is now inside the deny scope too; expected blocked"
podman unshare --rootless-netns nft list table inet yb_rootless | grep counter | sed 's/^\s*/  /'

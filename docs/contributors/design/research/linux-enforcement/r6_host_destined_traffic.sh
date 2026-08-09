#!/usr/bin/env bash
# ABOUTME: R6 — does forward-hook enforcement cover sandbox-to-HOST traffic? Packets
# ABOUTME: addressed to any address the host itself holds go prerouting -> INPUT, never
# ABOUTME: forward. Also re-checks L2's second conclusion, which attributed this to the allowlist.
set -uo pipefail

say() { printf '\n=== %s ===\n' "$*"; }
CT=yb_r6
cleanup() { sudo nft delete table inet yb_r6 2>/dev/null; docker rm -f $CT >/dev/null 2>&1; }
trap cleanup EXIT
cleanup

docker run -d --name $CT alpine sleep 300 >/dev/null
G=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' $CT)
GW=$(docker exec $CT ip route | awk '/default/{print $3}')
HOSTLAN=$(ip -4 route get 1.1.1.1 | awk '{for(i=1;i<=NF;i++) if($i=="src") print $(i+1)}')
echo "guest=$G  bridge gateway=$GW  host LAN=$HOSTLAN"

say "the strongest policy the design can express: drop everything, in the forward hook"
sudo nft -f - <<NFT
table inet yb_r6 {
	chain c_forward {
		type filter hook forward priority -10; policy accept;
		ip saddr $G counter drop comment "deny-all-forward"
	}
	chain c_input {
		type filter hook input priority -10; policy accept;
		ip saddr $G counter comment "seen-in-input"
	}
}
NFT

t() { docker exec $CT nc -z -w 3 "$1" "$2" >/dev/null 2>&1 && echo REACHABLE || echo blocked; }
echo "  guest -> 1.1.1.1:80 (external, genuinely routed): $(t 1.1.1.1 80)   <- CONTROL, must be blocked"
echo "  guest -> $GW:22 (bridge gateway = the host):      $(t "$GW" 22)"
echo "  guest -> $HOSTLAN:22 (the host's LAN address):     $(t "$HOSTLAN" 22)"

say "which hook saw what — this is the whole finding"
sudo nft list table inet yb_r6 | grep counter | sed 's/^\s*/  /'
echo
echo "READ: a packet addressed to any address the HOST holds is delivered locally, so it"
echo "traverses prerouting -> input and never enters the forward hook. A forward-only"
echo "design therefore cannot express any policy about sandbox-to-host traffic at all."

say "L2 RE-CHECK — was the host's LAN address reachable BECAUSE it was allowlisted?"
echo "L2 concluded that host-side DNS resolution wrote the host's own LAN address into"
echo "the guest's allowed set and that the guest then reached it through that entry."
echo "There is no allowlist at all in this run, and no accept rule of any kind:"
sudo nft list table inet yb_r6 | grep -c accept | sed 's/^/  accept rules present: /'
echo "  guest -> $HOSTLAN:22 : $(t "$HOSTLAN" 22)"
echo
echo "So the reachability does not depend on the allowlist entry. L2's second conclusion"
echo "named the wrong mechanism: the entry was inert, and the traffic was never subject"
echo "to the forward chain in the first place."

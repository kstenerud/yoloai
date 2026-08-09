#!/usr/bin/env bash
# ABOUTME: R4 — the X1 spoofing question inside the rootless netns, done properly.
# ABOUTME: R1-R3 tested it with ping while controlling with TCP; ICMP does not traverse
# ABOUTME: slirp4netns, so every "blocked" was free. Same protocol for test and control here.
set -uo pipefail

say() { printf '\n=== %s ===\n' "$*"; }
NET=yb_rnet4
CT=yb_r4
SPOOF=10.94.0.99
BR=""
cleanup() {
	podman unshare --rootless-netns nft delete table inet yb_rl4 2>/dev/null
	podman rm -f $CT >/dev/null 2>&1
	podman network rm $NET >/dev/null 2>&1
}
trap cleanup EXIT
cleanup

podman network create $NET --subnet 10.94.0.0/24 >/dev/null 2>&1
podman run -d --name $CT --cap-add NET_ADMIN --network $NET alpine sleep 400 >/dev/null
A=$(podman inspect $CT --format "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}")
BR=$(podman network inspect $NET --format "{{.NetworkInterface}}")

say "install curl BEFORE any policy exists — it needs egress to arrive"
podman exec $CT apk add --no-cache curl >/dev/null 2>&1
podman exec $CT sh -c 'command -v curl >/dev/null' || { echo "ABORT: curl missing; every result below would be free"; exit 1; }
echo "  curl present: $(podman exec $CT curl --version 2>/dev/null | head -1)"

ALLOW=$(getent ahostsv4 example.com | awk '{print $1; exit}')
DENY=1.1.1.1
echo "A=$A  bridge=$BR  allowlisted=$ALLOW  denied=$DENY  spoof=$SPOOF"
# R4 run 1 hardcoded "podman1" while this network sat on another bridge, so rule 0b
# named an interface no packet ever arrived on and its counter stayed at 0.
[ -n "$BR" ] || { echo "ABORT: bridge name not resolved"; exit 1; }

# every probe below is TCP, from a named source address — test and control alike
get() { podman exec $CT curl -s -m 5 -o /dev/null ${2:+--interface "$2"} "http://$1/" >/dev/null 2>&1 && echo REACHABLE || echo blocked; }

say "BASELINE — no policy anywhere"
echo "  own address -> denied     : $(get $DENY)"
echo "  own address -> allowlisted: $(get "$ALLOW")"

say "PROTOCOL CHECK — the thing that invalidated R1-R3"
echo "  ICMP from own address -> denied: $(podman exec $CT ping -c2 -W3 $DENY >/dev/null 2>&1 && echo REACHABLE || echo 'blocked — ICMP never traverses slirp4netns, so it can never be a valid probe here')"

say "install the yoloAI policy inside the rootless netns, keyed on A"
podman unshare --rootless-netns nft -f - <<NFT
table inet yb_rl4 {
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
echo "  controls with the policy loaded:"
echo "    own address -> denied     : $(get $DENY)        <- must be blocked"
echo "    own address -> allowlisted: $(get "$ALLOW")   <- must still work"

say "THE TEST — agent takes an address the policy does not name, same protocol"
podman exec --user root $CT ip addr add $SPOOF/24 dev eth0 2>&1 | sed 's/^/  /'
podman exec $CT ip -4 addr show eth0 | grep inet | sed 's/^/    /'
podman exec $CT ip -4 addr show eth0 | grep -q "$SPOOF" || { echo "  ABORT: spoof address not held"; exit 1; }
echo "  spoofed $SPOOF -> denied: $(get $DENY "$SPOOF")"
podman unshare --rootless-netns nft list table inet yb_rl4 | grep counter | sed 's/^\s*/    /'

say "RULE 0b — one bridge-scoped deny, naming no address"
podman unshare --rootless-netns nft add rule inet yb_rl4 c_forward iifname "\"$BR\"" counter drop comment '"bridge-default-deny"'
echo "  spoofed $SPOOF -> denied     : $(get $DENY "$SPOOF")   <- must be blocked"
echo "  own address  -> allowlisted : $(get "$ALLOW")   <- must STILL work"
echo "  own address  -> denied      : $(get $DENY)   <- still blocked"
podman unshare --rootless-netns nft list table inet yb_rl4 | grep counter | sed 's/^\s*/    /'

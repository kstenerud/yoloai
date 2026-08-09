#!/usr/bin/env bash
# ABOUTME: R2 — R1 showed enforcement works inside the rootless netns. Three things
# ABOUTME: that decide whether it is usable: does X1 spoofing defeat it (with the
# ABOUTME: NET_ADMIN the agent really holds), can the agent reach the netns, does it survive churn.
set -uo pipefail

say() { printf '\n=== %s ===\n' "$*"; }
NET=yb_rnet2
NS_DIR=/run/user/$(id -u)/netns
cleanup() {
	podman unshare --rootless-netns nft delete table inet yb_rl2 2>/dev/null
	podman rm -f yb_x1 yb_x2 >/dev/null 2>&1
	podman network rm $NET >/dev/null 2>&1
}
trap cleanup EXIT
cleanup

podman network create $NET --subnet 10.91.0.0/24 >/dev/null 2>&1
# NET_ADMIN is what the in-entrypoint firewall path grants, and podman has no
# sidecar runner, so this is the capability a real rootless podman sandbox holds.
podman run -d --name yb_x1 --cap-add NET_ADMIN --network $NET alpine sleep 400 >/dev/null
podman run -d --name yb_x2 --network $NET alpine sleep 400 >/dev/null
A=$(podman inspect yb_x1 --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}')
B=$(podman inspect yb_x2 --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}')
ALLOW=$(getent ahostsv4 example.com | awk '{print $1; exit}')
DENY=1.1.1.1
echo "A=$A (NET_ADMIN)  B=$B (control)  allowlisted=$ALLOW  denied=$DENY"

podman unshare --rootless-netns nft -f - <<NFT
table inet yb_rl2 {
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

p()  { podman exec "$1" nc -z -w 4 "$2" 80 >/dev/null 2>&1 && echo REACHABLE || echo blocked; }
ps_() { podman exec yb_x1 ping -c2 -W2 -I "$1" $DENY >/dev/null 2>&1 && echo REACHABLE || echo blocked; }

say "controls"
echo "  A -> allowlisted: $(p yb_x1 "$ALLOW")   A -> denied: $(p yb_x1 $DENY)   B -> denied: $(p yb_x2 $DENY)"

######################################################################
say "R2a — X1 spoofing, with the capability an agent actually has"
######################################################################
echo -n "  agent adds 10.91.0.99/24: "
if podman exec --user root yb_x1 ip addr add 10.91.0.99/24 dev eth0 2>&1; then echo "succeeded"; fi
echo "  addresses A now holds:"
podman exec yb_x1 ip -4 addr show eth0 2>/dev/null | grep inet | sed 's/^/    /'
if podman exec yb_x1 ip -4 addr show eth0 2>/dev/null | grep -q 10.91.0.99; then
	echo "  the spoof address IS held — the test below is meaningful"
	echo "  A -> denied sourced from 10.91.0.99: $(ps_ 10.91.0.99)"
else
	echo "  ABORT-WORTHY: the address was never added, so any 'blocked' below is free"
fi

say "R2b — does rule 0b close it here?"
podman unshare --rootless-netns nft add rule inet yb_rl2 c_forward iifname '"podman1"' counter drop comment '"bridge-default-deny"'
echo "  A -> denied from 10.91.0.99: $(ps_ 10.91.0.99)"
echo "  A -> allowlisted from own address: $(p yb_x1 "$ALLOW")"
podman unshare --rootless-netns nft list table inet yb_rl2 | grep counter | sed 's/^\s*/    /'

######################################################################
say "R2c — can the agent reach the rootless netns and undo any of this?"
######################################################################
echo "  capabilities the agent holds:"
podman exec yb_x1 sh -c 'grep CapEff /proc/self/status' 2>/dev/null | sed 's/^/    /'
echo "  is the netns directory visible inside the container?"
podman exec yb_x1 sh -c "ls '$NS_DIR' 2>&1 | head -3" | sed 's/^/    /'
echo "  can it see the host's /proc/1/ns/net (a route into another netns)?"
podman exec yb_x1 sh -c 'readlink /proc/1/ns/net 2>&1' | sed 's/^/    /'
echo "  can it flush the rules that bind it (they are not in its own netns)?"
podman exec --user root yb_x1 sh -c 'apk add --no-cache nftables >/dev/null 2>&1; nft list ruleset 2>&1 | head -5' | sed 's/^/    /'
echo "  and is it still contained after trying?"
echo "    A -> denied: $(p yb_x1 $DENY)"

######################################################################
say "R2d — lifecycle: does the netns, and our table, survive container churn?"
######################################################################
echo "  netns files on disk now:"
find "$NS_DIR" -maxdepth 1 -mindepth 1 -printf "%f\n" 2>/dev/null | sed 's/^/    /'
echo "  -- stop BOTH containers --"
podman stop yb_x1 yb_x2 >/dev/null 2>&1
sleep 3
echo "  netns files after stopping everything:"
find "$NS_DIR" -maxdepth 1 -mindepth 1 -printf "%f\n" 2>/dev/null | sed 's/^/    /' || echo "    (none — directory gone)"
echo "  does our table still exist?"
podman unshare --rootless-netns nft list table inet yb_rl2 >/dev/null 2>&1 \
	&& echo "    table SURVIVED" || echo "    table GONE — the netns was torn down with the last container"
echo "  -- start one container again --"
podman start yb_x1 >/dev/null 2>&1; sleep 3
A2=$(podman inspect yb_x1 --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}')
echo "  A came back as $A2 (was $A)"
podman unshare --rootless-netns nft list table inet yb_rl2 >/dev/null 2>&1 \
	&& echo "    table still present after restart" || echo "    table GONE after restart — enforcement must be reinstalled per bring-up"
echo "  is A contained right now?"
echo "    A -> denied: $(p yb_x1 $DENY)"

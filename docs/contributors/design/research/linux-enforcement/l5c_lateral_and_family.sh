#!/usr/bin/env bash
# ABOUTME: L5c — two things the earlier L5 runs stumbled into. Whether host-side
# ABOUTME: nftables can see one sandbox talking to another on the same bridge, and
# ABOUTME: whether an 'ip saddr' rule matches IPv6 traffic. Both with live controls.
set -uo pipefail

say() { printf '\n=== %s ===\n' "$*"; }
cleanup() {
	sudo nft delete table inet yb_l5c 2>/dev/null
	sudo nft delete table inet yb_l5c_h 2>/dev/null
	docker rm -f yb_l5c_a yb_l5c_b >/dev/null 2>&1
	docker network rm yb_l5c_net >/dev/null 2>&1
}
trap cleanup EXIT
cleanup

######################################################################
say "PART 1 — can host nftables see sandbox-to-sandbox traffic on one bridge?"
######################################################################
echo "br_netfilter module: $(lsmod | grep -q br_netfilter && echo loaded || echo NOT loaded)"
docker network create --subnet 10.53.0.0/24 yb_l5c_net >/dev/null
docker run -d --name yb_l5c_a --network yb_l5c_net alpine sleep 300 >/dev/null
docker run -d --name yb_l5c_b --network yb_l5c_net alpine sleep 300 >/dev/null
A=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' yb_l5c_a)
B=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' yb_l5c_b)
echo "A=$A  B=$B  (same bridge)"

echo "-- policy: drop everything from A, the strongest containment the design can express --"
sudo nft -f - <<NFT
table inet yb_l5c {
	chain c_forward {
		type filter hook forward priority -10; policy accept;
		ip saddr $A counter drop comment "drop-all-from-A"
	}
}
NFT

echo "  A -> internet (1.1.1.1:80) : $(docker exec yb_l5c_a nc -z -w 3 1.1.1.1 80 >/dev/null 2>&1 && echo REACHABLE || echo blocked)   <- POSITIVE CONTROL, must be blocked"
echo "  A -> B      (same bridge)  : $(docker exec yb_l5c_a ping -c2 -W2 "$B" >/dev/null 2>&1 && echo REACHABLE || echo blocked)   <- the question"
sudo nft list table inet yb_l5c | grep counter | sed 's/^\s*/  /'
echo "  READ: if the control is blocked and A still reaches B, then no host-side nftables"
echo "        rule can contain traffic between two sandboxes on the same bridge — the"
echo "        packets are switched, never routed, and never enter the forward hook."

######################################################################
say "PART 2 — does an 'ip saddr' rule match IPv6 traffic?"
######################################################################
echo "asked on the host's own loopback, where both families always exist, so the"
echo "answer does not depend on this host having v6 internet (it does not)."
sudo nft -f - <<'NFT'
table inet yb_l5c_h {
	chain c_output {
		type filter hook output priority 0; policy accept;
		ip saddr 127.0.0.1 counter comment "v4-rule"
		ip6 saddr ::1 counter comment "v6-rule"
	}
}
NFT
ping -c 3 -W 2 127.0.0.1 >/dev/null 2>&1
echo "-- after 3 IPv4 pings:"
sudo nft list chain inet yb_l5c_h c_output | grep counter | sed 's/^\s*/    /'
ping -6 -c 3 -W 2 ::1 >/dev/null 2>&1
echo "-- after 3 more IPv6 pings:"
sudo nft list chain inet yb_l5c_h c_output | grep counter | sed 's/^\s*/    /'
echo "  READ: the v4 counter must not move for v6 traffic. Every allowlist rule the"
echo "        design writes is 'ip saddr'-shaped, and the repo writes no ip6 rules."

#!/usr/bin/env bash
# ABOUTME: L5b — redo of L5, whose first attempt put both containers on one bridge,
# ABOUTME: where traffic never reaches the forward hook and the v4 control failed.
# ABOUTME: Two bridges, so the host actually routes, and the controls mean something.
set -uo pipefail

say() { printf '\n=== %s ===\n' "$*"; }
cleanup() {
	sudo nft delete table inet yb_l5b 2>/dev/null
	docker rm -f yb_l5b_a yb_l5b_b >/dev/null 2>&1
	docker network rm yb_l5b_n1 yb_l5b_n2 >/dev/null 2>&1
}
trap cleanup EXIT
cleanup

say "two v6-enabled bridges, so A -> B is genuinely FORWARDED by the host"
docker network create --ipv6 --subnet 10.51.0.0/24 --subnet fd00:51::/64 yb_l5b_n1 >/dev/null
docker network create --ipv6 --subnet 10.52.0.0/24 --subnet fd00:52::/64 yb_l5b_n2 >/dev/null
docker run -d --name yb_l5b_a --network yb_l5b_n1 alpine sleep 300 >/dev/null
docker run -d --name yb_l5b_b --network yb_l5b_n2 alpine sleep 300 >/dev/null
A4=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' yb_l5b_a)
A6=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.GlobalIPv6Address}}{{end}}' yb_l5b_a)
B4=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' yb_l5b_b)
B6=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.GlobalIPv6Address}}{{end}}' yb_l5b_b)
echo "A: v4=$A4  v6=$A6"
echo "B: v4=$B4  v6=$B6"
docker exec yb_l5b_a sysctl -w net.ipv6.conf.all.forwarding=0 >/dev/null 2>&1

say "baseline — both families reachable before any policy"
echo "  A -> B v4: $(docker exec yb_l5b_a ping -c2 -W2 "$B4" >/dev/null 2>&1 && echo REACHABLE || echo blocked)"
echo "  A -> B v6: $(docker exec yb_l5b_a ping -6 -c2 -W2 "$B6" >/dev/null 2>&1 && echo REACHABLE || echo blocked)"

say "install the allowlist EXACTLY as the design writes it — 'ip saddr' rules only"
sudo nft -f - <<NFT
table inet yb_l5b {
	chain c_forward {
		type filter hook forward priority -10; policy accept;
		ip saddr $A4 counter drop comment "v4-drop"
	}
}
NFT

echo "  A -> B v4: $(docker exec yb_l5b_a ping -c2 -W2 "$B4" >/dev/null 2>&1 && echo REACHABLE || echo blocked)   <- POSITIVE CONTROL, must be blocked"
echo "  A -> B v6: $(docker exec yb_l5b_a ping -6 -c2 -W2 "$B6" >/dev/null 2>&1 && echo REACHABLE || echo blocked)   <- the question"
sudo nft list table inet yb_l5b | grep counter | sed 's/^\s*/  /'

say "now add the v6 half the design does not write, and confirm it would close it"
sudo nft add rule inet yb_l5b c_forward ip6 saddr "$A6" counter drop comment '"v6-drop"'
echo "  A -> B v6: $(docker exec yb_l5b_a ping -6 -c2 -W2 "$B6" >/dev/null 2>&1 && echo REACHABLE || echo blocked)"
sudo nft list table inet yb_l5b | grep counter | sed 's/^\s*/  /'

say "verdict"
V4=$(sudo nft list table inet yb_l5b | grep 'v4-drop' | grep -oP 'packets \K[0-9]+')
V6=$(sudo nft list table inet yb_l5b | grep 'v6-drop' | grep -oP 'packets \K[0-9]+')
echo "v4-drop matched $V4 packets; v6-drop matched $V6 packets"
echo "READ: an 'ip saddr' rule in an inet table is IPv4-only. A guest holding a routable"
echo "      v6 address is unfiltered by it, and the repo writes no ip6 rules anywhere."

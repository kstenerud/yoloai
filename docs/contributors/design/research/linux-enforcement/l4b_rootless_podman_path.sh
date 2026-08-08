#!/usr/bin/env bash
# ABOUTME: L4b — rootless podman has no host route to its bridge, so where does its
# ABOUTME: egress actually appear in the host netns, and under what source address?
# ABOUTME: Decides whether host-side address-keyed enforcement can see it at all.
set -uo pipefail

TABLE=yb_l4b
NET=yb_l4bnet
DST=1.1.1.1
PORT=8443
say() { printf '\n=== %s ===\n' "$*"; }
cleanup() {
	sudo nft delete table inet $TABLE 2>/dev/null
	podman rm -f yb_l4b_c >/dev/null 2>&1
	podman network rm $NET >/dev/null 2>&1
	docker rm -f yb_l4b_d >/dev/null 2>&1
}
trap cleanup EXIT
cleanup

podman network create $NET --subnet 10.78.0.0/24 >/dev/null 2>&1
podman run -d --name yb_l4b_c --network $NET alpine sleep 300 >/dev/null
PIP=$(podman inspect yb_l4b_c --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}')
docker run -d --name yb_l4b_d alpine sleep 300 >/dev/null
DIP=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' yb_l4b_d)
HOSTIP=$(ip -4 route get 1.1.1.1 | awk '{for(i=1;i<=NF;i++) if($i=="src") print $(i+1)}')
echo "rootless podman container address : $PIP  (host route: $(ip -4 route | grep -c 10.78) )"
echo "docker container address          : $DIP  (host route: $(ip -4 route | grep -c 172.17) )"
echo "host address                      : $HOSTIP"

say "count traffic to $DST:$PORT in BOTH hooks, by source"
sudo nft -f - <<NFT
table inet $TABLE {
	chain c_forward {
		type filter hook forward priority -10; policy accept;
		ip daddr $DST tcp dport $PORT ip saddr $PIP counter comment "fwd-from-podman-addr"
		ip daddr $DST tcp dport $PORT ip saddr $DIP counter comment "fwd-from-docker-addr"
		ip daddr $DST tcp dport $PORT counter comment "fwd-any"
	}
	chain c_output {
		type filter hook output priority 0; policy accept;
		ip daddr $DST tcp dport $PORT ip saddr $HOSTIP counter comment "out-from-host-addr"
		ip daddr $DST tcp dport $PORT counter comment "out-any"
	}
}
NFT
echo "loaded rc=$?"

say "POSITIVE CONTROL: docker container reaches out (known to be forwarded)"
docker exec yb_l4b_d nc -z -w 3 $DST $PORT >/dev/null 2>&1
sudo nft list table inet $TABLE | grep -E 'comment' | sed 's/^\s*/  /'

say "TEST: rootless podman container reaches out"
podman exec yb_l4b_c nc -z -w 3 $DST $PORT >/dev/null 2>&1
sudo nft list table inet $TABLE | grep -E 'comment' | sed 's/^\s*/  /'

say "verdict"
FA=$(sudo nft list table inet $TABLE | grep 'fwd-any' | grep -oP 'packets \K[0-9]+')
OA=$(sudo nft list table inet $TABLE | grep 'out-any' | grep -oP 'packets \K[0-9]+')
FP=$(sudo nft list table inet $TABLE | grep 'fwd-from-podman-addr' | grep -oP 'packets \K[0-9]+')
OH=$(sudo nft list table inet $TABLE | grep 'out-from-host-addr' | grep -oP 'packets \K[0-9]+')
echo "forward hook total=$FA (from podman's own address: $FP)"
echo "output  hook total=$OA (from the host's address:   $OH)"
if [ "${FP:-0}" -eq 0 ] && [ "${OH:-0}" -gt 0 ]; then
	echo "VERDICT: rootless podman egress appears in the OUTPUT hook under the HOST's"
	echo "         source address. Its container address never appears in the host netns,"
	echo "         so host-side enforcement keyed on the guest address cannot see it —"
	echo "         and cannot distinguish it from the host's own traffic."
elif [ "${FP:-0}" -gt 0 ]; then
	echo "VERDICT: rootless podman egress IS forwarded under its own address."
else
	echo "VERDICT: inconclusive — see counters above."
fi

say "which process originates it, and is its cgroup per-sandbox?"
pgrep -a pasta 2>/dev/null | head -3 || pgrep -a slirp4netns 2>/dev/null | head -3 || echo "  neither pasta nor slirp4netns running"
for p in $(pgrep pasta 2>/dev/null | head -2); do
	echo "  pasta pid $p cgroup: $(awk -F: '$1=="0"{print $3}' "/proc/$p/cgroup" 2>/dev/null)"
done

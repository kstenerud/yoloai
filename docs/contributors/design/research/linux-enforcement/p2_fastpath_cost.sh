#!/usr/bin/env bash
# ABOUTME: P2 — what does evaluating the allowlist on every packet cost, versus
# ABOUTME: short-circuiting on conntrack state? Measures forward-path throughput for
# ABOUTME: both rule shapes across allowlist sizes, on a controlled same-bridge path.
set -uo pipefail

say() { printf '\n=== %s ===\n' "$*"; }
LOADED_BY_US=0
cleanup() {
	sudo nft delete table inet yb_p2 2>/dev/null
	docker rm -f yb_p2_srv yb_p2_cli >/dev/null 2>&1
	if [ "$LOADED_BY_US" = 1 ]; then
		sudo sysctl -qw net.bridge.bridge-nf-call-iptables=0 2>/dev/null
		sudo modprobe -r br_netfilter 2>/dev/null && echo "restored: br_netfilter unloaded"
	fi
}
trap cleanup EXIT
cleanup

say "rig: two containers on one bridge, br_netfilter loaded so the traffic is filtered"
echo "(an off-host path would measure the internet, not the ruleset)"
lsmod | grep -qw br_netfilter || { sudo modprobe br_netfilter && LOADED_BY_US=1; }
sudo sysctl -qw net.bridge.bridge-nf-call-iptables=1 >/dev/null
docker run -d --name yb_p2_srv alpine sleep 900 >/dev/null
docker run -d --name yb_p2_cli alpine sleep 900 >/dev/null
for c in yb_p2_srv yb_p2_cli; do docker exec $c apk add --no-cache iperf3 >/dev/null 2>&1; done
docker exec yb_p2_cli sh -c 'command -v iperf3 >/dev/null' || { echo "ABORT: iperf3 missing; results would be free"; exit 1; }
SRV=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' yb_p2_srv)
CLI=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' yb_p2_cli)
echo "server=$SRV  client=$CLI"
docker exec -d yb_p2_srv iperf3 -s
sleep 2

# pad the set to a target size; the real destination is always a member
padded_set() {
	local n=$1 out="$SRV"
	local i=0 a b
	while [ $i -lt "$n" ]; do
		a=$(( i / 254 )); b=$(( i % 254 + 1 ))
		out="$out, 203.$(( a / 254 )).$(( a % 254 )).$b"
		i=$(( i + 1 ))
	done
	echo "$out"
}

load() { # $1 = shape, $2 = set size
	local els; els=$(padded_set "$2")
	sudo nft delete table inet yb_p2 2>/dev/null
	if [ "$1" = fastpath ]; then
		sudo nft -f - <<NFT
table inet yb_p2 {
	set allowed { type ipv4_addr; flags interval; elements = { $els } }
	chain c_forward {
		type filter hook forward priority -10; policy accept;
		ip saddr $CLI ct state established,related counter accept
		ip saddr $CLI ip daddr @allowed counter accept
		ip saddr $CLI counter drop
	}
}
NFT
	elif [ "$1" = nofastpath ]; then
		sudo nft -f - <<NFT
table inet yb_p2 {
	set allowed { type ipv4_addr; flags interval; elements = { $els } }
	chain c_forward {
		type filter hook forward priority -10; policy accept;
		ip saddr $CLI ip daddr @allowed counter accept
		ip saddr $CLI counter drop
	}
}
NFT
	fi
}

bench() { docker exec yb_p2_cli iperf3 -c "$SRV" -t 5 -f m 2>/dev/null | awk '/sender/{print $7}' | tail -1; }

say "BASELINE — no policy loaded"
sudo nft delete table inet yb_p2 2>/dev/null
B0=$(bench); B0b=$(bench)
echo "  no policy: $B0 Mbit/s, $B0b Mbit/s"
[ -n "$B0" ] || { echo "ABORT: baseline produced no number; the rig is broken"; exit 1; }

say "throughput by rule shape and allowlist size (Mbit/s, two runs each)"
printf '  %-12s %-8s %-10s %-10s\n' shape set-size run1 run2
for size in 1 1000 10000; do
	for shape in fastpath nofastpath; do
		load $shape $size || { echo "  load failed at size $size"; continue; }
		r1=$(bench); r2=$(bench)
		printf '  %-12s %-8s %-10s %-10s\n' "$shape" "$size" "${r1:-?}" "${r2:-?}"
	done
done

say "sanity: the policy is actually being enforced, not bypassed"
load nofastpath 1
docker exec yb_p2_cli sh -c 'nc -z -w 3 1.1.1.1 80' >/dev/null 2>&1 && echo "  denied dst: REACHABLE (policy NOT enforcing — numbers above are meaningless)" || echo "  denied dst: blocked, so the chain is live"
sudo nft list table inet yb_p2 | grep counter | sed 's/^\s*/  /'

say "WHAT WAS NOT TRIED"
cat <<'NOT'
  - CPU time per packet. Throughput on an idle host with a short path is a proxy;
    if the two shapes are close, that proxy cannot separate them and a perf/bpftrace
    measurement would be needed to say anything sharper.
  - Small-packet / high-connection-rate workloads. iperf3 is a bulk stream; the set
    lookup happens per packet either way, but connection setup rate is untested.
  - Set types other than a hashed interval set. A plain (non-interval) set may
    perform differently and was not compared.
  - Any IPv6 set.
  - Whether br_netfilter itself changes the throughput baseline. Not isolated here.
NOT

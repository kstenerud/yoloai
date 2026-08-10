#!/usr/bin/env bash
# ABOUTME: P1b — P1's revocation test sampled once 8s after revoking and called any
# ABOUTME: growth "still flowing". A TCP transfer stalls gradually, so that is too blunt.
# ABOUTME: This samples the transfer rate across a 30s window to show decay, not a binary.
set -uo pipefail

say() { printf '\n=== %s ===\n' "$*"; }
LOADED_BY_US=0
cleanup() {
	sudo nft delete table inet yb_p1b 2>/dev/null
	docker rm -f yb_p1b_srv yb_p1b_cli >/dev/null 2>&1
	if [ "$LOADED_BY_US" = 1 ]; then
		sudo sysctl -qw net.bridge.bridge-nf-call-iptables=0 2>/dev/null
		sudo modprobe -r br_netfilter 2>/dev/null && echo "restored: br_netfilter unloaded"
	fi
}
trap cleanup EXIT
cleanup

lsmod | grep -qw br_netfilter || { sudo modprobe br_netfilter && LOADED_BY_US=1; }
sudo sysctl -qw net.bridge.bridge-nf-call-iptables=1 >/dev/null
docker run -d --name yb_p1b_srv nginx:alpine >/dev/null
docker exec yb_p1b_srv sh -c 'dd if=/dev/urandom of=/usr/share/nginx/html/big bs=1M count=200 2>/dev/null'
docker run -d --name yb_p1b_cli alpine sleep 900 >/dev/null
docker exec yb_p1b_cli apk add --no-cache curl >/dev/null 2>&1
docker exec yb_p1b_cli sh -c 'command -v curl >/dev/null' || { echo "ABORT: curl missing"; exit 1; }
sleep 2
SRV=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' yb_p1b_srv)
CLI=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' yb_p1b_cli)
echo "client=$CLI  server=$SRV  (200 MB file, rate-limited to 300 KB/s)"

load() {
	sudo nft delete table inet yb_p1b 2>/dev/null
	local fast=""
	[ "$1" = fastpath ] && fast="ip saddr $CLI ct state established,related counter accept comment \"fastpath\""
	sudo nft -f - <<NFT
table inet yb_p1b {
	set allowed { type ipv4_addr; elements = { $SRV } }
	chain c_forward {
		type filter hook forward priority -10; policy accept;
		$fast
		ip saddr $CLI ip daddr @allowed counter accept comment "allowlisted"
		ip saddr $CLI counter drop comment "post-revoke-drops"
	}
}
NFT
}

bytes() { docker exec yb_p1b_cli sh -c 'wc -c < /tmp/dl 2>/dev/null || echo 0' | tr -d ' '; }

for shape in fastpath nofastpath; do
	say "$shape"
	load $shape
	docker exec yb_p1b_cli sh -c 'rm -f /tmp/dl' 2>/dev/null
	docker exec -d yb_p1b_cli sh -c "curl -s --limit-rate 300k -m 120 -o /tmp/dl http://$SRV/big"
	sleep 6
	P=$(bytes)
	[ "${P:-0}" -gt 100000 ] || { echo "  ABORT: transfer did not start ($P bytes); any result would be free"; docker exec yb_p1b_cli sh -c 'pkill curl' 2>/dev/null; continue; }
	echo "  pre-revoke: $P bytes"
	sudo nft delete element inet yb_p1b allowed "{ $SRV }"
	echo "  --- allowlist element removed ---"
	LAST=$P
	for t in 5 10 15 20 25 30; do
		sleep 5
		N=$(bytes); D=$(( N - LAST )); LAST=$N
		printf '  t+%-3ss  total=%-10s  delta=%-9s  rate=%s KB/s\n' "$t" "$N" "$D" "$(( D / 5 / 1024 ))"
	done
	echo "  drop counter (client->server packets refused after revocation):"
	sudo nft list table inet yb_p1b | grep 'post-revoke-drops' | sed 's/^\s*/    /'
	docker exec yb_p1b_cli sh -c 'pkill curl' 2>/dev/null
done

say "READ"
cat <<'NOT'
  A rate that decays to 0 KB/s means revocation took effect; the transfer stalls
  because the client's outbound packets are refused, not because the server stopped.
  A rate that holds near 300 KB/s means the flow was never re-evaluated.
  The drop counter distinguishes "the rule fired" from "the transfer ended for
  some other reason" — a rate of 0 with a zero counter would be a free negative.

WHAT WAS NOT TRIED
  - Whether the client surfaces an error or hangs. curl is given -m 120; what a
    real agent's HTTP stack does with a stalled socket is untested.
  - UDP.
  - The reply direction. Bulk data flows server->client and matches no rule here;
    the chain policy is accept, so it falls through. If the policy were drop, that
    traffic would be dropped and this measurement would look completely different.
NOT

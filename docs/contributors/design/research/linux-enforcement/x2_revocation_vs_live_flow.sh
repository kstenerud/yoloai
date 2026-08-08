#!/usr/bin/env bash
# ABOUTME: X2-Linux — the macOS pass found that deleting an allowlist entry does not
# ABOUTME: stop traffic already flowing. L10 found the conntrack version for RECYCLED
# ABOUTME: addresses; this asks the revocation question, which is the one users hit.
set -uo pipefail

say() { printf '\n=== %s ===\n' "$*"; }
CT=yb_x2
cleanup() {
	sudo nft delete table inet yb_x2 2>/dev/null
	docker rm -f $CT >/dev/null 2>&1
}
trap cleanup EXIT
cleanup

PEER=$(getent ahostsv4 example.com | awk '{print $1; exit}')
echo "peer (initially allowlisted, then revoked): $PEER"

docker run -d --name $CT alpine sleep 500 >/dev/null
G=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' $CT)
echo "guest: $G"

say "policy: $PEER allowlisted"
sudo nft -f - <<NFT
table inet yb_x2 {
	set allowed { type ipv4_addr; elements = { $PEER } }
	chain c_forward {
		type filter hook forward priority -10; policy accept;
		ip saddr $G ct state established,related counter accept comment "established"
		ip saddr $G udp dport 53 accept
		ip saddr $G ip daddr @allowed counter accept comment "allowlisted"
		ip saddr $G counter drop comment "denied"
	}
}
NFT

say "open a long-lived keep-alive connection and let it run"
docker exec -d $CT sh -c "(while :; do printf 'HEAD / HTTP/1.1\r\nHost: example.com\r\nConnection: keep-alive\r\n\r\n'; sleep 2; done) | nc $PEER 80 > /tmp/out 2>&1"
sleep 8
BEFORE=$(docker exec $CT sh -c 'wc -c < /tmp/out' 2>/dev/null | tr -d ' ')
echo "  bytes received so far: $BEFORE"
[ "${BEFORE:-0}" -gt 0 ] || { echo "  ABORT: the flow never started; nothing below would mean anything"; exit 1; }
echo "  conntrack entry:"
sudo conntrack -L 2>/dev/null | grep "src=$G" | grep "dst=$PEER" | sed 's/^/    /'

say "REVOKE — remove $PEER from the allowlist, exactly as 'sandbox allow --remove' would"
sudo nft delete element inet yb_x2 allowed "{ $PEER }"
echo "  allowlist now: $(sudo nft list set inet yb_x2 allowed | grep -o 'elements = {[^}]*}' || echo '<empty>')"

say "CONTROL — a NEW connection to the revoked peer must fail"
docker exec $CT nc -z -w 4 "$PEER" 80 >/dev/null 2>&1 && echo "  REACHABLE (control failed — run is void)" || echo "  blocked, as revocation intends"

say "THE QUESTION — is the connection that was already open still carrying data?"
sleep 10
AFTER=$(docker exec $CT sh -c 'wc -c < /tmp/out' 2>/dev/null | tr -d ' ')
echo "  bytes before revocation: $BEFORE"
echo "  bytes 10s after        : $AFTER"
if [ "${AFTER:-0}" -gt "${BEFORE:-0}" ]; then
	echo "  RESULT: the revoked destination is STILL RECEIVING AND ANSWERING."
	echo "          Revocation did not revoke; ct state established carries the old flow."
else
	echo "  RESULT: the flow stopped — revocation took effect on the live connection."
fi

say "THE FIX — drop the connection's state as part of revoking"
sudo conntrack -D -s "$G" -d "$PEER" >/dev/null 2>&1
echo "  conntrack entries for that pair now: $(sudo conntrack -L 2>/dev/null | grep -c "src=$G.*dst=$PEER" || true)"
MID=$(docker exec $CT sh -c 'wc -c < /tmp/out' 2>/dev/null | tr -d ' ')
sleep 8
FINAL=$(docker exec $CT sh -c 'wc -c < /tmp/out' 2>/dev/null | tr -d ' ')
echo "  bytes at flush: $MID"
echo "  bytes 8s later: $FINAL"
if [ "${FINAL:-0}" -gt "${MID:-0}" ]; then
	echo "  RESULT: still flowing even after the state flush."
else
	echo "  RESULT: flow stopped. Revocation needs the conntrack drop to be part of it."
fi

say "counters"
sudo nft list table inet yb_x2 | grep counter | sed 's/^\s*/  /'

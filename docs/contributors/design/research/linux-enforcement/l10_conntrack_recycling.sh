#!/usr/bin/env bash
# ABOUTME: L10 — every rule shape here accepts `ct state established,related`, and
# ABOUTME: docker hands a freed address straight to the next sandbox. Does connection
# ABOUTME: state outlive the sandbox, and can a new one inherit it past the allowlist?
set -uo pipefail

say() { printf '\n=== %s ===\n' "$*"; }
cleanup() {
	sudo nft delete table inet yb_l10 2>/dev/null
	docker rm -f yb_l10_a yb_l10_b >/dev/null 2>&1
}
trap cleanup EXIT
cleanup

PEER=$(getent ahostsv4 example.com | awk '{print $1; exit}')
SRCPORT=54321
echo "peer (allowlisted for sandbox A, NOT for sandbox B): $PEER"
echo "conntrack tcp established timeout: $(sysctl -n net.netfilter.nf_conntrack_tcp_timeout_established)s"

say "sandbox A starts, is allowlisted for the peer, and opens a long-lived flow"
docker run -d --name yb_l10_a alpine sleep 400 >/dev/null
A=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' yb_l10_a)
echo "A address = $A"
sudo nft -f - <<NFT
table inet yb_l10 {
	set allowed { type ipv4_addr; elements = { $PEER } }
	chain c_forward {
		type filter hook forward priority -10; policy accept;
		ip saddr $A ct state established,related counter accept comment "established"
		ip saddr $A udp dport 53 accept
		ip saddr $A ip daddr @allowed counter accept comment "allowlisted"
		ip saddr $A counter drop comment "denied"
	}
}
NFT
docker exec -d yb_l10_a sh -c "sleep 300 | nc -p $SRCPORT $PEER 80"
sleep 4
echo "  conntrack entries for A:"
sudo conntrack -L 2>/dev/null | grep "src=$A" | sed 's/^/    /' || echo "    none"

say "sandbox A is destroyed — does its connection state go with it?"
docker rm -f yb_l10_a >/dev/null
sleep 3
STALE=$(sudo conntrack -L 2>/dev/null | grep -c "src=$A" || true)
echo "  conntrack entries still naming $A after the sandbox is gone: $STALE"
sudo conntrack -L 2>/dev/null | grep "src=$A" | sed 's/^/    /'
if [ "$STALE" -gt 0 ]; then
	echo "  RESULT: connection state OUTLIVES the sandbox. Nothing flushed it."
else
	echo "  RESULT: state was flushed with the sandbox."
fi

say "sandbox B starts and takes the freed address"
docker run -d --name yb_l10_b alpine sleep 400 >/dev/null
B=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' yb_l10_b)
echo "B address = $B  (A's was $A)"
[ "$A" = "$B" ] || echo "  NOTE: address not recycled this run; the inheritance test below is not meaningful."

say "B's policy: the peer is NOT allowlisted for B"
# `nft -f` MERGES into an existing table — it does not replace it. The first run of
# this script left A's rules and A's allowlisted peer in place, so the negative
# control passed traffic and both results were void. Delete, then install.
sudo nft delete table inet yb_l10 2>/dev/null
echo "  (A's table deleted first — nft -f merges, it does not replace)"
sudo nft -f - <<NFT
table inet yb_l10 {
	set allowed { type ipv4_addr; elements = { 192.0.2.1 } }
	chain c_forward {
		type filter hook forward priority -10; policy accept;
		ip saddr $B ct state established,related counter accept comment "established"
		ip saddr $B udp dport 53 accept
		ip saddr $B ip daddr @allowed counter accept comment "allowlisted"
		ip saddr $B counter drop comment "denied"
	}
}
NFT

say "NEGATIVE CONTROL — B reaches the peer from a fresh source port"
docker exec yb_l10_b nc -z -w 4 "$PEER" 80 >/dev/null 2>&1 && echo "  REACHABLE (unexpected)" || echo "  blocked, as the allowlist requires"

say "THE TEST — B reaches the peer reusing the dead sandbox's 5-tuple"
docker exec yb_l10_b sh -c "nc -p $SRCPORT -w 4 -z $PEER 80" >/dev/null 2>&1 \
	&& echo "  REACHABLE — B inherited A's connection state and bypassed its own allowlist" \
	|| echo "  blocked — the stale entry did not carry B past the allowlist"

say "counters"
sudo nft list table inet yb_l10 | grep counter | sed 's/^\s*/  /'
echo
echo "conntrack entries naming the shared address now:"
sudo conntrack -L 2>/dev/null | grep "src=$B" | sed 's/^/    /' || echo "    none"

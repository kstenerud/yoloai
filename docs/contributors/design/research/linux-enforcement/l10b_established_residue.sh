#!/usr/bin/env bash
# ABOUTME: L10b — L10 only reached a cleanly-closed flow, which lingers as TIME_WAIT
# ABOUTME: and is harmless. This forces the entry to stay ESTABLISHED (5-day timeout)
# ABOUTME: past the sandbox's death, which is the residue that could actually be inherited.
set -uo pipefail

say() { printf '\n=== %s ===\n' "$*"; }
cleanup() {
	sudo nft delete table inet yb_l10b 2>/dev/null
	docker rm -f yb_l10b_a yb_l10b_b >/dev/null 2>&1
}
trap cleanup EXIT
cleanup
sudo conntrack -F >/dev/null 2>&1

PEER=$(getent ahostsv4 example.com | awk '{print $1; exit}')
SRCPORT=54322
echo "peer: $PEER   reused source port: $SRCPORT"

say "sandbox A, allowlisted for the peer, WITH its FIN/RST suppressed"
docker run -d --name yb_l10b_a alpine sleep 400 >/dev/null
A=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' yb_l10b_a)
echo "A = $A"
sudo nft -f - <<NFT
table inet yb_l10b {
	set allowed { type ipv4_addr; elements = { $PEER } }
	chain c_forward {
		type filter hook forward priority -10; policy accept;
		ip saddr $A tcp flags & (fin|rst) != 0 counter drop comment "suppress-close"
		ip saddr $A ct state established,related counter accept comment "established"
		ip saddr $A udp dport 53 accept
		ip saddr $A ip daddr @allowed counter accept comment "allowlisted"
		ip saddr $A counter drop comment "denied"
	}
}
NFT
docker exec -d yb_l10b_a sh -c "sleep 300 | nc -p $SRCPORT $PEER 80"
sleep 4
echo "  entry while A is alive:"
sudo conntrack -L 2>/dev/null | grep "sport=$SRCPORT" | sed 's/^/    /'

say "destroy A — its close packets are dropped, so the entry should stay ESTABLISHED"
docker rm -f yb_l10b_a >/dev/null
sleep 4
ENTRY=$(sudo conntrack -L 2>/dev/null | grep "sport=$SRCPORT" || true)
echo "  entry after A is gone:"
printf "%s\n" "${ENTRY:-    none}"
if echo "$ENTRY" | grep -q ESTABLISHED; then
	echo "  RESULT: ESTABLISHED residue survives the sandbox, with a $(sysctl -n net.netfilter.nf_conntrack_tcp_timeout_established)s timeout"
else
	echo "  RESULT: entry is not ESTABLISHED — this run does not reach the case it set out to test"
fi

say "sandbox B takes the address; the peer is NOT allowlisted for B"
docker run -d --name yb_l10b_b alpine sleep 400 >/dev/null
B=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' yb_l10b_b)
echo "B = $B (A was $A)"
sudo nft delete table inet yb_l10b 2>/dev/null
sudo nft -f - <<NFT
table inet yb_l10b {
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

say "NEGATIVE CONTROL — fresh source port must be denied"
docker exec yb_l10b_b nc -z -w 4 "$PEER" 80 >/dev/null 2>&1 && echo "  REACHABLE (control failed — run is void)" || echo "  blocked, as required"

say "THE TEST — B reuses the dead sandbox's exact 5-tuple"
docker exec yb_l10b_b sh -c "nc -p $SRCPORT -w 4 -z $PEER 80" >/dev/null 2>&1 \
	&& echo "  REACHABLE — an ESTABLISHED residue carried B past its own allowlist" \
	|| echo "  blocked — even an ESTABLISHED residue does not carry a new sandbox through"

say "counters — which rule made the decision"
sudo nft list table inet yb_l10b | grep counter | sed 's/^\s*/  /'

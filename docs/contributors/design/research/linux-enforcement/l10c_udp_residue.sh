#!/usr/bin/env bash
# ABOUTME: L10c — TCP residue is harmless (L10) and could not be held ESTABLISHED
# ABOUTME: (L10b: conntrack hooks below the filter chain, so a drop cannot stop a
# ABOUTME: state transition). UDP has no close handshake, so its residue is the real case.
set -uo pipefail

say() { printf '\n=== %s ===\n' "$*"; }
cleanup() {
	sudo nft delete table inet yb_l10c 2>/dev/null
	docker rm -f yb_l10c_a yb_l10c_b >/dev/null 2>&1
}
trap cleanup EXIT
cleanup
sudo conntrack -F >/dev/null 2>&1

DNS=1.1.1.1
SRCPORT=54323
docker ps -q | xargs -r docker stop >/dev/null 2>&1   # free the low addresses so recycling is deterministic

say "sandbox A, allowlisted for $DNS, makes one UDP query from port $SRCPORT"
docker run -d --name yb_l10c_a alpine sleep 400 >/dev/null
A=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' yb_l10c_a)
docker exec yb_l10c_a apk add --no-cache bind-tools >/dev/null 2>&1
echo "A = $A"
sudo nft -f - <<NFT
table inet yb_l10c {
	set allowed { type ipv4_addr; elements = { $DNS } }
	chain c_forward {
		type filter hook forward priority -10; policy accept;
		ip saddr $A ct state established,related counter accept comment "established"
		ip saddr $A ip daddr @allowed counter accept comment "allowlisted"
		ip saddr $A counter drop comment "denied"
	}
}
NFT
docker exec yb_l10c_a dig -b "0.0.0.0#$SRCPORT" "@$DNS" example.com +time=3 +tries=1 >/dev/null 2>&1
sleep 2
echo "  entry while A is alive:"
sudo conntrack -L -p udp 2>/dev/null | grep "sport=$SRCPORT" | sed 's/^/    /' || echo "    none"

say "destroy A — UDP has no close, so nothing should retire the entry"
docker rm -f yb_l10c_a >/dev/null
sleep 3
ENTRY=$(sudo conntrack -L -p udp 2>/dev/null | grep "sport=$SRCPORT" || true)
echo "  entry after A is gone:"
printf "%s\n" "${ENTRY:-    none}"
[ -n "$ENTRY" ] || { echo "  ABORT: no residue survived; the inheritance case cannot be tested"; exit 1; }
echo "$ENTRY" | grep -q ASSURED && echo "  residue is ASSURED (bidirectional), which is what ct state established matches"

say "sandbox B must take the same address, or the test is meaningless"
# A's policy binds B too (same address), so it must come down BEFORE B fetches its
# tooling. The previous run left it up, apk could not reach its repos, dig was never
# installed, and both "blocked" results were free — the same trap L5d hit.
sudo nft delete table inet yb_l10c 2>/dev/null
docker run -d --name yb_l10c_b alpine sleep 400 >/dev/null
B=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' yb_l10c_b)
docker exec yb_l10c_b apk add --no-cache bind-tools >/dev/null 2>&1
echo "B = $B (A was $A)"
[ "$A" = "$B" ] || { echo "  ABORT: address not recycled — L10b failed silently on exactly this and must not repeat"; exit 1; }
docker exec yb_l10c_b sh -c 'command -v dig >/dev/null' || { echo "  ABORT: dig not installed in B — every result below would be free"; exit 1; }
echo "  dig present in B: $(docker exec yb_l10c_b dig -v 2>&1 | head -1)"

say "B's policy allows NOTHING — no $DNS, no port 53"
sudo nft -f - <<NFT
table inet yb_l10c {
	set allowed { type ipv4_addr; elements = { 192.0.2.1 } }
	chain c_forward {
		type filter hook forward priority -10; policy accept;
		ip saddr $B ct state established,related counter accept comment "established"
		ip saddr $B ip daddr @allowed counter accept comment "allowlisted"
		ip saddr $B counter drop comment "denied"
	}
}
NFT

say "NEGATIVE CONTROL — a query from a FRESH source port must be denied"
docker exec yb_l10c_b dig -b "0.0.0.0#59999" "@$DNS" example.com +time=3 +tries=1 2>&1 | grep -qE 'ANSWER SECTION|status: NOERROR' \
	&& echo "  ANSWERED (control failed — run is void)" || echo "  no answer: blocked, as required"

say "THE TEST — same query, reusing the dead sandbox's source port"
docker exec yb_l10c_b dig -b "0.0.0.0#$SRCPORT" "@$DNS" example.com +time=3 +tries=1 2>&1 | grep -qE 'ANSWER SECTION|status: NOERROR' \
	&& echo "  ANSWERED — B inherited A's UDP flow state and bypassed its own allowlist" \
	|| echo "  no answer: the residue did not carry B through"

say "counters — which rule decided"
sudo nft list table inet yb_l10c | grep counter | sed 's/^\s*/  /'

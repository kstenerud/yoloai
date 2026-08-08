#!/usr/bin/env bash
# ABOUTME: L5d — the host-side layer cannot see same-bridge sandbox-to-sandbox
# ABOUTME: traffic (L5c). Does the in-guest layer yoloAI ships today catch it?
# ABOUTME: Each layer is measured ALONE, so neither can be credited for the other.
set -uo pipefail

say() { printf '\n=== %s ===\n' "$*"; }
cleanup() {
	sudo nft delete table inet yb_l5d 2>/dev/null
	docker rm -f yb_l5d_a yb_l5d_b >/dev/null 2>&1
	docker network rm yb_l5d_net >/dev/null 2>&1
}
trap cleanup EXIT
cleanup

docker network create --subnet 10.54.0.0/24 yb_l5d_net >/dev/null
docker run -d --name yb_l5d_a --cap-add NET_ADMIN --network yb_l5d_net alpine sleep 400 >/dev/null
docker run -d --name yb_l5d_b --network yb_l5d_net alpine sleep 400 >/dev/null
A=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' yb_l5d_a)
B=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' yb_l5d_b)
ALLOW=$(getent ahostsv4 example.com | awk '{print $1; exit}')
echo "sandbox A=$A   sandbox B=$B   (one shared bridge, as both backends ship)"
echo "allowlisted destination: $ALLOW (example.com)"

say "install iptables in A BEFORE any policy exists"
echo "the previous run installed it after the host layer was already denying egress,"
echo "so apk could not reach its repos and the in-guest layer silently never existed."
docker exec yb_l5d_a apk add --no-cache iptables >/dev/null 2>&1
echo "  iptables in guest: $(docker exec yb_l5d_a iptables --version 2>&1 | head -1)"

probe() { # label
	printf '  %-30s allowlisted=%-10s denied=%-10s sibling=%s\n' "$1" \
		"$(docker exec yb_l5d_a nc -z -w 3 "$ALLOW" 80 >/dev/null 2>&1 && echo REACHABLE || echo blocked)" \
		"$(docker exec yb_l5d_a nc -z -w 3 1.1.1.1 80 >/dev/null 2>&1 && echo REACHABLE || echo blocked)" \
		"$(docker exec yb_l5d_a ping -c2 -W2 "$B" >/dev/null 2>&1 && echo REACHABLE || echo blocked)"
}

say "BASELINE — no layer at all"
probe "no policy"

######################################################################
say "LAYER 1 ALONE — host-side nftables, the layer being designed"
######################################################################
sudo nft -f - <<NFT
table inet yb_l5d {
	set allowed { type ipv4_addr; elements = { $ALLOW } }
	chain c_forward {
		type filter hook forward priority -10; policy accept;
		ip saddr $A ct state established,related accept
		ip saddr $A udp dport 53 accept
		ip saddr $A ip daddr @allowed counter accept
		ip saddr $A counter drop
	}
}
NFT
probe "host nftables only"

######################################################################
say "LAYER 2 ALONE — in-guest iptables, what yoloAI ships today"
######################################################################
sudo nft delete table inet yb_l5d 2>/dev/null
echo "  host layer removed, so nothing below can be credited to it"
docker exec yb_l5d_a sh -c "
  iptables -F OUTPUT
  iptables -A OUTPUT -o lo -j ACCEPT
  iptables -A OUTPUT -m state --state ESTABLISHED,RELATED -j ACCEPT
  iptables -A OUTPUT -p udp --dport 53 -j ACCEPT
  iptables -A OUTPUT -d $ALLOW -j ACCEPT
  iptables -P OUTPUT DROP
"
echo "  INSTALL CHECK — the guest's OUTPUT chain must really be default-deny:"
docker exec yb_l5d_a iptables -S OUTPUT | sed 's/^/    /'
probe "in-guest iptables only"

say "verdict"
echo "Read the sibling column in the two 'ALONE' rows. The host layer cannot act on"
echo "same-bridge sibling traffic at all — those packets are switched, not routed, and"
echo "never enter the forward hook. Whether the in-guest layer catches them is what the"
echo "second row answers, and it answers it without the host layer present to help."

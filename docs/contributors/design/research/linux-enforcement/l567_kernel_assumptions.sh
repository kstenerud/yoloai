#!/usr/bin/env bash
# ABOUTME: L5/L6/L7 — three assumptions the design makes about the kernel: that the
# ABOUTME: IPv6 hole is real, that `nft -f` is atomic, and that a base chain at
# ABOUTME: priority -10 relates to docker's own rules the way we assume it does.
set -uo pipefail

say() { printf '\n=== %s ===\n' "$*"; }
cleanup() {
	sudo nft delete table inet yb_l5 2>/dev/null
	sudo nft delete table inet yb_l6 2>/dev/null
	sudo nft delete table inet yb_l7 2>/dev/null
	docker rm -f yb_l5_a yb_l5_b yb_l7_a >/dev/null 2>&1
	docker network rm yb_l5net >/dev/null 2>&1
}
trap cleanup EXIT
cleanup

######################################################################
say "L5 — is the IPv6 hole live on Linux?"
######################################################################
echo "host IPv6 connectivity: $(ping -6 -c1 -W2 2606:4700:4700::1111 >/dev/null 2>&1 && echo yes || echo "none (no route to the v6 internet)")"
echo "docker daemon ipv6 setting: $(docker info --format '{{.Driver}}' >/dev/null 2>&1 && (grep -o '"ipv6"[^,]*' /etc/docker/daemon.json 2>/dev/null || echo 'not set in daemon.json (default off)'))"
echo "repo-wide ip6tables references: $(git grep -ric ip6tables -- ':!docs' 2>/dev/null | wc -l) files"

echo "-- build a v6-enabled docker network so the question can be answered locally --"
docker network create --ipv6 --subnet 2001:db8:l5::/64 yb_l5net >/dev/null 2>&1 \
	|| docker network create --ipv6 --subnet fd00:l5::/64 yb_l5net >/dev/null 2>&1 \
	|| docker network create --ipv6 --subnet fd00:5::/64 yb_l5net >/dev/null 2>&1
docker run -d --name yb_l5_a --network yb_l5net alpine sleep 300 >/dev/null 2>&1
docker run -d --name yb_l5_b --network yb_l5net alpine sleep 300 >/dev/null 2>&1
A4=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' yb_l5_a 2>/dev/null)
A6=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.GlobalIPv6Address}}{{end}}' yb_l5_a 2>/dev/null)
B6=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.GlobalIPv6Address}}{{end}}' yb_l5_b 2>/dev/null)
echo "guest A: v4=$A4  v6=${A6:-<none>}"
echo "guest B: v6=${B6:-<none>}"

if [ -n "$A6" ]; then
	echo "-- install the allowlist EXACTLY as the design writes it: 'ip saddr' rules only --"
	sudo nft -f - <<NFT
table inet yb_l5 {
	chain c_forward {
		type filter hook forward priority -10; policy accept;
		ip saddr $A4 ct state established,related accept
		ip saddr $A4 counter drop comment "v4-drop-all"
	}
}
NFT
	echo "  A -> B over IPv4: $(docker exec yb_l5_a ping -c2 -W2 "$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' yb_l5_b)" >/dev/null 2>&1 && echo REACHABLE || echo blocked)"
	echo "  A -> B over IPv6: $(docker exec yb_l5_a ping -6 -c2 -W2 "$B6" >/dev/null 2>&1 && echo REACHABLE || echo blocked)"
	sudo nft list table inet yb_l5 | grep counter | sed 's/^\s*/  /'
	echo "  READ: an 'ip saddr' rule in an inet table matches IPv4 only. If v6 is REACHABLE"
	echo "        while v4 is blocked, the same policy that contains the guest on v4 does not"
	echo "        exist for it on v6."
else
	echo "  v6 addressing unavailable on this docker install — L5 not settled here."
fi
sudo nft delete table inet yb_l5 2>/dev/null

######################################################################
say "L6 — is 'nft -f' atomic?"
######################################################################
echo "-- load a file whose LAST rule is invalid; do the earlier rules land? --"
sudo nft -f - 2>&1 <<'NFT' | head -3
table inet yb_l6 {
	chain c {
		type filter hook forward priority -10; policy accept;
		ip saddr 10.99.0.1 counter comment "rule-1"
		ip saddr 10.99.0.2 counter comment "rule-2"
		ip saddr 10.99.0.3 this-is-not-valid-syntax
	}
}
NFT
if sudo nft list table inet yb_l6 >/dev/null 2>&1; then
	echo "  RESULT: table exists after a failed load — NOT atomic"
	sudo nft list table inet yb_l6 | sed 's/^/    /'
else
	echo "  RESULT: nothing landed — the whole file rolled back, ATOMIC"
fi

echo "-- and a semantically-invalid (not syntactically) last rule --"
sudo nft -f - 2>&1 <<'NFT' | head -3
table inet yb_l6 {
	chain c {
		type filter hook forward priority -10; policy accept;
		ip saddr 10.99.0.1 counter comment "rule-1"
		socket cgroupv2 level 1 "definitely-not-a-real-cgroup" counter
	}
}
NFT
if sudo nft list table inet yb_l6 >/dev/null 2>&1; then
	echo "  RESULT: table exists — NOT atomic for kernel-rejected rules"
else
	echo "  RESULT: nothing landed — atomic for kernel-rejected rules too"
fi

echo "-- replace a live ruleset in one transaction (the acquisition sequence's shape) --"
sudo nft -f - <<'NFT'
table inet yb_l6 {
	set allowed { type ipv4_addr; elements = { 10.0.0.1 } }
	chain c { type filter hook forward priority -10; policy accept; ip saddr 10.99.0.1 ip daddr @allowed counter accept }
}
NFT
echo "  before: $(sudo nft list set inet yb_l6 allowed | grep -o 'elements = {[^}]*}')"
sudo nft -f - <<'NFT'
flush set inet yb_l6 allowed
table inet yb_l6 { set allowed { type ipv4_addr; elements = { 10.0.0.9, 10.0.0.10 } } }
NFT
echo "  after : $(sudo nft list set inet yb_l6 allowed | grep -o 'elements = {[^}]*}')"
echo "  READ: flush+repopulate in one file is a single transaction, so there is no window"
echo "        in which the set is empty. That is what removes the macOS step-ordering hazard."

######################################################################
say "L7 — hook priority against docker's own chains"
######################################################################
docker run -d --name yb_l7_a alpine sleep 300 >/dev/null
G=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' yb_l7_a)
echo "guest=$G"
echo "-- docker's own filtering lives here --"
sudo nft list tables | sed 's/^/  /'
echo "  docker chains in the shared ip filter table: $(sudo nft list table ip filter 2>/dev/null | grep -c 'chain ')"
echo "  their hook priorities:"
sudo nft list table ip filter 2>/dev/null | grep -E 'type filter hook' | sed 's/^\s*/    /'

for prio in -10 0 10; do
	sudo nft delete table inet yb_l7 2>/dev/null
	sudo nft -f - <<NFT
table inet yb_l7 {
	chain c_forward {
		type filter hook forward priority $prio; policy accept;
		ip saddr $G ct state established,related accept
		ip saddr $G udp dport 53 accept
		ip saddr $G counter drop
	}
}
NFT
	R=$(docker exec yb_l7_a nc -z -w 3 1.1.1.1 80 >/dev/null 2>&1 && echo "NOT enforced" || echo enforced)
	printf '  our chain at priority %-4s -> egress %s\n' "$prio" "$R"
done

echo "-- the asymmetry that matters: our ACCEPT does not bind a later chain --"
echo "  a drop at a lower priority number wins outright, because the packet is gone"
echo "  before docker's chain runs. An accept only ends OUR chain; docker's rules at"
echo "  priority 0 still run afterwards and can still drop. Verify by making docker's"
echo "  table drop what ours accepts:"
sudo nft delete table inet yb_l7 2>/dev/null
sudo nft -f - <<NFT
table inet yb_l7 {
	chain c_forward {
		type filter hook forward priority -10; policy accept;
		ip saddr $G counter accept comment "we-accept-everything"
	}
}
NFT
sudo nft add table ip yb_l7_late 2>/dev/null
sudo nft add chain ip yb_l7_late c "{ type filter hook forward priority 100; policy accept; }" 2>/dev/null
sudo nft add rule ip yb_l7_late c ip saddr "$G" counter drop 2>/dev/null
echo "  ours accepts at -10, a later chain drops at 100: egress is $(docker exec yb_l7_a nc -z -w 3 1.1.1.1 80 >/dev/null 2>&1 && echo "REACHABLE" || echo "blocked — a later chain overrode our accept")"
sudo nft delete table ip yb_l7_late 2>/dev/null

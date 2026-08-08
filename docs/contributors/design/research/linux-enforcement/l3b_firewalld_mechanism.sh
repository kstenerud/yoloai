#!/usr/bin/env bash
# ABOUTME: L3b — does a firewalld reload destroy a foreign nft table, or only its
# ABOUTME: own? Run inside a container's own netns so the mechanism is measured
# ABOUTME: without putting the SSH host's networking at risk. nft is per-netns.
set -uo pipefail

CT=yb_l3b_firewalld
say() { printf '\n=== %s ===\n' "$*"; }
cleanup() { docker rm -f $CT >/dev/null 2>&1; }
trap cleanup EXIT
cleanup

say "boot a fedora container with its own netns and NET_ADMIN"
docker run -d --name $CT --cap-add NET_ADMIN --cap-add NET_RAW \
	fedora:41 sleep 900 >/dev/null || { echo "could not start container"; exit 1; }

say "install firewalld"
docker exec $CT dnf install -y -q firewalld nftables iproute dbus-daemon dbus-tools >/dev/null 2>&1
docker exec $CT sh -c 'rpm -q firewalld; nft --version'

say "start dbus and firewalld"
docker exec $CT sh -c 'mkdir -p /run/dbus && dbus-daemon --system --fork' 2>&1
docker exec -d $CT sh -c 'firewalld --nofork >/tmp/fwd.log 2>&1'
sleep 8
docker exec $CT sh -c 'firewall-cmd --state' 2>&1

say "what firewalld put in the ruleset"
docker exec $CT nft list tables 2>&1

say "add a FOREIGN table, exactly the shape our design installs"
docker exec -i $CT nft -f - <<'NFT'
table inet yb_foreign {
	set allowed { type ipv4_addr; elements = { 203.0.113.7 } }
	chain c_forward {
		type filter hook forward priority -10; policy accept;
		ip saddr 10.99.99.99 ip daddr @allowed counter accept
		ip saddr 10.99.99.99 counter drop
	}
}
NFT
echo "foreign table loaded, rc=$?"
echo "-- tables now:"
docker exec $CT nft list tables 2>&1

say "TRIGGER: firewall-cmd --reload"
docker exec $CT firewall-cmd --reload 2>&1
sleep 3

say "RESULT: did the foreign table survive?"
docker exec $CT nft list tables 2>&1
if docker exec $CT nft list table inet yb_foreign >/dev/null 2>&1; then
	echo "VERDICT: foreign table SURVIVED a firewalld reload"
	echo "-- its contents (a table can survive with its rules gone):"
	docker exec $CT nft list table inet yb_foreign 2>&1
else
	echo "VERDICT: foreign table DESTROYED by a firewalld reload"
fi

say "POSITIVE CONTROL: firewalld's own table must have been rebuilt by that reload"
docker exec $CT sh -c 'nft list tables | grep -i firewalld' 2>&1 \
	&& echo "control OK — firewalld's table is present, so the reload really ran" \
	|| echo "control FAILED — firewalld has no table, so this run proves nothing"

say "which backend is firewalld using (nftables backend does a whole-ruleset flush)"
docker exec $CT sh -c 'grep -i ^FirewallBackend /etc/firewalld/firewalld.conf' 2>&1

#!/usr/bin/env bash
# ABOUTME: L3c — L3b showed firewalld's nftables backend spares a foreign table.
# ABOUTME: Tests the causal claim behind that: what a manager destroys is whatever
# ABOUTME: shares ITS table, which is why docker (in ip filter) got a CVE and we may not.
set -uo pipefail

CT=yb_l3c
say() { printf '\n=== %s ===\n' "$*"; }
cleanup() { docker rm -f $CT >/dev/null 2>&1; }
trap cleanup EXIT
cleanup

docker run -d --name $CT --cap-add NET_ADMIN --cap-add NET_RAW fedora:41 sleep 900 >/dev/null
docker exec $CT dnf install -y -q firewalld nftables iproute iptables-nft dbus-daemon >/dev/null 2>&1
docker exec $CT sh -c 'mkdir -p /run/dbus && dbus-daemon --system --fork' >/dev/null 2>&1

start_fwd() {
	docker exec $CT sh -c 'pkill -f firewalld; sleep 1' >/dev/null 2>&1
	docker exec -d $CT sh -c 'firewalld --nofork >/tmp/fwd.log 2>&1'
	for _ in $(seq 1 20); do
		docker exec $CT firewall-cmd --state >/dev/null 2>&1 && return 0
		sleep 1
	done
	echo "firewalld did not come up"; return 1
}

install_own_table() {
	docker exec -i $CT nft -f - <<'NFT'
table inet yb_own {
	chain c_forward {
		type filter hook forward priority -10; policy accept;
		ip saddr 10.99.99.99 counter drop
	}
}
NFT
}

survives_own() { docker exec $CT nft list table inet yb_own >/dev/null 2>&1 && echo SURVIVED || echo DESTROYED; }

say "PART 1 — nftables backend: our own dedicated table"
docker exec $CT sh -c 'sed -i s/^FirewallBackend=.*/FirewallBackend=nftables/ /etc/firewalld/firewalld.conf'
start_fwd || exit 1
for trigger in "--reload" "--complete-reload"; do
	install_own_table
	docker exec $CT firewall-cmd $trigger >/dev/null 2>&1
	sleep 2
	printf '  firewall-cmd %-18s own table: %s\n' "$trigger" "$(survives_own)"
done
install_own_table
start_fwd >/dev/null
printf '  %-31s own table: %s\n' "firewalld full restart" "$(survives_own)"
echo "  control — firewalld's own table present: $(docker exec $CT nft list tables | grep -c firewalld)"

say "PART 2 — iptables backend: a rule sharing the manager's table (docker's situation)"
docker exec $CT sh -c 'sed -i s/^FirewallBackend=.*/FirewallBackend=iptables/ /etc/firewalld/firewalld.conf'
start_fwd || exit 1
echo "  tables with iptables backend:"
docker exec $CT nft list tables | sed 's/^/    /'

# a foreign chain inside the SHARED filter table, the way docker installs DOCKER-USER
docker exec $CT iptables -N YB_FOREIGN 2>/dev/null
docker exec $CT iptables -A YB_FOREIGN -s 10.99.99.99 -j DROP
docker exec $CT iptables -A FORWARD -j YB_FOREIGN 2>/dev/null
echo "  foreign chain installed in the shared filter table:"
docker exec $CT iptables -S YB_FOREIGN 2>&1 | sed 's/^/    /'

install_own_table
docker exec $CT firewall-cmd --reload >/dev/null 2>&1
sleep 2
echo "  after firewall-cmd --reload:"
if docker exec $CT iptables -S YB_FOREIGN >/dev/null 2>&1; then
	echo "    shared-table foreign chain: SURVIVED"
	docker exec $CT iptables -S YB_FOREIGN 2>&1 | sed 's/^/      /'
else
	echo "    shared-table foreign chain: DESTROYED"
fi
echo "    our own dedicated table:    $(survives_own)"
echo "  control — is the FORWARD jump to our chain still there?"
docker exec $CT iptables -S FORWARD 2>&1 | grep -c YB_FOREIGN | sed 's/^/    jumps: /'

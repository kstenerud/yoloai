#!/usr/bin/env bash
# ABOUTME: L1b — prerouting accepts a cgroup match that forward rejects. Does it
# ABOUTME: ever fire for forwarded container traffic, or is it a rule that loads
# ABOUTME: clean and silently never matches? Same trap shape as the macOS M3 anchor.
set -uo pipefail

TABLE=yb_l1b
PROBE_CG=/sys/fs/cgroup/yb_l1b_probe
say() { printf '\n=== %s ===\n' "$*"; }
cleanup() {
	sudo nft delete table inet $TABLE 2>/dev/null
	docker rm -f yb_l1b_a >/dev/null 2>&1
	sudo rmdir $PROBE_CG 2>/dev/null
}
trap cleanup EXIT
cleanup

sudo mkdir -p $PROBE_CG
docker run -d --name yb_l1b_a alpine sleep 600 >/dev/null
A_IP=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' yb_l1b_a)
A_PID=$(docker inspect -f '{{.State.Pid}}' yb_l1b_a)
A_CG=$(awk -F: '$1=="0"{print $3}' "/proc/$A_PID/cgroup")
A_REL=${A_CG#/}
A_LEVEL=$(awk -F/ '{print NF}' <<<"$A_REL")
echo "A ip=$A_IP cgroup=$A_CG (level $A_LEVEL)"

say "load a prerouting chain with three counters: A-by-cgroup, A-by-address, probe-by-cgroup"
sudo nft add table inet $TABLE
sudo nft add chain inet $TABLE c_pre '{ type filter hook prerouting priority -10; policy accept; }'
sudo nft add rule inet $TABLE c_pre socket cgroupv2 level "$A_LEVEL" "\"$A_REL\"" counter comment '"A-by-cgroup"' \
	&& echo "A-by-cgroup rule LOADED in prerouting"
sudo nft add rule inet $TABLE c_pre ip saddr "$A_IP" counter comment '"A-by-address"'
sudo nft add rule inet $TABLE c_pre socket cgroupv2 level 1 '"yb_l1b_probe"' counter comment '"probe-by-cgroup"'

say "TEST: container A sends 3 pings"
docker exec yb_l1b_a ping -c 3 -W 2 1.1.1.1 >/dev/null 2>&1; echo "A ping rc=$?"

say "POSITIVE CONTROL: a host process in the probe cgroup sends 3 pings"
# prerouting sees inbound packets, so the control is the *reply* traffic arriving
# for a socket owned by the probe cgroup.
sudo bash -c "echo \$\$ > $PROBE_CG/cgroup.procs; ping -c 3 -W 2 1.1.1.1 >/dev/null 2>&1"

say "counters"
sudo nft list chain inet $TABLE c_pre

say "verdict"
CG=$(sudo nft list chain inet $TABLE c_pre | grep 'A-by-cgroup' | grep -oP 'packets \K[0-9]+')
AD=$(sudo nft list chain inet $TABLE c_pre | grep 'A-by-address' | grep -oP 'packets \K[0-9]+')
PR=$(sudo nft list chain inet $TABLE c_pre | grep 'probe-by-cgroup' | grep -oP 'packets \K[0-9]+')
echo "A-by-cgroup=$CG  A-by-address=$AD  probe-by-cgroup=$PR"
if [ "$AD" -gt 0 ] && [ "$CG" -eq 0 ]; then
	echo "VERDICT: prerouting sees A's packets (address counter moved) but the cgroup"
	echo "         match never fires — the rule loads clean and is inert."
elif [ "$CG" -gt 0 ]; then
	echo "VERDICT: cgroup match DOES fire in prerouting for forwarded traffic."
else
	echo "VERDICT: inconclusive — prerouting saw nothing at all; controls invalid."
fi

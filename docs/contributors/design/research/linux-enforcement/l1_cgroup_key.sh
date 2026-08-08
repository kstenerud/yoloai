#!/usr/bin/env bash
# ABOUTME: L1 — can nftables policy key on a container's cgroup instead of its
# ABOUTME: source address? Maps which hooks accept `socket cgroupv2`, proves the
# ABOUTME: match works where accepted, and measures cgroup vs address recycling.
set -uo pipefail

TABLE=yb_l1
PROBE_CG=/sys/fs/cgroup/yb_l1_probe
say() { printf '\n=== %s ===\n' "$*"; }
cleanup() {
	sudo nft delete table inet $TABLE 2>/dev/null
	docker rm -f yb_l1_a yb_l1_b >/dev/null 2>&1
	sudo rmdir $PROBE_CG 2>/dev/null
}
trap cleanup EXIT
cleanup

say "host cgroup layout"
stat -fc 'cgroup fs type: %T' /sys/fs/cgroup
echo -n 'net_cls (cgroup v1 — what the meta cgroup match reads): '
if [ -d /sys/fs/cgroup/net_cls ]; then echo present; else echo ABSENT; fi
echo "kernel: $(uname -r)   nft: $(nft --version)"

sudo mkdir -p $PROBE_CG

say "which hooks accept a cgroup match? (rule-load time, not match time)"
for hook in prerouting input forward output postrouting; do
	sudo nft delete table inet ${TABLE}_h 2>/dev/null
	sudo nft add table inet ${TABLE}_h
	if ! sudo nft add chain inet ${TABLE}_h c "{ type filter hook $hook priority 0; policy accept; }" 2>/dev/null; then
		printf '%-12s chain unavailable in this family\n' "$hook"
		continue
	fi
	err=$(sudo nft add rule inet ${TABLE}_h c socket cgroupv2 level 1 '"yb_l1_probe"' counter 2>&1)
	if [ -z "$err" ]; then
		printf '%-12s ACCEPTED\n' "$hook"
	else
		printf '%-12s REJECTED: %s\n' "$hook" "$(head -1 <<<"$err")"
	fi
	sudo nft delete table inet ${TABLE}_h 2>/dev/null
done

say "start two containers"
docker run -d --name yb_l1_a alpine sleep 600 >/dev/null
docker run -d --name yb_l1_b alpine sleep 600 >/dev/null
A_IP=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' yb_l1_a)
B_IP=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' yb_l1_b)
A_PID=$(docker inspect -f '{{.State.Pid}}' yb_l1_a)
A_CG=$(awk -F: '$1=="0"{print $3}' "/proc/$A_PID/cgroup")
A_REL=${A_CG#/}
A_LEVEL=$(awk -F/ '{print NF}' <<<"$A_REL")
echo "A ip=$A_IP cgroup=$A_CG (level $A_LEVEL)"
echo "B ip=$B_IP"

say "can a container's own cgroup be matched in the hook that sees its traffic?"
echo "-- forward hook, keyed on A's cgroup:"
sudo nft add table inet $TABLE
sudo nft add chain inet $TABLE c_forward '{ type filter hook forward priority -10; policy accept; }'
sudo nft add rule inet $TABLE c_forward socket cgroupv2 level "$A_LEVEL" "\"$A_REL\"" counter 2>&1 | head -2

say "POSITIVE CONTROL: the match does work where the kernel accepts it (output hook)"
sudo nft add chain inet $TABLE c_output '{ type filter hook output priority 0; policy accept; }'
sudo nft add rule inet $TABLE c_output socket cgroupv2 level 1 '"yb_l1_probe"' counter comment '"probe-cgroup"'
sudo bash -c "echo \$\$ > $PROBE_CG/cgroup.procs; ping -c 3 -W 2 1.1.1.1 >/dev/null 2>&1"
echo "counter after 3 pings FROM the probe cgroup:"
sudo nft list chain inet $TABLE c_output | grep counter
ping -c 3 -W 2 1.1.1.1 >/dev/null 2>&1
echo "counter after 3 more pings from OUTSIDE it (must be unchanged):"
sudo nft list chain inet $TABLE c_output | grep counter

say "does forwarded container traffic reach an output-hook cgroup rule at all?"
sudo nft add rule inet $TABLE c_output ip saddr "$A_IP" counter comment '"A-in-output"'
docker exec yb_l1_a ping -c 3 -W 2 1.1.1.1 >/dev/null 2>&1
echo "A-in-output counter after A pings (0 = container traffic never enters the output hook):"
sudo nft list chain inet $TABLE c_output | grep 'A-in-output'

say "recycling: destroy A, recreate immediately, compare both keys"
docker rm -f yb_l1_a >/dev/null
docker run -d --name yb_l1_a alpine sleep 600 >/dev/null
A2_PID=$(docker inspect -f '{{.State.Pid}}' yb_l1_a)
A2_CG=$(awk -F: '$1=="0"{print $3}' "/proc/$A2_PID/cgroup")
A2_IP=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' yb_l1_a)
echo "A  cgroup=$A_CG"
echo "A' cgroup=$A2_CG"
echo "A  ip=$A_IP    A' ip=$A2_IP"
[ "$A_CG" = "$A2_CG" ] && echo "RESULT: cgroup path recycled" || echo "RESULT: cgroup path NOT recycled"
[ "$A_IP" = "$A2_IP" ] && echo "RESULT: ADDRESS RECYCLED" || echo "RESULT: address not recycled"

say "final ruleset"
sudo nft list table inet $TABLE

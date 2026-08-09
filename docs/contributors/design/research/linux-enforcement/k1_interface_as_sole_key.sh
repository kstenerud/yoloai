#!/usr/bin/env bash
# ABOUTME: K1 — the experiment L1 should have run: is a per-sandbox INTERFACE a
# ABOUTME: usable key on Linux, with no address in the rule? This is macOS's M1/K4,
# ABOUTME: which discriminated there and failed only because vmnet bridge indices recycle.
set -uo pipefail

say() { printf '\n=== %s ===\n' "$*"; }
NA=yb_k1_neta
NB=yb_k1_netb
cleanup() {
	sudo nft delete table inet yb_k1 2>/dev/null
	docker rm -f yb_k1_a yb_k1_b >/dev/null 2>&1
	docker network rm $NA $NB >/dev/null 2>&1
	for i in 1 2 3 4; do docker network rm "yb_k1_r$i" >/dev/null 2>&1; done
}
trap cleanup EXIT
cleanup

ALLOW=$(getent ahostsv4 example.com | awk '{print $1; exit}')
DENY=1.1.1.1
# Every probe below is TCP, test and control alike — the pass's own rule, which its
# earlier Linux runs did not follow.
get() { docker exec "$1" curl -s -m 5 -o /dev/null ${3:+--interface "$3"} "http://$2/" >/dev/null 2>&1 && echo REACHABLE || echo blocked; }

######################################################################
say "PART 1 — one network per sandbox; is 'iifname <bridge>' a sufficient key?"
######################################################################
docker network create --subnet 10.201.0.0/24 $NA >/dev/null
docker network create --subnet 10.202.0.0/24 $NB >/dev/null
BRA=br-$(docker network inspect $NA --format '{{.Id}}' | cut -c1-12)
BRB=br-$(docker network inspect $NB --format '{{.Id}}' | cut -c1-12)
docker run -d --name yb_k1_a --cap-add NET_ADMIN --network $NA alpine sleep 500 >/dev/null
docker run -d --name yb_k1_b --cap-add NET_ADMIN --network $NB alpine sleep 500 >/dev/null
for c in yb_k1_a yb_k1_b; do docker exec $c apk add --no-cache curl >/dev/null 2>&1; done
docker exec yb_k1_a sh -c 'command -v curl >/dev/null' || { echo "ABORT: curl missing in A"; exit 1; }
docker exec yb_k1_b sh -c 'command -v curl >/dev/null' || { echo "ABORT: curl missing in B"; exit 1; }
ip -br link show "$BRA" >/dev/null 2>&1 || { echo "ABORT: bridge $BRA not present; key untestable"; exit 1; }
A=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' yb_k1_a)
B=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' yb_k1_b)
echo "A=$A on $BRA    B=$B on $BRB    allowlisted=$ALLOW  denied=$DENY"

echo "-- baseline, no policy --"
echo "   A->allow $(get yb_k1_a "$ALLOW")   A->deny $(get yb_k1_a $DENY)   B->deny $(get yb_k1_b $DENY)"

echo "-- policy keyed ONLY on A's bridge. No address appears in any rule. --"
sudo nft -f - <<NFT
table inet yb_k1 {
	set allowed { type ipv4_addr; elements = { $ALLOW } }
	chain c_forward {
		type filter hook forward priority -10; policy accept;
		iifname "$BRA" ct state established,related accept
		iifname "$BRA" udp dport 53 accept
		iifname "$BRA" ip daddr @allowed counter accept comment "A-allowlisted"
		iifname "$BRA" counter drop comment "A-denied"
	}
}
NFT
sudo nft list table inet yb_k1 | grep -c 'ip saddr' | sed 's/^/   address-matching rules in the table: /'

RA_ALLOW=$(get yb_k1_a "$ALLOW"); RA_DENY=$(get yb_k1_a $DENY); RB_DENY=$(get yb_k1_b $DENY)
echo "   A->allow $RA_ALLOW   A->deny $RA_DENY   B->deny $RB_DENY  <- B is the discrimination control"
sudo nft list table inet yb_k1 | grep counter | sed 's/^\s*/     /'

if [ "$RA_ALLOW" = REACHABLE ] && [ "$RA_DENY" = blocked ] && [ "$RB_DENY" = REACHABLE ]; then
	K1_VERDICT="the interface alone discriminates per sandbox, with no address in the rule"
elif [ "$RB_DENY" = blocked ]; then
	K1_VERDICT="the rule bit BOTH sandboxes — the key is not per-sandbox as configured"
else
	K1_VERDICT="inconclusive — see controls above"
fi
echo "   K1: $K1_VERDICT"

######################################################################
say "PART 2 — does the bridge name recycle? (macOS's K4 failed exactly here)"
######################################################################
NAMES=""
for i in 1 2 3 4; do
	docker network create --subnet "10.21$i.0.0/24" "yb_k1_r$i" >/dev/null 2>&1
	n=br-$(docker network inspect "yb_k1_r$i" --format '{{.Id}}' | cut -c1-12)
	echo "   create #$i -> $n"
	NAMES="$NAMES $n"
	docker network rm "yb_k1_r$i" >/dev/null 2>&1
done
UNIQ=$(echo "$NAMES" | tr ' ' '\n' | grep . | sort -u | wc -l)
TOT=$(echo "$NAMES" | tr ' ' '\n' | grep -c .)
echo "   $TOT created and destroyed in sequence, $UNIQ distinct names"
[ "$UNIQ" = "$TOT" ] && RECYCLE="names do NOT recycle" || RECYCLE="names RECYCLE — same failure as vmnet"
echo "   K1 recycling: $RECYCLE"

######################################################################
say "PART 3 — can the guest escape an interface-keyed rule?"
######################################################################
echo "-- 3a: take an address the policy does not name (defeats address keying) --"
docker exec --user root yb_k1_a ip addr add 10.201.0.99/24 dev eth0 2>&1 | sed 's/^/     /'
HELD=$(docker exec yb_k1_a ip -4 addr show eth0 | grep -c 10.201.0.99)
echo "     spoof address held: $HELD (0 would make the next line free)"
S=$(get yb_k1_a $DENY 10.201.0.99)
echo "     A->deny from 10.201.0.99: $S"

echo "-- 3b: change its MAC (defeats ether-saddr keying) --"
docker exec --user root yb_k1_a sh -c 'ip link set dev eth0 down; ip link set dev eth0 address 02:11:22:33:44:55; ip link set dev eth0 up' 2>&1 | sed 's/^/     /'
NEWMAC=$(docker exec yb_k1_a cat /sys/class/net/eth0/address 2>/dev/null)
echo "     A's MAC is now $NEWMAC"
sleep 2
S2=$(get yb_k1_a $DENY)
echo "     A->deny after MAC change: $S2"

echo "-- 3c: can it change which HOST-side interface its packets arrive on? --"
echo "     the veth peer and the bridge live in the host netns; A holds:"
docker exec yb_k1_a sh -c 'grep CapEff /proc/self/status' | sed 's/^/       /'
echo "     host-side devices visible to A: $(docker exec yb_k1_a ls /sys/class/net | tr '\n' ' ')"

######################################################################
say "PART 4 — meta cgroup, actually executed as a rule this time"
######################################################################
echo "L1 asserted this matcher was unusable from a directory-existence test."
if sudo nft add rule inet yb_k1 c_forward meta cgroup 1234 counter comment '"meta-cgroup"' 2>&1; then
	echo "   rule LOADED. sending traffic..."
	get yb_k1_a $DENY >/dev/null
	sudo nft list table inet yb_k1 | grep 'meta-cgroup' | sed 's/^\s*/     /'
	echo "   READ: a non-zero counter would mean meta cgroup matches here; zero with"
	echo "   traffic flowing means it loads and never fires, which is a measurement"
	echo "   rather than the directory test L1 relied on."
else
	echo "   rule REFUSED by the kernel — that is the measurement L1 should have made."
fi

######################################################################
say "SUMMARY"
######################################################################
echo "K1 (bridge as sole key)      : $K1_VERDICT"
echo "K1 recycling                 : $RECYCLE"
echo "3a address spoof vs iifname  : $S (REACHABLE would mean interface keying is ALSO defeasible)"
echo "3b MAC change vs iifname     : $S2"

######################################################################
say "WHAT WAS NOT TRIED"
######################################################################
cat <<'NOT'
  - veth-level keying (physdev --physdev-in / nft bridge family) on a SHARED bridge,
    which is what would remove the one-network-per-sandbox requirement. Needs
    br_netfilter; covered separately in k2_veth_key.sh.
  - ct mark / meta mark applied at claim time. Circular unless the mark is itself
    applied by interface or cgroup, so it is downstream of this result.
  - eBPF cgroup_skb/egress (systemd's IPAddressAllow mechanism). Different toolchain,
    and it cannot cover the VM backends, so it is a second question not this one.
  - Whether bridge names collide across a docker daemon restart, or across hosts.
  - podman and containerd: this run is docker-only. containerd uses one shared CNI
    bridge (yoloai0) for every sandbox, so a per-sandbox bridge key does NOT exist
    there as currently configured — that is a design change, not a measurement.
  - Any claim about macOS. vmnet recycles bridge indices; that was measured there
    and this run says nothing about it.
NOT

#!/usr/bin/env bash
# ABOUTME: K2 — K1 showed a per-sandbox bridge is a sound key but needs one network
# ABOUTME: per sandbox, which containerd (one shared yoloai0) does not do. Asks whether
# ABOUTME: the veth port is a key on a SHARED bridge, which would remove that requirement.
set -uo pipefail

say() { printf '\n=== %s ===\n' "$*"; }
LOADED_BY_US=0
cleanup() {
	sudo nft delete table bridge yb_k2 2>/dev/null
	sudo iptables -D FORWARD -m physdev --physdev-in "${VETH_A:-none}" -j DROP 2>/dev/null
	docker rm -f yb_k2_a yb_k2_b >/dev/null 2>&1
	for i in 1 2 3; do docker rm -f "yb_k2_r$i" >/dev/null 2>&1; done
	if [ "$LOADED_BY_US" = 1 ]; then
		sudo sysctl -qw net.bridge.bridge-nf-call-iptables=0 2>/dev/null
		sudo modprobe -r br_netfilter 2>/dev/null && echo "restored: br_netfilter unloaded"
	fi
}
VETH_A=""
trap cleanup EXIT
cleanup

# host-side veth peer of a container: eth0's iflink is the peer's ifindex in the host netns
host_veth() { local idx; idx=$(docker exec "$1" cat /sys/class/net/eth0/iflink 2>/dev/null | tr -d '\r'); ip -o link | awk -v i="$idx" -F': ' '$1==i{split($2,a,"@"); print a[1]}'; }

DENY=1.1.1.1
get() { docker exec "$1" curl -s -m 5 -o /dev/null "http://$2/" >/dev/null 2>&1 && echo REACHABLE || echo blocked; }

say "two sandboxes on ONE shared bridge (docker0) — containerd's shape"
docker run -d --name yb_k2_a --cap-add NET_ADMIN alpine sleep 500 >/dev/null
docker run -d --name yb_k2_b --cap-add NET_ADMIN alpine sleep 500 >/dev/null
for c in yb_k2_a yb_k2_b; do docker exec $c apk add --no-cache curl >/dev/null 2>&1; done
docker exec yb_k2_a sh -c 'command -v curl >/dev/null' || { echo "ABORT: curl missing"; exit 1; }
VETH_A=$(host_veth yb_k2_a); VETH_B=$(host_veth yb_k2_b)
[ -n "$VETH_A" ] && [ -n "$VETH_B" ] || { echo "ABORT: could not resolve host veth names (A='$VETH_A' B='$VETH_B')"; exit 1; }
echo "A host-side veth: $VETH_A     B host-side veth: $VETH_B"
echo "both enslaved to: $(ip -o link show "$VETH_A" | grep -o 'master [^ ]*')"

say "load br_netfilter so bridge ports are visible to netfilter"
lsmod | grep -qw br_netfilter || { sudo modprobe br_netfilter && LOADED_BY_US=1; }
sudo sysctl -qw net.bridge.bridge-nf-call-iptables=1
echo "  br_netfilter: $(lsmod | grep -qw br_netfilter && echo loaded || echo MISSING)   bridge-nf-call-iptables=$(sysctl -n net.bridge.bridge-nf-call-iptables)"

say "PART 1 — physdev: does the veth port discriminate on a shared bridge?"
echo "  baseline: A->deny $(get yb_k2_a $DENY)   B->deny $(get yb_k2_b $DENY)"
if sudo iptables -I FORWARD 1 -m physdev --physdev-in "$VETH_A" -j DROP 2>&1; then
	echo "  rule installed: -m physdev --physdev-in $VETH_A -j DROP"
	RA=$(get yb_k2_a $DENY); RB=$(get yb_k2_b $DENY)
	echo "  A->deny $RA   B->deny $RB  <- B is the discrimination control"
	echo "  packet counter on the rule:"
	sudo iptables -L FORWARD -v -n --line-numbers 2>/dev/null | grep physdev | sed 's/^/    /'
	if [ "$RA" = blocked ] && [ "$RB" = REACHABLE ]; then
		K2="physdev discriminates per sandbox on a SHARED bridge"
	elif [ "$RA" = blocked ] && [ "$RB" = blocked ]; then
		K2="rule bit both — not per-sandbox"
	else
		K2="did not bite; see counter above"
	fi
	sudo iptables -D FORWARD -m physdev --physdev-in "$VETH_A" -j DROP 2>/dev/null
else
	K2="physdev match unavailable on this host"
fi
echo "  K2/physdev: $K2"

say "PART 2 — nft bridge family keyed on the veth port"
if sudo nft -f - <<NFT 2>&1
table bridge yb_k2 {
	chain c_fwd {
		type filter hook forward priority -10; policy accept;
		iifname "$VETH_A" ip daddr $DENY counter drop comment "A-by-veth"
	}
}
NFT
then
	RA2=$(get yb_k2_a $DENY); RB2=$(get yb_k2_b $DENY)
	echo "  A->deny $RA2   B->deny $RB2"
	sudo nft list table bridge yb_k2 | grep counter | sed 's/^\s*/    /'
	if [ "$RA2" = blocked ] && [ "$RB2" = REACHABLE ]; then K2B="bridge-family iifname discriminates per sandbox"
	else K2B="did not discriminate (A=$RA2 B=$RB2)"; fi
else
	K2B="bridge family rule refused"
fi
echo "  K2/bridge-family: $K2B"
sudo nft delete table bridge yb_k2 2>/dev/null

say "PART 3 — do veth names recycle?"
NAMES=""
for i in 1 2 3; do
	docker run -d --name "yb_k2_r$i" alpine sleep 60 >/dev/null 2>&1
	n=$(host_veth "yb_k2_r$i"); echo "   container #$i -> $n"
	NAMES="$NAMES $n"
	docker rm -f "yb_k2_r$i" >/dev/null 2>&1
done
U=$(echo "$NAMES" | tr ' ' '\n' | grep -c .); D=$(echo "$NAMES" | tr ' ' '\n' | grep . | sort -u | wc -l)
[ "$U" = "$D" ] && VR="veth names do NOT recycle ($D distinct of $U)" || VR="veth names RECYCLE ($D distinct of $U)"
echo "   $VR"

say "PART 4 — can the guest reach or change its host-side veth?"
echo "  devices visible inside A: $(docker exec yb_k2_a ls /sys/class/net | tr '\n' ' ')"
echo "  can A rename the host-side peer? (it has no handle on it)"
docker exec --user root yb_k2_a sh -c "ip link set dev $VETH_A name evil 2>&1" | sed 's/^/    /'

say "SUMMARY"
echo "  physdev on shared bridge : $K2"
echo "  nft bridge family        : $K2B"
echo "  veth recycling           : $VR"

say "WHAT WAS NOT TRIED"
cat <<'NOT'
  - Whether br_netfilter being loaded host-wide breaks anything else. It changes
    netfilter's view of ALL bridged traffic on the host, not just ours, and L8 already
    showed it makes docker's own rules apply to sibling traffic. Cost unmeasured.
  - containerd/CNI: this run used docker's veths. CNI names its veths differently and
    the yoloai0 bridge is shared; the mechanism should carry but was not run there.
  - rootless podman: veths inside the rootless netns, untested for this key.
  - Whether physdev survives a docker restart, or a container restart in place.
  - Any IPv6 case.
  - Whether the veth ifindex (as opposed to the name) recycles, which is what a
    rule compiled to an index rather than a name would depend on.
NOT

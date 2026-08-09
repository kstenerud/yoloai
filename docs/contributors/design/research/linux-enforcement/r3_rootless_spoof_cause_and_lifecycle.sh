#!/usr/bin/env bash
# ABOUTME: R3 — R2 saw a spoofed source blocked in the rootless netns BEFORE rule 0b
# ABOUTME: was installed, which is containment we did not build and must not bank.
# ABOUTME: Finds the cause with no policy loaded at all, then measures netns lifecycle.
set -uo pipefail

say() { printf '\n=== %s ===\n' "$*"; }
NET=yb_rnet3
NS_DIR=/run/user/$(id -u)/netns
cleanup() {
	podman unshare --rootless-netns nft delete table inet yb_rl3 2>/dev/null
	podman rm -f yb_r3a yb_r3b >/dev/null 2>&1
	podman network rm $NET >/dev/null 2>&1
}
trap cleanup EXIT
cleanup

DENY=1.1.1.1
podman network create $NET --subnet 10.92.0.0/24 >/dev/null 2>&1
podman run -d --name yb_r3a --cap-add NET_ADMIN --network $NET alpine sleep 400 >/dev/null
A=$(podman inspect yb_r3a --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}')
echo "A=$A   (no yoloAI policy is loaded anywhere in this section)"

######################################################################
say "PART 1 — is a spoofed source blocked with NO policy of ours at all?"
######################################################################
echo "  our tables in the rootless netns: $(podman unshare --rootless-netns nft list tables | grep -c yb_rl3 || true) (expect 0)"
echo "  baseline, own address -> $DENY : $(podman exec yb_r3a ping -c2 -W3 $DENY >/dev/null 2>&1 && echo REACHABLE || echo blocked)"
podman exec --user root yb_r3a ip addr add 10.92.0.99/24 dev eth0 2>&1 | sed 's/^/  /'
podman exec yb_r3a ip -4 addr show eth0 | grep inet | sed 's/^/    /'
echo "  spoofed 10.92.0.99 -> $DENY   : $(podman exec yb_r3a ping -c2 -W3 -I 10.92.0.99 $DENY >/dev/null 2>&1 && echo REACHABLE || echo blocked)"
echo
echo "  If the spoof is blocked here, the block is NOT ours. The cause is below."

say "PART 2 — what netavark installed, and whether it is keyed per-address"
echo "-- NAT rules mentioning the subnet or the container address --"
podman unshare --rootless-netns nft list table ip nat 2>/dev/null | grep -E '10\.92\.|masquerade|snat' | sed 's/^\s*/  /'
echo "-- filter rules --"
podman unshare --rootless-netns nft list table ip filter 2>/dev/null | grep -E '10\.92\.|drop|accept' | sed 's/^\s*/  /' | head -12
echo
echo "  READ: if masquerade names $A rather than 10.92.0.0/24, then a spoofed source"
echo "  simply leaves un-NATed and slirp4netns drops it — a functional accident, not"
echo "  a security control, and it would evaporate if netavark ever widened that rule."

say "PART 3 — does the reverse hold? spoof an address netavark DOES masquerade"
SIBLING=10.92.0.3
echo "  adding $SIBLING (an address inside the subnet that no container holds)"
podman exec --user root yb_r3a ip addr add $SIBLING/24 dev eth0 2>&1 | sed 's/^/  /'
echo "  spoofed $SIBLING -> $DENY : $(podman exec yb_r3a ping -c2 -W3 -I $SIBLING $DENY >/dev/null 2>&1 && echo REACHABLE || echo blocked)"

######################################################################
say "PART 4 — lifecycle: does the netns and our table survive churn?"
######################################################################
podman unshare --rootless-netns nft -f - <<NFT
table inet yb_rl3 {
	chain c_forward {
		type filter hook forward priority -10; policy accept;
		ip saddr $A counter drop comment "marker"
	}
}
NFT
echo "  marker table installed: $(podman unshare --rootless-netns nft list tables | grep -c yb_rl3)"
echo "  netns files on disk:"
find "$NS_DIR" -maxdepth 1 -mindepth 1 -printf "%f\n" 2>/dev/null | sed 's/^/    /' || echo "    (none)"

echo "  -- stop the only container --"
podman stop yb_r3a >/dev/null 2>&1; sleep 4
echo "  netns files after stop:"
find "$NS_DIR" -maxdepth 1 -mindepth 1 -printf "%f\n" 2>/dev/null | sed 's/^/    /' || echo "    (none — torn down)"

echo "  -- start it again --"
podman start yb_r3a >/dev/null 2>&1; sleep 4
A2=$(podman inspect yb_r3a --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}')
echo "  A came back as $A2 (was $A)"
if podman unshare --rootless-netns nft list table inet yb_rl3 >/dev/null 2>&1; then
	echo "  marker table SURVIVED the stop/start"
	echo "  is A still contained by it? $(podman exec yb_r3a ping -c2 -W3 $DENY >/dev/null 2>&1 && echo 'no — REACHABLE' || echo 'yes — blocked')"
else
	echo "  marker table GONE — enforcement must be reinstalled on every bring-up,"
	echo "  and the reinstall has to happen before the agent runs."
fi

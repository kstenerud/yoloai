#!/usr/bin/env bash
# ABOUTME: L4 — does address recycling behave the same on podman and containerd as
# ABOUTME: it does on docker (lowest-free, immediate reuse)? Also asks whether two
# ABOUTME: backends on one host can ever hand out the same address to live sandboxes.
set -uo pipefail

say() { printf '\n=== %s ===\n' "$*"; }
cleanup() {
	docker rm -f yb_l4_a yb_l4_b >/dev/null 2>&1
	podman rm -f yb_l4_a yb_l4_b >/dev/null 2>&1
	sudo nerdctl rm -f yb_l4_a yb_l4_b >/dev/null 2>&1
}
trap cleanup EXIT
cleanup

# $1 label, $2 run-cmd, $3 addr-cmd (takes name), $4 rm-cmd
probe_backend() {
	local label=$1 runc=$2 addrc=$3 rmc=$4
	say "$label"
	if ! $runc yb_l4_a >/dev/null 2>&1; then echo "  backend unavailable — skipped"; return; fi
	$runc yb_l4_b >/dev/null 2>&1
	local a b a2
	a=$($addrc yb_l4_a); b=$($addrc yb_l4_b)
	echo "  A=$a  B=$b"
	$rmc yb_l4_a >/dev/null 2>&1
	$runc yb_l4_a >/dev/null 2>&1
	a2=$($addrc yb_l4_a)
	echo "  A destroyed and recreated while B still holds $b: A'=$a2"
	if [ "$a" = "$a2" ]; then
		echo "  RESULT: address RECYCLED immediately (same allocation returned)"
	else
		echo "  RESULT: address not reused on the next allocation"
	fi
	echo "  subnet in use: $(echo "$a" | cut -d. -f1-2).x"
	eval "SUBNET_${label%% *}=\$a"
	$rmc yb_l4_a >/dev/null 2>&1; $rmc yb_l4_b >/dev/null 2>&1
}

d_run() { docker run -d --name "$1" alpine sleep 300; }
d_addr() { docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$1"; }
d_rm() { docker rm -f "$1"; }
probe_backend "docker" d_run d_addr d_rm

p_run() { podman run -d --name "$1" alpine sleep 300; }
p_addr() { podman inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$1"; }
p_rm() { podman rm -f "$1"; }
probe_backend "podman" p_run p_addr p_rm

n_run() { sudo nerdctl run -d --name "$1" alpine sleep 300; }
n_addr() { sudo nerdctl inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$1"; }
n_rm() { sudo nerdctl rm -f "$1"; }
probe_backend "containerd" n_run n_addr n_rm

say "CROSS-BACKEND COLLISION: can two backends hand out the same address at once?"
echo "run one live sandbox on each backend and compare addresses"
docker run -d --name yb_l4_a alpine sleep 300 >/dev/null 2>&1 && DA=$(d_addr yb_l4_a) || DA="(docker unavailable)"
podman run -d --name yb_l4_b alpine sleep 300 >/dev/null 2>&1 && PA=$(p_addr yb_l4_b) || PA="(podman unavailable)"
sudo nerdctl run -d --name yb_l4_a alpine sleep 300 >/dev/null 2>&1 && NA=$(n_addr yb_l4_a) || NA="(containerd unavailable)"
echo "  docker     : $DA"
echo "  podman     : $PA"
echo "  containerd : $NA"
DUPES=$(printf '%s\n%s\n%s\n' "$DA" "$PA" "$NA" | grep -E '^[0-9]' | sort | uniq -d)
if [ -n "$DUPES" ]; then
	echo "  RESULT: COLLISION — the same address is live on more than one backend: $DUPES"
	echo "          a blind cross-backend scrub would delete a LIVE sandbox's entry."
else
	echo "  RESULT: no collision — each backend allocates from its own subnet."
	echo "          default bridges are disjoint, so an address identifies at most one live sandbox."
fi

say "host routing table — are these subnets actually distinct?"
ip -4 route | grep -E 'docker|podman|cni|nerdctl|br-|yoloai' || ip -4 route

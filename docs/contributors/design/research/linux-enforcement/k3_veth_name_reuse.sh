#!/usr/bin/env bash
# ABOUTME: K3 — the rewrite claims "Linux needs no lifecycle rule because its names do
# ABOUTME: not recycle", from three docker cycles. The same document says netavark lets the
# ABOUTME: kernel assign vethN, lowest-free. Tests reuse per backend, since one contradicts the other.
set -uo pipefail

say() { printf '\n=== %s ===\n' "$*"; }
CYCLES=6
cleanup() {
	for i in $(seq 1 $CYCLES); do
		docker rm -f "yb_k3_d$i" >/dev/null 2>&1
		podman rm -f "yb_k3_p$i" >/dev/null 2>&1
		sudo nerdctl rm -f "yb_k3_n$i" >/dev/null 2>&1
	done
	podman network rm yb_k3net >/dev/null 2>&1
	sudo nerdctl network rm yb_k3net >/dev/null 2>&1
}
trap cleanup EXIT
cleanup

# host-side veth peer of a container, via eth0's iflink -> ifindex in the relevant netns
veth_in_host() { local idx; idx=$(docker exec "$1" cat /sys/class/net/eth0/iflink 2>/dev/null | tr -d '\r'); ip -o link | awk -v i="$idx" -F': ' '$1==i{split($2,a,"@"); print a[1]}'; }
veth_nerdctl()  { local idx; idx=$(sudo nerdctl exec "$1" cat /sys/class/net/eth0/iflink 2>/dev/null | tr -d '\r'); ip -o link | awk -v i="$idx" -F': ' '$1==i{split($2,a,"@"); print a[1]}'; }
veth_podman()   { local idx; idx=$(podman exec "$1" cat /sys/class/net/eth0/iflink 2>/dev/null | tr -d '\r'); podman unshare --rootless-netns ip -o link | awk -v i="$idx" -F': ' '$1==i{split($2,a,"@"); print a[1]}'; }

report() { # $1 label, $2 = one space-separated string of names
	local label="$1" list="$2" tot uniq
	echo
	echo "  $label"
	tot=$(echo "$list" | tr ' ' '\n' | grep -c .)
	uniq=$(echo "$list" | tr ' ' '\n' | grep . | sort -u | wc -l)
	echo "    observed:$list"
	echo "    $tot names over $CYCLES create/destroy cycles, $uniq distinct"
	if [ "$tot" -lt 2 ]; then
		echo "    INCONCLUSIVE — fewer than two names captured; backend may be unavailable"
	elif [ "$tot" = "$uniq" ]; then
		echo "    => names do NOT recycle in this window"
	else
		echo "    => names DO RECYCLE. A stale match rule can re-point at a different sandbox."
	fi
}

say "docker — random hex, 3-attempt collision probe, name persisted by the daemon"
D=""
for i in $(seq 1 $CYCLES); do
	docker run -d --name "yb_k3_d$i" alpine sleep 30 >/dev/null 2>&1
	D="$D $(veth_in_host "yb_k3_d$i")"
	docker rm -f "yb_k3_d$i" >/dev/null 2>&1
done
report "docker" "$D"

say "rootless podman — netavark passes an empty name, so the KERNEL assigns it"
podman network create yb_k3net --subnet 10.98.0.0/24 >/dev/null 2>&1
P=""
for i in $(seq 1 $CYCLES); do
	podman run -d --name "yb_k3_p$i" --network yb_k3net alpine sleep 30 >/dev/null 2>&1
	P="$P $(veth_podman "yb_k3_p$i")"
	podman rm -f "yb_k3_p$i" >/dev/null 2>&1
done
report "rootless podman" "$P"

say "containerd / CNI — RandomVethName, 4 random bytes, 10 attempts, NOT persisted"
N=""
for i in $(seq 1 $CYCLES); do
	sudo nerdctl run -d --name "yb_k3_n$i" alpine sleep 30 >/dev/null 2>&1
	N="$N $(veth_nerdctl "yb_k3_n$i")"
	sudo nerdctl rm -f "yb_k3_n$i" >/dev/null 2>&1
done
report "containerd/CNI" "$N"

say "WHY THIS MATTERS"
cat <<'NOT'
  Rule identity comes from the sandbox ID, so REAPING is unaffected by name reuse.
  What reuse breaks is the MATCH: a rule still loaded, naming a veth that a later
  sandbox now owns, silently applies one sandbox's policy to another. That is the
  macOS I5 hazard, and the rewrite asserts Linux does not have it.

WHAT WAS NOT TRIED
  - Concurrent create/destroy. This is strictly sequential, which is the friendliest
    case for a lowest-free allocator; interleaving would be more adversarial.
  - More than six cycles. A larger sample could find reuse this one misses, so a
    "does not recycle" result here bounds the claim rather than proving it.
  - ifindex reuse as distinct from name reuse. A rule compiled to an index rather
    than a name would depend on the index, which was not captured.
  - Reuse across a daemon restart or a reboot.
NOT

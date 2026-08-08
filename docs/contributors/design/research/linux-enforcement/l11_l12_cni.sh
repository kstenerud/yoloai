#!/usr/bin/env bash
# ABOUTME: L11 — does CNI host-local IPAM wrap and reuse addresses, or allocate
# ABOUTME: forward forever? L12 — is DF9 (CNI firewall plugin installing no
# ABOUTME: CNI-FORWARD accept) still live? Both were left unverified by the first pass.
set -uo pipefail

say() { printf '\n=== %s ===\n' "$*"; }
NET=yb_l11net
cleanup() {
	for i in $(seq 1 12); do sudo nerdctl rm -f "yb_l11_$i" >/dev/null 2>&1; done
	sudo nerdctl network rm $NET >/dev/null 2>&1
}
trap cleanup EXIT
cleanup

######################################################################
say "L11 — does CNI host-local wrap and reuse?"
######################################################################
echo "a /29 leaves ~5 usable addresses, so a wrap is reachable in a few cycles"
sudo nerdctl network create --subnet 10.66.0.0/29 $NET >/dev/null 2>&1 || { echo "  could not create network"; exit 1; }

addr() { sudo nerdctl inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$1" 2>/dev/null; }

echo "-- fill the subnet --"
FIRST_SET=""
for i in 1 2 3 4; do
	sudo nerdctl run -d --name "yb_l11_$i" --network $NET alpine sleep 300 >/dev/null 2>&1
	a=$(addr "yb_l11_$i"); echo "  container $i -> ${a:-<failed>}"
	FIRST_SET="$FIRST_SET $a"
done

echo "-- free the first one and keep allocating --"
FREED=$(addr yb_l11_1)
sudo nerdctl rm -f yb_l11_1 >/dev/null 2>&1
echo "  freed: $FREED"
WRAPPED=no
for i in 5 6 7 8 9 10; do
	sudo nerdctl run -d --name "yb_l11_$i" --network $NET alpine sleep 300 >/dev/null 2>&1
	a=$(addr "yb_l11_$i")
	if [ -z "$a" ]; then echo "  container $i -> exhausted (no address)"; break; fi
	echo "  container $i -> $a"
	[ "$a" = "$FREED" ] && { echo "    ^ this is the freed address, reused"; WRAPPED=yes; }
	sudo nerdctl rm -f "yb_l11_$i" >/dev/null 2>&1
done

echo "  ---"
if [ "$WRAPPED" = yes ]; then
	echo "  RESULT: host-local DOES wrap and reuse. containerd needs clear-before-claim"
	echo "          exactly as docker does; the difference is only how long it takes."
else
	echo "  RESULT: no reuse observed before exhaustion in this window."
fi
echo "  host-local's on-disk record of what it last handed out:"
sudo find /var/lib/cni/networks/$NET -maxdepth 1 -type f 2>/dev/null | sed 's/^/    /' | head -10

######################################################################
say "L12 — is DF9 still live? (CNI firewall plugin installing no accept rule)"
######################################################################
sudo nerdctl rm -f yb_l11_2 >/dev/null 2>&1
sudo nerdctl run -d --name yb_l11_11 --network $NET alpine sleep 300 >/dev/null 2>&1
IP=$(addr yb_l11_11)
echo "probe container address: ${IP:-<none>}"
echo "-- CNI chains present in the shared filter table --"
sudo nft list table ip filter 2>/dev/null | grep -E 'chain (CNI|DOCKER)' | sed 's/^\s*/  /'
echo "-- does any CNI-FORWARD accept name this container? (DF9's symptom is: none) --"
sudo iptables -S 2>/dev/null | grep -E 'CNI-FORWARD|CNI-ADMIN' | sed 's/^/  /' | head -20
if sudo iptables -S 2>/dev/null | grep -q "CNI-FORWARD.*$IP"; then
	echo "  RESULT: an accept naming $IP is present — DF9's symptom is absent here"
else
	echo "  RESULT: no CNI-FORWARD rule names $IP"
	echo "          (matches DF9's symptom; note this is nerdctl's own network, not"
	echo "           yoloAI's conflist, so it is corroboration rather than the case itself)"
fi
echo "-- egress works regardless? --"
sudo nerdctl exec yb_l11_11 nc -z -w 3 1.1.1.1 80 >/dev/null 2>&1 && echo "  egress: REACHABLE" || echo "  egress: blocked"

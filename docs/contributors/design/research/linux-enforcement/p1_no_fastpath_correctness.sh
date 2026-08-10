#!/usr/bin/env bash
# ABOUTME: P1 — does removing the `ct state established,related accept` fast-path break
# ABOUTME: ordinary traffic, and does it make revocation work? Run 1 was invalid: it used a
# ABOUTME: multi-address CDN name, so the allowlisted host was dropped in both arms.
# ABOUTME: This rig has no DNS in the path at all — every destination is a container.
set -uo pipefail

say() { printf '\n=== %s ===\n' "$*"; }
LOADED_BY_US=0
cleanup() {
	sudo nft delete table inet yb_p1 2>/dev/null
	docker rm -f yb_p1_srv yb_p1_den yb_p1_cli >/dev/null 2>&1
	if [ "$LOADED_BY_US" = 1 ]; then
		sudo sysctl -qw net.bridge.bridge-nf-call-iptables=0 2>/dev/null
		sudo modprobe -r br_netfilter 2>/dev/null && echo "restored: br_netfilter unloaded"
	fi
}
trap cleanup EXIT
cleanup

say "rig — three containers on one bridge; allowlisted and denied are both local servers"
lsmod | grep -qw br_netfilter || { sudo modprobe br_netfilter && LOADED_BY_US=1; }
sudo sysctl -qw net.bridge.bridge-nf-call-iptables=1 >/dev/null
# alpine's busybox has no httpd applet (run 2 died on `applet not found`), so use nginx
for n in srv den; do
	docker run -d --name "yb_p1_$n" nginx:alpine >/dev/null
	docker exec "yb_p1_$n" sh -c 'dd if=/dev/urandom of=/usr/share/nginx/html/big bs=1M count=64 2>/dev/null'
done
docker run -d --name yb_p1_cli alpine sleep 900 >/dev/null
docker exec yb_p1_cli apk add --no-cache curl bind-tools >/dev/null 2>&1
docker exec yb_p1_cli sh -c 'command -v curl >/dev/null' || { echo "ABORT: curl missing"; exit 1; }
sleep 3
SRV=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' yb_p1_srv)
DEN=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' yb_p1_den)
CLI=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' yb_p1_cli)
echo "client=$CLI   allowlisted server=$SRV   denied server=$DEN   (no DNS anywhere in this path)"

get()  { docker exec yb_p1_cli curl -s -m 20 -o /dev/null -w '%{http_code}' "http://$1/big" 2>/dev/null; }
size() { docker exec yb_p1_cli curl -s -m 30 -o /dev/null -w '%{size_download}' "http://$1/big" 2>/dev/null; }
dns_q(){ docker exec yb_p1_cli dig +time=3 +tries=1 @1.1.1.1 example.com +short >/dev/null 2>&1 && echo ok || echo FAIL; }

say "BASELINE — no policy. BOTH servers must answer, or nothing below means anything."
B_SRV=$(get "$SRV"); B_DEN=$(get "$DEN"); B_SIZE=$(size "$SRV")
echo "  allowlisted server: HTTP $B_SRV, $B_SIZE bytes"
echo "  denied server     : HTTP $B_DEN"
[ "$B_SRV" = 200 ] || { echo "ABORT: allowlisted server not answering without policy"; exit 1; }
[ "$B_DEN" = 200 ] || { echo "ABORT: denied-server control not answering without policy — 'blocked' would be free"; exit 1; }
[ "${B_SIZE:-0}" -gt 60000000 ] || { echo "ABORT: baseline transfer short ($B_SIZE)"; exit 1; }

load() {
	sudo nft delete table inet yb_p1 2>/dev/null
	local fast=""
	[ "$1" = fastpath ] && fast="ip saddr $CLI ct state established,related counter accept comment \"fastpath\""
	sudo nft -f - <<NFT
table inet yb_p1 {
	set allowed { type ipv4_addr; elements = { $SRV } }
	chain c_forward {
		type filter hook forward priority -10; policy accept;
		$fast
		ip saddr $CLI udp dport 53 accept
		ip saddr $CLI ip daddr @allowed counter accept comment "allowlisted"
		ip saddr $CLI counter drop comment "denied"
	}
}
NFT
}

arm() {
	local s d sz
	s=$(get "$SRV"); d=$(get "$DEN"); sz=$(size "$SRV")
	echo "  allowlisted server : HTTP $s, $sz bytes   <- must be 200 and ~67108864"
	echo "  denied server      : HTTP $d   <- must NOT be 200"
	echo "  dns (udp 53)       : $(dns_q)"
	sudo nft list table inet yb_p1 | grep counter | sed 's/^\s*/    /'
}

say "ARM A — Calico shape: ct state established,related accept in front"
load fastpath; arm

say "ARM B — Cilium shape: no fast-path, destination matched on every packet"
load nofastpath; arm

say "DOES THE REPLY DIRECTION EVER HIT OUR CHAIN?"
sudo nft add rule inet yb_p1 c_forward ip daddr "$CLI" counter comment '"reply-direction"'
size "$SRV" >/dev/null
sudo nft list table inet yb_p1 | grep 'reply-direction' | sed 's/^\s*/  /'
echo "  READ: zero means replies never traverse the chain, so dropping the fast-path"
echo "  cannot affect return traffic. Non-zero means the egress-only reasoning is wrong."

say "THE POINT — revocation while a transfer is in flight"
for shape in fastpath nofastpath; do
	load $shape
	docker exec -d yb_p1_cli sh -c "rm -f /tmp/dl; curl -s --limit-rate 300k -m 90 -o /tmp/dl http://$SRV/big"
	sleep 5
	B1=$(docker exec yb_p1_cli sh -c 'wc -c < /tmp/dl 2>/dev/null || echo 0' | tr -d ' ')
	[ "${B1:-0}" -gt 0 ] || { echo "  $shape: ABORT — transfer never started, revocation result would be free"; continue; }
	sudo nft delete element inet yb_p1 allowed "{ $SRV }"
	sleep 8
	B2=$(docker exec yb_p1_cli sh -c 'wc -c < /tmp/dl 2>/dev/null || echo 0' | tr -d ' ')
	docker exec yb_p1_cli sh -c 'pkill curl' 2>/dev/null
	if [ "${B2:-0}" -gt "${B1:-0}" ]; then R="STILL FLOWING ($B1 -> $B2)"; else R="stopped ($B1 -> $B2)"; fi
	printf '  %-12s in-flight transfer after removing the allowlist element: %s\n' "$shape" "$R"
done

say "WHAT WAS NOT TRIED"
cat <<'NOT'
  - IPv6.
  - Conntrack helpers (FTP/SIP). The RELATED hole the no-fast-path shape would close
    is a claim about rule shape and is NOT exercised here.
  - Whether the client sees a clean error or hangs. Both shapes drop silently; the
    RST-injection question Calico solves is separate and untested.
  - UDP long flows under revocation. Only a TCP transfer is tested.
  - Any off-host path. Everything here is bridge-local, deliberately, because run 1
    was invalidated by DNS. That also means NAT and PMTU to the internet are untested
    under the no-fast-path shape.
  - macOS: pf state semantics differ and none of this transfers.
NOT

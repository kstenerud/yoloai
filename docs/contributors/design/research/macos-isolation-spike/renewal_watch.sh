#!/bin/bash
# ABOUTME: Watches a running sandbox across multiple DHCP lease renewals — the last path to
# ABOUTME: genuine fail-open — and records what IPv6 the guests actually have.
#
# Run: bash renewal_watch.sh          (no root, ~40 min)
#
# WHY THIS EXISTS
#   R1 RENEWAL. Every address change measured so far (.5->.2, .2->.3, .22->.23, .2->.4)
#      accompanied a RESTART of the sandbox, where yoloAI's start path re-runs and reinstalls
#      the table entry. The dangerous variant is an address moving underneath a sandbox nobody
#      restarted, which would silently unfilter it. That was written off as "needs days" —
#      wrongly. The host's lease log showed a lease with ~2 minutes left, i.e. a SHORT lease,
#      so several renewal cycles fit inside one session. This measures the lease duration and
#      then watches across at least three renewals.
#
#   R2 GUEST IPv6. The host has no IPv6 egress (ULAs on bridge100 and a Tailscale utun; curl -6
#      returns 000), so end-to-end IPv6 blocking cannot be tested here. What CAN be tested is
#      whether guests get IPv6 addresses at all — because a guest holding a ULA on the vmnet
#      bridge can reach the host and its bridge-mates over IPv6 regardless of any IPv4-only
#      allowlist. That is a lateral path, not egress, and it is exactly the kind of hole an
#      IPv4-only design does not notice.

set -u
YOLOAI=/Users/karlstenerud/Projects/yoloai/yoloai
ASB=renew-apple
TSB=renew-tart
WORK=$(mktemp -d)
RESULTS="$(cd "$(dirname "$0")" && pwd)/results/renewal-watch.txt"
mkdir -p "$(dirname "$RESULTS")"
exec > >(tee "$RESULTS") 2>&1

cleanup() {
  echo
  echo "== cleanup =="
  "$YOLOAI" destroy "$ASB" --abandon-unapplied >/dev/null 2>&1
  "$YOLOAI" destroy "$TSB" --abandon-unapplied >/dev/null 2>&1
  rm -rf "$WORK"
  echo "   sandboxes remaining: $("$YOLOAI" ls 2>/dev/null | grep -cE "$ASB|$TSB" || true)"
  echo "   results: $RESULTS"
}
trap cleanup EXIT

git init --quiet "$WORK/repo"
( cd "$WORK/repo" && echo hi > README.md && git add -A \
  && git -c user.email=t@t -c user.name=t commit --quiet -m init )

appip()  { container inspect "yoloai-cli-$ASB" 2>/dev/null \
             | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d[0]["status"]["networks"][0].get("ipv4Address","").split("/")[0])' 2>/dev/null; }
tartip() { tart ip "yoloai-cli-$TSB" 2>/dev/null | tr -d '[:space:]'; }
leasesecs() {
  python3 - "$1" <<'PY'
import re,sys,time
ip=sys.argv[1]
try: txt=open('/var/db/dhcpd_leases').read()
except Exception: sys.exit(0)
best=None
for m in re.finditer(r'\{(.*?)\}', txt, re.S):
    b=m.group(1)
    g=lambda k:(re.search(rf'{k}=([^\n]+)',b) or [None,''])[1].strip()
    if g('ip_address')==ip:
        try: e=int(g('lease'),16)
        except Exception: continue
        if best is None or e>best[0]: best=(e,g('hw_address'))
if best: print(int(best[0]-time.time()), best[1])
PY
}

echo "==================================================================="
echo " lease renewal watch + guest IPv6 inventory"
echo " host: $(sw_vers -productVersion)   started: $(date '+%H:%M:%S')"
echo "==================================================================="

echo
echo "== R2: what IPv6 do the guests actually have? =="
echo "   host (for reference):"
ifconfig 2>/dev/null | awk '/^[a-z]/{i=$1} /inet6 /{if ($2 !~ /^fe80/ && $2 !~ /^::1/) print "     "i" "$2}' | head -4

"$YOLOAI" new --backend apple "$ASB" "$WORK/repo" >/dev/null 2>&1
aip=$(appip)
echo "   apple ($aip):"
container exec "yoloai-cli-$ASB" sh -c 'ip -6 addr show scope global 2>/dev/null | grep inet6 || echo "     (no global IPv6)"' 2>&1 | sed 's/^/     /'
container exec "yoloai-cli-$ASB" sh -c 'ip -6 addr show 2>/dev/null | grep -c inet6 || echo 0' 2>&1 | sed 's/^/     total inet6 addrs (incl link-local): /'
echo -n "     IPv6 egress: "
container exec "yoloai-cli-$ASB" sh -c 'curl -6 -s -o /dev/null -w "%{http_code}" --max-time 8 https://ipv6.google.com/ 2>/dev/null || echo 000' 2>/dev/null || echo "n/a"
echo

"$YOLOAI" new --backend tart "$TSB" "$WORK/repo" >/dev/null 2>&1
for _ in $(seq 1 240); do tip=$(tartip); [ -n "$tip" ] && break; sleep 0.5; done
echo "   tart ($tip):"
tart exec "yoloai-cli-$TSB" sh -c 'ifconfig en0 inet6 2>/dev/null | grep inet6 || echo "(none)"' 2>&1 | sed 's/^/     /'
echo -n "     IPv6 egress: "
tart exec "yoloai-cli-$TSB" sh -c 'curl -6 -s -o /dev/null -w "%{http_code}" --max-time 8 https://ipv6.google.com/ 2>/dev/null || echo 000' 2>/dev/null || echo "n/a"

# ---------------------------------------------------------------------------
echo
echo "== R1: lease duration, then a watch across renewals =="
read -r secs mac <<< "$(leasesecs "$tip")"
if [ -z "${secs:-}" ]; then
  echo "   UNKNOWN  no lease record for tart ip $tip — cannot size the watch"
  exit 1
fi
echo "   tart ip=$tip mac=$mac"
echo "   lease has ${secs}s (~$((secs/60))m) remaining, measured just after issue"
# Watch long enough for at least three renewal opportunities. DHCP clients renew at T1 =
# 50% of the lease, so 3 leases is ~6 renewal attempts. Capped so the run stays bounded.
watch=$(( secs * 3 )); [ "$watch" -gt 2400 ] && watch=2400; [ "$watch" -lt 600 ] && watch=600
echo "   watching for ${watch}s (~$((watch/60))m), sampling every 30s"
echo

start=$(date +%s); changed=0; last="$tip"; lastmac="$mac"; n=0
while [ $(( $(date +%s) - start )) -lt "$watch" ]; do
  sleep 30
  cur=$(tartip); n=$((n+1))
  read -r csecs cmac <<< "$(leasesecs "${cur:-none}")"
  el=$(( $(date +%s) - start ))
  if [ -n "${cur:-}" ] && [ "$cur" != "$last" ]; then
    changed=1
    echo "   t=${el}s CHANGE  $last -> $cur  (mac $lastmac -> ${cmac:-?})"
    egr=$(tart exec "yoloai-cli-$TSB" sh -c 'curl -s -o /dev/null -w "%{http_code}" --max-time 6 http://1.1.1.1/ 2>/dev/null || echo 000' 2>/dev/null)
    if [ "$egr" = "000" ]; then
      echo "           fail-CLOSED: address moved but the guest is stranded"
    else
      echo "           FAIL-OPEN: address moved (egress=$egr) with NO restart — a table entry"
      echo "           keyed on $last no longer matches and this sandbox is unfiltered"
    fi
    last="$cur"; lastmac="${cmac:-?}"
  elif [ $(( n % 4 )) -eq 0 ]; then
    echo "   t=${el}s steady  ip=$cur lease_remaining=${csecs:-?}s"
  fi
done

echo
if [ "$changed" -eq 0 ]; then
  echo "   RESULT  address held for ${watch}s across $(( watch / (secs>0?secs:1) )) lease lifetimes"
  echo "           with no restart. Renewal does NOT move the address on tart, so the"
  echo "           remaining fail-open path is closed for this backend on this host."
else
  echo "   RESULT  address MOVED without a restart — see the CHANGE line(s). This is the"
  echo "           fail-open case the design must handle, not a theoretical one."
fi

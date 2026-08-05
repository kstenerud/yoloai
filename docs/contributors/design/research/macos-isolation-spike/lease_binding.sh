#!/bin/bash
# ABOUTME: Measures vmnet's DHCP lease duration and whether a guest's address survives a
# ABOUTME: stop/start — the two facts that decide how real the pf design's fail-open risk is.
#
# Run: bash lease_binding.sh          (no root, ~5 min)
#
# WHY THIS EXISTS
#   addr-stability.txt showed a running sandbox's address did not move over 240s of induced
#   churn. That is a negative result over minutes; the untested path was DHCP lease renewal
#   over the days a real sandbox lives. Reading /var/db/dhcpd_leases turned up two things
#   that make the question answerable much more cheaply:
#
#     * bootpd binds leases to hw_address. Across 253 historical records there were 253
#       distinct MACs and 253 distinct IPs, with NO ip ever bound to two MACs — consistent
#       with renewal preserving the address, but that is inference from a log, not a
#       measurement.
#     * only one lease was unexpired and it had ~2 minutes left, hinting the lease is SHORT.
#       If so, renewal is not a rare event over days; it happens constantly, which is why it
#       has to be settled rather than deferred.
#
#   L1 measures the actual lease duration. L2 tests binding stability directly by stopping
#   and starting a sandbox. L3 closes the tart half of the ordering claim in
#   plans/macos-pf-privileged-path.md, which was measured on apple only.

set -u
YOLOAI=/Users/karlstenerud/Projects/yoloai/yoloai
ASB=leasechk-apple
TSB=leasechk-tart
WORK=$(mktemp -d)
RESULTS="$(cd "$(dirname "$0")" && pwd)/results/lease-binding.txt"
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

# lease_for <ip> -> "<seconds-until-expiry> <mac>", empty if no record
lease_for() {
  python3 - "$1" <<'PY'
import re,sys,time
ip=sys.argv[1]
try: txt=open('/var/db/dhcpd_leases').read()
except Exception: sys.exit(0)
best=None
for m in re.finditer(r'\{(.*?)\}', txt, re.S):
    b=m.group(1)
    g=lambda k: (re.search(rf'{k}=([^\n]+)',b) or [None,''])[1].strip()
    if g('ip_address')==ip:
        try: exp=int(g('lease'),16)
        except Exception: continue
        if best is None or exp>best[0]: best=(exp,g('hw_address'))
if best: print(int(best[0]-time.time()), best[1])
PY
}

echo "==================================================================="
echo " vmnet DHCP lease duration and address-binding stability"
echo " host: $(sw_vers -productVersion)   started: $(date '+%H:%M:%S')"
echo "==================================================================="

# ---------------------------------------------------------------------------
echo
echo "== L1: how long is a vmnet DHCP lease? =="
"$YOLOAI" new --backend apple "$ASB" "$WORK/repo" >/dev/null 2>&1
aip=$(appip)
if [ -z "$aip" ]; then echo "   UNKNOWN  no apple address; L1/L2 unmeasured"; exit 1; fi
read -r secs mac <<< "$(lease_for "$aip")"
if [ -n "${secs:-}" ]; then
  echo "   apple sandbox: ip=$aip mac=$mac"
  echo "   lease expires in ${secs}s (~$((secs/60))m) — measured moments after issue, so this"
  echo "   approximates the FULL lease duration"
  if [ "$secs" -lt 3600 ]; then
    echo "   NOTE  short lease: renewal is a constant event, not a rare one over days."
    echo "         Whatever L2 shows about binding stability therefore matters a lot."
  else
    echo "   NOTE  long lease: renewal is comparatively rare."
  fi
else
  echo "   UNKNOWN  no lease record found for $aip (apple may not use bootpd's lease file)"
fi

# ---------------------------------------------------------------------------
echo
echo "== L2: does the address survive a stop/start (same MAC, re-requested lease)? =="
# This is the direct test of the inference drawn from the lease log. If the binding is
# MAC-keyed and honored, the address comes back identical, and renewal-driven fail-open
# is not a live risk. If it changes, a table keyed on the old address is already stale
# by the time the sandbox is running again.
echo "   before stop: $aip"
"$YOLOAI" stop "$ASB" >/dev/null 2>&1
sleep 5
"$YOLOAI" start "$ASB" >/dev/null 2>&1
for _ in $(seq 1 120); do a2=$(appip); [ -n "$a2" ] && break; sleep 0.5; done
echo "   after start: ${a2:-<none>}"
if [ -n "${a2:-}" ] && [ "$a2" = "$aip" ]; then
  echo "   PASS  address preserved across stop/start — the MAC-keyed binding is honored,"
  echo "         so a renewal returns the same address and fail-open needs a subnet change"
  echo "         rather than mere lease expiry"
elif [ -n "${a2:-}" ]; then
  echo "   FAIL  address CHANGED $aip -> $a2 across a stop/start. Table entries must be"
  echo "         rewritten on every start, and any cached address is a fail-open hazard"
else
  echo "   UNKNOWN  no address after restart"
fi

# ---------------------------------------------------------------------------
echo
echo "== L3: is tart's address knowable before 'yoloai new' returns? =="
# The plan states this ordering property generally but measured it on apple only, where
# the container starts in seconds. tart boots a full macOS VM and reads its address from
# host-side lease records, so it is the case most likely to break the claim.
t0=$(date +%s); found=""
"$YOLOAI" new --backend tart "$TSB" "$WORK/repo" >/dev/null 2>&1 &
np=$!
for _ in $(seq 1 1200); do
  t=$(tartip)
  if [ -n "$t" ]; then found=$(date +%s); break; fi
  sleep 0.5
done
wait $np 2>/dev/null; tdone=$(date +%s)
if [ -n "$found" ]; then
  echo "   tart address $(tartip) appeared after $((found-t0))s"
  echo "   'yoloai new' returned after $((tdone-t0))s"
  if [ "$found" -le "$tdone" ]; then
    echo "   PASS  knowable before creation returns, same as apple — the ordering claim"
    echo "         holds on both address-keyed backends"
  else
    echo "   FAIL  creation returned $((found-tdone))s BEFORE an address existed. The plan's"
    echo "         'install enforcement in-process before agent launch' is apple-only and"
    echo "         tart needs a post-start hook"
  fi
else
  echo "   UNKNOWN  no tart address within the poll bound"
fi

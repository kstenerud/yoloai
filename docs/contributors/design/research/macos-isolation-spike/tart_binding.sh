#!/bin/bash
# ABOUTME: Does tart preserve a sandbox's address across stop/start, where apple did not?
# ABOUTME: Decides whether the pf design's re-read-on-every-start rule is universal or apple-only.
#
# Run: bash tart_binding.sh          (no root, ~3 min)
#
# WHY THIS EXISTS
#   lease-binding.txt L2 showed apple's address moved .22 -> .23 across one stop/start, and
#   L1 showed apple has NO record in /var/db/dhcpd_leases — it is not bootpd-issued. tart's
#   addresses ARE in that file, where 253 historical records showed 253 distinct MACs bound
#   to 253 distinct addresses with no address ever bound to two MACs. That pattern is what a
#   MAC-keyed binding looks like, which would mean tart PRESERVES the address across a
#   restart while apple does not.
#
#   It matters because the plan tells implementers to re-read the address at every start and
#   never cache it. If that rule is apple-only, someone will eventually "optimize" the tart
#   path by caching, and the failure — a table entry naming an address the sandbox no longer
#   holds — is silent. If both backends move, the rule is unconditional and easy to defend.
#   Either answer is useful; the point is not to guess it from a log.

set -u
YOLOAI=/Users/karlstenerud/Projects/yoloai/yoloai
SB=tartbind
WORK=$(mktemp -d)
RESULTS="$(cd "$(dirname "$0")" && pwd)/results/tart-binding.txt"
mkdir -p "$(dirname "$RESULTS")"
exec > >(tee "$RESULTS") 2>&1

cleanup() {
  echo
  echo "== cleanup =="
  "$YOLOAI" destroy "$SB" --abandon-unapplied >/dev/null 2>&1
  rm -rf "$WORK"
  echo "   sandboxes remaining: $("$YOLOAI" ls 2>/dev/null | grep -c "$SB" || true)"
  echo "   results: $RESULTS"
}
trap cleanup EXIT

git init --quiet "$WORK/repo"
( cd "$WORK/repo" && echo hi > README.md && git add -A \
  && git -c user.email=t@t -c user.name=t commit --quiet -m init )

tartip() { tart ip "yoloai-cli-$SB" 2>/dev/null | tr -d '[:space:]'; }
macfor() {
  python3 - "$1" <<'PY'
import re,sys
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
if best: print(best[1])
PY
}
waitip() { for _ in $(seq 1 240); do t=$(tartip); [ -n "$t" ] && { echo "$t"; return; }; sleep 0.5; done; }

echo "==================================================================="
echo " tart address binding across stop/start"
echo " host: $(sw_vers -productVersion)   started: $(date '+%H:%M:%S')"
echo "==================================================================="

echo
echo "== creating tart sandbox =="
"$YOLOAI" new --backend tart "$SB" "$WORK/repo" >/dev/null 2>&1
ip1=$(waitip)
if [ -z "${ip1:-}" ]; then echo "   UNKNOWN  no tart address; unmeasured"; exit 1; fi
mac1=$(macfor "$ip1")
echo "   ip=$ip1  mac=${mac1:-<no lease record>}"

echo
echo "== stop / start =="
"$YOLOAI" stop "$SB" >/dev/null 2>&1
sleep 5
"$YOLOAI" start "$SB" >/dev/null 2>&1
ip2=$(waitip)
mac2=$(macfor "${ip2:-none}")
echo "   ip=${ip2:-<none>}  mac=${mac2:-<no lease record>}"

echo
if [ -z "${ip2:-}" ]; then
  echo "   UNKNOWN  no address after restart"
elif [ "$ip1" = "$ip2" ]; then
  echo "   PRESERVED  tart kept $ip1 across stop/start, unlike apple (.22 -> .23)."
  echo "   => the two backends DIFFER. The plan's 're-read at every start, never cache'"
  echo "      rule is still correct for both, but it is load-bearing only on apple, and"
  echo "      a tart-only test would never catch a caching regression."
  if [ -n "${mac1:-}" ] && [ "$mac1" = "${mac2:-}" ]; then
    echo "   MAC unchanged ($mac1) — consistent with a MAC-keyed bootpd binding being honored."
  else
    echo "   MAC CHANGED ($mac1 -> ${mac2:-none}) yet the address held — so the address is NOT"
    echo "   preserved by the MAC binding, and the lease-log inference was wrong."
  fi
else
  echo "   CHANGED  $ip1 -> $ip2. Both backends move their address across a restart, so"
  echo "   're-read at every start, never cache' is unconditional and any orphan-entry"
  echo "   hazard applies to tart exactly as it does to apple."
fi

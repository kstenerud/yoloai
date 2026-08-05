#!/bin/bash
# ABOUTME: Measures the two address-keyed risks in the host-pf design — when a sandbox's
# ABOUTME: address becomes knowable, and whether it can change while the sandbox runs.
#
# Run: bash addr_stability.sh          (no root, ~10 min)
#
# WHY THIS EXISTS
#   The pf design keys enforcement on the sandbox's source address (results/
#   pf-authz-unprivileged.txt U10). Two properties decide whether that is sound, and
#   neither is settled by the existing vmnet-switch run:
#
#   U14 ENFORCEMENT WINDOW. The address does not exist at launch — it is a DHCP lease
#       read host-side. So `add to table -> verify -> launch agent` is an ordering
#       requirement. This measures how long that window is and confirms the address is
#       available before any agent could run.
#
#   U15 FAIL-OPEN. A `block from <table>` rule stops matching if the sandbox's address
#       changes, silently unfiltering it. results/vmnet-switch.txt shows addresses DO
#       change (apple .5 -> .2, tart .2 -> .3) but only across a restart of the sandbox
#       itself, where yoloai's start path would re-add the entry. The dangerous case is a
#       CONTINUOUSLY RUNNING sandbox acquiring a new WORKING address. At T2 of that run
#       apple kept running while tart took the bridge and did not move — but that was one
#       snapshot, not a watch. This polls continuously through the same churn.
#
# READING THE RESULT
#   U15 is a NEGATIVE result if the address never moves. A negative here is worth having
#   but is NOT proof of stability: it bounds the risk over minutes of induced churn, not
#   over the days a real sandbox lives, and DHCP lease renewal is the untested path.
#   Say "not observed under X" rather than "cannot happen".

set -u
YOLOAI=/Users/karlstenerud/Projects/yoloai/yoloai
SB=addrstab-apple
TVM=addrstab-tart
WORK=$(mktemp -d)
RESULTS="$(cd "$(dirname "$0")" && pwd)/results/addr-stability.txt"
mkdir -p "$(dirname "$RESULTS")"
exec > >(tee "$RESULTS") 2>&1

cleanup() {
  echo
  echo "== cleanup =="
  "$YOLOAI" destroy "$SB" --abandon-unapplied >/dev/null 2>&1
  "$YOLOAI" destroy "$TVM" --abandon-unapplied >/dev/null 2>&1
  rm -rf "$WORK"
  echo "   sandboxes remaining: $("$YOLOAI" ls 2>/dev/null | grep -cE "$SB|$TVM" || true)"
  echo "   results: $RESULTS"
}
trap cleanup EXIT

# A throwaway repo — `yoloai new` copies the source tree, and copying this repo would
# dominate the timing measurement we are here to take.
git init --quiet "$WORK/repo"
( cd "$WORK/repo" && echo hi > README.md && git add -A \
  && git -c user.email=t@t -c user.name=t commit --quiet -m init )

appip()  { container inspect "yoloai-cli-$SB" 2>/dev/null \
             | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d[0]["status"]["networks"][0].get("ipv4Address",""))' 2>/dev/null; }
appegr() { local c; c=$(container exec "yoloai-cli-$SB" curl -s -o /dev/null -w '%{http_code}' --max-time 5 http://1.1.1.1/ 2>/dev/null); c=${c//[^0-9]/}; c=${c: -3}; printf '%s' "${c:-000}"; }
bridges(){ ifconfig 2>/dev/null | awk '/^[a-z]/{i=$1} /inet /{print i"="$2}' | grep '^bridge' | tr '\n' ' '; }

echo "==================================================================="
echo " address stability for host-pf enforcement"
echo " host: $(sw_vers -productVersion)   started: $(date '+%H:%M:%S')"
echo "==================================================================="

# ---------------------------------------------------------------------------
echo
echo "== U14: when does the address become knowable? =="
t0=$(date +%s)
"$YOLOAI" new --backend apple "$SB" "$WORK/repo" >/dev/null 2>&1 &
newpid=$!
addr=""; tfound=""
# Poll from the moment creation starts. 240s bound: an apple container that has not
# leased by then is a different failure and should not be reported as a slow lease.
for _ in $(seq 1 960); do
  a=$(appip)
  if [ -n "$a" ]; then addr=$a; tfound=$(date +%s); break; fi
  sleep 0.25
done
wait $newpid 2>/dev/null
tdone=$(date +%s)
if [ -n "$addr" ]; then
  echo "   address appeared : $addr after $((tfound-t0))s"
  echo "   'yoloai new' returned after $((tdone-t0))s"
  if [ "$tfound" -le "$tdone" ]; then
    echo "   PASS  the address is knowable BEFORE creation returns, so the ordering"
    echo "         'add to table -> verify -> launch agent' is achievable in-process"
  else
    echo "   FAIL  creation returned before an address existed — enforcement could not"
    echo "         be installed synchronously and would need a post-start hook"
  fi
else
  echo "   UNKNOWN  no address within the poll bound; U14 unmeasured"
  exit 1
fi

# ---------------------------------------------------------------------------
echo
echo "== U15: can the address change while the sandbox keeps running? =="
echo "   baseline: addr=$addr egress=$(appegr)"
echo "   bridges : $(bridges)"
echo
echo "   >>> inducing bridge churn with a tart VM while the apple sandbox keeps running"
echo "   (this is the T2 condition from vmnet-switch.txt, but watched rather than sampled)"

"$YOLOAI" new --backend tart "$TVM" "$WORK/repo" >/dev/null 2>&1 &
churnpid=$!

changed=0; samples=0; lastaddr=$addr
# Watch for ~4 min or until the churn finishes, whichever is longer-lived.
for _ in $(seq 1 120); do
  cur=$(appip); samples=$((samples+1))
  if [ -n "$cur" ] && [ "$cur" != "$lastaddr" ]; then
    e=$(appegr)
    echo "   CHANGE  $lastaddr -> $cur   egress=$e   (t=$(($(date +%s)-t0))s)"
    if [ "$e" = "000" ]; then
      echo "           fail-CLOSED: address moved but the sandbox is stranded (DF172 shape)"
    else
      echo "           FAIL-OPEN: address moved AND egress works — a table keyed on the old"
      echo "           address no longer matches, so this sandbox is now unfiltered"
    fi
    changed=1; lastaddr=$cur
  fi
  sleep 2
done
wait $churnpid 2>/dev/null

echo
echo "   final   : addr=$(appip) egress=$(appegr)"
echo "   bridges : $(bridges)"
echo "   samples : $samples over ~$(( samples * 2 ))s of induced churn"
if [ "$changed" -eq 0 ]; then
  echo "   RESULT  address did NOT move while running, across a full tart VM bring-up."
  echo "           NEGATIVE result: bounds the risk over minutes of churn, NOT over the"
  echo "           days a sandbox lives. DHCP lease renewal remains untested."
else
  echo "   RESULT  address DID move while running — see the CHANGE line(s) above."
fi

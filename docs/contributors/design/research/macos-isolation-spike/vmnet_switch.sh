#!/bin/bash
# ABOUTME: Instruments the macOS vmnet bridge alternation between tart and apple, to
# ABOUTME: identify the mechanism rather than infer it from a handful of observations.
#
# Run: bash vmnet_switch.sh <apple-sandbox> <tart-sandbox>   (no root; ~3 min)
#
# DF172 records that starting one macOS VM backend strands the other's sandboxes. That was
# concluded from n=4 on one host with the causal verb already attached. This script makes
# the claim falsifiable: it drives the switch deliberately, snapshots the bridge state and
# every sandbox's real egress at each step, and pulls the system log for bridge lifecycle
# events so the teardown has a named actor instead of an assumed one.

set -u
A_SB="${1:-mac-coh-apple}"
T_SB="${2:-mac-coh-tart}"
YOLOAI=/Users/karlstenerud/Projects/yoloai/yoloai
DENY=1.0.0.1
START_TS=$(date '+%Y-%m-%d %H:%M:%S')

bridges() { ifconfig 2>/dev/null | awk '/^[a-z]/{i=$1} /inet /{print i"="$2}' | grep '^bridge' | tr '\n' ' '; }
appip()   { container ls 2>/dev/null | awk -v n="yoloai-cli-$A_SB" '$1==n{print $6}'; }
tartip()  { tart ip "yoloai-cli-$T_SB" 2>/dev/null | tr -d '[:space:]'; }
appegr()  { container exec "yoloai-cli-$A_SB" curl -s -o /dev/null -w '%{http_code}' --max-time 6 "http://$DENY/" 2>/dev/null || echo "n/a"; }
tartegr() { tart exec "yoloai-cli-$T_SB" curl -s -o /dev/null -w '%{http_code}' --max-time 6 "http://$DENY/" 2>/dev/null || echo "n/a"; }

snap() {
  printf '\n--- %s\n' "$1"
  printf '    bridges : %s\n' "$(bridges)"
  printf '    apple   : ip=%-16s egress=%s\n' "$(appip)" "$(appegr)"
  printf '    tart    : ip=%-16s egress=%s\n' "$(tartip)" "$(tartegr)"
}

echo "==================================================================="
echo " vmnet alternation instrumentation"
echo " Reading: 301 = working egress, 000 = stranded, n/a = no guest reachable"
echo "==================================================================="

snap "T0 baseline (as found)"

echo
echo ">>> restarting APPLE's network (container system stop/start)"
container system stop >/dev/null 2>&1; sleep 3
container system start >/dev/null 2>&1; sleep 10
container start "yoloai-cli-$A_SB" >/dev/null 2>&1; sleep 8
snap "T1 after apple takes the bridge"

echo
echo ">>> restarting the TART VM (this is the step DF172 says strands apple)"
$YOLOAI stop "$T_SB" >/dev/null 2>&1; sleep 2
$YOLOAI start "$T_SB" >/dev/null 2>&1; sleep 20
snap "T2 after tart takes the bridge"

echo
echo ">>> restarting APPLE again — does it reverse? (a one-way result would refute"
echo "    'most-recent-start wins' and point at something tart-specific instead)"
container system stop >/dev/null 2>&1; sleep 3
container system start >/dev/null 2>&1; sleep 10
container start "yoloai-cli-$A_SB" >/dev/null 2>&1; sleep 8
snap "T3 after apple takes it back"

echo
echo "=== bridge lifecycle events from the system log since $START_TS ==="
echo "    (who actually detaches the bridge — configd? the vmnet helper? tart?)"
log show --start "$START_TS" --style compact 2>/dev/null \
  | grep -iE 'bridge1[0-9][0-9]|vmenet|vmnet' \
  | grep -viE 'yoloai|curl' \
  | tail -40 \
  | sed 's/^/    /'
echo
echo "Interpretation guide:"
echo "  * apple 301 at T1/T3 and 000 at T2, with tart the mirror image => alternation confirmed,"
echo "    and it is symmetric (neither backend is the sole aggressor)."
echo "  * both 301 at some step => they CAN coexist and the earlier finding was situational."
echo "  * a bridge index/subnet change across steps => rules keyed on interface name are unsafe."

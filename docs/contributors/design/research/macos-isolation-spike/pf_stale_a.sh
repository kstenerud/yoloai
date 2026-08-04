#!/bin/bash
# ABOUTME: Is the stale-entry allowlist-inheritance hazard (D3) specific to Shape B's slots, or
# ABOUTME: does an orphaned sub-anchor do the same thing under Shape A? Decides if D3 differentiates.
#
# Run: sudo bash pf_stale_a.sh <sandbox-A> <sandbox-B>
#
# WHY THIS EXISTS
#   pf-assumptions.txt D3 measured that under Shape B a stale entry in another slot makes a sandbox
#   LOSE its own allowlist and INHERIT the stale slot's — a widening, not merely a block. That is
#   the sharpest hazard found in this work.
#
#   The obvious reading is that it is a cost of Shape B's slot pool, which would count against
#   choosing B. But the mechanism is `quick` first-match over rules keyed on a recycled address,
#   and Shape A's sub-anchors are also evaluated in a defined order (alphabetical) over rules keyed
#   on the same addresses. If an ORPHANED sub-anchor naming a recycled address does the same thing,
#   D3 is a property of address-keyed enforcement, not of slots, and it does not differentiate the
#   shapes at all — it just raises reaping to a security requirement for both.
#
#   Asserting that without measuring is exactly the error pattern this workstream keeps repeating,
#   so it is measured here.
#
# SAFETY: only com.apple/yoloai_sa. Main ruleset never loaded or flushed; count asserted unchanged.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0 <A> <B>"; exit 2; }
U="${SUDO_USER:?run via sudo}"
A_SB="${1:?need A}"; B_SB="${2:?need B}"
PARENT="com.apple/yoloai_sa"
ALLOW_A=1.1.1.1; ALLOW_B=1.0.0.2; DENY=1.0.0.1
PASS=0; FAIL=0; UNKNOWN=0
RESULTS="$(cd "$(dirname "$0")" && pwd)/results/pf-stale-a.txt"
mkdir -p "$(dirname "$RESULTS")"
exec > >(tee "$RESULTS") 2>&1

asuser() { sudo -u "$U" "$@"; }
quiet_pf() { grep -viE 'use of -f option|main ruleset added|/etc/pf.conf for further|ALTQ|^$'; }
ipof() { asuser container inspect "yoloai-cli-$1" 2>/dev/null \
           | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d[0]["status"]["networks"][0].get("ipv4Address","").split("/")[0])' 2>/dev/null; }
egress() { local c; c=$(asuser container exec "yoloai-cli-$1" curl -s -o /dev/null -w '%{http_code}' --max-time 6 "http://$2/" 2>/dev/null)
  c=${c//[^0-9]/}; c=${c: -3}; printf '%s' "${c:-000}"; }
say() { printf '\n== %s ==\n' "$*"; }
ok()  { PASS=$((PASS+1)); printf '   PASS    %s\n' "$*"; }
bad() { FAIL=$((FAIL+1)); printf '   FAIL    %s\n' "$*"; }
unk() { UNKNOWN=$((UNKNOWN+1)); printf '   UNKNOWN %s\n' "$*"; }

MAIN_BEFORE=$(pfctl -s rules 2>/dev/null | grep -c . || true)
cleanup() {
  echo; echo "== cleanup =="
  for a in "$PARENT/aaa_stale" "$PARENT/box_b" "$PARENT"; do pfctl -a "$a" -F all >/dev/null 2>&1; done
  echo "   main ruleset: before=$MAIN_BEFORE after=$(pfctl -s rules 2>/dev/null | grep -c . || true)"
  echo "   B restored: own=$(egress "$B_SB" $ALLOW_B) deny=$(egress "$B_SB" $DENY)"
  chown "$U" "$RESULTS" 2>/dev/null; echo "   results: $RESULTS"; sync
}
A_IP=$(ipof "$A_SB"); B_IP=$(ipof "$B_SB")
[ -n "$B_IP" ] || { echo "could not resolve B"; exit 2; }
trap cleanup EXIT
echo "host: $(sw_vers -productVersion) | A=$A_SB($A_IP) B=$B_SB($B_IP)"

say "SA0 BASELINE"
z1=$(egress "$B_SB" $ALLOW_A); z2=$(egress "$B_SB" $ALLOW_B); z3=$(egress "$B_SB" $DENY)
echo "        B: $ALLOW_A=$z1 $ALLOW_B=$z2 $DENY=$z3"
if [ "$z1" = 000 ] || [ "$z2" = 000 ] || [ "$z3" = 000 ]; then bad "baseline incomplete; ABORTING"; exit 1; fi
ok "B reaches all three"

say "SA1 CONTROL — B in its OWN sub-anchor only, with its own allowlist"
pfctl -a "$PARENT" -F all >/dev/null 2>&1
printf 'anchor "*"\n' | pfctl -a "$PARENT" -f - 2>&1 | quiet_pf | sed 's/^/        parent: /'
printf 'pass in quick from %s to %s\nblock drop in quick from %s to any\n' "$B_IP" "$ALLOW_B" "$B_IP" \
  | pfctl -a "$PARENT/box_b" -f - 2>&1 | quiet_pf | sed 's/^/        box_b: /'
c_own=$(egress "$B_SB" $ALLOW_B); c_other=$(egress "$B_SB" $ALLOW_A)
echo "        B: own=$c_own other=$c_other"
if [ "$c_own" != 000 ] && [ "$c_other" = 000 ]; then
  ok "control holds — B has exactly its own policy"
else
  unk "control invalid (own=$c_own other=$c_other); nothing below is meaningful"; exit 1
fi

say "SA2 THE TEST — add an ORPHANED sub-anchor naming B's address with a DIFFERENT allowlist"
# 'aaa_stale' sorts before 'box_b', so under alphabetical evaluation it matches first. This is
# exactly what a recycled address plus a missed teardown produces under Shape A.
printf 'pass in quick from %s to %s\nblock drop in quick from %s to any\n' "$B_IP" "$ALLOW_A" "$B_IP" \
  | pfctl -a "$PARENT/aaa_stale" -f - 2>&1 | quiet_pf | sed 's/^/        aaa_stale: /'
n=$(pfctl -a "$PARENT/aaa_stale" -s rules 2>/dev/null | grep -c . || true)
s_own=$(egress "$B_SB" $ALLOW_B); s_other=$(egress "$B_SB" $ALLOW_A)
echo "        orphan rules=$n | B: own=$s_own other(orphan's allowlist)=$s_other"
if [ "${n:-0}" -lt 2 ]; then
  unk "the orphan anchor did not load; nothing measured"
elif [ "$s_other" != 000 ] && [ "$s_own" = 000 ]; then
  bad "SAME HAZARD UNDER SHAPE A: B lost its own policy and inherited the orphan's."
  echo "           D3 is a property of address-keyed enforcement, NOT of Shape B's slots — it does"
  echo "           not differentiate the shapes, and raises reaping to a security requirement for both"
elif [ "$s_own" != 000 ] && [ "$s_other" = 000 ]; then
  ok "Shape A is IMMUNE: B kept its own policy despite the orphan. D3 IS a Shape B cost and"
  echo "           counts against the slot pool"
elif [ "$s_own" != 000 ] && [ "$s_other" != 000 ]; then
  bad "orphan WIDENED B's reach to both allowlists under Shape A"
else
  ok "orphan left B fully blocked — fail-closed under Shape A, unlike Shape B's widening"
fi

mn=$(pfctl -s rules 2>/dev/null | grep -c . || true)
if [ "${mn:-0}" = "${MAIN_BEFORE:-0}" ]; then ok "main ruleset unchanged ($mn)"; else bad "MAIN CHANGED"; fi
printf '\n== TOTALS ==\n   pass=%d fail=%d unknown=%d\n' "$PASS" "$FAIL" "$UNKNOWN"

#!/bin/bash
# ABOUTME: Does the slot pool enforce on TART, and do apple and tart coexist in different slots?
# ABOUTME: The pool has only ever been run on apple; the plan records that as a gap.
#
# Run: sudo bash pf_tart.sh <apple-sandbox> <tart-sandbox>
#
# WHY THIS EXISTS
#   Every slot-pool measurement so far used apple sandboxes. tart's pf enforcement WAS measured
#   three times (pf-main-run.txt P1b and two reruns) but in a flat anchor with per-sandbox rules,
#   not the static pool + table membership the plan now specifies. "Same addressing shape, so it
#   should work" is precisely the kind of assertion this workstream keeps getting wrong (A29).
#
#   T1 does the pool enforce for a tart guest at all?
#   T2 apple and tart LIVE AT ONCE in different slots with different allowlists — the cross-backend
#      case, which no run has ever exercised. Both backends put guests on vmnet bridges and the
#      rules key on source address, so nothing about the design distinguishes them; that is a
#      prediction, and this is the test of it.
#   T3 does teardown by table delete restore a tart guest, as it does an apple one?
#
#   DF172 note: starting tart can take the vmnet bridge and strand apple. The baseline gate asserts
#   BOTH guests reach everything first, so a stranded backend aborts the run rather than passing
#   every "blocked" assertion for free.
#
# SAFETY: only com.apple/yoloai_t. Main ruleset never loaded or flushed; count asserted unchanged.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0 <apple-sb> <tart-sb>"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"
A_SB="${1:?need the apple sandbox}"; T_SB="${2:?need the tart sandbox}"
HERE="$(cd "$(dirname "$0")" && pwd)"; ROOT="$(cd "$HERE/../../../../.." && pwd)"

ANCHOR="com.apple/yoloai_t"
SLOTS=4
ALLOW_A=1.1.1.1; ALLOW_T=1.0.0.2; DENY=1.0.0.1
PASS=0; FAIL=0; UNKNOWN=0
RESULTS="$HERE/results/pf-tart-pool.txt"
mkdir -p "$(dirname "$RESULTS")"
exec > >(tee "$RESULTS") 2>&1

asuser() { sudo -u "$U" "$@"; }
quiet_pf() { grep -viE 'use of -f option|main ruleset added|/etc/pf.conf for further|ALTQ|^$'; }
say() { printf '\n== %s ==\n' "$*"; }
ok()  { PASS=$((PASS+1)); printf '   PASS    %s\n' "$*"; }
bad() { FAIL=$((FAIL+1)); printf '   FAIL    %s\n' "$*"; }
unk() { UNKNOWN=$((UNKNOWN+1)); printf '   UNKNOWN %s\n' "$*"; }

# Backend-aware: apple answers through `container`, tart through `tart`. Deriving the backend from
# `yoloai ls` rather than taking it as an argument means a swapped pair of arguments fails loudly
# instead of silently measuring one guest twice.
bk() { asuser "$ROOT/yoloai" ls 2>/dev/null | awk -v n="$1" '$1==n {print $3}'; }
ipof() {
  case "$(bk "$1")" in
    tart)  asuser tart ip "yoloai-cli-$1" 2>/dev/null | tr -d '[:space:]' ;;
    apple) asuser container inspect "yoloai-cli-$1" 2>/dev/null \
             | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d[0]["status"]["networks"][0].get("ipv4Address","").split("/")[0])' 2>/dev/null ;;
    *) printf '' ;;
  esac
}
egress() {
  local c
  case "$(bk "$1")" in
    tart)  c=$(asuser tart exec "yoloai-cli-$1" curl -s -o /dev/null -w '%{http_code}' --max-time 8 "http://$2/" 2>/dev/null) ;;
    apple) c=$(asuser container exec "yoloai-cli-$1" curl -s -o /dev/null -w '%{http_code}' --max-time 8 "http://$2/" 2>/dev/null) ;;
    *) c="" ;;
  esac
  c=${c//[^0-9]/}; c=${c: -3}; printf '%s' "${c:-000}"
}
nrules() { pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -c . || true; }

MAIN_BEFORE=$(pfctl -s rules 2>/dev/null | grep -c . || true)
cleanup() {
  echo; echo "== cleanup =="
  pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
  echo "   anchor rules=$(nrules)"
  echo "   main ruleset: before=$MAIN_BEFORE after=$(pfctl -s rules 2>/dev/null | grep -c . || true)"
  echo "   apple restored: allow=$(egress "$A_SB" $ALLOW_A) deny=$(egress "$A_SB" $DENY)"
  echo "   tart  restored: allow=$(egress "$T_SB" $ALLOW_T) deny=$(egress "$T_SB" $DENY)"
  chown "$U" "$RESULTS" 2>/dev/null; echo "   results: $RESULTS"; sync
}
BK_A=$(bk "$A_SB"); BK_T=$(bk "$T_SB")
[ "$BK_A" = apple ] || { echo "$A_SB is backend '$BK_A', expected apple"; exit 2; }
[ "$BK_T" = tart ]  || { echo "$T_SB is backend '$BK_T', expected tart"; exit 2; }
A_IP=$(ipof "$A_SB"); T_IP=$(ipof "$T_SB")
if [ -z "$A_IP" ] || [ -z "$T_IP" ]; then
  echo "could not resolve both IPs (apple=$A_IP tart=$T_IP)"
  exit 2
fi
trap cleanup EXIT

echo "host: $(sw_vers -productVersion)"
echo "apple=$A_SB($A_IP)  tart=$T_SB($T_IP)  allow_apple=$ALLOW_A allow_tart=$ALLOW_T deny=$DENY"

say "T0 BASELINE — both guests reach everything (a stranded backend would pass every block for free)"
a1=$(egress "$A_SB" $ALLOW_A); a2=$(egress "$A_SB" $ALLOW_T); a3=$(egress "$A_SB" $DENY)
t1=$(egress "$T_SB" $ALLOW_A); t2=$(egress "$T_SB" $ALLOW_T); t3=$(egress "$T_SB" $DENY)
echo "        apple: $a1 $a2 $a3 | tart: $t1 $t2 $t3"
if [ "$a1" = 000 ] || [ "$a2" = 000 ] || [ "$a3" = 000 ] || [ "$t1" = 000 ] || [ "$t2" = 000 ] || [ "$t3" = 000 ]; then
  bad "baseline incomplete — one backend may be stranded (DF172). ABORTING"; exit 1
fi
ok "all six paths reachable across both backends"

say "T1/T2 THE POOL, WITH APPLE IN SLOT 0 AND TART IN SLOT 1"
pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
{ for ((i=0;i<SLOTS;i++)); do
    echo "table <yt_src_$i> persist"; echo "table <yt_dst_$i> persist"
    echo "pass  in quick from <yt_src_$i> to <yt_dst_$i>"
    echo "block drop in quick from <yt_src_$i> to any"
  done; } | pfctl -a "$ANCHOR" -f - 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
n=$(nrules)
pfctl -a "$ANCHOR" -t yt_src_0 -T add "$A_IP"    >/dev/null 2>&1
pfctl -a "$ANCHOR" -t yt_dst_0 -T add "$ALLOW_A" >/dev/null 2>&1
pfctl -a "$ANCHOR" -t yt_src_1 -T add "$T_IP"    >/dev/null 2>&1
pfctl -a "$ANCHOR" -t yt_dst_1 -T add "$ALLOW_T" >/dev/null 2>&1
s0=$(pfctl -a "$ANCHOR" -t yt_src_0 -T show 2>/dev/null | tr -d ' \n')
s1=$(pfctl -a "$ANCHOR" -t yt_src_1 -T show 2>/dev/null | tr -d ' \n')
echo "        rules=$n | slot0(apple)=$s0 slot1(tart)=$s1"
if [ "${n:-0}" -ne $((SLOTS*2)) ] || [ "$s0" != "$A_IP" ] || [ "$s1" != "$T_IP" ]; then
  unk "pool or membership did not land — T1/T2 unmeasured"
else
  aa=$(egress "$A_SB" $ALLOW_A); ao=$(egress "$A_SB" $ALLOW_T); ad=$(egress "$A_SB" $DENY)
  tt=$(egress "$T_SB" $ALLOW_T); to=$(egress "$T_SB" $ALLOW_A); td=$(egress "$T_SB" $DENY)
  echo "        apple: own=$aa other=$ao deny=$ad"
  echo "        tart : own=$tt other=$to deny=$td"
  if [ "$td" = 000 ] && [ "$tt" != 000 ]; then
    ok "T1: the slot pool ENFORCES on tart — deny blocked, allowlist passes"
  else
    bad "T1: no enforcement on tart (own=$tt deny=$td) — the pool is apple-specific and the plan"
    echo "           must say so"
  fi
  if [ "$ad" = 000 ] && [ "$aa" != 000 ] && [ "$ao" = 000 ] && [ "$to" = 000 ]; then
    ok "T2: apple and tart hold DIFFERENT allowlists in different slots simultaneously —"
    echo "           the pool is backend-agnostic, as the address-keyed design predicts"
  else
    bad "T2: cross-backend policy is not independent (apple own=$aa other=$ao deny=$ad;"
    echo "           tart own=$tt other=$to deny=$td)"
  fi
fi

say "T3 TEARDOWN — does a table delete restore the tart guest?"
pre=$(egress "$T_SB" $DENY)
pfctl -a "$ANCHOR" -t yt_src_1 -T delete "$T_IP" >/dev/null 2>&1
post=$(egress "$T_SB" $DENY)
echo "        tart->deny before=$pre after=$post"
if [ "$pre" = 000 ] && [ "$post" != 000 ]; then
  ok "table delete alone restores tart egress — teardown needs no rule reload on either backend"
elif [ "$pre" != 000 ]; then
  unk "tart was not blocked before the delete; teardown unmeasured"
else
  bad "tart still blocked after the delete"
fi

mn=$(pfctl -s rules 2>/dev/null | grep -c . || true)
if [ "${mn:-0}" = "${MAIN_BEFORE:-0}" ]; then ok "main ruleset unchanged ($mn)"; else bad "MAIN CHANGED $MAIN_BEFORE->$mn"; fi
printf '\n== TOTALS ==\n   pass=%d fail=%d unknown=%d\n' "$PASS" "$FAIL" "$UNKNOWN"

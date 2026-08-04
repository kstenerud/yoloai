#!/bin/bash
# ABOUTME: Does Shape B (static rules + pf tables) actually ENFORCE, with the correct rule form?
# ABOUTME: Its only enforcement evidence used the `out` form later proved fail-open.
#
# Run: sudo bash pf_shapeb.sh <sandbox-A> <sandbox-B>
#
# WHY THIS EXISTS
#   Shape B was rejected in favour of per-sandbox sub-anchors, and the plan says it was "measured
#   working". It was not. pf-authz-final.txt F4 loaded 32 rules of the form
#       block drop out from <src> to any / pass out from <src> to <dst>
#   which pf-enforce.txt E1 later measured as FAIL-OPEN — pf NATs outbound, so a rule filtering
#   `out` sees the host's address and matches nothing. F4 proved those rules LOAD and that table
#   ops work; it never showed them filtering anything.
#
#   That matters now because pf-shapea2.txt C2 confirmed Shape A's ceiling: a `pass in quick all`
#   in any sub-anchor the grant can write voids every other sandbox's block. Shape B's grant cannot
#   express that — its whole argument surface is one IP address — so if Shape B enforces, the
#   choice becomes a real tradeoff (no cap vs lower ceiling) rather than a foregone conclusion.
#   A fallback nobody has measured is not a fallback.
#
#   B1 does Shape B enforce at all, with `in quick` and tables?
#   B2 do two slots give two sandboxes INDEPENDENT allowlists? (Shape A's advantage, tested on B)
#   B3 is Shape B's ceiling actually lower — can a table-grant holder reach any bypass?
#
# SAFETY: only com.apple/yoloai_b. The main ruleset is never loaded or flushed; its count is
# asserted unchanged at the end. pf enable/disable is never touched.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0 <A> <B>"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"
A_SB="${1:?need sandbox A}"; B_SB="${2:?need sandbox B}"

ANCHOR="com.apple/yoloai_b"
SUDOERS=/etc/sudoers.d/yoloai-shapeb-probe
ALLOW_A=1.1.1.1
ALLOW_B=1.0.0.2
DENY=1.0.0.1
PASS=0; FAIL=0; UNKNOWN=0
RESULTS="$(cd "$(dirname "$0")" && pwd)/results/pf-shapeb.txt"
mkdir -p "$(dirname "$RESULTS")"
exec > >(tee "$RESULTS") 2>&1

asuser() { sudo -u "$U" "$@"; }
quiet_pf() { grep -viE 'use of -f option|main ruleset added|/etc/pf.conf for further|ALTQ|^$'; }
ipof() { asuser container inspect "yoloai-cli-$1" 2>/dev/null \
           | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d[0]["status"]["networks"][0].get("ipv4Address","").split("/")[0])' 2>/dev/null; }
egress() {
  local c
  c=$(asuser container exec "yoloai-cli-$1" curl -s -o /dev/null -w '%{http_code}' --max-time 6 "http://$2/" 2>/dev/null)
  c=${c//[^0-9]/}; c=${c: -3}; printf '%s' "${c:-000}"
}
say() { printf '\n== %s ==\n' "$*"; }
ok()  { PASS=$((PASS+1)); printf '   PASS    %s\n' "$*"; }
bad() { FAIL=$((FAIL+1)); printf '   FAIL    %s\n' "$*"; }
unk() { UNKNOWN=$((UNKNOWN+1)); printf '   UNKNOWN %s\n' "$*"; }

MAIN_BEFORE=$(pfctl -s rules 2>/dev/null | grep -c . || true)
cleanup() {
  echo
  echo "== cleanup =="
  pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
  rm -f "$SUDOERS" /tmp/pfsb.*
  echo "   anchor rules=$(pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -c . || true)"
  echo "   main ruleset: before=$MAIN_BEFORE after=$(pfctl -s rules 2>/dev/null | grep -c . || true)"
  echo "   sudoers probe present: $([ -e "$SUDOERS" ] && echo YES-BAD || echo no)"
  echo "   A restored: allow=$(egress "$A_SB" $ALLOW_A) deny=$(egress "$A_SB" $DENY)"
  echo "   B restored: allow=$(egress "$B_SB" $ALLOW_B) deny=$(egress "$B_SB" $DENY)"
  chown "$U" "$RESULTS" 2>/dev/null
  echo "   results: $RESULTS"
  sync
}
A_IP=$(ipof "$A_SB"); B_IP=$(ipof "$B_SB")
[ -n "$A_IP" ] && [ -n "$B_IP" ] || { echo "could not resolve IPs (A=$A_IP B=$B_IP)"; exit 2; }
trap cleanup EXIT

echo "host: $(sw_vers -productVersion) | A=$A_SB($A_IP) B=$B_SB($B_IP)"
echo "allow_A=$ALLOW_A allow_B=$ALLOW_B deny=$DENY | main=$MAIN_BEFORE rules"

# ---------------------------------------------------------------------------
say "B0 BASELINE"
z=""
for p in "$A_SB $ALLOW_A" "$A_SB $ALLOW_B" "$A_SB $DENY" "$B_SB $ALLOW_A" "$B_SB $ALLOW_B" "$B_SB $DENY"; do
  # shellcheck disable=SC2086
  r=$(egress $p); z="$z$r "; done
echo "        six paths: $z"
if printf '%s' "$z" | grep -q "000"; then
  bad "baseline incomplete ($z); ABORTING"; exit 1
fi
ok "all six paths reachable — 'blocked' readings below are meaningful"

# ---------------------------------------------------------------------------
say "B1/B2 SHAPE B WITH THE CORRECT RULE FORM — two slots, two sandboxes"
# The slot pool as the plan describes it, but with `in quick` (the form E1 proved enforces)
# rather than the `out` form F4 actually loaded.
pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
{ for i in 0 1; do
    echo "table <yb_src_$i> persist"
    echo "table <yb_dst_$i> persist"
    echo "pass  in quick from <yb_src_$i> to <yb_dst_$i>"
    echo "block drop in quick from <yb_src_$i> to any"
  done; } > /tmp/pfsb.rules
pfctl -a "$ANCHOR" -f /tmp/pfsb.rules 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
n=$(pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -c . || true)
echo "        loaded $n filter rule(s) for 2 slots"
# Slot 0 -> A, slot 1 -> B, each with its own allowlist.
pfctl -a "$ANCHOR" -t yb_src_0 -T add "$A_IP"   >/dev/null 2>&1
pfctl -a "$ANCHOR" -t yb_dst_0 -T add "$ALLOW_A" >/dev/null 2>&1
pfctl -a "$ANCHOR" -t yb_src_1 -T add "$B_IP"   >/dev/null 2>&1
pfctl -a "$ANCHOR" -t yb_dst_1 -T add "$ALLOW_B" >/dev/null 2>&1
s0=$(pfctl -a "$ANCHOR" -t yb_src_0 -T show 2>/dev/null | tr -d ' \n')
s1=$(pfctl -a "$ANCHOR" -t yb_src_1 -T show 2>/dev/null | tr -d ' \n')
echo "        slot0 src=$s0  slot1 src=$s1"
if [ "${n:-0}" -lt 4 ] || [ "$s0" != "$A_IP" ] || [ "$s1" != "$B_IP" ]; then
  unk "rules or table membership did not land (n=$n s0=$s0 s1=$s1) — B1/B2 unmeasured"
else
  aa=$(egress "$A_SB" $ALLOW_A); ab=$(egress "$A_SB" $ALLOW_B); ad=$(egress "$A_SB" $DENY)
  ba=$(egress "$B_SB" $ALLOW_A); bb=$(egress "$B_SB" $ALLOW_B); bd=$(egress "$B_SB" $DENY)
  echo "        A: own=$aa other=$ab deny=$ad | B: other=$ba own=$bb deny=$bd"
  if [ "$ad" = 000 ] && [ "$aa" != 000 ]; then
    ok "B1: Shape B ENFORCES with the in-quick form — deny blocked, allowlist passes"
  else
    bad "B1: Shape B does not enforce (own=$aa deny=$ad) — it is not a viable fallback"
  fi
  if [ "$ab" = 000 ] && [ "$ba" = 000 ] && [ "$bb" != 000 ] && [ "$bd" = 000 ]; then
    ok "B2: two slots give two sandboxes INDEPENDENT allowlists — Shape A's advantage is not"
    echo "           unique to it; Shape B's real cost is the slot COUNT, not per-sandbox policy"
  else
    bad "B2: slots do not give independent policy (A other=$ab; B other=$ba own=$bb deny=$bd)"
  fi
fi

# ---------------------------------------------------------------------------
say "B3 IS SHAPE B'S CEILING ACTUALLY LOWER? (the reason to reconsider it)"
# Shape A's grant can load `pass in quick all` into a sub-anchor and void every other sandbox's
# block — measured, pf-shapea2.txt C2. Shape B's grant is table membership only. Install it and
# try to reach a bypass at all.
cat > /tmp/pfsb.sudoers <<EOF
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai_b -t yb_(src|dst)_[01] -T (add|delete|flush|show)( [0-9a-fA-F.:/]+)?\$
EOF
if ! visudo -c -f /tmp/pfsb.sudoers >/dev/null 2>&1; then
  bad "Shape B policy fails visudo -c"
else
  install -m 0440 -o root -g wheel /tmp/pfsb.sudoers "$SUDOERS"
  ok "Shape B table-only policy installed"
  su "$U" -c "sudo -K" >/dev/null 2>&1
  probe() {
    local cmd=$1 out rc
    out=$(su "$U" -c "sudo -k -n $cmd </dev/null" 2>&1); rc=$?
    if [ "$rc" -eq 0 ]; then printf 'permit'
    elif printf '%s' "$out" | grep -qiE "not allowed to execute|may not run|a password is required"; then printf 'refuse-by-policy'
    else printf 'ran-but-failed'; fi
  }
  m=$(cat <<'MATRIX'
intended add|permit|/sbin/pfctl -a com.apple/yoloai_b -t yb_src_0 -T add 192.0.2.5
intended delete|permit|/sbin/pfctl -a com.apple/yoloai_b -t yb_src_0 -T delete 192.0.2.5
CEILING load any ruleset|refuse-by-policy|/sbin/pfctl -a com.apple/yoloai_b -f -
CEILING load into a sub-anchor|refuse-by-policy|/sbin/pfctl -a com.apple/yoloai_b/evil -f -
CEILING flush the anchor|refuse-by-policy|/sbin/pfctl -a com.apple/yoloai_b -F rules
CEILING disable pf|refuse-by-policy|/sbin/pfctl -d
CEILING flush all|refuse-by-policy|/sbin/pfctl -F all
CEILING kill a table|refuse-by-policy|/sbin/pfctl -a com.apple/yoloai_b -t yb_src_0 -T kill
CEILING another anchor's table|refuse-by-policy|/sbin/pfctl -a com.apple/other -t yb_src_0 -T add 192.0.2.5
MATRIX
)
  while IFS='|' read -r label expect cmd; do
    [ -n "$label" ] || continue
    got=$(probe "$cmd")
    if [ "$got" = "$expect" ]; then ok "$label -> $got"; else bad "$label -> $got, expected $expect"; fi
  done <<< "$m"
  c=$(probe "/usr/bin/true")
  if [ "$c" = "refuse-by-policy" ]; then ok "CONTROL: unlisted command refused by policy"
  else bad "CONTROL: unlisted -> $c"; fi
  # The decisive difference: under Shape A this same holder could void every block. Under
  # Shape B the only reachable mutation is table membership.
  echo "        => Shape A's holder could load 'pass in quick all' (C2, measured). Shape B's"
  echo "           cannot load any ruleset at all; the reachable surface is one address."
fi

main_now=$(pfctl -s rules 2>/dev/null | grep -c . || true)
if [ "${main_now:-0}" = "${MAIN_BEFORE:-0}" ]; then ok "main ruleset unchanged ($main_now)"
else bad "MAIN RULESET CHANGED $MAIN_BEFORE->$main_now"; fi

printf '\n== TOTALS ==\n   pass=%d fail=%d unknown=%d\n' "$PASS" "$FAIL" "$UNKNOWN"

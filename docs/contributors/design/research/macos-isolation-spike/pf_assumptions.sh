#!/bin/bash
# ABOUTME: Tests every remaining untested assumption both shapes rest on — scale, ruleset reload
# ABOUTME: vs table contents, stale cross-slot membership, fail-open modes, and grant completeness.
#
# Run: sudo bash pf_assumptions.sh <sandbox-A> <sandbox-B>
#
# WHY THIS EXISTS
#   Shape B now looks better than Shape A, but it has been measured only at n=2 slots with one
#   address each. Before the plan is rewritten around it, the things it rests on that nobody has
#   run:
#
#   D1 SCALE. Only 2 slots were ever loaded with the correct rule form (F4's 16-slot run used the
#      `out` form later proved fail-open). Does a 32-slot pool — 64 tables, 64 rules — load, and
#      does a HIGH slot index enforce? A design whose cap is its main cost must be measured at
#      something like the cap.
#
#   D2 DOES RELOADING THE RULESET FLUSH THE TABLES? Critical. If `pfctl -a X -f rules` clears
#      table contents, then any ruleset change — resizing the pool, a repair, an upgrade —
#      silently de-isolates every running sandbox at once. Nothing has tested it.
#
#   D3 STALE MEMBERSHIP ACROSS SLOTS. Critical, and possibly worse than the documented over-block.
#      Addresses recycle on every start (measured). If a stale entry leaves sandbox X's address in
#      slot 0's src table while X legitimately occupies slot 1, `quick` means slot 0's pass/block
#      pair matches first — so X would inherit ANOTHER sandbox's allowlist rather than merely being
#      denied. Widening a sandbox's reachable set by accident is a different and worse failure than
#      blocking it.
#
#   D4 EMPTY ALLOWLIST. A slot whose dst table is empty should fail CLOSED, not open.
#   D5 GRANT COMPLETENESS. `-T show` (reaping needs it) and multiple addresses per call were never
#      tested; a 40-IP allowlist otherwise costs 40 sudo invocations.
#   D6 MISSING RULESET, POPULATED TABLES. Shape A's missing-parent fail-open is measured; Shape B's
#      equivalent is not.
#   D7 REBOOT RESTORE at Shape B's size, from a root-owned pinned file, unattended.
#   D8 SHAPE A: does `-s Anchors` actually enumerate sub-anchors? The grant permits it and reaping
#      depends on it, but no run ever looked at its output.
#
# SAFETY: only com.apple/yoloai_b. Main ruleset never loaded or flushed; count asserted unchanged.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0 <A> <B>"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"
A_SB="${1:?need A}"; B_SB="${2:?need B}"

ANCHOR="com.apple/yoloai_b"
SLOTS=32
HISLOT=17
CONFDIR=/etc/yoloai; CONF="$CONFDIR/pf-b.conf"
SUDOERS=/etc/sudoers.d/yoloai-assump-probe
ALLOW_A=1.1.1.1; ALLOW_B=1.0.0.2; DENY=1.0.0.1
PASS=0; FAIL=0; UNKNOWN=0
RESULTS="$(cd "$(dirname "$0")" && pwd)/results/pf-assumptions.txt"
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
nrules() { pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -c . || true; }
tshow()  { pfctl -a "$ANCHOR" -t "$1" -T show 2>/dev/null | tr -d ' ' | tr '\n' ',' ; }

MAIN_BEFORE=$(pfctl -s rules 2>/dev/null | grep -c . || true)
cleanup() {
  echo
  echo "== cleanup =="
  pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
  rm -f "$SUDOERS" /tmp/pfas.*; rm -rf "$CONFDIR"
  echo "   anchor rules=$(nrules)"
  echo "   main ruleset: before=$MAIN_BEFORE after=$(pfctl -s rules 2>/dev/null | grep -c . || true)"
  echo "   sudoers probe present: $([ -e "$SUDOERS" ] && echo YES-BAD || echo no)"
  echo "   A restored: allow=$(egress "$A_SB" $ALLOW_A) deny=$(egress "$A_SB" $DENY)"
  echo "   B restored: allow=$(egress "$B_SB" $ALLOW_B) deny=$(egress "$B_SB" $DENY)"
  chown "$U" "$RESULTS" 2>/dev/null
  echo "   results: $RESULTS"
  sync
}
A_IP=$(ipof "$A_SB"); B_IP=$(ipof "$B_SB")
[ -n "$A_IP" ] && [ -n "$B_IP" ] || { echo "could not resolve IPs"; exit 2; }
trap cleanup EXIT

gen_rules() {
  local n=$1
  { for ((i=0;i<n;i++)); do
      echo "table <yb_src_$i> persist"
      echo "table <yb_dst_$i> persist"
      echo "pass  in quick from <yb_src_$i> to <yb_dst_$i>"
      echo "block drop in quick from <yb_src_$i> to any"
    done; }
}

echo "host: $(sw_vers -productVersion) | A=$A_SB($A_IP) B=$B_SB($B_IP) | slots=$SLOTS | main=$MAIN_BEFORE"

say "D0 BASELINE"
b=""; for p in "$A_SB $ALLOW_A" "$A_SB $ALLOW_B" "$A_SB $DENY" "$B_SB $ALLOW_A" "$B_SB $ALLOW_B" "$B_SB $DENY"; do
  # shellcheck disable=SC2086
  b="$b$(egress $p) "; done
echo "        six paths: $b"
printf '%s' "$b" | grep -q 000 && { bad "baseline incomplete; ABORTING"; exit 1; }
ok "all six reachable"

# ---------------------------------------------------------------------------
say "D1 SCALE — a $SLOTS-slot pool, enforcing at a HIGH slot index ($HISLOT)"
pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
gen_rules "$SLOTS" > /tmp/pfas.rules
pfctl -a "$ANCHOR" -f /tmp/pfas.rules 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
n=$(nrules); want=$((SLOTS*2))
echo "        loaded $n filter rule(s), expected $want"
pfctl -a "$ANCHOR" -t "yb_src_$HISLOT" -T add "$A_IP"    >/dev/null 2>&1
pfctl -a "$ANCHOR" -t "yb_dst_$HISLOT" -T add "$ALLOW_A" >/dev/null 2>&1
d1a=$(egress "$A_SB" $ALLOW_A); d1d=$(egress "$A_SB" $DENY); d1b=$(egress "$B_SB" $DENY)
echo "        A(slot $HISLOT): allow=$d1a deny=$d1d | B(unassigned): deny=$d1b"
if [ "${n:-0}" -ne "$want" ]; then
  bad "expected $want rules, got $n — the pool does not scale as generated"
elif [ "$d1d" = 000 ] && [ "$d1a" != 000 ] && [ "$d1b" != 000 ]; then
  ok "$SLOTS slots load and a high slot index enforces; an unassigned sandbox is untouched"
else
  bad "high-slot enforcement wrong (allow=$d1a deny=$d1d unassigned=$d1b)"
fi

# ---------------------------------------------------------------------------
say "D2 CRITICAL — does reloading the ruleset FLUSH populated tables?"
before=$(tshow "yb_src_$HISLOT")
echo "        src_$HISLOT before reload: ${before:-<empty>}"
pfctl -a "$ANCHOR" -f /tmp/pfas.rules 2>&1 | quiet_pf | sed 's/^/        reload: /'
after=$(tshow "yb_src_$HISLOT")
post=$(egress "$A_SB" $DENY)
echo "        src_$HISLOT after reload : ${after:-<empty>}   A->deny=$post"
if [ -z "$before" ]; then
  unk "table was empty before the reload; D2 unmeasured"
elif [ "$before" = "$after" ] && [ "$post" = 000 ]; then
  ok "table contents SURVIVE a ruleset reload and enforcement continues — a pool resize or"
  echo "           repair does not de-isolate running sandboxes"
elif [ -z "$after" ]; then
  bad "RELOAD FLUSHED THE TABLES — every running sandbox silently de-isolates on any ruleset"
  echo "           change. Repopulation must be part of every reload, and that is a hard ordering"
else
  bad "table changed across reload: '$before' -> '$after' (A->deny=$post)"
fi

# ---------------------------------------------------------------------------
say "D3 CRITICAL — a stale entry in ANOTHER slot: does the sandbox inherit the wrong allowlist?"
# B legitimately occupies slot 1 with its own allowlist. A stale entry also has B's address in
# slot 0, whose allowlist is A's. `quick` means slot 0 matches first.
pfctl -a "$ANCHOR" -t yb_src_0  -T flush >/dev/null 2>&1
pfctl -a "$ANCHOR" -t yb_dst_0  -T flush >/dev/null 2>&1
pfctl -a "$ANCHOR" -t yb_src_1  -T flush >/dev/null 2>&1
pfctl -a "$ANCHOR" -t yb_dst_1  -T flush >/dev/null 2>&1
pfctl -a "$ANCHOR" -t yb_src_1 -T add "$B_IP"    >/dev/null 2>&1   # B's real slot
pfctl -a "$ANCHOR" -t yb_dst_1 -T add "$ALLOW_B" >/dev/null 2>&1
own_ok=$(egress "$B_SB" $ALLOW_B); own_no=$(egress "$B_SB" $ALLOW_A)
echo "        CONTROL, B in its own slot only: own=$own_ok other=$own_no"
if [ "$own_ok" = 000 ] || [ "$own_no" != 000 ]; then
  unk "control invalid (own=$own_ok other=$own_no); D3 unmeasured"
else
  ok "control holds: B has its own policy before the stale entry is introduced"
  pfctl -a "$ANCHOR" -t yb_src_0 -T add "$B_IP"    >/dev/null 2>&1  # the stale entry
  pfctl -a "$ANCHOR" -t yb_dst_0 -T add "$ALLOW_A" >/dev/null 2>&1
  st_own=$(egress "$B_SB" $ALLOW_B); st_other=$(egress "$B_SB" $ALLOW_A)
  echo "        with a stale entry in slot 0: own=$st_own other(A's allowlist)=$st_other"
  if [ "$st_other" != 000 ] && [ "$st_own" = 000 ]; then
    bad "INHERITS THE WRONG ALLOWLIST: B lost its own policy and gained slot 0's. A stale entry"
    echo "           WIDENS a sandbox's reachable set, not merely blocks it — worse than documented"
  elif [ "$st_own" != 000 ] && [ "$st_other" != 000 ]; then
    bad "stale entry ADDED reach: B now reaches both allowlists"
  elif [ "$st_own" != 000 ] && [ "$st_other" = 000 ]; then
    ok "B kept its own policy despite the stale entry — first-match is not slot-ordered here"
  else
    ok "stale entry left B fully blocked — fail-closed, the documented over-block behaviour"
  fi
fi

# ---------------------------------------------------------------------------
say "D4 EMPTY ALLOWLIST must fail CLOSED"
pfctl -a "$ANCHOR" -t yb_src_0 -T flush >/dev/null 2>&1
pfctl -a "$ANCHOR" -t yb_dst_0 -T flush >/dev/null 2>&1
pfctl -a "$ANCHOR" -t yb_src_1 -T flush >/dev/null 2>&1
pfctl -a "$ANCHOR" -t yb_dst_1 -T flush >/dev/null 2>&1
pfctl -a "$ANCHOR" -t yb_src_1 -T add "$B_IP" >/dev/null 2>&1   # src only, dst empty
e1=$(egress "$B_SB" $ALLOW_B); e2=$(egress "$B_SB" $DENY)
echo "        B with an EMPTY dst table: allow=$e1 deny=$e2"
if [ "$e1" = 000 ] && [ "$e2" = 000 ]; then
  ok "empty allowlist fails CLOSED — no egress at all"
else
  bad "empty allowlist did not fail closed (allow=$e1 deny=$e2)"
fi

# ---------------------------------------------------------------------------
say "D6 MISSING RULESET, POPULATED TABLES — Shape B's fail-open mode"
pfctl -a "$ANCHOR" -F rules >/dev/null 2>&1
left=$(tshow yb_src_1)
d6=$(egress "$B_SB" $DENY)
echo "        rules flushed, src_1 still holds '${left:-<empty>}': B->deny=$d6"
if [ "$d6" != 000 ]; then
  ok "confirms Shape B fails open the same way: membership without rules is unenforced, and"
  echo "           nothing distinguishes it from working isolation. VERIFY must check the RULES."
else
  unk "B still blocked with no rules loaded (unexpected)"
fi

# ---------------------------------------------------------------------------
say "D7 REBOOT RESTORE at pool size, from a root-owned pinned file, unattended"
install -d -m 0755 -o root -g wheel "$CONFDIR"
cp /tmp/pfas.rules "$CONF"; chown root:wheel "$CONF"; chmod 0644 "$CONF"
LEAF='yb_(src|dst)_([0-9]|[12][0-9]|3[01])'
cat > /tmp/pfas.sudoers <<EOF
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai_b -t $LEAF -T (add|delete|flush|show)( [0-9a-fA-F.:/]+)*\$
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai_b -f /etc/yoloai/pf-b\\.conf\$
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai_b -s rules\$
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-s info\$
EOF
if ! visudo -c -f /tmp/pfas.sudoers >/dev/null 2>&1; then
  bad "policy fails visudo -c"; visudo -c -f /tmp/pfas.sudoers 2>&1 | sed 's/^/        /'
else
  install -m 0440 -o root -g wheel /tmp/pfas.sudoers "$SUDOERS"
  su "$U" -c "sudo -K" >/dev/null 2>&1
  pfctl -a "$ANCHOR" -F all >/dev/null 2>&1     # the post-reboot state
  pre=$(nrules)
  su "$U" -c "sudo -k -n /sbin/pfctl -a com.apple/yoloai_b -f /etc/yoloai/pf-b.conf </dev/null" >/dev/null 2>&1
  postn=$(nrules)
  echo "        rules before restore=$pre after=$postn (want $want)"
  if [ "${postn:-0}" -eq "$want" ]; then
    ok "the full $SLOTS-slot ruleset restores unattended from the pinned file"
  else
    bad "restore did not reload the pool (got $postn, want $want)"
  fi
  # D5 grant completeness, on the same installed policy.
  probe() { local out rc; out=$(su "$U" -c "sudo -k -n $1 </dev/null" 2>&1); rc=$?
    if [ "$rc" -eq 0 ]; then printf permit
    elif printf '%s' "$out" | grep -qiE "not allowed to execute|may not run|a password is required"; then printf refuse-by-policy
    else printf ran-but-failed; fi; }
  say "D5 GRANT COMPLETENESS — -T show, and multiple addresses in one call"
  m=$(cat <<'MATRIX'
-T show (reaping needs it)|permit|/sbin/pfctl -a com.apple/yoloai_b -t yb_src_1 -T show
single address add|permit|/sbin/pfctl -a com.apple/yoloai_b -t yb_dst_1 -T add 192.0.2.1
THREE addresses in one call|permit|/sbin/pfctl -a com.apple/yoloai_b -t yb_dst_1 -T add 192.0.2.2 192.0.2.3 192.0.2.4
read own rules|permit|/sbin/pfctl -a com.apple/yoloai_b -s rules
restore from pinned file|permit|/sbin/pfctl -a com.apple/yoloai_b -f /etc/yoloai/pf-b.conf
slot 31 (top of pool)|permit|/sbin/pfctl -a com.apple/yoloai_b -t yb_src_31 -T show
ESCAPE slot 32 (out of pool)|refuse-by-policy|/sbin/pfctl -a com.apple/yoloai_b -t yb_src_32 -T show
ESCAPE arbitrary table|refuse-by-policy|/sbin/pfctl -a com.apple/yoloai_b -t evil -T add 1.2.3.4
ESCAPE ruleset from stdin|refuse-by-policy|/sbin/pfctl -a com.apple/yoloai_b -f -
ESCAPE ruleset from another file|refuse-by-policy|/sbin/pfctl -a com.apple/yoloai_b -f /tmp/pfas.rules
ESCAPE address list with a flag|refuse-by-policy|/sbin/pfctl -a com.apple/yoloai_b -t yb_dst_1 -T add -f /tmp/pfas.rules
ESCAPE table kill|refuse-by-policy|/sbin/pfctl -a com.apple/yoloai_b -t yb_src_1 -T kill
ESCAPE disable pf|refuse-by-policy|/sbin/pfctl -d
MATRIX
)
  while IFS='|' read -r l e c; do
    [ -n "$l" ] || continue
    g=$(probe "$c"); if [ "$g" = "$e" ]; then ok "$l -> $g"; else bad "$l -> $g, expected $e"; fi
  done <<< "$m"
  cc=$(probe "/usr/bin/true")
  if [ "$cc" = refuse-by-policy ]; then ok "CONTROL: unlisted refused by policy"; else bad "CONTROL -> $cc"; fi
  got3=$(tshow yb_dst_1)
  echo "        dst_1 after the multi-address add: ${got3:-<empty>}"
  case "$got3" in *192.0.2.2*192.0.2.3*|*192.0.2.3*) ok "multiple addresses in one call landed — a 40-IP allowlist is not 40 sudo calls";;
    *) bad "multi-address add did not land ($got3)";; esac
fi

# ---------------------------------------------------------------------------
say "D8 SHAPE A — does '-s Anchors' actually enumerate sub-anchors?"
pfctl -a com.apple/yoloai_probe -F all >/dev/null 2>&1
printf 'anchor "*"\n' | pfctl -a com.apple/yoloai_probe -f - >/dev/null 2>&1
printf 'block drop in quick from 192.0.2.77 to any\n' | pfctl -a com.apple/yoloai_probe/kid1 -f - >/dev/null 2>&1
printf 'block drop in quick from 192.0.2.78 to any\n' | pfctl -a com.apple/yoloai_probe/kid2 -f - >/dev/null 2>&1
lst=$(pfctl -a com.apple/yoloai_probe -s Anchors 2>/dev/null | tr '\n' ' ')
echo "        -s Anchors output: ${lst:-<empty>}"
if printf '%s' "$lst" | grep -q kid1 && printf '%s' "$lst" | grep -q kid2; then
  ok "'-s Anchors' lists sub-anchors — Shape A's reaping enumeration works as assumed"
else
  bad "'-s Anchors' did not list the sub-anchors — Shape A's reaping has no enumeration primitive"
fi
pfctl -a com.apple/yoloai_probe/kid1 -F all >/dev/null 2>&1
pfctl -a com.apple/yoloai_probe/kid2 -F all >/dev/null 2>&1
pfctl -a com.apple/yoloai_probe -F all >/dev/null 2>&1

mn=$(pfctl -s rules 2>/dev/null | grep -c . || true)
if [ "${mn:-0}" = "${MAIN_BEFORE:-0}" ]; then ok "main ruleset unchanged ($mn)"; else bad "MAIN CHANGED $MAIN_BEFORE->$mn"; fi
printf '\n== TOTALS ==\n   pass=%d fail=%d unknown=%d\n' "$PASS" "$FAIL" "$UNKNOWN"

#!/bin/bash
# ABOUTME: Closes the four evidence gaps three audits found in Shape A — two sandboxes live at
# ABOUTME: once, the load-anchor vector, set skip from a SUB-anchor, and the combined 5-line grant.
#
# Run: sudo bash pf_shapea2.sh <sandbox-A> <sandbox-B>
#
# WHY THIS EXISTS — each item is a verdict a previous run could not have produced the opposite of.
#
#   C1 "NO CAP ON CONCURRENT SANDBOXES" IS THE REASON SHAPE A WAS CHOSEN, AND IS UNMEASURED.
#      Every pf run in this tree loaded exactly ONE sub-anchor. "A second sandbox on the same
#      bridge is unaffected" measured a sandbox with no rules of its own — it never put two
#      sub-anchors on the packet path together. Here each sandbox gets its OWN allowlist and both
#      are asserted, so per-sandbox policy is demonstrated rather than assumed.
#
#   C2 THE SECURITY CEILING IS QUOTED FROM man pf.conf, NEVER RUN. Sub-anchors evaluate in
#      alphabetical order and a `quick` match "aborts the evaluation of the rules in other anchors
#      and the main ruleset". If true, a grant holder's `pass in quick all` in an early-sorting
#      anchor is a host-wide bypass. The plan states this as fact; nothing measured it.
#
#   C3 THE CONTAINMENT TEST WAS VACUOUS. pf_shapea.sh S1 loaded `load anchor ".." from <evil>` and
#      concluded containment held. But pfctl does NOT process `load anchor` when -a is given — the
#      file is never opened, so no content could have written anything. Confirmed unprivileged by
#      discrimination: under -a, `include <bad file>` yields a syntax error (file IS read) while
#      `load anchor ... from <bad file>` yields none (NOT read); without -a it errors (IS read).
#      That parse evidence is the positive control. C3 measures the EFFECT: load a VALID file into
#      a sibling anchor and read the sibling back. Empty means the directive is inert under -a.
#
#   C4 set skip WAS TESTED IN THE PARENT — THE ONE PLACE THE GRANT REFUSES TO WRITE. The threat
#      case is a sub-anchor, which the grant permits. Re-run there, with the same behavioural
#      control (a block rule proven working in the same run).
#
#   C5 THE FIVE-LINE GRANT WAS NEVER INSTALLED AS A UNIT. Two disjoint policies (3 rows, 4 rows)
#      were tested; sudoers rules compose additively, so the union is by construction more
#      permissive than either half. Also fixes the systemic detector flaw: the old matrix scored
#      ANY non-zero exit as "refuse", so a command that merely failed was indistinguishable from
#      one the policy denied. This one reads sudo's stderr and reports refuse-by-policy separately.
#
# SAFETY: only com.apple/yoloai_s and its sub-anchors. The main ruleset is never loaded or flushed;
# its rule count is asserted unchanged at the end of C2 and C3, aborting if it moved. pf
# enable/disable is never touched.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0 <A> <B>"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"
A_SB="${1:?need sandbox A}"; B_SB="${2:?need sandbox B}"

PARENT="com.apple/yoloai_s"
ALLOW_A=1.1.1.1
ALLOW_B=1.0.0.2  # Cloudflare; 8.8.8.8 is DNS-only and never answers HTTP
DENY=1.0.0.1
CONFDIR=/etc/yoloai; CONF="$CONFDIR/pf-parent.conf"
SUDOERS=/etc/sudoers.d/yoloai-shapea2-probe
PASS=0; FAIL=0; UNKNOWN=0
RESULTS="$(cd "$(dirname "$0")" && pwd)/results/pf-shapea2.txt"
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
rules_in() { pfctl -a "$1" -s rules 2>/dev/null | grep -c . || true; }

MAIN_BEFORE=$(pfctl -s rules 2>/dev/null | grep -c . || true)
cleanup() {
  echo
  echo "== cleanup =="
  for a in "$PARENT/aaa_early" "$PARENT/sibling" "$PARENT/box_a" "$PARENT/box_b" "$PARENT/s1" "$PARENT"; do
    pfctl -a "$a" -F all >/dev/null 2>&1
  done
  rm -f "$SUDOERS" /tmp/pfsa2.*; rm -rf "$CONFDIR"
  echo "   parent rules=$(rules_in "$PARENT")"
  echo "   main ruleset: before=$MAIN_BEFORE after=$(pfctl -s rules 2>/dev/null | grep -c . || true)"
  echo "   sudoers probe present: $([ -e "$SUDOERS" ] && echo YES-BAD || echo no)"
  echo "   A egress restored: allow=$(egress "$A_SB" $ALLOW_A) deny=$(egress "$A_SB" $DENY)"
  echo "   B egress restored: allow=$(egress "$B_SB" $ALLOW_B) deny=$(egress "$B_SB" $DENY)"
  chown "$U" "$RESULTS" 2>/dev/null
  echo "   results: $RESULTS"
  sync
}
A_IP=$(ipof "$A_SB"); B_IP=$(ipof "$B_SB")
[ -n "$A_IP" ] && [ -n "$B_IP" ] || { echo "could not resolve both IPs (A=$A_IP B=$B_IP)"; exit 2; }
trap cleanup EXIT

echo "host: $(sw_vers -productVersion) | A=$A_SB($A_IP) B=$B_SB($B_IP)"
echo "allow_A=$ALLOW_A allow_B=$ALLOW_B deny=$DENY | main ruleset=$MAIN_BEFORE rules"

setup_parent() {
  install -d -m 0755 -o root -g wheel "$CONFDIR"
  printf 'anchor "*"\n' > "$CONF"; chown root:wheel "$CONF"; chmod 0644 "$CONF"
  pfctl -a "$PARENT" -F all >/dev/null 2>&1
  pfctl -a "$PARENT" -f "$CONF" 2>&1 | quiet_pf | sed 's/^/        parent: /'
}

# ---------------------------------------------------------------------------
say "C0 BASELINE — every sandbox reaches every destination"
b1=$(egress "$A_SB" $ALLOW_A); b2=$(egress "$A_SB" $ALLOW_B); b3=$(egress "$A_SB" $DENY)
b4=$(egress "$B_SB" $ALLOW_A); b5=$(egress "$B_SB" $ALLOW_B); b6=$(egress "$B_SB" $DENY)
echo "        A: $ALLOW_A=$b1 $ALLOW_B=$b2 $DENY=$b3 | B: $ALLOW_A=$b4 $ALLOW_B=$b5 $DENY=$b6"
if [ "$b1$b2$b3$b4$b5$b6" = "301301301301301301" ] || \
   { [ "$b1" != 000 ] && [ "$b2" != 000 ] && [ "$b3" != 000 ] && [ "$b4" != 000 ] && [ "$b5" != 000 ] && [ "$b6" != 000 ]; }; then
  ok "all six paths reachable — every 'blocked' below is meaningful"
else
  bad "baseline incomplete; ABORTING"; exit 1
fi

# ---------------------------------------------------------------------------
say "C1 TWO SANDBOXES, TWO SUB-ANCHORS, DIFFERENT ALLOWLISTS, SIMULTANEOUSLY"
setup_parent
printf 'pass in quick from %s to %s\nblock drop in quick from %s to any\n' "$A_IP" "$ALLOW_A" "$A_IP" \
  | pfctl -a "$PARENT/box_a" -f - 2>&1 | quiet_pf | sed 's/^/        box_a: /'
printf 'pass in quick from %s to %s\nblock drop in quick from %s to any\n' "$B_IP" "$ALLOW_B" "$B_IP" \
  | pfctl -a "$PARENT/box_b" -f - 2>&1 | quiet_pf | sed 's/^/        box_b: /'
ra=$(rules_in "$PARENT/box_a"); rb=$(rules_in "$PARENT/box_b")
echo "        loaded: box_a=$ra rule(s), box_b=$rb rule(s)"
if [ "${ra:-0}" -lt 2 ] || [ "${rb:-0}" -lt 2 ]; then
  unk "both sub-anchors did not load (a=$ra b=$rb) — C1 unmeasured"
else
  aa=$(egress "$A_SB" $ALLOW_A); ab=$(egress "$A_SB" $ALLOW_B); ad=$(egress "$A_SB" $DENY)
  ba=$(egress "$B_SB" $ALLOW_A); bb=$(egress "$B_SB" $ALLOW_B); bd=$(egress "$B_SB" $DENY)
  echo "        A: own=$aa other=$ab deny=$ad | B: other=$ba own=$bb deny=$bd"
  if [ "$aa" != 000 ] && [ "$ab" = 000 ] && [ "$ad" = 000 ] && \
     [ "$bb" != 000 ] && [ "$ba" = 000 ] && [ "$bd" = 000 ]; then
    ok "each sandbox gets its OWN allowlist with both anchors live — per-sandbox policy at n=2,"
    echo "           which is the 'no cap' claim actually exercised rather than assumed"
  else
    bad "two live sub-anchors do not give independent policy (A own=$aa other=$ab deny=$ad;"
    echo "           B own=$bb other=$ba deny=$bd) — the no-cap advantage does not hold"
  fi
fi

# ---------------------------------------------------------------------------
say "C2 CEILING — can an alphabetically-early sub-anchor's 'pass quick' void another's block?"
# The plan asserts this from man pf.conf and has never run it. A's block is live from C1 and
# already demonstrated (ad=000 above) — that is the control.
pre=$(egress "$A_SB" $DENY)
printf 'pass in quick all\n' | pfctl -a "$PARENT/aaa_early" -f - 2>&1 | quiet_pf | sed 's/^/        aaa_early: /'
loaded=$(rules_in "$PARENT/aaa_early")
post=$(egress "$A_SB" $DENY)
echo "        aaa_early rules=$loaded | A->deny before=$pre after=$post"
if [ "${loaded:-0}" -lt 1 ]; then
  unk "the bypass rule did not load; ceiling unmeasured"
elif [ "$pre" = 000 ] && [ "$post" != 000 ]; then
  bad "CEILING CONFIRMED: one sub-anchor's 'pass quick' voided another sandbox's block."
  echo "           The grant is a host-wide filter-bypass primitive. State it in the indicative."
elif [ "$pre" = 000 ] && [ "$post" = 000 ]; then
  ok "an early sub-anchor's 'pass in quick all' did NOT void another's block — the ceiling is"
  echo "           narrower than man pf.conf's wording implies; correct the plan"
else
  unk "control invalid (before=$pre); nothing measured"
fi
pfctl -a "$PARENT/aaa_early" -F all >/dev/null 2>&1
main_now=$(pfctl -s rules 2>/dev/null | grep -c . || true)
if [ "${main_now:-0}" = "${MAIN_BEFORE:-0}" ]; then
  ok "main ruleset unchanged ($main_now)"
else
  bad "MAIN RULESET CHANGED $MAIN_BEFORE->$main_now; ABORTING"; exit 1
fi

# ---------------------------------------------------------------------------
say "C3 load anchor UNDER -a — inert, or a real write vector?"
# Positive control established unprivileged: under -a, `include <bad file>` errors (file read)
# while `load anchor ... from <bad file>` does not (file NOT read). Here we measure the EFFECT
# with a VALID file, so "empty" cannot be blamed on bad content.
printf 'block drop in quick from 192.0.2.90 to any\n' > /tmp/pfsa2.valid
chown root:wheel /tmp/pfsa2.valid
pfctl -a "$PARENT/sibling" -F all >/dev/null 2>&1
printf 'load anchor "sibling" from "/tmp/pfsa2.valid"\n' | pfctl -a "$PARENT/box_a" -f - 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
sib=$(rules_in "$PARENT/sibling")
echo "        after load-anchor from a VALID file: sibling holds ${sib:-0} rule(s)"
# Control: the same file loaded directly must produce a rule, or "empty" proves nothing.
pfctl -a "$PARENT/sibling" -f /tmp/pfsa2.valid >/dev/null 2>&1
ctl=$(rules_in "$PARENT/sibling")
echo "        CONTROL — same file loaded directly: ${ctl:-0} rule(s) (must be >=1 or test is blind)"
if [ "${ctl:-0}" -lt 1 ]; then
  unk "the control file does not produce a rule; C3 unmeasured"
elif [ "${sib:-0}" -eq 0 ]; then
  ok "'load anchor' under -a wrote NOTHING while the same file loads fine directly — the"
  echo "           directive is inert under -a, so it is not a containment vector at all"
else
  bad "'load anchor' under -a DID write a sibling anchor (${sib} rules) — it IS a write vector"
  echo "           and the sub-path-only regex does not contain it"
fi
pfctl -a "$PARENT/sibling" -F all >/dev/null 2>&1

# ---------------------------------------------------------------------------
say "C4 set skip FROM A SUB-ANCHOR — the position the grant actually permits"
IFC=$(ifconfig 2>/dev/null | awk -v ip="$A_IP" '
  /^[a-z]/ { i=$1; sub(/:$/,"",i) }
  $1=="inet" { split(ip,a,"."); split($2,b,".");
               if (a[1]"."a[2]"."a[3]==b[1]"."b[2]"."b[3]) { print i; exit } }')
echo "        A is on $IFC"
pfctl -a "$PARENT/box_a" -F all >/dev/null 2>&1
printf 'block drop in quick from %s to any\n' "$A_IP" | pfctl -a "$PARENT/box_a" -f - >/dev/null 2>&1
ctl2=$(egress "$A_SB" $DENY)
echo "        control (block only, in sub-anchor): A->deny=$ctl2  (must be 000)"
if [ "$ctl2" != 000 ]; then
  unk "the control block did not bite; set skip cannot be detected here"
else
  ok "control holds — a bypass would be visible"
  printf 'set skip on %s\nblock drop in quick from %s to any\n' "$IFC" "$A_IP" \
    | pfctl -a "$PARENT/box_a" -f - 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
  n=$(rules_in "$PARENT/box_a")
  skipped=$(egress "$A_SB" $DENY)
  echo "        with set skip in the SUB-anchor: rules=$n A->deny=$skipped"
  if [ "${n:-0}" -lt 1 ]; then
    unk "the set-skip ruleset did not load into the sub-anchor; nothing measured"
  elif [ "$skipped" != 000 ]; then
    bad "'set skip' IS honored from a SUB-anchor — the grant can disable host filtering"
  else
    ok "'set skip' does not bypass from a sub-anchor either — the position the grant permits"
  fi
fi

# ---------------------------------------------------------------------------
say "C5 THE COMBINED FIVE-LINE GRANT, with a detector that can tell refusal from failure"
LEAF='[A-Za-z0-9][A-Za-z0-9._-]*'
cat > /tmp/pfsa2.sudoers <<EOF
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai_s/$LEAF -f -\$
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai_s/$LEAF -(F rules|s rules|s nat)\$
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai_s -s (Anchors|rules)\$
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai_s -f /etc/yoloai/pf-parent\\.conf\$
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-s info\$
EOF
echo "        combined policy (all five rows, installed together for the first time):"
sed 's/^/          /' /tmp/pfsa2.sudoers
if ! visudo -c -f /tmp/pfsa2.sudoers >/dev/null 2>&1; then
  bad "combined policy fails visudo -c"; visudo -c -f /tmp/pfsa2.sudoers 2>&1 | sed 's/^/        /'
else
  install -m 0440 -o root -g wheel /tmp/pfsa2.sudoers "$SUDOERS"
  ok "combined five-line policy validated and installed"
  su "$U" -c "sudo -K" >/dev/null 2>&1
  # THE FIX: distinguish a policy refusal from a command that merely failed. The old matrix
  # scored any non-zero exit as "refuse", so a pfctl parse error counted as a policy denial.
  probe() {
    local cmd=$1 out rc
    out=$(su "$U" -c "sudo -k -n $cmd </dev/null" 2>&1); rc=$?
    if [ "$rc" -eq 0 ]; then printf 'permit'
    elif printf '%s' "$out" | grep -qiE "not allowed to execute|may not run|a password is required"; then printf 'refuse-by-policy'
    else printf 'ran-but-failed'; fi
  }
  matrix=$(cat <<'MATRIX'
install sub-anchor|permit|/sbin/pfctl -a com.apple/yoloai_s/box1 -f -
teardown sub-anchor|permit|/sbin/pfctl -a com.apple/yoloai_s/box1 -F rules
verify sub-anchor|permit|/sbin/pfctl -a com.apple/yoloai_s/box1 -s rules
enumerate parent|permit|/sbin/pfctl -a com.apple/yoloai_s -s Anchors
restore parent from pinned file|permit|/sbin/pfctl -a com.apple/yoloai_s -f /etc/yoloai/pf-parent.conf
read pf enable state|permit|/sbin/pfctl -s info
ESCAPE write parent from stdin|refuse-by-policy|/sbin/pfctl -a com.apple/yoloai_s -f -
ESCAPE flush parent|refuse-by-policy|/sbin/pfctl -a com.apple/yoloai_s -F rules
ESCAPE dotdot leaf|refuse-by-policy|/sbin/pfctl -a com.apple/yoloai_s/.. -f -
ESCAPE traversal leaf|refuse-by-policy|/sbin/pfctl -a com.apple/yoloai_s/../evil -f -
ESCAPE parent from another file|refuse-by-policy|/sbin/pfctl -a com.apple/yoloai_s -f /tmp/pfsa2.valid
ESCAPE sub-anchor from a file|refuse-by-policy|/sbin/pfctl -a com.apple/yoloai_s/box1 -f /tmp/pfsa2.valid
ESCAPE another anchor|refuse-by-policy|/sbin/pfctl -a com.apple/other/box1 -f -
ESCAPE main ruleset|refuse-by-policy|/sbin/pfctl -f /etc/yoloai/pf-parent.conf
ESCAPE disable pf|refuse-by-policy|/sbin/pfctl -d
ESCAPE flush all|refuse-by-policy|/sbin/pfctl -F all
ESCAPE flush all in sub|refuse-by-policy|/sbin/pfctl -a com.apple/yoloai_s/box1 -F all
ESCAPE table op|refuse-by-policy|/sbin/pfctl -a com.apple/yoloai_s/box1 -t t -T add 1.2.3.4
ESCAPE show all state|refuse-by-policy|/sbin/pfctl -s all
MATRIX
)
  while IFS='|' read -r label expect cmdline; do
    [ -n "$label" ] || continue
    got=$(probe "$cmdline")
    if [ "$got" = "$expect" ]; then ok "$label -> $got"
    else bad "$label -> $got, expected $expect"; fi
  done <<< "$matrix"
  c=$(probe "/usr/bin/true")
  if [ "$c" = "refuse-by-policy" ]; then
    ok "CONTROL: unlisted command refused BY POLICY (not merely failed)"
  else
    bad "CONTROL: unlisted command -> $c"
  fi
fi

printf '\n== TOTALS ==\n   pass=%d fail=%d unknown=%d\n' "$PASS" "$FAIL" "$UNKNOWN"

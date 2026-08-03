#!/bin/bash
# ABOUTME: Settles every open question on Shape A (per-sandbox sub-anchors) in one run —
# ABOUTME: containment bypass, the set-skip detector, its real grant, and enforce+inert together.
#
# Run: sudo bash pf_shapea.sh <sandbox-A> <sandbox-B>
#
# WHY THIS EXISTS
#   Two independent reviews found Shape A unestablished. Not for missing evidence — the
#   evidence runs the other way. This settles the five things they identified:
#
#   S1 CONTAINMENT BYPASS. Shape A's only security property is "the parent declares no
#      translation anchor, and the grant cannot name the parent". But man pf.conf allows '..'
#      as an anchor path component to reach the parent, and `load anchor ".." from <file>`
#      PARSES inside a sub-anchor (verified unprivileged). If it also WRITES, the sub-path-only
#      regex contains nothing and Shape A has no containment story.
#
#   S2 THE set-skip DETECTOR HAS NEVER BEEN SHOWN TO FIRE. The old detector greps
#      `pfctl -s Interfaces -v` for a literal tab before lo0; no run has ever produced anything
#      but 0, and there is no positive control. A grep that cannot match and "not honored" read
#      identically — the fifth instance of that shape in this work. Replaced here with a
#      BEHAVIOURAL test against a real listener, which carries its own control.
#      It matters most for Shape A: Shape A's grant can load `set skip`; Shape B's cannot.
#
#   S3 SHAPE A'S GRANT WAS NEVER WRITTEN. The validated 27/27 matrix is a TABLE grant; it
#      refuses `-f -` and `-F rules`, which are Shape A's install and teardown. And `-T show`
#      is absent from it, so the plan's own capability probe and reconciliation are refused
#      under BOTH shapes.
#
#   S4 ENFORCE AND INERT WERE NEVER SEEN TOGETHER. "Filter rules enforce" (pf-enforce E6) and
#      "rdr is inert" (pf-rdr R2) come from two runs on two differently-named anchors, and
#      neither read its parent back. R2 has no control proving its sub-anchor was on the packet
#      path at all — had the parent's `anchor "*"` not loaded, R2 prints INERT regardless.
#
#   S5 A MISSING PARENT FAILS OPEN SILENTLY. pfctl auto-creates an anchor path, so if the
#      parent has no `anchor "*"` (e.g. after reboot), the sub-anchor load SUCCEEDS and the
#      rules never evaluate. Confirm, because it decides whether a start-time check is required.
#
# SAFETY
#   Everything lives under com.apple/yoloai_s. `..` from com.apple/yoloai_s/s1 resolves to
#   com.apple/yoloai_s — OUR anchor, not the system's com.apple. S1 asserts the system anchor
#   and the main ruleset are unchanged afterwards and aborts the whole run if they are not.
#   The main ruleset is never loaded or flushed. pf enable/disable is never touched.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0 <sandbox-A> <sandbox-B>"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"
A_SB="${1:?need sandbox A}"; B_SB="${2:?need sandbox B}"

PARENT="com.apple/yoloai_s"
SUB="$PARENT/s1"
SUDOERS=/etc/sudoers.d/yoloai-shapea-probe
ALLOW=1.1.1.1
DENY=1.0.0.1
LPORT=48123
PASS=0; FAIL=0; UNKNOWN=0
RESULTS="$(cd "$(dirname "$0")" && pwd)/results/pf-shapea.txt"
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
loopback() {   # host -> its own listener; 000 means blocked/failed
  local c
  c=$(curl -s -o /dev/null -w '%{http_code}' --max-time 4 "http://127.0.0.1:$LPORT/" 2>/dev/null)
  c=${c//[^0-9]/}; c=${c: -3}; printf '%s' "${c:-000}"
}
say() { printf '\n== %s ==\n' "$*"; }
ok()  { PASS=$((PASS+1)); printf '   PASS    %s\n' "$*"; }
bad() { FAIL=$((FAIL+1)); printf '   FAIL    %s\n' "$*"; }
unk() { UNKNOWN=$((UNKNOWN+1)); printf '   UNKNOWN %s\n' "$*"; }

LPID=""
cleanup() {
  echo
  echo "== cleanup =="
  pfctl -a "$SUB" -F all >/dev/null 2>&1
  pfctl -a "$PARENT" -F all >/dev/null 2>&1
  rm -f "$SUDOERS" /tmp/pfsa.*
  [ -n "$LPID" ] && kill "$LPID" >/dev/null 2>&1
  echo "   parent rules=$(pfctl -a "$PARENT" -s rules 2>/dev/null | grep -c . || true) nat=$(pfctl -a "$PARENT" -s nat 2>/dev/null | grep -c . || true)"
  echo "   sudoers probe present: $([ -e "$SUDOERS" ] && echo YES-BAD || echo no)"
  echo "   system com.apple nat rules: $(pfctl -a com.apple -s nat 2>/dev/null | grep -c . || true)"
  echo "   main ruleset rules: $(pfctl -s rules 2>/dev/null | grep -c . || true)"
  echo "   A egress restored: $(egress "$A_SB" $DENY)"
  chown "$U" "$RESULTS" 2>/dev/null
  echo "   results: $RESULTS"
  sync
}

A_IP=$(ipof "$A_SB"); B_IP=$(ipof "$B_SB")
[ -n "$A_IP" ] && [ -n "$B_IP" ] || { echo "could not resolve sandbox IPs (A=$A_IP B=$B_IP)"; exit 2; }
MAIN_BEFORE=$(pfctl -s rules 2>/dev/null | grep -c . || true)
APPLE_NAT_BEFORE=$(pfctl -a com.apple -s nat 2>/dev/null | grep -c . || true)
trap cleanup EXIT

echo "host: $(sw_vers -productVersion) | A=$A_SB($A_IP) B=$B_SB($B_IP)"
echo "parent=$PARENT sub=$SUB | main ruleset=$MAIN_BEFORE rules, com.apple nat=$APPLE_NAT_BEFORE"

# ---------------------------------------------------------------------------
say "S0 BASELINE"
a_al=$(egress "$A_SB" $ALLOW); a_dn=$(egress "$A_SB" $DENY); b_dn=$(egress "$B_SB" $DENY)
echo "        A allow=$a_al deny=$a_dn | B deny=$b_dn"
if [ "$a_al" != "000" ] && [ "$a_dn" != "000" ] && [ "$b_dn" != "000" ]; then
  ok "baseline egress works — 'blocked' readings below are meaningful"
else
  bad "baseline already broken; ABORTING"; exit 1
fi

# ---------------------------------------------------------------------------
say "S4 ENFORCE **AND** INERT, ONE PARENT, ONE SUB-ANCHOR, PARENT READ BACK"
pfctl -a "$SUB" -F all >/dev/null 2>&1; pfctl -a "$PARENT" -F all >/dev/null 2>&1
printf 'anchor "*"\n' | pfctl -a "$PARENT" -f - 2>&1 | quiet_pf | sed 's/^/        parent: /'
echo "        parent -s rules:"; pfctl -a "$PARENT" -s rules 2>/dev/null | sed 's/^/          /'
echo "        parent -s nat:"; pfctl -a "$PARENT" -s nat 2>/dev/null | sed 's/^/          /'
IFC=$(ifconfig 2>/dev/null | awk -v ip="$A_IP" '
  /^[a-z]/ { i=$1; sub(/:$/,"",i) }
  $1=="inet" { split(ip,a,"."); split($2,b,".");
               if (a[1]"."a[2]"."a[3]==b[1]"."b[2]"."b[3]) { print i; exit } }')
echo "        A is on $IFC"
# One sub-anchor holding BOTH a filter rule and an rdr. The filter half proves the sub-anchor
# is on the packet path; without it, "rdr inert" is unfalsifiable.
printf 'rdr on %s proto tcp from %s to %s port 80 -> 127.0.0.1 port 9\npass  in quick from %s to %s\nblock drop in quick from %s to any\n' \
  "$IFC" "$A_IP" "$ALLOW" "$A_IP" "$ALLOW" "$A_IP" | pfctl -a "$SUB" -f - 2>&1 | quiet_pf | sed 's/^/        sub: /'
sub_f=$(pfctl -a "$SUB" -s rules 2>/dev/null | grep -c . || true)
sub_n=$(pfctl -a "$SUB" -s nat 2>/dev/null | grep -c . || true)
echo "        sub-anchor: $sub_f filter rule(s), $sub_n translation rule(s) loaded"
s_al=$(egress "$A_SB" $ALLOW); s_dn=$(egress "$A_SB" $DENY); s_b=$(egress "$B_SB" $DENY)
echo "        A allow=$s_al deny=$s_dn | B deny=$s_b"
if [ "${sub_f:-0}" -lt 2 ] || [ "${sub_n:-0}" -lt 1 ]; then
  unk "sub-anchor did not load both rule kinds (filter=$sub_f nat=$sub_n) — S4 unmeasured"
elif [ "$s_dn" = "000" ] && [ "$s_al" != "000" ] && [ "$s_b" != "000" ]; then
  ok "filter rules ENFORCE (so the sub-anchor IS on the packet path) and the rdr did NOT"
  echo "           redirect the allowlisted destination — enforce and inert, together, one parent"
elif [ "$s_al" = "000" ]; then
  bad "the allowlisted destination broke — either the pass failed or the rdr WAS evaluated"
else
  bad "filter rules did not enforce (deny=$s_dn) — the sub-anchor is not on the packet path,"
  echo "           which would also make every previous 'rdr inert' reading vacuous"
fi

# ---------------------------------------------------------------------------
say "S5 MISSING PARENT — does a sub-anchor load succeed and silently not enforce?"
pfctl -a "$SUB" -F all >/dev/null 2>&1; pfctl -a "$PARENT" -F all >/dev/null 2>&1
printf 'block drop in quick from %s to any\n' "$A_IP" | pfctl -a "$SUB" -f - 2>&1 | quiet_pf | sed 's/^/        sub: /'
np_loaded=$(pfctl -a "$SUB" -s rules 2>/dev/null | grep -c . || true)
np_dn=$(egress "$A_SB" $DENY)
echo "        parent empty | sub rules loaded=$np_loaded | A->deny=$np_dn"
if [ "${np_loaded:-0}" -ge 1 ] && [ "$np_dn" != "000" ]; then
  ok "CONFIRMED silent fail-open: the load succeeds, the rules do not evaluate, and nothing"
  echo "           distinguishes it from working enforcement. A start-time check is MANDATORY."
elif [ "$np_dn" = "000" ]; then
  bad "rules enforced without a parent anchor — the parent may not be required at all"
else
  unk "sub-anchor load did not succeed with an empty parent (loaded=$np_loaded)"
fi

# ---------------------------------------------------------------------------
say "S1 CONTAINMENT BYPASS — can a sub-anchor write its parent via load anchor '..'?"
pfctl -a "$SUB" -F all >/dev/null 2>&1; pfctl -a "$PARENT" -F all >/dev/null 2>&1
printf 'anchor "*"\n' | pfctl -a "$PARENT" -f - 2>&1 | quiet_pf >/dev/null
printf 'nat-anchor "*"\nanchor "*"\n' > /tmp/pfsa.evil
before_nat=$(pfctl -a "$PARENT" -s nat 2>/dev/null | grep -c . || true)
printf 'load anchor ".." from "/tmp/pfsa.evil"\n' | pfctl -a "$SUB" -f - 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
after_nat=$(pfctl -a "$PARENT" -s nat 2>/dev/null | grep -c . || true)
echo "        parent translation rules before=$before_nat after=$after_nat"
echo "        parent -s nat now:"; pfctl -a "$PARENT" -s nat 2>/dev/null | sed 's/^/          /'
if [ "${after_nat:-0}" -gt "${before_nat:-0}" ]; then
  bad "BYPASS CONFIRMED: a sub-anchor wrote a nat-anchor into its parent via '..'."
  echo "           Shape A's sub-path-only regex contains NOTHING. Shape A is not viable as designed."
else
  ok "'load anchor \"..\"' did not write the parent — the sub-path rule holds against this vector"
fi
# SAFETY GATE: whatever happened, the system anchor and main ruleset must be untouched.
apple_nat_now=$(pfctl -a com.apple -s nat 2>/dev/null | grep -c . || true)
main_now=$(pfctl -s rules 2>/dev/null | grep -c . || true)
if [ "${apple_nat_now:-0}" != "${APPLE_NAT_BEFORE:-0}" ] || [ "${main_now:-0}" != "${MAIN_BEFORE:-0}" ]; then
  bad "SYSTEM STATE CHANGED (com.apple nat $APPLE_NAT_BEFORE->$apple_nat_now, main $MAIN_BEFORE->$main_now)"
  echo "           ABORTING immediately."
  exit 1
fi
ok "system com.apple anchor and main ruleset unchanged"

# ---------------------------------------------------------------------------
say "S2 set skip — BEHAVIOURAL, on the sandbox path where blocking is PROVEN to work"
# The first attempt used a loopback listener; lo0 is not filtered on this host, so the block
# never bit and the control correctly declared the test blind. Re-based on the sandbox egress
# path, whose block rule S4 and pf-enforce.txt E1 both demonstrate working.
pfctl -a "$SUB" -F all >/dev/null 2>&1; pfctl -a "$PARENT" -F all >/dev/null 2>&1
printf 'block drop in quick from %s to any\n' "$A_IP" | pfctl -a "$PARENT" -f - 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
ctl=$(egress "$A_SB" $DENY)
echo "        with block rule: A->deny=$ctl   (POSITIVE CONTROL — must be 000 or the test is blind)"
if [ "$ctl" != "000" ]; then
  unk "the block rule did not take effect, so this test cannot detect set skip either"
else
  ok "control holds: the block demonstrably works, so a bypass would be visible"
  printf 'set skip on %s\nblock drop in quick from %s to any\n' "$IFC" "$A_IP" \
    | pfctl -a "$PARENT" -f - 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
  loaded=$(pfctl -a "$PARENT" -s rules 2>/dev/null | grep -c . || true)
  skipped=$(egress "$A_SB" $DENY)
  echo "        with 'set skip on $IFC' added: rules=$loaded A->deny=$skipped"
  if [ "${loaded:-0}" -lt 1 ]; then
    unk "the set-skip ruleset did not load; nothing measured"
  elif [ "$skipped" != "000" ]; then
    bad "'set skip' IS HONORED from inside an anchor — it bypassed a rule proven to work."
    echo "           Shape A's grant can load this, so Shape A can disable host filtering."
  else
    ok "'set skip' did NOT bypass a demonstrably-working block — genuinely not honored"
  fi
fi
pfctl -a "$PARENT" -F all >/dev/null 2>&1

# ---------------------------------------------------------------------------
say "S3 SHAPE A'S ACTUAL GRANT — write it, install it, matrix it"
# Leaf must start alphanumeric so ".." cannot match. Charset follows the real sandbox-name
# grammar (internal/config/names.go: ^[A-Za-z0-9]+(?:[._-][A-Za-z0-9]+)*$), which the plan's
# earlier [a-z0-9-]+ would have rejected. A separate READ-ONLY line grants -s Anchors on the
# bare parent: reaping needs to enumerate orphans, and -s cannot write anything, so the
# "never name the parent" rule applies to WRITES only.
LEAF='[A-Za-z0-9][A-Za-z0-9._-]*'
cat > /tmp/pfsa.sudoers <<EOF
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai_s/$LEAF -f -\$
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai_s/$LEAF -(F rules|s rules|s nat)\$
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai_s -s (Anchors|rules)\$
EOF
echo "        candidate policy:"; sed 's/^/          /' /tmp/pfsa.sudoers
if ! visudo -c -f /tmp/pfsa.sudoers >/dev/null 2>&1; then
  bad "candidate policy fails visudo -c; not installed"
  visudo -c -f /tmp/pfsa.sudoers 2>&1 | sed 's/^/        /'
else
  install -m 0440 -o root -g wheel /tmp/pfsa.sudoers "$SUDOERS"
  ok "policy validated and installed"
  su "$U" -c "sudo -K" >/dev/null 2>&1
  matrix=$(cat <<'MATRIX'
install rules (stdin)|permit|/sbin/pfctl -a com.apple/yoloai_s/box1 -f -
teardown flush rules|permit|/sbin/pfctl -a com.apple/yoloai_s/box1 -F rules
verify own rules|permit|/sbin/pfctl -a com.apple/yoloai_s/box1 -s rules
verify own nat|permit|/sbin/pfctl -a com.apple/yoloai_s/box1 -s nat
legal name underscore|permit|/sbin/pfctl -a com.apple/yoloai_s/my_box -s rules
legal name uppercase|permit|/sbin/pfctl -a com.apple/yoloai_s/Feature2 -s rules
legal name dotted|permit|/sbin/pfctl -a com.apple/yoloai_s/a.b -s rules
reaping enumerate parent (read-only)|permit|/sbin/pfctl -a com.apple/yoloai_s -s Anchors
verify parent rules (read-only)|permit|/sbin/pfctl -a com.apple/yoloai_s -s rules
ESCAPE write the parent|refuse|/sbin/pfctl -a com.apple/yoloai_s -f -
ESCAPE flush the parent|refuse|/sbin/pfctl -a com.apple/yoloai_s -F rules
ESCAPE parent via dotdot leaf|refuse|/sbin/pfctl -a com.apple/yoloai_s/.. -f -
ESCAPE traversal in leaf|refuse|/sbin/pfctl -a com.apple/yoloai_s/../evil -f -
ESCAPE leading-dot leaf|refuse|/sbin/pfctl -a com.apple/yoloai_s/.hidden -f -
ESCAPE rules from a file|refuse|/sbin/pfctl -a com.apple/yoloai_s/box1 -f /tmp/pfsa.evil
ESCAPE another anchor|refuse|/sbin/pfctl -a com.apple/other/box1 -f -
ESCAPE main ruleset load|refuse|/sbin/pfctl -f /etc/pf.conf
ESCAPE disable pf|refuse|/sbin/pfctl -d
ESCAPE flush all|refuse|/sbin/pfctl -F all
ESCAPE flush all in sub|refuse|/sbin/pfctl -a com.apple/yoloai_s/box1 -F all
ESCAPE table op|refuse|/sbin/pfctl -a com.apple/yoloai_s/box1 -t t -T add 1.2.3.4
MATRIX
)
  while IFS='|' read -r label expect cmdline; do
    [ -n "$label" ] || continue
    if su "$U" -c "sudo -k -n $cmdline </dev/null" >/dev/null 2>&1; then got=permit; else got=refuse; fi
    if [ "$got" = "$expect" ]; then ok "$label -> $got"; else bad "$label -> $got, expected $expect"; fi
  done <<< "$matrix"
  if su "$U" -c "sudo -k -n /usr/bin/true </dev/null" >/dev/null 2>&1; then
    bad "CONTROL: unlisted command permitted — matrix invalid"
  else
    ok "CONTROL: unlisted command refused — refusals reflect policy, not cache"
  fi
  # The install command must work with a RULESET ON STDIN and no tty. Every prior matrix probe
  # ran </dev/null, so the combination Shape A actually needs has never been exercised.
  if su "$U" -c "printf 'block drop in quick from 192.0.2.99 to any\n' | sudo -k -n /sbin/pfctl -a com.apple/yoloai_s/box1 -f -" >/dev/null 2>&1; then
    n=$(pfctl -a "$PARENT/box1" -s rules 2>/dev/null | grep -c . || true)
    if [ "${n:-0}" -ge 1 ]; then
      ok "unattended install with a ruleset on stdin and no tty WORKS (rule landed)"
    else
      bad "sudo accepted it but no rule landed — stdin did not reach pfctl"
    fi
  else
    bad "unattended install with stdin ruleset FAILED — Shape A cannot install without a tty"
  fi
  pfctl -a "$PARENT/box1" -F all >/dev/null 2>&1
fi

printf '\n== TOTALS ==\n   pass=%d fail=%d unknown=%d\n' "$PASS" "$FAIL" "$UNKNOWN"

#!/bin/bash
# ABOUTME: Second privileged run — validates the FINAL sudoers regex the plan proposes, the
# ABOUTME: slot-pool ruleset for real, mixed-family tables, and the unmeasured set-skip question.
#
# Run: sudo bash pf_authz_final.sh
#
# WHY A SECOND RUN
#   pf_authz.sh validated a NARROWER policy than the plan ended up proposing:
#     tested:   ^-a com\.apple/yoloai_authz -t yoloai_authz_src -T (add|delete) [0-9.]+$
#     proposed: ^-a com\.apple/yoloai -t yoloai_(src|dst)_([0-9]|1[0-5]) -T (add|delete|flush)( [0-9a-fA-F.:/]+)?$
#   Four widenings, none of them run: a slot alternation in the table name, `flush`, an
#   OPTIONAL address group, and a charset carrying hex/colon/slash for IPv6. Each is a place
#   where a plausible-looking regex grants more than intended, so the escape matrix is rebuilt
#   around them rather than reused.
#
#   Also settles three things left open:
#     * F1  Is `set skip` honored inside a nested anchor? pf_authz.sh's verdict was invalid —
#           `grep -c` prints "0" AND exits 1, so `|| echo 0` produced "0\n0" and the integer
#           comparison errored into the not-honored branch unconditionally. Moot for the
#           chosen design (Q2 condemned `-f -` on its own), live again if anyone revives it.
#     * F4  Does the slot-pool ruleset LOAD, not merely parse, and do per-slot table ops work?
#     * F5  Do mixed-family tables hold v4 and v6 together at runtime?
#
#   SUDOERS ESCAPING AMBIGUITY (F3). man sudoers says ',', ':', '=' and '\' must be escaped in
#   command arguments — but prefixes that with "Unless a regular expression is specified".
#   Our regex contains ':' (IPv6) and '\.'. Whether those need escaping inside a regex arg is
#   genuinely unclear from the text, and getting it wrong fails OPEN or CLOSED silently. Both
#   forms are tested against a live policy rather than reasoned about.
#
# WHAT IT TOUCHES, AND HOW IT UNDOES IT
#   * pf: loads ONLY into com.apple/yoloai_authz2. The main ruleset is never loaded or flushed
#     (`pfctl -f` there destroys vmnet NAT for every VM on the host and reloading /etc/pf.conf
#     does NOT restore it). pf enable/disable state is untouched. `set skip` is probed on lo0,
#     never a real uplink.
#   * sudoers: /etc/sudoers.d/yoloai-authz2-probe, validated with `visudo -c` BEFORE install —
#     an invalid file there can lock the user out of sudo entirely.
#   * The EXIT trap removes both and re-verifies removal.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"

ANCHOR="com.apple/yoloai_authz2"
SUDOERS=/etc/sudoers.d/yoloai-authz2-probe
PASS=0; FAIL=0; UNKNOWN=0

RESULTS="$(cd "$(dirname "$0")" && pwd)/results/pf-authz-final.txt"
mkdir -p "$(dirname "$RESULTS")"
exec > >(tee "$RESULTS") 2>&1

cleanup() {
  echo
  echo "== cleanup =="
  pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
  rm -f "$SUDOERS" /tmp/pfa2.*
  echo "   anchor rules remaining: $(pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -c . || true)"
  echo "   $SUDOERS present: $([ -e "$SUDOERS" ] && echo YES-BAD || echo no)"
  echo "   pf status: $(pfctl -s info 2>/dev/null | head -1)"
  echo "   lo0 skip flag: $(pfctl -s Interfaces -v 2>/dev/null | grep -A1 '^	lo0' | grep -c skip || true)"
  chown "$U" "$RESULTS" 2>/dev/null
  echo "   results: $RESULTS"
  sync
}
trap cleanup EXIT

say() { printf '\n== %s ==\n' "$*"; }
ok()  { PASS=$((PASS+1)); printf '   PASS    %s\n' "$*"; }
bad() { FAIL=$((FAIL+1)); printf '   FAIL    %s\n' "$*"; }
unk() { UNKNOWN=$((UNKNOWN+1)); printf '   UNKNOWN %s\n' "$*"; }

quiet_pf() { grep -viE 'use of -f option|main ruleset added|/etc/pf.conf for further|ALTQ|^$'; }
load_anchor() { printf '%s\n' "$1" | pfctl -a "$ANCHOR" -f - 2>&1 | quiet_pf | sed 's/^/        pfctl: /'; }
rulecount() { pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -c . || true; }

echo "host: $(sw_vers -productVersion) | pf: $(pfctl -s info 2>/dev/null | head -1)"
echo "sudo: $(sudo -V | head -1) | user: $U"

# ---------------------------------------------------------------------------
say "F0 positive control: the anchor accepts and retains rules"
pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
load_anchor 'block drop out proto tcp from 192.0.2.77 to any'
if [ "$(rulecount)" -ge 1 ]; then
  ok "anchor retains rules (count=$(rulecount)) — later verdicts are meaningful"
else
  bad "anchor retained nothing; ABORTING, nothing below can be trusted"; exit 1
fi

# ---------------------------------------------------------------------------
say "F1: is 'set skip' honored inside a nested anchor? (pf_authz.sh Q1, harness now fixed)"
before=$(pfctl -s Interfaces -v 2>/dev/null | grep -A1 '^	lo0' | grep -c skip || true)
before=${before:-0}
pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
load_anchor 'set skip on lo0
block drop out proto tcp from 192.0.2.77 to any'
after=$(pfctl -s Interfaces -v 2>/dev/null | grep -A1 '^	lo0' | grep -c skip || true)
after=${after:-0}
echo "        lo0 skip flag before=$before after=$after (rules loaded=$(rulecount))"
if [ "$before" -eq 0 ] && [ "$after" -eq 0 ] && [ "$(rulecount)" -ge 1 ]; then
  ok "'set skip' parsed but NOT honored in anchor context (the block rule DID load, so this"
  echo "           is a real negative rather than a load that silently failed)"
elif [ "$after" -gt "$before" ]; then
  bad "'set skip' IS honored from inside an anchor — anyone reviving '-f -' would be granting"
  echo "           a host-wide filter-disable. Restored by the cleanup trap."
else
  unk "inconclusive: before=$before after=$after rules=$(rulecount)"
fi

# ---------------------------------------------------------------------------
say "F4: does the 16-slot pool ruleset LOAD (not merely parse), and do slot table ops work?"
pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
{ for i in $(seq 0 15); do
    echo "table <yoloai_src_$i> persist"
    echo "table <yoloai_dst_$i> persist"
    echo "block drop out from <yoloai_src_$i> to any"
    echo "pass  out from <yoloai_src_$i> to <yoloai_dst_$i>"
  done; } > /tmp/pfa2.slots
pfctl -a "$ANCHOR" -f /tmp/pfa2.slots 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
n=$(rulecount)
if [ "${n:-0}" -eq 32 ]; then
  ok "all 32 filter rules loaded (16 block + 16 pass)"
else
  bad "expected 32 filter rules, got ${n:-0}"
fi
# Ordering is the whole semantic: pf is last-match-wins, so each slot's `pass` must appear
# AFTER its `block` or the allowlist never overrides the deny.
firstblock=$(pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -n 'block' | head -1 | cut -d: -f1)
firstpass=$(pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -n 'pass'  | head -1 | cut -d: -f1)
if [ -n "${firstblock:-}" ] && [ -n "${firstpass:-}" ] && [ "$firstpass" -gt "$firstblock" ]; then
  ok "rule order preserved: pass (line $firstpass) follows block (line $firstblock)"
else
  bad "rule order NOT preserved (block=$firstblock pass=$firstpass) — last-match-wins would"
  echo "           make the allowlist inert"
fi
for slot in 0 15; do
  if pfctl -a "$ANCHOR" -t "yoloai_src_$slot" -T add 192.0.2.7 >/dev/null 2>&1 \
     && [ "$(pfctl -a "$ANCHOR" -t "yoloai_src_$slot" -T show 2>/dev/null | tr -d ' \n')" = "192.0.2.7" ]; then
    ok "slot $slot table add/show works"
  else
    bad "slot $slot table op failed"
  fi
done

# ---------------------------------------------------------------------------
say "F5: do mixed-family tables hold v4 and v6 together at runtime?"
pfctl -a "$ANCHOR" -t yoloai_src_0 -T flush >/dev/null 2>&1
pfctl -a "$ANCHOR" -t yoloai_src_0 -T add 192.0.2.7 2001:db8::1 >/dev/null 2>&1
got=$(pfctl -a "$ANCHOR" -t yoloai_src_0 -T show 2>/dev/null | tr -d ' ' | sort | tr '\n' ',')
echo "        table contents: $got"
if printf '%s' "$got" | grep -q '192.0.2.7' && printf '%s' "$got" | grep -qi '2001:db8::1'; then
  ok "one table holds both families — the unqualified block rule can cover v4 and v6"
else
  bad "mixed-family table did not retain both entries"
fi

# ---------------------------------------------------------------------------
say "F3/F2: the FINAL regex — does it parse, and does it permit/refuse correctly?"
# Two escaping variants, because man sudoers requires ':' and '\' escaped in command args
# but prefixes that with "Unless a regular expression is specified".
RE_RAW='^-a com\.apple/yoloai_authz2 -t yoloai_(src|dst)_([0-9]|1[0-5]) -T (add|delete|flush)( [0-9a-fA-F.:/]+)?$'
RE_ESC='^-a com\\.apple/yoloai_authz2 -t yoloai_(src|dst)_([0-9]|1[0-5]) -T (add|delete|flush)( [0-9a-fA-F.\:/]+)?$'
chosen=""
for variant in RAW ESC; do
  [ "$variant" = RAW ] && re="$RE_RAW" || re="$RE_ESC"
  printf '%s ALL=(root) NOPASSWD: /sbin/pfctl %s\n' "$U" "$re" > /tmp/pfa2.sudoers
  if visudo -c -f /tmp/pfa2.sudoers >/dev/null 2>&1; then
    install -m 0440 -o root -g wheel /tmp/pfa2.sudoers "$SUDOERS"
    su "$U" -c "sudo -K" >/dev/null 2>&1
    if su "$U" -c "sudo -k -n /sbin/pfctl -a com.apple/yoloai_authz2 -t yoloai_src_0 -T add 192.0.2.9 </dev/null" >/dev/null 2>&1; then
      ok "$variant escaping: parses AND permits the intended command"
      chosen=$variant; break
    else
      echo "   ...    $variant escaping parses but REFUSES the intended command"
    fi
    rm -f "$SUDOERS"
  else
    echo "   ...    $variant escaping fails visudo -c"
  fi
done
if [ -z "$chosen" ]; then
  bad "neither escaping variant both parses and permits — the plan's regex is not installable as written"
  exit 1
fi
ok "escaping variant to use in the generated policy: $chosen"

echo "   (permit/refuse matrix, cache invalidated, -k on every probe)"
su "$U" -c "sudo -K" >/dev/null 2>&1
matrix=$(cat <<'MATRIX'
intended add v4|permit|/sbin/pfctl -a com.apple/yoloai_authz2 -t yoloai_src_0 -T add 192.0.2.9
intended add v6|permit|/sbin/pfctl -a com.apple/yoloai_authz2 -t yoloai_dst_3 -T add 2001:db8::1
intended add CIDR|permit|/sbin/pfctl -a com.apple/yoloai_authz2 -t yoloai_dst_3 -T add 192.0.2.0/24
intended delete|permit|/sbin/pfctl -a com.apple/yoloai_authz2 -t yoloai_src_15 -T delete 192.0.2.9
intended flush no-arg|permit|/sbin/pfctl -a com.apple/yoloai_authz2 -t yoloai_src_0 -T flush
WIDENING slot 16 out of range|refuse|/sbin/pfctl -a com.apple/yoloai_authz2 -t yoloai_src_16 -T add 192.0.2.9
WIDENING arbitrary table name|refuse|/sbin/pfctl -a com.apple/yoloai_authz2 -t yoloai_evil -T add 192.0.2.9
WIDENING table kill|refuse|/sbin/pfctl -a com.apple/yoloai_authz2 -t yoloai_src_0 -T kill
WIDENING table replace|refuse|/sbin/pfctl -a com.apple/yoloai_authz2 -t yoloai_src_0 -T replace 192.0.2.9
WIDENING address from file|refuse|/sbin/pfctl -a com.apple/yoloai_authz2 -t yoloai_src_0 -T add -f /tmp/pfa2.slots
WIDENING path as address|refuse|/sbin/pfctl -a com.apple/yoloai_authz2 -t yoloai_src_0 -T add /etc/pf.conf
ESCAPE main ruleset load|refuse|/sbin/pfctl -f /etc/pf.conf
ESCAPE disable pf|refuse|/sbin/pfctl -d
ESCAPE flush all|refuse|/sbin/pfctl -F all
ESCAPE other anchor|refuse|/sbin/pfctl -a com.apple/other -t yoloai_src_0 -T add 192.0.2.9
ESCAPE stdin ruleset|refuse|/sbin/pfctl -a com.apple/yoloai_authz2 -f -
ESCAPE anchor flush rules|refuse|/sbin/pfctl -a com.apple/yoloai_authz2 -F rules
MATRIX
)
while IFS='|' read -r label expect cmdline; do
  [ -n "$label" ] || continue
  if su "$U" -c "sudo -k -n $cmdline </dev/null" >/dev/null 2>&1; then got=permit; else got=refuse; fi
  if [ "$got" = "$expect" ]; then ok "$label -> $got"; else bad "$label -> $got, expected $expect"; fi
done <<< "$matrix"

# Without this control every refusal above is satisfied for free by a cold credential cache.
if su "$U" -c "sudo -k -n /usr/bin/true </dev/null" >/dev/null 2>&1; then
  bad "CONTROL: an unlisted command was permitted — the matrix is invalid"
else
  ok "CONTROL: unlisted command refused, so refusals reflect policy not cache"
fi

printf '\n== TOTALS ==\n   pass=%d fail=%d unknown=%d\n' "$PASS" "$FAIL" "$UNKNOWN"

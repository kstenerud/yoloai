#!/bin/bash
# ABOUTME: Settles how yoloai may be authorized to drive host `pf` without a password —
# ABOUTME: what anchor-loaded rules can actually do, and whether a table-based grant is tight.
#
# Run: sudo bash pf_authz.sh
#
# WHY THIS EXISTS
#   The design needs an unattended privileged path (teardown has no tty). That means a
#   NOPASSWD sudoers line, which means the exact authorized command set is the security
#   boundary. Unprivileged probing (2026-08-03) established:
#     * sudoers matches command arguments as ONE CONCATENATED STRING, so a glob like
#       `-a com.apple/yoloai*` also permits `-a com.apple/yoloai -f /etc/pf.conf`.
#       An anchored regex is the only safe argument form. (man sudoers, "Wildcards in
#       command arguments")
#     * `visudo -c` accepts the unsafe glob and the safe regex identically — syntax
#       validation cannot catch this, so the policy must be generated, never hand-copied.
#     * sudoers cannot constrain STDIN, and `pfctl -a A -f -` reads its ruleset from stdin.
#       Parse-only (`-n`) showed the anchor parser ACCEPTS `set skip`, nat, rdr, `include`
#       and `load anchor`. If those are honored, that grant is host-wide, not per-sandbox.
#   Q1/Q2 below decide whether the `-f -` form is survivable at all. Q3 measures the
#   alternative that needs no stdin: static rules referencing pf TABLES, with only table
#   membership changing per sandbox.
#
# WHAT IT TOUCHES, AND HOW IT UNDOES IT
#   * pf: loads ONLY into the nested anchor com.apple/yoloai_authz. The main ruleset is
#     never loaded or flushed — `pfctl -f` there destroys vmnet NAT for every VM on the
#     host and reloading /etc/pf.conf does NOT restore it. pf enable/disable is untouched.
#   * sudoers: writes /etc/sudoers.d/yoloai-authz-probe, validated with `visudo -c` BEFORE
#     install (an invalid file there can lock the user out of sudo entirely).
#   * The EXIT trap removes both, unconditionally, and re-verifies removal.
#
# Interfaces are chosen to be harmless: `set skip` is probed on lo0, never on a real
# uplink, so a positive result costs nothing beyond the seconds it is loaded.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"

ANCHOR="com.apple/yoloai_authz"
SUDOERS=/etc/sudoers.d/yoloai-authz-probe
PASS=0; FAIL=0; UNKNOWN=0

# Capture the whole run. The first version of this script printed verdicts to the
# terminal and nowhere else, so the evidence lived only in one scrollback buffer —
# which is the one thing a research harness must never do. Owned by the invoking
# user, not root, so it can be read and committed without a second sudo.
RESULTS="$(cd "$(dirname "$0")" && pwd)/results/pf-authz-privileged.txt"
mkdir -p "$(dirname "$RESULTS")"
exec > >(tee "$RESULTS") 2>&1

cleanup() {
  echo
  echo "== cleanup =="
  pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
  rm -f "$SUDOERS"
  local leftover
  leftover=$(pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -c . || true)
  echo "   anchor rules remaining: ${leftover:-0}"
  echo "   $SUDOERS present: $([ -e "$SUDOERS" ] && echo YES-BAD || echo no)"
  echo "   pf status: $(pfctl -s info 2>/dev/null | head -1)"
  echo "   lo0 skip flag: $(pfctl -s Interfaces -v 2>/dev/null | grep -A1 '^	lo0' | grep -c skip || true)"
  rm -f /tmp/pfauthz.err /tmp/pfauthz.inc /tmp/pfauthz.sudoers
  chown "$U" "$RESULTS" 2>/dev/null
  echo "   results written to: $RESULTS"
  sync
}
trap cleanup EXIT

say() { printf '\n== %s ==\n' "$*"; }
ok()   { PASS=$((PASS+1));    printf '   PASS    %s\n' "$*"; }
bad()  { FAIL=$((FAIL+1));    printf '   FAIL    %s\n' "$*"; }
unk()  { UNKNOWN=$((UNKNOWN+1)); printf '   UNKNOWN %s\n' "$*"; }

# load ANCHOR-scoped rules from stdin, filtering pfctl's unconditional -f warning.
# Returns pfctl's own exit status, and always reports how many rules landed: a load
# that silently no-ops is otherwise indistinguishable from one that worked (A22).
load_anchor() {
  local rc
  printf '%s\n' "$1" | pfctl -a "$ANCHOR" -f - 2>/tmp/pfauthz.err; rc=$?
  grep -viE 'use of -f option|main ruleset added|/etc/pf.conf for further|^$' /tmp/pfauthz.err | sed 's/^/        pfctl: /'
  return $rc
}
anchor_rule_count() { pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -c . || true; }

echo "host: $(sw_vers -productVersion) | pf: $(pfctl -s info 2>/dev/null | head -1) | sudo: $(sudo -V | head -1)"
echo "invoking user: $U"

# ---------------------------------------------------------------------------
say "Q0 positive control: a plain block rule loads into the anchor and is visible"
# Without this, every 'not honored' verdict below is unfalsifiable — it would look
# identical to an anchor that never accepted anything at all.
pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
if load_anchor 'block drop out proto tcp from 192.0.2.77 to any' && [ "$(anchor_rule_count)" -ge 1 ]; then
  ok "anchor accepts and retains rules (count=$(anchor_rule_count)) — later verdicts are meaningful"
else
  bad "anchor did not retain a plain rule; ABORTING, nothing below can be trusted"
  exit 1
fi

# ---------------------------------------------------------------------------
say "Q1 DECISIVE: is 'set skip' honored when loaded into a nested anchor?"
# If yes, a NOPASSWD grant for `pfctl -a <anchor> -f -` lets anyone who can write to
# that stdin disable pf filtering on any interface — i.e. it is a host-wide grant
# wearing a per-sandbox costume, and the `-f -` form must be abandoned.
before=$(pfctl -s Interfaces -v 2>/dev/null | grep -A1 '^	lo0' | grep -c skip || true)
pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
if load_anchor 'set skip on lo0
block drop out proto tcp from 192.0.2.77 to any'; then
  after=$(pfctl -s Interfaces -v 2>/dev/null | grep -A1 '^	lo0' | grep -c skip || true)
  echo "        lo0 skip flag before=$before after=$after"
  if [ "$after" -gt "$before" ]; then
    bad "'set skip' IS honored from inside an anchor — the -f - grant is host-wide. Use tables (Q3)."
  else
    ok  "'set skip' parsed but NOT honored in anchor context — the -f - grant stays anchor-scoped"
  fi
else
  ok "anchor load REJECTED 'set skip' outright at load time (better still)"
fi

# ---------------------------------------------------------------------------
say "Q2: do nat / rdr / include / load-anchor take effect from inside an anchor?"
for spec in \
  'nat|nat on lo0 from 192.0.2.0/24 to any -> 127.0.0.1' \
  'rdr|rdr on lo0 proto tcp from 192.0.2.0/24 to any port 80 -> 127.0.0.1 port 8080'
do
  label=${spec%%|*}; rule=${spec#*|}
  pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
  if load_anchor "$rule"; then
    n=$(pfctl -a "$ANCHOR" -s nat 2>/dev/null | grep -c . || true)
    if [ "${n:-0}" -ge 1 ]; then
      bad "$label rules ARE installed in the anchor (count=$n) — the grant can redirect traffic"
    else
      ok  "$label parsed but installed nothing in the anchor's translation ruleset"
    fi
  else
    ok "$label rejected at load time"
  fi
done
# `include` is a PARSE-TIME file read: it pulls arbitrary root-readable rule text in.
# Scoped to the anchor it is far less alarming, but confirm it cannot reach the main set.
pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
mainbefore=$(pfctl -s rules 2>/dev/null | grep -c . || true)
printf 'block drop out proto tcp from 192.0.2.78 to any\n' > /tmp/pfauthz.inc
if load_anchor 'include "/tmp/pfauthz.inc"'; then
  mainafter=$(pfctl -s rules 2>/dev/null | grep -c . || true)
  echo "        main ruleset count before=$mainbefore after=$mainafter; anchor=$(anchor_rule_count)"
  if [ "${mainafter:-0}" -ne "${mainbefore:-0}" ]; then
    bad "include CHANGED the main ruleset — catastrophic; the -f - form is unusable"
  else
    ok "include pulled rules into the anchor only; main ruleset unchanged"
  fi
else
  ok "include rejected at load time"
fi
rm -f /tmp/pfauthz.inc

# ---------------------------------------------------------------------------
say "Q3: the table design — static rules, per-sandbox membership, no stdin"
# The alternative that needs no stdin at all: load the rules ONCE, referencing a table,
# then add/remove sandbox addresses with `pfctl -t <table> -T add|delete <ip>`, whose
# entire argument surface is an IP address an anchored regex can pin down.
pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
if load_anchor 'table <yoloai_authz_src> persist
block drop out from <yoloai_authz_src> to any'; then
  ok "static table-referencing ruleset loaded (rules=$(anchor_rule_count))"
  if pfctl -a "$ANCHOR" -t yoloai_authz_src -T add 192.0.2.77 >/dev/null 2>&1; then
    got=$(pfctl -a "$ANCHOR" -t yoloai_authz_src -T show 2>/dev/null | tr -d ' \n')
    if [ "$got" = "192.0.2.77" ]; then
      ok "table membership add works and is visible ($got)"
    else
      bad "table add reported success but shows '$got'"
    fi
    pfctl -a "$ANCHOR" -t yoloai_authz_src -T delete 192.0.2.77 >/dev/null 2>&1
    left=$(pfctl -a "$ANCHOR" -t yoloai_authz_src -T show 2>/dev/null | grep -c . || true)
    if [ "${left:-0}" -eq 0 ]; then
      ok "table membership delete works (teardown needs no rule reload)"
    else
      bad "table delete left ${left} entries"
    fi
  else
    bad "table add failed — the table design does not work in a nested anchor"
  fi
else
  bad "static table ruleset would not load into the anchor"
fi

# ---------------------------------------------------------------------------
say "Q4: does an anchored-regex sudoers grant permit the intended set and refuse escapes?"
# Written to sudoers.d and VALIDATED BEFORE INSTALL. An invalid file here locks the
# user out of sudo, so visudo -c is not optional.
cat > /tmp/pfauthz.sudoers <<EOF
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai_authz -t yoloai_authz_src -T (add|delete) [0-9.]+\$
EOF
if visudo -c -f /tmp/pfauthz.sudoers >/dev/null 2>&1; then
  install -m 0440 -o root -g wheel /tmp/pfauthz.sudoers "$SUDOERS"
  ok "policy validated and installed"
  # Reload the static ruleset so the table exists for the permit test.
  load_anchor 'table <yoloai_authz_src> persist
block drop out from <yoloai_authz_src> to any' >/dev/null 2>&1

  # The invoking user's sudo credential cache is WARM — this script was launched with
  # `sudo`. A cached credential makes every "refuse" case succeed on the cache and read
  # as "permit", so the escape tests would silently pass while proving nothing. Drop the
  # timestamp, and use `sudo -k -n` per command so each probe tests POLICY, not cache.
  su "$U" -c "sudo -K" >/dev/null 2>&1
  echo "   (permit/refuse matrix as $U, credential cache invalidated, -k on every probe)"
  matrix=$(cat <<'MATRIX'
intended add|permit|/sbin/pfctl -a com.apple/yoloai_authz -t yoloai_authz_src -T add 192.0.2.77
intended delete|permit|/sbin/pfctl -a com.apple/yoloai_authz -t yoloai_authz_src -T delete 192.0.2.77
ESCAPE main ruleset load|refuse|/sbin/pfctl -f /etc/pf.conf
ESCAPE disable pf|refuse|/sbin/pfctl -d
ESCAPE flush all|refuse|/sbin/pfctl -F all
ESCAPE other anchor|refuse|/sbin/pfctl -a com.apple/other -t yoloai_authz_src -T add 192.0.2.77
ESCAPE stdin ruleset|refuse|/sbin/pfctl -a com.apple/yoloai_authz -f -
ESCAPE table from file|refuse|/sbin/pfctl -a com.apple/yoloai_authz -t yoloai_authz_src -T add -f /tmp/x
ESCAPE table kill|refuse|/sbin/pfctl -a com.apple/yoloai_authz -t yoloai_authz_src -T kill
MATRIX
)
  while IFS='|' read -r label expect cmdline; do
    [ -n "$label" ] || continue
    if su "$U" -c "sudo -k -n $cmdline </dev/null" >/dev/null 2>&1; then got=permit; else got=refuse; fi
    if [ "$got" = "$expect" ]; then ok "$label -> $got"; else bad "$label -> $got, expected $expect"; fi
  done <<< "$matrix"
  # Control: with the cache dropped, a command the policy does NOT name must refuse even
  # though this same user just ran sudo successfully. If this reads permit, the whole
  # matrix above is measuring the credential cache rather than the policy.
  if su "$U" -c "sudo -k -n /usr/bin/true </dev/null" >/dev/null 2>&1; then
    bad "CONTROL: an unlisted command was permitted — matrix results are invalid"
  else
    ok "CONTROL: unlisted command refused, so refusals above reflect policy not cache"
  fi
else
  unk "candidate policy failed visudo -c; NOT installed"
  visudo -c -f /tmp/pfauthz.sudoers 2>&1 | sed 's/^/        /'
fi
rm -f /tmp/pfauthz.sudoers

# ---------------------------------------------------------------------------
say "Q5: unattended teardown — does the grant work with no tty?"
# This is the property the whole authorization exists for: `yoloai stop` and any reaper
# run without a terminal. `sudo -n` must succeed there, not merely when a human is present.
if [ -e "$SUDOERS" ]; then
  if su "$U" -c "sudo -k -n /sbin/pfctl -a com.apple/yoloai_authz -t yoloai_authz_src -T delete 192.0.2.77 </dev/null" >/dev/null 2>&1; then
    ok "sudo -n succeeds with stdin closed and no controlling tty"
  else
    bad "sudo -n failed without a tty — unattended teardown would not work"
  fi
else
  unk "policy not installed; teardown property untested"
fi

# ---------------------------------------------------------------------------
say "Q6: what sudo does to the environment on macOS (the wrapper model)"
# fileutil.SudoParentEnv recovers sudo-stripped vars by reading /proc/<ppid>/environ,
# which does not exist on darwin. If sudo strips the agent's credentials here, then
# `sudo yoloai new` launches an agent that cannot authenticate.
echo "   HOME under sudo:        ${HOME:-<unset>}"
echo "   SUDO_USER/UID/GID:      ${SUDO_USER:-?}/${SUDO_UID:-?}/${SUDO_GID:-?}"
for v in ANTHROPIC_API_KEY CLAUDE_CODE_OAUTH_TOKEN; do
  if [ -n "${!v:-}" ]; then ok "$v survived sudo"; else bad "$v STRIPPED by sudo (no /proc to recover it on macOS)"; fi
done
echo "   env_reset / env_keep in effect:"
sudo -V 2>/dev/null | grep -iE 'environment variables to (preserve|remove)' -A 12 | sed 's/^/        /' | head -24

# ---------------------------------------------------------------------------
printf '\n== TOTALS ==\n   pass=%d fail=%d unknown=%d\n' "$PASS" "$FAIL" "$UNKNOWN"

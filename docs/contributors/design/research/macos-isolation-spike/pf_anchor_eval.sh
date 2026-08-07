#!/bin/bash
# ABOUTME: A loaded anchor that pf never evaluates, because the MAIN ruleset's reference to it is
# ABOUTME: gone — every yoloAI-visible check passes and nothing is enforced. Diagnoses and repairs.
#
# Run: sudo bash pf_anchor_eval.sh
#
# WHY THIS EXISTS — found by accident, which is the point
#   pf_pool_occupancy.sh put 8 sandboxes in 8 slots with 8 distinct allowlists and measured 56 of
#   56 cross-sandbox leaks: nothing was blocked at all, not even the destinations no slot allowed.
#   That reads as a catastrophic failure of the slot design. It is not. It is the host.
#
#   `/etc/pf.conf` contains `anchor "com.apple/*"`. That line is what makes pf DESCEND into the
#   com.apple anchor and evaluate its children — including ours. Earlier in the same session a
#   `pfctl -F all` destroyed the main ruleset, and restarting the apple daemon restored only
#   VMNET'S OWN rules. The anchor reference never came back. The main ruleset went 4 rules -> 0 ->
#   2, and the 2 that returned are not the one that matters.
#
#   So the anchor holds its full complement of correct rules, the addresses are in the right
#   tables, pf is enabled — and pf never looks at any of it.
#
# WHY THIS IS THE SHARPEST VERSION OF D6
#   D6 measured membership-without-rules. This is rules-and-membership-without-EVALUATION, and it
#   is worse in the way that matters: every check D132 specifies for the start path passes.
#     1. pf is enabled            -> `pfctl -s info` says Enabled. TRUE.
#     2. the pool ruleset loaded  -> `-a com.apple/yoloai -s rules` returns the full count. TRUE.
#     3. our address is in its slot -> `-T show` lists it. TRUE.
#   Three green checks, zero enforcement. The state that decides whether any of it runs lives in
#   the MAIN ruleset, which is the one thing the grant deliberately cannot read: it permits
#   `-s info` and `-a <anchor> -s rules`, and nothing that would reveal whether the anchor is
#   reachable from the main ruleset at all.
#
#   E1 DIAGNOSE. Does the main ruleset currently reference com.apple? Print it.
#   E2 DEMONSTRATE. Arm a sandbox properly and show it is NOT enforced, while all three of D132's
#      checks report healthy — the three-green-checks claim, measured rather than argued.
#   E3 REPAIR. Reload /etc/pf.conf to restore the anchor reference, restart the apple daemon to
#      restore vmnet NAT, and show enforcement working again. Establishes that the damage is
#      repairable and exactly what the repair is.
#
# SAFETY: this WRITES THE MAIN RULESET (`pfctl -f /etc/pf.conf`) as its repair step. That is
# normally forbidden — see macos-pf-privileged-path.md hazard 1 — and is done here because the
# main ruleset is already damaged and this is the documented way back.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/../../../../.." && pwd)"
YOLOAI="$REPO/yoloai"
ANCHOR="com.apple/yoloai_b"
SLOTS=4; SLOT=1
SB=ev-a
ALLOW=1.1.1.1; DENY=1.0.0.1
WD=$(mktemp -d /tmp/pfev-wd.XXXXXX)
PASS=0; FAIL=0; UNKNOWN=0
SB_IP=""

RESULTS="$HERE/results/pf-anchor-eval.txt"
mkdir -p "$(dirname "$RESULTS")"
exec > >(tee "$RESULTS") 2>&1

say() { printf '\n== %s ==\n' "$*"; }
ok()  { PASS=$((PASS+1));       printf '   PASS    %s\n' "$*"; }
bad() { FAIL=$((FAIL+1));       printf '   FAIL    %s\n' "$*"; }
unk() { UNKNOWN=$((UNKNOWN+1)); printf '   UNKNOWN %s\n' "$*"; }
asuser() { sudo -u "$U" -H "$@"; }
quiet_pf() { grep -viE 'use of -f option|main ruleset added|/etc/pf.conf for further|ALTQ|^$'; }
nrules()   { pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -c . || true; }
tshow()    { pfctl -a "$ANCHOR" -t "$1" -T show 2>/dev/null | tr -d ' ' | tr '\n' ',' ; }
mainrules(){ pfctl -s rules 2>/dev/null | grep -c . || true; }
# The predicate must require the SLASH. Run 1 grepped for "com.apple" and matched
# `anchor "com.apple.internet-sharing" all` — a different, top-level anchor that a system service
# re-inserts on its own — so it reported the ruleset healthy while the com.apple/* reference was
# missing, and skipped the repair. Two anchors whose names share a 9-character prefix, and only
# one of them is ours: `com.apple/` is the discriminator, and `com.apple` is not.
refs()     { pfctl -s rules 2>/dev/null | grep -c 'com\.apple/' || true; }
ipof() { asuser container inspect "yoloai-cli-$1" 2>/dev/null \
  | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d[0]["status"]["networks"][0].get("ipv4Address","").split("/")[0])' 2>/dev/null; }
egress() {
  local c
  c=$(asuser container exec "yoloai-cli-$SB" curl -s -o /dev/null -w '%{http_code}' --max-time 6 "http://$1/" 2>/dev/null)
  c=${c//[^0-9]/}; c=${c: -3}; printf '%s' "${c:-000}"
}

cleanup() {
  echo
  echo "== cleanup =="
  pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
  asuser "$YOLOAI" destroy "$SB" --abandon-unapplied >/dev/null 2>&1 \
    && echo "   destroyed $SB" || echo "   NOTE $SB not destroyed — remove by hand"
  rm -rf "$WD" /tmp/pfev.*
  echo "   main ruleset: $(mainrules) rules, $(refs) referencing com.apple"
  echo "   pf: $(pfctl -s info 2>/dev/null | head -1 | awk '{print $2}')"
  echo
  echo "pass=$PASS fail=$FAIL unknown=$UNKNOWN"
}
trap cleanup EXIT

arm() {
  pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
  { for ((i=0;i<SLOTS;i++)); do
      echo "table <yb_src_$i> persist"; echo "table <yb_dst_$i> persist"
      echo "pass  in quick from <yb_src_$i> to <yb_dst_$i>"
      echo "block drop in quick from <yb_src_$i> to any"
    done; } > /tmp/pfev.rules
  pfctl -a "$ANCHOR" -f /tmp/pfev.rules >/dev/null 2>&1
  pfctl -a "$ANCHOR" -t "yb_src_$SLOT" -T add "$SB_IP"  >/dev/null 2>&1
  pfctl -a "$ANCHOR" -t "yb_dst_$SLOT" -T add "$ALLOW"  >/dev/null 2>&1
}

# The three checks D132 specifies for the start path, reported exactly as yoloAI would see them.
d132_checks() {
  local enabled loaded member
  enabled=$(pfctl -s info 2>/dev/null | head -1 | awk '{print $2}')
  loaded=$(nrules)
  member=$(tshow "yb_src_$SLOT")
  printf '        D132 check 1  pf enabled ............ %s\n' "$enabled"
  printf '        D132 check 2  pool loaded ........... %s rules (want %s)\n' "$loaded" "$((SLOTS*2))"
  printf '        D132 check 3  our address in slot ... %s\n' "${member:-<empty>}"
  if [ "$enabled" = Enabled ] && [ "${loaded:-0}" -eq $((SLOTS*2)) ] && [ -n "$member" ]; then
    printf '        ==> all three checks PASS\n'; return 0
  fi
  printf '        ==> at least one check fails\n'; return 1
}

echo "host: macOS $(sw_vers -productVersion) $(uname -m)"
echo "date: $(date '+%Y-%m-%d %H:%M:%S %Z') | anchor=$ANCHOR"

# ---------------------------------------------------------------------------
say "E1 DIAGNOSE — does the main ruleset still reference com.apple?"
echo "        the whole main ruleset, verbatim:"
pfctl -s rules 2>/dev/null | sed 's/^/        | /'
echo "        $(mainrules) rule(s) total, $(refs) referencing the com.apple/* anchor"
echo "        (lines naming com.apple.internet-sharing are a DIFFERENT anchor and do not count —"
echo "         that near-miss is what made run 1 of this script report a healthy host)"
echo
echo "        what /etc/pf.conf says should be there:"
grep -nE 'anchor|load' /etc/pf.conf | sed 's/^/        | /'
DAMAGED=0
if [ "$(refs)" -eq 0 ]; then
  DAMAGED=1
  bad "the main ruleset contains NO reference to com.apple/*. Nothing descends into the anchor, so"
  echo "           every rule inside it is inert — loaded, correct, and never evaluated."
else
  ok "the main ruleset still references com.apple/* ($(refs) line(s))"
fi

# ---------------------------------------------------------------------------
say "E2 DEMONSTRATE — arm a sandbox and check enforcement against D132's own checks"
git -C "$WD" init -q 2>/dev/null; : > "$WD/README.md"
git -C "$WD" add -A >/dev/null 2>&1
git -C "$WD" -c user.email=spike@local -c user.name=spike commit -qm init >/dev/null 2>&1
chown -R "$U" "$WD"
asuser container system start >/dev/null 2>&1
asuser "$YOLOAI" destroy "$SB" --abandon-unapplied >/dev/null 2>&1
asuser "$YOLOAI" new "$SB" "$WD" --backend apple >/dev/null 2>&1 || { bad "could not create $SB"; exit 1; }
SB_IP=$(ipof "$SB")
[ -n "$SB_IP" ] || { bad "no address for $SB; ABORTING"; exit 1; }
echo "        $SB at $SB_IP"
ba=$(egress "$ALLOW"); bd=$(egress "$DENY")
echo "        before arming: allow=$ba deny=$bd (both must be reachable)"
{ [ "$ba" != 000 ] && [ "$bd" != 000 ]; } || { bad "no open egress to start from; ABORTING"; exit 1; }
arm
echo "        armed slot $SLOT with src=$SB_IP dst=$ALLOW"
d132_checks; checks_pass=$?
a1=$(egress "$ALLOW"); d1=$(egress "$DENY")
echo "        ACTUAL egress: allow=$a1 deny=$d1"
if [ "$checks_pass" -eq 0 ] && [ "$d1" != 000 ]; then
  bad "ALL THREE D132 CHECKS PASS AND THE SANDBOX IS UNFILTERED. A denied destination answered"
  echo "           $d1. This is the fail-open D6 describes, reached from a third direction, and it is"
  echo "           invisible to every read the grant permits — the deciding state is in the MAIN"
  echo "           ruleset, which the grant cannot see. Verification needs a fourth check, and the"
  echo "           grant does not currently authorize the read that would answer it."
elif [ "$checks_pass" -eq 0 ] && [ "$d1" = 000 ] && [ "$a1" != 000 ]; then
  ok "checks pass and enforcement is real — this host is healthy, no anchor-evaluation problem"
elif [ "$a1" = 000 ]; then
  unk "the permitted destination is unreachable; the guest has no network and nothing is"
  echo "           attributable (DF172)"
else
  unk "checks did not all pass (see above), so this is not the three-green-checks case"
fi

# ---------------------------------------------------------------------------
say "E3 REPAIR — restore the anchor reference, then vmnet's NAT, and re-verify"
if [ "$DAMAGED" -eq 0 ]; then
  unk "main ruleset was not damaged, so there is nothing to repair — E3 NOT TRIED"
else
  echo "        step 1: pfctl -f /etc/pf.conf   (restores the anchor reference; drops vmnet NAT)"
  pfctl -f /etc/pf.conf 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
  echo "        main ruleset now: $(mainrules) rules, $(refs) referencing com.apple"
  echo "        step 2: restart the apple daemon (restores vmnet NAT; moves guest addresses)"
  asuser container system stop  >/dev/null 2>&1; sleep 4
  asuser container system start >/dev/null 2>&1; sleep 6
  asuser "$YOLOAI" start "$SB"  >/dev/null 2>&1; sleep 6
  SB_IP=$(ipof "$SB")
  echo "        main ruleset now: $(mainrules) rules, $(refs) referencing com.apple"
  echo "        $SB now at ${SB_IP:-<none>}"
  if [ -z "$SB_IP" ]; then
    bad "sandbox has no address after repair; cannot re-verify"
  else
    arm
    d132_checks >/dev/null
    a2=$(egress "$ALLOW"); d2=$(egress "$DENY")
    echo "        ACTUAL egress after repair: allow=$a2 deny=$d2"
    if [ "$a2" != 000 ] && [ "$d2" = 000 ]; then
      ok "REPAIRED — enforcement is real again. The repair is two steps and the ORDER matters:"
      echo "           reload the main ruleset first, then restart the backend. Reloading pf.conf"
      echo "           alone leaves the guests without NAT; restarting the backend alone leaves the"
      echo "           anchor unreferenced, which is the state this run began in."
    elif [ "$a2" = 000 ]; then
      bad "no egress at all after repair — vmnet NAT did not come back"
    else
      bad "still unfiltered after repair (allow=$a2 deny=$d2)"
    fi
  fi
fi

#!/bin/bash
# ABOUTME: `pfctl -F all` as a named fail-OPEN trigger, shown end to end in one run, plus what
# ABOUTME: pf's -E/-X reference counting does and does not protect against.
#
# Run: sudo bash pf_flush_and_reference.sh
#
# WHY THIS EXISTS — and a correction it exists to make
#   pf_midlife_wipe.sh recorded `pfctl -F all` as leaving our rules and membership intact, with the
#   guest reaching nothing, and that was written up as macOS failing CLOSED — the opposite of the
#   Linux `flush ruleset` hazard. That conclusion was wrong, and the run's own caveat is what
#   caught it: it established STATE survival, never ENFORCEMENT continuity, because the same flush
#   destroyed vmnet's NAT and a guest with no network cannot demonstrate either direction.
#
#   The flush removes the main ruleset's `anchor "com.apple/*"` line along with everything else.
#   Our anchor keeps every rule; nothing descends into it any more. While NAT is also dead that
#   looks like fail-closed. Restore NAT — which a daemon restart does, and which any user whose VMs
#   stopped working will do — and the sandbox is UNFILTERED with all three of D132's checks green.
#
#   R0 THE FULL CHAIN, in one artifact: enforcing -> `pfctl -F all` -> NAT restored -> unfiltered.
#      pf_anchor_eval.sh found this state and repaired it; this CAUSES it from a named trigger, so
#      the causal claim rests on one run rather than on three stitched together.
#   R1 REFERENCES BASELINE.
#   R2 DOES OUR OWN `-E` REFERENCE PROTECT US? macos-pf-privileged-path.md lists holding one as an
#      open question. `pfctl -d` was already measured to destroy an existing token; the question
#      left is whether holding one of our own changes what another process can do to us.
#   R3 `-X` AND THE COUNT: does releasing the last reference disable pf and de-isolate everything?
#   R4 REPAIR, and verify the host is genuinely back before this exits.
#
# SAFETY: this deliberately damages the main ruleset and pf's enable state, then repairs both and
# asserts enforcement is real again before exiting. It writes the main ruleset (hazard 1) as its
# repair, which is the documented way back. Everything of ours stays in com.apple/yoloai_b.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/../../../../.." && pwd)"
YOLOAI="$REPO/yoloai"
ANCHOR="com.apple/yoloai_b"
SLOTS=4; SLOT=1
SB=fr-a
ALLOW=1.1.1.1; DENY=1.0.0.1
WD=$(mktemp -d /tmp/pffr-wd.XXXXXX)
PASS=0; FAIL=0; UNKNOWN=0
SB_IP=""; TOKEN=""

RESULTS="$HERE/results/pf-flush-reference.txt"
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
mainrefs() { pfctl -s rules 2>/dev/null | grep -c 'com\.apple/' || true; }
pfstate()  { pfctl -s info 2>/dev/null | head -1 | awk '{print $2}'; }
showrefs() { pfctl -s References 2>/dev/null | tr '\n' ' ' | sed 's/  */ /g'; }
ipof() { asuser container inspect "yoloai-cli-$1" 2>/dev/null \
  | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d[0]["status"]["networks"][0].get("ipv4Address","").split("/")[0])' 2>/dev/null; }
egress() {
  local c
  c=$(asuser container exec "yoloai-cli-$SB" curl -s -o /dev/null -w '%{http_code}' --max-time 6 "http://$1/" 2>/dev/null)
  c=${c//[^0-9]/}; c=${c: -3}; printf '%s' "${c:-000}"
}
arm() {
  pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
  { for ((i=0;i<SLOTS;i++)); do
      echo "table <yb_src_$i> persist"; echo "table <yb_dst_$i> persist"
      echo "pass  in quick from <yb_src_$i> to <yb_dst_$i>"
      echo "block drop in quick from <yb_src_$i> to any"
    done; } > /tmp/pffr.rules
  pfctl -a "$ANCHOR" -f /tmp/pffr.rules >/dev/null 2>&1
  pfctl -a "$ANCHOR" -t "yb_src_$SLOT" -T add "$SB_IP" >/dev/null 2>&1
  pfctl -a "$ANCHOR" -t "yb_dst_$SLOT" -T add "$ALLOW" >/dev/null 2>&1
}
report() {
  local a d
  a=$(egress "$ALLOW"); d=$(egress "$DENY")
  printf '        pf=%-9s anchor=%-2s rules  slot=%-18s main-refs=%s  || allow=%s deny=%s\n' \
    "$(pfstate)" "$(nrules)" "$(tshow "yb_src_$SLOT")" "$(mainrefs)" "$a" "$d"
  LAST_A=$a; LAST_D=$d
}
LAST_A=""; LAST_D=""

# Bring the host all the way back: main ruleset reference, then vmnet NAT, then our slot.
repair() {
  pfctl -e >/dev/null 2>&1
  pfctl -f /etc/pf.conf 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
  asuser container system stop  >/dev/null 2>&1; sleep 4
  asuser container system start >/dev/null 2>&1; sleep 6
  asuser "$YOLOAI" start "$SB"  >/dev/null 2>&1; sleep 6
  SB_IP=$(ipof "$SB")
  [ -n "$SB_IP" ] && arm
}

cleanup() {
  echo
  echo "== cleanup =="
  [ -n "$TOKEN" ] && { pfctl -X "$TOKEN" >/dev/null 2>&1; echo "   released our -E token"; }
  pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
  asuser "$YOLOAI" destroy "$SB" --abandon-unapplied >/dev/null 2>&1 \
    && echo "   destroyed $SB" || echo "   NOTE $SB not destroyed — remove by hand"
  rm -rf "$WD" /tmp/pffr.*
  echo "   pf=$(pfstate)  main ruleset refs to com.apple/*=$(mainrefs)  references: $(showrefs)"
  echo
  echo "   IF main-refs IS 0 THE HOST IS STILL FAIL-OPEN: run pf_anchor_eval.sh to repair."
  echo
  echo "pass=$PASS fail=$FAIL unknown=$UNKNOWN"
}
trap cleanup EXIT

echo "host: macOS $(sw_vers -productVersion) $(uname -m)"
echo "date: $(date '+%Y-%m-%d %H:%M:%S %Z') | anchor=$ANCHOR"

# ---------------------------------------------------------------------------
say "SETUP — one enforcing sandbox, verified against a control"
git -C "$WD" init -q 2>/dev/null; : > "$WD/README.md"
git -C "$WD" add -A >/dev/null 2>&1
git -C "$WD" -c user.email=spike@local -c user.name=spike commit -qm init >/dev/null 2>&1
chown -R "$U" "$WD"
asuser container system start >/dev/null 2>&1
asuser "$YOLOAI" destroy "$SB" --abandon-unapplied >/dev/null 2>&1
asuser "$YOLOAI" new "$SB" "$WD" --backend apple >/dev/null 2>&1 || { bad "could not create $SB"; exit 1; }
SB_IP=$(ipof "$SB"); [ -n "$SB_IP" ] || { bad "no address; ABORTING"; exit 1; }
echo "        $SB at $SB_IP | main ruleset refs to com.apple/*: $(mainrefs)"
[ "$(mainrefs)" -gt 0 ] || { bad "host is ALREADY fail-open before this run starts (main-refs=0)."
  echo "           Run pf_anchor_eval.sh first. ABORTING."; exit 1; }
ba=$(egress "$ALLOW"); bd=$(egress "$DENY")
echo "        before arming: allow=$ba deny=$bd (both must be reachable)"
{ [ "$ba" != 000 ] && [ "$bd" != 000 ]; } || { bad "no open egress; ABORTING"; exit 1; }
arm
report
{ [ "$LAST_A" != 000 ] && [ "$LAST_D" = 000 ]; } || { bad "not enforcing after arm; ABORTING"; exit 1; }
ok "enforcing: permitted reachable, denied blocked, main ruleset references the anchor"

# ---------------------------------------------------------------------------
say "R0 THE FULL CHAIN — pfctl -F all, then restore NAT, then look again"
echo "        Step 1: another tool runs pfctl -F all."
pfctl -F all 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
report
echo "        Note main-refs above: the flush took the main ruleset's anchor reference with it."
echo "        With NAT also gone the guest reaches nothing, which is what the earlier run saw and"
echo "        mistook for fail-closed. The next step is the one that reveals the real state."
echo
echo "        Step 2: restore ONLY vmnet's NAT (apple daemon restart) — no ruleset repair."
asuser container system stop  >/dev/null 2>&1; sleep 4
asuser container system start >/dev/null 2>&1; sleep 6
asuser "$YOLOAI" start "$SB"  >/dev/null 2>&1; sleep 6
SB_IP=$(ipof "$SB")
echo "        $SB now at ${SB_IP:-<none>} (a restart moves it, so the slot is re-armed to match)"
arm
report
if [ "$LAST_A" = 000 ]; then
  unk "R0: NAT did not come back, so the real state is still masked"
elif [ "$LAST_D" != 000 ] && [ "$(mainrefs)" -eq 0 ]; then
  bad "R0: FAIL-OPEN CONFIRMED. Denied destination answered $LAST_D with the anchor holding"
  echo "           $(nrules) correct rules and the address in its slot. pfctl -F all is a fail-open"
  echo "           trigger on macOS, not a fail-closed one — the earlier reading was NAT death"
  echo '           masking it. Same direction as Linux "systemctl restart nftables", and likewise'
  echo "           an ordinary action by a tool with no relationship to yoloAI."
elif [ "$LAST_D" = 000 ]; then
  ok "R0: still enforcing after the flush and NAT restore (main-refs=$(mainrefs))"
else
  unk "R0: inconclusive (allow=$LAST_A deny=$LAST_D main-refs=$(mainrefs))"
fi

say "R0b REPAIR before the reference tests, so they start from a sound host"
repair
report
if [ "$LAST_A" != 000 ] && [ "$LAST_D" = 000 ]; then
  ok "host repaired and enforcing again (main-refs=$(mainrefs))"
else
  bad "repair did not restore enforcement; the reference tests below run on a broken host"
fi

# ---------------------------------------------------------------------------
say "R1 REFERENCES BASELINE"
echo "        $(showrefs)"

# ---------------------------------------------------------------------------
say "R2 DOES HOLDING OUR OWN -E REFERENCE PROTECT AGAINST ANOTHER TOOL'S -d?"
out=$(pfctl -E 2>&1); echo "$out" | quiet_pf | sed 's/^/        pfctl: /'
TOKEN=$(printf '%s' "$out" | grep -oE 'Token : [0-9]+' | awk '{print $3}')
echo "        our token: ${TOKEN:-<none>}   references now: $(showrefs)"
echo "        now another tool disables pf:"
pfctl -d 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
echo "        pf state after that -d: $(pfstate)   references: $(showrefs)"
if [ "$(pfstate)" = Enabled ]; then
  ok "R2: our -E reference kept pf enabled through another process's -d"
else
  bad "R2: pf is $(pfstate) despite our -E reference — holding one does NOT protect against -d."
  echo "           So yoloAI cannot defend its own enforcement by taking a reference; the only"
  echo "           defence is detecting the state, which needs the fourth verification check."
fi

# ---------------------------------------------------------------------------
say "R3 -X AND THE COUNT — does releasing the last reference disable pf?"
pfctl -e >/dev/null 2>&1
echo "        pf re-enabled: $(pfstate) | references: $(showrefs)"
if [ -n "$TOKEN" ]; then
  pfctl -X "$TOKEN" 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
  TOKEN=""
  echo "        after releasing our token: pf=$(pfstate) | references: $(showrefs)"
  if [ "$(pfstate)" = Enabled ]; then
    ok "R3: releasing our reference did not disable pf on this host"
  else
    bad "R3: pf went $(pfstate) when our reference was released — an -X by yoloAI can"
    echo "           de-isolate every VM on the host, which is why this was left untested until now."
  fi
else
  unk "R3: no token was issued, so nothing to release — NOT TRIED"
fi

# ---------------------------------------------------------------------------
say "R4 FINAL REPAIR AND VERIFY — the host must leave this run enforcing"
pfctl -e >/dev/null 2>&1
[ "$(mainrefs)" -eq 0 ] && repair
report
if [ "$LAST_A" != 000 ] && [ "$LAST_D" = 000 ]; then
  ok "host is sound: pf enabled, anchor referenced, enforcement real"
else
  bad "host did NOT end this run enforcing (allow=$LAST_A deny=$LAST_D main-refs=$(mainrefs))"
  echo "           Run pf_anchor_eval.sh to repair before trusting any later measurement."
fi

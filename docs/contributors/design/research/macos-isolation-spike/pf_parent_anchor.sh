#!/bin/bash
# ABOUTME: The mid-life candidate family pf_midlife_wipe.sh excluded — rewrites of the PARENT
# ABOUTME: com.apple anchor — plus the enforcement-continuity check that run could not render.
#
# Run: sudo bash pf_parent_anchor.sh
#
# WHY THIS EXISTS
#   pf_midlife_wipe.sh ran seven candidates and none wiped the anchor, but it named its own gap:
#   every trigger it tried acted on the MAIN ruleset or on unrelated services. Our anchor nests at
#   com.apple/yoloai, and the census showed two Apple components living in that same parent —
#   200.AirDrop and 250.ApplicationFirewall. Whatever rewrites `com.apple` is the candidate family
#   most likely to behave like Linux's `flush ruleset`, and it was exactly the family not tested.
#
#   P1 A DIRECT PARENT RELOAD. `pfctl -a com.apple -f /etc/pf.anchors/com.apple` — the operation an
#      Apple component performs when it rewrites the anchor it owns. Do nested sub-anchors survive?
#   P2 A SIBLING SUB-ANCHOR WRITE. Does loading rules into one sub-anchor disturb another? This is
#      the mechanism question underneath P1, asked without depending on any Apple component's
#      behaviour, so its answer holds even if P1's trigger is unrepresentative.
#   P3 THE macOS APPLICATION FIREWALL, toggled. A real user action, performed by the component that
#      owns 250.ApplicationFirewall, i.e. a live instance of the P1 hazard if there is one.
#
#   B  ENFORCEMENT CONTINUITY — the verdict pf_midlife_wipe.sh had to withhold. It established that
#      rules and membership SURVIVE `pfctl -F all`; it could not establish that filtering still
#      WORKED, because the same flush destroyed vmnet's NAT and a guest with no network makes
#      neither a block nor a pass attributable (DF172). Its repair also re-armed the slot, which
#      would have tested fresh state rather than surviving state.
#
#      The trick this run uses: restore NAT WITHOUT touching the enforcing sandbox. Restarting the
#      guest is not allowed — a restart moves its address (measured, restart-control.txt), so the
#      surviving membership would name an address nobody holds and the test would fail for a
#      reason that has nothing to do with survival. Instead a SECOND, disposable sandbox is
#      restarted to make vmnet reinstall the bridge's NAT, while the enforcing sandbox keeps
#      running and keeps its address. Then its egress is read with its slot never re-armed.
#
# SAFETY: our writes go only to com.apple/yoloai_b. P1 reloads the com.apple parent from Apple's
# own file, which is the measurement; it can drop vmnet NAT, recovered as in the previous run. The
# main ruleset's count is printed throughout.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/../../../../.." && pwd)"
YOLOAI="$REPO/yoloai"
[ -x "$YOLOAI" ] || { echo "no yoloai binary at $YOLOAI"; exit 2; }

ANCHOR="com.apple/yoloai_b"
SIB="com.apple/yoloai_sib"
PARENT="com.apple"
PARENT_FILE=/etc/pf.anchors/com.apple
SLOTS=4; SLOT=1
SB=pa-a          # the enforcing sandbox; never restarted
SB2=pa-b         # disposable, restarted to make vmnet reinstall NAT
ALLOW=1.1.1.1; DENY=1.0.0.1
WD=$(mktemp -d /tmp/pfpa-wd.XXXXXX)
PASS=0; FAIL=0; UNKNOWN=0
SB_IP=""

RESULTS="$HERE/results/pf-parent-anchor.txt"
mkdir -p "$(dirname "$RESULTS")"
exec > >(tee "$RESULTS") 2>&1

say() { printf '\n== %s ==\n' "$*"; }
ok()  { PASS=$((PASS+1));       printf '   PASS    %s\n' "$*"; }
bad() { FAIL=$((FAIL+1));       printf '   FAIL    %s\n' "$*"; }
unk() { UNKNOWN=$((UNKNOWN+1)); printf '   UNKNOWN %s\n' "$*"; }
asuser() { sudo -u "$U" -H "$@"; }
quiet_pf() { grep -viE 'use of -f option|main ruleset added|/etc/pf.conf for further|ALTQ|^$'; }
nrules()   { pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -c . || true; }
sibrules() { pfctl -a "$SIB" -s rules 2>/dev/null | grep -c . || true; }
tshow()    { pfctl -a "$ANCHOR" -t "$1" -T show 2>/dev/null | tr -d ' ' | tr '\n' ',' ; }
mainrules(){ pfctl -s rules 2>/dev/null | grep -c . || true; }
anchors()  { pfctl -a 'com.apple/*' -s Anchors 2>/dev/null | tr -d ' ' | tr '\n' ' '; }
ipof() { asuser container inspect "yoloai-cli-$1" 2>/dev/null \
  | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d[0]["status"]["networks"][0].get("ipv4Address","").split("/")[0])' 2>/dev/null; }
egress() {
  local c
  c=$(asuser container exec "yoloai-cli-$1" curl -s -o /dev/null -w '%{http_code}' --max-time 6 "http://$2/" 2>/dev/null)
  c=${c//[^0-9]/}; c=${c: -3}; printf '%s' "${c:-000}"
}

MAIN_BEFORE=$(mainrules)
ALF=/usr/libexec/ApplicationFirewall/socketfilterfw
ALF_ORIG=""
cleanup() {
  echo
  echo "== cleanup =="
  [ -n "$ALF_ORIG" ] && { "$ALF" --setglobalstate "$ALF_ORIG" >/dev/null 2>&1; echo "   ALF restored to $ALF_ORIG"; }
  pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
  pfctl -a "$SIB" -F all >/dev/null 2>&1
  for s in "$SB" "$SB2"; do
    asuser "$YOLOAI" destroy "$s" --abandon-unapplied >/dev/null 2>&1 \
      && echo "   destroyed sandbox $s" || echo "   NOTE sandbox $s not destroyed — remove by hand"
  done
  rm -rf "$WD" /tmp/pfpa.*
  echo "   anchor rules=$(nrules)  sibling rules=$(sibrules)"
  echo "   main ruleset: before=$MAIN_BEFORE after=$(mainrules)"
  echo "   NOTE flushed anchors remain enumerable until reboot; pfctl has no verb that removes one"
  echo
  echo "pass=$PASS fail=$FAIL unknown=$UNKNOWN"
}
trap cleanup EXIT

gen_rules() {
  for ((i=0;i<SLOTS;i++)); do
    echo "table <yb_src_$i> persist"
    echo "table <yb_dst_$i> persist"
    echo "pass  in quick from <yb_src_$i> to <yb_dst_$i>"
    echo "block drop in quick from <yb_src_$i> to any"
  done
}
arm() {
  pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
  gen_rules > /tmp/pfpa.rules
  pfctl -a "$ANCHOR" -f /tmp/pfpa.rules >/dev/null 2>&1
  pfctl -a "$ANCHOR" -t "yb_src_$SLOT" -T add "$SB_IP" >/dev/null 2>&1
  pfctl -a "$ANCHOR" -t "yb_dst_$SLOT" -T add "$ALLOW" >/dev/null 2>&1
}
state_rules=""; state_src=""; state_allow=""; state_deny=""
capture() {
  state_rules=$(nrules); state_src=$(tshow "yb_src_$SLOT")
  state_allow=$(egress "$SB" "$ALLOW"); state_deny=$(egress "$SB" "$DENY")
  printf '        rules=%-3s src_%s=%-18s allow=%s deny=%s  | sub-anchors: %s\n' \
    "$state_rules" "$SLOT" "${state_src:-<empty>}" "$state_allow" "$state_deny" "$(anchors)"
}
verdict() {
  local label="$1"
  if [ "$state_allow" = 000 ]; then
    unk "$label: permitted destination also unreachable — the guest lost its network, so no"
    echo "           enforcement verdict is attributable (DF172). ANCHOR STATE: rules=$state_rules"
    echo "           src=${state_src:-<empty>} — read directly, and that part is still evidence."
  elif [ "${state_rules:-0}" -eq 0 ]; then
    bad "$label: ANCHOR WIPED — this is the Linux-shaped trigger, found on macOS"
  elif [ -z "$state_src" ]; then
    bad "$label: MEMBERSHIP WIPED, rules intact — sandbox matches no slot and falls through"
  elif [ "$state_deny" != 000 ]; then
    bad "$label: rules and membership present but a DENIED destination is reachable — unenforced"
  else
    ok "$label: SURVIVED — rules, membership and live enforcement all intact"
  fi
}

echo "host: macOS $(sw_vers -productVersion) $(uname -m) | $(sysctl -n hw.model)"
echo "date: $(date '+%Y-%m-%d %H:%M:%S %Z') | anchor=$ANCHOR parent=$PARENT"
echo "main ruleset rules before: $MAIN_BEFORE"

# ---------------------------------------------------------------------------
say "SETUP — one enforcing sandbox (never restarted) and one disposable"
git -C "$WD" init -q 2>/dev/null; : > "$WD/README.md"
git -C "$WD" add -A >/dev/null 2>&1
git -C "$WD" -c user.email=spike@local -c user.name=spike commit -qm init >/dev/null 2>&1
chown -R "$U" "$WD"
asuser container system start >/dev/null 2>&1
for s in "$SB" "$SB2"; do
  asuser "$YOLOAI" destroy "$s" --abandon-unapplied >/dev/null 2>&1
  asuser "$YOLOAI" new "$s" "$WD" --backend apple >/dev/null 2>&1 \
    || { bad "could not create $s; ABORTING"; exit 1; }
done
SB_IP=$(ipof "$SB"); SB2_IP=$(ipof "$SB2")
[ -n "$SB_IP" ] || { bad "no address for $SB; ABORTING"; exit 1; }
echo "        $SB at $SB_IP (enforcing, never restarted) | $SB2 at ${SB2_IP:-?} (disposable)"
ba=$(egress "$SB" "$ALLOW"); bd=$(egress "$SB" "$DENY")
echo "        BEFORE any rules: allow=$ba deny=$bd (both must be reachable)"
{ [ "$ba" != 000 ] && [ "$bd" != 000 ]; } || { bad "no open egress to start from; ABORTING"; exit 1; }
ok "baseline: both reachable, so a later block is attributable to pf"
arm; capture
{ [ "$state_allow" != 000 ] && [ "$state_deny" = 000 ]; } || { bad "not enforcing after arm; ABORTING"; exit 1; }
ok "armed and enforcing on slot $SLOT"
echo "        parent anchor file: $PARENT_FILE ($(grep -c . "$PARENT_FILE" 2>/dev/null) lines)"

# ---------------------------------------------------------------------------
say "P1 DIRECT PARENT RELOAD — pfctl -a $PARENT -f $PARENT_FILE"
echo "        Our anchor is a CHILD of $PARENT. If reloading a parent purges children, this is"
echo '        the macOS equivalent of Linux "flush ruleset", and the mid-life detector becomes'
echo "        urgent on both platforms rather than one."
[ -r "$PARENT_FILE" ] || { bad "no readable $PARENT_FILE — P1 cannot run"; }
pfctl -a "$PARENT" -f "$PARENT_FILE" 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
sleep 3
capture
verdict "P1 parent anchor reload"

# repair if the parent reload took NAT with it, so P2/P3 are not measured through an outage
if [ "$state_allow" = 000 ]; then
  echo "        restoring NAT via apple daemon restart before continuing"
  asuser container system stop >/dev/null 2>&1; sleep 3
  asuser container system start >/dev/null 2>&1; sleep 5
  asuser "$YOLOAI" start "$SB" >/dev/null 2>&1
  asuser "$YOLOAI" start "$SB2" >/dev/null 2>&1; sleep 5
  SB_IP=$(ipof "$SB"); echo "        $SB now at ${SB_IP:-<none>}"
  arm; capture
fi

# ---------------------------------------------------------------------------
say "P2 SIBLING SUB-ANCHOR WRITE — does loading one child disturb another?"
echo "        The mechanism under P1, asked without depending on any Apple component: load rules"
echo "        into $SIB and see whether $ANCHOR notices."
printf 'table <sib_t> persist\npass in quick from <sib_t> to any\n' > /tmp/pfpa.sib
pfctl -a "$SIB" -f /tmp/pfpa.sib 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
echo "        sibling now has $(sibrules) rule(s)"
sleep 2
capture
verdict "P2 sibling sub-anchor write"

# ---------------------------------------------------------------------------
say "P3 macOS APPLICATION FIREWALL toggled — the live instance of P1's hazard"
if [ ! -x "$ALF" ]; then
  unk "P3: no socketfilterfw at $ALF — NOT TRIED"
else
  ALF_ORIG=$("$ALF" --getglobalstate 2>/dev/null | grep -qi 'enabled' && echo on || echo off)
  echo "        ALF global state before: $ALF_ORIG (it owns com.apple/250.ApplicationFirewall)"
  if [ "$ALF_ORIG" = on ]; then "$ALF" --setglobalstate off >/dev/null 2>&1
  else "$ALF" --setglobalstate on >/dev/null 2>&1; fi
  sleep 3
  echo "        ALF toggled to: $("$ALF" --getglobalstate 2>/dev/null | tail -1)"
  capture
  verdict "P3 Application Firewall toggle"
  "$ALF" --setglobalstate "$ALF_ORIG" >/dev/null 2>&1
  echo "        ALF restored to $ALF_ORIG"
  ALF_ORIG=""
fi

# ---------------------------------------------------------------------------
say "B ENFORCEMENT CONTINUITY across a wipe — the verdict the last run had to withhold"
echo "        Sequence: confirm enforcing; pfctl -F all (kills vmnet NAT, leaves our state); restore"
echo "        NAT by restarting the DISPOSABLE sandbox only; then read $SB's egress WITHOUT ever"
echo "        re-arming its slot. $SB keeps running throughout, so it keeps its address — which is"
echo "        the whole point, since a restart would move it and break the test for the wrong reason."
arm; capture
{ [ "$state_allow" != 000 ] && [ "$state_deny" = 000 ]; } || { bad "not enforcing before B; ABORTING B"; exit 1; }
IP_BEFORE=$(ipof "$SB")
echo "        $SB address before the flush: $IP_BEFORE"
pfctl -F all 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
echo "        main ruleset after flush: $(mainrules) rules"
echo "        our anchor after flush:   rules=$(nrules) src_$SLOT=$(tshow "yb_src_$SLOT")"
echo "        restarting ONLY $SB2 to make vmnet reinstall the bridge's NAT"
asuser "$YOLOAI" stop "$SB2" >/dev/null 2>&1; sleep 3
asuser "$YOLOAI" start "$SB2" >/dev/null 2>&1; sleep 8
echo "        main ruleset now: $(mainrules) rules"
IP_AFTER=$(ipof "$SB")
echo "        $SB address after:  $IP_AFTER  (must equal $IP_BEFORE for this test to mean anything)"
ca=$(egress "$SB" "$ALLOW"); cd=$(egress "$SB" "$DENY")
echo "        $SB egress, slot NEVER re-armed: allow=$ca deny=$cd"
if [ "$IP_AFTER" != "$IP_BEFORE" ]; then
  unk "B: the enforcing sandbox's address moved without a restart ($IP_BEFORE -> $IP_AFTER), so"
  echo "           surviving membership legitimately names nobody. Not a continuity result."
elif [ "$ca" = 000 ]; then
  unk "B: NAT did not come back from restarting the other sandbox, so egress is still dead and"
  echo "           continuity remains untestable by this route. What survived is still only STATE."
elif [ "$ca" != 000 ] && [ "$cd" = 000 ]; then
  ok "B: CONTINUITY CONFIRMED — after pfctl -F all destroyed the main ruleset, the surviving"
  echo "           rules and membership still FILTER: permitted reachable, denied blocked, with the"
  echo "           slot never re-armed and the address unchanged. 'Survived' means enforcing."
else
  bad "B: the sandbox is reachable on a DENIED destination after the flush — state survived but"
  echo "           enforcement did NOT. This is D6's fail-open with a live trigger."
fi

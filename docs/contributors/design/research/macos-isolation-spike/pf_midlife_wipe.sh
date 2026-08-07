#!/bin/bash
# ABOUTME: Asks whether anything wipes yoloAI's pf anchor mid-life, while sandboxes are running,
# ABOUTME: which is the one failure in the reaping design that no start-path check would catch.
#
# Run: sudo bash pf_midlife_wipe.sh          (detached is fine and intended; W5/W6 drop networking)
#
# WHY THIS EXISTS
#   enforcement-state-reaping.md § Distro fragmentation found the Linux half: /etc/nftables.conf
#   conventionally begins with `flush ruleset`, so `systemctl restart nftables` destroys every
#   table including ours. That is D6's fail-open mode — rules gone, membership intact, sandbox
#   reaching denied destinations, nothing distinguishing it from working isolation — reached by an
#   ordinary administrative action with no relationship to yoloAI.
#
#   `reboot-post.txt` establishes that on macOS pf comes back enabled and the anchor restores after
#   a REBOOT. What is unestablished is whether anything wipes the anchor MID-LIFE. Every other
#   failure in the reaping design is caught at start; this one happens under a running sandbox.
#
#   Each candidate is measured the same way, from a known-good enforcing state:
#     W0  census — who else writes pf anchors on this host at all
#     W1  no-op control — wait, change nothing, confirm the harness reports SURVIVED
#     W2  Docker Desktop quit + start
#     W3  another tool toggling pf off and on (pfctl -d / -e)
#     W4  Tailscale (a VPN client) down and up
#     W5  a macOS network-location switch
#     W6  another tool flushing everything (pfctl -F all)
#     W7  a main-ruleset reload (pfctl -f /etc/pf.conf) — the direct analogue of the Linux finding
#
#   The order is deliberate: W6 and W7 act on the MAIN ruleset, where vmnet's NAT also lives, and
#   vmnet installs it at runtime rather than from any file. So either can leave the host's VM
#   networking broken, which would make every later candidate report UNKNOWN for a reason that has
#   nothing to do with it. They run last, and each is followed by a repair attempt that is itself
#   reported — whether the damage is repairable in-process is as much the finding as the wipe.
#
# EVERY VERDICT CARRIES ITS POSITIVE CONTROL (A22). "deny is blocked" is satisfied for free by a
# sandbox with no network at all — DF172's vacuity mode, which silently invalidated the first run
# of the pf harness. So each check reads BOTH a permitted destination and a denied one, and a
# result where the permitted destination also fails is UNKNOWN, never PASS.
#
# SAFETY: the anchor is com.apple/yoloai_b, never com.apple/yoloai. W7 is the exception to
# "never touch the main ruleset" and is the point of the run; the main ruleset's rule count is
# printed before and after so the damage is legible.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/../../../../.." && pwd)"
YOLOAI="$REPO/yoloai"
[ -x "$YOLOAI" ] || { echo "no yoloai binary at $YOLOAI"; exit 2; }

ANCHOR="com.apple/yoloai_b"
SLOTS=4                      # enough to be a pool; this run is about survival, not scale
SLOT=1
SB=mw-a
ALLOW=1.1.1.1; DENY=1.0.0.1
TS=/Applications/Tailscale.app/Contents/MacOS/Tailscale
WD=$(mktemp -d /tmp/pfmw-wd.XXXXXX)
PASS=0; FAIL=0; UNKNOWN=0
SB_IP=""

RESULTS="$HERE/results/pf-midlife-wipe.txt"
mkdir -p "$(dirname "$RESULTS")"
exec > >(tee "$RESULTS") 2>&1

say() { printf '\n== %s ==\n' "$*"; }
ok()  { PASS=$((PASS+1));       printf '   PASS    %s\n' "$*"; }
bad() { FAIL=$((FAIL+1));       printf '   FAIL    %s\n' "$*"; }
unk() { UNKNOWN=$((UNKNOWN+1)); printf '   UNKNOWN %s\n' "$*"; }
asuser() { sudo -u "$U" -H "$@"; }
quiet_pf() { grep -viE 'use of -f option|main ruleset added|/etc/pf.conf for further|ALTQ|^$'; }
nrules() { pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -c . || true; }
tshow()  { pfctl -a "$ANCHOR" -t "$1" -T show 2>/dev/null | tr -d ' ' | tr '\n' ',' ; }
mainrules() { pfctl -s rules 2>/dev/null | grep -c . || true; }
pfenabled() { pfctl -s info 2>/dev/null | head -1 | awk '{print $2}'; }

ipof() { asuser container inspect "yoloai-cli-$1" 2>/dev/null \
  | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d[0]["status"]["networks"][0].get("ipv4Address","").split("/")[0])' 2>/dev/null; }
egress() {
  local c
  c=$(asuser container exec "yoloai-cli-$SB" curl -s -o /dev/null -w '%{http_code}' --max-time 6 "http://$1/" 2>/dev/null)
  c=${c//[^0-9]/}; c=${c: -3}; printf '%s' "${c:-000}"
}

MAIN_BEFORE=$(mainrules)
cleanup() {
  echo
  echo "== cleanup =="
  pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
  asuser "$YOLOAI" destroy "$SB" --abandon-unapplied >/dev/null 2>&1 \
    && echo "   destroyed sandbox $SB" || echo "   NOTE sandbox $SB not destroyed — remove by hand"
  rm -rf "$WD" /tmp/pfmw.*
  echo "   anchor rules=$(nrules)"
  echo "   main ruleset: before=$MAIN_BEFORE after=$(mainrules)   pf status=$(pfenabled)"
  echo "   network location: $(networksetup -getcurrentlocation 2>/dev/null)"
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

# Load the pool and put the live sandbox in its slot. Used at setup and after every wipe, so each
# candidate starts from the same known-good state rather than from the previous candidate's damage.
arm() {
  pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
  gen_rules > /tmp/pfmw.rules
  pfctl -a "$ANCHOR" -f /tmp/pfmw.rules >/dev/null 2>&1
  pfctl -a "$ANCHOR" -t "yb_src_$SLOT" -T add "$SB_IP" >/dev/null 2>&1
  pfctl -a "$ANCHOR" -t "yb_dst_$SLOT" -T add "$ALLOW" >/dev/null 2>&1
}

# The whole observable state, in one line. Enforcement is read live, in both directions, every
# time — the permitted destination is the control and it is never assumed.
state_rules=""; state_src=""; state_dst=""; state_allow=""; state_deny=""
capture() {
  state_rules=$(nrules)
  state_src=$(tshow "yb_src_$SLOT"); state_dst=$(tshow "yb_dst_$SLOT")
  state_allow=$(egress "$ALLOW"); state_deny=$(egress "$DENY")
  printf '        rules=%-3s src_%s=%-18s dst_%s=%-12s allow=%s deny=%s\n' \
    "$state_rules" "$SLOT" "${state_src:-<empty>}" "$SLOT" "${state_dst:-<empty>}" "$state_allow" "$state_deny"
}

# Classify what a candidate did. The interesting axis is not "did anything change" but WHICH half
# went: rules without membership and membership without rules fail in opposite directions.
verdict() {
  local label="$1" r="$2" s="$3" a="$4" d="$5"
  if [ "$a" = 000 ]; then
    unk "$label: the PERMITTED destination is also unreachable, so nothing here is attributable to"
    echo "           enforcement — the sandbox lost its network (DF172 vacuity). Verdict withheld."
  elif [ "${r:-0}" -eq 0 ] && [ -n "$s" ]; then
    bad "$label: RULES WIPED, MEMBERSHIP INTACT — D6's fail-open exactly, reached mid-life"
  elif [ "${r:-0}" -eq 0 ]; then
    bad "$label: ANCHOR FULLY WIPED (rules and tables both gone)"
  elif [ -z "$s" ]; then
    bad "$label: MEMBERSHIP WIPED, rules intact — the sandbox matches no slot and falls through"
  elif [ "$d" != 000 ]; then
    bad "$label: state looks intact but a DENIED destination is reachable — unenforced"
  else
    ok "$label: SURVIVED — rules, membership and enforcement all intact"
  fi
}

echo "host: macOS $(sw_vers -productVersion) $(uname -m) | $(sysctl -n hw.model)"
echo "date: $(date '+%Y-%m-%d %H:%M:%S %Z') | user=$U | anchor=$ANCHOR | slots=$SLOTS slot=$SLOT"
echo "main ruleset rules before: $MAIN_BEFORE | pf: $(pfenabled) | location: $(networksetup -getcurrentlocation)"

# ---------------------------------------------------------------------------
say "W0 CENSUS — who else writes pf anchors on this host"
echo "        A wipe needs a writer. This is the population of them, read before anything moves."
pfctl -s Anchors 2>/dev/null | sed 's/^/        /'
echo "        --- nested under com.apple:"
pfctl -a 'com.apple/*' -s Anchors 2>/dev/null | sed 's/^/        /'
echo "        (Tailscale on macOS uses NetworkExtension rather than pf; its absence here is the"
echo "         evidence for that, and it is why W5 is a weak candidate a priori.)"

# ---------------------------------------------------------------------------
say "SETUP — one live sandbox, enforcing, with a control"
git -C "$WD" init -q 2>/dev/null; : > "$WD/README.md"
git -C "$WD" add -A >/dev/null 2>&1
git -C "$WD" -c user.email=spike@local -c user.name=spike commit -qm init >/dev/null 2>&1
chown -R "$U" "$WD"
asuser container system start >/dev/null 2>&1     # DF180: apple's daemon is not launchd-registered
asuser "$YOLOAI" destroy "$SB" --abandon-unapplied >/dev/null 2>&1
if ! asuser "$YOLOAI" new "$SB" "$WD" --backend apple >/dev/null 2>&1; then
  bad "could not create sandbox $SB; ABORTING"; exit 1
fi
SB_IP=$(ipof "$SB")
[ -n "$SB_IP" ] || { bad "could not resolve $SB's address; ABORTING"; exit 1; }
echo "        $SB at $SB_IP"

b_allow=$(egress "$ALLOW"); b_deny=$(egress "$DENY")
echo "        BEFORE any rules: allow=$b_allow deny=$b_deny  (both must be reachable)"
if [ "$b_allow" = 000 ] || [ "$b_deny" = 000 ]; then
  bad "the sandbox cannot reach the open internet before enforcement is armed. Every 'blocked'"
  echo "           below would then be free. ABORTING — this is the A22 gate."
  exit 1
fi
ok "baseline: both destinations reachable, so a later block is attributable to pf"

arm
capture
if [ "$state_allow" = 000 ] || [ "$state_deny" != 000 ]; then
  bad "armed state is not enforcing (allow=$state_allow deny=$state_deny); ABORTING"; exit 1
fi
ok "armed: $SB is enforcing on slot $SLOT — allowed reachable, denied blocked"

# Re-arm between candidates, and if the host's VM networking itself was collateral damage, say so
# and try to repair it — otherwise every later candidate reports UNKNOWN for a reason that has
# nothing to do with the candidate.
rearm() {
  arm
  local a; a=$(egress "$ALLOW")
  [ "$a" != 000 ] && return 0
  echo "        !! the permitted destination is unreachable after re-arming: this candidate took"
  echo "           out VM networking, not just our anchor. Repairing before continuing."
  asuser container system stop >/dev/null 2>&1; sleep 3
  asuser container system start >/dev/null 2>&1; sleep 5
  asuser "$YOLOAI" start "$SB" >/dev/null 2>&1; sleep 5
  SB_IP=$(ipof "$SB"); arm
  a=$(egress "$ALLOW")
  echo "        after apple daemon restart: $SB at ${SB_IP:-<none>}, allow=$a"
  [ "$a" != 000 ]
}

# A candidate that did not actually happen yields "SURVIVED" for free — the same vacuity that
# DF172 gives a dead sandbox. A candidate sets NOTRUN when it could not perform its action, and
# then no verdict is rendered at all: "we tried and it survived" and "we could not try" are
# different claims and this run must not collapse them.
NOTRUN=""

# Every candidate: capture, act, settle, capture, judge, re-arm.
candidate() {
  local label="$1"; shift
  NOTRUN=""
  say "$label"
  echo "        before:"; capture
  "$@"
  if [ -n "$NOTRUN" ]; then
    unk "$label: NOT TRIED — $NOTRUN"
    echo "           This candidate is UNTESTED. It is not evidence that the anchor survives it."
    arm; return
  fi
  sleep 3
  echo "        after:"; capture
  verdict "$label" "$state_rules" "$state_src" "$state_allow" "$state_deny"
  rearm
}

# --- W1 no-op control ------------------------------------------------------
w1() { echo "        (doing nothing for 10s)"; sleep 10; }
candidate "W1 NO-OP CONTROL — wait, change nothing" w1

# --- W2 Docker Desktop ------------------------------------------------------
w2() {
  if [ ! -d /Applications/Docker.app ]; then NOTRUN="Docker Desktop is not installed"; return; fi
  if ! asuser docker info >/dev/null 2>&1; then
    NOTRUN="Docker Desktop was not running to begin with, so a restart proves nothing"; return
  fi
  echo "        quitting Docker Desktop"
  asuser osascript -e 'quit app "Docker"' >/dev/null 2>&1
  for _ in $(seq 1 30); do asuser docker info >/dev/null 2>&1 || break; sleep 2; done
  if asuser docker info >/dev/null 2>&1; then
    NOTRUN="Docker Desktop never went down (the osascript quit may need Automation permission)"
    return
  fi
  echo "        docker down: yes"
  echo "        starting Docker Desktop"
  asuser open -a Docker >/dev/null 2>&1
  for _ in $(seq 1 60); do asuser docker info >/dev/null 2>&1 && break; sleep 2; done
  local up; up=$(asuser docker info >/dev/null 2>&1 && echo yes || echo no)
  echo "        docker up: $up"
  [ "$up" = yes ] || NOTRUN="Docker Desktop went down but did not come back within 120s"
}
candidate "W2 DOCKER DESKTOP quit and start" w2

# --- W3 pf toggled by another tool -----------------------------------------
w3() {
  echo "        pfctl -d (disable), then -e (enable) — pf is reference counted, so this is what a"
  echo "        tool that thinks it owns the firewall does"
  echo "        references before: $(pfctl -s References 2>/dev/null | tr '\n' ' ')"
  pfctl -d 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
  sleep 2
  echo "        pf while disabled: $(pfenabled), anchor rules=$(nrules), src_$SLOT=$(tshow "yb_src_$SLOT")"
  # The window itself is the finding, and it closes when pf is re-enabled — so it has to be
  # measured here rather than in the after-capture, which only ever sees the repaired state.
  local da dd; da=$(egress "$ALLOW"); dd=$(egress "$DENY")
  echo "        egress WHILE pf is disabled: allow=$da deny=$dd"
  if [ "$da" != 000 ] && [ "$dd" != 000 ]; then
    bad "W3-WINDOW: with pf disabled the sandbox reaches a DENIED destination while its rules and"
    echo "           membership both still read as present. Any check that reads state rather than"
    echo "           behaviour calls this healthy — D6's fail-open, reached from a third direction,"
    echo "           and reachable by any tool on the host running one command."
  elif [ "$da" = 000 ]; then
    unk "W3-WINDOW: permitted destination also unreachable while disabled; nothing attributable"
  else
    ok "W3-WINDOW: still enforcing with pf disabled (unexpected — re-read pfctl's output above)"
  fi
  pfctl -e 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
  echo "        references after: $(pfctl -s References 2>/dev/null | tr '\n' ' ')"
}
candidate "W3 ANOTHER TOOL DISABLES AND RE-ENABLES pf" w3

# --- W4 VPN client ----------------------------------------------------------
w4() {
  if [ ! -x "$TS" ]; then NOTRUN="no Tailscale CLI at $TS"; return; fi
  local before; before=$(asuser "$TS" status 2>&1 | head -1)
  echo "        ts before: $before"
  echo "        tailscale down"
  asuser "$TS" down 2>&1 | head -3 | sed 's/^/        ts: /'
  sleep 5
  local during; during=$(asuser "$TS" status 2>&1 | head -1)
  echo "        ts during: $during"
  echo "        tailscale up"
  asuser "$TS" up 2>&1 | head -3 | sed 's/^/        ts: /'
  sleep 5
  echo "        ts after:  $(asuser "$TS" status 2>&1 | head -1)"
  # If status never changed, the VPN never went down and "the anchor survived" is free.
  if [ "$before" = "$during" ]; then
    NOTRUN="tailscale status did not change across 'down' (CLI refused, or not logged in)"
  fi
}
candidate "W4 VPN CLIENT (Tailscale) down and up" w4

# --- W5 network location switch --------------------------------------------
w5() {
  local orig; orig=$(networksetup -getcurrentlocation)
  echo "        original location: $orig"
  networksetup -createlocation "yoloai-spike" populate >/dev/null 2>&1
  networksetup -switchtolocation "yoloai-spike" >/dev/null 2>&1
  local now; now=$(networksetup -getcurrentlocation)
  echo "        switched to: $now"
  sleep 8
  networksetup -switchtolocation "$orig" >/dev/null 2>&1
  networksetup -deletelocation "yoloai-spike" >/dev/null 2>&1
  local back; back=$(networksetup -getcurrentlocation)
  echo "        restored to: $back"
  sleep 8
  if [ "$now" = "$orig" ]; then
    NOTRUN="the location never actually switched (still '$orig')"
  elif [ "$back" != "$orig" ]; then
    # Leaving the host on a scratch location would poison every later candidate.
    bad "W5: the host did NOT return to '$orig' — it is on '$back'. Restore it by hand."
  fi
}
candidate "W5 macOS NETWORK LOCATION switch and back" w5

# --- W6 pfctl -F all — destructive at the main level ------------------------
w6() {
  echo "        pfctl -F all at the MAIN level (not -a), as another tool would run it. This also"
  echo "        flushes the main ruleset's nat rules, which is where vmnet's live."
  echo "        main ruleset rules before: $(mainrules)"
  pfctl -F all 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
  echo "        main ruleset rules after:  $(mainrules)"
}
candidate "W6 ANOTHER TOOL RUNS pfctl -F all" w6

# --- W7 main ruleset reload — destructive, last -----------------------------
say "W7 MAIN RULESET RELOAD (pfctl -f /etc/pf.conf) — the Linux analogue, and destructive"
echo "        /etc/pf.conf ends with: load anchor \"com.apple\" from \"/etc/pf.anchors/com.apple\","
echo "        and our anchor is nested at com.apple/yoloai_b, loaded at runtime and present in no"
echo "        file. vmnet's NAT is in the same position, which is why this is the one candidate"
echo "        that can break the host's VM networking rather than just our rules."
echo "        before:"; capture
echo "        main ruleset rules before: $(mainrules)"
pfctl -f /etc/pf.conf 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
sleep 3
echo "        main ruleset rules after:  $(mainrules)"
echo "        after:"; capture
verdict "W7 main ruleset reload" "$state_rules" "$state_src" "$state_allow" "$state_deny"

say "W7-RECOVERY — can a host get back to working VM networking without a reboot?"
echo "        This matters as much as the wipe: if enforcement vanishes and cannot be restored"
echo "        in-process, the design needs a repair path, not just a detector."
arm
capture
if [ "$state_allow" != 000 ]; then
  ok "reloading our anchor restored enforcement, and vmnet NAT survived the main reload"
else
  echo "        vmnet NAT did not survive; attempting apple daemon restart"
  asuser container system stop >/dev/null 2>&1; sleep 3
  asuser container system start >/dev/null 2>&1; sleep 5
  asuser "$YOLOAI" start "$SB" >/dev/null 2>&1; sleep 5
  SB_IP=$(ipof "$SB"); echo "        $SB now at ${SB_IP:-<none>}"
  arm; capture
  if [ "$state_allow" != 000 ]; then
    ok "recovered by restarting the apple daemon and the sandbox — no reboot needed"
  else
    bad "VM networking still broken after an anchor reload and a daemon restart. A REBOOT IS"
    echo "           LIKELY NEEDED on this host. That is itself the finding: a main-ruleset reload"
    echo "           by an unrelated tool is not repairable from inside yoloAI."
  fi
fi

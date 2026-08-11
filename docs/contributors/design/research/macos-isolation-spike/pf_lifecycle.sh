#!/bin/bash
# ABOUTME: Implements the macOS lifecycle rule -- withdraw on detach, re-read the index on return.
# ABOUTME: Then races it: can a stranger take a released bridge index faster than we can withdraw?
#
# Run: sudo bash pf_lifecycle.sh
#
# WHY THIS EXISTS
#   `pf-interface-key.txt` established that macOS has a usable per-sandbox key and that its whole
#   price is one lifecycle rule, because bridge indices recycle after release: a stranger that takes
#   a departed sandbox's index inherits its policy WHOLE -- reaching a destination only the departed
#   sandbox was granted, and refused its own -- and I5b measured that withdrawing the stale rule
#   restores correct policy exactly.
#
#   That is where it stopped. "Withdraw the rule" was measured as an OUTCOME (withdraw it, and
#   policy is correct again) and never as a MECHANISM running against a live restart. The rewritten
#   enforcement plan now carries it as the one thing macOS needs that Linux does not, so it should be
#   the best-tested part of the macOS half and is currently the least tested.
#
#   The unmeasured thing is not whether withdrawal works. It is the WINDOW. Detection is a poll --
#   macOS emits no signal on a pf change and none on this either -- so between the instant an index
#   is released and the instant we notice, a stranger can take it. If a sandbox can attach faster
#   than we can withdraw, the lifecycle rule has a race, and a race that is lost silently hands one
#   sandbox another's egress policy. Nothing here has ever timed either side of that.
#
#   L1 BASELINE      A enforcing on its own bridge, with the shape pf-no-state.txt established.
#   L2 RULE OFF      the control: no lifecycle mechanism at all. A departs, a stranger takes the
#                    index, and the stranger must INHERIT -- this is the I5 hazard reproduced, and
#                    without it the next section shows a stranger with correct policy for reasons
#                    that could have nothing to do with our mechanism.
#   L3 RULE ON       the same race with the withdrawal loop running. The stranger must get its own
#                    policy.
#   L4 THE WINDOW    the numbers behind L2/L3: milliseconds from release to withdrawal, against
#                    milliseconds from release to a stranger holding the index. A verdict about a
#                    race that does not report both sides is an opinion.
#   L5 THE RETURN    the other half of the rule. A comes back -- and if the stranger still holds its
#                    old index, it comes back on a DIFFERENT one. A rule that re-attaches by name
#                    (I4) is then attached to the wrong sandbox's interface. Re-reading the real
#                    index is the fix, and it has never been run.
#
# SAFETY: writes only into its own anchor; never touches the main ruleset.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"

HERE="$(cd "$(dirname "$0")" && pwd)"
ANCHOR="com.apple/yoloai_l"
IMG=yoloai-base:latest
NET_A=ylnet_a; G_A=yl_a
NET_W=ylnet_w; G_W=yl_w
ALLOW_A=1.1.1.1      # A's allowlist
ALLOW_W=1.0.0.1      # the stranger's, deliberately disjoint so a leak is observable at all
PASS=0; FAIL=0; UNKNOWN=0
WD=$(mktemp -d /tmp/pfl.XXXXXX)
DAEMON_PID=""
A_BR=""; A_GW=""; W_BR=""

# The daemon runs in a background subshell, so a shell variable it assigns is invisible to the
# parent -- and the parent's next reload would silently re-install the rules the daemon had just
# withdrawn, failing L3 for a reason that has nothing to do with the mechanism. Both sides share
# one-key-per-file state instead, so whoever reloads composes from what is actually current.
setk() { printf '%s' "$2" > "$WD/k_$1"; }
getk() { cat "$WD/k_$1" 2>/dev/null; }

RESULTS="$HERE/results/pf-lifecycle.txt"
mkdir -p "$(dirname "$RESULTS")"
exec > >(tee "$RESULTS") 2>&1

say() { printf '\n== %s ==\n' "$*"; }
ok()  { PASS=$((PASS+1));       printf '   PASS    %s\n' "$*"; }
bad() { FAIL=$((FAIL+1));       printf '   FAIL    %s\n' "$*"; }
unk() { UNKNOWN=$((UNKNOWN+1)); printf '   UNKNOWN %s\n' "$*"; }
note(){ printf '        %s\n' "$*"; }
asuser() { sudo -u "$U" -H "$@"; }
quiet_pf() { grep -viE 'use of -f option|main ruleset added|/etc/pf.conf for further|ALTQ|^$'; }
mainrefs() { pfctl -s rules 2>/dev/null | grep -c 'com\.apple/' || true; }
now_ms() { python3 -c 'import time; print(int(time.time()*1000))'; }

netfield() { asuser container inspect "$1" 2>/dev/null | python3 -c '
import json,sys
d=json.load(sys.stdin)
print(d[0]["status"]["networks"][0].get(sys.argv[1],"").split("/")[0])' "$2" 2>/dev/null; }
gwof() { netfield "$1" ipv4Gateway; }
brof() { ifconfig -a 2>/dev/null | awk -v want="$1" '
  /^bridge/ {br=$1; sub(":","",br)}
  /inet / {if ($2==want) {print br; exit}}'; }
brofguest() { local g; g=$(gwof "$1"); [ -n "$g" ] && brof "$g"; }
gtry() { local o; o=$(asuser container exec "$1" curl -s -o /dev/null -w '%{http_code}' \
           --max-time 5 "http://$2/" 2>/dev/null); printf '%s' "${o:-000}"; }

mknet()   { asuser container network create "$1" >/dev/null 2>&1; }
rmnet()   { asuser container network delete "$1" >/dev/null 2>&1; }
mkguest() { asuser container rm -f "$1" >/dev/null 2>&1
            asuser container run -d --name "$1" --network "$2" "$IMG" sleep 900 >/dev/null 2>&1; }

# The shape pf-no-state.txt established: both directions passed statelessly, both directions
# blocked. Using anything else here would measure a rule form the design no longer proposes.
rules_for() {   # $1 = bridge, $2 = table suffix
  [ -z "$1" ] && return
  printf 'pass  in  quick on %s proto tcp from any to <yl_%s> no state\n' "$1" "$2"
  printf 'pass  out quick on %s proto tcp from <yl_%s> to any no state\n' "$1" "$2"
  printf 'block drop in  quick on %s proto tcp from any to any\n' "$1"
  printf 'block drop out quick on %s proto tcp from any to any\n' "$1"
}

# Composes the whole anchor from the two on/off switches and the two current bridges, then
# RE-CLAIMS both tables -- a flush destroys table membership, and a ruleset loaded against an empty
# allowlist enforces nothing while reading correctly (pf-revocation-alt.txt T3).
#
# A's rules are emitted FIRST when both are on, because that is the ordering the hazard needs: pf is
# first-match-with-quick, so a stale rule sitting ahead of the stranger's own is what makes the
# stranger inherit rather than merely lose.
reload_anchor() {
  local abr wbr aon won
  abr=$(getk a_br); wbr=$(getk w_br); aon=$(getk a_on); won=$(getk w_on)
  { echo "table <yl_a> persist"; echo "table <yl_w> persist"
    [ "$aon" = 1 ] && rules_for "$abr" a
    [ "$won" = 1 ] && rules_for "$wbr" w
  } > "$WD/l.rules"
  pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
  pfctl -a "$ANCHOR" -f "$WD/l.rules" 2>&1 | quiet_pf >/dev/null
  pfctl -a "$ANCHOR" -t yl_a -T add "$ALLOW_A" >/dev/null 2>&1
  pfctl -a "$ANCHOR" -t yl_w -T add "$ALLOW_W" >/dev/null 2>&1
}
nrules() { pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -c . || true; }

# THE LIFECYCLE RULE ITSELF.
#
# Detection is a poll on `ifconfig`, not on `container inspect`: inspect costs a few hundred ms per
# call, and putting that inside the loop would make the measured window a fact about the harness
# rather than about the mechanism. The product would cache the gateway at claim time and watch the
# interface, which is what this does.
#
# The condition is "A's bridge no longer carries A's gateway", not "the bridge is gone". An index is
# dangerous precisely when it still exists and belongs to someone else.
lifecycle_daemon() {
  local seen=attached
  while :; do
    if [ "$(brof "$A_GW")" != "$A_BR" ]; then
      if [ "$seen" = attached ]; then
        now_ms > "$WD/t_detect"
        setk a_on 0; reload_anchor
        now_ms > "$WD/t_withdraw"
        seen=detached
      fi
    fi
    sleep 0.05
  done
}
start_daemon() { lifecycle_daemon & DAEMON_PID=$!; }
stop_daemon()  { [ -n "$DAEMON_PID" ] && kill "$DAEMON_PID" 2>/dev/null; DAEMON_PID=""; }

cleanup() {
  echo
  echo "== cleanup =="
  stop_daemon
  pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
  for g in "$G_A" "$G_W"; do asuser container rm -f "$g" >/dev/null 2>&1; done
  for n in "$NET_A" "$NET_W"; do rmnet "$n"; done
  rm -rf "$WD"
  echo "   main-refs: before=${MAINREFS0:-?} after=$(mainrefs)   anchor rules: $(nrules)"
  echo
  echo "pass=$PASS fail=$FAIL unknown=$UNKNOWN"
  echo "results: $RESULTS"
}
trap cleanup EXIT

echo "host: macOS $(sw_vers -productVersion) $(uname -m)"
echo "date: $(date '+%Y-%m-%d %H:%M:%S %Z') | anchor=$ANCHOR"

# ---------------------------------------------------------------------------
say "L1 BASELINE — A enforcing on its own bridge"
MAINREFS0=$(mainrefs)
[ "$MAINREFS0" -gt 0 ] || { bad "host is fail-open (main-refs=0). ABORTING"; exit 1; }
asuser container system start >/dev/null 2>&1; sleep 2
for g in "$G_A" "$G_W"; do asuser container rm -f "$g" >/dev/null 2>&1; done
for n in "$NET_A" "$NET_W"; do rmnet "$n"; done
mknet "$NET_A"; mkguest "$G_A" "$NET_A"; sleep 4
A_GW=$(gwof "$G_A"); A_BR=$(brof "$A_GW")
note "A: guest=$(netfield "$G_A" ipv4Address) gateway=$A_GW bridge=$A_BR"
[ -n "$A_BR" ] || { bad "A has no bridge; ABORTING"; exit 1; }
setk a_br "$A_BR"; setk w_br ""; setk a_on 1; setk w_on 0; reload_anchor
a_allow=$(gtry "$G_A" "$ALLOW_A"); a_deny=$(gtry "$G_A" "$ALLOW_W")
note "A -> its own allowlist ($ALLOW_A): $a_allow    A -> the stranger's ($ALLOW_W): $a_deny"
if [ "$a_allow" != 000 ] && [ "$a_deny" = 000 ]; then
  ok "L1: A is enforcing, and the two allowlists are disjoint so a leak is observable"
else
  bad "L1: no enforcement baseline (allow=$a_allow deny=$a_deny). ABORTING"; exit 1
fi

# ---------------------------------------------------------------------------
# One race, run twice with the mechanism absent and present. Shared so the two arms cannot drift.
# Sets RACE_W_BR, RACE_INHERIT, T_RELEASE, T_ATTACH.
race() {   # $1 = label
  RACE_W_BR=""; RACE_INHERIT=unknown
  asuser container rm -f "$G_W" >/dev/null 2>&1; rmnet "$NET_W"
  rm -f "$WD/t_withdraw" "$WD/t_detect"
  note "$1: stopping A and creating a stranger immediately"
  T_RELEASE=$(now_ms)
  asuser container stop "$G_A" >/dev/null 2>&1
  mknet "$NET_W"
  asuser container run -d --name "$G_W" --network "$NET_W" "$IMG" sleep 900 >/dev/null 2>&1
  # Wait for the stranger to hold an interface, then stamp the moment it does.
  local i wgw
  for ((i=0;i<60;i++)); do
    wgw=$(gwof "$G_W"); [ -n "$wgw" ] && RACE_W_BR=$(brof "$wgw")
    [ -n "$RACE_W_BR" ] && break
    sleep 0.5
  done
  T_ATTACH=$(now_ms)
  W_BR=$RACE_W_BR
  note "$1: stranger is on ${RACE_W_BR:-<none>} (A held $A_BR); gateway=$(gwof "$G_W")"
  if [ -z "$RACE_W_BR" ]; then
    note "$1: the stranger never got an interface; this arm cannot decide anything"
    return
  fi
  if [ "$RACE_W_BR" != "$A_BR" ]; then
    note "$1: the stranger did NOT take A's index, so the hazard was not reproduced in this arm."
    note "     That is the allocator's choice, not a result about the lifecycle rule."
    RACE_INHERIT=not-reproduced
    return
  fi
  # The stranger's own rules go on, AFTER whatever is already loaded -- first-match-with-quick is
  # the mechanism, so the ordering is the experiment.
  setk w_br "$RACE_W_BR"; setk w_on 1; reload_anchor
  local w_a w_w
  w_a=$(gtry "$G_W" "$ALLOW_A"); w_w=$(gtry "$G_W" "$ALLOW_W")
  note "$1: stranger -> A's allowlist ($ALLOW_A): $w_a    stranger -> its own ($ALLOW_W): $w_w"
  if [ "$w_a" != 000 ] && [ "$w_w" = 000 ]; then
    RACE_INHERIT=yes
  elif [ "$w_a" = 000 ] && [ "$w_w" != 000 ]; then
    RACE_INHERIT=no
  else
    RACE_INHERIT="neither (A=$w_a own=$w_w)"
  fi
  note "$1: anchor now holds $(nrules) rule(s)"
}

say "L2 THE CONTROL — no lifecycle mechanism at all, so the stranger must INHERIT"
note "Without this arm, a stranger with correct policy in L3 could be correct because the allocator"
note "handed it a different index, because the timing happened to differ, or because the hazard does"
note "not reproduce on this host today. I5 is reproducible, so the control is cheap and decisive."
race L2
L2_INHERIT=$RACE_INHERIT
case "$L2_INHERIT" in
  yes) ok "L2: the stranger INHERITED A's policy — reached A's destination, refused its own. The"
       note "    hazard reproduces, so L3 has something to fix." ;;
  no)  bad "L2: the stranger got its OWN policy with no mechanism running. Either the hazard does not"
       note "     reproduce here or something else withdrew A's rules; L3 would prove nothing." ;;
  not-reproduced) unk "L2: the stranger did not take A's index; the race did not happen." ;;
  *)   unk "L2: neither inheritance nor correct policy ($L2_INHERIT)" ;;
esac

# ---------------------------------------------------------------------------
say "L3 THE MECHANISM — the same race with the withdrawal loop running"
note "Restoring A first, so this arm starts where L2 started rather than from L2's wreckage."
asuser container rm -f "$G_W" >/dev/null 2>&1; rmnet "$NET_W"
asuser container start "$G_A" >/dev/null 2>&1; sleep 5
A_GW=$(gwof "$G_A"); A_BR=$(brof "$A_GW")
note "A is back on ${A_BR:-<none>} with gateway $A_GW"
if [ -z "$A_BR" ]; then
  unk "L3: A did not come back with an interface; the arm cannot run"
  L3_INHERIT=untested
else
  setk a_br "$A_BR"; setk w_br ""; setk a_on 1; setk w_on 0; reload_anchor
  a2=$(gtry "$G_A" "$ALLOW_A")
  note "A enforcing again on its returned interface: -> $ALLOW_A gives $a2 (need non-000)"
  start_daemon
  note "lifecycle daemon running: polls every 50 ms for 'bridge $A_BR no longer carries $A_GW'"
  race L3
  L3_INHERIT=$RACE_INHERIT
  stop_daemon
  case "$L3_INHERIT" in
    no)  ok "L3: the stranger got its OWN policy — it was refused A's destination and reached its own."
         note "    The lifecycle rule closes the hazard against a live restart, not just in principle." ;;
    yes) bad "L3: the stranger INHERITED A's policy even with the mechanism running. The withdrawal"
         note "     lost the race, which is the failure mode the window below is measuring." ;;
    not-reproduced) unk "L3: the stranger did not take A's index this time; nothing was raced." ;;
    *)   unk "L3: $L3_INHERIT" ;;
  esac
fi

# ---------------------------------------------------------------------------
say "L4 THE WINDOW — both sides of the race, in milliseconds"
note "A verdict about a race that reports only the side that won is an opinion. These are the two"
note "durations that decide whether the mechanism is sound or merely lucky on this host."
note "Three intervals, not one. The first is the backend tearing a sandbox down and is nothing to do"
note "with us; folding it into 'our side' would flatter the mechanism by several seconds. The margin"
note "that matters is measured from the moment the index is actually free."
if [ -f "$WD/t_withdraw" ] && [ -f "$WD/t_detect" ]; then
  TD=$(cat "$WD/t_detect"); TW=$(cat "$WD/t_withdraw")
  note "stop issued -> index actually released   : $(( TD - T_RELEASE )) ms   (the backend's, not ours)"
  note "release     -> withdrawal complete       : $(( TW - TD )) ms   (ours: 50 ms poll + pfctl reload)"
  note "release     -> stranger held an interface: $(( T_ATTACH - TD )) ms   (theirs)"
  d_withdraw=$(( TW - TD ))
  d_attach=$((  T_ATTACH - TD ))
  note "margin                                   : $(( d_attach - d_withdraw )) ms"
  if [ "$d_withdraw" -lt "$d_attach" ]; then
    ok "L4: withdrawal completed $(( d_attach - d_withdraw )) ms before the stranger held an interface."
    note "    That margin is a fact about THIS host's sandbox start time, not a property of the design:"
    note "    a faster start, a busier host, or folding detection into the 320-385 ms canary loop all"
    note "    narrow it, and the canary's cadence alone would consume most of it."
  else
    bad "L4: the stranger attached BEFORE the withdrawal completed. The mechanism is racing and losing;"
    note "     any run where it appears to work is winning on timing, not by construction."
  fi
else
  unk "L4: the daemon never recorded a withdrawal, so there is no window to report. Either it never"
  note "     detected the release or it was not running for this arm."
fi

# ---------------------------------------------------------------------------
say "L5 THE RETURN — re-reading the real index, which is the other half of the rule"
note "I4 established that a rule re-attaches BY NAME when its interface returns. That is the good"
note "case and also the trap: if the stranger still holds A's old index, A returns on a DIFFERENT"
note "one, and a rule that re-attached by name is now attached to the stranger's interface — pointing"
note "A's policy at another sandbox. Re-reading the index on return is the fix and has never been run."
if [ -z "${L3_INHERIT:-}" ] || [ "${L3_INHERIT:-}" = untested ]; then
  unk "L5: no usable state from L3 to continue from"
else
  note "leaving the stranger in place on ${W_BR:-<none>} so A cannot have its old index back"
  A_BR_STALE=$A_BR
  asuser container start "$G_A" >/dev/null 2>&1; sleep 6
  A_GW_NEW=$(gwof "$G_A"); A_BR_NEW=$(brof "$A_GW_NEW")
  W_GW_NOW=$(gwof "$G_W");  W_BR_NOW=$(brof "$W_GW_NOW")
  note "A returned on ${A_BR_NEW:-<none>} (it held $A_BR_STALE before)"
  note "the stranger is now on ${W_BR_NOW:-<none>} (it held ${W_BR:-<none>})"
  if [ -z "$A_BR_NEW" ]; then
    unk "L5: A came back without an interface — that is DF190's shape, not this experiment's"
    REREAD=not-reachable
  elif [ "$A_BR_NEW" = "$A_BR_STALE" ]; then
    # This is the case that actually occurs, and it is a fact about the backend rather than a
    # failure of anything here: a returning sandbox takes its old index BACK, off whoever holds it.
    REREAD=not-reachable
    w_own=$(gtry "$G_W" "$ALLOW_W"); w_a=$(gtry "$G_W" "$ALLOW_A")
    note ""
    note "A RECLAIMED ITS OLD INDEX, off the sandbox that had taken it. So the premise this section"
    note "was built on — that A returns on a DIFFERENT index while a stranger holds the old one —"
    note "does not arise on this backend, and the re-read half of the lifecycle rule cannot be"
    note "exercised by this route. That is a measured constraint, not a passing result."
    note "displaced stranger: -> its own allowlist $w_own   -> A's $w_a   (both 000 = no egress at all)"
    if [ "$w_own" = 000 ] && [ "$w_a" = 000 ] && [ -z "$W_BR_NOW" ]; then
      ok "L5: DF190 reproduces here as a side effect — the displaced sandbox has no bridge and no"
      note "    egress at all, while A is fine. This is the fourth independent reproduction, and it"
      note "    now has a trigger stated as a rule: a returning sandbox reclaims its index from the"
      note "    incumbent, and the incumbent is not rehomed."
    else
      unk "L5: A reclaimed the index but the stranger was not obviously displaced (own=$w_own"
      note "     A's=$w_a bridge=${W_BR_NOW:-<none>}); DF190 did not reproduce cleanly this time"
    fi
  else
    # The designed comparison, reachable only if A ever returns on a different index.
    REREAD=tested
    note ""
    note "A returned on a DIFFERENT index, so the two forms can finally be told apart."
    note "FIRST the stale form: A's rules as they were, still naming $A_BR_STALE."
    { echo "table <yl_a> persist"; echo "table <yl_w> persist"
      rules_for "$A_BR_STALE" a; rules_for "$W_BR_NOW" w; } > "$WD/stale.rules"
    pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
    pfctl -a "$ANCHOR" -f "$WD/stale.rules" 2>&1 | quiet_pf >/dev/null
    pfctl -a "$ANCHOR" -t yl_a -T add "$ALLOW_A" >/dev/null 2>&1
    pfctl -a "$ANCHOR" -t yl_w -T add "$ALLOW_W" >/dev/null 2>&1
    s_a=$(gtry "$G_A" "$ALLOW_A"); s_d=$(gtry "$G_A" "$ALLOW_W")
    note "A under stale rules: -> own $s_a   -> stranger's $s_d"
    note ""
    note "NOW the rule as specified: re-read the real index and reload."
    A_BR=$A_BR_NEW
    setk a_br "$A_BR_NEW"; setk w_br "$W_BR_NOW"; setk a_on 1; setk w_on 1; reload_anchor
    r_a=$(gtry "$G_A" "$ALLOW_A"); r_d=$(gtry "$G_A" "$ALLOW_W")
    r_w=$(gtry "$G_W" "$ALLOW_W"); r_wa=$(gtry "$G_W" "$ALLOW_A")
    note "A  -> own $r_a   -> stranger's $r_d      (need non-000 then 000)"
    note "W  -> own $r_w   -> A's $r_wa            (need non-000 then 000)"
    if [ "$r_a" != 000 ] && [ "$r_d" = 000 ] && [ "$r_w" != 000 ] && [ "$r_wa" = 000 ]; then
      ok "L5: re-reading the index restores BOTH sandboxes to their own policy simultaneously"
    else
      bad "L5: re-reading did not restore correct policy for both (A=$r_a/$r_d W=$r_w/$r_wa)"
    fi
  fi
fi

# ---------------------------------------------------------------------------
say "L6 VERDICT — derived from the arms above"
note "  hazard reproduces without the rule (L2): ${L2_INHERIT:-untested}"
note "  stranger's policy with the rule (L3):    ${L3_INHERIT:-untested}"
note "  re-read half exercised (L5):             ${REREAD:-untested}"
if [ "${L2_INHERIT:-}" = yes ] && [ "${L3_INHERIT:-}" = no ] && [ "${REREAD:-}" = tested ]; then
  ok "L6: both halves of the lifecycle rule are measured against a live restart, and the control"
  note "    proves the hazard was there to be closed."
elif [ "${L2_INHERIT:-}" = yes ] && [ "${L3_INHERIT:-}" = no ]; then
  ok "L6: the WITHDRAW half works against a live restart, with the control proving the hazard was"
  note "    there to be closed and L4 reporting both sides of the race."
  note "    The RE-READ half is ${REREAD:-untested}: a returning sandbox reclaims its old index"
  note "    rather than taking a new one, so the case that half exists for did not arise. It stays"
  note "    unverified — and the plan should say so rather than describing the rule as measured."
else
  unk "L6: the two arms did not separate cleanly; read L2 and L3 rather than this line."
fi

say "WHAT WAS NOT TRIED"
cat <<'EOF'
        - Concurrency beyond two sandboxes. One departing, one arriving. Nothing here says what
          happens when several are created inside one detach window, which is the case a busy host
          produces and the case where a lowest-free allocator is most likely to hand out a
          just-released index.
        - Crash rather than `container stop`. A clean stop may release the index differently from a
          killed VM or a daemon restart, and the product's dangerous case is the unclean one.
        - The poll interval as a variable. 50 ms was chosen and never varied, so the margin in L4 is
          one point on a curve nobody has drawn. The product's canary polls at 320-385 ms, and if
          detection is folded into that loop the margin changes by an order of magnitude.
        - What the daemon costs. It runs `ifconfig` twenty times a second per watched sandbox and
          nothing measures that against a host running eight of them.
        - Whether anything ELSE notices the withdrawal. The rules vanish from the anchor with no
          signal, and a second manager -- or our own liveness canary -- may read that as a fault and
          reinstall them. Untested, and it is the shape of the trample problem the plan already
          names for Linux.
        - Recovery if the daemon itself dies mid-window. The rules stay withdrawn and the sandbox
          stays unenforced-but-unreachable, or stay loaded and stale; neither is measured.
EOF

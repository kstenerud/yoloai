#!/bin/bash
# ABOUTME: M4 — does macOS emit ANY signal when the pf main ruleset changes, or must we poll?
# ABOUTME: Unified log, notify(3) keys, pfctl -s info deltas, and a file watch, against real events.
#
# Run: sudo bash pf_change_signal.sh
#
# WHY THIS EXISTS
#   Docker's answer to the same hazard is a SUBSCRIPTION: firewalld emits a D-Bus reload signal and
#   moby #49443 reacts to it, which is cheaper and faster than any health probe. Whether macOS has
#   an equivalent has never been asked. If it does not, the liveness detector must poll — and the
#   polling interval stops being an implementation detail and becomes a design parameter, because
#   it is exactly the width of the window in which a sandbox runs unenforced.
#
#   FOUR PLACES A SIGNAL COULD COME FROM, tried against real ruleset changes:
#     S1 THE UNIFIED LOG. pf is in-kernel; pfctl is a userland tool. Either might log a reload.
#     S2 notify(3). The Darwin-native publish/subscribe layer, and what a system service would use.
#        There is no way to enumerate every key, so this watches a named list and the list is
#        printed — which is what makes the negative a bounded claim rather than a shrug.
#     S3 `pfctl -s info` DELTAS. Long shot with the best payoff: `-s info` is ALREADY in the shipped
#        D132 grant, so if any field moves when the ruleset changes, polling costs no new privilege.
#     S4 A FILE WATCH on /etc/pf.conf. The obvious idea, and it is tested in order to be REFUTED:
#        `pfctl -F all` never touches the file, so a watcher sees nothing while enforcement dies.
#
#   THREE EVENTS, spanning what actually happens on a real host:
#     E1 `pfctl -f /etc/pf.conf`  — a reload, the benign case
#     E2 `pfctl -F all`           — the measured fault
#     E3 `pfctl -d` then `-e`     — disable/enable, which D132's check 1 already covers
#
# METHOD: every watcher is proven ALIVE in the same run before its silence is reported — the notify
#   watcher by posting a key to itself, the log by querying for something known to be there. A dead
#   watcher reports "no signal" for free, which is this directory's oldest mistake in a new costume.
#
# SAFETY: E2 breaks the main ruleset. It is repaired immediately and the host is re-asserted at exit.
#   No guest is involved, so nothing here depends on a sandbox being up.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"

HERE="$(cd "$(dirname "$0")" && pwd)"
PASS=0; FAIL=0; UNKNOWN=0
WD=$(mktemp -d /tmp/pfsig.XXXXXX)
NPID=""

RESULTS="$HERE/results/pf-change-signal.txt"
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

# The keys watched in S2. No API enumerates every notify key, so this list IS the claim's boundary
# and it is printed in the results for exactly that reason.
KEYS=(
  com.apple.system.config.network_change
  com.apple.system.config.ipv4
  com.apple.system.config.ipv6
  com.apple.system.config.dns
  com.apple.system.packetfilter
  com.apple.pfctl
  com.apple.networkextension.filter-configuration-changed
  state:/Network/Global/IPv4
  com.apple.system.config.sc_dynamicstore
)

restore_pf() {
  pfctl -e >/dev/null 2>&1
  pfctl -f /etc/pf.conf 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
}

cleanup() {
  echo
  echo "== cleanup =="
  [ -n "$NPID" ] && kill "$NPID" 2>/dev/null
  if [ "$(mainrefs)" -eq 0 ]; then
    echo "   main ruleset is empty — reloading"
    restore_pf
    asuser container system stop  >/dev/null 2>&1; sleep 3
    asuser container system start >/dev/null 2>&1; sleep 4
  fi
  echo "   pf=$(pfctl -s info 2>/dev/null | head -1 | awk '{print $2}')  main-refs=$(mainrefs)"
  [ "$(mainrefs)" -gt 0 ] && echo "   main ruleset references com.apple/* again" \
                          || echo "   !! STILL FAIL-OPEN — run pf_anchor_eval.sh"
  rm -rf "$WD"
  echo
  echo "pass=$PASS fail=$FAIL unknown=$UNKNOWN"
  echo "results: $RESULTS"
}
trap cleanup EXIT

echo "host: macOS $(sw_vers -productVersion) $(uname -m)"
echo "date: $(date '+%Y-%m-%d %H:%M:%S %Z')"
echo "pf:   $(pfctl -s info 2>/dev/null | head -1)"

# ---------------------------------------------------------------------------
say "S0 BASELINE"
M0=$(mainrefs)
note "main-refs=$M0"
[ "$M0" -gt 0 ] || { bad "host is already fail-open; run pf_anchor_eval.sh first. ABORTING"; exit 1; }
note "pf.conf mtime: $(stat -f '%Sm' /etc/pf.conf)"

# ---------------------------------------------------------------------------
say "S2a IS THE NOTIFY WATCHER ALIVE? — proven before its silence is reported"
WATCH=()
for k in "${KEYS[@]}"; do WATCH+=(-w "$k"); done
notifyutil -v "${WATCH[@]}" -w com.yoloai.spike.selftest > "$WD/notify.txt" 2>&1 &
NPID=$!
sleep 2
notifyutil -p com.yoloai.spike.selftest >/dev/null 2>&1
sleep 2
if grep -q 'com.yoloai.spike.selftest' "$WD/notify.txt" 2>/dev/null; then
  ok "S2a: the watcher receives notifications (its own test post came through)"
  WATCHER_OK=1
else
  unk "S2a: the watcher never saw a post it made itself; S2's silence below proves nothing"
  WATCHER_OK=0
fi
note "keys under watch:"
printf '%s\n' "${KEYS[@]}" | sed 's/^/          /'
: > "$WD/notify.txt"

# ---------------------------------------------------------------------------
LOGPRED='process == "pfctl" OR eventMessage CONTAINS[c] "packet filter" OR eventMessage CONTAINS[c] "pf ruleset"'

say "S1a IS THE LOG QUERY ALIVE? — the proof S2a already gives the notify watcher"
note "S1's result is a SILENCE, and a silence is only evidence if the instrument works. The notify"
note "half establishes that by posting to itself. The log half had no equivalent, so an inert query"
note "and a genuinely quiet subsystem produced identical output — and this directory has already"
note "published one wrong predicate (pf-anchor-eval-run1-predicate-bug.txt) that read exactly so."
note ""
note "Two arms, because they fail for different reasons and only the pair is interpretable:"
note "  A1  can this predicate match ANYTHING, in this window, through this command? A message is"
note "      written specifically to satisfy it, so a miss is the instrument and not the subject."
note "  A2  does the clause that would carry a real signal — process == \"pfctl\" — match a pfctl"
note "      invocation made deliberately, right now?"
note "Each is asked twice: at the default level, and with --info --debug. macOS withholds info and"
note "debug records from \`log show\` by default, so 'not logged' and 'logged beneath the default"
note "level' are different findings that the default form alone cannot separate."
LOGSELF="yoloai-spike-selftest packet filter probe"
LOGQ_OK=0
lt0=$(date '+%Y-%m-%d %H:%M:%S')
sleep 1
logger "$LOGSELF"
pfctl -s info >/dev/null 2>&1
sleep 3
logq() { log show --start "$lt0" --predicate "$1" --style compact ${2:-} 2>/dev/null; }
a1=$(logq "$LOGPRED"                | grep -c "$LOGSELF" || true)
a1d=$(logq "$LOGPRED" "--info --debug" | grep -c "$LOGSELF" || true)
a2=$(logq 'process == "pfctl"'                | grep -c '^[0-9][0-9][0-9][0-9]-' || true)
a2d=$(logq 'process == "pfctl"' "--info --debug" | grep -c '^[0-9][0-9][0-9][0-9]-' || true)
note ""
note "A1 self-emitted message found:  default=$a1   with --info --debug=$a1d"
note "A2 entries from process pfctl:  default=$a2   with --info --debug=$a2d"
if [ "$a1" -gt 0 ] || [ "$a1d" -gt 0 ]; then
  LOGQ_OK=1
  ok "S1a: the predicate and the query DO match a message emitted inside the window"
  if [ "$a1" -eq 0 ]; then
    note "     — but ONLY with --info --debug. S1 below runs the default form, so anything pf writes"
    note "       below notice level is invisible to it, and its silence is exactly that narrow."
  fi
else
  unk "S1a: nothing matched — not even a message written to match. S1's silence proves NOTHING,"
  note "     and the entries it reports below cannot be read as a measurement of pf's behaviour."
fi
if [ "$a2" -gt 0 ] || [ "$a2d" -gt 0 ]; then
  note "A2: pfctl DOES write to the unified log ($a2 default / $a2d with info+debug), so the"
  note "    process clause is live and a silence from it during an event is about the event."
else
  note "A2: a deliberate pfctl invocation produced ZERO entries under process == \"pfctl\". That"
  note "    clause cannot detect anything on this host, so S1's negative rests entirely on the two"
  note "    eventMessage clauses — a materially narrower claim than the predicate's shape suggests."
fi

# ---------------------------------------------------------------------------
say "S1/S3 THE EVENTS — log, notify, -s info and file mtime around each"

snapshot() { pfctl -s info 2>/dev/null; pfctl -s Anchors 2>/dev/null; }

run_event() {   # $1 = label, $2 = shell command to trigger it
  local label="$1" cmd="$2" t0
  note ""
  note "--- $label ---"
  snapshot > "$WD/before.txt"
  cp "$WD/notify.txt" "$WD/notify.before" 2>/dev/null || : > "$WD/notify.before"
  local mt0; mt0=$(stat -f '%m' /etc/pf.conf)
  t0=$(date '+%Y-%m-%d %H:%M:%S')
  eval "$cmd" 2>&1 | quiet_pf | sed 's/^/          pfctl: /'
  sleep 3
  snapshot > "$WD/after.txt"
  local mt1; mt1=$(stat -f '%m' /etc/pf.conf)

  # S1 — unified log
  local logn
  logn=$(log show --start "$t0" --predicate "$LOGPRED" --style compact 2>/dev/null \
         | grep -c '^[0-9][0-9][0-9][0-9]-' || true)
  note "S1 unified log entries matching the pf predicate since the event: $logn$([ "$LOGQ_OK" -eq 0 ] && echo '   (THE QUERY IS INERT — this number means nothing)')"
  if [ "$logn" -gt 0 ]; then
    log show --start "$t0" --predicate "$LOGPRED" --style compact 2>/dev/null \
      | head -4 | sed 's/^/            /'
  fi

  # S2 — notify keys
  local nn
  nn=$(comm -13 <(sort "$WD/notify.before") <(sort "$WD/notify.txt") 2>/dev/null | grep -c . || true)
  note "S2 notify keys that fired: $nn$([ "$WATCHER_OK" -eq 0 ] && echo '   (WATCHER IS DEAD — this number means nothing)')"
  [ "$nn" -gt 0 ] && comm -13 <(sort "$WD/notify.before") <(sort "$WD/notify.txt") | sed 's/^/            /'

  # S3 — pfctl -s info / -s Anchors deltas
  local dn
  dn=$(diff "$WD/before.txt" "$WD/after.txt" | grep -c '^[<>]' || true)
  note "S3 changed lines in 'pfctl -s info' + '-s Anchors': $dn"
  if [ "$dn" -gt 0 ]; then
    diff "$WD/before.txt" "$WD/after.txt" | grep '^[<>]' | head -8 | sed 's/^/            /'
  fi

  # S4 — file mtime
  note "S4 /etc/pf.conf mtime changed: $([ "$mt0" != "$mt1" ] && echo YES || echo no)"
  note "    main-refs now = $(mainrefs)"
}

run_event "E1  pfctl -f /etc/pf.conf   (a benign reload)" "pfctl -f /etc/pf.conf"
run_event "E3  pfctl -d then pfctl -e  (disable/enable)"  "pfctl -d; sleep 1; pfctl -e"
run_event "E2  pfctl -F all            (THE FAULT)"       "pfctl -F all"

note ""
note "repairing immediately — E2 leaves the host unenforced"
restore_pf
note "main-refs after repair = $(mainrefs)"

# ---------------------------------------------------------------------------
say "S3b THE ONE FIELD THAT MATTERS — does anything in the GRANTED read move?"
note "'pfctl -s info' is already permitted by the shipped D132 grant, so a field that changes on a"
note "ruleset load would give a poll that costs no new privilege at all. Counters that always move"
note "(searches/inserts) are useless — they change whether or not anything happened."
# Two events, compared, because "it moved" is not the finding — WHICH events move it is. A signal
# that catches a flush but sleeps through a reload is a partial tripwire, and the dangerous case is
# a reload that drops the anchor line.
up() { pfctl -s info 2>/dev/null | head -1 | sed -E 's/.*Enabled for //; s/ +Debug.*//'; }
# Seconds, so the comparison is "did it go BACKWARDS" and not "does it look small". Run 1 tested
# whether the string contained "00:00:0", which is true of any recently-enabled pf — so a reload
# that moved 3s -> 5s was scored as a reset, and the discrimination this section exists for was lost.
upsec() { pfctl -s info 2>/dev/null | head -1 \
          | sed -E 's/.*Enabled for ([0-9]+) days ([0-9]+):([0-9]+):([0-9]+).*/\1 \2 \3 \4/' \
          | awk '{print $1*86400 + $2*3600 + $3*60 + $4}'; }
brs=$(up); br=$(upsec)
pfctl -f /etc/pf.conf >/dev/null 2>&1
sleep 2
ars=$(up); ar=$(upsec)
note "uptime before a plain reload : $brs  (${br}s)"
note "uptime after  a plain reload : $ars  (${ar}s)   a RELOAD is the dangerous quiet case"
bf=$(upsec)
pfctl -F all >/dev/null 2>&1
af=$(upsec)
note "uptime before -F all         : (${bf}s)"
note "uptime after  -F all         : (${af}s)"
restore_pf >/dev/null
RELOAD_MOVED=no; FLUSH_MOVED=no
[ "$ar" -lt "$br" ] && RELOAD_MOVED=yes
[ "$af" -lt "$bf" ] && FLUSH_MOVED=yes
note "(a reset means the counter went BACKWARDS; 2s of elapsed time makes it go forwards)"
note ""
note "counter reset by a reload: $RELOAD_MOVED    by -F all: $FLUSH_MOVED"
if [ "$FLUSH_MOVED" = yes ] && [ "$RELOAD_MOVED" = no ]; then
  ok "S3b: A PARTIAL SIGNAL EXISTS, AND IT IS FREE. \`pfctl -s info\` is already in the shipped"
  note "    grant, and its 'Enabled for' counter RESETS on \`-F all\` — so a poll that remembers the"
  note "    previous value detects a flush with no new privilege, by seeing the uptime go backwards."
  note "    But it does NOT move on a plain reload, and a reload that omits the anchor line is"
  note "    exactly the fault that defeats every other check. So this is a cheap tripwire for the"
  note "    loud fault, not a detector for the quiet one — it cannot replace a behavioural probe."
elif [ "$FLUSH_MOVED" = yes ]; then
  ok "S3b: the counter reset on BOTH events — a free poll that catches both; verify by re-running"
else
  ok "S3b: the counter survived the flush, so the granted read cannot see the fault at all"
  note "    Detection has to come from M3's detectors or from a widened grant."
fi
note "main-refs after repair = $(mainrefs)"

# ---------------------------------------------------------------------------
say "S4b THE FILE WATCH, REFUTED EXPLICITLY"
note "Watching /etc/pf.conf is the first idea anyone has. Above, every event left its mtime"
note "untouched — including the one that voided enforcement. The file is the SOURCE; the fault is in"
note "the LOADED ruleset, and nothing keeps the two in agreement. A file watcher is not a weak"
note "detector here, it is a blind one."
ok "S4b: recorded — /etc/pf.conf mtime is not a signal for any of the three events"

# ---------------------------------------------------------------------------
say "S5 THE COST OF POLLING, since that is what a negative forces"
note "20 invocations of the granted read, as the user would issue them:"
t0=$(python3 -c 'import time;print(time.time())')
for _ in $(seq 20); do pfctl -s info >/dev/null 2>&1; done
t1=$(python3 -c 'import time;print(time.time())')
note "pfctl -s info x20 (as root, no sudo): $(python3 -c "print('%.1f' % (($t1-$t0)*1000/20))") ms each"
t0=$(python3 -c 'import time;print(time.time())')
for _ in $(seq 20); do pfctl -s rules >/dev/null 2>&1; done
t1=$(python3 -c 'import time;print(time.time())')
note "pfctl -s rules x20 (the main-ruleset read a poll would need): $(python3 -c "print('%.1f' % (($t1-$t0)*1000/20))") ms each"
note ""
note "Add ~9.3 ms of sudo per call (pf-acquire-cost.txt, where 85% of a call was sudo and not pfctl)."
note "At a 5s interval that is a fraction of a percent of one core, and a 5s worst-case window of"
note "unenforced running. The interval is the design parameter; these are its two axes."

# ---------------------------------------------------------------------------
say "WHAT WAS NOT TRIED"
cat <<'EOF'
        - Every notify(3) key. There is no way to enumerate them, so only the list printed under S2a
          was watched. A key exists that nobody here knows the name of is NOT excluded by this run.
        - kqueue/EVFILT on /dev/pf. pf is driven by ioctl, not by file writes, so a vnode filter has
          nothing to fire on; not attempted.
        - A DTrace probe on the pf ioctl path. It would detect any writer, including the ones that
          emit nothing — but it needs SIP disabled, which makes it a diagnostic and not a shipping
          mechanism.
        - NSWorkspace / SCDynamicStore notifications from a real system service inserting an anchor.
          Only pfctl-driven events were triggered; no service was induced to change the ruleset.
        - Whether a poll and M3's behavioural canary can share one timer. That is a design question,
          and it belongs to synthesis.
EOF

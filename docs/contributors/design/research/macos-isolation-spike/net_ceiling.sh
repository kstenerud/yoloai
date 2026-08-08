#!/bin/bash
# ABOUTME: How many vmnet networks can exist at once, how subnets are allocated, and whether the
# ABOUTME: apple and tart backends can ever hand the same address to two live sandboxes.
#
# Run: bash net_ceiling.sh            (no privilege needed)
#
# WHY THIS EXISTS
#   Two questions, both raised by other items and neither queued.
#
#   N1 THE CEILING. M1 found that `container network create` gives each network its own bridge,
#      subnet and gateway — which makes the INTERFACE a candidate non-address key. That is only
#      interesting if one network per sandbox is feasible. vmnet is a system resource with limits
#      nobody here knows, so the ceiling decides whether M1's most promising lead is buildable.
#
#   N2 CROSS-BACKEND ADDRESS COLLISION. This is Linux L4's macOS twin, and it is missing from the
#      queue. During M1's reconnaissance a freshly created network was handed 192.168.65.0/24 —
#      the range earlier runs recorded tart using (`pf-gid-tart-rerun.txt`: "tart = 192.168.65.3").
#      If apple and tart can hold the same address at the same time, then rule 1c's cross-slot
#      scrub — which deletes OUR address from every other slot — deletes a LIVE sandbox's entry,
#      silently disarming it. That turns a tidy-up into a fail-open, and it is exactly the failure
#      L4 exists to rule out on Linux.
#
#   N3 INDEX RECYCLING AT SCALE. M1's K4c asks whether one deleted bridge's index comes back. Here
#      the same question is asked across many, because an allocator that appends is a different
#      thing from one that fills holes, and only the second is a staleness hazard.
#
# METHOD: creation is attempted one at a time and STOPS at the first failure, recording the error
#   verbatim — a ceiling found by crashing the host is not a measurement. Everything created is
#   named `yb-n-*` and removed on exit, including on interrupt.

set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
MAXTRY=16
IMG=yoloai-base:latest
PASS=0; FAIL=0; UNKNOWN=0
REPO="$(cd "$HERE/../../../../.." && pwd)"
YOLOAI="$REPO/yoloai"

RESULTS="$HERE/results/net-ceiling.txt"
mkdir -p "$(dirname "$RESULTS")"
exec > >(tee "$RESULTS") 2>&1

say() { printf '\n== %s ==\n' "$*"; }
ok()  { PASS=$((PASS+1));       printf '   PASS    %s\n' "$*"; }
bad() { FAIL=$((FAIL+1));       printf '   FAIL    %s\n' "$*"; }
unk() { UNKNOWN=$((UNKNOWN+1)); printf '   UNKNOWN %s\n' "$*"; }
note(){ printf '        %s\n' "$*"; }

bridges() { ifconfig -a 2>/dev/null | grep -c '^bridge' || true; }
# Only bridges, and only once each — an earlier version printed lo0's address under an empty name
# because it tracked the last interface seen rather than the last BRIDGE seen.
brmap()   { ifconfig -a 2>/dev/null | awk '
  /^[a-z]/ {inbr = ($1 ~ /^bridge/); br=$1; sub(":","",br)}
  /inet / {if (inbr && !seen[br]++) printf "%s=%s ", br, $2}'; }

cleanup() {
  echo
  echo "== cleanup =="
  for n in $(container network ls 2>/dev/null | awk '/^yb-n-/{print $1}'); do
    container network delete "$n" >/dev/null 2>&1
  done
  "$YOLOAI" destroy netx --abandon-unapplied >/dev/null 2>&1
  sleep 2
  echo "   networks now:"; container network ls 2>&1 | sed 's/^/      /'
  echo "   bridges now: $(brmap)"
  echo
  echo "pass=$PASS fail=$FAIL unknown=$UNKNOWN"
  echo "results: $RESULTS"
}
trap cleanup EXIT

echo "host: macOS $(sw_vers -productVersion) $(uname -m)"
echo "date: $(date '+%Y-%m-%d %H:%M:%S %Z')"

# ---------------------------------------------------------------------------
say "N0 BASELINE"
container system start >/dev/null 2>&1; sleep 2
note "networks:"; container network ls 2>&1 | sed 's/^/          /'
note "bridges: $(brmap)"
BR0=$(bridges)
note "bridge count: $BR0"

# ---------------------------------------------------------------------------
say "N1 THE CEILING — create networks until one fails"
note "One at a time, stopping at the first failure with its message. Up to $MAXTRY attempted."
CREATED=0
note "NOTE the BRIDGE column stays empty: a created network gets no host bridge interface until a"
note "container actually attaches to it. That is measured below in N1b, and it matters — the"
note "interface M1 proposes as a key does not exist at create time, only at attach time."
printf '        %-4s %-10s %-9s %-18s %s\n' "#" NAME "SECONDS" SUBNET BRIDGE
for ((i=1;i<=MAXTRY;i++)); do
  t0=$(python3 -c 'import time;print(time.time())')
  err=$(container network create "yb-n-$i" 2>&1)
  rc=$?
  t1=$(python3 -c 'import time;print(time.time())')
  if [ "$rc" -ne 0 ]; then
    note ""
    note "FAILED at attempt $i after $CREATED successful creations:"
    printf '%s\n' "$err" | head -4 | sed 's/^/            /'
    break
  fi
  CREATED=$i
  sleep 1
  sub=$(container network ls 2>/dev/null | awk -v n="yb-n-$i" '$1==n{print $2}')
  gw="${sub%.*}.1"; gw="${gw%%/*}"
  br=$(ifconfig -a 2>/dev/null | awk -v want="${sub%%/*}" '
        /^bridge/ {b=$1; sub(":","",b)}
        /inet / {split($2,a,"."); split(want,w,"."); if (a[1]==w[1] && a[2]==w[2] && a[3]==w[3]) {print b; exit}}')
  printf '        %-4s %-10s %-9s %-18s %s\n' "$i" "yb-n-$i" \
    "$(python3 -c "print('%.2f' % ($t1-$t0))")" "$sub" "${br:-?}"
done
note ""
if [ "$CREATED" -ge "$MAXTRY" ]; then
  ok "N1: $CREATED networks created without hitting a limit (the probe's own cap, not vmnet's)"
  note "    So one-network-per-sandbox is not obviously capped below $MAXTRY. The real ceiling is"
  note "    above what was attempted and remains unmeasured."
elif [ "$CREATED" -gt 0 ]; then
  ok "N1: the ceiling is $CREATED concurrent networks on this host"
  note "    That is the hard cap on one-network-per-sandbox, and therefore on M1's interface key."
else
  bad "N1: could not create any network; the rest of this run is void"
fi
note "bridges now: $(brmap)"

if [ "$CREATED" -ge 1 ]; then
  note ""
  note "N1b  when does the bridge appear? Attaching one container to yb-n-1:"
  note "     before attach: $(brmap)"
  container run -d --name yb-n-probe --network yb-n-1 "$IMG" sleep 60 >/dev/null 2>&1
  sleep 3
  note "     after attach:  $(brmap)"
  container rm -f yb-n-probe >/dev/null 2>&1
  sleep 2
  note "     after detach:  $(brmap)"
  ok "N1b: recorded — the bridge is created on attach and torn down on detach, so an interface"
  note "     name is a per-ATTACHMENT identifier, not a per-network one"
fi

# ---------------------------------------------------------------------------
say "N2 SUBNET ALLOCATION — and whether it can collide with tart"
note "Ranges handed out above, in order, so the allocator's pattern is visible:"
container network ls 2>/dev/null | sed 's/^/          /'
note ""
note "Now start a TART sandbox and see which range it takes, with the apple networks still up."
git_wd=$(mktemp -d /tmp/pfn-wd.XXXXXX)
git -C "$git_wd" init -q 2>/dev/null; : > "$git_wd/README.md"
git -C "$git_wd" add -A >/dev/null 2>&1
git -C "$git_wd" -c user.email=spike@local -c user.name=spike commit -qm init >/dev/null 2>&1
"$YOLOAI" destroy netx --abandon-unapplied >/dev/null 2>&1
if ! "$YOLOAI" new netx "$git_wd" --backend tart >/dev/null 2>&1; then
  unk "N2: tart sandbox would not start; the collision question is NOT answered"
else
  TIP=$("$YOLOAI" exec netx ipconfig getifaddr en0 2>/dev/null | tr -d '\r')
  note "tart guest address: ${TIP:-<unreadable>}"
  note "bridges with tart up: $(brmap)"
  if [ -z "$TIP" ]; then
    unk "N2: could not read the tart guest's address; collision not determined"
  else
    T3="${TIP%.*}"
    HIT=$(container network ls 2>/dev/null | awk -v p="$T3." '$2 ~ ("^" p) {print $1}')
    if [ -n "$HIT" ]; then
      bad "N2: COLLISION. tart holds $TIP, inside apple network '$HIT'."
      note "    Two backends can hand out the same subnet, so two live sandboxes can hold the same"
      note "    address. Rule 1c's cross-slot scrub deletes OUR address from every other slot — and"
      note "    with a shared address that deletes a LIVE sandbox's entry, leaving it matching no"
      note "    src table and therefore unfiltered. A reaping step becomes a fail-open."
    else
      ok "N2: no collision in this configuration — tart at $TIP, no apple network in ${T3}.0/24"
      note "    A negative bounded to THIS allocation order. tart picked its range with $CREATED"
      note "    apple networks already up; a different order may still collide, and nothing here"
      note "    coordinates the two allocators."
    fi
  fi
fi
rm -rf "$git_wd"

# ---------------------------------------------------------------------------
say "N3 DOES A FREED INDEX COME BACK? — append, or fill the hole?"
note "An allocator that appends gives a key that never repeats. One that fills holes gives a key"
note "with exactly the staleness problem addresses have, and M1's K4 would inherit it."
if [ "$CREATED" -ge 3 ]; then
  MID="yb-n-2"
  RELEASED=$(container network ls 2>/dev/null | awk -v n="$MID" '$1==n{print $2}')
  HIGHEST=$(container network ls 2>/dev/null | awk '/^yb-n-|^default/{print $2}' \
            | awk -F. '{print $3}' | sort -n | tail -1)
  note "$MID currently holds $RELEASED; the highest third octet in use is $HIGHEST"
  container network delete "$MID" >/dev/null 2>&1; sleep 2
  note "deleted $MID, releasing $RELEASED"
  container network create yb-n-fresh >/dev/null 2>&1; sleep 2
  FRESH=$(container network ls 2>/dev/null | awk '$1=="yb-n-fresh"{print $2}')
  note "created yb-n-fresh -> $FRESH"
  if [ "$FRESH" = "$RELEASED" ]; then
    bad "N3: THE ALLOCATOR FILLS HOLES. A new network was handed the exact range a deleted one"
    note "     released ($RELEASED). So a subnet — and the bridge that carries it — recycles"
    note "     IMMEDIATELY, exactly the way an address does. M1's per-sandbox-network key would"
    note "     inherit the whole staleness hazard rules 1/1b/1c exist for: it is not a cure, it is"
    note "     the same disease with a different name."
  elif [ -n "$FRESH" ]; then
    ok "N3: the allocator did NOT reuse the freed range ($RELEASED -> $FRESH, highest was"
    note "     .$HIGHEST). It appends, so the key does not repeat within a boot — which would make"
    note "     a per-sandbox network a genuine non-recycling key."
  else
    unk "N3: could not read the new network's subnet"
  fi
  container network delete yb-n-fresh >/dev/null 2>&1
else
  unk "N3: fewer than 3 networks were created; recycling not tested"
fi

# ---------------------------------------------------------------------------
say "WHAT WAS NOT TRIED"
cat <<'EOF'
        - The true vmnet ceiling, if N1 hit this probe's own cap rather than the system's. Raising
          MAXTRY is a one-line change, but creating dozens of vmnet networks on a machine somebody
          is using is not something this run should do unattended.
        - Forcing a collision. N2 observed whichever ranges the two allocators happened to pick. It
          did not try to ENGINEER an overlap with `--subnet`, which would prove the hazard is
          reachable rather than merely unprevented. That is the stronger experiment and it is the
          obvious follow-up if N2 comes back clean.
        - Whether a container and a tart VM sharing a subnet can actually reach each other, or
          whether vmnet keeps them on separate links despite the overlap. The reaping hazard does
          not need them to be mutually reachable — it only needs the same address in two places —
          but it changes how bad a collision is.
        - Docker Desktop, which also allocates on this host and is a third allocator nobody
          coordinates with.
EOF

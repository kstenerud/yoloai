#!/bin/bash
# ABOUTME: Measures what the slot-acquisition sequence costs on the sandbox start path, and
# ABOUTME: whether one whole-anchor table dump could collapse its 31 blind deletes to ~zero.
#
# Run: sudo bash pf_acquire_cost.sh
#
# WHY THIS EXISTS
#   enforcement-state-reaping.md § 1c specifies the acquisition sequence and then says, in as many
#   words, that nobody has measured it: 2 flushes + 31 cross-slot deletes + 2 adds, ~35 `sudo pfctl`
#   invocations per sandbox start, because sudoers matches one table per invocation (D5 permits
#   many addresses in one call, never many tables).
#
#   That number is now the direct multiplier on sandbox start latency, and it decides a
#   user-visible one: the pool size caps concurrently-isolated sandboxes (32 today, 64 rules). The
#   cost is O(pool), not O(sandboxes), so if 35 calls are expensive the answer is a smaller pool,
#   never a skipped scrub — the scrub is what closes D3.
#
#   C1 ONE CALL. What does a single NOPASSWD `sudo pfctl -T` invocation cost, warm?
#   C2 ATTRIBUTION. The same pfctl work as root, no sudo. If sudo dominates, the call count is the
#      thing to reduce; if pfctl dominates, no batching scheme helps. Nothing else in this file
#      can be interpreted without this split.
#   C3 THE SEQUENCE. The real 35-call acquisition, end to end. And C3b: the same sequence with the
#      31 deletes removed (4 calls), which is what a working collapse would cost.
#   C4 POLICY SIZE. Does sudo's cost scale with the size of the sudoers policy it must evaluate?
#      A developer Mac carries a handful of rules; a managed one can carry hundreds.
#   C5 AGAINST TOTAL START TIME. A bare millisecond figure decides nothing. The number that
#      decides something is the fraction of `yoloai new`.
#   C6 THE COLLAPSE HYPOTHESIS. The 31 deletes exist because we cannot know which slot holds a
#      stale copy of our address. If pfctl can dump every table's CONTENTS in the anchor in one
#      call, the scrub becomes one read plus (usually zero) targeted deletes and ~35 falls to ~4.
#      Tested positively — marker addresses are planted in two slots and each candidate form's
#      output is searched for them, so "it printed something" cannot pass for "it printed the
#      contents".
#
#   C6 DOES NOT WIDEN THE GRANT. The shipped grant permits `-T show` per table; a whole-anchor
#   dump is a different verb and is expected to be refused. This script reports whether the
#   capability exists and what grant surface it would need. Extending that grant is a
#   security-boundary change requiring D132's permit/refuse matrix to be re-run, which is not this
#   script's business.
#
# SAFETY: only com.apple/yoloai_b, never com.apple/yoloai and never the main ruleset. The main
# ruleset's rule count is asserted unchanged at exit. All sudoers, /etc/yoloai and sandboxes this
# script creates are removed at exit.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"
UHOME=$(dscl . -read "/Users/$U" NFSHomeDirectory 2>/dev/null | awk '{print $2}')
[ -d "$UHOME" ] || { echo "cannot resolve home for $U"; exit 2; }

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/../../../../.." && pwd)"
YOLOAI="$REPO/yoloai"
[ -x "$YOLOAI" ] || { echo "no yoloai binary at $YOLOAI"; exit 2; }

ANCHOR="com.apple/yoloai_b"
SLOTS=32
SLOT=7                      # the claimed slot; arbitrary, deliberately not 0
CONFDIR=/etc/yoloai; CONF="$CONFDIR/pf-b.conf"
SUDOERS=/etc/sudoers.d/yoloai-cost-probe
FILLER=/etc/sudoers.d/00-yoloai-cost-filler
WD=$(mktemp -d /tmp/pfcost-wd.XXXXXX)
ADDR=192.168.64.200          # a plausible guest address; no guest need hold it
# 20 destinations, the shape of a resolved allowlist rather than a single IP
DEST_N=20
DESTS=$(python3 -c "print(','.join('198.51.100.%d'%i for i in range(1,$DEST_N+1)))")
PASS=0; FAIL=0; UNKNOWN=0

RESULTS="$HERE/results/pf-acquire-cost.txt"
mkdir -p "$(dirname "$RESULTS")"
exec > >(tee "$RESULTS") 2>&1

say() { printf '\n== %s ==\n' "$*"; }
ok()  { PASS=$((PASS+1));    printf '   PASS    %s\n' "$*"; }
bad() { FAIL=$((FAIL+1));    printf '   FAIL    %s\n' "$*"; }
unk() { UNKNOWN=$((UNKNOWN+1)); printf '   UNKNOWN %s\n' "$*"; }
nrules() { pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -c . || true; }
quiet_pf() { grep -viE 'use of -f option|main ruleset added|/etc/pf.conf for further|ALTQ|^$'; }
asuser() { sudo -u "$U" -H "$@"; }

MAIN_BEFORE=$(pfctl -s rules 2>/dev/null | grep -c . || true)
SANDBOXES=""
cleanup() {
  echo
  echo "== cleanup =="
  for s in $SANDBOXES; do
    asuser "$YOLOAI" destroy "$s" --abandon-unapplied >/dev/null 2>&1 \
      && echo "   destroyed sandbox $s" || echo "   NOTE sandbox $s not destroyed — remove by hand"
  done
  pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
  rm -f "$SUDOERS" "$FILLER" /tmp/pfcost.*; rm -rf "$CONFDIR" "$WD"
  sudo -u "$U" sudo -K >/dev/null 2>&1
  echo "   anchor rules=$(nrules)"
  echo "   main ruleset: before=$MAIN_BEFORE after=$(pfctl -s rules 2>/dev/null | grep -c . || true)"
  echo "   sudoers probe present: $([ -e "$SUDOERS" ] && echo YES-BAD || echo no)"
  echo "   sudoers filler present: $([ -e "$FILLER" ] && echo YES-BAD || echo no)"
  echo
  echo "pass=$PASS fail=$FAIL unknown=$UNKNOWN"
}
trap cleanup EXIT

# --- timing helpers ---------------------------------------------------------
# Run once per batch, as the user, so the outer `sudo -u` drop is NOT inside the timed loop.
cat > /tmp/pfcost.timer.py <<'PY'
import statistics, subprocess, sys, time
label, n, kill_ts, cmd = sys.argv[1], int(sys.argv[2]), sys.argv[3] == "kill", sys.argv[4:]
ts = []
for _ in range(n):
    if kill_ts:
        subprocess.run(["sudo", "-K"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    t0 = time.perf_counter()
    subprocess.run(cmd, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    ts.append((time.perf_counter() - t0) * 1000)
s = sorted(ts)
print("        %-34s first=%6.1f  min=%5.1f  med=%5.1f  p90=%5.1f  max=%6.1f  (n=%d, ms)"
      % (label, ts[0], s[0], statistics.median(s), s[min(len(s) - 1, int(len(s) * 0.9))], s[-1], n))
print("MED %s %.3f" % (label.replace(" ", "_"), statistics.median(s)))
PY

cat > /tmp/pfcost.seq.py <<'PY'
import statistics, subprocess, sys, time
anchor, slot, slots, addr, dests, n, scrub = sys.argv[1], int(sys.argv[2]), int(sys.argv[3]), \
    sys.argv[4], sys.argv[5].split(","), int(sys.argv[6]), sys.argv[7] == "scrub"
DN = subprocess.DEVNULL
def pf(*a):
    subprocess.run(["sudo", "-n", "/sbin/pfctl", "-a", anchor] + list(a), stdout=DN, stderr=DN)
tot, calls = [], 0
for _ in range(n):
    c = 0
    t0 = time.perf_counter()
    pf("-t", "yb_src_%d" % slot, "-T", "flush"); c += 1          # 1c step 1
    pf("-t", "yb_dst_%d" % slot, "-T", "flush"); c += 1
    if scrub:                                                    # 1c step 2: cross-slot scrub
        for i in range(slots):
            if i != slot:
                pf("-t", "yb_src_%d" % i, "-T", "delete", addr); c += 1
    pf("-t", "yb_src_%d" % slot, "-T", "add", addr); c += 1      # 1c step 3
    pf("-t", "yb_dst_%d" % slot, "-T", "add", *dests); c += 1    # 1c step 4
    tot.append((time.perf_counter() - t0) * 1000)
    calls = c
s = sorted(tot)
print("        %d calls/run: first=%.0f  min=%.0f  med=%.0f  max=%.0f  (n=%d, ms)"
      % (calls, tot[0], s[0], statistics.median(s), s[-1], n))
print("MED %s %.3f" % ("sequence_scrub" if scrub else "sequence_noscrub", statistics.median(s)))
PY

med() { grep "^MED $1 " /tmp/pfcost.meds 2>/dev/null | tail -1 | awk '{print $3}'; }
: > /tmp/pfcost.meds
run_timer() { sudo -u "$U" -H python3 /tmp/pfcost.timer.py "$@" | tee -a /tmp/pfcost.meds | grep -v '^MED '; }
run_seq()   { sudo -u "$U" -H python3 /tmp/pfcost.seq.py   "$@" | tee -a /tmp/pfcost.meds | grep -v '^MED '; }

echo "host: macOS $(sw_vers -productVersion) $(uname -m) | $(sysctl -n hw.model) | $(sysctl -n machdep.cpu.brand_string 2>/dev/null || echo '?')"
echo "date: $(date '+%Y-%m-%d %H:%M:%S %Z') | user=$U | anchor=$ANCHOR | slots=$SLOTS | claimed slot=$SLOT"
echo "main ruleset rules before: $MAIN_BEFORE"

# ---------------------------------------------------------------------------
say "C0 SETUP — load the pool and install the D132 grant"
{ for ((i=0;i<SLOTS;i++)); do
    echo "table <yb_src_$i> persist"
    echo "table <yb_dst_$i> persist"
    echo "pass  in quick from <yb_src_$i> to <yb_dst_$i>"
    echo "block drop in quick from <yb_src_$i> to any"
  done; } > /tmp/pfcost.rules
pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
pfctl -a "$ANCHOR" -f /tmp/pfcost.rules 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
n=$(nrules); want=$((SLOTS*2))
echo "        loaded $n filter rule(s), expected $want"
[ "${n:-0}" -eq "$want" ] || { bad "pool did not load; ABORTING"; exit 1; }

install -d -m 0755 -o root -g wheel "$CONFDIR"
install -m 0644 -o root -g wheel /tmp/pfcost.rules "$CONF"
LEAF='yb_(src|dst)_([0-9]|[12][0-9]|3[01])'
cat > /tmp/pfcost.sudoers <<EOF
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai_b -t $LEAF -T (add|delete|flush|show)( [0-9a-fA-F.:/]+)*\$
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai_b -f /etc/yoloai/pf-b\\.conf\$
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai_b -s rules\$
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-s info\$
EOF
visudo -c -f /tmp/pfcost.sudoers >/dev/null 2>&1 || { bad "policy fails visudo -c; ABORTING"; exit 1; }
install -m 0440 -o root -g wheel /tmp/pfcost.sudoers "$SUDOERS"
sudo -u "$U" sudo -K >/dev/null 2>&1
if sudo -u "$U" -H sudo -n /sbin/pfctl -a "$ANCHOR" -t yb_src_0 -T show >/dev/null 2>&1; then
  ok "$SLOTS-slot pool loaded and the NOPASSWD grant answers unattended"
else
  bad "grant does not work; ABORTING"; exit 1
fi

# ---------------------------------------------------------------------------
say "C1 ONE CALL — cost of a single NOPASSWD sudo pfctl -T invocation"
echo "        'first' is the cold call in each batch: cold page cache, cold sudoers parse, no"
echo "        sudo timestamp. Everything after it is warm, which is what a start path sees."
run_timer "show (warm)"            40 nokill sudo -n /sbin/pfctl -a "$ANCHOR" -t yb_src_0 -T show
run_timer "delete non-member"      40 nokill sudo -n /sbin/pfctl -a "$ANCHOR" -t yb_src_0 -T delete "$ADDR"
run_timer "add 1 address"          40 nokill sudo -n /sbin/pfctl -a "$ANCHOR" -t yb_dst_0 -T add 198.51.100.99
run_timer "flush"                  40 nokill sudo -n /sbin/pfctl -a "$ANCHOR" -t yb_dst_0 -T flush
run_timer "delete, timestamp cleared" 15 kill sudo -n /sbin/pfctl -a "$ANCHOR" -t yb_src_0 -T delete "$ADDR"
d_warm=$(med "delete_non-member"); d_cold=$(med "delete,_timestamp_cleared")
echo "        median warm delete=${d_warm}ms  with timestamp cleared each call=${d_cold}ms"
if [ -n "$d_warm" ]; then
  ok "single-invocation cost measured (the delete verb is the one repeated 31x)"
else
  bad "no timing produced"
fi

# ---------------------------------------------------------------------------
say "C2 ATTRIBUTION — the same pfctl work as root, without sudo"
echo "        If sudo dominates, reducing the CALL COUNT is the only lever. If pfctl dominates,"
echo "        no batching scheme helps and the pool size is the only lever."
python3 /tmp/pfcost.timer.py "delete, root direct" 40 nokill /sbin/pfctl -a "$ANCHOR" -t yb_src_0 -T delete "$ADDR" \
  | tee -a /tmp/pfcost.meds | grep -v '^MED '
d_root=$(med "delete,_root_direct")
if [ -n "$d_root" ] && [ -n "$d_warm" ]; then
  python3 - "$d_warm" "$d_root" <<'PY'
import sys
w, r = float(sys.argv[1]), float(sys.argv[2])
print("        sudo overhead = %.1fms of %.1fms (%.0f%% of each invocation); pfctl itself = %.1fms"
      % (w - r, w, 100 * (w - r) / w, r))
PY
  ok "cost attributed between sudo and pfctl"
else
  unk "could not attribute"
fi

# ---------------------------------------------------------------------------
say "C3 THE SEQUENCE — full acquisition per 1c, and the same without the cross-slot scrub"
echo "        scrub:   flush src+dst, delete our address from the other $((SLOTS-1)) src tables, add"
echo "                 our address, add $DEST_N destinations in one call (D5)"
run_seq "$ANCHOR" "$SLOT" "$SLOTS" "$ADDR" "$DESTS" 8 scrub
echo "        noscrub: what a working whole-anchor dump would collapse this to"
run_seq "$ANCHOR" "$SLOT" "$SLOTS" "$ADDR" "$DESTS" 8 noscrub
s_scrub=$(med sequence_scrub); s_no=$(med sequence_noscrub)
if [ -n "$s_scrub" ] && [ -n "$s_no" ]; then
  python3 - "$s_scrub" "$s_no" <<'PY'
import sys
a, b = float(sys.argv[1]), float(sys.argv[2])
print("        scrub costs %.0fms of the %.0fms sequence; collapsing it would leave %.0fms (%.1fx faster)"
      % (a - b, a, b, a / b if b else 0))
PY
  ok "acquisition sequence measured at a ${SLOTS}-slot pool"
else
  bad "sequence timing missing"
fi

echo
echo "        per-slot marginal cost — what pool size buys, since the scrub is O(pool):"
for p in 8 16 32; do
  run_seq "$ANCHOR" 3 "$p" "$ADDR" "$DESTS" 4 scrub | sed "s/^        /        pool=$p: /"
done

# ---------------------------------------------------------------------------
say "C4 POLICY SIZE — does sudo's cost scale with the sudoers policy it evaluates?"
for extra in 100 500; do
  python3 - "$U" "$extra" > /tmp/pfcost.filler <<'PY'
import sys
u, n = sys.argv[1], int(sys.argv[2])
for i in range(n):
    print("%s ALL=(root) NOPASSWD: /usr/bin/false filler%d" % (u, i))
PY
  if visudo -c -f /tmp/pfcost.filler >/dev/null 2>&1; then
    install -m 0440 -o root -g wheel /tmp/pfcost.filler "$FILLER"
    run_timer "delete, +$extra sudoers rules" 25 nokill sudo -n /sbin/pfctl -a "$ANCHOR" -t yb_src_0 -T delete "$ADDR"
  else
    unk "filler policy ($extra rules) fails visudo -c"
  fi
done
rm -f "$FILLER"
f100=$(med "delete,_+100_sudoers_rules"); f500=$(med "delete,_+500_sudoers_rules")
echo "        baseline=${d_warm}ms  +100 rules=${f100}ms  +500 rules=${f500}ms"
if [ -n "$f500" ] && [ -n "$d_warm" ]; then
  ok "policy-size scaling measured (read the three medians above; the claim is the numbers)"
else
  unk "policy-size scaling not measured"
fi

# ---------------------------------------------------------------------------
say "C5 AGAINST TOTAL START TIME — the sequence as a fraction of 'yoloai new'"
echo "        workdir is an empty throwaway git repo, so this is backend start cost and excludes"
echo "        any real project's copy time. n is small; these are latencies, not a distribution."
git -C "$WD" init -q 2>/dev/null
: > "$WD/README.md"
git -C "$WD" add -A >/dev/null 2>&1
git -C "$WD" -c user.email=spike@local -c user.name=spike commit -qm init >/dev/null 2>&1
chown -R "$U" "$WD"
for be in apple tart; do
  for i in 1 2; do
    [ "$be" = tart ] && [ "$i" = 2 ] && continue     # tart is slow; n=1 by design
    name="cost-$be-$i"
    t=$(sudo -u "$U" -H python3 - "$YOLOAI" "$WD" "$name" "$be" <<'PY'
import subprocess, sys, time
yoloai, wd, name, be = sys.argv[1:5]
t0 = time.perf_counter()
p = subprocess.run([yoloai, "new", name, wd, "--backend", be],
                   stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
d = time.perf_counter() - t0
print("%.0f %d %s" % (d * 1000, p.returncode, p.stderr.decode()[:120].replace("\n", " ")))
PY
)
    ms=$(echo "$t" | awk '{print $1}'); rc=$(echo "$t" | awk '{print $2}')
    echo "        yoloai new --backend $be (#$i): ${ms}ms rc=$rc $(echo "$t" | cut -d' ' -f3-)"
    [ "$rc" = 0 ] && SANDBOXES="$SANDBOXES $name"
    [ "$rc" = 0 ] && [ -z "${START_MS:-}" ] && [ "$be" = apple ] && START_MS=$ms
  done
done
if [ -n "${START_MS:-}" ] && [ -n "$s_scrub" ]; then
  python3 - "$s_scrub" "$START_MS" <<'PY'
import sys
seq, start = float(sys.argv[1]), float(sys.argv[2])
print("        acquisition %.0fms against apple start %.0fms = %.1f%% of sandbox start"
      % (seq, start, 100 * seq / start))
PY
  ok "acquisition cost landed as a fraction of real start time"
else
  unk "no start-time baseline (sandbox creation failed); the sequence figure stands alone"
fi

# ---------------------------------------------------------------------------
say "C6 COLLAPSE HYPOTHESIS — can one call dump every table's CONTENTS in the anchor?"
echo "        Marker addresses are planted in two slots first, so a form only passes if its output"
echo "        actually contains them. A form that lists table NAMES is not a collapse: knowing the"
echo "        names is free, the 31 calls exist to learn the contents."
M1=198.51.100.77; M2=203.0.113.88
pfctl -a "$ANCHOR" -t yb_src_5  -T add "$M1" >/dev/null 2>&1
pfctl -a "$ANCHOR" -t yb_src_20 -T add "$M2" >/dev/null 2>&1
echo "        planted $M1 in yb_src_5, $M2 in yb_src_20"
DUMP_FORM=""
probe_dump() {
  local label="$1"; shift
  local out rc
  out=$(pfctl -a "$ANCHOR" "$@" 2>&1); rc=$?
  local hits=0
  printf '%s' "$out" | grep -q "$M1" && hits=$((hits+1))
  printf '%s' "$out" | grep -q "$M2" && hits=$((hits+1))
  local names=no
  printf '%s' "$out" | grep -q 'yb_src_20' && names=yes
  printf '        %-28s rc=%d lines=%-5s names=%-3s markers=%d/2  %s\n' \
    "$label" "$rc" "$(printf '%s' "$out" | grep -c . )" "$names" "$hits" \
    "$([ "$hits" -eq 2 ] && echo '<-- CONTENTS' || echo '')"
  if [ "$hits" -eq 2 ] && [ -z "$DUMP_FORM" ]; then DUMP_FORM="$*"; fi
}
probe_dump "-s Tables"          -s Tables
probe_dump "-s Tables -v"       -s Tables -v
probe_dump "-s Tables -vv"      -s Tables -vv
probe_dump "-sT -v"             -sT -v
probe_dump "-sT -vv"            -sT -vv
probe_dump "-vvsT"              -vvsT
probe_dump "-T show (no -t)"    -T show
probe_dump "-s all"             -s all
probe_dump "-s all -v"          -s all -v
probe_dump "-t yb_src_5 -T show -v" -t yb_src_5 -T show -v

if [ -n "$DUMP_FORM" ]; then
  ok "a single call CAN dump table contents anchor-wide: pfctl -a $ANCHOR $DUMP_FORM"
  echo
  echo "        cost of that one call, as root:"
  # shellcheck disable=SC2086
  python3 /tmp/pfcost.timer.py "whole-anchor dump" 20 nokill /sbin/pfctl -a "$ANCHOR" $DUMP_FORM \
    | tee -a /tmp/pfcost.meds | grep -v '^MED '
  echo
  echo "        is it reachable under the SHIPPED grant (which permits -T show per table only)?"
  out=$(sudo -u "$U" -H sudo -k -n /sbin/pfctl -a "$ANCHOR" $DUMP_FORM 2>&1); rc=$?
  if [ "$rc" -eq 0 ]; then
    bad "the existing grant already permits an anchor-wide dump — unexpected; re-read the policy"
  elif printf '%s' "$out" | grep -qiE "not allowed to execute|may not run|a password is required"; then
    ok "refused by policy under the shipped grant, as expected"
    echo "        so the collapse costs one new NOPASSWD line permitting exactly the argv:"
    echo "            /sbin/pfctl -a com.apple/yoloai $DUMP_FORM"
    echo "        GRANT SURFACE: it adds a READ. It cannot modify membership, load a ruleset or"
    echo "        touch pf's enable state, so it does not reach any of the nine bypasses D132"
    echo "        refused. It does widen what a compromised caller can OBSERVE: every sandbox's"
    echo "        address and every sandbox's resolved allowlist, in one call, where today that"
    echo "        costs 64. On a single-user Mac the holder can read all of it anyway with the"
    echo "        per-table grant. NOT ADDED HERE — D132's permit/refuse matrix must be re-run"
    echo "        against the extended policy before anyone relies on this."
  else
    unk "dump refused for a non-policy reason: $(printf '%s' "$out" | head -1)"
  fi
else
  ok "no pfctl form dumps table contents anchor-wide — the 31 blind deletes cannot be collapsed"
  echo "        this way, and pool size is the only lever on the scrub. (Forms tried are listed"
  echo "        above; a form nobody thought of is not excluded by this run.)"
fi

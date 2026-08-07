#!/bin/bash
# ABOUTME: Follow-up to pf_acquire_cost.sh C6: whether one call reporting each table's ADDRESS
# ABOUTME: COUNT can collapse the 31-delete cross-slot scrub to one delete per occupied slot.
#
# Run: sudo bash pf_scrub_collapse.sh
#
# WHY THIS EXISTS
#   C6 asked whether pfctl can dump every table's CONTENTS in the anchor in one call. It cannot —
#   ten forms tried, zero markers found. That closes the hypothesis as posed and the file says so.
#
#   But C6's own output carried a loose end. `-s Tables -vv` returned 706 lines for 64 tables,
#   which is far too much for a name list: it is pf's per-table statistics block. If that block
#   reports each table's address COUNT, a weaker read than the contents dump C6 sought is enough:
#
#     a table holding zero addresses cannot hold ours, so it needs no delete.
#
#   That is not a heuristic. It is the same blind delete rule 1 specifies, with the provably-empty
#   slots skipped. Cost becomes 1 dump + 1 delete per OCCUPIED slot — O(running sandboxes) rather
#   than O(pool) — which is what § 1c wanted and could not have.
#
#   K1 Does the verbose table listing report a per-table address count, and does it distinguish
#      empty from occupied? Tested against a KNOWN plant, both directions: a slot said to hold 0
#      must really hold 0, and one said to hold 2 must really hold 2. A parser that reports every
#      table as occupied would be safe but useless, and would look identical in a one-sided test.
#   K2 What does the dump cost, and what does the collapsed sequence cost at k = 0, 1, 2, 4, 8
#      occupied slots, against the 329ms measured for the blind form?
#   K3 What grant surface does it need, and what does that surface reveal that today's does not?
#
#   TWO INVALIDATED RUNS ARE KEPT, and they failed the same way for different reasons — both
#   produced a confident "7x faster" from a sequence doing no work at all.
#
#   Run 1 (results/pf-scrub-collapse-run1-invalidated.txt): the K1 parser anchored the table-name
#   pattern to end-of-line, but pfctl emits a third column naming the owning anchor, so nothing
#   matched and every table read as empty. K1 failed correctly; K2 timed a "collapsed" sequence
#   that issued ZERO deletes and called it a win at every k.
#
#   Run 2 (results/pf-scrub-collapse-run2-invalidated.txt): K1 fixed and passing, K2 still issuing
#   zero deletes — because the sequence invokes the dump through `sudo -n` under the SHIPPED
#   grant, which K3 in that same run proves refuses it. The probe was measuring the cost of a
#   capability it had simultaneously demonstrated it did not have.
#
#   The tell both times was the shape: a cost that does not move between k=0 and k=8 is not doing
#   per-slot work. Two guards now exist because prose about care did not prevent either — K2
#   counts the deletes it issues and fails when that count is not k, and the extended grant is
#   asserted to actually answer before any timing is taken.
#
#   MEASURING UNDER AN EXTENDED GRANT IS NOT PROPOSING ONE. K2 installs the extra read line
#   because the cost of a capability cannot be measured without it. K3 states the surface, and
#   nothing here ships: the line lands in the design only if D132's permit/refuse matrix is
#   re-run against it.
#
#   CORRECTNESS ARGUMENT, stated so the measurement can be read against it. Between the dump and
#   the deletes, can a slot go from empty to holding OUR address? Only we ever write our own
#   address, a live sandbox holds it so no concurrent start can be handed it, and slot allocation
#   is under the cross-sandbox lock rule 3 already requires. So an empty slot stays empty of our
#   address for the length of the sequence. The skip is sound under that lock and only under it.
#
# SAFETY: only com.apple/yoloai_b. No sandboxes, no egress, no main-ruleset writes.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"

HERE="$(cd "$(dirname "$0")" && pwd)"
ANCHOR="com.apple/yoloai_b"
SLOTS=32
SLOT=7
CONFDIR=/etc/yoloai; CONF="$CONFDIR/pf-b.conf"
SUDOERS=/etc/sudoers.d/yoloai-collapse-probe
SUDOERS_EXT=/etc/sudoers.d/yoloai-collapse-extended   # measurement-only; K3 removes and re-checks
ADDR=192.168.64.200
DEST_N=20
DESTS=$(python3 -c "print(','.join('198.51.100.%d'%i for i in range(1,$DEST_N+1)))")
PASS=0; FAIL=0; UNKNOWN=0

RESULTS="$HERE/results/pf-scrub-collapse.txt"
mkdir -p "$(dirname "$RESULTS")"
exec > >(tee "$RESULTS") 2>&1

say() { printf '\n== %s ==\n' "$*"; }
ok()  { PASS=$((PASS+1));       printf '   PASS    %s\n' "$*"; }
bad() { FAIL=$((FAIL+1));       printf '   FAIL    %s\n' "$*"; }
unk() { UNKNOWN=$((UNKNOWN+1)); printf '   UNKNOWN %s\n' "$*"; }
nrules() { pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -c . || true; }
quiet_pf() { grep -viE 'use of -f option|main ruleset added|/etc/pf.conf for further|ALTQ|^$'; }

MAIN_BEFORE=$(pfctl -s rules 2>/dev/null | grep -c . || true)
cleanup() {
  echo
  echo "== cleanup =="
  pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
  rm -f "$SUDOERS" "$SUDOERS_EXT" /tmp/pfcol.*; rm -rf "$CONFDIR"
  sudo -u "$U" sudo -K >/dev/null 2>&1
  echo "   anchor rules=$(nrules)"
  echo "   main ruleset: before=$MAIN_BEFORE after=$(pfctl -s rules 2>/dev/null | grep -c . || true)"
  echo "   sudoers probe present:    $([ -e "$SUDOERS" ] && echo YES-BAD || echo no)"
  echo "   sudoers EXTENDED present: $([ -e "$SUDOERS_EXT" ] && echo YES-BAD || echo no)"
  echo
  echo "pass=$PASS fail=$FAIL unknown=$UNKNOWN"
}
trap cleanup EXIT

# Parse `pfctl -a X -s Tables -vv` into "table<TAB>count" lines. Written as a separate script
# because the collapsed sequence has to run it for real, not approximate it.
cat > /tmp/pfcol.parse.py <<'PY'
import re, subprocess, sys
anchor = sys.argv[1]
out = subprocess.run(["/sbin/pfctl", "-a", anchor, "-s", "Tables", "-vv"],
                     capture_output=True, text=True).stdout
cur, counts = None, {}
for line in out.splitlines():
    # The name line carries a THIRD column naming the owning anchor. Run 1 anchored this pattern
    # to end-of-line, matched nothing, and reported every table as empty.
    m = re.match(r'^\S*\s+(yb_(?:src|dst)_\d+)\b', line)
    if m:
        cur = m.group(1); continue
    m = re.match(r'^\s+Addresses:\s+(\d+)', line)
    if m and cur:
        counts[cur] = int(m.group(1)); cur = None
for k in sorted(counts, key=lambda s: (s.rsplit("_", 2)[1], int(s.rsplit("_", 1)[1]))):
    print("%s\t%d" % (k, counts[k]))
PY

# The collapsed acquisition: flush the claimed slot, dump counts once, delete only from src tables
# the dump says are non-empty, then add. Same post-condition as the blind form.
cat > /tmp/pfcol.seq.py <<'PY'
import re, statistics, subprocess, sys, time
anchor, slot, slots, addr, dests, n, mode = sys.argv[1], int(sys.argv[2]), int(sys.argv[3]), \
    sys.argv[4], sys.argv[5].split(","), int(sys.argv[6]), sys.argv[7]
DN = subprocess.DEVNULL
def pf(*a):
    subprocess.run(["sudo", "-n", "/sbin/pfctl", "-a", anchor] + list(a), stdout=DN, stderr=DN)
def dump():
    return subprocess.run(["sudo", "-n", "/sbin/pfctl", "-a", anchor, "-s", "Tables", "-vv"],
                          capture_output=True, text=True).stdout
tot, calls, dels = [], [], []
for _ in range(n):
    c, d = 0, 0
    t0 = time.perf_counter()
    pf("-t", "yb_src_%d" % slot, "-T", "flush"); c += 1
    pf("-t", "yb_dst_%d" % slot, "-T", "flush"); c += 1
    if mode == "collapsed":
        out = dump(); c += 1
        cur, occupied = None, []
        for line in out.splitlines():
            m = re.match(r'^\S*\s+(yb_src_(\d+))\b', line)
            if m:
                cur = int(m.group(2)); continue
            m = re.match(r'^\s+Addresses:\s+(\d+)', line)
            if m and cur is not None:
                if int(m.group(1)) > 0 and cur != slot:
                    occupied.append(cur)
                cur = None
        for i in occupied:
            pf("-t", "yb_src_%d" % i, "-T", "delete", addr); c += 1; d += 1
    else:
        for i in range(slots):
            if i != slot:
                pf("-t", "yb_src_%d" % i, "-T", "delete", addr); c += 1; d += 1
    pf("-t", "yb_src_%d" % slot, "-T", "add", addr); c += 1
    pf("-t", "yb_dst_%d" % slot, "-T", "add", *dests); c += 1
    tot.append((time.perf_counter() - t0) * 1000); calls.append(c); dels.append(d)
s = sorted(tot)
print("        %-10s %2d calls/run (%d of them deletes): min=%4.0f  med=%4.0f  max=%4.0f   (n=%d, ms)"
      % (mode, statistics.median(calls), statistics.median(dels), s[0], statistics.median(s), s[-1], n))
print("MED %s %.3f" % (mode, statistics.median(s)))
print("DELS %s %d" % (mode, statistics.median(dels)))
PY

echo "host: macOS $(sw_vers -productVersion) $(uname -m) | $(sysctl -n hw.model)"
echo "date: $(date '+%Y-%m-%d %H:%M:%S %Z') | user=$U | anchor=$ANCHOR | slots=$SLOTS slot=$SLOT"
echo "reference: pf_acquire_cost.sh measured the blind form at 329ms median, 35 calls, 9.3ms/call"

# ---------------------------------------------------------------------------
say "K0 SETUP"
{ for ((i=0;i<SLOTS;i++)); do
    echo "table <yb_src_$i> persist"
    echo "table <yb_dst_$i> persist"
    echo "pass  in quick from <yb_src_$i> to <yb_dst_$i>"
    echo "block drop in quick from <yb_src_$i> to any"
  done; } > /tmp/pfcol.rules
pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
pfctl -a "$ANCHOR" -f /tmp/pfcol.rules 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
[ "$(nrules)" -eq $((SLOTS*2)) ] || { bad "pool did not load; ABORTING"; exit 1; }
install -d -m 0755 -o root -g wheel "$CONFDIR"
install -m 0644 -o root -g wheel /tmp/pfcol.rules "$CONF"
LEAF='yb_(src|dst)_([0-9]|[12][0-9]|3[01])'
cat > /tmp/pfcol.sudoers <<EOF
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai_b -t $LEAF -T (add|delete|flush|show)( [0-9a-fA-F.:/]+)*\$
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai_b -f /etc/yoloai/pf-b\\.conf\$
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai_b -s rules\$
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-s info\$
EOF
visudo -c -f /tmp/pfcol.sudoers >/dev/null 2>&1 || { bad "policy fails visudo -c; ABORTING"; exit 1; }
install -m 0440 -o root -g wheel /tmp/pfcol.sudoers "$SUDOERS"
sudo -u "$U" sudo -K >/dev/null 2>&1
ok "$SLOTS-slot pool loaded, shipped grant installed"

# ---------------------------------------------------------------------------
say "K1 DOES ONE CALL REPORT A PER-TABLE ADDRESS COUNT?"
echo "        Planting a known distribution, then reading it back. Both directions are checked:"
echo "        occupied slots must read occupied AND empty ones must read empty. A parser that"
echo "        called every table occupied would be safe, useless, and pass a one-sided test."
pfctl -a "$ANCHOR" -t yb_src_3  -T add 198.51.100.77 >/dev/null 2>&1
pfctl -a "$ANCHOR" -t yb_src_20 -T add 203.0.113.88 203.0.113.89 >/dev/null 2>&1
echo "        planted: yb_src_3=1 address, yb_src_20=2 addresses, all other src tables empty"
echo
echo "        raw statistics block for one table, so the parsed field is visible:"
pfctl -a "$ANCHOR" -s Tables -vv 2>/dev/null | grep -A6 'yb_src_20' | head -8 | sed 's/^/        | /'
echo
python3 /tmp/pfcol.parse.py "$ANCHOR" > /tmp/pfcol.counts 2>/dev/null
got=$(grep -c . /tmp/pfcol.counts || true)
echo "        parsed $got of $((SLOTS*2)) tables"
c3=$(awk -F'\t' '$1=="yb_src_3"{print $2}'  /tmp/pfcol.counts)
c20=$(awk -F'\t' '$1=="yb_src_20"{print $2}' /tmp/pfcol.counts)
occ=$(awk -F'\t' '$1 ~ /^yb_src_/ && $2>0 {print $1}' /tmp/pfcol.counts | tr '\n' ' ')
echo "        yb_src_3=${c3:-?}  yb_src_20=${c20:-?}  | every non-empty src table: ${occ:-<none>}"
if [ "$got" -ne $((SLOTS*2)) ]; then
  bad "the listing did not yield a count for every table (got $got) — not a usable dump"
elif [ "${c3:-x}" = 1 ] && [ "${c20:-x}" = 2 ] && [ "$occ" = "yb_src_3 yb_src_20 " ]; then
  ok "one call reports every table's address count, and it discriminates: exactly the two planted"
  echo "           slots read non-empty, with the right counts, and the other 30 read empty"
else
  bad "counts do not match the plant (src_3=$c3 want 1, src_20=$c20 want 2, occupied='$occ')"
fi

# ---------------------------------------------------------------------------
say "K2a IS THE DUMP REACHABLE UNDER THE SHIPPED GRANT? (it must not be, and then we extend it)"
probe() { local out rc; out=$(sudo -u "$U" -H sudo -k -n $1 </dev/null 2>&1); rc=$?
  if [ "$rc" -eq 0 ]; then printf permit
  elif printf '%s' "$out" | grep -qiE "not allowed to execute|may not run|a password is required"; then printf refuse-by-policy
  else printf ran-but-failed; fi; }
DUMPCMD="/sbin/pfctl -a com.apple/yoloai_b -s Tables -vv"
r_before=$(probe "$DUMPCMD")
echo "        shipped grant: '$DUMPCMD' is $r_before"
if [ "$r_before" = refuse-by-policy ]; then
  ok "refused today, so the collapse is not free — it needs one added line"
else
  bad "expected refuse-by-policy, got $r_before — re-read the policy before trusting K3"
fi
cat > /tmp/pfcol.ext <<EOF
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai_b -s Tables -vv\$
EOF
visudo -c -f /tmp/pfcol.ext >/dev/null 2>&1 || { bad "extended policy fails visudo -c; ABORTING"; exit 1; }
install -m 0440 -o root -g wheel /tmp/pfcol.ext "$SUDOERS_EXT"
sudo -u "$U" sudo -K >/dev/null 2>&1
r_after=$(probe "$DUMPCMD")
echo "        with the extra line installed: $r_after"
# Run 2 timed the collapsed sequence while the dump was being refused, and reported a 7x win from
# a sequence that deleted nothing. Nothing below is measured until the dump is shown to answer.
parsed=$(sudo -u "$U" -H sudo -n /sbin/pfctl -a "$ANCHOR" -s Tables -vv 2>/dev/null | grep -c 'Addresses:' || true)
echo "        the dump, invoked AS THE USER through sudo, reports $parsed 'Addresses:' lines"
if [ "$r_after" = permit ] && [ "${parsed:-0}" -eq $((SLOTS*2)) ]; then
  ok "the extended grant answers with a full $((SLOTS*2))-table dump — timings below are real"
else
  bad "the dump does not answer through sudo (verdict=$r_after, tables=$parsed); ABORTING before"
  echo "           producing numbers for a capability the probe does not have — that is run 2"
  exit 1
fi

say "K2b WHAT THE COLLAPSE IS WORTH, at k occupied slots"
echo "        k is the number of slots holding an address — i.e. the number of sandboxes with"
echo "        live enforcement state. The blind form ignores k; the collapsed form is linear in it."
med()  { grep "^MED $1 "  /tmp/pfcol.meds 2>/dev/null | tail -1 | awk '{print $3}'; }
dels() { grep "^DELS $1 " /tmp/pfcol.meds 2>/dev/null | tail -1 | awk '{print $3}'; }
: > /tmp/pfcol.meds
run() { sudo -u "$U" -H python3 /tmp/pfcol.seq.py "$ANCHOR" "$SLOT" "$SLOTS" "$ADDR" "$DESTS" "$1" "$2" \
          | tee -a /tmp/pfcol.meds | grep -vE '^(MED|DELS) '; }

echo
echo "        the blind form, for a same-run baseline:"
run 6 blind
blind=$(med blind)
for k in 0 1 2 4 8; do
  for ((i=0;i<SLOTS;i++)); do pfctl -a "$ANCHOR" -t "yb_src_$i" -T flush >/dev/null 2>&1; done
  j=0
  for ((i=0;i<SLOTS && j<k;i++)); do
    [ "$i" -eq "$SLOT" ] && continue
    pfctl -a "$ANCHOR" -t "yb_src_$i" -T add "10.99.0.$((i+1))" >/dev/null 2>&1
    j=$((j+1))
  done
  echo "        k=$k occupied src tables:"
  run 6 collapsed
  c=$(med collapsed); d=$(dels collapsed)
  # Run 1 issued zero deletes at every k and looked like a 7x win. The cost was flat across k,
  # which is the only thing that gave it away. This makes that failure loud instead of subtle.
  if [ "${d:-x}" != "$k" ]; then
    bad "k=$k: the collapsed form issued ${d:-?} deletes, not $k — it is skipping slots it must"
    echo "           not skip, and its timing below means nothing"
  fi
  if [ -n "$c" ] && [ -n "$blind" ]; then
    python3 - "$blind" "$c" "$k" <<'PY'
import sys
b, c, k = float(sys.argv[1]), float(sys.argv[2]), sys.argv[3]
print("             k=%s: %.0fms against the blind form's %.0fms — %.1fx faster, %.0fms saved"
      % (k, c, b, b / c if c else 0, b - c))
PY
  fi
done
if [ -n "$blind" ]; then
  ok "collapsed sequence measured against a same-run blind baseline at five occupancies"
else
  bad "no baseline produced"
fi
echo
echo "        NOTE the collapsed form's cost tracks RUNNING SANDBOXES, not pool size, so it also"
echo "        removes pool size as a latency knob — the 32-slot cap stops costing anything at rest."

# ---------------------------------------------------------------------------
say "K3 GRANT SURFACE — what this costs at the security boundary"
echo "        measured in K2a, not re-asserted here: shipped grant = $r_before, extended = $r_after"
echo "        the collapse costs exactly one new NOPASSWD line:"
echo "             <user> ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai -s Tables -vv\$"
rm -f "$SUDOERS_EXT"
sudo -u "$U" sudo -K >/dev/null 2>&1
r_removed=$(probe "$DUMPCMD")
echo "        extended line removed mid-run; the dump is now: $r_removed"
if [ "$r_removed" = refuse-by-policy ]; then
  ok "the extension is the whole difference — removing it restores the refusal, so nothing else"
  echo "           in this run's policy was quietly permitting the dump"
else
  bad "still $r_removed after removing the extension — something else permits it; investigate"
fi
echo
echo "        WHAT IT REVEALS, against what the grant already reveals:"
echo "          - it is a READ. It cannot add or delete membership, load a ruleset, or touch pf's"
echo "            enable state, so it reaches none of the nine bypasses D132 refused."
echo "          - it discloses each slot's address COUNT, never an address. The shipped grant"
echo "            already permits '-t <table> -T show', which discloses the addresses themselves,"
echo "            one table per call. So this is strictly LESS informative than 64 calls a holder"
echo "            can already make — it is cheaper, not more revealing."
echo "          - it is anchor-scoped by the same literal-anchor regex as every other line, so it"
echo "            cannot read another anchor's tables."
echo "        NOT A RUBBER STAMP: D132's permit/refuse matrix must be re-run against the extended"
echo "        policy before anything relies on this. sudoers matches a concatenated argument"
echo "        string, so a new line is a new place for an argument to be smuggled, and the only"
echo "        thing that has ever caught that here is the matrix."

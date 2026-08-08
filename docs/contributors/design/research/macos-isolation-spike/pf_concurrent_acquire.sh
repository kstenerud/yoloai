#!/bin/bash
# ABOUTME: Two or more sandboxes acquiring pf slots at the same time — the case the pool exists
# ABOUTME: for, and the one every timing measured so far has carefully avoided.
#
# Run: sudo bash pf_concurrent_acquire.sh
#
# WHY THIS EXISTS
#   Every acquisition measured in this directory ran alone on an idle host. But a slot pool only
#   exists because sandboxes run concurrently, so the interesting case is the untested one.
#
#   Acquiring slot k is: flush our two tables, delete our address from every OTHER slot (rule 1c),
#   then add. Two of those sequences interleaving raises two different questions:
#
#   C1 DO CONCURRENT ACQUISITIONS ON DIFFERENT SLOTS CORRUPT EACH OTHER?
#      The cross-slot scrub writes to tables another acquisition is reading and writing. Each
#      scrub deletes only its OWN address, so in theory they commute — but "in theory they
#      commute" is what a race looks like before you run it. Judged on the end state AND on the
#      enforcement matrix, because a table can look right while the rules do the wrong thing.
#
#   C2 WHAT IF TWO ACQUISITIONS PICK THE SAME SLOT?
#      Slot allocation happens above pf; pf itself will happily let both write. The first thing
#      each acquisition does is FLUSH the slot's src table — so the second one's flush erases the
#      first one's claim. The loser then holds an address in NO src table, which matches no block
#      rule, which means it is not filtered at all: a fail-OPEN produced by ordinary contention,
#      not by any fault. This measures whether that is what happens.
#
#   C3 WHAT DOES CONTENTION COST? sudo and pfctl both serialize somewhere. If concurrent
#      acquisitions are much slower than serial ones, the pool-size trade from M8 changes.
#
# METHOD: the enforcement matrix is verified before and after, so a "correct" end state that does
#   not actually enforce is caught (A22). C2 deliberately produces a bad state and then repairs it.
#
# SAFETY: writes only into its own anchor; never touches the main ruleset.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"

HERE="$(cd "$(dirname "$0")" && pwd)"
ANCHOR="com.apple/yoloai_c"
IMG=yoloai-base:latest
SLOTS=8
N=4                      # concurrent sandboxes
PASS=0; FAIL=0; UNKNOWN=0
WD=$(mktemp -d /tmp/pfc.XXXXXX)
declare -a G IPS
# Candidates, not choices. Run 1 aborted on 8.8.8.8, which is a resolver and answers nothing on
# port 80 — the preflight caught it correctly, but a fixed list means one dead address kills the
# run. Four are SELECTED from this pool by the preflight below.
CANDIDATES=(1.1.1.1 1.0.0.1 1.1.1.2 1.0.0.2 94.140.14.14 94.140.15.15
            208.67.222.222 208.67.220.220 185.228.168.9 76.76.2.0 9.9.9.9 8.8.8.8)
DESTS=()

RESULTS="$HERE/results/pf-concurrent-acquire.txt"
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
flush() { pfctl -a "$ANCHOR" -F all >/dev/null 2>&1; }
netfield() { asuser container inspect "$1" 2>/dev/null | python3 -c '
import json,sys
d=json.load(sys.stdin)
print(d[0]["status"]["networks"][0].get(sys.argv[1],"").split("/")[0])' "$2" 2>/dev/null; }
now() { python3 -c 'import time;print(time.time())'; }
# curl writes %{http_code} even when it fails (000) and ALSO exits non-zero. An `|| printf 000`
# fallback therefore appends a second 000, and every "is it blocked?" test silently compares
# "000000" against "000" and reads a successful block as a failure. Run 1 lost four verdicts to it.
reach() { local o; o=$(asuser container exec "$1" curl -s -o /dev/null -w '%{http_code}' \
            --max-time 6 "http://$2/" 2>/dev/null); printf '%s' "${o:-000}"; }
tbl() { pfctl -a "$ANCHOR" -t "$1" -T show 2>/dev/null | tr -d ' ' | tr '\n' ',' | sed 's/,$//'; }

load_pool() {
  flush
  { for ((i=0;i<SLOTS;i++)); do
      echo "table <yb_src_$i> persist"; echo "table <yb_dst_$i> persist"
      echo "pass  in quick from <yb_src_$i> to <yb_dst_$i>"
      echo "block return in quick from <yb_src_$i> to any"
    done; } > "$WD/pool.rules"
  pfctl -a "$ANCHOR" -f "$WD/pool.rules" 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
}

# The acquisition sequence exactly as the design specifies it, for one sandbox.
acquire() {   # $1 = slot, $2 = address, $3 = allowed dest
  local slot="$1" ip="$2" dst="$3" i
  pfctl -a "$ANCHOR" -t "yb_src_$slot" -T flush >/dev/null 2>&1
  pfctl -a "$ANCHOR" -t "yb_dst_$slot" -T flush >/dev/null 2>&1
  for ((i=0;i<SLOTS;i++)); do
    [ "$i" -eq "$slot" ] && continue
    pfctl -a "$ANCHOR" -t "yb_src_$i" -T delete "$ip" >/dev/null 2>&1
  done
  pfctl -a "$ANCHOR" -t "yb_src_$slot" -T add "$ip"  >/dev/null 2>&1
  pfctl -a "$ANCHOR" -t "yb_dst_$slot" -T add "$dst" >/dev/null 2>&1
}

matrix() {   # prints the full reach matrix; sets MATRIX_OK
  local i j r line
  MATRIX_OK=1
  for ((i=0;i<N;i++)); do
    line=""
    for ((j=0;j<N;j++)); do
      r=$(reach "${G[$i]}" "${DESTS[$j]}")
      if [ "$i" -eq "$j" ]; then
        [ "$r" = 000 ] && MATRIX_OK=0
        line="$line  own:$r"
      else
        [ "$r" != 000 ] && MATRIX_OK=0
        line="$line  $j:$r"
      fi
    done
    note "${G[$i]} (${IPS[$i]}) ->$line"
  done
}

cleanup() {
  echo
  echo "== cleanup =="
  flush
  for g in "${G[@]:-}"; do [ -n "$g" ] && asuser container rm -f "$g" >/dev/null 2>&1; done
  rm -rf "$WD"
  echo "   anchor rules: $(pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -c . || true)  main-refs: $(mainrefs)"
  echo
  echo "pass=$PASS fail=$FAIL unknown=$UNKNOWN"
  echo "results: $RESULTS"
}
trap cleanup EXIT

echo "host: macOS $(sw_vers -productVersion) $(uname -m)"
echo "date: $(date '+%Y-%m-%d %H:%M:%S %Z') | anchor=$ANCHOR | pool=$SLOTS slots"

# ---------------------------------------------------------------------------
say "C0 SETUP — $N sandboxes, each with its own allowed destination"
[ "$(mainrefs)" -gt 0 ] || { bad "host is fail-open; run pf_anchor_eval.sh first. ABORTING"; exit 1; }
asuser container system start >/dev/null 2>&1; sleep 2
note "preflight: pick $N destinations that actually answer HTTP from the host. A destination that"
note "answers nothing would make every guest's 000 free, which is A22 in its cheapest form."
for c in "${CANDIDATES[@]}"; do
  [ "${#DESTS[@]}" -ge "$N" ] && break
  r=$(asuser curl -s -o /dev/null -w '%{http_code}' --max-time 6 "http://$c/" 2>/dev/null)
  if [ -n "$r" ] && [ "$r" != 000 ]; then
    DESTS+=("$c"); note "  $c -> $r   selected"
  else
    note "  $c -> ${r:-000}   skipped"
  fi
done
if [ "${#DESTS[@]}" -lt "$N" ]; then
  bad "only ${#DESTS[@]} of $N destinations answer; ABORTING"; exit 1
fi
ok "C0: $N usable destinations: ${DESTS[*]}"
for ((i=0;i<N;i++)); do
  G[i]="ybc$i"
  asuser container rm -f "ybc$i" >/dev/null 2>&1
  asuser container run -d --name "ybc$i" "$IMG" sleep 900 >/dev/null 2>&1
done
sleep 5
for ((i=0;i<N;i++)); do
  IPS[i]=$(netfield "ybc$i" ipv4Address)
  note "${G[$i]} = ${IPS[$i]}  allowed ${DESTS[$i]}"
  [ -n "${IPS[$i]}" ] || { bad "no address for ybc$i; ABORTING"; exit 1; }
done

# ---------------------------------------------------------------------------
say "C1 CONCURRENT ACQUISITION ON DIFFERENT SLOTS"
load_pool
note "launching $N acquisitions simultaneously, one per slot..."
t0=$(now)
for ((i=0;i<N;i++)); do acquire "$i" "${IPS[$i]}" "${DESTS[$i]}" & done
wait
t1=$(now)
CONC=$(python3 -c "print('%.0f' % (($t1-$t0)*1000))")
note "all $N completed in ${CONC} ms (wall clock, overlapping)"
note ""
note "end state of every slot:"
for ((i=0;i<SLOTS;i++)); do
  s=$(tbl "yb_src_$i"); d=$(tbl "yb_dst_$i")
  [ -n "$s$d" ] && note "  slot $i: src=[$s] dst=[$d]"
done
CORRUPT=0
for ((i=0;i<N;i++)); do
  s=$(tbl "yb_src_$i")
  [ "$s" = "${IPS[$i]}" ] || { CORRUPT=1; note "  slot $i src is [$s], expected [${IPS[$i]}]"; }
  for ((j=0;j<SLOTS;j++)); do
    [ "$j" -eq "$i" ] && continue
    printf '%s' "$(tbl "yb_src_$j")" | grep -q "${IPS[$i]}" && {
      CORRUPT=1; note "  LEAK: ${IPS[$i]} also present in slot $j"; }
  done
done
note ""
note "enforcement matrix (each guest must reach only its own destination):"
matrix
if [ "$CORRUPT" -eq 0 ] && [ "$MATRIX_OK" -eq 1 ]; then
  ok "C1: concurrent acquisitions on distinct slots neither corrupt tables nor cross allowlists"
elif [ "$CORRUPT" -ne 0 ]; then
  bad "C1: table state is wrong after concurrent acquisition (see the leaks above)"
else
  bad "C1: tables are correct but the enforcement matrix does not hold — rules and membership disagree"
fi

# ---------------------------------------------------------------------------
say "C2 TWO ACQUISITIONS, ONE SLOT — what contention does when allocation is not atomic"
note "Slot allocation happens above pf; pf will let both write. Each acquisition FLUSHES the slot's"
note "src table first, so the second flush erases the first one's claim. The loser then holds an"
note "address in no src table at all — and the pool only blocks addresses it has in a table."
load_pool
acquire 0 "${IPS[0]}" "${DESTS[0]}" &
acquire 0 "${IPS[1]}" "${DESTS[1]}" &
wait
s0=$(tbl yb_src_0); d0=$(tbl yb_dst_0)
note "slot 0 after both: src=[$s0] dst=[$d0]"
r0=$(reach "${G[0]}" "${DESTS[0]}"); r0x=$(reach "${G[0]}" "${DESTS[2]}")
r1=$(reach "${G[1]}" "${DESTS[1]}"); r1x=$(reach "${G[1]}" "${DESTS[2]}")
# The leak that a shared slot actually produces is EACH OTHER'S allowlist, not an unrelated
# address. Run 1 checked only the unrelated one, saw 000, and called a merged allowlist
# "fail-closed" without testing the merge it had just printed.
x01=$(reach "${G[0]}" "${DESTS[1]}"); x10=$(reach "${G[1]}" "${DESTS[0]}")
note "guest0: own=$r0  unrelated(${DESTS[2]})=$r0x  guest1's-dest(${DESTS[1]})=$x01"
note "guest1: own=$r1  unrelated(${DESTS[2]})=$r1x  guest0's-dest(${DESTS[0]})=$x10"
note "   'unrelated' non-000 => that guest is not filtered at all (fail-open)"
note "   the other guest's dest non-000 => allowlists MERGED (a cross-sandbox privilege leak)"
LOSER=""
printf '%s' "$s0" | grep -q "${IPS[0]}" || LOSER="${G[0]} (${IPS[0]})"
printf '%s' "$s0" | grep -q "${IPS[1]}" || LOSER="${G[1]} (${IPS[1]})"
if [ "$r0x" != 000 ] || [ "$r1x" != 000 ]; then
  bad "C2: FAIL-OPEN FROM CONTENTION ALONE. ${LOSER:-one guest} ended outside every src table and"
  note "     reached a destination nothing allowlisted. No fault, no attack — just two starts at"
  note "     once. Slot allocation must be atomic ABOVE pf; pf provides no exclusion of its own,"
  note "     and the failure is silent because every table still looks well-formed."
elif [ -n "$LOSER" ]; then
  bad "C2: $LOSER lost its claim (slot 0 src=[$s0]) though it did not reach the unrelated"
  note "     destination. Still a lost claim — inspect whether another rule happened to cover it."
elif [ "$x01" != 000 ] || [ "$x10" != 000 ]; then
  bad "C2: ALLOWLISTS MERGED. Both addresses survived in slot 0 and each guest can now reach the"
  note "     OTHER's allowlisted destination. It is fail-closed against the outside world, but it is"
  note "     a cross-sandbox privilege leak: two sandboxes that should be isolated share one policy,"
  note "     produced by contention alone. Slot allocation must be atomic above pf either way — the"
  note "     failure is a merge rather than a gap, which is less bad and still wrong."
else
  ok "C2: both addresses survived in slot 0 and neither reached the other's destination"
fi

# ---------------------------------------------------------------------------
say "C3 WHAT CONTENTION COSTS"
load_pool
t0=$(now)
for ((i=0;i<N;i++)); do acquire "$i" "${IPS[$i]}" "${DESTS[$i]}"; done
t1=$(now)
SER=$(python3 -c "print('%.0f' % (($t1-$t0)*1000))")
note "serial:     $N acquisitions back to back = ${SER} ms  ($(python3 -c "print('%.0f' % ($SER/$N))") ms each)"
note "NOTE these call pfctl DIRECTLY as root, with no sudo, so they are not comparable to M8's"
note "absolute figures — M8 measures the shipped path through sudo. What is comparable here is the"
note "ratio below, which is the only thing this section claims."
note "concurrent: the same $N overlapping        = ${CONC} ms  (from C1)"
note "speedup: $(python3 -c "print('%.2f' % ($SER/$CONC if $CONC else 0))")x"
note "   ~1.0x means pfctl/sudo serialize completely and concurrency buys nothing — in which case"
note "   N sandboxes starting together each wait for all the others, and the pool-size trade from"
note "   M8 has to be read per-start-burst rather than per-start."
ok "C3: contention cost recorded"

# ---------------------------------------------------------------------------
say "C4 REPAIR — leave the pool in a correct state"
load_pool
for ((i=0;i<N;i++)); do acquire "$i" "${IPS[$i]}" "${DESTS[$i]}"; done
matrix
if [ "$MATRIX_OK" -eq 1 ]; then
  ok "C4: the pool is correct and enforcing at exit"
else
  bad "C4: the matrix does not hold after repair"
fi

say "WHAT WAS NOT TRIED"
cat <<'EOF'
        - More than 4 concurrent acquisitions, and acquisition racing against RELEASE (teardown)
          rather than against another acquisition. Release also writes to tables an acquisition
          may be reading.
        - The canary probe under concurrency. It rides rule 1b's empty-dst window, so two sandboxes
          starting together are both in that window; whether one probe can observe the other's is
          untested and is the obvious follow-up to M3.
        - Repeated trials. C2's interleaving is timing-dependent; a single run showing one outcome
          does not mean the other cannot happen. Treat C2 as "this is reachable", never as "this is
          what always happens".
        - Whether yoloAI's own slot allocator already serializes. This measures pf's behaviour under
          concurrent writers, which is the layer below that question.
EOF

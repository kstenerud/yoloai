#!/bin/bash
# ABOUTME: M8 — does slot-acquisition cost really scale linearly with pool size? Measured at
# ABOUTME: 8, 16 and 32 slots through the real sudo path, against a real sandbox start.
#
# Run: sudo bash pf_pool_scaling.sh
#
# WHY THIS EXISTS
#   329 ms at 32 slots, 9.3 ms per sudo call, 85% of which was sudo and not pfctl. From that the
#   plan concluded "shrink the pool" is the lever — but linearity was ASSUMED, and so the lever was
#   assumed with it. If the cost is dominated by a per-acquisition constant, halving the pool buys
#   almost nothing and the pool size should be chosen for concurrency instead. That is an owner
#   decision, and it currently has numbers on one axis only.
#
#   THE SEQUENCE, which is what makes size matter at all. Acquiring slot k in an N-slot pool is:
#       2 flushes         — our own src and dst tables (rule 1: clear before claim)
#     + (N-1) deletes     — our address removed from every OTHER slot's src table (rule 1c: the
#                           cross-slot scrub, because a stale entry elsewhere grants its allowlist)
#     + 2 adds            — the address, and the allowlist
#     = N+3 sudo invocations, so the scrub is the only term that grows.
#
#   Run through the real path: the shipped grant installed, `sudo -n pfctl` issued as the user.
#   Timing it as root would measure pfctl and miss the 85% that is sudo.
#
# SAFETY: writes only into its own anchor; installs a spike-named grant and removes it at exit.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"

HERE="$(cd "$(dirname "$0")" && pwd)"
ANCHOR="com.apple/yoloai_p"
IMG=yoloai-base:latest
SUDOERS=/etc/sudoers.d/yoloai-pf-spike-p
POOLDIR=/etc/yoloai-spike-p
POOLCONF="$POOLDIR/pf-pool.conf"
ALLOW=1.1.1.1
REPS=5
PASS=0; FAIL=0; UNKNOWN=0

RESULTS="$HERE/results/pf-pool-scaling.txt"
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
now()   { python3 -c 'import time;print(time.time())'; }

cleanup() {
  echo
  echo "== cleanup =="
  rm -f "$SUDOERS"; rm -rf "$POOLDIR"
  pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
  asuser container rm -f ybp1 >/dev/null 2>&1
  echo "   sudoers.d: [$(ls /etc/sudoers.d/ 2>/dev/null | tr '\n' ' ')]  main-refs: $(mainrefs)"
  echo
  echo "pass=$PASS fail=$FAIL unknown=$UNKNOWN"
  echo "results: $RESULTS"
}
trap cleanup EXIT

echo "host: macOS $(sw_vers -productVersion) $(uname -m)"
echo "date: $(date '+%Y-%m-%d %H:%M:%S %Z') | anchor=$ANCHOR"

# ---------------------------------------------------------------------------
say "P0 SETUP — the grant, in its shipped shape"
[ "$(mainrefs)" -gt 0 ] || { bad "host is fail-open; run pf_anchor_eval.sh first. ABORTING"; exit 1; }
mkdir -p "$POOLDIR"
cat > "$SUDOERS" <<EOF
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai_p -t yb_(src|dst)_([0-9]|[12][0-9]|3[01]) -T (add|delete|flush|show)( [0-9a-fA-F.:/]+)*\$
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai_p -f $POOLCONF\$
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai_p -s rules\$
$U ALL=(root) NOPASSWD: /sbin/pfctl ^-s info\$
EOF
chmod 440 "$SUDOERS"
visudo -c -f "$SUDOERS" >/dev/null 2>&1 || { bad "grant did not validate; ABORTING"; exit 1; }
asuser sudo -n /sbin/pfctl -s info >/dev/null 2>&1 \
  && ok "grant is live (the user can run a permitted pfctl without a password)" \
  || { bad "grant is not authorizing; ABORTING"; exit 1; }

asuser container system start >/dev/null 2>&1; sleep 2
asuser container rm -f ybp1 >/dev/null 2>&1
asuser container run -d --name ybp1 "$IMG" sleep 900 >/dev/null 2>&1
sleep 4
IP=$(asuser container inspect ybp1 2>/dev/null | python3 -c '
import json,sys
d=json.load(sys.stdin)
print(d[0]["status"]["networks"][0].get("ipv4Address","").split("/")[0])' 2>/dev/null)
note "guest=${IP:-<none>}"
[ -n "$IP" ] || { bad "no guest address; ABORTING"; exit 1; }

# ---------------------------------------------------------------------------
load_pool() {   # $1 = slot count
  local n="$1"
  pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
  { for ((i=0;i<n;i++)); do
      echo "table <yb_src_$i> persist"; echo "table <yb_dst_$i> persist"
      echo "pass  in quick from <yb_src_$i> to <yb_dst_$i>"
      echo "block return in quick from <yb_src_$i> to any"
    done; } > "$POOLCONF"
  chmod 644 "$POOLCONF"
  asuser sudo -n /sbin/pfctl -a "$ANCHOR" -f "$POOLCONF" 2>&1 | quiet_pf >/dev/null
}

# One full acquisition of SLOT in an N-slot pool, through sudo, exactly as the design specifies.
# Returns elapsed ms and the number of sudo calls issued, and the two are checked against each
# other by the caller — a sequence that silently issues fewer calls than it should would report a
# beautiful number for doing nothing, which is how the collapse run was invalidated once already.
acquire() {   # $1 = slot count, $2 = slot to claim; echoes "<ms> <calls>"
  local n="$1" slot="$2" i calls=0 t0 t1
  t0=$(now)
  asuser sudo -n /sbin/pfctl -a "$ANCHOR" -t "yb_src_$slot" -T flush >/dev/null 2>&1; calls=$((calls+1))
  asuser sudo -n /sbin/pfctl -a "$ANCHOR" -t "yb_dst_$slot" -T flush >/dev/null 2>&1; calls=$((calls+1))
  for ((i=0;i<n;i++)); do
    [ "$i" -eq "$slot" ] && continue
    asuser sudo -n /sbin/pfctl -a "$ANCHOR" -t "yb_src_$i" -T delete "$IP" >/dev/null 2>&1
    calls=$((calls+1))
  done
  asuser sudo -n /sbin/pfctl -a "$ANCHOR" -t "yb_src_$slot" -T add "$IP"    >/dev/null 2>&1; calls=$((calls+1))
  asuser sudo -n /sbin/pfctl -a "$ANCHOR" -t "yb_dst_$slot" -T add "$ALLOW" >/dev/null 2>&1; calls=$((calls+1))
  t1=$(now)
  printf '%s %s' "$(python3 -c "print('%.1f' % (($t1-$t0)*1000))")" "$calls"
}

say "P1 ACQUISITION COST AT 8, 16 AND 32 SLOTS"
note "$REPS repetitions each. The expected call count is N+3; a run whose count disagrees is"
note "reported as broken rather than averaged in."
printf '        %-6s %-8s %-10s %-12s %s\n' SLOTS CALLS "MEAN ms" "PER CALL ms" "runs"
RES8=""; RES16=""; RES32=""
for n in 8 16 32; do
  load_pool "$n"
  loaded=$(pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -c . || true)
  if [ "$loaded" -ne $((n*2)) ]; then
    unk "P1: pool of $n slots loaded $loaded rules, expected $((n*2)); skipping this size"
    continue
  fi
  total=0; runs=""; badcalls=0; expect=$((n+3))
  for _ in $(seq "$REPS"); do
    r=$(acquire "$n" 1)
    t=${r%% *}; c=${r##* }
    [ "$c" -ne "$expect" ] && badcalls=$((badcalls+1))
    runs="$runs $t"
    total=$(python3 -c "print($total + $t)")
  done
  mean=$(python3 -c "print('%.1f' % ($total/$REPS))")
  per=$(python3 -c "print('%.2f' % ($total/$REPS/$expect))")
  printf '        %-6s %-8s %-10s %-12s %s\n' "$n" "$expect" "$mean" "$per" "$runs"
  [ "$badcalls" -gt 0 ] && bad "P1: $badcalls/$REPS runs at n=$n issued the wrong number of calls"
  case "$n" in 8) RES8=$mean;; 16) RES16=$mean;; 32) RES32=$mean;; esac
done

# ---------------------------------------------------------------------------
say "P2 IS IT LINEAR? — the question the 'shrink the pool' lever depends on"
if [ -n "$RES8" ] && [ -n "$RES16" ] && [ -n "$RES32" ]; then
  note "If cost = a + b*(N+3), then a and b fall straight out of two points and the third checks it."
  python3 - "$RES8" "$RES16" "$RES32" <<'PY'
import sys
t8, t16, t32 = (float(x) for x in sys.argv[1:4])
n8, n16, n32 = 11, 19, 35            # N+3 calls at each size
b = (t32 - t8) / (n32 - n8)
a = t8 - b * n8
pred16 = a + b * n16
print("        fitted from n=8 and n=32:")
print("          per-call cost b   = %6.2f ms" % b)
print("          fixed overhead a  = %6.1f ms" % a)
print("        check against the measured n=16 point:")
print("          predicted %.1f ms, measured %.1f ms, error %.1f ms (%.1f%%)"
      % (pred16, t16, t16 - pred16, 100 * (t16 - pred16) / t16 if t16 else 0))
err = abs(t16 - pred16) / t16 * 100 if t16 else 0
if err < 10:
    print("        => LINEAR within 10%. Cost is dominated by the per-call term, so pool size is a")
    print("           real latency lever: halving the pool removes (N/2) sudo invocations.")
else:
    print("        => NOT linear within 10%. Refit before treating pool size as a latency lever.")
print()
print("        What each size costs a sandbox start, using the fit:")
for n in (4, 8, 16, 32):
    print("          %2d slots -> %6.1f ms" % (n, a + b * (n + 3)))
PY
else
  unk "P2: not all three sizes measured; no fit"
fi

# ---------------------------------------------------------------------------
say "P3 THE DENOMINATOR — what a sandbox start costs without any of this"
note "A latency lever is only worth pulling against the thing it is a fraction of. This measures"
note "\`container run -d\` — the backend's own create — which is the floor. A full \`yoloai new\` is"
note "larger (~2.4s in earlier runs), so the percentages below are the PESSIMISTIC framing: against"
note "a real sandbox start, acquisition is a smaller fraction than this section makes it look."
tot=0
for i in 1 2 3; do
  asuser container rm -f "ybp-t$i" >/dev/null 2>&1
  t0=$(now)
  asuser container run -d --name "ybp-t$i" "$IMG" sleep 60 >/dev/null 2>&1
  t1=$(now)
  e=$(python3 -c "print('%.0f' % (($t1-$t0)*1000))")
  note "start $i: ${e} ms"
  tot=$(python3 -c "print($tot + $e)")
  asuser container rm -f "ybp-t$i" >/dev/null 2>&1
done
START=$(python3 -c "print('%.0f' % ($tot/3))")
note "mean start: ${START} ms"
if [ -n "$RES32" ]; then
  note "acquisition at 32 slots is $(python3 -c "print('%.1f' % (100*$RES32/$START))")% of that;"
  note "at 8 slots it is $(python3 -c "print('%.1f' % (100*${RES8:-0}/$START))")%."
  ok "P3: both axes of the pool-size decision now have numbers"
fi

# ---------------------------------------------------------------------------
say "P4 WHAT THE POOL SIZE ALSO BUYS — the axis that is not latency"
note "Slots are the concurrency ceiling: N slots means at most N isolated sandboxes at once, and"
note "the (N+1)th either waits or runs unenforced. Neither is measured here — the exhaustion policy"
note "is a design decision, not a measurement — but the trade is not latency-versus-nothing."
note "pf's own limits, for the record:"
pfctl -s memory 2>/dev/null | sed 's/^/          /' | head -8

say "WHAT WAS NOT TRIED"
cat <<'EOF'
        - Sizes above 32. The shipped grant's regex tops out at slot 31, so a larger pool is a
          grant change and not just a parameter.
        - Concurrent acquisitions. Every measurement here is one acquisition at a time on an idle
          host. Two sandboxes starting together contend for the same pfctl and the same sudo, and
          that is exactly the case the pool exists for.
        - Allowlist size. pf-acquire-cost.txt already measured that a call's cost does not scale
          with the number of addresses in it, which is why the adds are two calls and not forty.
        - The exhaustion policy. What the (N+1)th sandbox does is unmeasured and undecided.
EOF

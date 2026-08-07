#!/bin/bash
# ABOUTME: The slot pool at realistic occupancy — many live sandboxes, each with its OWN allowlist,
# ABOUTME: each checked against every other's, plus the scrub and free-slot search at a real k.
#
# Run: sudo bash pf_pool_occupancy.sh [n]        (default n=8)
#
# WHY THIS EXISTS
#   Two separate things are measured about this pool and neither is what the design claims.
#   `pf-assumptions.txt` D1 loaded 32 slots and showed that RULES load and that a high slot index
#   enforces — with one guest. `pf-shapeb.txt` B2 showed two sandboxes holding independent
#   allowlists. So "32 concurrently-isolated sandboxes" rests on 64 rules that parse plus an
#   independence result measured at n=2, and the gap between those has never been crossed.
#
#   O1 INDEPENDENCE AT n>2. Every sandbox gets a DIFFERENT allowlisted address. Each must reach its
#      own and be refused every other's. That is n(n-1) negative assertions with n positive
#      controls carrying them, and it is the assertion the user-visible cap actually rests on.
#      Cross-checking matters because the n=2 case cannot distinguish "each has its own policy"
#      from "each has the union of both" in the direction that would matter.
#   O2 THE FREE-SLOT SEARCH. The address-count dump from pf_scrub_collapse.sh gives every slot's
#      occupancy in one call, which is also how a start path would FIND a free slot. With k real
#      sandboxes placed, does it report exactly those k as occupied? A wrong answer here hands two
#      sandboxes the same slot.
#   O3 EXHAUSTION, observed rather than handled. With every slot occupied there is no free slot to
#      find. What the product should DO about that is undecided (see macos-pf-privileged-path.md);
#      this only establishes that the condition is detectable, and detectable cheaply.
#   O4 THE SCRUB AT A REAL k, against addresses vmnet actually assigned rather than the planted
#      10.99.x used before — so the collapse's cost model is confirmed against real occupancy.
#
# CONTROL (A22): every "blocked" is paired with that same sandbox reaching its own destination in
# the same pass. A sandbox stranded by the vmnet subnet re-pick refuses everything for free, and
# with n guests that failure mode is n times more likely, not less.
#
# SAFETY: only com.apple/yoloai_b. Main ruleset never written; its count is asserted at exit.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0 [n]"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"
N=${1:-8}

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/../../../../.." && pwd)"
YOLOAI="$REPO/yoloai"
[ -x "$YOLOAI" ] || { echo "no yoloai binary at $YOLOAI"; exit 2; }

ANCHOR="com.apple/yoloai_b"
SLOTS=8
WD=$(mktemp -d /tmp/pfocc-wd.XXXXXX)
SUDOERS=/etc/sudoers.d/yoloai-occ-probe
SUDOERS_EXT=/etc/sudoers.d/yoloai-occ-extended
CONFDIR=/etc/yoloai; CONF="$CONFDIR/pf-b.conf"
PASS=0; FAIL=0; UNKNOWN=0
# One distinct HTTP-answering destination per slot. Any non-000 code means reached — the code
# itself is irrelevant, only whether a packet completed a round trip. These are connected to BY
# ADDRESS, so CDN rotation cannot move them mid-run. More candidates than slots are listed because
# preflight discards the ones that do not answer: of an obvious first set, 8.8.8.8, 8.8.4.4,
# 9.9.9.9 and 149.112.112.112 run no web server at all and would have failed the baseline gate
# after every VM had already been built.
DESTS=(1.1.1.1 1.0.0.1 1.1.1.2 1.0.0.2 1.1.1.3 1.0.0.3 208.67.222.222 208.67.220.220
       94.140.14.14 94.140.15.15 208.67.222.123 208.67.220.123)
SBS=(); IPS=(); MADE=0

RESULTS="$HERE/results/pf-pool-occupancy.txt"
mkdir -p "$(dirname "$RESULTS")"
exec > >(tee "$RESULTS") 2>&1

say() { printf '\n== %s ==\n' "$*"; }
ok()  { PASS=$((PASS+1));       printf '   PASS    %s\n' "$*"; }
bad() { FAIL=$((FAIL+1));       printf '   FAIL    %s\n' "$*"; }
unk() { UNKNOWN=$((UNKNOWN+1)); printf '   UNKNOWN %s\n' "$*"; }
asuser() { sudo -u "$U" -H "$@"; }
quiet_pf() { grep -viE 'use of -f option|main ruleset added|/etc/pf.conf for further|ALTQ|^$'; }
nrules() { pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -c . || true; }
ipof() { asuser container inspect "yoloai-cli-$1" 2>/dev/null \
  | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d[0]["status"]["networks"][0].get("ipv4Address","").split("/")[0])' 2>/dev/null; }
egress() {
  local c
  c=$(asuser container exec "yoloai-cli-$1" curl -s -o /dev/null -w '%{http_code}' --max-time 5 "http://$2/" 2>/dev/null)
  c=${c//[^0-9]/}; c=${c: -3}; printf '%s' "${c:-000}"
}

MAIN_BEFORE=$(pfctl -s rules 2>/dev/null | grep -c . || true)
cleanup() {
  echo
  echo "== cleanup =="
  pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
  rm -f "$SUDOERS" "$SUDOERS_EXT" /tmp/pfocc.*; rm -rf "$CONFDIR" "$WD"
  for s in "${SBS[@]:-}"; do
    [ -n "$s" ] && { asuser "$YOLOAI" destroy "$s" --abandon-unapplied >/dev/null 2>&1 \
      && echo "   destroyed $s" || echo "   NOTE $s not destroyed — remove by hand"; }
  done
  echo "   anchor rules=$(nrules)"
  echo "   main ruleset: before=$MAIN_BEFORE after=$(pfctl -s rules 2>/dev/null | grep -c . || true)"
  echo "   sudoers present: probe=$([ -e "$SUDOERS" ] && echo YES-BAD || echo no) ext=$([ -e "$SUDOERS_EXT" ] && echo YES-BAD || echo no)"
  echo
  echo "pass=$PASS fail=$FAIL unknown=$UNKNOWN"
}
trap cleanup EXIT

echo "host: macOS $(sw_vers -productVersion) $(uname -m) | $(sysctl -n hw.model) | RAM $(( $(sysctl -n hw.memsize) / 1073741824 ))GB"
echo "date: $(date '+%Y-%m-%d %H:%M:%S %Z') | anchor=$ANCHOR | slots=$SLOTS | sandboxes requested=$N"

# ---------------------------------------------------------------------------
say "O0 SETUP — $SLOTS-slot pool, then as many live sandboxes as the host will give"
{ for ((i=0;i<SLOTS;i++)); do
    echo "table <yb_src_$i> persist"; echo "table <yb_dst_$i> persist"
    echo "pass  in quick from <yb_src_$i> to <yb_dst_$i>"
    echo "block drop in quick from <yb_src_$i> to any"
  done; } > /tmp/pfocc.rules
pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
pfctl -a "$ANCHOR" -f /tmp/pfocc.rules 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
[ "$(nrules)" -eq $((SLOTS*2)) ] || { bad "pool did not load; ABORTING"; exit 1; }

install -d -m 0755 -o root -g wheel "$CONFDIR"
install -m 0644 -o root -g wheel /tmp/pfocc.rules "$CONF"
LEAF='yb_(src|dst)_([0-9]|[12][0-9]|3[01])'
{ echo "$U ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai_b -t $LEAF -T (add|delete|flush|show)( [0-9a-fA-F.:/]+)*\$"
  echo "$U ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai_b -s rules\$"; } > /tmp/pfocc.sudoers
echo "$U ALL=(root) NOPASSWD: /sbin/pfctl ^-a com\\.apple/yoloai_b -s Tables -vv\$" > /tmp/pfocc.ext
visudo -c -f /tmp/pfocc.sudoers >/dev/null 2>&1 && visudo -c -f /tmp/pfocc.ext >/dev/null 2>&1 \
  || { bad "policy fails visudo -c; ABORTING"; exit 1; }
install -m 0440 -o root -g wheel /tmp/pfocc.sudoers "$SUDOERS"
install -m 0440 -o root -g wheel /tmp/pfocc.ext "$SUDOERS_EXT"   # measurement-only, as before
sudo -u "$U" sudo -K >/dev/null 2>&1

# Preflight the destinations from the HOST before building any VMs. A destination that does not
# answer HTTP would fail the baseline gate and abort the run after n sandboxes had been created —
# correct, but an expensive way to learn that 9.9.9.9 has no web server.
say "O0a PREFLIGHT — which destinations answer HTTP from the host at all"
WORKING=()
for d in "${DESTS[@]}"; do
  c=$(asuser curl -s -o /dev/null -w '%{http_code}' --max-time 5 "http://$d/" 2>/dev/null)
  c=${c//[^0-9]/}; c=${c: -3}; c=${c:-000}
  printf '        %-16s %s\n' "$d" "$c"
  [ "$c" != 000 ] && WORKING+=("$d")
done
DESTS=("${WORKING[@]}")
echo "        ${#DESTS[@]} usable destinations"
if [ "${#DESTS[@]}" -lt "$N" ]; then
  N=${#DESTS[@]}
  unk "only ${#DESTS[@]} destinations answer, so n is reduced to $N — each sandbox needs its own"
fi
[ "$N" -ge 3 ] || { bad "fewer than 3 usable destinations; this run needs n>2. ABORTING"; exit 1; }

git -C "$WD" init -q 2>/dev/null; : > "$WD/README.md"
git -C "$WD" add -A >/dev/null 2>&1
git -C "$WD" -c user.email=spike@local -c user.name=spike commit -qm init >/dev/null 2>&1
chown -R "$U" "$WD"
asuser container system start >/dev/null 2>&1
for ((i=0;i<N && i<SLOTS; i++)); do
  s="occ-$i"
  asuser "$YOLOAI" destroy "$s" --abandon-unapplied >/dev/null 2>&1
  if asuser "$YOLOAI" new "$s" "$WD" --backend apple >/dev/null 2>&1; then
    ip=$(ipof "$s")
    if [ -n "$ip" ]; then
      SBS+=("$s"); IPS+=("$ip"); MADE=$((MADE+1))
      printf '        slot %d: %-8s %-15s allowlist %s\n' "$i" "$s" "$ip" "${DESTS[$i]}"
    else
      SBS+=("$s"); echo "        slot $i: $s created but has no address — excluded"
    fi
  else
    echo "        slot $i: could not create $s — the host stopped here"
    break
  fi
done
echo "        $MADE live sandboxes with addresses"
if [ "$MADE" -lt 3 ]; then
  bad "only $MADE sandboxes — this run exists to test n>2; ABORTING"; exit 1
fi
[ "$MADE" -lt "$N" ] && unk "asked for $N, got $MADE — every count below is against $MADE, not $N"

# Distinct addresses are a precondition: two sandboxes sharing one would make the cross-check
# meaningless in a way that looks like a pass.
dupes=$(printf '%s\n' "${IPS[@]}" | sort | uniq -d | tr '\n' ' ')
if [ -n "$dupes" ]; then bad "duplicate guest addresses ($dupes); ABORTING"; exit 1; fi
ok "$MADE sandboxes, all with distinct addresses"

# ---------------------------------------------------------------------------
say "O0b BASELINE — every sandbox reaches every destination before any rules exist"
base_ok=1
for ((i=0;i<MADE;i++)); do
  row=""
  for ((j=0;j<MADE;j++)); do
    c=$(egress "${SBS[$i]}" "${DESTS[$j]}"); row="$row $c"
    [ "$c" = 000 ] && base_ok=0
  done
  printf '        %-8s ->%s\n' "${SBS[$i]}" "$row"
done
if [ "$base_ok" -eq 1 ]; then
  ok "every sandbox reaches every destination unfiltered — blocks below are attributable"
else
  bad "some path was already dead before any rule existed. Every 'blocked' below would be free."
  echo "           ABORTING — this is the A22 gate, and with $MADE guests it matters more, not less."
  exit 1
fi

# ---------------------------------------------------------------------------
say "O1 INDEPENDENCE AT n=$MADE — each sandbox gets ONE destination, and only its own"
for ((i=0;i<MADE;i++)); do
  pfctl -a "$ANCHOR" -t "yb_src_$i" -T add "${IPS[$i]}"  >/dev/null 2>&1
  pfctl -a "$ANCHOR" -t "yb_dst_$i" -T add "${DESTS[$i]}" >/dev/null 2>&1
done
echo "        all $MADE slots populated"

# MECHANISM CANARY. Run 1 of this script reported 56 of 56 cross-sandbox leaks and blamed the slot
# design. The design was fine: the host's main ruleset had lost its `anchor "com.apple/*"` line, so
# pf never descended into our anchor and nothing was evaluated (pf_anchor_eval.sh). The baseline
# gate could not catch it — it proves the guest HAS a network, not that pf WOULD block. One
# confirmed block before the matrix is the difference between measuring the design and measuring
# the host.
canary=$(egress "${SBS[0]}" "${DESTS[1]}")
echo "        canary: ${SBS[0]} -> another slot's destination ${DESTS[1]} = $canary (must be 000)"
if [ "$canary" != 000 ]; then
  bad "pf is not enforcing on this host at all, so every leak below would be the host's fault"
  echo "           and none of it would be evidence about the slot design. Diagnostic:"
  echo "           main ruleset has $(pfctl -s rules 2>/dev/null | grep -c 'com\.apple/' || echo 0) line(s) referencing com.apple/* — if that is 0,"
  echo "           run pf_anchor_eval.sh, which diagnoses and repairs exactly this. ABORTING."
  exit 1
fi
ok "canary blocked: the mechanism is live, so the matrix measures the design"
echo "        now the full $((MADE*MADE))-cell matrix"
echo "        (diagonal = own destination, must be REACHED; off-diagonal = another's, must be 000)"
own_fail=0; leak=0
for ((i=0;i<MADE;i++)); do
  row=""
  for ((j=0;j<MADE;j++)); do
    c=$(egress "${SBS[$i]}" "${DESTS[$j]}")
    if [ "$i" -eq "$j" ]; then
      [ "$c" = 000 ] && { own_fail=$((own_fail+1)); row="$row [$c!]"; } || row="$row [$c]"
    else
      [ "$c" != 000 ] && { leak=$((leak+1)); row="$row $c!"; } || row="$row $c"
    fi
  done
  printf '        %-8s ->%s\n' "${SBS[$i]}" "$row"
done
echo "        own-destination failures: $own_fail   cross-sandbox leaks: $leak"
if [ "$own_fail" -eq 0 ] && [ "$leak" -eq 0 ]; then
  ok "$MADE sandboxes hold $MADE independent allowlists: every diagonal reached, all"
  echo "           $((MADE*MADE-MADE)) off-diagonal paths refused. Independence is no longer an n=2 claim."
elif [ "$own_fail" -gt 0 ] && [ "$leak" -eq 0 ]; then
  bad "$own_fail sandbox(es) cannot reach their OWN destination — over-blocking, and the"
  echo "           zero leaks below are therefore not evidence of anything (DF172)"
else
  bad "$leak cross-sandbox leak(s): a sandbox reached a destination allowlisted for another."
  echo "           This is the failure the whole slot design exists to prevent."
fi

# ---------------------------------------------------------------------------
say "O2 FREE-SLOT SEARCH — does the one-call dump report exactly the occupied slots?"
echo "        This is how a start path would find a free slot. A wrong answer here puts two"
echo "        sandboxes in one slot, which is the collision the pool exists to avoid."
occ=$(sudo -u "$U" -H sudo -n /sbin/pfctl -a "$ANCHOR" -s Tables -vv 2>/dev/null | python3 -c '
import re,sys
cur,counts=None,{}
for line in sys.stdin:
    m=re.match(r"^\S*\s+(yb_src_(\d+))\b", line)
    if m: cur=int(m.group(2)); continue
    m=re.match(r"^\s+Addresses:\s+(\d+)", line)
    if m and cur is not None:
        counts[cur]=int(m.group(1)); cur=None
print(" ".join(str(k) for k in sorted(counts) if counts[k]>0))
print("FREE " + (" ".join(str(k) for k in sorted(counts) if counts[k]==0) or "none"))' 2>/dev/null)
reported=$(printf '%s' "$occ" | head -1)
free=$(printf '%s' "$occ" | tail -1)
expected=$(seq 0 $((MADE-1)) | tr '\n' ' ' | sed 's/ $//')
echo "        occupied slots reported: ${reported:-<none>}"
echo "        expected:                $expected"
echo "        $free"
if [ "$reported" = "$expected" ]; then
  ok "the dump names exactly the occupied slots, so free-slot search is one call"
else
  bad "occupancy report does not match reality (got '$reported', want '$expected')"
fi

# ---------------------------------------------------------------------------
say "O3 EXHAUSTION — with $MADE of $SLOTS slots taken, is 'full' detectable?"
if [ "$MADE" -eq "$SLOTS" ]; then
  if [ "$free" = "FREE none" ]; then
    ok "the pool reports FULL from the same single call — exhaustion is cheap to detect"
    echo "           What the product should DO about it is still undecided; this only establishes"
    echo "           that it need not be discovered by a failed write."
  else
    bad "pool is full but the dump still reports free slots: $free"
  fi
else
  unk "only $MADE of $SLOTS slots occupied, so exhaustion was not reached — NOT TRIED"
fi

# ---------------------------------------------------------------------------
say "O4 THE SCRUB AT REAL OCCUPANCY — cost model against addresses vmnet assigned"
echo "        pf_scrub_collapse.sh measured 49 + 9.25k ms using planted addresses. Same shape here?"
echo "        The address scrubbed is a NON-MEMBER (192.168.64.200): what is being measured is the"
echo "        cost at k occupied slots, and k is real here. Scrubbing a live sandbox's own address"
echo "        would instead delete it from its slot and silently destroy the matrix above."
sudo -u "$U" -H python3 - "$ANCHOR" "$SLOTS" 192.168.64.200 <<'PY'
import re, statistics, subprocess, sys, time
anchor, slots, addr = sys.argv[1], int(sys.argv[2]), sys.argv[3]
DN = subprocess.DEVNULL
def pf(*a): subprocess.run(["sudo","-n","/sbin/pfctl","-a",anchor]+list(a), stdout=DN, stderr=DN)
def dump():
    return subprocess.run(["sudo","-n","/sbin/pfctl","-a",anchor,"-s","Tables","-vv"],
                          capture_output=True, text=True).stdout
for mode in ("blind", "collapsed"):
    tot, dels = [], []
    for _ in range(5):
        d = 0; t0 = time.perf_counter()
        if mode == "collapsed":
            cur, occupied = None, []
            for line in dump().splitlines():
                m = re.match(r'^\S*\s+(yb_src_(\d+))\b', line)
                if m: cur = int(m.group(2)); continue
                m = re.match(r'^\s+Addresses:\s+(\d+)', line)
                if m and cur is not None:
                    if int(m.group(1)) > 0: occupied.append(cur)
                    cur = None
            for i in occupied:
                pf("-t","yb_src_%d"%i,"-T","delete",addr); d += 1
        else:
            for i in range(slots):
                pf("-t","yb_src_%d"%i,"-T","delete",addr); d += 1
        tot.append((time.perf_counter()-t0)*1000); dels.append(d)
    print("        %-10s %2d deletes: med=%5.1f ms  (n=5)" % (mode, statistics.median(dels), statistics.median(tot)))
PY
ok "scrub timed at $MADE-slot occupancy with $MADE real guests behind those slots"

#!/bin/bash
# ABOUTME: Validates the behavioural check proposed to close the verification gap — and measures
# ABOUTME: what it costs, including whether `block return` makes it nearly free.
#
# Run: sudo bash pf_canary_probe.sh
#
# WHY THIS EXISTS
#   pf-anchor-eval.txt showed all three of D132's start-path checks passing on a host where nothing
#   was enforced, because the deciding state — the main ruleset's `anchor "com.apple/*"` line —
#   is outside everything the grant can read. macos-pf-privileged-path.md now proposes a
#   behavioural check instead of a fourth proxy, on the grounds that every proxy has been observed
#   passing while the property was false.
#
#   That proposal is unvalidated, and recommending an unvalidated fix is the exact mistake this
#   spike has caught three times. So: build it, prove it detects the fault, and price it.
#
#   THE PROBE, concretely. It cannot invent a source address — only a real guest can emit a packet
#   — so it rides the window rule 1b already creates. During acquisition the slot's `dst` is
#   flushed and briefly EMPTY, and an empty `dst` fails closed (D4). So:
#
#       with our address in `src` and `dst` empty, a connection from the guest MUST fail.
#
#   If it succeeds, the rules are not being evaluated. That tests the property — packets — rather
#   than a proxy for it, and needs no new grant.
#
#   C1 DOES IT HOLD ON A HEALTHY HOST? Empty dst must block; populating must restore reach. Both
#      directions, because a probe that always says "blocked" would pass a one-sided test and be
#      worthless.
#   C2 WHAT DOES IT COST? `block drop` discards silently, so a healthy probe waits out its own
#      timeout on EVERY sandbox start. That is the price of the whole proposal and nobody has
#      priced it.
#   C3 DOES `block return` MAKE IT FREE? An RST answers immediately. If the pool used `block
#      return` the probe would cost a round trip instead of a timeout — and, separately, agents'
#      blocked connections would fail fast instead of hanging, which is a UX gain the design has
#      never considered. Measured for correctness as well as speed: return must still BLOCK.
#   C4 DOES IT ACTUALLY DETECT THE FAULT? The whole point. Break the main ruleset and confirm the
#      probe reports "not blocked" where all three D132 checks report healthy.
#
# SAFETY: breaks and repairs the main ruleset; asserts the host is enforcing before exiting.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/../../../../.." && pwd)"
YOLOAI="$REPO/yoloai"
ANCHOR="com.apple/yoloai_b"
SLOTS=4; SLOT=1
SB=cp-a
ALLOW=1.1.1.1; DENY=1.0.0.1
WD=$(mktemp -d /tmp/pfcp-wd.XXXXXX)
PASS=0; FAIL=0; UNKNOWN=0
SB_IP=""; BLOCKMODE="drop"

RESULTS="$HERE/results/pf-canary-probe.txt"
mkdir -p "$(dirname "$RESULTS")"
exec > >(tee "$RESULTS") 2>&1

say() { printf '\n== %s ==\n' "$*"; }
ok()  { PASS=$((PASS+1));       printf '   PASS    %s\n' "$*"; }
bad() { FAIL=$((FAIL+1));       printf '   FAIL    %s\n' "$*"; }
unk() { UNKNOWN=$((UNKNOWN+1)); printf '   UNKNOWN %s\n' "$*"; }
asuser() { sudo -u "$U" -H "$@"; }
quiet_pf() { grep -viE 'use of -f option|main ruleset added|/etc/pf.conf for further|ALTQ|^$'; }
nrules()   { pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -c . || true; }
mainrefs() { pfctl -s rules 2>/dev/null | grep -c 'com\.apple/' || true; }
pfstate()  { pfctl -s info 2>/dev/null | head -1 | awk '{print $2}'; }
ipof() { asuser container inspect "yoloai-cli-$1" 2>/dev/null \
  | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d[0]["status"]["networks"][0].get("ipv4Address","").split("/")[0])' 2>/dev/null; }

# Returns "<http_code> <seconds>". The time is the measurement; the code says which way it went.
try() {
  local dest="$1" maxt="$2" out
  out=$(asuser container exec "yoloai-cli-$SB" curl -s -o /dev/null \
        -w '%{http_code} %{time_total}' --max-time "$maxt" "http://$dest/" 2>/dev/null)
  printf '%s' "${out:-000 0}"
}
code() { printf '%s' "$1" | awk '{print $1}'; }
secs() { printf '%s' "$1" | awk '{printf "%.2f", $2}'; }

load_pool() {   # $1 = drop | return
  BLOCKMODE="$1"
  pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
  { for ((i=0;i<SLOTS;i++)); do
      echo "table <yb_src_$i> persist"; echo "table <yb_dst_$i> persist"
      echo "pass  in quick from <yb_src_$i> to <yb_dst_$i>"
      echo "block $1 in quick from <yb_src_$i> to any"
    done; } > /tmp/pfcp.rules
  pfctl -a "$ANCHOR" -f /tmp/pfcp.rules 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
}
claim()   { pfctl -a "$ANCHOR" -t "yb_src_$SLOT" -T add "$SB_IP" >/dev/null 2>&1; }
populate(){ pfctl -a "$ANCHOR" -t "yb_dst_$SLOT" -T add "$ALLOW" >/dev/null 2>&1; }
empty()   { pfctl -a "$ANCHOR" -t "yb_dst_$SLOT" -T flush >/dev/null 2>&1; }

repair() {
  pfctl -e >/dev/null 2>&1
  pfctl -f /etc/pf.conf 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
  asuser container system stop  >/dev/null 2>&1; sleep 4
  asuser container system start >/dev/null 2>&1; sleep 6
  asuser "$YOLOAI" start "$SB"  >/dev/null 2>&1; sleep 6
  SB_IP=$(ipof "$SB")
  [ -n "$SB_IP" ] && { load_pool "$BLOCKMODE" >/dev/null; claim; populate; }
}

cleanup() {
  echo
  echo "== cleanup =="
  pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
  asuser "$YOLOAI" destroy "$SB" --abandon-unapplied >/dev/null 2>&1 \
    && echo "   destroyed $SB" || echo "   NOTE $SB not destroyed — remove by hand"
  rm -rf "$WD" /tmp/pfcp.*
  echo "   pf=$(pfstate)  main-refs=$(mainrefs)"
  [ "$(mainrefs)" -eq 0 ] && echo "   !! HOST IS FAIL-OPEN — run pf_anchor_eval.sh to repair"
  echo
  echo "pass=$PASS fail=$FAIL unknown=$UNKNOWN"
}
trap cleanup EXIT

echo "host: macOS $(sw_vers -productVersion) $(uname -m)"
echo "date: $(date '+%Y-%m-%d %H:%M:%S %Z') | anchor=$ANCHOR"

# ---------------------------------------------------------------------------
say "C0 SETUP"
git -C "$WD" init -q 2>/dev/null; : > "$WD/README.md"
git -C "$WD" add -A >/dev/null 2>&1
git -C "$WD" -c user.email=spike@local -c user.name=spike commit -qm init >/dev/null 2>&1
chown -R "$U" "$WD"
asuser container system start >/dev/null 2>&1
asuser "$YOLOAI" destroy "$SB" --abandon-unapplied >/dev/null 2>&1
asuser "$YOLOAI" new "$SB" "$WD" --backend apple >/dev/null 2>&1 || { bad "could not create $SB"; exit 1; }
SB_IP=$(ipof "$SB"); [ -n "$SB_IP" ] || { bad "no address; ABORTING"; exit 1; }
echo "        $SB at $SB_IP | main-refs=$(mainrefs)"
[ "$(mainrefs)" -gt 0 ] || { bad "host is already fail-open; run pf_anchor_eval.sh first. ABORTING"; exit 1; }
u=$(try "$ALLOW" 8)
echo "        unfiltered reach, for the timeout budget: code=$(code "$u") time=$(secs "$u")s"
[ "$(code "$u")" != 000 ] || { bad "no egress before any rules; ABORTING"; exit 1; }
ok "baseline: guest reaches $ALLOW in $(secs "$u")s with nothing loaded"

# ---------------------------------------------------------------------------
say "C1 DOES THE PROBE HOLD ON A HEALTHY HOST? (block drop)"
echo "        Both directions. A probe that always says 'blocked' passes a one-sided test and is"
echo "        worth nothing, so the reach half is as load-bearing as the block half."
load_pool drop
claim; empty
p=$(try "$ALLOW" 5)
echo "        dst EMPTY  -> code=$(code "$p") time=$(secs "$p")s   (must be 000)"
populate
q=$(try "$ALLOW" 5)
echo "        dst LOADED -> code=$(code "$q") time=$(secs "$q")s   (must be non-000)"
if [ "$(code "$p")" = 000 ] && [ "$(code "$q")" != 000 ]; then
  ok "the probe discriminates: empty dst fails closed, populated dst reaches"
else
  bad "probe does not discriminate (empty=$(code "$p") populated=$(code "$q"))"
fi

# ---------------------------------------------------------------------------
say "C2 WHAT DOES IT COST? — the timeout a healthy start would pay, with block drop"
echo '        "block drop" discards silently, so there is nothing to answer the probe and it waits'
echo "        out its own timeout. That wait lands on EVERY sandbox start."
empty
for t in 1 2 3; do
  r=$(try "$ALLOW" "$t")
  printf '        --max-time %ss -> code=%s time=%ss\n' "$t" "$(code "$r")" "$(secs "$r")"
done
echo "        against an unfiltered reach of $(secs "$u")s, so a timeout of ~1s is already several"
echo "        times the round trip and still a real addition to a 2.4s sandbox start."
ok "block-drop probe cost measured"

# ---------------------------------------------------------------------------
say "C3 DOES block return MAKE IT FREE? — and does it still block?"
echo "        An RST answers immediately. If it also enforces correctly, the probe costs a round"
echo "        trip rather than a timeout, and agents' blocked connections stop hanging too."
load_pool return
claim; empty
pr=$(try "$ALLOW" 5)
echo "        dst EMPTY  -> code=$(code "$pr") time=$(secs "$pr")s   (must be 000, and FAST)"
populate
qr=$(try "$ALLOW" 5)
echo "        dst LOADED -> code=$(code "$qr") time=$(secs "$qr")s   (must be non-000)"
dr=$(try "$DENY" 5)
echo "        denied dest-> code=$(code "$dr") time=$(secs "$dr")s   (must be 000, and FAST)"
if [ "$(code "$pr")" = 000 ] && [ "$(code "$qr")" != 000 ] && [ "$(code "$dr")" = 000 ]; then
  ok "block return enforces exactly as block drop does — allowed reaches, denied and empty refuse"
  echo "           Compare the times above with C2: that difference is the price of the probe, and"
  echo "           it is also what an agent pays today every time it hits a denied destination."
else
  bad "block return changed enforcement (empty=$(code "$pr") loaded=$(code "$qr") denied=$(code "$dr"))."
  echo "           If it does not block, it cannot be used however fast it is."
fi

# ---------------------------------------------------------------------------
say "C4 DOES THE PROBE DETECT THE FAULT? — the whole reason it exists"
echo "        Break the main ruleset, restore NAT only, then run the probe against a host where"
echo "        all three D132 checks report healthy."
pfctl -F all 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
asuser container system stop  >/dev/null 2>&1; sleep 4
asuser container system start >/dev/null 2>&1; sleep 6
asuser "$YOLOAI" start "$SB"  >/dev/null 2>&1; sleep 6
SB_IP=$(ipof "$SB")
echo "        $SB now at ${SB_IP:-<none>} | main-refs=$(mainrefs)"
if [ -z "$SB_IP" ]; then
  unk "C4: no guest address after the break; cannot probe"
else
  load_pool drop >/dev/null
  claim; empty
  printf '        D132 check 1 pf=%s | check 2 pool=%s/%s rules | check 3 slot=%s\n' \
    "$(pfstate)" "$(nrules)" "$((SLOTS*2))" "$SB_IP"
  f=$(try "$ALLOW" 3)
  echo "        PROBE with dst EMPTY -> code=$(code "$f") time=$(secs "$f")s"
  if [ "$(code "$f")" != 000 ] && [ "$(mainrefs)" -eq 0 ]; then
    ok "PROBE DETECTS THE FAULT. An empty dst reached $ALLOW in $(secs "$f")s, which is only"
    echo "           possible if the rules are not evaluated — on a host where every check yoloAI"
    echo "           can perform reports healthy. This is the check the design needs, and it costs"
    echo "           no privilege at all."
  elif [ "$(mainrefs)" -gt 0 ]; then
    unk "C4: the main ruleset was not actually broken, so nothing was tested"
  else
    bad "C4: the probe did NOT detect the fault (code=$(code "$f")) — it is not a usable check"
  fi
fi

# ---------------------------------------------------------------------------
say "C5 REPAIR AND VERIFY"
BLOCKMODE=drop
repair
a=$(try "$ALLOW" 6); d=$(try "$DENY" 6)
echo "        after repair: allow=$(code "$a") deny=$(code "$d") main-refs=$(mainrefs)"
if [ "$(code "$a")" != 000 ] && [ "$(code "$d")" = 000 ]; then
  ok "host is sound: pf enabled, anchor referenced, enforcement real"
else
  bad "host did NOT end enforcing — run pf_anchor_eval.sh"
fi

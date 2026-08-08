#!/bin/bash
# ABOUTME: One slot pool holding apple AND tart guests at once, each with its own allowlist —
# ABOUTME: the backend-agnostic claim tested at n>2 instead of one guest per backend.
#
# Run: sudo bash pf_mixed_backend.sh          (~5 min; tart guests take ~40s each to create)
#
# WHY THIS EXISTS
#   "Nothing in the design distinguishes the backends because the rules key on source address" is
#   load-bearing — it is why one pool serves every macOS backend. The evidence is `pf-tart-pool.txt`
#   T1/T2/T3: one apple guest and one tart guest, in two slots, on separate vmnet bridges. That is
#   n=1 per backend, and it cannot distinguish "the pool is backend-agnostic" from "two guests
#   happen not to collide".
#
#   pf-pool-occupancy.txt then ran the full matrix at n=8 — but every guest was apple, on one
#   bridge, in one subnet. So the two strong results are orthogonal and neither covers the other:
#   many guests on one bridge, or two guests on two bridges. This run crosses them.
#
#   M1 THE MIXED MATRIX. Three apple guests (192.168.64.x) and two tart guests (192.168.65.x) in
#      five slots with five distinct allowlists. Every guest must reach its own destination and be
#      refused all four others — including across the bridge boundary, which is the case a
#      per-bridge assumption would get wrong.
#   M2 block return ON TART. pf-canary-probe.txt established that `block return` enforces exactly
#      as `block drop` does and answers instantly, on apple. This pool uses it, which both tests
#      that on a second backend and makes twenty blocked cells cost nothing instead of 8s each.
#
# CONTROL (A22): a full unfiltered baseline runs first, and a mechanism canary confirms one real
# block before the matrix is believed — the lesson from pf-pool-occupancy-run1-invalidated.txt,
# where 56 leaks turned out to be a broken host rather than a broken design.
#
# SAFETY: only com.apple/yoloai_b. The main ruleset is never written; its state is checked first,
# because on a fail-open host every cell in this matrix would read as a leak.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/../../../../.." && pwd)"
YOLOAI="$REPO/yoloai"
ANCHOR="com.apple/yoloai_b"
SLOTS=5
WD=$(mktemp -d /tmp/pfmx-wd.XXXXXX)
PASS=0; FAIL=0; UNKNOWN=0
# name:backend, in slot order. Apple first so the cheap guests fail fast if something is wrong.
SPEC=(mx-a0:apple mx-a1:apple mx-a2:apple mx-t0:tart mx-t1:tart)
DESTS=(1.1.1.1 1.0.0.1 1.1.1.2 1.0.0.2 1.1.1.3)
NAMES=(); IPS=(); BKS=(); MADE=0

RESULTS="$HERE/results/pf-mixed-backend.txt"
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

# Backend derived from `yoloai ls`, not passed in: a swapped argument then fails loudly instead of
# silently measuring one guest twice. Same idiom as pf_tart.sh, deliberately.
bk() { asuser "$YOLOAI" ls 2>/dev/null | awk -v n="$1" '$1==n {print $3}'; }
ipof() {
  case "$(bk "$1")" in
    tart)  asuser tart ip "yoloai-cli-$1" 2>/dev/null | tr -d '[:space:]' ;;
    apple) asuser container inspect "yoloai-cli-$1" 2>/dev/null \
             | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d[0]["status"]["networks"][0].get("ipv4Address","").split("/")[0])' 2>/dev/null ;;
    *) printf '' ;;
  esac
}
egress() {
  local c
  case "$(bk "$1")" in
    tart)  c=$(asuser tart exec "yoloai-cli-$1" curl -s -o /dev/null -w '%{http_code}' --max-time 8 "http://$2/" 2>/dev/null) ;;
    apple) c=$(asuser container exec "yoloai-cli-$1" curl -s -o /dev/null -w '%{http_code}' --max-time 8 "http://$2/" 2>/dev/null) ;;
    *) c="" ;;
  esac
  c=${c//[^0-9]/}; c=${c: -3}; printf '%s' "${c:-000}"
}

MAIN_BEFORE=$(pfctl -s rules 2>/dev/null | grep -c . || true)
cleanup() {
  echo
  echo "== cleanup =="
  pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
  for s in "${NAMES[@]:-}"; do
    [ -n "$s" ] && { asuser "$YOLOAI" destroy "$s" --abandon-unapplied >/dev/null 2>&1 \
      && echo "   destroyed $s" || echo "   NOTE $s not destroyed — remove by hand"; }
  done
  rm -rf "$WD" /tmp/pfmx.*
  echo "   anchor rules=$(nrules)"
  echo "   main ruleset: before=$MAIN_BEFORE after=$(pfctl -s rules 2>/dev/null | grep -c . || true), refs=$(mainrefs)"
  echo
  echo "pass=$PASS fail=$FAIL unknown=$UNKNOWN"
}
trap cleanup EXIT

echo "host: macOS $(sw_vers -productVersion) $(uname -m) | RAM $(( $(sysctl -n hw.memsize) / 1073741824 ))GB"
echo "date: $(date '+%Y-%m-%d %H:%M:%S %Z') | anchor=$ANCHOR | slots=$SLOTS"

# ---------------------------------------------------------------------------
say "M0 SETUP — a $SLOTS-slot pool using 'block return', and guests on two backends"
[ "$(mainrefs)" -gt 0 ] || { bad "host is fail-open (main-refs=0); run pf_anchor_eval.sh. ABORTING"; exit 1; }
{ for ((i=0;i<SLOTS;i++)); do
    echo "table <yb_src_$i> persist"; echo "table <yb_dst_$i> persist"
    echo "pass  in quick from <yb_src_$i> to <yb_dst_$i>"
    echo "block return in quick from <yb_src_$i> to any"
  done; } > /tmp/pfmx.rules
pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
pfctl -a "$ANCHOR" -f /tmp/pfmx.rules 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
[ "$(nrules)" -eq $((SLOTS*2)) ] || { bad "pool did not load; ABORTING"; exit 1; }

git -C "$WD" init -q 2>/dev/null; : > "$WD/README.md"
git -C "$WD" add -A >/dev/null 2>&1
git -C "$WD" -c user.email=spike@local -c user.name=spike commit -qm init >/dev/null 2>&1
chown -R "$U" "$WD"
asuser container system start >/dev/null 2>&1
for spec in "${SPEC[@]}"; do
  name="${spec%%:*}"; want="${spec##*:}"
  asuser "$YOLOAI" destroy "$name" --abandon-unapplied >/dev/null 2>&1
  printf '        creating %-7s (%s) ... ' "$name" "$want"
  if asuser "$YOLOAI" new "$name" "$WD" --backend "$want" >/dev/null 2>&1; then
    ip=$(ipof "$name"); got=$(bk "$name")
    if [ -n "$ip" ]; then
      NAMES+=("$name"); IPS+=("$ip"); BKS+=("$got"); MADE=$((MADE+1))
      printf 'ok  backend=%-6s ip=%s\n' "$got" "$ip"
    else
      NAMES+=("$name"); printf 'created but no address — excluded\n'
    fi
  else
    printf 'FAILED to create\n'
  fi
done
echo "        $MADE usable guests"
[ "$MADE" -ge 4 ] || { bad "need at least 4 guests across both backends; got $MADE. ABORTING"; exit 1; }

napple=0; ntart=0
for b in "${BKS[@]}"; do [ "$b" = apple ] && napple=$((napple+1)); [ "$b" = tart ] && ntart=$((ntart+1)); done
echo "        composition: $napple apple, $ntart tart"
if [ "$napple" -lt 2 ] || [ "$ntart" -lt 1 ]; then
  bad "this run is about MIXING backends; got apple=$napple tart=$ntart. ABORTING"; exit 1
fi
dupes=$(printf '%s\n' "${IPS[@]}" | sort | uniq -d | tr '\n' ' ')
[ -n "$dupes" ] && { bad "duplicate guest addresses ($dupes); ABORTING"; exit 1; }
subnets=$(printf '%s\n' "${IPS[@]}" | cut -d. -f1-3 | sort -u | tr '\n' ' ')
echo "        distinct subnets in play: $subnets"
ok "$napple apple + $ntart tart guests, distinct addresses, across $(printf '%s' "$subnets" | wc -w | tr -d ' ') subnet(s)"

# ---------------------------------------------------------------------------
say "M0b BASELINE — every guest reaches every destination before any membership exists"
base_ok=1
for ((i=0;i<MADE;i++)); do
  row=""
  for ((j=0;j<MADE;j++)); do c=$(egress "${NAMES[$i]}" "${DESTS[$j]}"); row="$row $c"; [ "$c" = 000 ] && base_ok=0; done
  printf '        %-7s %-6s ->%s\n' "${NAMES[$i]}" "${BKS[$i]}" "$row"
done
if [ "$base_ok" -eq 1 ]; then
  ok "all $((MADE*MADE)) paths open unfiltered — every block below is attributable"
else
  bad "a path was dead before any rule existed; every block would be free. ABORTING (A22 gate)"
  exit 1
fi

# ---------------------------------------------------------------------------
say "M1 THE MIXED MATRIX — one pool, two backends, $MADE independent allowlists"
for ((i=0;i<MADE;i++)); do
  pfctl -a "$ANCHOR" -t "yb_src_$i" -T add "${IPS[$i]}"   >/dev/null 2>&1
  pfctl -a "$ANCHOR" -t "yb_dst_$i" -T add "${DESTS[$i]}" >/dev/null 2>&1
done
canary=$(egress "${NAMES[0]}" "${DESTS[1]}")
echo "        canary: ${NAMES[0]} -> another slot's destination = $canary (must be 000)"
if [ "$canary" != 000 ]; then
  bad "the mechanism is not live, so nothing below would measure the design. ABORTING"; exit 1
fi
ok "canary blocked: the matrix measures the pool, not the host"
echo "        (diagonal = own destination, must be REACHED; off-diagonal must be 000)"
own_fail=0; leak=0; xleak=0
for ((i=0;i<MADE;i++)); do
  row=""
  for ((j=0;j<MADE;j++)); do
    c=$(egress "${NAMES[$i]}" "${DESTS[$j]}")
    if [ "$i" -eq "$j" ]; then
      [ "$c" = 000 ] && { own_fail=$((own_fail+1)); row="$row [$c!]"; } || row="$row [$c]"
    else
      if [ "$c" != 000 ]; then
        leak=$((leak+1)); row="$row $c!"
        [ "${BKS[$i]}" != "${BKS[$j]}" ] && xleak=$((xleak+1))
      else row="$row $c"; fi
    fi
  done
  printf '        %-7s %-6s ->%s\n' "${NAMES[$i]}" "${BKS[$i]}" "$row"
done
echo "        own-destination failures: $own_fail   leaks: $leak (of which cross-backend: $xleak)"
if [ "$own_fail" -eq 0 ] && [ "$leak" -eq 0 ]; then
  ok "one pool holds $napple apple and $ntart tart guests with $MADE independent allowlists:"
  echo "           every diagonal reached, all $((MADE*MADE-MADE)) off-diagonal paths refused, ACROSS"
  echo "           two vmnet bridges and two subnets. Backend-agnostic is no longer n=1 per backend."
elif [ "$own_fail" -gt 0 ] && [ "$leak" -eq 0 ]; then
  bad "$own_fail guest(s) cannot reach their OWN destination — over-blocking, so the zero leaks"
  echo "           are not evidence of anything (DF172)"
else
  bad "$leak leak(s), $xleak of them CROSS-BACKEND. A cross-backend leak would mean the pool does"
  echo "           not isolate guests on different bridges, which is the claim this run tests."
fi

# ---------------------------------------------------------------------------
say "M2 block return ON TART — does the fast form enforce on the second backend too?"
echo "        pf-canary-probe.txt measured this on apple only. Every 000 in the matrix above was"
echo "        produced by 'block return', so the tart rows are the new evidence."
tart_rows=0
for ((i=0;i<MADE;i++)); do [ "${BKS[$i]}" = tart ] && tart_rows=$((tart_rows+1)); done
if [ "$tart_rows" -gt 0 ] && [ "$leak" -eq 0 ] && [ "$own_fail" -eq 0 ]; then
  ok "block return enforces correctly on tart as well as apple ($tart_rows tart row(s) clean)"
else
  unk "no clean tart rows to draw that conclusion from"
fi

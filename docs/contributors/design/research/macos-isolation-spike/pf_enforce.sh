#!/bin/bash
# ABOUTME: Does the TABLE-based ruleset actually enforce against a live sandbox — the right
# ABOUTME: direction, the pass override, per-sandbox scoping, and clean teardown.
#
# Run: sudo bash pf_enforce.sh <sandbox-A> <sandbox-B>
#   e.g. sudo bash pf_enforce.sh enf-a enf-b     (create them first, --backend apple)
#
# WHY THIS EXISTS
#   Everything measured so far proves the AUTHORIZATION is tight and the ruleset LOADS. None of
#   it proves the ruleset FILTERS. Those are different claims, and the gap between them is where
#   this design can fail open without looking any different.
#
#   Specifically, an earlier draft of the plan wrote `block drop out from <table> to any`,
#   reasoning from pf's last-match-wins ordering. That is very likely wrong: pf applies NAT to
#   outbound traffic, so a rule filtering `out` sees the HOST's address as source and matches
#   nothing. The spike that did enforce used `in quick on <ifc>` — inbound on the bridge, before
#   NAT, where the packet still carries the guest's address. The table form additionally drops
#   the `on <ifc>` qualifier, because interface indices move between backends (DF172, hazard 3).
#   So the exact rule form this plan proposes has never been run. E1 runs both candidates.
#
#   EVERY block assertion is paired with a positive control in the SAME run. A sandbox stranded
#   by the vmnet subnet re-pick refuses every destination for free, which silently invalidated
#   the first generation of the pf harness (DF172). "Egress refused" alone certifies nothing.
#
# WHAT IT TOUCHES, AND HOW IT UNDOES IT
#   pf: loads ONLY into com.apple/yoloai_enf, never the main ruleset (flushing that destroys
#   vmnet NAT for every VM on the host and reloading /etc/pf.conf does not restore it). The EXIT
#   trap flushes the anchor and re-verifies. pf enable/disable state is never touched.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0 <sandbox-A> <sandbox-B>"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"
A_SB="${1:?need sandbox A}"
B_SB="${2:?need sandbox B (the per-sandbox scoping discriminator)}"

ANCHOR="com.apple/yoloai_enf"
ALLOW=1.1.1.1          # allowlisted destination — the positive control
DENY=1.0.0.1           # non-allowlisted destination
PASS=0; FAIL=0; UNKNOWN=0
RESULTS="$(cd "$(dirname "$0")" && pwd)/results/pf-enforce.txt"
mkdir -p "$(dirname "$RESULTS")"
exec > >(tee "$RESULTS") 2>&1

cleanup() {
  echo
  echo "== cleanup =="
  pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
  echo "   anchor rules remaining: $(pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -c . || true)"
  echo "   pf status: $(pfctl -s info 2>/dev/null | head -1)"
  echo "   A egress restored: allow=$(egress "$A_SB" $ALLOW) deny=$(egress "$A_SB" $DENY)"
  chown "$U" "$RESULTS" 2>/dev/null
  echo "   results: $RESULTS"
  sync
}

say() { printf '\n== %s ==\n' "$*"; }
ok()  { PASS=$((PASS+1)); printf '   PASS    %s\n' "$*"; }
bad() { FAIL=$((FAIL+1)); printf '   FAIL    %s\n' "$*"; }
unk() { UNKNOWN=$((UNKNOWN+1)); printf '   UNKNOWN %s\n' "$*"; }

quiet_pf() { grep -viE 'use of -f option|main ruleset added|/etc/pf.conf for further|ALTQ|^$'; }
ipof() { container inspect "yoloai-cli-$1" 2>/dev/null \
           | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d[0]["status"]["networks"][0].get("ipv4Address","").split("/")[0])' 2>/dev/null; }
egress() { container exec "yoloai-cli-$1" curl -s -o /dev/null -w '%{http_code}' --max-time 6 "http://$2/" 2>/dev/null || echo 000; }
flushA() { pfctl -a "$ANCHOR" -F all >/dev/null 2>&1; }

A_IP=$(ipof "$A_SB"); B_IP=$(ipof "$B_SB")
[ -n "$A_IP" ] && [ -n "$B_IP" ] || { echo "could not resolve both sandbox IPs (A=$A_IP B=$B_IP)"; exit 2; }
trap cleanup EXIT

echo "host: $(sw_vers -productVersion) | pf: $(pfctl -s info 2>/dev/null | head -1)"
echo "A=$A_SB($A_IP)  B=$B_SB($B_IP)  allow=$ALLOW deny=$DENY"

# ---------------------------------------------------------------------------
say "E0 BASELINE — both sandboxes reach both destinations with no rules loaded"
# Without this the whole run is unfalsifiable: a sandbox that could never reach anything
# passes every 'blocked' assertion below for free (DF172).
flushA
a_al=$(egress "$A_SB" $ALLOW); a_dn=$(egress "$A_SB" $DENY)
b_al=$(egress "$B_SB" $ALLOW); b_dn=$(egress "$B_SB" $DENY)
echo "        A: allow=$a_al deny=$a_dn | B: allow=$b_al deny=$b_dn"
if [ "$a_al" != "000" ] && [ "$a_dn" != "000" ] && [ "$b_al" != "000" ] && [ "$b_dn" != "000" ]; then
  ok "baseline egress works everywhere — later 'blocked' readings are meaningful"
else
  bad "baseline egress already broken; ABORTING, nothing below could be trusted"
  exit 1
fi

# ---------------------------------------------------------------------------
say "E1 WHICH RULE FORM ACTUALLY FILTERS?"
# Candidate 1 is what the plan proposes. Candidate 2 is the draft that reasoned from
# last-match-wins and is expected to match nothing because NAT rewrites the source on the
# way out. Running both is the point: a form that silently matches nothing is exactly what
# an implementer would ship believing it worked.
try_form() {
  local label=$1 ruleset=$2
  flushA
  printf '%s\n' "$ruleset" | pfctl -a "$ANCHOR" -f - 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
  local loaded; loaded=$(pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -c . || true)
  pfctl -a "$ANCHOR" -t yoloai_src -T add "$A_IP" >/dev/null 2>&1
  pfctl -a "$ANCHOR" -t yoloai_dst -T add "$ALLOW" >/dev/null 2>&1
  local al dn
  al=$(egress "$A_SB" $ALLOW); dn=$(egress "$A_SB" $DENY)
  echo "        [$label] rules=$loaded  A->allow=$al  A->deny=$dn"
  if [ "$dn" = "000" ] && [ "$al" != "000" ]; then
    ok "$label ENFORCES: deny blocked, allow still passes (the pass override works)"
    return 0
  elif [ "$dn" = "000" ] && [ "$al" = "000" ]; then
    bad "$label blocks EVERYTHING — the pass never overrides, so the allowlist is inert"
    return 1
  else
    bad "$label matches nothing — sandbox fully unfiltered (fail-open)"
    return 1
  fi
}

FORM_IN='table <yoloai_src> persist
table <yoloai_dst> persist
pass  in quick from <yoloai_src> to <yoloai_dst>
block drop in quick from <yoloai_src> to any'

FORM_OUT='table <yoloai_src> persist
table <yoloai_dst> persist
block drop out from <yoloai_src> to any
pass  out from <yoloai_src> to <yoloai_dst>'

winner=""
try_form "IN+quick (proposed)" "$FORM_IN" && winner=IN
try_form "OUT+last-match (earlier draft)" "$FORM_OUT" || true
[ -n "$winner" ] || { bad "neither form enforced; the design needs a different rule shape"; exit 1; }

# ---------------------------------------------------------------------------
say "E2 PER-SANDBOX SCOPING — B must be untouched while A is filtered"
flushA
printf '%s\n' "$FORM_IN" | pfctl -a "$ANCHOR" -f - 2>&1 | quiet_pf >/dev/null
pfctl -a "$ANCHOR" -t yoloai_src -T add "$A_IP" >/dev/null 2>&1
pfctl -a "$ANCHOR" -t yoloai_dst -T add "$ALLOW" >/dev/null 2>&1
a_dn=$(egress "$A_SB" $DENY); b_dn=$(egress "$B_SB" $DENY); b_al=$(egress "$B_SB" $ALLOW)
echo "        A->deny=$a_dn | B->deny=$b_dn B->allow=$b_al"
if [ "$a_dn" = "000" ] && [ "$b_dn" != "000" ] && [ "$b_al" != "000" ]; then
  ok "scoped to A only — B on the same bridge is unaffected"
else
  bad "scoping wrong (A_deny=$a_dn B_deny=$b_dn B_allow=$b_al)"
fi

# ---------------------------------------------------------------------------
say "E3 TEARDOWN — removing A from the table restores it, with no rule reload"
pfctl -a "$ANCHOR" -t yoloai_src -T delete "$A_IP" >/dev/null 2>&1
a_dn2=$(egress "$A_SB" $DENY)
echo "        after table delete: A->deny=$a_dn2"
if [ "$a_dn2" != "000" ]; then
  ok "table delete alone restores egress — teardown needs no rule reload"
else
  bad "A still blocked after table delete — teardown is not membership-only"
fi

# ---------------------------------------------------------------------------
say "E4 STALE-ENTRY HAZARD — an orphaned address blocks whoever inherits it"
# The reaping argument in the plan says a stale block entry over-blocks a later occupant.
# B stands in for that occupant: putting B's address in the table is exactly what a missed
# teardown plus an address recycle would produce.
pfctl -a "$ANCHOR" -t yoloai_src -T add "$B_IP" >/dev/null 2>&1
b_dn2=$(egress "$B_SB" $DENY)
echo "        B with a stale entry naming its address: B->deny=$b_dn2"
if [ "$b_dn2" = "000" ]; then
  ok "confirmed: an orphaned entry silently denies its next occupant — reaping is correctness"
else
  unk "stale entry did not block B (b_deny=$b_dn2)"
fi
pfctl -a "$ANCHOR" -t yoloai_src -T delete "$B_IP" >/dev/null 2>&1

printf '\n== TOTALS ==\n   pass=%d fail=%d unknown=%d  winner=%s\n' "$PASS" "$FAIL" "$UNKNOWN" "${winner:-none}"

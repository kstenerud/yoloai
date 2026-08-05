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
  pfctl -a "$ANCHOR/s1" -F all >/dev/null 2>&1
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

# The apple `container` CLI is PER-USER: it talks to a user-level daemon, so under sudo it
# sees root's context, which owns no containers. Every container call must drop back to the
# invoking user or it silently reports nothing — which on the first run looked exactly like
# "the sandboxes do not exist". Same helper the earlier harness used (privileged.sh:55).
asuser() { sudo -u "$U" "$@"; }
ipof() { asuser container inspect "yoloai-cli-$1" 2>/dev/null \
           | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d[0]["status"]["networks"][0].get("ipv4Address","").split("/")[0])' 2>/dev/null; }
# curl -w '%{http_code}' ALREADY prints 000 when the connection fails, and also exits
# non-zero — so a trailing `|| echo 000` emits BOTH and yields "000000". That string does not
# equal "000", so a genuinely blocked destination read as "not blocked" and the first run of
# this harness reported the working rule form as fail-open. Capture, then normalise: any
# non-3-digit or empty result becomes 000.
egress() {
  local c
  c=$(asuser container exec "yoloai-cli-$1" curl -s -o /dev/null -w '%{http_code}' \
        --max-time 6 "http://$2/" 2>/dev/null)
  c=${c//[^0-9]/}
  c=${c: -3}
  case "$c" in ''|*[!0-9]*) c=000;; esac
  printf '%s' "$c"
}
flushA() { pfctl -a "$ANCHOR" -F all >/dev/null 2>&1; }

A_IP=$(ipof "$A_SB"); B_IP=$(ipof "$B_SB")
if [ -z "$A_IP" ] || [ -z "$B_IP" ]; then
  echo "could not resolve both sandbox IPs (A=$A_IP B=$B_IP)"
  exit 2
fi
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

# ---------------------------------------------------------------------------
say "E5 IS rdr IN OUR ANCHOR ACTUALLY EVALUATED? (the stdin form's condemnation)"
# The `-f -` stdin form was ruled out because nat/rdr rules LOAD into the anchor and
# /etc/pf.conf declares nat-anchor/rdr-anchor "com.apple/*". The first half was measured;
# the second — that they are therefore EVALUATED — was inference. If they are inert, the
# condemnation collapses and dynamic per-sandbox rule loading is back on the table.
#
# Decisive test: redirect A's traffic for the DENY host to a dead local port. If rdr is
# evaluated, A can no longer reach DENY. If inert, A reaches it exactly as at baseline.
flushA
IFC=$(ifconfig 2>/dev/null | awk -v ip="$A_IP" '/^[a-z]/{i=$1} $1=="inet" && $2 ~ /^192\.168\./ {split(ip,a,"."); split($2,b,"."); if (a[1]"."a[2]"."a[3]==b[1]"."b[2]"."b[3]) print i}' | head -1)
echo "        A is on host interface: ${IFC:-<unknown>}"
pre=$(egress "$A_SB" $DENY)
printf 'rdr in on %s proto tcp from %s to %s port 80 -> 127.0.0.1 port 9\n' "${IFC:-bridge101}" "$A_IP" "$DENY" \
  | pfctl -a "$ANCHOR" -f - 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
nat_loaded=$(pfctl -a "$ANCHOR" -s nat 2>/dev/null | grep -c . || true)
post=$(egress "$A_SB" $DENY)
echo "        rdr rules in anchor=$nat_loaded | A->deny before=$pre after=$post"
if [ "$pre" != "000" ] && [ "$post" = "000" ]; then
  bad "rdr IS EVALUATED — the stdin grant really can redirect sandbox traffic. Condemnation stands."
elif [ "$pre" != "000" ] && [ "$post" != "000" ] && [ "${nat_loaded:-0}" -ge 1 ]; then
  ok "rdr loads but is INERT here — the stdin form may be safer than assumed; revisit the"
  echo "           slot-pool decision rather than treating it as settled"
else
  unk "inconclusive (pre=$pre post=$post loaded=${nat_loaded:-0})"
fi

# ---------------------------------------------------------------------------
say "E6 NESTED FILTER-ONLY SUB-ANCHOR — dynamic rules without the slot cap?"
# The alternative to a fixed slot pool: the parent anchor declares ONLY a filter
# `anchor "*"` and no nat-anchor/rdr-anchor, then each sandbox gets its own sub-anchor
# loaded from stdin. If sub-anchor filter rules enforce while sub-anchor rdr rules stay
# inert, per-sandbox rules become arbitrary and the concurrent-sandbox cap disappears.
flushA
pfctl -a "$ANCHOR/s1" -F all >/dev/null 2>&1
printf 'anchor "*"\n' | pfctl -a "$ANCHOR" -f - 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
printf 'pass  in quick from %s to %s\nblock drop in quick from %s to any\n' \
  "$A_IP" "$ALLOW" "$A_IP" | pfctl -a "$ANCHOR/s1" -f - 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
sub_rules=$(pfctl -a "$ANCHOR/s1" -s rules 2>/dev/null | grep -c . || true)
s_al=$(egress "$A_SB" $ALLOW); s_dn=$(egress "$A_SB" $DENY); s_b=$(egress "$B_SB" $DENY)
echo "        sub-anchor rules=$sub_rules | A->allow=$s_al A->deny=$s_dn | B->deny=$s_b"
if [ "$s_dn" = "000" ] && [ "$s_al" != "000" ] && [ "$s_b" != "000" ]; then
  ok "sub-anchor filter rules ENFORCE and stay scoped — dynamic per-sandbox rules are viable"
elif [ "$s_dn" != "000" ]; then
  bad "sub-anchor rules did not filter (wildcard anchor may not recurse) — slot pool stands"
else
  unk "inconclusive (allow=$s_al deny=$s_dn B=$s_b)"
fi
# And the security half: is rdr inside the sub-anchor inert, given the parent declares no
# nat-anchor? If so, stdin loading into sub-anchors is safe and the cap can go.
printf 'rdr in on %s proto tcp from %s to %s port 80 -> 127.0.0.1 port 9\n' "${IFC:-bridge101}" "$A_IP" "$ALLOW" \
  | pfctl -a "$ANCHOR/s1" -f - 2>&1 | quiet_pf >/dev/null
sub_al=$(egress "$A_SB" $ALLOW)
echo "        after rdr into sub-anchor: A->allow=$sub_al (was $s_al)"
if [ "$s_al" != "000" ] && [ "$sub_al" = "000" ]; then
  bad "rdr in a sub-anchor IS evaluated — stdin into sub-anchors is NOT safe"
elif [ "$s_al" != "000" ] && [ "$sub_al" != "000" ]; then
  ok "rdr in a sub-anchor is INERT — a filter-only parent contains the stdin grant"
else
  unk "inconclusive (before=$s_al after=$sub_al)"
fi
pfctl -a "$ANCHOR/s1" -F all >/dev/null 2>&1

printf '\n== TOTALS ==\n   pass=%d fail=%d unknown=%d  winner=%s\n' "$PASS" "$FAIL" "$UNKNOWN" "${winner:-none}"

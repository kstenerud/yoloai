#!/bin/bash
# ABOUTME: Is a `rdr` rule loaded into our anchor actually EVALUATED — directly, and inside a
# ABOUTME: filter-only sub-anchor? Decides slot-pool vs dynamic per-sandbox rule loading.
#
# Run: sudo bash pf_rdr.sh <sandbox-A>
#
# WHY A THIRD RUN
#   The `-f -` stdin form was ruled out because nat/rdr rules LOAD into the anchor and
#   /etc/pf.conf declares nat-anchor/rdr-anchor "com.apple/*". Only the first half was ever
#   measured. pf_enforce.sh tried to settle it and could not: it built the rule with the
#   interface name taken from ifconfig's `$1`, which carries a trailing colon ("bridge101:"),
#   so the rule was a syntax error and never loaded. E5 said UNKNOWN. E6 piped the same error
#   to /dev/null and reported "rdr is INERT" from a rule that did not exist — a vacuous PASS.
#
#   THE FIX THAT MATTERS IS NOT THE COLON. Every effect measurement here first asserts the rule
#   LOADED, and aborts if it did not. A rule that fails to load produces exactly the readings of
#   a rule that is inert, which is the whole trap (A22) — and the trap this harness fell into
#   after warning about it in its own sibling.
#
# WHAT IT DECIDES
#   rdr EVALUATED  -> the stdin grant can redirect host traffic. Slot pool stands.
#   rdr INERT      -> dynamic per-sandbox sub-anchors are safe, and the concurrent-sandbox cap
#                     that the slot pool imposes can be dropped.
#
# Touches only com.apple/yoloai_rdr and its s1 sub-anchor; EXIT trap flushes both.

set -u
[ "$(id -u)" -eq 0 ] || { echo "must run as root: sudo bash $0 <sandbox-A>"; exit 2; }
U="${SUDO_USER:?run via sudo, not as a root login}"
A_SB="${1:?need sandbox A}"

ANCHOR="com.apple/yoloai_rdr"
DENY=1.0.0.1
RESULTS="$(cd "$(dirname "$0")" && pwd)/results/pf-rdr.txt"
mkdir -p "$(dirname "$RESULTS")"
exec > >(tee "$RESULTS") 2>&1

asuser() { sudo -u "$U" "$@"; }
ipof() { asuser container inspect "yoloai-cli-$1" 2>/dev/null \
           | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d[0]["status"]["networks"][0].get("ipv4Address","").split("/")[0])' 2>/dev/null; }
egress() {
  local c
  c=$(asuser container exec "yoloai-cli-$1" curl -s -o /dev/null -w '%{http_code}' \
        --max-time 6 "http://$2/" 2>/dev/null)
  c=${c//[^0-9]/}; c=${c: -3}
  case "$c" in ''|*[!0-9]*) c=000;; esac
  printf '%s' "$c"
}
quiet_pf() { grep -viE 'use of -f option|main ruleset added|/etc/pf.conf for further|ALTQ|^$'; }

cleanup() {
  echo
  echo "== cleanup =="
  pfctl -a "$ANCHOR/s1" -F all >/dev/null 2>&1
  pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
  echo "   anchor rules: $(pfctl -a "$ANCHOR" -s rules 2>/dev/null | grep -c . || true) / nat: $(pfctl -a "$ANCHOR" -s nat 2>/dev/null | grep -c . || true)"
  echo "   A egress restored: $(egress "$A_SB" $DENY)"
  chown "$U" "$RESULTS" 2>/dev/null
  echo "   results: $RESULTS"
  sync
}

A_IP=$(ipof "$A_SB")
[ -n "$A_IP" ] || { echo "could not resolve $A_SB ip"; exit 2; }
# ifconfig's interface line is "bridge101: flags=..." — $1 carries the colon. Strip it.
IFC=$(ifconfig 2>/dev/null | awk -v ip="$A_IP" '
  /^[a-z]/ { i=$1; sub(/:$/,"",i) }
  $1=="inet" { split(ip,a,"."); split($2,b,".");
               if (a[1]"."a[2]"."a[3]==b[1]"."b[2]"."b[3]) { print i; exit } }')
[ -n "$IFC" ] || { echo "could not find host interface for $A_IP"; exit 2; }
trap cleanup EXIT

echo "host: $(sw_vers -productVersion) | A=$A_SB($A_IP) on $IFC | deny=$DENY"
echo "pf.conf translation anchors: $(grep -cE '^(nat|rdr)-anchor' /etc/pf.conf) declared for com.apple/*"

probe() { # probe <where> <anchor>
  local where=$1 anch=$2
  echo
  echo "== $where =="
  pfctl -a "$ANCHOR/s1" -F all >/dev/null 2>&1
  pfctl -a "$ANCHOR" -F all >/dev/null 2>&1
  if [ "$anch" = "$ANCHOR/s1" ]; then
    printf 'anchor "*"\n' | pfctl -a "$ANCHOR" -f - 2>&1 | quiet_pf | sed 's/^/        parent: /'
  fi
  local before; before=$(egress "$A_SB" $DENY)
  local rule="rdr on $IFC proto tcp from $A_IP to $DENY port 80 -> 127.0.0.1 port 9"
  printf '%s\n' "$rule" | pfctl -a "$anch" -f - 2>&1 | quiet_pf | sed 's/^/        pfctl: /'
  local loaded; loaded=$(pfctl -a "$anch" -s nat 2>/dev/null | grep -c . || true)
  echo "        rule: $rule"
  echo "        loaded into $anch: ${loaded:-0} translation rule(s)"
  # THE GATE. Without this, "no effect" from a rule that never loaded reads as "inert".
  if [ "${loaded:-0}" -lt 1 ]; then
    echo "   ABORT   the rdr rule did NOT load — any effect reading below would be vacuous"
    return 2
  fi
  if [ "$before" = "000" ]; then
    echo "   ABORT   baseline already blocked; cannot detect a redirect"
    return 2
  fi
  local after; after=$(egress "$A_SB" $DENY)
  echo "        A->deny before=$before after=$after"
  if [ "$after" = "000" ]; then
    echo "   RESULT  rdr IS EVALUATED here — traffic was redirected to a dead port"
    return 1
  fi
  echo "   RESULT  rdr is INERT here — loaded, but the packet path ignored it"
  return 0
}

probe "R1 rdr loaded DIRECTLY into com.apple/yoloai_rdr" "$ANCHOR"; r1=$?
probe "R2 rdr loaded into a SUB-anchor of a filter-only parent" "$ANCHOR/s1"; r2=$?

echo
echo "== VERDICT =="
case "$r1:$r2" in
  1:1) echo "   Both evaluated. The stdin grant can redirect traffic wherever it is loaded."
       echo "   => SLOT POOL STANDS; dynamic rule loading stays out." ;;
  1:0) echo "   Evaluated directly, INERT in a filter-only sub-anchor."
       echo "   => Dynamic per-sandbox sub-anchors are safe. The concurrent-sandbox cap can go,"
       echo "      provided the parent declares only a filter anchor and never nat/rdr." ;;
  0:0) echo "   Inert in both. The original condemnation of '-f -' was wrong on this host, and"
       echo "      the slot pool is solving a problem that does not exist here. Re-open the"
       echo "      decision — but weigh that this is one host's pf.conf, not a guarantee." ;;
  *)   echo "   Inconclusive (r1=$r1 r2=$r2) — see the ABORT lines above. Nothing is decided." ;;
esac

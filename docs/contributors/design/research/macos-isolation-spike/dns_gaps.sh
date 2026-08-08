#!/bin/bash
# ABOUTME: The two gaps dns-parity.txt named and did not close — split-horizon names the guest's
# ABOUTME: resolver cannot see, and whether a once-resolved allowlist decays as CDNs rotate.
#
# Run: bash dns_gaps.sh          (unprivileged; takes ~65 min, safe to run detached)
#
# WHY THIS EXISTS
#   Under host `pf`, the allowlist becomes a table of ADDRESSES resolved on the HOST, where today
#   the guest resolves names itself. dns-parity.txt measured that swap over five public domains and
#   found no divergence — then named two limits it could not close, both of which decide whether an
#   allowlist entry means what the user wrote:
#
#   C SPLIT-HORIZON. "a MagicDNS or VPN-internal host the guest's resolver cannot see at all is the
#     case most likely to diverge and was not exercised." This host runs Tailscale with MagicDNS as
#     its lead resolver and a real tailnet behind it, so the case is here rather than hypothetical.
#     The failure it would produce is fail-CLOSED and silent: the host resolves the name to a
#     tailnet address, writes that into `dst`, and the guest — resolving the same name differently
#     or not at all — sends somewhere the table does not list. The user allowlisted a domain and
#     the sandbox still cannot reach it.
#
#   D ROTATION. "Resolution is one-shot, so parity at start says nothing about hour six."
#     `resolve_domains` runs once and CDN addresses move underneath a long-lived sandbox. Inherited
#     from the in-guest ipset rather than caused by pf — but a table loaded once is exactly as stale
#     as an ipset loaded once, and the pf design's own sandboxes are the long-lived kind.
#
#   Both sides use the call the product uses — socket.getaddrinfo(AF_INET),
#   runtime/docker/resources/firewall.py:73 — rather than dig on one side and getaddrinfo on the
#   other, which measures the tools instead of the split. That is dns_parity.sh's convention and it
#   is kept deliberately.
#
# CONTROL (A22). A guest that resolves NOTHING makes every split-horizon name "diverge" for free.
# Public domains are resolved on both sides first and the run ABORTS if they do not agree, so a
# broken resolver cannot be reported as a finding about split DNS.
#
# INTERFERENCE. Phase D polls the HOST only, because the pf experiments running alongside it
# repeatedly destroy vmnet's NAT; guest-side readings taken during that window would record those
# outages as DNS drift. The guest is read once at the start and once at the end, and the end read
# is reported as best-effort with that caveat attached.
#
# PRIVACY. This resolves the operator's private tailnet names. yoloAI is a public repo, so the
# tailnet suffix, peer and host short names, and CGNAT (100.64/10) addresses are redacted from all
# output. What the run is evidence for — resolved, unresolved, or divergent — survives redaction
# intact; the literal names are not the finding.

set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/../../../../.." && pwd)"
YOLOAI="$REPO/yoloai"
TS=/Applications/Tailscale.app/Contents/MacOS/Tailscale
SB=dns-g
VM="yoloai-cli-$SB"
WD=$(mktemp -d /tmp/dnsgap-wd.XXXXXX)
TICKS=12; INTERVAL=300        # 12 x 5 min = 60 min
PUBLIC="example.com github.com api.anthropic.com registry.npmjs.org objects.githubusercontent.com"
ROTATE="api.anthropic.com registry.npmjs.org github.com objects.githubusercontent.com example.com"
PASS=0; FAIL=0; UNKNOWN=0

# --- redaction, established before any output is produced -------------------
TAILNET=$($TS status --json 2>/dev/null | python3 -c 'import json,sys; print(json.load(sys.stdin).get("MagicDNSSuffix",""))' 2>/dev/null)
SELF_FQDN=$($TS status --json 2>/dev/null | python3 -c 'import json,sys; print(json.load(sys.stdin)["Self"]["DNSName"].rstrip("."))' 2>/dev/null)
PEER_FQDN=$($TS status --json 2>/dev/null | python3 -c '
import json,sys
p=[v["DNSName"].rstrip(".") for v in json.load(sys.stdin).get("Peer",{}).values()]
print(p[0] if p else "")' 2>/dev/null)
SELF_SHORT=${SELF_FQDN%%.*}; PEER_SHORT=${PEER_FQDN%%.*}
HOST_LOCAL="$(hostname -s).local"
HOST_SHORT=$(hostname -s)

RESULTS="$HERE/results/dns-gaps.txt"
mkdir -p "$(dirname "$RESULTS")"
# BSD sed's case-insensitive flag is not portable enough to bet a privacy guarantee on, and the
# names arrive in two cases — Tailscale reports them lowercased, `hostname -s` does not. So each
# name is redacted in both cases explicitly rather than with a flag that might be a no-op.
lc() { printf '%s' "$1" | tr '[:upper:]' '[:lower:]'; }
# -l is line-buffered. Without it sed block-buffers when its stdout is a file, so a 65-minute run
# holds its output in a 4KB buffer and loses it entirely if interrupted — which is how this
# directory lost two rounds of reboot data. The flag is the whole reason the log survives a kill.
redact() {
  sed -l -E \
    -e "s/${TAILNET:-__none__}/<tailnet>/g" \
    -e "s/${PEER_SHORT:-__none__}/peer1/g" \
    -e "s/$(lc "${PEER_SHORT:-__none__}")/peer1/g" \
    -e "s/${SELF_SHORT:-__none__}/thishost/g" \
    -e "s/$(lc "${SELF_SHORT:-__none__}")/thishost/g" \
    -e "s/${HOST_SHORT:-__none__}/thishost/g" \
    -e "s/$(lc "${HOST_SHORT:-__none__}")/thishost/g" \
    -e 's/100\.(6[4-9]|[7-9][0-9]|1[0-1][0-9]|12[0-7])\.[0-9]+\.[0-9]+/100.x.x.x(cgnat)/g'
}
exec > >(redact | tee "$RESULTS") 2>&1

say() { printf '\n== %s ==\n' "$*"; }
ok()  { PASS=$((PASS+1));       printf '   PASS    %s\n' "$*"; }
bad() { FAIL=$((FAIL+1));       printf '   FAIL    %s\n' "$*"; }
unk() { UNKNOWN=$((UNKNOWN+1)); printf '   UNKNOWN %s\n' "$*"; }

cleanup() {
  echo
  echo "== cleanup =="
  "$YOLOAI" destroy "$SB" --abandon-unapplied >/dev/null 2>&1 \
    && echo "   destroyed sandbox $SB" || echo "   NOTE sandbox $SB not destroyed — remove by hand"
  rm -rf "$WD"
  echo
  echo "pass=$PASS fail=$FAIL unknown=$UNKNOWN"
  sleep 1        # let the redact|tee pipeline drain before the shell exits
}
trap cleanup EXIT

PYPROG='
import socket,sys
out=set()
for d in sys.argv[1:]:
    try:
        for r in socket.getaddrinfo(d,None,socket.AF_INET): out.add(r[4][0])
    except OSError: out.add("UNRESOLVED")
print(" ".join(sorted(out)) if out else "UNRESOLVED")'
hostres()  { python3 -c "$PYPROG" "$@" 2>/dev/null; }
guestres() { container exec "$VM" python3 -c "$PYPROG" "$@" 2>/dev/null; }

echo "host: macOS $(sw_vers -productVersion) $(uname -m)"
echo "date: $(date '+%Y-%m-%d %H:%M:%S %Z')"
echo "redaction active: tailnet suffix, peer/host short names and 100.64/10 addresses are masked"

# ---------------------------------------------------------------------------
say "SETUP"
git -C "$WD" init -q 2>/dev/null; : > "$WD/README.md"
git -C "$WD" add -A >/dev/null 2>&1
git -C "$WD" -c user.email=spike@local -c user.name=spike commit -qm init >/dev/null 2>&1
container system start >/dev/null 2>&1
"$YOLOAI" destroy "$SB" --abandon-unapplied >/dev/null 2>&1
"$YOLOAI" new "$SB" "$WD" --backend apple >/dev/null 2>&1 || { bad "could not create $SB; ABORTING"; exit 1; }
echo "        host resolvers:  $(scutil --dns | awk '/^resolver #1$/,/^$/' | grep nameserver | head -3 | awk '{print $3}' | tr '\n' ' ')"
# shellcheck disable=SC2016  # deliberate: the awk body runs in the GUEST via sh -c,
# so $2 must reach it unexpanded; expanding it here would substitute the host's value.
echo "        guest resolvers: $(container exec "$VM" sh -c 'grep ^nameserver /etc/resolv.conf | awk "{print \$2}" | tr "\n" " "' 2>/dev/null)"

# ---------------------------------------------------------------------------
say "C0 CONTROL — public domains must agree before any divergence is believable"
ctl_ok=1
for d in $PUBLIC; do
  h=$(hostres "$d"); g=$(guestres "$d")
  if [ "$h" = UNRESOLVED ] || [ -z "$h" ] || [ "$g" = UNRESOLVED ] || [ -z "$g" ]; then
    echo "        $d: host='$h' guest='$g'  <-- one side failed"
    ctl_ok=0
  else
    # Subset, not equality: CDNs return rotating subsets, which is phase D's subject.
    miss=$(python3 -c "
import sys
h=set('''$h'''.split()); g=set('''$g'''.split())
print(' '.join(sorted(g-h)) or 'none')")
    echo "        $d: host=$(printf '%s' "$h" | wc -w | tr -d ' ') addrs, guest=$(printf '%s' "$g" | wc -w | tr -d ' ') addrs, guest-not-in-host: $miss"
  fi
done
if [ "$ctl_ok" -eq 1 ]; then
  ok "both resolvers answer for public names, so a split-horizon miss below is attributable"
else
  bad "a public control failed to resolve on one side; every C result would be free. ABORTING"
  exit 1
fi

# ---------------------------------------------------------------------------
say "C SPLIT-HORIZON — names the host's resolver sees and the guest's may not"
echo "        This is the case dns-parity.txt named as most likely to diverge. Under host pf the"
echo "        HOST's answer is what lands in the dst table, so a name only the host can resolve"
echo "        produces an allowlist entry pointing where the guest will never send."
split_div=0
check_split() {
  local label="$1" name="$2"
  [ -n "$name" ] || { unk "$label: no such name on this host — NOT TRIED"; return; }
  local h g; h=$(hostres "$name"); g=$(guestres "$name")
  printf '        %-26s host=%-28s guest=%s\n' "$label" "${h:-<empty>}" "${g:-<empty>}"
  if [ "$h" != UNRESOLVED ] && [ -n "$h" ] && { [ "$g" = UNRESOLVED ] || [ -z "$g" ]; }; then
    split_div=$((split_div+1))
    echo "             ==> HOST-ONLY. Allowlisting this name writes an address into dst that the"
    echo "                 guest cannot even resolve, so the entry is inert and the sandbox is"
    echo "                 blocked from a domain the user explicitly allowed. Fail-closed, silent."
  elif [ "$h" != UNRESOLVED ] && [ "$g" != UNRESOLVED ] && [ -n "$h" ] && [ -n "$g" ] && [ "$h" != "$g" ]; then
    split_div=$((split_div+1))
    echo "             ==> DIVERGED. Both sides resolve and they DISAGREE — the worst case: dst"
    echo "                 holds the host's answer, the guest sends to its own, and the packet is"
    echo "                 dropped by a rule the user believes permits it."
  elif [ "$h" = UNRESOLVED ] && [ "$g" = UNRESOLVED ]; then
    echo "             ==> neither side resolves; no evidence either way"
  else
    echo "             ==> agree"
  fi
}
check_split "tailnet FQDN (self)"  "$SELF_FQDN"
check_split "tailnet FQDN (peer)"  "$PEER_FQDN"
check_split "bare name + search"   "$PEER_SHORT"
check_split "mDNS .local"          "$HOST_LOCAL"
if [ "$split_div" -gt 0 ]; then
  bad "$split_div split-horizon name(s) do not mean the same thing on both sides. Host-side"
  echo "           resolution is therefore NOT a transparent substitution, and the pf design needs a"
  echo "           stated answer for names the guest resolves differently or not at all."
else
  ok "no split-horizon divergence found among the names tried on this host"
fi

# ---------------------------------------------------------------------------
say "D ROTATION — does a once-resolved allowlist decay? (host-side, $((TICKS*INTERVAL/60)) min)"
echo "        Host-side only, deliberately: pf experiments running alongside this repeatedly"
echo "        destroy vmnet NAT, and guest readings taken through that would record the outage as"
echo "        DNS drift. The guest is sampled once now and once at the end, best-effort."
echo
echo "        guest at start:"
for d in $ROTATE; do printf '          %-34s %s\n' "$d" "$(guestres "$d")"; done
echo
declare -a FIRST
i=0
for d in $ROTATE; do FIRST[i]=$(hostres "$d"); i=$((i+1)); done
echo "        t=0 baseline recorded"
drifted=0
for ((t=1; t<=TICKS; t++)); do
  sleep "$INTERVAL"
  i=0; line=""
  for d in $ROTATE; do
    cur=$(hostres "$d")
    new=$(python3 -c "
import sys
a=set('''${FIRST[$i]}'''.split()); b=set('''$cur'''.split())
print(len(b-a))")
    gone=$(python3 -c "
import sys
a=set('''${FIRST[$i]}'''.split()); b=set('''$cur'''.split())
print(len(a-b))")
    [ "$new" != 0 ] || [ "$gone" != 0 ] && drifted=1
    line="$line $d:+$new/-$gone"
    i=$((i+1))
  done
  printf '        t=%-3s min%s\n' "$((t*INTERVAL/60))" "$line"
done
echo
echo "        guest at end (best-effort; pf experiments may have run in this window):"
for d in $ROTATE; do printf '          %-34s %s\n' "$d" "$(guestres "$d")"; done
echo
if [ "$drifted" -eq 1 ]; then
  bad "at least one domain's address set MOVED within $((TICKS*INTERVAL/60)) minutes. A dst table"
  echo "           resolved once at start is stale by the end of an ordinary session, and the"
  echo "           sandbox is blocked from a domain it was allowlisted for. This is inherited from"
  echo "           the one-shot in-guest ipset rather than caused by pf — but pf inherits it, and"
  echo "           the design should say whether it re-resolves or accepts the decay."
else
  ok "no address left or joined any set over $((TICKS*INTERVAL/60)) minutes on this host"
  echo "           A negative over one hour on one resolver. It does not cover hour six, a"
  echo "           different CDN edge, or a resolver with shorter TTLs."
fi

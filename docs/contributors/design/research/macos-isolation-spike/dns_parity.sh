#!/bin/bash
# ABOUTME: Measures whether an apple guest and its host resolve the same domain to the same IPs,
# ABOUTME: which decides whether host-resolved pf allowlists would block allowlisted traffic.
#
# Run: bash dns_parity.sh [sandbox-name]        (no sudo; creates the sandbox if absent)
#
# WHY THIS DECIDES SOMETHING
#   Today the allowlist is resolved INSIDE the sandbox: `resolve_domains` calls
#   socket.getaddrinfo(domain, None, AF_INET) from the guest
#   (`runtime/docker/resources/firewall.py:63`, driven by entrypoint.py), and the live-patch path
#   runs `dig +short A` in the guest too (`network.go:272`). The host-`pf` design moves that
#   resolution to the HOST, because a pf table holds addresses and the host is what loads it.
#   So the guest keeps resolving for itself while the authorization is computed somewhere else.
#   Where the two disagree, the guest connects to an address the host never authorized and is
#   blocked **while allowlisted** — a deny that looks like a product bug and reads, in the guest,
#   as a network failure with no explanation.
#
#   apple only. tart and seatbelt never had in-guest resolution, so they do not have two answers
#   to disagree about.
#
# WHAT COUNTS AS THE FINDING
#   guest \ host — addresses the guest would use that the host never put in the table. Those are
#   the blocked ones, and they are the reason this test exists. host \ guest is over-authorization:
#   wasteful, not user-visible.
#
# READ THE CAVEAT BEFORE QUOTING ANY NUMBER
#   Divergence depends on the HOST's resolver configuration, which is not a property of yoloAI.
#   This script prints both sides' nameservers precisely so a result is never quoted without them.
#   A host running a split-horizon or overlay resolver (Tailscale MagicDNS, a corporate VPN) can
#   diverge on domains a plain host would agree on. That is a realistic configuration rather than
#   an exotic one, but it is a configuration, and a clean run here does not mean the hazard is
#   absent for other users.

set -u

BOX=${1:-dns-parity}
ROUNDS=3                   # per side, to catch rotation within a single measurement
DOMAINS="api.anthropic.com registry.npmjs.org github.com objects.githubusercontent.com example.com"

die() { printf '\nABORT: %s\n' "$1" >&2; exit 1; }
command -v yoloai >/dev/null || die "yoloai not on PATH"
command -v container >/dev/null || die "container not on PATH"

printf '=== host/guest DNS parity on the apple backend ===\n'
printf 'date   : %s\nsandbox: %s\nrounds : %s per side\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$BOX" "$ROUNDS"

if ! yoloai files "$BOX" path >/dev/null 2>&1; then
  printf 'creating apple sandbox %s (this takes a few minutes)\n' "$BOX"
  WORKDIR=$(mktemp -d)
  git -C "$WORKDIR" init -q
  printf 'ORIGINAL\n' > "$WORKDIR/README.md"
  git -C "$WORKDIR" add -A
  git -C "$WORKDIR" -c user.email=spike@example.com -c user.name=spike commit -qm init
  yoloai new --backend apple "$BOX" "$WORKDIR" || die "yoloai new failed"
fi
VM="yoloai-cli-$BOX"

# The resolver each side uses is the whole explanation for any divergence, so it is recorded as
# data rather than left to the reader to reconstruct.
printf '\n== resolvers (the explanation for anything below) ==\n'
printf '  guest /etc/resolv.conf : %s\n' \
  "$(container exec "$VM" sh -c 'grep ^nameserver /etc/resolv.conf | tr "\n" " "' 2>/dev/null)"
printf '  host scutil            : %s\n' \
  "$(scutil --dns 2>/dev/null | awk '/nameserver\[0\]/{print $3}' | sort -u | tr '\n' ' ')"

# Both sides use the SAME call the product uses — socket.getaddrinfo(AF_INET) — rather than dig on
# one side and getaddrinfo on the other, which would measure the tools instead of the split.
PYPROG='import socket,sys
out=set()
for d in sys.argv[1:]:
    try:
        for r in socket.getaddrinfo(d,None,socket.AF_INET): out.add(d+" "+r[4][0])
    except OSError: out.add(d+" UNRESOLVED")
print("\n".join(sorted(out)))'

hostres() { python3 -c "$PYPROG" "$@" 2>/dev/null; }
guestres() { container exec "$VM" python3 -c "$PYPROG" "$@" 2>/dev/null; }

HOSTF=$(mktemp); GUESTF=$(mktemp)
trap 'rm -f "$HOSTF" "$GUESTF"' EXIT
# shellcheck disable=SC2086  # DOMAINS is a deliberate word-split list
for _ in $(seq 1 "$ROUNDS"); do
  hostres $DOMAINS >> "$HOSTF"
  guestres $DOMAINS >> "$GUESTF"
done
sort -u -o "$HOSTF" "$HOSTF"
sort -u -o "$GUESTF" "$GUESTF"

printf '\n== per-domain comparison ==\n'
diverged=0; agreed=0; unresolved=0
for d in $DOMAINS; do
  h=$(awk -v d="$d" '$1==d {print $2}' "$HOSTF" | sort -u | tr '\n' ' ')
  g=$(awk -v d="$d" '$1==d {print $2}' "$GUESTF" | sort -u | tr '\n' ' ')
  # guest-only: what the guest would use and the host never authorized. This is the finding.
  only_g=$(comm -13 <(awk -v d="$d" '$1==d {print $2}' "$HOSTF" | sort -u) \
                    <(awk -v d="$d" '$1==d {print $2}' "$GUESTF" | sort -u) | tr '\n' ' ')
  printf '\n  %s\n' "$d"
  printf '    host : %s\n' "${h:-<none>}"
  printf '    guest: %s\n' "${g:-<none>}"
  if [ "$h" = "UNRESOLVED " ] || [ "$g" = "UNRESOLVED " ]; then
    printf '    ==> UNRESOLVED on one side; not evidence either way\n'; unresolved=$((unresolved + 1)); continue
  fi
  if [ -n "$only_g" ]; then
    printf '    ==> GUEST-ONLY (would be BLOCKED by a host-resolved allowlist): %s\n' "$only_g"
    diverged=$((diverged + 1))
  else
    printf '    ==> every guest address is in the host set\n'
    agreed=$((agreed + 1))
  fi
done

printf '\n== TOTALS ==\n'
printf '  domains=%d  diverged(guest-only present)=%d  agreed=%d  unresolved=%d\n' \
  "$((diverged + agreed + unresolved))" "$diverged" "$agreed" "$unresolved"
if [ "$diverged" -gt 0 ]; then
  printf '  ==> host-resolved allowlists WOULD block allowlisted traffic on this host.\n'
else
  printf '  ==> no divergence observed here. Note example.com is the control: if it were the ONLY\n'
  printf '      agreement, the comparison would be suspect. Re-read the resolver lines above.\n'
fi
printf '\nCleanup: yoloai destroy %s --abandon-unapplied\n' "$BOX"

#!/bin/bash
# ABOUTME: Decides DF181's open mechanism — whether the repair's STALE verdict is premature
# ABOUTME: (invalidation worked, the re-digest read too early) or the invalidation truly failed.
#
# Run: bash df181_timing.sh [trials] [sandbox-name]      (no sudo; creates the sandbox if absent)
#
# THE QUESTION DF181 LEFT OPEN
#   `files put --overwrite` over a path the tart guest has read fails ~2 times in 3, saying the
#   sandbox "still reads stale content ... and it could not be refreshed" and prescribing a
#   restart. Underneath, RefreshGuestFiles runs `guestRefreshAttempts = 3` passes, each a FRESH
#   guest process that msync(MS_INVALIDATE)s the path and re-digests it
#   (`runtime/tart/guestrefresh.go:113-137`). Two explanations survive that observation and they
#   imply different fixes:
#
#     H1 PREMATURE VERDICT — the invalidation works, but the re-digest inside the same invocation
#        reads before the effect is visible. tart's revalidation is a ~1 s quantised tick, and
#        three back-to-back passes fit inside one. Fix: delay before re-digesting.
#     H2 INVALIDATION INSUFFICIENT — three passes genuinely are not enough for this file/size,
#        and more passes (not more time) are what converges it. Fix: raise the bound.
#
# THE DISCRIMINATOR, AND WHY IT IS THIS ONE
#   After a FAILED put, three invalidation passes have already run. So:
#     - If the guest becomes correct on its own, with NO further put and NO further pass, the
#       invalidation had already worked and only the verdict was wrong  -> H1.
#     - If it stays wrong until another put (i.e. more passes) runs, passes are the variable -> H2.
#   That single observation separates them, and it costs one sleep.
#
#   P0 is the control that makes it readable: the same wait against a path whose replacement was
#   never repaired at all (written directly, bypassing the CLI) must STAY wrong. DF175 measured
#   exactly that — no convergence in 60 s — so if P0 self-heals here, this guest is not exhibiting
#   the defect today and nothing below is evidence.

set -u

TRIALS=${1:-5}
BOX=${2:-df181-timing}
A20=AAAAAAAAAAAAAAAAAAAA
Z3=ZZZ
SETTLE=3                   # seconds to wait with the host doing nothing at all

die() { printf '\nABORT: %s\n' "$1" >&2; exit 1; }
command -v yoloai >/dev/null || die "yoloai not on PATH"
command -v tart >/dev/null || die "tart not on PATH"

printf '=== DF181: is the STALE verdict premature, or is the invalidation insufficient? ===\n'
printf 'date   : %s\ntrials : %s\nsandbox: %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$TRIALS" "$BOX"

if ! yoloai files "$BOX" path >/dev/null 2>&1; then
  printf 'creating tart sandbox %s (this takes a few minutes)\n' "$BOX"
  WORKDIR=$(mktemp -d)
  git -C "$WORKDIR" init -q
  printf 'ORIGINAL\n' > "$WORKDIR/README.md"
  git -C "$WORKDIR" add -A
  git -C "$WORKDIR" -c user.email=spike@example.com -c user.name=spike commit -qm init
  yoloai new --backend tart "$BOX" "$WORKDIR" || die "yoloai new failed"
fi

VM="yoloai-cli-$BOX"
EX=$(yoloai files "$BOX" path) || die "cannot resolve exchange dir"
GEX="/Volumes/My Shared Files/rw/files"
STAGE=$(mktemp -d)
trap 'rm -rf "$STAGE"' EXIT
RUN=$(date '+%H%M%S')

gcat() { tart exec "$VM" cat "$GEX/$1" 2>/dev/null || true; }
put() { # put <name> <content> -> prints rc, never dies: a failure IS the measurement here
  printf '%s' "$2" > "$STAGE/$1"
  yoloai files "$BOX" put "$STAGE/$1" --overwrite >/dev/null 2>&1
  printf '%d' "$?"
}

# --- P0: the control — an UNREPAIRED replacement must stay wrong across the same wait ---------
printf '\n== P0  control: a replacement written DIRECTLY (never repaired) must not self-heal\n'
C=ctl-$RUN.txt
printf '%s' "$A20" > "$STAGE/$C"
yoloai files "$BOX" put "$STAGE/$C" --overwrite >/dev/null 2>&1 || die "seed put failed"
printf '  guest v1                      : %s\n' "$(gcat "$C")"
printf '%s' "$Z3" > "$EX/$C"                     # direct write: no repair pass ever runs
sleep "$SETTLE"
CTL=$(gcat "$C")
printf '  guest after %ss, unrepaired    : %s\n' "$SETTLE" "${CTL:-<empty>}"
if [ "$CTL" = "$Z3" ]; then
  printf '  ==> CONTROL FAILED: an unrepaired path self-healed, so a "self-healed" result below\n'
  printf '      would mean nothing. This guest is not exhibiting DF175 right now.\n'
  die "control invalid"
fi
printf '  ok: unrepaired stays stale, so self-healing below is attributable to the repair passes\n'

# --- the trials ------------------------------------------------------------------------------
printf '\n== trials: put v1, guest reads it, then replace and watch what the verdict was worth\n'
fails=0; healed=0; needed_more=0
for i in $(seq 1 "$TRIALS"); do
  N="t$i-$RUN.txt"
  printf '%s' "$A20" > "$STAGE/$N"
  yoloai files "$BOX" put "$STAGE/$N" --overwrite >/dev/null 2>&1 || die "seed put failed for $N"
  v1=$(gcat "$N")
  [ "$v1" = "$A20" ] || { printf 'trial %d: guest never read v1 (got %s) — skipping\n' "$i" "${v1:-<empty>}"; continue; }

  rc=$(put "$N" "$Z3")
  if [ "$rc" = 0 ]; then
    printf 'trial %d: put rc=0 (repair succeeded first time)\n' "$i"
    continue
  fi
  fails=$((fails + 1))
  after=$(gcat "$N")
  sleep "$SETTLE"
  settled=$(gcat "$N")
  printf 'trial %d: put rc=1 | guest at verdict: %-22s | after %ss idle: %s\n' \
    "$i" "${after:-<empty>}" "$SETTLE" "${settled:-<empty>}"
  if [ "$settled" = "$Z3" ]; then
    healed=$((healed + 1))
  else
    rc2=$(put "$N" "$Z3")
    again=$(gcat "$N")
    printf '          still stale after idling; another put rc=%s -> guest: %s\n' "$rc2" "${again:-<empty>}"
    needed_more=$((needed_more + 1))
  fi
done

printf '\n== VERDICT ==\n'
printf '  trials=%s  puts that reported STALE=%d\n' "$TRIALS" "$fails"
printf '  of those: self-healed while idle=%d   needed further passes=%d\n' "$healed" "$needed_more"
if [ "$fails" -eq 0 ]; then
  printf '  ==> DF181 did not reproduce in this run; the rate is not 100%%, so re-run before concluding.\n'
elif [ "$healed" -gt 0 ] && [ "$needed_more" -eq 0 ]; then
  printf '  ==> H1 PREMATURE VERDICT: invalidation had already worked and the re-digest read too\n'
  printf '      early. Fix is a delay before re-digesting, not a higher attempt bound.\n'
elif [ "$needed_more" -gt 0 ] && [ "$healed" -eq 0 ]; then
  printf '  ==> H2 INVALIDATION INSUFFICIENT: idling does not converge it; passes are the variable.\n'
else
  printf '  ==> MIXED (%d healed, %d needed passes) — both effects present; neither fix alone suffices.\n' \
    "$healed" "$needed_more"
fi
printf '\nCleanup: yoloai destroy %s --abandon-unapplied\n' "$BOX"

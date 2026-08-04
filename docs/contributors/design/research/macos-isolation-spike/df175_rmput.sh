#!/bin/bash
# ABOUTME: Settles DF175's residual gap — whether `files rm` then `files put` of the same name
# ABOUTME: delivers stale bytes to a tart guest, because that pair is not a "replacement".
#
# Run: bash df175_rmput.sh [backend] [sandbox-name]     (no sudo; creates the sandbox if absent)
#      backend defaults to tart (the subject); pass `apple` for the cross-backend control.
#
# THE QUESTION
#   DF175 shipped a verify-and-repair: ImportFile calls refreshGuestView when an import
#   REPLACED an existing entry (`internal/orchestrator/engine_files.go:37`). A `files rm`
#   followed by a `files put` of the same name is not a replacement by that test — the path
#   does not exist when the put runs, so `res.Replaced` is false and no repair fires. But the
#   guest may well have read the path before the `rm`, and the spike measured that the stale
#   mapping SURVIVES UNLINK: after a host rm + recreate with different content and a different
#   length, the guest still served the original file's first bytes
#   (`results/df175-files-put-corruption.txt`, CANDIDATE FIXES). If that holds through the
#   shipped commands, `files rm` + `files put` is a live path to fabricated content.
#
# WHY THE CONTROLS ARE NOT THE OBVIOUS ONES
#   The natural control — "does DF175 reproduce? put, read, put --overwrite, expect stale" —
#   is now WRONG, and would fail. That path is the one the fix repairs. Reusing
#   df175_writepath.sh's P0 verbatim would report a broken harness on a healthy machine.
#   So the corruption is provoked here by writing into the exchange directory DIRECTLY,
#   bypassing the CLI, which is the raw backend behaviour and is not fixed and cannot be.
#   Read the probes as a set:
#     P0  the raw mechanism must still exist on this host    (else nothing below is evidence)
#     P1  the shipped fix must still work on the replace path (else P2 is testing a
#         regression in the fix rather than the gap it never covered)
#     P2  THE MEASUREMENT — rm + put of the same name
#     P3  a never-read path must land correctly              (else "stale" is just "the
#         harness cannot read the guest" wearing a costume — A22)
#   P2 without P0 and P3 is a coin toss with a plausible story attached.

set -u

BACKEND=${1:-tart}
BOX=${2:-df175-rmput-$BACKEND}
A20=AAAAAAAAAAAAAAAAAAAA   # 20 bytes, so a shorter rewrite lands inside the cached page
Z3=ZZZ
Q5=QQQQQ
POLL_S=12                  # the observed revalidation tick is ~1s; 12s separates slow from never

PASS=0
FAIL=0
UNKNOWN=0

hr() { printf '\n== %s\n' "$1"; }
chk() { # chk <label> <expected> <got>
  if [ "$2" = "$3" ]; then printf '  ok      %-46s %s\n' "$1" "$3"; PASS=$((PASS + 1))
  else printf '  FAIL    %-46s expected=%s got=%s\n' "$1" "$2" "$3"; FAIL=$((FAIL + 1)); fi
}
note() { # note <label> <got> — an observation whose value IS the finding; no pass/fail
  printf '  ?       %-46s %s\n' "$1" "$2"; UNKNOWN=$((UNKNOWN + 1))
}
die() { printf '\nABORT: %s\n' "$1" >&2; exit 1; }

command -v yoloai >/dev/null || die "yoloai not on PATH"
case "$BACKEND" in
  tart)  command -v tart >/dev/null || die "tart not on PATH" ;;
  apple) command -v container >/dev/null || die "container not on PATH" ;;
  *)     die "backend must be tart or apple (got '$BACKEND')" ;;
esac

printf '=== DF175 residual gap: files rm + files put of the same name ===\n'
printf 'date    : %s\n' "$(date '+%Y-%m-%d %H:%M:%S')"
printf 'backend : %s\nsandbox : %s\n' "$BACKEND" "$BOX"

# --- sandbox -----------------------------------------------------------------------------
if ! yoloai files "$BOX" path >/dev/null 2>&1; then
  printf 'creating %s sandbox %s (this takes a few minutes)\n' "$BACKEND" "$BOX"
  WORKDIR=$(mktemp -d)
  git -C "$WORKDIR" init -q
  printf 'ORIGINAL\n' > "$WORKDIR/README.md"
  git -C "$WORKDIR" add -A
  git -C "$WORKDIR" -c user.email=spike@example.com -c user.name=spike commit -qm init
  yoloai new --backend "$BACKEND" "$BOX" "$WORKDIR" || die "yoloai new failed"
fi

EX=$(yoloai files "$BOX" path) || die "cannot resolve exchange dir"
[ -d "$EX" ] || die "exchange dir does not exist: $EX"

# The guest-side path and exec mechanism differ per backend; everything else is identical, which
# is the point of running the control through this same script rather than a second one.
if [ "$BACKEND" = tart ]; then
  # tart guests are macOS: VirtioFS shares land under a path with spaces (runtime/tart/tart.go:135)
  # and stat is BSD.
  GEX="/Volumes/My Shared Files/rw/files"
  gexec() { tart exec "yoloai-cli-$BOX" "$@" 2>/dev/null || true; }
  gsize() { gexec stat -f '%z' "$GEX/$1" || echo "?"; }
else
  # apple guests are LINUX containers, so stat is GNU: the BSD form returns empty, which reads as
  # "the guest cannot see the file" and is a different finding entirely.
  #
  # And the guest layout is FLAT — /yoloai/files, NOT /yoloai/rw/files. The host-side host/ro/rw
  # tiering is not reproduced inside an apple guest, while tart DOES carry it
  # ("/Volumes/My Shared Files/rw/files"). Verified by `container exec … ls /yoloai`, after
  # deriving "/yoloai/rw/files" from apple.go's "literal mount paths" comment and watching every
  # probe return empty. The comment is about the mount TARGET, not the tier structure.
  GEX="/yoloai/files"
  gexec() { container exec "yoloai-cli-$BOX" "$@" 2>/dev/null || true; }
  gsize() { gexec stat -c '%s' "$GEX/$1" || echo "?"; }
fi
printf 'host dir: %s\nguest dir: %s\n' "$EX" "$GEX"

STAGE=$(mktemp -d)
trap 'rm -rf "$STAGE"' EXIT

# Probe names are unique per run, because the defect under test makes them single-use: once a guest
# has read a path, its mapping survives unlink and recreation, so re-running with fixed names in an
# existing sandbox would measure the PREVIOUS run's poisoning and call it this run's result. With
# this, the script is safely re-runnable against a sandbox that already exists — which matters,
# since creating one costs minutes.
RUN=$(date '+%H%M%S')
P0=p0-$RUN.txt
P1=p1-$RUN.txt
P2=p2-$RUN.txt
P3=p3-$RUN.txt
printf 'run id  : %s (probe names are suffixed with it; poisoned paths are single-use)\n' "$RUN"

# Bypasses `yoloai exec` deliberately: a sandbox whose agent has exited still has a guest.
gcat() { gexec cat "$GEX/$1"; }

# Through the shipped command, which is what is on trial. The error text is kept and printed
# rather than discarded: the first run of this harness swallowed it with `>/dev/null 2>&1|| die`
# and reported a bare "files put failed", which is indistinguishable from the interesting case —
# `refreshGuestView` returning an error because it could NOT make the guest agree, which is a
# deliberate design choice in ImportFile and would be a finding, not a harness problem.
# The retry exists because that first failure was transient (a put seconds after sandbox creation,
# while the agent was still starting); it retries once, says so, and dies with the real message.
put() { # put <name> <content>
  local out rc
  printf '%s' "$2" > "$STAGE/$1"
  out=$(yoloai files "$BOX" put "$STAGE/$1" --overwrite 2>&1); rc=$?
  if [ "$rc" -ne 0 ]; then
    printf '  retry   files put %s failed (rc=%d): %s\n' "$1" "$rc" "$out"
    sleep 3
    out=$(yoloai files "$BOX" put "$STAGE/$1" --overwrite 2>&1); rc=$?
  fi
  [ "$rc" -eq 0 ] || die "files put failed for $1 after a retry (rc=$rc): $out"
}

rmf() { # rmf <name> — same treatment for the other shipped command in the sequence
  local out rc
  out=$(yoloai files "$BOX" rm "$1" 2>&1); rc=$?
  [ "$rc" -eq 0 ] || die "files rm failed for $1 (rc=$rc): $out"
}

# Poll until the guest reports <want>, or POLL_S elapses. Reports the bytes it actually ended on,
# so a wrong answer is legible as content rather than as a bare "no".
await() { # await <name> <want>
  local deadline got=""
  deadline=$((SECONDS + POLL_S))
  while [ "$SECONDS" -lt "$deadline" ]; do
    got=$(gcat "$1")
    [ "$got" = "$2" ] && { echo "$got"; return; }
    sleep 0.25
  done
  echo "${got:-<empty>}"
}

# --- P0: the raw mechanism must still exist ----------------------------------------------
hr "P0  control: is this host still capable of serving stale guest reads at all?"
printf '        (provoked by writing into the share DIRECTLY — the CLI path is repaired now,\n'
printf '         so using it here would test the fix and report a broken harness)\n'
put "$P0" "$A20"
chk "guest reads v1 (caches the path)" "$A20" "$(await "$P0" "$A20")"
printf '%s' "$Z3" > "$EX/$P0"                 # direct, not through yoloai — no repair fires
chk "host side is correct" "$Z3" "$(cat "$EX/$P0")"
P0GOT=$(await "$P0" "$Z3")
if [ "$BACKEND" = tart ]; then
  chk "guest reads STALE bytes (the mechanism)" "AAA" "$P0GOT"
  [ "$P0GOT" = "AAA" ] || die "the DF175 mechanism did not reproduce (guest saw '$P0GOT').
Every probe below would pass vacuously, so this run is not evidence. Check the tart version and
the share layout before believing a green P2."
else
  chk "guest reads the NEW bytes (apple is coherent)" "$Z3" "$P0GOT"
fi

# --- P1: the shipped fix must still cover the replace path -------------------------------
hr "P1  control: does the DF175 fix still repair a REPLACEMENT? (put --overwrite over a read path)"
put "$P1" "$A20"
chk "guest reads v1 (caches the path)" "$A20" "$(await "$P1" "$A20")"
put "$P1" "$Z3"                                # res.Replaced=true -> refreshGuestView fires
chk "guest reads the NEW bytes (repaired)" "$Z3" "$(await "$P1" "$Z3")"

# --- P2: THE MEASUREMENT -----------------------------------------------------------------
hr "P2  THE GAP: files rm, then files put of the SAME name — no replacement, so no repair"
put "$P2" "$A20"
chk "guest reads v1 (caches the path)" "$A20" "$(await "$P2" "$A20")"
rmf "$P2"
chk "host side: the file is gone after rm" "gone" \
    "$([ -e "$EX/$P2" ] && echo present || echo gone)"
note "guest immediately after rm" "$(gcat "$P2" || echo '<none>')"
# NOT `put` here: a non-zero exit is one of the three outcomes this probe classifies, not an
# abort. Against the SHIPPED build this put exits 0 and corrupts silently (the refresh is gated on
# the import having replaced something, and an rm+put has not). Against a build with that gate
# removed it exits non-zero instead — see results/df175-rmput-gate-removed.txt. Both are findings,
# and a harness that dies on either reports "harness broken" for the product behaving as designed.
printf '%s' "$Q5" > "$STAGE/$P2"
p2out=$(yoloai files "$BOX" put "$STAGE/$P2" --overwrite 2>&1); p2rc=$?
chk "host side holds the new content" "$Q5" "$(cat "$EX/$P2")"
P2GOT=$(await "$P2" "$Q5")
note "put exit code" "$p2rc"
note "GUEST READS" "$P2GOT"
note "guest stat size" "$(gsize "$P2")"
# Three outcomes, and only one of them is the original defect. Collapsing "refused loudly" into
# "failed" would lose the distinction the whole fix is about.
if [ "$p2rc" = 0 ] && [ "$P2GOT" = "$Q5" ]; then
  printf '  ==> DELIVERED on %s: rm+put reaches the guest and the command succeeds.\n' "$BACKEND"
elif [ "$p2rc" != 0 ]; then
  printf '  ==> REFUSED on %s: the put exits non-zero rather than delivering content the guest\n' "$BACKEND"
  printf '      cannot read. Not silent corruption, and not a delivery either — the invalidation\n'
  printf '      cannot repair a path whose dentry still resolves to the unlinked inode. The error\n'
  printf '      names a restart, which does clear it. Message: %s\n' "$(printf '%s' "$p2out" | head -1)"
else
  printf '  ==> SILENT CORRUPTION on %s: put exited 0 and the guest serves %s where the host\n' \
    "$BACKEND" "${P2GOT:-<empty>}"
  printf '      holds %s. This is the original DF175 defect, unguarded.\n' "$Q5"
fi

# --- P3: a never-read path must land correctly -------------------------------------------
hr "P3  control: a path the guest has NEVER read must arrive correctly"
put "$P3" "$A20"
# deliberately no guest read here — that is the whole point
rmf "$P3"
put "$P3" "$Q5"
chk "guest reads the NEW bytes" "$Q5" "$(await "$P3" "$Q5")"

# --- P4: DF177's directory replace, which can only be safe if P2 is ------------------------
hr "P4  DF177: re-putting a DIRECTORY must replace it, and the guest must see the new tree"
printf '        (the replace unlinks the old directory, so on tart this IS an rm+put — it is safe
'
printf '         only because the removal now drops the guest dentries. That coupling is the point.)
'
DSRC=$STAGE/bundle-$RUN
mkdir -p "$DSRC"
printf '%s' "$A20" > "$DSRC/keep.txt"
printf 'STALE-SOURCE' > "$DSRC/gone.txt"
yoloai files "$BOX" put "$DSRC" --overwrite >/dev/null 2>&1 || die "directory put failed"
BN=$(basename "$DSRC")
chk "guest reads v1 of keep.txt" "$A20" "$(await "$BN/keep.txt" "$A20")"
# Second import: one file changed, one removed from the source entirely.
printf '%s' "$Q5" > "$DSRC/keep.txt"
rm -f "$DSRC/gone.txt"
d2out=$(yoloai files "$BOX" put "$DSRC" --overwrite 2>&1); d2rc=$?
note "second directory put rc" "$d2rc"
[ "$d2rc" = 0 ] || printf '        %s\n' "$(printf '%s' "$d2out" | head -1)"
chk "host: no nested copy (DF177)" "gone" \
    "$([ -d "$EX/$BN/$BN" ] && echo present || echo gone)"
chk "host: source-removed file is gone" "gone" \
    "$([ -e "$EX/$BN/gone.txt" ] && echo present || echo gone)"
note "GUEST READS keep.txt" "$(await "$BN/keep.txt" "$Q5")"
note "guest still sees gone.txt?" "$(gexec sh -c "[ -e '$GEX/$BN/gone.txt' ] && echo yes || echo no")"

printf '\n== TOTALS ==\n   pass=%d fail=%d observations=%d\n' "$PASS" "$FAIL" "$UNKNOWN"
printf '\nP2 is the finding; P0/P1/P3 only license reading it.\n'
printf 'Cleanup: yoloai destroy %s --abandon-unapplied\n' "$BOX"

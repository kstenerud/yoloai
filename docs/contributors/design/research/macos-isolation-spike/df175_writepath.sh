#!/bin/bash
# ABOUTME: Settles which host write pattern, if any, can deliver replacement content to a
# ABOUTME: tart guest at a path it has already read (DF175), and whether a restart clears it.
#
# Run: bash df175_writepath.sh [sandbox-name]      (no sudo; creates the sandbox if absent)
#
# DF175 recorded a fix lead — "write a new path and move it into place" — that the spike's own
# coherence matrix already refutes (overwrite_rename: read NEVER, stat NEVER). P2 re-tests that
# lead through a real rename into the real exchange directory rather than through the harness,
# because a lead this repo is about to build on should fail in the place it would be built.
#
# Every probe prints EXPECT and GOT. Two of them are the controls that make the rest mean
# anything (A22):
#   P0  the corruption must reproduce in THIS run, or nothing below is evidence of anything.
#   P1  a never-read path must update correctly, or "stale" is just "the harness cannot see
#       guest writes" wearing a costume.
# P4 restarts the VM, so it runs last: it clears every cache P0-P3 depend on.

set -u

BOX=${1:-df175-writepath}
VM="yoloai-cli-$BOX"
A20=AAAAAAAAAAAAAAAAAAAA   # 20 bytes, so a 3-byte overwrite lands inside the cached page
Z3=ZZZ
POLL_S=12                  # the observed tick is ~1s; 12s distinguishes "slow" from "never"

PASS=0
FAIL=0
UNKNOWN=0

hr() { printf '\n== %s\n' "$1"; }
chk() { # chk <label> <expected> <got>
  if [ "$2" = "$3" ]; then printf '  ok      %-44s %s\n' "$1" "$3"; PASS=$((PASS + 1))
  else printf '  FAIL    %-44s expected=%s got=%s\n' "$1" "$2" "$3"; FAIL=$((FAIL + 1)); fi
}
note() { # note <label> <got> — an observation with no pass/fail; the answer IS the finding
  printf '  ?       %-44s %s\n' "$1" "$2"; UNKNOWN=$((UNKNOWN + 1))
}
die() { printf '\nABORT: %s\n' "$1" >&2; exit 1; }

command -v yoloai >/dev/null || die "yoloai not on PATH"
command -v tart >/dev/null || die "tart not on PATH"

# --- sandbox -----------------------------------------------------------------------------
if ! yoloai files "$BOX" path >/dev/null 2>&1; then
  printf 'creating tart sandbox %s (this takes a few minutes)\n' "$BOX"
  WORKDIR=$(mktemp -d)
  git -C "$WORKDIR" init -q
  printf 'ORIGINAL\n' > "$WORKDIR/README.md"
  git -C "$WORKDIR" add -A
  git -C "$WORKDIR" -c user.email=spike@example.com -c user.name=spike commit -qm init
  yoloai new --backend tart "$BOX" "$WORKDIR" || die "yoloai new failed"
fi

EX=$(yoloai files "$BOX" path) || die "cannot resolve exchange dir"
[ -d "$EX" ] || die "exchange dir does not exist: $EX"
GEX="/Volumes/My Shared Files/rw/files"
printf 'host exchange dir : %s\nguest exchange dir: %s\nvm                : %s\n' "$EX" "$GEX" "$VM"

STAGE=$(mktemp -d)
trap 'rm -rf "$STAGE"' EXIT

# --- guest/host helpers ------------------------------------------------------------------
# Bypasses `yoloai exec` deliberately: a sandbox whose agent has exited still has a guest.
gcat() { tart exec "$VM" /bin/cat "$GEX/$1" 2>/dev/null || true; }
gstat() { tart exec "$VM" /usr/bin/stat -f '%z' "$GEX/$1" 2>/dev/null || echo "?"; }
greadlink() { tart exec "$VM" /usr/bin/readlink "$GEX/$1" 2>/dev/null || echo "?"; }

put() { # put <name> <content> — deliver via the shipped command, which is what is on trial
  printf '%s' "$2" > "$STAGE/$1"
  yoloai files "$BOX" put "$STAGE/$1" --overwrite >/dev/null 2>&1 || die "files put failed for $1"
}

# Poll until the guest reports <want>, or POLL_S elapses. Prints what it ended up seeing, so a
# wrong answer is reported as the bytes it actually got rather than as a bare "no".
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

# --- P0: the corruption must reproduce in this run ---------------------------------------
hr "P0  control: does DF175 still reproduce here? (nothing below means anything if not)"
put p0.txt "$A20"
chk "guest reads v1 (must succeed)" "$A20" "$(await p0.txt "$A20")"
put p0.txt "$Z3"
chk "host side is correct" "$Z3" "$(cat "$EX/p0.txt")"
P0GOT=$(await p0.txt "$Z3")
chk "guest reads STALE bytes (DF175)" "AAA" "$P0GOT"
chk "guest stat shows the NEW size" "3" "$(gstat p0.txt)"
[ "$P0GOT" = "AAA" ] || die "DF175 did not reproduce (guest saw '$P0GOT'). Every probe below \
would pass vacuously, so this run is not evidence. Re-check tart version and share layout."

# --- P1: a never-read path must update correctly -----------------------------------------
hr "P1  control: does a path the guest has NEVER read update? (proves we can see success)"
put p1.txt "$A20"
# deliberately no guest read here — that is the whole point
put p1.txt "$Z3"
chk "guest reads the NEW bytes" "$Z3" "$(await p1.txt "$Z3")"

# --- P2: the lead recorded in DF175, through a real rename into the real directory --------
hr "P2  the DF175 lead: write a temp file and rename it over the read path"
put p2.txt "$A20"
chk "guest reads v1 (caches the path)" "$A20" "$(await p2.txt "$A20")"
printf '%s' "$Z3" > "$EX/.p2.tmp"
mv "$EX/.p2.tmp" "$EX/p2.txt"
chk "host side is correct" "$Z3" "$(cat "$EX/p2.txt")"
note "guest after rename-into-place" "$(await p2.txt "$Z3")"
note "guest stat after rename" "$(gstat p2.txt)"

# --- P3: symlink indirection — every write lands at a name the guest has never read -------
hr "P3  symlink indirection: p3.txt -> .blobs/p3.vN, repointed instead of rewritten"
mkdir -p "$EX/.blobs"
printf '%s' "$A20" > "$EX/.blobs/p3.v1"
ln -sfn ".blobs/p3.v1" "$EX/p3.txt"
P3V1=$(await p3.txt "$A20")
chk "guest reads v1 through the symlink" "$A20" "$P3V1"
if [ "$P3V1" = "$A20" ]; then
  printf '%s' "$Z3" > "$EX/.blobs/p3.v2"      # a name the guest has never resolved
  ln -sfn ".blobs/p3.v2" "$EX/.p3.tmp"
  mv "$EX/.p3.tmp" "$EX/p3.txt"               # atomic repoint
  chk "host side is correct" "$Z3" "$(cat "$EX/p3.txt")"
  note "guest readlink (is the LINK stale?)" "$(greadlink p3.txt)"
  note "guest cat  (is the CONTENT stale?)" "$(await p3.txt "$Z3")"
else
  note "symlink indirection" "SKIPPED - guest could not read v1 through a symlink at all"
fi

# --- P4: does a restart clear a poisoned path? (runs last: it clears everything) ----------
hr "P4  does stop+start clear the poisoned path from P0? (the workaround we would document)"
yoloai stop "$BOX" >/dev/null 2>&1 || die "yoloai stop failed"
yoloai start "$BOX" >/dev/null 2>&1 || die "yoloai start failed"
chk "host side is still correct" "$Z3" "$(cat "$EX/p0.txt")"
note "guest reads p0.txt after restart" "$(await p0.txt "$Z3")"

printf '\n%d ok, %d FAIL, %d open question(s) answered above\n' "$PASS" "$FAIL" "$UNKNOWN"
printf 'sandbox %s left running for follow-up; destroy with: yoloai rm %s\n' "$BOX" "$BOX"
[ "$FAIL" -eq 0 ]

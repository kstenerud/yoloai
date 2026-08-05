#!/bin/bash
# ABOUTME: Settles DF176's loose end — whether a tart guest sees ro/prompt.txt reappear when
# ABOUTME: reset restores it after start, including the negative-dentry case underneath it.
#
# Run: bash df176_prompt.sh [sandbox-name]        (no sudo; creates the sandbox if absent)
#
# THE ROW THIS TESTS IS NOT THE ROW DF176 WROTE, AND THE DIFFERENCE IS THE POINT
#   DF176's table says: "applyPostResetOptions prompt rename | ro/prompt.txt | yes, on an
#   in-place reset | unverified". Read against the code, the scenario it names cannot happen
#   on tart:
#     - `applyPostResetOptions` has exactly one caller, `prepareResetRestart`
#       (`internal/orchestrator/lifecycle/reset.go:328`) — the RESTART path.
#     - `Reset` force-upgrades to restart whenever the backend is LocalitySandboxSide
#       (`reset.go:81-83`), and tart is LocalitySandboxSide (`runtime/tart/tart.go:62`).
#       So `resetInPlace` is unreachable on tart; there is no in-place reset to test.
#     - The restart path REMOVES the container (`reset.go:302`) before the hide-rename, so at
#       the moment prompt.txt is renamed aside there is no guest and no cache.
#   Writing the experiment as stated would have measured a path the product cannot reach —
#   AGENTS.md rule 10's empty-intersection trap, where a fake or a harness certifies a dead
#   path. What IS reachable is the mirror image, and nothing has measured it:
#
#   `cleanup()` runs as a `defer` AFTER `start()` (`reset.go:332,336`), so the RESTORE rename
#   puts prompt.txt back while the new guest is RUNNING — onto a path that guest booted
#   without, and may therefore have looked up and cached as absent.
#
# SO THE QUESTION IS ABOUT NEGATIVE CACHING, NOT STALE PAGES
#   DF175/DF176 are about a path the guest has already READ: the page cache serves old bytes.
#   This is the opposite shape — a path the guest has already MISSED. The spike's coherence
#   matrix measured "create a new file" at ~209 ms readdir / ~214 ms on tart, but only for
#   names never looked up. A name the guest previously resolved to ENOENT is a different
#   entry in a different cache, and it is not in the matrix.
#
#   P0 measures the mechanism directly and P1 is its control; P2 runs the real product flow.
#   P0 without P1 cannot tell "negative entries are sticky" from "this share is just slow".

set -u

BOX=${1:-df176-prompt}
BODY=PROMPT-BODY-V1
POLL_S=12                  # the observed revalidation tick is ~1 s; 12 s separates slow from never

PASS=0
FAIL=0
UNKNOWN=0

hr() { printf '\n== %s\n' "$1"; }
chk() { # chk <label> <expected> <got>
  if [ "$2" = "$3" ]; then printf '  ok      %-46s %s\n' "$1" "$3"; PASS=$((PASS + 1))
  else printf '  FAIL    %-46s expected=%s got=%s\n' "$1" "$2" "$3"; FAIL=$((FAIL + 1)); fi
}
note() { # note <label> <got> — an observation whose value IS the finding
  printf '  ?       %-46s %s\n' "$1" "$2"; UNKNOWN=$((UNKNOWN + 1))
}
die() { printf '\nABORT: %s\n' "$1" >&2; exit 1; }

command -v yoloai >/dev/null || die "yoloai not on PATH"
command -v tart >/dev/null || die "tart not on PATH"

printf '=== DF176: does a tart guest see a file that reappears after it saw it missing? ===\n'
printf 'date   : %s\nsandbox: %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$BOX"

if ! yoloai files "$BOX" path >/dev/null 2>&1; then
  printf 'creating tart sandbox %s (this takes a few minutes)\n' "$BOX"
  WORKDIR=$(mktemp -d)
  git -C "$WORKDIR" init -q
  printf 'ORIGINAL\n' > "$WORKDIR/README.md"
  git -C "$WORKDIR" add -A
  git -C "$WORKDIR" -c user.email=spike@example.com -c user.name=spike commit -qm init
  yoloai new --backend tart "$BOX" "$WORKDIR" --prompt "$BODY" || die "yoloai new failed"
fi

VM="yoloai-cli-$BOX"
EX=$(yoloai files "$BOX" path) || die "cannot resolve exchange dir"
# The exchange dir is <sandboxdir>/rw/files; the ro tier is its sibling. Derived rather than
# hardcoded so a layout change breaks loudly here instead of silently measuring the wrong path.
SBDIR=$(cd "$EX/../.." && pwd) || die "cannot resolve sandbox dir from $EX"
RO="$SBDIR/ro"
GRO="/Volumes/My Shared Files/ro"
[ -d "$RO" ] || die "ro tier not found at $RO"
printf 'sandbox dir: %s\nro tier    : %s\nguest ro   : %s\n' "$SBDIR" "$RO" "$GRO"

gexec() { tart exec "$VM" "$@" 2>/dev/null || true; }
gcat()  { gexec cat "$GRO/$1"; }
# Prints "yes"/"no" for guest-visible existence. `test -e` rather than stat output, because the
# question here is presence, and a negative dentry is exactly what makes presence lie.
gsees() { gexec sh -c "[ -e '$GRO/$1' ] && echo yes || echo no"; }

await() { # await <name> <want> — poll gcat until it matches, then report what it ended on
  local deadline got=""
  deadline=$((SECONDS + POLL_S))
  while [ "$SECONDS" -lt "$deadline" ]; do
    got=$(gcat "$1")
    [ "$got" = "$2" ] && { echo "$got"; return; }
    sleep 0.25
  done
  echo "${got:-<empty>}"
}

# --- P0: the mechanism — does a MISS stick? ----------------------------------------------
hr "P0  the mechanism: guest looks up a missing name, then the host creates it"
chk "guest sees it before creation (must be no)" "no" "$(gsees probe-neg.txt)"
note "guest cat before creation" "$(gcat probe-neg.txt || echo '<none>')"
printf 'NEG-CREATED' > "$RO/probe-neg.txt"
P0GOT=$(await probe-neg.txt "NEG-CREATED")
note "guest cat after host created it" "$P0GOT"
note "guest sees it now" "$(gsees probe-neg.txt)"
if [ "$P0GOT" = "NEG-CREATED" ]; then
  printf '  ==> negative lookups do NOT stick: the restored prompt would be visible.\n'
else
  printf '  ==> NEGATIVE ENTRY IS STICKY: a name the guest missed stays missing for %ss.\n' "$POLL_S"
fi

# --- P1: control — a name never looked up ------------------------------------------------
hr "P1  control: same creation, but a name the guest never looked up"
printf 'FRESH-CREATED' > "$RO/probe-fresh.txt"
chk "guest reads it (proves creates propagate at all)" "FRESH-CREATED" \
    "$(await probe-fresh.txt "FRESH-CREATED")"

rm -f "$RO/probe-neg.txt" "$RO/probe-fresh.txt"

# --- P2: the real flow -------------------------------------------------------------------
hr "P2  the product path: yoloai reset --no-prompt, which hides prompt.txt then restores it"
printf '        (tart is LocalitySandboxSide, so this auto-upgrades to a restart: the container is\n'
printf '         removed, prompt.txt is hidden, the guest starts WITHOUT it, and the deferred\n'
printf '         cleanup renames it back while that new guest is running)\n'
[ -f "$RO/prompt.txt" ] || \
  note "no prompt.txt on the host before reset" "$(find "$RO" -maxdepth 1 -type f -exec basename {} \; | tr '\n' ' ')"
yoloai reset "$BOX" --no-prompt >/dev/null 2>&1 || die "yoloai reset --no-prompt failed"
chk "host holds prompt.txt again after reset" "yes" \
    "$([ -f "$RO/prompt.txt" ] && echo yes || echo no)"
chk "no .bak left behind" "no" \
    "$([ -f "$RO/prompt.txt.bak" ] && echo yes || echo no)"
P2GOT=$(await prompt.txt "$BODY")
note "GUEST READS prompt.txt" "$P2GOT"
note "guest sees prompt.txt" "$(gsees prompt.txt)"
if [ "$P2GOT" = "$BODY" ]; then
  printf '  ==> DF176 row CLOSED: the restored prompt is visible to the running guest.\n'
else
  printf '  ==> DF176 row CONFIRMED: guest holds %s where the host holds %s.\n' \
    "${P2GOT:-<empty>}" "$BODY"
fi

printf '\n== TOTALS ==\n   pass=%d fail=%d observations=%d\n' "$PASS" "$FAIL" "$UNKNOWN"
printf '\nP2 is the product question; P0 is the mechanism under it and P1 licenses reading P0.\n'
printf 'Cleanup: yoloai destroy %s --abandon-unapplied\n' "$BOX"

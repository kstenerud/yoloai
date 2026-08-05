#!/bin/bash
# ABOUTME: Tests DF173 — whether Docker Desktop's single-file-bind-mount staleness really
# ABOUTME: depends on rename-vs-in-place rewriting, as the documented explanation claims.
#
# Run: bash docker_desktop_stale.sh     (no root; switches docker context and restores it)
#
# backend-idiosyncrasies.md ("Docker Desktop: a single-file bind mount serves stale content
# after the host atomic-renames it") explains the failure by the patch being an atomic
# rename that gives the file a NEW INODE, which gRPC-FUSE then mis-caches. But the writer it
# blames, patchKeepaliveOnly, calls fileutil.WriteFile -> os.WriteFile: an in-place
# truncate+write that KEEPS the inode. Both cannot be right. This runs the two rewrite
# shapes side by side against a single-file bind mount and reports which one goes stale.

set -u
D=$(mktemp -d)
ORIG_CTX=$(docker context show 2>/dev/null)
cleanup() {
  [ -n "${ORIG_CTX:-}" ] && docker context use "$ORIG_CTX" >/dev/null 2>&1
  rm -rf "$D"
  echo "[restore] docker context back to ${ORIG_CTX:-<unknown>}"
}
trap cleanup EXIT INT TERM

if ! docker context ls --format '{{.Name}}' 2>/dev/null | grep -qx desktop-linux; then
  echo "SKIP: no desktop-linux docker context on this host."; exit 0
fi
docker context use desktop-linux >/dev/null 2>&1
if ! docker info >/dev/null 2>&1; then
  echo "SKIP: Docker Desktop context selected but the daemon is not reachable (start Docker Desktop)."
  exit 0
fi
echo "docker context: $(docker context show)   server: $(docker info --format '{{.OperatingSystem}}' 2>/dev/null)"

# Models the real sequence from startViaLaunch, which is what the entry describes:
#   1. a container mounts the file and reads it   (populates the gRPC-FUSE cache)
#   2. the host patches the file                  (in place, or via rename)
#   3. a NEW container mounts the same path and reads it AT STARTUP
# The reported failure is step 3 seeing step 1's content. Reading through `docker exec` into
# an already-running container is a different path and was measured fresh either way, so the
# start-time read is the one that matters.
probe() { # probe <label> <rewrite-shape>
  local label=$1 shape=$2 f="$D/cfg_$2.json" out
  echo '{"v":"BEFORE"}' > "$f"
  docker run --rm -v "$f":/cfg.json:ro alpine:3 cat /cfg.json >/dev/null 2>&1
  case "$shape" in
    inplace) printf '{"v":"AFTER"}\n' > "$f" ;;                       # same inode
    rename)  printf '{"v":"AFTER"}\n' > "$f.tmp"; mv "$f.tmp" "$f" ;; # new inode
    nopatch) : ;;   # deliberately no rewrite: the detector MUST report STALE here
  esac
  out=$(docker run --rm -v "$f":/cfg.json:ro alpine:3 cat /cfg.json 2>/dev/null | tr -d '[:space:]')
  printf '  %-8s host=%s  guest-at-start=%s  -> %s\n' "$label" \
    "$(tr -d '[:space:]' < "$f")" "$out" \
    "$(if echo "$out" | grep -q AFTER; then echo 'FRESH'; else echo 'STALE <-- reproduces the bug'; fi)"
}

echo
echo "== rewriting a single-file bind mount AFTER the container has read it once =="
probe "nopatch" nopatch    # positive control: proves the probe can SEE staleness
probe "inplace" inplace
probe "rename"  rename
echo
echo "Reading:"
echo "  only 'rename' STALE  => the documented new-inode explanation is right, and the entry"
echo "                          then mis-attributes it to patchKeepaliveOnly (which is in-place)."
echo "  only 'inplace' STALE => the explanation names the wrong mechanism; the writer matches"
echo "                          but the stated cause does not."
echo "  both STALE           => staleness is not about inode identity at all."
echo "  (the 'nopatch' control must read STALE; if it reads FRESH the probe is broken and"
echo "   every other line here is meaningless)"
echo "  neither STALE        => not reproducible here; the bug may be version- or timing-specific"
echo "                          (the original report notes the cache refreshes within a second or two,"
echo "                          so re-run with the sleep shortened before concluding anything)."

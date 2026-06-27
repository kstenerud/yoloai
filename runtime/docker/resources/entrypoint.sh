#!/bin/sh
set -e
# Write one canned JSONL entry proving the container booted, then exec into
# the Python entrypoint. This thin shell trampoline exists so that container
# startup is always recorded in logs/sandbox.jsonl, even if Python fails to
# start (e.g. missing interpreter, bad config).
JSONL_LOG="${YOLOAI_DIR:-/yoloai}/logs/sandbox.jsonl"
printf '{"ts":"%s","level":"info","event":"entrypoint.start","msg":"entrypoint.sh started"}\n' \
  "$(date -u +%Y-%m-%dT%H:%M:%S.000Z)" >> "$JSONL_LOG"
exec python3 "${YOLOAI_DIR:-/yoloai}/bin/entrypoint.py" "$@"

#!/bin/bash
# Runs `make check` at end of turn iff post-edit.sh stamped a source change.
# On failure, emits a Stop-decision-block with the output so Claude is told to
# fix the issues before the turn actually completes. On success, exits silent.
set -u

project_dir="${CLAUDE_PROJECT_DIR:-$PWD}"
stamp="$project_dir/.claude/.cache/make-check-pending"

[ -f "$stamp" ] || exit 0
rm -f "$stamp"

cd "$project_dir" || exit 0

if output=$(make check 2>&1); then
  exit 0
fi

# make check failed — block the stop and feed Claude the failure output.
jq -n --arg reason "make check failed at end of turn. Fix the issues below before stopping:"$'\n\n'"$output" \
  '{decision: "block", reason: $reason}'
exit 0

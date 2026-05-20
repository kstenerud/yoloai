#!/bin/bash
# Stamps a marker file when Claude modifies a project source/build file so the
# Stop hook knows to run `make check` once at end of turn. Skips edits to
# documentation, .claude/, and .git/ — they don't affect the build/tests.
set -u

project_dir="${CLAUDE_PROJECT_DIR:-$PWD}"

file=$(jq -r '.tool_input.file_path // empty' 2>/dev/null)
[ -z "$file" ] && exit 0

case "$file" in
  "$project_dir"/docs/*|"$project_dir"/.claude/*|"$project_dir"/.git/*)
    exit 0
    ;;
  "$project_dir"/*)
    mkdir -p "$project_dir/.claude/.cache" 2>/dev/null
    touch "$project_dir/.claude/.cache/make-check-pending" 2>/dev/null
    ;;
esac

exit 0

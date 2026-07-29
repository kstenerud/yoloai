#!/usr/bin/env bash
# ABOUTME: Prints the next free rationale ID (D<n> decision or DF<n> finding) as a bare
# ABOUTME: integer, scanning every file that can define one. A hand-composed grep that
# ABOUTME: misses a sink is what mints duplicate IDs, so the corpus comes from a glob.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

usage() {
	echo "usage: ${0##*/} D|DF" >&2
	echo >&2
	echo "Prints the next free rationale ID as a bare integer, so it composes:" >&2
	echo "    printf '## D%s — ...' \"\$(${0##*/} D)\"" >&2
	exit 2
}

[ $# -eq 1 ] || usage

case "$1" in
D)
	# Both decision logs define D IDs. The archive is the one a hand-written grep
	# forgets, which is how D118 was minted twice.
	files=(docs/contributors/decisions/working-notes*.md)
	heading_re='^## D[0-9]+ — '
	;;
DF)
	# All four findings sinks define DF IDs: unresolved, resolved, deferred, abandoned.
	files=(docs/contributors/design/findings-*.md)
	heading_re='^### DF[0-9]+ — '
	;;
*)
	usage
	;;
esac

for f in "${files[@]}"; do
	[ -e "$f" ] || {
		echo "ERROR: no $1 corpus found — expected files matching ${files[0]}" >&2
		exit 1
	}
done

# The em dash in heading_re is load-bearing, not decoration. Headings like
# "### DF8 (4th data point, 2026-05-26): ..." and "### DF18 (run-coverage half) — ..."
# are continuations of an existing finding, not second definitions of it: a parenthetical
# or a colon sits where the em dash would. A bare DF[0-9]+ anchor reports false duplicates
# at DF8 and DF18 and this script would then refuse to ever allocate again. Same
# discriminator as Gate B (canonicalDFHeadingRe in repo_hygiene_test.go); keep them in step.
ids="$(grep -hoE "$heading_re" "${files[@]}" | grep -oE '[0-9]+' | sort -n || true)"

# The working tree is not the whole corpus. An ID defined on a branch that has not
# merged yet is invisible here, and that is not hypothetical: PR #44 allocated DF153
# on its own branch, this script scanned main, and handed out 153 a second time. The
# blind spot is the same class the ABOUTME above warns about — a corpus that misses a
# sink — except the sink is a ref rather than a file, so no glob can reach it.
#
# So also scan every ref this clone knows: local branches and remote-tracking
# branches, in one `git grep` across all of them. Fetched PR heads count, because
# `git fetch origin pull/N/head:<branch>` lands in refs/heads. What is still invisible
# is a fork branch nobody has fetched — bounded by fetching before allocating, and
# the reason the duplicate check below stays the real backstop.
#
# These IDs raise the ceiling ONLY. They are deliberately excluded from the duplicate
# check: the same finding legitimately appears at the same number in main and in every
# branch that carries it, so treating cross-ref repeats as duplicates would report a
# conflict for every unmerged branch and refuse to allocate at all.
refs="$(git for-each-ref --format='%(refname)' refs/heads refs/remotes 2>/dev/null | tr '\n' ' ' || true)"
ref_ids=""
if [ -n "${refs// /}" ]; then
	# shellcheck disable=SC2086 # refs is a deliberately word-split ref list
	ref_ids="$(git grep -hoE "$heading_re" $refs -- "${files[@]}" 2>/dev/null | grep -oE '[0-9]+' | sort -n || true)"
fi

dups="$(printf '%s\n' "$ids" | uniq -d | tr '\n' ' ')"
if [ -n "${dups// /}" ]; then
	echo "ERROR: the $1 corpus already defines these IDs more than once: ${dups% }" >&2
	echo "       Renumber the duplicates first. A next-free number derived from a corpus" >&2
	echo "       that is already inconsistent is not actually free." >&2
	exit 1
fi

max="$(printf '%s\n%s\n' "$ids" "$ref_ids" | grep -E '^[0-9]+$' | sort -n | tail -1 || true)"
echo $((${max:-0} + 1))

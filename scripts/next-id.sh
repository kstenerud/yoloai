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

dups="$(printf '%s\n' "$ids" | uniq -d | tr '\n' ' ')"
if [ -n "${dups// /}" ]; then
	echo "ERROR: the $1 corpus already defines these IDs more than once: ${dups% }" >&2
	echo "       Renumber the duplicates first. A next-free number derived from a corpus" >&2
	echo "       that is already inconsistent is not actually free." >&2
	exit 1
fi

max="$(printf '%s\n' "$ids" | tail -1)"
echo $((${max:-0} + 1))

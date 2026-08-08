#!/usr/bin/env bash
# ABOUTME: L9b — resolve_domains keeps every A record, so a one-shot resolution at
# ABOUTME: install time is only safe if the address SET is stable. Measures that for
# ABOUTME: the domains yoloAI actually ships in its Claude allowlist.
set -uo pipefail

say() { printf '\n=== %s ===\n' "$*"; }
cleanup() { docker rm -f yb_l9b >/dev/null 2>&1; }
trap cleanup EXIT
cleanup

# internal/agent/agent.go:385
DOMAINS="api.anthropic.com claude.ai platform.claude.com statsig.anthropic.com sentry.io"
ROUNDS=6
GAP=10

docker run -d --name yb_l9b alpine sleep 400 >/dev/null
docker exec yb_l9b true || { echo "ABORT: container not usable"; exit 1; }

resolve_set() { # domain -> sorted, deduped A records as one line
	docker exec yb_l9b getent ahostsv4 "$1" 2>/dev/null | awk '{print $1}' | sort -u | tr '\n' ' ' | sed 's/ $//'
}

say "shipped Claude allowlist (internal/agent/agent.go:385)"
echo "$DOMAINS" | tr ' ' '\n' | sed 's/^/  /'
echo "sampling each $ROUNDS times, ${GAP}s apart, from inside one container"

for d in $DOMAINS; do
	say "$d"
	FIRST=""
	UNION=""
	DRIFTED=no
	MISSES=0
	for i in $(seq 1 $ROUNDS); do
		S=$(resolve_set "$d")
		[ -z "$S" ] && { echo "  round $i: <unresolved>"; continue; }
		[ -z "$FIRST" ] && FIRST="$S"
		printf '  round %d: %s\n' "$i" "$S"
		UNION=$(printf '%s %s' "$UNION" "$S" | tr ' ' '\n' | grep . | sort -u | tr '\n' ' ')
		[ "$S" != "$FIRST" ] && DRIFTED=yes
		# would the install-time snapshot have covered this round's answers?
		for a in $S; do
			case " $FIRST " in *" $a "*) ;; *) MISSES=$((MISSES+1)) ;; esac
		done
		[ "$i" -lt "$ROUNDS" ] && sleep $GAP
	done
	echo "  ---"
	echo "  install-time snapshot (round 1): $FIRST"
	echo "  union across all rounds        : $UNION"
	echo "  set changed between rounds     : $DRIFTED"
	echo "  answers NOT covered by round 1 : $MISSES"
	if [ "$MISSES" -gt 0 ]; then
		echo "  => a one-shot allowlist installed at round 1 would have DENIED traffic the"
		echo "     agent was entitled to, within $(( (ROUNDS-1) * GAP ))s of the sandbox starting."
	else
		echo "  => round 1's snapshot covered every later answer over this window."
	fi
done

say "what this decides"
echo "resolve_domains() runs once, at install. If the set drifts, the allowlist is"
echo "stale from the moment it is written, and the failure is closed — the agent is"
echo "denied a destination its allowlist names. This is independent of where"
echo "resolution happens; moving it host-side (L2) adds split-horizon on top."

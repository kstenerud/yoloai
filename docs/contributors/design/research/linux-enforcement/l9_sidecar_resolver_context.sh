#!/usr/bin/env bash
# ABOUTME: L9 — the tamper-resistant path resolves the allowlist in the SIDECAR and
# ABOUTME: enforces it on the AGENT. install-firewall.py asserts the two share a
# ABOUTME: resolver view. Tests that assertion, and what happens where it does not hold.
set -uo pipefail

say() { printf '\n=== %s ===\n' "$*"; }
# Guard: an earlier run of this script reported "resolv.conf IS shared" by comparing
# two empty strings, because every docker command had failed. Empty is never a result.
need() { [ -n "$2" ] || { echo "ABORT: $1 came back empty — the run is invalid, not negative"; exit 1; }; }
cleanup() { docker rm -f yb_l9_agent yb_l9_sidecar >/dev/null 2>&1; }
trap cleanup EXIT
cleanup

docker run --rm alpine true >/dev/null 2>&1 || { echo "ABORT: docker cannot start containers"; exit 1; }

DIVERGENT_NAME=allowlisted.example
AGENT_ANSWER=203.0.113.55

say "start an agent container with a name only IT can resolve"
echo "--add-host writes the agent's /etc/hosts. The sidecar has its own filesystem,"
echo "so this is the divergence the shipped design is exposed to."
docker run -d --name yb_l9_agent --add-host "$DIVERGENT_NAME:$AGENT_ANSWER" \
	alpine sleep 400 >/dev/null

say "CLAIM UNDER TEST (install-firewall.py docstring)"
echo "  \"The agent's /etc/resolv.conf nameservers are shared into this container by"
echo "   Docker (--network container:<id> shares the resolv.conf), so read_nameservers()"
echo "   sees the same DNS servers the agent uses.\""

say "does --network container:<id> actually share /etc/resolv.conf?"
AGENT_RESOLV=$(docker exec yb_l9_agent sh -c 'grep -v "^#" /etc/resolv.conf | grep .' | sort)
SIDE_RESOLV=$(docker run --rm --network "container:yb_l9_agent" alpine sh -c 'grep -v "^#" /etc/resolv.conf | grep .' | sort)
need "agent resolv.conf" "$AGENT_RESOLV"
need "sidecar resolv.conf" "$SIDE_RESOLV"
echo "  agent  : $(echo "$AGENT_RESOLV" | tr '\n' ' ')"
echo "  sidecar: $(echo "$SIDE_RESOLV" | tr '\n' ' ')"
if [ "$AGENT_RESOLV" = "$SIDE_RESOLV" ]; then
	echo "  RESULT: resolv.conf IS shared — the docstring's claim holds for nameservers."
else
	echo "  RESULT: resolv.conf DIFFERS — the docstring's claim does not hold."
fi

say "is /etc/hosts shared? (resolve_domains uses getaddrinfo, which reads it first)"
AGENT_HOSTS=$(docker exec yb_l9_agent sh -c "grep -c . /etc/hosts")
SIDE_HAS=$(docker run --rm --network "container:yb_l9_agent" alpine sh -c "grep -c '$DIVERGENT_NAME' /etc/hosts || true")
echo "  agent /etc/hosts lines: $AGENT_HOSTS, and it contains $DIVERGENT_NAME"
echo "  sidecar /etc/hosts entries for $DIVERGENT_NAME: $SIDE_HAS"

say "THE MEASUREMENT — resolve the same name in each context"
echo -n "  agent   resolves $DIVERGENT_NAME -> "
docker exec yb_l9_agent getent ahostsv4 "$DIVERGENT_NAME" 2>/dev/null | awk '{print $1; exit}' || echo "<unresolved>"
echo -n "  sidecar resolves $DIVERGENT_NAME -> "
docker run --rm --network "container:yb_l9_agent" alpine getent ahostsv4 "$DIVERGENT_NAME" 2>/dev/null | awk '{print $1; exit}' || echo "<unresolved>"

say "POSITIVE CONTROL — a name both contexts resolve identically"
echo -n "  agent   resolves example.com -> "
docker exec yb_l9_agent getent ahostsv4 example.com 2>/dev/null | awk '{print $1; exit}'
echo -n "  sidecar resolves example.com -> "
docker run --rm --network "container:yb_l9_agent" alpine getent ahostsv4 example.com 2>/dev/null | awk '{print $1; exit}'

say "what this means for the installed allowlist"
echo "resolve_domains() runs in the sidecar. apply_firewall() writes rules into the"
echo "shared netns, so they bind the AGENT. Any name the two contexts resolve"
echo "differently is allowlisted at the sidecar's answer and used at the agent's."
echo "Where the sidecar cannot resolve a name at all, the agent is allowlisted for"
echo "nothing and the failure is closed — the same shape L2 measured for host-side"
echo "resolution, one boundary further in."

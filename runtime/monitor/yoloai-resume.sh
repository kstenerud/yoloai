#!/bin/sh
# ABOUTME: In-sandbox resume command — relaunches the agent (continuing the prior
# ABOUTME: conversation where supported) through the fall-to-shell wrapper.

# Run from the fall-to-shell shell (D96 DD4) after the agent exits. It re-launches
# the agent THROUGH agent-run.sh so a subsequent quit also falls to a shell, and
# re-establishes detection: the durable monitor (DF46) is still running and
# re-detects the agent the moment it reappears.
#
# resume_cmd (resolved host-side from the agent's ResumeFlag) continues the prior
# conversation, e.g. `claude --dangerously-skip-permissions --continue`. When the
# agent has no native resume, resume_cmd is empty and we relaunch a FRESH session
# and say so — never claim a resume that did not happen.
#
# The pane never died across exit→shell→resume, so the monitor's respawn idle-seed
# never fires; we seed `idle` ourselves first so the stale `done` does not linger
# (the agent's own callback corrects active/idle from there).

YOLOAI_DIR="${YOLOAI_DIR:-/yoloai}"
config="${YOLOAI_DIR}/runtime-config.json"
wrapper="${YOLOAI_DIR}/bin/agent-run.sh"
status_file="${YOLOAI_DIR}/agent-status.json"
monitor="${YOLOAI_DIR}/bin/status-monitor.py"

cfg_get() {
	python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get(sys.argv[2],""))' "$config" "$1" 2>/dev/null
}

resume_cmd=$(cfg_get resume_cmd)
agent_cmd=$(cfg_get agent_command)
workdir=$(cfg_get working_dir)

cmd="$resume_cmd"
if [ -z "$cmd" ]; then
	printf '[yoloai] this agent has no native resume — starting a FRESH session.\n'
	cmd="$agent_cmd"
fi

if [ -z "$cmd" ]; then
	printf '[yoloai] could not determine the agent command to resume.\n' >&2
	exit 1
fi

# Clear the stale `done` before the agent comes back up.
python3 "$monitor" --write-status idle "$status_file" 2>/dev/null || true

if [ -n "$workdir" ]; then
	cd "$workdir" || exit 1
fi
# shellcheck disable=SC2086  # $cmd is a command line that must split into argv
exec "$wrapper" $cmd

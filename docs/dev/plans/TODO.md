# Unimplemented Features

Designed features not yet implemented. Each links to its design spec.
Create a plan file in this directory before starting implementation.

## Parallel Agent Workflows

Based on [parallel agents research](../research/parallel-agents.md).

### Batch sandbox creation

Add a `yoloai batch` command (or similar) that creates multiple sandboxes from a task list. Input could be a file with one prompt per line, a markdown file with structured specs, or inline arguments. Each task gets its own sandbox against the same workdir. All sandboxes start in parallel.

Example: `yoloai batch ./project tasks.md` creates N sandboxes, one per task in the file.

Design considerations:
- Naming: auto-generate names from task index or allow a prefix (`--prefix feat-`)
- Prompt delivery: each sandbox gets its task as `--prompt-file` or `--prompt`
- Options: inherit shared flags (agent, model, profile, aux dirs) from the batch command
- Output: summary table of created sandboxes

### Agent status detection

Detect whether the agent process inside a sandbox is actively running, idle (waiting for input), or has exited. Surface this in `yoloai ls` output.

Possible approaches:
- Monitor the agent process state (running vs. sleeping on stdin)
- Detect agent exit (process no longer running in the container)
- Use agent-specific hooks where available (e.g., Claude Code notification hooks)

Minimum viable: distinguish "agent running" from "agent exited" by checking if the agent process is still alive in the container.

### Enhanced `yoloai ls` dashboard

Enrich `yoloai ls` output for multi-sandbox workflows:
- Agent type and model
- Runtime duration (how long the sandbox has been running)
- Agent status (running/idle/exited)
- Workdir dirty state (has uncommitted changes)

Keep default output concise; add `--long` or `-l` flag for the full dashboard view.

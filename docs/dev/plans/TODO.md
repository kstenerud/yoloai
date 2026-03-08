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

### Agent status detection (rework planned)

Current implementation works but is fragile. See [idle detection research](../research/idle-detection.md) for full audit, external research, and architecture proposal for a pluggable detector framework.

### Test agent harness

Replace the current test agent (plain `bash`) with a proper test harness process that simulates real agent workflows: startup sequence, accepting input, simulating work, transitioning to idle, and controllable exit. Should support mimicking different detection strategies (hook-based, pattern-based, context signals) via environment variables or commands, enabling integration testing of the full idle detection pipeline. Spec TBD.

### Sandbox chaining (pipelines)

Chain sandboxes sequentially so the output of one becomes the input of the next. Each stage runs an agent with its own prompt on the workdir as modified by prior stages.

Example: `yoloai chain ./project pipeline.yaml` runs stages in order, applying each stage's changes before starting the next.

Pipeline definition (YAML or similar) specifies an ordered list of stages, each with:
- Prompt or prompt file
- Agent and model (optional, inherit from defaults)
- Whether to pause for user review between stages (`--step` flag for interactive, default is unattended)

Data flow: stage N's workdir changes are applied (auto-apply) to produce stage N+1's starting state. Intermediate diffs are preserved for inspection. If a stage's agent exits with an error or the user rejects a stage's diff in `--step` mode, the pipeline stops.

Design considerations:
- Compose with batch: independent pipelines could run in parallel
- Resume: if a pipeline stops mid-way, allow resuming from the failed stage
- Naming: sandboxes could be named `<pipeline>-stage-1`, `<pipeline>-stage-2`, etc.
- Keep intermediate sandboxes around for inspection, or clean up on success (`--cleanup`)

### Enhanced `yoloai ls` dashboard

Enrich `yoloai ls` output for multi-sandbox workflows:
- Agent type and model
- Runtime duration (how long the sandbox has been running)
- ~~Agent status (active/idle/exited)~~ (done)
- Workdir dirty state (has uncommitted changes)

Keep default output concise; add `--long` or `-l` flag for the full dashboard view.

## Idle Detection

### User-overridable detector config

Allow users to override the auto-resolved detector stack via profile-level config. A `detectors` list in profile `config.yaml` would replace the automatically computed stack, letting users disable noisy detectors or change priority order. No CLI flag — config file only.

See [idle detection research](../research/idle-detection.md) §3.9 Q1.

## Profile Enhancements

### Shared cache volumes

Allow profile config to declare named Docker volumes for package manager caches (npm, pip, cargo, etc.) that persist across sandboxes. Currently each sandbox starts with a cold cache. Shared volumes would avoid re-downloading dependencies when creating new sandboxes with the same profile.

Inspired by [amazing-sandbox](https://github.com/ashishb/amazing-sandbox), which mounts ~15 named volumes for various package manager caches.

Design considerations:
- Profile config syntax: e.g. `cache_volumes: {npm: /root/.npm, pip: /root/.cache/pip, cargo: /usr/local/cargo}`
- Volumes are named per-profile to avoid cross-profile conflicts (e.g. `yoloai-base-npm`)
- Optional: `yoloai prune --caches` to clean up cache volumes
- Consider whether the base profile should ship with sensible defaults for common caches
- Read-write mount; acceptable since these are caches, not project files

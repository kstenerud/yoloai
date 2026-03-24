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

### Per-mechanism status files

Currently all status writers (agent hooks, status-monitor.py, sandbox-setup.py) share a single `agent-status.json`, using the `source` field to distinguish writes. A cleaner approach: each mechanism writes to its own status file (`hook-status.json`, `monitor-status.json`, etc.), and the status getter reads them in priority order, preferring the most recently updated. This eliminates the `source` field hack and makes the IPC contract explicit per writer. Not part of the structured logging change — tracked separately.

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
- Workdir dirty state (has uncommitted changes)

Keep default output concise; add `--long` or `-l` flag for the full dashboard view.

## Workflow Commands

### `yoloai wait`

Block until the agent in a named sandbox exits, then return the agent's exit code. Useful for CI/CD pipelines and scripting. Without `wait`, polling `yoloai list --json` is the only way to detect completion.

```
yoloai wait <name> [--timeout <duration>]
```

- Blocks until the sandbox's tmux pane is dead (agent has exited)
- Returns the agent's exit code as yoloai's exit code (0 = done, non-zero = failed)
- `--timeout`: fail with exit code 124 (matching `timeout(1)`) if the agent hasn't exited within the duration
- Related to the deferred `yoloai run` (#56 in OPEN_QUESTIONS) — `run` would be sugar on top of `wait`

See [OPEN_QUESTIONS.md](../OPEN_QUESTIONS.md) §77.

## Sandbox Live Mounts

### `sandbox mount add` / `sandbox mount rm`

Add and remove bind mounts on a running sandbox without tearing it down. Preserves agent context when a mid-conversation need for an additional directory is discovered.

See spec in [commands.md](../../design/commands.md) `### yoloai sandbox <name> mount`.

Mechanism: `nsenter --mount --target <container-pid>` to enter the container's mount namespace and bind mount without restart. Requires root. Docker/Podman on Linux only (Tart and Seatbelt cannot support this structurally).

Persistence: added to `live_mounts` in `meta.json` (new `DirMeta` slice field); applied as regular Docker mounts on next `start`.

## Idle Detection

### User-overridable detector config

Allow users to override the auto-resolved detector stack via profile-level config. A `detectors` list in profile `config.yaml` would replace the automatically computed stack, letting users disable noisy detectors or change priority order. No CLI flag — config file only.

See [idle detection research](../research/idle-detection.md) §3.9 Q1.

## Workspace

### `yoloai apply` should pull new tags

When applying changes, also fetch any new git tags from the sandbox's copy of the workdir so that tags created by the agent (e.g. version bumps, release tags) land on the host. Currently `apply` syncs file changes but does not transfer tags.

## Seatbelt Improvements

### Per-agent sandbox access documentation

Document what filesystem paths, network endpoints, and IPC each supported agent tries to access under sandboxing. Use this to improve SBPL profile generation and agent definitions. Agent Safehouse publishes per-agent investigation reports that could serve as a starting reference.

See [competitors research](../research/competitors.md) §9.

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

## VM-Level Isolation Backends (containerd backend)

### Privileged helper for CNI/netns setup

The containerd backend currently requires running the entire `yoloai` binary as root because CNI network namespace creation (`netns.NewNamed`, bridge plugin, IPAM) requires `CAP_SYS_ADMIN` + `CAP_NET_ADMIN`. This is terrible UX — users shouldn't need `sudo` for the main binary.

Fix: extract CNI/netns operations into a small privileged helper binary (`yoloai-netsetup` or similar). The main binary calls it via exec, passing namespace name and config path. The helper is either setuid root or granted file capabilities (`setcap cap_net_admin,cap_sys_admin+ep`). This follows the same pattern Podman uses for `newuidmap`/`newgidmap`.

The helper should handle:
- `setup <nsname> <containerName> <cniConfDir>` — create netns, run CNI ADD, return JSON state
- `teardown <nsname> <containerName> <cniConfDir>` — run CNI DEL, delete netns

The main binary retains ownership of sandbox directories (written as the calling user), so `yoloai destroy` and git ops work without permission errors.

See [linux-vm-backends research](../research/linux-vm-backends.md) for full analysis.

## Architecture Cleanup

### Backend and agent extensibility refactor

Five architectural issues that cause friction when adding new backends or agents. Each is independent. See [plan](backend-agent-extensibility.md) for full spec.

Summary of issues:
1. `meta.Backend` string comparisons (`== "seatbelt"`) scattered outside the dispatch layer — should use `meta.HostFilesystem` (a `BackendCaps`-derived field stored at creation time)
2. Agent-specific switch statements in `sandbox/create.go` — should use an `ApplySettings` function field on `agent.Definition`
3. Exit-code typed errors in `internal/cli/` — nearly all CLI errors exit 1 via plain `fmt.Errorf`; the typed error system (`sandbox/errors.go`) exists but is unused where it matters
4. Several sentinel errors in `sandbox/errors.go` (`ErrDockerUnavailable`, `ErrMissingAPIKey`, `ErrContainerNotRunning`, `ErrNoChanges`) appear unused
5. (See plan for issues 5–10)

## Operational Hardening

### Log rotation

`log.txt` in the sandbox directory grows unbounded. There is no rotation or size cap. For long-running sandboxes or sessions that produce a lot of output, this can accumulate gigabytes of log data.

Options: size-based rotation (cap at N MB, keep last N files), integration with `logrotate`, or a `--max-log-size` config key. Low priority but worth addressing before GA.

### Concurrency guard for sandbox operations

No concurrency controls exist. Multiple simultaneous `yoloai new` calls with the same sandbox name, or concurrent `yoloai start`/`destroy` on the same sandbox, are not guarded. Could result in corrupted `meta.json`, double container creation, or partial state.

Fix: file-based lock per sandbox directory (e.g., `meta.lock`), held during operations that mutate sandbox state. Low priority for single-user CLIs but worth doing before any CI/CD integration.

## Network

### Comprehensive network allowlist audit

All agents need a systematic audit of actual network traffic: capture traffic during full sessions (startup, auth, operation, token refresh, telemetry) and verify the allowlist covers everything. Gemini was missing `oauth2.googleapis.com` for OAuth token refresh; other agents likely have similar gaps.

Most important for `--network-isolated` mode where missing domains cause silent failures.

See [OPEN_QUESTIONS.md](../OPEN_QUESTIONS.md) §97.

## Agent and Model Maintenance

### Model alias tracking strategy

Model aliases drift as providers release new models. Gemini's aliases already drifted once. Need a process to stay current: periodic manual review cadence, automated checks against provider APIs/docs, or pinning to stable `-latest` identifiers where available.

See [OPEN_QUESTIONS.md](../OPEN_QUESTIONS.md) §98.

### Codex research items

Three unresolved questions needed before Codex network isolation is production-ready:

- **Proxy support (#37):** Whether Codex's static Rust binary honors `HTTP_PROXY`/`HTTPS_PROXY` env vars is unverified. Critical for `--network-isolated` mode — if it ignores proxy env vars, iptables-only enforcement is the only option.
- **Required network domains (#38):** Only `api.openai.com` is confirmed. Additional domains (telemetry, model downloads) may be required. Needs traffic capture during a full Codex session.
- **TUI behavior in tmux (#39):** Interactive mode (`codex --yolo` without `exec`) behavior inside tmux is unverified. May affect idle detection and prompt delivery.

See [OPEN_QUESTIONS.md](../OPEN_QUESTIONS.md) §37–39.

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
- ~~Agent status (active/idle/exited)~~ (done)
- Workdir dirty state (has uncommitted changes)

Keep default output concise; add `--long` or `-l` flag for the full dashboard view.

## Idle Detection

### User-overridable detector config

Allow users to override the auto-resolved detector stack via profile-level config. A `detectors` list in profile `config.yaml` would replace the automatically computed stack, letting users disable noisy detectors or change priority order. No CLI flag — config file only.

See [idle detection research](../research/idle-detection.md) §3.9 Q1.

## Workspace

### `yoloai apply` should pull new tags

When applying changes, also fetch any new git tags from the sandbox's copy of the workdir so that tags created by the agent (e.g. version bumps, release tags) land on the host. Currently `apply` syncs file changes but does not transfer tags.

## Seatbelt Improvements

### ~~Dynamic toolchain detection in SBPL profiles~~ ✅

Implemented in `profile.go:toolchainReadPaths()`. `GenerateProfile()` now resolves `python3`, `node`, `ruby`, `go`, `rustc`, `java` at runtime via `exec.LookPath`, extracts installation prefixes, and adds read rules for non-system paths.

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

### gVisor integration (`security: gvisor`)

Add an optional `security` config key to profile config. When set to `gvisor`, pass `--runtime=runsc` to `docker run`. Provides meaningful isolation improvement (host kernel not directly reachable by agent code) with no KVM requirement and near-zero integration complexity.

Design:
- Profile config key: `security: standard | gvisor | kata | kata-firecracker` (default: `standard`)
- `standard` → no change (existing runc behavior)
- `gvisor` → add `--runtime=runsc` to docker run
- Preflight check: if `security: gvisor` and `runsc` not found in PATH, fail with actionable error
- Known incompatibility: agents cannot run Docker-in-Docker inside gVisor sandbox; document this

### Kata Containers integration (`security: kata`)

When set to `kata` or `kata-firecracker`, pass `--runtime=kata-qemu` or `--runtime=kata-fc`. Provides hardware VM isolation (separate kernel per sandbox) while keeping full `docker exec` compatibility via kata-agent↔vsock.

- Requires KVM on host (excludes standard cloud VMs without nested virt or .metal)
- ~1-2s start overhead, ~100-150 MB per-sandbox VM overhead
- Same preflight check pattern as gVisor
- Defer until gVisor integration is validated

Not worth building: raw Firecracker backend (requires full orchestration layer — rootfs images, vsock exec daemon, networking). Revisit only if yoloAI targets hosted/SaaS deployment model.

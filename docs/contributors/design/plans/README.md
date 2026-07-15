> **ABOUTME:** Index of designed-but-unbuilt yoloAI features and plans, one section per topic.
> Each entry links to its own plan file or research doc; start a plan here before building.

# Unimplemented Features

Designed features not yet implemented. Each links to its design spec.
Create a plan file in this directory before starting implementation.

## Post-merge roadmap (current)

The public-layering endgame (D99) merged at `b9c91834`. The remaining work — the
D99 post-merge remainder and the open findings — is scoped and sequenced in
**[post-merge-roadmap.md](post-merge-roadmap.md)** (workstreams A–E with sizes,
dependencies, platform constraints, the decisions a human must make before building
each, and a recommended phase order). The E1 microvm backend was investigated and
**retired** ([D104](../../decisions/working-notes.md#d104--retire-the-hand-rolled-qemu--m-microvm-backend-libkrun-is-the-tech-if-a-light-vm-tier-is-ever-added-e1);
[archived plan](../archive/plans/microvm-backend.md)).

## Egress proxy (workstream D) — build-ready

Design settled + validated ([D105](../../decisions/working-notes.md#d105--egress-proxy-workstream-d-brokering-is-the-default-containment-is-opt-in-phased-by-credential-material-refines-d90d95)
+ its validation addendum). The credential-broker + egress-containment proxy: brokering the
agent's API key is the always-on default, egress restriction is opt-in. Actionable build plan
in **[egress-proxy-build.md](egress-proxy-build.md)**; specs in [secure-secrets.md](../secure-secrets.md)
(D95) + [netpolicy.md](../netpolicy.md) (D90); validating spike in
[research/egress-broker-spike/](../research/egress-broker-spike/). Brokering ships on all backends and
composes with `--network-isolated` (containment step 1). **Next build:
[tamper-resistant-network-isolation.md](tamper-resistant-network-isolation.md)** — step 1.5: make the
isolation firewall un-flushable by the agent (netns-sharing installer sidecar; design validated, not
yet implemented).

## Host-artifact reclamation

Active. Make `yoloai system prune` / `doctor` reap the host-side artifacts yoloAI
orphans on the non-happy path — leaked `__inject` broker processes, CNI
netns/IPAM leases, and (macOS) seatbelt tmux servers — via an **identity-keyed
sweep** that enumerates each artifact directly and diffs against the sandbox
registry, plus **kill-before-delete** teardown ordering so the state files that
key teardown never predecease the artifact. Decision [D114](../../decisions/working-notes.md#d114);
findings DF73–DF76. Plan: [host-artifact-reclamation.md](host-artifact-reclamation.md).

## Tart network liveness detection

**Implemented 2026-07-14** (doctor probe, `info`/`ls` net-health surfacing,
smoke-harness fail-fast — all verified against a live wedged VM, DF86); plan
archived to
[archive/plans/tart-network-liveness.md](../../archive/plans/tart-network-liveness.md).
Incident: [backend-idiosyncrasies.md](../../backend-idiosyncrasies.md#tart-vmnet-session-wedges-on-a-long-idle-vm-host-sleep--subnet-re-pick--guest-drops-to-a-169254-link-local-address-agent-gets-connectionrefused).

## Architecture Remediation

Complete — the multi-quarter program (Go↔Python boundary, `runtime.Backend` interface, dependency direction, error patterns, slog conventions) landed; the plan and its audit are archived under `../archive/`. The one release-gated remnant (W1b — retire the launch-prefix legacy path) and the rest of this branch's cross-version concerns are tracked in [release-migration.md](release-migration.md).

## Public API — Layer-1 honest completion

Complete — all in-scope phases landed (Phase 1 detector truth + carved `store.Meta` → public `yoloai.Environment` read-model; Phase 2 missing verbs; Phase 3 depguard tighten via `cli-sandbox-scope`/`cli-runtime-scope`; Phases 5/6 consistency + carried-forward F6/F7/F9). Phase 4 (agent-interaction reshape) was always a separate later effort with its own plan and is not part of this program. Archived under [../archive/plans/layer1-completion.md](../archive/plans/layer1-completion.md) (with implemented-shape divergences noted there). Supersedes [layer1-public-api.md](../archive/plans/layer1-public-api.md).

## Public API — Engine owns the lazy runtime

Complete — D74 landed (Stage 1 `9f67d28`/`45aab36`/`8479258`, Stage 2 `8d8106c`/`0b38b3e`/`5321a9e`); the `Engine` owns the lazy `runtime.New` connection and the sub-handles (`Agent`/`Workdir`/`Network`/`Files`) hold a `*sandbox.Engine`. Archived under [../archive/plans/engine-owns-runtime.md](../archive/plans/engine-owns-runtime.md).

## Public layering — composable layers staged behind `internal/`

Active. Decompose yoloAI into a stack of composable public layers (`runtime`, `store`, `copyflow`,
`agent`, a new agent-free managed lifecycle, …) under an 80/20 surface: the `yoloai` package stays
the small stable top; the layers are opt-in for power users, and the library is built on the same
layers. Strategy: shape every layer **as if public** but keep it under `internal/` (retaining
churn-freedom), mirror the future public paths 1:1, and promote by a mechanical move last. One
module throughout. Proceeds via audit cycles (mechanical `go list -deps` separation + semantic
conflation review) draining to the findings/questions queues. Supersedes the deferred C-full/F
notes in [D83](../../decisions/working-notes.md). Frame doc: [public-layering.md](public-layering.md).

### The Move (S6) — pre-Move audit + Stage 3b surface cleanup

The substrate/store promotion is gated on a surface-cleanup sub-phase (Stage 3b) decided by
the pre-Move audit ([move-audit.md](move-audit.md), [D97](../../decisions/working-notes.md)):
the runtime contract is request-in/no-mechanism-out (architecture-principles §4), agent-shaped
remnants leave, and the substrate record becomes agent-free. In progress on `substrate-move`.
Sub-tasks with their own scope: **[store-workload-split.md](store-workload-split.md)** ([D98](../../decisions/working-notes.md))
— split the inside-process config (`agent`/`model`) out of `store.Environment` into an
orchestration-owned `agent.json` (M2 migration); **[session-carve.md](session-carve.md)** — Phase 1a
(the long pole): the session-layer public realization (`IOSession` on `sb.Agent()`, the final
`Launch`/`ProcSpec` contract, `AgentLaunchPrefix` off the descriptor, one-shot `-p`/Tier-3).

### Endgame (D99) — drive to a solid, mergeable state

The whole program now runs to a **solid, mergeable state** in `substrate-move`, landing on `main` as
**one clean break** (incidental per-commit contract churn is fine). Three phases — P1 seal every
interface (the session carve + Q104 + `paths.go`), P2 the control-eval consumer surface
(`wait`/`sandbox_run`/usage/diff), P3 the Move — then a low-priority remainder. Frame:
[public-layering.md](public-layering.md) §Endgame + [D99](../../decisions/working-notes.md).

## Env access seal — `config.HostEnv` curated accessors

Replace the ad-hoc `config.Layout` env accessors (`LookupEnv`/`ExecEnv`/
`CuratedEnv`/`EnvSnapshot`) with a single opaque, purpose-method,
forbidigo-gated `HostEnv` type — every env touch names a purpose whose keyset
is decided centrally (`GitEnv`-style), not chosen inline at the call site. A
first encapsulation pass already landed (`9223058`); this supersedes its design.
Plan + full handoff: [env-access-seal.md](env-access-seal.md).

## Multi-workdir diff/apply

Let one sandbox `:copy`-track **multiple** project dirs (not just the single positional
workdir) so copy/diff/apply works on each. Restores the capability [Q-U](../../decisions/working-notes.md)
removed, behind the clean surface it deferred: bulk ops span all tracked dirs, precise
ops (`<ref>`/`-- pathspec`) name exactly one; a specifier is required only when 2+ dirs
are tracked; `apply --all` lands every dir independently. Decision [D81](../../decisions/working-notes.md#d81).
Plan: [multi-workdir-diff-apply.md](multi-workdir-diff-apply.md).

## Apply drift guard

Design draft (not yet a locked decision). Close the gap between `yoloai diff` and a
later `yoloai apply`: if the agent changes the tracked dir after the user reviews
`diff` but before `apply` lands, `apply` today silently lands whatever is beyond
baseline at that moment, reviewed or not. Fingerprint the exact artifact `apply`
would land (ordered beyond-baseline commit SHAs for the default mode; a content
hash of the generated patch for `--no-commit`) when a plain `diff` runs, and balk
unconditionally (ignoring `--yes`) if `apply` later finds a mismatch — cleared only
by re-running `diff`, no `--force` bypass. Plan: [apply-drift-guard.md](apply-drift-guard.md).

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

~~Currently all status writers (agent hooks, status-monitor.py, sandbox-setup.py) share a single `agent-status.json`, using the `source` field to distinguish writes. A cleaner approach: each mechanism writes to its own status file.~~ **Largely resolved.** The `source` field existed only because `HookDetector` read hook signals back out of the monitor's own `agent-status.json` output — a feedback loop that could leave it unable to confirm idle. `HookDetector` now reads the append-only hook event log (`logs/agent-hooks.jsonl`, written exclusively by the agent's hooks) instead, and the `source` field has been removed. `agent-status.json` is now purely the monitor's output channel for the host; its remaining multi-writer "last write wins" behavior is benign (every writer just expresses the current host-facing status). A full per-writer file split is no longer needed.

### Agent status detection (rework planned)

Current implementation works but is fragile. See [idle detection research](../research/idle-detection.md) for full audit, external research, and architecture proposal for a pluggable detector framework. **A concrete design+build plan now exists** — [agent-owned-detection.md](agent-owned-detection.md) — folding in fall-to-shell + `yoloai-resume`: make detection the agent's own responsibility (an agent-agnostic `DetectionSpec`, uniform external-watch mechanism first), have the launch wrapper own the exit lifecycle (write `done` + fall to an interactive shell + resume hint), and add a `yoloai-resume` one-command resume. Phase-gated with real-Docker checkpoints to protect the fragile detection layer. Supersedes the deferred fall-to-shell item in [public-layering.md](public-layering.md).

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

See [OPEN_QUESTIONS.md](../questions-unresolved.md) §77.

## Idle Detection

### User-overridable detector config

Allow users to override the auto-resolved detector stack via profile-level config. A `detectors` list in profile `config.yaml` would replace the automatically computed stack, letting users disable noisy detectors or change priority order. No CLI flag — config file only.

See [idle detection research](../research/idle-detection.md) §3.9 Q1.

### Per-agent custom detection strategies (🚧 public-layering merge gate)

[agent-detection-strategies.md](agent-detection-strategies.md) — deferred tail of
[agent-owned-detection.md](agent-owned-detection.md). Promote detection to a
first-class per-agent strategy and wire each agent's native turn-completion
callback (Codex `notify`, Gemini `AfterAgent`, OpenCode `session.idle`, Aider
`--notifications-command`; survey-backed by
[research/agent-callbacks.md](../research/agent-callbacks.md)). **The
`public-layering` branch does not merge to `main` until this is done.**

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

## Tart Runtime

### Skip symlink creation for `:copy` workdirs — RESOLVED (was stale)

**Resolved 2026-06-11 (DF27).** This item described a `:copy`-workdir symlink failure for temp-dir sources. Verified on an Apple Silicon host that it **does not reproduce** — `yoloai new` with a `/var/folders` temp-dir `:copy` workdir creates cleanly; the symlink-skip in `mounts.go` already handles it. The real blockers to Tart run coverage were the `idle` agent's non-portable `sleep infinity` and tart `Start`'s coupling to the sandbox monitor — both fixed; tart now participates in `RunInterfaceConformance` (`TestTartConformance`). See `findings-resolved.md` DF27.

Source TODO: `sandbox/integration_tart_test.go:33-37` (the test is skipped pending this fix).

## MCP Server

### MCP servers don't fully work inside sandboxes

Two limitations in MCP-inside-sandbox support today, surfaced by OQ #93
(closed 2026-05-27 — the architectural concern was resolved by W-L8b's
StdioExec abstraction, but the user-facing gaps remain):

1. **Stdio MCP servers need their binary in the sandbox image.** Claude
   Code's `settings.json` and `~/.claude.json` get seeded into the
   sandbox, but the agent then tries to spawn each configured MCP
   server (e.g. `npx @modelcontextprotocol/server-foo …`) as a child
   process. If the binary isn't installed in the sandbox image, the
   server fails to start and the agent loses that capability —
   silently, in most cases.

2. **Network MCP servers reference `localhost`.** Many MCP server
   configs point at `localhost:N` (e.g. an MCP server the user runs
   on their host). Inside the sandbox `localhost` resolves to the
   sandbox itself, not the host, so these connections fail.

**Workarounds available today:** custom profile
(`~/.yoloai/profiles/<name>/Dockerfile`) can install MCP binaries and
rewrite the MCP config to use `host.docker.internal` (Docker) / the
equivalent on other backends — `BackendDescriptor.HostFromContainer`
exposes the right hostname per backend. Users with MCP-heavy workflows
are expected to build a profile today.

**Possible future improvements:**
- Detect MCP server entries during sandbox creation and warn when
  referenced binaries aren't likely to be available.
- Auto-rewrite `localhost` MCP references to `HostFromContainer` on
  sandbox creation.
- Provide a `yoloai system mcp install …` helper that builds a profile
  with the requested MCP servers baked in.

These are open-ended feature work, not maintenance items — punt to
roadmap-driven design when MCP-in-sandbox usage justifies it.

### Pass runtime to MCP diff tool for non-Docker backends

`internal/mcpsrv/tools.go` calls `patch.GenerateDiff` with `Runtime: nil`, which works for Docker (host-side git) but fails for Tart (where git runs inside the VM via the runtime exec). The MCP server doesn't currently have a runtime handle; it would need one to support diff for VM-backed sandboxes.

Fix: thread the active `runtime.Backend` through the MCP server struct (`internal/mcpsrv/server.go`) and pass it via `patch.DiffOptions.Runtime` for the affected MCP tools. Verify against Tart on Apple Silicon once that backend is fully tested.

Source TODO: `internal/mcpsrv/tools.go:304-307` ("MCP is primarily used with Docker backends, we pass nil for now").

## VM-Level Isolation Backends (containerd backend)

### Privileged helper for CNI/netns setup

The containerd backend currently requires running the entire `yoloai` binary as root because CNI network namespace creation (`netns.NewNamed`, bridge plugin, IPAM) requires `CAP_SYS_ADMIN` + `CAP_NET_ADMIN`. This is terrible UX — users shouldn't need `sudo` for the main binary.

Fix: extract CNI/netns operations into a small privileged helper binary (`yoloai-netsetup` or similar). The main binary calls it via exec, passing namespace name and config path. The helper is either setuid root or granted file capabilities (`setcap cap_net_admin,cap_sys_admin+ep`). This follows the same pattern Podman uses for `newuidmap`/`newgidmap`.

The helper should handle:
- `setup <nsname> <containerName> <cniConfDir>` — create netns, run CNI ADD, return JSON state
- `teardown <nsname> <containerName> <cniConfDir>` — run CNI DEL, delete netns

The main binary retains ownership of sandbox directories (written as the calling user), so `yoloai destroy` and git ops work without permission errors.

See [linux-vm-backends research](../research/linux-vm-backends.md) for full analysis.

## Isolation Modes

### Podman + gVisor (`container-enhanced`)

gVisor `container-enhanced` works on the docker backend (Linux + macOS, per D69/D70) but not yet on podman. Plan: [podman-gvisor.md](podman-gvisor.md). Central research question is whether **rootless** podman can run gVisor (and how) — rootless is a first-class goal since it's a main reason users pick podman, not something to route around with "use rootful." Also fixes the host-`$PATH` runsc check for VM-backed Podman Machine (mirroring the docker daemon-location fix) and adds a `containers.conf` "runsc registered" check.

### `yoloai system setup-gvisor` (macOS)

An opt-in command to install + register `runsc` in the macOS Docker VM so `container-enhanced` works without manual VM surgery. Plan: [setup-gvisor.md](setup-gvisor.md). Blocking decision: the OrbStack `/tmp → /private/tmp` collision has no clean per-process fix, so the command must either replace the VM's `/tmp` (breaks OrbStack's `/tmp` sharing) behind explicit confirmation, or steer to Docker Desktop. Open research: whether runsc is installable in Docker Desktop's read-only LinuxKit VM at all.

## Apple `container` backend

Add an Apple `container` runtime backend (Linux OCI in per-container VMs, macOS 26 / Apple Silicon) — Docker-class mount-mode parity with stronger isolation, reusing existing profile images. Spike-verified; see [research/apple-container.md](../research/apple-container.md). Modeled as a **vm-tier** backend (`BaseModeName: IsolationModeVM`) that fills the macOS Linux-VM gap (`--isolation vm` for Linux currently degrades to docker). Because its VMs boot sub-second, it becomes the **macOS default** above the container slot — default macOS isolation shifts to VM when installed. macOS preference: `apple` > `orbstack` > `docker-desktop` > `podman` (OrbStack/Desktop hidden behind one `docker` backend); Linux keeps VMs low-priority (heavy/slow). Includes two cross-cutting reworks folded in: (1) a setup-wizard rework — a flat list of default-environment presets (each writing `os`/`isolation`/`container_backend`), sharing one priority order with the runtime auto-pick; (2) a two-tier probe across all backends (installed = binary exists; running = reachable now; select by installed, use by running, start-on-demand). Plan: [apple-container-backend.md](apple-container-backend.md). Backend key = `apple`; no design decisions remain open.

## Architecture Cleanup

### Backend and agent extensibility refactor

Complete — all ten architectural-cleanup issues are resolved in current code (verified 2026-06-08): `HostFilesystem` + `AgentProvisionedByBackend` `BackendCaps` fields replace the scattered `== "seatbelt"` / seed checks; `agent.Definition.ApplySettings`/`ShortLivedOAuthWarning`/`SeedsAllAgents` replace the create-layer agent switches; `EnsureSetup`/`IsReady` and `TmuxSocket(sandboxDir)` replace the image/tmux-socket verbs; `InstanceConfig` is field-grouped with a go.md convention; `store.Environment` carries `Version` + `migrate()`; CLI uses the typed exit-code errors; the `os.Setenv` mutation and the dead sentinels are gone. Archived under [../archive/plans/backend-agent-extensibility.md](../archive/plans/backend-agent-extensibility.md) (per-issue divergence notes there).

## Operational Hardening

### Copy-mode host-git RCE (security audit C1, CRITICAL) — IMPLEMENTED

Agent-controlled git `filter`/`diff`/`fsmonitor` drivers in the copy-mode work-copy `.git/config` executed on the host during `yoloai diff`/`apply`/`status`. **Fixed (D108):** the work-copy git now runs in the agent's confinement via `runtime.GitRunsInConfinement` + each backend's `GitExec`; verified on real Docker + Podman. Residuals (seatbelt host git; broken-metadata probe) tracked as DF67. Plan: [copy-mode-git-rce.md](copy-mode-git-rce.md).

### `:overlay` CAP_SYS_ADMIN host escape (security audit H2) — RESOLUTION: RETIRE overlay

`:overlay` mounts kernel overlayfs, which forces `CAP_SYS_ADMIN` + `apparmor=unconfined`; on Docker rootful (no userns remap) + the agent's passwordless sudo this is a host escape. The fuse-overlayfs "fix" was **audited and refuted** (needs the same cap outside a user namespace — a GEN §14 trap). **Decision (D109): retire `:overlay`** and recover its benefit via reflink-aware `:copy` (copy-on-write clones, verified safe across btrfs/XFS/APFS with graceful fallback). v0.6.0 interim: documented dangerous opt-in + loud warning. Active plan: [retire-overlay-reflink-copy.md](retire-overlay-reflink-copy.md); audit record: [overlay-sysadmin-escape.md](overlay-sysadmin-escape.md).

### Crash-safe data-dir migrations (DF68) — IMPLEMENTED (D110)

Built and independently audited on branch `crash-safe-migration`: `internal/fileutil` durable primitives + `internal/migrate` crash-safe promotion state machine + `Migrator` plan/apply gate + v3→v4 `OverlayFlatten` migrator. Design doc: [../../archive/crash-safe-migration.md](../../archive/crash-safe-migration.md); post-build audit: [../../archive/crash-safe-migration-audit.md](../../archive/crash-safe-migration-audit.md).

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

See [OPEN_QUESTIONS.md](../questions-unresolved.md) §97.

## Agent and Model Maintenance

### Model alias tracking strategy

Model aliases drift as providers release new models. Gemini's aliases already drifted once. Need a process to stay current: periodic manual review cadence, automated checks against provider APIs/docs, or pinning to stable `-latest` identifiers where available.

See [OPEN_QUESTIONS.md](../questions-unresolved.md) §98.

### Codex research items

Three unresolved questions needed before Codex network isolation is production-ready:

- **Proxy support (#37):** Whether Codex's static Rust binary honors `HTTP_PROXY`/`HTTPS_PROXY` env vars is unverified. Critical for `--network-isolated` mode — if it ignores proxy env vars, iptables-only enforcement is the only option.
- **Required network domains (#38):** Only `api.openai.com` is confirmed. Additional domains (telemetry, model downloads) may be required. Needs traffic capture during a full Codex session.
- **TUI behavior in tmux (#39):** Interactive mode (`codex --yolo` without `exec`) behavior inside tmux is unverified. May affect idle detection and prompt delivery.

See [OPEN_QUESTIONS.md](../questions-unresolved.md) §37–39.

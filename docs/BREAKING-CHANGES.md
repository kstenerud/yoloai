# Breaking Changes

Tracks breaking changes made during beta. Each entry should be included in release notes for the version that introduces it.

## Unreleased

### Apply moves under `client.Sandbox(name).Workdir()`

Step 4a of the F2 re-rooting (the first of several apply sub-steps). Go-embedder
API change; CLI behavior unchanged.

**Previous behavior:** `c.Apply(ctx, name) (*patch.ApplyResult, error)` and
`c.ApplyWithOptions(ctx, name, ApplyOptions) (*patch.ApplyResult, error)` —
returning the internal `*patch.ApplyResult`.

**New behavior:** `c.Sandbox(name).Workdir().Apply(ctx, yoloai.ApplyOptions{IncludeWIP})
(*yoloai.ApplyResult, error)`. `ApplyResult` is now a public root alias (closing
the F1 fence leak); `ApplyOptions` moved to the root `Workdir` surface.

**Migration:**
- `c.Apply(ctx, name)` → `c.Sandbox(name).Workdir().Apply(ctx, yoloai.ApplyOptions{})`
- `c.ApplyWithOptions(ctx, name, opts)` → `c.Sandbox(name).Workdir().Apply(ctx, opts)`

**4b (squash):** `Workdir().Apply` *is* the squash apply (single flattened patch).
`Client.GeneratePatch` is removed — its only caller (the CLI `--squash` path) now
routes through `Workdir().Apply`, with generate/validate/apply/advance-baseline
relocated into the library. `ApplyOptions` gains `Paths []string` (path-filtered
apply; baseline not advanced) and `DryRun bool` (generate + validate + return the
stat, without applying — the library never prompts, so the CLI uses DryRun to
preview before confirming). Migration: `c.GeneratePatch(ctx, name, paths, wip)`
→ `c.Sandbox(name).Workdir().Apply(ctx, yoloai.ApplyOptions{Paths: paths,
IncludeWIP: wip, DryRun: true})` then read `result.Stat` / apply with `DryRun: false`.

**Still pending:** the selective-ref / `--patches` export / overlay variants —
whose orchestration still lives in the CLI (`apply_*.go`) — fold into
`Workdir().Apply` in sub-steps 4c–4e.

### Diff moves under `client.Sandbox(name).Workdir()`

Folds the four `Client` diff methods into one verb on the workdir sub-handle (F2,
Step 3). Go-embedder API change; CLI behavior unchanged.

**Previous behavior:** `c.Diff(ctx, name)`, `c.DiffWithOptions(ctx, name, paths,
stat, nameOnly)`, `c.DiffOverlay(ctx, name, stat, nameOnly)`, and
`c.DiffRef(ctx, name, ref, stat)` — four methods, with the caller choosing the
copy vs. overlay variant.

**New behavior:** `c.Sandbox(name).Workdir().Diff(ctx, yoloai.DiffOptions{Paths,
Stat, NameOnly, Ref}) (string, error)`. Copy-vs-overlay is resolved internally
from the workdir's mount mode, so the overlay-explicit `DiffOverlay` is gone;
`Ref` selects a commit/range (still refused for overlay — commits aren't
host-addressable). `""` means no changes.

**Migration:**
- `c.Diff(ctx, name)` → `c.Sandbox(name).Workdir().Diff(ctx, yoloai.DiffOptions{})`
- `c.DiffWithOptions(ctx, name, paths, stat, nameOnly)` → `…Workdir().Diff(ctx, yoloai.DiffOptions{Paths: paths, Stat: stat, NameOnly: nameOnly})`
- `c.DiffOverlay(…)` → drop it; `…Workdir().Diff(ctx, yoloai.DiffOptions{Stat, NameOnly})` auto-detects overlay.
- `c.DiffRef(ctx, name, ref, stat)` → `…Workdir().Diff(ctx, yoloai.DiffOptions{Ref: ref, Stat: stat})`

**Rationale:** F2 / Q-G — diff/apply belong on a `Workdir()` sub-handle, and one
mode-agnostic verb removes the copy/overlay branching from every caller. (The
patch-generation methods stay on `Client` for now; they fold into `Apply` in a
later step, since they're apply-plumbing with an overlay shape that doesn't fit a
single byte-returning `Patch`.)

### Per-sandbox operations move from `Client` to `client.Sandbox(name)`

Re-roots every per-sandbox Go operation onto the resource-bound `*yoloai.Sandbox`
handle (F2). CLI behavior is unchanged; this is a Go-embedder API change.

**Previous behavior:** per-sandbox ops were methods on `*Client` taking a `name`
string — `c.Inspect(ctx, name)`, `c.Stop(ctx, name)`, `c.Start(ctx, name, opts)`,
`c.Reset(ctx, sandbox.ResetOptions{Name: name, …})`, `c.Destroy(ctx, name, force)`,
`c.Attach(ctx, name, io)`, `c.Exec(ctx, name, cmd, io)`, `c.StdioExec(…)`,
`c.SendInput(ctx, name, text)`, `c.ContainerLogs(ctx, name, n)`,
`c.SandboxDir(name)`, `c.NeedsConfirmation(ctx, name)`.

**New behavior:** call them on the handle `c.Sandbox(name)`, with `name` dropped
from each signature:

- `c.Sandbox(name).Inspect(ctx)` / `.Stop(ctx)` / `.Start(ctx, opts)` /
  `.Restart(ctx, opts)` / `.SendInput(ctx, text)` / `.ContainerLogs(ctx, n)`.
- `.Reset(ctx, yoloai.ResetOptions{…})` — `ResetOptions` is now a public
  hand-written struct: no `Name` (the handle supplies it), and `Restart` is
  renamed `RestartContainer`.
- `.Destroy(ctx, yoloai.DestroyOptions{Force})` — with `Force` false it returns
  a typed `*ActiveWorkError` (carrying the reason) instead of the removed
  `ErrUnappliedChanges` sentinel. `Client.NeedsConfirmation` is gone; the pure
  pre-check is `c.Sandbox(name).HasActiveWork(ctx) (bool, reason)` for batch
  "check-all-then-prompt" flows.
- `.Exec(ctx, yoloai.ExecOptions{Command, PTY}, io)` — folds the old `Exec`
  (now `PTY: true`) and `StdioExec` (`PTY: false`, pipes `io.In/Out/Err`).
- `.Dir()` replaces `SandboxDir(name)`.
- New public types: `Info`, `Status` (+ `StatusActive…` consts), `StartOptions`
  (re-exported aliases); `ResetOptions`, `DestroyOptions`, `ExecOptions`
  (hand-written). `Client.List`/`Run` now return `*yoloai.Info` (an alias of the
  previous `*sandbox.Info` — same type).

**Migration:** insert `.Sandbox(name)` and drop the `name` argument; replace
`Destroy(name, force)` with `Sandbox(name).Destroy(ctx, yoloai.DestroyOptions{Force: force})`;
switch `errors.Is(err, ErrUnappliedChanges)` to `errors.As(err, &*ActiveWorkError)`;
move `ResetOptions.Restart` → `RestartContainer` and drop its `Name`.

**Rationale:** F2 / Q-G (Shape B). Name-bound handles group per-sandbox ops behind
one accessor, drop the repeated `name` argument and the method-prefix sprawl, and
make `Destroy`'s safety check atomic (typed refusal, no check-then-act gap).

### Public creation surface: `Create` takes `yoloai.CreateOptions`, `Backend` required, dirty workdir refused

This reshapes the Go embedding API for sandbox creation (F1/F3/F4). The CLI is
unaffected except where noted.

**Previous behavior:**

- `Client.Create(ctx, sandbox.CreateOptions)` took the *internal* struct —
  uncallable by external embedders (they can't import `internal/sandbox`).
- `Options.Backend` was optional; empty auto-resolved from config, optionally
  routed via `Options.Isolation` / `Options.OS`.
- `Run` (and the MCP create path) silently proceeded when the workdir had
  uncommitted git changes; the CLI `new` prompted unless `--yes`. The
  `CreateOptions.Yes` flag conflated "non-interactive" with "proceed on dirty".
- A `.yoloai.yaml` `requires:` block prompted "Continue anyway?" (version
  verification was, and is, unimplemented).

**New behavior:**

- `Client.Create(ctx, yoloai.CreateOptions)` takes a **public** struct built
  from re-exported types (`yoloai.DirSpec`, `DirMode`, `NetworkMode`,
  `PortMapping`, …), so external embedders can construct it. `Run` is sugar over
  `Create`. `CreateOptions.Ports` is `[]yoloai.PortMapping` (was `[]string`).
- `Options.Backend` is **required** — empty returns a `*UsageError` (exit 2).
  `Options.Isolation` / `Options.OS` are removed. New `yoloai.SelectBackend(ctx,
  preferred, isolation, os)` does the CLI's auto-detect/routing explicitly;
  call it and pass the result into `Options.Backend`.
- A dirty workdir now yields a typed **`*yoloai.DirtyWorkdirError`** (exit 12)
  rather than a silent proceed or an in-library prompt. Acknowledge it with
  `CreateOptions.AllowDirtyWorkdir`, the per-directory `DirSpec.AllowDirty`, or
  `RunOptions.AllowDirtyWorkdir`. The library never prompts; the CLI `new`
  catches the error, warns, prompts, and retries (`--yes` pre-acks).
  `CreateOptions.Yes`, `Attach`, and `Version` are gone (`Version` moved to
  `Options.Version`; attach is a separate post-create step).
- `requires:` is now a non-blocking warning — no prompt, no error.
- `DirSpec.Force` (the `:force` dangerous-path override) is renamed
  `DirSpec.AllowDangerousPath`. The user-facing `:force` mount suffix is unchanged.

**Migration:**

- Embedders calling `Create` with `sandbox.CreateOptions` switch to
  `yoloai.CreateOptions` (drop `Yes`; set `AllowDirtyWorkdir: true` to keep the
  old proceed-on-dirty behavior; move `--port`-style strings to `PortMapping`).
- Construct the `Client` with an explicit `Backend` (e.g. `yoloai.BackendDocker`
  or `yoloai.SelectBackend(...)`); drop any `Options.Isolation` / `Options.OS`.
- Handle `*yoloai.DirtyWorkdirError` from `Run`/`Create` (or pre-ack it) where
  you previously relied on the implicit proceed.

**Rationale:** F1/F3/F4 + the D24 decision. Backend selection is ambient, so it
belongs at the boundary, not hidden in construction (§4/§12). Proceeding on a
dirty workdir is a real data-loss risk, so it must be a conscious, *named* ack
(`AllowDirtyWorkdir`) rather than a blanket `Yes` — a headless caller that set
`Yes: true` only to silence prompts was silently disabling the dirty guard.

### `yoloai sandbox <name> allowed --json` carries per-domain provenance

**Previous behavior:** `yoloai sandbox <name> allowed --json` emitted a flat `domains` array of strings:

```json
{ "name": "mybox", "network_mode": "isolated", "domains": ["api.anthropic.com", "example.com"] }
```

The same went for the `domains_removed` field of `yoloai sandbox <name> deny --json` and the Go API's `meta.NetworkAllow []string`.

**New behavior:** Each domain is now an object with a `source` field that identifies why it's on the list. The Go API exposes the same shape as `[]yoloai.AllowedDomain`.

```json
{
  "name": "mybox",
  "network_mode": "isolated",
  "domains": [
    {"domain": "api.anthropic.com", "source": "agent-requirement"},
    {"domain": "example.com",       "source": "user"}
  ]
}
```

The two source values:

- `"agent-requirement"` — the bound agent's `agent.Definition.NetworkAllowlist` requires this domain (e.g. `api.anthropic.com` for Claude). Removing it will break the agent itself.
- `"user"` — added by the user via `--network-allow` at create time or `yoloai sandbox <name> allow` at runtime.

Human-readable output also gains an `" (agent requirement)"` annotation next to each agent-required entry, and `yoloai sandbox <name> deny` now prints a warning when the removal hits an agent-required domain. The library does not block the removal (that's a UI policy decision).

**Migration:**

- Shell pipelines that did `yoloai sandbox <name> allowed --json | jq '.domains[]'` and expected raw strings should switch to `jq '.domains[].domain'`.
- Embedders using `yoloai.Client.Sandbox(name).Network().Allowed(ctx)` get `[]yoloai.AllowedDomain`; the `Source` field is a `yoloai.DomainSource` enum (`AllowedFromAgentRequirement` / `AllowedFromUser`).
- The on-disk `environment.json` (`meta.NetworkAllow`) is unchanged — it still stores `[]string`. Provenance is recovered at read time, so existing sandboxes continue to work without migration.

**Rationale:** Q-V (`api_surface.go` Q-V resolution 2026-05-25). Flattening provenance at the API boundary was the same anti-pattern as the Q-Q CLI-UI leak: information the implementation can answer for was being thrown away. Two real use cases motivated the change — "don't silently nuke an agent-required domain" warnings and "show me my additions vs baked-in defaults" management UIs.

### Auxiliary `:copy` and `:overlay` are no longer supported

**Previous behavior:** Any directory passed via `-d` (auxiliary mount) could carry the same `:copy` and `:overlay` mode suffixes as the workdir, in which case the directory participated in `yoloai diff` / `yoloai apply` alongside the workdir. The workflow was all-or-nothing: a single multi-directory diff was emitted, apply ran per-directory in sequence, and the first failure halted the chain.

**New behavior:** `-d` arguments accept only `:rw` (live bind-mount) and the default `:ro` (read-only reference). Passing `-d <path>:copy` or `-d <path>:overlay` now errors with a `*UsageError` pointing at the alternatives. `yoloai diff` / `yoloai apply` operate on the workdir only.

**Migration:** Pick whichever fits each use case:

- The aux dir was effectively a second project you wanted to track changes in → make it the workdir of a separate sandbox.
- The aux dir was sibling code (monorepo cousin, docs in a separate repo, tools repo) where live edits are fine → mount as `:rw` for a live bind.
- The aux dir was reference material the agent should read but not edit → leave it default (`:ro`); behavior unchanged.
- If the aux dir was a parent that contained the real project → make the parent the workdir.

**Rationale:** Q-U (`docs/dev/plans/layering-refactor.md` W-L8b; `api_surface.go` Q-U resolution 2026-05-25). The multi-directory diff/apply implementation was real but the user-visible surface was barely used: no per-directory selection, no cross-directory conflict resolution, no `:overlay`-only-not-`:copy` filtering. The API complexity required to do this properly significantly exceeded what the implementation actually did. Removing the surface now while we're in beta keeps `yoloai diff` / `yoloai apply` simple — if a real use case emerges, it can be restored with an API informed by that need.

**Affected Go API (for embedders):**

- `yoloai.Client.GenerateMultiPatch` — removed. The single-dir replacement is `yoloai.Client.GeneratePatch`.
- `yoloai.Client.Apply` / `Client.ApplyWithOptions` — return shape narrows from `([]*patch.ApplyResult, error)` to `(*patch.ApplyResult, error)`. A `nil` result is the no-op signal.
- `sandbox/patch.ApplyAll` — same return shape change.
- `sandbox/patch.GenerateMultiPatch` — removed.
- `sandbox/patch.LoadAllDiffContexts` — still exists, still returns a slice, but the slice now has at most one entry (the workdir). Loop callers don't need to change.

### First-run setup is non-interactive when triggered implicitly; `yoloai system setup` is the explicit wizard

**Previous behavior:** On the first `yoloai new` / `yoloai run` of a fresh install (when `setup_complete=false`), if stdin was a TTY the user was dropped into the interactive setup wizard before the sandbox could be created — three prompts (tmux config, default backend on macOS, default agent). When stdin wasn't a TTY, the same code path auto-configured silently with `tmux_conf=default+host`.

**New behavior:** Implicit first-run setup is **always non-interactive**: it writes `tmux_conf=default+host` and marks `setup_complete=true`, then proceeds. The interactive wizard is only run explicitly via `yoloai system setup` (which is also how a user re-runs setup to change their defaults).

**Rationale:** Q-F (`docs/dev/plans/layering-refactor.md` W-L8b) resolved that library entry points must not perform interactive IO — the CLI owns prompts. The previous behavior coupled `sandbox.Manager` to stdin/stdout and made first-run UX unpredictable depending on TTY state. The new shape:

- `yoloai.SystemClient.Setup(ctx, opts)` is a pure write — caller supplies every answer (TmuxConf, Backend, Agent) via SetupOptions.
- `yoloai.SystemClient.SetupStatus(ctx)` returns host inspection (tmux classification + available backends/agents) so external wizards (CLI, future HTTP/MCP) can render their own prompts.
- The CLI wizard (`internal/cli/system_setup.go`) reads SetupStatus, prompts the user where flags aren't supplied, and calls Setup.

**Migration:**
- If you relied on `yoloai new` prompting on first run, run `yoloai system setup` once after install (or before your first `yoloai new`). Subsequent runs are unaffected.
- CI / scripted installs already running on non-TTY stdin see no behavior change.
- Embedders calling `Client.Run` before configuring defaults still auto-get `default+host` — no code change needed.
- Embedders that want the interactive wizard's behavior should call `SystemClient.SetupStatus` + their own prompt UI + `SystemClient.Setup(opts)`.

### `yoloai system prune` always operates across all backends; `--all` and `--backend` removed

**Previous behavior:** `yoloai system prune` accepted `--backend <name>` to prune only that backend and `--all` to prune across every available backend. Default was the configured default backend.

**New behavior:** The command always prunes across every backend that's currently available. Both `--all` and `--backend` flags have been removed; passing them now errors.

**Rationale:** Per-backend pruning had no real-world use case — orphan detection is keyed off the sandbox dir on the host, which is shared across all backends. Selecting one backend either matched the same orphan set (if that backend was the only one with state) or missed orphans on other backends (silently). Always-cross-backend matches user expectation and matches the `Client.System().Prune` library shape resolved under Q-L. Tracked in `docs/dev/plans/layering-refactor.md` W-L8b Q-L.

**Migration:**
- Drop `--all` and `--backend` from scripts and CI invocations.
- The behavior of bare `yoloai system prune` matches the old `--all` invocation.

### `yoloai system runtime` renamed to `yoloai system tart`

**Previous behavior:** Apple simulator runtime base images were managed via `yoloai system runtime add|list|remove`. The command name read as generic, but the surface is structurally Tart-only (and is the only CLI subtree that imports `runtime/tart` directly).

**New behavior:** The subtree is now `yoloai system tart` (Pattern B, mirroring `podman machine`). The old `yoloai system runtime ...` invocations continue to work as a hidden alias and print a deprecation warning to stderr. The alias will be removed in a future breaking-changes window.

**Rationale:** Backend-scoped commands should name the backend explicitly. The new name makes the Tart scope honest and reserves `yoloai system <backend>` as the convention for any future backend-specific surfaces. Tracked in `docs/design/layering.md` D1 and W-L2 of `docs/dev/plans/layering-refactor.md`.

**Migration:**
- Replace `yoloai system runtime ...` with `yoloai system tart ...` in scripts, docs, and CI.
- Until the alias is removed, the old invocation still works but emits a warning.

### `yoloai apply` defaults to commits-only; `--no-wip` removed in favor of `--include-wip`

**Previous behavior:** `yoloai apply` applied agent commits AND uncommitted work-in-progress edits as unstaged files in the same invocation. `--no-wip` opted out of the WIP application.

**New behavior:** `yoloai apply` defaults to **committed changes only**. Uncommitted edits the agent left in the work copy are detected, reported as a hint, and NOT applied. To bring them across as unstaged modifications (the old default), pass `--include-wip`. The `--no-wip` flag has been removed.

The flip applies uniformly across the apply surface:

- Default format-patch path: applies commits; prints `Note: sandbox has uncommitted changes …` when WIP is present and excluded.
- `--squash`: flattens commits only (`git diff baselineSHA HEAD`). With `--include-wip`, flattens everything including uncommitted (`git diff baselineSHA`, after `git add -A`).
- `--patches`: writes `*.patch` for commits; writes `wip.diff` only when `--include-wip` is set.
- `--squash` and `--include-wip` are no longer mutually exclusive — `--squash` controls patch shape, `--include-wip` controls scope.
- `:overlay` sandboxes have no commit/WIP distinction; the flag has no effect there and is silently accepted (previously `--no-wip` errored on overlay).

**Rationale:** "Apply commits the agent made" is what users typically want; uncommitted edits are by definition unsettled work the agent didn't finalize. Defaulting to including them surprised users who weren't expecting the agent's scratch state in their tree. Making `--include-wip` opt-in matches the project's `--X-to-enable-non-default-behavior` CLI convention ([`dev/standards/CLI.md`](dev/standards/CLI.md)) and surfaces the WIP state explicitly so users can choose.

**Migration:**
- Drop `--no-wip` (it was a no-op for the new behavior anyway).
- If you relied on `yoloai apply` bringing across uncommitted edits, add `--include-wip` to the command.
- The Go library API `Client.Apply(ctx, name)` is now commits-only; use the new `Client.ApplyWithOptions(ctx, name, ApplyOptions{IncludeWIP: true})` for the old behavior.
- Internal `patch.GeneratePatch`, `patch.GenerateMultiPatch`, and `patch.ApplyAll` gain an `includeWIP bool` parameter at the end of their signatures.

### Cross-process JSON files gain `schema_version` field with mismatch-fails-loudly policy

**Previous behavior:** `runtime-config.json` and `agent-status.json` had no explicit version field. Drift between Go (writer/reader) and Python (reader/writer) could silently misinterpret fields.

**New behavior:** Both files carry `"schema_version": 1`. Mismatch between writer and reader (e.g., a newer yoloai writes a file an older yoloai reads) causes Go's `parseStatusJSON` to discard the file and Python's `read_config` / status-monitor.py to raise `RuntimeError` with a specific message naming the file and the version mismatch. Missing `schema_version` is tolerated as legacy (pre-W2) and follows the original parsing path; only an explicit non-matching value triggers a failure.

**Rationale:** W2 of the architecture remediation plan ([`dev/plans/architecture-remediation.md`](dev/plans/architecture-remediation.md)). The cross-process boundary needs a tripwire so coordinated Go/Python changes are explicit, not silent. Hard-fail on mismatch trades the inconvenience of re-creating a sandbox for the safety of not misreading a structurally different file.

**Bump policy:**
- **Additive changes** (new optional field with sensible defaults on both sides) do NOT require a version bump.
- **Required-field changes, removals, renames, semantic changes** require bumping the constant in three places coordinated: `runtimeConfigSchemaVersion` in `sandbox/create.go`, `RUNTIME_CONFIG_SCHEMA_VERSION` in `sandbox-setup.py`, and the inline `runtime_config_schema_version` in `status-monitor.py` (or `agentStatusSchemaVersion` in `sandbox/inspect.go` + `AGENT_STATUS_SCHEMA_VERSION` in `sandbox-setup.py` + shell-hook literals in `agent/agent.go`).
- Recreating a sandbox is the workaround for users running across an incompatible upgrade.

**Migration:** none required when upgrading — newly-created sandboxes carry `schema_version: 1`; existing sandboxes (no field) continue to work. If a future version bumps the constant and you downgrade, the older binary will hard-fail; re-create the sandbox.

### `container-nestable` isolation mode removed; use `container-privileged` instead

**Previous behavior:** `--isolation container-nestable` created a container with a targeted capability grant (`CAP_NET_ADMIN`, `CAP_NET_RAW`, `CAP_SYS_ADMIN`, `seccomp=unconfined`, `cgroupns=host`) intended as a minimal Docker-in-Docker mode.

**New behavior:** The mode no longer exists. `--isolation container-privileged` is the supported mode for Docker-in-Docker and Compose workloads. It passes `--privileged`, which grants full access to `/proc/sys` and `/sys/fs/cgroup` — both required for an inner Docker daemon. The sandbox image's `/etc/docker/daemon.json` pre-configures `fuse-overlayfs` as the storage driver.

**Rationale:** The capability delta between `container-nestable` and `container-privileged` was too narrow to provide a meaningful security boundary. Both modes required `CAP_SYS_ADMIN` + `seccomp=unconfined` + `apparmor=unconfined`, which is sufficient for container escape. Maintaining a separate mode implied a safety tier that didn't exist in practice.

**Migration:** Replace `--isolation container-nestable` with `--isolation container-privileged`. Update any profile `config.yaml` files with `isolation: container-nestable` to `isolation: container-privileged`.

### `apply --force` flag removed; dirty trees are handled automatically

**Previous behavior:** `yoloai apply` errored when the target repo had uncommitted changes unless `--force` was passed.

**New behavior:** `yoloai apply` auto-stashes uncommitted changes before applying commits (via `git am --autostash`) and restores them afterward. The `--force` flag no longer exists.

**Rationale:** Users who start a sandbox with a dirty tree couldn't apply agent changes without first committing or stashing manually. The guard was designed to prevent accidental data loss, but `git am --autostash` provides the same safety automatically.

**Migration:** Remove `--force` from any `yoloai apply` invocations. If the stash pop produces conflicts, git will report them with instructions.

### Smoke test `--limited` flag replaced by `--full`

**Previous behavior:** `python3 scripts/smoke_test.py` ran the full backend matrix by default. `--limited` skipped unavailable backends instead of aborting.

**New behavior:** The default (no flag) runs a base tier with only the most reliable backends (docker + containerd-vm on Linux, docker + tart on macOS). `--full` enables the full backend matrix (adds podman, gVisor, vm-enhanced). Missing backends are always skipped with a warning, never an abort.

**Rationale:** Base tier is fast enough for PR gates and nightly CI. Full tier is for pre-release validation. The old `--limited` flag was backwards — it made the safe option the non-default.

**Migration:** Replace `--limited` with no flag (base tier). Replace bare invocation with `--full` for pre-release runs. `make smoketest` now runs base tier; `make smoketest-full` runs full tier.

### Profile system redesigned: `profiles/base/` replaced by `defaults/`, no default profile setting

**Previous behavior:** Profile defaults lived in `~/.yoloai/profiles/base/` (config.yaml, Dockerfile, entrypoint.sh, tmux.conf). The `profile` config key let users set a default profile applied to all new sandboxes without `--profile`. Profile config merged over base config (base → profile → CLI). Profiles referenced a parent via `extends: base`.

**New behavior:**
- `~/.yoloai/profiles/base/` is replaced by `~/.yoloai/defaults/` (config.yaml and optionally tmux.conf only — no Dockerfile or scripts).
- Baked-in defaults (Dockerfile, entrypoint scripts, initial config values) are embedded in the binary. They are not written to disk and are not user-editable.
- The `profile` config key is removed. There is no default profile. `--profile <name>` on `yoloai new` is always explicit; omitting it uses `defaults/`.
- Profiles are self-contained: their config.yaml merges over baked-in defaults only. User defaults (`defaults/config.yaml`) do not apply when a profile is active.
- Profile directories contain `config.yaml`, optionally `Dockerfile` (must use `FROM yoloai-base`), and optionally `tmux.conf`. Entrypoint scripts are not profile-overridable.
- The `extends` field is removed from profile config. All profiles inherit from baked-in defaults.
- `profile.yaml` renamed to `config.yaml` in profile directories.

**Rationale:** Profiles are now fully deterministic — their behavior depends only on the profile and baked-in defaults, not on the user's personal config. This eliminates "works on my machine" problems when sharing profiles. User defaults are clearly separate from profiles and only apply in the no-profile case.

**Migration:**
- Copy `~/.yoloai/profiles/base/config.yaml` to `~/.yoloai/defaults/config.yaml`.
- Remove the `profile` key from your config if set. Use `--profile <name>` explicitly with `yoloai new`.
- If you customized `profiles/base/Dockerfile` or `profiles/base/entrypoint.sh`, move those customizations into a named profile.
- Rename `profile.yaml` to `config.yaml` in any existing profile directories.
- Remove `extends: base` from any profile configs.
- The profile name `base` is no longer reserved.

### `backend` config key renamed to `container_backend`

**Previous behavior:** The `backend:` key in `~/.yoloai/config.yaml` selected the container runtime (docker, podman, …). Shipped in tags `v0.1.0` and `v0.1.1`.

**New behavior:** The same setting is now `container_backend:`. Shipped in `v0.2.0` and later.

**Rationale:** The unqualified `backend` name was ambiguous against the broader yoloAI concept of "backend" (which also covers `tart` and `seatbelt` — not all of them are container runtimes). `container_backend` names the actual scope.

**Migration:** Rename `backend:` to `container_backend:` in any `~/.yoloai/config.yaml` written by v0.1.x. No auto-migration; v0.2+ ignores the old key.

**History note:** Earlier revisions of this entry described a `--security` → `--isolation` flag rename and a `gvisor`/`kata`/`kata-firecracker` → `container-enhanced`/`vm`/`vm-enhanced` value rename. Verified via `git tag` audit that **none of those names ever appeared in a tagged release** — `--isolation` and the `container`/`vm` value spellings have been the public surface since `v0.2.0`. See `docs/dev/discovered-findings.md` DF1 for the audit. The flag/value text was removed in this revision to avoid misleading migrations.

### `sandbox <name> log` redesigned around structured JSONL

**Previous behavior:** `yoloai log <name>` (and `yoloai sandbox <name> log`) displayed the raw agent terminal output (`logs/agent.log`) with ANSI stripped by default. `--raw` preserved ANSI escape sequences. `--json` returned `{"content": "..."}`.

**New behavior:** Default view is a pretty-printed, merge-sorted stream of all four structured JSONL logs (`logs/cli.jsonl`, `logs/sandbox.jsonl`, `logs/monitor.jsonl`, `logs/agent-hooks.jsonl`). Agent terminal output is accessed via dedicated flags. `--raw` now emits raw JSONL lines.

New flags:
- `--agent` — show agent terminal output with ANSI stripped (replaces old default)
- `--agent-raw` — show raw agent terminal stream (replaces old `--raw`)
- `--raw` — emit structured log as raw JSONL (new meaning)
- `--source cli,sandbox,monitor,hooks` — filter to specific log sources
- `--level debug|info|warn|error` — filter by minimum level (default: `info`)
- `--since <duration|time>` — filter by timestamp (e.g. `5m` or `14:20:00`)
- `--follow` / `-f` — tail all sources live; auto-exits when sandbox is done

`--json` flag no longer has special handling for the log command.

**Rationale:** Structured JSONL is the primary log format — the pretty-printed interleaved view is far more useful than raw terminal output for debugging. Agent output is still accessible via `--agent`/`--agent-raw` for cases where it's needed.

**Migration:**
- `yoloai log <name>` — now shows structured log. Add `--agent` to see agent output as before.
- `yoloai log <name> --raw` — now emits raw JSONL. Use `--agent-raw` for old behavior.
- `yoloai log <name> --json` — no longer returns `{"content": "..."}`. Read `logs/agent.log` directly.

### Caret encoding updated to current spec

**Previous behavior:** Caret encoding used raw hex for all unsafe characters (e.g., `/` → `^2F`, `^` → `^5E`). `.` and `~` were encoded as unsafe. The decoder accepted `g/G`, `h/H`, `i/I`, `j/J` as hex width modifiers (3-6 digit forms).

**New behavior:** Caret encoding uses single-letter shortcuts where defined by the spec (e.g., `/` → `^s`, `^` → `^^`, `:` → `^k`, `@` → `^o`). `.` and `~` are now in the safe set (passed through unencoded), except `.` is still encoded when it's the last character of a path component (trailing dots are stripped by Windows). The decoder treats `g`–`v` (and uppercase) as shortcut codes, not hex width modifiers. Hex encoding (`^2F`, etc.) is still accepted by the decoder for backward compatibility.

**Rationale:** Aligns with the current caret-encoding spec (https://github.com/kstenerud/caret-encoding). Shortcuts produce shorter, more readable directory names. `.` and `~` are "Problematic" (position-dependent) in the spec, not "Reserved", and are shown unencoded in spec examples.

**Migration:** Existing sandbox directory names on disk will not match new encodings (e.g., `^2Fhome^2Fuser` → `^shome^suser`). Destroy and recreate affected sandboxes. No automatic migration — this is a pre-release change.

### `reset` redesigned: in-place default, cache/files cleared, new flags

**Previous behavior:** `yoloai reset` stopped and restarted the container by default. `--no-restart` kept the agent running (in-place reset). `--clean` wiped agent state and cache. `--clean` and `--no-restart` were mutually exclusive.

**New behavior:** `yoloai reset` resets in-place by default (agent stays running). Cache and files directories are cleared by default. New flags:
- `--restart` — stop and restart container
- `--clear-state` — wipe agent runtime state (replaces `--clean`, implies `--restart`)
- `--keep-cache` — preserve cache directory
- `--keep-files` — preserve files directory
- `--attach` now implies `--restart`

Automatic upgrades to `--restart`: overlay mode, container not running, or `--clear-state` set.

**Rationale:** In-place reset is the better default — it preserves agent context while syncing workspace changes. The old `--no-restart` flag required opting in to the better UX. Cache and files are now cleared by default for a clean slate, with `--keep-X` flags for opt-out. `--clean` mixed too many concerns (agent state + cache); `--clear-state` is more precise.

**Migration:**
- `yoloai reset <name>` — now resets in-place (was restart). Add `--restart` to stop and restart the container.
- `yoloai reset <name> --no-restart` — remove `--no-restart` (now the default).
- `yoloai reset <name> --clean` — replace with `--clear-state`. Note: cache is now cleared by default, so `--clear-state` only adds agent runtime state wipe.
- Cache/files are now cleared by default. Add `--keep-cache` and/or `--keep-files` to preserve them.

### `files` command: name before subcommand

**Previous behavior:** `yoloai files put <sandbox> <file>...` — subcommand before sandbox name.

**New behavior:** `yoloai files <sandbox> put <file>...` — sandbox name before subcommand.

**Rationale:** Name-first ordering is more ergonomic (name is the "context", action is the "verb") and consistent with top-level commands (`yoloai diff <name>`) that already put the name first.

**Migration:** Swap the sandbox name and subcommand positions. For example, `yoloai files put mybox file.txt` becomes `yoloai files mybox put file.txt`.

### `sandbox` command: name before subcommand

**Previous behavior:** `yoloai sandbox info <name>`, `yoloai sandbox log <name>` — subcommand before sandbox name.

**New behavior:** `yoloai sandbox <name> info`, `yoloai sandbox <name> log` — sandbox name before subcommand. `sandbox list` is unchanged (no sandbox name).

**Rationale:** Same as `files` — name-first ordering is more ergonomic and consistent with top-level commands.

**Migration:** Swap the sandbox name and subcommand positions. For example, `yoloai sandbox info mybox` becomes `yoloai sandbox mybox info`.

### `sandbox network` flattened to `allow`/`allowed`/`deny`

**Previous behavior:** Network allowlist management used nested subcommands: `yoloai sandbox network add <name> <domain>...`, `yoloai sandbox network list <name>`, `yoloai sandbox network remove <name> <domain>...`.

**New behavior:** Flattened to direct subcommands with name-first ordering: `yoloai sandbox <name> allow <domain>...`, `yoloai sandbox <name> allowed`, `yoloai sandbox <name> deny <domain>...`.

**Rationale:** Reduces nesting depth and uses clearer verb names (`allow`/`deny` instead of `add`/`remove`, `allowed` instead of `list`).

**Migration:** Replace `sandbox network add` with `sandbox <name> allow`, `sandbox network list` with `sandbox <name> allowed`, `sandbox network remove` with `sandbox <name> deny`.

### `sandbox clone` removed

**Previous behavior:** `yoloai sandbox clone <src> <dst>` was available as an alias for `yoloai clone`.

**New behavior:** Only the top-level `yoloai clone <src> <dst>` is available.

**Rationale:** The `sandbox clone` alias conflicted with the name-first dispatch pattern (where `clone` would be interpreted as a sandbox name). The top-level command is the canonical form.

**Migration:** Replace `yoloai sandbox clone` with `yoloai clone`.

### `files get` signature changed: positional destination replaced with `-o` flag

**Previous behavior:** `yoloai files get <sandbox> <file> [dst]` — single file, optional positional destination argument.

**New behavior:** `yoloai files get <sandbox> <file/glob>... [-o dir]` — multiple files/globs, destination specified via `-o`/`--output` flag (defaults to `.`).

**Rationale:** Positional destination prevented accepting multiple file arguments. The `-o` flag is a standard convention (`curl -o`, `tar -C`) and removes ambiguity between file arguments and the destination.

**Migration:** Replace `yoloai files get <sandbox> <file> <dst>` with `yoloai files get <sandbox> <file> -o <dst>`.

### Entrypoint shell scripts consolidated into Python

**Previous behavior:** Each backend had its own shell entrypoint script: `entrypoint-user.sh` for Docker, `entrypoint.sh` for seatbelt, and `setup.sh` for Tart. These scripts contained ~80 lines of near-identical logic for config reading, tmux setup, agent launch, ready-pattern detection, prompt delivery, and status monitoring.

**New behavior:** A single Python script `sandbox-setup.py` replaces all three shell scripts. Backend-specific setup is dispatched by a CLI argument (`docker`, `seatbelt`, `tart`). The script is embedded in the Go binary via `runtime/monitor/` and deployed identically to `status-monitor.py`. The Docker root entrypoint (`entrypoint.sh`) remains shell — it handles system-level setup (iptables, usermod, gosu).

**Rationale:** The duplicated shell logic meant every bug fix or feature change had to be applied three times. Shell is also fragile for the complex polling/state logic these scripts contain. Python provides `json.load()` (eliminating 8+ `jq` calls per script), proper string handling, and threading for background tasks.

**Migration:** If you customized `entrypoint-user.sh` in a Docker profile, port your changes to Python by modifying the `setup_docker()` function in `sandbox-setup.py`. Docker images must be rebuilt (`yoloai system build`).

### Legacy sandbox support removed

**Previous behavior:** Old sandboxes (created before the directory layout reorganization) were supported via automatic fallbacks to legacy file names (`meta.json`, `config.json`, `state.json`, `status.json`, `agent-state/`) and legacy file locations (PID files, tmux sockets, profile files at the sandbox root). Config migration from the old flat `~/.yoloai/` layout ran automatically on startup.

**New behavior:** Legacy fallbacks are removed. Only the current file names and directory layout are supported. Config migration from the old flat layout is removed. The `destroy` command always succeeds (returns nil if the sandbox directory doesn't exist, warns instead of failing on directory removal errors). Non-destroy commands that fail on a sandbox include the sandbox directory path and a `yoloai destroy` hint in the error message.

**Rationale:** Legacy support was causing recurring issues during sandbox start, reset, and destroy operations. During early development, maintaining backward compatibility with old sandboxes added complexity without sufficient benefit.

**Migration:** Destroy old sandboxes with `yoloai destroy <name>` and recreate them. If you have an old `~/.yoloai/config.yaml` with the `defaults:` nesting from the pre-profile layout, delete `~/.yoloai/` and run `yoloai setup`.

### Sandbox directory layout reorganized; `YOLOAI_DIR` abstraction added

**Previous behavior:** Sandbox state files had generic names (`meta.json`, `config.json`, `state.json`, `status.json`) in a flat layout. The `agent-state/` directory held agent runtime state. Docker hardcoded `/yoloai/` paths; seatbelt and tart used different variable names. Scripts, tmux config, and backend-specific files all lived at the sandbox root.

**New behavior:** Files are renamed for clarity and organized into subdirectories:
- `meta.json` → `environment.json`
- `config.json` → `runtime-config.json`
- `state.json` → `sandbox-state.json`
- `status.json` → `agent-status.json`
- `agent-state/` → `agent-runtime/`
- Scripts moved to `bin/` (entrypoint.sh, status-monitor.py, diagnose-idle.sh)
- Tmux config moved to `tmux/` (tmux.conf, tmux.sock)
- Backend-specific files moved to `backend/` (instance.json, profile.sb, pid, stderr.log)

All entrypoint scripts now use `$YOLOAI_DIR` instead of hardcoded paths. Docker sets `ENV YOLOAI_DIR=/yoloai`, seatbelt exports `YOLOAI_DIR=$SANDBOX_DIR`, tart exports `YOLOAI_DIR=$SHARED_DIR`.

**Rationale:** Generic names like `config.json` and `status.json` didn't convey purpose. The flat layout mixed scripts, configs, state, and backend files. Hardcoded `/yoloai/` paths in hook commands broke on seatbelt (where the sandbox dir is a host-local path, not `/yoloai/`).

**Migration:** Automatic. The code checks for new filenames first, then falls back to legacy names. Existing sandboxes continue to work. New sandboxes use the new layout. Docker images must be rebuilt (`yoloai system build`) for new sandboxes.

### Sandbox status `running` renamed to `active`; `--running` flag renamed to `--active`

**Previous behavior:** The agent status was `"running"` when actively working. `yoloai ls --running` filtered for active sandboxes.

**New behavior:** The agent status is `"active"`. `yoloai ls --active` filters for active sandboxes.

**Rationale:** `"running"` was ambiguous -- the container process is also "running" when the agent is idle. `"active"` clearly means the agent is actively working on a task.

**Migration:** Replace `--running` with `--active` in scripts. Old sandboxes with `"running"` in the agent status file are handled automatically (backward compatible parsing).

### `container_id` removed from JSON output

**Previous behavior:** `yoloai ls --json` and `yoloai sandbox info --json` included a `container_id` field in the output.

**New behavior:** The `container_id` field is no longer present.

**Rationale:** The field was always empty — it was never populated with a value. Removing it cleans up the JSON API.

**Migration:** Remove any code that reads `container_id` from yoloAI JSON output. The field was always empty, so no information is lost.

### `yoloai new --replace` renamed to `--force`

**Previous behavior:** `yoloai new --replace` replaced an existing sandbox with the same name.

**New behavior:** `yoloai new --force` replaces an existing sandbox. `--replace` still works but prints a deprecation warning to stderr and will be removed in a future release.

**Rationale:** `--force` is the standard convention for "proceed despite conflict" across CLI tools (docker, git, etc.). `--replace` was non-standard and also conflicted with the `--force` flag used in `apply` for a similar "override safety check" purpose.

**Migration:** Replace `--replace` with `--force` in scripts. `--replace` continues to work during the deprecation period.

### `yoloai new` no longer auto-attaches by default

**Previous behavior:** `yoloai new` auto-attached to the tmux session after creation. `--detach`/`-d` skipped the attach.

**New behavior:** `yoloai new` starts the sandbox in the background (detached). Use `--attach`/`-a` to auto-attach. `--detach`/`-d` is removed.

**Also applies to:** `yoloai start` now supports `--attach`/`-a` with the same semantics (detached by default).

**Rationale:** Consistent unix-y model — both `new` and `start` are detached by default, both accept `-a` to attach. Avoids confusing asymmetry where `new` used `-d` (detach) while `start` used `-a` (attach).

**Migration:** Add `-a` to auto-attach: `yoloai new -a ...`. Remove `-d`/`--detach` from any scripts.

### Tmux mouse mode no longer enabled by default

**Previous behavior:** Sandbox tmux sessions had `set -g mouse on`, enabling mouse-wheel scrolling, click-to-select-pane, and drag-to-resize. OSC 52 clipboard and `MouseDragEnd1Pane` bindings were configured to compensate for mouse mode breaking copy/paste.

**New behavior:** Mouse mode is off. Text selection and copy/paste work normally via the terminal. Scrollback is accessed with `^b [` (shown in the status bar). The OSC 52 and MouseDragEnd workarounds are removed.

**Rationale:** Mouse mode breaks copy/paste in many terminal emulators, and the clipboard workarounds (OSC 52, MouseDragEnd1Pane pipe-and-cancel) don't work reliably across all setups. Broken copy is worse UX than needing a keybinding to scroll.

**Migration:** If you prefer mouse mode, add `set -g mouse on` to your own `~/.tmux.conf` or a custom profile's tmux config.

### `--backend` moved from global flag to per-command flag

**Previous behavior:** `--backend` was a global flag available on all commands.

**New behavior:** `--backend` is a local flag on `new`, `build`, and `setup` only. Lifecycle commands (`start`, `stop`, `destroy`, `reset`, `attach`, `exec`, `sandbox info`) read the backend from the sandbox's `meta.json` automatically. `list` uses the config default.

**Rationale:** The backend is a property of the sandbox, not the CLI invocation. Lifecycle commands should use the backend the sandbox was created with, not require the user to remember and pass it every time.

**Migration:** Remove `--backend` from lifecycle command invocations. If you were passing `--backend` to `start`/`stop`/etc., it now happens automatically via the sandbox's environment metadata.

### Config paths restructured: `defaults.` prefix removed, config moved to profile

**Previous behavior:** Config lived at `~/.yoloai/config.yaml` with settings nested under `defaults:` (e.g., `defaults.backend`, `defaults.agent`, `defaults.env.<NAME>`). Operational state (`setup_complete`) was stored in the same file.

**New behavior:** Config lives at `~/.yoloai/profiles/base/config.yaml` with a flat schema (e.g., `backend`, `agent`, `env.<NAME>`). Operational state moved to `~/.yoloai/state.yaml`. Resource files (Dockerfile, entrypoint.sh, tmux.conf) moved from `~/.yoloai/` to `~/.yoloai/profiles/base/`.

**Rationale:** Base config is now a profile — same structure and code path as user profiles. Flat schema is simpler and the `defaults:` wrapper added no value. Separating operational state from user preferences keeps config clean.

**Migration:** Automatic. On first run, yoloai detects the old layout and migrates: moves resource files to `profiles/base/`, flattens `defaults:` mapping to root level in `profiles/base/config.yaml`, extracts `setup_complete` to `state.yaml`. For manual config commands, drop the `defaults.` prefix (e.g., `yoloai config set backend docker` instead of `yoloai config set defaults.backend docker`).

### `tmux_conf` and `model_aliases` moved to global config

**Previous behavior:** `tmux_conf` and `model_aliases` were stored in the base profile config (`~/.yoloai/profiles/base/config.yaml`).

**New behavior:** These settings are stored in the global config (`~/.yoloai/config.yaml`), which is not overridable by profiles.

**Rationale:** `tmux_conf` and `model_aliases` are user preferences that should apply to all sandboxes regardless of profile. They don't belong in profile-overridable config.

**Migration:** Automatic. On first run, yoloai moves `tmux_conf` and `model_aliases` from the base profile config to the global config. No manual action needed.

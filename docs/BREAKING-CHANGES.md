# Breaking Changes

Tracks breaking changes made during beta. Each entry should be included in release notes for the version that introduces it.

## Unreleased

### 0.x public Go API reshape (layer-1)

Beta reshape of the Go embedding surface (critique findings F1–F4; plan
`docs/contributors/archive/plans/layer1-public-api.md`, step-by-step detail in the `D`-entries of
`docs/contributors/decisions/README.md`). Goal: external embedders drive yoloAI entirely
through the root `yoloai` package, never importing `internal/*`. Per-sandbox
operations moved onto resource-bound handles, every Options/Result/error became a
public `yoloai.*` type, and creation/backend-selection became explicit. **CLI
behavior is unchanged except for the flag renames called out below.**

**Handle model.** Per-sandbox ops are no longer `Client` methods taking a `name`
string — call them on `c.Sandbox(name)`, which now returns `(*Sandbox, error)`
and rejects a missing sandbox with `ErrSandboxNotFound` at construction (F22). Its
sub-accessors `.Workdir()` and `.Network()` are pure namespace expansion (no IO,
no error). Lifecycle/exec move onto the handle with `name` dropped from each
signature: `.Inspect(ctx)` / `.Stop(ctx)` / `.Start(ctx, opts)` / `.Restart(ctx,
opts)` / `.SendInput(ctx, text)` / `.ContainerLogs(ctx, n)` / `.Dir()` (replaces
`SandboxDir`); `.Reset(ctx, yoloai.ResetOptions{…})`; `.Destroy(ctx,
yoloai.DestroyOptions{…})`; `.Exec(ctx, yoloai.ExecOptions{Command, PTY}, io)`
(folds the old `Exec`=PTY and `StdioExec`=pipes). `Client.NeedsConfirmation` is
gone; the pure pre-check is `c.Sandbox(name).HasActiveWork(ctx) (bool, reason)`.

**Workdir verbs.** Diff/apply/export plus the commit/tag/uncommitted reads moved
onto `c.Sandbox(name).Workdir()`, each one mode-agnostic (copy-vs-overlay
resolved internally), replacing the old per-variant `Client` methods
(`Diff/DiffWithOptions/DiffOverlay/DiffRef`, `Apply/ApplyWithOptions`,
`GeneratePatch/AdvanceBaseline/OverlayPatch/UpdateOverlayBaseline`,
`ListCommits*`, `HasUncommittedChanges`, and tag listing previously reachable
only via `internal/sandbox`):

- `.Diff(ctx, yoloai.DiffOptions{Paths, Stat, NameOnly, Ref})` — `""` means no changes.
- `.Apply(ctx, yoloai.ApplyOptions{Mode, Refs, Paths, IncludeUncommitted, DryRun})`
  — `Mode` is **required** (`ApplyModeCommits` replays the commit series,
  `ApplyModeNoCommit` lands a net unstaged diff); the zero value is a `*UsageError`.
  `ApplyModeCommits` on a non-git/overlay target is refused. `DryRun` previews
  (generate+validate, no apply) so the library never prompts.
- `.Export(ctx, yoloai.ExportOptions{Dir, Refs, Paths, IncludeUncommitted})` —
  `--patches` is now its own verb (`Dir` required).
- `.Commits(ctx, yoloai.CommitsOptions{Stat}) []yoloai.CommitInfo`,
  `.Tags(ctx, yoloai.TagsOptions{UnappliedOnly}) []yoloai.TagInfo`,
  `.HasUncommittedChanges(ctx)`.

**Creation.** `Client.Create(ctx, yoloai.CreateOptions)` now takes a **public**
struct (built from re-exported `yoloai.DirSpec`/`DirMode`/`NetworkMode`/
`PortMapping`/…); `Run` is sugar over it. `CreateOptions.Backend` is
**required** — empty returns a `*UsageError`; do the old auto-detect explicitly
with `yoloai.SelectBackend(ctx, preferred, isolation, os)`. A dirty workdir yields
a typed `*yoloai.DirtyWorkdirError` (the library never prompts); ack it with
`CreateOptions.AllowDirtyWorkdir` / `DirSpec.AllowDirty` /
`RunOptions.AllowDirtyWorkdir`. Removed: `CreateOptions.Yes/Attach/Isolation/OS`
(`Version` moved to `Options.Version`; attach is a separate post-create step).
`Ports` is `[]yoloai.PortMapping`; `requires:` is now a non-blocking warning.

**Admin client.** `yoloai.NewSystemClient(opts yoloai.SystemOptions)
(*SystemClient, error)` replaces `NewSystemClient(config.Layout)` so embedders
need not name the internal `config.Layout`; `SystemOptions{DataDir, HomeDir, Env}`
(empty `DataDir` → `*UsageError`).

**Typed errors / public shapes replace sentinels and internal results.**
`ErrUnappliedChanges` → `*ActiveWorkError` (carries the reason); diff/apply/commits
return public `yoloai.*` shapes (`ApplyResult`, `CommitInfo`, `TagInfo`, `Info`)
instead of `internal/patch` / `internal/sandbox` types. Use `errors.As` for the
typed errors.

**`Force` fields renamed after the consequence (Q-J — no generic `Force` in the
API).** `CloneOptions.Force`→`Overwrite`, `DestroyOptions.Force`→
`AbandonUnappliedWork`, `DirSpec.Force`→`AllowDangerousPath`,
`ResetOptions.Restart`→`RestartContainer`. The CLI `--force` flags map onto these
at the boundary (the `:force` mount suffix is unchanged).

**Terminology: "uncommitted", not "WIP" (everywhere).** CLI flag
`--include-wip`→`--include-uncommitted`; Go `ApplyOptions.IncludeWIP`→
`IncludeUncommitted`, `ApplyResult.WIPApplied`→`UncommittedApplied`; JSON
`wip_applied`→`uncommitted_applied`; exported `wip.diff`→`uncommitted.diff`;
`Client.GenerateWIPDiff`→`GenerateUncommittedDiff`.

**Profile read model is public (closes the last F1 leak).**
`yoloai.ProfileInfo.Merged` and `.Parent` are now `*yoloai.ResolvedProfileConfig`
(was the internal `*config.MergedConfig`), so embedders can name every field.
`ResolvedProfileConfig` mirrors the merged tree with hand-written public types — `ProfileWorkdir`,
`ProfileAuxDir` (was `config.ProfileDir`), `ProfileResources`
(`CPULimit`/`MemoryLimit`, was `config.ResourceLimits{CPUs,Memory}`), `ProfileNetwork`,
and `ProfileAgentFiles`. JSON output of `profile info`/`--diff` is unchanged except
the `agent_files` object's inner keys, which now carry tags (`base_dir`/`files`)
instead of emitting the Go field names (`BaseDir`/`Files`).

**Sandbox read model: `Info.Meta` → `Info.Environment`.** The public `yoloai.Info`'s
creation-time-settings field is renamed `Meta` → `Environment`, and its JSON tag
`"meta"` → `"environment"` — aligning the Go field with the on-disk `environment.json`
artifact it has always mirrored (the type was already `yoloai.Environment`; only the
field name lagged). `--json` output of `sandbox info` and `list` now nests those
settings under `"environment"` instead of `"meta"`. Go embedders rename `info.Meta`
→ `info.Environment`. Internal-only in the same pass (no external effect):
`store.Meta`/`WorkdirMeta`/`DirMeta` → `store.Environment`/`WorkdirEnvironment`/
`DirEnvironment`, `LoadMeta`/`SaveMeta` → `LoadEnvironment`/`SaveEnvironment`, and the
source file `store/meta.go` → `store/environment.go`.

**Sandbox read model curated (D53): `Environment` no longer mirrors `environment.json`
field-for-field.** The public `yoloai.Environment` was a 1:1 mirror of the internal
on-disk schema; it is now a curated view of the sandbox a consumer reasons about —
identity & posture, as-built workdir/aux-dir provenance, and an echo of the resolved
config. Pure-mechanism fields are dropped from the public type (and its `--json`):
`version`, `yoloai_version`, `image_ref`, `has_prompt`, `debug`, `userns_mode`,
`archetype`, `vscode_tunnel`, and `WorkdirInfo.inception_sha`. The internal
`store.Environment` keeps all of them; only the public read-model is trimmed.
`Environment.NetworkMode` is now the typed `yoloai.NetworkMode` enum (was a bare
`string`); it marshals to the same JSON string, so `--json` network output is
byte-stable. `sandbox info` human output drops the `Image:` and `Version:` lines.

**Migration (Go embedders):** if you read any dropped field off `Info.Environment`,
source it elsewhere — the create-time options you passed, or (for diagnostics) a
future dedicated surface; none of the dropped fields described the sandbox a
consumer renders or decides from.

**Migration (Go embedders):** insert `.Sandbox(name)` and drop the `name` arg from
per-sandbox calls; route diff/apply/export/commits/tags through `.Workdir()`;
switch `errors.Is(err, ErrUnappliedChanges)` to
`errors.As(err, new(*yoloai.ActiveWorkError))`; build the `Client` with an
explicit `Backend` (`yoloai.SelectBackend(…)` or e.g. `yoloai.BackendDocker`);
handle `*DirtyWorkdirError` (or pre-ack with `AllowDirtyWorkdir`); rename the
`Force`/`WIP` fields as above; rename `info.Meta`→`info.Environment`. **CLI users:**
`--include-wip`→`--include-uncommitted`, `--squash`→`--no-commit` (JSON `method`
`"squash"`→`"no-commit"`); `sandbox info`/`list` `--json` nest settings under
`"environment"` (was `"meta"`).

### Data directory bifurcated into `library/` (engine) + `cli/` (app); one-time migration via `yoloai system migrate`

The CLI's `~/.yoloai/` is now split into two namespaces under the same top dir:

- `~/.yoloai/library/` holds everything the embeddable engine owns — `sandboxes/`,
  `profiles/`, `cache/`, `trash/`, `defaults/`, `config.yaml`, the tart/docker
  base-image locks + metadata, `cni/`, and `vscode-cli/`.
- `~/.yoloai/cli/` holds CLI-only application state — `extensions/` and the new
  `state.yaml` (first-run-tip bookkeeping).

**Why.** yoloAI is library-first: the engine owns everything under whatever
`DataDir` it is handed and never reaches above it, so the CLI now points the
library at `~/.yoloai/library/` and keeps its own app state in `~/.yoloai/cli/`.
This is purely a CLI convention — **embedders that pass an explicit `--data-dir`
(or `Options.DataDir`) still get every engine directory directly under that path,
with no `/library` subdir.** Each namespace carries its own plain-text-integer
`.schema-version` stamp (`<DataDir>/.schema-version` for the library,
`~/.yoloai/cli/.schema-version` for the CLI), so future layout changes migrate
independently.

**Migration is explicit — run `yoloai system migrate` once after upgrading.**
yoloAI no longer migrates your data directory automatically. On startup a
read-only check inspects each namespace's version stamp and, if your `~/.yoloai/`
predates this layout (a flat `config.yaml` with no `library/` beside it, or an
out-of-date stamp), the binary **fails fast** and tells you to run
`yoloai system migrate` — it will not touch your data until you do. That one
command relocates the engine dirs into `library/` and `extensions/` into `cli/`
(in-place renames within one filesystem — atomic, no copying), then stamps both
namespaces. It is idempotent and safe to re-run if it is interrupted. A brand-new
install (absent or empty `~/.yoloai/`) is created fresh automatically on first
use; read-only commands like `yoloai version` and `yoloai help` work regardless
of the directory's state.

**`--data-dir`.** When supplied, the CLI roots the library at `DIR/library` and
its own state at `DIR/cli` (same split as the default). Embedders calling the Go
API directly are unaffected — they own the path they pass.

**`setup_complete` removed.** The legacy `~/.yoloai/state.yaml` `setup_complete`
flag is gone. "Has the setup wizard run?" is application ceremony, not library
state, so the library no longer tracks it — `EnsureSetup` runs idempotently
inside `Create`, and the CLI's one-time onboarding tip is now keyed off
`~/.yoloai/cli/state.yaml` (`first_run_tip_shown`). The migration carries a legacy
`setup_complete: true` forward as `first_run_tip_shown: true` so upgraders don't
see the tip resurface.

**`tmux_conf` default.** Now defaults to `default+host` (was empty). The library
"just works" via opinionated declarative defaults rather than depending on a
completed setup ceremony to populate config.

### `yoloai system doctor` moves to `yoloai doctor`

The host health-check command is promoted to a top-level verb as part of the
repair/cleanup surface (see `docs/contributors/archive/plans/system-repair-cleanup.md`). It now
reports reclaimable cruft and sandboxes holding unreviewed work in addition to
backend capability status, and delegates remediation to `yoloai system prune`
and `yoloai destroy`.

**Previous behavior:** `yoloai system doctor`.

**New behavior:** `yoloai doctor` (same flags: `--backend`, `--isolation`,
`--json`). The `system doctor` subcommand is removed.

**Migration:** replace `yoloai system doctor` with `yoloai doctor`.

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

**Rationale:** Q-U (`docs/contributors/archive/plans/layering-refactor.md` W-L8b; `api_surface.go` Q-U resolution 2026-05-25). The multi-directory diff/apply implementation was real but the user-visible surface was barely used: no per-directory selection, no cross-directory conflict resolution, no `:overlay`-only-not-`:copy` filtering. The API complexity required to do this properly significantly exceeded what the implementation actually did. Removing the surface now while we're in beta keeps `yoloai diff` / `yoloai apply` simple — if a real use case emerges, it can be restored with an API informed by that need.

**Affected Go API (for embedders):**

- `yoloai.Client.GenerateMultiPatch` — removed. The single-dir replacement is `yoloai.Client.GeneratePatch`.
- `yoloai.Client.Apply` / `Client.ApplyWithOptions` — return shape narrows from `([]*patch.ApplyResult, error)` to `(*patch.ApplyResult, error)`. A `nil` result is the no-op signal.
- `sandbox/patch.ApplyAll` — same return shape change.
- `sandbox/patch.GenerateMultiPatch` — removed.
- `sandbox/patch.LoadAllDiffContexts` — still exists, still returns a slice, but the slice now has at most one entry (the workdir). Loop callers don't need to change.

### First-run setup is non-interactive when triggered implicitly; `yoloai system setup` is the explicit wizard

**Previous behavior:** On the first `yoloai new` / `yoloai run` of a fresh install (when `setup_complete=false`), if stdin was a TTY the user was dropped into the interactive setup wizard before the sandbox could be created — three prompts (tmux config, default backend on macOS, default agent). When stdin wasn't a TTY, the same code path auto-configured silently with `tmux_conf=default+host`.

**New behavior:** Implicit first-run setup is **always non-interactive**: it writes `tmux_conf=default+host` and marks `setup_complete=true`, then proceeds. The interactive wizard is only run explicitly via `yoloai system setup` (which is also how a user re-runs setup to change their defaults).

**Rationale:** Q-F (`docs/contributors/archive/plans/layering-refactor.md` W-L8b) resolved that library entry points must not perform interactive IO — the CLI owns prompts. The previous behavior coupled `sandbox.Manager` to stdin/stdout and made first-run UX unpredictable depending on TTY state. The new shape:

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

**Rationale:** Per-backend pruning had no real-world use case — orphan detection is keyed off the sandbox dir on the host, which is shared across all backends. Selecting one backend either matched the same orphan set (if that backend was the only one with state) or missed orphans on other backends (silently). Always-cross-backend matches user expectation and matches the `Client.System().Prune` library shape resolved under Q-L. Tracked in `docs/contributors/archive/plans/layering-refactor.md` W-L8b Q-L.

**Migration:**
- Drop `--all` and `--backend` from scripts and CI invocations.
- The behavior of bare `yoloai system prune` matches the old `--all` invocation.

### `yoloai system prune --cache` renamed to `--images`; plain prune now reclaims the no-rebuild cache

**Previous behavior:** Bare `yoloai system prune` only removed orphaned resources (containers/VMs with no sandbox dir, stale temp dirs, lock files) — it reclaimed no backend cache. `--cache` reclaimed the backend image cache, snapshots, volumes, and build cache in one destructive step that always forced a yoloai-base rebuild.

**New behavior:** The reclaim is split into two tiers by the prune invariant *plain prune must never force a rebuild*:
- Bare `yoloai system prune` now **also reclaims each backend's no-rebuild cache** (Docker/Podman build cache, retired volumes, dangling images). The base image is kept, so a subsequent `yoloai new` still runs without rebuilding.
- `--cache` is renamed **`--images`**, which additionally removes base/profile images and forces a rebuild on the next `new`.

`yoloai system disk` now reports two columns (`CACHE` = no-rebuild reclaim, `IMAGES` = rebuild-forcing) and `yoloai doctor` splits "reclaimable space" into the same two tiers. Prune also now reports bytes reclaimed (`freed_bytes` in `--json`).

**Rationale:** `--cache` was too generic to convey the rebuild cost, and the old bare prune left obvious reclaimable build cache on disk. The invariant gives users a safe default (`prune`, no rebuild) and an explicit destructive lever (`--images`, forces rebuild). On Docker's containerd image store, the build cache pins image layers, so the build cache must be pruned for `image rm` to actually free disk — bare prune now does this. See `docs/contributors/backend-idiosyncrasies.md`.

**Migration:**
- Replace `yoloai system prune --cache` with `yoloai system prune --images`.
- If you relied on bare `prune` *not* touching the build cache, note it now does (this only reclaims regenerable cache; no rebuild is forced).

### `yoloai system runtime` renamed to `yoloai system tart`

**Previous behavior:** Apple simulator runtime base images were managed via `yoloai system runtime add|list|remove`. The command name read as generic, but the surface is structurally Tart-only (and is the only CLI subtree that imports `runtime/tart` directly).

**New behavior:** The subtree is now `yoloai system tart` (Pattern B, mirroring `podman machine`). The old `yoloai system runtime ...` invocations continue to work as a hidden alias and print a deprecation warning to stderr. The alias will be removed in a future breaking-changes window.

**Rationale:** Backend-scoped commands should name the backend explicitly. The new name makes the Tart scope honest and reserves `yoloai system <backend>` as the convention for any future backend-specific surfaces. Tracked in `docs/contributors/archive/design/layering.md` D1 and W-L2 of `docs/contributors/archive/plans/layering-refactor.md`.

**Migration:**
- Replace `yoloai system runtime ...` with `yoloai system tart ...` in scripts, docs, and CI.
- Until the alias is removed, the old invocation still works but emits a warning.

### `yoloai apply` defaults to commits-only; `--no-wip` removed in favor of `--include-uncommitted`

**Previous behavior:** `yoloai apply` applied agent commits AND uncommitted edits as unstaged files in the same invocation. `--no-wip` opted out of applying the uncommitted edits.

**New behavior:** `yoloai apply` defaults to **committed changes only**. Uncommitted edits the agent left in the work copy are detected, reported as a hint, and NOT applied. To bring them across as unstaged modifications (the old default), pass `--include-uncommitted`. The `--no-wip` flag has been removed.

The flip applies uniformly across the apply surface:

- Default format-patch path: applies commits; prints `Note: sandbox has uncommitted changes …` when uncommitted edits are present and excluded.
- `--squash`: flattens commits only (`git diff baselineSHA HEAD`). With `--include-uncommitted`, flattens everything including uncommitted (`git diff baselineSHA`, after `git add -A`).
- `--patches`: writes `*.patch` for commits; writes `uncommitted.diff` only when `--include-uncommitted` is set.
- `--squash` and `--include-uncommitted` are no longer mutually exclusive — `--squash` controls patch shape, `--include-uncommitted` controls scope.
- `:overlay` sandboxes have no commit/uncommitted distinction; the flag has no effect there and is silently accepted (previously `--no-wip` errored on overlay).

**Rationale:** "Apply commits the agent made" is what users typically want; uncommitted edits are by definition unsettled work the agent didn't finalize. Defaulting to including them surprised users who weren't expecting the agent's scratch state in their tree. Making `--include-uncommitted` opt-in matches the project's `--X-to-enable-non-default-behavior` CLI convention ([`dev/standards/CLI.md`](contributors/standards/cli.md)) and surfaces the uncommitted state explicitly so users can choose.

**Migration:**
- Drop `--no-wip` (it was a no-op for the new behavior anyway).
- If you relied on `yoloai apply` bringing across uncommitted edits, add `--include-uncommitted` to the command.
- The Go library API `Client.Apply(ctx, name)` is now commits-only; use the new `Client.ApplyWithOptions(ctx, name, ApplyOptions{IncludeUncommitted: true})` for the old behavior.
- Internal `patch.GeneratePatch`, `patch.GenerateMultiPatch`, and `patch.ApplyAll` gain an `includeUncommitted bool` parameter at the end of their signatures.

### Cross-process JSON files gain `schema_version` field with mismatch-fails-loudly policy

**Previous behavior:** `runtime-config.json` and `agent-status.json` had no explicit version field. Drift between Go (writer/reader) and Python (reader/writer) could silently misinterpret fields.

**New behavior:** Both files carry `"schema_version": 1`. Mismatch between writer and reader (e.g., a newer yoloai writes a file an older yoloai reads) causes Go's `parseStatusJSON` to discard the file and Python's `read_config` / status-monitor.py to raise `RuntimeError` with a specific message naming the file and the version mismatch. Missing `schema_version` is tolerated as legacy (pre-W2) and follows the original parsing path; only an explicit non-matching value triggers a failure.

**Rationale:** W2 of the architecture remediation plan ([`dev/archive/plans/architecture-remediation.md`](contributors/archive/plans/architecture-remediation.md)). The cross-process boundary needs a tripwire so coordinated Go/Python changes are explicit, not silent. Hard-fail on mismatch trades the inconvenience of re-creating a sandbox for the safety of not misreading a structurally different file.

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

**History note:** Earlier revisions of this entry described a `--security` → `--isolation` flag rename and a `gvisor`/`kata`/`kata-firecracker` → `container-enhanced`/`vm`/`vm-enhanced` value rename. Verified via `git tag` audit that **none of those names ever appeared in a tagged release** — `--isolation` and the `container`/`vm` value spellings have been the public surface since `v0.2.0`. See `docs/contributors/design/unresolved-findings.md` DF1 for the audit. The flag/value text was removed in this revision to avoid misleading migrations.

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

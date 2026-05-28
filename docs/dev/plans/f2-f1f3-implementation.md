# Implementation plan — F2 re-rooting + F1+F3+F4 public surface

Sequences the two signed-off designs (`f2-subhandle-mapping.md`,
`f1-f3-public-surface.md`) into ordered, independently-landable steps. **Plan
only — no code yet.**

## Strategy

- **Each commit stays green** — `make check` (gofmt, golangci-lint incl.
  forbidigo, all Go tests, Python) + the `public_api_test.go` fence + the Stop
  hook. No step leaves the tree broken.
- **All on the `layering-refactor` branch (pre-release).** There's no released
  API to keep stable, so we don't carry deprecated duplicates: each method group
  *moves fully* in one step (add the handle method with the real logic + adopt
  the public type + migrate every caller + delete the old root method).
- **The fence is the progress meter.** Today `f1KnownLeaks` lists the internal
  types bleeding through the public surface. Each step **removes its entries** as
  it adopts public option/result types. End state: the map shrinks to only what
  we consciously defer.
- **Order:** additive foundations first → breaking re-rooting batched by domain,
  each with a `BREAKING-CHANGES.md` entry → fence/docs sweep last.
- **Delegation:** the type/design parts stay in the main agent; the mechanical
  caller migration (CLI + tests) in each step is sonnet-subagent territory,
  guided by compile errors after the signature change.

## Steps

### Step 1 — Public creation surface (F1 + F3 + F4) ✅ DONE (2026-05-28)
*Closes leak: `sandbox.CreateOptions`. Additive + in-module migration.*

**Landed.** All bullets below shipped. Two design wrinkles surfaced during
implementation and were resolved with the owner (see D24 in working-notes):
(1) `Yes` was **not** dropped silently — the manager actually prompted, so the
dirty/requires gates became a typed `*DirtyWorkdirError` + `AllowDirtyWorkdir`/
`DirSpec.AllowDirty` acks (CLI catches→prompts→retries; `requires:` downgraded
to a non-blocking warning). (2) F4 collided with F21's empty-Backend routing —
F4 won: `Options.Isolation`/`OS` removed, public `yoloai.SelectBackend` added.
Also renamed `DirSpec.Force` → `DirSpec.AllowDangerousPath` and dropped the now-
orphaned internal `CreateOptions.Attach`.

- Re-export at the yoloai root: `DirSpec`, `DirMode` (+ `DirModeCopy/Overlay/RW/RO`),
  `NetworkMode` (+ `NetworkModeNone/Isolated`).
- Add public `yoloai.CreateOptions` (the ~21-field advanced struct) + unexported
  `toInternal() sandbox.CreateOptions`. `Ports` is `[]PortMapping`.
- Swap `Client.Create` param: `sandbox.CreateOptions` → `yoloai.CreateOptions`.
- `RunOptions.materialize() CreateOptions`; `Run` routes through `Create`, then
  layers `Wait`/`OnProgress`.
- **F4:** `NewWithOptions` returns `*UsageError` when `Backend == ""`.
- Migrate in-module callers: `internal/cli/lifecycle/new.go`, `internal/mcpsrv`.
- Drop `sandbox.CreateOptions` from `f1KnownLeaks`.
- **BREAKING-CHANGES:** `Create` param type; `Backend == ""` now errors.
- *Risk: low.* Create was unusable externally (internal type), so no external break.

### Step 2 — `Sandbox(name)` lifecycle handle (F2 part 1)
*Closes leaks: `sandbox.StartOptions`, `sandbox.ResetOptions` (and `CloneOptions` if Clone moves). Breaking.*

- Public option types (or root re-exports): `StartOptions`, `RestartOptions`,
  `ResetOptions`, `DestroyOptions`.
- Add direct methods on `*Sandbox`: `Inspect`, `Status`, `Start`, `Stop`,
  `Restart`, `Destroy`, `Reset`, `Attach`, `Exec(ExecOptions, io)`, `SendInput`,
  `ContainerLogs`, `Dir`. (`CaptureTerminal` already there.)
- Fold `StdioExec` → `Exec` (`ExecOptions.PTY=false` default; MCP passes its pipes
  via `IOStreams`).
- Delete `NeedsConfirmation`; `Destroy(force=false)` returns a typed
  `*ActiveWorkError` carrying the reason (atomic; CLI catches → prompts → retries
  with force).
- Migrate CLI lifecycle commands + MCP + tests; delete the old `Client` root
  methods.
- **Open sub-decision (flag in PR):** `Info` is pervasive (`Inspect`/`List`/`Run`
  all return `*sandbox.Info`). Re-export `type Info = sandbox.Info` at the root
  (closes the leak cheaply) vs. a hand-written public struct (more work). Recommend
  the alias.
- **BREAKING-CHANGES:** per-sandbox ops move from `Client` root to `Sandbox(name)`.

### Step 3 — `Workdir().Diff` (F2 part 2)
*Breaking. Diff returns string (no leak).*

**Scope reconciled (2026-05-28, §12 facts-first):** Step 3 is `Workdir().Diff`
**only**. The mapping paired it with `Workdir().Patch`, but the patch-generation
methods (`GeneratePatch`, `GenerateUncommittedDiff`, `OverlayPatch`) turned out to be
apply-plumbing — consumed solely by `apply_squash`/`apply_export`/
`apply_format_patch`/`apply_overlay` — and `OverlayPatch` returns `[]PatchSet`
(per-overlay-dir), a different shape than copy's `[]byte`. A single
`Patch()→bytes` doesn't fit. So patch generation folds into **Step 4 (Apply)**,
where those apply modes live and already consume it.

- Add `Sandbox(name).Workdir()` handle.
- `Workdir().Diff(DiffOptions) (string, error)` — folds `Diff`, `DiffWithOptions`,
  `DiffRef` (via `DiffOptions.Ref`), `DiffOverlay`. Mode (copy/overlay) resolved
  internally from `meta.Workdir.Mode`; the overlay-explicit `DiffOverlay`
  disappears. Ref + overlay stays a typed refusal (commits aren't host-addressable).
- Migrate CLI `diff` (drops its own overlay branching) + MCP + tests; delete the
  folded root methods.
- **BREAKING-CHANGES:** diff ops move under `Workdir()`; `DiffOverlay` removed.

### Step 4 — `Workdir().Apply` (F2 part 3) — PHASED (4a–4e)
*Closes leak: `patch.ApplyResult`. Breaking.*

**Scope finding (2026-05-28, §12):** unlike Diff, the apply *orchestration* lives
in the CLI (`apply_*.go`, ~1000 lines) — the Client only exposes primitives
(`GeneratePatch`/`GenerateFormatPatch*`/`OverlayPatch`/`AdvanceBaseline`). Folding
all five variants (default format-patch, `--squash`, selective-refs, `--patches`
export, overlay) into one `Workdir().Apply` means relocating that orchestration
into the library — the W-L8 thin-CLI end-state, but large. Done **phased**:

- **4a — LANDED (2026-05-28):** `Workdir().Apply(ApplyOptions{IncludeUncommitted})` folds
  `Client.Apply`/`ApplyWithOptions` (the simple full-workdir apply over
  `patch.ApplyAll`); `ApplyResult` re-exported as `yoloai.ApplyResult` →
  `patch.ApplyResult` off `f1KnownLeaks`. `ApplyOptions` moved to `workdir.go`.
- **4b — LANDED (2026-05-28):** squash. `Workdir().Apply` *is* the squash apply;
  `patch.ApplyAll` extended with `Paths`/`DryRun` (+ CheckPatch validation),
  `ApplyOptions` gains `Paths`/`DryRun`. `Client.GeneratePatch` removed; the CLI
  `--squash` path (`apply_squash.go`) routes through `Workdir().Apply`, previewing
  via DryRun so the library never prompts. Established the **DryRun preview
  pattern** reused by 4c–4e.
**Model correction (D26, 2026-05-28):** the default apply was built backwards.
The *normal* flow **replays the commit series** (`format-patch` → `git am`,
preserving messages/authors); flattening to a net unstaged diff is the special
case. So what 4a/4b actually built is the **`NoCommit`** path (net diff,
unstaged — `git apply`), not the default. The real default (series replay) is the
core, still to land. Also: the library never auto-switches modes — series on a
non-git target returns a typed `*UsageError`; the CLI checks `IsGitRepo` and
picks `NoCommit` (mechanism complies-or-complains; policy decides). And `--squash`
becomes `--no-commit`.

- **4c — LANDED (2026-05-28), in two increments:**
  - *i1:* `patch.ApplySeries` (the series replay: `ListCommitsBeyondBaseline` →
    `GenerateFormatPatch` → `git am` → contiguous baseline advance → optional uncommitted)
    in the library; `Workdir().Apply` default = series, non-git → `*UsageError`.
    `ApplyResult` reshaped (`Commits []AppliedCommit{Subject,SourceSHA,HostSHA}`;
    dropped `FilesChanged`). Then made `ApplyOptions.Mode` **required** (D26/§4 —
    no movable default after the i1 default-flip bug), `ApplyModeCommits` /
    `ApplyModeNoCommit`.
  - *i2:* migrated `apply_format_patch.go`'s default path to `Workdir().Apply
    (ApplyModeCommits)` (gutted `applyFormatPatchFiles`/`applyWIPChanges`; tags
    stay CLI-side off `result.Commits`); renamed CLI `--squash` → `--no-commit`
    (`applySquash`→`applyNoCommit`); CLI checks `IsGitRepo` and selects
    `ApplyModeNoCommit`. Removed the now-dead `Client.AdvanceBaseline`.
- **4d — LANDED (2026-05-28):** `ApplyOptions.Refs []string` (empty = whole
  series; non-empty = subset; `ApplyModeCommits` only). The library's
  `ApplySeries` now resolves refs, generates the format-patch for the subset, and
  advances the baseline across the contiguous applied prefix (helpers
  `resolveSeriesCommits` / `generateSeriesPatch` / `advanceSeriesBaseline`).
  `apply_selective.go` migrated to `Workdir().Apply(ApplyModeCommits, Refs)` via
  the DryRun-preview→confirm→apply pattern; removed the dead
  `Client.ResolveCommitRefs` / `Client.GenerateFormatPatchForRefs`. New
  end-to-end tests: `TestApplySeries_{FullReplay,SelectiveRefs,DryRunDoesNotApply}`.
- **4e — LANDED (2026-05-28):** export is its own verb, **not** an apply mode
  (D29). `Workdir().Export(ExportOptions{Dir,Refs,Paths,IncludeUncommitted})
  (*ExportResult, error)` in the library (`patch.Export`); resolves copy
  (format-patch + optional `uncommitted.diff`) vs overlay (upper-layer diffs)
  internally. CLI `apply --patches` dispatched before the apply paths (fixes
  `apply <refs> --patches` ignoring `--patches`); `apply_export.go` gutted to a
  thin `runExport`; `applyOverlayExportPatches` removed. Dead
  `Client.GenerateFormatPatch`/`GenerateUncommittedDiff` removed. Tests:
  `TestExport_{CopyAllCommits,CopyWithRefs,IncludeUncommitted,RWRefused,OverlayRefsRefused}`.
- **4f:** overlay *apply* — fold `apply_overlay.go` into `Workdir().Apply`
  (overlay → `ApplyModeNoCommit`; `ApplyModeCommits` refused). Remove the
  top-level `hasOverlayDirs` dispatch and the dead `Client.OverlayPatch` /
  `UpdateOverlayBaseline`. Green + committable.

- Public `yoloai.ApplyResult` + `ApplyStatus` consts (per api_surface).
- `Workdir().Apply(ApplyOptions) (*ApplyResult, error)` — folds `Apply`,
  `ApplyWithOptions`; `Mode: ApplyExport` (+ `ExportDir`) folds
  `GenerateFormatPatch`, `GenerateFormatPatchForRefs`.
- Migrate CLI `apply` (+ squash/export/format-patch variants) + tests; delete
  root methods.
- Drop `patch.ApplyResult` from `f1KnownLeaks`.
- **BREAKING-CHANGES:** apply/format-patch move under `Workdir()`.

### Step 5 — `Workdir()` commits + baseline (F2 part 4)
*Closes leak: `patch.CommitInfoWithStat`. Breaking.*

- Public `yoloai.CommitInfo` + `BaselineLogEntry`.
- `Workdir().Commits(CommitOptions{WithStats, Refs})` — folds `ListCommits`,
  `ListCommitsWithStats`, `ListCommitsOverlay`, `ResolveCommitRefs`.
- `Workdir().AdvanceBaseline` / `SetBaseline` / `BaselineLog` /
  `HasUncommittedChanges`; delete `UpdateOverlayBaseline` (overlay folds in).
- Migrate CLI `baseline`/`log` + tests; delete root methods.
- Drop `patch.CommitInfoWithStat` from `f1KnownLeaks`.
- **BREAKING-CHANGES:** commit/baseline ops move under `Workdir()`.

### Step 6 — Fence tighten + docs sweep
- `public_api_test.go`: remove every leak entry closed in steps 1–5; what remains
  (e.g. `config.Layout`/`MergedConfig` from `SystemClient`, `TmuxConfigClass`,
  any deferred `Info`) is documented as conscious-defer with a reason.
- `docs/GUIDE.md` + `docs/dev/ARCHITECTURE.md`: update the API surface / call-map
  for the `Sandbox(name)` + `Workdir()` shape.
- Consolidate the `BREAKING-CHANGES.md` entries into one coherent "0.x public API
  reshape" section for the release notes.

## What this does NOT cover (separate findings)
- **`Files()` Client wiring** — deferred follow-up, gated on transport scoping
  (local host-dir vs networked stream); see `f2-subhandle-mapping.md`.
- **`Network()`** — already implemented.
- **`Info` / `config.Layout` leaks** beyond the cheap re-export — if a richer
  public `Info`/profile surface is wanted, that's its own finding.
- Later critique phases: F22 (strict `Sandbox(name)` validation), F18 (optional
  interfaces), F23 (cross-backend ops in `SystemClient`), F8 (structured
  results), F5 (`sandbox/` god-package carve), F24 (Python carves).

## Sequencing notes
- Steps are ordered so the **public types land before / with the methods that
  use them**, and each breaking step is self-contained (its callers migrate in
  the same commit). Steps 3–5 can each be one PR, or 3 split further if review
  appetite prefers smaller diffs.
- Step 1 is the only low-risk, mostly-additive one — good first landing to
  validate the materialize chain before the breaking re-rooting begins.
- F22 (strict `Sandbox(name)`) naturally follows Step 2 (it hardens the handle
  this plan introduces) — fold it in or do it right after.

# Layer 1 — Complete the public Go surface (consolidated)

**Plan only — no code yet.** Consolidates and extends the prior F1/F2/F3 work
(`f1-f3-public-surface.md`, `f2-subhandle-mapping.md`, `f2-f1f3-implementation.md`)
under the resolved 2026-05-30 direction.

## Resolved direction

yoloAI is a **library first**. The engine runs in-process; the CLI exists as a
proof-of-concept consumer that **keeps the library honest** about completeness. A
separate daemon app (its own module) will embed the library to expose REST + MCP;
GUIs/agents consume that daemon over the wire — they never link yoloAI. yoloAI
itself never becomes a daemon or a thin wire client ("layer 3", deferred).

"Layer 1" = make the public Go surface complete enough that **every capability is
reachable through `yoloai.*`**, with contract types usable by an external module.

## Two acceptance gates (these are different things)

1. **External-embedder gate (Half A) — mostly done.** `public_api_test.go`'s
   `f1KnownLeaks` is empty (or only conscious-defers with a written reason).
   Measures: *does the public `yoloai` surface expose internal types an external
   module can't name?* Mechanism already chosen and in use: **alias-at-root** (a
   `type X = internal.X` alias re-exports the type identity so external code can
   construct/use it without importing `internal/`).
2. **Consumer-honesty gate (Half B) — NOT started.** `internal/cli` *and*
   `internal/mcpsrv` compile with **zero `internal/sandbox[/...]` imports** (except
   their own CLI-tier helpers), enforced by depguard. Measures: *does every
   capability actually HAVE a public home the consumers use* — not just "can an
   external embedder theoretically compile." `internal/mcpsrv` is the daemon's
   prototype and the canary.

The prior plan delivered Half A's mechanism but never aimed at Half B — the CLI
still reaches into `internal/sandbox` in 42 files, mcpsrv in 2. Half B is the new
work that makes the honesty claim true and is the real proof the library is
complete.

## Current status

- **`f1KnownLeaks` remaining (1):** `config.MergedConfig` (ProfileInfo.Merged /
  .Parent) — a documented, conscious deferral (see A4). Closed in A4 (2026-05-30):
  `config.Layout` (SystemClient now built from public `yoloai.SystemOptions`) and
  `sandbox.TmuxConfigClass` (was a detector false-positive — it is aliased; the
  leak detector now unwraps `*types.Alias`, see below). Closed in A3 (2026-05-30):
  `sandbox.CloneOptions` (Clone re-rooted to public `yoloai.CloneOptions`) and
  `patch.CommitInfoWithStat` (commits re-rooted to `yoloai.CommitInfo` via
  `Workdir().Commits`).
- **Leak-detector fix (2026-05-30):** under Go 1.22+ `gotypesalias=1` (default since
  1.24), `type X = pkg.Y` materializes as `*types.Alias`, which `internalTypeKey`
  did not unwrap — so `collectAliasedInternalTypes` silently recorded no aliases.
  Aliased func params/results passed only because a reference to the alias keyed to
  `""`; but a `const` whose value type is the *underlying* aliased Named (the
  `TmuxConfig*` consts) surfaced it as a false leak. `internalTypeKey` now unwraps
  `*types.Alias` via `types.Unalias`, restoring the intended alias coverage.
- **Prior 6-step plan (`f2-f1f3-implementation.md`):** Steps 1 (creation),
  4a–4f (Apply), 2 (Sandbox lifecycle handle), 3 (Workdir().Diff), and 5 (Workdir
  commits/baseline + tags) **DONE**. Step 6 (fence/docs — the C gate) **REMAINS**.

## Half-B inventory — what CLI/mcpsrv grab from `internal/sandbox` today

(From the symbol categorization, ~270 refs across 42 CLI files.)

| Category | Symbols | Public home needed |
|---|---|---|
| Error vocabulary (~75) | `NewUsageError`, `ErrSandboxNotFound`, `NewPlatformError`, `IsDiskSpaceError`, `ExitCoder`, … | **B1** — public `yoerrors` package |
| Status read-model (~100) | `Info`, `Status`+`Status*` consts, `TagInfo`, `DetectStatus` | **B2** — root re-export + Client method |
| Presentation/prompt (~17) | `Confirm`, `FormatSize`, `FormatAge`, `DirSize` | **B3** — `cliutil` (F4/F5) |
| Parse/input (~14) | `DirSpec` (dup w/ `yoloai.DirSpec`), `ParseDirArg`, `ParseAuxDirArg`, `ExpandPath`, `ValidateBuildSecret` | **B4** — `cliutil` parse + single root `DirSpec` |
| Option stragglers | `CloneOptions` (A3 ✓ → `yoloai.CloneOptions`), `StartOptions`/`ResetOptions` (aliased ✓) | **A1/A3** |
| Bypass operations (~13) | `ListTagsBeyondBaseline`/`ListUnappliedTags`/`GetTagMessage` (A3 ✓ → `Workdir().Tags`), `WaitForAttachReady`, `ListSandboxesMultiBackend`, `NewEngine` (remain) | **B5** — Client/Sandbox/SystemClient methods |

## Plan (sequenced; every step keeps `make check` green)

### Pre — F8: retire `api_surface.go`
Salvage the Q-block resolutions into `working-notes.md` (dated D-entries), delete
the 2814-line `//go:build never` file. Do this first so we reshape the real
surface, not a drifted checkpoint. (Half-day, independent.)

### B1 — Public errors package *(additive, unblocks ~75 refs)*
Move `internal/yoerrors` → top-level `yoerrors/` (Docker `errdefs` style:
lightweight, no engine deps). Both `yoloai` and the consumers import it directly.
Delete the `internal/sandbox` error re-export aliases (F3 — the public package
stops building errors through the internal façade). The future daemon imports
`yoerrors` without linking the engine.

### B2 — Public status read-model *(additive, unblocks ~100 refs)*
Re-export at the `yoloai` root (consistent with the existing `DirMode` consts):
`type Status = status.Status`, all `Status*`/`AgentStatus*` consts, keep the
`Info`/`TagInfo` aliases. `DetectStatus` becomes `Sandbox(name).Status()` (folds
into A1). `ListSandboxesMultiBackend` → `SystemClient` method (B5).

### B3 — cliutil relocation (F4/F5) ✅ *(done 2026-05-30; see working-notes D46)*
Moved `Confirm`, `FormatAge`, `FormatSize` → `internal/cli/cliutil`. The domain
already returns typed refusals instead of prompting (Step-1 pattern), so no domain
function needs `Confirm`. Removes the two §2 policy violations.

`DirSize` stayed in the domain (`status/` leaf) — it's a filesystem measurement,
not rendering. The plan mis-grouped it. The real fix was **de-rendering `Info`**:
`DiskUsage string` → `DiskUsageBytes int64` (-1 = unknown); the CLI renders via
`cliutil.FormatDiskUsage`, mcpsrv emits raw `disk_usage_bytes`. `Info.HasChanges`
carries the same smell — deferred to a later de-render pass. **Breaking**, not
additive (drops `Info.DiskUsage` + the three relocated funcs).

### A1 — Step 2: `Sandbox(name)` lifecycle handle ✅ *(breaking; closed `CloneOptions`)*
Per `f2-f1f3-implementation.md` Step 2: `Start/Stop/Restart/Destroy/Reset/Exec/
SendInput/ContainerLogs/Dir/Status` as `*Sandbox` methods; `WaitForAttachReady`
behind `Attach`; `NeedsConfirmation` → typed `*ActiveWorkError`. CLI + mcpsrv
migrated; old root methods deleted. F22 (strict `Sandbox(name)`) folded in.

### A2 — Step 3: `Workdir().Diff` ✅ *(breaking)*
Per Step 3. Folded `Diff`/`DiffWithOptions`/`DiffRef`/`DiffOverlay` into the
single `Workdir().Diff(ctx, DiffOptions{})` that resolves copy-vs-overlay.

### A3 — Step 5: `Workdir()` commits/baseline ✅ *(done 2026-05-30; closed `CommitInfoWithStat`)*
`Workdir().Commits(ctx, CommitsOptions{Stat})` folds the three copy/overlay/stat
commit-list variants behind one verb (overlay+Stat → typed `*PlatformError`);
`Workdir().HasUncommittedChanges` replaces the root method; public
`yoloai.CommitInfo` replaces `patch.CommitInfo`/`CommitInfoWithStat` on the
surface. The **tags** bypass ops (`ListTagsBeyondBaseline`/`ListUnappliedTags`/
`GetTagMessage`) collapse into `Workdir().Tags(ctx, TagsOptions{UnappliedOnly})`,
which auto-populates `TagInfo.Message` (so `applyTags` drops its sandbox-workdir
dependency). `Client.Clone` re-rooted to public `yoloai.CloneOptions{Overwrite}`
(Q-J: no `Force` API field). See working-notes D47.

### A4 — config leaks ✅ *(done 2026-05-30; see working-notes D48)*
Decided per-type (no auto-aliasing of internal config structs):
- **`config.Layout` → hand-written public type (real fix).** `NewSystemClient` now
  takes a public `yoloai.SystemOptions{DataDir, HomeDir, Env}` and builds the Layout
  internally (mirroring `NewWithOptions`); the host-derived fields
  (HostUID/HostGID/ProcessIsRoot) come from `fileutil` the same way, so embedders
  never name `config.Layout`. Only `Env` must be threaded explicitly (§12 — the
  CLI's licensed `os.Environ()` snapshot). One call site changed: the
  `cliutil.NewSystemClient()` wrapper. **Breaking** (signature change) — see
  BREAKING-CHANGES.md.
- **`TmuxConfigClass` → not a real leak.** It is already aliased; the entry was a
  leak-detector false-positive (the `gotypesalias` gap, fixed above). Removed from
  the baseline once the detector unwraps `*types.Alias`.
- **`config.MergedConfig` → documented conscious-defer.** Promoting the full merged
  profile-config tree (21 fields + 5 nested config types) to hand-written public
  types is its own milestone — the "public profile-config API" tracked in
  `plans/TODO.md`. Aliasing only the top-level struct would leave nested fields
  un-nameable (a leak the detector can't see), which would make the F1 test lie;
  the branch's goal is a real, honest API, so we defer rather than alias.

### B4 — Parse/input at the boundary
Collapse the double `DirSpec` (keep the root alias `yoloai.DirSpec`; repoint
mcpsrv off `sandbox.DirSpec`). `ParseDirArg`/`ParseAuxDirArg`/`ExpandPath`/
`ValidateBuildSecret` parse **CLI flag strings** → `cliutil` (only the CLI parses
flags; the daemon parses its own wire input straight into `yoloai.DirSpec`). The
shared contract is the typed `yoloai.DirSpec`, not the parser.

### C — Enforce + repoint + docs (the gate)
- **C1 depguard:** new rule denying `internal/cli` + `internal/mcpsrv` →
  `internal/sandbox[/...]`, allowing `yoloai`, `yoerrors`, own `cliutil`. Model on
  the existing `cli-backend-scope` rule in `.golangci.yml`.
- **C2 repoint:** migrate every remaining `sandbox.X` → public equivalent
  (sonnet-subagent territory, driven by compile errors after C1 flips the rule).
- **C3 docs:** rewrite `ARCHITECTURE.md:49` to the now-true contract (F10); update
  GUIDE API map; consolidate `BREAKING-CHANGES.md` into one "0.x public API
  reshape" section. Fix §2 stale import paths (F9). Empty `f1KnownLeaks`.
- **Gate met when:** `f1KnownLeaks` empty (Half A) **and** depguard green with zero
  `internal/sandbox` imports in `internal/cli` + `internal/mcpsrv` (Half B).

## Sequencing rationale

B1+B2 are **additive** (no breaking signature changes) and remove the bulk of
the CLI's internal imports — do them first; they de-risk everything after. B3 was
planned additive but landed **breaking** (de-rendering `Info.DiskUsage`); since the
whole branch lands as one breaking cut into beta this was accepted (see D46). Then
the **breaking** re-rooting (A1→A2→A3, the existing plan's remaining steps), each
self-contained with its callers migrated in the same commit + a BREAKING-CHANGES
entry. B4/B5 stragglers fold into the A-steps where the operations live. C is last
— the depguard fence flips only once a public home exists for everything, then it
*permanently* prevents regression (every future consumer, including the daemon's
in-module precursor, is forced through the public surface).

## Semver note

The moment `yoerrors` + the `yoloai` contract types are the daemon's dependency,
every rename is a cross-module break. Beta breaking changes stay allowed but now
ripple to a separate consumer — track each in `BREAKING-CHANGES.md` deliberately.

## Off-spine (NOT layer 1)
F6/F7 (`lifecycle.go`/`create_prepare.go` file splits; `AGENT_STATUS_SCHEMA_VERSION`
fence) and F9 (§2 paths, folded into C3) are independent and can land anytime.

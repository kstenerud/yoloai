<!-- ABOUTME: Completed plan — replaced auto-migration with a status-driven startup -->
<!-- ABOUTME: gate + explicit `yoloai system migrate`. Shipped on branch layering-refactor. -->

# Plan: Replace auto-migration with a status-driven gate + explicit `yoloai system migrate`

> **Status: implemented** on branch `layering-refactor` (2026-06-02). Recorded in decision
> [D61](../../decisions/working-notes.md). The D60 data-dir bifurcation it builds on is unchanged; this
> plan changed *when/how* migration runs (read-only gate + one explicit command), the version-stamp
> format (JSON → plain int), and removed the engine's silent auto-migrate.

## Context

The D60 data-dir bifurcation introduced two on-disk migrations that ran **automatically and
silently** on the startup path:

1. **CLI migration** (`cliutil.MigrateCLI`, in `NewRootCmd`'s `PersistentPreRunE`): the one-shot
   flat→namespaced relocation moving a pre-namespace `~/.yoloai` into `TOP/library` + `TOP/cli`,
   then stamping `TOP/cli/.schema-version`. Ran on *every* command.
2. **Library migration** (`config.MigrateLibrary`, in `internal/sandbox/engine.go`
   `ensureLayoutScaffold`): stamped `<DataDir>/.schema-version`; v0→v1 a no-op. Ran during
   `EnsureSetup`.

The user's decision: **stop migrating automatically.** Silent auto-migration of user data on
every run is risky and surprising. A cheap, dumb status check decides one of three things on
startup — *fresh install* (create), *needs migration* (fail fast, tell the user), or *fine*
(proceed). Migration mutates the data dir **only** inside an explicit `yoloai system migrate`.

### The model

**Two realms, one shared check.** `cli` and `library` are symmetric "realms", each owning a
`DataDir` (`TOP/cli` and `TOP/library`) and its own independent integer version
(`CLISchemaVersion`, `LibrarySchemaVersion`). A single pure **realm check** looks only at a
realm's own `DataDir` + its plain-int version file — never at TOP, never sniffing other files:

- `DataDir` absent **or** empty → **Fresh**
- exists, version `< current` → **Migrate**
- exists, version `> current` → **error** ("…newer than this build supports; upgrade yoloai")
- exists, version `== current` → **OK**

**The gate** (CLI-orchestrated, in `PersistentPreRunE`, replaced the old auto-`MigrateCLI`):

- `TopDir()` absent **or** empty → the **only** fresh cases → create-fresh both realms; proceed.
- `TOP` non-empty → run both realm checks, then: any realm too-new → upgrade error; **all** realms
  Fresh → "run `yoloai system migrate`" (a v0 flat install); **some but not all** Fresh → hard
  **inconsistent-data-dir error** (a realm went missing; must not happen); else any realm Migrate →
  "run `yoloai system migrate`"; else all OK → proceed.
- The gate **never sniffs** flat/legacy markers and never mutates except create-fresh.

**`yoloai system migrate`** owns the messy parts: the v0→v1 flat→namespaced relocation lives here,
CLI-side, because the flat data sits *above* each realm's `DataDir`. It validates that TOP is
recognizable; **errors on garbage**. A v0→v2 jump is two-step inside one invocation. Idempotent;
partial failure is **not** auto-reconciled — re-running `migrate` fixes it.

**Plain-int version file.** Each realm's version file is a plain-text integer, replacing the
`schemaStamp{Version int}` JSON for both files.

**Consequence — the engine stops auto-migrating.** The `MigrateLibrary` call was removed from
`ensureLayoutScaffold`. Fresh-create is the gate's job; migration is `system migrate`'s job; direct
embedders (daemon/HTTP, who own a clean dedicated `DataDir` with no `TOP/cli` realm) call the
library realm's status + create-fresh/migrate APIs themselves.

This plan also folded in the fix to `internal/cli/root.go` (`Execute()` must chain to `NewRootCmd`'s
`PersistentPreRunE`, not clobber it) and reworked `internal/cli/root_test.go`.

## What shipped (files)

- `yoerrors/` — `MigrationRequiredError`, `InconsistentDataDirError` (+ `ExitCoder`; exit codes 13/14).
- `internal/config/schema.go` — plain-int stamp; `LayoutStatus`; `RealmStatus`; `CreateFreshLibrary`;
  kept `MigrateLibrary`.
- `internal/sandbox/engine.go` — dropped the `MigrateLibrary` call from `ensureLayoutScaffold`.
- `internal/cli/cliutil/clischema.go` — `CLIStatus`, `CreateFreshCLI`; `MigrateCLI` mutation-only +
  garbage validation.
- `system_client.go` — `DataDirStatus`, `CreateFresh`, `Migrate`.
- `internal/cli/gate.go` — the gate + `gateExempt`; wired in `internal/cli/root.go` (incl. the
  `Execute()` chaining fix).
- `internal/cli/cliutil/groups.go` — `AnnotationSkipMigrationGate`; annotated version/help/migrate.
- `internal/cli/system/migrate.go` (+ registration in `system.go`) — the command.
- Tests: `internal/config/schema_test.go`, `internal/cli/cliutil/clischema_test.go`,
  `internal/cli/gate_test.go`, `internal/cli/root_test.go` (reworked),
  `internal/cli/system/migrate_test.go`.
- Docs: `docs/BREAKING-CHANGES.md` (D60 entry), `docs/contributors/decisions/working-notes.md` (D61),
  `docs/contributors/architecture/README.md`.

## Commit plan (one logical change each)

- C1: `yoerrors` `MigrationRequiredError` + `InconsistentDataDirError`.
- C2: `config` plain-int stamp + `LayoutStatus`/`RealmStatus`/`CreateFreshLibrary`; drop engine
  auto-migrate; public `SystemClient.DataDirStatus`/`CreateFresh`/`Migrate`.
- C3: cliutil `CLIStatus`/`CreateFreshCLI`; `MigrateCLI` mutation-only + garbage validation.
- C4: gate in `root.go` (incl. folded `Execute()` fix) + `gateExempt`/annotations; rework
  `root_test.go`.
- C5: `system migrate` command + registration + test.
- C6: docs (+ this plan).

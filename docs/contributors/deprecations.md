> **ABOUTME:** The register of everything yoloAI keeps alive only for backward compatibility,
> with the date each was incurred, so retirement is a decision someone makes rather than a
> thing that silently never happens. Adding a migration adds an entry here (D127).

# Deprecations

Every entry is something the code carries **only** so an older install keeps working: a
migration rung, a compatibility reader, an alias. Each was cheap to add and is invisible once
added, which is exactly why they accumulate — nothing in a codebase ever announces that it has
outlived its reason.

**A migration is a deprecation.** The moment you write one you commit to supporting the old
form indefinitely, and that commitment is never revisited because nothing records when it
started. This file records it. See
[D127](decisions/working-notes.md#d127--a-migration-is-a-deprecation-registered-with-its-date-and-retired-after-a-settling-period).

## How this works

Two dates, and they do different jobs:

- **`Incurred:`** — the date the compatibility was added, recovered from `git log`/`git blame`,
  never estimated. A **fact**. It cannot rot, and it is what makes a due date defensible rather
  than invented.
- **`Due:`** — the date this entry comes up for review. A **decision**, made when the entry is
  written, using the grace periods below. Past it, the entry is **due for review** — a prompt,
  not a deadline. Nothing breaks, no gate fails, no bot deletes your code. **The owner decides
  when a deprecation converts into a retirement.**

**Why a date and not a release.** A release number cannot be the clock: a user may upgrade
v0.8.0 → v0.10.0 directly and never see the release that "retired" anything, so retiring on a
release schedule abandons exactly the population the compatibility exists for. Wall-clock
advances for everyone, including that user. (This is why [DF127](design/findings-deferred.md)
rejected a release number; the same objection does not apply to a date.)

### Recommended grace periods

The grace period is *how long until the population this exists for has moved on* — so it is a
property of who is stranded, not of how much the code annoys you. Pick from these unless the
entry argues otherwise, and say why when it does:

| What it is | Grace | Because |
| --- | --- | --- |
| **Never shipped** — compatibility for an unreleased/interim build | **none** | There is no population. It is dead code wearing a shim's coat; confirm and delete. |
| **Internal** — a migration rung, a legacy record reader | **6 months** | Waits on stale *data dirs*, not on people. Bounded by how long an install sits unopened. |
| **User-facing** — a flag, config key, command alias, or name in output | **12 months** | Waits on *people* editing scripts they forgot they wrote. They learn only at upgrade, and may upgrade rarely. |
| **Heuristic with a self-firing trigger** | **6 months**, as a backstop | The trigger should fire first. The date exists for when it never does. |

These are beta numbers, and deliberately short: breaking changes are currently allowed (rule 1),
installs are young (first release 2026-03-12), and the cost of stranding someone is a reinstall.
Post-beta, **lengthen the periods — do not abandon the mechanism**. The value is that the
question gets asked at all, and a longer grace asks it just as well.

### Lifecycle

**Retiring an entry is a breaking change**: it lands a `docs/BREAKING-CHANGES.md` entry under
`## Unreleased` (rule 1) and deletes the entry here. This register is the staging area for those
entries — a deprecation's whole life is *registered → settles → retired → recorded as a break*.

**`Shipped:`** names the first release carrying the compatibility, for judging exposure. Context,
not a clock; an unreleased entry says `(pending)`.

**Review status (2026-07-17): nothing is due.** The oldest entries come due 2026-09-12 through
2026-09-22. Sorted oldest-first — the top of this list is always what to look at.

## Register

### Library schema ladder — upgrading a data dir from before `.schema-version` existed

- **Incurred:** 2026-03-12 (v0.1.0 era; the ladder's floor, not a single commit) · **Shipped:** v0.1.0 · **Due:** 2026-09-12 (internal, 6mo)
- **What:** `MigrateLibrary` treats an unstamped `DataDir` as schema 0 and walks it up the sealed
  v0→v3 ladder, so a data dir last touched by any release since v0.1.0 still upgrades.
  `LibrarySchemaReleases` records schema 0 → `v0.1.0`.
- **Retire by:** raising the ladder's floor — "yoloAI upgrades a data dir from vX.Y.Z or later;
  older, reinstall" — which retires several rungs below in one decision. That is the decision
  this entry exists to make possible; today nothing states a floor at all.
- **Pointer:** `internal/config/schema.go` (`MigrateLibrary`, `migrateLibraryStep`, `libraryFrozenVersion`), `internal/config/schema_releases.go`

### Profile `extends:` inheritance chains

- **Incurred:** 2026-03-20 (`137b3319`) · **Shipped:** v0.2.0 · **Due:** 2027-03-20 (user-facing, 12mo)
- **What:** profiles no longer support inheritance chains, but `extends:` in a profile's
  `config.yaml` is still read so an old profile keeps loading. **User-facing** — a key a user may
  still have on disk.
- **Retire by:** deleting `loadProfileLegacy`/`legacyProfileConfig` and rejecting `extends:` with
  a usage error naming the replacement.
- **Pointer:** `internal/config/profile.go` (`loadProfileLegacy`, `legacyProfileConfig`, `ResolveProfileChain`)

### `store.Environment` pre-versioning records (`Version == 0`)

- **Incurred:** 2026-03-22 (`eaa0b85b`) · **Shipped:** v0.2.0 · **Due:** 2026-09-22 (internal, 6mo)
- **What:** records written before the `Version` field existed are read as version 0 and
  backfilled — `BackendType` defaults to `docker`, `ImageRef` to `yoloai-base`.
- **Retire by:** refusing a version-0 record with a message pointing at `system migrate`.
- **Pointer:** `store/environment.go` (the `Version == 0` branch and its backfills)

### `yoloai system runtime` alias for `yoloai system tart`

- **Incurred:** 2026-05-23 (`90b29917`) · **Shipped:** v0.3.0 · **Due:** 2027-05-23 (user-facing, 12mo)
- **What:** the old command name still resolves, printing
  `warning: 'yoloai system runtime' is deprecated; use 'yoloai system tart' instead.`
  **User-facing, and the only deprecation warning yoloAI prints at a user** — which is why it is
  the entry most likely to be forgotten: it already looks handled.
- **Retire by:** dropping the alias and its warning. The break is a renamed command, which the
  house style otherwise takes immediately ("hard break, no deprecated alias").
- **Pointer:** `internal/cli/system/tart/tart.go` (`Aliases`, `invokedViaRuntimeAlias`)

### CLI realm flat-v0 relocation, and the interim-build branch

- **Incurred:** 2026-06-02 (`da29cdc9`, `232e0921` — D60/D61) · **Shipped:** v0.3.0 · **Due:** 2026-12-02 (internal, 6mo)
- **What:** two readers for pre-bifurcation layouts — `isFlatV0Install`/`relocateFlatToNamespaced`
  moves a flat `TOP` into `TOP/cli` + `TOP/library` and carries `state.yaml`'s `setup_complete`
  forward; a second branch handles "a namespaced layout that predates the stamp (an interim
  build)".
- **Retire by:** dropping `isFlatV0Install`/`relocateFlatToNamespaced` with the ladder floor —
  they read a layout no release has produced since v0.3.0.
- **NOT the second branch.** `MigrateCLI`'s *"a namespaced layout that predates the stamp (an
  interim build)"* case reads like dead code and is not: its comment describes an interim build,
  but its **condition** is any TOP where `library/` or `cli/` exists without the CLI stamp — which
  includes a live shape, an integrator whose DataDir is `TOP/library` on a TOP the CLI later runs
  against. **Verified by execution 2026-07-17:** a library-only TOP makes the gate refuse with
  `inconsistent data directory`, and `system migrate` repairs it through exactly that branch. It
  is a recovery path, not a deprecation, and does not belong in this register. (It was listed here
  as "likely dead — confirm and delete" until the confirmation was actually run.)
- **Pointer:** `internal/cli/cliutil/clischema.go`

### v1→v2 launch-prefix backfill

- **Incurred:** 2026-06-08 (`87f9fe70`, W1b) · **Shipped:** v0.4.0 · **Due:** 2026-12-08 (internal, 6mo)
- **What:** backfills each sandbox's stored agent-launch prefix, which retired the Python
  `prepare_launch_command` fallback.
- **Retire by:** the ladder floor.
- **Pointer:** `internal/config/schema.go` (`backfillLaunchPrefix`)

### v2→v3 `agent.json` split — the legacy substrate-record reader

- **Incurred:** 2026-06-26/27 (`f5015c75`, `88599217` — D90/D102) · **Shipped:** v0.6.0 · **Due:** 2026-12-26 (internal, 6mo)
- **What:** an anonymous `legacy` struct still reads `agent`/`model`/`network_mode`/`network_allow`
  out of `environment.json`, from before they moved to `agent.json` and `netpolicy.json`.
- **Retire by:** the ladder floor.
- **Pointer:** `internal/orchestrator/migrate_agentcfg.go`

### `store.Environment` record ladder v1→v2, and its legacy fields

- **Incurred:** 2026-06-27 (`10004e1a`) · **Shipped:** v0.6.0 · **Due:** 2026-12-27 (internal, 6mo)
- **What:** `LegacyWorkdir`/`LegacyDirectories` are retained purely so `migrate()` can collapse
  them into the ordered `Dirs` list (D81).
- **Retire by:** the ladder floor. Note this is the **per-record** ladder (`environment.json`'s
  own `Version`), distinct from the realm-level one — it has its own floor to raise.
- **Pointer:** `store/environment.go` (`migrate`, `LegacyWorkdir`, `LegacyDirectories`)

### v3→v4 overlay flatten

- **Incurred:** 2026-07-01 (`a9683dc9`) · **Shipped:** v0.6.0 · **Due:** 2027-01-01 (internal, 6mo)
- **What:** `OverlayFlatten` converts any surviving `:overlay` sandbox to `:copy`. `:overlay` is
  retired as a mode (D109); this migrator exists only for stragglers.
- **Retire by:** the ladder floor. Its Apply also opens a backend, so retiring it removes the
  only reason `system migrate` ever contacts one.
- **Pointer:** `internal/orchestrator/migrate_overlay.go`

### v4→v5 principal rename, and `store.LegacyCLIInstanceName`

- **Incurred:** 2026-07-16 (`3ca2828e`) · **Shipped:** v0.9.0 (pending) · **Due:** 2027-01-16 (internal, 6mo)
- **What:** `PrincipalRename` moves pre-D126 `yoloai-<name>` instances to `yoloai-cli-<name>`;
  `LegacyCLIInstanceName` is the sanctioned use of the bare `yoloai-` prefix that D126 otherwise
  makes unwritable, and exists only for this migrator and the overlay one.
- **Retire by:** the ladder floor. `LegacyCLIInstanceName` dies with the last migrator that
  names an old instance — grep it; it should have no other callers, and if it ever does that is
  the defect D126 exists to prevent.
- **Pointer:** `internal/orchestrator/migrate_principal.go`, `store/paths.go`

### tart's legacy-CLI VM matcher

- **Incurred:** 2026-07-17 · **Shipped:** v0.9.0 (pending) · **Due:** 2027-01-17 (heuristic backstop, 6mo)
- **What:** `legacyCLIVMName` lets the CLI's tart sweep reclaim pre-D126 `yoloai-<sandbox>` VMs,
  which the migration cannot rename (it only walks sandboxes that still have a sandbox dir) and
  which would otherwise hold a capped VM slot forever. Tracked in full as
  [DF127](design/findings-deferred.md) — the only entry here that is a **heuristic** rather than
  a fact, because tart records no labels to read (DF124).
- **Retire by:** its DF127 trigger fires *before* this date if a second real principal ever runs
  on tart — `TestPruneLegacyMatchOverreachesForAnUnseenPrincipal` fails and forces the decision.
  Otherwise: the settling period.
- **Pointer:** `runtime/tart/prune.go` (`legacyCLIVMName`, `testPrincipalVMRe`)

## Not deprecations

Recorded so they are not re-filed. Each looks like a compatibility shim and is not:

- **`runtime/orphan.go`'s empty-principal label clause** — reads a fact the backend recorded, so
  it stays exactly correct while the value simply stops occurring. Permanent by construction, no
  sunset. (DF125.)
- **`startLegacy`** — named "legacy", selected by backend capability (`runtime.ProcessLauncher`
  absent), not by age. Podman takes it today. Not on death row.
- **`runtimeconfig`'s absent→default readers** (`IdleMode`, `FallToShell`) — additive optional
  fields, deliberately never migrated. Permanent by construction.
- **The npm `@anthropic-ai/claude-code` install** — upstream-deprecated, but it is the only
  proxy-capable path and the trigger is someone else's bug (anthropics/claude-code#14165). Not
  ours to retire; tracked in `../ROADMAP.md`.

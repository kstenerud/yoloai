# Plan: crash-safe data-dir migrations

ABOUTME: Design for making `yoloai system migrate` crash-safe — discrete,
ABOUTME: per-realm, individually-versioned migrations that stage in a workdir and
ABOUTME: commit via a resumable atomic rename; per-sandbox done one at a time.

Status: **DESIGN CONVERGED ON A SPINE — open for further critique; not yet built.**
Surfaced by overlay retirement (D109); tracked as DF68. Prerequisite (step 0) for
the overlay `v3→v4` flatten and a retro-harden of the existing agent.json split.
Sequencing is **D110** (the cadence table below). The independent audit behind this
rewrite is the companion [crash-safe-migration-audit.md](crash-safe-migration-audit.md)
(18 findings, A1–A18, four parallel reviewers + in-code verification).

> This rewrite supersedes the first-cut WAL-journal design. The user has further
> critiques pending — three open decisions are flagged at the end; treat the whole
> doc as a live critique target, not a sealed spec.

## The problem (DF68 + audit-confirmed, verified in code)

1. **Stamp-before-pass (DF68).** `MigrateLibrary` calls `WriteSchemaVersion`
   (`schema.go:185`) *before* `MigrateAgentConfigs` runs (`system.go:112-119`). A
   crash between leaves `stamp == current` over half-migrated data — a green gate
   (`RealmStatus → LayoutOK`) lying about the data.
2. **No durable/atomic write primitive (A1).** Every version/mode/stamp write goes
   through `fileutil.WriteFile` → bare `os.WriteFile` (`fileutil.go:144`):
   truncate-in-place, **no temp+rename, no fsync, no dir-fsync, no `F_FULLFSYNC`**.
   The "atomic commit = atomic write of the version file" the design assumes does
   not exist.
3. **The existing agent.json split is already power-loss-lossy (A4/C3).** Its
   writes are values-first in *program order* but **not fsynced**
   (`migrate_agentcfg.go:87-110`), so under power loss the slimmed
   `environment.json` can land before its `agent.json` sibling → config lost. The
   code comment "written durably" is overclaimed.
4. **The overlay→copy flatten** will be the first multi-step *destructive*
   migration; it needs all of the above plus container-bound source handling
   (DF69). See the audit companion for the full finding set.

## Requirements

- **Exclusive** while in flight; **crash-recoverable** at any kill point;
  **never lose data** (≥1 complete consistent copy at every instant; source never
  destroyed before target durably committed); **resumable & idempotent**; a
  persistent forward bug must have an **escape** that doesn't require manual surgery.

## The model (the spine)

A migration **run** = an ordered set of **discrete, per-realm, individually-versioned
migrations**. Each discrete migration builds a staging workdir, transforms its unit,
and commits via a **resumable atomic rename**, leaving the displaced original as a
one-generation backup. On failure it discards the workdir; the original is untouched;
it retries. There is **no separate write-ahead journal** — progress lives in the
per-unit version, and the commit state lives in the filesystem naming convention.

### Staging workdir

At the top of each realm, a discrete migration stages into `migration-<build-id>/`
(keyed to the binary's build id). It is the migrator's scratch space; its internal
structure is the migrator's concern. The machinery cares only that it becomes the
unit's replacement. A *different* build id → discard-and-restart (never resume
another migrator's partial staging — the source is intact, so a clean restart is
safe). A `target-version` marker inside the workdir records the intended end-state
so recovery doesn't have to trust the binary. Orphaned `migration-*` dirs are GC'd.

### Two granularities

- **Realm-structural** (layout / shared files; e.g. the CLI flat→namespaced
  relocation): all-or-nothing. Build the new realm form in the workdir — **moving /
  renaming** sandbox dirs into the new layout, never *copying* their bulk — then one
  resumable atomic swap replaces the realm. Crash → discard workdir, original
  intact, retry. The `.schema-version` stamp lives **inside** the swapped form, so
  it commits atomically with the realm.
- **Per-sandbox** (e.g. agent.json split, overlay flatten): iterate sandboxes **one
  at a time**. For each: the machinery seeds a per-sandbox workdir with a faithful
  reflink/copy of the sandbox, the migrator transforms it, and the machinery commits
  by atomically renaming that sandbox's dir into place. Each sandbox commits
  independently; a sandbox that can't migrate is **quarantined**, not fatal.

### The truth invariant (the DF68 fix)

For per-sandbox migrations the **per-sandbox record version is the source of truth**;
the realm `.schema-version` stamp is a **cache, flipped last** — only after a scan
confirms every sandbox is at ≥ target (or quarantined), *and* only after those
commits are durable (the fsync barrier). Recovery never trusts the stamp over the
records: it rescans and migrates stragglers (idempotent). The stamp is **physically
incapable** of being ahead of the data (stamp-last + barrier). This is the proven
agent.json-split pattern (each record checks its own version), hardened with atomic
commit + durable writes.

### Machinery vs migrator (the separation)

- **Generic machinery owns:** the `migration-<build-id>` workdir lifecycle; **seeding**
  the per-sandbox workdir with a faithful reflink/copy of the source (so unchanged
  data survives untouched — correct-by-default, with special-file + disk-preflight
  handling written *once*, not per migrator); the **durable-atomic-rename** primitive;
  the **resumable promotion** (naming convention) and its recovery; backup retention
  + GC; version-check gating; the run lock.
- **Each migrator owns:** how to transform its unit inside the workdir; and **source
  consistency** — starting/stopping containers, avoiding torn reads, and **detecting
  and refusing** an already-destroyed source (A2). It *declares* which blessed
  consistency strategy it uses (so the discipline doesn't drift per migrator).

## The durable primitive (foundation — audit A1, build first)

`write-temp-in-same-dir → fsync(temp) → rename(temp, final) → fsync(dir)`, with
`F_FULLFSYNC` on darwin (plain `fsync` doesn't flush the APFS device cache). Temp
must be on the **same filesystem** as the target (`EXDEV`). Build this first and
route **every** commit *and* the `.schema-version` stamp through it, replacing
today's bare `os.WriteFile`. Everything below assumes it. This single fix also
closes A3/A4/C3 for the existing split.

## The resumable naming convention (commit state in filenames, not a WAL)

To replace unit `X`: stage `X.new-<build-id>` (fsync) → rename `X` → `X.old-<build-id>`
→ rename `X.new-<build-id>` → `X` → fsync(dir) → remove `X.old-<build-id>`. Every
crash-point state is classifiable **from the names alone** (`{X, X.new-*, X.old-*}`
→ exactly one recovery action). The rigor a WAL needed does not vanish — it moves
into this state machine, which must be **exhaustively enumerated** and covered by
**crash-injection tests at every rename boundary**. The displaced `X.old-<build-id>`
is the one-generation backup.

## Source consistency (migrator concern; blessed strategies)

A small, shared set of strategies a migrator declares — not per-migrator hand-rolling
(avoids the ad-hoc-guard drift the project warns against):

- `QuiesceSandbox` — ensure the sandbox's container is stopped / agent-free before
  reading. **Quiescence comes from `DetectStatus == Stopped`, not the flock** (A5:
  the per-sandbox flock is released once the container launches, so a live agent
  holds no lock). Per-sandbox granularity means quiescing **one** sandbox at a time.
- `LiveContainerExtract` — for overlay: bring up an agent-free container, run the
  existing `apply` to materialize the flatten to host staging, fsync, tear down.
- **Destroyed-source refusal (A2, mandatory, not a "torn read"):** a *stopped* macOS
  overlay sandbox's upper is tmpfs-only and **already gone** (DF69, confirmed). The
  migrator must **detect and refuse** with a loud message — it cannot mitigate a
  source that no longer exists.

## Backup & downgrade

The displaced `*.old-<build-id>` *is* the backup; retain it one generation (retire on
the next migration). Per-sandbox backups give per-sandbox recovery *during* a run.
**R1 (realm downgrade ratchet) is not solved by this** — once the realm stamp flips,
an older binary hard-errors (`RealmStatus`: "newer than this build supports"). See
open decision 3.

## Open decisions (critique targets)

1. **Bad-sandbox policy (A12).** Recommended: **quarantine-and-continue** (set the
   failed sandbox aside in `trash/`, migrate the rest, flip the stamp once all are
   migrated *or* quarantined; the new-binary detector still refuses the quarantined
   one individually). Alternative (abort the run) bricks the realm on one bad sandbox.
2. **Ordering & realm-structural cost.** A run is an *ordered* sequence; a realm
   relocation that moves sandboxes must run before per-sandbox passes that iterate
   them. Realm-structural migrations **move/rename**, never copy sandbox bulk.
3. **R1 downgrade.** Retain backups one generation + a reversible realm step (a
   compat window), *or* accept and document "migration is one-way." Decide, don't
   inherit by accident.
4. **Network / synced FS (A11, was OQ4).** Lean **hard-refuse** on detected network
   FS *with a spelled-out escape* (relocate / `--data-dir`); detect common synced
   roots (Dropbox/iCloud — flock is meaningless across the sync); stamp the run with
   a host+boot id so a second host is refused.
5. **Gate ordering & live lock (A10/L2).** Wire a **live-flock** try-acquire (not a
   presence/file check, so a crash never leaves a permanent refusal) into the gate,
   evaluated **before** the needs-migration message (else a running migration tells
   other commands to "run system migrate").
6. *(plus the user's pending critiques)*

## Cadence (D110)

Every migration runs against a schema **frozen in a prior shipped release**:

| Release | Schema | Migration | Overlay code | Ships |
|---|---|---|---|---|
| **0.6.0** | v2→**v3** (freeze) | agent.json split, made crash-safe | yes (dangerous opt-in + warning) | staged 12 commits; reflink-`:copy`; **the floor**: durable-write primitive + stamp-last (move `WriteSchemaVersion` out of `MigrateLibrary` into `MigrateDataDir`) + run lock — fixes A1/A4/DF68 in 0.6.0's own migration |
| **0.7.0** *(migration version)* | v3→**v4** | overlay→copy flatten (per-sandbox, agent-free, while-live per DF69) + `migrate --check` audit | yes (last release with overlay) | the per-sandbox staging+promotion machinery + `LiveContainerExtract` |
| **0.8.0** *(post-removal)* | v4 | v3→v4 step → detect-and-refuse | no (reader deleted, **detector kept forever**) | 5-element refusal naming `yoloai-0.7.x` |

**0.6.0 floor is non-negotiable:** A1/A4 mean 0.6.0's *own* v2→v3 split loses config
on power loss today — the durable-write primitive + stamp-last must ship in 0.6.0
even if the per-sandbox staging machinery waits for 0.7.0. The heavier
snapshot/promotion surface lands in 0.7.0, where the destructive flatten both needs
*and* exercises it (v2→v3 is non-destructive, so it can't meaningfully bake rollback
anyway).

## The git question (answered: borrow the patterns, not the tool)

Git is instructive but the wrong vehicle (GEN §14). **Borrow:** atomic rename as
commit, content-addressed retained generations. **Reject git-the-tool:** it can't
faithfully represent the data (overlay whiteouts are char devices; ownership/xattrs
/special-file fidelity lost), would run `filter`/`clean` drivers (the C1 mechanism we
just contained), and is slow on binary trees. The contract-fit primitives are
durable atomic rename + a filesystem CoW/seed copy + version-checks — byte-exact,
metadata-preserving.

## Research

- [research/crash-safe-migration.md](../research/crash-safe-migration.md) — SQLite /
  ARIES / POSIX-rename / Nix-OSTree / dpkg-rpm prior art. Key: build-alongside +
  atomic rename; forward=resume-first, rollback=wholesale-restore-only; the
  container-bound extract exception.
- [research/migration-version-gating.md](../research/migration-version-gating.md) —
  stepping-stone + detect-and-refuse (the 0.7→0.8 split, the 5-element refusal).
- [research/reflink-vs-hardlink.md](../research/reflink-vs-hardlink.md) — the snapshot
  primitive is **reflink-or-full-copy** (hardlink rung dropped).

## Relationship to other work

- **Audit:** [crash-safe-migration-audit.md](crash-safe-migration-audit.md) (A1–A18).
- **Prerequisite for** overlay retirement
  ([retire-overlay-reflink-copy.md](retire-overlay-reflink-copy.md), D109); cadence
  **D110**.
- DF68 (this finding), DF69 (macOS overlay live-or-lose); migration philosophy: dumb
  plain-int stamps, explicit fail-fast `migrate` owns recognition/validation.

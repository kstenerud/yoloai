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

### Staging workdir (disposable, never resumed)

At the top of each realm, a discrete migration stages into `migration-<build-id>/` —
the migrator's scratch space, its internal structure the migrator's concern. **Scratch
is disposable and never resumed:** any yoloai invocation (when no migration holds the
lock) throws out a leftover scratch dir — a crashed build is garbage, rebuilt fresh,
never recovered. So the *build* phase (the complex part — containers, extract) needs no
crash-recovery; **only the live-dir *rename* (promotion) is resumable.** A scratch dir
is **not** a reason to block other functions. The build-id is for hygiene/debugging,
not resume. Scratch must be on the **same filesystem** as the live dir (decision 4) so
the move-in is atomic.

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
  the **resumable promotion** (naming convention) and its recovery; scratch disposal +
  transient-`*_^^_orig` cleanup; version-check gating; the **whole-tree run lock**.
- **Each migrator owns:** how to transform its unit inside the workdir; and **source
  consistency** — starting/stopping containers, avoiding torn reads, and **detecting
  and refusing** an already-destroyed source (A2). It *declares* which blessed
  consistency strategy it uses (so the discipline doesn't drift per migrator).

## Durability: fsync at the promotion boundaries (audit A1, reframed)

The dir-rename promotion scheme + the whole-tree flock already give commit
**atomicity** (a reader never sees a half-written unit; concurrency is excluded), and
the recovery scan handles resume — so there is **no general per-file atomic-write
primitive** wrapping every write. The unit commit *is* the directory rename.

What rename does **not** give is **durability**: `rename(2)` is atomic but a power loss
before the dir entry is flushed can lose it, and — worse — a moved-in `*_^^_new` whose
dir entry is durable but whose file *contents* are still in page cache reads back as
zero/garbage after power loss (the classic rename-without-fsync corruption), which the
recovery scan would then **promote as "complete."** The scan is only trustworthy if the
renames *and* the built contents are durable. So the machinery owns a **bounded fsync
discipline at the promotion boundaries** (not a wrapper on every write):

1. After building a unit in scratch, **fsync its files + dir before the move-in** (so a
   `*_^^_new` in the live dir is never durable-but-empty).
2. **fsync the parent dir after each promotion rename** (so the rename survives power
   loss and the scan sees a real point in the sequence).
3. The realm stamp flip is **one** atomic-durable single-file write
   (temp → fsync → rename → fsync(dir)), or folded into the swapped realm dir for
   realm-structural migrations. `F_FULLFSYNC` on darwin (plain `fsync` doesn't flush the
   APFS device cache). Same-filesystem temp (`EXDEV`; decision 4 guarantees it).

**Scope of the contract (flagged choice).** These fsyncs are exactly the price of
**power-loss / kernel-panic** safety. A plain process death (kill/crash/Ctrl-C) leaves
the page cache intact, so the scheme is already safe against it *without* fsync.
Recommendation: **keep the boundary fsyncs** — migrations touch irreplaceable data, are
rare, and the cost is a handful of fsyncs per sandbox. (Alternative: promise only
process-death safety and drop them — simpler, weaker.)

**C3/A4 (the existing split losing data) is fixed by routing the split through this
scheme** (writes land in disposable scratch; commit is the durable dir-rename) — *not*
by atomic-wrapping `SaveEnvironment`. Normal-operation writes outside migration are a
separate robustness question, out of scope here.

## Promotion: build complete, then resumable rename (commit state in filenames)

Two schemes, **one principle: copy what you *transform*, move what you only
*relocate*.** Incomplete builds live in the `migration-<build-id>/` scratch dir, which
**must be on the same filesystem as the live dir** (or the move-in is a non-atomic
copy+delete and a sentinel dir can appear *partial*). Only **complete** units ever
enter the live dir, under reserved sentinel names — the `_^^_` token **must be illegal
in a real sandbox/realm name** (validate/reserve it). The per-unit **version**
disambiguates the one ambiguous state (canonical name alone = not-started *or* done →
read its version, the truth).

**Per-sandbox** (the migrator rebuilds the sandbox's data — e.g. overlay→copy extract):
1. Build the complete new sandbox in scratch; on completion move it into the live
   sandboxes dir as `mysandbox_^^_new` (atomic rename — same FS).
2. rename `mysandbox` → `mysandbox_^^_orig`
3. rename `mysandbox_^^_new` → `mysandbox`
4. delete `mysandbox_^^_orig`

Recovery: `mysandbox`+`_^^_new` → resume at 2; `_^^_orig`+`_^^_new` → resume at 3;
`mysandbox`+`_^^_orig` → resume at 4; `mysandbox` alone → check its version. **Every
dir in the live sandboxes dir is always a complete, functional sandbox** — only
renames/deletes touch the live dir; the incomplete build stays in scratch.

**Realm-structural** (relocate/restructure *without* copying the bulk sandboxes):
1. Build the new realm **structure** (layout + migrated files, **not** the bulk) in
   scratch; move it to `library_^^_new` under `$YOLOAI_HOME`.
2. **Final migration:** move the deterministically-derivable set of unchanged items
   (sandboxes, …) from `library` into `library_^^_new` — each an atomic dir-rename.
3. rename `library` → `library_^^_orig`; rename `library_^^_new` → `library`; delete
   `library_^^_orig`.

Recovery during step 2 re-derives the item list and resumes (idempotent: an item
already in `_^^_new` is skipped). **Invariant relaxation to note:** during step 2
neither `library` nor `library_^^_new` is individually complete — the **union** is,
and every item is atomically in exactly one side (never torn). Safety rests on the
derivable item-list being *exact* + crash-tested. Step 2 is **forward-only** (once the
bulk move starts, old `library` is being gutted — clean abort is only available
*before* step 2 begins). Alternative that keeps old `library` whole until the swap:
**reflink** the bulk into `_^^_new` instead of moving (cheap on CoW FS, full-copy on
ext4) — a conscious trade.

The rigor a WAL needed doesn't vanish; it moves into these state machines, which must
be **exhaustively enumerated** and covered by **crash-injection tests at every rename
boundary**. The displaced `*_^^_orig` is **transient** (deleted as the final step) —
not a retained backup; migration is one-way (see No downgrade).

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

## No downgrade (decision 3)

Migration is **one-way.** The `*_^^_orig` displaced during a promotion is **transient**
(deleted as the final step once the new unit is live) — *not* a retained backup; there
is no compat window and no reversible realm step. Once the realm stamp flips, an older
binary hard-errors (`RealmStatus`: "newer than this build supports") — accepted (R1).
The escape from a *persistent forward bug* is **quarantine** (per-sandbox: set the bad
sandbox aside, its data preserved in `trash/`), backed by the fact that the original is
untouched until each commit (a pre-commit failure leaves the old data fully intact at
the old stamp). A persistent *realm-structural* bug is fix-forward only — acceptable
because those are simple metadata moves. Document prominently: **migration is one-way;
back up `~/.yoloai` before upgrading.**

## Single-filesystem requirement (decision 4)

Migration **hard-refuses** unless the entire realm *and* its scratch dir sit on **one
local filesystem**, so every `mv`/rename is atomic (no `EXDEV`, no copy+delete that can
leave a partial sentinel dir). This subsumes the network-FS refusal (flock is unreliable
on NFS/SMB and meaningless across a synced root like Dropbox/iCloud) and the
spanning-mounts case. The refusal names the escape (relocate `~/.yoloai` onto a single
local FS and retry).

## Exclusivity & crash-recovery gating (decision 5)

**One live `flock` over the entire `$YOLOAI_HOME`**, held by `system migrate` for the
whole run — even though a given migration touches only part of the tree. While held,
**every other yoloai command refuses** ("migration in progress"); a second `migrate`
refuses too. It is a *live* flock (released on process death), so a crash never leaves a
permanent lock. After a crash (flock released), two persistent signals gate recovery:

- **Scratch dir present** → disposable; toss it; **does not block** anything.
- **Half-finished rename in a live dir** (a `*_^^_new`/`*_^^_orig` sentinel) → a
  promotion was interrupted → **block everything except `migrate`**, which completes it
  (the dirs involved are complete, so it's only renames + a delete). An **independent**
  guard — belt-and-suspenders with the stamp gate (a sentinel implies the stamp hasn't
  flipped, but the physical presence of an in-flight rename must block on its own).
- **Stamp < current** (the existing gate) → route to `migrate`.

Migrate's recovery order: toss scratch → complete any in-flight live-dir renames →
resume the run (rescan per-unit versions, migrate stragglers).

## Open decisions (critique targets)

1. **Bad-sandbox policy (A12) — DECIDED: quarantine-or-abort, user's choice.** On a
   sandbox that can't migrate, the user chooses **quarantine** (set it aside in
   `trash/`, continue, flip the stamp once all are migrated-or-quarantined; the
   new-binary detector still refuses it individually) **or abort** the run. Abort is
   clean: already-committed sandboxes stay migrated (atomic + independent), the
   failed + remaining stay at the old version, stamp unflipped → re-runnable.
   Non-interactive runs take the choice via a flag, **default abort** (safe in
   headless contexts).
2. **Ordering & realm-structural cost.** A run is an *ordered* sequence; a realm
   relocation that moves sandboxes must run before per-sandbox passes that iterate
   them. Realm-structural migrations **move/rename**, never copy sandbox bulk.
3. **R1 downgrade — DECIDED: no downgrade.** See [No downgrade](#no-downgrade-decision-3).
4. **Network / synced FS — DECIDED: hard-refuse + single-FS.** See [Single-filesystem
   requirement](#single-filesystem-requirement-decision-4).
5. **Exclusivity / gate — DECIDED: whole-tree live flock.** See [Exclusivity &
   crash-recovery gating](#exclusivity--crash-recovery-gating-decision-5).
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

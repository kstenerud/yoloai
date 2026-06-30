# Plan: crash-safe data-dir migrations (initial design — for critique)

ABOUTME: Initial design for making `yoloai system migrate` crash-safe — exclusive
ABOUTME: lock, write-ahead journal, per-sandbox atomic commit, snapshot rollback.

Status: **INITIAL DESIGN — draft to be critiqued and refined; not yet built.**
Surfaced by the overlay-retirement work (D109); tracked as DF68. This substrate
is the **prerequisite (step 0)** for the overlay `v3→v4` retirement
([retire-overlay-reflink-copy.md](retire-overlay-reflink-copy.md)), and it
retro-hardens the existing agent.json split.

> ⚠️ This is a first cut to anchor the critique cycle, not a finished spec. The
> open questions at the end are real. Prior art should be mined before building
> (see "Research"). The whole point is to **not** invent a bespoke recovery scheme
> when this is a well-solved problem.

## The problem (DF68)

Migrations today are not crash-safe:

1. **Stamp-before-pass.** `MigrateDataDir` stamps the realm (`MigrateLibrary →
   WriteSchemaVersion`) *before* the per-sandbox pass runs. A crash mid-pass
   leaves a green gate (`stamp == current`) over half-migrated data; the invariant
   *stamp == current ⟹ data == current* is broken.
2. **No atomicity / journal.** A multi-step destructive transform — the planned
   overlay→copy flatten swaps two directory layouts in *one* path
   (`OverlayWorkBaseDir == WorkDir`) and flips `Mode` in a *separate* file — has no
   single atomic commit point, so a crash can leave a state re-run cannot classify
   ("what was migrated and what wasn't").
3. **No escape from a persistent bug.** If forward-migration code crashes
   deterministically, roll-forward loops forever and the user is bricked (normal
   operation is blocked until migration succeeds).

## Requirements

- **Exclusive:** no other process touches the data while a migration is in flight.
- **Crash-recoverable:** a kill at *any* point leaves a state from which a re-run
  deterministically completes (roll-forward) or reverts (rollback).
- **Never lose data:** at every instant, ≥1 complete, consistent copy of the data
  exists. The source is never destroyed before the target is durably committed.
- **Escape hatch:** a persistent forward-migration failure must be recoverable —
  the user can get back to a working state (downgrade + run) without manual
  surgery.
- **Resumable & idempotent:** re-running after interruption converges.

## Design

Three mechanisms, layered. (Two-level commit: per-sandbox commit + run-level stamp.)

### 1. Exclusive migration lock

A data-dir-root advisory lock (`flock` on `migration.lock`) held for the whole
`system migrate`. Two independent gates at startup:
- **needs-migration** — `RealmStatus` stamp `< current` → refuse normal commands,
  direct to `system migrate`.
- **migration-running** — lock held → refuse with "migration in progress."

Caveat: advisory locks are unreliable on network filesystems (see DF36 — the data
dir on NFS/SMB already warns); document the residual.

### 2. Write-ahead journal (telegraph, then follow the journal)

A single append-only journal at the data-dir root. Each entry is an atomic append
(`write` + `fsync` of a record). Protocol per destructive step:

1. Append an **intent** record (the op + enough to *redo* and to *undo* it).
2. fsync. Perform the step.
3. Append a **done** record.

On recovery (journal present and not finalized): replay. For each pending op,
roll-forward (idempotently complete it) or, if rolling back, undo via the recorded
info. The journal is the source of truth for in-flight work — "following the
journal" *is* performing the migration. Clear/archive the journal atomically only
after the whole run commits.

### 3. Per-sandbox atomic commit + non-destructive ordering

Each sandbox is the unit of work, with a single linearization point:

1. Build the new form in a **staging** path (never in place); verify it.
2. **Commit** = the atomic write of the version/mode-bearing file
   (`environment.json`): the `Mode`/`metaVersion` flip *is* the commit. Everything
   before it is discardable staging; everything after is idempotent cleanup.
3. GC the old form (overlay dirs / staging) — only after the commit.

Invariant: the overlay source (upper/lower) is **never** deleted until the copy is
durable and the record flipped. Crash before commit → original intact → redo;
crash after → finalize cleanup (idempotent). Hold `store.AcquireLock(name)` across
each sandbox.

For single-file-commit migrations (the agent.json split: write `agent.json`, then
one atomic `environment.json` rewrite bumping `metaVersion`) the version flip is
the commit point — no extra journaling needed beyond #2's run-level guarantees.
Retrofit the existing split to this ordering.

### 4. Pre-migration snapshot → rollback (the persistent-bug escape)

Before mutating, snapshot the data dir (or the parts a migration touches) to
`~/.yoloai/.migration-backup-<from>-<to>/`:

- **Cheap where possible:** a reflink/CoW clone (btrfs/XFS/APFS — the *same*
  primitive the reflink-`:copy` work adds; near-instant, disk-shared). Full-copy
  fallback on non-CoW (slower; migrations are rare and explicit).
- The snapshot is recorded in the journal as "ready" *before* any mutation, and
  **preserved until the new stamp is durably committed**, then GC'd.

Recovery policy:
- First attempt **roll-forward** (idempotent redo via the journal).
- On a **persistent** failure (repeated, or `system migrate --rollback`):
  **restore the snapshot** — a simple, robust swap-back, *far* less code than the
  migration, so far less likely to share its bug. After restore the stamp is back
  at `<from>`; the user can downgrade the binary and keep working.
- "If rollback crashes we're sunk" → it can't lose data: the snapshot is a
  complete, untouched copy preserved until commit, so the worst case is a
  **manual** file-level restore (it's plain files). The hard invariant — *never
  destroy both the live data and the snapshot* — means there is always a complete
  copy to recover from.

### 5. Run-level stamp-last

The realm `.schema-version` is written **only** after every step (realm + every
sandbox) commits and the journal finalizes — by the orchestrator, not inside
`MigrateLibrary`. Stamp = the run's commit marker.

## The git question (raised; answered: borrow the patterns, not the tool)

Git is instructive but the wrong vehicle (GEN §14 — don't lean on an incidental
property). **Borrow the patterns:** atomic ref update (= atomic rename as the
commit), the reflog (= an append-only WAL), content-addressable snapshots. **Reject
git-the-tool:** it can't faithfully represent the data — overlayfs whiteouts are
char devices git can't store, and it drops ownership/xattrs/special-file fidelity;
it would run `filter`/`clean` drivers on content (the very C1 mechanism we just
contained); it's slow on large/binary trees; and modeling a live data dir + work
trees as a repo reintroduces cross-system atomicity. The contract-fit primitives
are `flock` + an intentions journal + a filesystem CoW snapshot — purpose-built,
byte-exact, metadata-preserving.

## Research (done — see [research/crash-safe-migration.md](../research/crash-safe-migration.md))

Verified 2026-06-30 across SQLite (rollback-journal/WAL), ARIES, POSIX
atomic-rename, Nix/OSTree, and dpkg/rpm. Headline findings that feed back into
this design (detail + sources in the research file):

- **Build-alongside + atomic pointer-swap beats mutate-in-place + restore-backup**
  (Nix/OSTree unanimous). The pre-migration snapshot should *be* the retained old
  generation; rollback = an O(1) pointer flip, not a copy-back. → revisit §3/§4.
- **Forward = resume-first; rollback = wholesale snapshot-restore only**, never
  reverse-replay (dpkg roll-forward; rpm *shipped* reverse-replay rollback and
  **deleted it as "too unreliable"**). → confirms/sharpens §4.
- **Journal = coarse per-sandbox state, not a fine WAL** (dpkg state-machine;
  ARIES's CLRs exist only because a DBMS lacks our global snapshot). The snapshot
  is the undo. → simplifies §2.
- **WAL ordering is the one non-negotiable**; recovery validity = sentinel-after-
  body + per-record checksum (SQLite); commit idiom = write-temp-same-dir →
  fsync → rename → fsync(dir), macOS `F_FULLFSYNC`. → concretizes §2/§3.
- **OQ4:** lean hard-refuse on network FS rather than "document the residual."

## Open questions (for the critique cycle)

1. **Snapshot scope & cost:** whole data dir vs only touched sandboxes; the
   non-CoW full-copy cost (opt-out? bound? warn?); where the backup lives and its
   GC.
2. **Auto-rollback trigger:** how to distinguish a *persistent* failure (→ offer
   rollback) from a transient one (→ retry roll-forward) without guessing.
3. **Journal format & granularity:** per-op vs per-sandbox records; how much
   undo info to record; fsync cost.
4. **Lock semantics on network FS** (DF36) — residual or hard-refuse?
5. **Interaction with the realm-step model:** does each `vN→vN+1` step get its own
   journal+snapshot, or one envelope around the whole run?
6. **Rollback UX:** restoring the stamp + telling the user to downgrade; whether
   `--rollback` is explicit-only or offered automatically.

## Relationship to other work

- **Prerequisite for** overlay retirement ([retire-overlay-reflink-copy.md](retire-overlay-reflink-copy.md),
  D109) — the overlay flatten is the first multi-step destructive migration and
  cannot be crash-safe without this.
- **Synergy with** reflink-`:copy` (same plan) — the CoW snapshot primitive is the
  reflink primitive; both land together.
- DF68 (this finding); migration philosophy: dumb plain-int stamps, explicit
  fail-fast migrate command owns recognition/validation.

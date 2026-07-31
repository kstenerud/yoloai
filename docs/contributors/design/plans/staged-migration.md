> **ABOUTME:** Run the whole migration ladder in scratch and promote once, so the live data dir is
> never partly migrated — and so each migration step can assume the exact layout of its own era.

# Staged migration: run the ladder in scratch, promote once

- **Status:** PLANNED — designed with the owner 2026-07-31; no code yet. Every claim about existing
  machinery below was checked against the source, and the parts that already exist are marked as
  such so nobody rebuilds them.
- **Depends on:** —
- **Rides:** **a migration** — it *is* the migration framework change, and
  [sandbox-share-tiering.md](sandbox-share-tiering.md) cannot finish without it ([DF164](../findings-unresolved.md)).

## Why

Today each migrator promotes its own units as it goes, so a crash between migrators leaves the live
realm partly migrated: every unit internally consistent, but at different versions. Worse, and this
is what forced the design, **every pre-v6 migrator addresses the sandbox through the *live* path
builders**. Move a file into a tier and those migrators silently start looking in the layout they are
migrating *to* — finding nothing, and reporting "nothing to migrate" (DF164).

The fix is not to teach each migrator about tiers. It is to make **each step a shape → shape
transform** that assumes the exact layout of its own era, and to run the whole ladder somewhere the
live directory cannot be damaged.

## The model

1. **Stage.** Copy what the plan touches into scratch. Nothing enters the live dir yet.
2. **Ladder.** Run each migration in series, in scratch. Each step consumes the shape the previous
   step produced and leaves its own. Intermediate shapes are private to scratch and need satisfy
   nobody but the next step.
3. **Promote once.** When the last step completes, the shape matches current, and the staged tree
   replaces the live one.
4. **Failure discards.** Any failure before the final promotion throws the scratch away. The live
   dir is untouched, and a re-run starts over.

## What already exists — do not rebuild it

| Piece | Where | Note |
| --- | --- | --- |
| Atomic promote + crash recovery | `internal/migrate/promote.go` | `U` / `U_^^_orig` / `U_^^_new`, six-state classifier, exhaustive. `^^` is illegal in any sandbox or realm name, so the sentinels can never collide with live data |
| Same-filesystem precondition | `migrate.SameFilesystem` | Scratch must share a filesystem with the live dir or the move-in fails `EXDEV` mid-migration |
| Well-known scratch, per home | `migrate.ScratchPath(home)`, `.migration-scratch` | Already correctly scoped to the **home**, not the machine — an embedder can pass its own `DataDir`, and `AcquireHomeLock` locks per-home |
| Plan-before-apply over the whole set | `migrate.CollectPlans` + `Authorize` | Collects every migrator's plan first; `ApplyAll` re-derives under the lock and refuses a migrator that became destructive since planning |
| Approval axis | `migrate.Auth` | `AuthNone` / `AuthConfirm` / `AuthAbandonOverlay` / `AuthBlocked` |
| Stamp-as-resume-point | `Promotion.WriteReadyMarker` / `IsReady` | Written last, immediately before the promoting rename (D110). `IsReady` is explicitly the done-vs-not-started discriminator |
| Migrators parameterised on the root | `PrincipalRename.sandboxesRoot`, `OverlayFlatten.sandboxesRoot` | They take the sandboxes root as a field, so pointing them at a staged root is configuration, not surgery |

**A single directory `rename(2)` on one filesystem is atomic.** What is not atomic is the *pair* of
renames needed to swap two directories, which is the entire reason for the three-name scheme. The
suffixes are not a workaround for missing atomicity; they are the standard solution, and they are
built.

## No journal — the stamp is the resume point

An early draft carried a journal recording which steps had completed. It is unnecessary and it would
be a second copy of state that already exists on disk (D121). A completed step's output **is**
stamped, and `IsReady` already reads that stamp, so "where were we" is answered by the data itself.
A journal would add a second source of truth that can disagree with the directory it describes.

Consequence: **every step must leave a distinguishable stamp**, or it cannot be resumed into and
must be re-run from the last stamped point. That is acceptable and mostly already true — the
framework migrators each stamp their own target last, guarded so a re-run never lowers a higher
stamp. The sealed ladder (`MigrateLibrary`, v0→v3) stamps only at its end, so it is resumable as a
single unit rather than step by step. It is cheap; leave it that way.

## Two levels, and `repopulate` is right at one and wrong at the other

The staged unit is **`sandboxes/` — the tree**, not an individual sandbox, because migrators decide
what to do by *enumerating* that root. A migrator handed one sandbox at a time cannot plan.

- **Outer (tree) promotion — `repopulate` is correct and wanted.** `Build(dst)` writes only the
  sandboxes that actually migrate; `repopulate` carries every untouched sandbox over *by name* from
  the displaced original. Untouched sandboxes are never copied into scratch and the promoted tree is
  still complete. That is the bounded cost model, already implemented.
- **Inner (per-sandbox) promotion — `repopulate` must be disabled.** The tier move rewrites the whole
  top-level namespace: the new sandbox holds `host/`, `ro/`, `rw/` and none of the flat names. So
  `repopulate` would copy every flat entry straight back to the root and silently undo the move.
  This needs an explicit opt-out on `Promotion` — *"Build produced the complete unit; carry nothing
  over"* — and it is the one change to the primitive itself.

**A sandbox needing several migrations stays in scratch for all of them.** The inner ladder runs
view → view inside scratch (`sbx-v4` → `sbx-v5` → `sbx-v6`), each view stamped as it completes and
the previous deleted once the next is stamped. Peak disk is two views per in-flight sandbox. Only
the final view is placed into the tree being built, and the tree promotes once. A crash leaves a
stamped intermediate view; the stamp says where to resume.

## Preconditions: a second axis, unioned over the *planned* set

`Auth` answers "what must the user permit". It cannot express "the sandboxes must be stopped first",
which is not a policy to waive but an action the framework must take. That needs a second axis on
`Op` — `Requires` — carrying preconditions such as `RequiresStoppedSandboxes`.

The rule that makes it cheap: **union the requirements across the plans `CollectPlans` returns, and
satisfy them once, before step 1.** If any step in the planned set needs stopped sandboxes, stop
them up front. If the data is already at v6 and only steps 7 and 8 are planned, and neither needs
it, nothing is stopped. This falls out of `CollectPlans` for free, because a migrator with nothing to
do plans no ops.

Precedent for the stop itself already exists and is accepted: `OverlayFlatten.flattenRunning` swaps
the dir and then stops the container, because "its overlay mount now points at swapped-away dirs".
The tier move changes *every* mount path, so it will stop every running sandbox — that needs a
`BREAKING-CHANGES` line, not a new mechanism.

## Lockstep laddering (forward compatibility, bought cheap)

Every migrator today is a per-sandbox loop over an enumerated root; none needs a cross-sandbox
invariant. So laddering each sandbox independently to the target *happens* to work right now.

It will not always. A future migration needing a global property — uniqueness across renamed
instances, say — breaks silently under per-sandbox laddering. **Define the ladder as global: every
sandbox reaches vN before any reaches vN+1.** It costs nothing today and keeps the option open.

## Open questions

- **Sealed ladder vs framework, under staging.** `MigrateLibrary` (v0→v3) runs in-process on the
  config package and writes in place today; the framework migrators (v3→v4, v4→v5) use plan/apply.
  Staging has to cover both, and the sealed ladder's two deliberately-flat readers
  (`schema.go:287`, `:325`) are correct exactly because they run before any tier move. Decide whether
  the sealed ladder runs against the staged root or stays in place ahead of staging.
- **Where the tier move sits in the ladder.** `SchemaTiered = 6` as a framework migrator is the
  natural reading, and it is the step that flips the layout for everything after it.
- **Whether `migrate_agentcfg` needs era-pinned writers.** Under this model it writes v3 shape,
  which is flat, so `SaveAt`-style variants of `SaveEnvironment` / `agentcfg.Save` /
  `netpolicycfg.Save` are likely needed. Confirm when the step is written.

## Related

- [sandbox-share-tiering.md](sandbox-share-tiering.md) — the consumer; its step 4 is the v5→v6
  `TierLayout` migration and cannot land without this.
- [DF164](../findings-unresolved.md) — the defect that forced the design, including why the
  migrators' own tests could not catch it.
- [release-migration.md](release-migration.md) — the release-time migration story; different scope.

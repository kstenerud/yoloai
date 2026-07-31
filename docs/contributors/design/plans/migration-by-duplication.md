> **ABOUTME:** Migrate by duplicating the sandboxes tree into scratch, transforming it there,
> verifying it, and swapping it in — so a failed migration always leaves a tree the old
> binary reads.

# Migration by duplication: copy the tree, verify it, promote once

- **Status:** PLANNED — designed with the owner 2026-07-31; no code yet. Every claim about existing
  machinery below was checked against the source, and the parts that already exist are marked as
  such so nobody rebuilds them.
- **Depends on:** —
- **Rides:** **a migration** — it *is* the migration mechanism change, and
  [sandbox-share-tiering.md](sandbox-share-tiering.md) cannot finish without it
  ([DF164](../findings-unresolved.md)).

## Why

Today each migrator promotes its own units as it goes, so a failure partway leaves the live realm
with some sandboxes migrated and some not. The binary that produced that state refuses to run
(the realm stamp is still low), and the *older* binary refuses too, because the sandboxes it can
see are in a layout it has never heard of. **If the failure is deterministic rather than transient,
the user is stuck in both directions.** That, not "units at mixed versions", is the problem worth
building for — it is the owner's framing and it is what settled this design.

The other half is [DF164](../findings-unresolved.md): every pre-v6 migrator addresses the sandbox
through the *live* path builders, so moving a file into a tier silently repoints them at the layout
they are migrating *to*. That is a separate, mechanical defect (see § Era-pinned addressing) and no
amount of staging fixes it.

## The mechanism

Copy the whole `sandboxes/` tree into scratch with the transform applied, verify it, swap it in,
dispose of the old one. Nothing cleverer.

1. **Plan.** Enumerate and classify; check every precondition (§ The plan phase carries the risk).
   Mutate nothing. This is where a migration is supposed to fail.
2. **Build.** Write the complete transformed tree into scratch. The live tree is read-only
   throughout, so any failure here discards scratch and leaves the realm byte-identical.
3. **Verify.** Load every sandbox's records from the *staged* tree, plus a structural check
   (§ Verification runs before the commit). A failure discards scratch and refuses.
4. **Promote.** `Promotion` swaps the tree in and stamps the realm last (D110).
5. **Dispose.** The displaced tree goes to `trash/`, with its size reported.

The design constraints that produced this shape, from the owner: migration is a **rare** event, so
it may be heavy; free space is **pre-checkable**, so exhaustion is a plan-phase refusal rather than
a mid-apply failure; **Windows must stay possible**, so the mechanism uses only mkdir/copy/rename/
remove; and precisely *because* it runs so rarely in production, the machinery must be **small** —
rarely-executed cleverness is where bugs hide.

**The resource allowance is what buys the simplicity, and the clearest example is `repopulate`.**
Because `Build` produces a *complete* tree, `repopulate`'s structural filter
(`entries(orig) \ entries(newer)`, `promote.go:335`) is empty by construction — both trees carry the
same sandbox names. So it degenerates to a no-op and **`Promotion` needs no modification at all**:
its six-state classifier and crash tests stay exactly as they are.

The rule this yields is one sentence, with no qualifiers and one code path for every migration
present and future: *migration duplicates the sandboxes tree; it pre-checks 2× free space and
refuses before touching anything.*

## What the constraints deleted

This file previously described a **staged ladder**: each migration step running view → view inside
scratch (`sbx-v4` → `sbx-v5` → `sbx-v6`), each intermediate view stamped, with the stamp doubling as
a resume point. That is retracted, and the reasons are worth keeping because each one removes
machinery:

- **Resumable scratch — retracted, and it contradicted the code it was built on.** `scratch.go:11-19`
  states scratch is *never* resumed, and `driver.go:47-51` disposes it before and after every run.
  The owner's objection is why that invariant exists: staging leaves the live dir usable, so a
  resumed build promotes a tree derived from a pre-failure snapshot and silently discards whatever
  the user did in between. And resuming at all requires knowing the staged tree's era, which means
  versioning scratch itself — a migration for the migration's temp dir. Never-resume dissolves the
  question: scratch has no format contract, only one process's private working state.
- **The staged ladder — unnecessary.** Its purpose was to keep intermediate shapes private, but
  nothing reads them: the whole-home lock plus the startup gate (D61) already guarantee that.
  Migrators keep running in place.
- **Lockstep laddering (every sandbox reaches vN before any reaches vN+1) — retracted as YAGNI.**
  It was forward-compatibility for a migration nobody has proposed, in the subsystem the owner
  specifically wants kept small.
- **A `Requires` axis on `Op` — unnecessary.** `OverlayFlatten` already stops containers inside
  `Apply` with no second approval axis, and the union-over-the-planned-set optimization is moot once
  the tier move is a single final step.
- **The `repopulate` opt-out — unnecessary**, as above.
- **Per-sandbox quarantine — dropped.** All-or-nothing means all-or-nothing: any per-sandbox failure
  fails the run and discards scratch. No partial-success bookkeeping.

One correction to a factual claim the previous version made: it said the framework migrators are
"parameterised on the root, so pointing them at a staged root is configuration, not surgery". They
are not. Both carry a `config.Layout` *as well as* `sandboxesRoot`, and the Layout is the dominant
addressing path (`migrate_principal.go:84,160,227,285,312`; `migrate_overlay.go:83,229,304,369,443,537`),
with `layout.SchemaVersionPath()` for the stamp. The design above never needs a staged root, so the
claim is moot rather than load-bearing — but it was wrong, and it was the premise of the ladder.

## What already exists — do not rebuild it

| Piece | Where | Note |
| --- | --- | --- |
| Atomic promote + crash recovery | `internal/migrate/promote.go` | `U` / `U_^^_orig` / `U_^^_new`, six-state classifier, exhaustive. `^^` is illegal in any sandbox or realm name, so the sentinels can never collide with live data |
| Same-filesystem precondition | `migrate.SameFilesystem` | Scratch must share a filesystem with the live dir or the move-in fails `EXDEV`. Has a hole ([DF165](../findings-unresolved.md)) and is a no-op on Windows ([DF167](../findings-unresolved.md)) |
| Well-known scratch, per home | `migrate.ScratchPath(home)`, `.migration-scratch` | Correctly scoped to the **home**, not the machine — an embedder can pass its own `DataDir`, and `AcquireHomeLock` locks per-home |
| Plan-before-apply over the whole set | `migrate.CollectPlans` + `Authorize` | Collects every migrator's plan first; `ApplyAll` re-derives under the lock and refuses a migrator that became destructive since planning |
| Approval axis | `migrate.Auth` | `AuthNone` / `AuthConfirm` / `AuthAbandonOverlay` / `AuthBlocked` |
| Stamp-as-done-marker | `Promotion.WriteReadyMarker` / `IsReady` | Written last, immediately before the promoting rename (D110) |
| Sandboxes-tree size | `dirSize(layout.SandboxesDir())` | `system.go`, already used by `system disk` — the free-space estimate is free |
| User-facing notes | `migrate.Report.Notes` | The trash warning is a note, not a mechanism |

**A single directory `rename(2)` on one filesystem is atomic.** What is not atomic is the *pair* of
renames needed to swap two directories, which is the entire reason for the three-name scheme.

## The plan phase carries the risk

This design, and every alternative considered, has a window in which the tree is in a shape no
released binary understands — here it is the commit sequence, during which `sandboxes/` does not
exist under its own name. No design removes that window. What decides whether a user gets stuck is
whether a failure inside it is *deterministic*, and the only place to settle that is the plan phase.

So the plan phase must be as close to total as it can be made, and this is where the effort goes:

- Every root entry in every sandbox is classifiable (the mover is total: unrecognized entries default
  to `host/`, loudly — see [sandbox-share-tiering.md](sandbox-share-tiering.md)).
- Every sandbox dir is writable by the invoking user (`hostUnmanageableReason` is the precedent —
  `migrate_overlay.go:225`).
- No sandbox is running: a live instance's mounts point into the tree being replaced. `Apply` stops
  them, as `OverlayFlatten` already does.
- Nothing is bind-mounted inside a sandbox dir (a `rename` over a mount point gives `EBUSY`).
- Free space ≥ 2× the sandboxes tree, plus margin.
- The live dir and scratch share a filesystem (`SameFilesystem`).

`refuseIfBlocked` (`internal/cli/system/migrate.go:85`) and `AuthBlocked` are the existing shape for
"refuse before any mutation"; this extends it rather than adding a mechanism.

**Free space needs one new primitive.** There is no portable stdlib call: `Statfs` on unix,
`GetDiskFreeSpaceEx` on Windows. Same three-file platform split as
`preflight_dev_{linux,unix,windows}.go`, which is the pattern to copy. The requirement is a flat 2×
during build (live + scratch) and again after commit (new + displaced orig), so `TrashDisposer`
costs nothing at peak.

## Verification runs before the commit

All four per-sandbox record loaders take a `sandboxDir` string rather than a `Layout` —
`store.LoadEnvironment` (`store/environment.go:303`), `store.LoadSandboxState`
(`store/sandbox_state.go:42`), `agentcfg.Load` (`agentcfg.go:51`), `netpolicycfg.Load`
(`netpolicycfg.go:54`). So they can be pointed straight at the staged tree while the live one is
still untouched, which turns verification from "detect the damage and revert" into "refuse and
discard scratch". It needs no revert path to work, and it happens to cover exactly the `host/`
tier — the records whose relocation is the risky part.

Two constraints on what may be called, both load-bearing:

- **No runtime construction, ever.** The tempting "load the sandbox" call builds a `Runtime` lazily.
  A Linux host legitimately migrates tart and seatbelt sandboxes it cannot run —
  `config.LaunchPrefixResolver`'s docstring commits to that and forbids probing for a backend binary
  for exactly this reason. Verification is strictly host-side record loading; `status.DetectStatus`
  and everything below it are out. Get this wrong and the check passes on the developer's Mac and
  fails every Linux upgrade.
- **Readers must not mutate**, or verification modifies the thing it is verifying and can mask a
  defect by repairing it. All four loaders are pure reads today; that is now a written rule rather
  than a coincidence — [standards/go.md § Readers do not mutate](../../standards/go.md).

The loaders cover the records. Nothing covers `bin/`, `logs/`, `work/` and friends, so a structural
check runs alongside: every source entry present at its classified tier path, nothing left
unclassified, counts match. Records get the semantic check; everything else gets the counting one.

## Disposal, and telling the user what it cost

`TrashDisposer` keeps the displaced tree as a one-generation backup (crash-safe-migration decision
3). At tree scale that is the user's entire sandboxes directory, so **the size is reported, above a
50 MB floor** — quiet for a couple of small sandboxes, loud for the user who is about to hold 40 GB
they did not ask for.

Report it in `--check`/`--dry-run` as well as after the fact. The plan phase already computes both
numbers for the free-space precondition, and *"this needs 40 GB free and will leave 40 GB in
`trash/` until you clear it"* is considerably more useful before committing than after.

## Era-pinned addressing (DF164) — required, and independent of all the above

Every migrator below v6 reads a sandbox written **flat** but addresses it through the live builders,
which now resolve into `host/`. They are broken on this branch **today**, whatever the tier move
ends up being built out of. The fix is mechanical: pin each pre-v6 migrator to literal flat paths
for reads *and* writes. `migrate_agentcfg.go` needs all four — the read at `:64` plus
`agentcfg.Save` (`:88`), `netpolicycfg.Save` (`:93`) and `store.SaveEnvironment` (`:108`), which is
why a migrator that reads flat and writes tiered would re-migrate forever.

Prefer one path-taking writer inside `store` that today's `SaveEnvironment` also calls, over
`SaveAt` variants bolted onto three packages' surfaces, and keep the v3-era literals in one place so
exactly one file says "this is what v3 looked like".

**The durable output is not the pin, it is the rule:** *a migrator never addresses through the live
path builders.* Every layout move regenerates this defect otherwise. It graduates into `standards/`
with the test shape DF164 taught — fixtures seeded with literal paths, never with the builder the
migrator reads with, because a fixture built by the same builder cannot fail when the layout moves.

## What this does not cover

**Staging protects the directory, not the world.** `PrincipalRename` performs live backend
operations — `r.Rename` (`migrate_principal.go:183`) and `rt.Remove` (`:203`) against real
containers and VMs. For a user coming from v4, a later failure leaves an untouched directory beside
already-renamed instances, so "the old binary still works" is true only of the disk half. Filed as
[DF166](../findings-unresolved.md). The tier move itself has no external side effects beyond
requiring stopped sandboxes, so the guarantee is clean for the v5→v6 path this plan exists to serve.

## Questions this design answers

The three open questions the previous version carried are settled, all in the same direction:

- **The sealed ladder stays in place, unstaged.** `MigrateLibrary` returns early at
  `current >= libraryFrozenVersion` (`schema.go:210`), so it only ever runs on data at v<3 — which
  is pre-tier and therefore flat by definition. Its two literal readers (`schema.go:287,325`) are
  correct *permanently*; the tier move cannot reach them. It is not crash-safe, but it is
  idempotent, which is the bargain already in force.
- **The tier move is `SchemaTiered = 6`, and it runs last**, so every migrator below it sees flat
  data. The alternative — a version-independent pre-pass — would have to answer "is this already
  tiered?" by sniffing directory shape, against this project's stated rule of a plain-int stamp and
  no artifact-sniffing (`schema.go` ABOUTME, D61).
- **Era-pinned writers are needed** — confirmed against the source, three of them, not "likely".

## Related

- [sandbox-share-tiering.md](sandbox-share-tiering.md) — the consumer; its step 4 is the v5→v6
  `TierLayout` migration and cannot land without this.
- [DF164](../findings-unresolved.md) — the defect that forced the design, including why the
  migrators' own tests could not catch it.
- [DF165](../findings-unresolved.md), [DF166](../findings-unresolved.md),
  [DF167](../findings-unresolved.md) — defects found auditing this plan, all outliving it.
- [standards/go.md § Readers do not mutate](../../standards/go.md) — the invariant the pre-commit
  verification depends on.
- [release-migration.md](release-migration.md) — the release-time migration story; different scope.

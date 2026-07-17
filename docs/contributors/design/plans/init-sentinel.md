> **ABOUTME:** Proposes marking TOP "initializing" while the two realms are built, so a crashed
> first run is a recorded fact rather than a guess — which is what lets the gate say the right
> thing, and lets a retry be safe. Resolves DF128.

# Plan: mark an in-progress TOP init, so a failed one is a fact and not an inference

- **Status:** IN-PROGRESS — P1 (record and diagnose) implemented 2026-07-17: `TOP/.initializing`
  written/removed in `initFreshDataDir`, and all three sites taught it — `dirAbsentOrEmpty` counts a
  sentinel-only TOP as fresh, `checkDataDirStatus`'s exactly-one-Fresh branch resumes an interrupted
  build (and stays loud without a sentinel), and `MigrateCLI`'s switch recognizes it above its
  `default`. P2 (emptiness-gated destroy-and-retry) remains undone and is not scheduled.
- **Correction to §"The trap", learned in build:** the three sites are necessary but not sufficient.
  A sentinel must **never** let the gate reach a realm at `LayoutMigrate`: `initFreshDataDir` ends in
  `CreateFreshLibrary`, which stamps the current version unconditionally, so rebuilding on a stale
  sentinel silently certifies data no migration ever converted (v4→v5 `PrincipalRename` skipped;
  instances stay `yoloai-<name>`). That inverts D110's truth invariant and is worse than the
  destruction §"The danger" warns about, because it is silent. The plan's own "retry" wording for the
  exactly-one-Fresh branch reads as unconditional and would corrupt exactly this way — `resumableInit`
  therefore also requires the surviving realm to be `LayoutOK`. **The sentinel records that a build
  started; it never vouches for what the realms contain.**
- **Depends on:** —
- **Rides:** **any** release — nothing here is breaking or schema-touching. In v0.9.0's scope on value, not necessity; droppable if the cut is tight.

## The one-sentence version

`initFreshDataDir` builds two realms in sequence and records nothing about being mid-build, so a
crash between them leaves a state the startup gate has to *guess* at — and it guesses wrong, calling
a routine interrupted first run *"this should not happen — inspect the directory manually"*
([DF128](../findings-unresolved.md)). Write a sentinel first, remove it last, and the guess becomes a
lookup.

## Why a sentinel and not a better message

DF128's disposition says the fix is to make the gate more helpful. That is the *symptom*. The cause
is that **the gate cannot distinguish two states that matter, because nothing on disk distinguishes
them**:

- `TOP/cli` present, `TOP/library` absent, because a first run was interrupted — benign, nothing of
  value exists, and the right answer is "retry".
- `TOP/cli` present, `TOP/library` absent, because the realm holding every sandbox **went missing** —
  alarming, and the right answer is to stay loud.

Both are "exactly one realm Fresh". No message can be right for both, which is why the current one is
wrong for the common one. This is `research/llm-shaped-repos.md`'s *"absence has no representation"*
in its on-disk form: the fact that an init was underway is knowable only while it is happening, and
nobody wrote it down.

**It is the dual of the migration stamp, and that symmetry is the argument.** The framework's stamp
is written **last**, so it can never precede the data it certifies (D110's truth invariant). The
sentinel is written **first**, so an interrupted build is detectable. Between them they bracket the
operation: *in progress* and *complete* are both recorded, and every other combination is a
diagnosis rather than a shrug.

## The design

**`TOP/.initializing`**, alongside the realms it brackets. Owned by `cliutil`, not `config`, for the
same reason the flat→namespaced relocation is: it describes the directory *above* the library's
root, and the library is rooted at and confined to its own DataDir — it cannot and must not speak
about TOP.

Sequence in `initFreshDataDir` (`internal/cli/gate.go`):

1. `MkdirAll(TOP)`
2. write `TOP/.initializing` **durably** (see open question 1)
3. `CreateFreshCLI()` — `TOP/cli` + stamp
4. `sys.CreateDataDir()` — `TOP/library` + stamp
5. remove `TOP/.initializing`

### The state table this buys

| TOP | sentinel | Means | Action |
| --- | --- | --- | --- |
| absent / empty | — | fresh install | init |
| only the sentinel | yes | died before either realm | retry init |
| sentinel + partial realms | yes | died mid-build | retry init |
| sentinel + both realms OK | yes | built, but the final remove failed | remove sentinel, proceed |
| both realms OK | no | healthy | proceed |
| **cli only** | **no** | init COMPLETED, then the library realm vanished | **stay loud** — this is the one DF128 wanted to warn about, and now it is the only thing that reaches the warning |
| **library only** | no | an embedder rooted at `TOP/library`; the CLI never ran | adopt it (`MigrateCLI`'s stamp-only branch already does) |
| flat `config.yaml`, no `library/` | no | pre-D60 install | the existing v0 relocation |

The two rows in bold are what DF128 says are indistinguishable today. They stop being so.

## The trap: a bare sentinel wedges the CLI

**Verified by reading, 2026-07-17 — do not skip this.** `dirAbsentOrEmpty` is `len(entries) == 0`,
so a TOP containing *only* `.initializing` is **not empty**. Today that routes: gate → not empty →
`checkDataDirStatus` → both realms Fresh → `MigrationRequired` ("a v0 flat install") → user runs
`system migrate` → `MigrateCLI`'s switch matches no case (not flat v0: no `config.yaml`; no
`library/`; TOP not empty) → **default: `"cannot migrate: not a recognized yoloai data directory"`**.

A sentinel added without teaching these would take the exact state it exists to make recoverable and
make it *unrecoverable*. Three places must learn it, and they are the whole change:

1. `internal/cli/gate.go` — `dirAbsentOrEmpty`'s callers: a TOP whose only entry is the sentinel is
   "fresh" (or better: an explicit third answer, "initializing").
2. `internal/cli/cliutil/clischema.go` — `MigrateCLI`'s switch needs the sentinel case above its
   `default`.
3. `internal/cli/gate.go` — `checkDataDirStatus`'s exactly-one-Fresh branch: with a sentinel it is a
   retry; without, it is DF128's genuine anomaly and keeps the loud message.

## The danger, and the constraint that follows

**Never destroy on the sentinel alone.** The failure mode: the final remove fails once, the user
works for a week, and a later run reads a stale sentinel as "init failed, safe to wipe" — destroying
real sandboxes. The sentinel says *an init started*; it does not say *nothing of value exists*.

So "destroy and retry" must be conditioned on the realms actually being skeletal — no sandboxes, no
profiles, nothing but the stamps — which is cheap to check and is the difference between a safe
retry and a data-loss bug. **If that check is not worth building, the retry is not worth building:
prefer the conservative repair (create what is missing, as `MigrateCLI` already does) and let the
sentinel's whole contribution be that the gate finally says something true.** That is already most
of the value; the destroy is the optional half.

Recommended split, so the risky half is separable:

- **P1 — record and diagnose.** Sentinel written/removed; the three sites taught; DF128's message
  becomes correct in both directions. No destructive path. This is the whole win, and it is safe.
- **P2 — retry.** Only if wanted: an emptiness precondition, then rebuild. Gated on P1 landing.

## Tests

Crash points are enumerable, so test each by constructing the on-disk state directly (no fault
injection needed — that is the point of a fact on disk):

- sentinel only → gate initializes rather than refusing; `system migrate` does not say "not a
  recognized yoloai data directory" (the wedge, pinned).
- sentinel + `cli/` only → same.
- sentinel + both realms → sentinel removed, CLI proceeds.
- **no sentinel + `cli/` only → still loud.** The DF128 anomaly must survive; a fix that silences it
  everywhere has thrown away the signal instead of sharpening it.
- no sentinel + `library/` only → adopted (existing `MigrateCLI` behaviour, unchanged).
- Existing `TestGate_*` cases must still pass unchanged — they encode the non-sentinel routing.

## Open questions

1. **Durability. Answered 2026-07-17: already solved, nothing to add.** `fileutil.AtomicWriteFile`
   (`internal/fileutil/durable.go`) writes the temp file, fsyncs its contents, renames it over the
   target, then calls `FsyncDir` on the parent — the same `F_FULLFSYNC`-on-darwin path D110 already
   fought for the migration framework's own directory-entry durability. The sentinel write
   (`cliutil.MarkInitializing`) is a plain `AtomicWriteFile` call and needs no bespoke fsync code.
2. **Content.** Empty file, or a timestamp/pid? A timestamp lets a message distinguish "an install
   started 2 seconds ago" (another process, maybe) from "3 months ago" (definitely dead), and costs
   one line. Nothing needs it yet — YAGNI says empty, and the timestamp is trivially addable later
   since nothing parses it.
3. **Concurrency.** Two `yoloai` processes racing to init the same TOP is unhandled today and stays
   unhandled; the sentinel is not a lock and must not be mistaken for one. Worth a sentence in the
   code saying so, since the next reader will assume otherwise.
4. **Does the library realm want the same bracket?** An embedder calling `CreateFreshLibrary`
   directly has the same interrupted-build exposure, one level down. Out of scope here; note it if
   the shape proves out.

## References

[DF128](../findings-unresolved.md) (the message this makes correct, and its verified reachability);
D60/D61 (the two-realm split, and why TOP is the CLI's to describe); D110 (the crash-safe migration
framework, whose stamp-written-last this mirrors); `internal/cli/gate.go`
(`initFreshDataDir`, `checkDataDirStatus`, `dirAbsentOrEmpty`);
`internal/cli/cliutil/clischema.go` (`MigrateCLI`'s switch, the third site);
`research/llm-shaped-repos.md` ("absence has no representation").

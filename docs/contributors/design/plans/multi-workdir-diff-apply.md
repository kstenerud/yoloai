# Multi-workdir diff/apply

**Status:** Implemented on the `multi-workdir` branch (all 4 phases). Records
[D81](../../decisions/working-notes.md#d81). Revives the multi-dir diff/apply capability
that [Q-U](../../decisions/working-notes.md) (2026-05-25) removed, with the cleaner CLI
surface Q-U explicitly deferred to "real demand".

## Problem

A user wants one agent working across **two project directories at once** (e.g. a
library and its consumer, or a frontend and a backend), with the copy/diff/apply
workflow on **both**. Today only the single positional **workdir** is `:copy`-tracked
and diffable/appliable. Auxiliary (`-d`) dirs support only `:rw` (live, no review) or
`:ro` (read-only). So a second project must be mounted `:rw` — losing the protect-the-
original, review-before-landing guarantee that is yoloAI's core differentiator — or
`:ro` and edited indirectly through the files area. Both are poor.

This is not a hypothetical: a real user hit it. Under Q-U's own recorded principle
("cut barely-used features in beta; restore with a cleaner API if real use shows up"),
this is the trigger to restore it.

## Background — why it was removed, and what changed

Q-U did not find multi-dir diff/apply conceptually wrong or technically infeasible — the
feature was **built and tested** before being cut. It was cut because the *projected*
public API (per-dir status matrices, per-dir filter flags, cross-dir conflict
resolution, `DiffResult`/`PerDirApplyResult`/`SkippedDir`/`SkipReason` types) was far
more elaborate than the implementation, which only ever did all-or-nothing apply with no
per-dir filtering. "We were projecting sophistication onto a rarely-exercised feature."

The machinery was collapsed, not deleted in spirit:

- `internal/sandbox/patch/diff.go` — `LoadAllDiffContexts()` still **returns a slice**;
  it just yields ≤1 entry now. The comment says the slice shape was kept so overlay-loop
  callers wouldn't need rewriting.
- `internal/sandbox/patch/apply.go` — `ApplyAll()` keeps its name; "the iteration is
  gone." The multi-dir loop lived in the deleted `GenerateMultiPatch()`.

So restoring is largely **un-collapsing** these loops behind a deliberately small CLI
surface — not rebuilding from scratch.

## The key insight that keeps the UX clean

The mess Q-U feared comes entirely from **precise, partial, cross-dir operations**. We
sidestep it with one rule:

> **Bulk operations span all tracked dirs trivially; precise operations require naming
> exactly one dir — which collapses them to the existing single-dir code path.**

- A full `diff` of a dir, or a full `apply` of a dir to its origin, needs no cross-dir
  reasoning — it is N independent single-dir operations. `git diff` already groups
  multiple files under headers without a "status matrix"; we group multiple dirs the same
  way.
- Anything that names a commit range (`<ref>`) or a `-- pathspec` is inherently about
  **one** dir. We *require* a single-dir specifier for those, so they reduce to today's
  exact single-dir implementation. No cross-dir conflict resolution, no per-dir filter
  matrix, ever.

That rule is the whole design. Everything below is its mechanical consequence.

## Locked decisions

Confirmed with the user before writing:

1. **A specifier is required only when 2+ tracked dirs exist.** With a single tracked dir
   (today's common case) bare `diff`/`apply` are unchanged — no muscle-memory regression.
2. **"Tracked" = `:copy` or `:overlay`.** `:rw`/`:ro` aux dirs are reference mounts and
   never count toward the specifier requirement.
3. **`diff <name> <spec>` must resolve to exactly one tracked dir.** Read ops are
   one-at-a-time.
4. **`apply <name> <spec>` resolves to one dir; `apply <name> --all` applies every tracked
   dir** to its own origin. No glob — the realistic sets are "one" (name it) or "all".
5. **Declaration reuses `-d <path>:copy`** (re-enable `:copy`/`:overlay` on the aux-dir
   flag). The positional workdir is simply the first tracked dir; no new `--track` flag.

## CLI surface

### Declaration (`yoloai new`)

Re-enable the `:copy` and `:overlay` suffixes on `-d`. The aux-dir rejection added by Q-U
in `internal/cli/cliutil/dirspec.go` (`ParseAuxDirArg`) is removed.

```
yoloai new proj /home/karl/api -d /home/karl/web:copy
```

The set of **tracked dirs** is `{workdir} ∪ {aux dirs whose mode is :copy or :overlay}`.
`:rw`/`:ro` aux dirs are unchanged.

### Specifier resolution — a CLI-edge concern

Specifier resolution lives **entirely at the CLI edge** and never enters the library. The
fuzzy human-friendly fragment is resolved **once**, at parse time, into the **canonical
dir identity** (the exact host path) before any library call — consistent with
parse-don't-validate-at-edges: the edge resolves implicit/ambient input, the library core
is a pure accessor that demands a full specification (see [Layering](#layering)).

The CLI fetches the tracked-dir set (`Sandbox.TrackedDirs()`), then resolves the fragment
against it, taking the first rule that produces a **unique** match:

1. exact host path
2. exact mount path
3. basename of the host path
4. segment-aligned suffix of the host path (`b/web` matches `/home/karl/b/web`)

- **No match** → CLI error listing the tracked dirs.
- **Multiple matches** → CLI error listing the candidates; the user types more path to
  disambiguate (mirrors `git`'s ambiguous-ref behaviour and tab-completion ergonomics).

A specifier is a path fragment, **not a new label namespace** — nothing new to store,
collide on, or keep in sync. The library is handed the resolved exact host path and does
no matching of its own.

### `yoloai diff`

```
yoloai diff <name> [<spec> | --all] [<ref>...] [-- <path>...]
```

- 1 tracked dir → `<spec>`/`--all` optional; behaves exactly as today.
- 2+ tracked dirs → exactly one of `<spec>` or `--all` is **required**. Omitting both errors
  with the tracked-dir list.
- `<ref>` / `-- <path>` are single-dir by nature and require a `<spec>` (rejected with
  `--all`, same as `apply`).

```
$ yoloai diff proj            # 2 tracked dirs
error: sandbox 'proj' tracks 2 dirs — specify one (or --all):
  api   /home/karl/api
  web   /home/karl/web

$ yoloai diff proj web
  M  app.tsx
```

**`--all` is a panoramic, view-only diff.** Like `apply --all` it is a CLI concern: the
CLI expands `--all` into the tracked-dir set, calls the per-dir library `Diff` once per dir
(each with its **absolute host path as the git src/dst prefix**), and concatenates.
Because the prefix is git-native (`--src-prefix`/`--dst-prefix`), paths come out absolute
and self-describing while `/dev/null`, renames, and binary hunks are still rendered
correctly — no post-processing of diff text.

```
$ yoloai diff proj --all
diff --git /home/karl/api/server.go /home/karl/api/server.go
...
diff --git /home/karl/web/app.tsx /home/karl/web/app.tsx
...
```

Because the paths are absolute, a `--all` diff **is** applyable across roots — but with
`patch(1)`, not git. Verified incantation: `cd / && patch -p1 < all.diff` (the `-p1`
strips the leading `/`, relativising the path so `patch`'s "dangerous absolute path" guard
doesn't trip; `patch -p0` refuses absolute paths outright). `git apply`/`yoloai apply`
**cannot** consume it — they are single-repo-root tools. And the `patch` route covers only
text edits/adds/deletes: `patch` ignores git's extended headers, so **renames-without-
content, binary hunks, and mode changes are silently dropped**. So the cross-root `patch`
round-trip is a real escape hatch (transport changes to another checkout, apply to a
non-yoloai tree), but **`apply --all` is the supported way to land** — it runs git per dir
and preserves renames/binaries/modes plus commit replay. For an applyable *single-dir*
git patch, `diff <spec>` keeps native `a/`…`b/` prefixes exactly as today.

`--name-only --all` lists every changed file across dirs (absolute paths); `--stat --all`
keeps git's repo-relative paths under a per-dir `=== <host-path> ===` banner (git ignores
the prefix flags for `--stat`).

### `yoloai apply`

```
yoloai apply <name> [<spec> | --all] [<ref>...] [-- <path>...]
```

- 1 tracked dir → `<spec>` optional; behaves as today.
- 2+ tracked dirs → exactly one of `<spec>` or `--all` is **required**.
- **`--all` is bulk-only.** Combining it with `<ref>` or `-- <path>` is an error — those
  are single-dir operations. This restriction is what keeps `--all` out of cross-dir
  conflict territory.

**`--all` is a CLI concern** — it never reaches the library as a mode. The CLI expands
`--all` into the explicit tracked-dir set (`Sandbox.TrackedDirs()`), then **loops**,
calling the ordinary per-dir library apply once per dir, and aggregates the results into
the summary. The library has no "apply everything" verb.

**Semantics — independent, per-dir, fully reported.** N tracked dirs are N separate git
repos at N separate origins; cross-dir transactionality is impossible and we do not
pretend otherwise. Each dir applies atomically *to itself*; one dir's failure does not
roll back another. The CLI prints one summary line per dir:

```
$ yoloai apply proj --all
api:  applied 2 commits  -> /home/karl/api
web:  FAILED (merge conflict in app.tsx) -> /home/karl/web   # api's apply still stands
```

A partial failure yields a non-zero exit but is explicit about what landed.

### Sibling commands

The same specifier rule extends to the other workdir-targeting verbs:

- **`yoloai reset <name> [<spec>]`** — resets one tracked dir's copy + baseline. `--all`
  for resetting every tracked dir is a reasonable extension but **out of scope for v1**
  (reset is rarer and destructive; require an explicit single dir for now).
- **`yoloai apply --patches <dir>` (export)** — single-dir; takes `<spec>` like `apply`.
- **`yoloai sandbox <name> info`** — already lists workdir + dirs with modes; no change
  beyond aux dirs now possibly showing `copy`/`overlay`.

## Data model

The singular `store.Environment.Workdir` field and the separate `Directories` slice
**collapse into one ordered list**, `Dirs []DirEnvironment`, where **element 0 is the
workdir** (the agent's cwd; "the workdir" for all user docs and UI). Mode is a per-entry
attribute; **"tracked" is the derived predicate** `Mode ∈ {copy, overlay}`. A `:rw`
workdir is simply `Dirs[0]` with `Mode == rw`. Reference mounts (`:rw`/`:ro`) live in the
same list at positions ≥1 — there is no second list and no "which list does this go in"
bookkeeping.

This is a **breaking schema + public-API change**, taken deliberately in beta (D81) rather
than bolting peer-dirs onto a singular field later. Rationale: the singular `Workdir`
field is load-bearing in ~25 files; every later multi-dir feature would have to special-
case "workdir vs the others". One ordered list removes that seam permanently.

`internal/sandbox/store/environment.go`:

- `WorkdirEnvironment` and `DirEnvironment` unify into one element type carrying
  `HostPath`, `MountPath`, `Mode`, `BaselineSHA`, and `InceptionSHA` (the last was
  workdir-only; every tracked dir now needs it for the baseline-advance / apply-then-
  continue lifecycle). `Environment.Workdir` + `Environment.Directories` → `Environment.Dirs`.
- **Schema migration v1 → v2** via the existing read-time `migrate()` (the same mechanism
  that does v0→v1; this is the per-sandbox `environment.json` version, distinct from the
  realm-level `system migrate`). The legacy `workdir` + `directories` JSON keys still
  unmarshal (into deprecated shadow fields); `migrate()` repacks them as
  `Dirs = [workdir, directories...]` and bumps `Version` to 2. Lossless, deterministic.
- The host copy already lives per-path at `work/<encoded-host-path>/` via
  `store.WorkDir(sandboxDir, hostPath)`, so multiple `:copy` dirs coexist with no path
  collision — no change needed there.

Public read-model (`environment.go`): `Environment.Dirs []DirInfo` with `Workdir()` (=
`Dirs[0]`) and `TrackedDirs()` (filtered to copy/overlay) accessors; `HasOverlayDirs()`
iterates `Dirs`.

Create-time wiring (`internal/sandbox/create/prepare_dirs.go`): the copy + baseline path
the workdir takes today (`setupWorkdirDirs` / `createWorkdirBaseline`) generalises to run
for **every** tracked entry, not just `Dirs[0]`.

## Layering

The CLI/library split is the heart of this design's response to Q-U:

- **CLI (the edge) owns all the human-friendly resolution.** Fuzzy specifier → canonical
  host path. `--all` → an explicit set of host paths. Ambiguity/"did you mean" errors.
  Per-dir result aggregation and summary rendering. Exit-code policy on partial failure.
- **Library (the core) is a pure accessor that demands a full specification.** It takes an
  **exact** dir identity (host path), does no fuzzy matching, has no `--all` mode, and
  returns one dir's result per call. Nothing implicit, no lazy fallback.

This is parse-don't-validate-at-edges applied to dir selection: resolve the ambiguous
input once, at the edge, into a value the core can trust.

## Library API

There is **no bulk type and no bulk verb** — that is the Q-U deletion taken to its
conclusion. The CLI loops over per-dir calls and aggregates the existing single-dir
`*ApplyResult`s itself.

- **Primary handle:** `Sandbox.Workdir() *Workdir` — the primary workdir, unchanged for
  all existing callers.
- **Addressed handle:** `Sandbox.TrackedDir(hostPath string) (*Workdir, error)` — returns a
  handle bound to the tracked dir at the **exact** host path; errors if no `:copy`/
  `:overlay` dir matches. No fragment matching. (`Workdir()` is sugar for the primary's
  host path.) The `*Workdir` handle carries the bound dir's identity internally;
  `Diff`/`Apply`/`Export`/`Commits` signatures are **unchanged** and operate on the bound
  dir, returning the same single-dir types as today. The one additive change:
  `DiffOptions` gains a `PathPrefix string` field that, when set, is passed to git as
  `--src-prefix`/`--dst-prefix` — the CLI sets it to the dir's absolute host path when
  rendering `diff --all`, and leaves it empty (native `a/`…`b/`) otherwise. Still a
  full-spec input, not fuzzy resolution.
- **Enumeration:** `Sandbox.TrackedDirs() []TrackedDirInfo` (flat: host path, mount path,
  mode) — the CLI uses it to resolve specifiers, expand `--all`, and render `info`.

Internally, un-collapse the loops that were preserved for exactly this:
`LoadAllDiffContexts()` includes `:copy`/`:overlay` aux dirs again; the patch-layer
helpers iterate the contexts again. Most of the diff/apply core is unchanged because it
was always written to take a `DiffContext`/host-path, not a hardcoded singular workdir.

## Edge cases & errors

- **No specifier, 2+ tracked dirs:** error listing the tracked dirs (shown above).
- **Ambiguous specifier:** error listing the matching candidates.
- **`--all` with `<ref>` or `-- pathspec`:** rejected for both `diff` and `apply`
  (bulk-only).
- **`diff --all` applyability:** absolute paths make it applyable across roots via
  `cd / && patch -p1` (text edits/adds/deletes only — `patch` drops git renames/binaries/
  modes; `patch -p0` and `git apply` both refuse it). `apply --all` is the supported,
  full-fidelity way to land; `diff <spec>` is the applyable single-dir git patch.
- **`apply --all` partial failure:** non-zero exit, per-dir summary, successful dirs stay
  applied.
- **`:rw`/`:overlay` interaction:** `:rw` dirs never need apply (live); they are excluded
  from `--all` and from the specifier set. `:overlay` tracked dirs run their git ops
  inside the container as today and require the container running — `--all` mixing `:copy`
  (host-side git) and `:overlay` (in-container git) dirs is fine; each uses its own path.
- **Dirty-repo refusal** (`--allow-dirty`) applies per tracked dir at create time, as it
  does for the workdir today.

## Out of scope (v1)

- Glob/subset specifiers for `apply` (`'web*'`). `--all` + single-dir cover the realistic
  cases; add only on demand.
- `reset --all`.
- Cross-dir transactional apply. Structurally impossible across independent origins;
  explicitly not offered.

## Implementation phases

The unified-list reshape lands first as a behavior-preserving refactor, then the multi-dir
behaviors layer on. Each phase is one commit (or a small set), green under `make check`.

1. **Data model + migration (no behavior change).** Collapse `Workdir`/`Directories` into
   `Dirs []DirEnvironment` (element 0 = workdir), unify the element type (+`InceptionSHA`),
   add `migrate()` v1→v2, rewire every `meta.Workdir.*` consumer to `Dirs[0]` and every
   `Directories` consumer to `Dirs[1:]`, update the public read-model + the ~20 test files.
   Still effectively one tracked dir; behavior identical.
2. **Re-enable multi-`:copy` creation.** Drop `ParseAuxDirArg`'s `:copy`/`:overlay`
   rejection; give each tracked aux entry the same copy + baseline setup as `Dirs[0]`.
3. **CLI specifier + diff/apply over N dirs.** The CLI-edge specifier resolver
   (fragment → exact host path), `Sandbox.TrackedDir(hostPath)`, the require-a-specifier
   rule when 2+ tracked dirs; un-collapse `LoadAllDiffContexts`/patch-layer iteration.
4. DONE: **`--all`.** `diff --all` (absolute-path prefix, concatenated) and `apply --all` (CLI
   loop, per-dir summary, partial-failure exit code).

## Open questions

- **Specifier on `reset`/export now or later.** Wiring the shared CLI resolver into `diff`/
  `apply` first, then `reset`/export in the same pass, is cheap; sequencing only.

> **ABOUTME:** One owner for "produce or refresh a work copy and its baseline", so create and
> the three reset paths stop re-deriving the (mode × backend-locality) matrix independently —
> which is the single root behind the DF116/117/118/120/121 cluster.

# Plan: one owner for work-copy materialization

- **Status:** IMPLEMENTED (2026-07-16). All four materialization sites — create, reset `--restart`
  (workdir + aux), and reset in-place — now go through `workcopy.Materialize`, whose only
  behavioural parameter is the `Strategy` enum (`WipeAndCopy` / `InPlaceAndPrune`), as the research
  predicted: no per-caller boolean appeared. Stage 1 extracted the coordinator (create,
  `prepare_dirs.go` −53); stage 2 moved reset `--restart` onto it and erased divergence (a); stage 3
  moved reset in-place onto `InPlaceAndPrune` and fixed DF123 (the in-place path now loops aux dirs).
  Each stage merged on its own with a red-green guard. This plan is now archaeology and moves to
  `archive/plans/` in the same change.
- **Depends on:** —

## The problem, stated as a pattern rather than a bug

Five findings this cycle were the same finding wearing different clothes:

| Finding | The site that got a cell wrong |
| --- | --- |
| DF116 | `:copy-all` copy kept a worktree's `.git` link → committed to the user's real repo |
| DF117 | reset re-copied with no gitignore filter → re-imported the secrets `:copy` excludes |
| DF118 | reset synced `.git` entry-by-entry → merged two repos, dangling ref |
| DF120 | reset baselined at HEAD where create commits the dirty tree → diff blamed the user's work on the agent |
| DF121 | `:rw` recorded a baseline it does not track → `info` printed what `baseline` denied |

None is an isolated defect. Each is one call site's copy of "materialize a work copy" disagreeing
with another's. The operation is real and coherent — *given a directory in some **mode**, on a
backend of some **locality**, produce (or refresh) the sandbox's work copy and establish its diff
baseline* — but it is **open-coded at every caller**, so each caller re-derives the whole
`mode × locality` cross-product inline, and each is a fresh chance to miss a cell.

This is why fixing one surfaces three: the fixes are consolidations, and consolidating makes the
next divergence legible. DF120's `baseline.WorkCopy` collapsed four baseline sites into one and
*immediately* exposed DF122 by putting the ordering in one readable place instead of four. That is
the strangler pattern working as designed — and the argument for finishing the job rather than
stopping at the baseline slice.

## What this is NOT

Scope discipline is most of the value here; an unbounded "workdir interface" is the tar pit.

- **Not ENV isolation.** None of the cluster came from there. The axis that bites is
  materialization × backend locality. Building an abstraction around ENV would fix the wrong thing.
- **Not the consume side.** `diff.go`, `tags.go`, `mounts.go`, `export.go`, `apply.go` all branch
  on mode too (~21 files do), but they *read* a materialized work copy; they do not build one, and
  they have not produced this bug class, because reading is idempotent and their mode→behaviour map
  is flat. Unifying all 21 mode-branch sites is the over-reach to avoid. If the consume side ever
  earns its own consolidation it is a separate plan, justified by its own findings.
- **Not a new polymorphic interface.** The polymorphism that exists — host-side vs in-VM — is
  already captured by `runtime.Backend` / `runtime.WorkDirSetup`. What is missing is a *coordinator*
  that consumes those, not another interface layered over open-coded logic.

## The toolbox (already on `main`)

The sub-decisions are already factored into reusable pieces — this cycle built most of them. The
gap is purely that no single owner sequences them.

- `workspace.ProjectFileSet` / `CopyProjectDir` — which files (mode: gitignore vs all), + artifact strips
- `workspace.RemoveGitLink` — sever a worktree/submodule gitlink (DF116)
- `workspace.PreserveGit` — keep or strip source `.git` (mode + confinement)
- `workspace.PruneToFileSet` — drop what an in-place refresh must not keep (DF117)
- `baseline.WorkCopy` — establish the baseline: commit dirty / adopt HEAD / fresh (DF120)
- `runtime.WorkDirSetup` + `LocalityOf` — the host-vs-VM seam

## Verified against the code, 2026-07-16 — the API does not grow

The risk with any consolidation is that the callers turn out to need subtly different things, so
the "one" function grows a boolean per caller and the chaos is moved, not removed. So before
committing to the shape, every materialization site was read in full and diffed. There are exactly
**five**: create-workdir and create-aux (`prepare_dirs.go` `setupDirContent`+`createCopyBaseline`),
reset-restart-workdir and reset-restart-aux (`reset.go` `resetCopyWorkdir`, `resetAuxCopyDir`), and
reset-in-place-workdir (`resyncWorkCopy`). No others — `migrate`/overlay do not materialize.

**What actually varies across the five, and where it goes:**

| Apparent knob | Real? | Resolution — *not* a per-caller bool |
| --- | --- | --- |
| wipe vs copy-in-place | **yes** | one `Strategy` enum. `WipeAndCopy` = create **and** reset-restart (a `RemoveAll(dst)` is a no-op on create's absent dst, so they unify); `InPlaceAndPrune` = reset-in-place. Orthogonal to mode and locality — not per-caller. |
| prune the destination | no | *inherent* to `InPlaceAndPrune` (you prune only because you did not wipe). Not a separate flag. |
| defer baseline for SandboxSide | no | the coordinator **always** does the `LocalityOf` check. This *fixes* divergence (a): `resetAuxCopyDir` omits it today. |
| emit history-dropped warning | no | a **return value** (`HistoryNotice`), not a param. Create surfaces it; reset ignores it today (and could surface it too, uniformly). This is the one that would have been a `bool emitWarnings` — avoided. |
| input is `DirSpec` vs `DirEnvironment` | trivial | both carry `{path, mode, IncludeIgnored, StripHistory}`; a 4-field `Spec` with a 2-line adapter at each call site. |
| build a `DirEnvironment` / set `InceptionSHA` | out of scope | metadata packing stays in the caller. The coordinator returns a SHA, nothing else. |
| aux vs workdir | no | aux is just another dir; materialization is identical. The only aux/workdir differences (`DirEnvironment` assembly, the caller's loop) are caller-side. |

**Verdict:** the coordinator's only behavioural parameter is the `Strategy` enum, which is a genuine
physical axis (a live bind-mount cannot be `RemoveAll`'d), shared across callers rather than one-per.
Everything else either unifies, moves to a return, or stays in the caller as a thin adapter. The
callers get *shorter and dumber* — the kill-criterion passes. And the exercise found real drift the
consolidation erases: divergence (a) above, plus [DF123](../findings-unresolved.md) (in-place reset
skips aux dirs entirely — a live bug, filed separately because it is a missing caller *loop*, not a
materialization defect, so the coordinator alone does not fix it).

## The proposal

One entry point:

```
Materialize(ctx, spec Spec, dst string, strategy Strategy, g, backend) (baselineSHA string, notice HistoryNotice, err error)
//   Spec       = { Src string; Mode DirMode; IncludeIgnored, StripHistory bool }   — adapted from DirSpec or DirEnvironment
//   Strategy   = WipeAndCopy | InPlaceAndPrune
//   baselineSHA = "" means deferred to the VM (SandboxSide); notice carries "history not preserved" + reason, or none
```

that owns the sequence and the two axes, and that every caller invokes instead of re-deriving it:

1. **Locality first** (the ordering create had and reset did not, uniformly): a SandboxSide backend
   stages, then defers baseline into the VM and returns the empty signal. HostSide continues.
2. **Files**: `ProjectFileSet` / `CopyProjectDir` per `dir.Mode`.
3. **`.git`**: sever links always (DF116); preserve/strip per `PreserveGit`.
4. **Baseline**: `baseline.WorkCopy` (DF120), or defer per step 1.

**The one real axis besides mode/locality is the refresh strategy**, and the coordinator must name
it rather than let each caller imply it:

- **wipe-and-copy** — `create` and `reset --restart`: the destination can be removed and rebuilt
  from scratch (`RemoveAll` + `CopyProjectDir`); no prune needed.
- **copy-in-place-and-prune** — `reset` in-place: the destination is bind-mounted into a *live*
  container, so it must be overwritten in place and then pruned to the file set (`PruneToFileSet`),
  never removed. This is the constraint that DF117's rsync existed to satisfy and that the fix now
  satisfies with plain in-place copy.

So `strategy ∈ {WipeAndCopy, InPlaceAndPrune}`, orthogonal to mode and locality. Three axes, one
owner, instead of three axes re-derived at five sites.

## Migration — strangler, each stage a checkpoint

Never a big-bang rewrite; the safety comes from each stage being independently green and guarded by
an equivalence test.

1. **Extract** `Materialize` from `create`'s `setupDirContent` + `createCopyBaseline`, which is
   already the most correct site. Land it with `create` as its only caller. No behaviour change.
2. **Move `reset --restart`** (`resetCopyWorkdir`, `resetAuxCopyDir`) onto it with `WipeAndCopy`.
   Guard: a test that a reset-restart work copy is byte-identical to a fresh create of the same
   source+mode (the `TestProjectFileSet_MatchesCopyProjectDir` idea, one level up).
3. **Move `reset` in-place** (`resyncWorkCopy`) onto it with `InPlaceAndPrune`, **and add the aux
   loop `resetInPlace` is missing** so it resets aux `:copy` dirs like the restart path — fixing
   DF123 in the same stage, since the loop body is now one `Materialize` call. Guard: same
   equivalence assertion, the DF117/DF120 property tests, plus a test that an in-place reset
   refreshes an aux dir (the DF123 repro, inverted).
4. **Delete** the now-empty per-site variants. Success metric: the locality/mode/strategy branch
   count in `reset.go` + `prepare_dirs.go` drops to the single `Materialize` body, and
   `grep LocalitySandboxSide internal/orchestrator` no longer returns four independent sites.

Each stage merges on its own. If stage 3 turns out gnarlier than expected, stages 1–2 still stand
and still removed real duplication.

## Risks / open questions

- **The aux-dir path** — *resolved by the code read.* `resetAuxCopyDir` and `setupAuxDir`'s copy
  branch are the same materialization as the workdir with a different destination; they fold into
  `Materialize` with no variant. Two things surfaced doing this: divergence (a), the missing aux
  baseline-deferral, which the coordinator erases by construction; and DF123, in-place reset not
  looping aux dirs at all, which the coordinator does **not** fix on its own — stage 3 must also add
  the aux loop to `resetInPlace`, so DF123 rides in with it rather than as a separate change.
- **Over-abstraction** — *checked, passes* (see "Verified against the code"). One `Strategy` enum,
  no per-caller bool. If a later change adds one, it has failed and should stop; the callers must
  keep getting shorter.
- **The consume side stays split**, deliberately (see "What this is NOT"). Someone will be tempted
  to pull `diff`/`tags`/`mounts` in "while we're here". Resist without a finding to justify it.

## How we will know it worked

The bug class stops recurring: a new mode or backend is added in one place, not five, and the
equivalence test fails loudly if create and reset ever diverge again — which is the thing that,
today, only a user running `yoloai reset` on the wrong repo finds out.

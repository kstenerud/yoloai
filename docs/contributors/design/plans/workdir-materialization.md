> **ABOUTME:** One owner for "produce or refresh a work copy and its baseline", so create and
> the three reset paths stop re-deriving the (mode × backend-locality) matrix independently —
> which is the single root behind the DF116/117/118/120/121 cluster.

# Plan: one owner for work-copy materialization

- **Status:** PLANNED — designed here, not built. The pieces it sequences already exist and are
  on `main` (see "The toolbox"); what is missing is the coordinator that sequences them once.
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

## The proposal

One entry point, roughly:

```
Materialize(ctx, dir DirSpec, dst string, backend, strategy) (baselineSHA string, err error)
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
3. **Move `reset` in-place** (`resyncWorkCopy`) onto it with `InPlaceAndPrune`. Guard: same
   equivalence assertion, plus the DF117/DF120 property tests already written.
4. **Delete** the now-empty per-site variants. Success metric: the locality/mode/strategy branch
   count in `reset.go` + `prepare_dirs.go` drops to the single `Materialize` body, and
   `grep LocalitySandboxSide internal/orchestrator` no longer returns four independent sites.

Each stage merges on its own. If stage 3 turns out gnarlier than expected, stages 1–2 still stand
and still removed real duplication.

## Risks / open questions

- **The aux-dir path** (`resetAuxCopyDir`, `setupAuxDirs`) is a fourth near-copy; confirm it folds
  into the same entry point rather than needing a variant. It probably does — it is `:copy` with a
  different destination — but it is unverified.
- **Over-abstraction.** If `Materialize` grows a parameter per caller, it has failed: that is the
  open-coded matrix with extra steps. The test is whether callers get *shorter and dumber*. If they
  do not, stop.
- **The consume side stays split**, deliberately (see "What this is NOT"). Someone will be tempted
  to pull `diff`/`tags`/`mounts` in "while we're here". Resist without a finding to justify it.

## How we will know it worked

The bug class stops recurring: a new mode or backend is added in one place, not five, and the
equivalence test fails loudly if create and reset ever diverge again — which is the thing that,
today, only a user running `yoloai reset` on the wrong repo finds out.

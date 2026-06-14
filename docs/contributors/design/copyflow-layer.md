# Copyflow layer — the copy / diff / apply review refinement

**Status:** Design converged 2026-06-14 (design conversation), not yet implemented. The target
surface for the **copyflow** refinement of [plans/public-layering.md](plans/public-layering.md) —
shaped as-if-public behind `internal/`. A *consumer* of the substrate
([substrate-interface.md](substrate-interface.md), D84); persistence per D85. Decision: D86.

**One-line definition.** Copyflow is the **review workflow**: protect the original, let a work
copy diverge, diff it, land it selectively — yoloAI's core differentiator. Git is the mechanism;
*protect / review / land* is the essence. It is a refinement of *transfer*, not part of the
substrate.

## The model (the decisions behind the surface)

1. **It spans two places, not "inside the substrate."** Copyflow mediates between the **work copy**
   (at/in the substrate) and the **original** (always a host dir). `Diff` reads the work copy;
   `Apply` writes the original. So it consumes the substrate's exec/transfer *and* touches the host
   original directly.

2. **The per-tracked-dir handle is the atom.** A handle bound to one tracked dir (working name
   `TrackedDir`) replaces today's procedural free-functions that thread `(layout, rt, name,
   dirHostPath)` through ~50 calls. Uniform verbs — `Diff` / `Apply` / `AdvanceBaseline` / `Export`
   / `History` — hold their context the way `Substrate` holds its identity.

3. **Seeding vs propagation (the unifying decomposition).** *Seeding* = "get the files in there",
   decided by the **dir mode**: `copy` copies wholesale; `overlay` *lies* (the upper layer makes
   them appear); `rw` is a live mount (changes go straight to the original — **no copyflow**); `ro`
   has nothing to land (**no copyflow**). So the dir mode just decides *whether a divergeable work
   copy exists*; copyflow attaches only to `copy`/`overlay`. *Change propagation* is then **uniform**:
   produce a diff + metadata on the work side, replay it on the original side. Only the **transport**
   varies — same filesystem (HostSide) → hand it to git locally; cross-fs (SandboxSide) →
   intermediaries extract the diff out and replay it host-side.

4. **Resolution is repo-aware.** What a dir produces/applies depends on its nature: a **repo** dir
   (the original is a git repo) carries the agent's **commit series + tags** at the repo's own
   cadence, replayed onto *its own* original repo (history preserved); a **non-repo** dir collapses
   to **bare changes**. The "metadata" in propagation *is* commits + tags, and it exists only for
   repo dirs (non-repo = the degenerate, empty-metadata case).

5. **`--all` is a collection of independent resolutions — never a merge.** Two tracked repos are two
   distinct commit DAGs; flattening them into one patch is incoherent (loses tags, and can't express
   two histories). So each handle's resolution stays whole and lands on *its own* original:
   `apply --all` lands each dir separately (already how D81's apply behaves); `diff --all` is a
   **per-dir breakdown** for review ("A: these commits/tags · B: these commits/tags · C: bare
   changes"), not one blob. The single combined absolute-path patch is **demoted** to a *lossy,
   opt-in export view* ("flatten everything to bare changes") — never the default, never the
   resolution path. A handle *can* emit its diff host-rooted (it knows its own root) so the caller's
   merge for that export is trivial concatenation — but cross-dir merge/landing strategy is caller
   policy, never pushed into copyflow.

6. **Characterize-and-surface for nature mismatch.** "There are commits" means **commits beyond the
   baseline** (`ListCommitsBeyondBaseline`) — real history the agent authored, not copyflow's own
   bookkeeping commit. If commits-beyond-baseline exist but the original is **non-repo**, copyflow
   returns a **typed, informative signal** ("N commits + M tags would be flattened; the original
   isn't a repo") instead of proceeding. The user picks the remedy: **(a)** apply flat, accept the
   loss; **(b)** abort, make the original a repo, re-resolve preserving history. Copyflow **never**
   silently flattens history, and **never** auto-converts a bare original into a repo (option (c) —
   "copy the repo out" — silently changes the original's nature; it's a *user* choice, not a default).
   Invariant: *copyflow never silently destroys metadata and never silently changes the original's
   nature.* It fits the review-before-landing essence — `Diff`/preview shows the mismatch alongside
   the changes; `Apply` acts only on the user's decision.

7. **The hermetic-git security seal (load-bearing).** The git *inside* the sandbox is **untrusted**
   (compromised binary, compromised repo state). It is **read + emit only — it never writes anything
   outside the sandbox.** Changes leave *only* as diff + metadata, via a **read-only egress**
   (`Substrate.Exec` stdout, or a sandbox-temp file + `GetPath`); the original is never mounted in for
   it to touch. The **trusted host-side git** is what applies, path-confined (`--unsafe-paths` /
   `--directory` guards). Two independent layers of distrust: the **binary** (sealed by never letting
   in-sandbox git write out) and the **diff content** (agent-authored → confined by host-side
   `git apply` guards). This is *why* "where git runs" matters — it is a security boundary, not an
   optimization.

8. **Copyflow owns its persistence (the D85 instance).** The baseline + baseline-log are copyflow's
   state and leave `store.Environment` for a **copyflow-owned record** (e.g. `copyflow.json`) via the
   shared persistence helper (foundation; see public-layering / tomorrow's work). CAS-guarded
   baseline mutation + locking stay copyflow's concern.

9. **Substrate consumption.** Copyflow uses `Substrate.Exec` (run produce-side git, capture diff —
   the read-only egress), `Substrate.PutPath` (seed the copy at create), and **host-side git**
   (apply). `FilesystemLocality` is **injected** into the produce-side git executor (host-fs git vs
   in-sandbox exec), so the review verbs never branch on locality. Whether an op "needs the box
   running" is *derivable* from locality (SandboxSide + stopped → can't produce a diff); the
   **auto-start-vs-error** remedy is caller (CLI) policy, not copyflow's.

## Surface sketch (shape, not final signatures)

```go
// per tracked dir; bound at construction to: original (host) + work location +
// baseline store + the dir's nature + the injected produce-side git executor.
type TrackedDir interface {
    Diff(ctx, DiffOptions) (Resolution, error)   // characterized: repo → commits+tags; non-repo → bare
    Apply(ctx, ApplyOptions) (ApplyResult, error)// lands onto THIS dir's original; mismatch → typed signal
    AdvanceBaseline(ctx, expectedSHA string) (*BaselineChange, error) // CAS-guarded
    Export(ctx, destDir string) error
    History(ctx) ([]Commit, error)
}

type Resolution struct {                          // what would land
    Repo     bool
    Commits  []Commit                              // empty for non-repo / no commits-beyond-baseline
    Tags     []Tag
    Bare     []byte                                // patch, for non-repo or the flat view
    Mismatch *NatureMismatch                       // set when commits exist but the original can't receive them
}
type DiffOptions struct { Paths PathStyle /* DirRelative | HostRooted */ ; /* pathspec, refs, … */ }
```

Multi-dir is the **caller** holding N `TrackedDir`s and *collecting* (render N breakdowns / iterate
N applies) — never merging.

## Deliberately NOT in copyflow

- **`--all` merge into one patch** → caller (and only as a lossy opt-in export).
- **Auto-start a stopped sandbox** → caller policy (copyflow surfaces "needs the box running").
- **Auto-convert a bare original into a repo** → user choice, never a default.
- **Cross-dir landing strategy / `-p` level / dedup** → caller.

## Open / verify items

- **Verify the hermetic seal in the current code** (DF35): copy-mode `apply.go` uses `git.NewSandbox`
  on several paths — confirm none has an *in-sandbox* git writing to a host-mounted original. If any
  does, it's a security finding, not just a refactor.
- **Handle name** — `TrackedDir` vs `Workspace` vs `Review`. TBD.
- **The config/persistence helper** (foundation, generalizes D85) — copyflow's baseline record rides
  on it; its home/name is the next session's first task.
- Drop copyflow's apparent `internal/runtime/docker` import (no symbol usage found — likely indirect).

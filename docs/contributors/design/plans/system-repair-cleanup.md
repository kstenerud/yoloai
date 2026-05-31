# Plan: System repair / cruft cleanup

**Goal:** make yoloai self-heal cruft, and give users one read verb (`doctor`) +
safe cleanup (`system prune`) so they can repair their system and reclaim space
without losing recoverable data.

**Core invariant:** the bulk path (prune) can only remove zero-stakes +
rebuildable items. Anything that might hold user data is refused-and-reported
or quarantined (trash), never silently deleted.

**Mental model:** see → `doctor`; clean invisible → `system prune` (safe;
`--cache` for space); remove visible → `destroy`.

## Recoverability classes (the axis that matters — not "broken vs not")

| Class | Examples | Action |
|---|---|---|
| Zero-stakes | stale locks, temp dirs, orphaned backend resources, never-init dirs (no `work/`) | delete (safe tier) |
| Rebuildable | image/build/VM cache | delete under `--cache` |
| Data, detectable | copy dirty tree / commits; overlay w/ container | refuse + report (→ diff/destroy) |
| Data, ambiguous | corrupt / version-too-new meta, no detectable data | quarantine to trash |

Overlay recoverability tracks the **container**, not meta: container exists ⇒
recoverable (start + review) ⇒ precious; no container ⇒ changes already gone ⇒
scaffolding.

## Phase 0 — Foundations
- `internal/config/layout.go` (near `SandboxLockPath`): add `TrashDir()` → `DataDir/trash/`.
- `docs/BREAKING-CHANGES.md`: `yoloai system doctor` → `yoloai doctor` (beta; old path removed).
- Accept: builds; `TrashDir()` returns expected path.

## Phase 1 — Meta-independent recoverability probe (foundation)
- `internal/sandbox/inspect.go`: `ProbeWorkData(sandboxDir) (hasData bool, detail string)`, no meta required:
  - copy: glob `work/*`; reuse `detectChanges`. Dirty → data. `work/` present but clean/non-git → ambiguous (caller trashes).
  - no `work/` → no data (never-init, e.g. the 71).
- `internal/sandbox/lifecycle.go` `NeedsConfirmation` + `sandbox.go` `HasActiveWork`: on `LoadMeta` failure, fall back to `ProbeWorkData` + overlay container check instead of returning false.
- Accept: broken-dirty → active; never-init → none; overlay-with-container → precious. `destroy` on broken-but-dirty now prompts.

## Phase 2 — Self-heal normal paths
- `internal/sandbox/lifecycle.go` `destroy()`: after `forceRemoveAll(sandboxDir)`, remove `layout.SandboxLockPath(name)`.
- `internal/sandbox/create.go`: audit rollback paths; every pre-meta failure removes dir + lock. Investigate why `home/`-only dirs survived (backend-setup failure path).
- Accept: destroyed sandbox leaves no `.lock`; forced-fail `Create` leaves neither dir nor lock.

## Phase 3 — `system prune`: completeness + classification + trash + tiers
- `system_client.go`:
  - `scanSandboxes`: 3-way classify via `LoadMeta` error kind + `ProbeWorkData`:
    - known (meta loads) → untouched.
    - uninitialized (`errors.Is(os.ErrNotExist)`) + no data → delete.
    - broken/unreadable WITH data, or overlay-with-container → refuse + report.
    - corrupt / version-too-new, no data → quarantine to trash (DEFAULT — decided).
  - stale-lock sweep: `.lock` whose `<name>` dir gone and not flock-held (try-acquire).
  - `Prune()`: extend `PruneResult` with `RemovedLocks`, `Trashed`, `RefusedDataBearing`, `TrashContents`. `--cache` = image/build/VM cache only (decoupled from trash).
- `internal/cli/system/prune.go` (keep thin):
  - default: safe tier; print refused-data-bearing with fix commands.
  - trash non-empty → prompt "Trash holds N items (Z MB) that may contain data you wanted — delete it? [y/N]". No → keep trash. Non-TTY → keep + report. `--yes` → delete, no prompt.
- Accept: per-branch tests; trash prompt yes/no/--yes/non-TTY; lock sweep skips held. On this machine: clears 71 dirs + 965 locks, reports data-bearing, leaves real sandboxes.

## Phase 4 — Promote + extend `doctor`
- Move registration from `system.NewCmd` to top-level `doctor` in `internal/cli/commands.go`; remove from `system`.
- Extend read-only report (delegate, no deletes): reclaimable-now (locks/temp/never-init → prune), reclaimable-space (cache sizes → prune --cache), holds-unreviewed-work (ProbeWorkData → diff/destroy), trash (count/size/mv hint).
- Accept: `yoloai doctor` runs; `system doctor` gone; `--json` includes new sections.

## Phase 5 — Docs
- ARCHITECTURE.md (trichotomy + probe), GUIDE.md (repair workflow + trash restore), working-notes.md (D-entry), backend-idiosyncrasies.md (lock-file removal note).

**Sequencing:** 0 → 1 → 2 → 3 → 4 → 5. Phase 1 gates 2/3/4. Each phase ends with `make check`.

**Constraints:** keep the CLI layer thin (logic in library: `system_client.go`,
`internal/sandbox/`); follow docs/dev principles + standards (GO.md, CLI.md).

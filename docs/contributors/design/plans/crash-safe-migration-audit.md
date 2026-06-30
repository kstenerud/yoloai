# Audit: crash-safe migration (footguns, ratchets, corruption)

ABOUTME: Independent adversarial audit of the crash-safe-migration design — four
ABOUTME: parallel reviewers (corruption/ratchets/footguns) + in-code verification.

Status: **Audit complete 2026-06-30.** Companion to
[crash-safe-migration.md](crash-safe-migration.md). Four independent reviewers read
the committed plan cold (corruption & data-loss, ratchets & irreversibility,
operational footguns) plus a cadence reviewer; the four load-bearing claims were
then re-verified against the actual code by the maintainer (marked **[verified]**).
Dispositions reference the reworked plan's spine (discrete per-realm migrations,
per-sandbox one-at-a-time, staging workdir + resumable atomic rename, per-sandbox
version as truth).

**Disposition key:** *dissolved* = removed by the reworked spine · *still-live* =
real, must be handled in the build · *open-decision* = a flagged plan decision.

## Verified-in-code (the foundation)

- **A1 — no durable/atomic write primitive.** *[verified]* Every commit/stamp is bare
  `os.WriteFile` (`fileutil.go:144`). **reframed — no per-file primitive survives.** The
  commit is the dir-rename **swap** (atomic, and it carries the new `.schema-version`
  inside the unit — the version is never flipped separately, every migration changes ≥2
  files). Durability is a **bounded fsync discipline**: fsync built contents before move-in
  (so `_^^_new` is never durable-but-empty), fsync(dir) after each rename, `F_FULLFSYNC` on
  darwin (the price of power-loss safety; process-death alone needs none). C3/A4-class
  cross-file bugs are fixed **structurally for any migration that uses the machinery** (all
  of a unit's changed files commit together in the swap). The existing **v2→v3 split is
  sealed as-is**, does *not* use the machinery, and retains the A4 exposure (mitigated by
  idempotent re-run; retro-hardening is an open call); the machinery's first user is the
  **v3→v4** flatten. Severity CRITICAL.
- **A4/C3 — existing agent.json split is power-loss-lossy.** *[verified]* Values-first
  in program order but **not fsynced** (`migrate_agentcfg.go:87-110`); under power
  loss the slimmed `environment.json` can outrace its `agent.json` sibling. **sealed as-is**
  — the split ships un-hardened; retro-hardening (route through `atomicWriteJSON` +
  stamp-after-pass) is an **open call**, default leave-it per "lock it off as it stands."
  Severity HIGH (power-loss-only, config-not-worktree).
- **DF68 — stamp-before-pass.** *[verified]* `MigrateLibrary` stamps before the
  per-sandbox pass (`schema.go:185` / `system.go:112-119`). **dissolved** by stamp-last
  + per-sandbox-on-disk-form-as-truth + fsync barrier.
- **R1 — downgrade ratchet.** *[verified]* `RealmStatus` hard-errors `version > current`
  (`schema.go:91`); stamp-forward + GC-on-commit = no way back, even for a
  non-migration bug. **DECIDED: no downgrade** (one-way; escape from a persistent
  forward bug = per-sandbox quarantine + original-intact-until-commit; "back up before
  upgrading"). Severity CRITICAL/HIGH (architectural).
- **A5 — lock ≠ quiescence.** *[verified]* `Start` releases the per-sandbox flock once
  the container launches (`start.go:61-65`); a detached agent writes lock-free. **still-live
  — quiesce via `DetectStatus == Stopped`**, not the flock. Severity HIGH.

## CRITICAL

- **A2 — macOS overlay flatten = silent total loss.** *[rests on confirmed DF69]* The
  snapshot/seed reflinks the **empty** host upper; the real data is container-tmpfs
  only; a relaunch wipes it, so resume reads a baseline-only view and commits a
  "successful" empty flatten. **still-live** — the migrator must **require the sandbox
  running** and **refuse a *stopped* macOS overlay sandbox** (destroyed source, not a torn
  read). Reading the live upper into scratch is itself **non-destructive** (the source is
  never modified), so once extracted the overlay sandbox migrates exactly like any other —
  the extract is a single durable act, never re-derived from a relaunched container. The
  copy-and-swap spine does **not** dissolve the *stopped*-source case (nothing on disk to
  copy), but it fully covers the running case.

## HIGH

- **A6 — CLI-realm relocation outside the substrate.** *[verified misclassification in
  `clischema.go`]* `MigrateCLI`'s `os.Rename` layout swap wasn't covered by the
  substrate, and a crashed re-run green-stamps over a half-moved layout. **dissolved** —
  it becomes a discrete realm-structural migration under the same machinery
  (resumable rename, stamp-in-payload).
- **A7 — snapshot durability / completeness / disk / special-files.** Four-way
  convergence: fsync the seed/staging tree + a manifest before trusting it; `statvfs`
  pre-flight (full-copy doubles disk); `io.Copy` **can't copy overlay whiteout
  char-devices** → snapshot fails on the very sandboxes being migrated; exclude the
  nested backup dir. **still-live (reduced)** — per-sandbox staging shrinks the disk
  cost to one sandbox; special-file + preflight handling moves into the machinery's
  seed-copy (one place). Severity HIGH.
- **A8 — write-ahead journal under-specified / torn records.** **dissolved** — replaced
  by per-sandbox version (progress) + the resumable naming convention (commit). No
  separate WAL; rigor moves to the naming state machine (must be exhaustively
  crash-classifiable + crash-injection-tested).
- **A9 — in-place flatten violates "staging, never in place."** *(`WorkDir ==
  OverlayWorkBaseDir`)* **dissolved** — staging is the `migration-<id>` workdir by
  construction; commit is the atomic rename.

## MEDIUM (operational)

- **A10 — gate ordering / migration-running gate.** A running migration tells other
  commands to "run system migrate." **DECIDED: one whole-tree live flock** held for the
  run (every other command refuses); released on death, so no permanent lock. Post-crash
  blocking comes from the live-dir sentinel + stamp gate, not the lock.
- **A11 — network / synced-local FS defeats flock.** Dropbox/iCloud is *local* yet
  flock is meaningless across the sync → two hosts run concurrent migrations.
  **DECIDED: hard-refuse + single-FS** — migration requires the whole realm + scratch on
  one local filesystem (also guarantees atomic `mv`); refusal names the relocate escape.
- **A12 — one corrupt sandbox bricks the whole install** (stamp-last is all-or-nothing).
  **RESOLVED by plan/apply** — the dry-run **plan** surfaces a sandbox that can't migrate as
  a foreseen quarantine; the user approves the whole plan (quarantines included) or aborts,
  one up-front decision. All-or-nothing is the *decision*, not the *execution* (units still
  commit incrementally + resumably). See the plan's Plan/apply section.
- **A13 — realm-step refusal message.** The post-removal binary's v3→v4 default must
  carry the 5-element named-tool refusal, not "no migration registered." **still-live**
  (overlay-removal build). Severity MED.
- **A14 — stepping-stone availability.** Keep the **detector** forever + pin a
  download/checksum for the migration version. **still-live** (migration-version-gating).
- **A15 — recovery observability.** Signpost `--rollback` on repeated failure
  (dpkg-`--audit` style); a general "am I mid-migration / clean?" check in `system
  status` that survives the binary swap. **still-live**.
- **A16 — backend-up pre-flight for the flatten.** The container-bound extract needs
  the backend daemon running + base image present; emit a specific "start <backend>"
  refusal. **still-live** (the v3→v4 flatten).
- **A17 — persistent-vs-transient classification.** Bound forward retries with a
  recorded attempt count; require explicit `--rollback`; never auto-rollback over
  committed sandboxes. **still-live** (was OQ2).
- **A18 — stale backup/workdir GC.** **resolved** — scratch dirs are disposable (tossed
  on every invocation when the lock is free); the transient `*_^^_orig` is deleted at
  the end of each promotion. No retained generation to GC (no downgrade).

## Summary

The spine **dissolves** the most error-prone pieces — A8 (WAL), DF68, A6, A9 — and
**reduces** A7 (per-sandbox staging shrinks disk + quiescence to one sandbox). The
**still-live** core is the durable primitive (A1), the macOS overlay destroyed-source
refusal (A2), quiescence-via-status (A5 — note the whole-tree flock blocks other yoloai
*processes*, but the in-container agent isn't one, so the migrated sandbox still needs
`DetectStatus == Stopped`), and the operational set (A13–A17). The **decisions are now
made** (D110 + this round): 1 quarantine-or-abort, 3 **no downgrade** (one-way), 4
hard-refuse + single-FS, 5 whole-tree live flock, A18 scratch-disposable. R1 is accepted
as one-way — the escape from a persistent forward bug is per-sandbox quarantine, not
rollback.

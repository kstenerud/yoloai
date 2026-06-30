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
  `os.WriteFile` (`fileutil.go:144`); the design's "atomic commit = atomic write of
  the version file" does not exist. **still-live — build first** (plan: durable
  primitive). Severity CRITICAL.
- **A4/C3 — existing agent.json split is power-loss-lossy.** *[verified]* Values-first
  in program order but **not fsynced** (`migrate_agentcfg.go:87-110`); under power
  loss the slimmed `environment.json` can outrace its `agent.json` sibling. **still-live
  — fix in 0.6.0** (same root as A1). Severity HIGH (power-loss-only, config-not-worktree).
- **DF68 — stamp-before-pass.** *[verified]* `MigrateLibrary` stamps before the
  per-sandbox pass (`schema.go:185` / `system.go:112-119`). **dissolved** by stamp-last
  + per-sandbox-version-as-truth + fsync barrier.
- **R1 — downgrade ratchet.** *[verified]* `RealmStatus` hard-errors `version > current`
  (`schema.go:91`); stamp-forward + GC-on-commit = no way back, even for a
  non-migration bug. **open-decision 3** (retain backups + reversible step vs document
  one-way). Severity CRITICAL/HIGH (architectural).
- **A5 — lock ≠ quiescence.** *[verified]* `Start` releases the per-sandbox flock once
  the container launches (`start.go:61-65`); a detached agent writes lock-free. **still-live
  — quiesce via `DetectStatus == Stopped`**, not the flock. Severity HIGH.

## CRITICAL

- **A2 — macOS overlay flatten = silent total loss.** *[rests on confirmed DF69]* The
  snapshot/seed reflinks the **empty** host upper; the real data is container-tmpfs
  only; a relaunch wipes it, so resume reads a baseline-only view and commits a
  "successful" empty flatten. **still-live** — the migrator must **detect and refuse a
  stopped macOS overlay sandbox** (destroyed source, not a torn read); the live
  extract must be a single durable act never re-derived from a relaunched container.
  The copy-and-swap spine does **not** dissolve this (nothing on disk to copy).

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
  commands to "run system migrate." **open-decision 5** — wire a **live-flock**
  try-acquire (not a presence check) ahead of the needs-migration branch.
- **A11 — network / synced-local FS defeats flock.** Dropbox/iCloud is *local* yet
  flock is meaningless across the sync → two hosts run concurrent migrations.
  **open-decision 4** — hard-refuse on detected network FS with a spelled-out escape;
  host+boot-id in the run marker.
- **A12 — one corrupt sandbox bricks the whole install** (stamp-last is all-or-nothing).
  **open-decision 1** — quarantine-and-continue (recommended) vs abort.
- **A13 — realm-step refusal message.** The post-removal binary's v3→v4 default must
  carry the 5-element named-tool refusal, not "no migration registered." **still-live**
  (0.8.0). Severity MED.
- **A14 — stepping-stone availability.** Keep the **detector** forever + pin a
  download/checksum for the migration version. **still-live** (migration-version-gating).
- **A15 — recovery observability.** Signpost `--rollback` on repeated failure
  (dpkg-`--audit` style); a general "am I mid-migration / clean?" check in `system
  status` that survives the binary swap. **still-live**.
- **A16 — backend-up pre-flight for the flatten.** The container-bound extract needs
  the backend daemon running + base image present; emit a specific "start <backend>"
  refusal. **still-live** (0.7.0).
- **A17 — persistent-vs-transient classification.** Bound forward retries with a
  recorded attempt count; require explicit `--rollback`; never auto-rollback over
  committed sandboxes. **still-live** (was OQ2).
- **A18 — stale backup/workdir GC.** Tag `migration-<id>`/`*.old-<id>` with the build
  id; sweep stale ones whose target ≤ current stamp. **still-live**.

## Summary

The spine **dissolves** the most error-prone pieces — A8 (WAL), DF68, A6, A9 — and
**reduces** A7 (per-sandbox staging shrinks disk + quiescence to one sandbox). The
**still-live** core is the durable primitive (A1), the macOS overlay destroyed-source
refusal (A2), quiescence-via-status (A5), and the operational set (A10–A18). The
**open decisions** (1 bad-sandbox, 3 downgrade-ratchet, 4 network-FS, 5 gate-ordering)
are surfaced in the plan for the critique cycle. R1 (downgrade) is the one
architectural ratchet the copy-and-swap model does **not** fix on its own.

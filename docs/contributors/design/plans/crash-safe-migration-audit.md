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
  "successful" empty flatten. **designed (plan/apply); implementation item.** No open design
  question remains — it's two concrete behaviors in the overlay migrator: **(a) `Plan()`**
  requires the sandbox **already running** (both platforms — the binary has no mount code, so
  it can't start a stopped overlay) and surfaces a **stopped** one as a plan choice: go back &
  start it (preserves changes) **or** proceed destructively (abandons the overlay changes —
  macOS already gone per DF69; Linux displaced upper to `trash/`, manually recoverable); never
  a fabricated empty flatten. **(b) `Apply()`** reads the merged view from the running
  container in a **single durable act and never stops/restarts it** (a restart wipes the macOS
  tmpfs and unmounts overlay). Reading is non-destructive; a *running* sandbox on any OS is
  covered. (Note: this updates the earlier "macOS-only" framing — because the binary no longer
  mounts overlay at all, **stopped on Linux is also a refusal/choice**, not an A16 backend-up
  bring-up.)

## HIGH

- **A6 — CLI-realm flat→namespaced relocation isn't crash-resumable.** *[verified in
  `clischema.go`]* `MigrateCLI`'s `os.Rename` layout swap stamps only as its **terminal**
  step (`CreateFreshCLI` after `relocateFlatToNamespaced`). A crash **mid-move** leaves
  `TOP/library` created but the move unfinished; a re-run sees `library/` exists →
  `isFlatV0Install` returns false → it **green-stamps cli as done without finishing the
  move** (`clischema.go:96-104`). **sealed as-is** — this is the existing shipped
  flat→namespaced migration; like the agent.json split it is **not** reworked into the new
  machinery here (the ship has sailed). **Overlay interaction (new):** a half-relocated
  install can strand `:overlay` sandboxes at top-level `sandboxes/`, where the `v3→v4`
  flatten's sandboxes-root fallback (`library/sandboxes/` **else** top-level — it never
  consults *both*) won't look → silently skipped. This is A6's pre-existing exposure
  surfacing through overlay, **not** a new flatten defect: the flatten covers the
  *un-migrated* (look top-level) and *fully-migrated* (look library) cases by design, not the
  *half-relocated* one. Severity HIGH (crash/power-loss-only, rare).
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
- **A13 — realm-step refusal message.** **dissolved for overlay** — the overlay extractor
  is kept forever (decision 8), so any binary can always flatten; there is no
  post-removal-without-reader build and no refusal to author. (The 5-element refusal pattern
  stays as general prior art for any *future* migration that does delete a reader.)
- **A14 — stepping-stone availability.** **dissolved for overlay** — with the reader kept
  forever (decision 8), no intermediate/stepping-stone version is ever needed for overlay and
  there is nothing to pin. (Migration-version-gating stays as general prior art.)
- **A15 — recovery observability.** Signpost `--rollback` on repeated failure
  (dpkg-`--audit` style); a general "am I mid-migration / clean?" check in `system
  status` that survives the binary swap. **still-live**.
- **A16 — folded into A2.** The flatten reads from an **already-running** sandbox via exec
  (decision 8 — the binary no longer brings up a container or needs a base image present), so
  "backend up + sandbox running" is exactly A2's require-running precondition. No separate item.
- **A17 — persistent-vs-transient classification.** Bound forward retries with a
  recorded attempt count; require explicit `--rollback`; never auto-rollback over
  committed sandboxes. **still-live** (was OQ2).
- **A18 — stale backup/workdir GC.** **resolved** — scratch dirs are disposable (tossed
  on every invocation when the lock is free); the transient `*_^^_orig` is deleted at
  the end of each promotion. No retained generation to GC (no downgrade).

## Summary

The spine **dissolves** the most error-prone pieces — A8 (WAL), DF68, A9 — and
**reduces** A7 (per-sandbox staging shrinks disk + quiescence to one sandbox). **A6 is
re-dispositioned to sealed-as-is** (the shipped flat→namespaced relocation is not reworked
here; it keeps its crash-mid-move exposure, which the overlay flatten's `library/`-else-
top-level sandboxes-root fallback inherits as a silent-skip on a *half*-relocated install).
The
**still-live** core is the durable primitive (A1), the **require-running precondition +
stopped-sandbox plan-choice** (A2, both platforms), quiescence-via-status for host-side
migrations (A5 — note the whole-tree flock blocks other yoloai
*processes*, but the in-container agent isn't one, so the migrated sandbox still needs
`DetectStatus == Stopped` for host-side reads; the overlay flatten instead **requires the
sandbox running** and reads via exec), and the operational set (A15 observability, A17
retry-classification). **Dissolved / folded for overlay:** A13/A14 (decision 8 keeps the
read-glue forever — no detect-and-refuse/stepping-stone) and A16 (folded into A2 — the
flatten reads an already-running sandbox, no container bring-up). The **decisions are now
made** (D110 + this round): 1 plan/apply (was quarantine-or-abort), 3 **no downgrade**
(one-way), 4 hard-refuse + single-FS, 5 whole-tree live flock, 8
delete-create/start-keep-read-glue, A18 scratch-disposable. R1 is accepted
as one-way — the escape from a persistent forward bug is per-sandbox quarantine, not
rollback.

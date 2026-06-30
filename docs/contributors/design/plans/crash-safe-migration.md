# Plan: crash-safe data-dir migrations

ABOUTME: Design for making `yoloai system migrate` crash-safe — discrete,
ABOUTME: per-realm, individually-versioned migrations that stage in a workdir and
ABOUTME: commit via a resumable atomic rename; per-sandbox done one at a time.

Status: **DESIGN CONVERGED ON A SPINE — open for further critique; not yet built.**
Surfaced by overlay retirement (D109); tracked as DF68. Designs the overlay→copy flatten
as the **next data-dir migration (`v3→v4`)** and the crash-safe machinery it rides on. The
existing agent.json split (`v2→v3`) is **sealed as-is**. Migrations form a **linear schema
chain, decoupled from release numbers**.
Sequencing is **D110** (the migration chain below). The independent audit behind this
rewrite is the companion [crash-safe-migration-audit.md](crash-safe-migration-audit.md)
(18 findings, A1–A18, four parallel reviewers + in-code verification).

> This rewrite supersedes the first-cut WAL-journal design. The user has further
> critiques pending — three open decisions are flagged at the end; treat the whole
> doc as a live critique target, not a sealed spec.

## The problem (DF68 + audit-confirmed, verified in code)

1. **Stamp-before-pass (DF68).** `MigrateLibrary` calls `WriteSchemaVersion`
   (`schema.go:185`) *before* `MigrateAgentConfigs` runs (`system.go:112-119`). A
   crash between leaves `stamp == current` over half-migrated data — a green gate
   (`RealmStatus → LayoutOK`) lying about the data.
2. **No durable/atomic write primitive (A1).** Every version/mode/stamp write goes
   through `fileutil.WriteFile` → bare `os.WriteFile` (`fileutil.go:144`):
   truncate-in-place, **no temp+rename, no fsync, no dir-fsync, no `F_FULLFSYNC`**.
   The "atomic commit = atomic write of the version file" the design assumes does
   not exist.
3. **The existing agent.json split is already power-loss-lossy (A4/C3).** Its
   writes are values-first in *program order* but **not fsynced**
   (`migrate_agentcfg.go:87-110`), so under power loss the slimmed
   `environment.json` can land before its `agent.json` sibling → config lost. The
   code comment "written durably" is overclaimed.
4. **The overlay→copy flatten** will be the first multi-step *destructive*
   migration; it needs all of the above plus container-bound source handling
   (DF69). See the audit companion for the full finding set.

## Requirements

- **Exclusive** while in flight; **crash-recoverable** at any kill point;
  **never lose data** (≥1 complete consistent copy at every instant; source never
  destroyed before target durably committed); **resumable & idempotent**; a
  persistent forward bug must have an **escape** that doesn't require manual surgery.

## The model (the spine)

A migration **run** = an ordered set of **discrete, per-realm, individually-versioned
migrations**. Each discrete migration builds a staging workdir, transforms its unit,
and commits via a **resumable atomic rename**, leaving the displaced original as a
one-generation backup. On failure it discards the workdir; the original is untouched;
it retries. There is **no separate write-ahead journal** — progress lives in the
per-unit version, and the commit state lives in the filesystem naming convention.

### Staging workdir (disposable, never resumed)

A single well-known scratch dir at the **top of `$YOLOAI_HOME`** (not per-realm, no
build-id — it's never resumed, so identity is irrelevant; a fixed name makes it easy to
find). **Scratch is disposable and never resumed:** any yoloai invocation (when no
migration holds the lock) throws out a leftover scratch dir — a crashed build is garbage,
rebuilt fresh, never recovered. It is also **cleared between chain steps** (v1→v2,
delete, v2→v3, delete, …). So the *build* phase (the complex part — containers, extract)
needs no crash-recovery; **only the live-dir *rename* (promotion) is resumable.** A
scratch dir is **not** a reason to block other functions. Scratch must be on the **same
filesystem** as the live dirs (decision 4) so the move-in is atomic.

### One shape, applied per unit

Every migration uses **one shape — build-new → repopulate → atomic-rename swap** — on a
*unit* (a directory that gets atomically replaced). The data dir carries **one linear
schema version** (the `.schema-version` stamp); migrations form a **chain**
(v2→v3→v4→…), each run against a schema frozen in a prior shipped build. A migration may
**reach into sandboxes** via an idempotent **per-sandbox pass** — exactly how the existing
v2→v3 agent.json split already works (each sandbox inspected and transformed on its own; a
re-run skips ones already done). So a *unit* is the data-dir realm as a whole, **or a
single sandbox** during such a pass — each its own build-new→swap.
Changed files are rebuilt in scratch; unchanged bulk is **moved** (not copied) from the
displaced original; the swap commits **all of the unit's changed files atomically** (the
key property — a migration touching many files needs no per-file ordering; see Promotion).
A crash re-does only the in-progress unit (the rest are already swapped or untouched); a
per-sandbox failure is quarantine-or-abort (decision 1).
**Per-sandbox progress is keyed on observable on-disk state, not a per-sandbox version
file** — a sandbox is "already migrated" when its form shows it (the split:
`environment.json` already slimmed; the flatten: no longer in overlay mode). The single
data-dir `.schema-version` is flipped **last**, only after the per-sandbox pass completes
*and* its commits are durable (the fsync barrier) — so the stamp is physically incapable of
being ahead of the data. During promotion `_^^_orig` keeps the **old** content and `_^^_new`
the **new**; recovery reads the live state to place itself in the chain and resume.

### The truth invariant (the DF68 fix)

For a per-sandbox pass the **per-sandbox on-disk form is the source of truth**; the
single data-dir `.schema-version` stamp is a **cache, flipped last** — only after a scan
confirms every sandbox is at ≥ target (or quarantined), *and* only after those commits are
durable (the fsync barrier). Recovery never trusts the stamp over the sandboxes: it
rescans and migrates stragglers (idempotent). The stamp is **physically incapable** of
being ahead of the data (stamp-last + barrier). This is the proven agent.json-split
pattern (each sandbox inspects its own form), hardened with atomic commit + durable writes.

### Machinery vs migrator (the separation)

- **Generic machinery owns:** the `migration-<build-id>` workdir lifecycle; **seeding**
  the per-sandbox workdir with a faithful reflink/copy of the source (so unchanged
  data survives untouched — correct-by-default, with special-file + disk-preflight
  handling written *once*, not per migrator); the **durable-atomic-rename** primitive;
  the **resumable promotion** (naming convention) and its recovery; scratch disposal +
  transient-`*_^^_orig` cleanup; version-check gating; the **whole-tree run lock**;
  collecting every migrator's **plan** and enforcing the **apply-gate** (proceed only when
  destructive operations have been approved — see [Plan / apply](#plan--apply-dry-run)).
- **Each migrator owns** two methods: **`Plan()`** — a pure read-only inspection that
  describes the operations it would perform (each flagged **destructive** or not, plus any
  **refusals/quarantines** it foresees); and **`Apply()`** — performs the transform inside
  the workdir, **re-validating** its preconditions. It also owns **source consistency** —
  starting/stopping containers, avoiding torn reads, and **detecting and refusing** an
  already-destroyed source (A2) — and *declares* which blessed consistency strategy it uses
  (so the discipline doesn't drift per migrator).

**Bespoke migrators, lean framework — no cathedral, and no mid-run prompts.** Each migrator
is hand-written and bespoke in *what it does* (its transform), but it does **not** prompt or
abort interactively. Interaction is centralized via **plan → confirm → apply**: the
framework gathers all migrators' plans, and the **app** (CLI) renders them, takes the one
confirmation, and only then drives `Apply()`. Migration *policy* (approve / abort, and the
per-sandbox quarantines surfaced in the plan) lives in the app; migration *responsibility*
(the durable mechanics) lives in the framework — deliberately separate, and the
cross-migrator dependencies are **not uniform**, which is fine. The **library never prompts**
(it exposes plan + gated-apply; the app owns interaction). We deliberately did **not** build
a general sub-realm/carve framework around migrations (it proved brittle) — see D110.

## Plan / apply (dry-run)

Every `migrate` run computes a **plan first**, then applies it — the terraform
`plan`/`apply`, `rsync -n`, `apt --dry-run` shape:

1. **Plan (read-only).** Each pending migrator's `Plan()` is collected into one list of
   described **operations**, each flagged **destructive** or not, plus any foreseen
   **refusals/quarantines** ("sandbox Y: stopped macOS overlay — cannot migrate, will
   quarantine"). No mutation. A migrator enumerating realm contents resolves them at their
   **current** physical location, not the post-migration one (see the overlay flatten's
   [sandboxes-root resolution](#source-consistency-migrator-concern-blessed-strategies)), so
   the plan is accurate even before a pending relocation runs.
2. **Confirm (the app).** The CLI renders the plan. If it contains destructive operations
   and a TTY is present, it asks for one confirmation; **`--yes`** proceeds without asking
   (the scripted path). **Headless / no TTY defaults to abort** on any destructive op unless
   `--yes` was given.
3. **Apply.** The framework drives each `Apply()` under the whole-tree lock, **re-deriving
   the plan and re-validating preconditions** — it does *not* execute the printed plan
   blindly, so a precondition that drifted since the plan (e.g. a container that stopped)
   becomes a refusal, never an unreviewed change. The approval carried into apply is a
   **policy flag** ("destructive approved"), not a plan object.

**All-or-nothing applies to the *decision*, not the *execution*.** You approve the whole
plan or abort — no mid-run prompts. Execution stays **incremental and resumable**: each unit
commits via its own atomic swap, so an *unpredicted* apply-time failure stops with
already-committed units done (re-run resumes the rest, or quarantines the failed unit). It is
**not** transactional rollback — no-downgrade (decision 3) forbids un-committing.

`yoloai system migrate --dry-run` (alias `--check`) stops after step 1 and prints the plan
(`--json`-aware) without changing anything — this doubles as the pre-upgrade audit (A15).

## Durability: fsync makes the rename scheme trustworthy (audit A1)

The promotion shape gives commit **atomicity** for free (the swap; concurrency excluded
by the whole-tree lock). It does **not** give **durability**: `rename(2)` is atomic but a
power loss before the dir entry flushes can lose it, and — worse — a moved-in `*_^^_new`
whose dir entry is durable but whose *contents* are still in page cache reads back as
zero/garbage (the classic rename-without-fsync corruption), which recovery would then
**promote as "complete."** So the scan is trustworthy only with a **bounded fsync
discipline**:

1. **fsync the built contents in scratch, then a `build-complete` sentinel last** (Promotion
   step 1) — so `*_^^_new` is never durable-but-incomplete even if the move-in degraded to a copy.
2. **fsync the parent dir after each rename** — so the rename survives power loss and
   recovery sees a real point in the sequence.
3. **fsync the source dir `U_^^_orig` after each repopulate move** (step 4) — so a source-unlink
   can't become durable *before* its dest-add, which would strand the item in **neither** dir
   (#5). POSIX `rename` is atomic vs. concurrent observers, **not** crash-ordered across two
   directory updates, and single-local-FS does not imply a journaling FS — so the earlier
   "step-4 moves are exempt, a lost move is just re-done" was wrong: a lost move is re-derivable
   *only* if the source survives, which this fsync guarantees.
4. **write the `.schema_version` marker durably** (step 5) — temp + fsync + rename, never a bare
   in-place write — so its presence implies complete contents (#2).
5. `F_FULLFSYNC` on darwin (plain `fsync` doesn't flush the APFS device cache).

**Scope — DECIDED: keep the fsyncs (max recoverability).** They buy **power-loss /
kernel-panic** safety on top of the process-death safety the page cache already gives;
migrations touch irreplaceable data and are rare, so we pay the handful of fsyncs per unit
for recoverability across every interruption method.

**C3/A4 (the existing split losing data) is fixed by construction:** the split runs
through the unified shape, so its files *and* the new version commit atomically at the
swap — the cross-file ordering C3 got wrong no longer exists. (The version must be atomic
with the data, so the lighter file-granularity path is **not** an option — see Cadence.)
Normal-operation writes outside migration are a separate robustness question.

## Promotion: build-new → repopulate → swap (commit state in filenames)

**One principle: rebuild what you *change*, move what you *keep*.** Incomplete builds
live in the `migration-<build-id>/` scratch dir, which **must be on the same filesystem
as the live dir** (decision 4; else the move-in is a non-atomic copy+delete and a
sentinel can appear *partial*). Only the live dir uses the reserved sentinel names — the
`_^^_` token **must be illegal in a real realm/sandbox name** (validate/reserve it).

**The sequence (unit `U` = `library` or `mysandbox`), fsyncs explicit. Key invariant:
`U_^^_new` *never* coexists with the canonical `U` — `U` becomes `U_^^_orig` **before** the new
build is placed — so the complete data always lives under exactly one well-known name:**
1. Build the **changed** files in scratch (without the new `.schema_version` yet); **fsync
   contents**, then write a **`build-complete` sentinel last** and **fsync** it — so a *partial*
   build (e.g. an interrupted copy, if the same-FS guard was wrong and the move-in degraded to
   copy+delete) is detectable and never mistaken for complete (#6).
2. rename `U` → `U_^^_orig`; **fsync(dir)**. `U_^^_orig` is now the complete old unit.
3. move the built dir → `U_^^_new` in the live dir; **fsync(dir)**.
4. **repopulate:** move the unchanged items from `U_^^_orig` → `U_^^_new` using the **structural
   filter `entries(U_^^_orig) \ entries(U_^^_new)`** — exactly the orig entries whose name is
   **absent from `_^^_new`**, **never** re-running the migrator's transform (#3). Each is an
   atomic rename (a multi-GB workdir moves as *one*, no copy). **fsync `U_^^_orig` (the source
   dir) after each move** so a source-unlink can't become durable ahead of its dest-add and
   strand the item in neither dir (#5); **fsync(`U_^^_new`)** when the list completes. Granularity
   rule: **any directory whose children changed is rebuilt whole** (so it lands in `_^^_new` and
   is never repopulated) — this is how the flatten's old overlay artifacts (`upper/`, `ovlwork/`)
   stay in `_^^_orig` and are dropped, not carried forward (#4).
5. **flip the version:** write the new `.schema_version` into `U_^^_new` **durably — temp-file +
   fsync(temp) + rename + fsync(dir)**, *not* a bare in-place write, so its **presence guarantees
   complete contents** (a bare write leaves the dir entry durable over zero/garbage bytes and the
   marker promotes torn, #2). Written **last**, after build + repopulate. Its presence is the
   authoritative "ready to promote" marker.
6. rename `U_^^_new` → `U`; **fsync(dir)**.
7. **dispose of `U_^^_orig`:** the default is **move → `trash/`** (not delete — a manual revert
   path; decision 3); **fsync(dir)**. A migrator may instead **drop** (delete) it when its
   displaced original is redundant — **the overlay flatten does** (its `_^^_orig` is the old
   overlay upper, whose content is already baked into the new copy and is revivable only with
   the old binary; dropping it also keeps the agent's secret-bearing upper out of `trash/`).

Step 6 commits **all of `U`'s changed files + the new version atomically** — no cross-file
ordering to coordinate (the C3 class of bug is structurally impossible).

**Recovery** is a classifier over the post-crash live-dir names + markers, and must be
**exhaustive**:
- **`U` alone** → not-started *or* fully-done; tell them apart by **unit kind (#7)**: a **realm**
  unit by its `.schema_version` (`< target` = not-started, `= target` = done); a **per-sandbox**
  unit by its **on-disk form** (`Mode` in `environment.json`, which swaps atomically with the
  unit — overlay = not-done, copy = done; there is no per-sandbox version file). No action either
  way.
- **`U_^^_orig` alone** → crashed between steps 2 and 3 (renamed away, build not yet placed).
  `U_^^_orig` is the complete old unit → **rename it back to `U`**, restart. (This clean transient
  is what the rename-before-move ordering buys — there is never a partial `U_^^_new` beside a live
  `U`, so the old #1 ambiguous state cannot occur.)
- **`U_^^_orig` + `U_^^_new` *without* the version marker** → build/repopulate incomplete. If
  `U_^^_new` also lacks its **`build-complete` sentinel**, the move-in itself was partial →
  **discard `U_^^_new`, rename `U_^^_orig` → `U`**, restart. Otherwise → **resume repopulate** (the
  structural filter re-derives the orig entries absent from `_^^_new`), write the version durably,
  then swap.
- **`U_^^_orig` + `U_^^_new` *with* the version marker** → ready → **promote** (step 6), then
  dispose (step 7).
- **`U` + `U_^^_orig`** → crashed between steps 6 and 7 → **finish step 7** (dispose `U_^^_orig`).

The complete data always lives under exactly one of `{U, U_^^_orig}`; `U_^^_new` only ever
appears **beside `U_^^_orig`** (never beside a live `U`), and their union is always complete
(items never torn — per-item rename atomicity). Step 4 is **forward-only** once it starts gutting
`_^^_orig`. If a migration changes *everything* (empty move-list) step 4 is empty. An alternative
that keeps `U_^^_orig` whole until the swap is to **reflink** the kept items into `_^^_new` rather
than move (cheap on CoW, full-copy on ext4) — a conscious trade.

The rigor a WAL needed doesn't vanish; it moves into this state machine, which must be
**exhaustively enumerated** + covered by **crash-injection tests at every rename
boundary**. By default the displaced `*_^^_orig` is moved to **`trash/`** (decision 3 — no
automated downgrade, but the prior schema's data is preserved for a manual, LLM-assisted
revert); the **overlay flatten opts out and drops it** (its old upper is redundant with the new
copy — see step 7).

## Source consistency (migrator concern; blessed strategies)

A small, shared set of strategies a migrator declares — not per-migrator hand-rolling
(avoids the ad-hoc-guard drift the project warns against):

- `QuiesceSandbox` — ensure the sandbox's container is stopped / agent-free before
  reading. **Quiescence comes from `DetectStatus == Stopped`, not the flock** (A5:
  the per-sandbox flock is released once the container launches, so a live agent
  holds no lock). Per-sandbox granularity means quiescing **one** sandbox at a time.
- `LiveContainerExtract` (overlay) — the flatten captures the **merged view** of a sandbox
  that is **already running** (overlay already mounted by whatever binary started it); the
  migrating binary **never starts or mounts** an overlay sandbox — that code is deleted
  (decision 8). The capture is a **raw recursive file copy of the merged tree** — **no git, no
  tar, no baseline diff** — landing the merged bytes as the new `:copy` work dir:
  1. **copy the merged tree** out as real files via the existing real-copy primitive
     (`cp -a` / `cp -rp` — the one that already seeds copy mode at `files.go:143` and copies
     the macOS overlay upper at `entrypoint.py:262`), reached either by reconstructing
     host-side from `lower` + `upper` (Linux — both are host-side) or by an in-container
     `cp -a` of the merged mount into a host-bound dir (uniform; **required on macOS**, where
     the upper is container-tmpfs). The copy is **destination-confined** — no write escapes
     the new work dir — which a real-tree copy gives for free: a directory entry physically
     cannot be named `..`, symlinks copy as inert symlinks, overlay whiteouts become
     deletions. There is no serialized patch/tarball whose embedded paths could traverse (the
     `--unsafe-paths`/tar-extract class of bug, DF70).
  2. fsync; **leave the container as it was** — the migrator does not stop it (stopping
     unmounts the overlay and, on macOS, destroys the tmpfs upper).
  Because **no git and no tar run at migration time**, the flatten touches neither the C1
  filter/hook class (no host git over untrusted content) nor DF70 (no host `git apply` of a
  container-generated patch). The merged tree *is* exactly what a `:copy` work dir holds, so
  the captured bytes are correct by construction — **including gitignored files and
  uncommitted state**, which the earlier diff-against-baseline mechanic silently dropped
  (re-audit B1/B2/B3). Only the merged-path location is needed — **no baseline SHA, no
  in-container git**. Reading is **non-destructive** (the source is never modified). All later
  diff/apply use copy mode's existing in-confinement git. Once captured, the sandbox migrates
  *exactly* like any other (stamp, swap) — the capture is just how Promotion step 1 is
  populated.
  **Caveat — non-atomic against a live agent (re-audit F2):** the merged tree is copied while
  the agent may still be writing (it can't be quiesced without unmounting the overlay), so a
  raw copy can catch a half-written file just as a live `git diff` could. The capture is *not*
  a consistent snapshot merely because the container is running; the plan must advise idling
  the agent (stop prompting) before migrating, and this is a known bound, not a guarantee.
- **Running-required precondition (A2 / re-audit Op-F1, mandatory, both platforms).** A
  *correct* merged view comes only from overlayfs having already assembled it — i.e. from a
  **running** container — and the new binary **cannot create that mount** (decision 8 deletes
  the mount code). Two non-options confirm why "running" is mandatory, not incidental:
  - *Starting it in plain mode doesn't help* — a no-overlay (`:copy`/bind) start mounts only the
    **lower** (the pristine original workdir) without the agent's upper, so it reads the wrong
    tree, not a recovered one.
  - *Offline host-side reconstruction isn't safe* — even on Linux, where the upper persists as a
    host dir, reconstructing the merge correctly requires honoring overlayfs
    `trusted.overlay.opaque`/`redirect` xattrs (deleted-and-recreated dirs, renames), and
    **`trusted.*` xattrs are readable only with `CAP_SYS_ADMIN`** (man 7 xattr), which the
    unprivileged binary lacks. It can handle whiteouts (char-dev 0,0) unprivileged but cannot
    reconstruct opaque dirs — and cannot even *detect* when it would be wrong → silent
    incorrectness on deletions, which we refuse to ship.

  So the migrator **requires the sandbox already running**, and the **dry-run plan enumerates
  every affected *stopped* overlay sandbox and branches by backend**:
  - **Linux-stopped — recoverable.** The upper is on the host, so the plan directs: abort,
    downgrade to the prior binary, **start** the sandbox (overlay remounts; the container
    survives the later binary swap), upgrade, re-run `migrate` — which now reads it running.
    Preserves the changes.
  - **macOS-stopped — already lost (DF69).** The tmpfs upper vanished at stop; downgrade-and-
    start yields an *empty* upper, so there is **no** recovery path — the plan says so plainly.
  - **Either, if the user declines/ignores** — proceed is a **destructive** op that flattens the
    original workdir and **abandons** the agent's overlay changes — **lost** (the user consented
    by proceeding): macOS was already gone, and on Linux the displaced upper is **dropped**, not
    trashed (consistent with the flatten's step-7 drop; we don't stash secret-bearing deltas in
    `trash/`). Never a silent empty-flatten.

  This is the resolution of the **Op-F1 gate-deadlock**: the migration gate blocks `start`, and
  the new binary can't mount overlay anyway, so a stopped overlay sandbox genuinely **cannot** be
  recovered post-upgrade — the recovery must happen *before* upgrading, which is why the plan
  surfaces it as a pre-upgrade audit, not an in-migration action.
- **Sandboxes-root resolution (flat-v0 installs).** The flatten migrator lives in the
  **orchestrator** (it needs `Exec`/`ApplyOverlay`/backend types `internal/config` cannot
  import — exactly why `MigrateAgentConfigs` lives there too), which legitimately spans
  realms, so it resolves its sandboxes root directly: **`library/sandboxes/` if present, else
  top-level `sandboxes/`** (no cross-realm plumbing, no generalization). This keeps the
  **plan accurate on a not-yet-migrated flat-v0 install**: `Plan()` runs *before* any
  migration, so there the sandboxes still sit at top-level `sandboxes/` (the `MigrateCLI`
  flat→namespaced relocation hasn't run yet) and a naive scan of `library/sandboxes/` would
  see nothing and under-report the flatten work. By apply time the cli relocation has run (it
  precedes the library realm in `system migrate`, which re-reads library status after it —
  `migrate.go:57-77`) and the same sandboxes resolve under `library/sandboxes/` — **same set
  either way, so the plan never lies.** This deliberately covers only *un-migrated* (look
  top-level) and *fully-migrated* (look library); a **half-relocated** install (a crash
  mid-relocation — audit A6 — where `library/sandboxes/` exists but some sandboxes are
  stranded at top-level) is **not** covered: those are silently skipped. That is the
  pre-existing A6 exposure on the sealed flat→namespaced migration surfacing through overlay,
  not a defect this flatten introduces.

## No automated downgrade — but the prior schema is preserved in trash (decision 3)

There is no *automated* downgrade: no compat window, no reversible step, and once a unit's
`.schema_version` advances an older binary hard-errors (`RealmStatus`: "newer than this
build supports"). **By default the displaced `*_^^_orig` is moved to `trash/`, not deleted** —
so the prior schema's complete data survives (until trash GC) and a desperate user can
manually revert (restore from trash + run the older binary), LLM-assisted. Strictly better
than gone-forever, without building downgrade tooling. **Exception — the overlay flatten drops
its `_^^_orig`** (the old upper is redundant with the new copy and revivable only with the old
binary; dropping also avoids stashing the agent's secret-bearing delta in `trash/`), so a
flattened overlay sandbox has **no** trash revert generation — acceptable given the redundancy.
The escape from a *persistent forward bug* is **quarantine** (per-sandbox: set the bad
sandbox aside, its data preserved in `trash/`), backed by the fact that the original is
untouched until each commit (a pre-commit failure leaves the old data fully intact at
the old stamp). A persistent *realm-structural* bug is fix-forward only — acceptable
because those are simple metadata moves. Document prominently: **migration is one-way;
back up `~/.yoloai` before upgrading.**

## Single-filesystem requirement (decision 4)

Migration **hard-refuses** unless the entire realm *and* its scratch dir sit on **one
local filesystem**, so every `mv`/rename is atomic (no `EXDEV`, no copy+delete that can
leave a partial sentinel dir). This subsumes the network-FS refusal (flock is unreliable
on NFS/SMB and meaningless across a synced root like Dropbox/iCloud) and the
spanning-mounts case. The refusal names the escape (relocate `~/.yoloai` onto a single
local FS and retry).

## Exclusivity & crash-recovery gating (decision 5)

**One live `flock` over the entire `$YOLOAI_HOME`**, held by `system migrate` for the
whole run — even though a given migration touches only part of the tree. While held,
**every other yoloai command refuses** ("migration in progress"); a second `migrate`
refuses too. It is a *live* flock (released on process death), so a crash never leaves a
permanent lock. After a crash (flock released), two persistent signals gate recovery:

- **Scratch dir present** → disposable; toss it; **does not block** anything.
- **Half-finished rename in a live dir** (a `*_^^_new`/`*_^^_orig` sentinel) → a
  promotion was interrupted → **block everything except `migrate`**, which **resolves it per the
  Recovery state table** — *not* always "complete it forward": depending on the state it
  promotes, resumes repopulate, or **discards** a partial/orphan `_^^_new` and restores `U` from
  `_^^_orig` (a build that never reached its `build-complete` sentinel or version marker is
  rolled back, not published). An **independent** guard — belt-and-suspenders with the stamp gate
  (a sentinel implies the stamp hasn't flipped, but the physical presence of an in-flight rename
  must block on its own).
- **Stamp < current** (the existing gate) → route to `migrate`.

Migrate's recovery order: toss scratch → resolve any in-flight live-dir sentinel per the Recovery
state table (promote / resume / discard-and-restore) → resume the run (rescan per-unit
version-or-form, migrate stragglers).

## Open decisions (critique targets)

1. **Bad-sandbox policy (A12) — RESOLVED by plan/apply.** A sandbox that can't migrate is
   surfaced in the **plan** as a foreseen quarantine ("set aside in `trash/`; the new-binary
   detector still refuses it individually"). The user then approves the **whole plan**
   (accepting the quarantines; the stamp flips once all are migrated-or-quarantined) **or
   aborts** — one up-front decision, no mid-run branch. Abort is clean: already-committed
   sandboxes stay migrated (atomic + independent), the rest stay at the old version, stamp
   unflipped → re-runnable. Headless/no-TTY **defaults to abort** on any destructive op
   unless `--yes`. See [Plan / apply](#plan--apply-dry-run).
2. **Ordering — DECIDED: data-dir realm (incl. its per-sandbox pass), then cli.** Each
   realm (data-dir, cli) carries its own linear `.schema-version`; a data-dir migration
   does any realm-level work plus an idempotent per-sandbox pass as **one** schema step.
   yoloai's other functionality unlocks only when every realm is at its current version.
3. **R1 downgrade — DECIDED: no downgrade.** See [No downgrade](#no-downgrade-decision-3).
4. **Network / synced FS — DECIDED: hard-refuse + single-FS.** See [Single-filesystem
   requirement](#single-filesystem-requirement-decision-4).
5. **Exclusivity / gate — DECIDED: whole-tree live flock.** See [Exclusivity &
   crash-recovery gating](#exclusivity--crash-recovery-gating-decision-5).
7. **Unit granularity — DECIDED: one linear data-dir schema + idempotent per-sandbox pass
   (no per-sandbox version files).** The overlay flatten is a normal data-dir migration
   (`v3→v4`) whose per-sandbox pass flattens each overlay sandbox via its own
   build-new→swap; "already done" is read from the sandbox's on-disk form, not a stamp.
   This gives incremental progress (a crash re-does only the in-progress sandbox) without a
   second version axis. (Supersedes the earlier sandbox-as-sub-realm framing — dropped
   2026-06-30 per "bump the schema version" being a single linear chain.)
8. **Overlay-reader placement — RESOLVED (revised by re-audit): delete *all* overlay code,
   including the git read-glue; the flatten captures the merged tree by raw recursive copy.**
   The earlier resolution kept the in-container git-diff "read-glue"
   (`generateOverlayPatchForContext` / `ApplyOverlay` / baseline helpers) and reused it for the
   flatten. The re-audit retired that: the diff-against-baseline mechanic silently dropped data
   (empty-baseline flatten B1, double-apply B2, gitignored-files-lost B3) **and** rode the DF70
   host-`git apply --unsafe-paths` hole. The flatten instead does a **raw recursive copy of the
   merged tree** (Source consistency) — **no git, no tar, no baseline** — so the read-glue is no
   longer needed by the migration and is **deleted with everything else.** The new binary
   deletes **every** overlay unit: create/start (entrypoint `apply_overlays`, the
   `CAP_SYS_ADMIN` grant + AppArmor/seccomp/podman-userns exceptions, `mounts.go` overlay specs,
   `collectOverlayMounts`, `OverlayMountConfig`, overlay-dir init) **and** the diff/apply
   read-glue; `:overlay` is removed as a creatable mode. Only the **path-location knowledge**
   (where `lower`/`upper`/merged live) survives, consumed by the raw copy. Consequences:
   **(1)** no detect-and-refuse / stepping-stone for overlay (A13/A14 dissolve); **(2)** no
   two-binary ordering constraint — the binary that adds the flatten also removes `:overlay`;
   **(3)** the security win is **maximal and clean** — the new binary **cannot mount overlayfs
   at all** (no `CAP_SYS_ADMIN`/mount code) *and* runs **no git over the captured tree** (clear
   of C1 and DF70). The cost: the sandbox must be **already running** to flatten (the binary
   can't start a stopped overlay) — stopped is a plan-surfaced choice (Source consistency).
   (DF70's `--unsafe-paths` fix is still needed independently for copy-mode's non-git apply.)
9. *(plus the user's pending critiques)*

## Migration chain (decoupled from release cadence, D110)

There are **two independent realm chains** — **library** and **cli** — each with **one**
linear `.schema-version` that orders its own migrations. They migrate independently with
**one structural exception**: the `MigrateCLI` flat→namespaced relocation creates `library/`
and lifts the library-owned content into it, so on a flat-v0 install the **cli realm must
relocate before the library realm can see its own contents**. `system migrate` honors this —
cli first, then it **re-reads** library status (the relocation may have just created
`library/`) before running the library chain (`migrate.go:57-77`). The library `v3→v4`
overlay flatten depends on this ordering; its `Plan()`, which runs before any relocation,
sidesteps it by resolving sandboxes at their current physical location (see Source
consistency). **Both realms must be fully current before yoloai runs anything** (the existing
gate). There is **no top-level `$YOLOAI_HOME` marker** (decided
no): its direct children are just whatever is installed (library always; cli/daemon
optionally), and the one above-realm migration we have — the `MigrateCLI` flat→namespaced
relocation — stays as-is. Schema versions are **not tied to app versions**, so a
single release can ship several migrations, and one migration can bundle several changes. A
migration is free to **reach into sandboxes** (they are part of the library realm, **not** a
sub-realm — see D110 on why we did not build a general carve framework). The chain:

- **`v2→v3` — agent.json split (existing, sealed as-is).** Already built and shipping
  (`LibrarySchemaVersion=3`, `MigrateLibrary` + `MigrateAgentConfigs`, run via `yoloai
  system migrate`; reaches into each sandbox). **Frozen** — not fused, not reworked into the
  new machinery. (It carries the A4/DF68 power-loss exposure — bare `os.WriteFile`,
  stamp-before-pass — mitigated by idempotent re-run; whether to retro-harden it is a
  separate open call, see below.)
- **`v3→v4` — this branch's migration (overlay retirement + any other on-disk change made
  here).** One version bump covers everything this branch changes. Its headline work is the
  **overlay→copy flatten**, the **first customer of the crash-safe machinery**
  (build-new→repopulate→swap + fsync + resumable rename + whole-tree lock). A per-sandbox
  pass converts **each overlay sandbox in isolation**: capture its merged tree by a raw
  recursive copy — no git, no tar, no baseline diff (see
  [Source consistency](#source-consistency-migrator-concern-blessed-strategies)), then swap —
  **no per-sandbox version marker** (progress is read from the sandbox's on-disk form). Both
  platforms **require the sandbox already running** (the binary never mounts overlay); a
  stopped overlay sandbox is a plan-surfaced choice — go back & start it, or proceed and
  abandon its overlay changes. The machinery lands here, with the destructive flatten as the
  user that both needs and exercises it.
- **overlay removal (rides the same `v3→v4` release).** Delete **all** overlay code — both
  create/start (entrypoint mount, `CAP_SYS_ADMIN` grant + AppArmor/podman exceptions,
  `mounts.go` overlay specs, `collectOverlayMounts`, `OverlayMountConfig`, dir init) **and** the
  git diff/apply read-glue (`generateOverlayPatchForContext` / `ApplyOverlay` / baseline
  helpers) — and `:overlay` as a creatable mode. The flatten uses a **raw recursive copy** of
  the merged tree (decision 8), not the read-glue, so nothing git-bound survives; only
  path-location knowledge remains. **No mount code remains**, so there is **no later
  detect-and-refuse binary** and **no ordering constraint**: any binary flattens a *running*
  overlay sandbox via `v3→v4`.

**Settled: leave the existing `v2→v3` split as-is.** "The agent.json ship has sailed" — we
do not retro-harden it. The A4/DF68 power-loss exposure stays (bare `os.WriteFile`,
stamp-before-pass), mitigated by idempotent re-run; the small fix (`atomicWriteJSON` +
stamp-after-pass) exists if it ever earns priority, but it is **not** in scope here.

## The git question (answered: borrow the patterns, not the tool)

Git is instructive but the wrong vehicle (GEN §14). **Borrow:** atomic rename as
commit, content-addressed retained generations. **Reject git-the-tool:** it can't
faithfully represent the data (overlay whiteouts are char devices; ownership/xattrs
/special-file fidelity lost), would run `filter`/`clean` drivers (the C1 mechanism we
just contained), and is slow on binary trees. The contract-fit primitives are
durable atomic rename + a filesystem CoW/seed copy + version-checks — byte-exact,
metadata-preserving.

## Research

- [research/crash-safe-migration.md](../research/crash-safe-migration.md) — SQLite /
  ARIES / POSIX-rename / Nix-OSTree / dpkg-rpm prior art. Key: build-alongside +
  atomic rename; forward=resume-first, rollback=wholesale-restore-only; the
  container-bound extract exception.
- [research/migration-version-gating.md](../research/migration-version-gating.md) —
  stepping-stone + detect-and-refuse, kept as **general prior art**; **not applied to overlay**
  (decision 8 — the flatten is a raw merged-tree copy any binary can run, so it never needs to
  refuse).
- [research/reflink-vs-hardlink.md](../research/reflink-vs-hardlink.md) — the snapshot
  primitive is **reflink-or-full-copy** (hardlink rung dropped).

## Relationship to other work

- **Audit:** [crash-safe-migration-audit.md](crash-safe-migration-audit.md) (A1–A18).
- **Prerequisite for** overlay retirement
  ([retire-overlay-reflink-copy.md](retire-overlay-reflink-copy.md), D109); cadence
  **D110**.
- DF68 (this finding), DF69 (macOS overlay live-or-lose); migration philosophy: dumb
  plain-int stamps, explicit fail-fast `migrate` owns recognition/validation.

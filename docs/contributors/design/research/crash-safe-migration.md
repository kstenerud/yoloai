# Crash-safe migration — prior art

ABOUTME: Verified prior-art research backing the crash-safe `system migrate`
ABOUTME: design (DF68): journaling, atomic commit, snapshot rollback patterns.

Status: **Verified 2026-06-30.** Backs the draft plan
[crash-safe-migration.md](../plans/crash-safe-migration.md) (DF68) — the
prerequisite (step 0) for overlay retirement (D109) and the agent.json-split
retro-harden. Mines the five systems the draft's "Research" section named:
SQLite, POSIX atomic-rename, ARIES, Nix/OSTree, dpkg/rpm. The point of this pass
was to **not invent** a bespoke recovery scheme for a solved problem.

> Method: four parallel research agents, each required to cite primary sources
> (sqlite.org, man7/POSIX, LWN, the ARIES TODS paper via CMU 15-445 / Berkeley
> CS262a / Stanford CS346 course notes, nixos.org, ostreedev.github.io, Debian
> Policy, rpm.org) and flag anything unverified. Verification gaps are listed at
> the end and must be closed before any flagged fact enters a normative doc.

## Executive summary — what the prior art is unanimous about

Five systems, ~30 years of production hardening between them, all doing some form
of "destructive transform of on-disk state that must survive a kill at any
instant." They converge on six points:

1. **Never mutate in place. Build the new state *alongside* the old, then flip one
   atomic pointer.** Nix and OSTree both do exactly this (symlink / deployment
   swap); SQLite and POSIX make the same move at file granularity (write-temp →
   rename). The retained old state *is* the snapshot — not a backup you restore.
2. **Rollback is a pointer swap, not a copy-back.** Nix `--rollback` and OSTree
   rollback repoint to the retained prior generation in O(1). A "restore the
   backup" step is itself a second bulk operation that can crash — pointer-swap
   has no such window.
3. **Forward recovery is resume-first; rollback is the escape hatch, not the
   routine path.** dpkg and rpm both bet almost entirely on roll-**forward**.
   rpm *shipped* a reverse-replay rollback and **deleted it as "too unreliable"**
   ([RPM 4.6.0 notes](https://rpm.org/wiki/Releases/4.6.0)). The lesson: a
   rollback users trust but that silently diverges is worse than none.
4. **Coarse step-level state beats a fine-grained WAL** when steps are idempotent
   and ordered. dpkg records one enum per package (`half-installed`,
   `half-configured`, …); that plus atomic-rename plus idempotent replay *is* its
   crash-safety. No byte-level undo log.
5. **Write-ahead ordering is the one non-negotiable primitive** (ARIES principle
   1; SQLite's journal-synced-before-DB-write). Telegraph intent durably, act,
   mark done — in that order.
6. **The commit is a single atomic filesystem op** (SQLite: "deleting the journal
   *is* the commit"; POSIX `rename`). Recovery decides "is this a crashed run?"
   by a validity check (valid-sentinel-after-body + checksum) plus "is a live
   process still holding the lock?".

The single biggest design signal: **the draft's "mutate in place, keep a backup to
restore" framing is the weaker of the two patterns the prior art knows.**
Build-alongside-then-swap is structurally crash-safe, makes rollback trivial and
unfailable, and collapses most of the journal bookkeeping. See "Implications for
the draft" below.

## Verified facts by system

### SQLite — rollback journal & WAL

Source: [Atomic Commit In SQLite](https://www.sqlite.org/atomiccommit.html),
[WAL](https://www.sqlite.org/wal.html),
[PRAGMA synchronous](https://www.sqlite.org/pragma.html#pragma_synchronous).

- **Rollback journal = undo log of *original* pages.** Before changing a page,
  SQLite copies the original content to a side journal. Commit sequence (at
  `synchronous=FULL`): write original pages + header to journal → **fsync
  journal** → write real page-count into journal header + **fsync again** →
  write modified pages to DB → **fsync DB** → **delete the journal**. *"Deleting
  the rollback journal is the single atomic commit point."* ≈3 sync points per
  commit.
- **The header page-count is written/synced *after* the page bodies** — so an
  interrupted journal reads "zero pages" and recovery applies nothing. Plus a
  **per-page 32-bit checksum** (seeded by a header nonce): a torn/garbage-extended
  journal fails the checksum and is abandoned. Two independent validity layers.
- **"Hot journal" detection** (must roll back) requires ALL of: journal exists,
  non-empty, **no RESERVED lock held** (i.e. no live writer owns it — a crash
  remnant, not an in-flight txn), well-formed/non-zeroed header, and any named
  super-journal still exists. The RESERVED-lock test is how SQLite distinguishes
  *a live migration in progress* from *a dead one that crashed*.
- **Commit-by-truncate/zero option:** instead of unlinking, SQLite can zero the
  journal header or truncate to zero — making it non-hot without a directory
  metadata change.
- **WAL mode:** new page images are *appended* to `-wal`; commit = appending a
  frame whose "database size after commit" field is non-zero (zero on all
  non-commit frames). A reader validates a frame by matching the header **salts**
  (re-randomized each checkpoint to invalidate stale frames) and a cumulative
  checksum; a torn final txn simply fails the checksum and is ignored — no
  separate rollback pass. `-shm` is the wal-index for locating newest pages.
- **`synchronous` trades:** WAL is *corruption-safe at NORMAL* (only durability,
  not integrity, is at risk); rollback-journal at NORMAL has a "very small
  non-zero" corruption chance on older filesystems. `EXTRA` = FULL + **fsync the
  directory after unlinking the journal**, closing the "commit-just-before-power-
  loss gets rolled back on reboot" window.
- **macOS:** plain `fsync(2)` does **not** flush the APFS device write cache;
  `F_FULLFSYNC` is required for true power-loss durability (`PRAGMA fullfsync`).
  The macOS leg of the cross-platform matrix.

### POSIX atomic-rename + fsync

Source: [POSIX rename](https://pubs.opengroup.org/onlinepubs/9699919799/functions/rename.html),
[Linux rename(2)](https://man7.org/linux/man-pages/man2/rename.2.html),
[LWN: Ensuring data reaches disk](https://lwn.net/Articles/457667/).

- **What rename guarantees:** *observational atomicity of the directory entry* —
  a concurrent reader of the target always sees either the complete old file or
  the complete new file, never missing/partial. POSIX: a link named `new` *"shall
  remain visible … throughout the renaming operation."*
- **Atomicity ≠ durability.** rename gives ordering/visibility, *not* persistence.
  The man pages assert only atomicity; durability comes from fsync (LWN /
  SQLite-EXTRA), not from `rename(2)`.
- **The safe replace idiom (LWN, verbatim 5 steps):** create temp **on the same
  filesystem** → write → **fsync(temp)** → rename(temp, target) → **fsync(dir)**.
  fsync-before-rename ensures the data is durable before the name is reachable;
  fsync-the-directory ensures the rename itself survives a crash.
- **EXDEV:** rename across filesystems fails — the temp file *must* be in the
  target's directory, or "atomic rename" silently degrades to copy+unlink
  (non-atomic, racy). Linux `renameat2` adds `RENAME_NOREPLACE` and
  `RENAME_EXCHANGE` (atomic swap of two existing paths, Linux-only).
- dpkg's `unsafe-io` (disabling fsync-before-rename) *"caus[es] zero-length files
  on abrupt system crashes"* — independent confirmation that this idiom is the
  load-bearing one ([dpkg(1)](https://man7.org/linux/man-pages/man1/dpkg.1.html)).

### ARIES — redo/undo recovery theory

Source: Mohan et al., *ARIES*, ACM TODS 17(1) 1992, via
[CMU 15-445](https://15445.courses.cs.cmu.edu/fall2023/notes/20-recovery.pdf),
[Berkeley CS262a](https://people.eecs.berkeley.edu/~kubitron/courses/cs262a-F13/lectures/lec05-aries-rev.pdf),
[Stanford CS346](https://web.stanford.edu/class/cs346/2015/notes/ARIES_One.pdf).

- **Three principles:** (1) **WAL** — the log record hits stable storage before
  the dirty data page. (2) **Repeat history on redo** — on restart, replay the
  log forward to reconstruct the *exact* pre-crash state (including
  soon-to-be-undone work), *then* undo losers. (3) **Log changes during undo** —
  undo actions are themselves logged as Compensation Log Records.
- **CLRs = the crash-safe-undo insight (most relevant bit).** Each undo step is
  logged as a **redo-only** record (CLRs are never themselves undone) carrying an
  `undoNextLSN` that points *past* what it just reversed, to the next thing
  needing undo. So a crash *during* rollback resumes at the right frontier and can
  never re-undo, re-redo, or loop. Rollback is monotone and bounded.
- **Idempotency / convergence:** redo is gated by `pageLSN < LSN` (skip if already
  applied); undo is gated by the CLR chain. Together, "repeat history" reaches a
  deterministic fixpoint regardless of how many times recovery is interrupted.
- **Honest limits (for us):** ARIES has **no concept of a forward step that is
  *wrong***. Its only failure model is "the process crashed"; it *always* repeats
  history and rolls losers back. It offers **nothing** for the persistent-vs-
  transient classification — that is a policy layer above the journal. And its
  heavy machinery (Dirty Page Table, `recLSN`/`pageLSN`, fuzzy checkpoints,
  fine-grained locking, the separate Analysis scan) exists to serve concurrency +
  a buffer pool we don't have. **Overkill — skip it.**

### Nix & OSTree — atomic state transitions without a database

Source: [Nix manual — Profiles](https://nix.dev/manual/nix/2.18/package-management/profiles),
Dolstra thesis (2006), [OSTree Introduction](https://ostreedev.github.io/ostree/introduction/) /
[Atomic Upgrades](https://ostreedev.github.io/ostree/atomic-upgrades/) /
[Atomic Rollbacks](https://ostreedev.github.io/ostree/atomic-rollbacks/),
[rpm-ostree handbook](https://coreos.github.io/rpm-ostree/administrator-handbook/).

- **Nix atomic switch:** a new generation is built entirely in the immutable
  store first; activation is *"a single atomic symlink swap … this last step is
  atomic on Unix, which explains how we can do atomic upgrades."* A crash before
  the swap leaves the old generation; after, the new — never a half-state.
- **Nix rollback = repoint the symlink** to a retained numbered generation
  (`nix-env --rollback` / `--switch-generation`). Cheap because the store is
  content-addressed/immutable: old and new live at different hashed paths, so
  retaining prior state costs ≈ a pointer (shared/hardlinked content).
- **OSTree** ("git for OS binaries"): content-addressed object store; each
  deployment is *"a hardlink farm … the running system is untouched."* Upgrade =
  stage the new deployment, then *"atomically swap the symbolic link."* *"If the
  system crashes or you pull the power, you will have either the old system or the
  new one."* Cost is *"proportional to the new files"* (dedup).
- **Rollback** = the prior deployment "changes places" with the current (a
  pointer/ordering change, not a copy). Retention is **bounded — default N=2**
  (current + one rollback); GC beyond that.
- **The cheap-snapshot insight maps exactly to reflink/CoW:** where Nix/OSTree get
  free retention from immutability + hardlinks, yoloAI gets it from CoW reflinks
  (block-shared until written), full-copy as the fallback. Conceptually identical.

### dpkg & rpm — the cautionary tale (destructive transforms *without* CoW)

Source: [dpkg(1)](https://man7.org/linux/man-pages/man1/dpkg.1.html),
[Debian Policy §6](https://www.debian.org/doc/debian-policy/ch-maintainerscripts.html),
[rpm-scriptlets(7)](https://www.man7.org/linux/man-pages/man7/rpm-scriptlets.7.html),
[RPM 4.6.0 notes](https://rpm.org/wiki/Releases/4.6.0),
[Linux Journal: Transactions and Rollback with RPM](https://www.linuxjournal.com/article/7034).

- **dpkg recovers by roll-forward/resume.** Each package's lifecycle position is a
  persisted enum (`half-installed`, `unpacked`, `half-configured`, `installed`,
  …); `dpkg --configure -a` drives every interrupted package forward to a
  consistent endpoint; `dpkg --audit` *diagnoses* (doesn't repair).
- **Crash-tolerant replacement = fsync-then-atomic-rename** (`.dpkg-{new,old,tmp}`
  suffixes; `unsafe-io` disables the fsync and risks zero-length files).
- **dpkg's "point of no return"** (Policy §6): before the renames begin, a failure
  triggers a *best-effort backward unwind* (scripts re-run with unwind args,
  backups restored); once renames start, *"it won't back off past this point"* —
  forward-only. Maintainer scripts **must be idempotent** so resume converges.
- **No true rollback in either.** rpm *built* `--rollback` + `--repackage`
  (rebuild an .rpm of each package before erasing) and **removed it**: *"seen too
  unreliable to be generally useful."* Why it failed: repackaging captured *files*
  but could not invert side effects (scriptlets, generated state, externally-
  modified files); it depended on every prior repackaged copy + a consistent
  rpmdb still existing. The result *looked* transactional but silently diverged —
  worse than no rollback. The modern replacement (`dnf history undo`) just
  computes a *new forward transaction* to approximate the old state.
- **rpm file ops are explicitly *not* transactionally recoverable** — `rpmdb`
  recovery (`--rebuilddb`) repairs *metadata*, not a half-applied filesystem.
- **Granularity:** dpkg's status DB and rpm's TID stamping are **per-package**
  (coarse), not a fine WAL. Coarse state + atomic step + idempotent replay is
  sufficient.

## Mapping to the draft's open questions

**(1) Snapshot scope & cost.** Reflink the **whole data dir** (cheap on
btrfs/XFS/APFS — only changed blocks diverge, so whole-dir is as cheap as partial
and far simpler; YAGNI). Full-copy fallback is acceptable for a rare explicit op.
**Retain exactly one** pre-migration snapshot, GC on commit — yoloAI's migration
is a discrete transition, not a rolling history, so OSTree's bounded-N (default 2)
model fits better than Nix's unbounded generations; N=1. Nix/OSTree validate that
"cost ≈ a pointer" via dedup/hardlink; reflink generalizes it to mutable copies.

**(2) Persistent vs transient failure.** **Not derivable from ARIES** (orthogonal
— ARIES always repeats history; it has no notion of a buggy forward step). This is
a **policy layer above the journal**, and dpkg/rpm tell us how to bias it:
**resume-first.** Bounded idempotent retries of the failed step (transient
assumption: ENOSPC since cleared, a kill, lock contention); if it fails
*identically* on re-attempt, or returns a classified-permanent error → flip the
recorded direction to **rollback**. Never auto-roll-back over recoverable partial
progress — that discards correct work. Resume is routine; snapshot-rollback is the
escape hatch.

**(3) Journal format & granularity.** **Coarse per-sandbox state enum**, dpkg-style
(`pre-migrate → migrating → migrated`, plus a `needs-resume` marker), **not** a
fine WAL. The **snapshot is the undo** (ARIES's CLRs exist only because a DBMS has
no global snapshot — we do), so the journal need only record *intent + progress +
direction*, not byte-level undo. Borrow SQLite's recovery-validity contract: a
**valid sentinel written/synced *after* the body** + a **per-record checksum**, so
a torn journal is ignored, not half-applied. **fsync freely** — this runs once per
migration; even dozens of syncs is milliseconds, so favour EXTRA-equivalent
durability everywhere and never reach for NORMAL/OFF. The commit primitive for any
version-bearing file: **write-temp-same-dir → fsync(temp) → rename → fsync(dir)**;
on macOS use `F_FULLFSYNC` for file and dir.

**(4) Lock semantics on network FS.** SQLite documents POSIX advisory locks as
unreliable on NFS, and the existing
[shared-state-concurrency.md](shared-state-concurrency.md) research already covers
the `flock`-on-network-FS hazard (and DF36 already warns on the data dir). Migration
is the highest-stakes operation in the system — recommend **hard-refuse** a
migration when the data dir is on a network filesystem rather than rely on an
advisory lock that may silently not be exclusive. (Confirm detection approach;
this is stricter than the draft's "document the residual.")

**(5) Per-step vs per-run envelope.** Nix, OSTree, and SQLite all argue a **single
per-run envelope with one commit point**. The build-alongside-then-swap model
(below) collapses per-step bookkeeping entirely: run all steps against the new
copy, expose exactly one commit (the pointer swap) and one rollback (swap back). A
crash *anywhere* in the run is then trivially recoverable — the old dir is
untouched, so discard the new copy and retry. dpkg is per-package only because it
*lacks* a cheap whole-tree snapshot; we have one, so we should prefer the
whole-run envelope.

**(6) Rollback UX.** Nix/OSTree make rollback first-class, explicit, near-instant
(a pointer flip). rpm's deleted rollback is the cautionary bound: **rollback must
be radically simpler than forward migration and obviously correct**, because it
runs exactly when trust is lowest. Concretely: `system migrate --rollback` =
**restore/swap to the snapshot wholesale** — a single dumb atomic op, *never* a
reverse-replay of forward steps (that is precisely what made rpm's rollback
diverge). Surface state like `dpkg --audit` ("here is exactly where it stopped")
and offer two clearly-labelled actions — **resume** (finish forward) and **roll
back** (restore snapshot). Make the "point of no return" (dropping the snapshot)
explicit and the **final** atomic act; never leave a window where neither resume
nor rollback is clean.

## Implications for the draft design

These are research conclusions to feed the critique cycle, not unilateral edits to
the plan:

1. **Reconsider "mutate in place + keep a backup to restore" → "build alongside +
   atomic pointer-swap."** This is the strongest and most unanimous signal. The
   prior art never copies-back on rollback; it retains the old generation and
   flips a pointer. Proposed shape: reflink `data/` → `data.new/`, run the
   transform on `data.new/`, then **atomically flip a `current` symlink** (Nix's
   portable, all-Unix model — preferred over a two-rename sequence which has a
   tiny non-atomic window, and over Linux-only `RENAME_EXCHANGE`). The
   pre-migration snapshot then *is* the live old generation, retained until commit
   — not a separate "backup." Rollback = flip the symlink back; O(1), can't fail.
   - **Tension to resolve in the critique:** this introduces a `current` symlink
     indirection into `~/.yoloai/`'s layout, a bigger change than the draft's
     in-place model, and must reconcile with the draft's per-sandbox-commit
     granularity (§3). The whole-dir-swap model largely *replaces* per-sandbox
     commits with one run-level commit; decide whether the layout change is worth
     it or whether to keep per-sandbox atomic-rename commits under a run-level
     stamp. Either way, **(5)** says converge on a single run-level commit point.

2. **Sharpen the recovery policy (draft §4):** resume-first (idempotent forward
   steps + coarse recorded state); rollback = wholesale snapshot restore **only**
   on persistent failure or explicit `--rollback`; **never reverse-replay**, never
   auto-rollback over recoverable progress. This is already the draft's instinct —
   now backed by rpm's deleted-rollback evidence as the strongest datum in the set.

3. **Journal is intent/progress/direction at per-sandbox granularity, not implied
   byte-level undo.** Add SQLite's sentinel-after-body + per-record checksum
   validity contract. Record the chosen **direction** durably before acting on it
   (the ARIES "don't flip-flop" point).

4. **Add the concrete commit idiom and the macOS `F_FULLFSYNC` requirement to §3**,
   and the EXDEV same-filesystem constraint for temp files.

5. **Lean hard-refuse on network FS (OQ4)** rather than "document the residual."

6. **The git verdict in the draft stands and is reinforced:** ARIES/SQLite/Nix/
   OSTree show the *patterns* (atomic pointer-swap = ref update, WAL, content-
   addressed retained generations) are exactly right; none reaches for git-the-
   tool. Borrow the patterns, reject the binary.

## Verification gaps (close before promoting to a normative doc)

- **WAL exact per-txn fsync count** is not stated on wal.html ("fewer, content
  written once"); only FULL's "+1 WAL sync per commit" is exact (PRAGMA docs).
- **ext4 `auto_da_alloc` / zero-length-file episode** is recounted from background
  knowledge; LWN 457667 documents the *safe pattern* but the specific mount-option
  mechanism was not re-fetched from a kernel.org primary — cite kernel docs if
  used.
- **`ostree admin deploy` retention flag spellings** were paraphrased from a search
  summary; the substance (bounded N, default 2, rollback = swap default) is from
  primary OSTree/rpm-ostree docs.
- **ARIES `undoNextLSN` resume semantics** confirmed via CMU 15-445 search result +
  sh-reya notes-on-the-paper; the paywalled TODS PDF was not fetched directly.
- The "one migration at a time per host" invariant underpins "skip ARIES
  concurrency machinery" — confirm it holds (it does today via the migration lock).

## The container-bound extract exception (overlay flatten)

The "exclusive, offline, no concurrent writers" precondition holds for **host-side**
data (the common case: the agent.json split, future metadata migrations). The
overlay→copy flatten is the exception: its source (the git baseline) lives *inside*
a container, so its **extract** phase is container-bound on every backend (verified
2026-06-30 — see [migration-version-gating.md](migration-version-gating.md) and the
retire-overlay plan's codebase map). The decomposition that contains the hazard:
**extract** (run the existing `apply` against a live, **agent-free** container — no
writer, so the read is quiescent) → **transform/commit** (offline host data, the
clean journal/snapshot/flip model above, unchanged). That flatten runs in a
**migration version** (which still ships the overlay/apply path), not the
post-removal binary — so the substrate here is exercised by that version. The
cheap-hardlink retention does **not** cross the container boundary, so extract is a
genuine content copy (inherent to overlay→copy anyway). macOS adds DF69 (overlay
upper may be tmpfs-only → convert while live).

## Relationship to other work

- Backs the draft plan [crash-safe-migration.md](../plans/crash-safe-migration.md)
  (DF68) and, through it, overlay retirement (D109) +
  [retire-overlay-reflink-copy.md](../plans/retire-overlay-reflink-copy.md).
- Pairs with [migration-version-gating.md](migration-version-gating.md) — the
  upgrade-UX / detect-and-refuse half of the same migration story.
- **Synergy with reflink-`:copy`:** the CoW snapshot primitive here *is* the
  reflink primitive that work adds — they share the implementation.
- Builds on [shared-state-concurrency.md](shared-state-concurrency.md) for the
  flock/network-FS facts (OQ4).
- Migration philosophy: dumb plain-int stamps; explicit fail-fast `migrate`
  command owns recognition/validation (feedback: migration-versioning-philosophy).

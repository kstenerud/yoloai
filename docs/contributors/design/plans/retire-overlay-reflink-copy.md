# Plan: retire `:overlay`, base `:copy` on reflink-aware copy

ABOUTME: Decision + implementation plan to remove the `:overlay` mount mode (a
ABOUTME: rootful-Docker host-escape surface) and recover its perf benefit via
ABOUTME: copy-on-write (reflink/clonefile) copies in the `:copy` path.

Status: **decided (D109), not yet implemented.** Supersedes the "gate `:overlay`"
direction in [overlay-sysadmin-escape.md](overlay-sysadmin-escape.md) (H2/DF65).

## Why (the short version)

`:overlay`'s only real benefit over `:copy` is **instant setup / low disk for
large working trees** — it skips the upfront directory copy by using an
in-container kernel overlayfs. Everything else about it is cost:

- **Host escape on rootful Docker (H2/DF65)** — kernel overlayfs forces
  `CAP_SYS_ADMIN` + `apparmor=unconfined` in the init user namespace; with the
  agent's passwordless sudo that's `cgroup release_agent` / `core_pattern` host
  code execution. The audit proved this is **not cheaply fixable**:
  fuse-overlayfs needs the *same* cap in a rootful, non-userns container (it's
  unprivileged only inside a user namespace — empirically refuted, see the H2
  plan's Audit section, a textbook GEN §14 trap).
- **Safe exactly where it's least needed, dangerous where it's most common.**
  `:overlay` is safe on Podman rootless and Apple (namespaced/in-VM cap) and on a
  userns-remapped Docker daemon — and a **host escape** on the default rootful
  Docker. It's also **incompatible with gVisor** (`container-enhanced`), so the
  security-conscious tier can't use it. The feature structurally pulls users
  toward the weakest isolation posture.
- **Audience collapse.** The only people who can run `:overlay` safely
  (rootless-Podman / userns-remap operators) are precisely the people
  sophisticated enough to put `~/.yoloai` on a copy-on-write filesystem — where a
  reflink `:copy` gives the *same* instant-setup/low-disk benefit with **no
  privilege, full snapshot isolation, every backend, offline diff/apply, and none
  of the VirtioFS/nested-overlay fragility**. So the safe audience doesn't need
  overlay, and the audience that needs it (rootful Docker) can't use it safely.
- **Large, fragile code surface** — ~18 files plus an entire parallel
  diff/apply/baseline path and the VirtioFS read-only-downgrade / nested-overlay
  handling in `entrypoint.py`.

**Reflink was verified before committing to this** (the prerequisite): `cp
--reflink=auto`-style copy-on-write (FICLONE on Linux, clonefile on macOS) is
safe to depend on — empirically confirmed on btrfs, XFS (`reflink=1`, the
`mkfs.xfs` default since xfsprogs 5.1), and APFS that it (a) clones with shared
blocks, (b) is **copy-on-write independent** (editing the clone never touches the
original — safe to diff), and (c) **falls back silently to a full copy** on every
non-CoW / cross-filesystem case (ext4, old-XFS, ZFS-default, NFS, overlayfs, WSL
DrvFs, cross-device). Documented idiosyncrasies (OpenZFS 2.2.0 block-cloning
corruption — fixed in 2.2.2, disabled by default through 2.2.6; btrfs `defrag`
un-sharing — disk-accounting only) do **not** break correctness, and the
fallback discipline neutralizes them. The one non-correctness caveat: the
speed/disk win only materializes when the project and `~/.yoloai` are on the
**same filesystem** (macOS satisfies this by default — single APFS Data volume).

## The decision (D109)

1. **Retire `:overlay`** as a shipped mount mode.
2. **Make `:copy` reflink-aware** so it recovers overlay's instant-setup/low-disk
   benefit on copy-on-write filesystems, transparently falling back to a normal
   full copy everywhere else.

Sequenced so the replacement is proven *before* the removal: **Phase 1 (reflink
`:copy`) is additive and ships first/independently; Phase 2 (remove `:overlay`)
follows.**

---

## Phase 1 — reflink-aware `:copy` (additive, no migration, no breaking change)

**Current state (grounded).** The copy lives in `internal/workspace`:

- The **default `:copy`** (honor `.gitignore`) goes `CopyProjectDir` →
  `copyFileList` → **`copyFile` (`copy.go:123`, plain `io.Copy` at `copy.go:135`)
  per file — no clone today.**
- `:copy-all` / non-repo dirs go `CopyProjectDir` → `CopyDir`, which tries
  `cloneDir` first. `cloneDir` is **macOS-only** (`copy_darwin.go`:
  `unix.Clonefile` whole-tree) with a `!darwin` **stub** (`copy_other.go`) that
  always errors → falls back to file-by-file `copyFile`. **So Linux has no
  reflink at all, and even macOS doesn't clone on the common gitignore path.**

**Change.** Add a per-file copy-on-write attempt to the shared `copyFile`, so
both paths (and every platform) benefit:

1. `copyFile` opens src/dst as today; before `io.Copy`, attempt a whole-file
   clone of src→dst; on **any** error fall back to the existing `io.Copy`. Keep
   the existing mode/mtime/symlink preservation unchanged.
2. New build-tagged `tryCloneFile(dstFd, srcFd) error`:
   - `copy_linux.go`: `unix.IoctlFileClone(dstFd, srcFd)` (the `FICLONE` ioctl).
   - `copy_darwin.go`: `unix.Clonefile`/`Clonefileat` (single file); keep the
     existing whole-tree `cloneDir` fast path for `CopyDir` (`:copy-all`).
   - `copy_other.go`: stub error → fallback (unchanged behavior).
3. Use **fallback (`=auto`) semantics only** — never error-on-failure. This is
   what makes ZFS-default (clone off → ENOTSUP), cross-device (EXDEV), and every
   non-CoW FS degrade cleanly to today's copy.

**Correctness invariants to preserve / assert:** the copy is a fully independent
file (CoW breaks the shared extent on first write — verified), so editing and
`git diff`-ing the work copy is unchanged. Byte-identical output on every FS.

**Tests.**
- Unit: clone-unsupported path falls back to `io.Copy` and produces a
  byte-identical, independent copy (force the stub / a non-CoW temp dir).
- Existing copy tests must stay green (no behavior change on ext4 CI).
- Optional `releasetest` check: a btrfs/XFS loop mount (the script in this
  workstream's scratch already does this) asserting the clone engages
  (df-used delta ≈ 0) and the copy is CoW-independent — gated/skipped when no
  reflink FS is available, like other backend-gated checks.

**Acceptance:** no behavior change on non-CoW filesystems; on a CoW filesystem
where project and `~/.yoloai` share the FS, `:copy` setup is near-instant and
disk-shared. `make check` green.

**Optional (separate, YAGNI-gated):** a hint/doc that placing `~/.yoloai` on the
same filesystem/volume as your projects is what unlocks the fast path; possibly a
`yoloai info` line reporting whether the data dir's FS supports reflink.

---

## Phase 2 — remove `:overlay` (breaking; after Phase 1 ships)

Mechanical but wide. Remove, don't gate:

- **Parsing / surface:** the `:overlay` suffix (`internal/cli/cliutil/dirspec.go`),
  `DirModeOverlay` (`store/dirmode.go` + the `aliases.go`/`create.go` re-exports),
  `yoloai.DirModeOverlay`, and every `case "overlay"` / `Mode == DirModeOverlay`
  branch (CLI `workflow/{diff,apply,apply_export,apply_overlay}.go`, `mounts.go`,
  `prepare_dirs.go`, `tags.go`, `status.go`).
- **Launch privilege:** the `SYS_ADMIN` grant for overlay
  (`internal/orchestrator/launch/launch.go:~843`) and the `apparmor=unconfined`
  downgrade trigger (`runtime/docker/docker.go:~547`). Removing overlay removes
  the *only* non-`container-privileged` reason these fire — verify nothing else
  depends on them.
- **Backend capability:** `BackendCaps.OverlayDirs` and its declarations
  (docker/podman/apple), the gVisor-overlay incompatibility check
  (`runtime/isolation.go`), the create-time capability gate
  (`launch.go` overlay refusal).
- **Container mount:** `apply_overlays` + the VirtioFS read-only-downgrade /
  tmpfs / nested-overlay fallback in `runtime/docker/resources/entrypoint.py`;
  `collectOverlayMounts` / `OverlayOrResolvedMountPath`
  (`internal/orchestrator/launch/launch.go`); overlay entries in
  `internal/orchestrator/mounts/mounts.go` and
  `internal/orchestrator/runtimeconfig/runtimeconfig.go`.
- **Diff/apply/baseline:** `copyflow/apply_overlay.go` (whole file),
  `GenerateOverlayDiff`/`GenerateOverlayChanges`,
  `ListCommitsBeyondBaselineOverlay`, `ensureOverlayBaseline`,
  `overlayDiffContext`, `ErrOverlayRequiresRuntime` (`copyflow/diff.go`,
  `copyflow/baseline.go`, `copyflow/export.go`), and the Engine wrappers in
  `internal/orchestrator/engine_workdir.go`.
- **Lifecycle:** overlay branches in `lifecycle/{reset,restart,start}.go` and
  `launch/teardown.go`; the overlay dir helpers in `store/paths.go`
  (`OverlayUpperDir`/`OverlayOvlworkDir`/`OverlayMergedDir`/`OverlayLowerDir`/
  `OverlayWorkBaseDir`).
- **Docs:** GUIDE.md (`:overlay` description + the worktree comparison),
  ROADMAP.md, the root CLAUDE.md `:overlay` bullet, and `docs/contributors/...`
  references.

**Migration substrate (prerequisite — step 0).** The overlay→copy flatten is the
first *multi-step destructive* migration (it swaps two directory layouts in one
path and flips `Mode` in a separate file), so it cannot be made crash-safe by
idempotency alone. It is built on the crash-safe migration substrate (exclusive
lock + write-ahead journal + per-sandbox atomic commit + snapshot rollback +
run-level stamp-last) designed in
[crash-safe-migration.md](crash-safe-migration.md) (DF68). That substrate lands
first and retro-hardens the agent.json split.

**Who runs the flatten — raw merged-tree copy against an *already-running* sandbox
(decided 2026-06-30; revised by the 2026-06-30 re-audit).** The overlayfs **merged view**
exists only while the container runs. The flatten captures it by a **raw recursive file copy**
(`cp -a` / `cp -rp` — the existing primitive at `files.go:143` / `entrypoint.py:262`) and lands
the bytes as the new `:copy` work dir — **no git, no tar, no baseline diff**. (The first cut
reused the in-container git-diff "read-glue" — `copyflow/apply.go:495-535`, `apply_overlay.go`
— but the re-audit retired that: a diff-against-baseline silently dropped agent work
[empty-baseline B1, double-apply B2, gitignored-files-lost B3] and rode the **DF70**
host-`git apply --unsafe-paths` traversal hole.) A raw tree copy is destination-confined for
free (a dir entry can't be named `..`; symlinks copy inert; whiteouts become deletions) and
needs **zero overlay runtime/mount code** and **zero git**. So the migrating binary **deletes
all overlay code** — create/start (entrypoint mount, `CAP_SYS_ADMIN` grant + AppArmor/podman
exceptions, `mounts.go` specs, `collectOverlayMounts`, `OverlayMountConfig`, dir init) **and**
the git diff/apply read-glue — keeping only path-location knowledge for the copy. It **never
mounts**; it copies out of a container a **prior** binary already mounted. Every future binary
can flatten a *running* overlay sandbox, so there is **no stepping-stone / detect-and-refuse**.
The crash-safe substrate is exercised here (and by future host-side migrations).

**macOS hazard (CONFIRMED on a Mac 2026-06-30, DF69).** When the host bind-mount
lacks `trusted.*` xattr support the entrypoint remounts the overlay upper to **tmpfs
inside the container** (`entrypoint.py:240-276`); changes exist **only while the
container runs**. Live-tested: agent edits to an `:overlay` workdir are **lost on
both graceful `stop`+`start` and non-graceful `kill`+`start`** (no tmpfs→host sync
on shutdown; the host upper stays empty). Verified on **all three** macOS container
backends — OrbStack, Docker Desktop, and Apple `container` — so it is **not
Docker-Desktop-only**. ⇒ a *stopped* macOS overlay sandbox has **already** lost its
uncommitted changes; the migration version **must** convert **while the sandbox is
running**, and the messaging must say so. (Results: [research/reflink-vs-hardlink.md](../research/reflink-vs-hardlink.md) §B/§C.)

**Recovery of existing on-disk `:overlay` sandboxes (DECIDED — resolves Open Q1;
supersedes the earlier split-reader-from-detector plan).** The `v3→v4` migration
flattens each overlay sandbox to `:copy` by a **raw recursive copy** of the
**already-running** container's merged view (crash-safe via the DF68 substrate;
idempotent/resumable; stamp flipped **last**), plus a pre-upgrade **audit**
(`migrate --check` / `system status`) listing sandboxes still on the overlay stamp
so the user learns *before* upgrading. Because the binary needs **no mount code** and
**no overlay git path** (decision 8 — all overlay code deleted, raw copy used instead),
there is **no post-removal binary distinction** and **no detect-and-refuse** — any binary
flattens a *running* overlay sandbox. **Requires the sandbox running**, both platforms — a
correct merged view comes only from a live overlayfs mount; the new binary can't create it,
a plain start reads only the lower (no agent changes), and offline host-side reconstruction
needs `CAP_SYS_ADMIN` to read overlayfs `trusted.overlay.*` opaque/redirect xattrs (the
unprivileged binary can't, so it can't reconstruct deletions correctly — re-audit Op-F1). The
dry-run plan enumerates affected **stopped** overlay sandboxes and **branches by backend**:
**Linux-stopped** is recoverable (upper persists host-side) → downgrade, start it, re-upgrade,
re-run (the container survives the binary swap); **macOS-stopped** is **already lost** (DF69 —
tmpfs upper gone at stop, so downgrade-and-start can't help). Either way the user may instead
**proceed** — destructive, abandons the overlay changes (macOS: already gone; Linux: host-side
upper set aside in `trash/`, manually recoverable). The gate-deadlock (the gate blocks `start`
post-upgrade) is *why* this is a **pre-upgrade** audit, not an in-migration recovery. **Security claim restated:** the host-escape vector was an *untrusted
agent* in a `CAP_SYS_ADMIN` overlay container; the new binary has **no overlay mount
code at all** — it cannot mount overlayfs, it only *reads* (exec `git`) from a
container a prior binary mounted. So the win is **maximal**: not merely "no agent
runs with overlay+`CAP_SYS_ADMIN`," but "the new binary cannot mount overlayfs,
period." (Flagged for security review — security claims get the highest scrutiny.)

**BREAKING-CHANGES.md** (breakage vs the last *published* release): `:overlay`
removed; rationale (host-escape on rootful Docker, not cheaply fixable; reflink
`:copy` replaces the benefit); migration (apply/destroy overlay sandboxes before
upgrading; use `:copy`, ideally with the data dir on a CoW filesystem).

---

## Sequencing (a linear migration chain — D110)

Sequenced by the crash-safe-migration chain ([D110](../../decisions/working-notes.md);
[crash-safe-migration.md](crash-safe-migration.md)). Migrations are a **linear data-dir
schema chain, decoupled from release numbers** — what matters is schema-step order, not
which version ships them:

- **`v2→v3` — agent.json split (existing, sealed as-is).** Already shipping; the overlay
  flatten is **not** fused with it.
- **`v3→v4` — overlay→copy flatten.** The first customer of the crash-safe machinery; a
  per-sandbox pass that **raw-copies the merged tree** of the **already-running** container into
  the new `:copy` work dir (no git, no tar, no baseline), then stamps + swaps. Reflink-`:copy`
  (Phase 1, additive) ships alongside or before this. **Requires the sandbox running** (both
  platforms); a stopped overlay sandbox is a plan-surfaced choice (go back & start, or proceed
  and abandon its changes).
- **overlay removal (rides the same release).** This build **deletes all overlay code** —
  create/start **and** the git diff/apply read-glue — and `:overlay` as a creatable mode; the
  flatten uses a raw copy, so no overlay git path remains (no mount code).
  Because no binary can mount overlay (and the flatten works by raw copy on any binary), there
  is **no separate post-removal binary**, **no detect-and-refuse**, and **no ordering
  constraint** — any binary flattens a *running* overlay sandbox via `v3→v4`.

## Open questions (for the human)

1. **Migration policy:** ~~fail-fast refuse vs auto-convert vs quarantine~~
   **RESOLVED 2026-06-30** → the `v3→v4` migration flattens each *running* overlay sandbox
   (plan/apply, with a pre-upgrade `migrate --check` audit) via a raw merged-tree copy (no
   overlay git path), so there is **no detect-and-refuse** — any binary flattens a running
   sandbox. A *stopped*
   overlay sandbox is a plan choice: go back & start it, or proceed and abandon its changes.
   See the Recovery section above.
2. **Phase 2 timing:** next minor, or sooner given it's a security-motivated
   removal of a beta feature?
3. **Same-filesystem ergonomics:** do anything to nudge `~/.yoloai` onto the
   project's filesystem (doc only, an `info` hint, or a config knob), or leave it
   to the user?

## Pointers

- Reflink prerequisite verification: empirical loopback tests (btrfs/XFS) + macOS
  APFS test + ZFS/btrfs idiosyncrasy research (this workstream, 2026-06-29).
- Copy path: `internal/workspace/copy.go` (`CopyDir`/`copyFile`),
  `copy_gitignore.go` (`CopyProjectDir`/`copyFileList`), `copy_darwin.go` /
  `copy_other.go` (`cloneDir`).
- Overlay surface: see Phase 2 list above; H2 analysis in
  [overlay-sysadmin-escape.md](overlay-sysadmin-escape.md); DF65.
- Decision: D109 (working-notes.md).

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

**Who runs the flatten — the migration version, not the post-removal binary
(decided; see [research/migration-version-gating.md](../research/migration-version-gating.md)).**
The codebase map (2026-06-30) settled a crux: overlay diff/apply reads a git
**baseline that lives *inside* the container** (established by
`UpdateOverlayBaselineToHEAD()` exec'd in the container; `copyflow/diff.go`,
`apply_overlay.go`), so the operation is container-bound on **every** backend —
even on Linux/Podman where the raw upper bytes happen to sit on a host bind-mount
(`~/.yoloai/sandboxes/<name>/work/<encoded>/upper/`). The post-removal binary
deletes exactly that machinery, so it **cannot** flatten an overlay sandbox. The
overlay→copy flatten is therefore essentially the existing **`apply`** operation
(apply the overlay delta onto the workdir, then treat it as `:copy`), and it must
run in a **migration version** that still ships the overlay/apply path — brought
up **agent-free** (the decoupled-launch enabler) so no agent writes while we read.
The crash-safe substrate is exercised *there* (and by future host-side
migrations), not in the new binary.

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

**Recovery of existing on-disk `:overlay` sandboxes (DECIDED — resolves Open Q1).**
Split the legacy *reader* from the legacy *detector*:
- **Migration version** ships a `migrate` verb that flattens each overlay sandbox
  to `:copy` (drives the existing apply path, agent-free, crash-safe via the DF68
  substrate; idempotent/resumable; flips the plain-int stamp **last**), plus a
  pre-upgrade **audit** (`migrate --check` / surfaced in `system status`) listing
  sandboxes still on the overlay stamp so the user learns *before* upgrading.
- **Post-removal binary** carries **zero overlay-read code** but keeps the cheap
  **detector forever**: a `Mode == overlay` / stamp check that **fails fast with a
  good refusal** naming the sandbox, the version gap, and the exact migration
  binary to run (the ES `IndexMetadataVerifier` five-element message is the model;
  a bare "unrecognized mode" is the anti-pattern). It never silently auto-converts
  — re-copying from source would **lose** the agent's overlay changes.

**BREAKING-CHANGES.md** (breakage vs the last *published* release): `:overlay`
removed; rationale (host-escape on rootful Docker, not cheaply fixable; reflink
`:copy` replaces the benefit); migration (apply/destroy overlay sandboxes before
upgrading; use `:copy`, ideally with the data dir on a CoW filesystem).

---

## Sequencing & timing

- **Phase 1 is independent and low-risk** — it can land any time (including the
  current release line) since it's purely additive with graceful fallback.
- **Phase 2 is a breaking change** — target the next minor (e.g. v0.7.0), *after*
  Phase 1 is proven. Until Phase 2 lands, the **v0.6.0 interim from the H2 plan
  stands**: `:overlay` is a documented dangerous opt-in with a loud warning (the
  escape requires explicit `:overlay` selection).

## Open questions (for the human)

1. **Migration policy:** ~~fail-fast refuse vs auto-convert vs quarantine~~
   **RESOLVED 2026-06-30** → migration-version flattens (a `migrate` verb +
   pre-upgrade audit, while overlay is still supported); post-removal binary
   detect-and-refuses with a pointer. See the Recovery section above and
   [research/migration-version-gating.md](../research/migration-version-gating.md).
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

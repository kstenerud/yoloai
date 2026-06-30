# Cheap-copy primitives: reflink vs hardlink (undecided)

ABOUTME: Tradeoff + verification status for the two "build-a-tree-alongside-cheaply"
ABOUTME: primitives — reflink (CoW clone) and hardlink — used by :copy and migration.

Status: **OPEN — both recorded, neither chosen (2026-06-30).** Cross-platform
support matrix: **web verification in flight**; macOS specifics: **on-device brief
below, awaiting a Mac run.** Feeds the reflink-`:copy` work
([retire-overlay-reflink-copy.md](../plans/retire-overlay-reflink-copy.md) Phase 1)
and the migration snapshot / build-alongside model
([crash-safe-migration.md](crash-safe-migration.md)).

> Decision posture (per the user): do **not** pick yet. Record both primitives and
> the tradeoff; the choice may differ **per use case** and depends on facts still
> being verified (esp. Windows, if it ever becomes a native target).

## The two primitives

- **Hardlink** — multiple directory entries, **one shared inode**. The content
  *and* all metadata (perms/owner/mtime/xattrs) live in that inode. `link(2)`.
- **Reflink / CoW block clone** — **independent inodes** sharing data *blocks*
  until one is written; the first write copies-on-write the touched blocks,
  leaving the other file untouched. Independent metadata. Linux `FICLONE` ioctl /
  `cp --reflink`; macOS `clonefile(2)` / `cp -c`.

## The tradeoff (the part that's clear)

Two axes pull in opposite directions:

| Axis | Favors | Why |
|---|---|---|
| **Support breadth** | **hardlink** | hardlink works on ext4, HFS+, native NTFS, tmpfs — where reflink does **not**. Everywhere reflink works, hardlink works too. |
| **Safe by construction** | **reflink** | CoW means an *in-place* write transparently protects the original. Hardlink shares the inode, so any in-place write (content **or** metadata: `chmod`/`chown`/xattr) **silently corrupts the old generation** unless code is *perfectly* replace-only (temp+rename only). |
| **Graceful degradation** | **reflink** | where unsupported, reflink falls back to a correct (slower) full copy; the calling code is unchanged. Hardlink has no "safe slow mode" — if you rely on it for cheapness you've also taken on the corruption risk. |
| **Cross-volume** | tie (both fail) | hardlink → `EXDEV`; reflink → fails/falls back to copy. Both require same-filesystem; the temp/staging tree must live under the same mount as the source. |

**The likely correction to the "reflink has wider support" intuition:** it's almost
certainly **backwards** on raw support — the decisive datum is **ext4** (the most
common Linux default) which has hardlinks but **no reflink** (and HFS+ and native
NTFS the same). But reflink may still be the better *default* for the
safe-by-construction + graceful-degrade reasons above — a different argument than
breadth. *(Full matrix being verified — see below.)*

## Per-use-case fit (this is why "record both" is right)

The two consumers have **opposite** write disciplines, so the right primitive may
differ:

- **`:copy` workdir** — handed to an *arbitrary agent that edits files in place*.
  Hardlinks are **unsafe** here (the agent's in-place edits would write through the
  shared inode to the original). → **reflink, or full copy.** This is settled:
  Phase 1 of the retire-overlay plan is already "reflink-aware `:copy`."
- **Migration snapshot / build-alongside** — produced by **our own code**, which we
  can *guarantee* is replace-only (write temp, rename into place; never open an
  existing path for write). Hardlink's one hazard is thereby removed, and hardlink
  buys cheapness on ext4 (where reflink gives nothing). → **hardlink is viable and
  possibly preferable**, *if* we are confident in the replace-only discipline;
  reflink remains the safer-by-construction alternative. **This is the open
  choice.**

So a plausible end state is **reflink for `:copy`, and either primitive for
migration** — decided once the support matrix and the macOS facts are in.

## Cross-platform support matrix — PRELIMINARY (web verification in flight)

Confidence-labeled; cells marked **(v?)** await citation from the in-flight web
pass. Do **not** treat unlabeled-confident cells as final until that lands.

| Filesystem | Hardlink | Reflink / CoW clone |
|---|---|---|
| ext4 (Linux) | ✅ | ❌ (no FICLONE) — **confident** |
| XFS `reflink=1` (Linux) | ✅ | ✅ (mkfs default since xfsprogs 5.1) — confident |
| btrfs (Linux) | ✅ | ✅ — confident |
| OpenZFS (Linux) | ✅ | ⚠️ block cloning 2.2+; 2.2.0 corruption bug, default-off through ~2.2.6 **(v?)** |
| tmpfs (Linux) | ✅ | ❌ — confident |
| APFS (macOS) | ✅ | ✅ `clonefile` — confident (on-device brief refines cross-volume) |
| HFS+ (macOS) | ✅ | ❌ — confident |
| NTFS (Windows, native) | ✅ `CreateHardLinkW` | ❌ no native reflink **(v?)** |
| ReFS / Dev Drive (Windows) | ✅ | ✅ block cloning `FSCTL_DUPLICATE_EXTENTS` **(v?)** |
| FAT / exFAT | ❌ | ❌ — confident |

If verified, this means: **hardlink support ⊃ reflink support** (strict superset on
the filesystems we care about). The Windows story (native NTFS = no reflink;
ReFS/Dev Drive = reflink) is the most consequential cell for a future Windows
target and is being checked first.

## macOS on-device verification brief (for a Mac agent on this branch)

The dev host is Linux, so these can only be confirmed on a Mac. Open this branch on
a macOS machine (Docker Desktop installed) and have an agent work through these.
**Report results back into this file** (replace the matrix cells / add a "macOS
verified" subsection) and **into DF69**.

### A. APFS clone & link semantics
1. **clonefile CoW-shares:** `cp -c bigfile clone` on an APFS volume; confirm
   `du`/disk-used delta ≈ 0 (blocks shared), and that the clone reads identical.
   Cite: clonefile(2) / "APFS" man material.
2. **clonefile is CoW-independent:** edit `clone` in place; confirm `bigfile` is
   byte-unchanged (CoW broke the shared extent). This is the property `:copy`
   relies on.
3. **Cross-APFS-volume clonefile:** does `clonefile` work across two APFS volumes
   in the same container (e.g. the Data volume vs a separate APFS volume), or does
   it `EXDEV`/fall back to copy? Determines whether `~/.yoloai` and a project on
   *different* APFS volumes still get the fast path.
4. **Hardlink on APFS:** `ln a b` works within a volume; confirm a cross-volume
   `ln` fails `EXDEV`.
5. **`F_FULLFSYNC`:** confirm that on APFS plain `fsync(2)` does **not** flush the
   device write cache and `F_FULLFSYNC` is required for true power-loss durability
   (the migration commit-primitive durability point). Cite Apple's fsync/fcntl
   docs.

### B. DF69 — is the macOS overlay upper "live-or-lose"? (the high-value one)
Static code reading (`entrypoint.py:~240-276`) suggests that when VirtioFS lacks
xattr support, the overlay `upper`/`ovlwork` are remounted to **tmpfs inside the
container** and the host upper goes stale — implying uncommitted overlay changes
are lost on restart. **Verify or refute on Docker Desktop:**
1. Create an `:overlay` sandbox; have the agent modify/create files in the workdir.
2. Inspect inside the container: is there a `tmpfs` mount at `/run/yoloai-overlay/…`
   (the fallback path)? Inspect the host
   `~/.yoloai/sandboxes/<name>/work/<encoded>/upper/` — is it stale/empty while the
   container holds the real upper?
3. **Graceful stop + restart** the sandbox: are the changes still present, or gone?
   Is there any tmpfs→host sync on graceful shutdown?
4. **Non-graceful kill** + restart: same check.
5. Conclusion: does macOS overlay lose uncommitted changes across a restart? →
   This determines whether the overlay→copy flatten must run **while the sandbox is
   live** on macOS, and resolves DF69.

### C. Apple `container` backend
Does the Apple `container` backend (`apple.go`, `OverlayDirs=true`) hit the **same**
VirtioFS xattr/tmpfs fallback as Docker Desktop, or is its overlay upper
host-readable? (Untested in the codebase map.) Determines whether the macOS hazard
is Docker-Desktop-only or both.

## Relationship to other work

- **reflink-`:copy`** ([retire-overlay-reflink-copy.md](../plans/retire-overlay-reflink-copy.md)
  Phase 1) already commits to reflink for `:copy` (correct — arbitrary in-place
  agent edits). This file is about the **migration snapshot** choice + the
  underlying matrix both share.
- **crash-safe migration** ([crash-safe-migration.md](crash-safe-migration.md)) —
  the build-alongside/pointer-swap snapshot uses one of these primitives; the
  "implications" there currently say "reflink the data dir," which this file
  reopens as undecided (hardlink may be better for the migration's replace-only
  case).
- **DF69** — the macOS tmpfs/VirtioFS overlay hazard, verified by brief §B.

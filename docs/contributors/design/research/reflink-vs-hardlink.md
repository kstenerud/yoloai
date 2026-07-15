> **ABOUTME:** Tradeoff and verification record for reflink (copy-on-write clone) versus
> hardlink as yoloAI's cheap-copy primitive for `:copy` and the migration snapshot,
> including the decision to drop hardlink and the macOS on-device confirmation behind it.

# Cheap-copy primitives: reflink vs hardlink

Status: **DECIDED 2026-06-30 — both consumers use reflink-or-full-copy; the
hardlink rung is dropped** (see "Decision" below). Cross-platform support matrix:
**verified + cited 2026-06-30**; macOS on-device specifics (APFS clonefile/hardlink
semantics, cross-volume, `F_FULLFSYNC`, DF69): **verified on-device 2026-06-30 —
results below.** Feeds the reflink-`:copy`
work
([retire-overlay-reflink-copy.md](../plans/retire-overlay-reflink-copy.md) Phase 1)
and the migration snapshot / build-alongside model
([crash-safe-migration.md](crash-safe-migration.md)).

## Decision (2026-06-30)

**Both `:copy` and the migration snapshot use reflink-or-full-copy. The hardlink
rung is dropped.** Rationale: migrations are **rare and explicit**, so a full-copy
fallback on a non-reflink filesystem (ext4, native NTFS) is an acceptable one-time
cost — not worth taking on hardlink's replace-only-discipline footgun (a single
in-place write through a shared inode would silently corrupt the pre-migration
snapshot). Reflink-or-copy is **safe by construction** (a CoW clone or a literal
copy is fully independent), so migration steps need no special "never write in
place" discipline, and the clone primitive is **unified** with the Phase-1
reflink-`:copy` work. The matrix and tradeoff below remain as the backing
reference; hardlink stays documented but unused.

The original posture (record both, pick later) is now resolved; the per-use-case
analysis that follows is kept for the reasoning trail.

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

**The "reflink has wider support" intuition is backwards — confirmed.** The
decisive datum is **ext4** (the common Linux default), which has hardlinks but
**no reflink** (`FICLONE` → `EOPNOTSUPP`); same for **HFS+** and **native NTFS**.
Hardlink support is a **strict superset** of reflink support on every filesystem
we care about (the only place hardlink loses is FAT/exFAT, where *neither* works).
But reflink may still be the better *default* for the safe-by-construction +
graceful-degrade reasons above — a different argument than breadth.

## Per-use-case fit (this is why "record both" is right)

The two consumers have **opposite** write disciplines, so the right primitive may
differ:

- **`:copy` workdir** — handed to an *arbitrary agent that edits files in place*.
  Hardlinks are **unsafe** here (the agent's in-place edits would write through the
  shared inode to the original). → **reflink, or full copy.** This is settled:
  Phase 1 of the retire-overlay plan is already "reflink-aware `:copy`."
- **Migration snapshot / build-alongside** — produced by **our own code**, which
  *could* be made replace-only, making hardlink technically viable (and cheap on
  ext4 where reflink gives nothing). **But rejected** (see Decision above): the
  cheapness only matters for large sandboxes on ext4/NTFS *during a migration*, and
  migrations are rare enough that a one-time full copy there is acceptable — not
  worth hardlink's replace-only footgun. → **reflink-or-full-copy**, same as
  `:copy`.

End state: **reflink-or-full-copy for both** consumers; hardlink documented but
unused.

## Cross-platform support matrix — verified 2026-06-30

| OS | Filesystem | Hardlink | Reflink / CoW clone | Source |
|---|---|---|---|---|
| Linux | **ext4** | ✅ | ❌ `EOPNOTSUPP` | [ioctl_ficlonerange(2)](https://man7.org/linux/man-pages/man2/ioctl_ficlonerange.2.html) |
| Linux | **XFS** | ✅ | ✅ (default; needs `crc=1`, also default) | [mkfs.xfs(8)](https://man7.org/linux/man-pages/man8/mkfs.xfs.8.html) |
| Linux | **btrfs** | ✅ | ✅ (origin of `FICLONE`) | [btrfs Reflink](https://btrfs.readthedocs.io/en/latest/Reflink.html) |
| Linux | **OpenZFS** | ✅ | ⚠️ ≥2.2, **default-off until 2.3.0** | [zfsconcepts(7)](https://openzfs.github.io/openzfs-docs/man/master/7/zfsconcepts.7.html); §ZFS below |
| Linux | **tmpfs / F2FS / overlayfs** | ✅ | ❌ (no clone impl) | by absence of `remap_file_range` |
| Linux/macOS | **FAT / exFAT** | ❌ | ❌ | no inode/link concept |
| macOS | **APFS** | ✅ | ✅ `clonefile` | [Apple: About APFS](https://developer.apple.com/library/archive/documentation/FileManagement/Conceptual/APFS_Guide/Features/Features.html) |
| macOS | **HFS+** | ✅ (incl. *directory* hardlinks) | ❌ | [Apple TN1150](https://developer.apple.com/library/archive/technotes/tn/tn1150.html) |
| Windows | **NTFS** | ✅ `CreateHardLinkW` (files, same vol, ≤1023 added) | ❌ **no native reflink** | [Block Cloning](https://learn.microsoft.com/en-us/windows/win32/fileio/block-cloning) · [CreateHardLinkW](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-createhardlinkw) |
| Windows | **ReFS** | ✅ | ✅ block clone (since Server 2016) | [ReFS block cloning](https://learn.microsoft.com/en-us/windows-server/storage/refs/block-cloning) |
| Windows | **Dev Drive** (ReFS-backed) | ✅ | ✅ (Dev Drive block clone 24H2) | [Dev Drive](https://learn.microsoft.com/en-us/windows/dev-drive/) |

**Verdict: hardlink support ⊋ reflink support** (strict superset; only FAT/exFAT
support neither). Reflink is absent on the three dominant defaults — **ext4, native
NTFS, HFS+** — plus tmpfs/overlayfs and ZFS < 2.3.

**Syscalls/APIs.** Hardlink: `link(2)`/`linkat(2)`; Windows `CreateHardLinkW`.
Reflink: Linux `FICLONE`/`FICLONERANGE` ioctl + `copy_file_range(2)` (`cp
--reflink=auto`); macOS `clonefile(2)`/`clonefileat` (`cp -c`); Windows
`FSCTL_DUPLICATE_EXTENTS_TO_FILE` (**ReFS only** — does nothing on NTFS).

**Windows reality (if it ever becomes a native target).** A stock install is
`C:` = NTFS, where the **only** cheap-copy primitive is the **hardlink**; reflink
requires a **separately-created** ReFS/Dev Drive volume (`C:` can't be a Dev Drive,
existing volumes can't be converted). So on default Windows, a reflink-based design
gives *nothing* (full-copy fallback) while a hardlink-based one works — a real
point for hardlink on the migration side.

**OpenZFS chronology.** Block cloning landed in **2.2.0**; the infamous 2.2.0
zero-corruption was **not** block-cloning's bug but a ~17-year-old dnode dirty-state
race it merely exposed at higher throughput ([issue #15526](https://github.com/openzfs/zfs/issues/15526)).
It was disabled by default in 2.2.1, the dnode race fixed in 2.2.2 (backport
2.1.14), and block cloning **enabled by default only in 2.3.0**. Treat **ZFS ≥
2.3.0** as the "reflink works" line; assume silent fallback below it.

## macOS on-device verification — RESULTS (verified 2026-06-30)

Run on macOS 26.5.1 (build 25F80), APFS, Docker 29.4.0, Apple `container` 1.0.0, on
this branch with the installed `yoloai` (commit `8c14d055` — current for the overlay
code, no diffs since). Method notes inline; commands were ad-hoc shell + `yoloai`.

### A. APFS clone & link semantics — all VERIFIED

1. **clonefile CoW-shares — ✅.** `cp -c` (clonefile) of a 200 MB file consumed
   **0 bytes** of container free space; a plain `cp` of the same file consumed
   ~200 MB. **`du` is misleading on APFS** — it reports each clone's *logical* size
   (200 MB), not shared blocks; the truth is the container free-space delta. The
   clone gets an **independent inode** (≠ source), unlike a hardlink.
2. **clonefile is CoW-independent — ✅.** Editing the clone in place left the source
   **byte-identical** (`shasum` unchanged); the clone diverged. This is the exact
   property `:copy` relies on.
3. **Cross-APFS-volume clonefile — falls back to full copy (no sharing).**
   clonefile(2) returns **`EXDEV`** ("src and dst are not on the same filesystem" —
   confirmed in the on-device man page); `cp -c` across volumes returns **rc=0 but
   does a real full copy** (~100 MB actually consumed on the target volume). ⇒ the
   staging/snapshot tree must live on the **same APFS volume** as its source to get
   the fast path; a project and `~/.yoloai` on *different* APFS volumes get a silent
   full copy, not a clone.
4. **Hardlink on APFS — ✅ / EXDEV.** `ln` within a volume shares the inode; a
   cross-volume `ln` fails **`EXDEV`** ("Cross-device link").
5. **`F_FULLFSYNC` — ✅ confirmed via on-device docs.** `man 2 fsync` states fsync
   flushes host→drive but "the drive itself may not physically write the data to the
   platters … if the drive loses power or the OS crashes, the application may find
   that only some or none of their data was written," and points to **`F_FULLFSYNC`**
   (fcntl `51`, present in the SDK's `sys/fcntl.h`) to "flush all buffered data to
   permanent storage." ⇒ the migration commit primitive **must** use `F_FULLFSYNC`
   (not plain `fsync`) on macOS for true power-loss durability.

**Bonus (the real use case): recursive directory clone — ✅.** `cp -cR` of a
two-level tree (the actual `:copy` / migration-snapshot operation) shared the
**whole tree** (0 bytes consumed), **preserved perms (640) and xattrs**, kept inodes
independent, and CoW-isolated a leaf edit from the source. clonefile(2)'s man page
warns against calling it directly on directories and recommends **`copyfile(3)` with
`COPYFILE_CLONE`** — which is what `cp -cR` uses under the hood.

**Net for the primitive choice on macOS:** reflink (`clonefile`/`copyfile`+CLONE) is
fully viable, byte-exact, metadata-preserving, and degrades to a correct full copy
cross-volume. It is the natural default for both `:copy` and the migration snapshot
on APFS. Hardlink works too but only within a volume and only under perfect
replace-only discipline — and buys **nothing** on APFS (clonefile already covers it).
Hardlink's only breadth advantage is on Linux **ext4** (no reflink there); on macOS
there is no reason to prefer it.

### B. DF69 — overlay "live-or-lose" — CONFIRMED (not refuted)

Reproduced with a real `:overlay` sandbox (`yoloai new … :overlay`):

- **Fallback triggers.** The live overlay `upperdir` is
  `/run/yoloai-overlay/<base64>/upper` — a **tmpfs inside the container**. The host
  upper (`~/.yoloai/library/sandboxes/<name>/work/<encoded>/upper/`) stayed
  **empty** the entire time.
- **Graceful `stop` + `start`: changes LOST.** An agent edit (appended line + new
  file) was gone after a clean restart — `file.txt` reverted to its baseline, the
  new file vanished. **There is no tmpfs→host sync on graceful shutdown** (the host
  upper remained empty across the stop).
- **Non-graceful `docker kill` + `start`: changes LOST** (as expected — a strict
  subset of the graceful case).
- `yoloai diff` *does* show the changes while the container is up — but **only
  because it execs `git` inside the container** against the merged overlay. This
  confirms the readability-map claim that overlay diff/apply is container-bound.

⇒ On macOS, a **stopped** overlay sandbox has *already* lost its uncommitted
changes. The overlay→copy flatten therefore **must run while the sandbox is live**
on macOS — there is no host-readable upper to flatten offline. This is **pre-existing
shipped behavior**, independent of the migration. DF69 resolved; → roll into
`backend-idiosyncrasies.md`.

### C. Apple `container` backend — SAME hazard; not Docker-Desktop-only

Verified on **all three** macOS container backends — **OrbStack**, **Docker
Desktop**, and **Apple `container`** — the tmpfs fallback triggers identically. Root
cause is the host bind-mount filesystem: **virtiofs** (OrbStack and Apple
`container`) or **`fakeowner` over VirtioFS** (Docker Desktop). None expose the
`trusted.*` xattrs overlayfs requires, so all three downgrade to the container-local
tmpfs upper. The hazard is **universal to macOS virtiofs-based container backends**,
not specific to Docker Desktop.

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
- **DF69** — the macOS tmpfs/VirtioFS overlay hazard: **CONFIRMED** by §B/§C
  results (live-or-lose on all three macOS container backends; flatten must run
  while the sandbox is live).

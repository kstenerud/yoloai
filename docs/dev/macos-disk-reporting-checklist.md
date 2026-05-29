<!-- ABOUTME: macOS verification checklist for the disk-reporting/prune fixes that -->
<!-- ABOUTME: were developed and validated only on Linux (see working-notes D35/D36). -->

# macOS disk-reporting & prune verification checklist

> **STATUS: VERIFIED on macOS 2026-05-29 — see working-notes D38.** Docker
> (OrbStack), Podman 5.8.1, Tart 2.31.0, and Seatbelt all confirmed. Docker /
> Podman / Seatbelt reporting was already accurate (no code change); Tart had a
> real gap (invisible footprint, 0-byte reclaim) that is now fixed — it
> implements `DiskUsageReporter` and reports a real before/after reclaim delta.
> The per-backend outcomes are inlined below. This file is kept as the record of
> what was checked and how.

The disk-reporting and prune accuracy work (working-notes **D35** + **D36**,
`backend-idiosyncrasies.md` entries on Podman `LayersSize: 0`, the containerd
two-snapshotter copy, and the Docker containerd-store pinning) was developed and
validated **only on Linux**. macOS has materially different disk semantics, so the
numbers `yoloai doctor` / `yoloai system disk` / `yoloai system prune` report there
were **unverified**. This was the pick-up list for a Claude session on a Mac.

The goal is the same as on Linux: **(a) `doctor` reports usage accurately, and
(b) `system prune` actually clears data** — verified by diffing yoloai's reported
numbers against the backend's own tools and the host filesystem.

## Why macOS differs (read first)

- **Docker Desktop runs in a LinuxKit VM.** The daemon's data root
  (`info.DockerRootDir`, e.g. `/var/lib/docker`) lives *inside* that VM, not on the
  macOS filesystem. This used to matter because the Linux reclaim path measured a
  host `statfs` delta on that data root — **that approach is gone** (working-notes
  D37). Reclaim is now the drop in the backend's own `CacheUsage` across the prune
  (`before − after`, `docker/prune.go` `reclaimableBytes`), which goes entirely
  through the daemon API and so works identically whether the data root is on the
  host or inside the VM. The macOS question is therefore no longer "is the statfs
  fallback right" but **"is `CacheUsage` accurate on the macOS store?"** — i.e. does
  the before/after delta match what the backend's own tool reports as freed. The
  raw SDK `SpaceReclaimed` is **not** used on any platform (it undercounts on the
  containerd store and *over*counts ~28x on Podman — see `backend-idiosyncrasies.md`).
- **Docker Desktop on macOS defaults to the _classic_ image store**, not the
  containerd snapshotter. The base image read ~5 GiB there vs ~33 GiB on the Linux
  containerd store. Confirm which store is active (`docker info` →
  `features.containerd-snapshotter`) before comparing numbers.
- **Podman on macOS runs via Podman Machine** (a Linux VM). The `LayersSize: 0`
  bug is a Podman-API property, so `podmanImageBytes` *should* apply identically —
  but the socket is the Machine's, and `podman system df` runs against the same VM.
  Confirm the dedup still matches.
- **Tart and Seatbelt are macOS-only and have no Linux equivalent.** Their
  `CacheUsage`/`PruneCache` (if any) have never been exercised by this work. Tart
  stores VM images under `~/.tart/`; Seatbelt is a lightweight sandbox with a
  different footprint model entirely. These need first-principles verification.

## Per-backend checklist

For each backend available on the Mac, compare three sources and confirm they agree:
1. yoloai's report: `yoloai doctor` and `yoloai system disk` (and `--json`).
2. the backend's own tool (below).
3. the host/VM filesystem (`du`, or `df` inside the VM where reachable).

### Docker — VERIFIED (was OrbStack, not Docker Desktop)
- [x] `docker info` store: **OrbStack**, `Storage Driver: overlay2` on btrfs, containerd-snapshotter **off** (classic store), `Default Runtime: runc`. "macOS Docker" ≠ Docker Desktop — always check the context.
- [x] `docker system df` vs `yoloai system disk`: **byte-exact** — `image_bytes 5023481654` = Images `5.023GB`; `cached_bytes 507954634` ≈ Local Volumes `508MB`.
- [x] Classic store → no build-cache layer pinning, logical≈physical. Reclaim = `CacheUsage` before−after (D37) works unchanged. **No code change.**

### Podman — VERIFIED (LayersSize is NOT 0 on 5.8.1)
- [x] Raw `/system/df` via Machine socket returns `LayersSize: 5018303449` — **not 0**. The Linux bug is version-specific; the `podmanImageBytes` dedup computes the identical value here (harmless redundancy). Keep the injection.
- [x] Dedup figure = `yoloai` podman image figure = `podman system df` Images `5.018GB`, byte-exact (`5018303449`).
- [x] `prune --images` reclaim confirmed by the real-prune run below. **No code change.**

### Tart (Apple Silicon) — VERIFIED + FIXED
- [x] Before: **no `DiskUsageReporter`** (reported `IMAGES: ?`), and `PruneCache` returned hardcoded `0`, despite ~56 GiB in `~/.tart`.
- [x] `tart list --format json` Size (whole-GB) + `du -sh ~/.tart/{vms,cache}/*`: yoloai-base ~27 GiB, OCI base ~29 GiB. Two `tart list` OCI rows (tag + digest) share **one** on-disk copy under `cache/OCIs/<repo>/sha256:<digest>/`.
- [x] **Fix:** Tart now implements `CacheUsage` (provisioned VM + base OCI deduped, → ImageBytes; CachedBytes 0) and `PruneCache` reports the before−after delta and deletes **both** OCI rows (tag-only delete leaks the digest copy). Now reports **55.88 GiB** (≈ `du` ~56 GiB). See `backend-idiosyncrasies.md` + D38.

### Seatbelt — VERIFIED (correct no-op)
- [x] On-disk state: **none beyond** the per-sandbox `~/.yoloai/sandboxes/<name>/` dir (already reported by the `sandboxes` row). `Setup` only checks PATH binaries; runs agents via host tools.
- [x] No backend cache → `CacheUsage`/`PruneCache` correctly absent (`?`/no-op). Documented as intentional, not a gap.

## macOS real-prune validation (2026-05-29)

A single real `yoloai system prune --images` across all backends, before/after
diffed against each backend's own tool and `du`:

| Backend | yoloai reclaim | Ground truth |
|---|---|---|
| docker (OrbStack) | **4.68 GB** | Images `5.023GB → 0`. The 508 MB Local Volumes survived (`VolumesPrune` skips named/in-use volumes — standard Docker behavior); the before/after delta correctly **excluded** them, reporting only the image reclaim. |
| podman | **4.67 GB** | Images `5.018GB → 0`. (`BuildCachePrune` → "Not Found" — expected on Podman, harmless.) |
| tart | **55.88 GiB** | `tart list` empty; **`du ~/.tart` 56G → 0**. Deleting *both* OCI rows (tag + digest) freed the on-disk copy — a tag-only delete would have leaked ~29 GiB. |
| **total** | **65.23 GiB** | = 4.68 + 4.67 + 55.88 GiB; all backends cleared to 0. |

Every backend reported only its own footprint (no cross-contamination), and each
figure reconciled with the backend's own tool — the D37 before/after-`CacheUsage`
model holds on macOS exactly as on Linux. (Side effect: the docker/podman/tart
base images are now gone; the next `yoloai new` rebuilds them, and tart re-pulls
~30 GB.)

## How the Linux side was validated (mirror this)

On Linux the ground-truth sources were: `podman system df -v`, `ctr -n yoloai
snapshots --snapshotter {overlayfs,devmapper} usage <key>` summed across all
snapshots, and `dmsetup status containerd-pool` for thin-pool allocation. Results
(2026-05-29): podman **5.18 GiB** (exact match to the `/system/df` dedup),
containerd **10.87 GiB** (overlayfs 5.30 + devmapper 5.53). Find the macOS
equivalents of these authoritative sources and confirm yoloai's numbers land on them.

## When done

- Record findings as `backend-idiosyncrasies.md` entries (one per surprising macOS
  behavior) + symptom-index rows, and a working-notes D-entry that references D36.
- If a fix is needed, keep the socket/API-only principle: don't reach for the host
  filesystem (the user may run unprivileged; the data root may be inside a VM).
- Update D36's "macOS unverified" note to point at the verification outcome.

<!-- ABOUTME: macOS verification checklist for the disk-reporting/prune fixes that -->
<!-- ABOUTME: were developed and validated only on Linux (see working-notes D35/D36). -->

# macOS disk-reporting & prune verification checklist

The disk-reporting and prune accuracy work (working-notes **D35** + **D36**,
`backend-idiosyncrasies.md` entries on Podman `LayersSize: 0`, the containerd
two-snapshotter copy, and the Docker containerd-store pinning) was developed and
validated **only on Linux**. macOS has materially different disk semantics, so the
numbers `yoloai doctor` / `yoloai system disk` / `yoloai system prune` report there
are **unverified**. This is the pick-up list for a Claude session on a Mac.

The goal is the same as on Linux: **(a) `doctor` reports usage accurately, and
(b) `system prune` actually clears data** — verified by diffing yoloai's reported
numbers against the backend's own tools and the host filesystem.

## Why macOS differs (read first)

- **Docker Desktop runs in a LinuxKit VM.** The daemon's data root
  (`info.DockerRootDir`, e.g. `/var/lib/docker`) lives *inside* that VM, not on the
  macOS filesystem. The Linux reclaim path measures a `statfs` free-space delta on
  that data root (`docker/prune.go` `freeBytes`/`daemonDataRoot`/`measuredReclaim`);
  on macOS `freeBytes` returns `-1` (path not host-visible) and the code **falls
  back to the SDK's `SpaceReclaimed`** — which `backend-idiosyncrasies.md` documents
  as undercounting by ~4x on the containerd store. So Docker-on-macOS reclaim totals
  may be wrong even though the Linux path is right. **Verify the fallback's accuracy.**
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

### Docker (Docker Desktop)
- [ ] `docker info` — is `containerd-snapshotter` on or off? Note it; it changes everything.
- [ ] `docker system df -v` vs `yoloai system disk` — do CACHE and IMAGES columns match?
- [ ] `yoloai system prune --images --dry-run` estimate vs the actual reclaim after a real run.
- [ ] Since `freeBytes` returns `-1` on macOS, confirm `measuredReclaim` falls back to
      `SpaceReclaimed` and check how far off it is (it undercounts on the containerd store).
      If Docker Desktop exposes a way to `statfs` the VM data root, consider wiring it.

### Podman (Podman Machine)
- [ ] `curl --unix-socket <machine.sock> http://d/v1.41/system/df` — confirm `LayersSize: 0`
      still holds (the whole `podmanImageBytes` workaround depends on it).
- [ ] `podman system df` dedup (`Σ(Size−SharedSize)+max(SharedSize)`) vs `yoloai doctor`'s
      podman image figure — should match to the byte as it does on Linux.
- [ ] Real `prune --images` reclaim vs `podman system df` before/after.

### Tart (Apple Silicon only) — UNVERIFIED, first-principles
- [ ] Does Tart's `CacheUsage`/`PruneCache` exist and report anything? (`internal/runtime/tart/`)
- [ ] `tart list` + `du -sh ~/.tart/vms/*` vs yoloai's report.
- [ ] Does `yoloai system prune` actually remove Tart VM images, and is the reclaim measured correctly?

### Seatbelt (macOS lightweight sandbox) — UNVERIFIED, first-principles
- [ ] What on-disk state does Seatbelt accumulate, and where? Map it before trusting any report.
- [ ] Does it participate in `CacheUsage`/`PruneCache` at all, or is it a no-op? Confirm and document.

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

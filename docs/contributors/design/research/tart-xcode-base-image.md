# Tart `-xcode` base image — A/B findings

**Status:** investigation complete, feature (image mode) **uncommitted, pending adoption decision.** 2026-06-09.

## Question

Tart sandboxes for Apple/iOS work currently use the Cirrus `macos-<codename>-base`
image, which has **no Xcode**: yoloAI mounts the *host's* Xcode.app into the VM
via VirtioFS and downloads the iOS simulator runtime in-VM
(`xcodebuild -downloadPlatform iOS`, ~8–16 GB, 10–15 min per base). Cirrus also
publishes `macos-<codename>-xcode` images that **bake in Xcode and the default
iOS runtime**. Would adopting those eliminate the slow per-base runtime download?

## Cirrus `-xcode` images (verified)

- Built by `cirruslabs/macos-image-templates` `templates/xcode.pkr.hcl`, which runs
  `xcodebuild -downloadPlatform iOS` + `-runFirstLaunch` at **image build time**.
- Tagged by **Xcode version** (`26.5`, `26.4.1`, …), not macOS version; point-release
  fixes are republished within ~24h–a weekend. `:latest` floats to newest stable.
- Ships the **default** iOS runtime only; non-default versions still download in-VM.
- **Size:** `macos-tahoe-xcode:26.5` is **68.8 GB compressed** to pull, **~87 GB
  on disk** — vs ~32 GB for `-base`. Known sporadic Xcode-26.x flake where a baked
  runtime isn't recognized by `simctl` (#303); keep the in-VM download as fallback.

## Image mode (the implemented, uncommitted feature)

Auto-detected when the resolved `tart.image` is an `-xcode` family ref. When active:
- **Skip** the host-Xcode VirtioFS mount (base-build + per-sandbox run paths) — the
  baked Xcode is real and pre-selected (`/Applications/Xcode_26.5.app`, **not**
  `/Applications/Xcode.app`); mounting host Xcode would shadow it.
- **Skip** `configureXcodeInVM` (symlink/xcode-select/license/firstLaunch) — the
  image already did all of it.
- **Verify-else-download** the runtime: check `simctl` for the requested runtime; skip
  the download if baked in, else fall back to the existing download.
- Runtime-base VM name encodes the family (`yoloai-base-xcode-<key>`) so `-base` and
  `-xcode` bases coexist without colliding.

## A/B result (host = macOS 26 / Xcode 26.5, Apple Silicon)

| | Arm A (`-base`) | Arm B (`-xcode:26.5`) |
|---|---|---|
| Base image on disk | ~32 GB | **~87 GB** (68.8 GB pull) |
| Xcode | host-mounted (VirtioFS) | **baked, pre-selected** |
| iOS runtime | downloaded in-VM (slow) | **baked; download skipped** ("…already present, skipping") |
| Host-Xcode mount in sandbox | yes | **none** (verified) |
| iOS tests (embrace-apple-sdk) | 1,269 pass / 0 fail | smoke 133 pass / 0 fail |

Both arms require the **arm64 pin** (`ARCHS=arm64 ONLY_ACTIVE_ARCH=YES` + destination
`arch=arm64`) to dodge an Xcode-26.5 swift-syntax `_SwiftSyntaxCShims` x86_64 build
failure — that is a toolchain issue, independent of base image.

**Tradeoff:** image mode removes the per-sandbox runtime download but pays a much
larger one-time base pull/footprint (~87 GB vs ~32 GB). Favorable on a warm machine
spinning up many sandboxes; unfavorable cold or in CI. **Open decision: is the
footprint worth it, or keep the host-mount + download default and offer `-xcode`
purely as an opt-in via `tart.image`?**

## Bugs surfaced during validation

1. **`Setup` promote deletes a running base** → `tart delete` returns a misleading
   `instance not found`, abandoning an hour-long build. Fix: `stopVM` before delete.
   *General, not image-mode-specific.* (Uncommitted, in the entangled `build.go` diff.)
2. **`sandbox-setup.py` `None`-deref** at `os.path.isdir(xcode_mount)` when there is
   no host Xcode mount (image mode) — crashed in-VM setup, hung `start`, left the
   workdir uncopied. Fix: guard for `None`. Slipped past `make check` because
   `sandbox-setup.py` is outside the mypy-typed surface (only `setup_helpers.py` is).
   **Follow-up: add `sandbox-setup.py` to the typed set.**
3. **`parseVMList` parses the wrong columns** of modern `tart list` output (reads the
   `Source` column as the VM name) → `system tart list` always reports "No runtime
   base images found." **Pre-existing format drift, independent of image mode.**
   `new` is unaffected (it uses `tart list --quiet`). Fix: parse `tart list --format json`.
4. **Runtime-base cache key ignored the base image** → `-base` and `-xcode` runtime
   bases for the same runtime collided on one name; image mode could silently reuse a
   `-base` base. Fix: family segment in the name (part of image mode).

## Unresolved

- **Runtime base `yoloai-base-xcode-ios-26.5` vanished once** between creation and
  reuse (raw `tart list` confirmed it was actually gone, not just a display artifact).
  Not reproduced; potential base-lifecycle / data-loss bug. **Needs a clean repro.**

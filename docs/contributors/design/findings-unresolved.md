<!-- ABOUTME: Mid-workstream discoveries that were not in the original audit. Critical -->
<!-- ABOUTME: findings escalate; everything else parks here until the next re-audit. -->

# Discovered Findings

Findings that turned up mid-workstream (architecture-remediation, layering-refactor, or any future plan) and were **not** in the originating audit. Per the discovered-findings policy:

- **Critical findings escalate immediately, do not park.** Critical = observable data loss, security issues, observable regressions in shipped behavior, or anything that would block the current release.
- **Everything else parks here** until the next re-audit checkpoint. Don't expand a workstream's scope to absorb new findings.
- The discoverer makes the severity call; when uncertain, escalate.

## Entry format

```
### DF<N> — <one-line title>

- **Discovered:** <YYYY-MM-DD> · **Workstream:** <W-L1 / W7 / etc>
- **Severity:** CRITICAL / MEDIUM / LOW
- **Disposition:** ESCALATED / PARKED / ADDRESSED-IN-PLACE
- **Description:** <2-4 sentences>
- **Pointer:** <file:line or commit hash>
```

## Findings

### DF13 — Restart prompt re-injection races Claude Code's folder-trust dialog (second prompt dropped)

- **Discovered:** 2026-05-31 · **Workstream:** W-L1 (G7, surfaced by smoke run `yoloai-smoketest-20260531-233151.431`)
- **Severity:** LOW
- **Disposition:** PARKED
- **Description:** On the `stop_start` restart leg (`restart` → `sb.Restart(StartOptions{Prompt:…})`), Claude Code v2.1.157 shows a "Quick safety check: Is this a project you trust?" dialog at startup whose selector line begins with `❯` — the same readiness pattern the prompt-injection waits for. The relaunched agent reached the welcome screen and sat idle at the ready prompt; the staged second prompt (`prompt.txt` correctly held the `done2` task) was never executed, so `files/done2` was never created and the test timed out (31s gap). Likely mechanism: the injected prompt + Enter is consumed by the trust dialog (Enter confirms "Yes, I trust this folder") rather than delivered to the agent REPL, dropping the task text. Non-deterministic: only podman failed this run (docker recovered on retry; docker-cenhanced/containerd-vm/vmenhanced passed). Matches the known podman network-flake family ("network: unreachable"). **NOT a regression** from the G7 carves — those relocate host-side Go functions and never touch entrypoint, start/restart, or tmux prompt injection (the `StartOptions.Prompt` path is unchanged; only `ResetOptions`/`Reset` were modified). Needs a reproduction before any fix; candidate remedy is to make restart prompt-injection wait for the *post-trust-dialog* steady-state ready prompt (or pre-trust the work copy) rather than the first `❯`.
- **Pointer:** `internal/cli/lifecycle/restart.go:74`; agent-side readiness wait in the monitor/lifecycle start path; autopsy `.testcache/runs/yoloai-smoketest-20260531-233151.431/sandboxes/stop_start/podman/attempt1/FAILURE.md`

### DF18 — Backend run-coverage gap: live-daemon error paths + zero Seatbelt/Tart run coverage

- **Discovered:** 2026-06-04 · **Workstream:** testing-critique (T13 split-out)
- **Severity:** MEDIUM
- **Disposition:** PARKED (partially addressed)
- **Progress (2026-06-11):** A Tart integration tier now exists (`internal/runtime/tart/integration_test.go`, `//go:build integration`, Apple Silicon + tart) covering the *cheap* paths — `New`, `Descriptor`, `Inspect` not-found, idempotent `Remove`. The core gaps remain open: `TestTart_FullVMLifecycle` is still `t.Skip`-ped ("pending Tart Runtime fix"), **Seatbelt still has zero run coverage**, the docker-compat `runtimetest.RunConformance` table is not mirrored for Tart/Seatbelt, and the live-daemon error-injection cases (dead-daemon-mid-op, image-missing, exec-on-stopped, prune-failure, overlay diff/apply) are not added. Keep parked until the remaining items land.
- **Description:** T13 promoted the *cheap* (host-only, fakeable) error paths to first-class
  assertions, but a class of error branches is reachable only against a live backend and stays
  unhit: dead-daemon-mid-op, image-missing, exec-on-stopped-container, prune-failure, and the
  overlay diff/apply error paths (overlay requires a running container for the in-container git
  exec). More structurally, **Seatbelt and Tart have no real run coverage at all** — no integration
  tier exercises a real Seatbelt host-process sandbox or a real Tart VM, so their happy *and* error
  paths are unverified except by the Python smoke harness. The conformance suite extracted in T2
  (`runtimetest.RunConformance`) is docker-compatible only; Seatbelt and Tart need their own
  behavioral tables. Not absorbed into the testing-critique scope because it needs live-daemon /
  VM / macOS infrastructure, not a test rewrite.
- **Trigger:** when CI (or a contributor host) gains a reachable Seatbelt (macOS) and Tart (macOS
  VM) environment, stand up per-backend integration tables mirroring the docker conformance shape;
  separately, add live-daemon error-injection cases (kill the daemon mid-op, reference a missing
  image, exec into a stopped container, force a prune failure) to the docker/podman integration
  tier where the daemon is already required.
- **Pointer:** `internal/runtime/runtimetest/conformance.go` (docker-compat table to mirror);
  `internal/runtime/seatbelt/`, `internal/runtime/tart/` (no integration tier); overlay error paths
  in `internal/sandbox/patch/apply.go` (`generateOverlayPatchForContext`, `ensureOverlayBaseline`).

### DF24 — Stale-base detection flags the *wanted* base as "superseded" when `tart.image` pins a non-default image

- **Renumbered:** originally recorded as DF20; renumbered to DF24 on 2026-06-11 to resolve a duplicate (DF20 is canonically the gVisor world-readable-credentials finding, which every cross-reference points to). The recording commit `7e1566c` still says "DF20".
- **Discovered:** 2026-06-10 · **Workstream:** Apple `container` backend planning (side-discovery while diagnosing a leftover config override)
- **Severity:** MEDIUM (actively recommends deleting a wanted artifact; gated behind a dry-run preview + `y/N`, so not silent data loss)
- **Disposition:** PARKED
- **Description:** `staleBaseImagesFrom` classifies every installed Tart base whose repo ≠ the *current* repo as "superseded (safe to remove, no rebuild)", where current = `baseImageRepo(r.resolveBaseImage(""))` — and `resolveBaseImage` honors the `tart.image` config override. So an explicit `tart.image` pin makes the host-matched `macos-<codename>-base` look superseded, and `yoloai doctor` / `yoloai system prune --stale-bases` offer to delete the base the user actually uses. Observed 2026-06-10: a leftover `tart.image: ghcr.io/cirruslabs/macos-tahoe-xcode:26.5` (from the `-xcode` A/B) made `prune --stale-bases` offer to remove the real `macos-tahoe-base` (29.8 GiB) as "safe, no rebuild." Technically consistent with the override, but a sharp edge — nothing surfaces *which* configured image drives the "current" determination, so a config typo or leftover reads as a free cleanup.
- **Fix:** surface the configured `tart.image` (and the resolved "current base repo") in the `doctor` / `prune --stale-bases` output so the user can see *why* a base is flagged; and/or treat an explicit non-`-base` override specially — don't label the host-matched `-base` "no rebuild, safe to remove" when the only reason it is non-current is an explicit override (warn instead, or exclude it from the stale set). Keep the genuine cross-macOS supersede case (old codename after an OS upgrade) working.
- **Trigger:** the next time a custom `tart.image` is set in earnest (xcode image-mode adoption, pinning an older/newer macOS, etc.) — before that ships broadly, make the stale-base output name the driving config so it can never recommend deleting a wanted base.
- **Pointer:** `internal/runtime/tart/diskusage.go:139` (`staleBaseImagesFrom`; `currentRepo` from `resolveBaseImage("")`), `internal/runtime/tart/stalebases.go:23` (`PruneStaleBases`); the `doctor` / `system prune --stale-bases` surfacing.

### DF21 — Docker Desktop containerd store: BuildKit attestations make `yoloai-base` a manifest-list index that vanishes between runs (full rebuild every run)

- **Discovered:** 2026-06-10 · **Workstream:** Apple `container` backend (diagnosing repeated base-image rebuilds during `make smoketest-full`)
- **Severity:** MEDIUM (no data loss, but a full ~5-minute `yoloai-base` rebuild on *every* operation against a Docker Desktop daemon that uses the containerd image store — increasingly the default)
- **Disposition:** RESOLVED (primary, this commit); the secondary host-global-marker bug remains PARKED.
- **Root cause (confirmed empirically).** `buildBaseImage`/the profile build ran `docker build -t yoloai-base -` with no attestation flags. BuildKit's default provenance/SBOM attestations make the result a **manifest list / image index** on Docker Desktop's containerd image store: the tag points to an index whose platform image has a *different* id. Verified with `docker image ls --tree`: a default build tags an index (`42259e91…` → linux/arm64 `ed62fb1b…`, two different ids), while `--provenance=false --sbom=false` tags a **single image** (`8174802f…`, tag points directly at it). The classic `overlay2` store (OrbStack) flattens to a single image, which is why **OrbStack was unaffected and Docker Desktop rebuilt every run**. The index-wrapped image is lost between runs (containerd-store GC / existence resolution), so `Setup` hit the `!exists` path ("Building base image (first run only)…") on every run. *(Two earlier diagnoses were wrong and corrected: the transient VS Code 404 — a separate flake fixed by `7335018` — and "the SDK can't see containerd-store images" — refuted by a live diagnostic that found the image fine.)*
- **Fix (applied):** both `docker build` invocations in `internal/runtime/docker/build.go` now pass `--provenance=false --sbom=false`, producing a plain single-platform image on both store types — a local base image has no use for SBOM/provenance attestations. **Verify:** re-run `make smoketest-full`; Docker Desktop should report "Base image built successfully" (skipped) like OrbStack, not "first run only".
- **Remaining (parked, minor):** the staleness marker `.base-image-checksum` is **host-global** (`baseImageChecksumPath` → `CacheDir()`) while images are **per-daemon**. After a Dockerfile change the first daemon to rebuild records the shared marker; a second daemon that already has an image skips `NeedsBuild` (`docker.go:321`) and keeps a **stale** image. Niche (multi-daemon only). Fix: record the build-inputs checksum as an image label read per-daemon.
- **Pointer:** `internal/runtime/docker/build.go` (both `docker build` invocations; `NeedsBuild`/`baseImageChecksumPath`/`RecordBuildChecksum` for the secondary), `internal/runtime/docker/docker.go:309/321` (Setup gate).

### DF22 — Switching Docker providers silently *recreates* a sandbox in the wrong daemon instead of warning

- **Discovered:** 2026-06-10 · **Workstream:** Apple `container` backend — container-system selector (orbstack/docker-desktop) implementation
- **Severity:** MEDIUM (no host-side data loss — the `:copy` work dir is on the host — but the original container, its agent state, and any uncommitted in-container work become unreachable; surfaces as a confusing "fresh" sandbox)
- **Disposition:** PARKED
- **Description:** A sandbox created on one Docker provider (e.g. OrbStack) records only `backend: docker` in `environment.json` — the chosen endpoint is **not** persisted (the deliberate Approach-A tradeoff for the container-system selector: orbstack/docker-desktop are input aliases that resolve to `(docker, DOCKER_HOST=<socket>)` at creation time, not first-class persisted backends). On a later `yoloai start <name>` the docker backend auto-resolves the socket. If the creating provider is **stopped** and a *different* provider is **running**, `Inspect` returns not-found → `DetectStatus` maps that to `StatusRemoved` → `Start` routes to `recreateContainer`, which silently builds a **new** container in the now-active daemon. The user sees a working-but-fresh sandbox, not an error. `attach`/`exec` do surface the enriched not-found hint (added this commit), and a fully-stopped provider yields the enriched `pingFailureError` hint — but the `start`→recreate path swallows both.
- **Fix options:** (a) cheapest — in the `StatusRemoved`→recreate path, when ≥2 Docker providers are installed, emit a notice ("recreating in <active>; if 'X' was created on a different provider, start it to reconnect the original") before recreating; needs the provider-detection signal (host docker knowledge) surfaced to the backend-agnostic lifecycle layer as a notice. (b) durable — the Hybrid-C upgrade: persist the resolved endpoint (or the container-system id) in `environment.json` next to `backend: docker` and re-inject `DOCKER_HOST` on restart, so a switched-but-still-running original re-pins exactly and a stopped original fails loudly instead of silently recreating.
- **Trigger:** the first real report of "my sandbox reset itself after I switched from OrbStack to Docker Desktop" (or vice-versa), or when persisting a per-sandbox endpoint becomes worthwhile for another reason — whichever comes first. Until then the creation-time pin + the attach/connection hints are the agreed v1 behavior.
- **Pointer:** `internal/runtime/container_system.go` (`ResolveContainerSystem`), `internal/cli/cliutil/client.go` (`BackendEnv` pin), `internal/sandbox/status/status.go:205` (not-found → `StatusRemoved`), `internal/sandbox/lifecycle/lifecycle.go` (`Start`→`recreateContainer`), `internal/runtime/docker/docker.go` (`notFound`/`pingFailureError` hints, `providerNames`).

## Policy origin

Established in [architecture-remediation.md](../archive/plans/architecture-remediation.md) and inherited by [layering-refactor.md](../archive/plans/layering-refactor.md).

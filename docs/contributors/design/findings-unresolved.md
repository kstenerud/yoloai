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

### DF18 — Live-daemon error paths unhit by the conformance suite

- **Discovered:** 2026-06-04 · **Workstream:** testing-critique (T13 split-out)
- **Severity:** LOW–MEDIUM
- **Disposition:** PARKED. (The other half of the original DF18 — "zero Seatbelt/Tart run coverage" — was **resolved 2026-06-11**; see `findings-resolved.md`. This entry is the remaining half.)
- **Description:** A class of error branches is reachable only against a live backend and stays unhit by `RunInterfaceConformance`: **dead-daemon-mid-op**, **image-missing**, **prune-failure**, and the **overlay diff/apply** error paths (overlay needs a running container for the in-container git exec). `exec-on-stopped` was already promoted to a universal conformance assertion; these remain. Note **image-missing is not actually "live error-injection"** — it's a plain integration test (create with a bogus `ImageRef` → expect a clean error); the original "needs infrastructure, not a test rewrite" framing overstated the difficulty for that one, so start there.
- **Trigger:** add error-injection cases to the docker/podman integration tier (the daemon is already required there) — image-missing first (cheapest), then prune-failure and dead-daemon-mid-op.
- **Pointer:** `internal/runtime/runtimetest/conformance_iface.go` (shared suite — add assertions here); overlay error paths in `internal/copyflow/apply.go` (`generateOverlayPatchForContext`, `ensureOverlayBaseline`).

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
- **Pointer:** `internal/runtime/container_system.go` (`ResolveContainerSystem`), `internal/cli/cliutil/client.go` (`BackendEnv` pin), `internal/orchestrator/status/status.go:205` (not-found → `StatusRemoved`), `internal/orchestrator/lifecycle/lifecycle.go` (`Start`→`recreateContainer`), `internal/runtime/docker/docker.go` (`notFound`/`pingFailureError` hints, `providerNames`).

### DF25 — containerd `Exec` returned untrimmed stdout (contract inconsistency)

- **Discovered:** 2026-06-11 · **Workstream:** testing-refactor (surfaced wiring containerd onto the shared conformance suite)
- **Severity:** LOW
- **Disposition:** ADDRESSED-IN-PLACE (2026-06-11)
- **Description:** `ExecResult.Stdout` was an undocumented contract, and backends diverged: docker (`docker.go:581`), apple, seatbelt, and tart all trim (the latter two via the shared `runtime.RunCmdExec` helper), but containerd's `exec.go` returned `stdout.String()` **raw**. The old `TestIntegration_ContainerLifecycle` papered over it with `assert.Contains` instead of `assert.Equal`. A uniform conformance suite that asserts a trimmed result surfaced the divergence.
- **Fix:** documented the trimmed contract on `runtime.ExecResult.Stdout` and made containerd's exec path trim (`strings.TrimSpace`). The shared suite now asserts the contract strictly for every backend.
- **Pointer:** `internal/runtime/runtime.go` (`ExecResult.Stdout` doc), `internal/runtime/containerd/exec.go` (trim), `internal/runtime/docker/docker.go:581` / `internal/runtime/exec.go:64` (reference impls).

### DF26 — containerd `skipIfNotAvailable` only stat'd the socket, so an unconnectable daemon failed every test

- **Discovered:** 2026-06-11 · **Workstream:** testing-refactor (running `integration-containerd` on a host with a present-but-unconnectable socket)
- **Severity:** LOW (test-infra; no production impact)
- **Disposition:** ADDRESSED-IN-PLACE (2026-06-11)
- **Description:** The containerd integration gate did only `os.Stat("/run/containerd/containerd.sock")`. On a host where the socket file exists but isn't connectable (daemon down, or the test user lacks dial permission — the socket is commonly root-owned `srw-rw----`), the stat passed and every containerd integration test then **failed** at first use instead of skipping. This breaks the host-aware contract (skip cleanly where the backend can't run). Reproduced on the Linux LXC dev host (socket present, owned by root, no Kata/KVM).
- **Fix:** `skipIfNotAvailable` now stats **and** dials the socket (`net.DialTimeout`), skipping with a clear reason when it can't connect.
- **Pointer:** `internal/runtime/containerd/integration_test.go` (`skipIfNotAvailable`).

### DF31 — Substrate `Backend` bakes in tmux + the agent monitor

- **Discovered:** 2026-06-14 · **Workstream:** public-layering (first audit pass)
- **Severity:** MEDIUM
- **Disposition:** PARKED (tracked by [public-layering.md](plans/public-layering.md) Shape stage)
- **Description:** `go list -deps` of the intended substrate island (`internal/runtime` + a backend + `internal/store`) is clean of agent/copyflow/PTY, **but still pulls `internal/runtime/monitor` and `internal/resources/tmux`** — the backend's container `Setup`/launch embeds the tmux + status-monitor Python launch convention. So even a headless `Backend.Create` ships the agent-monitoring scripts and a tmux session: "run a container" is fused with "run a tmux-wrapped, monitored agent session." This is the Phase C-full "tmux is mandatory middleware" finding re-surfacing at the substrate boundary. The cleanest split makes tmux+monitor a *session/idle refinement* injected at launch, not a substrate `Setup` default.
- **Pointer:** `internal/runtime/*/{build,setup}.go` (container bootstrap); `internal/runtime/monitor/`, `internal/resources/tmux/`. Related: Q103. **Resolution direction:** [research/container-init-delineation.md](research/container-init-delineation.md) — give Docker/Podman a neutral PID 1 (`--init`/tini, the k8s-`pause` / Seatbelt-P1 pattern) and launch the agent via exec; the VM backends are already clean.

### DF32 — No agent-free managed lifecycle (lifecycle verbs only exist agent-aware)

- **Discovered:** 2026-06-14 · **Workstream:** public-layering (first audit pass)
- **Severity:** MEDIUM
- **Disposition:** PARKED (the load-bearing carve for [public-layering.md](plans/public-layering.md))
- **Description:** `go list -deps ./internal/orchestrator/lifecycle` pulls `internal/agent` (restart relaunches the agent) and `internal/copyflow` (reset re-syncs copy dirs; status probes uncommitted copy changes). Raw `runtime.Backend` gives create/start/stop/destroy, but the *managed* lifecycle (name→instance resolution, persisted status, liveness) lives entangled with agents + the copy workflow. A power-user wanting "managed lifecycle, no agents" must drop to raw `Backend` + `store` and hand-roll the glue. Resolution: carve a substrate-level managed lifecycle (Backend + store, agent-agnostic) and let the agent-aware orchestrator layer *that* + relaunch + copy-resync on top.
- **Pointer:** `internal/orchestrator/lifecycle/{start,restart,reset}.go`; direct `internal/agent` importers — `lifecycle`, `invocation`, `state`, `provision`. Related: Q103.

### DF33 — `runtimeconfig` mixes substrate and agent-launch fields

- **Discovered:** 2026-06-14 · **Workstream:** public-layering (first audit pass)
- **Severity:** LOW–MEDIUM
- **Disposition:** PARKED (tracked by [public-layering.md](plans/public-layering.md) Shape stage)
- **Description:** The Go↔Python container config (`internal/orchestrator/runtimeconfig`) carries substrate fields (mounts, network, copy dirs) **and** agent-launch fields (`AgentCommand`, `ReadyPattern`, `Idle`) in one DTO, and the Python entrypoint always sets up tmux + launches the agent. So the substrate's container bootstrap is agent-shaped. For a clean substrate the config should split into a substrate-launch part and an agent-launch part (the module-split plan flagged this under Phase A but only closed the *import* edge, not the *schema* conflation).
- **Pointer:** `internal/orchestrator/runtimeconfig/runtimeconfig.go`; `internal/runtime/monitor/sandbox-setup.py`. Related: DF31, Q104.

### DF34 — Network isolation threaded into the containerd backend

- **Discovered:** 2026-06-14 · **Workstream:** public-layering (first audit pass)
- **Severity:** LOW
- **Disposition:** PARKED (deferred refinement; [public-layering.md](plans/public-layering.md) later cycle)
- **Description:** Network isolation / allowlist (CNI, netns, iptables) is woven into the containerd backend's startup rather than living as a standalone `netpolicy` refinement injected over the substrate. The substrate backend therefore "knows about" network policy. Lower priority than DF31/DF32 (netpolicy is a later-cycle refinement), but recorded so the substrate audit accounts for it.
- **Pointer:** `internal/runtime/containerd/` (CNI setup in startup path). Related: [public-layering.md](plans/public-layering.md) netpolicy row.

## Policy origin

Established in [architecture-remediation.md](../archive/plans/architecture-remediation.md) and inherited by [layering-refactor.md](../archive/plans/layering-refactor.md).

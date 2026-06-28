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

### DF56 — `system build --backend podman` no-ops against a stale image (build-inputs checksum is host-side, shared across backends)

- **Discovered:** 2026-06-28 · **Workstream:** egress-broker podman validation (workstream D)
- **Severity:** LOW (only bites multi-backend image work; the workaround is trivial once known)
- **Disposition:** PARKED
- **Description:** The base-image build is skipped when the recorded build-inputs checksum
  (`<layout>/cache/.base-image-checksum`, written by `RecordBuildChecksum`) matches the current
  embedded inputs. But that checksum is **host-side and backend-agnostic** — a `docker` build marks
  it "current" for *every* backend. So `yoloai system build --backend podman` silently no-ops if a
  docker build already stamped the checksum, leaving podman's actual image stale (observed: a
  3-week-old podman `yoloai-base` whose `entrypoint.py` predated the `keepalive_only` agent-free
  logic, which made the agent-free launch path look broken on podman when it was just a stale
  image). The image existence/freshness is per-backend (per daemon store); the checksum is not.
  **Workaround:** `podman rmi -f localhost/yoloai-base:latest` then rebuild — a missing image forces
  the build regardless of checksum. **Fix directions:** key the checksum per-backend (e.g.
  `.base-image-checksum-<backend>`), or have the freshness check also confirm the image exists *in
  that backend's store* before trusting the checksum.
- **Pointer:** `runtime/docker/build.go` (`RecordBuildChecksum`, `buildBaseImage` skip logic);
  the per-backend image lives in each daemon's store. Surfaced during the podman broker validation
  (see `research/egress-broker-host-reachability.md` "Rootless podman" aside).

### DF55 — `:copy` directory setup ignores `.gitignore`, copying gitignored secrets into the sandbox

- **Discovered:** 2026-06-28 · **Workstream:** credential-hygiene (raised alongside egress-broker workstream D)
- **Severity:** MEDIUM (secret exposure, but bounded — the sandbox runs an agent the user chose to run on their own machine; deliberately parked by the user to address later)
- **Disposition:** PARKED
- **Description:** `CopyDir` copies the *entire* host directory tree, filtered only by a hardcoded build-artifact/bugreport list — **`.gitignore` is not honored**. Files a user deliberately excluded from their repo (`.env`, `*.pem`, `credentials.json`, `.aws/`, local config with tokens) live on disk in the project dir and get copied into the sandbox's copy area, where the agent can read (and potentially exfiltrate) them. This defeats the user's own intent: gitignored means "not part of this project's shared surface," yet we expose it to the agent wholesale. **Fix direction:** during copy, skip paths matched by the applicable `.gitignore` set. Nuances to handle: `.gitignore` only has meaning inside a git repo (non-git `:copy` dirs have no gitignore semantics — copy as today); must respect nested `.gitignore` files, negation (`!`) patterns, and `.git/info/exclude`; the global core.excludesFile is debatable (it's user-machine state, not project intent — likely out of scope). Cleanest implementation is to enumerate via git itself (`git ls-files --cached --others --exclude-standard`) when the source is a repo, rather than re-implementing gitignore matching — this also naturally excludes `.git`-tracked-vs-ignored correctly. Interaction with diff/apply is benign: excluded files never enter the sandbox, so they can't be modified or surface in a diff. Consider an escape hatch (e.g. a `:copy-all` modifier or `--include-ignored`) for users who *do* want the ignored files.
- **Pointer:** `internal/workspace/copy.go:21` (`CopyDir` entry — fast-clone then `copyDirWalk`), `copy.go:65` (`copyDirEntry`, where per-entry exclusion is applied), `copy.go:164` (`isBuildArtifact`, the existing hardcoded filter to extend/replace); fast-clone path (`copy.go:246`/`:272` post-clone cleanup) needs the same exclusion or must fall back to the walk when a source is a gitignore-bearing repo; entry from `internal/orchestrator/create/prepare_dirs.go:274` (`setupDirContent`).

### DF54 — New verbs (`run`, `diff --json`, `sandbox_run`) lack automated E2E/smoke coverage

- **Discovered:** 2026-06-27 · **Workstream:** pre-merge audit (test-gap)
- **Severity:** LOW (the paths were verified manually on real Docker; their decision logic is now unit-tested)
- **Disposition:** PARKED
- **Description:** The orchestration happy-paths of `yoloai run` (`executeRun`/`waitForRunResult`), the `diff --json` structured output, and MCP `sandbox_run` take concrete `*yoloai.Client`/`*yoloai.Sandbox` and so aren't unit-stubbable; the smoke harness (`scripts/smoke_test.py`) doesn't exercise them either. The decision logic IS now unit-tested (commit `373e2735` — `changesFromCopyflow`, `agentHasUsableAuth`, `resolveAgentParams` downgrade, `handleSandboxRun` guards), and the full paths were verified live (real-Docker `run` success/failure exit codes + `--rm` cleanup; MCP `sandbox_run` full stdio flow), but there is no automated regression gate.
- **Trigger:** when extending the smoke matrix, or before these verbs take on more behavior — add a `run` / `diff --json` / `sandbox_run` case to `scripts/smoke_test.py` (real Docker + test agent), and/or extract a thin interface so `executeRun`/`waitForRunResult` become unit-testable.
- **Pointer:** `internal/cli/lifecycle/run.go`; `internal/cli/workflow/diff.go`; `internal/mcpsrv/tools.go`; `scripts/smoke_test.py`.

### DF53 — Tart silently ignores `-p` port mappings (port-forwarding never wired into `tart run`)

- **Discovered:** 2026-06-27 · **Workstream:** pre-merge audit (tart test-bypass cleanup)
- **Severity:** LOW (tart is a macOS-only backend with limited network features — its descriptor declares `NetworkIsolation: false`)
- **Disposition:** PARKED
- **Description:** Tart's production run path (`buildRunArgs` → `Start`) never adds any `--net-softnet*` arguments, so a user's `-p` port mappings (`cfg.Ports`) are silently dropped — a tart sandbox gets default VM networking with no port forwarding and no `--network-isolated` enforcement. This was masked by `BuildNetworkArgs`/`portForwardArgs`, which built the args correctly but **were never called in production** (dead code with passing unit tests, removed during the pre-merge audit). The unit tests gave false confidence that ports worked.
- **Trigger:** before tart is positioned for workloads that need port forwarding or network isolation — wire `BuildNetworkArgs`-equivalent logic into `buildRunArgs`, flip the descriptor's `NetworkIsolation`, and verify with real `--net-softnet` on macOS.
- **Pointer:** `runtime/tart/tart.go` (`buildRunArgs`, `Start` — no network args); descriptor `NetworkIsolation: false`.

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
- **Pointer:** `runtime/runtimetest/conformance_iface.go` (shared suite — add assertions here); overlay error paths in `copyflow/apply.go` (`generateOverlayPatchForContext`, `ensureOverlayBaseline`).

### DF24 — Stale-base detection flags the *wanted* base as "superseded" when `tart.image` pins a non-default image

- **Renumbered:** originally recorded as DF20; renumbered to DF24 on 2026-06-11 to resolve a duplicate (DF20 is canonically the gVisor world-readable-credentials finding, which every cross-reference points to). The recording commit `7e1566c` still says "DF20".
- **Discovered:** 2026-06-10 · **Workstream:** Apple `container` backend planning (side-discovery while diagnosing a leftover config override)
- **Severity:** MEDIUM (actively recommends deleting a wanted artifact; gated behind a dry-run preview + `y/N`, so not silent data loss)
- **Disposition:** PARKED
- **Description:** `staleBaseImagesFrom` classifies every installed Tart base whose repo ≠ the *current* repo as "superseded (safe to remove, no rebuild)", where current = `baseImageRepo(r.resolveBaseImage(""))` — and `resolveBaseImage` honors the `tart.image` config override. So an explicit `tart.image` pin makes the host-matched `macos-<codename>-base` look superseded, and `yoloai doctor` / `yoloai system prune --stale-bases` offer to delete the base the user actually uses. Observed 2026-06-10: a leftover `tart.image: ghcr.io/cirruslabs/macos-tahoe-xcode:26.5` (from the `-xcode` A/B) made `prune --stale-bases` offer to remove the real `macos-tahoe-base` (29.8 GiB) as "safe, no rebuild." Technically consistent with the override, but a sharp edge — nothing surfaces *which* configured image drives the "current" determination, so a config typo or leftover reads as a free cleanup.
- **Fix:** surface the configured `tart.image` (and the resolved "current base repo") in the `doctor` / `prune --stale-bases` output so the user can see *why* a base is flagged; and/or treat an explicit non-`-base` override specially — don't label the host-matched `-base` "no rebuild, safe to remove" when the only reason it is non-current is an explicit override (warn instead, or exclude it from the stale set). Keep the genuine cross-macOS supersede case (old codename after an OS upgrade) working.
- **Trigger:** the next time a custom `tart.image` is set in earnest (xcode image-mode adoption, pinning an older/newer macOS, etc.) — before that ships broadly, make the stale-base output name the driving config so it can never recommend deleting a wanted base.
- **Pointer:** `runtime/tart/diskusage.go:139` (`staleBaseImagesFrom`; `currentRepo` from `resolveBaseImage("")`), `runtime/tart/stalebases.go:23` (`PruneStaleBases`); the `doctor` / `system prune --stale-bases` surfacing.

### DF21 — Docker Desktop containerd store: BuildKit attestations make `yoloai-base` a manifest-list index that vanishes between runs (full rebuild every run)

- **Discovered:** 2026-06-10 · **Workstream:** Apple `container` backend (diagnosing repeated base-image rebuilds during `make smoketest-full`)
- **Severity:** MEDIUM (no data loss, but a full ~5-minute `yoloai-base` rebuild on *every* operation against a Docker Desktop daemon that uses the containerd image store — increasingly the default)
- **Disposition:** RESOLVED (primary, this commit); the secondary host-global-marker bug remains PARKED.
- **Root cause (confirmed empirically).** `buildBaseImage`/the profile build ran `docker build -t yoloai-base -` with no attestation flags. BuildKit's default provenance/SBOM attestations make the result a **manifest list / image index** on Docker Desktop's containerd image store: the tag points to an index whose platform image has a *different* id. Verified with `docker image ls --tree`: a default build tags an index (`42259e91…` → linux/arm64 `ed62fb1b…`, two different ids), while `--provenance=false --sbom=false` tags a **single image** (`8174802f…`, tag points directly at it). The classic `overlay2` store (OrbStack) flattens to a single image, which is why **OrbStack was unaffected and Docker Desktop rebuilt every run**. The index-wrapped image is lost between runs (containerd-store GC / existence resolution), so `Setup` hit the `!exists` path ("Building base image (first run only)…") on every run. *(Two earlier diagnoses were wrong and corrected: the transient VS Code 404 — a separate flake fixed by `7335018` — and "the SDK can't see containerd-store images" — refuted by a live diagnostic that found the image fine.)*
- **Fix (applied):** both `docker build` invocations in `runtime/docker/build.go` now pass `--provenance=false --sbom=false`, producing a plain single-platform image on both store types — a local base image has no use for SBOM/provenance attestations. **Verify:** re-run `make smoketest-full`; Docker Desktop should report "Base image built successfully" (skipped) like OrbStack, not "first run only".
- **Remaining (parked, minor):** the staleness marker `.base-image-checksum` is **host-global** (`baseImageChecksumPath` → `CacheDir()`) while images are **per-daemon**. After a Dockerfile change the first daemon to rebuild records the shared marker; a second daemon that already has an image skips `NeedsBuild` (`docker.go:321`) and keeps a **stale** image. Niche (multi-daemon only). Fix: record the build-inputs checksum as an image label read per-daemon.
- **Pointer:** `runtime/docker/build.go` (both `docker build` invocations; `NeedsBuild`/`baseImageChecksumPath`/`RecordBuildChecksum` for the secondary), `runtime/docker/docker.go:309/321` (Setup gate).

### DF22 — Switching Docker providers silently *recreates* a sandbox in the wrong daemon instead of warning

- **Discovered:** 2026-06-10 · **Workstream:** Apple `container` backend — container-system selector (orbstack/docker-desktop) implementation
- **Severity:** MEDIUM (no host-side data loss — the `:copy` work dir is on the host — but the original container, its agent state, and any uncommitted in-container work become unreachable; surfaces as a confusing "fresh" sandbox)
- **Disposition:** PARKED
- **Description:** A sandbox created on one Docker provider (e.g. OrbStack) records only `backend: docker` in `environment.json` — the chosen endpoint is **not** persisted (the deliberate Approach-A tradeoff for the container-system selector: orbstack/docker-desktop are input aliases that resolve to `(docker, DOCKER_HOST=<socket>)` at creation time, not first-class persisted backends). On a later `yoloai start <name>` the docker backend auto-resolves the socket. If the creating provider is **stopped** and a *different* provider is **running**, `Inspect` returns not-found → `DetectStatus` maps that to `StatusRemoved` → `Start` routes to `recreateContainer`, which silently builds a **new** container in the now-active daemon. The user sees a working-but-fresh sandbox, not an error. `attach`/`exec` do surface the enriched not-found hint (added this commit), and a fully-stopped provider yields the enriched `pingFailureError` hint — but the `start`→recreate path swallows both.
- **Fix options:** (a) cheapest — in the `StatusRemoved`→recreate path, when ≥2 Docker providers are installed, emit a notice ("recreating in <active>; if 'X' was created on a different provider, start it to reconnect the original") before recreating; needs the provider-detection signal (host docker knowledge) surfaced to the backend-agnostic lifecycle layer as a notice. (b) durable — the Hybrid-C upgrade: persist the resolved endpoint (or the container-system id) in `environment.json` next to `backend: docker` and re-inject `DOCKER_HOST` on restart, so a switched-but-still-running original re-pins exactly and a stopped original fails loudly instead of silently recreating.
- **Trigger:** the first real report of "my sandbox reset itself after I switched from OrbStack to Docker Desktop" (or vice-versa), or when persisting a per-sandbox endpoint becomes worthwhile for another reason — whichever comes first. Until then the creation-time pin + the attach/connection hints are the agreed v1 behavior.
- **Pointer:** `runtime/container_system.go` (`ResolveContainerSystem`), `internal/cli/cliutil/client.go` (`BackendEnv` pin), `internal/orchestrator/status/status.go:205` (not-found → `StatusRemoved`), `internal/orchestrator/lifecycle/lifecycle.go` (`Start`→`recreateContainer`), `runtime/docker/docker.go` (`notFound`/`pingFailureError` hints, `providerNames`).

### DF25 — containerd `Exec` returned untrimmed stdout (contract inconsistency)

- **Discovered:** 2026-06-11 · **Workstream:** testing-refactor (surfaced wiring containerd onto the shared conformance suite)
- **Severity:** LOW
- **Disposition:** ADDRESSED-IN-PLACE (2026-06-11)
- **Description:** `ExecResult.Stdout` was an undocumented contract, and backends diverged: docker (`docker.go:581`), apple, seatbelt, and tart all trim (the latter two via the shared `runtime.RunCmdExec` helper), but containerd's `exec.go` returned `stdout.String()` **raw**. The old `TestIntegration_ContainerLifecycle` papered over it with `assert.Contains` instead of `assert.Equal`. A uniform conformance suite that asserts a trimmed result surfaced the divergence.
- **Fix:** documented the trimmed contract on `runtime.ExecResult.Stdout` and made containerd's exec path trim (`strings.TrimSpace`). The shared suite now asserts the contract strictly for every backend.
- **Pointer:** `runtime/runtime.go` (`ExecResult.Stdout` doc), `runtime/containerd/exec.go` (trim), `runtime/docker/docker.go:581` / `runtime/exec.go:64` (reference impls).

### DF26 — containerd `skipIfNotAvailable` only stat'd the socket, so an unconnectable daemon failed every test

- **Discovered:** 2026-06-11 · **Workstream:** testing-refactor (running `integration-containerd` on a host with a present-but-unconnectable socket)
- **Severity:** LOW (test-infra; no production impact)
- **Disposition:** ADDRESSED-IN-PLACE (2026-06-11)
- **Description:** The containerd integration gate did only `os.Stat("/run/containerd/containerd.sock")`. On a host where the socket file exists but isn't connectable (daemon down, or the test user lacks dial permission — the socket is commonly root-owned `srw-rw----`), the stat passed and every containerd integration test then **failed** at first use instead of skipping. This breaks the host-aware contract (skip cleanly where the backend can't run). Reproduced on the Linux LXC dev host (socket present, owned by root, no Kata/KVM).
- **Fix:** `skipIfNotAvailable` now stats **and** dials the socket (`net.DialTimeout`), skipping with a clear reason when it can't connect.
- **Pointer:** `runtime/containerd/integration_test.go` (`skipIfNotAvailable`).

### DF31 — Substrate `Backend` bakes in tmux + the agent monitor

- **Discovered:** 2026-06-14 · **Workstream:** public-layering (first audit pass)
- **Severity:** MEDIUM
- **Disposition:** PARKED (tracked by [public-layering.md](plans/public-layering.md) Shape stage)
- **Description:** `go list -deps` of the intended substrate island (`runtime` + a backend + `store`) is clean of agent/copyflow/PTY, **but still pulls `runtime/monitor` and `internal/resources/tmux`** — the backend's container `Setup`/launch embeds the tmux + status-monitor Python launch convention. So even a headless `Backend.Create` ships the agent-monitoring scripts and a tmux session: "run a container" is fused with "run a tmux-wrapped, monitored agent session." This is the Phase C-full "tmux is mandatory middleware" finding re-surfacing at the substrate boundary. The cleanest split makes tmux+monitor a *session/idle refinement* injected at launch, not a substrate `Setup` default.
- **Pointer:** `runtime/*/{build,setup}.go` (container bootstrap); `runtime/monitor/`, `internal/resources/tmux/`. Related: Q103. **Resolution direction:** [research/container-init-delineation.md](research/container-init-delineation.md) — give Docker/Podman a neutral PID 1 (`--init`/tini, the k8s-`pause` / Seatbelt-P1 pattern) and launch the agent via exec; the VM backends are already clean.

### DF32 — No agent-free managed lifecycle (lifecycle verbs only exist agent-aware)

- **Discovered:** 2026-06-14 · **Workstream:** public-layering (first audit pass)
- **Severity:** MEDIUM
- **Disposition:** PARKED (the load-bearing carve for [public-layering.md](plans/public-layering.md))
- **Description:** `go list -deps ./internal/orchestrator/lifecycle` pulls `internal/agent` (restart relaunches the agent) and `copyflow` (reset re-syncs copy dirs; status probes uncommitted copy changes). Raw `runtime.Backend` gives create/start/stop/destroy, but the *managed* lifecycle (name→instance resolution, persisted status, liveness) lives entangled with agents + the copy workflow. A power-user wanting "managed lifecycle, no agents" must drop to raw `Backend` + `store` and hand-roll the glue. Resolution: carve a substrate-level managed lifecycle (Backend + store, agent-agnostic) and let the agent-aware orchestrator layer *that* + relaunch + copy-resync on top.
- **Pointer:** `internal/orchestrator/lifecycle/{start,restart,reset}.go`; direct `internal/agent` importers — `lifecycle`, `invocation`, `state`, `provision`. **Resolution direction:** [substrate-interface.md](substrate-interface.md) / [D84](../decisions/working-notes.md) — the agent-free managed lifecycle is the `Substrate` handle (Start/Stop/Suspend/Resume/Destroy + Launch/Exec); the agent-aware orchestrator becomes a consumer that adds relaunch + copy-resync on top.

### DF33 — `runtimeconfig` mixes substrate and agent-launch fields

- **Discovered:** 2026-06-14 · **Workstream:** public-layering (first audit pass)
- **Severity:** LOW–MEDIUM
- **Disposition:** PARKED (tracked by [public-layering.md](plans/public-layering.md) Shape stage)
- **Description:** The Go↔Python container config (`internal/orchestrator/runtimeconfig`) carries substrate fields (mounts, network, copy dirs) **and** agent-launch fields (`AgentCommand`, `ReadyPattern`, `Idle`) in one DTO, and the Python entrypoint always sets up tmux + launches the agent. So the substrate's container bootstrap is agent-shaped. For a clean substrate the config should split into a substrate-launch part and an agent-launch part (the module-split plan flagged this under Phase A but only closed the *import* edge, not the *schema* conflation).
- **Pointer:** `internal/orchestrator/runtimeconfig/runtimeconfig.go`; `runtime/monitor/sandbox-setup.py`. Related: DF31, Q104. **Resolution direction:** [substrate-interface.md](substrate-interface.md) §9 / [D84](../decisions/working-notes.md) — `ProvisionSpec` is agent-free (image/mounts/resources/network/isolation/env only); agent command/ready/idle move to the agent layer's `ProcSpec` at `Launch`.

### DF34 — Network isolation threaded into the containerd backend

- **Discovered:** 2026-06-14 · **Workstream:** public-layering (first audit pass)
- **Severity:** LOW
- **Disposition:** PARKED (deferred refinement; [public-layering.md](plans/public-layering.md) later cycle)
- **Description:** Network isolation / allowlist (CNI, netns, iptables) is woven into the containerd backend's startup rather than living as a standalone `netpolicy` refinement injected over the substrate. The substrate backend therefore "knows about" network policy. Lower priority than DF31/DF32 (netpolicy is a later-cycle refinement), but recorded so the substrate audit accounts for it.
- **Pointer:** `runtime/containerd/` (CNI setup in startup path). Related: [public-layering.md](plans/public-layering.md) netpolicy row.

### DF35 — Verify copyflow's hermetic-git seal: no in-sandbox git may write outside the sandbox

- **Discovered:** 2026-06-14 · **Workstream:** public-layering (copyflow design, D86)
- **Severity:** MEDIUM (security — needs verification; could be CRITICAL if violated)
- **Disposition:** **RESOLVED — VERIFIED CLEAN 2026-06-24** (D92 design-review remediation; should drain to `findings-resolved.md`). Every `git.NewSandbox` call site is **read/emit-only** and structurally confined to the sandbox work copy: target dirs resolve through `store.WorkDir(sandboxDir, hostPath)` under `~/.yoloai/sandboxes/<name>/` (the host original `dir.HostPath` is *never* passed to a sandbox git), and the sandbox executor never routes stdin/`RunInput` (the write path) — every writer (`ApplyPatch`/`ApplyFormatPatch`/`CheckPatch`/`CreateTag`) is on `git.NewHost` targeting `dir.HostPath`. Overlay git (`execInSandbox`, `git -C <containerPath>`) is even more confined. The seal **holds**.
- **Residual invariant (carry forward):** the host-side apply uses `git apply --unsafe-paths --directory=<original>` (`git/ops.go:220-228`), safe *only* because the patch is always yoloAI-generated from the work copy — **never an agent-supplied raw patch**. The code comment warns against routing raw patches through this path; that is the real surface to keep protected. Note in copyflow-layer.md §7.
- **Description:** The copyflow design (D86 §7) makes a hermetic-git security seal load-bearing: the git *inside* the sandbox is untrusted and must be **read + emit only** — it never writes anything outside the sandbox; changes egress *only* as diff+metadata via a read-only channel, and the trusted host-side git applies. Copy-mode `apply.go` uses `git.NewSandbox` (in-sandbox/in-VM git) on several paths (≈ lines 332, 579, 614, 785, 843, 858, 917). **[Verified — see Disposition.]**
- **Pointer:** `copyflow/apply.go` (`git.NewSandbox` call sites); contrast `git.NewHost` (the trusted apply path); the `--unsafe-paths` note at `internal/git/ops.go:220-228`. Design: [copyflow-layer.md](copyflow-layer.md) §7 / [D86](../decisions/working-notes.md).

### DF36 — Persistence is unsafe on a network filesystem; detect and warn/refuse

- **Discovered:** 2026-06-15 · **Workstream:** public-layering (persistence helper, D87)
- **Severity:** MEDIUM (data-safety)
- **Disposition:** PARKED (implement with the persistence helper)
- **Description:** The persistence model (D87) relies on POSIX advisory locking (`flock`) + atomic
  `rename`. Both are **unreliable on network filesystems** — "POSIX advisory locking is known to be
  buggy or even unimplemented on many NFS implementations," and a lock that gets lost can lead to
  silent corruption ([sqlite.org/lockingv3](https://sqlite.org/lockingv3.html); man7 `fcntl_locking`).
  Networked `$HOME` is real (corporate/university). SQLite is *worse* here (WAL needs same-host shared
  memory), so this is not a JSON-vs-SQLite escape — it's an environmental constraint. yoloAI should
  **detect the data dir's filesystem and warn (or refuse) on a network FS** rather than corrupt
  silently.
- **Pointer:** wherever the data dir is resolved (`config.Layout`); the persistence helper's open path. Design: [persistence-helper.md](persistence-helper.md) / [research/shared-state-concurrency.md](research/shared-state-concurrency.md) / [D87](../decisions/working-notes.md).

### DF37 — File-locking hardening: confirm `flock` not `fcntl`, add the fsync-durability dance

- **Discovered:** 2026-06-15 · **Workstream:** public-layering (persistence helper, D87)
- **Severity:** MEDIUM (silent-corruption / data-loss avoidance)
- **Disposition:** PARKED (verify + harden with the persistence helper)
- **Description:** Two file-locking footguns the research flagged: (1) **`fcntl`/`F_SETLK` POSIX record
  locks release when *any* fd to the file is closed** (a library that opens/reads/closes the same file
  silently drops the lock — the man page calls it "bad") and don't inherit across `fork` as expected;
  **`flock`** has sane semantics (binds to the open file description, self-cleans on crash) and is
  portable to macOS. Confirm `store/lock_unix.go` (and any other locking) uses `flock`, not `fcntl`.
  (2) Atomic `rename` gives atomicity but **not durability** — a crash can leave a zero-length file
  (the real ext4 delayed-allocation bug) unless the write does **`fsync(temp) → rename → fsync(parent
  dir)`**. Add that dance to the atomic-write path.
- **Pointer:** `store/lock_unix.go`; `internal/fileutil` (atomic write). Design: [research/shared-state-concurrency.md](research/shared-state-concurrency.md) / [D87](../decisions/working-notes.md).

### DF38 — MCP surface has no per-call credential input, and tool-arg injection collides with "agents shouldn't handle credentials"

- **Discovered:** 2026-06-16 · **Workstream:** public-layering (session-layer / trial-engine design, driven by the control-eval consumer — see `design/session-layer.md`, `~/experiments/control-eval/docs/yoloai-trial-engine-report.md` P3)
- **Severity:** MEDIUM (security — credential handling on an unbuilt surface; no shipped regression)
- **Disposition:** **RESOLVED-IN-DESIGN by [D95](../decisions/working-notes.md) ([secure-secrets.md](secure-secrets.md))**; build phased (kept here until built, per the partial-resolution rule). The dedicated design pass is done — the credential boundary is a host-side egress proxy that holds/injects/refreshes credentials so the live key never enters the sandbox; for MCP, the cleaner "supply credentials to the server at launch, tool calls carry no secrets" path is the chosen shape. The contract seam (EnvSpec credential-shape + a refresh-capable `CredentialSource`) is reserved now; the proxy builds later with netpolicy's `egress-proxy` strategy.
- **Description:** D63 established the credential model: the library does **zero ambient credential reads**; credentials arrive as an injected `Env` snapshot populated **at the edge**. The CLI edge already honors this — control-eval cleans its env and passes only the keys Claude Code needs via `--env`. The **MCP surface is also an edge**, but its tools (`sandbox_create`/`sandbox_run` — `name, workdir, prompt, agent, model`) expose **no credential input**. For a caller (control-eval now, a daemon later) to inject per call, the tools need an explicit `env`/`credentials` input **and** the MCP edge must enforce the same no-ambient-read discipline (never fall back to the MCP *server's* own host env). Such a param must be treated as a **secret** — redacted from any tool-call logging/tracing (local stdio transport doesn't cross a new trust boundary, but the key must not land in logs).
  **The wrinkle (load-bearing, the reason this is PARKED not just a TODO):** MCP servers are designed for **agents** to call, and an agent should not be handling raw credentials — so passing a real API key as a *tool-call argument* is architecturally suspect. A cleaner alternative: supply credentials to the **MCP server at launch** (env/config), so it performs all operations under those fixed credentials and tool calls carry no secrets. That wants a proper **secure-secrets-handling** design. The upcoming **API-key (metered JV key) + adversarial-agent** context raises the stakes: a real billable key inside an untrusted sandbox makes exfiltration-prevention (network-isolation allowlist) load-bearing, not theoretical.
- **Trigger:** when the concurrent MCP orchestration surface (trial-engine P3) is taken up, or when a secure-secrets model is designed — whichever first.
- **Pointer:** `internal/mcpsrv/tools.go`, `internal/cli/mcp/` (tool schemas — add the credential input + no-ambient discipline). Credential model: [D63] (`Env` snapshot, `SecretsStagingDir`); principal/credential-bundle [D58]/[D63]. Design context: [session-layer.md](session-layer.md).

### DF39 — `$HOME` credential files are the last implicit ambient-credential bleed into the sandbox

- **Discovered:** 2026-06-16 · **Workstream:** public-layering (session-layer / trial-engine design)
- **Severity:** LOW–MEDIUM (security — implicit host credentials enter the sandbox; matters most for untrusted agents on a real key)
- **Disposition:** **RESOLVED-IN-DESIGN by [D95](../decisions/working-notes.md) ([secure-secrets.md](secure-secrets.md))**; build phased (kept here until built). Under D95 the `$HOME` credential mount becomes **caller-controlled and filtered** (never implicit) — the caller fully controls what credential material enters, and where an agent authenticates via the proxy-injected path, no host credential file enters at all.
- **Description:** yoloAI bind-mounts the agent's host credential/state directory (e.g. `~/.claude`) into the sandbox so the in-container agent authenticates. After D63 removed ambient credential reads from the library proper — and with the CLI edge otherwise cleaning the env to only required keys — this `$HOME` mount is the **last implicit ambient-config source**. It contradicts the caller-injects-everything model, and in the adversarial-agent + real-JV-key world it means the user's **actual host credentials can be mounted into an untrusted sandbox** (leak/exfil vector + an unaccounted auth path that may not even be the intended billing principal). Eventual shape: the caller fully controls what credentials enter; the `$HOME` credential mount becomes **opt-in**, not implicit.
- **Trigger:** when API-key / adversarial usage becomes routine (the Anthropic JV engagement), or when DF38's MCP credential model is designed — whichever first.
- **Pointer:** the agent-state / credential bind-mount wiring (per-agent definition `state directory` → provision mount setup); contrast the env-var credential path under [D63]. Related: DF38.

### DF40 — seatbelt/tart launch leaves the host-run tmux+agent attached to the caller's controlling terminal (macOS terminal-corruption sibling of the fixed docker bug)

- **Discovered:** 2026-06-18 · **Workstream:** terminal-corruption investigation (external concurrent trial harness; the **docker/Linux** half was root-caused and fixed on `main` in `f208b32` — `WithTerminal` only enters raw mode when stdin *and* stdout are terminals)
- **Severity:** MEDIUM (terminal corruption / usability — staircased output, dead Ctrl-C; **seatbelt and tart only**, i.e. macOS)
- **Disposition:** PARKED — **needs a macOS host to reproduce + verify; cannot be tested or fixed from Linux** (both backends are macOS-only). Recorded so a macOS-running agent can pick it up.
- **Description:** The fixed docker bug was a host-side raw-mode race in the CLI's `WithTerminal`. The **seatbelt** and **tart** backends have a *separate, structurally similar* exposure on the **backend launch** path (not `WithTerminal`): each starts its long-lived host process — `sandbox-exec … python3 sandbox-setup.py` (seatbelt) / `tart run` (tart), which on these backends runs **tmux + the agent on the host** — with `cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}` and **no `Setsid`**. `Setpgid` makes a new process group but stays in the **same session**, so the detached host-run tmux/agent keeps the caller's **controlling terminal** (`/dev/tty`) and can leave it in raw mode after `new`/`start` returns. `sysexec` already leaves `cmd.Stdin` nil (→ `/dev/null`), so the residual vector is the controlling terminal via Setpgid-without-Setsid, **not** stdin. **Unverified** — needs a macOS host (no repro possible on Linux/docker, where tmux runs *inside* the container and never touches the host tty).
- **Likely fix:** launch these host processes detached from the controlling terminal — `Setsid: true` (new session, no controlling tty) and/or `cmd.Stdin = nil`/`/dev/null` made explicit — mirroring the docker fix's intent (a non-interactive launch must not mutate the caller's terminal). Verify tmux still starts cleanly under the new session on both backends.
- **Trigger:** the next time anyone runs yoloai **non-interactively on macOS** with `--backend seatbelt` or `--backend tart` (e.g. a batch/trial harness), or any macOS terminal-corruption report — reproduce there, then apply + verify the detach.
- **Pointer:** `runtime/seatbelt/seatbelt.go:324`, `runtime/tart/tart.go:360` (the `SysProcAttr{Setpgid: true}` launches). Contrast the fixed docker path: `internal/cli/cliutil/streams.go` (`WithTerminal`, `main`@`f208b32`).

### DF41 — the carve orphans the agent-free root work fused into `entrypoint.py` (each layer must claim its piece)

- **Discovered:** 2026-06-24 · **Workstream:** public-layering design-review remediation ([D92](../decisions/working-notes.md))
- **Severity:** MEDIUM (load-bearing for the D88 carve; not a runtime bug yet — a design hole that would orphan working code at Shape)
- **Disposition:** PARTIALLY RESOLVED — **Docker/Launch path: dissolved by E3** (secrets delivered as
  `ProcSpec.Env`; no host-staged `/run/secrets` dir, no `.secrets-consumed` marker — nothing to re-home for
  the secrets piece on this path; implemented + verified on real Docker, commit 163533a9). UID-remap,
  overlay-mount → substrate; `isolate_network` → netpolicy; all per D92 design — pending Shape
  implementation. **Legacy backends** (containerd, tart, seatbelt): secrets-read + marker still present in
  `entrypoint.py`, re-home to envsetup as Go-driven steps when those backends are carved. The UID-remap,
  overlay-mount, and `isolate_network` pieces remain PARKED pending Shape for all backends.
- **Description:** `entrypoint.py::main()` does four **agent-free** root operations before `gosu`-exec'ing the agent: **UID/GID remap** (`:70-103`), reading **staged secrets** from `/run/secrets` + the **`.secrets-consumed` marker handshake** (`:106-152`), **network isolation** (`:180-286`), and the **in-container overlay mount** (`:289-368`). The D88 carve makes PID 1 neutral and demotes the agent session to a `Launch` — which would **orphan all four**, because they live inline in the agent-facing Python with no Go/abstraction owning them. Verified across the tree. Rehoming (D92): **UID-remap + overlay-mount → substrate** (provisioning); **`isolate_network` → netpolicy** (its `ip-filter` strategy — already designed); **secrets read + consumed-marker → envsetup** (credential delivery + its teardown half). Each spec must now **explicitly claim** its piece.
- **Pointer:** `runtime/docker/resources/entrypoint.py` (`main` `:393-446`; `remap_uid`, `read_secrets`, `signal_secrets_consumed`, `isolate_network`, `apply_overlays`). Go path-computation only: `collectOverlayMounts` (`orchestrator/create/prepare_dirs.go:434`). Resolution: [backend-topology.md](backend-topology.md), substrate/netpolicy/envsetup specs.

### DF42 — the in-container overlay mount has no owning abstraction and no explicit teardown

- **Discovered:** 2026-06-24 · **Workstream:** public-layering design-review remediation (D92)
- **Severity:** MEDIUM
- **Disposition:** PARKED (substrate claims it per D92; teardown to add at Shape)
- **Description:** The `:overlay` mode's actual `mount -t overlay` (with the VirtioFS→tmpfs fallback for macOS Docker Desktop) is **inline in `entrypoint.py::apply_overlays` with zero Go ownership** — verified: no `mount -t overlay`/`umount`/`Unmount` anywhere in the Go tree; Go only computes the lower/upper/work/merged path strings. So no layer conceptually owns the mount today (it's owned by "whatever runs `entrypoint.py`"), and there is **no explicit unmount** — the overlay/tmpfs is reclaimed only by kernel namespace teardown on container destroy. The carve must give the mount an owner (substrate, per D92) **and** an explicit unmount on the teardown path (today implicit-via-destroy; the carve must not lose it).
- **Pointer:** `entrypoint.py:289-368` (`apply_overlays`); `orchestrator/create/prepare_dirs.go:434` (`collectOverlayMounts`, path-strings only); copyflow `:overlay` (D86 §3). Related: DF41.

### DF43 — seatbelt/tart keep staged secrets at rest for the sandbox lifetime; container path has a narrow crash-leak

- **Discovered:** 2026-06-24 · **Workstream:** public-layering design-review remediation (D92)
- **Severity:** LOW (DOWNGRADED from MEDIUM — Docker now stages no host file at all post-E3; at-rest is moot
  for single-user/ephemeral use)
- **Disposition:** DOWNGRADED — decided NOT to emit a runtime warning. Reasoning (D93): at-rest hygiene is
  not a default concern on the single-user/ephemeral model (the staged secret is the user's own `0600` file;
  Docker/Launch path stages no host file at all post-E3). The multi-principal-daemon case is the embedder's
  concern, addressed by the `SecretsStagingDir` knob and surfaced in integrator documentation if anywhere.
  **seatbelt/tart** still persist secrets to `<sandbox>/secrets/` for the sandbox lifetime — that is a
  real-but-non-default concern documented in `envsetup.md §5` for integrators. The secure-secrets build
  (DF38) remains the durable fix.
- **Description:** The **container** backends stage secrets in an ephemeral `os.MkdirTemp(…, "yoloai-secrets-*")` deleted via `defer os.RemoveAll` after the consumed-marker handshake — and **post-E3 (Docker/Launch path) no host file is staged at all** (credentials delivered as `ProcSpec.Env`). But **seatbelt and tart write secrets into the *persistent* `<sandboxPath>/secrets/`**, on disk for the **whole sandbox lifetime**, removed only on destroy. The legacy container path also has a narrow crash-leak: a SIGKILL between `MkdirTemp` and the deferred remove leaves `0600` files in `/tmp` with no startup sweep for stale `yoloai-secrets-*`.
- **Pointer:** Docker/Launch path: no host-staged dir (E3, commit 163533a9); legacy container `provision.go:33`, `launch.go:52-54,201-217`; seatbelt `seatbelt.go:206-225`; tart `tart.go:1196-1215`, `:456`. Related: DF38, DF39.

### DF44 — the Launch'd session-runner does not survive the launching client's exit

- **Discovered:** 2026-06-24 · **Workstream:** public-layering Shape, live smoke of the S3 carve re-route
- **Severity:** HIGH (silently breaks idle/completion detection + the secrets handshake; only caught because the smoke ran on real docker — the unit test cannot see `docker exec` lifetime)
- **Disposition:** RESOLVED-VERIFIED (`cc78ac86`; end-to-end on real docker — runner persists, status-monitor up, both markers written, `agent-status.json` populated, agent interactive). Drain to `findings-resolved.md` at next tidy. **Two root causes, not one:** (1) process-lifetime coupling — fixed with a first-class `ProcSpec.Detached` (`ContainerExecStart{Detach:true}` + stdio → `/yoloai/logs/session-runner.log`); (2) a **readiness race** the smoke matrix then exposed — a runner launched *during* entrypoint root-setup (UID remap / `chown -R` / network) is silently killed even when detached (a trivial command or a post-provision launch both survive). Fixed with a `.substrate-ready` marker the keepalive entrypoint writes after root-setup (before exec'ing the holder); `startViaLaunch` waits for it before Launch, erroring on timeout (substrate `Ready()` semantics).
- **Description:** S3 launches `sandbox-setup.py` via `ProcessLauncher.Launch`, implemented (S2a) as an **attached** `docker exec` (`ContainerExecAttach`). The runner is long-lived (it sets up tmux, launches the agent, runs the status-monitor, then blocks on `tmux wait-for`). But an attached exec is bound to its client connection: when `yoloai new` returns, the connection drops and the runner is **killed mid-`main()`**. Smoke evidence: PID-1 holder + tmux + `claude` alive, but **no `sandbox-setup.py` process**, `sandbox.jsonl` stops at `entrypoint.keepalive_only`, `agent-status.json` = `{}`, `monitor.jsonl` empty (status-monitor never ran), `.secrets-consumed` **never written** (host `waitForSecretsConsumed` stalls to its timeout; env/`/run/secrets` credentials unreliable — the smoke's claude only authed via the file-mounted `~/.claude`). The agent survives **only** because tmux self-daemonizes. The S2a "drain streams to discard" was a band-aid for the demux goroutine; the real issue is **process-lifetime coupling**. "A durable process that outlives its launcher" is a *general* substrate need (yoloAI's defining trait: the box runs on after the CLI exits) → it belongs in the `Launch` primitive, not bolted on at the call site. Also recorded as a docker backend idiosyncrasy.
- **Pointer:** `runtime/docker/launch.go` (the attached `Launch`), `internal/orchestrator/launch/launch.go::startViaLaunch`, `runtime/runtime.go` (`ProcSpec`). Sibling: [substrate-interface.md](substrate-interface.md) §ProcSpec.

### DF45 — base-image build lock is keyed by data-dir but the image tag is global to the docker daemon

- **Discovered:** 2026-06-24 · **Workstream:** public-layering Shape (concurrency question raised during the smoke)
- **Severity:** LOW (benign redundancy, **not** corruption — surfaced for the multi-principal/[D62](../decisions/working-notes.md) direction)
- **Disposition:** PARKED (single-data-dir behavior is correct; **Trigger:** the multi-principal daemon that serves several data dirs against one docker daemon)
- **Description:** `Setup` serializes base-image builds with a proper double-checked `flock`: acquire `layout.DockerBaseLockPath("yoloai-base")` → re-check `imageExists` + `NeedsBuild` **inside** the lock → build only if needed → write the checksum inside the lock. So concurrent `yoloai new` within one data dir **cooperate** (one builds, the rest block then skip — no double build, no checksum race, no tag stomp). BUT the lock path derives from the **data-dir** (`layout`), while the image tag `yoloai-base` is **global to the docker daemon**. Two `yoloai new` with *different* `--data-dir` against the *same* daemon (the D62 multi-principal case) do **not** serialize on this lock → redundant concurrent `docker build` of the same global tag, last-write-wins. Benign (wasted work; per-data-dir checksum files don't corrupt each other), but a latent inefficiency the multi-tenant work should account for — e.g. namespace the tag per principal, or key the lock on the global image name rather than the data dir. Ties into the [shared-state-concurrency](research/shared-state-concurrency.md) research (D87): "is the lock keyed to the same scope as the resource it guards?"
- **Pointer:** `runtime/docker/docker.go:332` (`Setup`, the double-checked lock), `runtime/docker/base_lock.go` (`AcquireBaseLock` → `DockerBaseLockPath`), `runtime/docker/build.go:42-54,134` (checksum). Tart mirrors the same pattern.

### DF46 — in-place agent relaunch restarts the agent but not the status-monitor (idle detection dies)

- **Discovered:** 2026-06-24 · **Workstream:** public-layering Shape, S3.3 restart-brings-it-back verification (real-docker smoke)
- **Severity:** MEDIUM (idle/done detection silently dead after an in-place relaunch — matters for scripted/completion use, e.g. `yoloai wait`; interactive use is unaffected since the agent itself returns fine)
- **Disposition:** RESOLVED-VERIFIED — fix **(C)**, the durable monitor (`status-monitor.py` no longer exits on pane-death; it records `done` and keeps watching, re-detecting `done→active/idle` on respawn). Verified end-to-end on real docker: kill agent → monitor survives + status `done`; `yoloai start` → agent back + same monitor + status recovered to `idle`. Drain to `findings-resolved.md` at next tidy. **Pre-existing, not a carve regression** — but the carve made in-place relaunch the common, reliable path (box always stays up), so the gap surfaced. Fix (C) chosen over (A)/(B) because it matches the carve thesis: the session (runner + tmux + monitor) is the durable thing the agent is launched into, and it's the right foundation for the tier-2 completion work.
- **Description:** `status-monitor.py` is **one-shot**: it watches the tmux pane and, when the agent exits, writes `status:done` + exit code and **exits** (`status-monitor.py:615-695`). It is launched **only** by `sandbox-setup.py` at initial setup (`:1325`). The terminal-status relaunch path — `Start` on a `Done`/`Failed` agent → `handleTerminalStatus` → `relaunchAgent` — does `tmux respawn-pane -t main -k <agentCmd>` to bring the agent back, but **never restarts the monitor** (no monitor reference anywhere in `lifecycle/`). **Verified on real docker:** kill the agent → box survives (carve holder + session-runner persist) → status flips to `done, exit_code 143` → `yoloai start` → agent returns and is interactive, **but the monitor is gone and `agent-status.json` is frozen at the prior `done`** — so idle/done detection no longer tracks the relaunched agent. Three fix directions: **(A)** `relaunchAgent` also re-launches the monitor (host-driven, matches today's model); **(B)** route relaunch through the persisting session-runner (carve-aligned: the runner owns the session); **(C)** make the monitor durable across agent runs (don't exit on pane-death; re-detect `done→active` on respawn).
- **Pointer:** `runtime/monitor/status-monitor.py:615-695` (exits on `pane_dead`); `runtime/monitor/sandbox-setup.py:1188,1325` (`launch_monitor`, setup-only); `internal/orchestrator/lifecycle/restart.go:277` (`relaunchAgent`, no monitor restart); `start.go:301` (`Done`/`Failed` → `handleTerminalStatus`).

### DF47 — E3 loose ends: vestigial `.secrets-consumed` write in `sandbox-setup.py`; `YOLOAI_SECRET_KEYS` visible in tmux env

- **Discovered:** 2026-06-25 · **Workstream:** public-layering Shape, E3 env-delivery verification (commit 163533a9)
- **Severity:** LOW (harmless litter + minor info-leak; neither is a correctness or security regression)
- **Disposition:** **RESOLVED 2026-06-25, commit 4908b11b** (should drain to `findings-resolved.md`). Both fixed + verified on real Docker: (a) `DockerBackend.writes_consumed_marker = False` gates the marker write off for the Docker/Launch path (legacy backends keep it); (b) `read_secrets_from_env` pops `YOLOAI_SECRET_KEYS` from `os.environ` before tmux starts, so it never reaches the agent's panes. Verified: `ANTHROPIC_API_KEY` still reaches the agent (no regression), `YOLOAI_SECRET_KEYS` absent from the tmux env, no marker for Docker.
- **Description:** Two small loose ends left after E3 on the Docker/Launch path: **(a)** `sandbox-setup.py`
  still **writes the `.secrets-consumed` marker** (`:signal_secrets_consumed`) even on the Docker/Launch path
  where the host no longer waits for it — the host's `waitForSecretsConsumed` logic was removed by E3, so the
  write is vestigial litter under `~/.yoloai/.../logs/`. Harmless, but should be gated off for Docker (the
  marker is still meaningful for legacy backends). **(b)** The `YOLOAI_SECRET_KEYS` sentinel (key *names*
  only, not values) passed in `ProcSpec.Env` is visible in the agent's tmux environment after
  `sandbox-setup.py` starts tmux — could be unset after the secrets are consumed to reduce the info-footprint
  in the session env.
- **Pointer:** `runtime/docker/resources/sandbox-setup.py` (`signal_secrets_consumed`); the
  `YOLOAI_SECRET_KEYS` env var set in `internal/orchestrator/launch/launch.go` (or wherever E3 injects the
  sentinel into `ProcSpec.Env`). Related: DF41, DF43.

### DF49 — `yoloai run` can't yet run workdir-less (the "agent just makes API calls" case) — the create pipeline assumes workdir is Dirs[0]

- **Discovered:** 2026-06-26 · **Workstream:** Phase 1a-i (D100, the `run` verb)
- **Severity:** LOW
- **Disposition:** PARKED
- **Description:** The `run` design (D100) allows an optional workdir — a headless agent that
  only makes API calls needs no project dir. But the create pipeline bakes in "the workdir is
  `Dirs[0]`": `meta.Workdir()` is `Dirs[0]`, `setupWorkdir`/baseline/mount/`working_dir` all derive
  from `workdir.Path`, and an empty `Path` resolves to an empty mount path (`ResolvedMountPath()`
  returns `""`). A clean no-workdir mode (skip workdir provisioning, run in `/home/yoloai`, no diff
  target, `ChangeState` = not-applicable) means breaking that invariant across many readers — too
  broad for 1a-i. **Interim:** `run` requires a workdir like `new` (enforced in `runRunCmd`; the
  positional parser stays a pure split that accepts name-only). The no-workdir user just passes a
  throwaway dir or `.`.
- **Pointer:** `internal/cli/lifecycle/run.go` (`runRunCmd` workdir guard); the invariant lives in
  `internal/orchestrator/create/prepare_dirs.go` (`setupWorkdir`), `internal/orchestrator/state/state.go`
  (`DirSpec.ResolvedMountPath`), and every `meta.Workdir()` reader.

### DF50 — a headless agent with a present-but-invalid credential can still hang; the durable fix is a headless launch with no answerable TTY

- **Discovered:** 2026-06-26 · **Workstream:** Phase 1a (D101, headless-auth fallback)
- **Severity:** LOW
- **Disposition:** PARKED
- **Description:** D101 gates headless on *observed* auth (`agentHasUsableAuth`), which covers the
  common no-auth case (→ TTY fallback). But "credential present" ≠ "credential valid": an expired
  token still presents a file/env var, so an agent that re-authenticates on expiry (Gemini, Codex)
  could still launch a login/browser flow and **hang** in a headless pane. The auth-presence check
  can't detect validity. The durable, agent-agnostic fix is to run headless with **no answerable
  interactive TTY** (close stdin / no PTY the agent can block on), so any interactive login attempt
  fails fast instead of stalling — but today the headless flow runs the agent in the tmux pane (a
  PTY) to reuse pane-death detection (D100), so it *has* an answerable terminal. This ties to the
  session-carve's no-TTY headless mode. Until then the auth-presence gate + `run --tty` escape hatch
  are the mitigation.
- **Expired-precedence angle (broker, 2026-06-28).** The same "present ≠ valid" blindness governs
  credential *selection*, not just the headless hang. Auth gating keys on env-var/file **presence**
  via `HasAnyAPIKey`: when any of an agent's `APIKeyEnvVars` is set, the `AuthOnly` on-disk seed
  (Claude's `~/.claude/.credentials.json`) is suppressed (`shouldSkipSeedFile`), and the broker's
  `SelectCredential` picks the first *present* env credential. So env beats file unconditionally. The
  benign case (file expired, env valid) resolves correctly — the stale file is never seeded and the
  valid env credential is brokered. The footgun is the inverse: **env credential present but
  expired/invalid while the on-disk file is still valid** → the good file is suppressed and the dead
  env credential is brokered, so the agent 401s upstream despite a working credential existing on
  disk. Pre-existing (env-over-file precedence predates brokering; the broker just forwards the
  selected credential faithfully). A real fix needs validity awareness, not just presence — the same
  root cause as the headless-hang variant above.
- **Pointer:** `internal/orchestrator/create/create.go` (`agentHasUsableAuth`); the headless launch
  runs in the tmux pane via `runtime/monitor/sandbox-setup.py` (`launch_agent`). Expired-precedence:
  `internal/envsetup/envsetup.go` (`HasAnyAPIKey`, `shouldSkipSeedFile`), `internal/agent/agent.go`
  (`BrokerConfig.SelectCredential`). Related: D100, D101, D105, session-layer.md.

## Policy origin

Established in [architecture-remediation.md](../archive/plans/architecture-remediation.md) and inherited by [layering-refactor.md](../archive/plans/layering-refactor.md).

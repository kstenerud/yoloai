> **ABOUTME:** Active queue for findings discovered mid-workstream that are not yet resolved.
> Critical findings escalate immediately; everything else parks here until the next re-audit.

# Discovered Findings

Findings that turned up mid-workstream (architecture-remediation, layering-refactor, or any future plan) and were **not** in the originating audit. Per the discovered-findings policy:

- **Critical findings escalate immediately, do not park.** Critical = observable data loss, security issues, observable regressions in shipped behavior, or anything that would block the current release.
- **Everything else parks here** until the next re-audit checkpoint. Don't expand a workstream's scope to absorb new findings.
- **Park the fix, never the verification** (D119). The no-scope-creep rule above governs the *remedy*. Establishing whether the defect is real is not scope creep — it is what makes the entry worth writing. File what is true, not what is plausible: an unverified finding is worse than none, because it occupies the slot and the next reader inherits a guess wearing the costume of a result. If a check is cheap (a grep, a unit test, a run on hardware you already have), run it before filing; DF98 is the worked example of not doing so. If it genuinely isn't cheap, say so explicitly and name the check that would settle it.
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

`<N>` comes from **`scripts/next-id.sh DF`**, which scans all four findings sinks. Don't grep for
the highest DF yourself: the sink you forget is where the duplicate comes from, and a partial grep
is worse than none because it also supplies the confidence.

## Findings

### DF82 — Credential broker (D105/D106) is architecturally general but only wired for the Claude agent

- **Discovered:** 2026-07-14 · **Workstream:** sandboxing-blog research (gap analysis). **Updated 2026-07-14:** Gemini + Codex wired (D115); single-provider generalization done. Aider + OpenCode remain (multi-provider).
- **Severity:** LOW–MEDIUM (no security hole — un-brokered agents deliver the API key directly, the pre-D105 baseline behavior — but a real gap versus the "isolate environment, not just files" thesis the broker exists to support)
- **Disposition:** PARTIALLY ADDRESSED (D115) — the broker redirect is now per-agent (`PlaceholderHeader` + env-or-file base-URL delivery) and **Gemini + Codex are wired and tested**. Still PARKED for the two **multi-provider** agents (Aider, OpenCode).
- **Description:** `internal/broker` and `internal/credential` (D105/D106) were built explicitly general but only `"claude"` populated `Broker`. **D115 generalized the redirect** (removed the hardcoded `StripHeaders: ["Authorization"]`, added `BaseURLFile` for CLIs with no base-URL env var) and wired **`gemini`** (env `GOOGLE_GEMINI_BASE_URL`, header `x-goog-api-key`, placeholder in `GEMINI_API_KEY`) and **`codex`** (config.toml `openai_base_url`, `Authorization: Bearer`, Responses API). **Remaining:** `"aider"` and `"opencode"` are **multi-provider** — each provider (anthropic/openai/gemini/…) has its own upstream, base-URL target, and auth header, so brokering them needs a multi-upstream generalization the current single-fixed-upstream injector doesn't do. `aider` redirects via per-provider env vars (`ANTHROPIC_API_BASE`, `OPENAI_API_BASE`, `GEMINI_API_BASE`; Gemini needs a `?key=` query fallback for pre-Nov-2025 LiteLLM). `opencode` redirects via per-provider `opencode.json` `provider.<id>.options.baseURL` and carries an unresolved upstream bug (custom `baseURL` drops the Anthropic key, sst/opencode #21737 — prefer `@ai-sdk/openai-compatible` for the Anthropic leg).
- **Trigger / fix:** revive when someone picks up the **multi-provider broker generalization** (the injector fronting N upstreams with per-provider header routing + a query-param injection mode for old-LiteLLM Gemini), or when a user requests brokering for Aider/OpenCode. Verified per-provider specs are in D115's research. AWS/non-LLM request-signing is a separate reserved workstream (`credential.RequestSigner`), not part of this finding.
- **Pointer:** `internal/agent/agent.go` (`gemini`/`codex` `Broker`; `aider`/`opencode` still lack it), `internal/agent/broker_codex.go`; `internal/orchestrator/launch/launch.go` (`buildInjectorSpec`, `patchBrokerBaseURLFile`); `docs/contributors/decisions/working-notes.md` (D105, D106, **D115**).

### DF79 — Smoke gate fails on transient daemon-connect errors instead of retrying the retryable class

- **Discovered:** 2026-07-06 · **Workstream:** v0.7.0 release-gate flake investigation
- **Severity:** MEDIUM (false-negative release gate — a real release was repeatedly blocked by known-transient rootless-podman errors that a fresh run passed; not a product bug)
- **Disposition:** PARKED
- **Description:** The smoke harness treats a transient backend-daemon error (rootless-podman `containers/create: EOF`, `runc create: no mapping for uid 0` on restart — see DF80) the same as a hard failure. Some legs retry once; others (e.g. `stop_start`) do not, and even the retry re-hits the same overloaded daemon — so a transient blip fails the whole release gate. During the v0.7.0 cut this repeatedly failed podman/heavy-backend legs that a subsequent run passed. The harness already fingerprints failure causes (`FINGERPRINTS`, incl. the new 529-overload fingerprint, commit `d121811d`); it should also **classify daemon-connect/`EOF`/uid-0 errors as retryable** and back off + retry them, distinct from a genuine agent/product failure.
- **Trigger / fix:** in `scripts/smoke_test.py`, add a retryable-error classifier (daemon `EOF` / connection-refused / uid-0-no-mapping) with bounded backoff + retry, applied uniformly across test types; keep genuine product failures non-retried. Surface "retried N× (transient daemon error)" in the summary so a flaky-but-green run stays visible.
- **Pointer:** `scripts/smoke_test.py` (`FINGERPRINTS`, the per-leg run/retry loop); DF80 (the underlying podman behavior).

### DF80 — Rootless podman: socket `EOF` on container create and `uid 0` no-mapping on restart, under concurrent load

- **Discovered:** 2026-07-06 · **Workstream:** v0.7.0 release-gate flake investigation
- **Severity:** LOW–MEDIUM (intermittent; rootless-podman only; recovers on a fresh run — but fails the gate, see DF79)
- **Disposition:** PARKED
- **Description:** Under the concurrent churn of a full smoke matrix, rootless podman intermittently (a) closes its API socket mid-`containers/create` (`error during connect: Post .../podman.sock/.../containers/create: EOF`), and (b) on a restart-recreate fails runc with `user namespaces enabled, but no mapping found for uid 0`. Both are rootless-podman/runc **service** instabilities, not yoloAI code — the podman runtime path is byte-identical across the affected runs, and they clustered on the isolation/restart legs. yoloAI's launch rollback (D114) already cleans up the partial launch afterward, so a retry is clean. Not obviously fixable inside yoloAI; the practical mitigation is harness retry (DF79).
- **Trigger / fix:** primarily mitigated by DF79 (retry the transient class). If it becomes chronic outside the smoke: investigate rootless-podman service headroom / create serialization / concurrency caps, and add a `backend-idiosyncrasies.md` entry (the rootless keep-id family already has three).
- **Pointer:** `runtime/podman/podman.go` (`UsernsMode` keep-id); `docs/contributors/backend-idiosyncrasies.md` (existing rootless keep-id entries); DF79 (harness mitigation).

### DF67 — Copy-mode work-copy host-git still runs on apple + seatbelt + the broken-metadata probe (DF66 residuals)

- **Discovered:** 2026-06-29 · **Workstream:** DF66 (C1) implementation — host git on the agent-controlled work copy. **Updated 2026-07-04** (added apple; corrected the probe analysis; fsmonitor now globally disabled). Fix designed in [plans/confine-host-side-git.md](plans/confine-host-side-git.md) (+ [macOS build brief](plans/confine-host-side-git-macos-build.md)).
- **Severity:** LOW (was MEDIUM). Apple + seatbelt are now confined (D113, merged 2026-07-05), leaving only the broken-metadata-probe path — low-exploitability: `.meta` lives outside the sandbox, so the agent can't corrupt it to trigger the host-git path.
- **Disposition:** PARKED — **paths (1) seatbelt and (2) apple RESOLVED by D113** (seatbelt runs work-copy git under a dedicated tight `sandbox-exec` profile; apple dispatches it into the per-container VM via `GitExecer`). **Only path (3), the broken-metadata probe, remains.** Container backends were already fixed (DF66).
- **Description:** DF66 routed copy-mode work-copy git into confinement for docker/podman/containerd (+ tart, in-VM). Three paths still run host git on the agent-controlled work copy with attribute-bound **filter/textconv drivers live** (only hooks + fsmonitor are neutralized): **(1) seatbelt** has no container to exec into, so `git.NewSandbox` falls back to host git — a planted `.git` filter/textconv driver executes on the macOS host (under `sandbox-exec`, bounded by the SBPL profile, which caps-F5 flags as too permissive). **(2) apple** — verified 2026-07-04 — has `GitExecInConfinement:false` and no `GitExecer`, so it *also* falls back to host git; this was not previously recorded. **(3)** `status.DetectChanges`/`ProbeWorkData` run host `git status --porcelain` on the work copy for broken/unreadable-metadata sandboxes only (the normal path uses the confined `workprobe.HasUnappliedWorkVia`).
- **Partially addressed (2026-07-04):** `runtime.GitHardeningArgs` now also emits `-c core.fsmonitor=false`, closing the `core.fsmonitor=<command>` vector on every host-side (and in-confinement) invocation. This does **not** close the clean-filter vector: contrary to an earlier note, `git status` **does** run `filter.<name>.clean` when a stat-cache miss forces a re-hash (a same-size change with a racy stat entry — verified empirically), and filters can't be blanket-disabled via `-c` without knowing driver names.
- **Trigger:** apple — implement `GitExecer` via the existing `container exec` + set `GitExecInConfinement` (small). seatbelt — `sandbox-exec`-wrapped `GitExec` + a dedicated tight git SBPL profile (caps-F5: deny `mach-lookup`, scope FS, restrict `process-exec`). probe — route through the confined path when a backend is resolvable, or replace `status` with a filter-free dirtiness check (fsmonitor is done; the clean-filter path is what remains). Exploitability of the probe is low: `.meta` lives outside the sandbox, so the agent can't corrupt it to trigger the path.
- **Pointer:** [plans/confine-host-side-git.md](plans/confine-host-side-git.md); `runtime/runtime.go` (`GitHardeningArgs`, `GitRunsInConfinement`); `runtime/apple/apple.go` + `runtime/seatbelt/` (missing `GitExecer`); `internal/git/git.go` (`NewSandbox` host fallback), `internal/orchestrator/workprobe/workprobe.go` (`DetectChanges`), `internal/orchestrator/status/` (`ProbeWorkData`).

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

### DF21 — Docker Desktop containerd store: BuildKit attestations make `yoloai-base` a manifest-list index that vanishes between runs (full rebuild every run)

- **Discovered:** 2026-06-10 · **Workstream:** Apple `container` backend (diagnosing repeated base-image rebuilds during `make smoketest`)
- **Severity:** MEDIUM (no data loss, but a full ~5-minute `yoloai-base` rebuild on *every* operation against a Docker Desktop daemon that uses the containerd image store — increasingly the default)
- **Disposition:** RESOLVED (primary, this commit); the secondary host-global-marker bug remains PARKED.
- **Root cause (confirmed empirically).** `buildBaseImage`/the profile build ran `docker build -t yoloai-base -` with no attestation flags. BuildKit's default provenance/SBOM attestations make the result a **manifest list / image index** on Docker Desktop's containerd image store: the tag points to an index whose platform image has a *different* id. Verified with `docker image ls --tree`: a default build tags an index (`42259e91…` → linux/arm64 `ed62fb1b…`, two different ids), while `--provenance=false --sbom=false` tags a **single image** (`8174802f…`, tag points directly at it). The classic `overlay2` store (OrbStack) flattens to a single image, which is why **OrbStack was unaffected and Docker Desktop rebuilt every run**. The index-wrapped image is lost between runs (containerd-store GC / existence resolution), so `Setup` hit the `!exists` path ("Building base image (first run only)…") on every run. *(Two earlier diagnoses were wrong and corrected: the transient VS Code 404 — a separate flake fixed by `7335018` — and "the SDK can't see containerd-store images" — refuted by a live diagnostic that found the image fine.)*
- **Fix (applied):** both `docker build` invocations in `runtime/docker/build.go` now pass `--provenance=false --sbom=false`, producing a plain single-platform image on both store types — a local base image has no use for SBOM/provenance attestations. **Verify:** re-run `make smoketest`; Docker Desktop should report "Base image built successfully" (skipped) like OrbStack, not "first run only".
- **Remaining (parked, minor):** the staleness marker `.base-image-checksum` is **host-global** (`baseImageChecksumPath` → `CacheDir()`) while images are **per-daemon**. After a Dockerfile change the first daemon to rebuild records the shared marker; a second daemon that already has an image skips `NeedsBuild` (`docker.go:321`) and keeps a **stale** image. Niche (multi-daemon only). Fix: record the build-inputs checksum as an image label read per-daemon.
- **Pointer:** `runtime/docker/build.go` (both `docker build` invocations; `NeedsBuild`/`baseImageChecksumPath`/`RecordBuildChecksum` for the secondary), `runtime/docker/docker.go:309/321` (Setup gate).

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

### DF91 — `.goreleaser.yaml`'s `changelog:` block is inert; release notes come from the tag annotation

- **Discovered:** 2026-07-15 · **Workstream:** contributor-docs sweep (D116)
- **Severity:** LOW
- **Disposition:** PARKED
- **Description:** `release.yml:37` extracts `%(contents:body)` from the annotated tag and `:43` always passes `--release-notes=<file>`, which supplies the release body wholesale. goreleaser therefore never generates a changelog, so the `changelog:` block's groups (`^feat`/`^fix`/`(?i)(breaking|!:)`) and filters (`^docs:`, `^test:`, `^ci:`, `^chore`, `^build\(make\)`) never apply to anything. Not a rendering bug — `release.footer` **does** render (verified verbatim at the tail of the published v0.8.0 body) — but ~25 lines of config that look load-bearing and are not. **This matters beyond tidiness:** it is tempting to justify the commit-subject type set by "goreleaser groups the changelog by it", and that justification is false. The type set is a convention, full stop. Either delete the block or comment it as a fallback for hand-written-notes-absent releases.
- **Pointer:** `.goreleaser.yaml:142-163` (changelog), `:172` (footer, works); `.github/workflows/release.yml:37`, `:43`.

### DF92 — the bug-report templates promise a triage flow and labels that do not exist

- **Discovered:** 2026-07-15 · **Workstream:** contributor-docs sweep (D116)
- **Severity:** MEDIUM (the templates apply labels silently to nothing until the labels exist)
- **Disposition:** PARKED
- **Description:** `.github/ISSUE_TEMPLATE/` was built from `design/github-issues.md` (D116) after sitting designed-but-unimplemented. The templates auto-apply `needs-triage`, which **does not exist** in the repo — labels cannot be created from repo files, only via the API/UI. The design's wider taxonomy (`needs-info`, `confirmed`, `stale`, `keep`, `runtime/*`, `agent/*`, `cmd/*`) and the triage automation that would apply it are also unbuilt; those labels are deliberately not created until something applies them. Additionally the design's `--bugreport` sections specify an **outer** `<details>` wrapper around the whole report and a 64,000-byte threshold; `bugreport/writer.go` emits per-section `<details>` (`:96`, `:116`, `:122`) but no outer wrapper and no size check, so the template's "pastes render as a single collapsible line" is not yet true of real reports.
- **Pointer:** `.github/ISSUE_TEMPLATE/`, `docs/contributors/design/github-issues.md`, `internal/cli/bugreport/writer.go:96`.

### DF95 — silent scope gates are invisible; D112 only closed the availability half

- **Discovered:** 2026-07-15 · **Workstream:** contributor-docs sweep (D117)
- **Severity:** LOW
- **Disposition:** PARKED
- **Description:** D112 made *availability* skips fail loudly (absent platform-possible backend → FAIL, carve-out `YOLOAI_TEST_UNCONTROLLED_BACKENDS`), and explicitly left *scope* gates alone as "a different axis" — `YOLOAI_TEST_TART_VM`, `YOLOAI_TEST_APPLE_BASE`, `YOLOAI_TEST_BACKEND=podman`. DF94 shows the axis is not as separate as it looked: a scope gate nothing turns on is indistinguishable from a deleted test, and it reports green forever. Worth deciding whether a scope gate should announce itself — e.g. every gated tier prints one line naming the variable and whether it fired, so a `releasetest` transcript states what it did *not* do. Note the constraint from D117: `.claude/hooks/on-stop.sh` discards `make check` output on success, so anything that only prints on the happy path is invisible to agents — the announcement has to survive that, or live where a human reads it.
- **Pointer:** `docs/contributors/design/plans/mandatory-infra-test-policy.md:113-117`; DF94.

### DF96 — the ambient $HOME cannot distinguish per-test state from shared host infrastructure

- **Discovered:** 2026-07-15 · **Workstream:** DF94 Tart lifecycle verification (D117)
- **Severity:** MEDIUM (one reading of `$HOME` silently cost a ~30 GB re-download per test; the opposite reading silently writes to the developer's real `~/.yoloai` — and the two are indistinguishable at the call site)
- **Disposition:** PARKED — backstopped under D117 for tart only
- **Description:** The integration TestMains rewrite `$HOME` to a temp dir before `m.Run()` (`internal/orchestrator/integration_main_test.go:69`, `internal/cli/integration_main_test.go:144`). That is **deliberate and load-bearing**, not a defect to remove: `internal/cli` runs the real CLI as a subprocess, and the environment is the only channel by which that subprocess learns where its state lives. The defect is that `$HOME` afterwards means two different things with no way to tell them apart. For *per-test state* the temp HOME is the right answer, and `testutil.NewIntegrationRuntime` depends on it — resolving the real home there would inspect a different store than the CLI under test writes to. For *shared host infrastructure* it is the wrong answer: tart's store is `TART_HOME` → `<HomeDir>/.tart`, so the temp HOME points tart at an EMPTY store and it re-downloads the ~30 GB base image per test (DF19, rediscovered here in a suite whose own comment documented the trap). D117 backstopped the tart side only, by capturing the curated env at package init (`testutil.hostEnvAtStart`, which runs before TestMain) and threading it through `TartStoreLayout`. That fixes the one known victim; it does not make the ambiguity visible to the next caller, who still sees one `$HOME` and must somehow know which meaning applies. Worth deciding whether the two should be separate, explicitly-named values (e.g. a Layout that carries both a state home and a host-infrastructure home) rather than one variable serving both. Note `os.Setenv`/`os.UserHomeDir` are lint-exempt in `_test.go` and `internal/testutil/`, so `make check` cannot catch a regression either way.
- **Pointer:** `internal/testutil/backend.go` (`hostEnvAtStart`, `HostHomeAtStart`, and the ambient read in `NewIntegrationRuntime` that is correct as-is); `internal/testutil/tart.go`; `internal/orchestrator/integration_main_test.go:69`; `.golangci.yml:426-441` (the exemption that lets it through).

### DF104 — `--network-isolated` is IPv4-only: nothing in the repo configures `ip6tables`, on any backend

- **Discovered:** 2026-07-15 · **Workstream:** AC10 — running the apple backend's `--network-isolated` path end-to-end for the first time, which is exactly what "not just the raw-iptables capability" was meant to surface
- **Severity:** MEDIUM (a security property with an unfiltered protocol; **latent, not currently exploitable** — see below. It would become live the moment any backend's guest gets a globally-routable IPv6 address, with no code change and nothing to notice.)
- **Disposition:** PARKED — filed, not fixed. Adding an `ip6tables` default-deny touches the shared firewall for every backend and needs its own verification per backend; AC10's scope was apple's end-to-end run, which passed.
- **Description:** `runtime/docker/resources/firewall.py` builds the default-deny + allowlist as **IPv4 `iptables`/`ipset` rules only**. `grep -rn ip6tables` across the whole repo — Go, Python, shell — returns **nothing**, so no backend has ever filtered IPv6. Verified live in an apple sandbox created with `--network-isolated`: the IPv4 `OUTPUT` chain is correct and complete (gateway:53 accepted, `allowed-domains` ipset matched, everything else REJECTed), while `ip6tables -L OUTPUT` is **empty with policy ACCEPT**. The guest is not v6-less: it holds a global-scope address (`fd96:…/64`) and a v6 default route via the vmnet gateway.
- **Why it is not exploitable today, which is the whole of the mitigation:** the address vmnet hands out is a **ULA** (`fd00::/8`, unique-local, not internet-routable), so there is no v6 egress to allow or block — a `curl -6` to a non-allowlisted host fails with exit 7 like everything else. Nothing in the design guarantees that stays true. It rests on Apple's vmnet choosing not to hand out a GUA, which is not a contract we hold, is not asserted anywhere, and no test would notice it changing.
- **Two ways it turns live:** (1) any backend whose network gives the guest a routable v6 — Docker with `--ipv6` enabled, a future vmnet, an IPv6-capable CNI on containerd — silently loses the allowlist entirely for v6 traffic, because the rules simply don't exist there; (2) an allowlisted domain that resolves v6-only would still be reached, but **unfiltered** — the allowlist would not be what let it through.
- **The honest framing:** the capability is declared `NetworkIsolation: true` on every backend that sets it. That claim is IPv4-scoped and says so nowhere. Either the rules should cover v6, or the guests should have v6 disabled (`net.ipv6.conf.all.disable_ipv6=1` in the entrypoint) so the claim is true by construction — the second is cheaper and matches how the isolation already prefers "impossible" over "filtered".
- **Pointer:** `runtime/docker/resources/firewall.py` (`install_rules`, IPv4-only); `runtime/apple/apple.go:61` (`NetworkIsolation: true`); verified against a live apple sandbox 2026-07-15.

### DF103 — `post-merge-roadmap.md` copies every workstream's status into a table that nothing keeps in step

- **Discovered:** 2026-07-15 · **Workstream:** the plan-status gate — the roadmap was the one live plan the vocabulary would not fit, and the reason turned out to be the finding
- **Severity:** LOW (a stale planning table; each workstream's own plan is authoritative and correct)
- **Disposition:** PARKED — the roadmap now carries `**Status:** IN-PROGRESS` and says out loud that its table lags. Draining the duplication is a separate change with a real design question behind it (below).
- **Description:** The roadmap sequences fifteen workstreams (A1–A7, B1–B5, C, D, E1–E3) and reproduces each one's size, dependencies, platform constraints and **status**. Every one of those workstreams now has its own plan carrying an authoritative `**Status:**` token, so the table is a second location for a fact the plans own — and it has already drifted: the table reads "research done, impl-ready" for E2 while the apple-container backend has largely shipped, and it lists E1 as retired only because someone remembered to edit it. This is D121's exhaustive-list failure at one remove: the roadmap is an index of *statuses* rather than of files, and it rots the same way `plans/README.md` rotted to 20-of-29.
- **It also stores its dependency edges twice, in opposite directions.** The table carries both a **Blocked by** and an **Unblocks** column, so "B2 depends on B1" is written on B2's row *and* on B1's row inverted. One edge, two locations, free to disagree. `Unblocks` is the wrong half to keep even if only one survives: a plan's author knows what their own plan needs, so a dependency is local knowledge, while an "unblocks" list obliges you to edit B every time some later A starts depending on it — action at a distance, performed by whoever is least likely to open B. The `- **Depends on:**` field declares the edge once, in the direction its author actually knows, and the reverse view is a grep that cannot be stale (`standards/markdown.md` → Metadata list).
- **The durable fix, and why it is not built here:** the roadmap's columns are plan metadata living in the wrong file. If each plan declared its own `EFFORT:`, `LAYER:` and what it depends on, the table would be **derivable** — a grep, complete by construction, unable to lag — and the roadmap would keep only what a machine cannot generate: the recommended order and the human decisions gating each workstream. That is the same normalization as the `**Status:**` token, applied to the rest of the row. It is deferred because each new field is a claim that rots unless a gate enforces it, and a field nobody reads is worse than no field: the token earns its place by answering "what haven't we fleshed out?", and `EFFORT:`/`LAYER:` need an equally concrete question before they are worth adding.
- **Interim:** the roadmap's Status line names the table as lagging, so a reader is told to trust each plan instead. That is a characterization, not a fix.

### DF102 — `architecture/code-map.md` omits a whole backend and seven other packages, while claiming to map every one

- **Discovered:** 2026-07-15 · **Workstream:** the ABOUTME conformance sweep — surfaced by adversarially verifying the header an agent had just written for the file
- **Severity:** MEDIUM (`code-map.md` is the "where does this live" entry point, and it is silently missing a backend; `where-to-change.md` routes new contributors and agents here)
- **Disposition:** PARTIALLY ADDRESSED — the eight packages the finding names are now mapped, each entry written from the package's own source rather than its name. **Still open:** the class, not just those eight. Five real packages remain absent (`internal/buildinfo`, `internal/orchestrator/agentcfg`, `internal/orchestrator/envspec`, `internal/orchestrator/workprobe`, `internal/cli/clitest`) — all predate the fix, so the sweep that closed the eight missed them by working from the reported list rather than from `go list`. The completeness *claim* also survived the fix: the ABOUTME was rescoped to "the mapped packages", but the intro went on saying "every package and file" until 2026-07-15, when the doc covered 30 of 62. That claim is now gone and replaced with the real ratio (D124). A coverage gate — every package `go list` reports must have a section — is not built; the name/path gates that landed do not check it.
- **Description:** `runtime/apple` — the Apple `container` backend — has **no entry anywhere in the file**, while all five sibling backends (docker, podman, containerd, tart, seatbelt) are mapped. Also absent: `internal/broker`, `internal/credential`, `internal/netpolicy`, `internal/netpolicycfg`, `internal/sysexec`, `runtime/ptybridge`, `runtime/runtimetest`. Each verified present as a real package in the tree and absent from the doc by grep. Several are load-bearing: `internal/broker` implements D105's credential brokering, `internal/sysexec` is the single licensed subprocess site DEV §12 names, and `runtime/runtimetest` is the shared conformance harness `architecture/testing.md` points every backend at.
- **Why it went unnoticed:** the file's ABOUTME asserted it covered "every package". An exhaustive claim nothing enforces reads as a completed inventory, so nobody checks — D121's "avoid exhaustive lists: they imply completeness and the next addition falsifies them in silence", with `runtime/apple` as the addition. The doc was accurate when written and no gate has an opinion about a package it never mentions.
- **A gate is possible** and would remove the class rather than the instance: every directory under `internal/` and `runtime/` containing a `.go` file should appear somewhere in `code-map.md`. That is a set difference, which is exactly the shape a machine catches and a reader does not (compare DF94). It would have failed the moment `runtime/apple` landed.

### DF101 — docs cite the standards by upper-case names the files don't have (`standards/GO.md` vs `go.md`)

- **Discovered:** 2026-07-15 · **Workstream:** the ABOUTME conformance sweep — a subagent noticed it while reading `standards/` and flagged it as out of its scope
- **Severity:** LOW (nothing 404s today; it is a grep asymmetry, not a broken link)
- **Disposition:** PARKED — filed, not fixed. The sweep that found it was scoped to ABOUTME blocks, and rewriting body prose across the principles docs is a different change with a different review.
- **Description:** The files are `docs/contributors/standards/go.md`, `cli.md`, `python.md`, `shell.md`, `markdown.md`, `makefile.md`, `dockerfile.md` — lowercase, per `README.md`'s "filenames are lowercase kebab-case". Several live docs refer to them in prose as `standards/GO.md` / `standards/CLI.md`. **Verified: no Markdown *link* targets the wrong case**, so nothing is broken on GitHub or on Linux; every instance is a backticked prose mention. What it costs is a grep: search for `go.md` and the `GO.md` mentions are invisible, search for `GO.md` and the file itself is invisible. That is the same near-namesake asymmetry as DF94 (`YOLOAI_TEST_TART` vs `YOLOAI_TEST_TART_VM`), which no human review caught for months, and which neither a human nor an agent catches by reading.
- **Why it was checked before filing:** the initial report was "wrong-case links, would 404 on Linux". That was plausible and wrong. This host's filesystem is case-insensitive, so `GO.md` resolves locally and a naive check confirms whatever you already believe; the actual test is whether any `](...)` target carries the wrong case, and none does (D119).
- **Live sites:** `principles/general-principles.md`, `principles/development-principles.md`, `principles/testing-principles.md`, `standards/shell.md`, `design/research/principles/development-principles-research.md`. Append-only sinks and `archive/` also carry mentions and are exempt from the sweep (AGENTS.md rule 2).
- **A gate is possible here** and would be cheap: every `<name>.md` mentioned in a docs path-shaped token should resolve to a tracked file, compared case-sensitively. That catches this class and link rot together, and it is the durable fix — a one-time correction re-arms the moment someone types `GO.md` again.

### DF99 — the multi-backend orchestrator suite made docker mandatory for every backend, and two C1 security tests had never run

- **Discovered:** 2026-07-15 · **Workstream:** the test-gate liveness gate (D119), which surfaced it before it was even written
- **Severity:** MEDIUM (two shipped security properties were unverified; no defect found in them once run)
- **Disposition:** PARTIALLY ADDRESSED (D119) — the docker-mandatory half and the two seatbelt/apple C1 tests are fixed and verified; **still open:** containerd has no malicious-filter test. (This line read `ADDRESSED-IN-PLACE` until 2026-07-15, while the body four lines below already said "Still open". A disposition that overclaims relative to its own body is why this queue cannot be drained by reading dispositions — D123's shape, in the sink meant to record it.)
- **Description:** `internal/orchestrator` is the repo's only **multi-backend** test package — docker, podman, seatbelt, apple and tart tests all live in it — and its `TestMain` connected to docker and built the docker base image unconditionally, before any test ran, exiting via `BackendAbsent` if the daemon was down. Every single-backend package's TestMain probes its own backend, which is correct because the package *is* that backend (`runtime/docker`, `runtime/tart`, `runtime/seatbelt`, `runtime/apple`); this package inherited that pattern with docker cast as the implicit default. The warm-up was only an optimisation — its own comment said "builds the base Docker image once… subsequent Setup calls hit the cache" — and it was redundant twice over: `integrationSetup` already did the full docker bootstrap per test, and `make integration` already fails loudly via `make base-image` if the daemon is absent (the D112 enforcement). So a hard docker dependency bought a warm cache, and cost every non-docker test in the package a daemon it never needed. **The knock-on was the real damage.** Because the seatbelt and apple orchestrator tests could not live under `integration-seatbelt` / `integration-apple` (those targets would have needed docker), they were parked behind `YOLOAI_TEST_SEATBELT` and `YOLOAI_TEST_APPLE` — gates that **nothing set**, exactly DF94's defect. Both guard `TestIntegration_CopyModeMaliciousFilterNoHostExec_*`, the audit-C1 check that a malicious git filter in a `:copy` workdir cannot execute on the host. Five backends advertise `GitExecInConfinement: true`; the test ran for two. It had never run for seatbelt, the backend that needs it most, because seatbelt has no container and its confinement is an SBPL profile wrapping git itself. Both pass on first run: seatbelt in 0.46s, apple in 285s. Seatbelt's gate had no cost to justify it at all and is deleted; apple's is kept (it boots a VM) and is now wired into `integration-apple`. Fixed by giving each backend a `sync.Once` warm-up owned by its own setup helper, so a backend whose tests do not run is never touched — verified by running the seatbelt test to green with `DOCKER_HOST` pointed at a nonexistent socket. **Still open:** containerd advertises `GitExecInConfinement: true` and has no malicious-filter test at all. That is a missing test rather than a dead gate, so no gate can find it.
- **Pointer:** `internal/orchestrator/integration_main_test.go` (TestMain, now backend-free); `internal/orchestrator/integration_helpers_test.go` (`warmDockerBase`); `internal/orchestrator/integration_macos_test.go`; `Makefile` (`ORCHESTRATOR_NON_DOCKER_TESTS`, `integration-seatbelt`, `integration-apple`, `integration-tart`); `runtime/containerd/containerd.go:50` (the untested claim); DF94, DF95.


### DF111 — in-VM git intermittently fails exit 69 (Xcode licence) on tart; the original diagnosis is disproven and the mechanism is still UNRESOLVED

- **Discovered:** 2026-07-15 · **Workstream:** releasetest on Apple Silicon (post-DF94 rerun); re-investigated on the Mac 2026-07-15
- **Severity:** MEDIUM (blocked `TestIntegrationTart_GitCorruption` once; D113 confines copy-mode work-copy git **inside** the VM for tart, so in-VM git is the exposed surface). Not reproducible as of 2026-07-15 — the tier has since passed ~5 consecutive times, so severity is inferred from the one observed failure, not from a live repro.
- **Disposition:** PARKED — **the title's original claim is disproven and no replacement mechanism is confirmed.** Three candidates were tested on Apple Silicon; none survives. Filing what is true rather than the most plausible story, per this file's own rule: an unverified finding is worse than none.
- **The original claim, disproven.** The first filing said "the tart base VM's git needs an Xcode licence agreement" and asked whether the acceptance was missing from `yoloai-base` provisioning or inherited from host state. **Neither.** On a raw `tart clone yoloai-base` + `tart run` (no yoloai setup), `git --version` → **exit 0**, `git init`/`git status`/`git add` → **exit 0**, and `xcode-select -p` → `/Library/Developer/CommandLineTools`, which needs no licence. The base VM's git is fine. Both open questions are therefore answered: (i) it is neither a base-provisioning gap nor inherited host state; (ii) `git --version` **does** trigger it once — and only once — the active developer dir is an Xcode whose licence this VM has not accepted, which is why a raw base VM shows nothing.
- **The three mechanisms tested, and why each fails to explain it.**
  1. **Base-image gap / inherited host state** — DISPROVEN, above.
  2. **`sandbox-setup.py` switches `xcode-select` onto the mounted host Xcode *before* accepting its licence, opening a window where all in-VM git fails** — the window is **REAL but UNREACHABLE**. Real: with Xcode mounted `:ro` exactly as the backend mounts it, `xcode-select --switch` then `git --version` → **exit 69** verbatim, and `git add` → exit 69 (the `:377` failure); accepting first via `sudo env DEVELOPER_DIR=<mounted> xcodebuild -license accept` makes every subsequent probe exit 0. Unreachable: widening that window from milliseconds to **25 seconds** with a temporary `sleep` between the switch and the accept, then running `TestIntegrationTart_GitCorruption`, **still passes** (117.91s vs the usual ~78–93s, so the sleep demonstrably ran, twice). A 25s window the test never lands in is not the mechanism. (Reading `main()` agrees — `backend.setup()` is line 1392, `signal_secrets_consumed()` line 1415 — but the experiment is what settles it, because the barrier at `launch.go:802` is `if hasSecrets`, and `hasSecrets` is `secretsDir != ""` (`launch.go:128`), which is false for the tier's agent `test`.)
  3. **The `xcodebuild -runFirstLaunch` security-scan storm** (the mechanism `backend-idiosyncrasies.md` attributes it to) — **NOT REPRODUCED**. With the licence accepted and `xcode-select` switched, backgrounding a real `sudo xcodebuild -runFirstLaunch` (log: `Install Started` → `Install Succeeded`) while probing `git --version` once per second for 90s gave **90/90 exit 0** — zero exit-69, zero exit-127. That does not disprove the storm (the conditions may not have been matched), but it is not confirmed either, and the entry it rests on has its own problem: see DF112.
- **Best remaining hypothesis, unverified.** Production's baseline path wraps its in-VM execs in `execVMSetupWithStormRetry` (`vmworkdir.go:69,78`), whose `isFirstlaunchStormTransient` matches exactly `exit 69 + "Xcode license"` (and exit 127) and retries to a 240s ceiling — so shipped code *survives* whatever this is, which is consistent with "intermittent, passes on retry". The tier's own `mgr.Runtime().Exec(...)` calls (`integration_tart_test.go:348,356,368,377`) have **no** such wrapper. That asymmetry would explain why the tier catches a transient production absorbs. It is a hypothesis: the transient itself was never made to occur, so wrapping the test execs would be a fix for an undemonstrated cause.
- **The check that would settle it (not run — this is the honest gap):** catch it in the wild with the storm actually present — i.e. a run where `sandbox.jsonl` shows the test's failing exec landing between `tart.xcode.firstlaunch.started` and the storm subsiding — or find the conditions that raise a storm heavy enough to break the licence check (the 90-probe attempt above did not). Until then, do not wrap the test execs and call it fixed: the tier passes consistently without any change, so a green tier proves nothing about this finding.
- **One relevant fact for whoever picks it up:** the host is Xcode **26.5** with `IDEXcodeVersionForAgreedToGMLicense` = **26.4** (the host has not accepted 26.5), and every fresh VM carries **no** acceptance record at all. So the accept in `sandbox-setup.py` is load-bearing on every start, not the "might already be accepted or not required" no-op its sibling comment in `build.go` assumes. See DF114 — that accept's result is never checked.
- **Pointer:** `internal/orchestrator/integration_tart_test.go:348,356,368,377` (the unwrapped execs); `runtime/monitor/sandbox-setup.py` (`TartBackend.setup`, the switch/accept order); `internal/orchestrator/launch/vmworkdir.go:69,78,115-130` (the retry production already has); `internal/orchestrator/launch/launch.go:128,802` (`hasSecrets` gating the barrier); `runtime/tart/tart.go:719` (Xcode mounted `ReadOnly`); `backend-idiosyncrasies.md` "Same storm, host side" (the incumbent theory) and DF112 (a defect in that entry); D113 (in-VM git confinement).

### DF112 — `backend-idiosyncrasies.md`'s storm entry explains "passes on a cold retry" with VirtioFS persistence that a read-only mount cannot provide

- **Discovered:** 2026-07-15 · **Workstream:** DF111 re-investigation on Apple Silicon
- **Severity:** LOW (a docs defect — but the claim is load-bearing for the entry's model, and that entry is the incumbent explanation for DF111)
- **Disposition:** PARKED — filed, not fixed; correcting it means first establishing where firstlaunch state *actually* persists, which is the same unresolved question as DF111
- **Description:** The "Same storm, host side — the baseline-SHA `git` fails with exit 69" subsection explains why the failure clears on a retry like this: "It passes on a cold retry because firstlaunch state persists in the host Xcode.app via VirtioFS, so the second VM finds it done and never raises the storm." But the live sandbox mounts Xcode **read-only** — `runtime/tart/tart.go:719` sets `ReadOnly: true` on every detected Xcode path, and the base-build path mounts `:ro` too (`build.go:569`). A VM cannot write firstlaunch state into a read-only VirtioFS mount, so it cannot persist there and a second VM cannot "find it done" by that route. Either the state persists somewhere else (in-VM `/Library/Developer`, receipts under `/var/db`), or the mount was read-write when the entry was written and the entry was never revisited.

  This matters beyond tidiness: the persistence claim is *why* the entry concludes the storm is a first-VM-only phenomenon, which is in turn why "passes on a cold retry" reads as explained rather than as an open question. If the premise is wrong the conclusion is unsupported, and DF111 currently has no other candidate. Consistent with this, a fresh VM (which by the entry's model *should* raise a storm, having no persisted state) produced 90/90 clean git probes during a real `-runFirstLaunch`.
- **Pointer:** `docs/contributors/backend-idiosyncrasies.md` ("Same storm, host side — the baseline-SHA `git` fails with exit 69", and the Symptom Index row mapping the exit-69 string to it); `runtime/tart/tart.go:719` (`ReadOnly: true`); `runtime/tart/build.go:569` (`:ro`). Related: DF111.

### DF114 — `sandbox-setup.py` never checks the Xcode licence accept, then logs "xcode license accepted" regardless

- **Discovered:** 2026-07-15 · **Workstream:** DF111 re-investigation on Apple Silicon
- **Severity:** MEDIUM (not a known live failure — but it is the reason a live failure here would be invisible, and every tart sandbox depends on this accept succeeding)
- **Disposition:** PARKED — filed, not fixed. It is one line, but it sits in the middle of DF111's unresolved mechanism, and changing what the tart setup path reports while that is open risks reading as a fix for DF111. Land it once DF111 is settled, or as its own commit.
- **Description:** `TartBackend.setup` runs `sudo xcodebuild -license accept`, captures the result into `result`, **never inspects `result.returncode`**, and then unconditionally emits `log_debug("tart.xcode.license.done", "xcode license accepted")`. The neighbouring `xcode-select --switch` call *does* check its returncode and syslogs on failure, so the omission is local to the accept. Consequence: if the accept ever fails, the VM is left with `xcode-select` pointed at an Xcode whose licence it has not accepted — a state in which **every** in-VM git returns exit 69 for the VM's whole life, not transiently — while the log says the licence was accepted. That is the exact symptom DF111 is chasing, reported as success.

  The accept is load-bearing, not defensive: verified 2026-07-15, a fresh VM has **no** `IDEXcodeVersionForAgreedToGMLicense` record at all, and the record lives in the VM's own `/Library/Preferences` (as the code comment notes), so it dies with each ephemeral VM and must be re-established on every start. `build.go`'s sibling `configureXcodeInVM` treats its own accept as best-effort with the comment "license might already be accepted or not required" — on the evidence, it is never already accepted and always required, so that comment is wrong too.
- **Pointer:** `runtime/monitor/sandbox-setup.py` (`TartBackend.setup`, the `xcodebuild -license accept` call and the unconditional `tart.xcode.license.done` log); `runtime/tart/build.go` (`configureXcodeInVM`, same unchecked accept plus the "might already be accepted" comment). Related: DF111 (the symptom this would hide), DF112.

### DF113 — `destroy` frees the sandbox name while leaving the instance behind, so the next `start` adopts a VM it never provisioned

- **Discovered:** 2026-07-15 · **Workstream:** the DF110 fix on Apple Silicon — this is the same shape in shipped code, found while establishing that DF110's severity was the false PASS rather than the confusing failure
- **Severity:** MEDIUM — but the MEDIUM/CRITICAL call is genuinely open, and the check that decides it is named below rather than guessed. Nothing here is a regression or a release blocker; the trigger needs a *failed* instance removal, which is not the common path.
- **Disposition:** PARKED — filed, not fixed (owner's call, 2026-07-15). The DF110 scope was the test tier, and the remedy here touches the shipped `start` contract: the "already running" no-op is *correct* for `yoloai start` on a live sandbox, so a guard has to distinguish "mine, already up" from "predates me" without breaking idempotency.
- **Description:** `lifecycle.start` treats any pre-existing instance as its own. `DetectStatus` → `StatusActive`/`StatusIdle` → "Sandbox %s is already running" → `return nil` (`start.go:370-372`), which skips `recreateContainer` and therefore the VM work-dir/baseline setup. `create.Run` never inspects the runtime — its own comment says "Create provisions only — it does not launch the container" (`create/create.go:168-171`) — so nothing between the two verbs notices that the instance predates the sandbox.

  **The trigger is a designed path, not an exotic one.** `launch.Teardown` discards the removal result (`teardown.go:52`, `_ = d.Runtime.Remove(ctx, cname)`) and then deletes `environment.json` *first*, explicitly so that undeletable state still frees the name: "Create keys 'already exists' off the metadata, not the directory, so a leftover (e.g. root-owned overlay/VM state we can't delete) won't block re-creating with the same name" (`teardown.go:54-57`). The single case that comment is written for — the instance survived the destroy — is exactly the case that hands the name to a new sandbox while the old instance still answers to it. So: `destroy` (Remove fails, ignored) → `new` same name (succeeds; no metadata) → `start` → adoption.
- **Verified:** the adoption itself, by planting a running VM at the computed instance name and running the tart tier against it — `start` no-ops in 0.90s with no `recreating container` line, `BaselineSHA` stays empty, and the in-VM `git -C` hits a work dir the leftover never had. That reproduction ran under a *test* principal, but the code path is `lifecycle.start` unmodified; only the principal differs.
- **NOT verified, and it is what decides the severity:** an end-to-end sequence in which `Runtime.Remove` genuinely fails, and what `apply`/`diff` then do. The concerning case is re-creating against the **same host project dir**: the VM work path is the encoded *host* path (`tart.go:181`), so it is byte-identical across sandboxes for one project — the adopted VM already holds a work dir at exactly the path the new sandbox expects, containing the *previous* sandbox's work, while the new `environment.json` carries an empty `BaselineSHA`. Whether that errors (as it did under test) or silently diffs against the wrong baseline separates MEDIUM from CRITICAL.
- **The cheap check that would settle it (not run here):** force `Runtime.Remove` to fail — `internal/orchestrator/lifecycle/fakeruntime_test.go` already exists for exactly this kind of injection — then destroy, re-create the same name against the same project dir, start, and inspect `meta.Workdir().BaselineSHA` and `copyflow.GenerateDiff`. D119 parks the fix, never the verification; this is named rather than assumed because the run that would settle it is a fake-runtime unit test, not a VM.
- **Pointer:** `internal/orchestrator/lifecycle/start.go:370-372` (the no-op); `internal/orchestrator/create/create.go:135-174` (no runtime guard); `internal/orchestrator/launch/teardown.go:52,54-58` (ignored `Remove`, name freed on purpose); `runtime/tart/tart.go:181` (encoded work path). Related: DF110 (same shape in the test tier, fixed there by reaping under the test principal), D62/DF19 (principal scoping).

## Policy origin

Established in [architecture-remediation.md](../archive/plans/architecture-remediation.md) and inherited by [layering-refactor.md](../archive/plans/layering-refactor.md).

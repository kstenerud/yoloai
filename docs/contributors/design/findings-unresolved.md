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

### DF85 — Codex 0.144 folder-trust onboarding prompt blocks the agent (untrusted workdir → agent exits)

- **Discovered:** 2026-07-14 · **Workstream:** DF82 broker generalization (D115) — unmasked once brokering cleared the Codex login blocker.
- **Severity:** MEDIUM (Codex is non-functional on 0.144 for any workdir the user hasn't previously trusted on the host; independent of brokering — affects `--no-broker` too).
- **Disposition:** ADDRESSED-IN-PLACE — the launch path now marks the container workdir trusted on every Codex launch (see implementation below); verified live (0 trust prompts, agent runs in the workdir, prompt delivered). Kept here as a record; the `Description`/`Trigger` capture the behavior for future reference.
- **Implementation:** `agent.Definition.WorkdirTrust` (a new declarative `*WorkdirTrustPatch{RelPath, Patch}`) is applied unconditionally at launch by `launch.applyWorkdirTrust` (in `LaunchContainer`, after `brokerCredentials`, before `buildAndStart`), which writes `[projects."<st.Workdir.ResolvedMountPath()>"] trust_level = "trusted"` into `agent-runtime/config.toml` via `agent.patchCodexWorkdirTrust` (a go-toml map round-trip that preserves the user's config and any broker `openai_base_url`). Codex declares it; it runs brokered or not, and re-applies each launch (config.toml is re-seeded from the host every launch).
- **Description:** On launch, Codex 0.144 shows a **"Do you trust the contents of this directory?"** onboarding dialog for any working directory not recorded as trusted in `~/.codex/config.toml` (`[projects."<path>"] trust_level = "trusted"`). yoloAI launches Codex interactively and pastes the user's task prompt, which lands in that dialog instead of Codex's input box, and the agent **exits (status 0)** to a fall-to-shell. The existing launch flags do **not** suppress it: `--dangerously-bypass-approvals-and-sandbox` governs command-execution approvals and `--dangerously-bypass-hook-trust` governs hook trust — neither covers folder trust. Verified: with `[projects."<workdir>"] trust_level = "trusted"` present, Codex shows **0** trust prompts and starts normally. There is **no global trust-all / disable-folder-trust config key** (confirmed against the shipped binary) — trust is strictly per-project-root. So the workdir the container runs Codex in (the mirrored mount path) must be marked trusted at launch. This is the Codex analogue of Claude's onboarding suppression / Gemini's `folderTrust:false`, but it is **workdir-path-dependent**, so it needs a launch-time, path-aware `config.toml` write (a static `ApplySettings` patch can't know the path), and it must apply to **every** Codex launch (brokered or not).
- **Trigger / fix:** at Codex launch, write `[projects."<container-workdir>"] trust_level = "trusted"` into `config.toml` for the resolved container workdir path (yoloAI knows the mount path). Must compose with the broker's `openai_base_url` patch on the same file (`agent.patchCodexBaseURL` already round-trips the whole TOML map, so a shared codex-config step or an added map key is the natural home). Consider also `hide_full_access_warning`/`hide_world_writable_warning` notices if they surface. Revive when someone makes Codex usable end-to-end on 0.144, or when a user reports Codex quitting to a shell right after launch.
- **Pointer:** `internal/agent/agent.go` (codex `InteractiveCmd`/`Definition`), `internal/agent/broker_codex.go` (`patchCodexBaseURL` — the existing codex config.toml patch), `internal/orchestrator/launch/launch.go` (`patchBrokerConfigFiles` — launch-time config patching, but only runs when brokering); `docs/contributors/backend-idiosyncrasies.md` (Codex entry).

### DF84 — Direct-delivery (`--no-broker`) Codex is broken for API-key users: the env var doesn't authenticate Codex 0.144

- **Discovered:** 2026-07-14 · **Workstream:** DF82 broker generalization (D115) — surfaced while wiring/verifying Codex brokering.
- **Severity:** MEDIUM (a shipped agent is non-functional for a common auth setup; pre-existing, not a regression — masked until now because brokering is Codex's default path).
- **Disposition:** ADDRESSED-IN-PLACE — the launch path now materializes the real key into `auth.json` when Codex is not brokered (see implementation below); verified live (`--no-broker` Codex: `auth.json` holds the real key, `codex login status` = "Logged in using an API key", no login prompt).
- **Description:** Codex CLI 0.144 does **not** authenticate from a bare `OPENAI_API_KEY`/`CODEX_API_KEY` environment variable — `codex login status` reports "Not logged in" and the TUI prompts for login (verified empirically, see `backend-idiosyncrasies.md` → "Agent CLIs: base-URL override for brokering differs per CLI"). It reads a logged-in `~/.codex/auth.json` (`{"auth_mode":"apikey","OPENAI_API_KEY":<key>}`). yoloAI's **direct** (non-brokered) credential delivery hands Codex the key as an env var / `/run/secrets` file and seeds `auth.json` **only** from a host `~/.codex/auth.json` (an `AuthOnly` seed, skipped when an API key is present). So a user with an `OPENAI_API_KEY` but no prior host `codex login` got a Codex sandbox that immediately asked to log in. D115's **brokered** path already generated a working placeholder `auth.json`; this closes the `--no-broker` / unsupported-backend gap.
- **Implementation:** `agent.Definition.DirectCredentialFile` (a new declarative `*DirectCredentialFile{RelPath, EnvVars, Render}`) is applied by `launch.applyDirectCredential` (in `LaunchContainer`, right after `applyWorkdirTrust`, while `secretEnv` still holds the real key). It writes `auth.json` = `{"auth_mode":"apikey","OPENAI_API_KEY":<real-key>}` (`agent.renderCodexAuth`, shared with the brokered `patchCodexAuth`) **only** when a credential is still present in `secretEnv` — a brokered launch removed it and wrote the placeholder, so this is a no-op then; the direct path (`--no-broker`, unsupported backend, `--network-none`) still has it. Codex declares `EnvVars: ["OPENAI_API_KEY","CODEX_API_KEY"]`.
- **Pointer:** `internal/agent/agent.go` (`DirectCredentialFile`, codex def), `internal/agent/codex.go` (`renderCodexAuth`), `internal/orchestrator/launch/launch.go` (`applyDirectCredential`); `docs/contributors/backend-idiosyncrasies.md` (Codex auth.json entry).

### DF81 — Unpinned runc/crun version floor: no defense against known container-escape CVEs

- **Discovered:** 2026-07-14 · **Workstream:** sandboxing-blog research (gap analysis)
- **Severity:** MEDIUM
- **Disposition:** ADDRESSED-IN-PLACE — advisory version-floor capability checks added for runc (Docker) and crun (Podman), surfaced via `doctor` and a non-blocking launch-time warning (see implementation below).
- **Description:** Neither the Docker nor Podman backend ever checked the host's OCI runtime version. Three runc CVEs disclosed 2025-11-05 (CVE-2025-31133, CVE-2025-52565, CVE-2025-52881 — masked-path/`/dev/console` mount-race container escapes) are fixed only in runc 1.2.8 / 1.3.3 / 1.4.0-rc.3+; a host on an older runc has a live escape with no warning from yoloAI at any point. crun has an analogous confirmed fix (the `.krun_config.json` symlink-escape, GHSA-f42g-r5jj-qh4j, fixed in crun 1.20) plus a newer masked-path-class fix (CVE-2026-47766) whose exact fixed version tag wasn't pinned down by research — see Trigger below. Added `Advisory bool` to `caps.HostCapability` (`runtime/caps/caps.go`) so a failing check is informational-only: `doctor` reports it via the existing NeedsSetup tier (no doctor code changes needed), while the blocking `CheckIsolationPrerequisites` path (`FormatError`) now skips advisory failures entirely — never blocks, never prompts. A new unconditional check in `launch.LaunchContainer` prints a one-line `Warning: ...` (reusing the existing `filterAvailablePorts` writer precedent) whenever a sandbox actually launches against a below-floor runtime, covering `new`/`start`/`restart`/`reset` uniformly.
- **Trigger / fix:** already implemented this pass. Follow-up (now resolved): crun **1.28** (released 2026-05-27) confirmed as the fix for CVE-2026-47766 (changelog: "do not follow rootfs /dev symlinks"), verified against the upstream release notes. The crun floor has been raised from 1.20 to 1.28 (`crunVersionFloorMeets` in `runtime/podman/caps.go`) — 1.28 supersedes 1.20, so the higher floor alone now covers both fixes.
- **Pointer:** `runtime/caps/caps.go`, `runtime/caps/common.go` (`NewOCIRuntimeVersionFloor`), `runtime/docker/caps.go`, `runtime/podman/caps.go`, `internal/orchestrator/launch/launch.go` (`LaunchContainer`).

### DF82 — Credential broker (D105/D106) is architecturally general but only wired for the Claude agent

- **Discovered:** 2026-07-14 · **Workstream:** sandboxing-blog research (gap analysis). **Updated 2026-07-14:** Gemini + Codex wired (D115); single-provider generalization done. Aider + OpenCode remain (multi-provider).
- **Severity:** LOW–MEDIUM (no security hole — un-brokered agents deliver the API key directly, the pre-D105 baseline behavior — but a real gap versus the "isolate environment, not just files" thesis the broker exists to support)
- **Disposition:** PARTIALLY ADDRESSED (D115) — the broker redirect is now per-agent (`PlaceholderHeader` + env-or-file base-URL delivery) and **Gemini + Codex are wired and tested**. Still PARKED for the two **multi-provider** agents (Aider, OpenCode).
- **Description:** `internal/broker` and `internal/credential` (D105/D106) were built explicitly general but only `"claude"` populated `Broker`. **D115 generalized the redirect** (removed the hardcoded `StripHeaders: ["Authorization"]`, added `BaseURLFile` for CLIs with no base-URL env var) and wired **`gemini`** (env `GOOGLE_GEMINI_BASE_URL`, header `x-goog-api-key`, placeholder in `GEMINI_API_KEY`) and **`codex`** (config.toml `openai_base_url`, `Authorization: Bearer`, Responses API). **Remaining:** `"aider"` and `"opencode"` are **multi-provider** — each provider (anthropic/openai/gemini/…) has its own upstream, base-URL target, and auth header, so brokering them needs a multi-upstream generalization the current single-fixed-upstream injector doesn't do. `aider` redirects via per-provider env vars (`ANTHROPIC_API_BASE`, `OPENAI_API_BASE`, `GEMINI_API_BASE`; Gemini needs a `?key=` query fallback for pre-Nov-2025 LiteLLM). `opencode` redirects via per-provider `opencode.json` `provider.<id>.options.baseURL` and carries an unresolved upstream bug (custom `baseURL` drops the Anthropic key, sst/opencode #21737 — prefer `@ai-sdk/openai-compatible` for the Anthropic leg).
- **Trigger / fix:** revive when someone picks up the **multi-provider broker generalization** (the injector fronting N upstreams with per-provider header routing + a query-param injection mode for old-LiteLLM Gemini), or when a user requests brokering for Aider/OpenCode. Verified per-provider specs are in D115's research. AWS/non-LLM request-signing is a separate reserved workstream (`credential.RequestSigner`), not part of this finding.
- **Pointer:** `internal/agent/agent.go` (`gemini`/`codex` `Broker`; `aider`/`opencode` still lack it), `internal/agent/broker_codex.go`; `internal/orchestrator/launch/launch.go` (`buildInjectorSpec`, `patchBrokerBaseURLFile`); `docs/contributors/decisions/working-notes.md` (D105, D106, **D115**).

### DF83 — vm-enhanced (Kata+Firecracker) CVE exposure verified: no floor needed now, but the install hint is broken

- **Discovered:** 2026-07-14 · **Workstream:** sandboxing-blog research (gap analysis)
- **Severity:** LOW (verified no live CVE exposure) / MEDIUM for the separate install-hint defect
- **Disposition:** ADDRESSED-IN-PLACE for the CVE question (verified, no action needed); ADDRESSED-IN-PLACE for the install-hint defect (fixed this pass)
- **Description:** Two CVE IDs surfaced in earlier secondary-source research (CVE-2026-5747, CVE-2026-1386) were verified against NVD and the firecracker-microvm GitHub security advisories directly. Both are real, both are genuine Firecracker VMM vulnerabilities: **CVE-2026-5747** (GHSA-776c-mpj7-jm3r, virtio-pci OOB write, opt-in `--enable-pci` only, CVSS 7.5–8.7, affects 1.13.0–1.14.3 and 1.15.0, fixed in 1.14.4/1.15.1) and **CVE-2026-1386** (GHSA-36j2-f825-qvgc, jailer symlink-following arbitrary host-file overwrite, CVSS 6.0, affects ≤1.13.1 and 1.14.0, fixed in 1.13.2/1.14.1). Kata Containers' own `versions.yaml` (main branch) pins `assets.hypervisor.firecracker.version: v1.12.1` — below the affected range of both CVEs, so yoloAI's `vm-enhanced` mode has no current exposure to either. Kata itself has six unrelated 2025-2026 CVEs, but the two hypervisor-specific ones (CVE-2026-24834, CVE-2026-47243) affect Cloud Hypervisor/QEMU, not Firecracker. **Separately:** the install hint in `runtime/containerd/caps.go` (`buildKataShimV2Cap`, `buildKataFCShimV2Cap`) and `containerd.go` — `sudo apt install kata-containers` — did not correspond to any currently maintained package; Kata's apt/OBS repo was archived in 2021 and only ever covered the legacy 1.x series. Fixed by replacing all three call sites with the verified-current `kata-manager.sh -o` install-only command (a new shared `kataManagerInstallOnly` constant in `runtime/containerd/caps.go`), confirmed verbatim against upstream `utils/README.md`. Also fixed a second, related instance of doc rot found while verifying this: the devmapper snapshotter's `Fix` step pointed at a dead Kata doc URL (`docs/how-to/containerd-kata-fc-for-ubuntu.md`, 404), replaced with the live `docs/how-to/how-to-use-kata-containers-with-firecracker.md` (verified 200).
- **Trigger / fix:** CVE question needs no action now — revisit if Kata's `versions.yaml` ever bumps Firecracker into the 1.13.0–1.15.0 range without also picking up 1.14.4/1.15.1/1.13.2/1.14.1. Install-hint fix: already implemented this pass.
- **Pointer:** `runtime/containerd/caps.go` (`kataManagerInstallOnly`, `buildKataShimV2Cap`, `buildKataFCShimV2Cap`, `buildDevmapperSnapshotterCap`); `runtime/containerd/containerd.go` (`InstallHint`); upstream `kata-containers/kata-containers` `versions.yaml`, `utils/README.md`.

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

### DF45 — base-image build lock is keyed by data-dir but the image tag is global to the docker daemon

- **Discovered:** 2026-06-24 · **Workstream:** public-layering Shape (concurrency question raised during the smoke)
- **Severity:** LOW (benign redundancy, **not** corruption — surfaced for the multi-principal/[D62](../decisions/working-notes.md) direction)
- **Disposition:** PARKED (single-data-dir behavior is correct; **Trigger:** the multi-principal daemon that serves several data dirs against one docker daemon)
- **Description:** `Setup` serializes base-image builds with a proper double-checked `flock`: acquire `layout.DockerBaseLockPath("yoloai-base")` → re-check `imageExists` + `NeedsBuild` **inside** the lock → build only if needed → write the checksum inside the lock. So concurrent `yoloai new` within one data dir **cooperate** (one builds, the rest block then skip — no double build, no checksum race, no tag stomp). BUT the lock path derives from the **data-dir** (`layout`), while the image tag `yoloai-base` is **global to the docker daemon**. Two `yoloai new` with *different* `--data-dir` against the *same* daemon (the D62 multi-principal case) do **not** serialize on this lock → redundant concurrent `docker build` of the same global tag, last-write-wins. Benign (wasted work; per-data-dir checksum files don't corrupt each other), but a latent inefficiency the multi-tenant work should account for — e.g. namespace the tag per principal, or key the lock on the global image name rather than the data dir. Ties into the [shared-state-concurrency](research/shared-state-concurrency.md) research (D87): "is the lock keyed to the same scope as the resource it guards?"
- **Pointer:** `runtime/docker/docker.go:332` (`Setup`, the double-checked lock), `runtime/docker/base_lock.go` (`AcquireBaseLock` → `DockerBaseLockPath`), `runtime/docker/build.go:42-54,134` (checksum). Tart mirrors the same pattern.

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

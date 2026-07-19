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

- **Discovered:** 2026-06-29 · **Workstream:** DF66 (C1) implementation — host git on the agent-controlled work copy. **Updated 2026-07-04** (added apple; corrected the probe analysis; fsmonitor now globally disabled). Fix designed in [plans/confine-host-side-git.md](plans/confine-host-side-git.md) (+ [macOS build brief](../archive/plans/confine-host-side-git-macos-build.md)).
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
- **Rides:** **any** release. Out of v0.9.0, 2026-07-17 — see below; the earlier claim that it was cheap only inside a breaking release does not survive.
- **Rescoped 2026-07-17, before building it: tart is not the only one, and the fix is not a tart fix.** [DF135](#df135) records that **containerd** drops `-p` identically, while loading the CNI portmap plugin so that it looks wired. The cause both share is that `runtime.BackendCaps` declares no port-forwarding capability, so a backend can silently not implement one. The right fix is therefore a declared capability plus one central rejection — not a guard bolted into tart, which would be the ad-hoc one-off convention this project treats as a hygiene defect in its own right. That also needs a call on **seatbelt**, whose sandbox is a host process with no NAT layer: `-p` there is meaningless rather than dropped.
- **Why it left v0.9.0.** The rejection is breaking, so the argument was that it should ride a release that already breaks. But it touches no persisted state and needs no migration, so landing it later merely escalates a later release — which during beta costs approximately nothing. That is the same reasoning that descoped DF104, and it applies here identically. DF126 rode v0.9.0 because a later fix would have cost a *second migration*; nothing else in the candidate set had that property, which is why every other scope argument collapsed on inspection.
- **Description:** Tart's production run path (`buildRunArgs` → `Start`) never adds any `--net-softnet*` arguments, so a user's `-p` port mappings (`cfg.Ports`) are silently dropped — a tart sandbox gets default VM networking with no port forwarding and no `--network-isolated` enforcement. This was masked by `BuildNetworkArgs`/`portForwardArgs`, which built the args correctly but **were never called in production** (dead code with passing unit tests, removed during the pre-merge audit). The unit tests gave false confidence that ports worked.
- **Trigger:** before tart is positioned for workloads that need port forwarding or network isolation — wire `BuildNetworkArgs`-equivalent logic into `buildRunArgs`, flip the descriptor's `NetworkIsolation`, and verify with real `--net-softnet` on macOS.
- **Pointer:** `runtime/tart/tart.go` (`buildRunArgs`, `Start` — no network args); descriptor `NetworkIsolation: false`.

### DF13 — a swallowed submit key parks the agent on a prompt it never runs (the trust dialog is one cause, not the cause)

> **Title and scope corrected 2026-07-17** (was "Restart prompt re-injection races Claude
> Code's folder-trust dialog (second prompt dropped)"). Two claims in the original are now
> known to be narrower than the defect: it is **not restart-only** — the initial `new` leg
> does it too — and the **trust dialog is not necessary**. Both corrections are evidenced
> below. The mechanism is still **unknown**: 34 controlled trials failed to reproduce it.
> Read the 2026-07-17 evidence before theorising, and especially before re-running the
> approaches already ruled out.

- **Discovered:** 2026-05-31 · **Workstream:** W-L1 (G7, surfaced by smoke run `yoloai-smoketest-20260531-233151.431`)
- **Severity:** LOW — rare (8 of 191 recorded smoke runs) and **loud**: the agent stalls and the caller times out at 90s. No silent corruption. It is a bad failure to *own* rather than a dangerous one: the user's first prompt is silently not run.
- **Disposition:** PARKED — mechanism unreproduced. **Mitigated 2026-07-17, not fixed:** `deliver_prompt` is now closed-loop (confirm the prompt left the input box, re-send the submit key up to 3×, and log `prompt.submit_retry` / `prompt.submit_unconfirmed`). That recovers the observed failure whatever causes it, and turns a silent stall into a named log event — but the race itself is still there, and a retry firing in the wild is the signal that it fired.
- **Re-audited 2026-07-17** against smoke run `yoloai-smoketest-20260717-033706.736` (`stop_start/seatbelt`, commit `a0b4d7c4`, Claude Code v2.1.212):
  - **The signature.** The prompt text sits **composed in the input box, unsubmitted**; context reads `0k/200k (0%)`; `agent-hooks.jsonl` is **0 lines**. So the agent never processed a turn — the text arrived and the submit did not. The input box also carries **trailing blank lines** where the submit keys went, which is what "the Enter became a newline" looks like on screen.
  - **Not restart-only.** This was the initial `new` leg (sentinel `done`, not `done2`). The original's restart framing is an artefact of where it was first seen.
  - **The trust dialog is not necessary**, but *not* for the reason first argued here. The failing snapshot shows no dialog — which proves nothing, because it is captured ~95s in, long after any startup dialog would be gone (that inference was made and retracted the same session). The real evidence is direct: driving Claude Code v2.1.212 by hand, the dialog **does** still appear despite the seeded `hasCompletedOnboarding` + sentinel `lastOnboardingVersion`, and `wait_for_ready` auto-accepts it correctly (it checks `"Enter to confirm"` *before* `ready_pattern`, so the dialog's own `❯` cannot be mistaken for readiness). So the dialog is handled on the path that failed.
  - **Not a regression** from the apple/label-equality work merged at `a0b4d7c4`: `stop_start/seatbelt` passed 4/4 re-runs on that exact commit, and nothing in it touches tmux prompt delivery.
- **Ruled out by experiment 2026-07-17 — do not re-run these.** 34 trials, none reproduced the stall:
  - *Paste→Enter timing gap* — forced both delivery sleeps to 0 (maximally adversarial): **3/3 passed**. Not timing between the paste and the submit key.
  - *Reader starvation coalescing the keys into one paste burst* — 24-32 CPU spinners on 10 cores (load avg 60-98), driving a real Claude Code v2.1.212 through the exact `wait_for_ready` + `deliver_prompt` sequence: **15/15** into a settled REPL and **8/8** against a freshly started one, raw and bracketed alike. Not paste-window absorption in either state.
  - *Load alone via the real harness* — one stall in 2 loaded runs looked like a reproduction; the unfixed control then passed **5/5** under identical load. That first hit was luck, and the A/B cannot distinguish. **Beware:** this is the trap the finding is prone to — a rare flake makes any small sample look causal.
- **A separate, deterministic defect found while investigating this — fixed 2026-07-17, and NOT this bug.** `tmux paste-buffer` without `-p` rewrites every LF to a CR (tmux(1)); Claude Code eats the CR and **joins a multi-line prompt into one line**, so every multi-line prompt on every backend was delivered mangled. Verified by hand: raw joined the lines 3/3, `-p` preserved them 3/3. Claude Code requests bracketed paste (`[?2004h`) and yoloai was ignoring it. All four paste sites now pass `-p`. It is recorded here because it was found here — it is not a candidate mechanism for the swallowed submit, and must not be cited as this row's fix.
- **What would settle it.** The stall has only ever been observed through the full smoke matrix, never in isolation, so the next attempt should instrument rather than theorise: have `deliver_prompt` capture the pane immediately before and after each submit key (and dump the raw pane bytes, not the rendered text), then run the matrix until it trips. The open question is narrow — the text reaches the input box, so input is being accepted; what happens to the key that should send it?
- **Description:** On the `stop_start` restart leg (`restart` → `sb.Restart(StartOptions{Prompt:…})`), Claude Code v2.1.157 shows a "Quick safety check: Is this a project you trust?" dialog at startup whose selector line begins with `❯` — the same readiness pattern the prompt-injection waits for. The relaunched agent reached the welcome screen and sat idle at the ready prompt; the staged second prompt (`prompt.txt` correctly held the `done2` task) was never executed, so `files/done2` was never created and the test timed out (31s gap). Likely mechanism: the injected prompt + Enter is consumed by the trust dialog (Enter confirms "Yes, I trust this folder") rather than delivered to the agent REPL, dropping the task text. Non-deterministic: only podman failed this run (docker recovered on retry; docker-cenhanced/containerd-vm/vmenhanced passed). Matches the known podman network-flake family ("network: unreachable"). **NOT a regression** from the G7 carves — those relocate host-side Go functions and never touch entrypoint, start/restart, or tmux prompt injection (the `StartOptions.Prompt` path is unchanged; only `ResetOptions`/`Reset` were modified). Needs a reproduction before any fix; candidate remedy is to make restart prompt-injection wait for the *post-trust-dialog* steady-state ready prompt (or pre-trust the work copy) rather than the first `❯`.
- **Pointer:** `runtime/monitor/sandbox-setup.py` (`deliver_prompt`, `prompt_pending_in_input` — the closed loop; `wait_for_ready` — the `"Enter to confirm"`-before-`ready_pattern` ordering that makes the dialog safe); `runtime/monitor/tests/test_prompt_submit.py` (the stuck box, built deliberately); `internal/cli/lifecycle/restart.go:74`; autopsies `.testcache/runs/yoloai-smoketest-20260717-033706.736/sandboxes/stop_start/seatbelt/attempt1/FAILURE.md` (the 2026-07-17 re-audit) and `.testcache/runs/yoloai-smoketest-20260531-233151.431/sandboxes/stop_start/podman/attempt1/FAILURE.md` (as first filed).

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
- **Rides:** a **breaking** release *when built* (it changes a default) — but the scope test is not what gates it: this is D95's phased build, not a flag flip, and until that path exists an opt-in mount removes the only auth the non-brokered agents have. Out of v0.9.0, 2026-07-17.
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
- **Rides:** **any** release. Deferred out of v0.9.0, 2026-07-17: the fix is not release-gated, its verification needs a v6-routable network per backend, and the claim was corrected in the meantime. See [ipv6-network-isolation.md](plans/ipv6-network-isolation.md).
- **Description:** `runtime/docker/resources/firewall.py` builds the default-deny + allowlist as **IPv4 `iptables`/`ipset` rules only**. `grep -rn ip6tables` across the whole repo — Go, Python, shell — returns **nothing**, so no backend has ever filtered IPv6. Verified live in an apple sandbox created with `--network-isolated`: the IPv4 `OUTPUT` chain is correct and complete (gateway:53 accepted, `allowed-domains` ipset matched, everything else REJECTed), while `ip6tables -L OUTPUT` is **empty with policy ACCEPT**. The guest is not v6-less: it holds a global-scope address (`fd96:…/64`) and a v6 default route via the vmnet gateway.
- **Why it is not exploitable today, which is the whole of the mitigation:** the address vmnet hands out is a **ULA** (`fd00::/8`, unique-local, not internet-routable), so there is no v6 egress to allow or block — a `curl -6` to a non-allowlisted host fails with exit 7 like everything else. Nothing in the design guarantees that stays true. It rests on Apple's vmnet choosing not to hand out a GUA, which is not a contract we hold, is not asserted anywhere, and no test would notice it changing.
- **Two ways it turns live:** (1) any backend whose network gives the guest a routable v6 — Docker with `--ipv6` enabled, a future vmnet, an IPv6-capable CNI on containerd — silently loses the allowlist entirely for v6 traffic, because the rules simply don't exist there; (2) an allowlisted domain that resolves v6-only would still be reached, but **unfiltered** — the allowlist would not be what let it through.
- **The honest framing:** the capability is declared `NetworkIsolation: true` on every backend that sets it. That claim is IPv4-scoped and says so nowhere. Either the rules should cover v6, or the guests should have v6 disabled (`net.ipv6.conf.all.disable_ipv6=1` in the entrypoint) so the claim is true by construction.
- **Correction, 2026-07-17 (owner's call): the recommendation above is wrong — take the first route, not the second.** The original preferred disabling v6 as "cheaper, and matching how the isolation already prefers *impossible* over *filtered*". **That parallel does not hold.** Preferring "impossible" is right for a protocol nothing ever promised to route; it is not right here, because **the allowlist is a promise about destinations, and the destination does not choose its family.** `resolve_domains` asks for `AF_INET` only (`firewall.py:73`), so a domain the user *explicitly allowlisted* that resolves AAAA is, under a v6 disable, **unreachable** — and under a v6 default-deny with no v6 allowlist, **blocked**. Either way the allowlist silently fails to *allow*, surfacing as a network fault rather than a policy decision. Both shortcuts are therefore not cheap-but-adequate; they are **wrong**, and they regress legitimate use. Proper family-aware support is the only route that keeps the promise, and the only thing it takes away is v6 egress to *non*-allowlisted hosts — which is the hole, not a feature. Sized and planned in [ipv6-network-isolation.md](plans/ipv6-network-isolation.md); DF134 is the same family-blindness biting one step earlier.
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


### DF112 — `backend-idiosyncrasies.md`'s storm entry explains "passes on a cold retry" with VirtioFS persistence that a read-only mount cannot provide

- **Discovered:** 2026-07-15 · **Workstream:** DF111 re-investigation on Apple Silicon
- **Severity:** LOW as a docs defect — but the claim is load-bearing for the entry's model, and that entry is the incumbent explanation for DF111, so a wrong premise there costs debugging time downstream (it cost this workstream a full wrong turn)
- **Disposition:** PARKED — **the defect is verified by experiment**; the doc is left uncorrected because rewriting the entry means stating what *does* explain "passes on a cold retry" for the *production* reports it cites, and that is still unknown. DF111 turned out to be an ordering bug in yoloai's own setup (resolved), not this storm — so the entry is now not just mis-premised but pointing at the wrong cause for the symptom in its own Symptom Index. Fix it as a standalone docs commit.
- **Description:** The "Same storm, host side — the baseline-SHA `git` fails with exit 69" subsection explains why the failure clears on a retry like this: "It passes on a cold retry because firstlaunch state persists in the host Xcode.app via VirtioFS, so the second VM finds it done and never raises the storm." But the live sandbox mounts Xcode **read-only** — `runtime/tart/tart.go:719` sets `ReadOnly: true` on every detected Xcode path, and the base-build path mounts `:ro` too (`build.go:569`). A VM cannot write firstlaunch state into a read-only VirtioFS mount, so it cannot persist there and a second VM cannot "find it done" by that route.
- **Verified on Apple Silicon 2026-07-15 — the claim is false.** Two fresh `yoloai-base` clones, each booted with Xcode mounted `:ro` exactly as the backend mounts it. VM A: `xcodebuild -checkFirstLaunchStatus` → non-zero (first-launch needed), ran `-runFirstLaunch` to completion, then `-checkFirstLaunchStatus` → **0** (done, state recorded *inside VM A*). VM B, cloned fresh **after** A finished: `-checkFirstLaunchStatus` → **non-zero again** — B must redo the whole thing, and did (8s, its own "Install Started → Install Succeeded"). So firstlaunch state does **not** cross VMs, exactly as the read-only mount predicts, and "the second VM finds it done" never happens. It persists *within* a VM and dies with it, like the licence record (DF114).
- **The knock-on: the entry's whole first-VM-only model fails here, and its Symptom Index row is now known to be wrong.** The exit-69 string it maps to this storm entry was root-caused instead to a switch-before-accept ordering bug in `sandbox-setup.py` (DF111, resolved 2026-07-15) — reproduced at 1-in-10 and driven to 0-in-36 by reordering, with the storm never reproducing at all. If every VM re-runs firstlaunch, then by the entry's own logic every sandbox raises the storm — yet the tier passes consistently and 90 one-second git probes during a real `-runFirstLaunch` came back 90/90 clean, as did `shutil.which`-equivalent and direct-stat probes for tmux (the entry's *primary* symptom). Measured duration was **5s and 8s**, against the entry's "60-120s+". The entry's historical observations are cited with real run IDs and commit hashes and are not disputed — something changed (Xcode version, base image contents) such that firstlaunch now has almost nothing to do on this host. But as written, the entry describes a mechanism that no longer reproduces and explains it with a persistence route that cannot exist.
- **Caution for whoever rewrites it:** the storm claim justifies two shipped mitigations — `tmux_io.tmux_bin`'s 240s probe loop and `execVMSetupWithStormRetry` — so "it doesn't reproduce today" is not licence to delete them. It is licence to stop treating the entry as the explanation for DF111.
- **Also worth recording: `xcodebuild` overloads exit 69.** `-checkFirstLaunchStatus` returns **69 with empty output** to mean "first-launch tasks are needed", on a VM whose licence is accepted and whose `git --version` exits 0. So exit 69 alone does **not** imply the licence error — `isFirstlaunchStormTransient` is right to require the "Xcode license" string as well (`vmworkdir.go:122-128`), and any future check here must do the same.
- **Pointer:** `docs/contributors/backend-idiosyncrasies.md` ("Same storm, host side — the baseline-SHA `git` fails with exit 69", and the Symptom Index row mapping the exit-69 string to it); `runtime/tart/tart.go:719` (`ReadOnly: true`); `runtime/tart/build.go:569` (`:ro`); `internal/orchestrator/launch/vmworkdir.go:122-128` (the string check that saves it). Related: DF111, DF114.

### DF113 — `destroy` frees the sandbox name while leaving the instance behind, so the next `start` adopts a VM it never provisioned

- **Discovered:** 2026-07-15 · **Workstream:** the DF110 fix on Apple Silicon — this is the same shape in shipped code, found while establishing that DF110's severity was the false PASS rather than the confusing failure
- **Severity:** MEDIUM — **confirmed by reproduction on Apple Silicon 2026-07-15, not inferred.** The MEDIUM/CRITICAL question was whether an adopted VM's stale work silently diffs against the wrong baseline; it does not. `diff` **fails closed**. Nothing here is a regression or a release blocker; the trigger needs a *failed* instance removal, which is not the common path.
- **Disposition:** PARKED — filed, not fixed (owner's call, 2026-07-15). The DF110 scope was the test tier, and the remedy here touches the shipped `start` contract: the "already running" no-op is *correct* for `yoloai start` on a live sandbox, so a guard has to distinguish "mine, already up" from "predates me" without breaking idempotency.
- **It is not schema-gated, and three arguments that it was are withdrawn (2026-07-17).** The claim
  originated as a one-line gloss on the release-staging page — *"wants a provenance field in
  metadata, i.e. schema"* — and was then defended three times, each time from prose rather than from
  the struct: that the field needed a read-time backfill; that it needed the v4→v5 library
  migration's backfill (which conflated `environment.json`'s own `metaVersion` ladder with the
  library realm's `.schema-version`); and that it needed a `metaVersion` 3→4 bump, which would force
  every sandbox through `system migrate` and so was free only inside a release already forcing that.
  Each argument was better-formed than the last and all three rest on the same unexamined premise:
  **that a new field is needed at all.**
- **`Environment.CreatedAt` already is the fact.** `internal/orchestrator/create/create.go:701`
  writes `CreatedAt: time.Now()` — a per-sandbox timestamp, on disk, written by the create that
  provisioned the instance, present on every record. That is precisely what a guard needs to know it
  did not provision what it found: an instance older than the sandbox claiming it cannot be that
  sandbox's.
- **The missing half is a runtime capability, not a schema.** Nothing in `runtime.Runtime` or
  `runtime_optional.go` reports an instance's creation time (or any identity a leftover could not
  forge), so the guard needs a new **optional interface** — the shape D126 used for
  `runtime.Renamer`, added with no schema bump and no migration. Interfaces are additive and ship in
  any release. Whether age-comparison is the right guard, or an instance-carried marker is, remains
  the open design question the disposition above names; **neither answer is release-gated**, which is
  the only thing the scope decision needed.
- **Verified:** the adoption itself, by planting a running VM at the computed instance name and running the tart tier against it — `start` no-ops in 0.90s with no `recreating container` line, `BaselineSHA` stays empty, and the in-VM `git -C` hits a work dir the leftover never had. That reproduction ran under a *test* principal, but the code path is `lifecycle.start` unmodified; only the principal differs.
- **Reproduced end-to-end on real tart (2026-07-15).** Create sandbox for project P → start (VM provisions, `BaselineSHA=bc044c86…`, work dir at the encoded host path) → plant `round1-marker.txt` inside the VM's work dir → remove **only** the sandbox dir, leaving the VM running (teardown's exact end state: `Remove` failed and was ignored, `environment.json` gone, name freed) → re-create the **same name against the same project dir** → start. Results:
  - `create` → **no error**: the name was freed, exactly as `teardown.go:54-57` intends.
  - `start` → **no error, 18.5ms** (against ~29s to genuinely provision), no `recreating container` line — **adopted**.
  - `BaselineSHA` → **empty**: work-dir setup skipped.
  - Reading `round1-marker.txt` from the new sandbox → **`"ROUND1-SECRET-CONTENT"`**. The new sandbox is handed the previous sandbox's work, readable, at exactly the path it expects — because the VM work path is the encoded *host* path (`tart.go:181`) and is therefore byte-identical across sandboxes for one project.
  - `copyflow.GenerateDiff` → **errors**: `sandbox has no baseline SHA — was it created before diff support?`
- **Why this stays MEDIUM: it fails closed.** The empty `BaselineSHA` stops `diff` before it can produce a wrong-baseline patch, so there is no silent corruption and no bad `apply`. The real harm is narrower but real: an agent begins work on top of a previous sandbox's uncommitted changes while believing it has a fresh copy of the project, and `diff`/`apply` then refuse — so that work is stranded rather than lost. Adopting across *different* projects is self-limiting for the same reason the same-project case is dangerous: a different host path encodes to a different VM path, which the adopted VM does not have, so it breaks rather than mixes.
- **A second defect the reproduction surfaced:** `GenerateDiff`'s error blames the wrong thing — "was it created before diff support?" — when the actual cause is adoption. It replaces a real, knowable cause with a speculative hint about ancient sandboxes, which is precisely the wrong direction to send the next reader. Worth fixing with (or before) the guard.
- **Pointer:** `internal/orchestrator/lifecycle/start.go:370-372` (the no-op); `internal/orchestrator/create/create.go:135-174` (no runtime guard); `internal/orchestrator/launch/teardown.go:52,54-58` (ignored `Remove`, name freed on purpose); `runtime/tart/tart.go:181` (encoded work path). Related: DF110 (same shape in the test tier, fixed there by reaping under the test principal), D62/DF19 (principal scoping).


### DF128 — the gate tells you to hand-inspect a half-initialized first run that `system migrate` repairs deterministically

> **Reframed 2026-07-17, before anyone acted on it.** First filed as "migrate silently launders a
> missing library realm", severity MEDIUM, on the theory that `TOP/cli` without `TOP/library` meant
> a populated install had lost every sandbox (blamed on a wrong `--data-dir` or a partial restore —
> both speculation, neither observed). The owner asked the obvious question the filing had skipped:
> *is this a half-initialized dir that crashed, or a migration?* It is the former, and that inverts
> the finding. `initFreshDataDir` creates the CLI realm and **then** the library realm, so any
> interruption between the two lines leaves exactly that state on a **first run with no sandboxes in
> it**. Migrate's repair is therefore correct, not laundering, and the remaining defect is only the
> gate's message. Kept as a correction rather than rewritten silently: the original reading is the
> one a reader will arrive at unaided, and the refutation is the useful part.

- **Discovered:** 2026-07-17 · **Workstream:** reading `clischema.go` while auditing the deprecation register's flat-v0 entry (D127) — the branch that turned out not to be dead code
- **Severity:** LOW — a wrong error message on a recoverable state, not a data-integrity problem. Nothing is deleted, nothing is lost, and the repair that runs is the right one. The cost is that a user whose first run was interrupted is told their data dir is in a state that "should not happen" and to go inspect directories by hand, when one documented command fixes it.
- **Disposition:** ADDRESSED-IN-PLACE (P1 of [init-sentinel.md](plans/init-sentinel.md), 2026-07-17). The fix is to make the gate more helpful, not to make migrate more paranoid — which is the reverse of what the original filing implied, and the reason it is worth stating explicitly. A `TOP/.initializing` sentinel, written before either realm and cleared after both, now makes "interrupted first run" a fact on disk instead of an inference: the gate retries the build directly, `MigrateCLI` recognizes the same state instead of hitting its unrecognized-directory default, and — the part this finding is actually about — the exactly-one-realm-Fresh message stays exactly as loud as before when there is no sentinel, which is the genuine anomaly this finding says must not be silenced. P2 (an emptiness-gated destroy-and-retry) remains undone; the plan's Status line tracks that.
- **Description:** the startup gate maps "exactly one realm Fresh" to `InconsistentDataDirError` — *"this should not happen — inspect the directory manually"* — and deliberately does not name migrate (`gate.go`: *"a realm went missing; loud, does not point at migrate"*). But `system migrate` is gate-exempt, is the only other command that runs, and repairs the state cleanly. **The gate and migrate disagree about the same directory**: one calls it unexplainable, the other fixes it without comment.
- **Both reachable states are benign, and both are repaired correctly.** Verified by execution 2026-07-17 against the real binary:
  - **`TOP/cli` present, `TOP/library` absent** — an **interrupted first run**: `initFreshDataDir` does `CreateFreshCLI()` then `sys.CreateDataDir()` (`gate.go`), so a crash, an error, or a Ctrl-C between them lands here, on an install that by definition holds nothing yet. `system migrate` creates the library realm and stamps it. Correct.
  - **`TOP/library` present, `TOP/cli` absent** — an embedder rooted at `TOP/library` (which is exactly where the CLI's own `LayoutForDataDir` puts it) on a TOP the CLI later runs against. `system migrate` adopts it via `MigrateCLI`'s stamp-only branch. Correct.
- **What "a realm went missing" would have to mean.** The gate's phrasing assumes a populated install lost a realm. That is possible, but nothing observed produces it, and it is indistinguishable on disk from the two benign cases above — so the message pessimises the routine case to warn about a hypothetical one, and gives no actionable advice in either. If it stays loud for anything, it should be for a `TOP/library` that vanished *from an install that had sandboxes*, which the gate would have to record to know.
- **Pointer:** `internal/cli/gate.go` (`initFreshDataDir`'s ordering — the cause; `checkDataDirStatus`'s exactly-one-Fresh branch and its "does not point at migrate" note — the defect); `internal/cli/cliutil/clischema.go` (`MigrateCLI`'s stamp-only branch, the repair, and the reason it is not dead code); `yoerrors/errors.go:149-159` (`InconsistentDataDirError`); D60/D61 (the two-realm split); [D127](../decisions/working-notes.md) (the audit that surfaced it).

### DF129 — the provenance hook counts naming a path as reading it, so a long session launders its own citations

- **Discovered:** 2026-07-17 · **Workstream:** measuring the noise of D122's hook before extending it to source paths — the measurement demonstrated the defect on itself
- **Severity:** LOW–MEDIUM. It weakens a gate rather than breaking anything, but it weakens it **exactly where it is needed most**: the longer the session, the more paths have been named for reasons that are not reading, so the check decays as context grows and the agent's memory of what it actually opened gets least reliable.
- **Disposition:** FILED, not fixed — and the fix is not obvious, which is why it is filed rather than patched. `READ_TOOLS` includes `Bash`, and the check is a substring test over tool inputs, so any command that merely *mentions* a path clears every citation of it. That is the deliberate over-match bias (D122: *"Over-matching here is the safe direction — it yields a false pass, where under-matching yields a false accusation, and an accusation is what gets a hook disabled"*). The bias is right; what was not anticipated is how routinely a session names a file without opening it.
- **Description:** the ways a path enters a Bash input without being read are ordinary, not exotic:
  - a **commit-message heredoc** — commit bodies name the files they change, constantly;
  - a path as a **string literal in a test** being written (`_SRC = "runtime/tart/prune.go"`);
  - a **grep for the filename itself**, i.e. asking "did I touch this?";
  - an **`echo`** in a diagnostic.
- **Verified by execution, on the hook itself (2026-07-17).** Replaying the checker over a real session's transcript against the four finding commits it produced reported **zero** blocks. Truncating each replay to the transcript as it stood *when the finding was written* reported **two**, both true positives (`lifecycle/restart.go:197`, `seatbelt.go:168,172` — line numbers taken from grep output for files never opened). The difference was entirely self-inflicted: the commands run to investigate the fire (`echo "=== the fire: where did I cite seatbelt.go..."`, a script listing `"seatbelt.go"`, a grep for `seatbelt\.go` in the transcript) put the basename in the blob. **Asking whether you read a file marks it read.**
- **Why this is not just "tighten the regex".** Telling a read from a mention means parsing shell, which is the under-match direction the hook refuses on purpose. Options worth weighing, none free: restrict `Bash` matching to the argument position of known readers (`sed`/`cat`/`less`/`head`/`tail`/`rg`); drop `Bash` from `READ_TOOLS` and rely on `Read`/`Grep`/`Glob` (under-matches every `sed -n` in this repo's idiom); or key the check on tool *results* being non-empty for that path, which contradicts the hook's founding asymmetry.
- **The sibling.** `READ_TOOLS` also contains `Task`/`Agent`, so naming a path in a **subagent prompt** counts as the parent having read it. That is the same shape and arguably worse: the parent gets a *summary*, which is the precise thing the hook exists to distinguish (*"a summary in context and a source you read are indistinguishable from the inside"*). Delegation is most of how the work is done, so this is not a corner.
- **Pointer:** `scripts/check_citation_provenance.py` (`READ_TOOLS`, `read_tool_inputs`, and the substring test in `unread_citations`); `scripts/tests/test_citation_provenance.py` (`test_grep_and_bash_count_as_going_to_look` pins the behaviour, correctly, as intended); D122; [`research/llm-shaped-repos.md`](research/llm-shaped-repos.md) Part 7 "No provenance on a fact".

### DF130 — the deprecation register's gate can only see entries that exist, so an unregistered migration passes

- **Discovered:** 2026-07-17 · **Workstream:** auditing the instruction corpus for rules that assume a human reader — the audit found this in a gate written four hours earlier, by the same session that wrote the rule about it
- **Severity:** LOW–MEDIUM. Nothing breaks; the register just silently under-reports, which is the exact failure it was built to end. The historical base rate is the argument: the audit that motivated [D127](../decisions/working-notes.md) found **16** compatibility mechanisms and **0** recording a date, so the unassisted compliance rate for "remember to register it" is measured at zero, and this gate does not move it.
- **Disposition:** FILED, not fixed — closing it needs a code-side oracle that can enumerate migrators, which is a design question rather than a patch.
- **Description:** `TestRepoHygiene_DeprecationsAreDated` is strict about entries: each needs a parseable `Incurred:` and `Due:`, `Due` may not precede `Incurred`, and a `Retire by:` must be stated. But it **iterates the entries in the file**. A migration written and never registered produces no entry, so there is nothing to iterate and the gate passes green. AGENTS.md rule 9 asks the author to register it — and "remember to write the entry" is precisely the instruction shape that does not work on a reader with no durable memory (`research/llm-shaped-repos.md` Part 7, "Absence has no representation": *"The agent has the grep output and nothing else. Absence is not a fact it holds; it is a non-event."*).
- **The gate saw the hole one level up and not this one.** It already refuses an empty register — *"either every compatibility mechanism was retired (celebrate, then delete this gate) or the gate is checking nothing, which is the failure mode DF94 documents"* — so its author reasoned about absence at the whole-file level and not per-entry. Worth naming: the same blind spot, in the same function, at two scales.
- **Shape of a fix.** A migrator is structurally detectable in this codebase — framework migrators are assembled in `System.frameworkMigrators`, and the sealed ladder's rungs are `case` arms in `migrateLibraryStep`. A test that enumerates those from code and requires a matching register entry closes it by set difference, the class `llm-shaped-repos.md` Part 7 says belongs to a machine regardless of who is typing. That is the same move as `check_breaking_changes.py`, against a code-side oracle rather than a doc-side one.
- **Pointer:** `repo_hygiene_test.go` (`TestRepoHygiene_DeprecationsAreDated`, and its empty-register guard); `docs/contributors/deprecations.md`; `internal/config/schema.go` (`migrateLibraryStep`'s rungs — a candidate oracle); D127; DF94 (the gate-checking-nothing class).

### DF131 — rule 2's cross-file name sweep is ungated for every tier except `architecture/`

- **Discovered:** 2026-07-17 · **Workstream:** the instruction-corpus audit; ranked as the highest-value unbuilt gate in the corpus
- **Severity:** MEDIUM, with a shipped instance as evidence: the `backend` config key was renamed and its dead name survived in `internal/cli/helpcmd/help/*.md` — `//go:embed`ed, shipped UI — through 15 releases with `make check` green throughout. The cost is not tidiness; it is a user reading shipped help text that names a key the binary rejects.
- **Disposition:** FILED, not fixed. D124 built exactly this gate and scoped it to `architecture/`; AGENTS.md states the scope honestly (*"`architecture/` now fails loudly rather than drifting quietly. **No other tier has that backstop.**"*). Extending it is a build, not a decision.
- **Description:** rule 2 asks a reader to `git grep` an invalidated name tree-wide and fix every live mention, and the PR procedure adds *"Check every block within a file, not just the first hit"* — evidence being PR #36, which fixed a help topic's examples and left the settings table naming the dead key 13 lines above. Both instructions ask for **thoroughness against absence**: an unfixed second block produces nothing to notice unless it was in the grep output. That is the failure mode a machine does not have.
- **Why it is gateable.** It is a set difference, which is the argument of `research/llm-shaped-repos.md` Part 7 (*"a machine does, in milliseconds, because it is a set difference"* — the `YOLOAI_TEST_TART` vs `YOLOAI_TEST_TART_VM` row). Both oracles already exist: `config.IsKnownConfigPath` is the code side, and `internal/cli/docs_sync_test.go`'s extractors (`extractKeySettingsKeys`, `extractConfigCommandKeys`, `extractGuideSettingsTableKeys`) are the doc side — `TestDocsConfigKeysResolve` already runs that comparison for config keys. What is missing is the same treatment for the rest of rule 2's surface list, with the append-only sinks excluded.
- **Note the direction already covered.** Docs naming a dead key is gated for config keys today. The reverse — code removing a name — is now gated for removals by `scripts/check_breaking_changes.py`. This finding is the remainder: the other doc surfaces, and names that are neither config keys nor flags (agent names, backend names).
- **Pointer:** `docs/contributors/procedures/pull-requests.md` (rule 2 and its surface list); `internal/cli/docs_sync_test.go` (the extractors + `TestDocsConfigKeysResolve`, the working half); `repo_hygiene_test.go` (`TestRepoHygiene_ArchitectureDocRefs_Resolve`, D124's gate — the template); `internal/cli/helpcmd/help/*.md` (the shipped surface that drifted); D124.

### DF132 — three standing rules are written in the noticing register, which is the one register that does not fire

- **Discovered:** 2026-07-17 · **Workstream:** the instruction-corpus audit, commissioned after three consecutive misses of the same shape in one session
- **Severity:** MEDIUM as a class. Each rule is individually sound and each fails the same way: it is triggered by the reader *registering* something rather than by an observable condition in the work. On a reader whose fluency is constant and who holds no persistent model, a noticing-triggered rule does not fire late or partially — it does not fire.
- **Disposition:** FILED. Rewriting a principle is the owner's call, and the corpus already contains the pattern to rewrite them toward, so this is a scoped edit rather than an open question.
- **The three:**
  - **AGENTS.md rule 1 / GEN's breaking-change rule** — the trigger is the classification *"is this user-visible?"*, which needs a model of what the code is *for*. **Partially closed 2026-07-17** by `scripts/check_breaking_changes.py` for removals; changed defaults and newly-rejected input remain.
  - **AGENTS.md rule 7 / DEV §16** (file the defects you don't fix) — DEV §16 makes the best attempt available, naming the tell: *"The tell is a sentence like 'I'll just use X directly instead' or 'it works if I do Y first.'"* That is self-monitoring on one's own output, which is closer to a trigger than "be careful", but it still fires on introspection.
  - **GEN §13** (speak up against the plan) — *"if something looks off"*, then asks the reader to sort signal from mood. The moment it is meant to fire is the moment coherence pressure is highest: asserting the claim is what generates the defense of it. Its own worked examples are all concrete events (a detector blind to aliases; an audit that found a third surface) — the rule is written in the noticing register, the examples are not.
- **The pattern to rewrite toward is already in the corpus, stated once.** `general-principles.md` §7's D123 corollary: *"What makes this fire where 'be careful' cannot is the trigger: a returned delegation is an event you can see, unlike the uncertainty you don't feel."* GEN §13's surgery is to replace "if something looks off" with the visible events that historically preceded a §13 moment: a plan step whose acceptance criteria cannot be satisfied as written; a file the plan names that does not exist; a plan claim contradicted by code just read.
- **Evidence that the instruction form alone does not carry this.** D119 (*"a finding parks its fix, never its verification"*) is loaded every session, and [DF128](#df128--the-gate-tells-you-to-hand-inspect-a-half-initialized-first-run-that-system-migrate-repairs-deterministically) was filed with an unverified reachability claim by a session that could recite it. The rule was well-placed and well-worded; it still did not fire.
- **Pointer:** `AGENTS.md` (rules 1, 7); `docs/contributors/principles/development-principles.md` (DEV §16 and its tell); `docs/contributors/principles/general-principles.md` (GEN §13; §7's D123 corollary — the template); `research/llm-shaped-repos.md` Part 7 (the taxonomy); D119; DF128.

### DF135 — containerd loads the CNI portmap plugin and never feeds it, so `-p` is silently dropped there too

- **Discovered:** 2026-07-17 · **Workstream:** checking DF53's premise before building it — the finding names tart, so the question was whether tart is the only one
- **Severity:** MEDIUM (a user-visible option accepted and silently ignored; identical in effect to DF53, and worse in appearance)
- **Disposition:** FILED, not fixed. It is DF53's defect on a second backend, and the two should be fixed together by the same mechanism — see DF53's `**Rides:**` and the capability note there.
- **Rides:** **any** release, alongside DF53.
- **Description:** `runtime/containerd/cni.go:81` puts `{"type": "portmap", "capabilities": {"portMappings": true}}` in the CNI plugin chain — so the plugin is present, configured, and enabled. Nothing ever passes it any port mappings: `portMappings` appears **nowhere else in the package**, `setupCNI`'s signature (`ctx, env, layout, sandboxDir, containerName`) does not take ports, and `grep -rn Ports runtime/containerd/` returns nothing outside a comment. So `cfg.Ports` is dropped on the floor, exactly as in tart (DF53).
- **Why it is worse than tart's:** tart simply has no port code, so the absence is at least visible to anyone who greps. Here the plugin *is* wired into the chain, which reads as evidence that port-forwarding works — the configuration asserts a capability the invocation never supplies. DF53 was masked by dead-but-tested code (`BuildNetworkArgs`); this is masked by live-but-unfed config. Both make a reader confident for the same wrong reason.
- **The class, not the instance.** Checked all six backends: **docker** converts ports (`ConvertPorts`, `docker.go:504`); **podman** embeds `*docker.Runtime` and inherits it; **apple** consumes `cfg.Ports` (`apple.go:214`). **tart** (DF53) and **containerd** (this) drop them. **seatbelt** has no port code either, but its sandbox is a host process with no NAT layer, so a host port is already the port — `-p` there is meaningless rather than broken, and needs a decision rather than a fix. Nothing declares any of this: `runtime.BackendCaps` has `NetworkIsolation`, `CapAdd`, `HostFilesystem`, `ContainerAttach` and others, and **no port-forwarding capability at all**, which is why two backends can silently not implement it.
- **Pointer:** `runtime/containerd/cni.go:81` (the plugin config), `:222` (`setupCNI`, no ports parameter); `runtime/runtime.go:246-256` (`BackendCaps`, no port capability); `runtime/docker/docker.go:504,1058` (the working path); `runtime/podman/podman.go:87` (embeds docker); `runtime/apple/apple.go:214`. Related: DF53.

### DF136 — on tart/seatbelt the whole sandbox dir is agent-writable, so an agent can redirect `apply` to any host path

- **Discovered:** 2026-07-17 · **Workstream:** pre-release data-destruction audit (copy/diff/apply host surface)
- **Severity:** HIGH (security — defeats the review gate that is the product's core safety property), **macOS-only** (tart, seatbelt). Latent: needs a compromised/prompt-injected agent.
- **Disposition:** FILED, not fixed — needs a design decision, deferred to a macOS agent (owner's call, 2026-07-17). NOT a v0.9.0 blocker.
- **Verified (by me):** the two mount facts. `runtime/tart/tart.go:703` shares the entire sandbox dir into the guest **read-write** (`--dir sharedDir:sandboxPath`, no `:ro`); `sandboxPath` is `SandboxesDir()/name`, the directory `environment.json` lives in. `runtime/seatbelt/profile.go:193` grants `(allow file-read* file-write* (subpath sandboxDir))` over that same directory, at the invoking user's uid. Docker/containerd/apple mount only specific subpaths — not exposed.
- **The chain (traced by reading, not executed):** `store.LoadEnvironment` (`store/environment.go`) does a bare read+unmarshal with **no integrity check**. `copyflow.ApplyAll`/`ApplySeries` use the record's `Dirs[0].HostPath` **verbatim** as the target of a host-side `git apply` (`git.NewHost`). So an agent inside the confinement can rewrite `environment.json`'s `HostPath` to any path the invoking user can write (`~/.ssh`, another project), stage a matching fake work copy + `BaselineSHA`, and the next `yoloai apply` writes the agent's patch there. It defeats the gate via **raw file I/O, not git**, which is why DF35's hermetic-git-seal verification (git ops only) does not cover it.
- **Corrects a stale claim:** DF67 asserts *"`.meta` lives outside the sandbox, so the agent can't corrupt it"* — false for this metadata; `environment.json` sits inside the seatbelt grant and the tart share.
- **Shape of a fix (for the mac agent, not prescriptive):** mount the record read-only / keep it outside the writable share; or sign/verify it; or confirm the resolved apply target against the path recorded at create time before writing. `apply` also only prints its target *after* applying (`internal/cli/workflow/apply.go:364`) — showing it first would surface a redirect.
- **Pointer:** `runtime/tart/tart.go:703`; `runtime/seatbelt/profile.go:193`; `store/environment.go` (`LoadEnvironment`, no integrity check); `copyflow/apply.go`; `internal/git/ops.go` (host apply). Related: DF35, DF67.

### DF137 — plain `yoloai system prune` reclaims non-yoloai content on a shared daemon

- **Discovered:** 2026-07-17 · **Workstream:** pre-release deletion-verb audit
- **Severity:** MEDIUM (destroys unrelated daemon content, but only on a host shared with other Docker/Podman work; a yoloai-dedicated host is unaffected — which is the documented assumption)
- **Disposition:** **docker/podman RESOLVED 2026-07-18** (see "Fix landed" below); **apple deferred to the mac queue** (Apple's `container` CLI has no label filter, and it can't be verified off a Mac). The finding stays open until the apple half lands. Pre-existing (not a v0.9.0 regression).
- **Fix landed (docker/podman, 2026-07-18):** `PruneCache` (`runtime/docker/prune.go`) now scopes `ContainersPrune` by `com.yoloai.sandbox` (the identity label containers actually carry — *not* `managedLabel`, which the original "cheap mitigation" note got wrong, since containers don't carry it) and `NetworksPrune` by `managedLabel` (mirroring `VolumesPrune`; yoloai creates no networks, so this reclaims nothing today but never touches a foreign one). `splitCacheBytes` was scoped to match so the dry-run estimate no longer over-promises. BuildKit cache stays daemon-wide (unlabelable — owner's accepted call; help text + comments now say so plainly for *plain* prune, not just `--images`). **Verified end-to-end:** foreign stopped containers and unused networks on both docker and podman survived a plain `yoloai system prune`. Filed as a **bugfix, not a breaking change** (owner's call, 2026-07-18): whole-daemon prune was never a promised capability — the docs always scoped it to "a host dedicated to yoloai" — so a dedicated-host user sees no change and a shared-host user only stops having their unrelated content reaped. The apple deferred half keeps this finding open.
- **Verified (by me):** `system.go:847` calls `PruneCacheFor` **unconditionally** — `--images` sets only the *depth*, not whether it runs. So plain `yoloai system prune` reaches `runtime/docker/prune.go:157` `PruneCache`, which runs `ContainersPrune(filters.NewArgs())` (every stopped container), `BuildCachePrune(All:true)` (the whole cache), and `NetworksPrune(filters.NewArgs())` (all unused networks) — **all with empty filters**. Only `VolumesPrune` is label-scoped (`managedLabel`). Apple's `runtime/apple/prune.go` runs unscoped `container prune`/`image prune`/`builder delete --force` the same way.
- **The gap it also exposes:** the source comment (`docker/prune.go:141-143`) admits *"Affects ALL backend content ... appropriate for a host dedicated to yoloai"* — the only guard is that comment. The user-facing `--help` (`internal/cli/system/prune.go`) puts the caveat on `--images`/`--stale-bases`, so a user reading help for plain `prune` gets no signal it reaches outside yoloai.
- **`--images` scoped 2026-07-19 (docker/podman) — the seed-and-flip timer collapsed to a bridge.**
  The original plan left `ImagesPrune(--images)` daemon-wide for a 12-month settling period because
  a label-only flip would strand pre-label images. The owner instead ruled (2026-07-19) that since
  every image yoloai has ever built is trivially name-identifiable (`yoloai-base` +
  `InstancePrefix`-derived tags, enforced by D126's gate), the sweep should scope **immediately** as
  a bugfix: `pruneManagedImages` (`runtime/docker/prune.go`) removes unused images matching
  `com.yoloai.managed` **∪** an anchored bare-local `yoloai-` name — a strict subset of what the
  unscoped sweep reaped, so no new false positive is possible, foreign images are spared a year
  early, and no yoloai image loses reclaim eligibility. The name half is the deprecated bridge
  (registered in [deprecations.md](../deprecations.md), superseding the settling-period entry);
  name-only matches are logged so the pre-label population's decay is observable before the bridge
  retires. Bespoke non-derived, non-yoloai-named user images are no longer reclaimed — owner's
  explicit accept ("they know they created them"). Remaining for this finding: the apple backend
  (mac-deferred), whose `--images` still runs `container image prune --all` unscoped.
- **Cheap partial mitigation (if wanted before the full fix):** scope `ContainersPrune`/`NetworksPrune` by `managedLabel` exactly as `VolumesPrune` already is; and move the "dedicated host" caveat onto plain `prune`'s help. The build-cache reclaim stays global (BuildKit cache is not per-project labelable) but forces only a rebuild, not data loss.
- **Pointer:** `system.go:847` (unconditional call); `runtime/docker/prune.go:157-214`; `runtime/apple/prune.go:99-119`; `internal/cli/system/prune.go` (help text).

### DF139 — seatbelt `killByPID` sends SIGKILL to a process group from an unverified, possibly-reused PID

- **Discovered:** 2026-07-17 · **Workstream:** pre-release deletion-verb audit
- **Severity:** MEDIUM (can signal an unrelated process group after PID reuse), **macOS-only**
- **Disposition:** FILED, not fixed — **reported by the audit, not independently verified by me.** Deferred to the macOS agent with DF136.
- **Description (audit-reported):** `runtime/seatbelt/seatbelt.go:711-753` `killByPID` reads a bare PID from a file and does `syscall.Kill(-pid, SIGTERM)` then `SIGKILL` (process-group kill, since the child ran `Setsid`), with **no** check that the PID still belongs to a process this sandbox launched (no start-time / argv identity). Called from `Stop()` (destroy, `reset --restart`, teardown). If the original `sandbox-exec` died and the OS reused the PID as a new group leader, the unrelated group is killed. Contrast `internal/broker/host.go:341-356`, which signals a single PID and explicitly acknowledges the reuse race.
- **Pointer:** `runtime/seatbelt/seatbelt.go:711-753`; contrast `internal/broker/host.go:331-356`.

### DF140 — a sandbox whose agent never came up can report Active / Idle / Done instead of failed

- **Discovered:** 2026-07-17 · **Workstream:** pre-release broken-sandbox audit
- **Severity:** MEDIUM (silent wrong status — the caller believes a dead sandbox is working; no data loss, but it misleads every downstream decision). **Docker agent-free-launch path** for the Active case; all backends for the others.
- **Disposition:** FILED, not fixed — **reported by the audit, not independently verified by me.** A cluster of three faces of one root cause: nothing verifies the launched-agent → written-status linkage.
- **Description (audit-reported):** (1) Docker brings up a neutral keepalive then launches `sandbox-setup.py` as a **detached** exec whose handle is discarded (`launch.go:311-317`); if it dies before `launch_monitor()`, `agent-status.json` stays `{}`, `parseStatusJSON` rejects it, and `DetectStatus` falls through to *"assume active"* (`status.go:249-251`) — permanently, since the keepalive never exits and the monitor never started. (2) A dead status-monitor while the last state was `idle` reports Idle forever — `idle` has no staleness check by design (`status.go:285-289`) and the monitor is an unsupervised `Popen` with no restart. (3) An agent binary that never launches leaves a dead tmux pane the monitor writes up as `done exit_code=0` (`status-monitor.py`), i.e. clean success. `runtime/runtimetest/conformance.go` exercises none of the `Launch`/`ProcessLauncher` bring-up path.
- **Pointer:** `internal/orchestrator/launch/launch.go:247-328,1045-1087`; `internal/orchestrator/status/status.go:249-251,285-289`; `runtime/docker/resources/sandbox-setup.py`, `status-monitor.py`; `runtime/runtimetest/conformance.go` (coverage gap).

### DF141 — `Engine.Restart` is Stop-then-Start with an unlocked gap, so `--isolation`/`--broker` overrides can persist a record that contradicts the running instance

- **Discovered:** 2026-07-17 · **Workstream:** pre-release broken-sandbox audit
- **Severity:** MEDIUM (durably wrong metadata: the record says one isolation mode, the instance is another), **race-triggered**
- **Disposition:** FILED, not fixed — **reported by the audit, not independently verified by me.**
- **Description (audit-reported):** `internal/orchestrator/engine_lifecycle.go:49-57` calls `lifecycle.Stop` then `lifecycle.Start`, each taking and releasing its own per-sandbox lock, so there is an unlocked window between them (violating the whole-op lock invariant at `store/lock_unix.go:50-54`). A concurrent `start` in the gap recreates the container in the old mode; Restart's Start half then runs `applyIsolationOverride` (`start.go:74-98`), which **persists** `isolation=vm` unconditionally, then `DetectStatus` sees Active and returns early without recreating (`start.go:369-372`). The record says `vm`; the instance is a container. Same persist-then-check-then-maybe-skip order silently drops `--broker`/`--vscode-tunnel`/`--resume` on the race. Distinct from DF113.
- **Pointer:** `internal/orchestrator/engine_lifecycle.go:49-57`; `internal/orchestrator/lifecycle/start.go:74-98,343,369-372`; `store/lock_unix.go:50-54`.

### DF147 — the conformance harness's parallel path panics on a fixture that uses `t.Setenv`, and apple's is one

- **Discovered:** 2026-07-19 · **Workstream:** integration-test speedup, Mac-check phase
- **Severity:** LOW (latent — no in-tree combination currently triggers it), but it is exactly the
  class of macOS-only breakage the Linux phase cannot see.
- **Disposition:** FILED, worked around by design rather than fixed. With
  `SharesReadOnlyInstance: true` (apple's intended, now-set policy) the suite runs serially and the
  panic cannot fire; the incompatibility itself remains.
- **Description:** `RunInterfaceConformance` calls `t.Parallel()` on every subtest of a non-sharing
  backend, and calls `setup(t)` *inside* those parallel subtests. A fixture whose setup uses
  `testutil.IsolatedHome` (which calls `t.Setenv`) then panics: Go forbids `t.Setenv` in a parallel
  test (`testing: test using t.Setenv ... can not use t.Parallel`). `appleSetup` is such a fixture,
  so apple with `SharesReadOnlyInstance: false` panics the whole suite — verified on hardware
  2026-07-19 (the Linux phase verified only docker/podman/containerd fixtures, none of which call
  `IsolatedHome` per-subtest; tart's closure captures its runtime once at parent scope). Any future
  fixture that isolates env per-subtest will hit the same wall the moment it opts out of sharing.
- **Remedy sketch:** either make the harness detect the combination and fail with a named error
  (cheap), or make per-subtest fixtures isolate HOME without `t.Setenv` (a Layout-only isolation,
  since the env leak the helper guards against is process-global anyway).
- **Pointer:** `runtime/runtimetest/conformance_iface.go` (`parallelize`, the non-sharing branch);
  `runtime/apple/integration_test.go` (`appleSetup`); `internal/testutil/home.go:33`.

## Policy origin

Established in [architecture-remediation.md](../archive/plans/architecture-remediation.md) and inherited by [layering-refactor.md](../archive/plans/layering-refactor.md).

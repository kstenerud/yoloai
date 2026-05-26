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

### DF1 — `--security` flag was never in a tagged release; existing BREAKING-CHANGES entry is misleading

- **Discovered:** 2026-05-23 · **Workstream:** W-L9
- **Severity:** LOW
- **Disposition:** PARKED
- **Description:** D6 in `layering.md` was conditional: add a BREAKING-CHANGES entry for `--security` → `--isolation` only if `--security` ever shipped in a tagged release. Audit of `git grep '\.Flags().String."security"' v0.1.0..v0.2.6` confirms the CLI flag was never registered in any released tag — `--isolation` has been the public flag name since v0.2.0. The flag existed only on `main` between commit 87956ac and a rename predating v0.2.0. The existing `--security`-related Unreleased entry in `docs/BREAKING-CHANGES.md` is therefore inaccurate for that portion. It does, however, also cover the `backend` → `container_backend` config-key rename, which IS a real v0.1.x → v0.2.x breaking change and should remain documented. W-L9 closes as **N/A**: no new entry needed, and rewording the existing one is scope-creep for W-L9. A future docs pass can correct the conflation.
- **Pointer:** `docs/BREAKING-CHANGES.md:97`

### DF2 — Smoke test prompt may provoke a clarifying-question idle on Haiku (containerd-vm)

- **Discovered:** 2026-05-24 · **Workstream:** observed during W-L4 validation
- **Severity:** LOW
- **Disposition:** PARKED (revisit after the layering refactor completes)
- **Description:** `stop_start/containerd-vm` failed once with the documented "agent idle for 9s+ without sentinel 'done'" signature, then passed cleanly on isolated rerun. Existing idiosyncrasy entry blames QEMU slow startup (extended by `stall_grace_secs=120` in `scripts/smoke_test.py:191,212,216`), but the prompt itself is also suspicious: `"Run this shell command exactly as written; do not modify it or ask for clarification: touch …"`. The negative phrasing ("do not ask for clarification") can prime smaller / faster models like Haiku to do exactly that — output a clarifying question (no tool call), which yoloAI's monitor classifies as `idle`. The agent then waits forever for a user response that never comes, while the smoke test waits forever for the `done` sentinel file. **Hypothesis to verify post-refactor:** capture the agent transcript when this fails and check whether Haiku produced a non-tool-using response (a question, a confirmation, a summary). If so, two possible fixes: (a) rephrase the prompt positively (e.g. "Execute this shell command and exit:" with no negative instruction), (b) treat "model produced a tool-less response on a tool-required prompt" as a distinct failure mode in the smoke test rather than an idle.
- **Pointer:** `scripts/smoke_test.py` (prompt construction), `docs/dev/backend-idiosyncrasies.md#qemu-slow-startup-exceeds-smoke-test-stall-grace-period` (existing entry; this hypothesis is complementary, not contradictory)

### DF3 — Smoke test agent logs are unreadable in ANSI form; need rendered text snapshots

- **Discovered:** 2026-05-26 · **Workstream:** observed during W-L8b kickoff (failed `full_workflow/containerd-vm` smoke run, log `yoloai-smoketest-20260526-050950.470`)
- **Severity:** LOW
- **Disposition:** PARKED
- **Description:** When a smoke test fails on a TUI-driven agent (Claude Code), `agent.log` is a stream of raw ANSI control codes and cursor movements — fundamentally unrenderable without piping through a terminal emulator. Diagnosing whether the agent produced a tool-less response (DF2's hypothesis) or genuinely never made an API call requires the rendered text, not the escape sequence stream. **Proposed fix:** add a `terminal-snapshot.txt` artifact captured via `tmux capture-pane -p -e -t main` immediately before stall detection fires and on every smoke-test failure. Include in bug reports too. Lets DF2 be confirmed or disconfirmed conclusively.
- **Pointer:** `scripts/smoke_test.py::wait_for_sentinel` (stall path)

### DF4 — `wchan + connections` idle classification is decisive; surface it in bug reports

- **Discovered:** 2026-05-26 · **Workstream:** observed during W-L8b kickoff (same failure as DF3)
- **Severity:** LOW
- **Disposition:** PARKED
- **Description:** The `monitor.jsonl` line `do_epoll_wait + no connections -> idle` was the decisive signal for diagnosing the failed `containerd-vm` run — it ruled out "slow API," "network unreachable," and "agent making retries with backoff" in one observation, leaving "agent is genuinely sitting idle without trying" as the only consistent explanation. This data point currently requires grepping the right JSONL stream manually. **Proposed fix:** when stall detection fires, dump the most recent N detector results (with wchan + connection-count fields) into a top-level diagnostic section of the preserved sandbox dir, and include this in `yoloai sandbox <name> bugreport`. Cross-reference DF3 — together they would make most "agent idle 9s+" failures self-diagnosing.
- **Pointer:** `runtime/monitor/` (detector source), `scripts/smoke_test.py` (stall handler)

### DF5 — Smoke tests should network-probe inside the sandbox before delivering the prompt

- **Discovered:** 2026-05-26 · **Workstream:** observed during W-L8b kickoff (same failure as DF3/DF4)
- **Severity:** LOW
- **Disposition:** PARKED
- **Description:** When a smoke test fails as "agent idle 9s+", one of the candidate explanations is "network unreachable from inside the sandbox" (especially relevant for Kata VMs, where the historical idiosyncrasy is that Docker shimv2 doesn't wire netns and nerdctl is required — see project memory `kata_nerdctl_networking.md`). The current smoke test has no in-sandbox network probe; failures with broken network are indistinguishable from real agent stalls. **Proposed fix:** before delivering the prompt, the smoke test runs `curl -sS --max-time 5 https://api.anthropic.com/ 2>&1` inside the sandbox via `yoloai exec`. A non-2xx / non-401 response (or curl error) becomes its own clear failure mode — "network unreachable from inside <backend> sandbox" — rather than masquerading as an idle agent.
- **Pointer:** `scripts/smoke_test.py` (between sandbox creation and prompt delivery)

### DF6 — Stall detector conflates "never reached READY" with "idle after prompt"

- **Discovered:** 2026-05-26 · **Workstream:** observed during W-L8b kickoff (same failure as DF3/DF4/DF5)
- **Severity:** LOW
- **Disposition:** PARKED
- **Description:** The failed `containerd-vm` run showed `wait_for_ready(pattern=❯)` taking 46 seconds (sandbox.jsonl, 05:10:39 → 05:11:25) before the prompt was even delivered. That 46s ate over a third of the `stall_grace_secs=120` window — so when stall detection fired, only ~33s of that window covered actual agent work. The smoke-test failure message ("agent idle for 9s+") is identical whether the agent was idle for 9s on top of 46s ready + 33s work, or 9s on top of 5s ready + 74s work. These two cases have very different diagnoses (VM-startup tuning vs. agent-behavior tuning) but no signal distinguishes them in the failure report. **Proposed fix:** distinguish "agent never reached READY" (timer up vs. ready pattern) from "agent reached READY, then went idle after prompt" (idle for 9s after prompt-delivered timestamp). Each gets its own message; the existing 9s threshold applies to the second case only.
- **Pointer:** `scripts/smoke_test.py::wait_for_sentinel`, `scripts/smoke_test.py::wait_for_ready`

### DF7 — `stall_grace_secs=120` for containerd-vm may need re-measuring against current startup latency

- **Discovered:** 2026-05-26 · **Workstream:** observed during W-L8b kickoff (same failure as DF3-DF6)
- **Severity:** LOW
- **Disposition:** PARKED — but **downweighted** by DF8 evidence (see below). The 46s `wait_for_ready` from the original failure was an outlier; a subsequent failure showed an 11s `wait_for_ready` with the same end-state.
- **Description:** The 120s grace for `containerd-vm` was set against a measured QEMU startup distribution at the time. Two runs in this session failed at the 129s mark with the agent genuinely idle (DF3-6) — suggesting either (a) the agent-behavior issue (DF2) is real and unrelated to grace tuning, or (b) startup has drifted upward and the grace no longer matches reality. The 46s `wait_for_ready` observed in the first failure log was consistent with (b), but DF8 shows the same idle-after-prompt failure also fires with a fast 11s `wait_for_ready` — so startup tuning is at best a partial answer. **Proposed action:** re-measure containerd-vm startup latency (from `entrypoint.start` to `❯` ready pattern) across, say, 30 successful runs; pick the 99th percentile + safety margin as the new `stall_grace_secs`. Do this AFTER DF3/DF6 land — without rendered transcripts and the ready-vs-idle split, the measurement is muddled.
- **Pointer:** `scripts/smoke_test.py::BackendSpec.stall_grace_secs`, `docs/dev/backend-idiosyncrasies.md#qemu-slow-startup-exceeds-smoke-test-stall-grace-period`

### DF9 — Smoke test orchestration exceeds macOS concurrent-VM limit on Tart; some VMs leak across runs

- **Discovered:** 2026-05-26 · **Workstream:** observed during W-L8b kickoff (two macOS smoke runs)
- **Severity:** LOW
- **Disposition:** PARKED — W-L14 covers the error-mapping half; the cross-run leak is a separate smoke-test cleanup gap (see below)
- **Description:** Two failure surfaces, same end-state. Both `stop_start/tart` attempts in two consecutive macOS smoke runs failed at `tart run` with `"The number of VMs exceeds the system limit (other running VMs: …)"`. Apple's `VZError.virtualMachineLimitExceeded` (code 6) — macOS limits concurrent VMs (commonly 2 on base Apple Silicon, more on M-Pro/Max).

  **Two distinct contributing factors:**

  1. **Intra-run parallelism.** The smoke test runs `full_workflow/tart` and `stop_start/tart` in parallel; both create their own Tart VMs; the third concurrent VM hits the cap. This is the case **W-L14** addresses: detect Tart's stderr substring `"The number of VMs exceeds the system limit"`, wrap as a typed `ErrConcurrentVMLimit`, surface a user-friendly message instead of the raw tart error.

  2. **Cross-run VM leak.** Comparing the two failure outputs:
     - Run 1's blocking VMs: `1779775833-workflow-tart` + `1779775969-workflow-tart`
     - Run 2's blocking VMs: `1779776810-workflow-tart` + **`1779775833-workflow-tart`**

     VM `1779775833-workflow-tart` appears in BOTH runs — it's a leaked VM from a prior smoke invocation that wasn't cleaned up. This is a smoke-test infrastructure problem orthogonal to W-L14: even after W-L14 maps the error nicely, the user still can't run smoke tests on the affected host until they manually `tart stop` the leaked VMs.

  Two corresponding fixes needed (track as separate tasks):
  - **W-L14 (planned):** error mapping for `ErrConcurrentVMLimit`. Fixes user-facing message.
  - **New smoke-test action item:** add a pre-run cleanup step that enumerates `tart list` for `yoloai-smoke-*` VMs and stops them before starting new scenarios. Or post-run cleanup that ensures every `tart run` is matched by a `tart stop`. The leak source (which failure mode left a VM running on a prior run) is unknown from these logs alone.

- **Pointer:** `docs/dev/plans/layering-refactor.md::W-L14`, `docs/dev/research/tart-limit-detection.md`, `scripts/smoke_test.py` (orchestration + cleanup)

### DF8 (4th data point, 2026-05-26): containerd-vm idle-after-prompt failed once, passed on retry

- This session's fourth `full_workflow/containerd-vm` failure (log `yoloai-smoketest-20260526-062648.461`) followed the same pattern as the second: failed attempt 1 with the documented "agent idle 9s+" signature, passed on the retry. Continues to reinforce DF8's revised hypothesis (no Type A; all failures are post-ready-idle agent behavior, possibly DF2's tool-less-response on Haiku under the QEMU CPU profile).
- Four-of-four observations is a clear pattern; the action items in DF8 (rendered transcript capture per DF3) remain the next step.

### DF8 — `containerd-vm` "agent idle after prompt" fires across the full range of startup times; root cause is NOT startup-tuning

- **Discovered:** 2026-05-26 · **Workstream:** observed during W-L8b kickoff
- **Severity:** LOW
- **Disposition:** PARKED
- **Description:** Three `full_workflow/containerd-vm` failures in the same session, all sharing identical end-state (`do_epoll_wait + no connections -> idle`, agent never made a TCP request after prompt delivery). The `wait_for_ready` durations span the full range:

  | Run | `wait_for_ready` | Retry result | Wchan idle entries |
  |---|---|---|---|
  | 1 (050950.470) | 46 s | Failed | 46 |
  | 2 (054703.093) | 11 s | **Passed** | 46 |
  | 3 (061232.921) | 24 s | (attempt 1 captured) | 40 |

  Three points across 11s / 24s / 46s startup demonstrate the failure is **not** correlated with startup latency. The agent reaches READY, the prompt is delivered cleanly via paste-buffer, and then the agent sits in `do_epoll_wait` with no TCP socket ever being opened. The earlier bimodal framing (Type A = slow startup, Type B = post-ready idle) collapses: all three are Type B; Type A is uncorroborated and probably doesn't exist as a separate failure mode.

  **Refined hypothesis:** the failure is purely post-prompt agent-behavior on `containerd-vm`. Other backends (docker, podman, docker-cenhanced, containerd-vmenhanced) PASS consistently in the same runs, so the trigger is something specific to the Kata+QEMU environment that the agent process is running in. Plausible candidates:

  - **DF2's tool-less response on Haiku** under QEMU's resource profile (slower CPU → different generation latencies → different model output behavior).
  - **PTY/tmux paste-buffer delivery edge case under QEMU** where the prompt is partially-delivered or arrives in a state Claude's input loop swallows without firing.
  - **Kata networking warm-up race** where the network namespace isn't fully wired before the agent's first API attempt; subsequent connections succeed (consistent with retries passing).

  Confirmation requires DF3 (rendered tmux capture-pane snapshot) — without it we cannot tell if the agent saw the prompt, what it printed in response, or whether it tried and failed at the network layer.

- **Pointer:** `runtime/monitor/` (detector source), `scripts/smoke_test.py::wait_for_sentinel`, cross-ref DF2 / DF3 / DF7. DF7 is **further downweighted** — three failures across 11–46s startup conclusively rule out startup-tuning as the fix.

## Policy origin

Established in [architecture-remediation.md](plans/architecture-remediation.md) and inherited by [layering-refactor.md](plans/layering-refactor.md).

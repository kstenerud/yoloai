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
- **Disposition:** PARTIAL — smoke-test-side capture landed; bug-report-integrated path remains TODO.
- **Description:** When a smoke test fails on a TUI-driven agent (Claude Code), `agent.log` is a stream of raw ANSI control codes and cursor movements — fundamentally unrenderable without piping through a terminal emulator. Diagnosing whether the agent produced a tool-less response (DF2's hypothesis) or genuinely never made an API call requires the rendered text, not the escape sequence stream.
- **Smoke-test fix landed (2026-05-26):** `scripts/smoke_test.py::_capture_terminal_snapshot` shells out per-backend (docker / podman / containerd*) to `tmux capture-pane -p -S -200 -t main` and writes `terminal-snapshot.txt` + `terminal-snapshot.ansi` into the preserved attempt directory. Best-effort: failures don't change the smoke test outcome. Unsupported backends (tart, seatbelt) skip silently. The container is still running when `_preserve_sandbox` runs (retry/cleanup destroy happens later), so `docker exec` / `ctr task exec` succeeds.
- **TODO (the better fix):** Move the capture into `internal/cli/bugreport_writer.go` so `yoloai sandbox <name> bugreport` includes it for users, not just the smoke test. Blocker: needs a non-interactive `Exec` surface (either `yoloai sandbox <name> exec --no-tty -- tmux ...` or a `--terminal-snapshot` flag on `bugreport` that uses `runtime.Exec` directly — InteractiveExec forces a PTY which corrupts the captured output for scripts). Once available, the smoke test should switch from per-backend Python dispatch to calling the single yoloai-level capability. Tracked here; reference from `bugreport_writer.go`'s TODO when implementing.
- **Pointer:** `scripts/smoke_test.py::_capture_terminal_snapshot` (current), `internal/cli/bugreport_writer.go` (TODO destination), cross-ref DF2/4/5/6/8.

### DF4 — `wchan + connections` idle classification is decisive; surface it in bug reports

- **Discovered:** 2026-05-26 · **Workstream:** observed during W-L8b kickoff (same failure as DF3)
- **Severity:** LOW
- **Disposition:** LANDED 2026-05-26.
- **Description:** The `monitor.jsonl` line `do_epoll_wait + no connections -> idle` was the decisive signal for diagnosing the failed `containerd-vm` run — it ruled out "slow API" and left "agent is genuinely sitting idle" (or, after DF8: "agent is busy waiting for network") as the explanation. Used to require grepping the raw stream.
- **Implementation:** two surfaces. (1) `scripts/smoke_test.py::_write_monitor_tail` writes `monitor-tail.txt` next to environment.json / terminal-snapshot.* in every preserved attempt dir — last 30 `detector.result` entries as one-per-line plain text. (2) `internal/cli/sandbox_bugreport.go::writeBugReportMonitorTail` adds a "Recent detector decisions" section to every `yoloai sandbox <name> bugreport` output, placed BEFORE the full monitor.jsonl dump so readers see the decisive signal first. Both surfaces use the same N=30 default. Unit tests cover the bug-report path; the smoke-test path was validated empirically against the captured monitor.jsonl from the DF8 smoking-gun run — surfaced 30 lines of `wchan: do_epoll_wait + no connections -> idle` repeating.
- **Diagnostic stack now complete:** every preserved attempt directory has `environment.json` (sandbox config), `terminal-snapshot.txt` (DF3 — rendered agent screen), `monitor-tail.txt` (DF4 — recent detector decisions), plus the `network: …` field on the failure-message line (DF5). The full `logs/monitor.jsonl` and ANSI `agent.log` are also preserved for deeper investigation.
- **Pointer:** `scripts/smoke_test.py::_write_monitor_tail`, `internal/cli/sandbox_bugreport.go::writeBugReportMonitorTail`. Cross-ref DF3 / DF5 / DF8.

### DF5 — Smoke tests should network-probe inside the sandbox before delivering the prompt

- **Discovered:** 2026-05-26 · **Workstream:** observed during W-L8b kickoff (same failure as DF3/DF4)
- **Severity:** LOW (raised after DF8 smoking gun)
- **Disposition:** LANDED 2026-05-26.
- **Description:** When a smoke test fails as "agent idle 9s+", one of the candidate explanations is "network unreachable from inside the sandbox" (especially relevant for Kata VMs, where the historical idiosyncrasy is that Docker shimv2 doesn't wire netns and nerdctl is required — see project memory `kata_nerdctl_networking.md`). The current smoke test had no in-sandbox network probe; failures with broken network were indistinguishable from real agent stalls.
- **Implementation choice:** rather than pre-prompt probe (would add latency to every passing test), the probe runs at failure-diagnosis time inside `_sentinel_diag`. Every stall / terminal / sentinel-timeout failure now carries `network: reachable (HTTP …)` or `network: unreachable (curl exit N)` in its diagnostic. Curl-from-inside-the-sandbox via per-backend dispatch (docker exec / podman exec / `sudo -n ctr task exec`). Best-effort: probe failures append "probe error" rather than masking the underlying test failure. Skipped for tart/seatbelt (unsupported backends).
- **Pointer:** `scripts/smoke_test.py::_probe_network`. Composes with DF3's terminal-snapshot — both run when a failure is preserved, so the rendered screen + network state appear together. The next "agent idle 9s+" containerd-vm flake should be self-classifying without further investigation.

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

### DF8 (5th data point, 2026-05-26): containerd-vm failed BOTH attempts

- Fifth `full_workflow/containerd-vm` failure (log `yoloai-smoketest-20260526-063648.819`) — first one in this session to fail BOTH attempts. Running totals across the W-L8b-kickoff session: 5 failures, 3 transient (pass on retry), 2 persistent (fail both). Still 100% post-ready-idle shape (same `do_epoll_wait + no connections` signature); the persistent-vs-transient split is along an unknown axis. Whether the "warming effect on retry" hypothesis (DF8 first version) is real or coincidence is still open — the rendered transcripts of DF3 are needed to distinguish "Haiku produced different output on retry" from "VM warmed up I/O cache, second run hit the API window."

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

### DF8 (6th data point, 2026-05-26): `containerd-vmenhanced` exhibits the same failure mode

- Log `yoloai-smoketest-20260526-120447.993`. First observation of `full_workflow/containerd-vmenhanced` failing the same way `containerd-vm` has been failing: "agent idle 9s+ without sentinel 'done'", passed on retry. Same session: docker / podman / docker-cenhanced / containerd-vm all PASS, only vmenhanced fails first attempt. Host `/` at 76% / 18G free rules out the disk-pressure pattern from `smoke-containerd-disk-pressure` project memory.
- **Implication:** the failure family is not unique to the `containerd-vm` snapshotter setup — `containerd-vmenhanced` (devmapper snapshotter) reproduces it too. What's common is Kata+QEMU, not the snapshotter. Both candidates in the refined hypothesis (Haiku tool-less response under QEMU CPU profile, or Kata networking warm-up race) remain consistent.
- Still PARKED pending DF3's rendered tmux capture-pane snapshot. Action item unchanged.

### DF8 (7th data point, 2026-05-26): `containerd-vm` failed BOTH attempts; `containerd-vmenhanced` PASSED same session

- Log `yoloai-smoketest-20260526-125802.053`. `containerd-vm` failed both attempts (the "agent idle 9s+" signature, same as DF8). `containerd-vmenhanced` passed in the same session. The previous run (6th data point, 120447.993) showed the inverse: vmenhanced fails first attempt, vm passes. Two adjacent runs with opposite outcomes between the two containerd snapshotters.
- **Implication:** the failure is NOT correlated between vm and vmenhanced on a single run, which argues against "host was in a bad state at run start" as the explanation. Each backend independently rolls the dice — consistent with a per-backend race (e.g. Kata netns wiring, QEMU CPU latency variability) rather than a global precondition. Now 2 confirmed failures of vmenhanced, 7 of vm.
- Still PARKED pending DF3. Confirming with rendered tmux output remains the unblocker for any further diagnosis.

### DF8 (9th data point, 2026-05-26): full diagnostic stack runs clean; agent's "ConnectionRefused" label is misleading

- Log `yoloai-smoketest-20260526-143616.771`. First failure captured with the complete DF3/DF4/DF5 diagnostic stack landed. Failure line:
  ```
  agent idle for 9s+ without sentinel 'done'
    exchange dir: empty; host /: 76% used, 18G free; network: unreachable (curl exit 28)
  ```
- `terminal-snapshot.txt` shows the agent's actual error: "Unable to connect to API (ConnectionRefused) · Retrying in 0s · attempt 5/10" — same wording as the 8th data point.
- `monitor-tail.txt` shows the same `wchan: do_epoll_wait + no connections -> idle` pattern, stability counter climbing 30→35.
- BUT: curl probe says exit 28 (operation timeout), NOT exit 7 (connection refused). **The agent's error label is misleading.** Claude Code's TUI prints "ConnectionRefused" as a generic "couldn't connect" label regardless of whether the underlying syscall returned ECONNREFUSED or ETIMEDOUT. Curl gives the authoritative diagnosis. Practical implication for diagnosis: trust the `network: ...` curl-exit code over the agent's text. Two distinct sub-modes confirmed inside the DF8 family:
  - **exit 7 (refused):** TCP RST received. Something at the destination port refuses the connection. Consistent with netns routing to a wrong/local destination.
  - **exit 28 (timeout):** No response at all. Packets leave the netns but no SYN-ACK comes back. Consistent with packets being silently dropped (broken outbound routing, missing iptables NAT rule, no default route yet).
- Both modes fit the "Kata netns warm-up race" hypothesis with slightly different downstream effects. Worth probing `runtime/containerd/cni.go` for the precise stage that's racy: address allocation? Route insertion? iptables MASQUERADE setup? Each would produce a distinguishable curl signature.
- **Diagnostic refinement (staged probe added):** the curl-only probe replaced with a multi-stage probe inside `_probe_network` — DNS resolution → default route → raw TCP to 1.1.1.1:443 → HTTPS to api.anthropic.com. The DF5 diagnostic now reads e.g. `unreachable [tcp failed | dns=ok route=ok tcp=fail https=exit 28]`, telling you which CNI stage broke without further investigation. The next data point will land with structural info about the racy step (route absent? NAT missing? packet dropped?). After two-three such data points we should be able to point at the precise CNI step that needs ordering/synchronization in `runtime/containerd/cni.go`.

### DF8 (8th data point, 2026-05-26): **SMOKING GUN — root cause is ConnectionRefused, not idle**

- Log `yoloai-smoketest-20260526-135935.545`. First failure captured with the new DF3 terminal-snapshot patch (after the meta.json → environment.json + tmux socket fixes in `7ea5488`). `stop_start/containerd-vmenhanced` failed attempt 1, passed retry. Rendered transcript shows the agent's actual state when the smoke test gave up:
- ```
  ❯ Run this shell command exactly as written; do not modify it or ask for clarification: touch /yoloai/files/in-progress ...
    ⎿  Unable to connect to API (ConnectionRefused)
       Retrying in 23s · attempt 7/10
  ✻ Contemplating… (1m 36s)
  ```
- **The agent is NOT idle.** It received the prompt, parsed it, tried to make the API call, and is on attempt 7 of 10 retries because every connection is being refused. Smoke test classifies this as "idle" because the agent isn't actively writing to the exchange dir — but the agent is busy waiting for an API connection that never lands.
- **DF2 is now downweighted dramatically.** Hypothesis: "Haiku produced a clarifying question instead of using its tool." Reality: Haiku is doing exactly what it should — calling the API — but the connection is refused. The negative-phrased prompt is fine.
- **DF8's refined hypothesis "Kata networking warm-up race" is now the strong candidate.** ConnectionRefused (not Unreachable/Timeout) means TCP got to a host but the destination refused. Most likely: the Kata netns wiring hasn't completed when the agent's first API attempt fires, so the packet hits something on localhost that refuses. By the time retries fire, networking is up. Consistent with: failures only on Kata-backed runs (containerd-vm + containerd-vmenhanced); failures always passing on retry (the retry attempt fires after warm-up); first attempt's `wait_for_ready` time doesn't correlate (network warmup is independent of tmux readiness — DF6's hypothesis).
- **DF5 jumps in priority.** The proposed pre-prompt network probe ("`curl -sS --max-time 5 https://api.anthropic.com/` inside the sandbox before delivering the prompt") would have flagged THIS exact failure as "network unreachable from inside sandbox" rather than letting it masquerade as "agent idle." Recommend implementing DF5 now that we have direct evidence the failure family is network, not agent.
- **DF7 conclusively eliminated.** Startup latency wasn't the issue — the agent gets to the prompt fine in <30s. The 1m 36s in the snapshot is purely retry waiting.
- Pointer: `runtime/containerd/cni.go` (CNI netns plumbing), DF5's action item, cross-ref `kata_nerdctl_networking.md` project memory.

## Policy origin

Established in [architecture-remediation.md](plans/architecture-remediation.md) and inherited by [layering-refactor.md](plans/layering-refactor.md).

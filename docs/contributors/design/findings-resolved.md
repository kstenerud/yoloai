<!-- ABOUTME: History sink for resolved findings drained from findings-unresolved.md. -->
<!-- ABOUTME: Item-queue pattern: active items live in the unresolved- file, done ones land here. -->

# Resolved findings

History of codebase findings (issues discovered mid-work) that have been addressed. Items
are moved here from [`findings-unresolved.md`](findings-unresolved.md) once resolved, so the
active file stays a working set. Newest first.

### DF16 — `ValidNameRe` is looser than the containerd identifier regex (a valid sandbox name can be an invalid containerd id)

- **Discovered:** 2026-06-03 · **Workstream:** D58/D59 principal-namespacing research
- **Severity:** LOW
- **Disposition:** RESOLVED 2026-06-03 (commit C1 on `layering-refactor`). Fixed as part of the D62 multi-principal implementation: `config.ParseSandboxName` is now the single grammar chokepoint enforcing the containerd-conformant rule `^[A-Za-z0-9]+(?:[._-][A-Za-z0-9]+)*$` (`len ≤ 56`), and `store.ValidateName` delegates to it. `my-app-`, `a..b`, `x__y`, and 1-char-after-separator names are rejected at the boundary, so a yoloAI-valid name can no longer be an invalid containerd id (parse-don't-validate). `config.ValidNameRe` was retained only for the looser profile-name grammar (`profile.go`), which is a host directory name, not a container id. This also closes the [[DF15]] convention-drift straggler for sandbox names. (Minor breaking change for any existing sandbox name ending in `-`/`.`/`_`.)
- **Description:** `config.ValidNameRe` = `^[a-zA-Z0-9][a-zA-Z0-9_.-]*$` accepts a trailing separator and consecutive separators (e.g. `my-app-`, `a..b`, `x__y`), and its `*` allows a 1-char name. containerd's identifier validation (`pkg/identifiers/validate.go`, pinned `containerd/v2@v2.2.2`) is stricter: `^[A-Za-z0-9]+(?:[._-](?:[A-Za-z0-9]+))*$` with `maxLength=76` — every separator must be *surrounded* by alphanumerics, so `my-app-` is rejected. Because `InstanceName(name)="yoloai-"+name` becomes **both** the containerd container id **and** the snapshot key (`lifecycle.go:235,246`), a sandbox whose name yoloAI accepts could fail at containerd create time. Pre-existing and independent of multi-principal (surfaced while researching the namespacing budget); Docker's charset is the same family but doesn't reject the trailing separator, so docker/tart users never hit it.
- **Pointer:** `internal/config/names.go` (`ParseSandboxName`); `internal/sandbox/store/paths.go` (`ValidateName` delegates); containerd regex at `containerd/v2@v2.2.2/pkg/identifiers/validate.go:34-42`; reasoning in `docs/contributors/design/research/principal-namespacing.md` (Q1); decision [D62](decisions/working-notes.md)

### DF9 — Some Kata VMs spawn with permanently-broken netns (separate from DF8 warm-up race)

- **Discovered:** 2026-05-26 · **Workstream:** containerd backend reliability
- **Severity:** MEDIUM (smoke-test retry masks; agent users see "Unable to connect to API")
- **Disposition:** SUPERSEDED BY DF10 — see correction below. Originally marked ROOT-CAUSED + MITIGATED (revision 2, 2026-05-26), attributing the failure to an upstream CNI **firewall** plugin no-op. On 2026-05-26 (later same day) DF10 was root-caused: `canCreateNetNS` was leaking Go OS threads into anonymous netns via `netns.NewNamed` without `runtime.LockOSThread`. libcni's plugin execs sometimes landed on a poisoned thread → bridge or firewall plugin ran in the wrong netns → POSTROUTING and/or CNI-FORWARD landed in an unreachable namespace. Every observed "DF9" signature (POSTROUTING present + CNI-FORWARD missing, the inverse, or empty `result.IPConfigs`) is explained by DF10 alone, and the 20-iteration reproducer dropped from 4/20 fail to 0/20 fail after the DF10 fix. The upstream firewall plugin code path described below does exist, but was never independently confirmed in our environment; the DF9 verify+retry mitigation now serves as defense-in-depth, not the primary cause.

- **Description:** With DF8 V3 landed (probe verifies DNS + external TCP, retries on failure), one out of four containerd-vm runs still fails first-attempt with `dns=fail tcp=fail`. The smoking gun: V3's probe correctly ran 7 attempts over 31 seconds, every attempt exited 1 (script's "not ready" exit), then the 30s outer budget expired and V3 warned-and-proceeded per its best-effort policy. The agent then launched, attempted API calls, and got `FailedToOpenSocket` for the entire run.

  This is **not** the DF8 warm-up race. In DF8, the network comes up within a few seconds of `task.Start` returning; V3 waits and detects it. Here the network never comes up at all — V3's probe never sees DNS or TCP succeed in 30 seconds of polling.

  The retry sandbox (fresh Kata VM) succeeded normally, so the failure is **instance-specific**, not a permanent Kata-on-this-host bug. Hypotheses (one now confirmed):

  1. **CNI IPAM lease contention.** Two sandboxes created in quick succession could collide on the host-local-ipam range; one VM gets a working IP, the other gets a partially-configured netns.
  2. **CNI plugin transient failure.** `firewall` or `bridge` plugin returns an error that isn't fatal at CNI ADD time but leaves the netns half-wired. ~~CONFIRMED~~ — symptom was real but mechanism was misattributed. See DF10 below; the actual cause was our own `canCreateNetNS` netns leak, not an upstream plugin bug. The observed signature (firewall plugin "returns success without installing any CNI-FORWARD ACCEPT rules") is what happens when the firewall plugin runs on a netns-poisoned Go thread.
  3. **Kernel resource exhaustion** (conntrack table, neighbor cache, br_netfilter limits) — affects only some VMs.
  4. **Kata-internal netdev teardown not completing on prior shim crash** — partial state survives.

- **Evidence (initial, 163655.031):** `yoloai-smoketest-20260526-163655.031/full_workflow-containerd-vm.log` contains the `sandbox.network.probe_timeout attempts=7 elapsed_ms=31442 last_err="probe exit 1: "` warning. Preserved attempt dir has terminal-snapshot.txt with agent's `Unable to connect to API (FailedToOpenSocket) Retrying in 32s · attempt 8/10`.

- **Smoking gun (175645.907, with network-diag.txt landed):** `full_workflow/containerd-vmenhanced` attempt2 captured the actual host-side state at probe-timeout time. The failing sandbox is `10.89.1.90`:

  | Layer | State |
  |---|---|
  | Netns + `eth0` + default route | present, healthy |
  | `cni-state.json` written | yes (overall CNI ADD reported success) |
  | POSTROUTING masquerade for `10.89.1.90` | **PRESENT** (bridge plugin ran) |
  | CNI-FORWARD ACCEPT rules for `10.89.1.90` | **MISSING** (firewall plugin no-op'd) |
  | Sibling `10.89.1.88` (same smoke run, vm not vmenhanced) | both rule sets present |

  FORWARD policy is `DROP`, so DNS/TCP from the VM is dropped at the host bridge. **Post-DF10 correction:** at the time this was attributed to the upstream "addRules() no-op on empty result.IPs" pathology. With DF10 root-caused later the same day, the more plausible explanation is that the firewall plugin ran on a netns-leaked Go thread and wrote CNI-FORWARD into the wrong namespace; the sibling sandbox got a clean thread. "Why it fires for sibling-but-not-this-IP" is then answered by goroutine scheduling rather than an upstream internal.

- **Why V3's 30s budget isn't the fix:** extending the budget would just make sandboxes that are permanently broken wait longer before the agent starts failing. V3 is already correctly detecting the broken state; we shouldn't paper over it by waiting more.

- **Mitigation (revision 2, landed 2026-05-26):**
  - `runCNIAdd` extracts the bridge-allocated IP from `result.Interfaces["eth0"].IPConfigs[0].IP`. After `n.Setup` returns success, it runs `verifyCNIForwardRules(ctx, ip)` which shells out to `iptables -S CNI-FORWARD` and looks for an `ACCEPT` line referencing `<ip>/32`. `cniForwardHasIP` is a pure helper covered by unit tests.
  - **Both** "extraction returned empty IP" and "verify found no rules" map to `errFirewallRulesMissing`. Empty IP is treated as the same failure mode because the documented empty-result pathology in the firewall plugin produces the same surface in the Go side: bridge plugin allocated and installed POSTROUTING (visible via raw iptables) but the result-cache → result.IPs conversion lost it. Without this, the original `if ip != ""` guard silently skipped verify in exactly the case we're trying to catch — observed in smoke run `183343.392`.
  - On verify failure (either variant), `runCNIAdd` calls `n.Remove` to undo the bridge plugin's POSTROUTING + IPAM allocation so the retry starts clean. A failure of the rollback itself emits `sandbox.network.firewall_rollback_failed` warn log; first-attempt POSTROUTING leak is observable as a stranded entry in `iptables -t nat -S POSTROUTING` if this fires.
  - `setupCNI` detects the sentinel via `errors.Is`, recreates the netns + IPAM lease, retries CNI ADD **once**. A successful retry returns normally; a second failure surfaces as `CNI setup (retry after firewall no-op): …`. The retry emits a `sandbox.network.firewall_retry` warn log so production occurrences can be grepped.
  - Net effect: the DF9 silent-no-op symptom should no longer reach `waitForNetworkReady` for either variant. If it ever does, you'll see the warn log AND the probe-timeout warning together — that's the "retry also failed" case and warrants upstream investigation.

- **Diagnostic path bug fixed in the same change:** `network_diag.go` was reading `<sandboxDir>/cni-state.json` while the writer (`cni.go:cniStatePath`) uses `<sandboxDir>/backend/cni-state.json`. The diag now uses the shared `cniStatePath()` helper, so future DF9 captures will actually surface the state file instead of always reporting ENOENT.

- **Open follow-ups (not blocking):**
  - ~~Upstream root cause.~~ Resolved by DF10: there was no upstream firewall plugin bug active in our environment. If `sandbox.network.firewall_retry` ever fires after the DF10 fix lands, capture iptables + `/proc/<pid>/task/*/ns/net` for the yoloai process before destroying — that is the case where an actual upstream pathology or a second netns leak is firing.
  - **Detection-only mode for prod.** Right now the retry is silent (warn log only). If we ever see the same sandbox fail twice in a row in production, surface a structured event to the user, not just slog.
  - **Smoke-test signal.** Grep `sandbox.network.firewall_retry` in smoke runs; any occurrence is a free upstream data point even when the run passes.

- **Pointer:** `runtime/containerd/cni.go::setupCNI`, `::runCNIAdd`, `::verifyCNIForwardRules`, `::cniForwardHasIP`, `::errFirewallRulesMissing`. Cross-ref DF8 (warm-up race, separate cause), DF10 (actual root cause for every observed instance), and [backend-idiosyncrasies.md](../backend-idiosyncrasies.md) (the "Firewall plugin: silent no-op" entry + the new "Post-ADD verify" entry pointing back here).

### DF10 — `canCreateNetNS` leaked Go OS thread netns; libcni plugin execs landed in wrong namespace

- **Discovered:** 2026-05-26 · **Workstream:** containerd backend reliability (follow-up to DF9)
- **Severity:** HIGH (caused every observed "DF9" smoke failure; ~20% per-create failure rate in a tight loop)
- **Disposition:** ROOT-CAUSED + FIXED (2026-05-26).

- **Description:** `runtime/containerd/containerd.go::canCreateNetNS` (capability probe called on every containerd-backend `new`) called `netns.NewNamed(probe)` and `netns.DeleteNamed(probe)` with no `runtime.LockOSThread` and no `netns.Set(origNS)` restore. `NewNamed` calls `unshare(CLONE_NEWNET)`, which switches **the current OS thread** into a brand-new netns; after `DeleteNamed` removes the bind mount, that thread is in an anonymous netns. Without LockOSThread the goroutine can be scheduled off that thread, leaving it in Go's runtime pool **still in the wrong netns**. Any later goroutine landing on the poisoned thread inherits the netns — including libcni's `exec.Command` for plugin invocations. Bridge or firewall plugin then ran in the wrong netns and wrote iptables rules to a namespace the host can't see.

- **Symptom signatures (all observed):**
  - POSTROUTING entry for sandbox IP present in host, CNI-FORWARD ACCEPT for same IP missing (firewall on poisoned thread). Originally misattributed to upstream firewall plugin no-op (DF9 v1 evidence in smoke run `175645.907`).
  - POSTROUTING missing for sandbox IP, CNI-FORWARD present (bridge on poisoned thread). Captured in smoke run `194842.389/stop_start/containerd-vm/attempt1/network-diag.txt`.
  - `iptables -S CNI-FORWARD` returns `No chain/target/match by that name` even though `n.Setup` reported success (firewall created the chain in the leaked netns; that netns is anonymous so no other process can reach it).
  - `result.Interfaces["eth0"].IPConfigs` empty after `n.Setup` (libcni's result-build path returning a malformed result when an upstream plugin ran in the wrong netns).

- **Reproduction:** 20-iteration loop of `sudo -E ./yoloai new sb-$i /tmp/dir --agent test --os linux --isolation vm --yes --debug` in a session that has already run containerd `system check`. Pre-fix: 4/20 failures + 6/20 with wrong-netns observed in instrumented `iptables` subprocess. Post-fix: 20/20 success, 0 wrong-netns observed.

- **Fix:** Wrap `canCreateNetNS` in the same pattern `createNetNS` already uses — `goruntime.LockOSThread()` + `defer Unlock`, save `origNS` via `netns.Get()`, run the probe, `netns.Set(origNS)` to restore the thread's netns before unlock. Same single-callsite change.

- **Why DF9's mitigation masked this for a while:** The verify+retry path in `cni.go::setupCNI` sometimes landed on a clean thread on the retry, and the sandbox came up. The retry was attributed to "upstream firewall plugin bug" rather than "we have a thread netns leak". DF9's mitigation now stays as defense-in-depth — if it ever fires post-DF10, there is either (a) an actual upstream bug, (b) a different netns leak we haven't found yet, or (c) genuine iptables-nft transient state — and all three warrant investigation rather than another retry.

- **Pointer:** `runtime/containerd/containerd.go::canCreateNetNS`; entry "Go OS thread netns leak from `netns.NewNamed` / `netns.Set` without `runtime.LockOSThread`" in `backend-idiosyncrasies.md`. Cross-ref DF9 (mitigation kept as defense-in-depth) and DF8 (Kata warm-up race, separate cause).

### DF1 — `--security` flag was never in a tagged release; existing BREAKING-CHANGES entry is misleading

- **Discovered:** 2026-05-23 · **Workstream:** W-L9
- **Severity:** LOW
- **Disposition:** CLOSED 2026-05-27.
- **Description:** D6 in `layering.md` was conditional: add a BREAKING-CHANGES entry for `--security` → `--isolation` only if `--security` ever shipped in a tagged release. Audit of `git grep '\.Flags().String."security"' v0.1.0..v0.2.6` confirmed the CLI flag was never registered in any released tag — `--isolation` has been the public flag name since `v0.2.0`. Cross-verified 2026-05-27 by reading every tagged `config/config.go` for `yaml:"backend"` vs `yaml:"container_backend"`: the rename happened between `v0.1.1` and `v0.2.0`. Also verified the `gvisor`/`kata`/`kata-firecracker` isolation value strings never shipped; v0.2.0 already used `container-enhanced`/`vm`/`vm-enhanced`, and v0.1.x had no isolation field at all. The earlier "Unreleased" entry in `BREAKING-CHANGES.md` conflated this fabricated `--security` → `--isolation` flag rename (plus the parallel never-shipped value rename) with the genuine `backend:` → `container_backend:` config-key rename.
- **Fix:** entry rewritten 2026-05-27 to keep only the real config-key rename. Title became "`backend` config key renamed to `container_backend`". A history note in the entry references this DF for the audit trail.
- **Pointer:** `docs/BREAKING-CHANGES.md` § "`backend` config key renamed to `container_backend`".

### DF3 — Smoke test agent logs are unreadable in ANSI form; need rendered text snapshots

- **Discovered:** 2026-05-26 · **Workstream:** observed during W-L8b kickoff (failed `full_workflow/containerd-vm` smoke run, log `yoloai-smoketest-20260526-050950.470`)
- **Severity:** LOW
- **Disposition:** CLOSED 2026-05-27. Both halves landed: smoke-test-side capture (2026-05-26) + bug-report-integrated yoloai-level capture (2026-05-27).
- **Description:** When a smoke test fails on a TUI-driven agent (Claude Code), `agent.log` is a stream of raw ANSI control codes and cursor movements — fundamentally unrenderable without piping through a terminal emulator. Diagnosing whether the agent produced a tool-less response (DF2's hypothesis) or genuinely never made an API call requires the rendered text, not the escape sequence stream.
- **Phase 1 landed (2026-05-26):** `scripts/smoke_test.py::_capture_terminal_snapshot` shelled out per-backend (docker / podman / containerd*) to `tmux capture-pane -p -S -200 -t main` and wrote `terminal-snapshot.txt` + `terminal-snapshot.ansi` into the preserved attempt directory. Best-effort; tart/seatbelt skipped silently.
- **Phase 2 landed (2026-05-27):** the capture moved into yoloai itself.
  - **New primitive:** `sandbox.Manager.CaptureTerminal(ctx, name, scrollback)` in `internal/sandbox/terminal.go` uses the runtime's existing non-interactive `Exec` surface (no PTY, no output corruption) to run `tmux capture-pane`. Backend-specific socket dispatch is hidden inside `runtime.TmuxSocket(sandboxDir)` so tart and seatbelt now capture too — the per-backend Python dispatch couldn't reach them.
  - **Sandbox sub-handle:** `Client.Sandbox(name).CaptureTerminal(ctx, scrollback) (TerminalSnapshot, error)` wraps the manager method; `TerminalSnapshot` carries Plain + ANSI byte slices.
  - **CLI command:** `yoloai sandbox <name> terminal-snapshot [--ansi]` calls the sub-handle and writes the bytes to stdout. Returns `ErrContainerNotRunning` for the "best-effort skip" path callers (bug-report writer, smoke test) need.
  - **Bug-report integration:** `internal/cli/sandboxcmd/bugreport.go::writeBugReportTerminalSnapshot` adds a "Terminal snapshot (DF3)" section to `yoloai sandbox <name> bugreport unsafe`, so users hitting the failure outside the smoke test get the same diagnostic. Safe reports omit it (terminal output may contain prompts / API responses).
  - **Smoke test migration:** `_terminal_snapshot_cmd` (the per-backend dispatch) deleted; `_capture_terminal_snapshot` rewritten to call `yoloai sandbox <name> terminal-snapshot [--ansi]` once per variant. ~60 lines of per-backend code → ~20 lines of CLI invocations, and tart/seatbelt now produce captures too.
  - **Tests:** `internal/sandbox/terminal_test.go` covers not-running rejection, tmux command shape (plain + ANSI variants, scrollback ON/OFF), and partial-result semantics on ANSI failure.
- **Pointer:** `internal/sandbox/terminal.go::CaptureTerminal`, `sandbox.go::Sandbox.CaptureTerminal`, `internal/cli/sandboxcmd/terminal_snapshot.go`, `internal/cli/sandboxcmd/bugreport.go::writeBugReportTerminalSnapshot`, `scripts/smoke_test.py::_capture_terminal_snapshot`. Cross-ref DF2/4/5/6/8.

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
- **Disposition:** CLOSED 2026-05-27 (partial — see Followup below).
- **Description:** The failed `containerd-vm` run showed `wait_for_ready(pattern=❯)` taking 46 seconds (sandbox.jsonl, 05:10:39 → 05:11:25) before the prompt was even delivered. That 46s ate over a third of the `stall_grace_secs=120` window — so when stall detection fired, only ~33s of that window covered actual agent work. The smoke-test failure message ("agent idle for 9s+") was identical whether the agent was idle for 9s on top of 46s ready + 33s work, or 9s on top of 5s ready + 74s work. These two cases have very different diagnoses (VM-startup tuning vs. agent-behavior tuning) but no signal distinguished them in the failure report.
- **Fix landed 2026-05-27:** `scripts/smoke_test.py::wait_for_sentinel` now calls a new `_idle_phase()` helper when the idle-fail fires. The helper reads the exchange dir via `yoloai files ls` and classifies based on whether the smoke prompt's first action (`touch /yoloai/files/in-progress`) has landed: if `IN_PROGRESS` or `SENTINEL` is present → "after the prompt was delivered, no progress past <sentinel>"; if the dir is empty → "before the prompt was even processed; no <sentinel>". The two phases get distinct failure messages slotted into the existing AssertionError. Diagnosis is now self-classifying for any future idle-stall fail.
- **Followup (deferred, separate workstream):** DF7's "re-measure stall_grace_secs" can now use the phase signal — only "before the prompt was even processed" cases count toward startup-latency tuning; "after the prompt was delivered" cases are agent-behavior.
- **Pointer:** `scripts/smoke_test.py::wait_for_sentinel`, `scripts/smoke_test.py::Test._idle_phase`.

### DF11 — Smoke test orchestration exceeds macOS concurrent-VM limit on Tart; some VMs leak across runs

- **Discovered:** 2026-05-26 · **Workstream:** observed during W-L8b kickoff (two macOS smoke runs)
- **Severity:** LOW
- **Disposition:** CLOSED 2026-05-27. W-L14 landed the error-mapping half (`ResourceLimitError` from `runtime/tart`); the cross-run leak is now handled by a smoke-driver pre-run prune. Commit 3c433b0 added a post-run prune (catches the current run's wedged-shim destroys); 2026-05-27 adds the pre-run prune (catches state from prior runs that exited mid-flight). (Renumbered from DF9 → DF11 to resolve a numbering collision with the Kata-netns DF9/DF10 chain.)
- **Description:** Two failure surfaces, same end-state. Both `stop_start/tart` attempts in two consecutive macOS smoke runs failed at `tart run` with `"The number of VMs exceeds the system limit (other running VMs: …)"`. Apple's `VZError.virtualMachineLimitExceeded` (code 6) — macOS limits concurrent VMs (commonly 2 on base Apple Silicon, more on M-Pro/Max).

  **Two distinct contributing factors:**

  1. **Intra-run parallelism.** The smoke test runs `full_workflow/tart` and `stop_start/tart` in parallel; both create their own Tart VMs; the third concurrent VM hits the cap. This is the case **W-L14** addresses: detect Tart's stderr substring `"The number of VMs exceeds the system limit"`, wrap as a typed `ErrConcurrentVMLimit`, surface a user-friendly message instead of the raw tart error.

  2. **Cross-run VM leak.** Comparing the two failure outputs:
     - Run 1's blocking VMs: `1779775833-workflow-tart` + `1779775969-workflow-tart`
     - Run 2's blocking VMs: `1779776810-workflow-tart` + **`1779775833-workflow-tart`**

     VM `1779775833-workflow-tart` appears in BOTH runs — it's a leaked VM from a prior smoke invocation that wasn't cleaned up. This is a smoke-test infrastructure problem orthogonal to W-L14: even after W-L14 maps the error nicely, the user still can't run smoke tests on the affected host until they manually `tart stop` the leaked VMs.

  Two corresponding fixes — both now landed:
  - **W-L14 (landed, commit 1f9ebed):** error mapping for `ResourceLimitError`. The user-facing message is "macOS concurrent VM limit reached — only 2 macOS VMs can run simultaneously" + a pointer to `yoloai sandbox stop`.
  - **Smoke-driver pre-run prune (landed 2026-05-27):** `scripts/smoke_test.py::_prerun_prune` runs `yoloai system prune --yes` once before tests start. The underlying prune inherits the wedged-Kata-shim escalation (commit 3c433b0) and the wedged-Tart-VM escalation (commit 0b6d2f9), so it can't hang on the same orphan that caused the leak. The pre-run path catches state left by prior smoke invocations that exited mid-flight (Ctrl-C, OOM, etc.); the existing post-run prune (also in 3c433b0) catches the current run's wedged-destroy timeouts.

- **Pointer:** `docs/contributors/archive/plans/layering-refactor.md::W-L14`, `docs/contributors/design/research/tart-limit-detection.md`, `scripts/smoke_test.py::_prerun_prune` and `::cleanup`.

### DF8 — `containerd-vm` "agent idle after prompt" fires across the full range of startup times; root cause is NOT startup-tuning

- **Discovered:** 2026-05-26 · **Workstream:** observed during W-L8b kickoff
- **Severity:** LOW
- **Disposition:** RESOLVED 2026-05-26 — superseded by DF10. The "agent idle after prompt" family was root-caused as the DF10 netns thread leak (not agent behavior); DF8 FIX V3 (probe) + DF10's LockOSThread fix gave 20/20 success. Kept as the diagnostic trail.
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

### DF8 (4th data point, 2026-05-26): containerd-vm idle-after-prompt failed once, passed on retry

- This session's fourth `full_workflow/containerd-vm` failure (log `yoloai-smoketest-20260526-062648.461`) followed the same pattern as the second: failed attempt 1 with the documented "agent idle 9s+" signature, passed on the retry. Continues to reinforce DF8's revised hypothesis (no Type A; all failures are post-ready-idle agent behavior, possibly DF2's tool-less-response on Haiku under the QEMU CPU profile).
- Four-of-four observations is a clear pattern; the action items in DF8 (rendered transcript capture per DF3) remain the next step.

### DF8 (5th data point, 2026-05-26): containerd-vm failed BOTH attempts

- Fifth `full_workflow/containerd-vm` failure (log `yoloai-smoketest-20260526-063648.819`) — first one in this session to fail BOTH attempts. Running totals across the W-L8b-kickoff session: 5 failures, 3 transient (pass on retry), 2 persistent (fail both). Still 100% post-ready-idle shape (same `do_epoll_wait + no connections` signature); the persistent-vs-transient split is along an unknown axis. Whether the "warming effect on retry" hypothesis (DF8 first version) is real or coincidence is still open — the rendered transcripts of DF3 are needed to distinguish "Haiku produced different output on retry" from "VM warmed up I/O cache, second run hit the API window."

### DF8 (6th data point, 2026-05-26): `containerd-vmenhanced` exhibits the same failure mode

- Log `yoloai-smoketest-20260526-120447.993`. First observation of `full_workflow/containerd-vmenhanced` failing the same way `containerd-vm` has been failing: "agent idle 9s+ without sentinel 'done'", passed on retry. Same session: docker / podman / docker-cenhanced / containerd-vm all PASS, only vmenhanced fails first attempt. Host `/` at 76% / 18G free rules out the disk-pressure pattern from `smoke-containerd-disk-pressure` project memory.
- **Implication:** the failure family is not unique to the `containerd-vm` snapshotter setup — `containerd-vmenhanced` (devmapper snapshotter) reproduces it too. What's common is Kata+QEMU, not the snapshotter. Both candidates in the refined hypothesis (Haiku tool-less response under QEMU CPU profile, or Kata networking warm-up race) remain consistent.
- Still PARKED pending DF3's rendered tmux capture-pane snapshot. Action item unchanged.

### DF8 (7th data point, 2026-05-26): `containerd-vm` failed BOTH attempts; `containerd-vmenhanced` PASSED same session

- Log `yoloai-smoketest-20260526-125802.053`. `containerd-vm` failed both attempts (the "agent idle 9s+" signature, same as DF8). `containerd-vmenhanced` passed in the same session. The previous run (6th data point, 120447.993) showed the inverse: vmenhanced fails first attempt, vm passes. Two adjacent runs with opposite outcomes between the two containerd snapshotters.
- **Implication:** the failure is NOT correlated between vm and vmenhanced on a single run, which argues against "host was in a bad state at run start" as the explanation. Each backend independently rolls the dice — consistent with a per-backend race (e.g. Kata netns wiring, QEMU CPU latency variability) rather than a global precondition. Now 2 confirmed failures of vmenhanced, 7 of vm.
- Still PARKED pending DF3. Confirming with rendered tmux output remains the unblocker for any further diagnosis.

### DF8 FIX V3 LANDED 2026-05-26

V2's external-probe target was right, but V2 also kept a fast-path
early-exit on missing default route, treating it as "network=none →
declare ready". The 13th data point (run `161305.478`) proved that
incorrect: `stop_start/containerd-vm` failed BOTH attempts with the
DF8 signature (`dns=fail tcp=fail`) and NO probe annotation — the
probe finished in <200ms (under the log threshold), which can only
mean it took the fast-exit. The smoke-test diagnostic probe, run
seconds later, confirmed the network was actually broken.

Root cause of V2's residual flake: `ip route show default` returns
empty during a transient setup window before CNI fully wires the
netns. V2 treated that the same as a permanent absent route ("user
passed --network=none"). But cni.go::setupCNI is unconditional in
the containerd backend — every sandbox gets a network — so missing
route here is *always* transient, never a network=none signal.

V3 removes the missing-route early exit. The probe now retries on
missing-route, DNS failure, OR TCP timeout. The 30s outer budget
catches whichever stage is racing.

Hypothetical cost: if a future change makes the containerd backend
honor `NetworkMode == "none"`, V3 will loop 30s and warn on those
sandboxes. Acceptable; the code comment documents it for that
hypothetical future caller.

History:
- V1: gateway:22 RST = success — too lenient, MASQUERADE not tested
- V2: DNS + external TCP — good target, but missing-route early exit
       miscategorized transient absence as network=none
- V3: same target as V2, retry on missing-route too (this version)

### DF8 FIX V2 LANDED 2026-05-26 (superseded by V3)

Initial V1 fix (gateway-only probe) proved insufficient — the 12th data
point (run `154844.342`) showed three containerd failures still slipping
through. The smoke-test probe inside the same sandbox reported
`tcp=fail` to `1.1.1.1:443` while my runtime probe to the gateway had
just declared "ready". The TC mirred filter (Kata bridge ↔ TAP) installs
**before** host-side MASQUERADE / forwarding is ready, so a gateway
probe returns RST ("success") while external traffic still times out.

The runtime probe and the smoke-test probe were testing different
stages. V2 fixes that:

  V1 (insufficient): TCP to gateway:22 — exits 0 on RST.
  V2 (current):      DNS lookup api.anthropic.com + TCP-connect.

The full chain (DNS resolution + TC filter + bridge + MASQUERADE +
host forwarding) is now what the probe verifies, matching the agent's
actual reality. Per-stage timeouts: 4s DNS + 3s TCP + overhead ≈ 7.5s
worst-case; per-probe context: 10s. Outer budget unchanged at 30s ×
500ms intervals.

For network-isolated sandboxes that allow api.anthropic.com (the
common case), this passes. For sandboxes that don't allow it, the
probe fails — but the agent would also have failed, so matching the
agent's reality is correct.

DF8 family fix iterating; will check next smoke run for empirical
confirmation. If V2 still misses, we're looking at a deeper race
than "MASQUERADE comes up after Start returns" — possibly a kernel
conntrack delay or sysctl pending settings.

### DF8 FIX V1 (2026-05-26): gateway-only probe — INSUFFICIENT

V1 of the fix used `bash -c '</dev/tcp/$gw/22'` to the bridge gateway.
12th data point showed this probe declares ready before the agent's
real path works. Replaced with V2 (DNS + external TCP). Kept here for
the record because the design logic ("any flow proves wiring") was
sound — what was wrong was the path tested.

### DF8 (11th data point, 2026-05-26): **SECOND SMOKING GUN — staged probe pinpoints the broken CNI stage**

- Log `yoloai-smoketest-20260526-150145.945`. Two `containerd-vm` failures (full_workflow + stop_start, both first-attempt then passed retry). Both show the identical staged-probe signature:
  ```
  network: unreachable [dns failed | dns=fail route=ok tcp=fail https=exit 28]
  ```
- **Translation:**
  - `route=ok` — CNI bridge plugin ran, IPAM assigned an IP, default route inserted into the netns.
  - `dns=fail` + `tcp=fail` — packets going OUT of the netns silently dropped. UDP query to the nameserver and TCP SYN to `1.1.1.1:443` both produce no response (timeout, not refused).
  - `https=exit 28` — confirms total outbound dead, same as the TCP probe.
- **Locating the broken stage:** the netns IS wired, the route IS pointing the right way, but packets aren't actually reaching the upstream. For Kata-VM specifically (which both failures here are), `backend-idiosyncrasies.md` documents the architecture: Kata creates a `tap0_kata` TUN/TAP inside the netns and installs a TC mirred filter that mirrors traffic between `eth0` and `tap0_kata`. The filter is what carries packets between the VM (via TAP) and the bridge (via veth/eth0). If the TC filter isn't fully installed when the agent's first packet fires, packets go in but don't come out — exactly what we see.
- **Confirmation that this is a race, not a deterministic break:** retries pass within 30s. The TC filter installation completes during the retry window.
- **Proposed fix location:** `runtime/containerd/cni.go::setupCNI` (or a post-`NewTask()` hook in `lifecycle.go::Create`). Two viable approaches:
  1. **Connectivity probe after CNI ADD + task.Start**: run a brief in-netns ping/TCP-connect to the gateway or upstream before declaring the sandbox ready. Fail-fast or short retry loop.
  2. **Post-Start sleep + verify**: short stabilization delay (similar to the existing "Tart exec needs brief stabilization delay after boot" pattern documented in backend-idiosyncrasies.md), then verify connectivity once.
- Approach (1) is more robust (catches deterministic CNI breakage too). Approach (2) is simpler but doesn't surface the real failure cleanly if connectivity NEVER comes up. Both should add a `backend-idiosyncrasies.md` entry describing the race.
- **DF8 family is now fully diagnosed.** Closing out further data-collection diagnostic work; the next step on this front is the fix.

### DF8 (10th data point, 2026-05-26): staged probe hit our outer 20s timeout — likely getent hanging

- Log `yoloai-smoketest-20260526-144807.235`. Three failures in one run (most so far in a single session): `full_workflow/containerd-vmenhanced` failed BOTH attempts (first time persistent for vmenhanced in this session), `stop_start/containerd-vm` failed first attempt then passed retry. All three carry `network: unreachable (subprocess timeout)` — meaning the multi-stage probe didn't complete within the 20s outer subprocess budget. Terminal snapshots still capture (DF3 works); the staged probe output didn't (we lost the per-stage detail to the timeout).
- **Root cause analysis:** the probe script's most likely hang is `getent hosts api.anthropic.com` when DNS is broken. glibc's resolver, with no nameserver responding, waits the configured timeout * tries — typically 5s × 3 = 15s, sometimes longer. None of the stages had per-step timeouts; the only bound was our outer 20s. So a slow DNS step starves the rest.
- **Fix landed in same commit batch:** every stage now wrapped in `timeout N` (5s/5s/5s/9s = 24s worst case). Outer subprocess budget raised to 30s for ctr-exec setup overhead. On subprocess timeout we now ALSO parse any partial stdout the script emitted before the timeout fired, so partial information ("dns=ok route=fail tcp=?…") is preserved.
- **Tentative inference from the loss:** the run that hit our timeout had THREE containerd failures including one persistent. The agent's terminal still showed "ConnectionRefused" — same retry pattern. If `getent` was hanging that's also a signal: DNS inside the sandbox isn't just slow, it's *broken*. The likely earliest CNI stage failure (resolv.conf not wired or nameserver unreachable from inside the netns) is now visible to the next data point.

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

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

### DF14 — `TestCLI_StartStop` intermittent `inspect instance after start: instance not found` on podman

- **Discovered:** 2026-06-01 · **Workstream:** W-L1 (G7 store-surface carve)
- **Severity:** LOW
- **Disposition:** PARKED
- **Description:** A single `TestCLI_StartStop` run on the podman backend failed at `integration_test.go:183` (`new --agent test cli-startstop`) with `inspect instance after start: instance not found` (run duration 2.70s). The error originates in `verifyInstanceRunning`, which does `time.Sleep(1 * time.Second)` then a single `rt.Inspect(ctx, cname)` and wraps the error — so ~1s after container launch podman momentarily could not find the just-started container. **NOT a regression** from the G7 carves: those relocate host-side Go functions (name validation, path computation, log paths, and the *post-start* `SandboxMetadata` summary) and never touch the create/start/inspect path where the error fires. Did **not** reproduce — `TestCLI_StartStop` passed cleanly at HEAD `33982a3` on both backends (docker 1.51s, podman 2.68s). Same non-deterministic podman family as [[DF13]] ("podman flaked alone on this leg; docker recovered"). Candidate remedy: replace `verifyInstanceRunning`'s bare 1s sleep + single `Inspect` with a short retry/backoff so a transient post-launch "not found" self-heals instead of failing the start.
- **Trigger:** the next `TestCLI_StartStop` / `stop_start` "instance not found" or "not found shortly after start" failure on podman — if it recurs, capture `podman ps -a` + the container's exit state at the moment `verifyInstanceRunning` fires (before teardown) to distinguish a podman inspect race from a container that genuinely exited <1s after start, then implement the retry/backoff. If no recurrence across the next several podman integration/smoke runs, evict as a one-off environmental flake.
- **Pointer:** `internal/sandbox/launch/launch.go:257` (`verifyInstanceRunning`); test `internal/cli/integration_test.go:183` (`TestCLI_StartStop`)

### DF17 — CLI `--json` output has no structural convention (list-envelope + error/empty shape vary by command)

- **Discovered:** 2026-06-03 · **Workstream:** Public-API "right reasons" round (A4 re-examination)
- **Severity:** MEDIUM
- **Disposition:** ESCALATED
- **Description:** The CLI `--json` output is the live machine-readable contract (wrapper apps shell
  out to the `yoloai` binary and parse it). Casing is already uniform (snake_case everywhere), but
  the **structure** is ad-hoc per command: (1) list-type commands disagree on shape — some return a
  bare array (`system backends`, `system agents`, `extensions list`, `stop`, `destroy`), others an
  envelope (`sandbox list` → `{"sandboxes":[…],"unavailable_backends":[…]}`, `system disk` →
  `{"entries":[…]}`) — with no rule for when to wrap; (2) error/empty representation disagrees —
  array commands carry a per-item `"error"` omitted-on-success (`stop`/`destroy`/`disk`), bare-object
  commands have no error field at all (errors via exit code + stderr), and empty results are
  variously `[]`, `{}`, or a bare object with the array key absent. Escalated (not parked) because
  this release is meant to set the **baseline public-facing interface** (API + CLI/JSON + MCP) from
  which all future migrations are measured — the convention must be fixed *now*, before the baseline
  freezes. CLI-layer-owned: the fix is a `--json` output style guide + a shared emission helper, not
  a public-API change. (Split out from [[A4]], whose original public-struct-tag premise was
  abandoned.)
- **Pointer:** `internal/cli/cliutil/json.go` (`WriteJSON`); divergent sites incl.
  `internal/cli/system/disk.go` (`formatDiskJSON` → `{"entries"}`), `internal/cli/sandboxcmd/list.go`
  (`{"sandboxes"}` envelope), `internal/cli/system/backends_agents.go` (bare array),
  `internal/cli/lifecycle/stop.go` / `destroy.go` (bare array, per-item `error`).

### DF18 — Backend run-coverage gap: live-daemon error paths + zero Seatbelt/Tart run coverage

- **Discovered:** 2026-06-04 · **Workstream:** testing-critique (T13 split-out)
- **Severity:** MEDIUM
- **Disposition:** PARKED
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

## Policy origin

Established in [architecture-remediation.md](../archive/plans/architecture-remediation.md) and inherited by [layering-refactor.md](../archive/plans/layering-refactor.md).

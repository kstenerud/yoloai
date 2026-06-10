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

### DF19 — `make check` deletes the developer's real yoloai VMs via the system Prune test

- **Discovered:** 2026-06-09 · **Workstream:** Tart `-xcode` base-image A/B investigation
- **Severity:** CRITICAL (observable data loss)
- **Disposition:** ESCALATED
- **Description:** `TestPrune_ExecutesClassifications` (`system_test.go`, package `yoloai`, **no build tag → runs in `make check`**) calls the real `System.Prune(DryRun:false)`. Prune "iterates every registered backend available in the current environment and spins up an ephemeral runtime per backend" (`system.go:31`), so it runs the **tart orphan-sweep** (and the docker/podman equivalents) against the developer's **real** store. `newTestClient` isolates only yoloAI's DataDir/HomeDir; the `tart` CLI still reads the real `~/.tart` (it honors `$HOME`/`TART_HOME`, which the test never sets). Result: running `make check` on a host with live yoloAI sandboxes / runtime bases **deletes them** (keeps `yoloai-base`, sweeps the rest as orphans). Reproduced 2026-06-09 — a planted `yoloai-canary-*` VM vanished after a single `make check`. This is what repeatedly wiped the Tart runtime base during the A/B (the "unexplained disappearance").
- **Fix:** isolate real backends in mutating system tests per [testing-principles §6](../principles/testing-principles.md): `t.Setenv("TART_HOME", t.TempDir())` and point `DOCKER_HOST`/`CONTAINER_HOST` at a bogus socket so the backend sweep hits empty/unavailable stores, while the host-side sandbox-dir classification (the test's actual subject) still runs against the temp layout. Apply in `newTestClient` (covers all system tests) or the prune tests specifically; verify it doesn't change the Info/Doctor tests' expectations.
- **Pointer:** `system_test.go` (`TestPrune_ExecutesClassifications`), `profile_test.go::newTestClient`, `system.go::Prune` (backend sweep).

### DF20 — Stale-base detection flags the *wanted* base as "superseded" when `tart.image` pins a non-default image

- **Discovered:** 2026-06-10 · **Workstream:** Apple `container` backend planning (side-discovery while diagnosing a leftover config override)
- **Severity:** MEDIUM (actively recommends deleting a wanted artifact; gated behind a dry-run preview + `y/N`, so not silent data loss)
- **Disposition:** PARKED
- **Description:** `staleBaseImagesFrom` classifies every installed Tart base whose repo ≠ the *current* repo as "superseded (safe to remove, no rebuild)", where current = `baseImageRepo(r.resolveBaseImage(""))` — and `resolveBaseImage` honors the `tart.image` config override. So an explicit `tart.image` pin makes the host-matched `macos-<codename>-base` look superseded, and `yoloai doctor` / `yoloai system prune --stale-bases` offer to delete the base the user actually uses. Observed 2026-06-10: a leftover `tart.image: ghcr.io/cirruslabs/macos-tahoe-xcode:26.5` (from the `-xcode` A/B) made `prune --stale-bases` offer to remove the real `macos-tahoe-base` (29.8 GiB) as "safe, no rebuild." Technically consistent with the override, but a sharp edge — nothing surfaces *which* configured image drives the "current" determination, so a config typo or leftover reads as a free cleanup.
- **Fix:** surface the configured `tart.image` (and the resolved "current base repo") in the `doctor` / `prune --stale-bases` output so the user can see *why* a base is flagged; and/or treat an explicit non-`-base` override specially — don't label the host-matched `-base` "no rebuild, safe to remove" when the only reason it is non-current is an explicit override (warn instead, or exclude it from the stale set). Keep the genuine cross-macOS supersede case (old codename after an OS upgrade) working.
- **Trigger:** the next time a custom `tart.image` is set in earnest (xcode image-mode adoption, pinning an older/newer macOS, etc.) — before that ships broadly, make the stale-base output name the driving config so it can never recommend deleting a wanted base.
- **Pointer:** `internal/runtime/tart/diskusage.go:139` (`staleBaseImagesFrom`; `currentRepo` from `resolveBaseImage("")`), `internal/runtime/tart/stalebases.go:23` (`PruneStaleBases`); the `doctor` / `system prune --stale-bases` surfacing.

## Policy origin

Established in [architecture-remediation.md](../archive/plans/architecture-remediation.md) and inherited by [layering-refactor.md](../archive/plans/layering-refactor.md).

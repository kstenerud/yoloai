> **ABOUTME:** Terminal sink for findings decided "won't fix" and permanently dropped.
> Distinct from resolved (the issue got fixed) and deferred (parked with a revival trigger).

# Abandoned findings

Findings (issues discovered mid-work) permanently dropped — decided **"won't fix."** Distinct
from [`findings-resolved.md`](findings-resolved.md) (the finding got *fixed*) and
[`findings-deferred.md`](findings-deferred.md) (parked with a revival trigger): items here are
terminal and not expected to come back. Each carries a short **`Why:`** line recording the
reason for abandonment. Newest first.

### DF62 — interactive commands (`yoloai destroy`) have no `--yes`/non-interactive flag

- **Discovered:** 2026-06-29 · **Workstream:** disk-reclaim / prune evaluation
- **Severity:** LOW (ergonomics)
- **Disposition:** ABANDONED (user decision 2026-06-29)
- **Why:** The premise was wrong. Investigation showed `yoloai destroy` does **not** prompt interactively at all — `checkActiveWork` *refuses with an error* (requiring `--abandon-unapplied`) for unapplied work and otherwise just proceeds; the code documents this deliberate choice ("We never prompt to widen the scope, so there is no --yes to paper over it", `internal/cli/lifecycle/destroy.go`). And every command that *does* call the interactive `cliutil.Confirm` helper — `apply`, `profile delete`, `system prune`, `system tart` — **already** has a `--yes/-y` flag (via `cliutil.EffectiveYes`). So there is no lost flag and no inconsistency to fix. The only adjacent gap (`destroy --all` destroys every sandbox with no confirmation) was reviewed and the existing promptless design was kept by the user — adding `--yes`/a prompt would contradict the documented decision. The `-y` that errored simply doesn't exist by design.
- **Pointer:** `internal/cli/lifecycle/destroy.go` (no `Confirm`; flags = `--all`, `--abandon-unapplied`); `internal/cli/cliutil/json.go` `EffectiveYes`; existing `--yes` on prune/apply/profile/tart.

### DF7 — `stall_grace_secs=120` for containerd-vm may need re-measuring against current startup latency

- **Discovered:** 2026-05-26 · **Workstream:** observed during W-L8b kickoff (same failure as DF3-DF6)
- **Severity:** LOW
- **Disposition:** PARKED — but **downweighted** by DF8 evidence (see below). The 46s `wait_for_ready` from the original failure was an outlier; a subsequent failure showed an 11s `wait_for_ready` with the same end-state.
- **Description:** The 120s grace for `containerd-vm` was set against a measured QEMU startup distribution at the time. Two runs in this session failed at the 129s mark with the agent genuinely idle (DF3-6) — suggesting either (a) the agent-behavior issue (DF2) is real and unrelated to grace tuning, or (b) startup has drifted upward and the grace no longer matches reality. The 46s `wait_for_ready` observed in the first failure log was consistent with (b), but DF8 shows the same idle-after-prompt failure also fires with a fast 11s `wait_for_ready` — so startup tuning is at best a partial answer. **Proposed action:** re-measure containerd-vm startup latency (from `entrypoint.start` to `❯` ready pattern) across, say, 30 successful runs; pick the 99th percentile + safety margin as the new `stall_grace_secs`. Do this AFTER DF3/DF6 land — without rendered transcripts and the ready-vs-idle split, the measurement is muddled.
- **Pointer:** `scripts/smoke_test.py::BackendSpec.stall_grace_secs`, `docs/contributors/backend-idiosyncrasies.md#qemu-slow-startup-exceeds-smoke-test-stall-grace-period`

**Why:** DF8's data across 11–46 s `wait_for_ready` plus DF10's root-cause (the `canCreateNetNS` thread netns leak) conclusively ruled out startup-latency tuning as the fix — the failures were network, not startup. Re-measuring `stall_grace_secs` would not have helped.

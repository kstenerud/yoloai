<!-- ABOUTME: Terminal sink for abandoned findings — decided "won't fix", drained from unresolved-findings.md. -->
<!-- ABOUTME: Distinct from resolved- (fixed) and deferred- (parked w/ trigger): these are permanently dropped. -->

# Abandoned findings

Findings (issues discovered mid-work) permanently dropped — decided **"won't fix."** Distinct
from [`resolved-findings.md`](resolved-findings.md) (the finding got *fixed*) and
[`deferred-findings.md`](deferred-findings.md) (parked with a revival trigger): items here are
terminal and not expected to come back. Each carries a short **`Why:`** line recording the
reason for abandonment. Newest first.

### DF7 — `stall_grace_secs=120` for containerd-vm may need re-measuring against current startup latency

- **Discovered:** 2026-05-26 · **Workstream:** observed during W-L8b kickoff (same failure as DF3-DF6)
- **Severity:** LOW
- **Disposition:** PARKED — but **downweighted** by DF8 evidence (see below). The 46s `wait_for_ready` from the original failure was an outlier; a subsequent failure showed an 11s `wait_for_ready` with the same end-state.
- **Description:** The 120s grace for `containerd-vm` was set against a measured QEMU startup distribution at the time. Two runs in this session failed at the 129s mark with the agent genuinely idle (DF3-6) — suggesting either (a) the agent-behavior issue (DF2) is real and unrelated to grace tuning, or (b) startup has drifted upward and the grace no longer matches reality. The 46s `wait_for_ready` observed in the first failure log was consistent with (b), but DF8 shows the same idle-after-prompt failure also fires with a fast 11s `wait_for_ready` — so startup tuning is at best a partial answer. **Proposed action:** re-measure containerd-vm startup latency (from `entrypoint.start` to `❯` ready pattern) across, say, 30 successful runs; pick the 99th percentile + safety margin as the new `stall_grace_secs`. Do this AFTER DF3/DF6 land — without rendered transcripts and the ready-vs-idle split, the measurement is muddled.
- **Pointer:** `scripts/smoke_test.py::BackendSpec.stall_grace_secs`, `docs/contributors/backend-idiosyncrasies.md#qemu-slow-startup-exceeds-smoke-test-stall-grace-period`

**Why:** DF8's data across 11–46 s `wait_for_ready` plus DF10's root-cause (the `canCreateNetNS` thread netns leak) conclusively ruled out startup-latency tuning as the fix — the failures were network, not startup. Re-measuring `stall_grace_secs` would not have helped.

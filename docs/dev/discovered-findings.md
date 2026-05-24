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

## Policy origin

Established in [architecture-remediation.md](plans/architecture-remediation.md) and inherited by [layering-refactor.md](plans/layering-refactor.md).

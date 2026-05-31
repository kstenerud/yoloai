<!-- ABOUTME: Holding pen for deferred findings parked from unresolved-findings.md. -->
<!-- ABOUTME: Each item carries a revival trigger; when it fires the item flows back to unresolved. -->

# Deferred findings

Findings (issues discovered mid-work) parked as "not now." Unlike
[`resolved-findings.md`](resolved-findings.md) (terminal history), every item here is still
potentially actionable and carries a **`Trigger:`** line — the condition that should pull it
back into [`unresolved-findings.md`](unresolved-findings.md). The trigger may be unlikely, but
it must exist so the item can be evaluated for eviction later. Newest first.

### DF2 — Smoke test prompt may provoke a clarifying-question idle on Haiku (containerd-vm)

- **Discovered:** 2026-05-24 · **Workstream:** observed during W-L4 validation
- **Severity:** LOW
- **Disposition:** PREVENTIVE FIX LANDED 2026-05-27 (option (a)); empirical verification still TBD on the next flake — if there ever is one — using the rendered transcript captured by DF3.
- **Description:** `stop_start/containerd-vm` failed once with the documented "agent idle for 9s+ without sentinel 'done'" signature, then passed cleanly on isolated rerun. Existing idiosyncrasy entry blames QEMU slow startup (extended by `stall_grace_secs=120` in `scripts/smoke_test.py:191,212,216`), but the prompt itself is also suspicious: `"Run this shell command exactly as written; do not modify it or ask for clarification: touch …"`. The negative phrasing ("do not ask for clarification") can prime smaller / faster models like Haiku to do exactly that — output a clarifying question (no tool call), which yoloAI's monitor classifies as `idle`. The agent then waits forever for a user response that never comes, while the smoke test waits forever for the `done` sentinel file.
- **Fix landed 2026-05-27 (option (a), preventive):** `scripts/smoke_test.py::_prompt` rephrased from `"Run this shell command exactly as written; do not modify it or ask for clarification: <cmd>"` → `"Run this shell command exactly as written, using your shell/bash tool: <cmd>"`. Keeps the explicit "Run this shell command" wrapper that previously resolved the v1 "what is this code?" failure on bare snippets, drops the negation that DF2 hypothesizes activates the failure response, and adds a positive tool reference so the model gets a hint that a tool call is expected. The full iteration history is preserved in `_prompt`'s docstring so future readers don't re-derive the rationale.
- **Why not option (b)?** Distinguishing "tool-less response on a tool-required prompt" from "real idle" requires monitor-side classifier changes (`internal/runtime/monitor/status-monitor.py` reading the agent's tmux output, parsing the last narrative turn, asking "did the model speak without calling a tool?"). That's a heavier instrument than the cost of one prompt rephrase. Revisit if option (a) doesn't eliminate the flake class — if a future failure transcript still shows Haiku producing a non-tool-using reply under the new wording.
- **Pointer:** `scripts/smoke_test.py::_prompt` (current wording + iteration history), `docs/contributors/backend-idiosyncrasies.md#qemu-slow-startup-exceeds-smoke-test-stall-grace-period` (existing complementary entry).

**Trigger:** the next post-DF10 `containerd-vm` "agent idle" flake — inspect DF3's rendered transcript to confirm whether Haiku produced a tool-less reply under the QEMU CPU profile. If no such flake recurs now that DF10 fixed the netns leak, evict this finding.

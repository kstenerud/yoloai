# Critique

Idle detection architecture review — March 2026. Assessed [idle detection research and architecture proposal](research/idle-detection.md) against actual codebase.

## What's Working Well

- The pluggable detector framework is well-designed — clean separation of capability declaration from detector selection.
- `status.json` as IPC is sound. Detectors write, host reads. No change needed.
- The confidence/stability counter approach handles flapping elegantly.
- wchan research is thorough and verified against kernel 6.17.4.
- Q4 resolution (TCP traffic volume via `ss`/`nettop`) closes the WebSocket gap.
- Hook-based detection for Claude Code is verified working and survives monitor death (hooks write `status.json` directly).

## Findings

### 1. `idle_threshold` is live code, not dead (Medium)

**Observation:** The design doc (known issue #1) calls `idle_threshold` "never read or used." This is wrong. `config/config.go:46` parses it from YAML, `config/config.go:86` lists it in `knownSettings`, `sandbox/create_prepare.go:28,40,133` propagates it through profile resolution, and `sandbox/meta.go:38` stores it in Meta. It flows through the entire creation pipeline.

Only the `DefaultIdleThreshold` constant in `sandbox/inspect.go:32-35` is marked deprecated. The field itself is actively wired up.

**Recommendation:** The Phase 1 cleanup must trace and remove all of these sites, not just the constant. Update the design doc's known issue #1 to list the actual removal scope: `config.go`, `create_prepare.go`, `meta.go`, `inspect.go`.

**When:** Phase 1.

### 2. Phase 2 code examples are bash, should be Python (Low)

**Observation:** Section 3.5 Phase 2 shows bash snippets (`WCHAN=$(cat /proc/...)`, `case "$WCHAN" in ...`) even though Q2 resolved the monitor will be Python from the start. The section 3.7 code changes table was updated but the inline examples weren't.

**Recommendation:** Replace bash code examples in Phase 2-4 with Python (or pseudocode) for consistency.

**When:** Before implementation begins.

### 3. `ss` / `iproute2` not in Dockerfile (Medium)

**Observation:** Q4 resolution says "Linux uses `ss -ti`" for TCP traffic volume monitoring. But `iproute2` is not installed in the base image Dockerfile. Phase 2 step 2 acknowledges `/proc/<pid>/net/tcp6` parsing as an alternative, but the Q4 resolution doesn't mention it.

**Recommendation:** Pick one approach and be consistent:
- **Option A:** Add `iproute2` to Dockerfile. Simple, `ss` is a ~100KB binary.
- **Option B:** Parse `/proc/<pid>/net/tcp6` directly in Python. No new dependency, but more code.

Option B is more aligned with the Python monitor approach (no subprocess spawning). Either way, update Q4 resolution and Phase 2 to agree.

**When:** Before Phase 2 implementation.

### 4. No macOS idle detection story for most agents (Medium)

**Observation:** On macOS (Tart/Seatbelt), wchan is unavailable. For non-hook agents (gemini, codex, aider, opencode), the primary detector is gone. What remains: `ready_pattern` (medium confidence, fragile), `context_signal` (medium, unreliable), `output_stability` (low, false positives). OpenCode on macOS has only context_signal and output_stability — both weak.

The design acknowledges this for OpenCode ("known limitation") but doesn't call out that *most agents on macOS* have significantly degraded idle detection.

**Recommendation:** Add a note to section 3.4 explicitly stating that macOS idle detection for non-Claude agents is best-effort. Consider whether this gap is acceptable or whether it warrants a macOS-specific fallback (e.g., `ps -o wchan` heuristic, even if weaker than Linux).

**When:** Before Phase 2.

### 5. ANSI escape sequences in context_signal log matching (Low-Medium)

**Observation:** The `context_signal` detector reads the pipe-pane log (`/yoloai/log.txt`) for `[YOLOAI:IDLE]` / `[YOLOAI:WORKING]` markers. This log captures raw terminal output including ANSI color/formatting codes. If the agent wraps the marker text in formatting (bold, color, markdown rendering), a literal string match will fail.

**Recommendation:** The Python monitor must strip ANSI escape sequences before matching markers. Alternatively, the context file instructions should explicitly say "print this exact text on its own line with no formatting." Both mitigations are cheap — do both.

**When:** Phase 3 implementation.

### 6. Process tree walking for wchan is underspecified (Low-Medium)

**Observation:** Phase 2 step 3 says "check children's wchan too: if any child has active network connections, the agent is working." But it doesn't specify traversal depth. Claude Code spawns Node workers, which spawn shells, which spawn build tools. Recursive `/proc/<pid>/children` traversal could be expensive.

**Recommendation:** Specify a depth limit (e.g., 2 levels) or use a smarter heuristic: only check processes with TCP connections (scan `/proc/<pid>/net/tcp6` for the agent PID's network namespace, which covers all processes in the container). The network namespace approach is both simpler and more complete.

**When:** Phase 2 implementation.

### 7. Prompt delivery race condition not addressed (Low)

**Observation:** Known issue #5 documents that `resetStatusToActive` fires before the prompt is actually delivered (up to 60s wait for agent readiness). The architecture proposal doesn't fix this — it persists into the new design.

**Recommendation:** Either fix it (reset status only after confirming delivery) or explicitly note in the architecture proposal that this is a known limitation carried forward. Currently it's documented as a known issue in Part 1 but not mentioned in Part 3.

**When:** Note it now, fix when prompt delivery is reworked.

### 8. Hook detector survives monitor death — undocumented strength (Low)

**Observation:** The `hook` detector is unique: Claude Code's hooks write to `status.json` directly, bypassing the status monitor entirely. If the Python monitor crashes, hook-based idle detection continues working (the host reads `status.json` regardless). All other detectors die with the monitor.

This is a significant reliability advantage that the design doesn't capture. It also means the monitor's role for hook agents is purely cosmetic (terminal title updates) — worth documenting for the implementation.

**Recommendation:** Add a note to the `hook` detector section (3.3) about this resilience property.

**When:** Before implementation.

## Summary Table

| # | Finding | Severity | Status |
|---|---------|----------|--------|
| 1 | `idle_threshold` is live code, not dead | Medium | **Applied** — Phase 1 updated with full removal scope, known issue #1 corrected |
| 2 | Phase 2 code examples still bash | Low | **Applied** — replaced with Python |
| 3 | `ss`/`iproute2` not in Dockerfile | Medium | **Applied** — resolved: parse `/proc` in Python, no `ss` dependency |
| 4 | Weak macOS idle detection for non-Claude agents | Medium | **Applied** — `sysctl KERN_PROC` `e_wmesg` (`ttyin`) fills the gap; wchan detector now cross-platform |
| 5 | ANSI sequences break context_signal matching | Low-Medium | **Applied** — ANSI caveat added to context_signal detector |
| 6 | Process tree walk depth unspecified | Low-Medium | **Applied** — replaced with network namespace approach |
| 7 | Prompt delivery race not addressed | Low | **Applied** — fix added to Phase 1 step 2 |
| 8 | Hook detector resilience undocumented | Low | **Applied** — resilience note added to hook detector |

## Verdict

All findings applied. macOS research (#4) found `sysctl KERN_PROC` `e_wmesg` as the macOS equivalent of Linux's wchan — the wchan detector is now cross-platform. One caveat remains: `e_wmesg` may be empty on some macOS versions, needing verification on Sequoia/Sonoma hardware.

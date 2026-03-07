# Idle Detection: Audit and Research

Comprehensive analysis of yoloAI's idle detection system — what we do today, what's broken, what alternatives exist, and potential paths forward. Conducted 2026-03.

## Part 1: Current System Audit

### 1.1 Status Model

Seven statuses defined in `sandbox/inspect.go:18-30`:

| Status | Meaning |
|--------|---------|
| `running` | Container up, agent actively working |
| `idle` | Container up, agent waiting for input |
| `done` | Container up, agent exited cleanly (exit 0) |
| `failed` | Container up, agent exited with error (non-zero) |
| `stopped` | Container stopped via `docker stop` |
| `removed` | Container gone, sandbox dir remains |
| `broken` | Sandbox dir exists, `meta.json` missing/invalid |

### 1.2 Detection Architecture: Two Paths

**Primary path — `status.json` file (no exec required):**

The status monitor inside the container writes JSON to `/yoloai/status.json`, which is bind-mounted to `~/.yoloai/sandboxes/<name>/status.json` on the host. The host reads it directly via `os.ReadFile()`.

```go
type statusJSON struct {
    Status    string `json:"status"`              // "running", "idle", "done"
    ExitCode  *int   `json:"exit_code,omitempty"`
    Timestamp int64  `json:"timestamp"`           // unix seconds
}
```

Trust rules in `parseStatusJSON` (`sandbox/inspect.go:203`):
- **"running"**: Only trusted if timestamp < 10 seconds old (`statusFileStaleness`). If stale, falls back to exec.
- **"idle"**: Trusted regardless of staleness. Rationale: idle is a persistent state written once and cleared only by `resetStatusToRunning` or agent exit.
- **"done"**: Terminal state, always trusted. Uses `exit_code` to distinguish done (0) vs failed (non-zero). Missing exit_code defaults to 1.
- Empty/zero/unknown: Falls through to exec fallback.

**Fallback path — exec-based tmux query:**

`detectStatusViaExec` (`sandbox/inspect.go:244-267`) runs:
```
tmux list-panes -t main -F '#{pane_dead}|#{pane_dead_status}'
```

Returns alive (`0|0`), done (`1|0`), or failed (`1|N`). If exec fails, defaults to `running`. **This fallback cannot detect idle** — only running vs done/failed.

**Full resolution flow** (`DetectStatus`, line 174):
1. `rt.Inspect()` — check container exists/is running
2. Not found → `StatusRemoved`; not running → `StatusStopped`
3. Try reading `status.json` from host filesystem
4. If valid and non-stale → return parsed status
5. Otherwise → exec fallback

### 1.3 Two Idle Detection Strategies

#### Hook-based (Claude Code only)

Claude Code is the **only** agent with `HookIdle: true` (`agent/agent.go:105`).

During creation, `injectIdleHook()` (`sandbox/create.go:1246-1275`) adds two hooks to Claude Code's `settings.json`:

1. **Notification hook**: On response completion, writes `{"status":"idle",...}` to `/yoloai/status.json`
2. **PreToolUse hook**: On tool use start, writes `{"status":"running",...}` to `/yoloai/status.json`

When `HOOK_IDLE=true` in the status monitor loop (`entrypoint-user.sh:188-209`), the monitor does NOT poll for idle. It only:
- Checks for pane death (writes "done" status)
- Reads `status.json` to update terminal title (`"> name"` for idle, `"name"` for running)

#### Polling-based (all other agents)

The status monitor polls every 2 seconds (`entrypoint-user.sh:196-208`):

```bash
if [ -n "$READY_PATTERN" ] && [ "$READY_PATTERN" != "null" ]; then
    PANE_CONTENT=$(tmux capture-pane -t main -p 2>/dev/null || true)
    if echo "$PANE_CONTENT" | grep -qF "$READY_PATTERN"; then
        NEW_STATUS="idle"
    fi
fi
write_status "$NEW_STATUS" null
```

### 1.4 Per-Agent Configuration

| Agent | HookIdle | ReadyPattern | Notes |
|-------|----------|--------------|-------|
| claude | **true** | `"❯"` (unused) | Hook-driven, most reliable |
| gemini | false | `"Type your message"` | Polling |
| codex | false | `"›"` | Polling |
| aider | false | `"> $"` | Polling |
| opencode | false | `""` (empty) | **Never detected as idle** |
| test | false | `""` | **Never detected as idle** |
| shell | false | `""` | **Never detected as idle** |

### 1.5 Communication Mechanisms

1. **status.json** — primary, bind-mounted, read from host
2. **Terminal title** — `"> name"` prefix when idle, set via tmux `set-titles-string "#W"`. Visual indicator only.
3. **Bell** — Claude Code emits BEL via `preferredNotifChannel: terminal_bell`. Configured in tmux (`monitor-bell on`) but **not used for status determination**. Comment says "track for idle detection" but no code reads bell state.
4. **resetStatusToRunning** (`sandbox/inspect.go:158-170`) — host writes "running" to `status.json` when sending prompts to hook-based agents

### 1.6 Consumers of Idle Status

| Command | How it uses status |
|---------|--------------------|
| `yoloai ls` | Displays status column; `--idle` filter |
| `yoloai attach` | Allows attach when running/idle/done/failed |
| `yoloai exec` | Requires running or idle |
| `yoloai stop --all` | Stops running/idle/done/failed sandboxes |
| `yoloai diff` | Warns if running/idle that diff may be incomplete |
| `sandbox.Start()` | Returns "already running" if running/idle |
| `sandbox.Destroy()` | Requires confirmation if running/idle |
| `sandbox.Reset()` | Checks running/idle for `--no-restart` |

### 1.7 Known Issues

**1. Deprecated `idle_threshold` config still exists.**
The `idle_threshold` field is in config, profiles, and meta.json, with `DefaultIdleThreshold = 30` marked deprecated (`sandbox/inspect.go:32-35`). It is never read or used. Dead configuration that could confuse users.

**2. Agents without ReadyPattern can never be detected as idle.**
OpenCode, test, and shell agents have empty `ReadyPattern` and `HookIdle: false`. Once a prompt is sent, they show as "running" forever until the process exits.

**3. Polling-based detection is inherently fragile.**
`grep -qF "$READY_PATTERN"` against `tmux capture-pane -p` has multiple failure modes:
- **False positive**: Pattern appears in agent output (e.g., agent prints a `›` character)
- **False negative**: Pattern scrolled off visible terminal area
- **Buffer boundary**: `capture-pane` only captures visible content (~50 lines)

**4. Stale "running" + exec fallback = no idle detection.**
A "running" `status.json` older than 10 seconds triggers exec fallback. But exec can't detect idle — only pane death. So if the status monitor dies, a non-hook agent stuck in idle will report as "running" forever.

**5. Race condition on prompt delivery.**
`sendResumePrompt`/`sendCustomPrompt` call `resetStatusToRunning` immediately, but prompt delivery involves waiting up to 60 seconds for the agent to be ready. During this window, status says "running" but no prompt has been delivered yet.

**6. Claude Code hook reliability concerns.**
The `Notification` hook with `idle_prompt` matcher is widely reported as unreliable upstream (fires on every response, or doesn't fire at all in VS Code). yoloAI uses a plain `Notification` hook (not `idle_prompt`-specific), which fires on every completion — this is correct for our use case but worth noting the upstream instability.

**7. Bell monitoring is configured but unused.**
tmux.conf configures `monitor-bell on` and Claude Code is set to `terminal_bell`, but no code reads bell state for detection. This is a leftover from an earlier approach.

---

## Part 2: External Research

### 2.1 tmux Built-in Mechanisms

**monitor-silence:**
tmux triggers an alert when a pane produces no output for N seconds. Cannot distinguish "waiting for input" from "thinking quietly." AI agents that think for minutes produce false positives. Latency = configured interval (minimum ~5-10s practical).

**monitor-bell / alert-bell hook:**
Detects ASCII BEL (0x07) in pane output. Near-instant. Requires the agent to emit BEL. yoloAI already configures this but doesn't use it for status determination. The bell flag clears when the tmux window is selected (attached), which is a problem for our use case.

**capture-pane + pattern matching:**
What yoloAI does for polling-based agents. The tmux-notify plugin uses this approach, polling every 10 seconds for `$`, `#`, or `%` suffixes. yoloAI polls every 2 seconds.

**tmux hooks (pane-exited, alert-bell, etc.):**
Event-driven callbacks. `pane-exited` is 100% reliable for process termination. `alert-bell` is reliable when agent emits bells. Available in tmux 2.4+.

### 2.2 Shell Integration Protocols (OSC 133 / 633)

The shell emits invisible escape sequences at key points:
- `OSC 133 ; A ST` — Prompt start
- `OSC 133 ; B ST` — Command start
- `OSC 133 ; C ST` — Command executed
- `OSC 133 ; D [; exitcode] ST` — Command finished

Very high reliability when configured. Supported by iTerm2, kitty, Ghostty, WezTerm, VSCode, Windows Terminal. However, this only works for **shells**, not arbitrary interactive programs. An AI agent would need to emit these sequences itself.

**Relevance:** Limited for yoloAI. Our agents are not shells — they're interactive TUI programs. Unless an agent adopted OSC 133 natively (unlikely), this doesn't help.

### 2.3 Process-Level Monitoring (Linux /proc)

**`/proc/PID/wchan`:** Shows kernel function where process is sleeping. When blocked on `read(0, ...)` (stdin), shows `wait_woken`, `n_tty_read`, or `ep_poll`. Varies across kernel versions. Programs using epoll show `ep_poll` regardless of whether they're waiting for user input or network I/O. **Linux only.**

**`/proc/PID/stat` CPU delta:** Sample utime+stime at intervals. Zero delta + S state = likely idle. False positives during network I/O (agent making API calls appears idle). **Linux only.**

**Process tree CPU monitoring:** Sum CPU across all child processes. Better than single-process, but same network I/O false-positive problem.

**strace (syscall tracing):** Watch for `read(0, ...)` — very high reliability but requires `CAP_SYS_PTRACE`, significant performance overhead. **Linux only.**

### 2.4 Container/cgroup Monitoring

**cgroup v2 `cpu.stat`:** `usage_usec` counter shows total CPU consumed by all processes in the container. Zero delta = no CPU activity. Same network I/O issue. **Linux only** (Docker Desktop on macOS runs Linux in a VM, so stats are accessible but add complexity).

**Docker health checks:** Scheduling mechanism, not detection mechanism. A health check script could check any condition.

### 2.5 What Other Tools Do

**Claude Squad** (most sophisticated in the ecosystem):
Multi-layer idle detection:
1. Output silence (no new terminal output for N seconds)
2. AI-powered check (sends terminal content to a small model to ask "is this agent waiting?")
3. Token stability (compare terminal snapshots; if unchanged, idle)
4. Circuit breaker (N consecutive idle checks → definitively idle)
5. Tunable sensitivity presets

This accepts that no single signal is reliable and layers heuristics. The AI-powered check is creative but adds cost and latency.

**Ralph** (autonomous loop wrapper):
Dual-condition exit: both completion indicators AND an explicit `EXIT_SIGNAL` must be present. Circuit breaker after 3 consecutive no-change loops.

**CCManager:**
Uses Claude Haiku to analyze prompts and determine if they need manual approval. Custom status-change commands for extensibility.

**OpenHands/OpenDevin:**
Agent explicitly emits `AgentFinishAction`. Reports ~60% accuracy for task completion prediction based on this signal.

**SWE-agent:**
Hard limits: `max_iterations`, `cost_limit`, timeout. Agent can explicitly submit. The limits are 100% reliable as safety nets.

**Rivet Sandbox Agent SDK:**
Runs inside the sandbox, wraps agents behind HTTP/SSE API. Parses JSONL stdout (Claude), JSON-RPC (Codex), HTTP+SSE (OpenCode). Status is implicit in the event stream.

### 2.6 Headless/Non-Interactive Mode

Claude Code's `--print` flag, Codex SDK's JSONL, etc. Process exits on completion. 100% reliable but eliminates interactivity.

### 2.7 File System Monitoring (inotify/FSEvents)

Monitor working directory for file changes. Activity = file changes, silence = idle. Low reliability alone — agents pause to think, build tools create noise. More useful as supplementary signal.

**Status file watching via inotify:** Watch `status.json` for changes instead of polling. Near-instant notification. Combines status file reliability with event-driven latency.

---

## Part 3: Analysis

### 3.1 What's Working

1. **Hook-based detection for Claude Code** is fundamentally sound. The Notification/PreToolUse hooks write directly to `status.json`, giving near-instant event-driven detection without polling fragility. This is the most reliable approach in the codebase.

2. **Process exit detection** via `#{pane_dead}` is 100% reliable and handles done/failed correctly.

3. **The `status.json` architecture** (bind-mounted file, host reads directly, no exec needed) is a good design. Fast, no container exec overhead, works across all backends.

### 3.2 What's Not Working

1. **Polling-based detection for non-Claude agents** is the primary pain point. `grep -qF` against `capture-pane` is fragile — false positives from pattern appearing in output, false negatives from scrolling. The 2-second poll adds latency.

2. **Three agents can never be detected as idle** (opencode, test, shell) because they have no ReadyPattern and no hooks.

3. **The bell mechanism is half-implemented** — configured in tmux and Claude Code but never wired into the status determination logic. This seems like a remnant of an earlier approach.

4. **Dead configuration** (`idle_threshold`) clutters the codebase and confuses the mental model.

### 3.3 Root Cause

The fundamental problem: **there is no universal, reliable way to detect when an arbitrary interactive TUI program is waiting for input from outside the process.** Every approach has tradeoffs:

| Approach | Reliability | Universality | Cooperation Needed |
|----------|------------|--------------|-------------------|
| Agent hooks (status file) | High | Low (agent-specific) | Yes |
| Ready pattern polling | Medium | Medium (needs known pattern) | Partial |
| Process exit | 100% | Universal | No |
| Bell monitoring | High | Low (agent must emit) | Yes |
| CPU quiescence | Medium | Universal | No |
| Output silence | Low-Medium | Universal | No |
| AI-powered analysis | Medium-High | Universal | No |

No single mechanism solves it. The ecosystem (Claude Squad, Ralph, etc.) has converged on **layered heuristics** as the pragmatic answer.

### 3.4 Key Questions for Discussion

**Q1: Is polling-based detection good enough for non-Claude agents?**
It works *most* of the time for agents with distinctive prompts (Gemini's "Type your message" is quite unique). The question is whether the failure cases matter enough to invest in alternatives.

**Q2: Should we add more detection layers?**
Options, roughly ordered by effort:
- **Wire up bell monitoring** — already configured, just needs the status logic. Near-instant for agents that emit BEL. Low effort.
- **Output stability** — compare consecutive `capture-pane` snapshots. If identical for N polls, likely idle. Already used for startup detection. Low-medium effort.
- **CPU quiescence** — read cgroup `cpu.stat`. Zero CPU delta for N seconds suggests idle. Medium effort, Linux-only.
- **AI-powered check** — send terminal content to a fast model. High reliability but adds cost/latency/dependency. High effort.

**Q3: Should agents without idle detection just not support it?**
For `test` and `shell` agents, idle detection arguably doesn't make sense — they're not AI agents. For `opencode`, this is a genuine gap. We could add a `ReadyPattern` if OpenCode has a distinctive prompt, or accept it as a known limitation.

**Q4: Should we move toward headless mode for prompt-based workflows?**
For `yoloai start --prompt "..."` one-shot usage, running agents in headless/print mode with process exit as the signal would be 100% reliable. The agent would still run inside tmux (for `attach`), but the outer process would exit when done. This is a larger architectural change.

**Q5: Should we clean up the dead code?**
The deprecated `idle_threshold`, unused bell detection wiring, and the exec fallback's inability to detect idle are all sources of confusion. Cleaning these up would simplify the mental model even if it doesn't fix detection.

**Q6: Should `resetStatusToRunning` be refactored?**
Currently only called for hook-based agents. For polling-based agents, the assumption is that removing the ready pattern from the screen will naturally cause the next poll to write "running". But this creates an asymmetry — hook agents get immediate status reset, polling agents have up to a 2-second delay.

### 3.5 What Claude Squad Gets Right

Claude Squad's approach is worth studying because it's the most battle-tested in the ecosystem. Key insights:

1. **No single signal is trusted alone.** Every signal is treated as a hint, not a fact.
2. **Circuit breaker pattern.** N consecutive idle signals → confident idle. Prevents flapping.
3. **The AI check is a last resort**, not the primary mechanism. It's expensive and slow but catches cases that heuristics miss.
4. **Tunable sensitivity** lets users trade latency for accuracy based on their workflow.

### 3.6 Potential Architecture: Layered Detection

A layered approach for yoloAI, ordered by priority:

```
Layer 1: Agent hooks (status.json write)
  → Instant, high reliability, Claude Code only
  → If HookIdle=true and status.json is fresh, trust it

Layer 2: Process exit (pane_dead)
  → Instant, 100% reliable, universal
  → Terminal state, always trust

Layer 3: Ready pattern polling (capture-pane + grep)
  → 2s latency, medium reliability
  → For agents with ReadyPattern and no hooks

Layer 4: Output stability (consecutive identical captures)
  → 4-6s latency (2-3 consecutive matches), medium reliability
  → Supplementary signal to increase confidence in pattern match

Layer 5: CPU quiescence (cgroup cpu.stat)
  → 2-4s latency, medium reliability, Linux only
  → Supplementary signal for agents without patterns
```

Layers 1-3 are what we have today (minus the staleness/fallback issues). Layers 4-5 would be new. The key insight is that layers 4 and 5 are **supplementary** — they increase confidence in layer 3's result or provide a signal when layer 3 has nothing to match.

Whether the added complexity is worth it depends on how much the current gaps actually hurt users in practice.

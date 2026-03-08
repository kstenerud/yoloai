# Idle Detection: Audit, Research, and Architecture

Comprehensive analysis of yoloAI's idle detection system — what we do today, what's broken, what alternatives exist, and the proposed pluggable architecture. Conducted 2026-03.

## Part 1: Current System Audit

### 1.1 Status Model

Seven statuses defined in `sandbox/inspect.go:18-30`:

| Status | Meaning |
|--------|---------|
| `active` | Container up, agent actively working |
| `idle` | Container up, agent waiting for input |
| `done` | Container up, agent exited cleanly (exit 0) |
| `failed` | Container up, agent exited with error (non-zero) |
| `stopped` | Container stopped via `docker stop` |
| `removed` | Container gone, sandbox dir remains |
| `broken` | Sandbox dir exists, `meta.json` missing/invalid |

### 1.2 Detection Architecture: Two Paths

**Primary path -- `status.json` file (no exec required):**

The status monitor inside the container writes JSON to `/yoloai/status.json`, which is bind-mounted to `~/.yoloai/sandboxes/<name>/status.json` on the host. The host reads it directly via `os.ReadFile()`.

```go
type statusJSON struct {
    Status    string `json:"status"`              // "active", "idle", "done"
    ExitCode  *int   `json:"exit_code,omitempty"`
    Timestamp int64  `json:"timestamp"`           // unix seconds
}
```

Trust rules in `parseStatusJSON` (`sandbox/inspect.go:203`):
- **"active"**: Only trusted if timestamp < 10 seconds old (`statusFileStaleness`). If stale, falls back to exec.
- **"idle"**: Trusted regardless of staleness. Rationale: idle is a persistent state written once and cleared only by `resetStatusToActive` or agent exit.
- **"done"**: Terminal state, always trusted. Uses `exit_code` to distinguish done (0) vs failed (non-zero). Missing exit_code defaults to 1.
- Empty/zero/unknown: Falls through to exec fallback.

**Fallback path -- exec-based tmux query:**

`detectStatusViaExec` (`sandbox/inspect.go:244-267`) runs:
```
tmux list-panes -t main -F '#{pane_dead}|#{pane_dead_status}'
```

Returns alive (`0|0`), done (`1|0`), or failed (`1|N`). If exec fails, defaults to `active`. **This fallback cannot detect idle** -- only active vs done/failed.

**Full resolution flow** (`DetectStatus`, line 174):
1. `rt.Inspect()` -- check container exists/is running
2. Not found -> `StatusRemoved`; not running -> `StatusStopped`
3. Try reading `status.json` from host filesystem
4. If valid and non-stale -> return parsed status
5. Otherwise -> exec fallback

### 1.3 Two Idle Detection Strategies

#### Hook-based (Claude Code only)

Claude Code is the **only** agent with `HookIdle: true` (`agent/agent.go:105`).

During creation, `injectIdleHook()` (`sandbox/create.go:1246-1275`) adds two hooks to Claude Code's `settings.json`:

1. **Notification hook**: On response completion, writes `{"status":"idle",...}` to `/yoloai/status.json`
2. **PreToolUse hook**: On tool use start, writes `{"status":"active",...}` to `/yoloai/status.json`

When `HOOK_IDLE=true` in the status monitor loop (`entrypoint-user.sh:188-209`), the monitor does NOT poll for idle. It only:
- Checks for pane death (writes "done" status)
- Reads `status.json` to update terminal title (`"> name"` for idle, `"name"` for active)

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

1. **status.json** -- primary, bind-mounted, read from host
2. **Terminal title** -- `"> name"` prefix when idle, set via tmux `set-titles-string "#W"`. Visual indicator only.
3. **Bell** -- Claude Code emits BEL via `preferredNotifChannel: terminal_bell`. Configured in tmux (`monitor-bell on`) but **not used for status determination**. Comment says "track for idle detection" but no code reads bell state.
4. **resetStatusToActive** (`sandbox/inspect.go:158-170`) -- host writes "active" to `status.json` when sending prompts to hook-based agents

### 1.6 Consumers of Idle Status

| Command | How it uses status |
|---------|--------------------|
| `yoloai ls` | Displays status column; `--idle` filter |
| `yoloai attach` | Allows attach when active/idle/done/failed |
| `yoloai exec` | Requires active or idle |
| `yoloai stop --all` | Stops active/idle/done/failed sandboxes |
| `yoloai diff` | Warns if active/idle that diff may be incomplete |
| `sandbox.Start()` | Returns "already running" if active/idle |
| `sandbox.Destroy()` | Requires confirmation if active/idle |
| `sandbox.Reset()` | Checks active/idle for `--no-restart` |

### 1.7 Known Issues

**1. `idle_threshold` config is wired up but unused.**
The `idle_threshold` field is actively parsed from YAML (`config/config.go`), propagated through profile resolution (`create_prepare.go`), and stored in meta.json (`meta.go`). Only the `DefaultIdleThreshold` constant is marked deprecated (`sandbox/inspect.go:32-35`). The field flows through the entire creation pipeline but is never read for any detection logic. Full removal scope documented in Phase 1.

**2. Agents without ReadyPattern can never be detected as idle.**
OpenCode, test, and shell agents have empty `ReadyPattern` and `HookIdle: false`. Once a prompt is sent, they show as "active" forever until the process exits.

**3. Polling-based detection is inherently fragile.**
`grep -qF "$READY_PATTERN"` against `tmux capture-pane -p` has multiple failure modes:
- **False positive**: Pattern appears in agent output (e.g., agent prints a `>` character)
- **False negative**: Pattern scrolled off visible terminal area
- **Buffer boundary**: `capture-pane` only captures visible content (~50 lines)

**4. Stale "active" + exec fallback = no idle detection.**
An "active" `status.json` older than 10 seconds triggers exec fallback. But exec can't detect idle -- only pane death. So if the status monitor dies, a non-hook agent stuck in idle will report as "active" forever.

**5. Race condition on prompt delivery.**
`sendResumePrompt`/`sendCustomPrompt` call `resetStatusToActive` on the host immediately after spawning a `nohup` background script, but the script waits up to 60 seconds for the agent to be ready before delivering the prompt. During this window, status says "active" but no prompt has been delivered yet. **Fix:** Move the status write into the background script — write `{"status":"active",...}` to `/yoloai/status.json` after successful prompt delivery, not on the host when spawning the script. Remove `resetStatusToActive` calls from `sendResumePrompt`/`sendCustomPrompt` in `lifecycle.go`.

**6. Claude Code hook reliability concerns.**
The `Notification` hook with `idle_prompt` matcher is widely reported as unreliable upstream (fires on every response, or doesn't fire at all in VS Code). yoloAI uses a plain `Notification` hook (not `idle_prompt`-specific), which fires on every completion -- this is correct for our use case but worth noting the upstream instability.

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
- `OSC 133 ; A ST` -- Prompt start
- `OSC 133 ; B ST` -- Command start
- `OSC 133 ; C ST` -- Command executed
- `OSC 133 ; D [; exitcode] ST` -- Command finished

Very high reliability when configured. Supported by iTerm2, kitty, Ghostty, WezTerm, VSCode, Windows Terminal. However, this only works for **shells**, not arbitrary interactive programs. An AI agent would need to emit these sequences itself.

**Relevance:** Limited for yoloAI. Our agents are not shells -- they're interactive TUI programs. Unless an agent adopted OSC 133 natively (unlikely), this doesn't help.

### 2.3 Process-Level Monitoring (Linux /proc)

**`/proc/PID/wchan` -- The Best Non-Cooperative Signal:**

Shows the kernel function where a process is sleeping. Key values:

| wchan value | Meaning |
|---|---|
| `n_tty_read` | Blocked reading from TTY/PTY (terminal input) -- **the target** |
| `wait_woken` | Generic wait, often seen during TTY reads on some kernels |
| `do_epoll_wait` | Blocked in epoll (event loop -- network I/O multiplexing) |
| `poll_schedule_timeout` | Blocked in poll/ppoll |
| `sk_wait_data` | Blocked waiting for network socket data |
| `do_wait` | Waiting for child process |
| `hrtimer_nanosleep` | In sleep/nanosleep |

Verified working on kernel 6.17.4. Readable for same-user processes (no root needed). Negligible overhead.

**Distinguishing "waiting for user input" from "waiting for network":**
1. `wchan == n_tty_read` -> waiting for terminal input (high confidence)
2. `wchan == do_epoll_wait` + no ESTABLISHED TCP connections -> idle event loop (medium confidence)
3. `wchan == do_epoll_wait` + ESTABLISHED TCP connections -> waiting for API response (working)

**`/proc/PID/syscall`:** Shows current syscall number and arguments. Can confirm `read(fd=0, ...)` (stdin read). But requires `PTRACE_MODE_ATTACH` -- blocked by default `ptrace_scope=1` on most distros. Not practical without `CAP_SYS_PTRACE` or root.

**`/proc/PID/fd/N` readlink:** Confirms what stdin points to (`/dev/pts/N` = PTY). Readable same-user, no ptrace needed.

**Docker:** Host can read `/proc/<host_pid>/wchan` directly. Get host PID via `docker inspect --format '{{.State.Pid}}'`. Same fidelity as native Linux, no container exec needed.

**macOS:** No reliable equivalent. `ps -o wchan` exists but shows truncated XNU function names that vary across versions. No `/proc` filesystem. `dtrace` requires SIP disabled. Best available is heuristic combining `ps -o wchan` + fd enumeration + network check. Significantly weaker than Linux.

### 2.4 What Other Tools Do

**Claude Squad** (most sophisticated in the ecosystem):
Multi-layer idle detection:
1. Output silence (no new terminal output for N seconds)
2. AI-powered check (sends terminal content to a small model to ask "is this agent waiting?")
3. Token stability (compare terminal snapshots; if unchanged, idle)
4. Circuit breaker (N consecutive idle checks -> definitively idle)
5. Tunable sensitivity presets

Accepts that no single signal is reliable and layers heuristics. The AI-powered check is creative but adds cost and latency.

**Ralph** (autonomous loop wrapper):
Dual-condition exit: both completion indicators AND an explicit `EXIT_SIGNAL`. Circuit breaker after 3 consecutive no-change loops.

**OpenHands/OpenDevin:**
Agent explicitly emits `AgentFinishAction`. ~60% accuracy.

**SWE-agent:**
Hard limits: `max_iterations`, `cost_limit`, timeout. 100% reliable as safety nets.

**Rivet Sandbox Agent SDK:**
Runs agents in headless mode with machine-readable output (`--output-format stream-json` for Claude Code, JSON-RPC for Codex). **Requires API keys -- won't work with subscriptions.** Different architecture from yoloAI (programmatic sidecar vs interactive TUI).

### 2.5 Context.md Signaling (New Idea)

Instruct agents via their context file (CLAUDE.md, GEMINI.md, etc.) to emit a specific marker when transitioning between working and idle states. For example:

```
When you complete a task or finish responding, print the marker: [YOLOAI:IDLE]
When you begin working on a new request, print the marker: [YOLOAI:WORKING]
```

**Properties:**
- Agent-agnostic: any agent that follows system instructions could do it
- Free: no API cost, no special agent support needed
- Unreliable alone: LLMs don't follow instructions 100%
- Works as supplementary heuristic alongside other signals
- Detectable via `tmux capture-pane` or `tmux pipe-pane` log

---

## Part 3: Architecture Proposal

### 3.1 Design Principles

1. **Pluggable detectors.** Each detection method is a self-contained unit. New methods can be added without changing the framework.
2. **Context-dependent.** Which detectors are active depends on the agent and the runtime backend. A detector declares its own applicability.
3. **Layered confidence.** Detectors produce signals, not final verdicts. The framework combines signals into a status determination.
4. **Graceful degradation.** When no detector can determine idle state, the system reports "active" (safe default) rather than guessing.
5. **status.json remains the IPC mechanism.** The in-container monitor writes it, the host reads it. This architecture is sound and shouldn't change.
6. **Test agent mocks idle transitions.** The test agent provides controllable idle/working signals for testing the framework.

### 3.2 Concepts

**Detector:** A named strategy that can determine whether an agent is idle. Runs inside the container's status monitor loop. Each detector:
- Has a unique name (e.g., `hook`, `ready_pattern`, `wchan`, `output_stability`, `context_signal`)
- Declares what it needs (agent config fields, platform capabilities)
- Returns one of: `idle`, `active`, or `unknown` on each poll
- Has a confidence level: `high`, `medium`, `low`

**Detector Stack:** The ordered list of detectors active for a given sandbox. Determined at creation time based on agent definition + runtime backend. Stored in `config.json` so the entrypoint knows what to run.

**Resolution Rule:** The framework evaluates detectors in priority order. The first detector that returns a non-`unknown` result wins, weighted by confidence:
- A single `high` confidence result is trusted immediately
- A single `medium` confidence result is trusted after N consecutive identical results (stability check)
- `low` confidence results are only used when no better signal exists, and require more consecutive matches

**Stability Counter:** For medium/low confidence detectors, the framework tracks consecutive identical results. This is the "circuit breaker" pattern — prevents flapping between idle and active on noisy signals.

### 3.3 Detector Catalog

#### `hook` -- Agent Hook Writes (High Confidence)

**How:** Agent's own hooks write directly to `status.json`. The monitor reads the file to update the title but doesn't poll for idle.

**Applies to:** Agents with `HookIdle: true` (currently Claude Code only).

**Platform:** All.

**Implementation:** Already exists. The current `HOOK_IDLE=true` path in `entrypoint-user.sh`.

**Resilience:** Unique among detectors — hooks write to `status.json` directly, bypassing the status monitor. If the Python monitor crashes, hook-based idle detection continues working (the host reads `status.json` regardless). The monitor's role for hook agents is purely cosmetic (terminal title updates).

#### `ready_pattern` -- Terminal Prompt Matching (Medium Confidence)

**How:** `tmux capture-pane -t main -p | grep -qF "$READY_PATTERN"`

**Applies to:** Agents with a non-empty `ReadyPattern`.

**Platform:** All (requires tmux).

**Implementation:** Already exists. The current polling path in `entrypoint-user.sh`.

**Improvement opportunity:** Use `grep -c` and check if the pattern appears on the **last non-empty line** only, reducing false positives from the pattern appearing in output mid-screen.

#### `wchan` -- Kernel Wait Channel (High Confidence)

**How:** Read `/proc/<pid>/wchan` for the agent's main process. `n_tty_read` or `wait_woken` = idle.

**Applies to:** All agents (the signal is process-level, not agent-specific).

**Platform:** Linux only (Docker, Seatbelt on Linux). Not available on macOS/Tart.

**Implementation:** New. The status monitor finds the agent PID (it's the process exec'd in the tmux pane) and reads its wchan every poll cycle. Distinguishes TTY read (idle) from epoll/network wait (working) and child process wait (working).

**Finding the agent PID:** After `exec $AGENT_COMMAND` in tmux, the agent IS the pane's process. `tmux list-panes -t main -F '#{pane_pid}'` returns it.

**Distinguishing network I/O:** When wchan is `do_epoll_wait` (event loop), check for active TCP connections via `/proc/<pid>/net/tcp` or `ss -tnp`. Active connections = waiting for API response = working.

**Key advantage:** Works for ALL agents, including those with no ReadyPattern and no hooks (opencode, test, shell). No agent cooperation needed.

**Caveat:** Node.js-based agents (Claude Code, Codex) use epoll for their event loop, so wchan will show `do_epoll_wait` even when idle. The network connection check becomes essential: `do_epoll_wait` + no TCP connections = idle event loop = agent is waiting for input.

#### `output_stability` -- Screen Content Stability (Low Confidence)

**How:** Compare consecutive `tmux capture-pane` snapshots. If identical for N consecutive polls, declare idle.

**Applies to:** All agents.

**Platform:** All.

**Implementation:** New, but the concept already exists in the startup readiness check. Track `PREV_CONTENT` and a stability counter.

**Parameters:** Stability threshold (number of identical polls required). Suggested: 3 consecutive matches at 2-second intervals = 6 seconds to detect idle.

**Weakness:** Long-thinking agents produce no output and would trigger false idle. Agents with animated spinners or progress bars never stabilize. Best used as a supplementary signal, not primary.

#### `context_signal` -- Agent-Emitted Markers (Medium Confidence)

**How:** Instruct the agent via its context file (CLAUDE.md, GEMINI.md) to print `[YOLOAI:IDLE]` when it finishes and `[YOLOAI:WORKING]` when it starts. Monitor via `tmux capture-pane` or the `pipe-pane` log file.

**Applies to:** Agents that have a `ContextFile` and follow instructions (claude, gemini, codex, aider — but reliability varies).

**Platform:** All.

**Implementation:** New.
- At sandbox creation, append idle signaling instructions to the agent's context file
- In the status monitor, check `tmux capture-pane` output (or tail the pipe-pane log) for the markers
- Using the log file (`/yoloai/log.txt`, already captured via `tmux pipe-pane`) avoids the visible-buffer limitation of `capture-pane`

**Key advantage:** Agent-agnostic. Any agent that reads its context file and follows instructions will emit the signal. Works even for agents where we don't know the prompt pattern.

**Key weakness:** LLMs are unreliable instruction followers. The agent may emit the marker at wrong times, or not at all. Must be combined with other signals.

**ANSI caveat:** The pipe-pane log captures raw terminal output including ANSI escape/color codes. The Python monitor must strip ANSI sequences before matching markers. The context file instructions should also explicitly say "print this exact text on its own line with no formatting."

#### `test_mock` -- Controllable Test Signals (High Confidence, Deferred)

**How:** The test agent writes `status.json` directly in response to specific commands, simulating idle/working transitions.

**Applies to:** Test agent only.

**Platform:** All.

**Implementation:** Deferred. The test agent is currently a plain bash shell and will remain unchanged for the idle detection rework. A full test harness that can mimic agent workflows and idle signal strategies is planned as a separate TODO (see `docs/dev/plans/TODO.md`). For now, the test agent has no idle detection -- it stays as "active" until the process exits.

### 3.4 Detector Selection Per Agent x Platform

The detector stack for a sandbox is determined at creation time. This table shows which detectors apply:

| Detector | claude (Linux) | claude (macOS) | gemini | codex | aider | opencode | test/shell |
|----------|---------------|----------------|--------|-------|-------|----------|------------|
| `hook` | **primary** | **primary** | - | - | - | - | - |
| `wchan` | supplementary | - | **primary** | **primary** | **primary** | **primary** | - |
| `ready_pattern` | - | - | supplementary | supplementary | supplementary | - | - |
| `context_signal` | - | - | supplementary | supplementary | supplementary | supplementary | - |
| `output_stability` | - | - | fallback | fallback | fallback | fallback | - |
| (none) | - | - | - | - | - | - | *n/a* |

Notes:
- **primary**: First detector checked. If it returns idle/active, that result is used.
- **supplementary**: Checked to increase confidence in the primary result, or used when primary returns `unknown`.
- **fallback**: Only used when primary and supplementary return `unknown`.
- Claude on macOS: hooks are primary, no wchan available. Falls back to ready_pattern if hooks fail.
- Test/shell agents: no idle detection. They stay as "active" until the process exits. A full test harness is planned separately (see `docs/dev/plans/TODO.md`).
- OpenCode on macOS: no hooks, no wchan, no ready pattern. Only context_signal and output_stability. This is a known limitation.
- **macOS idle detection is best-effort for non-Claude agents.** Without wchan, gemini/codex/aider fall back to ready_pattern + context_signal + output_stability — all medium/low confidence. A research round for macOS-specific detection alternatives is needed; if nothing better exists, accept and document the limitation.

### 3.5 Implementation Plan

#### Phase 1: Framework + Cleanup

1. **Remove `idle_threshold` plumbing:** The field is actively wired through the creation pipeline despite being unused. Full removal scope:
   - `sandbox/inspect.go`: `DefaultIdleThreshold` constant (marked deprecated)
   - `sandbox/meta.go`: `IdleThreshold` field in `Meta` struct
   - `config/config.go`: `IdleThreshold` field in `YoloaiConfig`, `idle_threshold` in `knownSettings`
   - `config/profile.go`: `idle_threshold` in profile config
   - `sandbox/create_prepare.go`: `idleThreshold` resolution and propagation
   - Remove unused bell-detection comments/config.

2. **Fix prompt delivery race condition.** Move `resetStatusToActive` from the host side (`lifecycle.go`) into the in-container background script. The script writes `{"status":"active","timestamp":...}` to `/yoloai/status.json` after successful prompt delivery, not before. Remove `resetStatusToActive` calls from `sendResumePrompt` and `sendCustomPrompt`.

3. **Declare agent idle capabilities.** Replace `HookIdle bool` and `ReadyPattern string` with an `IdleSupport` struct that describes what idle signals the agent can produce:

```go
// IdleSupport describes what idle detection signals an agent can produce.
// These are agent capabilities, not configuration — the framework decides
// which detectors to activate based on these capabilities plus the platform.
type IdleSupport struct {
    // Hook: agent has a native hook system that yoloAI can wire up to
    // write status.json on state transitions. Requires agent-specific
    // setup code in sandbox/create.go.
    Hook bool

    // ReadyPattern: terminal prompt text visible when agent is waiting
    // for input. Used by the ready_pattern detector.
    ReadyPattern string

    // ContextSignal: agent reads a context file where we can inject
    // instructions to emit idle/working markers.
    ContextSignal bool

    // WchanApplicable: wchan-based detection is meaningful for this agent.
    // False for test/shell where the process is always waiting on stdin.
    WchanApplicable bool
}
```

The framework resolves which detectors to run at creation time via a function like `resolveDetectors(idle agent.IdleSupport, backend string) []Detector`, separating capability declaration from detector selection.

4. **Compute detector stack at creation time.** Based on `IdleSupport` + runtime backend, determine which detectors to activate. Store in `config.json` as a list:

```json
{
    "detectors": ["hook"],
    ...
}
```

or for a non-hook agent on Linux:

```json
{
    "detectors": ["wchan", "ready_pattern", "context_signal"],
    ...
}
```

5. **Refactor the status monitor loop.** Replace the current `if HOOK_IDLE ... else ...` with a detector loop that evaluates each configured detector in order.

#### Phase 2: wchan Detector

1. **Implement wchan reading in the Python status monitor.** After getting the agent PID via `tmux list-panes -t main -F '#{pane_pid}'`:

```python
def read_wchan(pid: int) -> str:
    try:
        return Path(f"/proc/{pid}/wchan").read_text().strip()
    except OSError:
        return "unknown"

wchan = read_wchan(agent_pid)
if wchan in ("n_tty_read", "wait_woken"):
    status = "idle"       # blocked on terminal read
elif wchan in ("do_epoll_wait", "poll_schedule_timeout"):
    # event loop — check for network activity
    if has_active_connections(agent_pid):
        status = "active"  # waiting for API
    else:
        status = "idle"    # idle event loop
elif wchan == "do_wait":
    status = "active"      # waiting for child process
else:
    status = "unknown"
```

2. **Network connection check.** Parse `/proc/<pid>/net/tcp6` directly in Python (no `ss` or `iproute2` dependency). Each line has connection state at a fixed offset — `01` = ESTABLISHED. For WebSocket-aware detection, track per-connection `tx_queue:rx_queue` byte counter deltas between poll cycles (zero delta = idle persistent connection). On macOS, fall back to `nettop -p <pid> -J bytes_in,bytes_out -l 1`.

3. **Network namespace approach for process trees.** Rather than walking child processes, read `/proc/<pid>/net/tcp6` which covers ALL processes in the container's network namespace. If any process in the container has active TCP connections with traffic, the agent is working. Simpler and more complete than recursive process tree traversal.

4. **Platform guard.** Only activate wchan detector on Linux. The Python monitor checks `os.path.exists("/proc/1/wchan")` at startup.

#### Phase 3: Context Signal Detector

1. **Inject signaling instructions.** During sandbox creation, when building the agent's context file content, append:

```
## Status Signaling
When you finish responding and are waiting for the next prompt, print this exact line:
[YOLOAI:IDLE]
When you begin working on a new task, print this exact line:
[YOLOAI:WORKING]
```

2. **Monitor the pipe-pane log.** The entrypoint already runs `tmux pipe-pane -t main "cat >> /yoloai/log.txt"`. The detector can `tail` this file for markers, avoiding the visible-buffer limitation of `capture-pane`.

3. **Debounce.** The agent might emit markers in unexpected places. Require the marker to appear after the most recent output burst (i.e., no output for 1 second after the marker).

#### Phase 4: Output Stability

1. **Output stability detector.** Track consecutive identical `capture-pane` snapshots. After N matches (configurable, default 3), report idle. Reset counter on any content change.

Note: Test agent harness is a separate future TODO, not part of this rework.

### 3.6 Resolution Algorithm

The status monitor loop runs every 2 seconds. On each tick:

```
1. Check pane_dead -> done/failed (always, all agents)
2. For each detector in order:
   a. Query the detector
   b. If result is "idle" or "active":
      - If confidence is HIGH: use result immediately
      - If confidence is MEDIUM: increment stability counter
        - If counter >= 2: use result
        - Otherwise: continue to next detector
      - If confidence is LOW: increment stability counter
        - If counter >= 3: use result
        - Otherwise: continue to next detector
   c. If result is "unknown": continue to next detector
3. If no detector returned a usable result: status = "active" (safe default)
4. Write status to status.json
5. Update terminal title
```

The stability counters are per-detector and reset when the detector's result changes. This prevents flapping: a single false-positive from `ready_pattern` won't flip the status, but 2 consecutive matches will.

### 3.7 Where Code Changes

| Component | Changes |
|-----------|---------|
| `agent/agent.go` | Replace `HookIdle bool` + `ReadyPattern string` with `IdleSupport` struct |
| `sandbox/create.go` | Compute detector stack at creation time, write to `config.json` |
| `sandbox/inspect.go` | Remove `DefaultIdleThreshold`. The `parseStatusJSON` and `DetectStatus` functions don't change -- they read `status.json` regardless of how it was written |
| `sandbox/meta.go` | Remove `IdleThreshold` field |
| `config/config.go` | Remove `idle_threshold` key |
| `config/profile.go` | Remove `idle_threshold` from profile config |
| `sandbox/create_prepare.go` | Remove `idle_threshold` handling |
| `entrypoint-user.sh` | Remove bash status monitor loop, launch Python status monitor instead |
| (new) `status-monitor.py` | Python status monitor implementing the detector framework |
| `runtime/tart/resources/setup.sh` | Same detector framework changes |
| `runtime/seatbelt/resources/entrypoint.sh` | Same detector framework changes |

### 3.8 What Doesn't Change

- **status.json format and semantics.** The IPC mechanism is sound. Detectors write to it, host reads it.
- **DetectStatus() on the host side.** It reads status.json and falls back to exec. This doesn't need to know about detectors.
- **Terminal title convention.** `"> name"` for idle, `"name"` for active.
- **resetStatusToActive() concept.** Status reset when delivering prompts. Implementation moves from host-side (`lifecycle.go`) to in-container background script to fix the prompt delivery race (see Phase 1 step 2).
- **All consumer commands** (list, attach, exec, stop, diff, etc.) -- they read status, not detect it.

### 3.9 Open Questions

~~**Q1: Should detector config be user-overridable?**~~ **Resolved:** Yes, via config file only (profile-level `detectors` list). No CLI flag. Users can override the auto-resolved detector stack in their profile's `config.yaml` to disable noisy detectors or change the order. Not in phase 1 — TODO.

~~**Q2: Should the entrypoint be rewritten in a compiled language?**~~ **Resolved:** Rewrite the status monitor in Python from the start. Python 3 is already in the base image — no new dependency or build step. Far more maintainable than bash for the detector framework (ordered evaluation, stability counters, `/proc` parsing, network checks, process tree walking). No compiled language needed — avoids cross-compilation and build complexity for a long-running in-container process where startup time is irrelevant. **macOS caveat (Seatbelt backend):** macOS doesn't ship Python 3 pre-installed since Monterey 12.3. It's available via Xcode Command Line Tools (practically universal for macOS developers). The monitor should check for Python 3 at startup and error with a clear message ("Python 3 required — install Xcode Command Line Tools: `xcode-select --install`") if missing.

~~**Q3: How to handle the node.js epoll problem?**~~ **Resolved:** The stability counter handles it. The post-API-response window (no TCP connections, agent about to start working) is milliseconds long. Even the medium-confidence stability check requires 2 consecutive matches at 2-second intervals — a momentary false idle blip won't survive the debounce. No special handling needed.

~~**Q4: What about agents that use WebSocket connections?**~~ **Resolved:** Monitor TCP traffic volume by tracking byte counter deltas between poll cycles — zero delta on a persistent connection = idle. Cross-platform: Linux parses `/proc/<pid>/net/tcp6` directly in Python (per-connection `tx_queue:rx_queue` counters, no external dependencies); macOS uses `nettop -p <pid> -J bytes_in,bytes_out -l 1` (per-process byte counters). The Python status monitor abstracts this behind a platform check.

~~**Q5: Should context_signal use the pipe-pane log or capture-pane?**~~ **Resolved:** Use the pipe-pane log (`/yoloai/log.txt`). The Python status monitor opens the file, seeks to end at startup, and reads new lines each poll cycle — checking for `[YOLOAI:IDLE]` / `[YOLOAI:WORKING]` markers. Essentially `tail -f | grep` in-process, no subprocess needed. Beats `capture-pane` because the log captures all output regardless of terminal scrollback — markers can't scroll off the visible buffer.

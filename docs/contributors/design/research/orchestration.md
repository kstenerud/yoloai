# Agent Orchestration, Idle Detection, and Handoffs

Research on how the ecosystem detects agent completion/idle state, orchestrates multi-agent workflows, and interfaces with coding agent CLIs. Conducted 2026-03.

**Sources:** Listed inline. Key repos cloned to `/yoloai/files/` for examination where noted.

## 1. Agent Completion/Idle Detection Mechanisms

### 1.1 Claude Code

Claude Code provides the richest set of signals, but none is fully reliable for external orchestrators.

**Hooks system (14+ events):**
- `Stop` hook — fires when agent finishes responding. Can force continuation by returning `{ "decision": "block" }`. Most reliable signal for "agent finished a turn."
- `Notification` hook with `idle_prompt` matcher — intended to fire when agent is waiting for user input. **Widely reported as unreliable:** fires after every response in some environments, doesn't fire at all in VS Code. Issues: [#12048](https://github.com/anthropics/claude-code/issues/12048), [#16975](https://github.com/anthropics/claude-code/issues/16975), [#8320](https://github.com/anthropics/claude-code/issues/8320), [#29928](https://github.com/anthropics/claude-code/issues/29928).
- `TeammateIdle` hook — for official Agent Teams feature (experimental, behind feature flag).

**Terminal bell:** Claude Code emits `\a` (BEL) on completion when `preferredNotifChannel` is set to `terminal_bell`. With tmux `monitor-bell on`, this sets `window_bell_flag`. This is the mechanism used by the [parallel agents blog post](https://schipper.ai/posts/parallel-coding-agents/) and by yoloAI's current `DetectStatus()`.

**Known limitation:** The bell flag clears when the tmux window is selected (attached). Also, not all agents emit bells. The bell approach works only for agents that explicitly emit BEL characters.

**`#{pane_last_activity}` is not a valid tmux format variable.** This was yoloAI's original approach and is confirmed broken — tmux silently outputs empty string. There is no tmux variable that tracks "last time output appeared in a pane." The closest alternative is `#{pane_current_command}` (shows the currently running process) or monitoring pane content directly.

**Headless mode (`-p`):** Claude Code's `--print` flag runs in headless mode with structured JSON output to stdout and clean exit codes (0 = success). This is the cleanest programmatic interface but only supports one-shot tasks, not interactive sessions.

**Claude Agent SDK:** The `@anthropic-ai/claude-agent-sdk` npm package provides an in-process API that runs the same agent loop as the CLI. Returns an `AsyncGenerator` of typed events (text, tool calls, results, done). This is what `one-agent-sdk` wraps. Not applicable for subprocess/container-based orchestration.

### 1.2 Codex CLI

Two mechanisms:
- `notify` config — runs an external program on `agent-turn-complete` events. The event payload includes `last-assistant-message` as structured JSON. This is the most composable approach.
- `tui.notification_method` — supports `auto`, `osc9`, or `bel` for terminal notifications.
- The `@openai/codex-sdk` npm package communicates via JSONL events over stdin/stdout. Events include `item.completed` (subtypes: `agent_message`, `mcp_tool_call`, `command_execution`, `file_change`, `error`) and `turn.completed`/`turn.failed`.

### 1.3 Aider

Simplest system: `--notifications` flag uses platform-specific desktop notifications (`notify-send` on Linux, `terminal-notifier` on macOS). `--notifications-command` allows custom command. No hooks, no bell, no structured events.

### 1.4 Summary of Detection Approaches

| Mechanism | Reliability | Applicability | Notes |
|-----------|-------------|---------------|-------|
| Process exit code | High | One-shot only | Headless mode (`-p`) for Claude, standard for Codex |
| Terminal bell (`\a`) | Medium | Bell-emitting agents | Clears on window focus; not all agents emit |
| Hook system (Claude) | Medium | Claude only | `idle_prompt` notification buggy; `Stop` hook more reliable |
| Notify command (Codex) | High | Codex only | Clean structured JSON payload |
| Pane content monitoring | Medium | Universal | Requires regex matching on terminal output; fragile |
| SDK async generator | High | In-process only | Not applicable across container boundary |

**Key finding:** There is no universal, reliable mechanism for detecting agent idle state across a container boundary. The best approaches are agent-specific.

## 2. Orchestration Tool Ecosystem

The space has exploded in early 2026. An [awesome-agent-orchestrators](https://github.com/andyrewlee/awesome-agent-orchestrators) list catalogs 60+ tools. Key categories:

### 2.1 tmux-Based Session Managers

**Claude Squad** ([smtg-ai/claude-squad](https://github.com/smtg-ai/claude-squad)) — the most popular open-source orchestrator. Uses tmux sessions + git worktrees. Notably has **multi-layer idle detection:**
1. Output silence (no new terminal output for N seconds)
2. AI-powered check (sends terminal content to a small model to determine if agent is waiting)
3. Token stability (compares terminal content snapshots; if unchanged, considers idle)
4. Circuit breaker (after N consecutive idle checks, marks as definitively idle)
5. Tunable presets for different sensitivity levels

This is the most sophisticated idle detection in the ecosystem and worth studying. It accepts that no single signal is reliable and layers multiple heuristics.

**claude-tmux** — tmux-based session lifecycle management with worktree support. Uses tmux hooks for state detection.

**muxtree** ([raine/workmux](https://github.com/raine/workmux)) — couples worktrees with tmux windows. Handles the full lifecycle: create worktree, open tmux window, work, merge, delete worktree, close window.

### 2.2 GUI Orchestrators

**Scape** ([scape.work](https://www.scape.work/)) — macOS-native app, launched 2026-03-05. Closed-source. Built on top of Claude Code + git worktrees.
- One-click worktree creation with Claude session auto-launch
- "Orchestrators" that auto-answer questions and auto-approve tool use based on custom rules
- Relies on iTerm2 scripting API for terminal monitoring and input injection
- Free tier: session monitoring, basic orchestrator. Pro ($9.99/mo): custom scripts, unlimited orchestrators
- No public code to examine. Repo ([fliptables/scape-releases](https://github.com/fliptables/scape-releases)) contains only a DMG release artifact.

**Crystal** — Desktop app for orchestrating, monitoring, and interacting with Claude Code agents.

**Context Manager** ([contextmanager.cc](https://contextmanager.cc/)) — macOS menubar app. Token usage and cost tracking.

**constellagent** — macOS app: per-agent terminal, editor, and git worktree.

### 2.3 Farm/Swarm Orchestrators

**Claude Code Agent Farm** ([Dicklesworthstone/claude_code_agent_farm](https://github.com/Dicklesworthstone/claude_code_agent_farm)) — runs 20+ Claude Code agents with:
- Lock-based coordination (file locks prevent conflicting edits)
- tmux dashboard for monitoring
- Adaptive idle timeout (adjusts threshold based on agent behavior)
- Auto-restart on failure

**ccswarm** ([nwiizo/ccswarm](https://github.com/nwiizo/ccswarm)) — multi-agent orchestration with Claude Code via worktree isolation.

**agent-orchestrator** (Composio) ([ComposioHQ/agent-orchestrator](https://github.com/ComposioHQ/agent-orchestrator)) — manages fleets of AI coding agents with worktrees, branches, and PRs.

### 2.4 Autonomous Loop Wrappers

**Ralph** ([frankbria/ralph-claude-code](https://github.com/frankbria/ralph-claude-code)) — autonomous Claude Code loop with dual-condition exit:
1. Both completion indicators AND an explicit `EXIT_SIGNAL` must be present
2. Circuit breaker opens after 3 consecutive no-change loops
3. Rate limit handling with exponential backoff

**CCManager** ([kbwo/ccmanager](https://github.com/kbwo/ccmanager)) — self-contained (no tmux dependency). Supports 8+ agent CLIs. Uses Claude Haiku to analyze prompts and determine if they need manual approval. Custom status-change commands for extensibility.

### 2.5 Official Anthropic: Agent Teams

[Agent Teams docs](https://code.claude.com/docs/en/agent-teams) — experimental, behind feature flag. Uses `TeammateTool` for inter-agent messaging. Agents can send tasks to teammates and receive results. Built into Claude Code itself. `TeammateIdle` hook for monitoring.

### 2.6 Claude Code Built-In Worktree Support

Claude Code added native `--worktree` (`-w`) flag. Creates a git worktree and runs the session scoped to it. Announced by Boris Cherny. This is the baseline that GUI tools like Scape wrap with additional UX.

## 3. Agent SDK Interfaces

### 3.1 one-agent-sdk

**Repo:** [odysa/one-agent-sdk](https://github.com/odysa/one-agent-sdk) (cloned to `/yoloai/files/one-agent-sdk/`)
**Created:** 2026-03-04 (1 day before this research). v0.1.2, 1 star, TypeScript.

A thin TypeScript adapter over three vendor agent SDKs:
- Claude Code via `@anthropic-ai/claude-agent-sdk`
- Codex via `@openai/codex-sdk`
- Kimi via `@moonshot-ai/kimi-agent-sdk`

**Architecture:** Provider pattern with dynamic imports. Each provider wraps a vendor SDK and normalizes output to a `StreamChunk` discriminated union:

```typescript
type StreamChunk =
  | { type: "text"; text: string }
  | { type: "tool_call"; toolName: string; toolArgs: Record<string, unknown>; toolCallId: string }
  | { type: "tool_result"; toolCallId: string; result: string }
  | { type: "handoff"; fromAgent: string; toAgent: string }
  | { type: "error"; error: string }
  | { type: "done"; text?: string; usage?: { inputTokens: number; outputTokens: number } };
```

**Key patterns:**
- **No state detection.** State is implicit in the async generator: yielding chunks = running, `done` chunk = finished, `error` chunk = problem. No idle concept.
- **Handoffs via `transfer_to_{name}` tools.** For Claude, delegates to SDK's built-in multi-agent support. For Kimi, creates synthetic tools that trigger agent swaps. Codex has no handoff support.
- **MCP server for custom tools.** Claude provider registers user-defined tools via an in-process MCP server with naming convention `mcp__{serverName}__{toolName}`.
- **Full permission bypass.** All providers run with `permissionMode: "bypassPermissions"` / `approvalPolicy: "never"`.

**Relevance to yoloAI:** This operates at a fundamentally different layer (in-process SDK wrapping vs. container-based lifecycle management). The `StreamChunk` normalized event format is a good design pattern if yoloAI ever needs a unified agent event protocol. The `transfer_to_{name}` handoff convention is interesting but only works within in-process multi-agent systems.

### 3.2 Vendor Agent SDKs

| SDK | Communication | Process Model |
|-----|---------------|---------------|
| `@anthropic-ai/claude-agent-sdk` | In-process function calls | Same process (runs agent loop directly) |
| `@openai/codex-sdk` | JSONL over stdin/stdout | Spawns Codex CLI as subprocess |
| `@moonshot-ai/kimi-agent-sdk` | In-process (wraps Kimi CLI) | Spawns subprocess internally |

None of these SDKs are useful for container-based orchestration where the agent runs inside a sandbox and the orchestrator runs outside. They're designed for building applications that embed agents, not for managing agents across process/container boundaries.

## 4. Git Worktree Patterns

Git worktrees are the dominant isolation model in the orchestrator ecosystem. Every major tool uses them.

**The pattern:** `git worktree add ../worktree-name -b branch-name` creates a new checkout sharing the same `.git` directory. Each agent gets its own worktree, so parallel agents can't conflict at the filesystem level. After work completes, the worktree's branch is merged (or PR'd) and the worktree is removed.

**Key tools:**
- **workmux** ([raine/workmux](https://github.com/raine/workmux)) — full lifecycle: create worktree + tmux window, work, merge, delete worktree + close window
- **Worktrunk** ([max-sixty/worktrunk](https://github.com/max-sixty/worktrunk)) — worktrees addressed by branch name, shell integration
- **agent-worktree** ([nekocode/agent-worktree](https://github.com/nekocode/agent-worktree)) — purpose-built for AI agents
- **WorkTreeFlow** ([Timon33/WorkTreeFlow](https://github.com/Timon33/WorkTreeFlow)) — workflow automation around worktrees

**Consensus from community** ([LLM Codegen go Brrr](https://dev.to/skeptrune/llm-codegen-go-brrr-parallelization-with-git-worktrees-and-tmux-2gop), widely discussed on [HN](https://news.ycombinator.com/item?id=44116872)): with agents costing $0.10-$0.40 per task, running 5-10 in parallel via worktrees is economically obvious. The pain is in lifecycle management — creation is easy, merge + delete + cleanup is the hard part.

**Relationship to yoloAI:** yoloAI's `:copy` mode serves a similar isolation purpose but with stronger guarantees (container-level isolation, not just filesystem). The copy/diff/apply workflow is more explicit about change review than worktree merge. The tradeoff is setup cost (full directory copy) vs. instant worktree creation.

## 5. User Pain Points and Wishlists

From Reddit (r/ClaudeAI, r/LocalLLaMA), HN, GitHub issues, and blogs:

### 5.1 Idle Detection Pain

The #1 orchestration pain point. Representative quotes:
- "I have 6 agents running and I have no idea which ones are waiting for me" (HN)
- "The idle notification fires on every response, making it useless" (GitHub #12048)
- "In VS Code terminal, the notification hook never fires" (GitHub #16975)
- "I just visually scan tmux tabs for the bell highlight, it's janky but it works" (parallel agents blog)

The Claude Squad approach (multi-layer heuristics) is the most robust solution found, but it's complex and agent-specific.

### 5.2 The Babysitting Tax

Permission prompts require unpredictable human attention. Auto-approve is dangerous (data loss incidents reported). No good middle ground exists in the current ecosystem — which is exactly yoloAI's value proposition (sandbox provides safety, agent operates freely within it).

### 5.3 Usage Limits

$200/month Claude Pro plan's weekly token caps are the #1 Reddit complaint. Running multiple agents multiplies the problem. Users want cost tracking per sandbox/task.

### 5.4 Worktree Lifecycle

Creation is solved. The hard part is: when an agent finishes, merge the branch, delete the worktree, and clean up the tmux session. Most tools handle creation but leave cleanup manual.

### 5.5 What Users Wish Existed

- Reliable "agent is done" signal that works across all agents and environments
- Dashboard showing all agent states, progress, and costs
- Auto-merge when agent succeeds + tests pass
- Notification system that doesn't cry wolf (only notify on genuine need for human input)

## 6. Implications for yoloAI

### 6.1 Idle Detection: Pragmatic Options

Given that no universal mechanism exists, yoloAI should consider a layered approach:

1. **Process exit** (universal, high reliability): If the pane's process has exited (`pane_dead == 1`), status is definitively done/failed based on exit code. This already works.

2. **Bell flag** (medium reliability, agent-specific): Keep `window_bell_flag` as an idle signal for agents that emit bells. This is what we have now. Works for Claude Code with correct config.

3. **Pane content matching** (universal, medium reliability): Use `tmux capture-pane` to grab the last few lines of terminal output. Match against known agent prompt patterns (e.g., Claude's `> ` prompt, Codex's prompt). If the prompt is visible and hasn't changed, the agent is idle. This is what Claude Squad does with its "token stability" check.

4. **Hook-based signaling** (high reliability, Claude-specific): Configure a Claude Code hook that writes a status file (e.g., `~/.yoloai/sandboxes/<name>/agent-status`) on `Stop` events. yoloAI polls or watches this file. The hook config would be injected into the sandbox's `~/.claude/settings.json`.

5. **Headless mode exit** (high reliability, one-shot only): For `--prompt` workflows, use `-p` mode where the agent process exits on completion. `pane_dead` is the signal. This would be the most reliable approach for non-interactive sandbox runs.

Options 4 and 5 are worth implementing. Option 3 is a reasonable fallback for agents without hook support.

### 6.2 Don't Build an Orchestrator

The orchestrator space is crowded (60+ tools) and rapidly evolving. yoloAI's value is in the sandbox layer, not orchestration. Per the design principle "don't reinvent the wheel," yoloAI should:
- Provide clean, reliable status in `yoloai ls` (the signals discussed above)
- Expose status via machine-readable output (`--json` flag) so orchestrators can consume it
- Compose well with Claude Squad, workmux, and other orchestrators

### 6.3 Worktrees vs. Copies

The ecosystem has standardized on git worktrees for parallel agent isolation. yoloAI's copy mode is heavier but provides stronger isolation (container + filesystem copy vs. just filesystem). The overlay mode bridges this gap (instant setup like worktrees, with container isolation). For users who want worktree-like speed with yoloAI's safety, overlay mode is the answer.

### 6.4 Agent SDK Wrapping Is a Different Layer

Tools like one-agent-sdk operate inside the agent process (in-process SDK calls). yoloAI operates outside (container lifecycle). These are complementary, not competitive. yoloAI should not try to provide an in-process agent API.

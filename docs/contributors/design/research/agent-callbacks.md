<!-- ABOUTME: Verified survey of native turn-finished/stop/idle callbacks across the four non-Claude shipped agents. -->
<!-- ABOUTME: Gates the detection-strategy design: which agents supply a native completion callback vs fall to the heuristic default. -->

# Agent Turn-Completion Callbacks (Codex, Gemini, OpenCode, Aider)

Research conducted **2026-06-25** against official docs and repos. yoloAI detects "the agent finished its turn and is waiting for input" so it can write an authoritative status file. For Claude Code this is wired to the native `Stop` hook; for every other shipped agent yoloAI currently falls back to a heuristic detector stack (ready-prompt pattern, process `wchan`, output-stability). This file establishes, per agent, whether a **native turn-completion callback** exists that yoloAI could subscribe to instead — feeding the design of detection as a pluggable strategy (default = heuristic; agents with a native callback supply their own).

**Headline:** all four agents expose *some* completion signal, but they differ sharply in fitness. **Gemini** (`AfterAgent` hook) and **OpenCode** (`session.idle` event / plugin hook) offer first-class structured callbacks comparable to Claude's `Stop`. **Codex** offers a clean structured `notify` program (turn-complete with JSON payload). **Aider**'s `--notifications-command` fires on turn-completion but passes **no data** (bare command, fire-and-forget) — usable as a binary "turn done" pulse, not a rich callback.

## Comparison table

| Agent | Hooks/events system? | Turn-finished callback? | Shape / config location | Resume command | Sources |
|-------|----------------------|-------------------------|--------------------------|----------------|---------|
| **Codex CLI** | Yes — `notify` program + JSONL/SDK events + `tui.notification_method` | **Yes** — `agent-turn-complete` (rich JSON) | `notify` array in `~/.codex/config.toml` runs an external program; arg is JSON incl. `type: "agent-turn-complete"` + `last-assistant-message` | `codex resume` / `--last` / session id | [config.toml docs](https://github.com/openai/codex/blob/main/docs/config.md) |
| **Gemini CLI** | Yes — lifecycle hooks (GA, v0.26.0+) | **Yes** — `AfterAgent` (fires once/turn after final response) | `hooks.AfterAgent` in `~/.gemini/settings.json`; command-type hook, JSON via stdin (incl. `prompt_response`, `stop_hook_active`) | `gemini --resume [idx\|uuid]`, `/rewind`, `--checkpointing` | [hooks ref (repo)](https://github.com/google-gemini/gemini-cli/blob/main/docs/hooks/reference.md), [geminicli.com/docs/hooks](https://geminicli.com/docs/hooks/reference/) |
| **OpenCode** | Yes — server SSE event bus **and** plugin `event` hook | **Yes** — `session.idle` event | SSE `GET /event` (`opencode serve`) **or** plugin `event` hook (`~/.config/opencode/plugin/`) filtering `event.type === "session.idle"` | `opencode --continue` / `--session <id>` (`--fork`) | [server docs](https://opencode.ai/docs/server/), [plugins docs](https://opencode.ai/docs/plugins/) |
| **Aider** | No general hook system; only `--notifications-command` | **Partial** — fires on turn-complete but passes **no data** | `--notifications-command "CMD"` / `notifications_command:` in `.aider.conf.yml` / `AIDER_NOTIFICATIONS_COMMAND`; runs bare via `shell=True`, fire-and-forget | `--restore-chat-history` (binary toggle); `/save`+`/load` | [notifications docs](https://aider.chat/docs/usage/notifications.html), [io.py](https://github.com/Aider-AI/aider/blob/main/aider/io.py) |

## Codex CLI — VERIFIED (callback present, rich)

Codex CLI has three relevant signaling paths:

1. **`notify` config program (the load-bearing one).** A `notify` key in `~/.codex/config.toml` (an array specifying an external program, e.g. `notify = ["python3", "/path/notify.py"]`) is invoked by Codex on events. The program receives a JSON argument whose `type` field includes **`agent-turn-complete`**, and the payload carries `last-assistant-message` (the final assistant text) plus turn/session context. This is a clean, structured "turn finished, waiting for input" callback — the closest analog to Claude's `Stop` hook among the non-hook agents. Source: [Codex config docs](https://github.com/openai/codex/blob/main/docs/config.md).
2. **`tui.notification_method`** — `auto` / `osc9` / `bel` terminal-notification escape sequences (cosmetic; a BEL-style signal, not structured).
3. **`@openai/codex-sdk` JSONL events** — over stdin/stdout when driven programmatically, including `item.completed` (subtypes `agent_message`, `mcp_tool_call`, `command_execution`, `file_change`, `error`) and `turn.completed` / `turn.failed`. Applies only when Codex is embedded via the SDK, not the interactive `codex` TUI.

**Documentation/stability:** officially documented in the repo's `docs/config.md`; the `notify` + `agent-turn-complete` mechanism corroborates the prior 2026-03 finding in [orchestration.md §1.2](orchestration.md). Stable, official.

**Resume (bonus):** `codex resume` (resume last/selected session), with `--last` and session-id selection. VERIFIED-as-documented; exact flag spelling should be re-checked against the installed version.

## Gemini CLI — VERIFIED (callback present, GA)

Gemini CLI gained a **lifecycle hooks system** (command-type hooks configured in `settings.json` under a `hooks` key; user-level `~/.gemini/settings.json` or project `.gemini/settings.json`). Hooks receive JSON on stdin and may return JSON on stdout — deliberately close to Claude Code's design.

**Turn-completion hook = `AfterAgent`** (DOUBLE-VERIFIED against both the repo docs and geminicli.com): *"Fires once per turn after the model generates its final response."* Its stdin JSON includes `prompt`, `prompt_response` (the agent's final text), `stop_hook_active`, plus base fields (`session_id`, `transcript_path`, `cwd`, `hook_event_name`, `timestamp`). Primary documented use is response validation / automatic retries — exactly the "agent done with a turn" boundary yoloAI needs.

> **Naming correction (important for implementation):** Gemini does **NOT** have a hook named `Stop` (nor `PreToolUse`/`PostToolUse`/`SubagentStop`). One research pass hallucinated Claude-Code-identical names; the official reference lists `SessionStart`, `SessionEnd`, `Notification`, `PreCompress`, plus the agent/model/tool family (`BeforeAgent`/**`AfterAgent`**, `BeforeModel`/`AfterModel`, `BeforeToolSelection`, `BeforeTool`/`AfterTool`). Wire to **`AfterAgent`**, not `Stop`. There is no separate "now waiting for user input" event — `AfterAgent` is the boundary, and control returns to the prompt immediately after it completes.

**Documentation/stability:** GA (not flagged experimental). Hooks introduced in **v0.26.0**; current line v0.45.x as of research date. Comprehensively documented at geminicli.com/docs/hooks and in the repo `docs/hooks/`. Sources: [repo hooks reference](https://github.com/google-gemini/gemini-cli/blob/main/docs/hooks/reference.md), [geminicli.com/docs/hooks/reference](https://geminicli.com/docs/hooks/reference/).

**Caveat:** require ≥ v0.26.0 at runtime; older installs lack hooks entirely and must fall to the heuristic detector.

**Resume (bonus):** `gemini --resume` (most-recent), `--resume <index>`, `--resume <uuid>`; `/rewind`; `--checkpointing` for file-state snapshots. VERIFIED-as-documented. Sources: [session-management docs](https://geminicli.com/docs/cli/session-management/).

## OpenCode — VERIFIED (callback present, two paths)

OpenCode exposes the completion signal **twice**:

1. **Server SSE event bus.** `opencode serve` runs a headless HTTP server with a Server-Sent-Events endpoint at **`GET /event`** streaming all bus events. The relevant type is **`session.idle`** — fires when the agent finishes processing and the session goes idle (turn complete, waiting for input). Payload shape `{ "type": "session.idle", "properties": { ... } }`. The official docs use `session.idle` as the canonical "OpenCode finished" example. Sources: [server docs](https://opencode.ai/docs/server/), [SDK docs](https://opencode.ai/docs/sdk/) (typed `@opencode-ai/sdk`).
2. **Plugin `event` hook.** A JS/TS plugin in `~/.config/opencode/plugin/` (global) or `.opencode/plugin/` (project) exports an `event` hook that receives the same bus events; filter `event.type === "session.idle"` and write a status file. This is the closest structural analog to yoloAI's existing "agent writes a status file" pattern and works in the TUI (plugins load in all modes), so it avoids forcing `opencode serve`. Source: [plugins docs](https://opencode.ai/docs/plugins/).

**Documentation/stability:** both paths officially documented; project pre-1.0 but very actively released (v1.17.x as of 2026-06-24). The plugin `event` hook is currently **fire-and-forget** (plugin's returned promise is ignored); a proposal to make it awaitable on `session.idle` is open ([sst/opencode#16879](https://github.com/sst/opencode/issues/16879)) — note in case yoloAI needs the agent to block until the status write lands. The exact version `session.idle` first appeared is UNKNOWN (not pinned in changelog), but it is present and documented mid-2026.

**Resume (bonus):** `opencode --continue` / `-c` (most-recent session in cwd), `opencode --session <id>` / `-s`, plus `--fork` to branch. VERIFIED-as-documented. Source: [CLI docs](https://opencode.ai/docs/cli/).

## Aider — VERIFIED (signal present, data-poor)

Aider has **no general-purpose hook/event/callback system** — no lifecycle hooks, no event subscription, no structured-event output mode. The Python scripting API exists but is explicitly *"not officially supported or documented, and could change … without backwards compatibility"* ([scripting docs](https://aider.chat/docs/scripting.html)), so it is not a reliable integration surface.

The **only** built-in turn-completion signal is the notifications feature:

- `--notifications` (env `AIDER_NOTIFICATIONS`, YAML `notifications: true`) — desktop notification via platform default (`notify-send` / `terminal-notifier`).
- `--notifications-command "CMD"` (env `AIDER_NOTIFICATIONS_COMMAND`, YAML `notifications_command:`) — runs a custom command.

**Trigger (quoted from official docs):** *"when the LLM has finished generating a response and is waiting for your input."* That is precisely the boundary yoloAI wants. **But** source inspection ([io.py](https://github.com/Aider-AI/aider/blob/main/aider/io.py)) shows it is run as `subprocess.run(self.notifications_command, shell=True, capture_output=True)` — the command runs **verbatim with no arguments and no payload** (no model, no message, no session id; stdout/stderr captured only for error handling). So it is a **bare "turn done" pulse**, not a structured callback. Usable to flip a yoloAI status file (the command itself can write the file), but it carries no content yoloAI could otherwise extract.

**Documentation/stability:** notifications documented at [aider.chat/docs/usage/notifications.html](https://aider.chat/docs/usage/notifications.html); introduced **v0.76.0**, stable, no deprecation. Sources: [options reference](https://aider.chat/docs/config/options.html), [YAML config](https://aider.chat/docs/config/aider_conf.html), [HISTORY.md](https://raw.githubusercontent.com/Aider-AI/aider/main/HISTORY.md).

**Resume (bonus):** `--restore-chat-history` (env `AIDER_RESTORE_CHAT_HISTORY`, since v0.35.0) — a binary toggle that reloads the last conversation; not a structured session API. In-chat `/save` + `/load` reconstruct a session's file set. A community PR for richer `/session` management was not merged. Sources: HISTORY.md, [commands docs](https://aider.chat/docs/usage/commands.html).

## What this means for the detection-strategy design

The pluggable-strategy hypothesis holds: heuristic stays the default, and **three of the four** agents justify a native callback strategy — but the abstraction must accommodate **two delivery shapes**, not one.

| Agent | Verdict | Strategy |
|-------|---------|----------|
| **Codex CLI** | **Native callback.** `notify` program on `agent-turn-complete` with rich JSON. | Custom strategy — register a `notify` program (in `~/.codex/config.toml`) that writes the status file, mirroring the Claude `Stop`-hook wiring. Highest-value non-Claude integration. |
| **Gemini CLI** | **Native callback.** `AfterAgent` hook, GA. Closest structural twin to Claude's `Stop` (stdin JSON, command hook). | Custom strategy — wire `hooks.AfterAgent` in `~/.gemini/settings.json`. Gate on runtime version ≥ v0.26.0; older → heuristic. Wire `AfterAgent`, **not** `Stop`. |
| **OpenCode** | **Native callback.** `session.idle` via plugin `event` hook (preferred) or SSE. | Custom strategy — ship a plugin that writes the status file on `session.idle`. Plugin path fits the existing "agent writes a status file" model and avoids forcing `opencode serve`. |
| **Aider** | **Borderline.** Real turn-complete trigger, but **zero payload** (bare command). | A degenerate "callback" strategy: point `--notifications-command` at a one-liner that touches the status file. Worth it (deterministic, removes terminal-scraping for Aider), but it carries no data — so it's a status *pulse*, not a content callback. |

**Design implications worth flagging:**

- **The abstraction is justified, and it pays off for 3–4 agents, not just Claude** — so it's worth more than a Claude-only special case. But the strategy interface must not assume Claude's exact mechanism. Concretely, native callbacks arrive in **two families**: (a) **agent-runs-our-command** (Claude `Stop`, Codex `notify`, Gemini `AfterAgent`, Aider `--notifications-command`) — the agent execs a yoloAI-provided program/hook that writes the status file; and (b) **we-subscribe-to-a-stream** (OpenCode SSE, or the OpenCode plugin which is family (a) again). A clean strategy boundary is *"produce an authoritative status signal,"* with the per-agent strategy choosing command-hook vs plugin vs stream-subscription internally.
- **Aider is the agent that changes the abstraction calculus.** Its signal is real but data-free, so a strategy interface that assumes "the callback hands me the assistant's last message / structured turn metadata" would not fit Aider. Keep the contract minimal — "tell me a turn ended" — and treat any richer payload as an optional bonus, or Aider forces a leaky special-case. This is the one finding that argues for a *thinner* strategy interface than Claude alone would suggest.
- **Version-gating is a first-class concern for Gemini** (hooks only ≥ v0.26.0) and to a lesser degree OpenCode (fast-moving pre-1.0 event names). The strategy selection should be able to fall back to heuristic at runtime when the native mechanism is absent, rather than assuming presence from agent identity alone.
- **Net:** after this research, the heuristic detector is the *floor*, not the common case — only legacy/old-version installs and unknown future agents land there. That strengthens the case for building the strategy seam, while keeping its contract narrow (binary turn-ended) so Aider doesn't break it.

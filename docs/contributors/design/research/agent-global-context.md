> **ABOUTME:** Verified survey of how each shipped agent ingests global, user-level context (its
> file, path, and import mechanism), so the agent-layer's Context capability can inject yoloAI's
> operating instructions without assuming a Claude-shaped mechanism.

# Agent global-context ingestion

**Verified mid-2026 (web survey of official docs); a fast-moving snapshot — treat as version-dependent.**
Supports the **agent layer** ([../agent-layer.md](../agent-layer.md)) Context capability: yoloAI injects
generated *operating instructions* (orientation + the file-exchange/Q&A protocol + later a netpolicy notice)
into wherever each agent reads its **global** (user-level, cross-project) context. This note maps each shipped
agent's mechanism so the design isn't surprised by an incompatible shape.

## Comparison

| Agent | Global instructions file (name + location) | Project-level file | AGENTS.md standard? | Hierarchy / precedence | Import mechanism | Simple global file? |
|---|---|---|---|---|---|---|
| **Claude Code** | `~/.claude/CLAUDE.md` | `./CLAUDE.md` / `./.claude/CLAUDE.md`; `./CLAUDE.local.md`; walks up ancestors, subdir files lazy | No — reads `CLAUDE.md`; bridges `AGENTS.md` only via `@AGENTS.md`/symlink/`/init` | managed → user → project(+ancestors) → local → subdir; concatenated, later wins | **Yes — `@path`** (rel/abs/`~`, depth 4) | **YES** |
| **Aider** | **None auto-read.** Closest: a conventions file via `read:` in `~/.aider.conf.yml`, or `--read <file>` | `CONVENTIONS.md` — **must be declared** (`--read`/`/read`/`read:`), never auto-discovered | No documented support | config search `~/.aider.conf.yml` → git-root → cwd, last-wins | none for instructions | **NO** — needs file **+** a `read` declaration |
| **Gemini CLI** | `~/.gemini/GEMINI.md` (filename configurable via `context.fileName`) | `GEMINI.md` in cwd; up to `.git`/home + down into subdirs | Not by default; `context.fileName` can be set to `AGENTS.md` | global → project(+ancestors) → subdirs; concatenated | **Yes — `@path`** | **YES** (watch configurable name) |
| **OpenAI Codex CLI** | `~/.codex/AGENTS.md` (or `AGENTS.override.md`; `CODEX_HOME` relocates) | `AGENTS.md`/`AGENTS.override.md` per dir, merged root→cwd | **Yes — native** (OpenAI co-authored the standard) | home → each dir root→cwd; concatenated, closer wins | none documented | **YES** (watch override file + `CODEX_HOME`) |
| **OpenCode** | `~/.config/opencode/AGENTS.md` (fallback `~/.claude/CLAUDE.md`) | `AGENTS.md` (fallback `CLAUDE.md`), up-traversal | **Yes — primary**, CLAUDE.md back-compat | local + global + `instructions[]` config, combined; first match per category | **No `@import`** — use `instructions[]` (globs/URLs) | **YES** (watch CLAUDE.md fallback) |

## Synthesis

**Four fit "append to one global file"** the agent auto-reads, concatenated with project files:
Claude (`~/.claude/CLAUDE.md`), Gemini (`~/.gemini/GEMINI.md`), Codex (`~/.codex/AGENTS.md`), OpenCode
(`~/.config/opencode/AGENTS.md`). yoloAI appends a fenced operating-instructions block at the right path.

**Aider is the structural outlier.** No auto-read global markdown file exists. Persistent cross-project
instructions need **two artifacts**: the instructions file **plus** a declaration that loads it. Since yoloAI
controls the launch command, the robust route is injecting **`--read /path/to/yoloai-operating.md`** into
Aider's launch args (more reliable than mutating `~/.aider.conf.yml`, whose last-wins merge a project config
can override).

## Secondary design notes

- **Imports are not portable.** Only Claude + Gemini expand `@path`. Codex/OpenCode/Aider do not. So a
  "stub that `@`-imports a yoloAI-managed file" strategy works for *only two* agents — append/inject directly.
- **The injected global block is the *weakest* layer.** All file-agents concatenate global + project and let
  the closer-to-cwd file win. yoloAI's operating instructions are thus overridable by a project file — fine for
  defaults (Q&A protocol, orientation), **not a hard guarantee** (a project `CLAUDE.md` could suppress them).
  Not a containment concern (the sandbox is the real boundary), but the design shouldn't *rely* on the
  instructions being authoritative.
- **Per-agent footguns the runner must resolve (don't assume the default path):** Gemini's `context.fileName`
  may rename the file; Codex silently ignores `AGENTS.md` when `AGENTS.override.md` exists, and `CODEX_HOME`
  relocates the dir; OpenCode reads `~/.claude/CLAUDE.md` if its own file is absent. Resolve the *effective*
  global file, not the nominal default.
- **AGENTS.md convergence.** Codex + OpenCode are native `AGENTS.md`; Gemini can be pointed at it; only Claude
  is hard-proprietary and Aider supports no standard. A *future* agent most likely uses `AGENTS.md` — a useful
  default assumption for new registrations.

## Flagged / unverified

- Gemini `/memory add` + explicit override semantics not confirmed across the two cross-checked pages (drift:
  `/memory refresh` vs `reload`).
- Codex's legacy `~/.codex/instructions.md` (pre-`AGENTS.md` rename) is plausible-but-undocumented in current
  sources.
- Aider's "home config makes it global" is *inferred* from two documented facts (home config always searched +
  `read:` always loads); the AGENTS.md non-support and no-auto-file are absence-of-evidence.

## Design implication (agent layer)

The Context capability is **the agent's DEF-injection *method*, declared as data** — and the user's global
config (ABC) is already brought in by the **Credentials/State** seed capability (the agent-files copy; the
global `CLAUDE.md` is not in `AgentFilesExclude`), so Context does **not** reach outside. Two method shapes:

- **append-to-context-file** (Claude/Gemini/Codex/OpenCode): append DEF at the agent's declared
  `StateDir`/`ContextFile` — but resolve the *effective* path (the footguns above).
- **launch-flag** (Aider): write DEF to a scratch file + inject `--read <file>` into the launch command —
  crossing into the Launch capability.

Both are **data** (a tagged method + its parameters), so the capability *generalizes* across the divergence
rather than breaking — confirming the agent-layer's "data + thin adapter" shape held without needing code.

## Primary sources

Claude — `code.claude.com/docs/en/memory`, `/settings`. Aider — `aider.chat/docs/usage/conventions.html`,
`/config/aider_conf.html`. Gemini — `github.com/google-gemini/gemini-cli` `docs/cli/gemini-md.md`,
`docs/reference/configuration.md`. Codex — `developers.openai.com/codex/guides/agents-md`, `/config-reference`.
OpenCode — `opencode.ai/docs/rules/`, `/docs/config/`.

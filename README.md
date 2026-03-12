# yoloAI

**Sandboxed runner for AI coding agents. Your files stay untouched until you say otherwise.**

![terminal](terminal.svg)

AI coding agents (Claude Code, Codex, Gemini, etc.) want to edit your files and run commands. You can approve every action one by one — or let them loose in a disposable sandbox and review the diff when they're done.

```text
  You                          Sandbox                        Your project
   │                              │                               │
   ├─ yoloai new fix-bug .        ├─ local sandbox copy of dir    │
   │                              │                               │
   ├─ << write your prompt(s) >>  ├─ agent works freely           │
   │                              │  (no permission prompts)      │
   │                              │                               │
   ├─ yoloai diff fix-bug         ├─ shows what changed           │
   │                              │                               │
   ├─ yoloai apply fix-bug        │                               ├─ patches applied
   │  (you can choose which ones) │                               │
   │                              │                               │
   ├─ yoloai destroy fix-bug      ├─ destroys sandbox             │
```

## Why?

**Permission fatigue is real.** After a hundred approve/deny prompts you stop reading and just hit "yes" — or you reach for `--dangerously-skip-permissions` and hope for the best. Neither is great.

yoloAI takes a different approach: let the agent do whatever it wants inside a disposable container. Your originals are never modified. When the agent is done, review the diff and choose what to keep.

- **Your files are untouchable.** The agent works on an isolated copy. Originals never change until you say so.
- **Git-powered review.** `diff` shows exactly what changed. `apply` patches your project cleanly, preserving individual commits.
- **No permission prompts.** The container is disposable — agents run with full access inside the sandbox.
- **Persistent agent state.** Session history and config survive stops and restarts.
- **Easy retry.** `yoloai reset` re-copies your original for a fresh attempt.

## The workflow

```bash
# Authenticate (yoloAI picks up existing credentials automatically)
export ANTHROPIC_API_KEY=sk-ant-...   # Claude Code
export GEMINI_API_KEY=...             # Gemini CLI
# Or just let it pick up your already authenticated session

# 1. Spin up a sandbox. Agent starts working immediately when you supply a prompt here
yoloai new fix-bug ./my-project --prompt "fix the failing tests"

# 2. See what the agent changed
yoloai diff fix-bug

# 3. Apply the good parts to your real project
yoloai apply fix-bug

# 4. Toss the container
yoloai destroy fix-bug
```

Or drop into an interactive session:

```bash
yoloai new exploration ./my-project -a
# You're inside the agent, running in tmux in the sandbox.
#   Ctrl-B, D to detach.
#   yoloai attach exploration to reconnect.
```

## Iterative workflow

For longer tasks, work in a commit-by-commit loop. Keep two terminals open — one for yoloAI, one for your normal shell.

```
┌─ YOLO shell ──────────────────────┬─ Outer shell ─────────────────────┐
│                                   │                                   │
│ yoloai new myproject . -a         │                                   │
│                                   │                                   │
│ # Tell the agent what to do,      │                                   │
│ # have it commit when done.       │                                   │
│                                   │ yoloai apply myproject            │
│                                   │ # Review and accept the commits.  │
│                                   │                                   │
│ # ... next task, next commit ...  │                                   │
│                                   │ yoloai apply myproject            │
│                                   │                                   │
│                                   │ # When you have a good set of     │
│                                   │ # commits, push:                  │
│                                   │ git push                          │
│                                   │                                   │
│                                   │ # Done? Tear it down:             │
│                                   │ yoloai destroy myproject          │
└───────────────────────────────────┴───────────────────────────────────┘
```

The agent works on an isolated copy, so you can keep iterating without risk. Each `apply` patches the real project with only the new commits since the last apply.

## Agents

**Claude Code** (default), **Aider**, **Codex**, **Gemini**, **OpenCode**. Use `yoloai system agents` to list available agents.

## Install

```bash
git clone https://github.com/kstenerud/yoloai.git
cd yoloai
make build
sudo mv yoloai /usr/local/bin/  # or add to PATH
```

Single Go binary, no runtime dependencies beyond your chosen backend. On first run, yoloAI builds its base image (~2 min) and creates `~/.yoloai/`.

**Build requires:** Go 1.24+

**Runtime backends:**

| Backend  | Supported Hosts              | Dependencies                                                       |
|----------|------------------------------|--------------------------------------------------------------------|
| docker   | Linux, macOS, Windows (WSL2) | [Docker Engine](https://docs.docker.com/engine/install/) or [Docker Desktop](https://docs.docker.com/get-docker/) |
| tart     | macOS (Apple Silicon)        | [Tart](https://github.com/cirruslabs/tart) (`brew install cirruslabs/cli/tart`) |
| seatbelt | macOS (any)                  | None (uses built-in `sandbox-exec`)                                |

## Learn more

- **[Usage Guide](docs/GUIDE.md)** — commands, flags, workdir modes, configuration, security
- **[Roadmap](docs/ROADMAP.md)** — upcoming features
- **[Architecture](docs/dev/ARCHITECTURE.md)** — code navigation for contributors

Early access. Core workflow works, rough edges expected. [Feedback welcome.](https://github.com/kstenerud/yoloai/issues)

## License

[MIT](LICENSE)

# yoloAI

**Fearless YOLO sandbox for your agent.**

* No more endless inane prompts that you'll just answer yes to
* No more babysitting the agent's long running tasks
* No more annoying interruptions

Let AI coding agents go **wild** (inside a disposable container or VM).

* Your files are never touched.
* When the agent's done, review what changed with `yoloai diff` and cherry-pick what you want with `yoloai apply`.
* No permission prompts, no anxiety, no messy cleanup.

![terminal](terminal.svg)

> MVP — early access. Core workflow works, rough edges expected. [Feedback welcome.](https://github.com/kstenerud/yoloai/issues)

## The workflow

```bash
# Authenticate (API key or OAuth — yoloAI picks up existing credentials automatically)
export ANTHROPIC_API_KEY=sk-ant-...   # Claude Code
export GEMINI_API_KEY=...             # Gemini CLI (or `gemini` OAuth login)

# 1. Spin up a sandbox — agent starts working immediately
yoloai new fix-bug ./my-project --prompt "fix the failing tests"

# 2. See what the agent changed
yoloai diff fix-bug

# 3. Apply the good parts to your real project
yoloai apply fix-bug

# 4. Toss the container
yoloai destroy fix-bug
```

Or skip the prompt and drop into an interactive session:

```bash
yoloai new explore ./my-project
# You're inside the agent, running in tmux in the sandbox.
#   Ctrl-B, D to detach.
#   yoloai attach explore to reconnect.
```

Agents: **Claude Code** (default), **Gemini CLI** (`-a gemini`). Use `yoloai info agents` to list available agents.

## Iterative workflow

For longer tasks, work in a commit-by-commit loop. Keep two terminals open in your project directory — one for yoloAI, one for your normal shell. Start with a clean git repo.

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

## Why?

- **Your files are untouchable.** The agent works on an isolated copy. Originals never change until you say so.
- **Git-powered review.** `diff` shows exactly what changed. `apply` patches your project cleanly.
- **No permission prompts.** The container is disposable — agents run with their sandbox-bypass flags. No more clicking "approve" on every edit.
- **Persistent agent state.** Session history and config survive stops and restarts.
- **Easy retry.** `yoloai reset` re-copies your original for a fresh attempt — no leftover state.

## Install

```bash
git clone https://github.com/kstenerud/yoloai.git
cd yoloai
make build
sudo mv yoloai /usr/local/bin/  # or add to PATH
```

On first run, yoloAI builds its base image (~2 min, depending on backend type) and creates `~/.yoloai/`.

**Requirements:**

For building: Go 1.24+

For running:

| Backend  | Supported Hosts                  | Dependencies                                                       |
|----------|----------------------------------|--------------------------------------------------------------------|
| docker   | Linux, macOS, Windows (WSL2)     | [Docker Engine](https://docs.docker.com/engine/install/) or [Docker Desktop](https://docs.docker.com/get-docker/) |
| tart     | macOS (Apple Silicon only)       | [Tart](https://github.com/cirruslabs/tart) (`brew install cirruslabs/cli/tart`) |
| seatbelt | macOS (any architecture)         | None (uses built-in `sandbox-exec`)                                |

Use `yoloai system backends` to check which backends are available on your system.

## Learn more

- **[Usage Guide](docs/GUIDE.md)** — commands, flags, workdir modes, configuration, security
- **[Roadmap](docs/ROADMAP.md)** — upcoming agents, network isolation, profiles, overlayfs
- **[Architecture](docs/dev/ARCHITECTURE.md)** — code navigation for contributors

## License

[MIT](LICENSE)

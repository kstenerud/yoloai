# yoloAI

**Fearless YOLO for your agent.**

Let AI coding agents go **wild** (inside a disposable Docker container). Your files are never touched. When the agent's done, review what changed with `yoloai diff` and cherry-pick what you want with `yoloai apply`. No permission prompts, no anxiety, no messy cleanup.

![terminal](terminal.svg)

> MVP — early access. Core workflow works, rough edges expected. [Feedback welcome.](https://github.com/kstenerud/yoloai/issues)

## The workflow

```bash
# Authenticate (API key or `claude login` OAuth — yoloAI picks it up automatically)
export ANTHROPIC_API_KEY=sk-ant-...

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
# You're inside Claude Code, running in the container.
# Ctrl-B, D to detach. yoloai attach explore to reconnect.
```

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

On first run, yoloAI builds its base Docker image (~2 min) and creates `~/.yoloai/`.

**Requirements:** Docker, Go 1.24+ (build only)

## Learn more

- **[Usage Guide](docs/GUIDE.md)** — commands, flags, workdir modes, configuration, security
- **[Roadmap](docs/ROADMAP.md)** — upcoming agents, network isolation, profiles, overlayfs
- **[Architecture](docs/ARCHITECTURE.md)** — code navigation for contributors

## License

[MIT](LICENSE)

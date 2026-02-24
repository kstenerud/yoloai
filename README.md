# yoloai

Sandboxed AI coding agent runner. Runs AI coding agents (Claude Code, Codex) inside disposable Docker containers with a copy/diff/apply workflow.

Your originals are protected — the agent works on an isolated copy, you review what changed with `yoloai diff`, and choose what to keep with `yoloai apply`.

## Why

Existing approaches either give the agent live access to your files (risky) or isolate it without a clean way to review and land changes. yoloai gives you both: full isolation with git-based diff/apply to bridge the gap.

## Requirements

- Go 1.24+
- Docker

## Install

```bash
git clone https://github.com/kstenerud/yoloai.git
cd yoloai
make build
sudo mv yoloai /usr/local/bin/  # or add to PATH
```

On first run, yoloai automatically builds its base Docker image and creates `~/.yoloai/`.

## Quick Start

```bash
# Set your API key
export ANTHROPIC_API_KEY=sk-ant-...

# Create a sandbox (copies your project into an isolated container)
yoloai new fix-bug ./my-project --prompt "fix the failing tests"

# Watch the agent work
yoloai attach fix-bug
# Ctrl-B, D to detach

# Review what changed
yoloai diff fix-bug

# Apply changes back to your original project
yoloai apply fix-bug

# Clean up
yoloai destroy fix-bug
```

## Commands

| Command | Description |
|---------|-------------|
| `yoloai new <name> [workdir]` | Create and start a sandbox |
| `yoloai attach <name>` | Attach to the agent's tmux session |
| `yoloai show <name>` | Show sandbox configuration and state |
| `yoloai list` | List all sandboxes |
| `yoloai log <name>` | Show session log |
| `yoloai diff <name>` | Show changes the agent made |
| `yoloai apply <name>` | Apply changes back to original directory |
| `yoloai exec <name> <cmd>` | Run a command inside the sandbox |
| `yoloai stop <name>...` | Stop sandboxes (preserving state) |
| `yoloai start <name>` | Start a stopped sandbox |
| `yoloai destroy <name>...` | Stop and remove sandboxes |
| `yoloai reset <name>` | Re-copy workdir and reset to original state |
| `yoloai build` | Build or rebuild the base Docker image |
| `yoloai completion <shell>` | Generate shell completion (bash/zsh/fish/powershell) |
| `yoloai version` | Show version information |

All commands that take `<name>` support the `YOLOAI_SANDBOX` environment variable as a default:

```bash
export YOLOAI_SANDBOX=fix-bug
yoloai diff      # equivalent to: yoloai diff fix-bug
yoloai show      # equivalent to: yoloai show fix-bug
```

## Workdir Modes

The workdir (your project directory) is copied into the sandbox by default. You can control the mount mode with a suffix:

| Mode | Syntax | Behavior |
|------|--------|----------|
| `:copy` | `./my-app` (default) | Isolated copy. Agent changes are reviewed via diff/apply. |
| `:rw` | `./my-app:rw` | Live bind-mount. Changes are immediate — no diff/apply needed. |

```bash
# Default: safe isolated copy
yoloai new task1 ./my-project

# Live mount (use with caution — agent writes directly to your files)
yoloai new task2 ./my-project:rw
```

## Agents

| Agent | API Key | Description |
|-------|---------|-------------|
| `claude` (default) | `ANTHROPIC_API_KEY` | Claude Code in interactive mode |

```bash
# Use a specific model
yoloai new task ./my-project --model sonnet
yoloai new task ./my-project --model opus
```

## Key Flags

```bash
# Provide a prompt (headless mode)
yoloai new task ./project --prompt "refactor the auth module"
yoloai new task ./project --prompt-file instructions.md

# Create without starting
yoloai new task ./project --no-start

# Replace an existing sandbox
yoloai new task ./project --replace

# Skip confirmation prompts
yoloai destroy task --yes
yoloai apply task --yes

# Disable network access
yoloai new task ./project --network-none

# Port forwarding
yoloai new task ./project --port 3000:3000

# Stop/destroy all sandboxes
yoloai stop --all
yoloai destroy --all --yes

# Reset workdir (re-copy from original, restart agent)
yoloai reset task
yoloai reset task --clean       # also wipe agent memory
yoloai reset task --no-prompt   # don't re-send prompt

# Diff options
yoloai diff task --stat              # summary only
yoloai diff task -- src/handler.go   # filter to specific paths
```

## How It Works

1. `yoloai new` copies your project into `~/.yoloai/sandboxes/<name>/work/`, creates a git baseline commit, and launches a Docker container running the agent.

2. The agent works inside the container on the copy. Your original files are never touched.

3. `yoloai diff` shows a git diff between the baseline and the agent's current state — including new files, deletions, and binary changes.

4. `yoloai apply` generates a patch and applies it to your original directory. It does a dry-run check first and prompts for confirmation.

5. `yoloai reset` re-copies the original and resets the baseline, letting you retry the same task from scratch.

## Sandbox State

All sandbox state lives on the host at `~/.yoloai/sandboxes/<name>/`:

```
~/.yoloai/sandboxes/<name>/
  meta.json       # sandbox config (paths, mode, baseline SHA)
  config.json     # container entrypoint config
  prompt.txt      # initial prompt (if provided)
  log.txt         # tmux session log
  agent-state/    # agent's persistent state (e.g., ~/.claude/)
  work/           # isolated copy of your project
```

Containers are ephemeral — if removed, `yoloai start` recreates them from `meta.json`. Your work and agent state persist.

## Security

- **Read-only by default.** Workdirs use `:copy` mode unless you explicitly opt into `:rw`.
- **Dangerous directory detection.** Refuses to mount `$HOME`, `/`, or system directories without `:force`.
- **Dirty repo warning.** Prompts if your workdir has uncommitted git changes.
- **Credential injection via files.** API keys are mounted as read-only files at `/run/secrets/`, not passed as environment variables. Host temp files are cleaned up after container start.
- **Non-root execution.** Containers run as a non-root user with UID/GID matching your host user.

## Development

```bash
make build          # Build binary with version info
make test           # Run unit tests
make lint           # Run golangci-lint
make integration    # Run integration tests (requires Docker)
make clean          # Remove binary

# Build with custom version
make VERSION=1.0.0 build
```

## License

TBD

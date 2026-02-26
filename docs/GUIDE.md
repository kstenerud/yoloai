# yoloAI Usage Guide

Full reference for commands, flags, configuration, and internals. For a quick overview, see the [README](../README.md).

## Commands

**Core Workflow**

| Command | Description |
|---------|-------------|
| `yoloai new <name> [workdir]` | Create and start a sandbox |
| `yoloai attach <name>` | Attach to the agent's tmux session |
| `yoloai diff <name>` | Show changes the agent made |
| `yoloai apply <name>` | Apply changes back to original directory |

**Lifecycle**

| Command | Description |
|---------|-------------|
| `yoloai stop <name>...` | Stop sandboxes (preserving state) |
| `yoloai start <name>` | Start a stopped sandbox |
| `yoloai reset <name>` | Re-copy workdir and reset to original state |
| `yoloai destroy <name>...` | Stop and remove sandboxes |

**Inspection**

| Command | Description |
|---------|-------------|
| `yoloai list` | List sandboxes and their status (alias: `ls`) |
| `yoloai show <name>` | Show sandbox configuration and state |
| `yoloai log <name>` | Show session log |
| `yoloai exec <name> <cmd>` | Run a command inside the sandbox |

**Admin**

| Command | Description |
|---------|-------------|
| `yoloai build` | Build or rebuild the base Docker image |
| `yoloai completion <shell>` | Generate shell completion (bash/zsh/fish/powershell) |
| `yoloai version` | Show version information |

### YOLOAI_SANDBOX

All commands that take `<name>` support the `YOLOAI_SANDBOX` environment variable as a default:

```bash
export YOLOAI_SANDBOX=fix-bug
yoloai diff      # equivalent to: yoloai diff fix-bug
yoloai attach    # equivalent to: yoloai attach fix-bug
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

## Agents and Models

This MVP ships with Claude Code as the only supported agent. The architecture is agent-agnostic — more agents are planned (see [Roadmap](ROADMAP.md)).

| Agent | API Key | Description |
|-------|---------|-------------|
| `claude` (default) | `ANTHROPIC_API_KEY` | Claude Code in interactive mode |

You can select a model using shorthand aliases or full model names:

```bash
yoloai new task ./my-project --model sonnet   # claude-sonnet-4-latest
yoloai new task ./my-project --model opus     # claude-opus-4-latest
yoloai new task ./my-project --model haiku    # claude-haiku-4-latest
yoloai new task ./my-project --model claude-sonnet-4-20250514  # exact model
```

## Global Flags

| Flag | Description |
|------|-------------|
| `-v` | Verbose output |
| `-q` | Quiet output |
| `--no-color` | Disable color output |
| `--backend <name>` | Runtime backend to use (default: `docker`). Overrides `defaults.backend` in config. |

## Key Flags

### Creating sandboxes

```bash
# Prompt (headless — agent runs the task autonomously)
yoloai new task ./project --prompt "refactor the auth module"
yoloai new task ./project --prompt-file instructions.md
echo "fix the build" | yoloai new task ./project --prompt -   # from stdin

# Create without starting the container
yoloai new task ./project --no-start

# Create without auto-attaching
yoloai new task ./project --detach

# Replace an existing sandbox with the same name
yoloai new task ./project --replace

# Pass extra arguments directly to the agent CLI
yoloai new task ./project -- --allowedTools "Edit,Write,Bash"

# Disable network access entirely
yoloai new task ./project --network-none

# Expose a container port to the host
yoloai new task ./project --port 3000:3000
```

### Managing sandboxes

```bash
# Skip confirmation prompts
yoloai destroy task --yes
yoloai apply task --yes

# Stop/destroy all sandboxes
yoloai stop --all
yoloai destroy --all --yes

# Reset workdir (re-copy from original, restart agent)
yoloai reset task
yoloai reset task --clean       # also wipe agent memory
yoloai reset task --no-prompt   # don't re-send prompt
```

### Reviewing changes

```bash
# Full diff
yoloai diff task

# Summary only (files changed, insertions, deletions)
yoloai diff task --stat

# Filter to specific paths
yoloai diff task -- src/handler.go
```

## How It Works

1. **`yoloai new`** copies your project into `~/.yoloai/sandboxes/<name>/work/`, creates a git baseline commit, and launches a Docker container running the agent.

2. **The agent works inside the container** on the copy. Your original files are never touched.

3. **`yoloai diff`** shows a git diff between the baseline and the agent's current state — including new files, deletions, and binary changes.

4. **`yoloai apply`** generates a patch and applies it to your original directory. It does a dry-run check first and prompts for confirmation.

5. **`yoloai reset`** re-copies the original and resets the baseline, letting you retry the same task from scratch.

## Configuration

On first run, yoloAI creates `~/.yoloai/config.yaml` with sensible defaults:

```yaml
defaults:
  agent: claude
  backend: docker    # Runtime backend: "docker" (default) or "tart" (macOS VMs)
  # tart_image: ghcr.io/cirruslabs/macos-sequoia-base:latest  # Custom base VM for tart backend

  mounts:
    - ~/.gitconfig:/home/yoloai/.gitconfig:ro

  ports: []

  resources:
    cpus: 4
    memory: 8g
```

You can edit this file to change the default agent, runtime backend, add persistent bind mounts (like SSH config or tool configs), adjust resource limits, or set default port mappings. These defaults apply to all new sandboxes.

Backend resolution order: `--backend` CLI flag > `defaults.backend` in config > `"docker"` default.

## Sandbox State

All sandbox state lives on the host at `~/.yoloai/sandboxes/<name>/`:

```
~/.yoloai/sandboxes/<name>/
  meta.json       # sandbox config (paths, mode, baseline SHA, backend)
  config.json     # container entrypoint config
  prompt.txt      # initial prompt (if provided)
  log.txt         # tmux session log
  agent-state/    # agent's persistent state (e.g., ~/.claude/)
  work/           # isolated copy of your project
```

Containers are ephemeral — if removed, `yoloai start` recreates them from `meta.json`. Your work and agent state persist.

## Security

- **Originals are protected.** Workdirs use `:copy` mode by default — the agent works on an isolated copy, never your original files. Opt into `:rw` explicitly for live access.
- **Dangerous directory detection.** Refuses to mount `$HOME`, `/`, or system directories. Append `:force` to override (e.g., `$HOME:force`).
- **Dirty repo warning.** Prompts if your workdir has uncommitted git changes, so you don't lose work.
- **Credential injection via files.** API keys are mounted as read-only files at `/run/secrets/`, not passed as environment variables. Temp files on the host are cleaned up after container start. On macOS, yoloai also checks the macOS Keychain for Claude Code OAuth credentials (service `Claude Code-credentials`). If you're logged in via `claude` CLI, yoloai will automatically detect your credentials even without `~/.claude/.credentials.json` on disk.
- **Non-root execution.** Containers run as a non-root user with UID/GID matching your host user.

## Development

```bash
make build          # Build binary with version info
make test           # Run unit tests
make lint           # Run golangci-lint
make integration    # Run integration tests (requires Docker)
make clean          # Remove binary
```

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
| `yoloai system` | System information and management |
| `yoloai system info` | Show version, paths, disk usage, backend availability |
| `yoloai system agents [name]` | List available agents |
| `yoloai system backends [name]` | List available runtime backends |
| `yoloai system build` | Build or rebuild the base Docker image |
| `yoloai system setup` | Re-run interactive first-run setup |
| `yoloai sandbox` | Sandbox inspection |
| `yoloai sandbox list` | List sandboxes and their status |
| `yoloai sandbox info <name>` | Show sandbox configuration and state |
| `yoloai sandbox log <name>` | Show session log |
| `yoloai sandbox exec <name> <cmd>` | Run a command inside the sandbox |
| `yoloai ls` | List sandboxes (shortcut for `sandbox list`) |
| `yoloai log <name>` | Show sandbox log (shortcut for `sandbox log`) |

**Admin**

| Command | Description |
|---------|-------------|
| `yoloai config get [key]` | Print configuration values (all settings or a specific key) |
| `yoloai config set <key> <value>` | Set a configuration value |
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

## Auxiliary Directories

You can mount additional directories alongside your workdir using the `-d` / `--dir` flag (repeatable). Auxiliary directories are read-only by default.

```bash
# Read-only auxiliary directories
yoloai new mybox . -d /path/to/lib

# Copy-mode auxiliary directory (isolated, diff/apply available)
yoloai new mybox . -d /path/to/lib:copy

# Writable bind-mount auxiliary directory
yoloai new mybox . -d /path/to/lib:rw

# Custom mount point for workdir
yoloai new mybox ./app=/opt/app

# Custom mount point for auxiliary directory
yoloai new mybox . -d ./lib=/opt/lib

# Multiple auxiliary directories
yoloai new mybox ./app -d ./shared-lib -d ./common-types
```

By default, directories are mounted at their original absolute host paths (mirrored paths). Use `=<path>` to mount at a custom container path instead.

## Agents and Models

yoloai ships with multiple agents. The architecture is agent-agnostic — more agents are planned (see [Roadmap](ROADMAP.md)).

| Agent | API Key | Description |
|-------|---------|-------------|
| `claude` (default) | `ANTHROPIC_API_KEY` | Claude Code in interactive mode |
| `gemini` | `GEMINI_API_KEY` | Gemini CLI in interactive mode |

Codex (`codex`) is defined but still undergoing testing — see the roadmap for status.

You can select a model using shorthand aliases or full model names. Aliases are agent-specific — use `yoloai system agents <name>` to see the full list for each agent.

```bash
# Claude model aliases
yoloai new task ./my-project --model sonnet   # claude-sonnet-4-latest
yoloai new task ./my-project --model opus     # claude-opus-4-latest
yoloai new task ./my-project --model haiku    # claude-haiku-4-latest
yoloai new task ./my-project --model claude-sonnet-4-20250514  # exact model

# Gemini model aliases
yoloai new task ./my-project --agent gemini --model pro    # gemini-2.5-pro
yoloai new task ./my-project --agent gemini --model flash  # gemini-2.5-flash
```

### Local Models (Aider)

Aider supports local model servers like Ollama and LM Studio. To use aider without a cloud API key, set the appropriate environment variable:

```bash
export OLLAMA_API_BASE=http://host.docker.internal:11434
```

Or configure it persistently:

```bash
yoloai config set defaults.env.OLLAMA_API_BASE http://host.docker.internal:11434
```

The `host.docker.internal` hostname allows the container to reach services running on the host machine.

## Global Flags

| Flag | Description |
|------|-------------|
| `-v` | Verbose output |
| `-q` | Quiet output |
| `--no-color` | Disable color output |

## Key Flags

### Creating sandboxes

`--backend <name>` selects the runtime backend (`docker` or `tart`). Available on `new`, `build`, and `setup`. Lifecycle commands (`start`, `stop`, etc.) read the backend from the sandbox's `meta.json` automatically.

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

# Restart agent (stop + start, preserving workspace)
yoloai restart task
yoloai restart task -a          # restart and auto-attach

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

On first run, yoloAI creates `~/.yoloai/config.yaml`. Use `yoloai config` to view and change settings:

```bash
# Show all settings with effective values (defaults + overrides)
yoloai config get

# Get a specific setting
yoloai config get defaults.backend

# Change a setting
yoloai config set defaults.backend tart

# Reset a setting to its default
yoloai config reset defaults.backend

# Remove an env var
yoloai config reset defaults.env.OLLAMA_API_BASE
```

### Settings

| Key | Default | Description |
|-----|---------|-------------|
| `setup_complete` | `false` | Set to `true` after first-run setup completes |
| `defaults.agent` | `claude` | Agent to use: `aider`, `claude`, `codex`, `gemini`, `opencode` |
| `defaults.model` | (empty) | Model name or alias passed to the agent |
| `defaults.backend` | `docker` | Runtime backend: `docker`, `tart`, `seatbelt` |
| `defaults.tart.image` | (empty) | Custom base VM image for tart backend |
| `defaults.tmux_conf` | (set by setup) | Tmux config mode: `default+host`, `default`, `host`, `none` |
| `defaults.env.<NAME>` | (empty) | Environment variable forwarded to container |

Agent resolution: `new` uses `--agent` flag > `defaults.agent` in config > `"claude"`.

Model resolution: `new` uses `--model` flag > `defaults.model` in config > `""` (empty = agent's default model).

Backend resolution: `new`/`build`/`setup` use `--backend` flag > `defaults.backend` in config > `"docker"`. Lifecycle commands read the backend from the sandbox's `meta.json`, falling back to config default.

You can also edit `~/.yoloai/config.yaml` directly — `config set` preserves comments and formatting.

## Sandbox State

All sandbox state lives on the host at `~/.yoloai/sandboxes/<name>/`:

```
~/.yoloai/sandboxes/<name>/
  meta.json       # sandbox config (paths, mode, baseline SHA, backend)
  config.json     # container entrypoint config
  prompt.txt      # initial prompt (if provided)
  log.txt         # tmux session log
  agent-state/    # agent's persistent state (e.g., ~/.claude/, ~/.gemini/)
  work/           # isolated copy of your project
```

Containers are ephemeral — if removed, `yoloai start` recreates them from `meta.json`. Your work and agent state persist.

## Security

- **Originals are protected.** Workdirs use `:copy` mode by default — the agent works on an isolated copy, never your original files. Opt into `:rw` explicitly for live access.
- **Dangerous directory detection.** Refuses to mount `$HOME`, `/`, or system directories. Append `:force` to override (e.g., `$HOME:force`).
- **Dirty repo warning.** Prompts if your workdir has uncommitted git changes, so you don't lose work.
- **Credential injection via files.** API keys are mounted as read-only files at `/run/secrets/`, not passed as environment variables. Temp files on the host are cleaned up after container start. Some agents support additional credential sources — for example, on macOS, yoloai checks the macOS Keychain for Claude Code OAuth credentials (service `Claude Code-credentials`). If you're logged in via `claude` CLI, yoloai will automatically detect your credentials even without `~/.claude/.credentials.json` on disk.
- **Non-root execution.** Containers run as a non-root user with UID/GID matching your host user.

## Development

```bash
make build          # Build binary with version info
make test           # Run unit tests
make lint           # Run golangci-lint
make integration    # Run integration tests (requires Docker)
make clean          # Remove binary
```

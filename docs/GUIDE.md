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
| `yoloai restart <name>` | Restart the agent in an existing sandbox |
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
| `yoloai system prune` | Remove orphaned resources and stale temp files |
| `yoloai system setup` | Re-run interactive first-run setup |
| `yoloai sandbox` | Sandbox inspection |
| `yoloai sandbox list` | List sandboxes and their status |
| `yoloai sandbox info <name>` | Show sandbox configuration and state |
| `yoloai sandbox log <name>` | Show session log |
| `yoloai sandbox exec <name> <cmd>` | Run a command inside the sandbox |
| `yoloai sandbox network-allow <name> <domain>` | Allow additional domains in an isolated sandbox |
| `yoloai ls` | List sandboxes (shortcut for `sandbox list`) |
| `yoloai log <name>` | Show sandbox log (shortcut for `sandbox log`) |
| `yoloai exec <name> <cmd>` | Run a command inside a sandbox (shortcut for `sandbox exec`) |

**Admin**

| Command | Description |
|---------|-------------|
| `yoloai profile create <name>` | Create a new profile with scaffold |
| `yoloai profile list` | List profiles |
| `yoloai profile info <name>` | Show merged profile configuration |
| `yoloai profile delete <name>` | Delete a profile |
| `yoloai files put <name> <file>...` | Copy files into sandbox exchange directory |
| `yoloai files get <name> <file> [dst]` | Copy a file out of sandbox exchange directory |
| `yoloai files ls <name> [glob]` | List files in sandbox exchange directory |
| `yoloai files rm <name> <glob>` | Remove files from sandbox exchange directory |
| `yoloai files path <name>` | Print host path to sandbox exchange directory |
| `yoloai config get [key]` | Print configuration values (all settings or a specific key) |
| `yoloai config set <key> <value>` | Set a configuration value |
| `yoloai help [topic]` | Show help topics (quickstart, agents, workflow, etc.) |
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
| `:overlay` | `./my-app:overlay` | Overlay mount. Instant setup, diff/apply workflow. Docker only. |
| `:rw` | `./my-app:rw` | Live bind-mount. Changes are immediate — no diff/apply needed. |

```bash
# Default: safe isolated copy
yoloai new task1 ./my-project

# Overlay: instant setup for large projects (Docker only)
yoloai new task2 ./large-project:overlay

# Live mount (use with caution — agent writes directly to your files)
yoloai new task3 ./my-project:rw
```

### Overlay Mode

`:overlay` uses Linux kernel overlayfs inside the Docker container to mount the original directory as a read-only lower layer, with agent changes captured in an upper layer. This provides:

- **Instant setup** — no file copying, regardless of project size
- **diff/apply workflow** — same review process as `:copy` mode
- **Instant reset** — clearing the upper layer is immediate

**Tradeoffs vs `:copy`:**
- No snapshot isolation — changes to the original host directory are visible for files the agent hasn't modified
- Container must be running for `yoloai diff` and `yoloai apply` (auto-started if stopped)
- Requires `CAP_SYS_ADMIN` capability in the container
- Docker backend only (not available with `--backend seatbelt` or `--backend tart`)

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
| `aider` | `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`, `DEEPSEEK_API_KEY`, `OPENROUTER_API_KEY` | Aider — AI pair programming in your terminal |
| `claude` (default) | `ANTHROPIC_API_KEY` | Anthropic Claude Code — AI coding assistant |
| `codex` | `CODEX_API_KEY`, `OPENAI_API_KEY` | OpenAI Codex — AI coding agent |
| `gemini` | `GEMINI_API_KEY` | Google Gemini CLI — AI coding assistant |
| `opencode` | `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`, + others | OpenCode — open-source AI coding agent (auth check is a warning, not error) |
| `test` | (none) | Test agent — launches a shell for testing sandbox behavior |
| `shell` | (none) | Shell agent — launches an interactive shell (no AI agent) |

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

# Codex model aliases
yoloai new task ./my-project --agent codex --model default  # gpt-5.3-codex
yoloai new task ./my-project --agent codex --model spark    # gpt-5.3-codex-spark
yoloai new task ./my-project --agent codex --model mini     # codex-mini-latest
```

### Custom Model Aliases

You can define custom model aliases or pin versions in your global config (`~/.yoloai/config.yaml`):

```bash
# Pin sonnet to a specific version
yoloai config set model_aliases.sonnet claude-sonnet-4-20250514

# Create a custom shortcut
yoloai config set model_aliases.fast claude-haiku-4-latest

# Use your custom alias
yoloai new task ./my-project --model fast

# View configured aliases
yoloai config get model_aliases

# Remove an alias
yoloai config reset model_aliases.fast
```

User-defined aliases take priority over built-in agent aliases. Full model names always work regardless of aliases.

### Local Models (Aider, OpenCode)

Aider and OpenCode support local model servers like Ollama and LM Studio. To use them without a cloud API key, set the appropriate environment variable:

```bash
export OLLAMA_API_BASE=http://host.docker.internal:11434
```

Or configure it persistently:

```bash
yoloai config set env.OLLAMA_API_BASE http://host.docker.internal:11434
```

The `host.docker.internal` hostname allows the container to reach services running on the host machine. OpenCode also supports GitHub Copilot credentials, AWS Bedrock, Azure OpenAI, and Vertex AI — see `yoloai system agents opencode` for details.

## Global Flags

| Flag | Description |
|------|-------------|
| `-v` | Verbose output |
| `-q` | Quiet output |
| `--no-color` | Disable color output |
| `--json` | Output as JSON for scripting and CI |

### JSON Output

Use `--json` on any command to get machine-readable JSON output:

```bash
yoloai ls --json                           # all sandboxes as JSON array
yoloai sandbox info mybox --json           # sandbox details as JSON object
yoloai diff mybox --json                   # diff result as JSON
yoloai destroy mybox --json --yes          # action result (--yes required)
yoloai config get --json                   # full config as JSON
```

Errors are output to stderr as `{"error": "message"}`. Interactive commands (`attach`, `exec`) reject `--json`.

## Key Flags

### Creating sandboxes

`--backend <name>` selects the runtime backend (`docker`, `tart`, or `seatbelt`). Available on `new`, `build`, and `setup`. Lifecycle commands (`start`, `stop`, etc.) read the backend from the sandbox's `meta.json` automatically.

```bash
# Prompt (headless — agent runs the task autonomously)
yoloai new task ./project --prompt "refactor the auth module"
yoloai new task ./project --prompt-file instructions.md
echo "fix the build" | yoloai new task ./project --prompt -   # from stdin

# Create without starting the container
yoloai new task ./project --no-start

# Auto-attach after creation
yoloai new task ./project --attach

# Skip confirmation prompts
yoloai new task ./project --yes

# Replace an existing sandbox with the same name
yoloai new task ./project --replace

# Pass extra arguments directly to the agent CLI
yoloai new task ./project -- --allowedTools "Edit,Write,Bash"

# Network isolation (allow only agent API traffic)
yoloai new task ./project --network-isolated

# Allow extra domains in network-isolated mode
yoloai new task ./project --network-allow api.example.com

# Disable network access entirely
yoloai new task ./project --network-none

# Use a profile
yoloai new task ./project --profile go-dev

# Resource limits
yoloai new task ./project --cpus 4 --memory 8g

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

# Resume a stopped sandbox (re-feed original prompt with context)
yoloai start task --resume
yoloai start task -a            # start and auto-attach

# Restart agent (stop + start, preserving workspace)
yoloai restart task
yoloai restart task -a          # restart and auto-attach

# Reset workdir (re-copy from original, restart agent)
yoloai reset task
yoloai reset task --clean       # also wipe agent memory
yoloai reset task --no-prompt   # don't re-send prompt
yoloai reset task --no-restart  # keep agent running, reset workspace in-place
yoloai reset task -a            # reset, restart, and auto-attach
```

### Reviewing changes

```bash
# Full diff
yoloai diff task

# Summary only (files changed, insertions, deletions)
yoloai diff task --stat

# List individual agent commits
yoloai diff task --log

# Diff for a specific commit or range
yoloai diff task abc123
yoloai diff task abc123..def456

# Filter to specific paths
yoloai diff task -- src/handler.go
```

### Applying changes

```bash
# Default: preserve individual commits (git am --3way)
yoloai apply task

# Squash all changes into a single unstaged patch
yoloai apply task --squash

# Export .patch files for manual curation
yoloai apply task --patches ./my-patches

# Skip uncommitted (WIP) changes, only apply commits
yoloai apply task --no-wip

# Apply specific commits by ref
yoloai apply task abc123 def456

# Force apply even if host repo has uncommitted changes
yoloai apply task --force
```

## How It Works

1. **`yoloai new`** copies your project into `~/.yoloai/sandboxes/<name>/work/`, creates a git baseline commit, and launches a Docker container running the agent.

2. **The agent works inside the container** on the copy. Your original files are never touched.

3. **`yoloai diff`** shows a git diff between the baseline and the agent's current state — including new files, deletions, and binary changes.

4. **`yoloai apply`** generates a patch and applies it to your original directory. It does a dry-run check first and prompts for confirmation.

5. **`yoloai reset`** re-copies the original and resets the baseline, letting you retry the same task from scratch.

## Configuration

On first run, yoloAI creates two config files:
- `~/.yoloai/config.yaml` — global settings (tmux_conf, model_aliases)
- `~/.yoloai/profiles/base/config.yaml` — profile defaults (agent, model, backend, env, etc.)

Use `yoloai config` to view and change settings (keys are automatically routed to the correct file):

```bash
# Show all settings with effective values (defaults + overrides)
yoloai config get

# Get a specific setting
yoloai config get backend

# Change a setting
yoloai config set backend tart

# Reset a setting to its default
yoloai config reset backend

# Remove an env var
yoloai config reset env.OLLAMA_API_BASE
```

### Settings

| Key | Default | Description |
|-----|---------|-------------|
| `agent` | `claude` | Agent to use: `aider`, `claude`, `codex`, `gemini`, `opencode` |
| `model` | (empty) | Model name or alias passed to the agent |
| `backend` | `docker` | Runtime backend: `docker`, `tart`, `seatbelt` |
| `tart.image` | (empty) | Custom base VM image for tart backend |
| `env.<NAME>` | (empty) | Environment variable forwarded to container |
| `agent_args.<AGENT>` | (empty) | Default CLI args for an agent (e.g., `agent_args.aider`) |
| `resources.cpus` | (empty) | CPU limit (e.g., `4`, `2.5`) |
| `resources.memory` | (empty) | Memory limit (e.g., `8g`, `512m`) |
| `network.isolated` | `false` | Enable network isolation by default |
| `network.allow` | (empty) | Additional domains to allow (additive with agent defaults) |
| `tmux_conf` | (set by setup) | Tmux config mode (global config) |
| `profile` | (empty) | Default profile name (used when `--profile` is not specified) |
| `model_aliases.<alias>` | (empty) | Custom model alias (global config) |

Operational state (`setup_complete`) is stored in `~/.yoloai/state.yaml`, separate from config.

Agent resolution: `new` uses `--agent` flag > `agent` in config > `"claude"`.

Model resolution: `new` uses `--model` flag > `model` in config > `""` (empty = agent's default model).

Backend resolution: `new`/`build`/`setup` use `--backend` flag > `backend` in config > `"docker"`. Lifecycle commands read the backend from the sandbox's `meta.json`, falling back to config default.

Agent args: persistent default CLI args for specific agents. Inserted between the model flag and CLI passthrough (`--` args), so passthrough always takes precedence. Example: `yoloai config set agent_args.aider "--no-auto-commits --no-pretty"`. Profile `agent_args` merge with base config (per-agent key, profile wins on conflict).

### Agent Files

`agent_files` controls additional files seeded into a sandbox's `agent-state/` directory on first creation. Useful for sharing agent configuration (e.g., a team CLAUDE.md, custom settings) across sandboxes without modifying host agent state.

Two forms are supported:

**String form** — a base directory. yoloAI derives the agent-specific subdirectory automatically:

```yaml
# In ~/.yoloai/profiles/base/config.yaml
agent_files: "${HOME}"
# Claude sandbox → copies from ~/.claude/ (minus excluded files)
# Gemini sandbox → copies from ~/.gemini/
```

**List form** — explicit file/directory paths copied into `agent-state/`:

```yaml
# In ~/.yoloai/profiles/base/config.yaml
agent_files:
  - ~/.claude/settings.json
  - /shared/team-configs/CLAUDE.md
```

Key behaviors:
- Files already placed by SeedFiles (auth credentials, container settings) are never overwritten.
- Each agent excludes session data and caches (e.g., Claude excludes `projects/`, `statsig/`, `todos/`, `.credentials.json`, `*.log`).
- Files are only seeded on first creation. Tracked via `state.json` — `reset --clean` resets the flag so files are re-seeded on next start.
- Agents without a state directory (aider, test, shell) are silently skipped.
- In profiles, `agent_files` uses replacement semantics (child completely replaces parent).

You can also edit the config files directly — `config set` preserves comments and formatting.

## Sandbox State

All sandbox state lives on the host at `~/.yoloai/sandboxes/<name>/`:

```
~/.yoloai/sandboxes/<name>/
  meta.json       # sandbox config (paths, mode, baseline SHA, backend)
  state.json      # per-sandbox state (agent_files_initialized, etc.)
  config.json     # container entrypoint config
  prompt.txt      # initial prompt (if provided)
  log.txt         # tmux session log
  agent-state/    # agent's persistent state (e.g., ~/.claude/, ~/.gemini/)
  files/          # bidirectional file exchange (mounted at /yoloai/files/)
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

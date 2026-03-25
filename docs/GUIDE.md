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
| `yoloai clone <source> <dest>` | Clone a sandbox (copy state to a new sandbox) |
| `yoloai reset <name>` | Re-copy workdir and reset to original state |
| `yoloai destroy <name>...` | Stop and remove sandboxes |

**Inspection**

| Command | Description |
|---------|-------------|
| `yoloai system` | System information and management |
| `yoloai system info` | Show version, paths, disk usage, backend availability |
| `yoloai system agents [name]` | List available agents |
| `yoloai system backends [name]` | List available runtime backends |
| `yoloai system build [profile]` | Build or rebuild the base image (`--backend`, `--secret`, `--all`) |
| `yoloai system check` | Verify prerequisites for CI/CD pipelines |
| `yoloai system doctor` | Show capability status for all backends and isolation modes |
| `yoloai system prune` | Remove orphaned resources (`--dry-run`, `--yes`, `--all`, `--backend`) |
| `yoloai system setup` | Re-run interactive first-run setup |
| `yoloai sandbox` (alias: `sb`) | Sandbox inspection |
| `yoloai sandbox list` | List sandboxes and their status |
| `yoloai sandbox <name> info` | Show sandbox configuration and state |
| `yoloai sandbox <name> prompt` | Show sandbox prompt |
| `yoloai sandbox <name> log` | Show session log |
| `yoloai sandbox <name> exec <cmd>` | Run a command inside the sandbox |
| `yoloai sandbox <name> allow <domain>...` | Allow additional domains in an isolated sandbox |
| `yoloai sandbox <name> allowed` | Show allowed domains for a sandbox |
| `yoloai sandbox <name> deny <domain>...` | Remove domains from the allowlist |
| `yoloai ls` | List sandboxes (shortcut for `sandbox list`) |
| `yoloai log <name>` | Show sandbox log (shortcut for `sandbox log`) |
| `yoloai exec <name> <cmd>` | Run a command inside a sandbox (shortcut for `sandbox exec`) |

**Admin**

| Command | Description |
|---------|-------------|
| `yoloai profile create <name>` | Create a new profile with scaffold |
| `yoloai profile list` | List profiles |
| `yoloai profile info <name>` | Show merged profile configuration |
| `yoloai profile delete <name>` | Delete a profile (`--yes` to skip confirmation) |
| `yoloai files <name> put <file/glob>...` | Copy files into sandbox exchange directory |
| `yoloai files <name> get <file/glob>... [-o dir]` | Copy files from sandbox exchange directory |
| `yoloai files <name> ls [glob]...` | List files in sandbox exchange directory |
| `yoloai files <name> rm <glob>...` | Remove files from sandbox exchange directory |
| `yoloai files <name> path` | Print host path to sandbox exchange directory |
| `yoloai config get [key]` | Print configuration values (all settings or a specific key) |
| `yoloai config set <key> <value>` | Set a configuration value |
| `yoloai config reset <key>` | Reset a configuration value to its default |
| `yoloai x [extension]` | Run a user-defined extension (alias: `ext`) |
| `yoloai help [topic]` | Show help topics (agents, workflow, workdirs, config, security, flags, extensions) |
| `yoloai system completion <shell>` | Generate shell completion (bash/zsh/fish/powershell) |
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
| `:overlay` | `./my-app:overlay` | Overlay mount. Instant setup, diff/apply workflow. Docker/Podman only. |
| `:rw` | `./my-app:rw` | Live bind-mount. Changes are immediate — no diff/apply needed. |

```bash
# Default: safe isolated copy
yoloai new task1 ./my-project

# Overlay: instant setup for large projects (Docker/Podman only)
yoloai new task2 ./large-project:overlay

# Live mount (use with caution — agent writes directly to your files)
yoloai new task3 ./my-project:rw
```

### Why Copies, Not Git Worktrees?

Many AI coding tools use `git worktree` for isolation — it's instant and space-efficient. yoloAI uses full copies instead because worktrees have fundamental problems for sandboxed agents:

- **Missing files.** Worktrees only include tracked files. Gitignored directories like `node_modules/`, build artifacts, and `.env` files are excluded — agents can't build or test without them.
- **Host pollution.** Worktree branches and commits are visible in your original repo. Agent git operations clutter your ref history.
- **Git-only.** Worktrees require a git repository. yoloAI supports any directory.
- **Shared object store.** Worktrees share the `.git` directory with the host repo, weakening isolation inside the container.

For large projects where copy speed is a concern, use `:overlay` mode — it provides instant setup with the same isolation and diff/apply workflow.

### Overlay Mode

`:overlay` uses Linux kernel overlayfs inside the container to mount the original directory as a read-only lower layer, with agent changes captured in an upper layer. This provides:

- **Instant setup** — no file copying, regardless of project size
- **diff/apply workflow** — same review process as `:copy` mode
- **Instant reset** — clearing the upper layer is immediate

**Tradeoffs vs `:copy`:**
- No snapshot isolation — changes to the original host directory are visible for files the agent hasn't modified
- Container must be running for `yoloai diff` and `yoloai apply` (auto-started if stopped)
- Requires `CAP_SYS_ADMIN` capability in the container
- Not available with `--backend seatbelt` or `--backend tart`

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
| `claude` (default) | `ANTHROPIC_API_KEY` or `CLAUDE_CODE_OAUTH_TOKEN` | Anthropic Claude Code — AI coding assistant |
| `codex` | `CODEX_API_KEY`, `OPENAI_API_KEY` | OpenAI Codex — AI coding agent |
| `gemini` | `GEMINI_API_KEY` | Google Gemini CLI — AI coding assistant |
| `opencode` | `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`, + others | OpenCode — open-source AI coding agent (auth check is a warning, not error) |
| `test` | (none) | Bash shell for testing and development |
| `shell` | All agents' keys | Bash shell with all agents' credentials seeded |

You can select a model using shorthand aliases or full model names. Aliases are agent-specific — use `yoloai system agents <name>` to see the full list for each agent.

```bash
# Claude model aliases
yoloai new task ./my-project --model sonnet   # claude-sonnet-4-latest
yoloai new task ./my-project --model opus     # claude-opus-4-latest
yoloai new task ./my-project --model haiku    # claude-haiku-4-latest
yoloai new task ./my-project --model claude-sonnet-4-20250514  # exact model

# Gemini model aliases
yoloai new task ./my-project --agent gemini --model pro          # gemini-2.5-pro
yoloai new task ./my-project --agent gemini --model flash        # gemini-2.5-flash
yoloai new task ./my-project --agent gemini --model preview-pro  # gemini-3.1-pro-preview
yoloai new task ./my-project --agent gemini --model preview-flash # gemini-3-flash-preview

# Codex model aliases
yoloai new task ./my-project --agent codex --model default  # gpt-5.3-codex
yoloai new task ./my-project --agent codex --model spark    # gpt-5.3-codex-spark
yoloai new task ./my-project --agent codex --model mini     # codex-mini-latest

# OpenCode model aliases (requires provider/model format)
yoloai new task ./my-project --agent opencode --model sonnet        # anthropic/claude-sonnet-4-5-latest
yoloai new task ./my-project --agent opencode --model gpt4o         # openai/gpt-4o
yoloai new task ./my-project --agent opencode --model mini          # openai/gpt-4o-mini
yoloai new task ./my-project --agent opencode --model openai/gpt-4o # explicit provider/model format
```

**OpenCode Requirements:** OpenCode requires models in `provider/model` format (e.g., `openai/gpt-4o`, `anthropic/claude-sonnet-4-20250514`). Providers must be configured first — launch OpenCode and run `/connect` to set up authentication, then use `/models` to see available models.

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

### OpenCode Setup

OpenCode requires provider configuration on your **host machine** before use. yoloAI automatically copies your OpenCode config into containers.

**First-time setup (on your host machine):**

1. **Install OpenCode** on your host (not in yoloAI):
   ```bash
   npm install -g @opencode/cli
   ```

2. **Configure providers** on your host:
   ```bash
   opencode  # Launch OpenCode
   # Then run /connect and choose:
   #   - ChatGPT Plus/Pro: Authenticate via browser (recommended)
   #   - API Keys: Enter your OpenAI, Anthropic, or other provider API key
   #   - Local Models: Configure Ollama, LM Studio, etc.
   # Run /models to verify what's available
   ```

3. **Use with yoloAI** — Your config is automatically seeded into containers:
   ```bash
   yoloai new task ./my-project --agent opencode --model openai/gpt-4o
   yoloai new task ./my-project --agent opencode --model anthropic/claude-sonnet-4-20250514
   ```

**Config files seeded:** `~/.local/share/opencode/auth.json`, `~/.config/opencode/.opencode.json`, GitHub Copilot credentials (if configured).

**Alternative: Use API keys** — Instead of running `/connect`, set environment variables:
```bash
export OPENAI_API_KEY=sk-...
yoloai new task ./my-project --agent opencode --model openai/gpt-4o
```

**Supported Providers:** OpenAI, Anthropic, Google Gemini, Groq, OpenRouter, XAI, GitHub Copilot, AWS Bedrock, Azure OpenAI, Vertex AI, and 75+ others via custom configuration.

### Local Models (Aider)

Aider supports local model servers like Ollama and LM Studio. To use them without a cloud API key, set the appropriate environment variable:

```bash
export OLLAMA_API_BASE=http://host.docker.internal:11434
```

Or configure it persistently:

```bash
yoloai config set env.OLLAMA_API_BASE http://host.docker.internal:11434
```

The `host.docker.internal` hostname allows the container to reach services running on the host machine.

## Global Flags

| Flag | Description |
|------|-------------|
| `-h`, `--help` | Show help for any command |
| `-v`, `--verbose` | Increase verbosity (repeatable: `-v` debug, `-vv` reserved) |
| `-q`, `--quiet` | Decrease verbosity (repeatable: `-q` warnings only, `-qq` errors only) |
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

### Exit Codes

| Code  | Meaning |
|-------|---------|
| 0     | Success |
| 1     | General error |
| 2     | Usage error (bad arguments, missing required args) |
| 3     | Configuration error (bad config file, missing required config) |
| 4     | Active work — sandbox has unapplied changes or a running agent; use `--yes` to force or `yoloai apply` first |
| 5     | Dependency error — required software not installed or not running (e.g., Docker daemon) |
| 6     | Platform error — operation not possible on this OS/arch (e.g., tart on Linux) |
| 7     | Auth error — credentials completely absent (e.g., `ANTHROPIC_API_KEY` not set) |
| 8     | Permission error — access denied by policy (e.g., user not in docker group) |
| 128+N | Terminated by signal N (POSIX convention) |
| 130   | Interrupted by SIGINT / Ctrl+C |

## Key Flags

### Creating sandboxes

`--backend <name>` selects the runtime backend (`docker`, `podman`, `tart`, or `seatbelt`). Available on `new`, `build`, and `setup`. Lifecycle commands (`start`, `stop`, etc.) read the backend from the sandbox's `environment.json` automatically.

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

# Replace even if unapplied changes exist
yoloai new task ./project --force

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

# Use base image even if config sets a default profile
yoloai new task ./project --no-profile

# Resource limits
yoloai new task ./project --cpus 4 --memory 8g

# Expose a container port to the host
yoloai new task ./project --port 3000:3000

# Pass environment variables to the sandbox
yoloai new task ./project --env MY_VAR=value --env OTHER=val2

# Debug entrypoint issues
yoloai new task ./project --debug
```

### Managing sandboxes

```bash
# Skip confirmation prompts
yoloai destroy task --yes
yoloai apply task --yes

# Stop/destroy all sandboxes
yoloai stop --all
yoloai destroy --all --yes

# Destroy sandboxes matching a wildcard pattern
yoloai destroy test*         # destroy all sandboxes starting with "test"
yoloai destroy *-old --yes   # skip confirmation for sandboxes ending with "-old"

# Resume a stopped sandbox (re-feed original prompt with context)
yoloai start task --resume
yoloai start task -a            # start and auto-attach
yoloai start task --prompt "continue with the API changes"  # new prompt
yoloai start task --prompt-file next-steps.md               # prompt from file

# Restart agent (stop + start, preserving workspace)
yoloai restart task
yoloai restart task -a          # restart and auto-attach
yoloai restart task --resume    # restart with resume prompt
yoloai restart task --prompt "now add tests"  # restart with new prompt
yoloai restart task --prompt-file next-steps.md  # restart with prompt from file

# Attach with resume (restart agent with resume prompt, then attach)
yoloai attach task --resume

# Clone a sandbox
yoloai clone source-box dest-box
yoloai clone source-box dest-box -a           # clone, start, and attach
yoloai clone source-box dest-box --no-start   # clone without starting
yoloai clone source-box dest-box --force      # replace existing destination
yoloai clone source-box dest-box --prompt "continue with tests"

# Reset workdir (in-place by default, agent stays running)
yoloai reset task
yoloai reset task --keep-cache  # preserve cache directory
yoloai reset task --keep-files  # preserve files directory
yoloai reset task --no-prompt   # don't re-send prompt
yoloai reset task --restart     # stop and restart container
yoloai reset task --clear-state  # wipe agent state and restart
yoloai reset task --restart -a  # restart and auto-attach
yoloai reset task --debug       # debug entrypoint issues on restart
```

### Reviewing changes

```bash
# Full diff
yoloai diff task

# Summary only (files changed, insertions, deletions)
yoloai diff task --stat

# List changed file names only
yoloai diff task --name-only

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

# Dry-run: check what would be applied without making changes
yoloai apply task --dry-run

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
- `~/.yoloai/defaults/config.yaml` — user defaults (agent, model, isolation, env, etc.)

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
| `os` | `linux` | Guest OS: `linux` (default), `mac` (requires macOS host) |
| `container_backend` | (auto-detect) | Linux container backend: `docker`, `podman`, or `""` (auto-detect, prefers docker) |
| `isolation` | `container` | Isolation mode: `container` (runc), `container-enhanced` (gVisor), `vm` (Kata+QEMU), `vm-enhanced` (Kata+Firecracker) |
| `tart.image` | (empty) | Custom base VM image for tart backend |
| `env.<NAME>` | (empty) | Environment variable forwarded to container |
| `agent_args.<AGENT>` | (empty) | Default CLI args for an agent (e.g., `agent_args.aider`) |
| `resources.cpus` | (empty) | CPU limit (e.g., `4`, `2.5`) |
| `resources.memory` | (empty) | Memory limit (e.g., `8g`, `512m`) |
| `network.isolated` | `false` | Enable network isolation by default |
| `network.allow` | (empty) | Additional domains to allow (additive with agent defaults) |
| `auto_commit_interval` | `0` | Auto-commit interval in seconds (0 = disabled) |
| `mounts` | (empty) | Additional bind mounts (list of `host:container` paths) |
| `ports` | (empty) | Port mappings (list of `host:container` ports) |
| `cap_add` | (empty) | Additional Linux capabilities (list, e.g. `SYS_PTRACE`) |
| `devices` | (empty) | Device mappings (list of `/dev/` paths) |
| `setup` | (empty) | Shell commands to run inside the container on first start (list) |
| `tmux_conf` | (set by setup) | Tmux config mode (global config) |
| `model_aliases.<alias>` | (empty) | Custom model alias (global config) |

Operational state (`setup_complete`) is stored in `~/.yoloai/state.yaml`, separate from config.

Agent resolution: `new` uses `--agent` flag > `agent` in config > `"claude"`.

Model resolution: `new` uses `--model` flag > `model` in config > `""` (empty = agent's default model).

Container backend resolution: `new`/`build`/`setup` use `--backend` flag > `container_backend` in config > auto-detect (prefers docker over podman). Valid values: `docker`, `podman`. Isolation level: `--isolation` flag > `isolation` in config > `"container"`. Lifecycle commands read the backend from the sandbox's `environment.json`.

Agent args: persistent default CLI args for specific agents. Inserted between the model flag and CLI passthrough (`--` args), so passthrough always takes precedence. Example: `yoloai config set agent_args.aider "--no-auto-commits --no-pretty"`. Profile `agent_args` merge with base config (per-agent key, profile wins on conflict).

### Agent Files

`agent_files` controls additional files seeded into a sandbox's `agent-state/` directory on first creation. Useful for sharing agent configuration (e.g., a team CLAUDE.md, custom settings) across sandboxes without modifying host agent state.

Two forms are supported:

**String form** — a base directory. yoloAI derives the agent-specific subdirectory automatically:

```yaml
# In ~/.yoloai/defaults/config.yaml
agent_files: "${HOME}"
# Claude sandbox → copies from ~/.claude/ (minus excluded files)
# Gemini sandbox → copies from ~/.gemini/
```

**List form** — explicit file/directory paths copied into `agent-state/`:

```yaml
# In ~/.yoloai/defaults/config.yaml
agent_files:
  - ~/.claude/settings.json
  - /shared/team-configs/CLAUDE.md
```

Key behaviors:
- Files already placed by SeedFiles (auth credentials, container settings) are never overwritten.
- Each agent excludes session data and caches (e.g., Claude excludes `projects/`, `statsig/`, `todos/`, `.credentials.json`, `*.log`).
- Files are only seeded on first creation. Tracked via `sandbox-state.json` — `reset --clear-state` resets the flag so files are re-seeded on next start.
- Agents without a state directory (aider, test, shell) are silently skipped.
- In profiles, `agent_files` uses replacement semantics (child completely replaces parent).

You can also edit the config files directly — `config set` preserves comments and formatting.

## Sandbox State

All sandbox state lives on the host at `~/.yoloai/sandboxes/<name>/`:

```
~/.yoloai/sandboxes/<name>/
  environment.json   # sandbox config (paths, mode, baseline SHA, backend)
  sandbox-state.json # per-sandbox state (agent_files_initialized, etc.)
  runtime-config.json # container entrypoint config
  prompt.txt         # initial prompt (if provided)
  log.txt            # tmux session log
  agent-runtime/     # agent's persistent state (e.g., ~/.claude/, ~/.gemini/)
  files/             # bidirectional file exchange (mounted at /yoloai/files/)
  cache/             # agent cache — HTTP responses, cloned repos (mounted at /yoloai/cache/)
  work/              # isolated copy of your project
```

Containers are ephemeral — if removed, `yoloai start` recreates them from `environment.json`. Your work and agent state persist.

### Shared Files Directory

The `files/` directory is a bidirectional exchange between you and the agent. It's mounted read-write inside the sandbox (at `/yoloai/files/` for Docker, or the sandbox path for seatbelt) and managed via the `yoloai files` command:

```bash
# Pass reference material to the agent
yoloai files mybox put error-log.txt spec.pdf

# Retrieve artifacts the agent produced
yoloai files mybox get report.md

# List what's in the exchange directory
yoloai files mybox ls

# Direct host access (for scripts, rsync, etc.)
yoloai files mybox path
```

Files here never appear in `yoloai diff` or `yoloai apply` — they live outside the work directory. Use this for anything the agent needs to see or anything you want to retrieve from the agent: logs, specs, screenshots, generated reports, exported files, etc.

### Cache Directory

The `cache/` directory gives the agent a persistent scratch space for data that speeds up its work but you don't need to see. It's mounted read-write inside the sandbox (at `/yoloai/cache/` for Docker, or the sandbox path for seatbelt).

The agent is instructed to use this directory to:

- **Cache HTTP responses** — avoid re-fetching the same URL repeatedly, which risks rate limiting or bans during research tasks.
- **Clone remote repos** — `git clone --depth 1` into the cache to search a codebase locally instead of fetching individual files over HTTPS.
- **Store reusable data** — downloaded archives, parsed documentation, intermediate results, etc.

The cache directory persists across agent restarts (`yoloai stop` / `yoloai start`) but is destroyed with `yoloai destroy`. It's cleared by default on `yoloai reset` (use `--keep-cache` to preserve it).

## Security

- **Originals are protected.** Workdirs use `:copy` mode by default — the agent works on an isolated copy, never your original files. Opt into `:rw` explicitly for live access.
- **Dangerous directory detection.** Refuses to mount `$HOME`, `/`, or system directories. Append `:force` to override (e.g., `$HOME:force`).
- **Dirty repo warning.** Prompts if your workdir has uncommitted git changes, so you don't lose work.
- **Credential injection via files.** API keys are mounted as read-only files at `/run/secrets/`, not passed as environment variables. Temp files on the host are cleaned up after container start. Some agents support additional credential sources — for example, on macOS, yoloai checks the macOS Keychain for Claude Code OAuth credentials (service `Claude Code-credentials`). If you're logged in via `claude` CLI, yoloai will automatically detect your credentials even without `~/.claude/.credentials.json` on disk.

### Claude Code Authentication

Claude Code supports two authentication methods:

- **`ANTHROPIC_API_KEY`** — For API plan users. API keys don't expire and work reliably in sandboxes.
- **`CLAUDE_CODE_OAUTH_TOKEN`** — For Pro/Max/Team subscription users. Run `claude setup-token` on your host machine to generate a long-lived OAuth token, then export it:

  ```bash
  export CLAUDE_CODE_OAUTH_TOKEN=<token>
  ```

  **Why not use `~/.claude/.credentials.json` directly?** The OAuth credentials from `claude login` contain short-lived access tokens (~30 minute expiry) and single-use refresh tokens. When the token expires, the first client to refresh it invalidates the refresh token for all other copies. This means running Claude Code on your host machine will break any sandbox sessions using the same credentials, and running multiple sandboxes will break each other. `CLAUDE_CODE_OAUTH_TOKEN` avoids this entirely — it's a long-lived token that requires no refresh and works across any number of concurrent sandboxes.
- **Non-root execution.** Containers run as a non-root user with UID/GID matching your host user.

### Isolation Modes

On Docker and Podman backends, you can upgrade the OCI runtime for stronger isolation:

| Mode | Requires | Description |
|------|----------|-------------|
| `container` | nothing | Default `runc` — Linux namespaces and cgroups |
| `container-enhanced` | `runsc` in PATH + Docker runtime registered | gVisor userspace kernel — intercepts syscalls in userspace, no KVM needed |
| `vm` | KVM + Kata Containers | Kata Containers with QEMU VM |
| `vm-enhanced` | KVM + Kata Containers + Firecracker | Kata Containers with Firecracker microVM |

```bash
# Set gVisor as default for all sandboxes
yoloai config set isolation container-enhanced

# Or specify per sandbox
yoloai new task . --isolation container-enhanced
```

Isolation modes are silently ignored on non-container backends (tart, seatbelt). Using `--isolation` explicitly on an incompatible backend is an error.

**Setup — gVisor (`container-enhanced`):**

1. Install the `runsc` binary: see [gVisor installation docs](https://gvisor.dev/docs/user_guide/install/)
2. Register it with Docker in `/etc/docker/daemon.json`:
   ```json
   {"runtimes": {"runsc": {"path": "/usr/local/bin/runsc"}}}
   ```
3. Restart the Docker daemon: `sudo systemctl restart docker`

Installing the binary alone is not enough — Docker must also have the runtime registered. yoloai checks both.

**Setup — Kata Containers (`vm`, `vm-enhanced`):**

1. Install Kata Containers 3.x: see [Kata releases](https://github.com/kata-containers/kata-containers/releases)
2. `vm` uses the containerd backend directly (not Docker) — ensure containerd is configured with Kata shims.
3. `vm-enhanced` additionally requires Firecracker.

**Incompatibilities:**

- **`container-enhanced` + `:overlay` directories:** gVisor's VFS2 kernel does not support overlayfs mounts inside the container. Use `:copy` or `:rw` instead — yoloai will error if you combine them.
- **`vm` and `vm-enhanced`:** Use the containerd backend, not Docker or Podman. Selected automatically when `--isolation vm` or `vm-enhanced` is used on Linux.

## Toolchain Support

### Swift Package Manager

Swift PM works transparently in Seatbelt sandboxes. yoloAI automatically adds the `--disable-sandbox` flag to `swift build` and `swift test` commands via a shell wrapper function, since macOS sandboxes don't support nesting.

**How it works:**

- Swift PM normally runs `sandbox-exec` to securely compile Package.swift manifests
- macOS doesn't allow nested sandboxing (one sandbox inside another)
- yoloAI creates a shell wrapper that automatically adds `--disable-sandbox` for build/test commands
- The outer Seatbelt sandbox still provides isolation; we're just disabling Swift PM's inner sandbox

**Usage:**

Run Swift commands normally inside Seatbelt sandboxes:

```bash
yoloai shell --backend seatbelt my-project ~/path/to/swift-project:rw
# Inside the sandbox:
swift build
swift test
swift run
```

The wrapper only affects `build` and `test` commands; other Swift commands work unchanged.

**Cache isolation:**

Swift PM's cache is redirected to `<sandbox>/cache/swiftpm/` to maintain isolation and avoid permission errors. You may see warnings about being unable to access the host's user-level cache — these are expected and harmless.

**First build:**

If you previously built the project outside the sandbox, clean the build directory first to avoid path mismatch errors:

```bash
rm -rf .build
swift build
```

## Development

```bash
make build          # Build binary with version info
make test           # Run unit tests
make lint           # Run golangci-lint
make integration    # Run integration tests (requires Docker)
make clean          # Remove binary
```

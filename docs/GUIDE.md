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
| `yoloai system disk` | Report on-disk usage per backend (sandboxes + image cache + snapshots) |
| `yoloai doctor` | Capability status for all backends + a read-only repair advisory (see [Repair & cleanup](#repair--cleanup)) |
| `yoloai system prune` | Clean up leftover state across all backends (`--dry-run`, `--yes`, `--images`) — see [Repair & cleanup](#repair--cleanup) |
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

### Build Artifact Exclusion

When creating sandboxes in `:copy` mode, yoloAI automatically excludes common build artifacts that would cause compilation failures inside the container. Build tools like Swift, Xcode, and npm embed hardcoded absolute paths in their build artifacts. When copied to the sandbox, these mismatched paths break builds.

**Example:** Swift Package Manager creates precompiled header (PCH) files in `.build/` containing paths like `/Users/user/project/.build/...`. When this directory is copied to the sandbox at `/Users/user/.yoloai/library/sandboxes/name/work/...`, Swift rejects the PCH files:

```
error: PCH was compiled with module cache path '/Users/user/project/.build/...'
but the path is currently '/Users/user/.yoloai/library/sandboxes/name/work/.build/...'
```

**Excluded artifacts:**
- `.build/` — Swift Package Manager build artifacts
- `DerivedData/` — Xcode derived data and build caches
- `node_modules/` — Node.js dependencies (native modules can have hardcoded paths; also improves copy performance)
- `__pycache__/` — Python bytecode cache
- `*.xcworkspace/xcuserdata/` — Xcode workspace user-specific data
- `*.xcodeproj/xcuserdata/` — Xcode project user-specific data

These directories are regenerated automatically when the agent runs build commands inside the sandbox.

**Important notes:**
- Exclusion only applies to `:copy` mode — `:overlay` and `:rw` modes see all files
- The exclusion list is conservative to avoid false positives (e.g., generic names like `build/`, `target/`, or `env/` are NOT excluded)
- If you need to exclude additional project-specific artifacts, please file an issue on GitHub

## Auxiliary Directories

You can mount additional directories alongside your workdir using the `-d` / `--dir` flag (repeatable). Auxiliary directories are read-only by default.

```bash
# Read-only auxiliary directories (default)
yoloai new mybox . -d /path/to/lib

# Writable bind-mount auxiliary directory (live edits)
yoloai new mybox . -d /path/to/lib:rw

# Custom mount point for workdir
yoloai new mybox ./app=/opt/app

# Custom mount point for auxiliary directory
yoloai new mybox . -d ./lib=/opt/lib

# Multiple auxiliary directories
yoloai new mybox ./app -d ./shared-lib -d ./common-types
```

By default, directories are mounted at their original absolute host paths (mirrored paths). Use `=<path>` to mount at a custom container path instead.

Auxiliary directories accept only `:rw` (live bind) or default `:ro`. `:copy` and `:overlay` are workdir-only — `yoloai diff` and `yoloai apply` operate on the workdir, and the multi-directory diff/apply surface was removed during beta (see [BREAKING-CHANGES](BREAKING-CHANGES.md)). If you need to track changes in a second project, run a separate sandbox for it.

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

You can define custom model aliases or pin versions in your global config (`~/.yoloai/library/config.yaml`):

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
| 9     | Sandbox locked — another process holds the per-sandbox lock; `yoloai sandbox <name> unlock` if stale |
| 10    | Disk space exhausted — host filesystem full; `yoloai system disk` + `yoloai system prune` (or `--images`) to recover |
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

# Apply as a single unstaged patch instead of replaying commits
yoloai apply task --no-commit

# Export .patch files for manual curation
yoloai apply task --patches ./my-patches

# Also bring across uncommitted edits as unstaged files (default: commits only)
yoloai apply task --include-uncommitted

# Apply specific commits by ref
yoloai apply task abc123 def456

# Dry-run: check what would be applied without making changes
yoloai apply task --dry-run

# Also transfer git tags the agent created
yoloai apply task --tags

# Skip the confirmation prompt
yoloai apply task --yes
```

## How It Works

1. **`yoloai new`** copies your project into `~/.yoloai/library/sandboxes/<name>/work/`, creates a git baseline commit, and launches a Docker container running the agent.

2. **The agent works inside the container** on the copy. Your original files are never touched.

3. **`yoloai diff`** shows a git diff between the baseline and the agent's current state — including new files, deletions, and binary changes.

4. **`yoloai apply`** generates a patch and applies it to your original directory. It does a dry-run check first and prompts for confirmation.

5. **`yoloai reset`** re-copies the original and resets the baseline, letting you retry the same task from scratch.

## Configuration

On first run, yoloAI creates its data directory at `~/.yoloai/`, split into two areas:
- `~/.yoloai/library/` — engine state: sandboxes, profiles, caches, and your config files
  - `~/.yoloai/library/config.yaml` — global settings (tmux_conf, model_aliases)
  - `~/.yoloai/library/defaults/config.yaml` — user defaults (agent, model, isolation, env, etc.)
- `~/.yoloai/cli/` — CLI application state (extensions, first-run flag)

> **Upgrading from an older layout:** yoloAI no longer migrates your data directory automatically. If you upgrade from a pre-bifurcation install (a flat `~/.yoloai/` with `config.yaml` and `sandboxes/` directly inside it), the binary stops and tells you to run `yoloai system migrate` once — it relocates your existing data into the `library/` + `cli/` layout. The command is idempotent, so re-run it if it's interrupted.

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
| `isolation` | `container` | Isolation mode: `container` (runc), `container-enhanced` (gVisor), `container-privileged` (Docker `--privileged`, use for Docker-in-Docker), `vm` (Kata+QEMU), `vm-enhanced` (Kata+Firecracker) |
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
| `tmux_conf` | `default+host` | Tmux config mode (global config): `default+host` sources yoloAI defaults then your `~/.tmux.conf`; `host` uses only yours |
| `model_aliases.<alias>` | (empty) | Custom model alias (global config) |

Agent resolution: `new` uses `--agent` flag > `agent` in config > `"claude"`.

Model resolution: `new` uses `--model` flag > `model` in config > `""` (empty = agent's default model).

Container backend resolution: `new`/`build`/`setup` use `--backend` flag > `container_backend` in config > auto-detect (prefers docker over podman). Valid values: `docker`, `podman`. Isolation level: `--isolation` flag > `isolation` in config > `"container"`. Lifecycle commands read the backend from the sandbox's `environment.json`.

Agent args: persistent default CLI args for specific agents. Inserted between the model flag and CLI passthrough (`--` args), so passthrough always takes precedence. Example: `yoloai config set agent_args.aider "--no-auto-commits --no-pretty"`. Profile `agent_args` merge with base config (per-agent key, profile wins on conflict).

### Agent Files

`agent_files` controls additional files seeded into a sandbox's `agent-state/` directory on first creation. Useful for sharing agent configuration (e.g., a team CLAUDE.md, custom settings) across sandboxes without modifying host agent state.

Two forms are supported:

**String form** — a base directory. yoloAI derives the agent-specific subdirectory automatically:

```yaml
# In ~/.yoloai/library/defaults/config.yaml
agent_files: "${HOME}"
# Claude sandbox → copies from ~/.claude/ (minus excluded files)
# Gemini sandbox → copies from ~/.gemini/
```

**List form** — explicit file/directory paths copied into `agent-state/`:

```yaml
# In ~/.yoloai/library/defaults/config.yaml
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

All sandbox state lives on the host at `~/.yoloai/library/sandboxes/<name>/`:

```
~/.yoloai/library/sandboxes/<name>/
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

### Reclaiming Disk

Container backends accumulate disk over time — image layers, overlayfs snapshots, BuildKit cache, retired volumes. yoloai exposes two commands for this:

- **`yoloai system disk`** — read-only report of what each available backend is consuming, plus the size of `~/.yoloai/library/sandboxes/`. The `CACHE` column is reclaimable with no rebuild; the `IMAGES` column needs `--images` and forces a rebuild. Run this when `df` looks unhappy to identify which backend is the culprit.
- **`yoloai system prune`** — always reclaims each backend's *no-rebuild* cache: build cache, retired volumes, and dangling images. Crucially, this does **not** force a rebuild — the base image is kept, so the next `yoloai new` still runs without rebuilding. This is the safe default.
- **`yoloai system prune --images`** — additionally removes each backend's base/profile images. This forces yoloai-base to rebuild on the next `yoloai new`, so expect a multi-minute first run afterwards. Prune always runs across every available backend; `--dry-run` previews what would be removed.

`--images` is intentionally aggressive: backends don't tag their content by who created it, so it removes ALL image content the backend tracks, not only yoloai's. On a host dedicated to yoloai (CI, dev VM) that's exactly what you want; on a shared workstation, prefer the backend's own prune (`docker system prune`, `podman system prune`, etc.) so you don't nuke unrelated projects' images.

## Repair & cleanup

Over time a yoloai install accumulates cruft: orphaned containers/VMs from crashed runs, stale lock files, leftover temp dirs, and the occasional half-created or corrupt sandbox dir. yoloai cleans this up itself — you don't need to know where any of it lives.

**`yoloai doctor`** is the place to start. It's read-only — it never deletes anything — and reports four things alongside the backend capability status:

- **Reclaimable now** — orphaned resources, lock files, temp dirs, and never-initialized sandbox dirs. Fix: `yoloai system prune`.
- **Reclaimable space** — split into two tiers: cached data freed by plain `yoloai system prune` (no rebuild), and base images freed by `yoloai system prune --images` (forces a base rebuild).
- **Unreviewed work** — broken sandbox dirs that still hold changes the agent made. yoloai refuses to touch these; review with `yoloai diff <name>` and remove with `yoloai destroy <name>` once you're done.
- **Trash** — dirs that were quarantined rather than deleted (see below).

**`yoloai system prune`** does the actual cleanup. It classifies every sandbox dir by *how recoverable it is* and never deletes anything that might hold your work:

- **Deleted** — zero-stakes cruft: orphaned backend resources, stale locks, temp dirs, and never-initialized sandbox dirs (no metadata and no work directory).
- **Refused** — dirs where yoloai can still detect uncommitted work (a dirty git copy, or a non-empty overlay upper layer). These are reported and left untouched; you review and remove them yourself.
- **Quarantined to trash** — dirs whose metadata is corrupt or too new to read, but with no detectable work. Rather than guess, yoloai moves them to `~/.yoloai/library/trash/<name>` so nothing is lost.

Use `--dry-run` to preview, and `--yes` to skip confirmation prompts (including the trash-deletion prompt).

### Trash and recovery

Quarantined dirs go to `~/.yoloai/library/trash/`. There's no dedicated restore command — a quarantined dir is just a normal directory, so recover it with `mv`:

```bash
mv ~/.yoloai/library/trash/<name> ~/.yoloai/library/sandboxes/<name>
```

`yoloai system prune` reports how much is in the trash and offers to empty it. Because trash may hold something you wanted, it always asks first (answer no to keep it); `--yes` empties it without prompting. Nothing else ever deletes the trash automatically.

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

On Docker and Podman backends, you can select isolation modes that trade security for capability. The full spectrum, from least to most restricted:

| Mode | Requires | Description |
|------|----------|-------------|
| `container-privileged` | nothing (standard Docker) | Docker `--privileged` — all capabilities, seccomp=unconfined, AppArmor=unconfined. Use for Docker-in-Docker and Compose stacks. |
| `container` | nothing | Default `runc` — Linux namespaces and cgroups |
| `container-enhanced` | `runsc` registered with the daemon (and on the daemon's filesystem) | gVisor userspace kernel — intercepts syscalls in userspace, no KVM needed; works on Linux and macOS hosts (runs in the backend's Linux VM) |
| `vm` | KVM + Kata Containers | Kata Containers with QEMU VM |
| `vm-enhanced` | KVM + Kata Containers + Firecracker | Kata Containers with Firecracker microVM |

```bash
# Set gVisor as default for all sandboxes
yoloai config set isolation container-enhanced

# Or specify per sandbox
yoloai new task . --isolation container-enhanced

# Allow Docker-in-Docker or Compose stacks inside the sandbox
yoloai new task . --isolation container-privileged
```

Isolation modes are silently ignored on non-container backends (tart, seatbelt). Using `--isolation` explicitly on an incompatible backend is an error.

**`container-privileged` — Docker-in-Docker and Compose:**

This mode passes Docker's `--privileged` flag. The container receives all Linux capabilities, disables seccomp, and disables AppArmor/SELinux enforcement. Use it when the agent needs to run Docker or Compose stacks inside the sandbox.

Docker CE and the Compose plugin are pre-installed. `fuse-overlayfs` is configured as the default storage driver in `/etc/docker/daemon.json` — `overlay2` fails with EPERM in nested container environments.

**Security stance:** `--privileged` grants the agent near-full host kernel access. A rogue agent could escape the container. Use this mode only with agents and prompts you trust. It protects against accidental damage (copy/diff/apply workflow, clean teardown) but not against deliberate malicious behavior.

Works on macOS too, via the Docker/Podman backend's Linux VM (Docker Desktop, OrbStack, or Podman Machine all run `--privileged` containers): use `--os linux --isolation container-privileged`. It is only unavailable with `--os mac`, where the native Seatbelt/Tart backends have no privileged mode. No additional host prerequisites are needed on a standard Linux machine. On Proxmox LXC hosts, ensure `features: nesting=1` is set on the LXC container.

**Setup — gVisor (`container-enhanced`):**

`runsc` must live wherever the Docker daemon runs and be registered as a runtime in that daemon's `daemon.json`. yoloai's system check adapts to the daemon location: on a Linux host it verifies the binary on `PATH` **and** the registration; on a macOS/Windows host (where the daemon runs in a VM) it can only verify registration — the daemon checks the binary itself at container-create time.

*Linux host (local daemon):*

1. Install the `runsc` binary: see [gVisor installation docs](https://gvisor.dev/docs/user_guide/install/)
2. Register it with Docker in `/etc/docker/daemon.json`:
   ```json
   {"runtimes": {"runsc": {"path": "/usr/local/bin/runsc"}}}
   ```
3. Restart the Docker daemon: `sudo systemctl restart docker`

*macOS host (Docker Desktop / OrbStack — daemon in a Linux VM):* gVisor runs on macOS, including Apple Silicon (the `systrap` platform needs no nested virtualization). `runsc` must be installed **inside the VM** (not on the macOS host) and registered there. Use `--os linux --isolation container-enhanced`; `--os mac` (Seatbelt/Tart) has no gVisor.

> **OrbStack caveat:** OrbStack symlinks the VM's `/tmp` to the macOS `/private/tmp` over virtiofs, which collides with gVisor's hard-coded `/tmp` sandbox chroot and fails with `cannot read client sync file: EOF` (chroot: `expected to open /tmp, but found /private/tmp`). Docker Desktop's LinuxKit VM has a normal `/tmp` and is unaffected. See `docs/contributors/backend-idiosyncrasies.md`.

**Setup — Kata Containers (`vm`, `vm-enhanced`):**

1. Install Kata Containers 3.x: see [Kata releases](https://github.com/kata-containers/kata-containers/releases)
2. `vm` uses the containerd backend directly (not Docker) — ensure containerd is configured with Kata shims.
3. `vm-enhanced` additionally requires Firecracker.

**Incompatibilities:**

- **`container-enhanced` + `:overlay` directories:** gVisor's VFS2 kernel does not support overlayfs mounts inside the container. Use `:copy` or `:rw` instead — yoloai will error if you combine them.
- **`vm` and `vm-enhanced`:** Use the containerd backend, not Docker or Podman. Selected automatically when `--isolation vm` or `vm-enhanced` is used on Linux.
- **`container-privileged`:** Requires a container backend (Docker/Podman). Available on both Linux and macOS hosts via that backend's Linux VM; only unavailable with `--os mac` (Seatbelt/Tart have no privileged mode).

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
yoloai new my-project ~/path/to/swift-project:rw --backend seatbelt --agent shell --attach
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

### iOS Simulator Testing (Tart only)

Tart VMs automatically mount Xcode from your Mac. **Simulator runtimes must be copied or downloaded locally in the VM.**

**Prerequisites (on host Mac):**
- Xcode installed at `/Applications/Xcode.app`

**How it works:**

1. Create any Tart VM: `yoloai new mysandbox`
2. If host has Xcode → VM automatically mounts it and runs initial setup
3. Copy runtime from host OR download it in the VM
4. iOS testing works

**Setup: Copy runtime from host (fastest if host has it)**

```bash
# Check what runtimes are available on host
yoloai exec embsdk -- ls "/Volumes/My Shared Files/m-Volumes/"
# Example output: iOS_23B86 (iOS 26.1), tvOS_23J579 (tvOS 26.1), etc.

# Copy iOS runtime to VM (adjust iOS_* to match your host's runtime)
yoloai exec embsdk -- bash <<'EOF'
sudo mkdir -p /Library/Developer/CoreSimulator/Profiles/Runtimes
sudo ditto "/Volumes/My Shared Files/m-Volumes/iOS_23B86/Library/Developer/CoreSimulator/Profiles/Runtimes/iOS 26.1.simruntime" \
  /Library/Developer/CoreSimulator/Profiles/Runtimes/
sudo cp "/Volumes/My Shared Files/m-Volumes/iOS_23B86/Library/Developer/CoreSimulator/Profiles/Runtimes/iOS 26.1.simruntime/Contents/Info.plist" \
  "/Library/Developer/CoreSimulator/Profiles/Runtimes/iOS 26.1.simruntime/Contents/"
EOF

# Verify runtime is available
yoloai exec embsdk -- xcrun simctl list runtimes
```

**Setup: Download runtime in VM (if host doesn't have it)**

```bash
# Download iOS runtime (this takes 10-15 minutes and ~8-16GB)
yoloai exec embsdk -- xcodebuild -downloadPlatform iOS

# Verify runtime is available
yoloai exec embsdk -- xcrun simctl list runtimes
```

**Example: Running iOS tests**

```bash
# Create simulator device
yoloai exec embsdk -- xcrun simctl create "Test iPhone" \
  com.apple.CoreSimulator.SimDeviceType.iPhone-17-Pro \
  com.apple.CoreSimulator.SimRuntime.iOS-26-1

# Run iOS unit tests
yoloai exec embsdk -- xcodebuild test \
  -scheme MyApp \
  -destination 'platform=iOS Simulator,name=Test iPhone' \
  -resultBundlePath /tmp/test-results

# Run tests on multiple platforms (requires copying/downloading tvOS runtime too)
yoloai exec embsdk -- xcodebuild test -scheme MyApp \
  -destination 'platform=iOS Simulator,name=Test iPhone' \
  -destination 'platform=tvOS Simulator,name=Apple TV 4K'
```

**Disk usage:**
- Xcode mounted from host: 0GB (saves ~11GB)
- iOS runtime local: ~8-16GB per OS version
- Simulator devices/caches: ~2-5GB
- **Total: ~25-40GB** (with 1-2 runtimes) vs ~100GB (Xcode + runtime both local)

**Why runtimes must be local:**
CoreSimulator cannot discover runtimes from VirtioFS mounts. Even with proper symlinks, `xcrun simctl` won't see mounted runtimes. This is a limitation of how CoreSimulator interacts with VirtioFS.

**Troubleshooting:**

*simctl hangs or shows no runtimes:*
1. Verify PrivateFrameworks symlink exists: `yoloai exec embsdk -- ls -la /Library/Developer/PrivateFrameworks`
2. If missing, restart the VM (setup script creates it on boot)
3. Ensure runtime was copied to `/Library/Developer/CoreSimulator/Profiles/Runtimes/`

*Xcode tools not found:*
1. Verify Xcode installed on host: `ls /Applications/Xcode.app`
2. Restart VM to trigger auto-mount and setup

## Development

```bash
make build          # Build binary with version info
make test           # Run unit tests
make lint           # Run golangci-lint
make integration    # Run integration tests (requires Docker)
make clean          # Remove binary
```

# yoloAI

Sandboxed AI coding agent runner. Run AI coding agents inside disposable Docker containers with a copy/diff/apply workflow.

Your originals are never touched — the agent works on an isolated copy, you review what changed with `yoloai diff`, and choose what to keep with `yoloai apply`.

> **MVP — early access.** This is a minimal viable release with the core copy/diff/apply workflow and Claude Code support. It works, but expect rough edges. More agents, profiles, network isolation, and other features are on the [roadmap](#roadmap). [Issues and feedback welcome.](https://github.com/kstenerud/yoloai/issues)

## Why yoloAI?

AI coding agents are powerful, but giving them live access to your files is risky — one bad command and your work is gone. Existing sandboxing tools either provide isolation without a review workflow (you have to figure out what changed yourself), or sync changes immediately with no way to approve them first.

yoloAI gives you both: **full isolation** and **a clean review step**.

- **Your files are protected.** The agent works on an isolated copy inside a Docker container. Your originals are never modified.
- **Git-based review.** `yoloai diff` shows you exactly what the agent changed. `yoloai apply` patches your project only when you're ready.
- **Run agents without permission prompts.** The container is disposable, so agents run with their sandbox-bypass flags (e.g., `--dangerously-skip-permissions`). No more clicking "approve" on every file edit.
- **Persistent agent state.** Each sandbox keeps its own agent session history and configuration, so the agent retains context across stops and restarts.
- **Retry from scratch.** `yoloai reset` re-copies your original and lets the agent try again — no leftover state from the failed attempt.

## Requirements

- Go 1.24+ (to build from source)
- Docker

## Install

```bash
git clone https://github.com/kstenerud/yoloai.git
cd yoloai
make build
sudo mv yoloai /usr/local/bin/  # or add to PATH
```

On first run, yoloAI automatically builds its base Docker image (~2 min) and creates `~/.yoloai/`.

## Quick Start

```bash
# 1. Authenticate — either set an API key:
export ANTHROPIC_API_KEY=sk-ant-...
#    ...or if you've already run `claude login`, yoloAI will automatically
#    copy your OAuth credentials (~/.claude/.credentials.json) into the sandbox.

# 2. Create a sandbox and watch the agent work
#    (auto-attaches to the tmux session; Ctrl-B, D to detach)
yoloai new fix-bug ./my-project --prompt "fix the failing tests"

# 3. Review what changed
yoloai diff fix-bug

# 4. Apply changes back to your original project
yoloai apply fix-bug

# 5. Clean up
yoloai destroy fix-bug
```

### Interactive mode (no prompt)

If you omit `--prompt`, yoloAI starts Claude Code in interactive mode and auto-attaches so you can work directly in the sandboxed session:

```bash
yoloai new explore ./my-project
# You're now inside Claude Code, running in the container.
# Ctrl-B, D to detach; yoloai attach explore to reconnect.
```

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

This MVP ships with Claude Code as the only supported agent. The architecture is agent-agnostic — more agents are planned (see [Roadmap](#roadmap)).

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

  mounts:
    - ~/.gitconfig:/home/yoloai/.gitconfig:ro

  ports: []

  resources:
    cpus: 4
    memory: 8g
```

You can edit this file to change the default agent, add persistent bind mounts (like SSH config or tool configs), adjust resource limits, or set default port mappings. These defaults apply to all new sandboxes.

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

- **Originals are protected.** Workdirs use `:copy` mode by default — the agent works on an isolated copy, never your original files. Opt into `:rw` explicitly for live access.
- **Dangerous directory detection.** Refuses to mount `$HOME`, `/`, or system directories. Append `:force` to override (e.g., `$HOME:force`).
- **Dirty repo warning.** Prompts if your workdir has uncommitted git changes, so you don't lose work.
- **Credential injection via files.** API keys are mounted as read-only files at `/run/secrets/`, not passed as environment variables. Temp files on the host are cleaned up after container start.
- **Non-root execution.** Containers run as a non-root user with UID/GID matching your host user.

## Roadmap

yoloAI is under active development. The current MVP covers the core copy/diff/apply workflow with Claude Code. Here's what's planned next:

**More agents**
- OpenAI Codex support (the architecture is agent-agnostic — adding an agent is a definition, not a rewrite)
- Community-requested agents (Aider, Goose, etc.)

**Network isolation**
- Domain-based allowlisting — let the agent reach its API but nothing else (`--network-isolated`, `--network-allow <domain>`)
- Proxy sidecar for fine-grained traffic control

**Profiles**
- Reusable environment definitions (`~/.yoloai/profiles/<name>/`) with user-supplied Dockerfiles
- Per-profile config: custom mounts, resource limits, environment variables

**Overlayfs copy strategy**
- Instant sandbox setup using overlayfs instead of full copy (space-efficient, fast for large repos)

**Other**
- Auxiliary directory mounts (`-d` flag for read-only dependencies)
- Custom mount points (`=<path>` syntax)
- Auto-commit intervals for crash recovery
- Config file generation (`yoloai config generate`)
- User-defined extensions (`yoloai x <extension>`)

## Development

```bash
make build          # Build binary with version info
make test           # Run unit tests
make lint           # Run golangci-lint
make integration    # Run integration tests (requires Docker)
make clean          # Remove binary
```

## License

[MIT](LICENSE)

# yoloAI Usage Guide

Full reference for commands, flags, configuration, and internals. For a quick overview, see the [README](../README.md).

## Commands

**Core Workflow**

| Command | Description |
|---------|-------------|
| `yoloai new <name> [workdir]` | Create and start a sandbox |
| `yoloai run <name> <workdir>` | Create and run a sandbox headlessly to completion |
| `yoloai attach <name>` | Attach to the agent's tmux session |
| `yoloai diff <name>` | Show changes the agent made |
| `yoloai apply <name>` | Apply changes back to original directory |

**Lifecycle**

| Command | Description |
|---------|-------------|
| `yoloai stop <name>...` | Stop sandboxes (preserving state) |
| `yoloai start <name>` | Start a stopped sandbox |
| `yoloai restart <name>` | Restart the agent in an existing sandbox |
| `yoloai wait <name>` | Block until the agent is idle or exits (`--for idle\|exit`, `--timeout`) |
| `yoloai clone <source> <dest>` | Clone a sandbox (copy state to a new sandbox) |
| `yoloai reset <name>` | Re-copy workdir and reset to original state |
| `yoloai destroy <name>...` | Stop and remove sandboxes |
| `yoloai baseline advance <name>` | Move the sandbox baseline to the current HEAD of the work copy |
| `yoloai baseline set <name> <sha>` | Move the sandbox baseline to a specific commit SHA |
| `yoloai baseline log <name>` | Show the sandbox work copy commit log, marking the current baseline |

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
| `yoloai system prune` | Clean up leftover state across all backends (`--dry-run`, `--yes`, `--images`, `--stale-bases`, `--trash`) — see [Repair & cleanup](#repair--cleanup) |
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

**MCP Server (experimental)**

| Command | Description |
|---------|-------------|
| `yoloai mcp serve` | Start the yoloAI MCP server on stdio |
| `yoloai mcp proxy <name> [workdir] -- <cmd>` | Run an MCP server inside a sandbox and proxy its stdio |

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
| `:copy` | `./my-app` (default) | Isolated copy. Agent changes are reviewed via diff/apply. **Honors `.gitignore`** — ignored files (secrets, build output) are not copied. |
| `:copy-all` | `./my-app:copy-all` | Like `:copy` but copies **everything, including gitignored files**. Opt-out of gitignore-honoring; use when the sandbox genuinely needs an ignored file (e.g. a local `.env`). |
| `:rw` | `./my-app:rw` | Live bind-mount. Changes are immediate — no diff/apply needed. |

**`.gitignore` is honored for `:copy`.** When the source is a git repository, only
files git considers part of the project are copied (`git ls-files --cached --others
--exclude-standard`) — so files you deliberately excluded from your repo (`.env`,
`*.pem`, `.aws/`, `credentials.json`, local config) never enter the sandbox where the
agent could read them, and they never show up in diffs. Nested `.gitignore`, `!`
negation, and `.git/info/exclude` are all respected. Non-git directories have no
gitignore semantics and are copied in full. To include ignored files anyway, use
`:copy-all`. (Honoring happens host-side when the work copy is created, so it applies
to every backend — VM backends like tart copy the already-filtered work copy into the
VM.)

```bash
# Default: safe isolated copy
yoloai new task1 ./my-project

# Live mount (use with caution — agent writes directly to your files)
yoloai new task3 ./my-project:rw
```

### Why Copies, Not Git Worktrees?

Many AI coding tools use `git worktree` for isolation — it's instant and space-efficient. yoloAI uses full copies instead because worktrees have fundamental problems for sandboxed agents:

- **Missing files.** Worktrees only include tracked files. Gitignored directories like `node_modules/`, build artifacts, and `.env` files are excluded — agents can't build or test without them.
- **Host pollution.** Worktree branches and commits are visible in your original repo. Agent git operations clutter your ref history.
- **Git-only.** Worktrees require a git repository. yoloAI supports any directory.
- **Shared object store.** Worktrees share the `.git` directory with the host repo, weakening isolation inside the container.

> **Note:** An earlier `:overlay` mode used in-container overlayfs for instant setup, but
> it required granting the agent container `CAP_SYS_ADMIN` — a host-escape primitive on
> rootful Docker — so it was retired (D109). Existing overlay sandboxes are auto-converted
> to `:copy` by `yoloai system migrate`. Use `:copy` instead.

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
- This hardcoded list is applied **on top of `.gitignore` honoring** (see Workdir Modes): in a git repo, anything you've gitignored (commonly `node_modules/`, `.build/`, etc.) is already excluded by git; this list is the safety net for non-git directories and for repos that commit such artifacts. To copy everything regardless, use `:copy-all`.
- Exclusion only applies to `:copy`/`:copy-all` mode — `:rw` mode sees all files
- The exclusion list is conservative to avoid false positives (e.g., generic names like `build/`, `target/`, or `env/` are NOT excluded)
- If you need to exclude additional project-specific artifacts, gitignore them (honored by `:copy`) or file an issue on GitHub

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

Auxiliary directories accept only `:rw` (live bind) or default `:ro`. `:copy` is workdir-only — `yoloai diff` and `yoloai apply` operate on the workdir, and the multi-directory diff/apply surface was removed during beta (see [BREAKING-CHANGES](BREAKING-CHANGES.md)). If you need to track changes in a second project, run a separate sandbox for it.

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
yoloai destroy mybox --json                # action result
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
| 4     | Active work — sandbox has unapplied changes; use `--abandon-unapplied` to discard them or `yoloai apply` first |
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

`--backend <name>` selects the runtime backend (`docker`, `podman`, `apple`, `tart`, or `seatbelt`). Available on `new`, `build`, and `setup`. Lifecycle commands (`start`, `stop`, etc.) read the backend from the sandbox's `environment.json` automatically.

On macOS you can also name a specific Docker provider — `--backend orbstack` or `--backend docker-desktop` — and yoloAI pins the docker backend to that provider's daemon socket, so the choice is honored even when both are installed. `apple` is the [Apple `container`](#apple-container-backend-macos) backend (per-container Linux VMs).

```bash
# Prompt (headless — agent runs the task autonomously)
yoloai new task ./project --prompt "refactor the auth module"
yoloai new task ./project --prompt-file instructions.md
echo "fix the build" | yoloai new task ./project --prompt -   # from stdin

# Create without starting the container
yoloai new task ./project --no-start

# Auto-attach after creation
yoloai new task ./project --attach

# Proceed even if the workdir has uncommitted changes (otherwise refused)
yoloai new task ./project --allow-dirty

# Replace an existing sandbox with the same name
yoloai new task ./project --replace

# Replace even if unapplied changes exist (discards the unreviewed work)
yoloai new task ./project --abandon-unapplied

# Pass extra arguments directly to the agent CLI
yoloai new task ./project -- --allowedTools "Edit,Write,Bash"

# Network isolation (allow only agent API traffic)
yoloai new task ./project --network-isolated

# Allow extra domains in network-isolated mode
yoloai new task ./project --network-allow api.example.com

# Disable network access entirely
yoloai new task ./project --network-none

# Credential brokering is on by default (key stays host-side). Opt out / require it:
yoloai new task ./project --no-broker   # deliver the key directly into the sandbox
yoloai new task ./project --broker      # require brokering (error if unsupported)

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

### Headless run

`yoloai run` is an alternate entry point to `yoloai new` for scripted and CI use: it creates a sandbox, delivers the prompt in the agent's own headless mode, and optionally blocks until the agent finishes.

```bash
# Fire-and-forget: returns as soon as the agent is launched
yoloai run mybox ./project --prompt "fix the build"

# Block until the agent finishes (exit code reflects the agent's outcome)
yoloai run mybox ./project --prompt "fix the build" --wait

# Block and destroy the sandbox after the agent finishes (implies --wait)
yoloai run mybox ./project --prompt "fix the build" --rm

# Read the prompt from a file
yoloai run mybox ./project --prompt-file instructions.md --wait

# Run interactively instead of headless — useful for monitoring/debugging
yoloai run mybox ./project --prompt "fix the build" --tty
```

`--prompt` or `--prompt-file` is required. `--rm` implies `--wait`. Without `--wait`, `yoloai run` returns as soon as the agent is launched and the sandbox persists for later `diff`/`apply`. With `--wait`, a failed agent causes `yoloai run` to exit non-zero, so `yoloai run … --wait && next-step` works. All `yoloai new` flags are accepted (see [Creating sandboxes](#creating-sandboxes)).

### Managing sandboxes

```bash
# Destroy a sandbox that has unapplied changes (otherwise refused; a running agent alone is fine)
yoloai destroy task --abandon-unapplied
yoloai apply task --yes       # --yes confirms the apply you invoked

# Stop/destroy all sandboxes
yoloai stop --all
yoloai destroy --all --abandon-unapplied

# Destroy sandboxes matching a wildcard pattern
yoloai destroy test*         # destroy all sandboxes starting with "test"
yoloai destroy *-old --abandon-unapplied   # discard unreviewed work in matched sandboxes

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
yoloai clone source-box dest-box --overwrite  # overwrite existing destination
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

### When the agent exits (fall-to-shell)

When an agent process exits inside a sandbox — you quit it (e.g. Claude's
`/exit`), or it ends its run — the pane does **not** die. It drops to an
interactive shell in the workdir, and the sandbox status becomes `done` (so
`yoloai wait` and status reporting still work). The shell keeps the box usable:
inspect what the agent changed, run `git`, or relaunch the agent.

A hint in the pane points at **`yoloai-resume`** — run it from that shell to
relaunch the agent in place:

```bash
yoloai-resume   # run inside the fall-to-shell shell (yoloai attach <name> to get there)
```

- For agents with native conversation resume (Claude → `--continue`),
  `yoloai-resume` continues the **prior conversation**.
- For agents without native resume, it starts a **fresh** session and says so —
  it never claims a resume that didn't happen.

**Codex note:** Codex has no native session-continuation mechanism. Both `yoloai-resume` (in-sandbox) and `yoloai restart --resume` / `yoloai start --resume` (host-side) always start a **fresh** Codex session — the original prompt is re-fed as context, but no prior conversation state is carried over.

This is distinct from the host-side `yoloai restart --resume` / `yoloai start
--resume` (above), which relaunch from *outside* the sandbox and re-feed the
original prompt. `yoloai-resume` runs *inside* the shell you're already in and,
where supported, resumes the live conversation rather than re-feeding a prompt.

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

### Managing the sandbox baseline

`yoloai baseline` corrects the baseline SHA when it falls out of sync — for example after a stash-pop conflict or a non-contiguous selective apply.

```bash
# Advance the baseline to the current HEAD of the work copy
yoloai baseline advance mybox

# Set the baseline to a specific commit (short SHA accepted)
yoloai baseline set mybox abc12345

# Show the commit log of the sandbox work copy, marking the current baseline
yoloai baseline log mybox
```

The baseline is the reference commit used by `yoloai diff` and `yoloai apply` to determine what the agent changed. After a normal apply, yoloai advances it automatically — `baseline` is the recovery tool when it gets out of sync. The `set` output includes an undo hint (`yoloai baseline set <name> <old-sha>`) in case you need to reverse the move.

### MCP Server (experimental)

**Experimental:** `yoloai mcp` is functional but under-tested; its tool surface and flags may change.

`yoloai mcp serve` starts the yoloAI MCP server on stdin/stdout, exposing sandbox operations as tools for outer agents (Claude Desktop, VS Code Copilot, etc.) driving a two-layer agentic workflow. Tools: `sandbox_create`, `sandbox_status`, `sandbox_list`, `sandbox_destroy`, `sandbox_diff`, `sandbox_diff_file`, `sandbox_log`, `sandbox_input`, `sandbox_reset`, `sandbox_files_list`, `sandbox_files_read`, `sandbox_files_write`.

To use with Claude Desktop, add to `~/.claude.json`:

```json
{
  "mcpServers": {
    "yoloai": {
      "command": "yoloai",
      "args": ["mcp", "serve"]
    }
  }
}
```

`yoloai mcp proxy` runs an MCP server inside a sandbox and proxies its stdio, injecting `sandbox_diff` into the tool surface. The sandbox is created automatically if it does not exist; existing sandboxes are reused. Path placeholders in the inner command are expanded from sandbox metadata: `{workdir}` (primary working directory), `{files}`, `{cache}`, `{dir:N}` (Nth auxiliary dir, 0-indexed).

```bash
# New sandbox — workdir required
yoloai mcp proxy mybox /path/to/project -- npx -y @modelcontextprotocol/server-filesystem {workdir}

# Reuse existing sandbox
yoloai mcp proxy mybox -- npx -y @modelcontextprotocol/server-filesystem {workdir}
```

Proxy flags: `--agent <name>` (default: `idle`), `--model`, `--profile`, `-d`/`--dir`, `--replace` (destroy and recreate if the sandbox exists), `--backend`.

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
| `tart.image` | (empty → host-matched) | Custom base VM image for tart backend. Empty = the Cirrus `macos-<codename>-base` matching the host's macOS (so the guest can run the host's Xcode), falling back to the newest macOS yoloai knows. Set it to pin a specific macOS — e.g. stay on an older base, or jump to a brand-new one (`ghcr.io/cirruslabs/macos-tahoe-base:latest`) the day Cirrus publishes it, without waiting for a yoloai release |
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

Container backend resolution: `new`/`build`/`setup` use `--backend` flag > `container_backend` in config > auto-detect. Valid values: `docker`, `podman`, and on macOS the container-system aliases `orbstack` / `docker-desktop` (the docker backend pinned to that provider's socket). Auto-detect on macOS prefers `apple` (when installed) for the VM-isolation default, otherwise a container backend (docker > podman); on Linux it prefers docker > podman. Isolation level: `--isolation` flag > `isolation` in config > `"container"` — except on macOS where an installed `apple` makes the unspecified default `vm`. Lifecycle commands read the backend from the sandbox's `environment.json`.

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

- **`yoloai system disk`** — read-only report of what each available backend is consuming, plus the size of `~/.yoloai/library/sandboxes/`. The `CACHE` column is reclaimable with no rebuild; the `IMAGES` column needs `--images` and forces a rebuild; the `STALE` column (Tart, after a host-macOS upgrade) is reclaimable with `--stale-bases` and no rebuild. Run this when `df` looks unhappy to identify which backend is the culprit.
- **`yoloai system prune`** — always reclaims each backend's *no-rebuild* cache: build cache, retired volumes, and dangling images. Crucially, this does **not** force a rebuild — the base image is kept, so the next `yoloai new` still runs without rebuilding. This is the safe default.
- **`yoloai system prune --images`** — additionally removes each backend's base/profile images. This forces yoloai-base to rebuild on the next `yoloai new`, so expect a multi-minute first run afterwards. Prune always runs across every available backend; `--dry-run` previews what would be removed.
- **`yoloai system prune --stale-bases`** — removes *superseded* base images left behind on the Tart backend when the host's macOS (and thus the matched base codename) changed. Unlike `--images`, this never touches the *current* base, so it forces no rebuild — it just reclaims the old macOS base (~30 GB) you upgraded away from. `yoloai doctor` flags these and prints this command.

`--images` is intentionally aggressive: backends don't tag their content by who created it, so it removes ALL image content the backend tracks, not only yoloai's. On a host dedicated to yoloai (CI, dev VM) that's exactly what you want; on a shared workstation, prefer the backend's own prune (`docker system prune`, `podman system prune`, etc.) so you don't nuke unrelated projects' images.

## Repair & cleanup

Over time a yoloai install accumulates cruft: orphaned containers/VMs from crashed runs, stale lock files, leftover temp dirs, and the occasional half-created or corrupt sandbox dir. yoloai cleans this up itself — you don't need to know where any of it lives.

**`yoloai doctor`** is the place to start. It's read-only — it never deletes anything — and reports four things alongside the backend capability status:

- **Reclaimable now** — orphaned resources, lock files, temp dirs, and never-initialized sandbox dirs. Fix: `yoloai system prune`.
- **Reclaimable space** — split into tiers: cached data freed by plain `yoloai system prune` (no rebuild), base images freed by `yoloai system prune --images` (forces a base rebuild), and — on Tart, after a host-macOS upgrade — superseded base images freed by `yoloai system prune --stale-bases` (no rebuild).
- **Unreviewed work** — broken sandbox dirs that still hold changes the agent made. yoloai refuses to touch these; review with `yoloai diff <name>` and remove with `yoloai destroy <name>` once you're done.
- **Trash** — dirs that were quarantined rather than deleted (see below).

**`yoloai system prune`** does the actual cleanup. It classifies every sandbox dir by *how recoverable it is* and never deletes anything that might hold your work:

- **Deleted** — zero-stakes cruft: orphaned backend resources, stale locks, temp dirs, and never-initialized sandbox dirs (no metadata and no work directory).
- **Refused** — dirs where yoloai can still detect uncommitted work (a dirty git copy). These are reported and left untouched; you review and remove them yourself.
- **Quarantined to trash** — dirs whose metadata is corrupt or too new to read, but with no detectable work. Rather than guess, yoloai moves them to `~/.yoloai/library/trash/<name>` so nothing is lost.

Use `--dry-run` to preview, `--yes` to skip the reclaim confirmation prompt, and `--trash` to also empty the trash (see below).

### Trash and recovery

Quarantined dirs go to `~/.yoloai/library/trash/`. There's no dedicated restore command — a quarantined dir is just a normal directory, so recover it with `mv`:

```bash
mv ~/.yoloai/library/trash/<name> ~/.yoloai/library/sandboxes/<name>
```

`yoloai system prune` reports how much is in the trash but never empties it on its own — because trash may hold something you wanted, emptying it is opt-in via the `--trash` selector flag (parallel to `--images`). A plain prune, even with `--yes`, only reports the trash; `--yes` suppresses the reclaim prompt but never widens the scope to the trash. Nothing else ever deletes the trash automatically.

## Security

- **Originals are protected.** Workdirs use `:copy` mode by default — the agent works on an isolated copy, never your original files. Opt into `:rw` explicitly for live access.
- **Dangerous directory detection.** Refuses to mount `$HOME`, `/`, or system directories. Append `:force` to override (e.g., `$HOME:force`).
- **Dirty repo warning.** Prompts if your workdir has uncommitted git changes, so you don't lose work.
- **Credential brokering (default).** For supported setups the agent's LLM API key is held **host-side** and never enters the sandbox — see [Credential Brokering](#credential-brokering) below. Credentials that aren't brokered (other agents, subscription tokens, unsupported backends) are delivered as files instead (next bullet).
- **Credential injection via files.** Non-brokered API keys are mounted as read-only files at `/run/secrets/`, not passed as environment variables. Temp files on the host are cleaned up after container start. Some agents support additional credential sources — for example, on macOS, yoloai checks the macOS Keychain for Claude Code OAuth credentials (service `Claude Code-credentials`). If you're logged in via `claude` CLI, yoloai will automatically detect your credentials even without `~/.claude/.credentials.json` on disk.

### Credential Brokering

By default, yoloAI keeps the agent's LLM credential **out of the sandbox entirely**. Instead of placing the credential in the container, it runs a tiny per-sandbox proxy on the host (the *broker*), points the agent at it (`ANTHROPIC_BASE_URL`) with a harmless placeholder token, and swaps in the real credential on the way to the provider. The live credential is never in the container's environment or filesystem, so a misbehaving or prompt-injected agent can't read or exfiltrate it.

**When it applies (the default):** a brokerable agent (currently **Claude**), on a supporting backend (docker, containerd, podman; macOS docker/apple/seatbelt), with a credential present. It composes with `--network-isolated` (the injector endpoint is allowlisted). For Claude, both the metered API key (`ANTHROPIC_API_KEY`) and the subscription token (`CLAUDE_CODE_OAUTH_TOKEN` from `claude setup-token`) are brokered — the API key takes precedence when both are set. Anything outside that — other agents, an interactive subscription login with no `CLAUDE_CODE_OAUTH_TOKEN` (the short-lived `~/.claude/.credentials.json` path), backends that can't host an injector (tart, podman-macOS), or `--network-none` — falls back to direct delivery automatically; no action needed.

```bash
# Brokering is automatic — nothing to do. To opt out (deliver the key directly):
yoloai new task ./project --no-broker

# To require brokering (error out instead of falling back if it can't be done):
yoloai new task ./project --broker
```

The posture is **sticky**: once a sandbox is created with `--broker` / `--no-broker`, restarts keep that choice (so a restart never silently puts the key back in the box). `--broker` and `--no-broker` are mutually exclusive.

**Caveats:**
- **Composes with `--network-isolated`.** A brokered, network-isolated sandbox keeps its credential host-side *and* is egress-restricted: the agent's allowlist stays default-deny, but the injector endpoint is allowlisted so the agent reaches the host-side proxy (which reaches the real upstream host-side). The agent's LLM egress collapses to that single endpoint. (`--network-none` has no egress at all, so brokering is skipped there; `--broker` with `--network-none` is rejected. On a backend that needs a dedicated network mode to reach the injector — rootless podman's slirp — `--network-isolated` brokering isn't composed yet and falls back to direct delivery.)
- **Subscription tokens are brokered** when supplied via `CLAUDE_CODE_OAUTH_TOKEN` (see below) — the long-lived token stays host-side and is injected as `Authorization: Bearer`. An interactive `claude login` that leaves only the short-lived `~/.claude/.credentials.json` (no `CLAUDE_CODE_OAUTH_TOKEN`) is not brokered and takes the file path.
- Brokering bounds the credential's blast radius (the agent can call the API but can't steal the credential); it does **not** stop a duped agent from misusing that in-scope API access. Compose it with the copy/diff/apply review gate and network controls.

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
| `container-enhanced` | Linux host + `runsc` registered with the daemon | gVisor userspace kernel — intercepts syscalls in userspace, no KVM needed. **Linux-only — not supported on macOS** (D71) |
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

**macOS `vm` isolation uses Apple `container`.** On a macOS host, `--isolation vm` for a Linux workload routes to the [Apple `container`](#apple-container-backend-macos) backend (per-container Linux VMs), not containerd — containerd/Kata is Linux-only. When Apple `container` is installed, it also becomes the **default** on macOS: a plain `yoloai new` gets VM isolation rather than a shared-kernel container, because Apple's per-container VMs boot in well under a second. Opt back to a shared-kernel container with `--isolation container` (Docker/Podman/OrbStack). This is a behavior change from earlier versions — see [BREAKING-CHANGES](BREAKING-CHANGES.md).

#### Apple `container` backend (macOS)

`apple` runs each sandbox as a Linux OCI container inside its own lightweight VM, via Apple's `container` CLI. It needs **no Docker Desktop** — the same `yoloai-base` Dockerfile is built by Apple's own builder.

- **Requires macOS 26 (Tahoe) or newer on Apple Silicon.** yoloAI gates on `macOS ≥ 26`; on older macOS the `--isolation vm` error explains the upgrade path. Some features (e.g. Rosetta-backed amd64) want **M3 or newer**.
- **Strong isolation, fast.** Each container gets a real VM boundary (the host kernel isn't shared), yet VMs start sub-second.
- **Network isolation works** the same as on Linux — the in-VM Linux kernel enforces the `--network-isolated` allowlist.
- **No suspend/resume and no VS Code "Attach to Running Container".** `container` has no checkpoint or docker-compat API; `exec`-based attach (`yoloai attach`) works normally.
- **Memory is not released back to the host** until the VM stops (virtio-balloon) — minor for ephemeral sandboxes.

**`container-privileged` — Docker-in-Docker and Compose:**

This mode passes Docker's `--privileged` flag. The container receives all Linux capabilities, disables seccomp, and disables AppArmor/SELinux enforcement. Use it when the agent needs to run Docker or Compose stacks inside the sandbox.

Docker CE and the Compose plugin are pre-installed. `fuse-overlayfs` is configured as the default storage driver in `/etc/docker/daemon.json` — `overlay2` fails with EPERM in nested container environments.

**Security stance:** `--privileged` grants the agent near-full host kernel access. A rogue agent could escape the container. Use this mode only with agents and prompts you trust. It protects against accidental damage (copy/diff/apply workflow, clean teardown) but not against deliberate malicious behavior.

Works on macOS too, via the Docker/Podman backend's Linux VM (Docker Desktop, OrbStack, or Podman Machine all run `--privileged` containers): use `--os linux --isolation container-privileged`. It is only unavailable with `--os mac`, where the native Seatbelt/Tart backends have no privileged mode. No additional host prerequisites are needed on a standard Linux machine. On Proxmox LXC hosts, ensure `features: nesting=1` is set on the LXC container.

**Setup — gVisor (`container-enhanced`), Linux only:**

`container-enhanced` requires a **Linux host**. On macOS it is rejected up front (D71): the Docker daemon runs inside a Linux VM (Docker Desktop / OrbStack / Podman Machine) and none can run `runsc` turn-key — Docker Desktop's engine fails when runsc is registered, OrbStack's `/tmp→/private/tmp` virtiofs symlink breaks runsc's chroot, plus a nested cgroup-v2 hazard. Use a Linux host for gVisor, or `container` / `container-privileged` on macOS. See `docs/contributors/design/plans/setup-gvisor.md` for the full investigation.

On a Linux host, `runsc` must be installed and registered as a Docker runtime:

1. Install the `runsc` binary: see [gVisor installation docs](https://gvisor.dev/docs/user_guide/install/)
2. Register it with Docker in `/etc/docker/daemon.json`:
   ```json
   {"runtimes": {"runsc": {"path": "/usr/local/bin/runsc"}}}
   ```
3. Restart the Docker daemon: `sudo systemctl restart docker`

yoloai's system check then verifies the `runsc` binary on `PATH` and its registration with the daemon.

**Setup — Kata Containers (`vm`, `vm-enhanced`):**

1. Install Kata Containers 3.x: see [Kata releases](https://github.com/kata-containers/kata-containers/releases)
2. `vm` uses the containerd backend directly (not Docker) — ensure containerd is configured with Kata shims.
3. `vm-enhanced` additionally requires Firecracker.

**Incompatibilities:**

- **`vm` and `vm-enhanced`:** On Linux, use the containerd backend (Kata), not Docker or Podman — selected automatically when `--isolation vm` or `vm-enhanced` is used. On macOS, `vm` uses the [Apple `container`](#apple-container-backend-macos) backend instead (containerd is Linux-only); `vm-enhanced` has no macOS backend.
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

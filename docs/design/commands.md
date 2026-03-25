> **Design documents:** [Overview](README.md) | [Config](config.md) | [Setup](setup.md) | [Security](security.md) | [Research](../dev/RESEARCH.md) | [research/](../dev/research/)

### 4. CLI (`yoloai`)

Single Go binary. No runtime dependencies — just the binary and Docker.

**Global flags:**
- `--verbose` / `-v`: Enable verbose terminal output showing Docker commands, mount operations, config resolution, and entrypoint activity. Controls what is printed to the console; does not affect log persistence. Also settable via `YOLOAI_VERBOSE=1`.
- `--quiet` / `-q`: Suppress non-essential output. `-q` for warn-only, `-qq` for error-only.
- `--no-color`: Disable colored output.
- `--json`: Output as JSON for scripting and CI. Errors go to stderr as `{"error": "message"}`. Interactive commands (`attach`, `exec`) reject `--json`.
- `--debug`: Enable debug-level logging to the sandbox's persistent debug log (`~/.yoloai/sandboxes/<name>/debug.log`). For commands that do not operate on a sandbox, silently ignored. Useful for capturing a detailed trail before a problem occurs, so it is available when filing a bug report.
- `--bugreport <type>`: Write a structured Markdown bug report. `<type>` is `safe` (sanitized, suitable for sharing) or `unsafe` (unsanitized, for author debugging). Implicitly enables `--debug`. Report is always written regardless of outcome (success, error, panic, or signal). Output filename is auto-generated in the current directory: `yoloai-bugreport-[<sandbox>-]<timestamp>.md`. See [Bug Report Design](bugreport.md).

**Environment Variables:**
- `YOLOAI_SANDBOX`: Default sandbox name for commands that accept `<name>`. Explicit `<name>` argument always takes precedence. Example: `YOLOAI_SANDBOX=my-task yoloai diff` is equivalent to `yoloai diff my-task`.
- `YOLOAI_VERBOSE`: Set to `1` to enable verbose output (same as `--verbose` flag).

## Commands

```
Core Workflow:
  yoloai new [options] [-a] <name> <workdir> [-d <auxdir>...]    Create and start a sandbox
  yoloai attach <name>                           Attach to a sandbox's tmux session
  yoloai diff <name> [<ref>] [-- <path>...]       Show changes the agent made
  yoloai apply <name>                            Copy changes back to original dirs

Lifecycle:
  yoloai start [-a] [--resume] <name>             Start a stopped sandbox
  yoloai stop <name>...                          Stop sandboxes (preserving state)
  yoloai destroy <name>...                       Stop and remove sandboxes
  yoloai reset <name>                            Re-copy workdir and reset git baseline
  yoloai restart [-a] <name>                     Restart the agent in an existing sandbox

Inspection:
  yoloai system                                  System information and management
  yoloai system info                             Show version, paths, disk usage, backend availability
  yoloai system agents [name]                    List available agents
  yoloai system backends [name]                  List available runtime backends
  yoloai system build [profile|--all]            Build/rebuild container image(s)
  yoloai system check                            Verify prerequisites for CI/CD pipelines
  yoloai system doctor                           Show capability status for all backends and isolation modes
  yoloai system prune                            Remove orphaned backend resources and stale temp files
  yoloai system setup                            Run interactive setup  (--agent, --backend, --tmux-conf for automation)
  yoloai sandbox                                 Sandbox inspection
  yoloai sandbox list                            List sandboxes and their status
  yoloai sandbox <name> info                     Show sandbox configuration and state
  yoloai sandbox <name> log                      Show sandbox session log
  yoloai sandbox <name> exec <command>           Run a command inside the sandbox
  yoloai sandbox <name> allow <domain>...       Allow additional domains in an isolated sandbox
  yoloai sandbox <name> allowed                 Show allowed domains for a sandbox
  yoloai sandbox <name> deny <domain>...        Remove domains from the allowlist
  yoloai sandbox <name> mount add <host-path> [<container-path>]  Add a bind mount to a running sandbox (Docker/Podman, Linux only)
  yoloai sandbox <name> mount rm <container-path>                 Remove a live-added bind mount
  yoloai sandbox <name> bugreport <file>        Write a bug report for a sandbox to a file
  yoloai ls                                      List sandboxes (shortcut for 'sandbox list')
  yoloai log <name>                              Show sandbox log (shortcut for 'sandbox log')
  yoloai exec <name> <command>                   Run a command inside a sandbox (shortcut for 'sandbox exec')

Workflow:
  yoloai files <name> put <file/glob>...               Copy files into sandbox exchange dir
  yoloai files <name> get <file/glob>... [-o dir]      Copy files out of sandbox exchange dir
  yoloai files <name> ls [glob]...                     List files in sandbox exchange dir
  yoloai files <name> rm <glob>...                     Remove files from sandbox exchange dir
  yoloai files <name> path                             Print host path to sandbox exchange dir
  yoloai x <extension> <name> [args...] [--flags...]  Run a user-defined extension

Admin:
  yoloai config get [key]                        Print configuration values (all or specific key)
  yoloai config set <key> <value>                Set a configuration value
  yoloai config reset <key>                      Remove key from config, reverting to internal default
  yoloai profile create <name>                   Create a profile with scaffold
  yoloai profile list                            List profiles
  yoloai profile info <name>                     Show merged profile configuration
  yoloai profile delete <name>                   Delete a profile
  yoloai system completion [bash|zsh|fish|powershell]   Generate shell completion script
  yoloai version                                 Show version information
```

### Agent Definitions

Built-in agent definitions (v1). Each agent specifies its install method, launch commands, API key requirements, and behavioral characteristics. No user-defined agents in v1.

| Field                         | Aider                                                     | Claude                                                    | Codex                                          | Gemini                                          | OpenCode                                       | test                                           | shell                                          |
|-------------------------------|-----------------------------------------------------------|-----------------------------------------------------------|-------------------------------------------------|-------------------------------------------------|------------------------------------------------|-------------------------------------------------|------------------------------------------------|
| Install                       | `pip install aider-chat`                                  | `npm i -g @anthropic-ai/claude-code`                      | `npm i -g @openai/codex`                       | `npm i -g @google/gemini-cli`                   | `go install github.com/opencode-ai/opencode`   | (none — bash is built-in)                       | (none — uses agents installed in base image)   |
| Runtime                       | Python                                                    | Node.js                                                   | Node.js                                        | Node.js                                         | Go binary                                      | None                                            | None                                           |
| Interactive cmd               | `aider --yes-always`                                      | `claude --dangerously-skip-permissions`                   | `codex --dangerously-bypass-approvals-and-sandbox` | `gemini --yolo`                                 | `opencode`                                     | `bash`                                          | `bash`                                         |
| Headless cmd                  | `aider --message "PROMPT" --yes-always --no-pretty --no-fancy-input` | `claude -p "PROMPT" --dangerously-skip-permissions`       | `codex exec --dangerously-bypass-approvals-and-sandbox "PROMPT"` | `gemini -p "PROMPT" --yolo`                     | `opencode run "PROMPT"`                        | `sh -c "PROMPT"`                                | `sh -c "PROMPT"`                               |
| Default prompt mode           | interactive                                               | interactive                                               | interactive                                    | interactive                                     | headless                                       | headless (with prompt) / interactive (without)  | headless (with prompt) / interactive (without) |
| Submit sequence (interactive) | `Enter` + ready-pattern polling                           | `Enter Enter` (double) + ready-pattern polling            | `Enter` + ready-pattern polling                | `Enter` + ready-pattern polling                 | `Enter` + 3s startup delay                     | `Enter` + 0s startup delay                      | `Enter` + 0s startup delay                     |
| API key env vars              | `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`, `DEEPSEEK_API_KEY`, `OPENROUTER_API_KEY` | `ANTHROPIC_API_KEY`                                       | `CODEX_API_KEY` (preferred), `OPENAI_API_KEY` (fallback) | `GEMINI_API_KEY`                                | `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`, `GROQ_API_KEY`, `OPENROUTER_API_KEY`, `XAI_API_KEY` | (none) | Union of all agents' keys |
| Auth optional                 | No                                                        | No                                                        | No                                             | No                                              | Yes (warning, not error — many auth paths)     | N/A                                             | N/A                                            |
| State directory               | (none)                                                    | `~/.claude/`                                              | `~/.codex/`                                    | `~/.gemini/`                                    | `~/.local/share/opencode/`                     | (none)                                          | (none — seeds into all agents' state dirs)     |
| Model flag                    | `--model <model>`                                         | `--model <model>`                                         | `--model <model>`                              | `--model <model>`                               | `--model <model>`                              | (ignored)                                       | (ignored)                                      |
| Model aliases                 | `sonnet`, `opus`, `haiku`, `deepseek`, `flash` (passthrough to aider's model names) | `sonnet` → `claude-sonnet-4-latest`, `opus` → `claude-opus-4-latest`, `haiku` → `claude-haiku-4-latest` | `default` → `gpt-5.3-codex`, `spark` → `gpt-5.3-codex-spark`, `mini` → `codex-mini-latest` | `pro` → `gemini-2.5-pro`, `flash` → `gemini-2.5-flash`, `preview-pro` → `gemini-3.1-pro-preview`, `preview-flash` → `gemini-3-flash-preview` | `sonnet` → `anthropic/claude-sonnet-4-5-latest`, `opus` → `anthropic/claude-opus-4-latest`, `haiku` → `anthropic/claude-haiku-4-5-latest` | (none) | (none) |
| Ready pattern                 | `> $`                                                     | `❯` (polls tmux output; replaces fixed delay)             | `›` (polls tmux output)                        | `"Type your message"` (polls tmux output)       | (none — uses fixed delay)                      | (none — uses fixed delay)                       | (none — uses fixed delay)                      |
| Seed files                    | `.aider.conf.yml` (home dir)                              | `.credentials.json` (auth-only, Keychain fallback), `settings.json`, `.claude.json` (home dir) | `auth.json` (auth-only), `config.toml`         | `oauth_creds.json` (auth-only), `google_accounts.json` (auth-only), `settings.json` | `auth.json` (auth-only), `.opencode.json` (auth-only, home dir), `hosts.json`/`apps.json` (GitHub Copilot, auth-only, home dir), `.config/opencode/.opencode.json` (home dir) | (none) | Union of all agents' seed files (remapped to home dir paths) |
| Non-root required             | No                                                        | Yes (refuses as root)                                     | No (convention is non-root)                    | No                                              | No                                             | No                                              | No                                             |
| Proxy support                 | TBD                                                       | Yes (npm install only, not native binary)                 | No (upstream limitation — github.com/openai/codex#4242) | TBD                                             | TBD                                            | N/A                                             | N/A                                            |
| Default network allowlist     | (none)                                                    | `api.anthropic.com`, `claude.ai`, `platform.claude.com`, `statsig.anthropic.com`, `sentry.io` | `api.openai.com` | `generativelanguage.googleapis.com`, `cloudcode-pa.googleapis.com`, `oauth2.googleapis.com` | `api.anthropic.com`, `api.openai.com`, `generativelanguage.googleapis.com`, `api.github.com`, `api.githubcopilot.com` | (none) | Union of all agents' allowlists |
| Extra env vars / quirks       | Supports local models via `OLLAMA_API_BASE`, `OPENAI_API_BASE`. Model prefixes auto-applied (e.g. `ollama_chat/`). | —                                                         | Landlock sandbox fails in containers — use `--dangerously-bypass-approvals-and-sandbox`. Auth via `auth.json` (browser OAuth cache) or API key env vars. `cli_auth_credentials_store = "file"` must be set in `config.toml` for file-based auth. | Sandbox disabled by default; `--yolo` auto-approves tool calls. OAuth login also supported but API key is the primary auth path. | Supports GitHub Copilot credentials, local endpoints, AWS Bedrock, Azure OpenAI, and Vertex AI auth. Auth hint env vars skip API key check. | Deterministic shell-based agent for development and bug report reproduction. No API key needed. Prompt IS the shell script. | Pseudo-agent that seeds ALL agents' credentials and drops to bash. Run any agent manually with `yolo-<name>` aliases. Not shown in `yoloai system setup`. |

**Ready pattern:** Agents can specify a `ready_pattern` — a string the entrypoint polls for in the tmux pane output to determine when the agent is ready to receive a prompt. This replaces the fixed startup delay with responsive detection. Claude uses `❯` (the prompt character). While polling, the entrypoint auto-accepts confirmation prompts (e.g., workspace trust dialogs) by detecting "Enter to confirm" and sending Enter. After the pattern is found, the entrypoint waits for screen output to stabilize before delivering the prompt. Agents without a ready pattern fall back to the fixed `startup_delay`.

**Seed files:** Agents can specify files to copy from the host into the container's agent-state directory at sandbox creation time. These seed agent configuration and credentials without bind-mounting (sandbox gets its own copy). Claude seeds `.credentials.json` (auth-only — skipped when `ANTHROPIC_API_KEY` is set), `settings.json` (Claude Code settings), and `.claude.json` (from home dir — global Claude config). The `auth_only` flag means the file is only needed when no API key is provided via environment variable.

**Prompt delivery modes:**

- **Interactive mode:** Agent starts in interactive mode inside tmux. If `--prompt` is provided, prompt is fed via `tmux send-keys` after a startup delay. Agent stays running for follow-up via `yoloai attach`. This is Claude's natural mode.
- **Headless mode:** Agent launched with prompt as CLI arg inside tmux (tmux still used for logging + attach). Runs to completion. This is Codex's natural mode via `codex exec --dangerously-bypass-approvals-and-sandbox "PROMPT"`. Without `--prompt`, Codex falls back to interactive mode.

The agent definition determines the default prompt delivery mode. `--prompt` selects headless mode for agents that support it (Codex); Claude always uses interactive mode regardless.

### `yoloai new`

`yoloai new [options] <name> [<workdir>] [-d <auxdir>...]`

The **name** is always required (no auto-generation).

The **workdir** is the single primary project directory — the agent's working directory. It is positional (after name) and defaults to `:copy` mode if no suffix is given. The `:rw` suffix must be explicit. When `--profile` is set and the profile provides a workdir, `<workdir>` is optional (profile workdir is used as default). If the profile has no workdir and `<workdir>` is omitted, error: "workdir is required (profile '<name>' doesn't provide one)". Without `--profile`, `<workdir>` is always required.

**`-d` / `--dir`** specifies auxiliary directories (repeatable). Default read-only. Additive with profile directories.

When both CLI and profile provide directories: CLI workdir **replaces** profile workdir. CLI `-d` dirs are **additive** with profile dirs.

Directory argument syntax: `<path>[:<suffixes>][=<mount-point>]`

Suffixes (combinable in any order):
- `:rw` — bind-mounted read-write (live, immediate changes)
- `:copy` — copied to sandbox state, read-write, diff/apply available
- `:overlay` — uses Linux overlayfs for instant setup with diff/apply workflow (Docker/Podman, requires CAP_SYS_ADMIN)
- `:force` — override dangerous directory detection

Mount point:
- `=<path>` — mount at a custom container path instead of the mirrored host path

All directories are **bind-mounted read-only by default** at their original absolute host paths (mirrored paths).

```
# Copy workdir (safe, reviewable), aux dirs read-only (workdir defaults to :copy)
yoloai new fix-build --prompt "fix the build" ./my-app -d ./shared-lib -d ./common-types

# Live-edit workdir, one aux dir also writable
yoloai new my-task ./my-app:rw -d ./shared-lib:rw -d ./common-types

# Custom mount points for tools that need specific paths
yoloai new my-task ./my-app=/opt/myapp -d ./shared-lib=/usr/local/lib/shared -d ./common-types

# Use profile workdir, add extra aux dir from CLI
yoloai new my-task --profile my-project -d ./extra-lib

# Explicit :copy suffix also works
yoloai new my-task ./my-app:copy -d ./shared-lib
```

Name is required. If omitted, an error is shown with a helpful message suggesting a name based on the workdir basename.

Example layout inside the container (assuming cwd is `/home/user/projects`):

```
yoloai new my-task ./my-app -d ./shared-lib:rw -d ./common-types
```

```
/home/user/projects/
├── my-app/          ← workdir (copied, read-write, diff/apply available)
├── shared-lib/      ← aux dir (bind-mounted, read-write, live)
└── common-types/    ← aux dir (bind-mounted, read-only)
```

Paths inside the container mirror the host — configs, error messages, and symlinks work without translation.

With custom mount points:

```
yoloai new my-task ./my-app=/opt/myapp -d ./shared-lib=/usr/local/lib/shared -d ./common-types
```

```
/opt/myapp/                          ← workdir (copied, agent's cwd)
/usr/local/lib/shared/               ← aux dir (bind-mounted, read-only)
/home/user/projects/common-types/    ← aux dir (mirrored host path, read-only)
```

Options:
- `--profile <name>`: Use a profile's derived image and runtime config. No profile = base image + defaults only. The profile name and resolved image ref (`yoloai-<profile>`) are stored in `meta.json` so lifecycle commands recreate containers with the correct image. Auto-builds missing or stale images on demand (see [config.md](config.md#1-docker-images)).
- `--prompt` / `-p` `<text>`: Initial prompt/task for the agent (see Prompt Mechanism below). Use `--prompt -` to read from stdin. Mutually exclusive with `--prompt-file`.
- `--prompt-file` / `-f` `<path>`: Read prompt from a file. Use `--prompt-file -` to read from stdin. Mutually exclusive with `--prompt`.
- `--model` / `-m` `<model>`: Model to use. Passed to the agent's `--model` flag. If omitted, uses the agent's default. Accepts built-in aliases (see Agent Definitions) or full model names. Supports user-configurable aliases via `model_aliases` in config.yaml for version pinning and custom shortcuts.
- `--agent <name>`: Agent to use (`aider`, `claude`, `codex`, `gemini`, `opencode`, `shell`, `test`). Overrides `agent` from config.
- `--network-isolated`: Allow only the agent's required API traffic. The agent can function but cannot access other external services, download arbitrary binaries, or exfiltrate code.
- `--network-allow <domain>`: Allow traffic to specific additional domains (can be repeated). Implies `--network-isolated`. Added to the agent's default allowlist (see below).
- `--network-none`: Run with `--network none` for full network isolation (agent API calls will also fail). Mutually exclusive with `--network-isolated` and `--network-allow`. **Warning:** Most agents (Claude, Codex) require network access to reach their API endpoints. This flag is useful for testing container setup without agent execution or for agents with locally-hosted models.
- `--port <host:container>`: Expose a container port on the host (can be repeated). Example: `--port 3000:3000` for web dev. Without this, container services are not reachable from the host browser. Ports must be specified at creation time — Docker does not support adding port mappings to running containers. To add ports later, use `yoloai new --replace`.
- `--replace`: Destroy existing sandbox of the same name before creating. Shorthand for `yoloai destroy <name> && yoloai new <name>`. Inherits destroy's smart confirmation — prompts when the existing sandbox has a running agent or unapplied changes. `--yes` skips confirmation.
- `--attach` / `-a`: Auto-attach to the tmux session after creation. Without this flag, the sandbox starts in the background and prints `yoloai attach <name>` as a hint.
- `--no-start`: Create sandbox without starting the container. Useful for setup-only operations.
- `--yes`: Skip confirmation prompts (dirty repo warning). For scripting.
- `-- <args>...`: Pass remaining arguments directly to the agent CLI invocation. Appended verbatim after yoloAI's built-in flags in the agent command. Example: `yoloai new fix-bug . --model opus -- --max-turns 5` produces `claude --dangerously-skip-permissions --model claude-opus-4-latest --max-turns 5`. **Do not duplicate first-class flags** (e.g., `--model`) in passthrough args — behavior is undefined (depends on the agent's CLI parser, which typically uses last-wins semantics).

**Default network allowlist (per agent):**

**Claude Code:**

| Domain                  | Purpose                               |
|-------------------------|---------------------------------------|
| `api.anthropic.com`     | API calls (required)                  |
| `claude.ai`             | OAuth/web auth (required for OAuth)   |
| `platform.claude.com`   | OAuth/web auth (required for OAuth)   |
| `statsig.anthropic.com` | Telemetry/feature flags (recommended) |
| `sentry.io`             | Error reporting (recommended)         |

**Gemini CLI:**

| Domain                                 | Purpose                       |
|----------------------------------------|-------------------------------|
| `generativelanguage.googleapis.com`    | API calls (required)          |
| `cloudcode-pa.googleapis.com`          | OAuth auth route (recommended)|
| `oauth2.googleapis.com`               | OAuth token refresh (required for OAuth) |

**Codex:**

| Domain           | Purpose              |
|------------------|----------------------|
| `api.openai.com` | API calls (required) |

The allowlist is agent-specific — each agent's definition includes its required domains. `--network-allow` domains are additive with the selected agent's defaults.

**Workflow:**

1. Apply `:copy` suffix to workdir if no mode suffix (`:rw` or `:copy`) is given — `:force` is a modifier, not a mode, so `./my-app:force` is treated as `./my-app:copy:force`.
2. Error if any two directories resolve to the same absolute container path (mirrored host path or custom `=<path>`).
3. For each `:copy` directory, create an isolated writable copy:
   - If the directory is a git repo, record the current HEAD SHA in `meta.json`.
   - Copy via `cp -rp` to `~/.yoloai/sandboxes/<name>/work/<encoded-path>/`, where `<encoded-path>` is the absolute host path with path separators and filesystem-unsafe characters encoded using [caret encoding](https://github.com/kstenerud/caret-encoding) (e.g., `/home/user/my-app` → `^2Fhome^2Fuser^2Fmy-app`). This is fully reversible and avoids collisions when multiple directories share the same basename. `cp -rp` preserves permissions, timestamps, and symlinks (POSIX-portable; `cp -a` is GNU-specific and unavailable on macOS). Everything is copied including `.git/` and files ignored by `.gitignore`, **except build artifacts** (`.build/`, `DerivedData/`, `node_modules/`, `__pycache__/`, `*.xcworkspace/xcuserdata/`, `*.xcodeproj/xcuserdata/`) which are excluded to prevent compilation failures from hardcoded paths and to improve copy performance.
   - If the copy already has a `.git/` directory (from the original repo), use the recorded SHA as the baseline — `yoloai diff` will diff against it.
   - If the copy has no `.git/`, `git init` + `git add -A` + `git commit -m "initial"` to create a baseline.
   - The container receives a ready-to-use directory with a git baseline already established, mounted at the mirrored host path inside the container.

   Note: `git add -A` naturally honors `.gitignore` if one is present, so gitignored files (e.g., `node_modules`) won't clutter `yoloai diff` output.
4. If `auto_commit_interval` > 0, start a background auto-commit loop for `:copy` directories inside the container for recovery. The interval is passed to the container via `config.json`. Disabled by default.
5. Store original paths, modes, and mapping in `meta.json`.
6. Start Docker container (see Container Startup below).

### Safety Checks

Before creating the sandbox (all checks run before any state is created on disk):
- **Duplicate name detection:** Error if a sandbox with the same name already exists (unless `--replace` is used).
- **Missing API key:** Error if the required API key for the selected agent is not set in the host environment.
- **Dangerous directory detection:** Error if any mount target is `$HOME`, `/`, macOS system directories (`/System`, `/Library`, `/Applications`), or Linux system directories (`/usr`, `/etc`, `/var`, `/boot`, `/bin`, `/sbin`, `/lib`). All paths are resolved through symlinks (`filepath.EvalSymlinks`) before checking — a symlink to `$HOME` is caught the same as `$HOME` itself. Simple string match on the resolved absolute path. Override with `:force` suffix (e.g., `$HOME:force`, `$HOME:rw:force`), which downgrades to a warning.
- **Path overlap detection:** Error if any two sandbox mounts have path prefix overlap (one resolved path starts with the other). All paths are resolved through symlinks before checking. Applies to all mount combinations (`:rw`/`:rw`, `:rw`/`:copy`, `:copy`/`:copy`). Check: does either resolved absolute path start with the other? Override with `:force` suffix on the overlapping path (e.g., `./parent:rw`, `./parent/child:copy:force`), which downgrades to a warning. `:force` is the explicit escape hatch for both dangerous directory and path overlap detection.
- **Dirty git repo detection:** If any `:rw` or `:copy` directory is a git repo with uncommitted changes, warn with specifics and prompt for confirmation (skippable with `--yes` for scripting):
  ```
  WARNING: ./my-app has uncommitted changes (3 files modified, 1 untracked)
  These changes will be visible to the agent and could be modified or lost.
  Continue? [y/N]
  ```

### Container Startup

1. Generate a **sandbox context file** (`context.md`) in the sandbox state directory describing the environment: workdir, auxiliary directories, mount modes (read-only / read-write / copy), network constraints, and resource limits. For agents with a native instruction mechanism (Claude's `CLAUDE.md`, Gemini's `GEMINI.md`), the context is written inline into the agent's instruction file in `agent-state/`. A reference copy is kept at `<sandbox>/context.md`. This approach works across all backends (Docker, Tart, Seatbelt) without requiring bind mounts to arbitrary paths.
2. Generate `/yoloai/config.json` on the host (in the sandbox state directory) containing all entrypoint configuration: agent_command, startup_delay, submit_sequence, host_uid, host_gid, and later overlay_mounts, iptables_rules, setup_script. This is bind-mounted into the container and read by the entrypoint.
3. Start Docker container (as non-root user `yoloai`) with:
   - When `--network-isolated`: entrypoint configures iptables + ipset rules (default-deny, allow only resolved IPs from the agent's domain allowlist + `--network-allow` domains). Requires `CAP_NET_ADMIN`.
   - `:copy` directories: copies from sandbox state mounted at their mount point (mirrored host path or custom `=<path>`, read-write)
   - `:rw` directories bind-mounted at their mount point (mirrored host path or custom `=<path>`, read-write)
   - Default (no suffix) directories bind-mounted at their mount point (mirrored host path or custom `=<path>`, read-only)
   - `agent-state/` mounted at the agent's state directory path (read-write, per-sandbox)
   - Files listed in `agent_files` (from config) copied into `agent-state/` on first run
   - `log.txt` from sandbox state bind-mounted at `/yoloai/log.txt` (read-write, for tmux `pipe-pane`)
   - `prompt.txt` from sandbox state bind-mounted at `/yoloai/prompt.txt` (read-only, if provided)
   - `/yoloai/config.json` bind-mounted read-only
   - Config mounts from defaults + profile
   - Resource limits from defaults + profile
   - API key(s) injected via file-based bind mount at `/run/secrets/` — env var names from agent definition (see [security.md](security.md#credential-management))
   - `CAP_NET_ADMIN` added when `--network-isolated` is used (required for iptables rules)
   - Container name: `yoloai-<name>`
   - User: `yoloai` (UID/GID matching host user)
   - `/yoloai/` internal directory for sandbox context file, overlay working directories, and bind-mounted state files (`log.txt`, `prompt.txt`, `config.json`)
4. Run `setup` commands from config (if any).
5. Start tmux session named `main` with logging to `/yoloai/log.txt` (`tmux pipe-pane`) and `remain-on-exit on` (container stays up after agent exits, only stops on explicit `yoloai stop` or `yoloai destroy`). Tmux config sourced based on the `tmux_conf` value in `config.json` (see [setup.md](setup.md#tmux-configuration)).
6. Inside tmux: launch the agent using the command from its agent definition (e.g., `claude --dangerously-skip-permissions [--model X]` or `codex --dangerously-bypass-approvals-and-sandbox`).
7. Start a background monitor that polls `#{pane_dead}` — when the agent exits, all attached tmux clients are auto-detached so the user's terminal returns cleanly instead of showing a dead pane.
8. Wait for the agent to initialize (interactive mode only). If the agent defines a `ready_pattern`, poll the tmux pane output for it (with a 60s timeout), auto-accepting confirmation prompts along the way and waiting for screen output to stabilize. Otherwise, fall back to a fixed `startup_delay`.
9. Prompt delivery depends on the agent's prompt mode:
   - **Interactive mode** (Claude default, Codex without `--prompt`): If `/yoloai/prompt.txt` exists, feed it via `tmux load-buffer` + `tmux paste-buffer` + `tmux send-keys` with the agent's submit sequence (`Enter Enter` for Claude, `Enter` for Codex).
   - **Headless mode** (Codex with `--prompt`): Prompt passed as CLI argument in the launch command (e.g., `codex exec --yolo "PROMPT"`). No `tmux send-keys` needed.

### Prompt Mechanism

The agent definition specifies the default prompt delivery mode. Two modes exist:

**Interactive mode** (Claude's default; Codex's fallback when no `--prompt`):

The agent runs in interactive mode inside tmux. When `--prompt` is provided:
1. The prompt is saved to `~/.yoloai/sandboxes/<name>/prompt.txt`.
2. After the agent starts inside tmux, wait for it to be ready (~3s).
3. For long prompts, write to a temp file and use `tmux load-buffer` + `tmux paste-buffer` to avoid shell escaping issues.
4. Send via `tmux send-keys` with the agent's submit sequence (`Enter Enter` for Claude — double Enter required; `Enter` for Codex).
5. The agent begins working immediately but remains interactive — if it needs clarification, the question sits in the tmux session until you `yoloai attach`.

Without `--prompt`, you get a normal interactive session waiting for input.

**Headless mode** (Codex's default when `--prompt` is provided):

The agent is launched with the prompt as a CLI argument (e.g., `codex exec --yolo "PROMPT"`) inside tmux. Tmux is still used for logging (`pipe-pane`) and `yoloai attach`. The agent runs to completion; no `tmux send-keys` needed. Without `--prompt`, Codex falls back to interactive mode.

### Creation Output

After `yoloai new` completes, print a brief summary of resolved settings followed by context-aware next-command suggestions:

```
Sandbox fix-bug created
  Agent:    claude
  Profile:  go-dev
  Workdir:  /home/user/projects/my-app (copy)
  Network:  isolated
  Strategy: overlay

Run 'yoloai attach fix-bug' to interact (Ctrl-b d to detach)
    'yoloai diff fix-bug' when done
```

When no `--prompt` was given, the agent is waiting for input — lead with `attach`:

```
Sandbox explore created
  Agent:    claude
  Workdir:  /home/user/projects/my-app (copy)

Run 'yoloai attach explore' to start working (Ctrl-b d to detach)
```

When `--port` is used:

```
Sandbox web-dev created
  Agent:    claude
  Workdir:  /home/user/projects/my-app (copy)
  Ports:    3000:3000

Run 'yoloai attach web-dev' to interact (Ctrl-b d to detach)
    'yoloai diff web-dev' when done
```

Profile, network, and ports lines are omitted when using defaults (base image, unrestricted network, no ports).

### `yoloai attach`

Runs `tmux attach -t main` inside the container via the backend's exec mechanism.

Detach with standard tmux `Ctrl-b d` — container keeps running.

### `yoloai sandbox <name> info`

Displays sandbox configuration and state:
- Name
- Status (active / stopped / done / failed)
- Agent (claude, codex, etc.)
- Model (if specified)
- Profile (name or "(base)")
- Prompt (first 200 chars from `prompt.txt`, or "(none)")
- Workdir (resolved absolute path with mode)
- Network (if non-default, e.g., "none")
- Ports (if any)
- Directories with access modes (read-only / rw / copy)
- Creation time
- Baseline SHA (for `:copy` directories that were git repos, or "(synthetic)" for non-git dirs)
- Container ID
- Changes (yes/no/- — same detection as `list`)

Reads from `environment.json` and queries live container state via the sandbox's backend. Agent status is detected via container exec (`tmux list-panes -t main -F '#{pane_dead}'`) combined with container state for full status (active / stopped / done / failed). Useful for quick inspection without listing all sandboxes.

### `yoloai diff`

For `:copy` directories: runs `git add -A` (to capture untracked files created by the agent) then `git diff` against the baseline (the recorded HEAD SHA for existing repos, or the synthetic initial commit for non-git dirs). Shows exactly what the agent changed with proper diff formatting. For the full copy strategy, runs on the host (reads `work/` directly). For the overlay strategy, runs inside the container via container exec (the merged view requires the overlay mount). Same as `yoloai apply` — see that section for details.

For `:rw` directories: runs `git diff` directly on the host (same files via bind mount). Does not require the container to be running. If the original is not a git repo, notes that diff is not available for live-mounted dirs without git. Note: for `:rw` directories, diff shows all uncommitted changes relative to HEAD, not just changes made by the agent. Pre-existing uncommitted changes are mixed in. Use `:copy` mode for clean agent-only diffs.

Read-only directories are skipped (no changes possible).

**Warning when agent is running:** If the tmux pane is still alive (agent hasn't exited), prints "Note: agent is still running; diff may be incomplete" before showing the diff.

Options:
- `--stat`: Show summary (files changed, insertions, deletions) instead of full diff.
- `--log`: List individual agent commits beyond baseline (with commit SHA and subject). Combine with `--stat` to include per-commit file change summaries. Also notes uncommitted changes if present.
- `<ref>`: Show diff for a specific commit (hex SHA prefix, 4+ chars) or range (`sha..sha`). Without `--`, auto-detected by hex pattern; with `--`, everything after is treated as path filters.
- `-- <path>...`: Filter diff output to specific paths (relative to workdir).

### `yoloai apply`

`yoloai apply <name> [--squash | --patches <dir>] [--no-wip] [--force] [-- <path>...]`

For `:copy` directories only. `:rw` directories need no apply — changes are already live. Read-only directories have no changes. For dirs that had no original git repo, excludes the synthetic `.git/` directory created by yoloAI.

Runs entirely on the host — reads from `work/<encoded-path>/`. Does not require the container to be running.

**Default behavior (commit-preserving):**

1. Get baseline SHA from `meta.json`.
2. Check for commits beyond baseline (`git rev-list <baseline>..HEAD` in the work copy).
3. If commits exist:
   - `git format-patch <baseline>..HEAD` in the work copy → temp directory.
   - Show summary: "N commits to apply (+ uncommitted changes)" with `git log --oneline <baseline>..HEAD`.
   - `git am --3way *.patch` on the host repo — preserves commit messages, authorship, and individual commits.
   - If uncommitted changes also exist in the work copy (and `--no-wip` not set): `git diff HEAD` → `git apply` on the host (left unstaged).
4. If no commits beyond baseline:
   - Apply uncommitted changes as an unstaged patch via `git diff <baseline>` → `git apply` on the host.

Without `-- <path>...`, applies all changes. With `-- <path>...`, `git format-patch` filters to commits touching those paths and `git diff` is scoped to those paths. The `--` separator is required to distinguish paths from sandbox names.

**Pre-flight checks:**

- If the host repo has uncommitted changes, warns and aborts (suggest `git stash` or commit first). Overridable with `--force`.
- If there are no changes at all (no commits beyond baseline, no uncommitted changes), informs the user and exits 0.
- If the agent is still running, prints "Note: agent is still running; apply may be incomplete" before proceeding.

**Conflict handling:**

- `git am --3way` is used by default for 3-way merge support.
- If a patch conflicts, `git am` stops at the conflicting commit. Earlier commits remain applied. yoloAI prints which commit conflicted and reminds the user of `git am --continue` (after resolving), `git am --skip` (skip that commit), or `git am --abort` (undo all applied commits).
- Uncommitted changes (WIP) are only applied after all commits succeed. If the WIP `git apply` fails, the error is reported but successfully applied commits are not undone.

**Options:**

- `--squash`: Flatten all changes (commits + uncommitted) into a single unstaged patch. Generates one `git diff <baseline>` and applies it with `git apply`. Shows a summary via `git diff --stat` and verifies cleanly with `git apply --check` before prompting for confirmation. Useful when you want to review everything as a single diff before committing.
- `--no-wip`: Skip uncommitted changes, only apply commits. Has no effect with `--squash` (which always includes everything). Has no effect when there are no commits beyond baseline.
- `--patches <dir>`: Export `.patch` files to the specified directory instead of applying. Also exports `wip.diff` if uncommitted changes exist (unless `--no-wip`). Prints instructions for manual application (`git am --3way <dir>/*.patch`). Useful for selective commit application — the user can delete unwanted `.patch` files before running `git am`, or use standard git tools (`git rebase -i`, `git cherry-pick`) after importing.
- `--force`: Proceed even if the host repo has uncommitted changes.

### `yoloai destroy`

`yoloai destroy <name>...`

Stops and removes the container via the sandbox's backend. Removes `~/.yoloai/sandboxes/<name>/` entirely. No special overlay cleanup needed — the kernel tears down the mount namespace when the container stops.

Accepts multiple sandbox names (e.g., `yoloai destroy sandbox1 sandbox2 sandbox3`) with a single confirmation prompt showing all sandboxes to be destroyed.

**Wildcard support:** Sandbox names can include `*` and `?` wildcards for pattern matching. For example, `yoloai destroy test*` will destroy all sandboxes whose names start with "test". Wildcards are expanded against existing sandboxes; an error is returned if no matches are found.

**Smart confirmation:** Confirmation is only required when the agent is still running or unapplied changes exist (detected via `git status --porcelain` on the host-side work directory, consistent with `list` CHANGES detection). If the sandbox is stopped/exited with no unapplied changes, destruction proceeds without prompting. `--yes` skips all confirmation regardless.

Options:
- `--all`: Destroy all sandboxes (confirmation required unless `--yes` is also provided).
- `--yes`: Skip confirmation prompts.

### `yoloai sandbox <name> log` / `yoloai log`

`yoloai log <name>` displays the session log (`log.txt`) for the named sandbox. Auto-pages through `$PAGER` / `less -R` when stdout is a TTY, matching `git log` behavior. When piped (stdout is not a TTY), outputs raw for composition with unix tools: `yoloai log my-task | tail -100`, `yoloai log my-task | grep error`.

### `yoloai sandbox <name> exec`

`yoloai exec <name> <command>` runs a command inside the sandbox container without attaching to tmux. Useful for debugging (`yoloai exec my-sandbox bash`) or quick operations (`yoloai exec my-sandbox npm install foo`).

Implemented via the backend's exec mechanism (e.g., `docker exec` or `podman exec`), with `-i` added when stdin is a pipe/TTY and `-t` added when stdin is a TTY. This allows both interactive use (`yoloai exec my-sandbox bash`) and non-interactive use (`yoloai exec my-sandbox ls`, `echo "test" | yoloai exec my-sandbox cat`).

### `yoloai sandbox <name> allow/allowed/deny`

Parent command for managing sandbox network allowlists.

#### `yoloai sandbox <name> allow`

`yoloai sandbox <name> allow <domain>...` adds domains to the allowlist of a network-isolated sandbox. Changes are persisted to meta.json and config.json so they survive container restarts. If the container is running, ipset rules are live-patched via container exec (as root, using `dig` + `ipset add`).

Requires `network_mode == "isolated"`. Errors if the sandbox uses `--network-none` or has no network restrictions. Duplicate domains are silently skipped (idempotent). If the container is stopped, changes are saved and take effect on the next start.

#### `yoloai sandbox <name> allowed`

`yoloai sandbox <name> allowed` shows the allowed domains for a sandbox.

- Text output: one domain per line, or "No network isolation" / "No domains allowed".
- JSON output: `{"name": "...", "network_mode": "...", "domains": [...]}`.

No runtime needed — pure file read from meta.json.

#### `yoloai sandbox <name> deny`

`yoloai sandbox <name> deny <domain>...` removes domains from the allowlist of a network-isolated sandbox. Changes are persisted to meta.json and config.json. If the container is running, ipset rules are live-patched (flush and re-add remaining IPs).

Requires `network_mode == "isolated"`. Errors if a specified domain is not in the allowlist.

### `yoloai sandbox <name> mount`

Commands for adding and removing bind mounts on a running sandbox, without tearing down the container and losing agent context.

**Backend and platform support:** Docker and Podman on Linux only.

- **Tart:** VirtioFS shares are passed as `--dir` flags to `tart run` and cannot be added to a running VM.
- **Seatbelt:** The SBPL sandbox profile is generated at `Create` time and applied to the process at `Start` time. A running process's sandbox profile cannot be modified.
- **Docker Desktop on macOS:** Container PIDs live inside the hypervisor's Linux VM and are not accessible via `nsenter` from the macOS host.

**Mechanism:** `nsenter --mount --target <container-pid> -- mount --bind <host-path> <container-path>`. This enters the container's mount namespace from the host and performs the bind mount without restarting the container. Requires root — if yoloai is not running as root, the command fails with a clear error: `mount add requires root; try: sudo yoloai sandbox <name> mount add <path>`.

**Persistence:** Live mounts are saved to `meta.json` (`live_mounts` field, same `DirMeta` structure as `directories`). On the next `yoloai start`, they are applied as regular Docker bind mounts during `ContainerCreate` — no nsenter needed on restart, since the container is freshly created with all mounts baked in. Live mounts survive stop/start cycles.

**`sandbox info` output:** Live mounts appear in the Directories section, tagged `(live)` to distinguish them from mounts configured at creation.

#### `yoloai sandbox <name> mount add <host-path> [<container-path>]`

Adds a bind mount to a running sandbox.

- `<host-path>`: Absolute or relative path on the host (resolved to absolute). Must exist.
- `<container-path>`: Mount point inside the container. Defaults to the same absolute path as `<host-path>` (mirrored path convention, matching `yoloai new`).

Options:
- `--read-only` / `-r`: Mount read-only. Default is read-write.

Procedure:
1. Require sandbox to be running (active or idle). Error if stopped.
2. Check backend is Docker or Podman, and host is Linux. Error otherwise with explanation.
3. Resolve `<host-path>` to absolute. Error if it doesn't exist.
4. Default `<container-path>` to `<host-path>` if omitted.
5. Error if `<container-path>` is already in use (either from original mounts or a prior `mount add`).
6. Create mount point inside container: `docker exec <instance> mkdir -p <container-path>`.
7. Get container PID: `docker inspect --format '{{.State.Pid}}' <instance>`.
8. Bind mount via nsenter: `nsenter --mount --target <pid> -- mount --bind <host-path> <container-path>` (append `,ro` remount for `--read-only`).
9. Append to `live_mounts` in `meta.json`.
10. Print: `<host-path> → <container-path>` (or `… (read-only)`).

#### `yoloai sandbox <name> mount rm <container-path>`

Removes a live-added bind mount from a running sandbox.

Only removes mounts that were added via `mount add` (present in `live_mounts`). Cannot remove mounts configured at sandbox creation time — those are part of the container's core configuration and cannot be safely removed without restarting the container.

Procedure:
1. Require sandbox to be running.
2. Look up `<container-path>` in `live_mounts`. Error if not found (with hint if it's an original mount).
3. `nsenter --mount --target <pid> -- umount <container-path>`.
4. Remove from `live_mounts` in `meta.json`.
5. Print: `Removed <container-path>`.

### `yoloai sandbox list` / `yoloai ls`

Lists all sandboxes with their current status.

| Column  | Description                                                    |
|---------|----------------------------------------------------------------|
| NAME    | Sandbox name                                                   |
| STATUS  | `active`, `stopped`, `done` (exit 0), `failed` (non-zero exit) |
| AGENT   | Agent name (`aider`, `claude`, `codex`, `gemini`, `opencode`, `test`) |
| PROFILE | Profile name or `(base)`                                       |
| SIZE    | Sandbox disk usage                                             |
| AGE     | Time since creation                                            |
| WORKDIR | Working directory path                                         |
| CHANGES | `yes` if unapplied changes exist, `no` if clean, `-` if unknown. Detected via `git status --porcelain` on the host-side work directory (any output = changes; read-only, catches both tracked modifications and untracked files; no Docker needed). |

Agent exit status is detected via `tmux list-panes -t main -F '#{pane_dead_status}'` when `#{pane_dead}` is 1. Non-zero exit code shows STATUS as "failed"; exit 0 shows as "done". Running containers with live panes show "active"; stopped containers show "stopped".

Top-level shortcut: `yoloai ls`.

Options:
- `--active`: Show only active sandboxes.
- `--stopped`: Show only stopped sandboxes.
- `--agent <name>`: Show only sandboxes using this agent.
- `--profile <name>`: Show only sandboxes using this profile (`base` matches default).
- `--changes`: Show only sandboxes with unapplied changes.

### `yoloai system build`

`yoloai system build` with no arguments rebuilds the base image (`yoloai-base`).

`yoloai system build <profile>` rebuilds a specific profile's image (which derives from `yoloai-base`).

`yoloai system build --all` rebuilds everything: base image first, then all profile images (those with Dockerfiles).

Useful after modifying a profile's Dockerfile or when the base image needs updating (e.g., new agent CLI versions).

Profile Dockerfiles that install private dependencies (e.g., `RUN go mod download` from a private repo, `RUN npm install` from a private registry) need build-time credentials. yoloAI passes host credentials to Docker BuildKit via `--secret` so they're available during the build but never stored in image layers. Example: `RUN --mount=type=secret,id=npmrc,target=/root/.npmrc npm install` in the Dockerfile, with yoloAI automatically providing `~/.npmrc` as the secret source. Additional secrets can be passed via `yoloai system build --secret id=<name>,src=<path> <profile>`.

### `yoloai system doctor`

`yoloai system doctor` probes all known backends and their supported isolation modes, then prints a three-tier summary:

- **Ready to use** — all prerequisites are satisfied.
- **Needs setup** — prerequisites are missing but fixable (e.g. install a package, add a user to a group). Exit code 1 when any entry is in this tier.
- **Not available on this machine** — hardware or OS mismatch (e.g. no KVM, wrong OS). Informational only; does not affect exit code.

Flags:
- `--backend BACKEND` — restrict to one backend.
- `--isolation MODE` — restrict to one isolation mode.
- `--json` — machine-readable output.

Example:

```
$ yoloai system doctor
Ready to use:
  docker          container (default)
  docker          container-enhanced
  podman          container (default)

Needs setup:
  containerd      vm              2 of 4 checks failing
```

### `yoloai system prune`

`yoloai system prune` scans for orphaned backend resources and stale temporary files, reports what it finds, and (after confirmation) removes them.

**What gets pruned:**

- **Docker/Podman:** Containers named `yoloai-*` that have no corresponding sandbox directory.
- **Tart:** VMs named `yoloai-*` that have no corresponding sandbox directory.
- **Seatbelt:** No-op (no central instance registry; processes are tied to sandbox dirs).
- **Cross-backend:** `/tmp/yoloai-*` directories older than 1 hour (leaked secrets, apply, format-patch temps).

**Broken sandbox warnings:** Sandbox directories with missing or corrupt `meta.json` are reported as warnings (with full path and suggested `yoloai destroy` command) but are NOT deleted — they may contain recoverable work.

**What is NOT pruned:** Container images and build cache (affects all Docker/Podman usage, not just yoloai). Orphaned seatbelt processes (complex detection, low frequency).

Options:
- `--dry-run`: Report only, don't ask or remove.
- `-y`/`--yes`: Skip confirmation prompt.
- `--backend`: Override runtime backend (default from config).

### `yoloai stop`

`yoloai stop <name>...` stops sandbox containers, preserving all state (work directory, agent-state, logs). Containers can be restarted later without losing progress.

Accepts multiple sandbox names (e.g., `yoloai stop sandbox1 sandbox2 sandbox3`). With `--all`, stops all running sandboxes.

Internally, `docker stop` sends SIGTERM and the agent terminates. The agent's state directory persists on the host. `yoloai start` relaunches a fresh agent process with state intact — Claude preserves session history (resumes context); Codex starts fresh (no built-in session persistence). Think of stop/start as pausing the sandbox environment, not the agent's thought process. Use `--resume` with `start` to re-feed the original task prompt.

Options:
- `--all`: Stop all running sandboxes.

### `yoloai start`

`yoloai start [-a|--attach] [--resume] <name>` ensures the sandbox is running — idempotent "get it running, however needed". Like `new`, starts detached by default.
- If the container has been removed: re-run full container creation from `meta.json` (skipping the copy step for `:copy` directories — state already exists in `work/`). Create a new credential temp file (ephemeral by design).
- If the container is stopped: starts it. If `--network-isolated`, iptables rules are reapplied by the entrypoint.
- If the container is running but the agent has exited: relaunches the agent in the existing tmux session.
- If already running: no-op.

This eliminates the need to diagnose *why* a sandbox isn't running before choosing a command.

**`-a`/`--attach` flag:** After the sandbox is running, automatically attach to the tmux session (equivalent to running `yoloai attach <name>` immediately after). Saves a round-trip for the common workflow of starting a sandbox and then interacting with it.

**`--resume` flag:** When used, the agent is relaunched in **interactive mode** (regardless of the original prompt delivery mode) with the original prompt from `prompt.txt` prefixed with a preamble: "You were previously working on the following task and were interrupted. The work directory contains your progress so far. Continue where you left off:" followed by the original prompt text. Interactive mode is always used for resume because the user may want to follow up or redirect. Error if the sandbox has no `prompt.txt` (was created without `--prompt`). Without `--resume`, `yoloai start` relaunches the agent in interactive mode with no prompt (user attaches and gives instructions manually).

### `yoloai restart`

`yoloai restart [-a|--attach] [--resume] <name>` is equivalent to `yoloai stop <name>` followed by `yoloai start <name>`. Use cases: recovering from a corrupted container environment, applying config changes that require a fresh container (e.g., new mounts or resource limits), or restarting a wedged agent process.

**`-a`/`--attach` flag:** After the sandbox is restarted, automatically attach to the tmux session.

**`--resume` flag:** Passed through to `start --resume` — the agent is relaunched with the original prompt prefixed with a continuation preamble.

### `yoloai reset`

`yoloai reset <name>` re-copies the workdir from the original host directory and resets the git baseline. Also clears the cache and files directories by default. Sandbox configuration (`meta.json`) is preserved. Only affects `:copy` directories — `:rw` directories reference the original and have no sandbox copy to reset.

**Default behavior (in-place):**

By default, the agent stays running and retains its conversational context while the workspace is reset underneath it. Cache and files directories are cleared. Use case: host repo got new upstream commits (user merged a PR, fetched), user wants to update the agent's copy without losing conversational context.

1. Re-sync workdir from host while container is running:
   - `rsync -a --delete` from original host dir to `work/<encoded-path>/` on the host (bind-mount makes changes immediately visible in container)
2. Re-create git baseline
3. Update `baseline_sha` in `meta.json`
4. Clear cache directory (unless `--keep-cache`)
5. Clear files directory (unless `--keep-files`)
6. Send notification to agent via tmux `send-keys`:
   - Default (with prompt): notification text + original prompt from `prompt.txt`
   - With `--no-prompt`: notification text only
7. Notification text: `"[yoloai] Workspace has been reset to match the current host directory. All previous changes have been reverted and any new upstream changes are now present. Re-read files before assuming their contents."`

Auto-upgrade to `--restart`: if the container is not running, overlay mode is used, or `--clear-state` is set, the reset automatically upgrades to restart mode.

**`--restart` behavior (stop and restart container):**

The container is stopped and restarted. The agent loses its conversational context but gets a clean workspace synced from the host. Use case: retry the same task with a fresh workspace after the agent has made undesired changes.

1. Stop the container (if running)
2. Delete `work/<encoded-path>/` contents
3. Re-copy workdir from original host dir via `cp -rp`
4. Re-create git baseline
5. Update `baseline_sha` in `meta.json`
6. If `--clear-state`, also delete and recreate `agent-runtime/` directory
7. Clear cache directory (unless `--keep-cache`)
8. Clear files directory (unless `--keep-files`)
9. Start container (entrypoint runs as normal)
10. If `prompt.txt` exists and `--no-prompt` not set, wait for agent ready and re-send prompt via tmux

Options:
- `--restart`: Stop and restart the container instead of resetting in-place.
- `--clear-state`: Also wipe agent runtime state (`agent-runtime/` directory). Implies `--restart`.
- `--keep-cache`: Preserve the cache directory (not cleared).
- `--keep-files`: Preserve the files directory (not cleared).
- `--no-prompt`: Skip re-sending the prompt after reset.
- `-a`/`--attach`: Auto-attach after restart. Implies `--restart`.
- `--debug`: Enable debug logging in sandbox entrypoint.

Implied behaviors:
- `--clear-state` implies `--restart` (can't wipe state while agent is running).
- `--attach` implies `--restart` (need to reconnect after restart).
- Overlay mode auto-upgrades to `--restart` (overlay requires container restart).
- Container not running auto-upgrades to `--restart`.

### `yoloai x` (Extensions)

`yoloai x <extension> [args...] [--flags...]`

Extensions are user-defined custom commands. Each extension is a separate YAML file in `~/.yoloai/extensions/` that defines its own arguments, flags, and a shell script action. `x` is short for "extension" to the command vocabulary. This is a power-user feature.

Extensions define all their own positional arguments — there are no built-in positionals. Extensions that create sandboxes include a name arg; extensions that operate on existing sandboxes or don't involve sandboxes at all define whatever args make sense.

Extensions declare which agents they support via the `agent` field — a single string (`agent: claude`), a list (`agent: [claude, codex]`), or omitted entirely for any-agent compatibility. yoloAI validates the current agent against this list before running the action. For extensions supporting multiple agents, the `$agent` variable lets the script branch on the active agent. For extensions that are fundamentally different per agent, create separate files (e.g., `lint-claude.yaml`, `lint-codex.yaml`).

**Extension format:**

Each extension is a standalone YAML file — self-contained, shareable, no external dependencies.

```yaml
# ~/.yoloai/extensions/from-issue.yaml
description: "Create a sandbox from a GitHub issue"

args:
  - name: issue
    description: "GitHub issue URL or number"
  - name: workdir
    description: "Repository directory"

flags:
  - name: model
    short: m
    description: "Model to use"
    default: "sonnet"

action: |
  title="$(gh issue view "${issue}" --json title -q .title)"
  body="$(gh issue view "${issue}" --json body -q .body)"
  slug="$(echo "${title}" | tr '[:upper:] ' '[:lower:]-' | tr -cd 'a-z0-9-' | head -c 40)"

  yoloai new "${slug}" "${workdir}":copy -a \
    --model "${model}" \
    --prompt "Fix this GitHub issue.

  Title: ${title}

  ${body}"
```

**Usage:**

```
# Create a sandbox from issue #42, working on the current directory
yoloai x from-issue 42 .

# Use a different model
yoloai x from-issue 42 . -m opus

# Works with URLs too
yoloai x from-issue https://github.com/org/repo/issues/42 ~/projects/repo
```

**How it works:**

1. yoloAI finds `~/.yoloai/extensions/from-issue.yaml` and parses its `args` and `flags` definitions.
2. yoloAI parses the user's command line against those definitions — all positional args are extension-defined.
3. If `agent` is specified in the YAML and doesn't match the current `--agent` / `defaults.agent`, error with a message suggesting the right extension.
4. All captured values are set as environment variables and the `action` script is executed via `sh -c`.

**Variables available to the action script:**

| Variable      | Source                                       |
|---------------|----------------------------------------------|
| `$agent`      | Resolved agent name (`--agent` / `defaults.agent`) |
| `$<arg_name>` | Each defined arg, by its `name` field        |
| `$<flag_name>`| Each defined flag, by its `name` field (default applied if not provided) |

Flags with hyphens in their name are available with underscores: `--max-turns` → `$max_turns`.

**More examples:**

```yaml
# ~/.yoloai/extensions/race.yaml
# Run the same task across multiple models in parallel, then compare diffs.
description: "Race models against each other on the same task"

args:
  - name: workdir
    description: "Directory to work on"
  - name: prompt
    description: "Task description"

flags:
  - name: models
    description: "Comma-separated models to race"
    default: "sonnet,opus"

action: |
  IFS=',' read -ra models <<< "${models}"
  pids=()
  for m in "${models[@]}"; do
    yoloai new "race-${m}" "${workdir}":copy --model "$m" --prompt "${prompt}" &
    pids+=($!)
  done
  wait "${pids[@]}"

  echo "All agents launched. Compare results:"
  for m in "${models[@]}"; do
    echo ""
    echo "=== ${m} ==="
    yoloai diff "race-${m}" --stat
  done
```

```yaml
# ~/.yoloai/extensions/refine.yaml
# Destroy a sandbox and recreate it with the previous diff and feedback
# baked into the new prompt, so the agent can improve on its first attempt.
description: "Iterate on a sandbox with feedback"

args:
  - name: sandbox
    description: "Existing sandbox to refine"
  - name: feedback
    description: "What to improve"

action: |
  info="$(yoloai sandbox info --json "${sandbox}")"
  workdir="$(echo "${info}" | jq -r .workdir.host_path)"
  model="$(echo "${info}" | jq -r .model)"
  prev_prompt="$(cat ~/.yoloai/sandboxes/${sandbox}/prompt.txt 2>/dev/null || echo '(none)')"
  prev_diff="$(yoloai diff "${sandbox}" 2>/dev/null || echo '(no changes)')"

  yoloai destroy --yes "${sandbox}"
  yoloai new "${sandbox}" "${workdir}":copy -a \
    --model "${model}" \
    --prompt "Previous attempt at this task produced the diff below.

  The user's feedback: ${feedback}

  Original prompt: ${prev_prompt}

  Previous diff:
  ${prev_diff}

  Incorporate the feedback and produce an improved version."
```

```yaml
# ~/.yoloai/extensions/pr-review.yaml
# Fetch a PR diff, have the agent review it, extract a structured report.
description: "AI code review for a pull request"
agent: claude

args:
  - name: workdir
    description: "Repository directory"

flags:
  - name: pr
    description: "PR number"
  - name: focus
    short: f
    description: "Review focus areas"
    default: "bugs, security, correctness"

action: |
  diff="$(cd "${workdir}" && gh pr diff "${pr}")"
  pr_title="$(cd "${workdir}" && gh pr view "${pr}" --json title -q .title)"

  yoloai new "review-pr${pr}" "${workdir}" \
    --prompt "Review this pull request. Focus on: ${focus}

  Title: ${pr_title}

  ${diff}

  Write your review to /yoloai/files/review.md with sections:
  Summary, Issues Found, Suggestions, Verdict."

  echo "Review written. Retrieve it with:"
  echo "  yoloai files get review-pr${pr} review.md"
```

Extensions compose yoloai with the rest of the unix ecosystem — `gh`, `jq`, `git`, and any other tools. They're most valuable for multi-step workflows that cross the boundary of a single sandbox: orchestrating parallel runs, feeding one sandbox's output into another, or integrating with external services.

**Listing extensions:**

`yoloai x` with no arguments scans `~/.yoloai/extensions/` and shows each extension's name, description, agent, and defined args/flags.

**Validation:**

- Extension name derived from filename (e.g., `lint.yaml` → `lint`). Must not collide with built-in yoloAI commands.
- `args` are positional and order matters — parsed in definition order.
- `flags` support `short` (single char), `default` (string), and `description`. Flag names and shorts must not collide with yoloAI's global flags (`--verbose`/`-v`, `--quiet`/`-q`, `--yes`/`-y`, `--no-color`, `--help`/`-h`) — error at parse time if they do.
- Missing required args (no `default`) produce a usage error with the extension's arg definitions.
- The `action` field is required.

### `yoloai files`

Bidirectional file exchange between host and sandbox. Files live in `~/.yoloai/sandboxes/<name>/files/` on the host, mounted read-write at `/yoloai/files/` inside the sandbox. Both the user and the agent can read and write here. The directory is created when the sandbox is created and destroyed with `yoloai destroy`.

**Purpose:** Pass reference material to the agent (logs, specs, screenshots) without polluting the work directory's git state, and retrieve artifacts the agent produces (reports, generated files) without them appearing in `yoloai diff` / `yoloai apply`.

**Mount details:**

| Backend  | Mechanism | Read-only enforcement |
|----------|-----------|----------------------|
| Docker   | Bind mount to `/yoloai/files/` | N/A (intentionally rw) |
| Tart     | VirtioFS share, remapped to `/Users/admin/.yoloai/files/` | N/A (intentionally rw) |
| Seatbelt | SBPL profile grants rw on sandbox dir; accessible at source path | N/A (intentionally rw) |

#### `yoloai files <sandbox> put <file/glob>...`

Copy one or more files or directories into the sandbox's exchange directory. Arguments can be literal paths or glob patterns — quoted globs (e.g., `"*.txt"`) are expanded by the tool if the shell didn't expand them. Directories are copied recursively. If a target file already exists, the command fails with an error listing the conflicting paths.

Options:
- `--force`: Overwrite existing files instead of failing.

#### `yoloai files <sandbox> get <file/glob>... [-o dir]`

Copy files or directories from the sandbox's exchange directory to the host. Arguments are relative to the exchange directory and may be literal names or glob patterns. Directories are copied recursively. Multiple files/globs can be specified.

Options:
- `-o`, `--output`: Destination directory (or file path for a single file). Defaults to `.` (current directory). When getting multiple items, the destination must be an existing directory.
- `--force`: Overwrite existing destination files instead of failing.

#### `yoloai files <sandbox> ls [glob]...`

List files in the exchange directory. Without a glob, lists everything (implicit `*`). Multiple glob patterns can be specified — results are deduplicated and sorted. Glob matching does not treat dotfiles specially — `*` matches `.hidden` files. Output is one path per line, relative to the exchange directory.

#### `yoloai files <sandbox> rm <glob>...`

Remove files matching the glob(s) from the exchange directory. At least one glob argument is required — no implicit wildcard. Multiple patterns can be specified. Removes read-only files without prompting (uses whatever OS operations are needed to force deletion). Glob matching does not treat dotfiles specially.

Prints the list of removed files. If no files match any pattern, exits with an error.

#### `yoloai files <sandbox> path`

Print the absolute host-side path to the exchange directory (`~/.yoloai/sandboxes/<name>/files/`). Useful for direct manipulation with host tools (`cp`, `rsync`, `open`, etc.).

### Cache Directory

The cache directory (`~/.yoloai/sandboxes/<name>/cache/`) is mounted read-write at `/yoloai/cache/` inside the sandbox. It provides the agent with persistent scratch space for data that speeds up its work: cached HTTP responses, shallow-cloned Git repos, downloaded archives, and other reusable data. The agent context instructs it to check the cache before fetching URLs and to clone repos locally rather than fetching files over HTTPS.

| Backend  | Mechanism | Path inside sandbox |
|----------|-----------|---------------------|
| Docker   | Bind mount to `/yoloai/cache/` | `/yoloai/cache/` |
| Tart     | VirtioFS share | Backend-specific |
| Seatbelt | SBPL profile grants rw on sandbox dir | Source path |

The cache persists across `stop`/`start` cycles and is destroyed with `yoloai destroy`. `yoloai reset --clean` also clears it.

### Image Cleanup

Docker images (`yoloai-base`, `yoloai-<profile>`) accumulate indefinitely. A cleanup mechanism is needed but deferred pending research into Docker's image lifecycle: base images are shared parents of profile images, profile images may have running containers, layer caching means "removing" doesn't necessarily free space, and `docker image prune` vs `docker rmi` have different semantics. Half-baked pruning could break running sandboxes or nuke images the user spent time building.


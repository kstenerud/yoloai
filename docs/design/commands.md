> **Design documents:** [Overview](README.md) | [Config](config.md) | [Setup](setup.md) | [Security](security.md) | [Research](../dev/RESEARCH.md)

### 4. CLI (`yoloai`)

Single Go binary. No runtime dependencies — just the binary and Docker.

**Global flags:**
- `--verbose` / `-v`: Enable verbose output showing Docker commands, mount operations, config resolution, and entrypoint activity. Essential for troubleshooting overlay mount failures, proxy startup issues, and entrypoint errors. Also settable via `YOLOAI_VERBOSE=1`.
- `--quiet` / `-q`: Suppress non-essential output. `-q` for warn-only, `-qq` for error-only.
- `--no-color`: Disable colored output.

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
  yoloai start [-a] [--resume] <name>             Start a stopped sandbox  (--resume [PLANNED])
  yoloai stop <name>...                          Stop sandboxes (preserving state)
  yoloai destroy <name>...                       Stop and remove sandboxes
  yoloai reset <name>                            Re-copy workdir and reset git baseline
  yoloai restart <name>                          Restart the agent in an existing sandbox  [PLANNED]

Inspection:
  yoloai system                                  System information and management
  yoloai system info                             Show version, paths, disk usage, backend availability
  yoloai system agents [name]                    List available agents
  yoloai system backends [name]                  List available runtime backends
  yoloai system build [profile|--all]            Build/rebuild Docker image(s)  (profile/--all [PLANNED])
  yoloai system prune                            Remove orphaned backend resources and stale temp files
  yoloai system setup                            Run interactive setup  (--power-user [PLANNED])
  yoloai sandbox                                 Sandbox inspection
  yoloai sandbox list                            List sandboxes and their status
  yoloai sandbox info <name>                     Show sandbox configuration and state
  yoloai sandbox log <name>                      Show sandbox session log
  yoloai sandbox exec <name> <command>           Run a command inside the sandbox
  yoloai ls                                      List sandboxes (shortcut for 'sandbox list')
  yoloai log <name>                              Show sandbox log (shortcut for 'sandbox log')

Admin:
  yoloai config get [key]                        Print configuration values (all or specific key)
  yoloai config set <key> <value>                Set a configuration value
  yoloai profile create <name> [--template <tpl>]  Create a profile with scaffold  [PLANNED]
  yoloai profile list                            List profiles  [PLANNED]
  yoloai profile delete <name>                   Delete a profile  [PLANNED]
  yoloai x <extension> <name> [args...] [--flags...]  Run a user-defined extension  [PLANNED]
  yoloai completion [bash|zsh|fish|powershell]   Generate shell completion script
  yoloai version                                 Show version information
```

### Agent Definitions

Built-in agent definitions (v1). Each agent specifies its install method, launch commands, API key requirements, and behavioral characteristics. No user-defined agents in v1.

| Field                         | Claude                                                    | Gemini                                          | test                                           | Codex                                          |
|-------------------------------|-----------------------------------------------------------|-------------------------------------------------|-------------------------------------------------|------------------------------------------------|
| Install                       | `npm i -g @anthropic-ai/claude-code`                      | `npm i -g @google/gemini-cli`                   | (none — bash is built-in)                       | `npm i -g @openai/codex`                       |
| Runtime                       | Node.js                                                   | Node.js                                         | None                                            | Node.js                                        |
| Interactive cmd               | `claude --dangerously-skip-permissions`                   | `gemini --yolo`                                 | `bash`                                          | `codex --dangerously-bypass-approvals-and-sandbox` |
| Headless cmd                  | `claude -p "PROMPT" --dangerously-skip-permissions`       | `gemini -p "PROMPT" --yolo`                     | `sh -c "PROMPT"`                                | `codex exec --dangerously-bypass-approvals-and-sandbox "PROMPT"` |
| Default prompt mode           | interactive                                               | interactive                                     | headless (with prompt) / interactive (without)  | interactive                                    |
| Submit sequence (interactive) | `Enter Enter` (double) + ready-pattern polling            | `Enter` + 3s startup delay                      | `Enter` + 0s startup delay                      | `Enter` + ready-pattern polling                |
| API key env vars              | `ANTHROPIC_API_KEY`                                       | `GEMINI_API_KEY`                                | (none)                                          | `CODEX_API_KEY` (preferred), `OPENAI_API_KEY` (fallback) |
| State directory               | `~/.claude/`                                              | `~/.gemini/`                                    | (none)                                          | `~/.codex/`                                    |
| Model flag                    | `--model <model>`                                         | `--model <model>`                               | (ignored)                                       | `--model <model>`                              |
| Model aliases                 | `sonnet` → `claude-sonnet-4-latest`, `opus` → `claude-opus-4-latest`, `haiku` → `claude-haiku-4-latest` | `pro` → `gemini-2.5-pro`, `flash` → `gemini-2.5-flash` | (none) | (none yet) |
| Ready pattern                 | `❯` (polls tmux output; replaces fixed delay)             | (none — uses startup delay; needs testing)      | (none — uses fixed delay)                       | `›` (polls tmux output)                        |
| Seed files                    | `.credentials.json` (auth-only), `settings.json`, `.claude.json` (home dir) | `oauth_creds.json` (auth-only), `google_accounts.json` (auth-only), `settings.json` | (none)                | `auth.json` (auth-only), `config.toml`         |
| Non-root required             | Yes (refuses as root)                                     | No                                              | No                                              | No (convention is non-root)                    |
| Proxy support                 | Yes (npm install only, not native binary)                 | TBD                                             | N/A                                             | TBD (research needed)                          |
| Default network allowlist     | `api.anthropic.com`, `statsig.anthropic.com`, `sentry.io` | `generativelanguage.googleapis.com`             | (none)                                          | `api.openai.com` (minimum; additional TBD)     |
| Extra env vars / quirks       | —                                                         | Sandbox disabled by default; `--yolo` auto-approves tool calls. OAuth login also supported but API key is the primary auth path. | Deterministic shell-based agent for development and bug report reproduction. No API key needed. Prompt IS the shell script. | Landlock sandbox fails in containers — use `--dangerously-bypass-approvals-and-sandbox`. Auth via `auth.json` (browser OAuth cache) or API key env vars. `cli_auth_credentials_store = "file"` must be set in `config.toml` for file-based auth. |

**Ready pattern:** Agents can specify a `ready_pattern` — a string the entrypoint polls for in the tmux pane output to determine when the agent is ready to receive a prompt. This replaces the fixed startup delay with responsive detection. Claude uses `❯` (the prompt character). While polling, the entrypoint auto-accepts confirmation prompts (e.g., workspace trust dialogs) by detecting "Enter to confirm" and sending Enter. After the pattern is found, the entrypoint waits for screen output to stabilize before delivering the prompt. Agents without a ready pattern fall back to the fixed `startup_delay`.

**Seed files:** Agents can specify files to copy from the host into the container's agent-state directory at sandbox creation time. These seed agent configuration and credentials without bind-mounting (sandbox gets its own copy). Claude seeds `.credentials.json` (auth-only — skipped when `ANTHROPIC_API_KEY` is set), `settings.json` (Claude Code settings), and `.claude.json` (from home dir — global Claude config). The `auth_only` flag means the file is only needed when no API key is provided via environment variable.

**Prompt delivery modes:**

- **Interactive mode:** Agent starts in interactive mode inside tmux. If `--prompt` is provided, prompt is fed via `tmux send-keys` after a startup delay. Agent stays running for follow-up via `yoloai attach`. This is Claude's natural mode.
- **Headless mode:** Agent launched with prompt as CLI arg inside tmux (tmux still used for logging + attach). Runs to completion. This is Codex's natural mode via `codex exec --dangerously-bypass-approvals-and-sandbox "PROMPT"`. Without `--prompt`, Codex falls back to interactive mode.

The agent definition determines the default prompt delivery mode. `--prompt` selects headless mode for agents that support it (Codex); Claude always uses interactive mode regardless.

### `yoloai new`

`yoloai new [options] <name> <workdir> [-d <auxdir>...]`

The **workdir** is the single primary project directory — the agent's working directory. It is positional (after name) and defaults to `:copy` mode if no suffix is given. The `:rw` suffix must be explicit. **[PLANNED]** The workdir will become optional when a profile provides one; currently it is always required.

**`-d` / `--dir`** specifies auxiliary directories (repeatable). Default read-only. Additive with profile directories.

**[PLANNED]** When both CLI and profile provide directories: CLI workdir **replaces** profile workdir. CLI `-d` dirs are **additive** with profile dirs.

Directory argument syntax: `<path>[:<suffixes>][=<mount-point>]`

Suffixes (combinable in any order):
- `:rw` — bind-mounted read-write (live, immediate changes)
- `:copy` — copied to sandbox state, read-write, diff/apply available
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
- **[PLANNED]** `--profile <name>`: Use a profile's derived image and mounts. No profile = base image + defaults only.
- `--prompt` / `-p` `<text>`: Initial prompt/task for the agent (see Prompt Mechanism below). Use `--prompt -` to read from stdin. Mutually exclusive with `--prompt-file`.
- `--prompt-file` / `-f` `<path>`: Read prompt from a file. Use `--prompt-file -` to read from stdin. Mutually exclusive with `--prompt`.
- `--model` / `-m` `<model>`: Model to use. Passed to the agent's `--model` flag. If omitted, uses the agent's default. Accepts built-in aliases (see Agent Definitions) or full model names. **[PLANNED]** User-configurable aliases in config.yaml, plus version pinning to prevent surprise behavior changes.
- `--agent <name>`: Agent to use (`claude`, `test`, `codex`). Overrides `defaults.agent` from config.
- **[PLANNED]** `--network-isolated`: Allow only the agent's required API traffic. The agent can function but cannot access other external services, download arbitrary binaries, or exfiltrate code.
- **[PLANNED]** `--network-allow <domain>`: Allow traffic to specific additional domains (can be repeated). Implies `--network-isolated`. Added to the agent's default allowlist (see below).
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
| `statsig.anthropic.com` | Telemetry/feature flags (recommended) |
| `sentry.io`             | Error reporting (recommended)         |

**Gemini CLI:**

| Domain                                 | Purpose                       |
|----------------------------------------|-------------------------------|
| `generativelanguage.googleapis.com`    | API calls (required)          |
| `cloudcode-pa.googleapis.com`          | OAuth auth route (recommended)|

**Codex:**

| Domain           | Purpose              |
|------------------|----------------------|
| `api.openai.com` | API calls (required) |

> **Note:** Codex may require additional domains (telemetry, model downloads). This needs verification. See [RESEARCH.md](../dev/RESEARCH.md) for known gaps.

The allowlist is agent-specific — each agent's definition includes its required domains. `--network-allow` domains are additive with the selected agent's defaults.

**Workflow:**

1. Apply `:copy` suffix to workdir if no mode suffix (`:rw` or `:copy`) is given — `:force` is a modifier, not a mode, so `./my-app:force` is treated as `./my-app:copy:force`.
2. Error if any two directories resolve to the same absolute container path (mirrored host path or custom `=<path>`).
3. For each `:copy` directory, set up an isolated writable view using one of two strategies (selected by `copy_strategy` config, default `auto`):

   **[PLANNED] Overlay strategy** (default when available):
   - Host side: bind-mount the original directory read-only into the container at its original absolute path. Provide an empty upper directory from `~/.yoloai/sandboxes/<name>/work/<encoded-path>/`, where `<encoded-path>` is the absolute host path with path separators and filesystem-unsafe characters encoded using [caret encoding](https://github.com/kstenerud/caret-encoding) (e.g., `/home/user/my-app` → `^2Fhome^2Fuser^2Fmy-app`). This is fully reversible and avoids collisions when multiple directories share the same basename.
   - Container side (entrypoint): mount overlayfs with `lowerdir` (original, read-only), `upperdir` (from host `work/<encoded-path>/upper/`), and `workdir` (from host `work/<encoded-path>/work/` — required by overlayfs for atomic copy-up operations) merged at the mirrored host path. The agent sees the full project; writes go to upper only. Original directory is inherently protected (read-only lower layer). The entrypoint must be idempotent — use `mkdir -p` for directories and check `mountpoint -q` before mounting, so it handles both fresh starts and restarts cleanly.
   - After overlay mount, the entrypoint creates the git baseline on the merged view: `git init` + `git add -A` + `git commit -m "initial"`. If the directory is already a git repo, the baseline SHA was recorded in `meta.json` by host-side yoloAI at sandbox creation time — the entrypoint skips git init.
   - Requires `CAP_SYS_ADMIN` capability on the container (not full `--privileged`).

   **Full copy strategy** (fallback):
   All steps run on the host via yoloAI before container start:
   - If the directory is a git repo, record the current HEAD SHA in `meta.json`.
   - Copy via `cp -rp` to `~/.yoloai/sandboxes/<name>/work/<encoded-path>/` (same caret-encoding scheme as overlay), then mount at the mirrored host path inside the container. `cp -rp` preserves permissions, timestamps, and symlinks (POSIX-portable; `cp -a` is GNU-specific and unavailable on macOS). Everything is copied including `.git/` and files ignored by `.gitignore`.
   - If the copy already has a `.git/` directory (from the original repo), use the recorded SHA as the baseline — `yoloai diff` will diff against it.
   - If the copy has no `.git/`, `git init` + `git add -A` + `git commit -m "initial"` to create a baseline.
   The container receives a ready-to-use directory with a git baseline already established.

   **Currently uses the full copy strategy only.** Both strategies produce the same result from the user's perspective: a protected, writable view with git-based diff/apply. The overlay strategy is instant and space-efficient; the full copy strategy is more portable. `auto` tries overlay first, falls back to full copy if `CAP_SYS_ADMIN` is unavailable or the kernel doesn't support nested overlayfs. Explicitly setting `copy_strategy: overlay` when overlay is not available is an error (the user asked for something specific — don't silently degrade).

   **[PLANNED] `auto` detection strategy** (following the pattern used by containers/storage, Podman, and Docker itself):
   1. Check `/proc/filesystems` for `overlay` — if absent, skip to full copy.
   2. Check `CAP_SYS_ADMIN` via `/proc/self/status` `CapEff` bitmask (bit 21) — if absent, skip to full copy.
   3. Attempt a test mount in a temp directory (`mount -t overlay` with `lowerdir`, `upperdir`, `workdir`). This is the authoritative test — it validates kernel support, security profiles (seccomp, AppArmor), and backing filesystem compatibility simultaneously. If it fails, fall back to full copy.
   4. Cache the result in `~/.yoloai/cache/overlay-support`, invalidated on kernel or Docker version change.

   Note: `git add -A` naturally honors `.gitignore` if one is present, so gitignored files (e.g., `node_modules`) won't clutter `yoloai diff` output regardless of strategy.
4. **[PLANNED]** If `auto_commit_interval` > 0, start a background auto-commit loop for `:copy` directories inside the container for recovery. The interval is passed to the container via `config.json`. Disabled by default.
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

1. **[PLANNED]** Generate a **sandbox context file** on the host (in the sandbox state directory) describing the environment for the agent: which directory is the workdir, which are auxiliary, mount paths and access mode of each (read-only / read-write / copy), available tools, and how auto-save works. Bind-mounted read-only into the container at `/yoloai/context.md` (same pattern as `log.txt` and `prompt.txt`). This file lives outside the work tree so it never pollutes project files, git baselines, or diffs. The agent is pointed to it via agent-specific mechanisms (`--append-system-prompt` for Claude, inclusion in the initial prompt for Codex).
2. Generate `/yoloai/config.json` on the host (in the sandbox state directory) containing all entrypoint configuration: agent_command, startup_delay, submit_sequence, host_uid, host_gid, and later overlay_mounts, iptables_rules, setup_script. This is bind-mounted into the container and read by the entrypoint.
3. Start Docker container (as non-root user `yoloai`) with:
   - **[PLANNED]** When `--network-isolated`: `HTTPS_PROXY` and `HTTP_PROXY` env vars pointing to the proxy sidecar (required — Claude Code's npm installation honors these via undici; Codex proxy support is TBD — see RESEARCH.md)
   - `:copy` directories: overlay strategy mounts originals as overlayfs lower layers with upper dirs from sandbox state; full copy strategy mounts copies from sandbox state. Both at their mount point (mirrored host path or custom `=<path>`, read-write)
   - `:rw` directories bind-mounted at their mount point (mirrored host path or custom `=<path>`, read-write)
   - Default (no suffix) directories bind-mounted at their mount point (mirrored host path or custom `=<path>`, read-only)
   - `agent-state/` mounted at the agent's state directory path (read-write, per-sandbox)
   - **[PLANNED]** Files listed in `agent_files` (from config) copied into `agent-state/` on first run
   - `log.txt` from sandbox state bind-mounted at `/yoloai/log.txt` (read-write, for tmux `pipe-pane`)
   - `prompt.txt` from sandbox state bind-mounted at `/yoloai/prompt.txt` (read-only, if provided)
   - `/yoloai/config.json` bind-mounted read-only
   - Config mounts from defaults + profile
   - Resource limits from defaults + profile
   - API key(s) injected via file-based bind mount at `/run/secrets/` — env var names from agent definition (see [security.md](security.md#credential-management))
   - **[PLANNED]** `CAP_SYS_ADMIN` capability (required for overlayfs mounts inside the container; omitted when `copy_strategy: full`). `CAP_NET_ADMIN` added when `--network-isolated` is used (required for iptables rules; independent capability, not included in `CAP_SYS_ADMIN`)
   - Container name: `yoloai-<name>`
   - User: `yoloai` (UID/GID matching host user)
   - `/yoloai/` internal directory for sandbox context file, overlay working directories, and bind-mounted state files (`log.txt`, `prompt.txt`, `config.json`)
4. **[PLANNED]** Run `setup` commands from config (if any).
5. Start tmux session named `main` with logging to `/yoloai/log.txt` (`tmux pipe-pane`) and `remain-on-exit on` (container stays up after agent exits, only stops on explicit `yoloai stop` or `yoloai destroy`). Tmux config sourced based on the `tmux_conf` value in `config.json` (see [setup.md](setup.md#tmux-configuration)).
6. Inside tmux: launch the agent using the command from its agent definition (e.g., `claude --dangerously-skip-permissions [--model X]` or `codex --yolo`).
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

Profile, network, and ports lines are omitted when using defaults (base image, unrestricted network, no ports). Strategy line is omitted when using `full` copy (only shown for overlay since it implies `CAP_SYS_ADMIN`).

### `yoloai attach`

Runs `docker exec -it yoloai-<name> tmux attach -t main`.

Detach with standard tmux `Ctrl-b d` — container keeps running.

### `yoloai sandbox info <name>`

Displays sandbox configuration and state:
- Name
- Status (running / stopped / done / failed)
- Agent (claude, codex, etc.)
- Model (if specified)
- [PLANNED] Profile (name or "(base)")
- Prompt (first 200 chars from `prompt.txt`, or "(none)")
- Workdir (resolved absolute path with mode)
- Network (if non-default, e.g., "none")
- Ports (if any)
- [PLANNED] Directories with access modes (read-only / rw / copy)
- Creation time
- Baseline SHA (for `:copy` directories that were git repos, or "(synthetic)" for non-git dirs)
- Container ID
- Changes (yes/no/- — same detection as `list`)

Reads from `meta.json` and queries live Docker state. Agent status is detected via `docker exec tmux list-panes -t main -F '#{pane_dead}'` combined with Docker container state for full status (running / stopped / done / failed). Useful for quick inspection without listing all sandboxes.

### `yoloai diff`

For `:copy` directories: runs `git add -A` (to capture untracked files created by the agent) then `git diff` against the baseline (the recorded HEAD SHA for existing repos, or the synthetic initial commit for non-git dirs). Shows exactly what the agent changed with proper diff formatting. For the full copy strategy, runs on the host (reads `work/` directly). For the overlay strategy, runs inside the container via `docker exec` (the merged view requires the overlay mount). Same as `yoloai apply` — see that section for details.

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

**Strategy selection:** For the **full copy** strategy, runs entirely on the host — reads from `work/<encoded-path>/`. Does not require the container to be running. For the **overlay** strategy, requires the container to be running (the merged view only exists when overlayfs is mounted). If the container is stopped, `apply` auto-starts it (printing "Starting container for overlay diff..." to stderr) and leaves it in whatever state it was before. Works identically from the user's perspective regardless of `copy_strategy`.

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

- `--squash`: Flatten all changes (commits + uncommitted) into a single unstaged patch. This is the legacy behavior — generates one `git diff <baseline>` and applies it with `git apply`. Shows a summary via `git diff --stat` and verifies cleanly with `git apply --check` before prompting for confirmation. Useful when you want to review everything as a single diff before committing.
- `--no-wip`: Skip uncommitted changes, only apply commits. Has no effect with `--squash` (which always includes everything). Has no effect when there are no commits beyond baseline.
- `--patches <dir>`: Export `.patch` files to the specified directory instead of applying. Also exports `wip.diff` if uncommitted changes exist (unless `--no-wip`). Prints instructions for manual application (`git am --3way <dir>/*.patch`). Useful for selective commit application — the user can delete unwanted `.patch` files before running `git am`, or use standard git tools (`git rebase -i`, `git cherry-pick`) after importing.
- `--force`: Proceed even if the host repo has uncommitted changes.

### `yoloai destroy`

`yoloai destroy <name>...`

`docker stop` + `docker rm` the container (and proxy sidecar if `--network-isolated`). Removes `~/.yoloai/sandboxes/<name>/` entirely. No special overlay cleanup needed — the kernel tears down the mount namespace when the container stops.

Accepts multiple sandbox names (e.g., `yoloai destroy sandbox1 sandbox2 sandbox3`) with a single confirmation prompt showing all sandboxes to be destroyed.

**Smart confirmation:** Confirmation is only required when the agent is still running or unapplied changes exist (detected via `git status --porcelain` on the host-side work directory, consistent with `list` CHANGES detection). If the sandbox is stopped/exited with no unapplied changes, destruction proceeds without prompting. `--yes` skips all confirmation regardless.

Options:
- `--all`: Destroy all sandboxes (confirmation required unless `--yes` is also provided).
- `--yes`: Skip confirmation prompts.

### `yoloai sandbox log` / `yoloai log`

`yoloai log <name>` displays the session log (`log.txt`) for the named sandbox. Auto-pages through `$PAGER` / `less -R` when stdout is a TTY, matching `git log` behavior. When piped (stdout is not a TTY), outputs raw for composition with unix tools: `yoloai log my-task | tail -100`, `yoloai log my-task | grep error`.

### `yoloai sandbox exec`

`yoloai exec <name> <command>` runs a command inside the sandbox container without attaching to tmux. Useful for debugging (`yoloai exec my-sandbox bash`) or quick operations (`yoloai exec my-sandbox npm install foo`).

Implemented as `docker exec yoloai-<name> <command>`, with `-i` added when stdin is a pipe/TTY and `-t` added when stdin is a TTY. This allows both interactive use (`yoloai exec my-sandbox bash`) and non-interactive use (`yoloai exec my-sandbox ls`, `echo "test" | yoloai exec my-sandbox cat`).

### `yoloai sandbox list` / `yoloai ls`

Lists all sandboxes with their current status.

| Column  | Description                                                    |
|---------|----------------------------------------------------------------|
| NAME    | Sandbox name                                                   |
| STATUS  | `running`, `stopped`, `done` (exit 0), `failed` (non-zero exit) |
| AGENT   | Agent name (`claude`, `test`, `codex`)                         |
| PROFILE | [PLANNED] Profile name or `(base)`                            |
| AGE     | Time since creation                                            |
| WORKDIR | Working directory path                                         |
| CHANGES | `yes` if unapplied changes exist, `no` if clean, `-` if unknown. Detected via `git status --porcelain` on the host-side work directory (any output = changes; read-only, catches both tracked modifications and untracked files; no Docker needed). |

Agent exit status is detected via `tmux list-panes -t main -F '#{pane_dead_status}'` when `#{pane_dead}` is 1. Non-zero exit code shows STATUS as "failed"; exit 0 shows as "done". Running containers with live panes show "running"; stopped containers show "stopped".

Top-level shortcut: `yoloai ls`.

[PLANNED] Options:
- `--running`: Show only running sandboxes.
- `--stopped`: Show only stopped sandboxes.
- `--json`: Output as JSON for scripting.

### `yoloai system build`

`yoloai system build` with no arguments rebuilds the base image (`yoloai-base`).

**[PLANNED]** `yoloai system build <profile>` rebuilds a specific profile's image (which derives from `yoloai-base`).

**[PLANNED]** `yoloai system build --all` rebuilds everything: base image first, then the proxy image (`yoloai-proxy`), then all profile images.

**[PLANNED]** `yoloai system build` and `yoloai system build --all` also build the proxy sidecar image (`yoloai-proxy`), a purpose-built Go forward proxy (~200-300 lines) used by `--network-isolated`. Compiled as a static binary in a `FROM scratch` image (~5 MB). Uses HTTPS CONNECT tunneling with domain-based allowlist (no MITM). Allowlist loaded from a config file; reloadable via SIGUSR1. Logs allowed/denied requests. See [RESEARCH.md](../dev/RESEARCH.md) "Proxy Sidecar Research" for the evaluation of alternatives.

Useful after modifying a profile's Dockerfile or when the base image needs updating (e.g., new Claude CLI version).

**[PLANNED]** Profile Dockerfiles that install private dependencies (e.g., `RUN go mod download` from a private repo, `RUN npm install` from a private registry) need build-time credentials. yoloAI passes host credentials to Docker BuildKit via `--secret` so they're available during the build but never stored in image layers. Example: `RUN --mount=type=secret,id=npmrc,target=/root/.npmrc npm install` in the Dockerfile, with yoloAI automatically providing `~/.npmrc` as the secret source. Supported secret sources are documented in `yoloai system build --help`.

### `yoloai system prune`

`yoloai system prune` scans for orphaned backend resources and stale temporary files, reports what it finds, and (after confirmation) removes them.

**What gets pruned:**

- **Docker:** Containers named `yoloai-*` that have no corresponding sandbox directory.
- **Tart:** VMs named `yoloai-*` that have no corresponding sandbox directory.
- **Seatbelt:** No-op (no central instance registry; processes are tied to sandbox dirs).
- **Cross-backend:** `/tmp/yoloai-*` directories older than 1 hour (leaked secrets, apply, format-patch temps).

**Broken sandbox warnings:** Sandbox directories with missing or corrupt `meta.json` are reported as warnings (with full path and suggested `yoloai destroy` command) but are NOT deleted — they may contain recoverable work.

**What is NOT pruned:** Docker images and build cache (affects all Docker usage, not just yoloai). Orphaned seatbelt processes (complex detection, low frequency).

Options:
- `--dry-run`: Report only, don't ask or remove.
- `-y`/`--yes`: Skip confirmation prompt.
- `--backend`: Override runtime backend (default from config).

### `yoloai stop`

`yoloai stop <name>...` stops sandbox containers (and proxy sidecars if `--network-isolated`), preserving all state (work directory, agent-state, logs). Containers can be restarted later without losing progress.

Accepts multiple sandbox names (e.g., `yoloai stop sandbox1 sandbox2 sandbox3`). With `--all`, stops all running sandboxes.

Internally, `docker stop` sends SIGTERM and the agent terminates. The agent's state directory persists on the host. `yoloai start` relaunches a fresh agent process with state intact — Claude preserves session history (resumes context); Codex starts fresh (no built-in session persistence). Think of stop/start as pausing the sandbox environment, not the agent's thought process. Use `--resume` with `start` to re-feed the original task prompt.

Options:
- `--all`: Stop all running sandboxes.

### `yoloai start`

`yoloai start [-a|--attach] [--resume] <name>` ensures the sandbox is running — idempotent "get it running, however needed". Like `new`, starts detached by default.
- If the container has been removed: re-run full container creation from `meta.json` (skipping the copy step for `:copy` directories — state already exists in `work/`). Create a new credential temp file (ephemeral by design).
- If the container is stopped: starts it (and proxy sidecar if `--network-isolated`). The entrypoint re-establishes overlayfs mounts (mounts don't survive `docker stop` — this is by design; the upper directory persists on the host and the entrypoint re-mounts idempotently).
- If the container is running but the agent has exited: relaunches the agent in the existing tmux session.
- If already running: no-op.

This eliminates the need to diagnose *why* a sandbox isn't running before choosing a command.

**`-a`/`--attach` flag:** After the sandbox is running, automatically attach to the tmux session (equivalent to running `yoloai attach <name>` immediately after). Saves a round-trip for the common workflow of starting a sandbox and then interacting with it.

**[PLANNED] `--resume` flag:** When used, the agent is relaunched in **interactive mode** (regardless of the original prompt delivery mode) with the original prompt from `prompt.txt` prefixed with a preamble: "You were previously working on the following task and were interrupted. The work directory contains your progress so far. Continue where you left off:" followed by the original prompt text. Interactive mode is always used for resume because the user may want to follow up or redirect. Error if the sandbox has no `prompt.txt` (was created without `--prompt`). Without `--resume`, `yoloai start` relaunches the agent in interactive mode with no prompt (user attaches and gives instructions manually).

### [PLANNED] `yoloai restart`

`yoloai restart <name>` is equivalent to `yoloai stop <name>` followed by `yoloai start <name>`. Use cases: recovering from a corrupted container environment, applying config changes that require a fresh container (e.g., new mounts or resource limits), or restarting a wedged agent process.

### `yoloai reset`

`yoloai reset <name>` re-copies the workdir from the original host directory and resets the git baseline. Sandbox configuration (`meta.json`) is preserved. Only affects `:copy` directories — `:rw` directories reference the original and have no sandbox copy to reset.

**Default behavior (restart):**

By default, the container is stopped and restarted. The agent loses its conversational context but gets a clean workspace synced from the host. Use case: retry the same task with a fresh workspace after the agent has made undesired changes.

1. Stop the container (if running)
2. Delete `work/<encoded-path>/` contents
3. Re-copy workdir from original host dir via `cp -rp`
4. Re-create git baseline
5. Update `baseline_sha` in `meta.json`
6. If `--clean`, also delete and recreate `agent-state/` directory
7. Start container (entrypoint runs as normal)
8. If `prompt.txt` exists and `--no-prompt` not set, wait for agent ready and re-send prompt via tmux

**`--no-restart` behavior (keep agent running):**

The agent stays running and retains its conversational context while the workspace is reset underneath it. Use case: host repo got new upstream commits (user merged a PR, fetched), user wants to update the agent's copy without losing conversational context.

1. Re-sync workdir from host while container is running:
   - Full copy strategy: `rsync -a --delete` from original host dir to `work/<encoded-path>/` on the host (bind-mount makes changes immediately visible in container)
   - [PLANNED] Overlay strategy: `docker exec` to unmount overlay, clear upper/work dirs, remount overlay (lower dir already reflects latest host state)
2. Re-create git baseline inside container via `docker exec` (`git add -A && git commit -m "yoloai baseline" --allow-empty`)
3. Update `baseline_sha` in `meta.json`
4. Send notification to agent via tmux `send-keys`:
   - Default (with prompt): notification text + original prompt from `prompt.txt`
   - With `--no-prompt`: notification text only
5. Notification text: `"[yoloai] Workspace has been reset to match the current host directory. All previous changes have been reverted and any new upstream changes are now present. Re-read files before assuming their contents."`

Options:
- `--no-prompt`: Skip re-sending the prompt after reset (applies to both default and `--no-restart` modes).
- `--clean`: Wipe agent-state in addition to re-copying workdir. Full reset of both workspace and agent memory. Mutually exclusive with `--no-restart` — error if both specified: "Cannot wipe agent state while agent is running. Use --clean without --no-restart, or stop the agent first."
- `--no-restart`: Keep the agent running instead of stopping/restarting. Resets the workspace in-place and sends a notification to the agent. Requires the agent to be idle (if the agent is actively writing, results are undefined — caveat, not enforced).

Constraints:
- `--clean` + `--no-restart` is an error (see above).
- `--no-restart` when container is not running: falls back to default behavior (start the container).

### [PLANNED] `yoloai x` (Extensions)

`yoloai x <extension> <name> [args...] [--flags...]`

Extensions are user-defined custom commands. Each extension is a separate YAML file in `~/.yoloai/extensions/` that defines its own arguments, flags, and a shell script action. `x` is short for "extension" to the command vocabulary. This is a power-user feature.

Extensions declare which agents they support via the `agent` field — a single string (`agent: claude`), a list (`agent: [claude, codex]`), or omitted entirely for any-agent compatibility. yoloAI validates the current agent against this list before running the action. For extensions supporting multiple agents, the `$agent` variable lets the script branch on the active agent. For extensions that are fundamentally different per agent, create separate files (e.g., `lint-claude.yaml`, `lint-codex.yaml`).

**Extension format:**

Each extension is a standalone YAML file — self-contained, shareable, no external dependencies.

```yaml
# ~/.yoloai/extensions/lint.yaml
description: "Lint and fix code issues"
agent: claude                         # optional: string or list (e.g., [claude, codex]); omit for any agent

args:
  - name: directory
    description: "Directory to lint"

flags:
  - name: severity
    short: s
    description: "Minimum severity to fix"
    default: "warning"
  - name: max-turns
    description: "Maximum agent turns"
    default: "5"

action: |
  yoloai new "${name}" "${directory}" \
    --model sonnet \
    --prompt "Find and fix all ${severity}-level and above linting issues. Run the linter, fix what it finds, repeat until clean." \
    -- --max-turns "${max_turns}"
```

**Usage:**

```
# Run the lint extension — creates sandbox "my-lint", lints ./src
yoloai x lint my-lint ./src

# Override a flag
yoloai x lint my-lint ./src --severity error

# Short flag
yoloai x lint my-lint ./src -s error --max-turns 10
```

**How it works:**

1. yoloAI finds `~/.yoloai/extensions/lint.yaml` and parses its `args` and `flags` definitions.
2. yoloAI parses the user's command line against those definitions — `name` is always the first positional (built-in), then extension-defined `args` follow.
3. If `agent` is specified in the YAML and doesn't match the current `--agent` / `defaults.agent`, error with a message suggesting the right extension.
4. All captured values are set as environment variables and the `action` script is executed via `sh -c`.

**Variables available to the action script:**

| Variable      | Source                                       |
|---------------|----------------------------------------------|
| `$name`       | Sandbox name (first positional, always required) |
| `$agent`      | Resolved agent name (`--agent` / `defaults.agent`) |
| `$<arg_name>` | Each defined arg, by its `name` field        |
| `$<flag_name>`| Each defined flag, by its `name` field (default applied if not provided) |

Flags with hyphens in their name are available with underscores: `--max-turns` → `$max_turns`.

**More examples:**

```yaml
# ~/.yoloai/extensions/review.yaml
description: "Code review with configurable focus"
agent: claude

args:
  - name: directory
    description: "Directory to review"

flags:
  - name: focus
    short: f
    description: "Review focus area"
    default: "bugs and security"
  - name: model
    short: m
    description: "Model to use"
    default: "opus"

action: |
  yoloai new "${name}" "${directory}" \
    --model "${model}" \
    --prompt "Review this codebase. Focus on: ${focus}. Provide a detailed report with file locations and suggested fixes."
```

```yaml
# ~/.yoloai/extensions/iterate.yaml
description: "Destroy and recreate a sandbox with the same settings"

args:
  - name: existing
    description: "Existing sandbox to recreate"

action: |
  workdir="$(jq -r .workdir.host_path ~/.yoloai/sandboxes/"${existing}"/meta.json)"
  yoloai destroy --yes "${existing}"
  yoloai new "${name}" "${workdir}"
```

**The action script is not limited to `yoloai new`** — it can call any yoloAI command (`exec`, `destroy`, `diff`), chain multiple commands, use conditionals, or call external tools. yoloAI is just the argument parser and executor; the script defines the behavior.

**Listing extensions:**

`yoloai x --list` scans `~/.yoloai/extensions/` and shows each extension's name, description, agent, and defined args/flags.

**Validation:**

- Extension name derived from filename (e.g., `lint.yaml` → `lint`). Must not collide with built-in yoloAI commands.
- `args` are positional and order matters — parsed in definition order after `name`.
- `flags` support `short` (single char), `default` (string), and `description`. Flag names and shorts must not collide with yoloAI's global flags (`--verbose`/`-v`, `--quiet`/`-q`, `--yes`/`-y`, `--no-color`, `--help`/`-h`) — error at parse time if they do.
- Missing required args (no `default`) produce a usage error with the extension's arg definitions.
- The `action` field is required.

### Image Cleanup

Docker images (`yoloai-base`, `yoloai-<profile>`) accumulate indefinitely. A cleanup mechanism is needed but deferred pending research into Docker's image lifecycle: base images are shared parents of profile images, profile images may have running containers, layer caching means "removing" doesn't necessarily free space, and `docker image prune` vs `docker rmi` have different semantics. Half-baked pruning could break running sandboxes or nuke images the user spent time building.


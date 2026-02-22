# yoloai: Sandboxed AI Coding Agent Runner

## Goal

Run AI coding CLI agents (Claude Code, Codex, and others) with their sandbox-bypass flags inside disposable, isolated containers so that the agent can work autonomously without constant permission prompts. Project directories are presented as isolated writable views inside the container. The user reviews changes via `yoloai diff` and applies them back to the originals via `yoloai apply` when satisfied.

**Scope:** v1 ships with Claude Code and OpenAI Codex. The architecture is agent-agnostic — Docker, overlayfs, network isolation, diff/apply are not agent-specific. Adding a new agent requires only a new agent definition (install command, launch command, API key env vars, state directory). See RESEARCH.md "Multi-Agent Support Research" for additional agents researched.

## Architecture

```
┌────────────────────────────────────────────────────────────┐
│  Host (any machine with Docker)                            │
│                                                            │
│  yoloai CLI (Go binary)                                    │
│    │                                                       │
│    ├─ docker run ──► sandbox-1  ← ephemeral                │
│    │                  ├ tmux                               │
│    │                  ├ AI agent (Claude, Codex, ...)      │
│    │                  ├ project dirs (mirrored host paths) │
│    │                  └ agent state (per-sandbox)          │
│    │                                                       │
│    ├─ docker run ──► sandbox-2                             │
│    └─ ...                                                  │
│                                                            │
│  ~/.yoloai/sandboxes/<name>/  ← persistent state           │
│    ├── work/          (overlay upper dirs or full copies)  │
│    ├── agent-state/  (agent's state directory)             │
│    ├── log.txt        (session output)                     │
│    ├── prompt.txt     (initial prompt)                     │
│    └── meta.json      (config, paths, status)              │
└────────────────────────────────────────────────────────────┘
```

### Container Technology: Docker

- Works natively on Linux/macOS, inside LXC with Proxmox nesting enabled, etc.
- Provides process, filesystem, and network namespace isolation
- Ephemeral by default — `docker rm` and it's gone
- All persistent state lives on the host in `~/.yoloai/sandboxes/`

### Key Principle: Containers are ephemeral, state is not

The Docker container is disposable — it can crash, be destroyed, be recreated. Everything that matters lives in the sandbox's state directory on the host:
- **`work/`** — copies of project directories (what the agent modifies)
- **`agent-state/`** — the agent's state directory (session history, settings)
- **`prompt.txt`** — the initial prompt to feed the agent
- **`log.txt`** — captured tmux output for post-mortem review
- **`meta.json`** — original paths, mode, profile, timestamps, status, `yoloai_version` (for format migration on upgrades)

## Components

### 1. Docker Images

**Base image (`yoloai-base`):** Minimal foundation with common tools, rebuilt occasionally.
- Common tools: tmux, git, build-essential, python3, etc.
- **Claude Code:** Node.js + npm installation (`npm i -g @anthropic-ai/claude-code`) — npm required, not native binary (native binary bundles Bun which ignores proxy env vars, breaking `--network-isolated`)
- **Codex:** Static Rust binary download (musl-linked, zero runtime dependencies, ~zero image bloat)
- **Non-root user** (`yoloai`, UID/GID matching host user via entrypoint). Image builds with a placeholder user (UID 1001). At container start, the entrypoint runs as root: `usermod -u $HOST_UID yoloai && groupmod -g $HOST_GID yoloai` (handling exit code 12 for chown failures on mounted volumes), fixes ownership on container-managed directories, then drops privileges via `gosu yoloai`. Uses `tini` as PID 1 (`--init` or explicit `ENTRYPOINT`). Images are portable across machines since UID/GID are set at run time, not build time. Claude Code refuses `--dangerously-skip-permissions` as root; Codex does not enforce this but convention is non-root

**Profile images (`yoloai-<profile>`):** Derived from base, one per profile. Users supply a `Dockerfile` per profile with `FROM yoloai-base`. This avoids the limitations of auto-generating Dockerfiles from package lists and gives full flexibility (PPAs, tarballs, custom install steps).

```
~/.yoloai/profiles/
├── go-dev/
│   ├── Dockerfile       ← FROM yoloai-base; RUN apt-get install ...
│   └── profile.yaml     ← runtime config (mounts, env, resources, workdir, directories)
└── node-dev/
    ├── Dockerfile
    └── profile.yaml
```

`yoloai build` with no arguments rebuilds the base image. `yoloai build <profile>` rebuilds that profile's image. `yoloai build --all` rebuilds everything (base first, then all profiles).

### 2. Config File (`~/.yoloai/config.yaml`)

```yaml
# Always applied to every sandbox
defaults:
  agent: claude                          # which agent to launch (claude, codex); CLI --agent overrides
  # Files/dirs copied into agent-state/ on first sandbox run.
  # These seed the agent's environment. Sandbox gets its own copy (not bind-mounted).
  # Options:
  #   agent_files: home               # copy from agent's default state dir (~/.claude/ for Claude, ~/.codex/ for Codex)
  #   agent_files:                    # explicit file list (deterministic, for team setups)
  #     - .claude/CLAUDE.md           #   paths relative to $HOME
  #     - /path/to/shared/settings    #   absolute paths for deterministic setups
  #   (omit agent_files entirely)     # nothing copied — safe default
  # Profile agent_files replaces defaults (no merge).
  # agent_files: home

  mounts:
    - ~/.gitconfig:/home/yoloai/.gitconfig:ro

  copy_strategy: auto                   # auto | overlay | full
                                        # auto: use overlayfs where available, fall back to full copy
                                        # overlay: overlayfs lower layer (instant, deltas-only, needs CAP_SYS_ADMIN)
                                        # full: traditional full copy (portable, all reads VM-local)
  auto_commit_interval: 0               # seconds between auto-commits in :copy dirs; 0 = disabled
  ports: []                             # default port mappings; profile ports are additive
  env: {}                               # environment variables passed to container; profile env merges with defaults
  network_isolated: false               # true to enable network isolation by default
  network_allow: []                     # additional domains to allow (additive with agent defaults)
  resources:
    cpus: 4                           # docker --cpus
    memory: 8g                        # docker --memory
    # disk: 10g                        # docker --storage-opt size= (Linux with XFS + pquota only; not supported on macOS Docker Desktop)
                                        # Rationale: cpus/memory sized for Claude Code + build tooling running concurrently.
                                        # Claude CLI itself uses ~2GB RSS; the rest covers builds, tests, language servers.
```

`config.yaml` contains only `defaults` — settings applied to every sandbox. Profile-specific configuration lives in separate `profile.yaml` files (see Profiles section).

- `defaults.agent` selects the agent to launch. Valid values: `claude`, `codex`. Determines the launch command, API key env vars, state directory, network allowlist, and prompt delivery mode. CLI `--agent` overrides config.
- `defaults.agent_files` controls what files are copied into the sandbox's `agent-state/` directory on first run. Set to `home` to copy from the agent's default state directory (`~/.claude/` for Claude, `~/.codex/` for Codex). Set to a list of paths (relative to `$HOME` or absolute) for deterministic setups. Omit entirely to copy nothing (safe default). Profile `agent_files` **replaces** (not merges with) defaults.
- `defaults.mounts` are bind mounts added at container run time. Profile mounts are **additive** (merged with defaults, no deduplication — duplicates are a user error).
- `defaults.resources` sets baseline limits. Profiles can override individual values.
- `defaults.env` sets environment variables passed to the container via `docker run -e`. Profile `env` is merged with defaults (profile values win on conflict). Note: API keys (e.g., `ANTHROPIC_API_KEY`, `CODEX_API_KEY`) are injected via file-based bind mount, not `env` — see Credential Management.
- `defaults.network_isolated` enables network isolation for all sandboxes. Profile can override. CLI `--network-isolated` flag overrides config.
- `defaults.network_allow` lists additional allowed domains. Non-empty `network_allow` implies `network_isolated: true`. Profile `network_allow` is additive with defaults. CLI `--network-allow` is additive with config.

#### Recipes (advanced)

For advanced setups like Tailscale inside containers:

```yaml
# Example: add to defaults or a specific profile
defaults:
  cap_add:
    - NET_ADMIN
  devices:
    - /dev/net/tun
  setup:                              # runs at container start
    - tailscale up --authkey=${TAILSCALE_AUTHKEY}
```

`cap_add`, `devices`, and `setup` are available in both `defaults` and profiles but not shown in the default config. Profile values are **additive** (merged with defaults). `setup` commands run at container start before the agent launches, in order (defaults first, then profile).

Config values support `${VAR}` environment variable interpolation from the host environment (following Docker Compose conventions). This allows secrets like `${TAILSCALE_AUTHKEY}` to be referenced in config without hardcoding. Unset variables produce an error at sandbox creation time (fail-fast, not silent empty string).

### 3. Profiles

Profiles live in `~/.yoloai/profiles/<name>/`, each containing a `Dockerfile` and a `profile.yaml`:

```
~/.yoloai/profiles/<name>/
├── Dockerfile        ← FROM yoloai-base
└── profile.yaml      ← runtime config
```

**`profile.yaml` format:**

```yaml
# ~/.yoloai/profiles/my-project/profile.yaml
workdir:
  path: /home/user/my-app
  mode: copy                            # copy or rw (required for workdir)
directories:
  - path: /home/user/shared-lib
    mode: rw
    mount: /usr/local/lib/shared        # custom mount point (default: mirrors host path)
  - path: /home/user/common-types
    # default: read-only
agent_files:
  - .claude/CLAUDE.md
  - /shared/configs/claude-settings.json  # absolute paths for team setups
mounts:
  - ~/.ssh:/home/yoloai/.ssh:ro
resources:
  memory: 16g
ports:
  - "8080:8080"
env:
  GOMODCACHE: /home/yoloai/go/pkg/mod   # Go module cache (not source code — no conflict with mirrored paths)
```

CLI workdir **replaces** profile workdir. CLI `-d` dirs are **additive** with profile dirs. CLI arguments for one-offs, config for repeatability — same options available in both.

**Profile merge rules** (profile values merge with `defaults` from `config.yaml`):

| Field           | Merge behavior                                    |
|-----------------|---------------------------------------------------|
| `agent_files`   | Profile replaces defaults (no merge)              |
| `mounts`        | Additive (no deduplication — duplicates are user error) |
| `resources`     | Profile overrides individual values               |
| `ports`         | Additive                                          |
| `env`           | Merged (profile wins on conflict)                 |
| `cap_add`       | Additive                                          |
| `devices`       | Additive                                          |
| `setup`         | Additive (defaults first, then profile)           |
| `network_allow` | Additive                                          |
| `workdir`       | Profile provides default, CLI replaces            |
| `directories`   | Profile provides defaults, CLI `-d` is additive   |

**`yoloai profile` commands:**

- `yoloai profile create <name> [--template <tpl>]` — Create a profile directory with a scaffold Dockerfile and minimal `profile.yaml`. Templates: `base` (default), `go`, `node`, `python`, `rust`. Creates pre-filled files tailored to the language/stack.
- `yoloai profile list` — List all profiles in `~/.yoloai/profiles/`.
- `yoloai profile delete <name>` — Delete a profile directory. Asks for confirmation if any sandbox references the profile.

### 4. CLI (`yoloai`)

Single Go binary. No runtime dependencies — just the binary and Docker.

## Commands

```
yoloai new [options] <name> [<workdir>] [-d <auxdir>...]  Create and start a sandbox
yoloai list                                    List sandboxes and their status
yoloai attach <name>                           Attach to a sandbox's tmux session
yoloai status <name>                           Show detailed sandbox info
yoloai log <name>                              Show sandbox session log
yoloai tail <name>                             Tail sandbox session log in real time
yoloai diff <name>                             Show changes the agent made
yoloai apply <name>                            Copy changes back to original dirs
yoloai exec <name> <command>                   Run a command inside the sandbox
yoloai stop <name>                             Stop a sandbox (preserving state)
yoloai start [--resume] <name>                 Start a stopped sandbox
yoloai restart <name>                          Restart the agent in an existing sandbox
yoloai destroy <name>                          Stop and remove a sandbox
yoloai build [profile|--all]                   Build/rebuild Docker image(s)
yoloai profile create <name> [--template <tpl>]  Create a profile with scaffold
yoloai profile list                            List profiles
yoloai profile delete <name>                   Delete a profile
yoloai init                                    First-time setup (dirs, config, base image)
```

### Agent Definitions

Built-in agent definitions (v1). Each agent specifies its install method, launch commands, API key requirements, and behavioral characteristics. No user-defined agents in v1.

| Field                         | Claude                                                    | Codex                                          |
|-------------------------------|-----------------------------------------------------------|------------------------------------------------|
| Install                       | `npm i -g @anthropic-ai/claude-code`                      | Static binary download                         |
| Runtime                       | Node.js                                                   | None (static musl binary)                      |
| Interactive cmd               | `claude --dangerously-skip-permissions`                   | `codex --yolo`                                 |
| Headless cmd                  | `claude -p "PROMPT" --dangerously-skip-permissions`       | `codex exec --yolo "PROMPT"`                   |
| Default prompt mode           | interactive                                               | headless (with prompt) / interactive (without) |
| Submit sequence (interactive) | `Enter Enter` (double) + 3s startup delay                 | `Enter` + 3s startup delay                     |
| API key env vars              | `ANTHROPIC_API_KEY`                                       | `CODEX_API_KEY` (preferred), `OPENAI_API_KEY` (fallback) |
| State directory               | `~/.claude/`                                              | `~/.codex/`                                    |
| Model flag                    | `--model <model>`                                         | `--model <model>`                              |
| Non-root required             | Yes (refuses as root)                                     | No (convention is non-root)                    |
| Proxy support                 | Yes (npm install only, not native binary)                 | TBD (research needed)                          |
| Default network allowlist     | `api.anthropic.com`, `statsig.anthropic.com`, `sentry.io` | `api.openai.com` (minimum; additional TBD)     |
| Extra env vars / quirks       | —                                                         | `--skip-git-repo-check` useful outside repos; Landlock fails in containers (use `--yolo`) |

**Prompt delivery modes:**

- **Interactive mode:** Agent starts in interactive mode inside tmux. If `--prompt` is provided, prompt is fed via `tmux send-keys` after a startup delay. Agent stays running for follow-up via `yoloai attach`. This is Claude's natural mode.
- **Headless mode:** Agent launched with prompt as CLI arg inside tmux (tmux still used for logging + attach). Runs to completion. This is Codex's natural mode via `codex exec --yolo "PROMPT"`. Without `--prompt`, Codex falls back to interactive mode (`codex --yolo` in tmux).

The agent definition determines the default prompt delivery mode. `--prompt` selects headless mode for agents that support it (Codex); Claude always uses interactive mode regardless.

### `yoloai new`

`yoloai new [options] <name> [<workdir>] [-d <auxdir>...]`

The **workdir** is the single primary project directory — the agent's working directory. It is positional (after name) and must have `:rw` or `:copy` — error otherwise. The workdir is optional if a profile provides one.

**`-d` / `--dir`** specifies auxiliary directories (repeatable). Default read-only. Additive with profile directories.

When both CLI and profile provide directories: CLI workdir **replaces** profile workdir. CLI `-d` dirs are **additive** with profile dirs.

Directory argument syntax: `<path>[:<suffixes>][=<mount-point>]`

Suffixes (combinable in any order):
- `:rw` — bind-mounted read-write (live, immediate changes)
- `:copy` — copied to sandbox state, read-write, diff/apply available
- `:force` — override dangerous directory detection

Mount point:
- `=<path>` — mount at a custom container path instead of the mirrored host path

All directories are **bind-mounted read-only by default** at their original absolute host paths (mirrored paths).

```
# Copy workdir (safe, reviewable), aux dirs read-only
yoloai new fix-build --prompt "fix the build" ./my-app:copy -d ./shared-lib -d ./common-types

# Live-edit workdir, one aux dir also writable
yoloai new my-task ./my-app:rw -d ./shared-lib:rw -d ./common-types

# Custom mount points for tools that need specific paths
yoloai new my-task ./my-app:copy=/opt/myapp -d ./shared-lib=/usr/local/lib/shared -d ./common-types

# Use profile workdir, add extra aux dir from CLI
yoloai new my-task --profile my-project -d ./extra-lib

# Error: workdir has no write access
yoloai new my-task ./my-app -d ./shared-lib
```

Name is required. If omitted, an error is shown with a helpful message suggesting a name based on the workdir basename.

Example layout inside the container (assuming cwd is `/home/user/projects`):

```
yoloai new my-task ./my-app:copy -d ./shared-lib:rw -d ./common-types
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
yoloai new my-task ./my-app:copy=/opt/myapp -d ./shared-lib=/usr/local/lib/shared -d ./common-types
```

```
/opt/myapp/                          ← workdir (copied, agent's cwd)
/usr/local/lib/shared/               ← aux dir (bind-mounted, read-only)
/home/user/projects/common-types/    ← aux dir (mirrored host path, read-only)
```

Options:
- `--profile <name>`: Use a profile's derived image and mounts. No profile = base image + defaults only.
- `--prompt <text>`: Initial prompt/task for the agent (see Prompt Mechanism below).
- `--model <model>`: Model to use. Passed to the agent's `--model` flag. If omitted, uses the agent's default.
- `--agent <name>`: Agent to use (`claude`, `codex`). Overrides `defaults.agent` from config.
- `--network-isolated`: Allow only the agent's required API traffic. The agent can function but cannot access other external services, download arbitrary binaries, or exfiltrate code.
- `--network-allow <domain>`: Allow traffic to specific additional domains (can be repeated). Implies `--network-isolated`. Added to the agent's default allowlist (see below).
- `--network-none`: Run with `--network none` for full network isolation (agent API calls will also fail). Mutually exclusive with `--network-isolated` and `--network-allow`. **Warning:** Most agents (Claude, Codex) require network access to reach their API endpoints. This flag is only useful for agents with locally-hosted models or for testing container setup without agent execution.
- `--port <host:container>`: Expose a container port on the host (can be repeated). Example: `--port 3000:3000` for web dev. Without this, container services are not reachable from the host browser.

**Default network allowlist (per agent):**

**Claude Code:**

| Domain                  | Purpose                               |
|-------------------------|---------------------------------------|
| `api.anthropic.com`     | API calls (required)                  |
| `statsig.anthropic.com` | Telemetry/feature flags (recommended) |
| `sentry.io`             | Error reporting (recommended)         |

**Codex:**

| Domain           | Purpose              |
|------------------|----------------------|
| `api.openai.com` | API calls (required) |

> **Note:** Codex may require additional domains (telemetry, model downloads). This needs verification. See RESEARCH.md for known gaps.

The allowlist is agent-specific — each agent's definition includes its required domains. `--network-allow` domains are additive with the selected agent's defaults.

**Workflow:**

1. Error if workdir has no `:rw` or `:copy` suffix.
2. Error if any two directories resolve to the same absolute container path (mirrored host path or custom `=<path>`).
3. For each `:copy` directory, set up an isolated writable view using one of two strategies (selected by `copy_strategy` config, default `auto`):

   **Overlay strategy** (default when available):
   - Host side: bind-mount the original directory read-only into the container at its original absolute path. Provide an empty upper directory from `~/.yoloai/sandboxes/<name>/work/<dirname>/`.
   - Container side (entrypoint): mount overlayfs merging lower (original, read-only) + upper at the mirrored host path. The agent sees the full project; writes go to upper only. Original directory is inherently protected (read-only lower layer). The entrypoint must be idempotent — use `mkdir -p` for directories and check `mountpoint -q` before mounting, so it handles both fresh starts and restarts cleanly.
   - After overlay mount: `git init` + `git add -A` + `git commit -m "initial"` on the merged view to create a baseline. If the directory is already a git repo, record the current HEAD SHA in `meta.json` and use it as the baseline instead.
   - Requires `CAP_SYS_ADMIN` capability on the container (not full `--privileged`).

   **Full copy strategy** (fallback):
   - If the directory is a git repo, record the current HEAD SHA in `meta.json`.
   - Copy to `~/.yoloai/sandboxes/<name>/work/<dirname>/`, then mount at the mirrored host path inside the container. Everything is copied regardless of `.gitignore`.
   - If the copy already has a `.git/` directory (from the original repo), use the recorded SHA as the baseline — `yoloai diff` will diff against it.
   - If the copy has no `.git/`, `git init` + `git add -A` + `git commit -m "initial"` to create a baseline.

   Both strategies produce the same result from the user's perspective: a protected, writable view with git-based diff/apply. The overlay strategy is instant and space-efficient; the full copy strategy is more portable. `auto` tries overlay first, falls back to full copy if `CAP_SYS_ADMIN` is unavailable or the kernel doesn't support nested overlayfs.

   **`auto` detection strategy** (following the pattern used by containers/storage, Podman, and Docker itself):
   1. Check `/proc/filesystems` for `overlay` — if absent, skip to full copy.
   2. Check `CAP_SYS_ADMIN` via `/proc/self/status` `CapEff` bitmask (bit 21) — if absent, skip to full copy.
   3. Attempt a test mount in a temp directory (`mount -t overlay` with `lowerdir`, `upperdir`, `workdir`). This is the authoritative test — it validates kernel support, security profiles (seccomp, AppArmor), and backing filesystem compatibility simultaneously. If it fails, fall back to full copy.
   4. Cache the result in `~/.yoloai/cache/overlay-support`, invalidated on kernel or Docker version change.

   Note: `git add -A` naturally honors `.gitignore` if one is present, so gitignored files (e.g., `node_modules`) won't clutter `yoloai diff` output regardless of strategy.
4. If `auto_commit_interval` > 0, start a background auto-commit loop for `:copy` directories inside the container for recovery. The interval is passed to the container via the `YOLOAI_AUTO_COMMIT_INTERVAL` environment variable. Disabled by default.
5. Store original paths, modes, and mapping in `meta.json`.
6. Start Docker container (see Container Startup below).

### Safety Checks

Before creating the sandbox:
- **Dangerous directory detection:** Error if any mount target is `$HOME`, `/`, or a system directory. Override with `:force` suffix (e.g., `$HOME:force`, `$HOME:rw:force`).
- **Dirty git repo detection:** If any `:rw` or `:copy` directory is a git repo with uncommitted changes, warn with specifics and prompt for confirmation:
  ```
  WARNING: ./my-app has uncommitted changes (3 files modified, 1 untracked)
  These changes will be visible to the agent and could be modified or lost.
  Continue? [y/N]
  ```

### Container Startup

1. Generate a **sandbox context file** at `/yoloai/context.md` describing the environment for the agent: which directory is the workdir, which are auxiliary, mount paths and access mode of each (read-only / read-write / copy), available tools, and how auto-save works. This file lives outside the work tree in the `/yoloai/` internal directory, so it never pollutes project files, git baselines, or diffs. The agent is pointed to it via agent-specific mechanisms (`--append-system-prompt` for Claude, inclusion in the initial prompt for Codex).
2. Start Docker container (as non-root user `yoloai`) with:
   - When `--network-isolated`: `HTTPS_PROXY` and `HTTP_PROXY` env vars pointing to the proxy sidecar (required — Claude Code's npm installation honors these via undici; Codex proxy support is TBD — see RESEARCH.md)
   - `:copy` directories: overlay strategy mounts originals as overlayfs lower layers with upper dirs from sandbox state; full copy strategy mounts copies from sandbox state. Both at their mount point (mirrored host path or custom `=<path>`, read-write)
   - `:rw` directories bind-mounted at their mount point (mirrored host path or custom `=<path>`, read-write)
   - Default (no suffix) directories bind-mounted at their mount point (mirrored host path or custom `=<path>`, read-only)
   - `agent-state/` mounted at the agent's state directory path (read-write, per-sandbox)
   - Files listed in `agent_files` (from config) copied into `agent-state/` on first run
   - `log.txt` from sandbox state bind-mounted at `/yoloai/log.txt` (read-write, for tmux `pipe-pane`)
   - `prompt.txt` from sandbox state bind-mounted at `/yoloai/prompt.txt` (read-only, if provided)
   - Config mounts from defaults + profile
   - Resource limits from defaults + profile
   - API key(s) injected via file-based bind mount at `/run/secrets/` — env var names from agent definition (see Credential Management)
   - `CAP_SYS_ADMIN` capability (required for overlayfs mounts inside the container; omitted when `copy_strategy: full`). `CAP_NET_ADMIN` added when `--network-isolated` is used (required for iptables rules; independent capability, not included in `CAP_SYS_ADMIN`)
   - Container name: `yoloai-<name>`
   - User: `yoloai` (UID/GID matching host user)
   - `/yoloai/` internal directory for sandbox context file, overlay working directories, and bind-mounted state files (`log.txt`, `prompt.txt`)
3. Run `setup` commands from config (if any).
4. Start tmux session named `main` with logging to `/yoloai/log.txt` (`tmux pipe-pane`).
5. Inside tmux: launch the agent using the command from its agent definition (e.g., `claude --dangerously-skip-permissions [--model X]` or `codex --yolo`)
6. Wait ~3s for the agent to initialize (interactive mode only).
7. Prompt delivery depends on the agent's prompt mode:
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

### `yoloai attach`

Runs `docker exec -it yoloai-<name> tmux attach -t main`.

Detach with standard tmux `Ctrl-b d` — container keeps running.

### `yoloai status <name>`

Shows sandbox details from `meta.json`: profile, directories with their access modes (read-only / rw / copy), creation time, and whether the agent is still running (checks if the agent process is alive inside the container).

### `yoloai diff`

For `:copy` directories: runs `git diff` + `git status` against the baseline (the recorded HEAD SHA for existing repos, or the synthetic initial commit for non-git dirs). Shows exactly what the agent changed with proper diff formatting.

For `:rw` directories: if the original is a git repo, runs `git diff` against it. Otherwise, notes that diff is not available for live-mounted dirs without git.

Read-only directories are skipped (no changes possible).

### `yoloai apply`

For `:copy` directories only. Generates a patch from `git diff` in the sandbox (whether overlay-merged or full-copy) and applies it to the original directory. This handles additions, modifications, and deletions. Works identically regardless of `copy_strategy` — git provides the diff in both cases. For dirs that had no original git repo, excludes the synthetic `.git/` directory created by yoloai.

Before applying, shows a summary via `git diff --stat` (files changed, insertions, deletions) and verifies the patch applies cleanly via `git apply --check`. Prompts for confirmation before proceeding.

`:rw` directories need no apply — changes are already live. Read-only directories have no changes.

### `yoloai destroy`

`docker stop` + `docker rm` the container (and proxy sidecar if `--network-isolated`). Removes `~/.yoloai/sandboxes/<name>/` entirely. No special overlay cleanup needed — the kernel tears down the mount namespace when the container stops.

Asks for confirmation if the agent is still running.

### `yoloai log`

`yoloai log <name>` displays the session log (`log.txt`) for the named sandbox.

### `yoloai tail`

`yoloai tail <name>` tails the session log in real time (like `tail -f`).

### `yoloai exec`

`yoloai exec <name> <command>` runs a command inside the sandbox container without attaching to tmux. Useful for debugging (`yoloai exec my-sandbox bash`) or quick operations (`yoloai exec my-sandbox npm install foo`).

Implemented as `docker exec yoloai-<name> <command>`, with `-i` added when stdin is a pipe/TTY and `-t` added when stdin is a TTY. This allows both interactive use (`yoloai exec my-sandbox bash`) and non-interactive use (`yoloai exec my-sandbox ls`, `echo "test" | yoloai exec my-sandbox cat`).

### `yoloai list`

Lists all sandboxes with their current status.

| Column  | Description                                                    |
|---------|----------------------------------------------------------------|
| NAME    | Sandbox name                                                   |
| STATUS  | `running`, `stopped`, `exited` (agent exited but container up) |
| AGENT   | Agent name (`claude`, `codex`)                                 |
| PROFILE | Profile name or `(base)`                                       |
| AGE     | Time since creation                                            |
| WORKDIR | Working directory path                                         |

Options:
- `--running`: Show only running sandboxes.
- `--stopped`: Show only stopped sandboxes.
- `--json`: Output as JSON for scripting.

### `yoloai build`

`yoloai build` with no arguments rebuilds the base image (`yoloai-base`).

`yoloai build <profile>` rebuilds a specific profile's image (which derives from `yoloai-base`).

`yoloai build --all` rebuilds everything: base image first, then the proxy image (`yoloai-proxy`), then all profile images.

`yoloai build` and `yoloai build --all` also build the proxy sidecar image (`yoloai-proxy`), a lightweight forward proxy (Go binary or tinyproxy) used by `--network-isolated`.

Useful after modifying a profile's Dockerfile or when the base image needs updating (e.g., new Claude CLI version).

Profile Dockerfiles that install private dependencies (e.g., `RUN go mod download` from a private repo, `RUN npm install` from a private registry) need build-time credentials. yoloai passes host credentials to Docker BuildKit via `--secret` so they're available during the build but never stored in image layers. Example: `RUN --mount=type=secret,id=npmrc,target=/root/.npmrc npm install` in the Dockerfile, with yoloai automatically providing `~/.npmrc` as the secret source. Supported secret sources are documented in `yoloai build --help`.

### `yoloai stop`

`yoloai stop <name>` stops a sandbox container (and proxy sidecar if `--network-isolated`), preserving all state (work directory, agent-state, logs). The container can be restarted later without losing progress.

Internally, `docker stop` sends SIGTERM and the agent terminates. The agent's state directory persists on the host. `yoloai start` relaunches a fresh agent process with state intact — Claude preserves session history (resumes context); Codex starts fresh (no built-in session persistence). Think of stop/start as pausing the sandbox environment, not the agent's thought process. Use `--resume` with `start` to re-feed the original task prompt.

### `yoloai start`

`yoloai start [--resume] <name>` ensures the sandbox is running — idempotent "get it running, however needed":
- If the container is stopped: starts it (and proxy sidecar if `--network-isolated`). The entrypoint re-establishes overlayfs mounts (mounts don't survive `docker stop` — this is by design; the upper directory persists on the host and the entrypoint re-mounts idempotently).
- If the container is running but the agent has exited: relaunches the agent in the existing tmux session.
- If already running: no-op.

This eliminates the need to diagnose *why* a sandbox isn't running before choosing a command.

**`--resume` flag:** When used, the agent is relaunched with the original prompt from `prompt.txt` prefixed with a preamble: "You were previously working on the following task and were interrupted. The work directory contains your progress so far. Continue where you left off:" followed by the original prompt text. Without `--resume`, `yoloai start` relaunches the agent in interactive mode with no prompt (user attaches and gives instructions manually).

### `yoloai restart`

`yoloai restart <name>` is equivalent to `yoloai stop <name>` followed by `yoloai start <name>`. Use cases: recovering from a corrupted container environment, applying config changes that require a fresh container (e.g., new mounts or resource limits), or restarting a wedged agent process.

### Image Cleanup

Docker images (`yoloai-base`, `yoloai-<profile>`) accumulate indefinitely. A cleanup mechanism is needed but deferred pending research into Docker's image lifecycle: base images are shared parents of profile images, profile images may have running containers, layer caching means "removing" doesn't necessarily free space, and `docker image prune` vs `docker rmi` have different semantics. Half-baked pruning could break running sandboxes or nuke images the user spent time building.

## Directory Layout

```
~/.yoloai/
├── config.yaml                  ← global defaults (no profiles — those live in profiles/)
├── profiles/
│   ├── go-dev/
│   │   ├── Dockerfile           ← FROM yoloai-base
│   │   └── profile.yaml        ← runtime config (mounts, env, resources, workdir, directories)
│   └── node-dev/
│       ├── Dockerfile
│       └── profile.yaml
└── sandboxes/
    └── <name>/
        ├── meta.json            ← original paths, mode, profile, timestamps
        ├── prompt.txt           ← initial prompt (if provided)
        ├── log.txt              ← tmux session log
        ├── agent-state/         ← agent's state directory (per-sandbox, read-write)
        └── work/                ← overlay upper dirs (deltas) or full copies, for :copy dirs only
            ├── <workdir>/       ← if workdir is :copy
            └── <auxdir>/        ← if aux dir is :copy
```

## Credential Management

API keys are injected via **file-based credential injection** following OWASP and CIS Docker Benchmark guidance (never pass secrets as environment variables to `docker run`):

1. yoloai writes the API key(s) to temporary file(s) on the host. The agent definition specifies which env vars to inject (e.g., `ANTHROPIC_API_KEY` for Claude, `CODEX_API_KEY` for Codex).
2. Each key file is bind-mounted read-only into the container at `/run/secrets/<key_name>`.
3. The container entrypoint reads the file(s), exports the appropriate env var(s) (since CLI agents expect credentials as env vars), then launches the agent.
4. The host-side temp file is cleaned up immediately after container start.

**What this protects against:** `docker inspect` does not show the key. `docker commit` does not capture it. `docker logs` does not leak it. No temp file lingers on host disk. Image layers never contain the key.

**Accepted tradeoff:** The agent process has the API key in its environment (unavoidable — CLI agents read credentials from env vars). `/proc/<pid>/environ` exposes it to same-user processes inside the container. This is acceptable because the agent already has full use of the key.

The user sets the appropriate API key in their host shell profile (`ANTHROPIC_API_KEY` for Claude, `CODEX_API_KEY` or `OPENAI_API_KEY` for Codex). yoloai reads the required key(s) from the host environment at sandbox creation time based on the agent definition.

**Future directions:** Credential proxy (the MITM approach used by Docker Sandboxes) could provide stronger isolation by keeping the API key entirely outside the container. If CLI agents add `ANTHROPIC_API_KEY_FILE` support, the env var export step can be eliminated. macOS Keychain integration (cco's approach) could serve as an alternative credential source. These are deferred to future versions.

**Industry expectations:** yoloai's file-based injection follows OWASP and CIS guidance and matches what Docker official images (MySQL, Postgres) and most competitors (deva.sh, cco) use. Key gaps vs. enterprise expectations:
- **Credential rotation/expiry:** Not supported. Most users use long-lived API keys. Not a v1 blocker, but enterprise users expect time-scoped credentials.
- **Vault integration:** HashiCorp Vault, AWS Secrets Manager, GCP Secret Manager are expected in enterprise tooling. Deferred.
- **OAuth/SSO:** Claude supports OAuth for Pro/Max/Team plans. v1 supports API key auth only. Docker Sandboxes supports OAuth but reports it as broken (docker/for-mac#7842).
- **Assessment:** File-based injection is the industry standard for developer tools at this level. Credential proxy and vault integration are enterprise features for future versions.

## Prerequisites

- Docker installed and running (clear error message if Docker daemon is not available)
- Distribution: binary download from GitHub releases, `go install`, or Homebrew. No runtime dependencies — Go compiles to a static binary.
- API key for your chosen agent set in environment (`ANTHROPIC_API_KEY` for Claude, `CODEX_API_KEY` or `OPENAI_API_KEY` for Codex)
- If running from inside an LXC container: nesting enabled (`security.nesting=true`). Unprivileged containers also need `keyctl=1` (Proxmox: `features: nesting=1,keyctl=1`). Available on any LXC/LXD host — Proxmox exposes this as a checkbox, but it's a standard LXC feature. Note: runc 1.3.x has known issues with Docker inside LXC containers.
- **Windows/WSL:** Expected to work via Docker Desktop + WSL2. Known limitations: path translation between Windows and WSL paths, UID/GID mapping differences, `.gitignore` line ending handling. Not a primary target but should degrade gracefully.

## First-Run Experience

`yoloai` with no arguments shows help/usage. `yoloai init` performs first-time setup:
1. Create `~/.yoloai/` directory structure.
2. Write a default `config.yaml` with sensible defaults.
3. Build the base image.
4. Print a quick-start guide.

## Security Considerations

- **The agent runs arbitrary code** inside the container: shell commands, file operations, network requests. The container provides isolation, not prevention.
- **All directories are read-only by default.** You explicitly opt in to write access per directory via `:rw` (live) or `:copy` (staged).
- **`:copy` directories** protect your originals. Changes only land when you explicitly `yoloai apply`.
- **`:rw` directories** give the agent direct read/write access. Use only when you've committed your work or don't mind destructive changes. The tool warns if it detects uncommitted git changes.
- **API key exposure:** The `ANTHROPIC_API_KEY` is injected via file-based credential injection (bind-mounted at `/run/secrets/`, read by entrypoint, host file cleaned up immediately). This hides the key from `docker inspect`, `docker commit`, and `docker logs`. The key is still present in the agent process's environment (unavoidable — CLI agents expect env vars). Use scoped API keys with spending limits where possible. See RESEARCH.md "Credential Management for Docker Containers" for the full analysis of approaches and tradeoffs.
- **SSH keys:** If you mount `~/.ssh` into the container (even read-only), the agent can read private keys. Prefer SSH agent forwarding: add `${SSH_AUTH_SOCK}:${SSH_AUTH_SOCK}:ro` to `mounts` and `SSH_AUTH_SOCK: ${SSH_AUTH_SOCK}` to `env` in config. This passes the socket without exposing key material.
- **Network access** is unrestricted by default (required for agent API calls). The agent can download binaries, connect to external services, or exfiltrate code. Use `--network-isolated` to restrict traffic to the agent's required API domains, `--network-allow <domain>` for finer control, or `--network-none` for full isolation.
- **Network isolation implementation:** `--network-isolated` uses a layered approach based on verified production implementations (Anthropic's sandbox-runtime, Docker Sandboxes, and Anthropic's own devcontainer firewall):
  1. **Network topology:** The sandbox container runs on a Docker `--internal` network with no route to the internet. A separate proxy sidecar container bridges the internal and external networks — it is the only exit.
  2. **Proxy allowlist:** The proxy sidecar (a lightweight forward proxy) allowlists Anthropic API domains by default. HTTPS traffic uses `CONNECT` tunneling (no MITM — the proxy sees the domain from the `CONNECT` request). `--network-allow <domain>` adds domains to the allowlist.
  3. **iptables defense-in-depth:** Inside the sandbox container, iptables rules restrict outbound traffic to only the proxy's IP and port. This prevents bypass via any path other than the proxy. Requires `CAP_NET_ADMIN` (a separate capability from `CAP_SYS_ADMIN` — both must be granted when using overlay + `--network-isolated`; for `copy_strategy: full`, only `CAP_NET_ADMIN` is added). The entrypoint configures iptables rules while running as root, then drops privileges via `gosu` — the agent never has `CAP_NET_ADMIN`.
  4. **DNS control:** The sandbox container uses the proxy sidecar as its DNS resolver. Direct outbound DNS (UDP 53) is blocked by iptables. This mitigates the DNS exfiltration vector demonstrated by CVE-2025-55284.
  **Proxy sidecar lifecycle:** yoloai manages the proxy container automatically. On `yoloai new --network-isolated`, a proxy container (`yoloai-<name>-proxy`) is created on both the internal and external networks, configured with the allowlist from defaults + `--network-allow` domains. The proxy container is stopped/started/destroyed alongside the sandbox container. The proxy image (`yoloai-proxy`) is built by `yoloai build` (see `yoloai build` section). The allowlist is stored in `meta.json` for sandbox recreation.

  Known limitations: DNS exfiltration is mitigated but not fully eliminated — the proxy's DNS resolver must forward queries upstream, and data can be encoded in subdomain queries. Domain fronting remains theoretically possible on CDNs that haven't disabled it. These limitations are shared by all production implementations including Docker Sandboxes and Anthropic's own devcontainer. See RESEARCH.md "Network Isolation Research" for detailed analysis of bypass vectors.
- **Runs as non-root** inside the container (user `yoloai` matching host UID/GID). Claude Code requires this (refuses `--dangerously-skip-permissions` as root); Codex runs non-root by convention.
- **`CAP_SYS_ADMIN` capability** is granted to the container when using the overlay copy strategy (the default). This is required for overlayfs mounts inside the container. It is a broad capability — it also permits other mount operations and namespace manipulation. The container's namespace isolation limits the blast radius, but this is a tradeoff: overlay gives instant setup and space efficiency at the cost of a wider capability grant. Users concerned about this can set `copy_strategy: full` to avoid the capability entirely.
- **Dangerous directory detection:** The tool refuses to mount `$HOME`, `/`, or system directories unless `:force` is appended, preventing accidental exposure of your entire filesystem.
- **Privilege escalation via recipes:** The `setup` commands and `cap_add`/`devices` config fields enable significant privilege escalation. These are power-user features for advanced setups (e.g., Tailscale, GPU passthrough) but have no guardrails — a misconfigured recipe could undermine container isolation. Document risks clearly when these features are used.

## Resolved Design Decisions

1. ~~**Headless mode?**~~ Agent definition specifies prompt delivery mode. Claude always uses interactive mode via tmux (`--prompt` fed via `tmux send-keys`). Codex uses headless mode (`codex exec`) when `--prompt` is provided, interactive mode otherwise. Tmux used in all cases for logging and attach.
2. ~~**Multiple mounts?**~~ Yes. Workdir is primary (cwd), aux dirs are dependencies. Error on container path collision.
3. ~~**Dotfiles/tools?**~~ Config file with defaults + profiles. Profiles use user-supplied Dockerfiles for full flexibility.
4. ~~**Resource limits?**~~ Configurable in `config.yaml` with sensible defaults.
5. ~~**Auto-destroy?**~~ No. Sandboxes persist until explicitly destroyed.
6. ~~**Git integration?**~~ Yes. Copy mode auto-inits git for clean diffs. `yoloai apply` excludes `.git/`.
7. ~~**Default mode?**~~ All dirs read-only by default. Per-directory `:rw` (live) or `:copy` (staged) suffixes. Workdir must have one of these.
8. ~~**Container work directory?**~~ Directories are mounted at their original host paths (mirrored) by default, so configs, error messages, and symlinks work without translation. Custom paths available via `=<path>` override. `/yoloai/` reserved for internals. The `/work` prefix was considered but rejected — path consistency (matching host paths) outweighs the minor safety benefit, and dangerous directory detection already prevents mounting over system paths.
9. ~~**Copy strategy?**~~ OverlayFS by default (`copy_strategy: auto`). The original directory is bind-mounted read-only as the overlayfs lower layer; writes go to an upper directory in sandbox state. Git provides diff/apply on the merged view. Falls back to full copy if overlayfs isn't available. Works cross-platform — Docker on macOS/Windows runs a Linux VM, so overlayfs works inside the container regardless of host OS. VirtioFS overhead for macOS host reads is acceptable (70-90% native after page cache warms). Config option `copy_strategy: full` available for users who prefer the traditional full-copy approach or want to avoid `CAP_SYS_ADMIN`.
10. ~~**Config template generation?**~~ Deferred to v2. `yoloai config generate --agent codex` would generate a starter config tailored to a specific agent with recommended settings.

## Design Considerations

### Overlay + existing `.git/` directories

When the original directory is a git repo, `.git/` is in the overlay lower layer (read-only). Git operations inside the sandbox (add, commit, etc.) write to `.git/` internals (objects, index, refs), and these writes go to the overlay upper directory via copy-on-write. The agent sees the full project; writes go to upper only. This means: (a) the upper directory will contain modified `.git/` files alongside project changes, and (b) `yoloai diff` must diff against the *original* repo's HEAD SHA (recorded in `meta.json`), not whatever HEAD the sandbox has moved to. This works correctly because `meta.json` records the original HEAD at sandbox creation time, and `yoloai diff` uses `git diff <original-HEAD>` regardless of subsequent commits inside the sandbox.

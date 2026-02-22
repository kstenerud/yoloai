# yoloai: Sandboxed Claude CLI Runner

## Goal

Run Claude CLI with `--dangerously-skip-permissions` inside disposable, isolated containers so that Claude can work autonomously without constant permission prompts. Project directories are presented as isolated writable views inside the container. The user reviews changes via `yoloai diff` and applies them back to the originals via `yoloai apply` when satisfied.

## Architecture

```
┌──────────────────────────────────────────────────┐
│  Host (any machine with Docker)                  │
│                                                  │
│  yoloai CLI (Python script)                      │
│    │                                             │
│    ├─ docker run ──► sandbox-1  ← ephemeral      │
│    │                  ├ tmux                     │
│    │                  ├ claude --dangerously-... │
│    │                  ├ /work ──► project dirs   │
│    │                  └ /claude-state            │
│    │                                             │
│    ├─ docker run ──► sandbox-2                   │
│    └─ ...                                        │
│                                                  │
│  ~/.yoloai/sandboxes/<name>/  ← persistent state   │
│    ├── work/          (overlay upper dirs or full copies) │
│    ├── claude-state/  (Claude's ~/.claude)       │
│    ├── log.txt        (session output)           │
│    ├── prompt.txt     (initial prompt)           │
│    └── meta.json      (config, paths, status)    │
└──────────────────────────────────────────────────┘
```

### Container Technology: Docker

- Works natively on Linux/macOS, inside LXC with Proxmox nesting enabled, etc.
- Provides process, filesystem, and network namespace isolation
- Ephemeral by default — `docker rm` and it's gone
- All persistent state lives on the host in `~/.yoloai/sandboxes/`

### Key Principle: Containers are ephemeral, state is not

The Docker container is disposable — it can crash, be destroyed, be recreated. Everything that matters lives in the sandbox's state directory on the host:
- **`work/`** — copies of project directories (what Claude modifies)
- **`claude-state/`** — Claude's `~/.claude/` directory (session history, settings)
- **`prompt.txt`** — the initial prompt to feed Claude
- **`log.txt`** — captured tmux output for post-mortem review
- **`meta.json`** — original paths, mode, profile, timestamps, status, `yoloai_version` (for format migration on upgrades)

## Components

### 1. Docker Images

**Base image (`yoloai-base`):** Minimal foundation, rebuilt occasionally.
- Node.js (for Claude CLI)
- Claude CLI (`@anthropic-ai/claude-code`)
- tmux
- git
- Common dev tools (build-essential, python3, etc.)
- **Non-root user** (`yoloai`, matching host UID/GID via entrypoint at container run time — not baked into the image at build time, so images are portable across machines) — Claude CLI refuses `--dangerously-skip-permissions` as root

**Profile images (`yoloai-<profile>`):** Derived from base, one per profile. Users supply a `Dockerfile` per profile with `FROM yoloai-base`. This avoids the limitations of auto-generating Dockerfiles from package lists and gives full flexibility (PPAs, tarballs, custom install steps).

```
~/.yoloai/profiles/
├── go-dev/
│   └── Dockerfile       ← FROM yoloai-base; RUN apt-get install ...
└── node-dev/
    └── Dockerfile
```

`yoloai build` with no arguments rebuilds the base image. `yoloai build <profile>` rebuilds that profile's image. `yoloai build --all` rebuilds everything (base first, then all profiles).

### 2. Config File (`~/.yoloai/config.yaml`)

```yaml
# Always applied to every sandbox
defaults:
  # Files/dirs copied into claude-state/ on first sandbox run.
  # These seed Claude's environment. Sandbox gets its own copy (not bind-mounted).
  # Options:
  #   claude_files: home              # copy standard files from $HOME/.claude/ (convenient, non-portable)
  #   claude_files:                   # explicit file list (deterministic, for team setups)
  #     - .claude/CLAUDE.md           #   paths relative to $HOME
  #     - /path/to/shared/settings    #   absolute paths for deterministic setups
  #   (omit claude_files entirely)    # nothing copied — safe default
  # Profile claude_files replaces defaults (no merge).
  # claude_files: home

  mounts:
    - ~/.gitconfig:/home/yoloai/.gitconfig:ro

  copy_strategy: auto                   # auto | overlay | full
                                        # auto: use overlayfs where available, fall back to full copy
                                        # overlay: overlayfs lower layer (instant, deltas-only, needs CAP_SYS_ADMIN)
                                        # full: traditional full copy (portable, all reads VM-local)
  auto_commit_interval: 0               # seconds between auto-commits in :copy dirs; 0 = disabled
  resources:
    cpus: 4                           # docker --cpus
    memory: 8g                        # docker --memory
    disk: 10g                         # docker --storage-opt size=

# Named profiles
profiles:
  go-dev:
    claude_files:                     # replaces defaults (no merge)
      - .claude/CLAUDE.md
      - /shared/configs/go-claude-settings.json   # team-shared config
    mounts:
      - ~/.ssh:/home/yoloai/.ssh:ro
    resources:                        # override defaults per profile
      memory: 16g
  node-dev: {}
```

- `defaults` apply to all sandboxes regardless of profile.
- `defaults.claude_files` controls what files are copied into the sandbox's `claude-state/` directory on first run. Set to `home` to copy standard files from `$HOME/.claude/` (convenient but non-portable). Set to a list of paths (relative to `$HOME` or absolute) for deterministic setups. Omit entirely to copy nothing (safe default). Profile `claude_files` **replaces** (not merges with) defaults.
- `defaults.mounts` and profile `mounts` are bind mounts added at container run time.
- `defaults.resources` sets baseline limits. Profiles can override individual values.
- Profile Docker images are defined by the Dockerfiles in `~/.yoloai/profiles/<name>/`, not in this config. The config only holds runtime settings (mounts, resources, claude_files).

#### Directory Mappings in Profiles

For setups users repeat often, profiles support a `directories` section so the elaborate configurations live in YAML and the CLI stays clean:

```yaml
profiles:
  my-project:
    directories:
      - path: /home/user/my-app
        mode: copy
      - path: /home/user/shared-lib
        mode: rw
        mount: /usr/local/lib/shared    # custom mount point (default: /work/<basename>/)
      - path: /home/user/common-types
        # default: read-only
```

CLI arguments for one-offs, config for repeatability — same options available in both. When both CLI directories and profile directories are specified, CLI directories take precedence.

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

`cap_add`, `devices`, and `setup` are available but not in the default config. `setup` commands run at container start before Claude launches.

Config values support `${VAR}` environment variable interpolation from the host environment (following Docker Compose conventions). This allows secrets like `${TAILSCALE_AUTHKEY}` to be referenced in config without hardcoding. Unset variables produce an error at sandbox creation time (fail-fast, not silent empty string).

### 3. CLI Script (`yoloai`)

Single Python script. No external dependencies beyond the standard library and Docker.

## Commands

```
yoloai new [options] <name> <dir> [<dir>...]   Create and start a sandbox
yoloai list                                    List sandboxes and their status
yoloai attach <name>                           Attach to a sandbox's tmux session
yoloai status <name>                           Show detailed sandbox info
yoloai log <name>                              Show sandbox session log
yoloai log -f <name>                           Tail sandbox session log
yoloai diff <name>                             Show changes Claude made
yoloai apply <name>                            Copy changes back to original dirs
yoloai exec <name> <command>                   Run a command inside the sandbox
yoloai stop <name>                             Stop a sandbox (preserving state)
yoloai start <name>                            Start a stopped sandbox
yoloai restart <name>                          Restart Claude in an existing sandbox
yoloai destroy <name>                          Stop and remove a sandbox
yoloai build [profile|--all]                   Build/rebuild Docker image(s)
```

### `yoloai new`

The first directory is the **primary** project — Claude's working directory. All directories are **bind-mounted read-only by default** under `/work/<dirname>/`.

Directory argument syntax: `<path>[:<suffixes>][=<mount-point>]`

Suffixes (combinable in any order):
- `:rw` — bind-mounted read-write (live, immediate changes)
- `:copy` — copied to sandbox state, read-write, diff/apply available
- `:force` — override dangerous directory detection

Mount point:
- `=<path>` — mount at a custom container path instead of `/work/<dirname>/`

The primary directory **must** have `:rw` or `:copy` — error otherwise.

```
# Copy primary (safe, reviewable), deps read-only
yoloai newfix-build --prompt "fix the build" ./my-app:copy ./shared-lib ./common-types

# Live-edit primary, one dep also writable
yoloai newmy-task ./my-app:rw ./shared-lib:rw ./common-types

# Custom mount points for tools that need specific paths
yoloai newmy-task ./my-app:copy=/opt/myapp ./shared-lib=/usr/local/lib/shared ./common-types

# Error: primary has no write access
yoloai newmy-task ./my-app ./shared-lib
```

Name is required. If omitted, an error is shown with a helpful message suggesting a name based on the primary directory basename.

Example layout inside the container:

```
yoloai newmy-task ./my-app:copy ./shared-lib:rw ./common-types
```

```
/work/
├── my-app/          ← primary (copied, read-write, diff/apply available)
├── shared-lib/      ← dependency (bind-mounted, read-write, live)
└── common-types/    ← dependency (bind-mounted, read-only)
```

With custom mount points:

```
yoloai newmy-task ./my-app:copy=/opt/myapp ./shared-lib=/usr/local/lib/shared ./common-types
```

```
/opt/myapp/                  ← primary (copied, Claude's cwd)
/usr/local/lib/shared/       ← dependency (bind-mounted, read-only)
/work/common-types/          ← dependency (default location, read-only)
```

Options:
- `--profile <name>`: Use a profile's derived image and mounts. No profile = base image + defaults only.
- `--prompt <text>`: Initial prompt/task for Claude (see Prompt Mechanism below).
- `--model <model>`: Claude model to use. If omitted, uses Claude CLI's default.
- `--network-isolated`: Allow only Anthropic API traffic. Claude can function but cannot access other external services, download arbitrary binaries, or exfiltrate code.
- `--network-allow <domain>`: Allow traffic to specific additional domains (can be repeated). Combines with `--network-isolated`.
- `--network-none`: Run with `--network none` for full network isolation (Claude API calls will also fail — only useful for offline tasks with pre-cached models).

**Workflow:**

1. Error if primary directory has no `:rw` or `:copy` suffix.
2. Error if any two directories resolve to the same absolute container path (default `/work/<dirname>/` or custom `=<path>`).
3. For each `:copy` directory, set up an isolated writable view using one of two strategies (selected by `copy_strategy` config, default `auto`):

   **Overlay strategy** (default when available):
   - Host side: bind-mount the original directory read-only into the container. Provide an empty upper directory from `~/.yoloai/sandboxes/<name>/work/<dirname>/`.
   - Container side (entrypoint): mount overlayfs merging lower (original, read-only) + upper at the container mount point (`/work/<dirname>/`). Claude sees the full project; writes go to upper only. Original directory is inherently protected (read-only lower layer). The entrypoint must be idempotent — use `mkdir -p` for directories and check `mountpoint -q` before mounting, so it handles both fresh starts and restarts cleanly.
   - After overlay mount: `git init` + `git add -A` + `git commit -m "initial"` on the merged view to create a baseline. If the directory is already a git repo, record the current HEAD SHA in `meta.json` and use it as the baseline instead.
   - Requires `CAP_SYS_ADMIN` capability on the container (not full `--privileged`).

   **Full copy strategy** (fallback):
   - If the directory is a git repo, record the current HEAD SHA in `meta.json`.
   - Copy to `~/.yoloai/sandboxes/<name>/work/<dirname>/`. Everything is copied regardless of `.gitignore` — the `disk` resource limit (container-level) is the storage guardrail for both copy strategies.
   - If the copy already has a `.git/` directory (from the original repo), use the recorded SHA as the baseline — `yoloai diff` will diff against it.
   - If the copy has no `.git/`, `git init` + `git add -A` + `git commit -m "initial"` to create a baseline.

   Both strategies produce the same result from the user's perspective: a protected, writable view with git-based diff/apply. The overlay strategy is instant and space-efficient; the full copy strategy is more portable. `auto` tries overlay first, falls back to full copy if `CAP_SYS_ADMIN` is unavailable or the kernel doesn't support nested overlayfs.

   Note: `git add -A` naturally honors `.gitignore` if one is present, so gitignored files (e.g., `node_modules`) won't clutter `yoloai diff` output regardless of strategy.
4. If `auto_commit_interval` > 0, start a background auto-commit loop for `:copy` directories inside the container for recovery. Disabled by default.
5. Store original paths, modes, and mapping in `meta.json`.
6. Start Docker container (see Container Startup below).

### Safety Checks

Before creating the sandbox:
- **Dangerous directory detection:** Error if any mount target is `$HOME`, `/`, or a system directory. Override with `:force` suffix (e.g., `$HOME:force`, `$HOME:rw:force`).
- **Dirty git repo detection:** If any `:rw` or `:copy` directory is a git repo with uncommitted changes, warn with specifics and prompt for confirmation:
  ```
  WARNING: ./my-app has uncommitted changes (3 files modified, 1 untracked)
  These changes will be visible to Claude and could be modified or lost.
  Continue? [y/N]
  ```

### Container Startup

1. Generate a **sandbox context file** at `/yoloai/context.md` describing the environment for Claude: which directory is primary, which are dependencies, mount paths and access mode of each (read-only / read-write / copy), available tools, and how auto-save works. This file lives outside the work tree in the `/yoloai/` internal directory, so it never pollutes project files, git baselines, or diffs. Claude is pointed to it via `--append-system-prompt` or inclusion in the initial prompt.
2. Start Docker container (as non-root user `yoloai`) with:
   - `:copy` directories: overlay strategy mounts originals as overlayfs lower layers with upper dirs from sandbox state; full copy strategy mounts copies from sandbox state. Both at their mount point (custom or `/work/<dirname>`, read-write)
   - `:rw` directories bind-mounted at their mount point (custom or `/work/<dirname>`, read-write)
   - Default (no suffix) directories bind-mounted at their mount point (custom or `/work/<dirname>`, read-only)
   - `claude-state/` mounted at `/home/yoloai/.claude` (read-write, per-sandbox)
   - Files listed in `claude_files` (from config) copied into `claude-state/` on first run
   - Config mounts from defaults + profile
   - Resource limits from defaults + profile
   - `ANTHROPIC_API_KEY` from host environment
   - `CAP_SYS_ADMIN` capability (required for overlayfs mounts inside the container; omitted when `copy_strategy: full`)
   - Container name: `yoloai-<name>`
   - User: `yoloai` (UID/GID matching host user)
   - `/yoloai/` internal directory for sandbox context file and overlay working directories (not mounted from host — ephemeral, lives on the container filesystem)
3. Run `setup` commands from config (if any).
4. Start tmux session named `main` with logging to `log.txt` (`tmux pipe-pane`).
5. Inside tmux: `cd <primary-mount-point> && claude --dangerously-skip-permissions [--model X]`
6. Wait ~3s for Claude to initialize.
7. If `prompt.txt` exists, feed it via `tmux load-buffer` + `tmux paste-buffer` + `tmux send-keys Enter Enter`.

### Prompt Mechanism

Claude always runs in interactive mode. When `--prompt` is provided:
1. The prompt is saved to `~/.yoloai/sandboxes/<name>/prompt.txt`.
2. After Claude starts inside tmux, wait for Claude to be ready (~3s).
3. For long prompts, write to a temp file and use `tmux load-buffer` + `tmux paste-buffer` to avoid shell escaping issues.
4. Send via `tmux send-keys ... Enter Enter` (**double Enter** — Claude Code requires this to submit via tmux).
5. Claude begins working immediately but remains interactive — if it needs clarification, the question sits in the tmux session until you `yoloai attach`.

Without `--prompt`, you get a normal interactive session waiting for input.

### `yoloai attach`

Runs `docker exec -it yoloai-<name> tmux attach -t main`.

Detach with standard tmux `Ctrl-b d` — container keeps running.

### `yoloai status <name>`

Shows sandbox details from `meta.json`: profile, directories with their access modes (read-only / rw / copy), creation time, and whether Claude is still running (checks if the Claude process is alive inside the container).

### `yoloai diff`

For `:copy` directories: runs `git diff` + `git status` against the baseline (the recorded HEAD SHA for existing repos, or the synthetic initial commit for non-git dirs). Shows exactly what Claude changed with proper diff formatting.

For `:rw` directories: if the original is a git repo, runs `git diff` against it. Otherwise, notes that diff is not available for live-mounted dirs without git.

Read-only directories are skipped (no changes possible).

### `yoloai apply`

For `:copy` directories only. Generates a patch from `git diff` in the sandbox (whether overlay-merged or full-copy) and applies it to the original directory. This handles additions, modifications, and deletions. Works identically regardless of `copy_strategy` — git provides the diff in both cases. For dirs that had no original git repo, excludes the synthetic `.git/` directory created by yoloai.

Before applying, shows a summary via `git diff --stat` (files changed, insertions, deletions) and verifies the patch applies cleanly via `git apply --check`. Prompts for confirmation before proceeding.

`:rw` directories need no apply — changes are already live. Read-only directories have no changes.

### `yoloai destroy`

`docker stop` + `docker rm` the container. Removes `~/.yoloai/sandboxes/<name>/` entirely. No special overlay cleanup needed — the kernel tears down the mount namespace when the container stops.

Asks for confirmation if Claude is still running.

### `yoloai log`

`yoloai log <name>` displays the session log (`log.txt`) for the named sandbox.

`yoloai log -f <name>` tails the log in real time (like `tail -f`).

### `yoloai exec`

`yoloai exec <name> <command>` runs a command inside the sandbox container without attaching to tmux. Useful for debugging (`yoloai exec my-sandbox bash`) or quick operations (`yoloai exec my-sandbox npm install foo`).

Implemented as `docker exec yoloai-<name> <command>`, with `-i` added when stdin is a pipe/TTY and `-t` added when stdin is a TTY. This allows both interactive use (`yoloai exec my-sandbox bash`) and non-interactive use (`yoloai exec my-sandbox ls /work`, `echo "test" | yoloai exec my-sandbox cat`).

### `yoloai list`

Lists all sandboxes with their current status.

| Column | Description |
|--------|-------------|
| NAME | Sandbox name |
| STATUS | `running`, `stopped`, `exited` (Claude exited but container up) |
| PROFILE | Profile name or `(base)` |
| AGE | Time since creation |
| PRIMARY | Primary directory path |

Options:
- `--running`: Show only running sandboxes.
- `--stopped`: Show only stopped sandboxes.
- `--json`: Output as JSON for scripting.

### `yoloai build`

`yoloai build` with no arguments rebuilds the base image (`yoloai-base`).

`yoloai build <profile>` rebuilds a specific profile's image (which derives from `yoloai-base`).

`yoloai build --all` rebuilds everything: base image first, then all profile images.

Useful after modifying a profile's Dockerfile or when the base image needs updating (e.g., new Claude CLI version).

### `yoloai stop`

`yoloai stop <name>` stops a sandbox container, preserving all state (work directory, claude-state, logs). The container can be restarted later without losing progress.

### `yoloai start`

`yoloai start <name>` ensures the sandbox is running — idempotent "get it running, however needed":
- If the container is stopped: starts it. The entrypoint re-establishes overlayfs mounts (mounts don't survive `docker stop` — this is by design; the upper directory persists on the host and the entrypoint re-mounts idempotently).
- If the container is running but Claude has exited: relaunches Claude in the existing tmux session.
- If already running: no-op.

This eliminates the need to diagnose *why* a sandbox isn't running before choosing a command.

### `yoloai restart`

`yoloai restart <name>` is equivalent to `yoloai stop <name>` followed by `yoloai start <name>`. Useful for a clean restart of the entire sandbox environment.

### Image Cleanup

Docker images (`yoloai-base`, `yoloai-<profile>`) accumulate indefinitely. A cleanup mechanism is needed but deferred pending research into Docker's image lifecycle: base images are shared parents of profile images, profile images may have running containers, layer caching means "removing" doesn't necessarily free space, and `docker image prune` vs `docker rmi` have different semantics. Half-baked pruning could break running sandboxes or nuke images the user spent time building.

## Directory Layout

```
~/.yoloai/
├── config.yaml                  ← defaults + profile runtime config
├── profiles/
│   ├── go-dev/
│   │   └── Dockerfile           ← FROM yoloai-base
│   └── node-dev/
│       └── Dockerfile
└── sandboxes/
    └── <name>/
        ├── meta.json            ← original paths, mode, profile, timestamps
        ├── prompt.txt           ← initial prompt (if provided)
        ├── log.txt              ← tmux session log
        ├── claude-state/        ← Claude's ~/.claude (per-sandbox, read-write)
        └── work/                ← overlay upper dirs (deltas) or full copies, for :copy dirs only
            ├── <primary>/       ← if primary is :copy
            └── <dep>/           ← if dep is :copy
```

## Claude Authentication

The `ANTHROPIC_API_KEY` environment variable is passed from the host into the container. The user sets this once in their shell profile on the host.

## Prerequisites

- Python 3.10+ (floor on Ubuntu 22.04+, Debian 12+, Fedora 40+, Arch; macOS users with Docker will have this)
- Docker installed and running (clear error message if Docker daemon is not available)
- `ANTHROPIC_API_KEY` set in environment
- If running from inside an LXC container: nesting enabled (`security.nesting=true`). Available on any LXC/LXD host — Proxmox exposes this as a checkbox, but it's a standard LXC feature.
- **Windows/WSL:** Expected to work via Docker Desktop + WSL2. Known limitations: path translation between Windows and WSL paths, UID/GID mapping differences, `.gitignore` line ending handling. Not a primary target but should degrade gracefully.

## First-Run Experience

`yoloai` with no arguments shows help/usage. `yoloai init` performs first-time setup:
1. Create `~/.yoloai/` directory structure.
2. Write a default `config.yaml` with sensible defaults.
3. Build the base image.
4. Print a quick-start guide.

## Security Considerations

- **Claude runs arbitrary code** inside the container: shell commands, file operations, network requests. The container provides isolation, not prevention.
- **All directories are read-only by default.** You explicitly opt in to write access per directory via `:rw` (live) or `:copy` (staged).
- **`:copy` directories** protect your originals. Changes only land when you explicitly `yoloai apply`.
- **`:rw` directories** give Claude direct read/write access. Use only when you've committed your work or don't mind destructive changes. The tool warns if it detects uncommitted git changes.
- **API key exposure:** The `ANTHROPIC_API_KEY` is visible inside the container. Claude (or code Claude runs) could exfiltrate it. Use scoped API keys with spending limits where possible. Do not use sandboxes for untrusted workloads. A file-based approach (mount a file containing the key, read it in the entrypoint) would reduce casual exposure via `docker inspect` and process listing. Docker secrets are another option for Swarm deployments.
- **SSH keys:** If you mount `~/.ssh` into the container (even read-only), Claude can read private keys. Prefer SSH agent forwarding via config (`SSH_AUTH_SOCK` passthrough) over mounting key files directly.
- **Network access** is unrestricted by default (required for Claude API calls). Claude can download binaries, connect to external services, or exfiltrate code. Use `--network-isolated` to allow only Anthropic API traffic, `--network-allow <domain>` for finer control, or `--network-none` for full isolation.
- **Network isolation implementation:** `--network-isolated` uses a proxy-based approach (proven by Anthropic's sandbox-runtime, Trail of Bits, and Fence). The container runs on an isolated Docker network with no direct internet access. A lightweight HTTP/SOCKS proxy inside the container is the only exit — it whitelists Anthropic API domains by default. `--network-allow <domain>` adds domains to the proxy whitelist. Known limitation: this only covers HTTP/HTTPS traffic. Raw TCP and SSH connections bypass the proxy (a constraint shared with Anthropic's own implementation; see GitHub issues #11481, #24091). Network isolation is a v1 requirement — real CVEs demonstrate the threat (CVE-2025-55284: DNS exfiltration, Claude Pirate: File API exfiltration), and competitors treat it as table stakes (Codex disables network by default).
- **Runs as non-root** inside the container (user `yoloai` matching host UID/GID). This is required because Claude CLI refuses `--dangerously-skip-permissions` as root.
- **`CAP_SYS_ADMIN` capability** is granted to the container when using the overlay copy strategy (the default). This is required for overlayfs mounts inside the container. It is a broad capability — it also permits other mount operations and namespace manipulation. The container's namespace isolation limits the blast radius, but this is a tradeoff: overlay gives instant setup and space efficiency at the cost of a wider capability grant. Users concerned about this can set `copy_strategy: full` to avoid the capability entirely.
- **Dangerous directory detection:** The tool refuses to mount `$HOME`, `/`, or system directories unless `:force` is appended, preventing accidental exposure of your entire filesystem.
- **Security design needs dedicated research.** The security story around Docker + Claude (credential handling, network isolation, privilege escalation, exfiltration vectors) needs dedicated research into best practices before finalizing. The `setup` commands and `cap_add`/`devices` fields enable significant privilege escalation with insufficient documentation of risks. Ad-hoc mitigations risk missing the bigger picture.

## Resolved Design Decisions

1. ~~**Headless mode?**~~ Always interactive via tmux. `--prompt` feeds the first message via `tmux send-keys` so Claude starts immediately but can still ask questions.
2. ~~**Multiple mounts?**~~ Yes. First dir is primary (cwd), rest are dependencies. Error on container path collision.
3. ~~**Dotfiles/tools?**~~ Config file with defaults + profiles. Profiles use user-supplied Dockerfiles for full flexibility.
4. ~~**Resource limits?**~~ Configurable in `config.yaml` with sensible defaults.
5. ~~**Auto-destroy?**~~ No. Sandboxes persist until explicitly destroyed.
6. ~~**Git integration?**~~ Yes. Copy mode auto-inits git for clean diffs. `yoloai apply` excludes `.git/`.
7. ~~**Default mode?**~~ All dirs read-only by default. Per-directory `:rw` (live) or `:copy` (staged) suffixes. Primary must have one of these.
8. ~~**Copy strategy?**~~ OverlayFS by default (`copy_strategy: auto`). The original directory is bind-mounted read-only as the overlayfs lower layer; writes go to an upper directory in sandbox state. Git provides diff/apply on the merged view. Falls back to full copy if overlayfs isn't available. Works cross-platform — Docker on macOS/Windows runs a Linux VM, so overlayfs works inside the container regardless of host OS. VirtioFS overhead for macOS host reads is acceptable (70-90% native after page cache warms). Config option `copy_strategy: full` available for users who prefer the traditional full-copy approach or want to avoid `CAP_SYS_ADMIN`.

## Design Considerations

### Overlay + existing `.git/` directories

When the original directory is a git repo, `.git/` is in the overlay lower layer (read-only). Git operations inside the sandbox (add, commit, etc.) write to `.git/` internals (objects, index, refs), and these writes go to the overlay upper directory via copy-on-write. This means: (a) the upper directory will contain modified `.git/` files alongside project changes, and (b) `yoloai diff` must diff against the *original* repo's HEAD SHA (recorded in `meta.json`), not whatever HEAD the sandbox has moved to. This works correctly because `meta.json` records the original HEAD at sandbox creation time, and `yoloai diff` uses `git diff <original-HEAD>` regardless of subsequent commits inside the sandbox.

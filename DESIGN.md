# yolo-claude: Sandboxed Claude CLI Runner

## Goal

Run Claude CLI with `--dangerously-skip-permissions` inside disposable, isolated containers so that Claude can work autonomously without constant permission prompts. Project directories are copied in, the user reviews the results, and applies them back when satisfied.

## Architecture

```
┌──────────────────────────────────────────────────┐
│  Host (any machine with Docker)                  │
│                                                  │
│  yolo CLI (Python script)                        │
│    │                                             │
│    ├─ docker run ──► sandbox-1  ← ephemeral      │
│    │                  ├ tmux                     │
│    │                  ├ claude --dangerously-... │
│    │                  ├ /work ──► sandbox state  │
│    │                  └ /claude-state            │
│    │                                             │
│    ├─ docker run ──► sandbox-2                   │
│    └─ ...                                        │
│                                                  │
│  ~/.yolo/sandboxes/<name>/  ← persistent state   │
│    ├── work/          (project copies)           │
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
- All persistent state lives on the host in `~/.yolo/sandboxes/`

### Key Principle: Containers are ephemeral, state is not

The Docker container is disposable — it can crash, be destroyed, be recreated. Everything that matters lives in the sandbox's state directory on the host:
- **`work/`** — copies of project directories (what Claude modifies)
- **`claude-state/`** — Claude's `~/.claude/` directory (session history, settings)
- **`prompt.txt`** — the initial prompt to feed Claude
- **`log.txt`** — captured tmux output for post-mortem review
- **`meta.json`** — original paths, mode, profile, timestamps, status, `yolo_version` (for format migration on upgrades)

## Components

### 1. Docker Images

**Base image (`yolo-base`):** Minimal foundation, rebuilt occasionally.
- Node.js (for Claude CLI)
- Claude CLI (`@anthropic-ai/claude-code`)
- tmux
- git
- Common dev tools (build-essential, python3, etc.)
- **Non-root user** (`yolo`, matching host UID/GID via entrypoint at container run time — not baked into the image at build time, so images are portable across machines) — Claude CLI refuses `--dangerously-skip-permissions` as root

**Profile images (`yolo-<profile>`):** Derived from base, one per profile. Users supply a `Dockerfile` per profile with `FROM yolo-base`. This avoids the limitations of auto-generating Dockerfiles from package lists and gives full flexibility (PPAs, tarballs, custom install steps).

```
~/.yolo/profiles/
├── go-dev/
│   └── Dockerfile       ← FROM yolo-base; RUN apt-get install ...
└── node-dev/
    └── Dockerfile
```

`yolo build` with no arguments rebuilds the base image. `yolo build <profile>` rebuilds that profile's image. `yolo build --all` rebuilds everything (base first, then all profiles).

### 2. Config File (`~/.yolo/config.yaml`)

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
    - ~/.gitconfig:/home/yolo/.gitconfig:ro

  max_copy_size: 1g                    # max size per :copy dir; error if exceeded
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
      - ~/.ssh:/home/yolo/.ssh:ro
    resources:                        # override defaults per profile
      memory: 16g
  node-dev: {}
```

- `defaults` apply to all sandboxes regardless of profile.
- `defaults.claude_files` controls what files are copied into the sandbox's `claude-state/` directory on first run. Set to `home` to copy standard files from `$HOME/.claude/` (convenient but non-portable). Set to a list of paths (relative to `$HOME` or absolute) for deterministic setups. Omit entirely to copy nothing (safe default). Profile `claude_files` **replaces** (not merges with) defaults.
- `defaults.mounts` and profile `mounts` are bind mounts added at container run time.
- `defaults.resources` sets baseline limits. Profiles can override individual values.
- Profile Docker images are defined by the Dockerfiles in `~/.yolo/profiles/<name>/`, not in this config. The config only holds runtime settings (mounts, resources, claude_files).

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

### 3. CLI Script (`yolo`)

Single Python script. No external dependencies beyond the standard library and Docker.

## Commands

```
yolo new <name> [options] <dir> [<dir>...]   Create and start a sandbox
yolo list                                    List sandboxes and their status
yolo attach <name>                           Attach to a sandbox's tmux session
yolo status <name>                           Show detailed sandbox info
yolo log <name>                              Show sandbox session log
yolo log -f <name>                           Tail sandbox session log
yolo diff <name>                             Show changes Claude made
yolo apply <name>                            Copy changes back to original dirs
yolo exec <name> <command>                   Run a command inside the sandbox
yolo stop <name>                             Stop a sandbox (preserving state)
yolo start <name>                            Start a stopped sandbox
yolo restart <name>                          Restart Claude in an existing sandbox
yolo destroy <name>                          Stop and remove a sandbox
yolo build [profile|--all]                   Build/rebuild Docker image(s)
```

### `yolo new`

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
yolo new fix-build --prompt "fix the build" ./my-app:copy ./shared-lib ./common-types

# Live-edit primary, one dep also writable
yolo new my-task ./my-app:rw ./shared-lib:rw ./common-types

# Custom mount points for tools that need specific paths
yolo new my-task ./my-app:copy=/opt/myapp ./shared-lib=/usr/local/lib/shared ./common-types

# Error: primary has no write access
yolo new my-task ./my-app ./shared-lib
```

Name is required. If omitted, an error is shown with a helpful message suggesting a name based on the primary directory basename.

Example layout inside the container:

```
yolo new my-task ./my-app:copy ./shared-lib:rw ./common-types
```

```
/work/
├── my-app/          ← primary (copied, read-write, diff/apply available)
├── shared-lib/      ← dependency (bind-mounted, read-write, live)
└── common-types/    ← dependency (bind-mounted, read-only)
```

With custom mount points:

```
yolo new my-task ./my-app:copy=/opt/myapp ./shared-lib=/usr/local/lib/shared ./common-types
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
3. For each `:copy` directory:
   - If the directory is a git repo, record the current HEAD SHA in `meta.json`.
   - Copy to `~/.yolo/sandboxes/<name>/work/<dirname>/`. Error if directory size exceeds `max_copy_size` (default 1GB). Everything is copied regardless of `.gitignore` — the size limit is the guardrail.
   - If the copy already has a `.git/` directory (from the original repo), use the recorded SHA as the baseline — `yolo diff` will diff against it.
   - If the copy has no `.git/`, `git init` + `git add -A` + `git commit -m "initial"` to create a baseline. Note: `git add -A` naturally honors `.gitignore` if one is present, so copied-but-gitignored files (e.g., `node_modules`) won't clutter `yolo diff` output. These are separate concerns: what gets copied vs what gets tracked for diffs.
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

1. Generate a **sandbox context file** (placed in the primary directory as `.sandbox-context.md`) describing the environment for Claude: which directory is primary, which are dependencies, mount paths and access mode of each (read-only / read-write / copy), available tools, and how auto-save works. This file is excluded from the git baseline (added to `.gitignore` in synthetic repos) and excluded from `yolo apply`.
2. Start Docker container (as non-root user `yolo`) with:
   - `:copy` directories mounted from sandbox state at their mount point (custom or `/work/<dirname>`, read-write)
   - `:rw` directories bind-mounted at their mount point (custom or `/work/<dirname>`, read-write)
   - Default (no suffix) directories bind-mounted at their mount point (custom or `/work/<dirname>`, read-only)
   - `claude-state/` mounted at `/home/yolo/.claude` (read-write, per-sandbox)
   - Files listed in `claude_files` (from config) copied into `claude-state/` on first run
   - Config mounts from defaults + profile
   - Resource limits from defaults + profile
   - `ANTHROPIC_API_KEY` from host environment
   - Container name: `yolo-<name>`
   - User: `yolo` (UID/GID matching host user)
3. Run `setup` commands from config (if any).
4. Start tmux session named `main` with logging to `log.txt` (`tmux pipe-pane`).
5. Inside tmux: `cd <primary-mount-point> && claude --dangerously-skip-permissions [--model X]`
6. Wait ~3s for Claude to initialize.
7. If `prompt.txt` exists, feed it via `tmux load-buffer` + `tmux paste-buffer` + `tmux send-keys Enter Enter`.

### Prompt Mechanism

Claude always runs in interactive mode. When `--prompt` is provided:
1. The prompt is saved to `~/.yolo/sandboxes/<name>/prompt.txt`.
2. After Claude starts inside tmux, wait for Claude to be ready (~3s).
3. For long prompts, write to a temp file and use `tmux load-buffer` + `tmux paste-buffer` to avoid shell escaping issues.
4. Send via `tmux send-keys ... Enter Enter` (**double Enter** — Claude Code requires this to submit via tmux).
5. Claude begins working immediately but remains interactive — if it needs clarification, the question sits in the tmux session until you `yolo attach`.

Without `--prompt`, you get a normal interactive session waiting for input.

### `yolo attach`

Runs `docker exec -it yolo-<name> tmux attach -t main`.

Detach with standard tmux `Ctrl-b d` — container keeps running.

### `yolo status <name>`

Shows sandbox details from `meta.json`: profile, directories with their access modes (read-only / rw / copy), creation time, and whether Claude is still running (checks if the Claude process is alive inside the container).

### `yolo diff`

For `:copy` directories: runs `git diff` + `git status` against the baseline (the recorded HEAD SHA for existing repos, or the synthetic initial commit for non-git dirs). Shows exactly what Claude changed with proper diff formatting.

For `:rw` directories: if the original is a git repo, runs `git diff` against it. Otherwise, notes that diff is not available for live-mounted dirs without git.

Read-only directories are skipped (no changes possible).

### `yolo apply`

For `:copy` directories only. Generates a patch from the git diff in the sandbox copy and applies it to the original directory. This handles additions, modifications, and deletions. For dirs that had no original git repo, excludes the synthetic `.git/` directory created by yolo.

Performs a `--dry-run` first and shows a summary before prompting for confirmation.

`:rw` directories need no apply — changes are already live. Read-only directories have no changes.

### `yolo destroy`

`docker stop` + `docker rm` the container. Removes `~/.yolo/sandboxes/<name>/` entirely.

Asks for confirmation if Claude is still running.

### `yolo log`

`yolo log <name>` displays the session log (`log.txt`) for the named sandbox.

`yolo log -f <name>` tails the log in real time (like `tail -f`).

### `yolo exec`

`yolo exec <name> <command>` runs a command inside the sandbox container without attaching to tmux. Useful for debugging (`yolo exec my-sandbox bash`) or quick operations (`yolo exec my-sandbox npm install foo`).

Implemented as `docker exec -it yolo-<name> <command>`.

### `yolo stop` / `yolo start`

`yolo stop <name>` stops a sandbox container, preserving all state (work directory, claude-state, logs). The container can be restarted later without losing progress.

`yolo start <name>` starts a previously stopped sandbox. Re-enters the tmux session with Claude's state intact.

### `yolo restart`

`yolo restart <name>` restarts Claude inside an existing sandbox. Useful when Claude exits or crashes — relaunches Claude in the same tmux session with the same sandbox state, without destroying and recreating the container.

### Image Cleanup

Docker images (`yolo-base`, `yolo-<profile>`) accumulate indefinitely. A cleanup mechanism is needed but deferred pending research into Docker's image lifecycle: base images are shared parents of profile images, profile images may have running containers, layer caching means "removing" doesn't necessarily free space, and `docker image prune` vs `docker rmi` have different semantics. Half-baked pruning could break running sandboxes or nuke images the user spent time building.

## Directory Layout

```
~/.yolo/
├── config.yaml                  ← defaults + profile runtime config
├── profiles/
│   ├── go-dev/
│   │   └── Dockerfile           ← FROM yolo-base
│   └── node-dev/
│       └── Dockerfile
└── sandboxes/
    └── <name>/
        ├── meta.json            ← original paths, mode, profile, timestamps
        ├── prompt.txt           ← initial prompt (if provided)
        ├── log.txt              ← tmux session log
        ├── claude-state/        ← Claude's ~/.claude (per-sandbox, read-write)
        └── work/                ← copies of :copy dirs only
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

`yolo` with no arguments shows help/usage. `yolo init` performs first-time setup:
1. Create `~/.yolo/` directory structure.
2. Write a default `config.yaml` with sensible defaults.
3. Build the base image.
4. Print a quick-start guide.

## Security Considerations

- **Claude runs arbitrary code** inside the container: shell commands, file operations, network requests. The container provides isolation, not prevention.
- **All directories are read-only by default.** You explicitly opt in to write access per directory via `:rw` (live) or `:copy` (staged).
- **`:copy` directories** protect your originals. Changes only land when you explicitly `yolo apply`.
- **`:rw` directories** give Claude direct read/write access. Use only when you've committed your work or don't mind destructive changes. The tool warns if it detects uncommitted git changes.
- **API key exposure:** The `ANTHROPIC_API_KEY` is visible inside the container. Claude (or code Claude runs) could exfiltrate it. Use scoped API keys with spending limits where possible. Do not use sandboxes for untrusted workloads. A file-based approach (mount a file containing the key, read it in the entrypoint) would reduce casual exposure via `docker inspect` and process listing. Docker secrets are another option for Swarm deployments.
- **SSH keys:** If you mount `~/.ssh` into the container (even read-only), Claude can read private keys. Prefer SSH agent forwarding via config (`SSH_AUTH_SOCK` passthrough) over mounting key files directly.
- **Network access** is unrestricted by default (required for Claude API calls). Claude can download binaries, connect to external services, or exfiltrate code. Use `--network-isolated` to allow only Anthropic API traffic, `--network-allow <domain>` for finer control, or `--network-none` for full isolation.
- **Runs as non-root** inside the container (user `yolo` matching host UID/GID). This is required because Claude CLI refuses `--dangerously-skip-permissions` as root.
- **Dangerous directory detection:** The tool refuses to mount `$HOME`, `/`, or system directories unless `:force` is appended, preventing accidental exposure of your entire filesystem.
- **Security design needs dedicated research.** The security story around Docker + Claude (credential handling, network isolation, privilege escalation, exfiltration vectors) needs dedicated research into best practices before finalizing. The `setup` commands and `cap_add`/`devices` fields enable significant privilege escalation with insufficient documentation of risks. Ad-hoc mitigations risk missing the bigger picture.

## Resolved Design Decisions

1. ~~**Headless mode?**~~ Always interactive via tmux. `--prompt` feeds the first message via `tmux send-keys` so Claude starts immediately but can still ask questions.
2. ~~**Multiple mounts?**~~ Yes. First dir is primary (cwd), rest are dependencies. Error on container path collision.
3. ~~**Dotfiles/tools?**~~ Config file with defaults + profiles. Profiles use user-supplied Dockerfiles for full flexibility.
4. ~~**Resource limits?**~~ Configurable in `config.yaml` with sensible defaults.
5. ~~**Auto-destroy?**~~ No. Sandboxes persist until explicitly destroyed.
6. ~~**Git integration?**~~ Yes. Copy mode auto-inits git for clean diffs. `yolo apply` excludes `.git/`.
7. ~~**Default mode?**~~ All dirs read-only by default. Per-directory `:rw` (live) or `:copy` (staged) suffixes. Primary must have one of these.

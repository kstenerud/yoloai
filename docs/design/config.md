> **Design documents:** [Overview](README.md) | [Commands](commands.md) | [Setup](setup.md) | [Security](security.md) | [Research](../dev/RESEARCH.md)

## Components

### 1. Docker Images

**Base image (`yoloai-base`):** Based on Debian slim. Development-ready foundation with common tools, rebuilt occasionally. Debian slim over Ubuntu (smaller) or Alpine (musl incompatibilities with Node.js/npm).
- Common tools: tmux, git, build-essential, cmake, clang, python3, python3-pip, python3-venv, curl, wget, jq, ripgrep, fd-find, less, file, unzip, openssh-client, pkg-config, libssl-dev
- Go 1.24.1 (from official tarball)
- Rust via rustup (stable toolchain, system-wide install)
- golangci-lint
- **Claude Code:** Node.js 22 LTS + npm installation (`npm i -g @anthropic-ai/claude-code`) — npm required, not native binary (native binary bundles Bun which ignores proxy env vars, segfaults on Debian bookworm AMD64, and auto-updates). npm is deprecated but still published and is the only reliable Docker/proxy path. See [RESEARCH.md](../dev/RESEARCH.md) "Claude Code Installation Research"
- **[PLANNED] Codex:** Static Rust binary download (musl-linked, zero runtime dependencies, ~zero image bloat)
- **Non-root user** (`yoloai`, UID/GID matching host user via entrypoint). Image builds with a placeholder user (UID 1001). At container start, the entrypoint runs as root: reads `host_uid`/`host_gid` from `/yoloai/config.json` via `jq`, runs `usermod -u <uid> yoloai && groupmod -g <gid> yoloai` (exit code 12 means "can't update /etc/passwd" — if the UID already matches the desired UID, this is a no-op; otherwise log a warning and continue), fixes ownership on container-managed directories, then drops privileges via `gosu yoloai`. Uses `tini` as PID 1 (`--init` or explicit `ENTRYPOINT`). Images are portable across machines since UID/GID are set at run time, not build time. Claude Code refuses `--dangerously-skip-permissions` as root; Codex does not enforce this but convention is non-root
- **Entrypoint:** Default `Dockerfile.base`, `entrypoint.sh`, and `tmux.conf` are embedded in the binary via `go:embed`. On first run, these are seeded to `~/.yoloai/` if they don't exist. `yoloai build` always reads from `~/.yoloai/`, not from embedded copies. Users can edit for fast iteration without rebuilding yoloAI itself. The entrypoint reads all configuration from a bind-mounted `/yoloai/config.json` file containing `agent_command`, `startup_delay`, `ready_pattern`, `submit_sequence`, `tmux_conf`, `host_uid`, `host_gid`, and later `overlay_mounts`, `iptables_rules`, `setup_script`. No environment variables are used for configuration passing.

**[PLANNED] Profile images (`yoloai-<profile>`):** Derived from base, one per profile. Users supply a `Dockerfile` per profile with `FROM yoloai-base`. This avoids the limitations of auto-generating Dockerfiles from package lists and gives full flexibility (PPAs, tarballs, custom install steps).

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

Profile creation always seeds a Dockerfile. If the template doesn't provide one, `yoloai profile create` copies from the base image Dockerfile. Every profile has an explicit Dockerfile, preventing binary updates from silently changing behavior on existing profiles.

### 2. Config File (`~/.yoloai/config.yaml`)

```yaml
# Set to true after first-run experience completes. Do not edit manually.
setup_complete: false

# Always applied to every sandbox
defaults:
  backend: docker                       # Runtime backend: docker, tart, seatbelt
  # tart_image:                         # Custom base VM image (tart backend only)
  tmux_conf: default+host               # default+host | default | host | none (see setup.md)

  # --- Planned fields (not yet implemented) ---
  # agent: claude                        # [PLANNED] which agent to launch (claude, codex); CLI --agent overrides
  # profile: my-project                  # [PLANNED] default profile to use; CLI --profile overrides
  # agent_files: home                    # [PLANNED] files seeded into agent-state/ on first run
  # mounts:                              # [PLANNED] bind mounts added at container run time
  #   - ~/.gitconfig:/home/yoloai/.gitconfig:ro
  # copy_strategy: auto                  # [PLANNED] auto | overlay | full (currently full copy only)
  # auto_commit_interval: 0              # [PLANNED] seconds between auto-commits in :copy dirs; 0 = disabled
  # ports: []                            # [PLANNED] default port mappings; profile ports are additive
  # env: {}                              # [PLANNED] environment variables passed to container
  # network_isolated: false              # [PLANNED] true to enable network isolation by default
  # network_allow: []                    # [PLANNED] additional domains to allow (additive with agent defaults)
  # resources:                           # [PLANNED] container resource limits
  #   cpus: 4                            # docker --cpus
  #   memory: 8g                         # docker --memory
```

Settings are managed via `yoloai config get/set` or by editing `~/.yoloai/config.yaml` directly.

`config.yaml` contains only `defaults` — settings applied to every sandbox. Profile-specific configuration lives in separate `profile.yaml` files (see [Profiles](#3-planned-profiles)).

**Implemented settings:**

- `defaults.backend` selects the runtime backend. Valid values: `docker`, `tart`, `seatbelt`. CLI `--backend` overrides config.
- `defaults.tart_image` overrides the base VM image for the tart backend.
- `defaults.tmux_conf` controls how user tmux config interacts with the container. Set by the interactive first-run setup. Values: `default+host`, `default`, `host`, `none` (see [setup.md](setup.md#tmux-configuration)).

**Planned settings (not yet parsed from config):**

- `defaults.agent` will select the agent to launch. Currently the `--agent` flag defaults to `claude` in the CLI.
- `defaults.agent_files` will control what files are copied into the sandbox's `agent-state/` directory on first run. Set to `home` to copy from the agent's default state directory (`~/.claude/` for Claude, `~/.codex/` for Codex). Set to a list of paths (`~/` or absolute) for deterministic setups. Relative paths without `~/` are an error. Omit entirely to copy nothing (safe default). Profile `agent_files` **replaces** (not merges with) defaults.
- `defaults.mounts` will be bind mounts added at container run time. Profile mounts are **additive** (merged with defaults, no deduplication — duplicates are a user error).
- `defaults.ports` will be default port mappings. Profile ports are additive.
- `defaults.resources` will set baseline limits. Profiles can override individual values.
- `defaults.env` will set environment variables passed to the container via `docker run -e`. Profile `env` is merged with defaults (profile values win on conflict). Note: API keys (e.g., `ANTHROPIC_API_KEY`, `CODEX_API_KEY`) are injected via file-based bind mount, not `env` — see [security.md](security.md#credential-management).
- `defaults.network_isolated` will enable network isolation for all sandboxes. Profile can override. CLI `--network-isolated` flag overrides config.
- `defaults.network_allow` will list additional allowed domains. Non-empty `network_allow` implies `network_isolated: true`. Profile `network_allow` is additive with defaults. CLI `--network-allow` is additive with config.

#### [PLANNED] Recipes (advanced)

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

**[PLANNED] Environment variable interpolation in config files:** Config values (in both `config.yaml` and `profile.yaml`) will support `${VAR}` interpolation from the host environment. Only the braced syntax `${VAR}` is recognized — bare `$VAR` is **not** interpolated and treated as literal text. This avoids the class of bugs where `$` in passwords, regex patterns, or shell strings is silently misinterpreted (a well-documented pain point with Docker Compose's unbraced `$VAR` support — see [RESEARCH.md](../dev/RESEARCH.md)). Interpolation is applied after YAML parsing, so expanded values cannot break YAML syntax. Unset variables produce an error at sandbox creation time (fail-fast, not silent empty string). Note: CLI path inputs already support `${VAR}` interpolation and `~/` expansion — this planned work extends that to config file values.

**[PLANNED] Tilde expansion in config files:** `~/` will be expanded to `$HOME` in all path-valued fields across both `config.yaml` and `profile.yaml` (`agent_files`, `mounts`, `workdir.path`, `directories[].path`). Bare relative paths (without `~/`) are an error in all path fields. Note: CLI path inputs already support `~/` expansion — this planned work extends that to config file values.

### 3. [PLANNED] Profiles

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
  # mount: /opt/myapp                   # optional custom mount point (default: mirrors host path)
directories:
  - path: /home/user/shared-lib
    mode: rw
    mount: /usr/local/lib/shared        # custom mount point (default: mirrors host path)
  - path: /home/user/common-types
    # default: read-only
agent_files:
  - ~/.claude/CLAUDE.md                    # ~ expands to $HOME
  - /shared/configs/claude-settings.json   # absolute path for team setups
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

| Field                  | Merge behavior                                           |
|------------------------|----------------------------------------------------------|
| `agent`                | Profile overrides default. CLI `--agent` overrides both. |
| `profile`              | Defaults provide fallback. CLI `--profile` overrides. `--no-profile` uses base image. |
| `agent_files`          | Profile replaces defaults (no merge)                     |
| `mounts`               | Additive (no deduplication — duplicates are user error)  |
| `resources`            | Profile overrides individual values                      |
| `ports`                | Additive                                                 |
| `env`                  | Merged (profile wins on conflict)                        |
| `cap_add`              | Additive                                                 |
| `devices`              | Additive                                                 |
| `setup`                | Additive (defaults first, then profile)                  |
| `network_isolated`     | Profile overrides default. CLI overrides profile.        |
| `network_allow`        | Additive                                                 |
| `copy_strategy`        | Profile overrides default                                |
| `auto_commit_interval` | Profile overrides default                                |
| `workdir`              | Profile provides default, CLI replaces                   |
| `directories`          | Profile provides defaults, CLI `-d` is additive          |

**`yoloai profile` commands:**

- `yoloai profile create <name> [--template <tpl>]` — Create a profile directory with a scaffold Dockerfile and minimal `profile.yaml`. Templates: `base` (default), `go`, `node`, `python`, `rust`. Creates pre-filled files tailored to the language/stack.
- `yoloai profile list` — List all profiles in `~/.yoloai/profiles/`.
- `yoloai profile delete <name>` — Delete a profile directory. Asks for confirmation if any sandbox references the profile.


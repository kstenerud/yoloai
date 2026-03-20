> **Design documents:** [Overview](README.md) | [Commands](commands.md) | [Setup](setup.md) | [Security](security.md) | [Research](../dev/RESEARCH.md) | [research/](../dev/research/)

## Components

### 1. Docker Images

**Base image (`yoloai-base`):** Based on Debian slim. Development-ready foundation with common tools, rebuilt occasionally. Debian slim over Ubuntu (smaller) or Alpine (musl incompatibilities with Node.js/npm).
- Common tools: tmux, git, build-essential, cmake, clang, python3, python3-pip, python3-venv, curl, wget, jq, ripgrep, fd-find, less, file, unzip, openssh-client, pkg-config, libssl-dev
- Go 1.24.1 (from official tarball)
- Rust via rustup (stable toolchain, system-wide install)
- golangci-lint
- **Claude Code:** Node.js 22 LTS + npm installation (`npm i -g @anthropic-ai/claude-code`) — npm required, not native binary (native binary bundles Bun which ignores proxy env vars, segfaults on Debian bookworm AMD64, and auto-updates). npm is deprecated but still published and is the only reliable Docker/proxy path. See [Implementation Research](../dev/research/implementation.md) "Claude Code Installation Research"
- **Codex:** Node.js npm package (`npm i -g @openai/codex`) — shares Node.js runtime already installed for Claude/Gemini
- **Non-root user** (`yoloai`, UID/GID matching host user via entrypoint). Image builds with a placeholder user (UID 1001). At container start, the entrypoint runs as root: reads `host_uid`/`host_gid` from `/yoloai/config.json` via `jq`, runs `usermod -u <uid> yoloai && groupmod -g <gid> yoloai` (exit code 12 means "can't update /etc/passwd" — if the UID already matches the desired UID, this is a no-op; otherwise log a warning and continue), fixes ownership on container-managed directories, then drops privileges via `gosu yoloai`. Uses `tini` as PID 1 (`--init` or explicit `ENTRYPOINT`). Images are portable across machines since UID/GID are set at run time, not build time. Claude Code refuses `--dangerously-skip-permissions` as root; Codex does not enforce this but convention is non-root
- **Entrypoint:** The Dockerfile, entrypoint scripts (`entrypoint.sh`, `entrypoint.py`, `sandbox-setup.py`, `status-monitor.py`), and default `tmux.conf` are embedded in the binary via `go:embed`. These are baked-in defaults — they are not written to disk and are not user-editable. The entrypoint reads all configuration from a bind-mounted `/yoloai/config.json` file containing `agent_command`, `startup_delay`, `ready_pattern`, `submit_sequence`, `tmux_conf`, `host_uid`, `host_gid`, and later `overlay_mounts`, `iptables_rules`, `setup_script`. No environment variables are used for configuration passing.

**Profile images (`yoloai-<profile>`):** One per profile. Profile Dockerfiles must use `FROM yoloai-base`. The profile layers additional software and configuration on top while the yoloai runtime (entrypoint scripts, status monitor, sandbox setup) remains the baked-in version. Profiles without a Dockerfile use `yoloai-base` directly.

```
~/.yoloai/
├── defaults/
│   ├── config.yaml      ← user defaults (agent, model, isolation, etc.)
│   └── tmux.conf        ← optional; overrides baked-in default
└── profiles/
    ├── go-dev/
    │   ├── config.yaml  ← profile settings (merged over baked-in defaults)
    │   ├── Dockerfile   ← FROM yoloai-base; RUN apt-get install ...
    │   └── tmux.conf    ← optional
    └── node-dev/
        ├── config.yaml
        └── Dockerfile
```

**Auto-build on demand:** `yoloai new --profile <name>` automatically builds any missing or stale images before creating the sandbox. If `yoloai-base` doesn't exist, it is built first. If the profile has a Dockerfile and `yoloai-<profile>` doesn't exist or is older than `yoloai-base`, it is rebuilt. Profiles without a Dockerfile skip this step and use `yoloai-base`. Only the images needed for *this* sandbox are built — other profiles are untouched. This eliminates the need for users to manually run `yoloai build` before first use.

**Explicit rebuild:** `yoloai system build` with no arguments rebuilds the base image. `yoloai system build <profile>` rebuilds that profile's image (building base first if stale; error if profile has no Dockerfile). `yoloai system build --all` rebuilds everything (base first, then all profiles with Dockerfiles). Use explicit rebuild after editing a Dockerfile to pick up changes without creating a new sandbox.

### 2. Config Files

There are three layers of configuration, applied in order:

1. **Baked-in defaults** — embedded in the binary. Not user-editable. Define the baseline for all sandboxes and profiles.
2. **User defaults (`~/.yoloai/defaults/config.yaml`)** — personal settings applied on top of baked-in defaults. Only active when `--profile` is not specified. Managed via `yoloai config get/set`.
3. **Profile config (`~/.yoloai/profiles/<name>/config.yaml`)** — profile-specific settings, merged over baked-in defaults only. User defaults do not apply when a profile is active. See [Profiles](#3-profiles).

**Global config (`~/.yoloai/config.yaml`)** — user preferences that apply regardless of whether a profile is active:

```yaml
tmux_conf: default+host               # default+host | default | host | none (see setup.md)
# model_aliases:                       # Custom model alias overrides
#   fast: claude-haiku-4-latest
```

**User defaults (`~/.yoloai/defaults/config.yaml`)** — active only when `--profile` is not given:

```yaml
# container_backend: docker           # Container backend preference: docker, podman (applies to --isolation container/container-enhanced only)
# tart:                               # Tart backend settings
#   image:                            # Custom base VM image

agent: claude                         # Agent to launch: aider, claude, codex, gemini, opencode; CLI --agent overrides
# model:                              # Model name or alias; CLI --model overrides

# agent_files: "${HOME}"              # string: base dir (agent subdir appended); list: specific files
# mounts:                             # bind mounts added at container run time
#   - ~/.gitconfig:/home/yoloai/.gitconfig:ro
# auto_commit_interval: 0             # seconds between auto-commits in :copy dirs; 0 = disabled
# ports: []                           # default port mappings
env: {}                               # Environment variables forwarded to container via /run/secrets/
# agent_args:                         # Per-agent default CLI args (inserted before -- passthrough)
#   aider: "--no-auto-commits --no-pretty"
#   claude: "--allowedTools '*'"
# network:                            # Network isolation settings
#   isolated: false                   # true to enable network isolation by default
#   allow: []                         # additional domains to allow (additive with agent defaults)
# resources:                          # Container resource limits
#   cpus: 4                           # docker --cpus
#   memory: 8g                        # docker --memory
```

Settings are managed via `yoloai config get/set` or by editing the file directly.

**Implemented settings:**

- `container_backend` selects the preferred container backend when `--isolation container` or `container-enhanced` is in effect. Valid values: `docker`, `podman`. Only applies to container isolation modes — `vm`, `vm-enhanced`, and `--os mac` always auto-select their backends (`containerd`/`tart`/`seatbelt`) regardless of this setting. CLI `--backend` overrides config.
- `tart.image` overrides the base VM image for the tart backend.
- `tmux_conf` (global config) controls how user tmux config interacts with the container. Set by the interactive first-run setup. Values: `default+host`, `default`, `host`, `none` (see [setup.md](setup.md#tmux-configuration)).
- `agent` selects the agent to launch. Valid values: `aider`, `claude`, `codex`, `gemini`, `opencode`. CLI `--agent` overrides config.
- `model` sets the model name or alias passed to the agent. Empty means the agent uses its own default. CLI `--model` overrides config.
- `env` sets environment variables forwarded to the container. Values are written as files in `/run/secrets/` (same mechanism as API keys). API keys take precedence if a name conflicts. Supports `${VAR}` expansion. Set via `yoloai config set env.NAME value`. In profiles, `env` merges with baked-in defaults (profile values win on conflict).
- `agent_args` sets per-agent default CLI args. Map of agent name → arg string. Args are inserted between the model flag and CLI passthrough (`--` args), so passthrough always wins. Set via `yoloai config set agent_args.aider "--no-auto-commits"`. In profiles, `agent_args` merges with baked-in defaults (profile values win on conflict per agent key).
- `resources` sets container resource limits. `resources.cpus` (e.g., `"4"`, `"2.5"`) maps to `--cpus`. `resources.memory` (e.g., `"8g"`, `"512m"`) maps to `--memory`. CLI `--cpus` and `--memory` override config. Profile overrides individual values.
- `network` controls network isolation. `network.isolated: true` enables network isolation for all sandboxes. `network.allow` lists additional allowed domains (additive with agent defaults). Non-empty `network.allow` implies `network.isolated: true`. CLI `--network-isolated` and `--network-allow` override config.
- `mounts` specifies bind mounts added at container run time (e.g., `~/.gitconfig:/home/yoloai/.gitconfig:ro`). In profiles, mounts are additive (merged with baked-in defaults).
- `auto_commit_interval` sets the interval in seconds between automatic git commits in `:copy` directories inside the container. Disabled by default (`0`). When enabled, a background loop periodically runs `git add -A && git commit` in each `:copy` directory, providing recovery checkpoints for unattended runs. Only affects `:copy` dirs (`:overlay` has its own mechanism; `:rw` is the user's live repo). Profile overrides baked-in default.
- `agent_files` controls what files are copied into the sandbox's `agent-state/` directory on first run (see below).

Agents may define `AuthHintEnvVars` — environment variables that indicate authentication is configured through a non-API-key mechanism (e.g. local model server). When any of these vars are set (in host env or `env`), the auth check passes without requiring a cloud API key.

**Use case: local models with Aider.** Aider can use local model servers (Ollama, LM Studio, vLLM) via environment variables. Example:

```yaml
env:
  OLLAMA_API_BASE: http://host.docker.internal:11434
```

A "local-models" profile can bundle env, network config, and Dockerfile additions for a turnkey local-model setup.

**`agent_files`** controls what files are copied into the sandbox's `agent-state/` directory on first run. Two forms: **string** — a base directory from which yoloai derives the agent-specific subdir (e.g. `"${HOME}"` → `~/.claude/` for Claude, `~/.gemini/` for Gemini; `"/shared/team-configs"` → `/shared/team-configs/.claude/` for Claude). **list** — specific files or directories to copy in verbatim (e.g. `["~/.claude/settings.json", "/shared/CLAUDE.md"]`). Omit entirely to copy nothing (safe default). Profile `agent_files` **replaces** the baked-in default entirely (not additive). Files placed by SeedFiles (auth credentials, settings) are never overwritten. Each agent defines exclusion patterns for session data and caches. First-run status is tracked in `state.json` (`agent_files_initialized`); `reset --clean` resets the flag so files are re-seeded on next start.

**Planned settings (not yet parsed from config):**
- (none currently — all designed config fields are implemented)

#### Recipes (advanced)

For advanced setups like Tailscale inside containers:

```yaml
# Example: add to defaults/config.yaml or a profile's config.yaml
cap_add:
  - NET_ADMIN
devices:
  - /dev/net/tun
setup:                              # runs at container start
  - tailscale up --authkey=${TAILSCALE_AUTHKEY}
```

`cap_add`, `devices`, and `setup` are available in both user defaults and profiles but not shown in the default config. `setup` commands run at container start before the agent launches. Within a profile, lists are additive (merged over baked-in defaults).

**Environment variable interpolation in config files:** Config values in all `config.yaml` files (user defaults and profiles) support `${VAR}` interpolation from the host environment. Only the braced syntax `${VAR}` is recognized — bare `$VAR` is **not** interpolated and treated as literal text. This avoids the class of bugs where `$` in passwords, regex patterns, or shell strings is silently misinterpreted (a well-documented pain point with Docker Compose's unbraced `$VAR` support — see [Implementation Research](../dev/research/implementation.md)). Interpolation is applied after YAML parsing, so expanded values cannot break YAML syntax. Unset variables produce an error (fail-fast, not silent empty string). CLI path inputs also support `${VAR}` interpolation and `~/` expansion.

**Tilde expansion in config files:** `~/` is expanded to `$HOME` in path-valued fields across all config files (`agent_files`, `mounts`, `workdir.path`, `directories[].path`). CLI path inputs also support `~/` expansion.

### 3. Profiles

Profiles live in `~/.yoloai/profiles/<name>/` and are always selected explicitly via `--profile <name>` on `yoloai new`. There is no default profile setting — omitting `--profile` uses user defaults (`~/.yoloai/defaults/`) instead.

```
~/.yoloai/profiles/<name>/
├── config.yaml   ← profile settings (all optional; merged over baked-in defaults)
├── Dockerfile    ← optional; must use FROM yoloai-base
└── tmux.conf     ← optional; replaces baked-in default
```

**Profiles are self-contained.** Profile config merges over baked-in defaults only — user defaults (`~/.yoloai/defaults/config.yaml`) do not apply when a profile is active. This makes profiles fully deterministic: their behavior is the same regardless of who runs them or what their personal defaults are.

**File resolution.** For each file (Dockerfile, tmux.conf), the profile directory is checked first; if absent, the baked-in default is used. For `config.yaml`, the baked-in default config is loaded first, then the profile's config.yaml is merged on top.

**Name validation:** Profile names must match `^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`, max 56 characters. Profile names become Docker image tags (`yoloai-<profile>`), so the character restrictions ensure compatibility with Docker's naming rules.

**Implemented profile fields:** `agent`, `model`, `container_backend`, `tart.image`, `env`, `agent_args`, `agent_files`, `ports`, `workdir`, `directories`, `resources`, `network`, `mounts`, `isolation`, `cap_add`, `devices`, `setup`, `auto_commit_interval`. Unknown fields are silently ignored.

**Backend handling:**
- `container_backend` — optional preference. Only meaningful for `--isolation container` or `container-enhanced`; ignored for `vm`, `vm-enhanced`, and `--os mac`.
- `Dockerfile` — optional. Used with Docker and Podman backends to build a `yoloai-<profile>` image. Must use `FROM yoloai-base`. Ignored with Tart and Seatbelt backends. When absent, Docker/Podman backends use `yoloai-base`.
- `tart.image` — optional. Used only with the Tart backend. Ignored with other backends.

**Sandbox metadata:** When a profile is used, `meta.json` records the profile name and the resolved image ref. Lifecycle commands use the stored image ref — profile changes only take effect on new sandboxes.

**Profile image building:** The sandbox manager calls `Runtime.EnsureImage()` for the base image, then uses container-backend build logic for profile images when Docker or Podman is active and the profile has a Dockerfile. Tart and Seatbelt skip profile image building.

**Profile image staleness:** A profile image is considered stale when: (a) it doesn't exist, (b) the profile's Dockerfile has changed since last build (checksum-tracked), or (c) `yoloai-base` has been rebuilt since the profile image was last built. Stale images are automatically rebuilt during `yoloai new --profile`.

**`config.yaml` format:**

```yaml
# ~/.yoloai/profiles/my-project/config.yaml

# --- All fields optional; merged over baked-in defaults ---
# container_backend: docker               # preferred container backend; only applies to container/container-enhanced isolation
agent: claude                             # override agent
# model: sonnet                           # override model
# tart:
#   image: my-custom-vm                   # custom VM image (tart only)
ports:
  - "8080:8080"
env:
  GOMODCACHE: /home/yoloai/go/pkg/mod
# agent_args:
#   aider: "--no-auto-commits"
# agent_files: "${HOME}"                  # string: base dir (agent subdir appended)
# agent_files:                            # list: specific files/dirs
#   - ~/.claude/settings.json
#   - /shared/configs/CLAUDE.md
# mounts:
#   - ~/.ssh:/home/yoloai/.ssh:ro
resources:
  cpus: "4"
  memory: 16g
# cap_add:
#   - NET_ADMIN
# devices:
#   - /dev/net/tun
# setup:
#   - tailscale up --authkey=${TAILSCALE_AUTHKEY}
# network:
#   isolated: true
#   allow:
#     - api.example.com
# auto_commit_interval: 300
# isolation: vm

# --- Profile-specific fields ---
workdir:
  path: /home/user/my-app
  mode: copy                              # copy, overlay, or rw
  # mount: /opt/myapp                     # optional custom mount point
directories:
  - path: /home/user/shared-lib
    mode: rw
    mount: /usr/local/lib/shared
  - path: /home/user/common-types
    # default: read-only
```

CLI workdir **replaces** profile workdir. CLI `-d` dirs are **additive** with profile dirs.

**Merge rule:** Baked-in defaults → profile config.yaml → CLI flags. Scalars override. Lists are additive. Maps are merged (profile wins on conflict). Exception: `agent_files` replaces entirely.

| Field                  | Merge behavior                                                                        |
|------------------------|---------------------------------------------------------------------------------------|
| `container_backend`    | Profile overrides baked-in. Only applies to `container`/`container-enhanced`. CLI `--backend` overrides. |
| `agent`                | Profile overrides baked-in. CLI `--agent` overrides.                                 |
| `model`                | Profile overrides baked-in. CLI `--model` overrides.                                 |
| `isolation`            | Profile overrides baked-in. CLI `--isolation` overrides.                             |
| `tart.image`           | Profile overrides baked-in.                                                           |
| `ports`                | Additive                                                                              |
| `env`                  | Merged (profile wins on conflict)                                                     |
| `workdir`              | Profile provides default, CLI replaces                                                |
| `directories`          | Profile provides defaults, CLI `-d` is additive                                       |
| `agent_files`          | Profile **replaces** baked-in (no merge)                                              |
| `mounts`               | Additive                                                                              |
| `resources`            | Profile overrides individual values                                                   |
| `cap_add`              | Additive                                                                              |
| `devices`              | Additive                                                                              |
| `setup`                | Additive                                                                              |
| `network.isolated`     | Profile overrides baked-in. CLI overrides profile.                                    |
| `network.allow`        | Additive                                                                              |
| `auto_commit_interval` | Profile overrides baked-in                                                            |

**`yoloai profile` commands:**

- `yoloai profile create <name>` — Create a profile directory with a scaffold `config.yaml` containing commented-out examples of all supported fields. [DEFERRED] `--template <tpl>` flag with language-specific scaffolds (`go`, `node`, `python`, `rust`).
- `yoloai profile list` — List all profiles in `~/.yoloai/profiles/`.
- `yoloai profile delete <name>` — Delete a profile directory. Asks for confirmation if any sandbox references the profile.


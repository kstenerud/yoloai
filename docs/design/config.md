> **Design documents:** [Overview](README.md) | [Commands](commands.md) | [Setup](setup.md) | [Security](security.md) | [Research](../dev/RESEARCH.md)

## Components

### 1. Docker Images

**Base image (`yoloai-base`):** Based on Debian slim. Development-ready foundation with common tools, rebuilt occasionally. Debian slim over Ubuntu (smaller) or Alpine (musl incompatibilities with Node.js/npm).
- Common tools: tmux, git, build-essential, cmake, clang, python3, python3-pip, python3-venv, curl, wget, jq, ripgrep, fd-find, less, file, unzip, openssh-client, pkg-config, libssl-dev
- Go 1.24.1 (from official tarball)
- Rust via rustup (stable toolchain, system-wide install)
- golangci-lint
- **Claude Code:** Node.js 22 LTS + npm installation (`npm i -g @anthropic-ai/claude-code`) — npm required, not native binary (native binary bundles Bun which ignores proxy env vars, segfaults on Debian bookworm AMD64, and auto-updates). npm is deprecated but still published and is the only reliable Docker/proxy path. See [RESEARCH.md](../dev/RESEARCH.md) "Claude Code Installation Research"
- **Codex:** Node.js npm package (`npm i -g @openai/codex`) — shares Node.js runtime already installed for Claude/Gemini
- **Non-root user** (`yoloai`, UID/GID matching host user via entrypoint). Image builds with a placeholder user (UID 1001). At container start, the entrypoint runs as root: reads `host_uid`/`host_gid` from `/yoloai/config.json` via `jq`, runs `usermod -u <uid> yoloai && groupmod -g <gid> yoloai` (exit code 12 means "can't update /etc/passwd" — if the UID already matches the desired UID, this is a no-op; otherwise log a warning and continue), fixes ownership on container-managed directories, then drops privileges via `gosu yoloai`. Uses `tini` as PID 1 (`--init` or explicit `ENTRYPOINT`). Images are portable across machines since UID/GID are set at run time, not build time. Claude Code refuses `--dangerously-skip-permissions` as root; Codex does not enforce this but convention is non-root
- **Entrypoint:** Default `Dockerfile`, `entrypoint.sh`, and `tmux.conf` are embedded in the binary via `go:embed`. On first run, these are seeded to `~/.yoloai/profiles/base/` if they don't exist. `yoloai build` always reads from `~/.yoloai/profiles/base/`, not from embedded copies. Users can edit for fast iteration without rebuilding yoloAI itself. The entrypoint reads all configuration from a bind-mounted `/yoloai/config.json` file containing `agent_command`, `startup_delay`, `ready_pattern`, `submit_sequence`, `tmux_conf`, `host_uid`, `host_gid`, and later `overlay_mounts`, `iptables_rules`, `setup_script`. No environment variables are used for configuration passing.

**Profile images (`yoloai-<profile>`):** Derived from base, one per profile. Users supply an optional `Dockerfile` per profile with `FROM yoloai-base`. This avoids the limitations of auto-generating Dockerfiles from package lists and gives full flexibility (PPAs, tarballs, custom install steps). Profiles without a Dockerfile use `yoloai-base` directly.

```
~/.yoloai/profiles/
├── base/
│   ├── config.yaml      ← global defaults (flat keys, no defaults: nesting)
│   ├── Dockerfile       ← seeded from embedded defaults, user-editable
│   ├── entrypoint.sh    ← seeded from embedded defaults, user-editable
│   ├── tmux.conf        ← seeded from embedded defaults, user-editable
│   └── .checksums       ← tracks seeded file checksums
├── go-dev/
│   ├── profile.yaml     ← runtime config (env, ports, workdir, directories, etc.)
│   └── Dockerfile       ← optional; FROM yoloai-base; RUN apt-get install ...
└── node-dev/
    └── profile.yaml     ← runtime-only profile (no custom image)
```

**Auto-build on demand:** `yoloai new --profile <name>` automatically builds any missing or stale images before creating the sandbox. If `yoloai-base` doesn't exist, it is built first. If the profile has a Dockerfile and `yoloai-<profile>` doesn't exist or is older than `yoloai-base`, it is rebuilt. Profiles without a Dockerfile skip this step and use `yoloai-base`. Only the images needed for *this* sandbox are built — other profiles are untouched. This eliminates the need for users to manually run `yoloai build` before first use.

**Explicit rebuild:** `yoloai system build` with no arguments rebuilds the base image. `yoloai system build <profile>` rebuilds that profile's image (building base first if stale; error if profile has no Dockerfile). `yoloai system build --all` rebuilds everything (base first, then all profiles with Dockerfiles). Use explicit rebuild after editing a Dockerfile to pick up changes without creating a new sandbox.

### 2. Config File (`~/.yoloai/profiles/base/config.yaml`)

```yaml
# Always applied to every sandbox
backend: docker                       # Runtime backend: docker, tart, seatbelt
# tart:                               # Tart backend settings
#   image:                            # Custom base VM image
tmux_conf: default+host               # default+host | default | host | none (see setup.md)

agent: claude                           # Agent to launch: aider, claude, codex, gemini, opencode; CLI --agent overrides
# model:                               # Model name or alias; CLI --model overrides

# --- Planned fields (not yet implemented) ---
# profile: my-project                  # [PLANNED] default profile to use; CLI --profile overrides
# agent_files: home                    # [PLANNED] files seeded into agent-state/ on first run
# mounts:                              # [PLANNED] bind mounts added at container run time
#   - ~/.gitconfig:/home/yoloai/.gitconfig:ro
# copy_strategy: auto                  # [PLANNED] auto | overlay | full (currently full copy only)
# auto_commit_interval: 0              # [PLANNED] seconds between auto-commits in :copy dirs; 0 = disabled
# ports: []                            # [PLANNED] default port mappings; profile ports are additive
env: {}                                # Environment variables forwarded to container via /run/secrets/
# network_isolated: false              # [PLANNED] true to enable network isolation by default
# network_allow: []                    # [PLANNED] additional domains to allow (additive with agent defaults)
# resources:                           # [PLANNED] container resource limits
#   cpus: 4                            # docker --cpus
#   memory: 8g                         # docker --memory
```

Settings are managed via `yoloai config get/set` or by editing `~/.yoloai/profiles/base/config.yaml` directly.

`config.yaml` contains default settings applied to every sandbox. Profile-specific configuration lives in separate `profile.yaml` files (see [Profiles](#3-planned-profiles)).

**Implemented settings:**

- `backend` selects the runtime backend. Valid values: `docker`, `tart`, `seatbelt`. CLI `--backend` overrides config.
- `tart.image` overrides the base VM image for the tart backend.
- `tmux_conf` controls how user tmux config interacts with the container. Set by the interactive first-run setup. Values: `default+host`, `default`, `host`, `none` (see [setup.md](setup.md#tmux-configuration)).
- `agent` selects the agent to launch. Valid values: `aider`, `claude`, `codex`, `gemini`, `opencode`. CLI `--agent` overrides config.
- `model` sets the model name or alias passed to the agent. Empty means the agent uses its own default. CLI `--model` overrides config.
- `env` sets environment variables forwarded to the container. Values are written as files in `/run/secrets/` (same mechanism as API keys). API keys take precedence if a name conflicts. Supports `${VAR}` expansion. Set via `yoloai config set env.NAME value`. Profile `env` merges with defaults (profile values win on conflict).

Agents may define `AuthHintEnvVars` — environment variables that indicate authentication is configured through a non-API-key mechanism (e.g. local model server). When any of these vars are set (in host env or `env`), the auth check passes without requiring a cloud API key.

**Use case: local models with Aider.** Aider can use local model servers (Ollama, LM Studio, vLLM) via environment variables. Example:

```yaml
env:
  OLLAMA_API_BASE: http://host.docker.internal:11434
```

With profiles (future), a "local-models" profile can bundle env, network config, and Dockerfile additions for a turnkey local-model setup.

**Planned settings (not yet parsed from config):**
- `agent_files` will control what files are copied into the sandbox's `agent-state/` directory on first run. Set to `home` to copy from the agent's default state directory (`~/.claude/` for Claude, `~/.codex/` for Codex). Set to a list of paths (`~/` or absolute) for deterministic setups. Relative paths without `~/` are an error. Omit entirely to copy nothing (safe default). Profile `agent_files` **replaces** (not merges with) defaults.
- `mounts` will be bind mounts added at container run time. Profile mounts are **additive** (merged with defaults, no deduplication — duplicates are a user error).
- `ports` will be default port mappings. Profile ports are additive.
- `resources` will set baseline limits. Profiles can override individual values.
- `network_isolated` will enable network isolation for all sandboxes. Profile can override. CLI `--network-isolated` flag overrides config.
- `network_allow` will list additional allowed domains. Non-empty `network_allow` implies `network_isolated: true`. Profile `network_allow` is additive with defaults. CLI `--network-allow` is additive with config.

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

**Environment variable interpolation in config files:** Config values in `config.yaml` (and future `profile.yaml`) support `${VAR}` interpolation from the host environment. Only the braced syntax `${VAR}` is recognized — bare `$VAR` is **not** interpolated and treated as literal text. This avoids the class of bugs where `$` in passwords, regex patterns, or shell strings is silently misinterpreted (a well-documented pain point with Docker Compose's unbraced `$VAR` support — see [RESEARCH.md](../dev/RESEARCH.md)). Interpolation is applied after YAML parsing, so expanded values cannot break YAML syntax. Unset variables produce an error (fail-fast, not silent empty string). CLI path inputs also support `${VAR}` interpolation and `~/` expansion.

**[PLANNED] Tilde expansion in config files:** `~/` will be expanded to `$HOME` in path-valued fields across both `config.yaml` and `profile.yaml` (`agent_files`, `mounts`, `workdir.path`, `directories[].path`). Bare relative paths (without `~/`) are an error in all path fields. Currently no path-valued fields are parsed from config — tilde expansion will be added alongside those fields. CLI path inputs already support `~/` expansion.

### 3. Profiles

Profiles live in `~/.yoloai/profiles/<name>/`, containing a `profile.yaml` and optionally a `Dockerfile`:

```
~/.yoloai/profiles/<name>/
├── profile.yaml      ← runtime config (edit directly — no CLI config command)
└── Dockerfile        ← optional; FROM yoloai-base (Docker backend only)
```

**Name validation:** Profile names follow the same rules as sandbox names — must match `^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`, max 56 characters. The name `base` is reserved and cannot be used for user profiles. Profile names become Docker image tags (`yoloai-<profile>`), so the character restrictions ensure compatibility with Docker's naming rules.

**Profile.yaml mirrors config.yaml.** The profile format uses the same field names and structure as `config.yaml`, plus profile-specific fields (`workdir`, `directories`). Users learn one config format. Backend-specific fields (`backend`, `tart.image`, Dockerfile) are optional — omit them for backend-agnostic profiles.

**Implemented profile fields:** `agent`, `model`, `backend`, `tart.image`, `env`, `ports`, `workdir`, `directories`. Other config.yaml fields (`agent_files`, `mounts`, `resources`, etc.) will be supported in profile.yaml as they are implemented. Unknown fields are silently ignored — profiles written for future versions won't break on older ones.

**Backend handling:**
- `backend` in profile — optional constraint. If set, error when the user's backend doesn't match. If omitted, the profile works with any backend.
- `Dockerfile` in profile dir — optional. Used only with the Docker backend to build a `yoloai-<profile>` image. Ignored with Tart and Seatbelt backends. When absent, the Docker backend uses `yoloai-base`.
- `tart.image` in profile — optional. Used only with the Tart backend to specify a custom VM image. Ignored with other backends.

**Sandbox metadata:** When a profile is used, `meta.json` records the profile name and the resolved image ref (`yoloai-<profile>` or `yoloai-base`). Lifecycle commands (`start`, `reset`, `restart`) use the stored image ref to recreate containers correctly — they do not re-resolve the profile. This means profile changes (Dockerfile or yaml) only take effect on new sandboxes, not existing ones.

**Profile image building:** Profile image building is Docker-specific — handled outside the backend-agnostic `Runtime` interface. The sandbox manager calls `Runtime.EnsureImage()` for the base image as before, then uses Docker-specific build logic for profile images when the Docker backend is active and the profile has a Dockerfile. Tart and Seatbelt backends skip profile image building entirely (Tart uses `tart.image` override; Seatbelt has no image concept).

**Profile image staleness:** A profile image (`yoloai-<profile>`) is considered stale when: (a) it doesn't exist, (b) the profile's Dockerfile has changed since last build (tracked via checksum, same pattern as base image), or (c) `yoloai-base` has been rebuilt since the profile image was last built. Stale images are automatically rebuilt during `yoloai new --profile`.

**Profile inheritance:** Profiles can extend other profiles via the `extends` field, forming a chain rooted at base. Config merging, image resolution, and image building all follow this chain. The merge order is: base config.yaml → each profile in extends order → CLI flags. Cycles are detected (error on revisit). The `extends` field defaults to `"base"` if omitted.

**Profile image chain:** Each profile with a Dockerfile builds `yoloai-<name>` FROM its parent's image. Profiles without Dockerfiles inherit their parent's resolved image. Staleness cascades: rebuilding a parent triggers rebuilds of all descendants that have Dockerfiles.

**`profile.yaml` format:**

```yaml
# ~/.yoloai/profiles/my-project/profile.yaml

extends: base                             # parent profile (default: base)

# --- Same fields as config.yaml (all optional) ---
# backend: docker                         # constrain to a specific backend; omit for any
agent: claude                             # override default agent
# model: sonnet                           # override default model
# tart:                                   # Tart backend settings
#   image: my-custom-vm                   # custom VM image (tart only)
ports:
  - "8080:8080"
env:
  GOMODCACHE: /home/yoloai/go/pkg/mod     # Go module cache
# [PLANNED] agent_files:                  # files seeded into agent-state/ on first run
#   - ~/.claude/CLAUDE.md
#   - /shared/configs/claude-settings.json
# [PLANNED] mounts:                       # bind mounts added at container run time
#   - ~/.ssh:/home/yoloai/.ssh:ro
# [PLANNED] resources:                    # container resource limits
#   memory: 16g
# [PLANNED] cap_add:
#   - NET_ADMIN
# [PLANNED] devices:
#   - /dev/net/tun
# [PLANNED] setup:                        # commands run at container start before agent
#   - tailscale up --authkey=${TAILSCALE_AUTHKEY}
# [PLANNED] network_isolated: true
# [PLANNED] network_allow:
#   - api.example.com
# [PLANNED] copy_strategy: overlay
# [PLANNED] auto_commit_interval: 300

# --- Profile-specific fields (not in config.yaml) ---
workdir:
  path: /home/user/my-app
  mode: copy                              # copy or rw (required for workdir)
  # mount: /opt/myapp                     # optional custom mount point (default: mirrors host path)
directories:
  - path: /home/user/shared-lib
    mode: rw
    mount: /usr/local/lib/shared          # custom mount point (default: mirrors host path)
  - path: /home/user/common-types
    # default: read-only
```

CLI workdir **replaces** profile workdir. CLI `-d` dirs are **additive** with profile dirs. CLI arguments for one-offs, config for repeatability — same options available in both.

**Merge rule:** Merging is iterative across the inheritance chain: base config.yaml → each profile in extends order → CLI flags. Scalars override (child profile over parent, CLI over all). Lists are additive. Maps are merged (later profile wins on conflict). Exception: `agent_files` replaces entirely (not additive — it's a coherent set).

The full merge table for reference:

| Field                  | Merge behavior                                           |
|------------------------|----------------------------------------------------------|
| `backend`              | Profile constrains backend. CLI `--backend` overrides both. Error if profile backend doesn't match resolved backend. |
| `agent`                | Profile overrides default. CLI `--agent` overrides both. |
| `model`                | Profile overrides default. CLI `--model` overrides both. |
| `tart.image`           | Profile overrides default.                               |
| `ports`                | Additive                                                 |
| `env`                  | Merged (profile wins on conflict)                        |
| `workdir`              | Profile provides default, CLI replaces                   |
| `directories`          | Profile provides defaults, CLI `-d` is additive          |
| [PLANNED] `profile`              | Defaults provide fallback. CLI `--profile` overrides. `--no-profile` uses base image. |
| [PLANNED] `agent_files`          | Profile **replaces** defaults (no merge)                 |
| [PLANNED] `mounts`               | Additive (no deduplication — duplicates are user error)  |
| [PLANNED] `resources`            | Profile overrides individual values                      |
| [PLANNED] `cap_add`              | Additive                                                 |
| [PLANNED] `devices`              | Additive                                                 |
| [PLANNED] `setup`                | Additive (defaults first, then profile)                  |
| [PLANNED] `network_isolated`     | Profile overrides default. CLI overrides profile.        |
| [PLANNED] `network_allow`        | Additive                                                 |
| [PLANNED] `copy_strategy`        | Profile overrides default                                |
| [PLANNED] `auto_commit_interval` | Profile overrides default                                |

**`yoloai profile` commands:**

- `yoloai profile create <name>` — Create a profile directory with a scaffold `profile.yaml` containing commented-out examples of all supported fields. [DEFERRED] `--template <tpl>` flag with language-specific scaffolds (`go`, `node`, `python`, `rust`).
- `yoloai profile list` — List all profiles in `~/.yoloai/profiles/`.
- `yoloai profile delete <name>` — Delete a profile directory. Asks for confirmation if any sandbox references the profile.


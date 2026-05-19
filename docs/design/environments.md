> **Design documents:** [Overview](README.md) | [Commands](commands.md) | [Config](config.md) | [Setup](setup.md) | [Security](security.md) | [Research](../dev/RESEARCH.md) | [research/](../dev/research/)

# Development Environments

Design for a two-layer UX model that lets users express development intent at a high level while preserving full expert control over the underlying machinery.

---

## Problem

yoloAI has accumulated flags and configuration that map to *capabilities* (isolation mode, ports, mounts, services, tunnels), but users think in terms of *workflows*: "I want to develop a Go web app that needs a database." Getting the right combination of capabilities for a given workflow requires knowing yoloAI internals — and as features are added, that list grows.

Two specific conflations make this worse:

**Isolation mode ≠ orchestration need.** `container-privileged` is a security/capability decision (give the container full Linux capabilities). "My project needs Docker Compose" is a workflow decision. A user who wants Compose has to know that `--isolation container-privileged` is the enabler — a leaky abstraction.

**Project config ≠ user config.** Profiles serve per-user customization (image, agent, model). But project requirements (services to run, lifecycle commands, VS Code extensions) belong to the project, not the user. There is currently no project-level config, so project requirements either get encoded in user profiles or typed as flags every time.

---

## Goals

1. Users can express "what kind of project is this" and get a working environment without knowing underlying flags.
2. Power users can still control every flag directly. The high-level layer is additive, not a replacement.
3. When yoloAI makes an inference, it says so — what was detected, what it implies, how to override.
4. Project-level environment requirements can be checked into the project repo, not stored in user config.

---

## Two-Layer Model

```
┌─────────────────────────────────────────────────────────┐
│  Layer 1: Environment (high-level, per-project intent)  │
│                                                         │
│  auto-detected or declared via --env / .yoloai.yaml     │
│  devcontainer.json and docker-compose.yaml are sources  │
│                                                         │
│  simple | compose | devcontainer | ios | ...            │
└─────────────────────────────────────────────────────────┘
                          ↓ expands to
┌─────────────────────────────────────────────────────────┐
│  Layer 2: Configuration (low-level, per-flag control)   │
│                                                         │
│  isolation, dockerd, ports, mounts, lifecycle commands, │
│  VS Code workspace files, profile image, ...            │
│                                                         │
│  All existing flags remain available and composable     │
└─────────────────────────────────────────────────────────┘
```

The two layers are separate. Layer 1 is sugar over Layer 2. A user who specifies every flag explicitly bypasses Layer 1 entirely — nothing changes for them. A user who specifies an environment gets Layer 2 configured automatically, with an explanation of what was set and why.

---

## Environment Archetypes

### `simple` (default)

Standard isolated container. No extra services, no Docker daemon. The default when no other archetype is detected or specified.

**Expands to:** isolation=container (or user default), no service startup, no lifecycle commands.

### `compose`

For projects that need Docker Compose services alongside the agent (databases, mail catchers, local cloud stubs, etc.) but have no `devcontainer.json`.

**Why this requires special handling:** Docker Compose runs containers inside the sandbox, which requires full Linux kernel capabilities. Those are blocked by default Docker. `--privileged` unlocks them. The Docker daemon also needs to be started inside the sandbox before Compose can run.

**Why not auto-start dockerd for all `container-privileged` sandboxes:** `container-privileged` is used for reasons other than Compose (kernel module testing, capability probing, overlayfs experiments). Auto-starting dockerd in all privileged sandboxes imposes startup cost and leaves a daemon running for users who don't need it. The archetype — not the isolation mode — is the correct signal for orchestration intent.

**Expands to:**
- isolation=container-privileged
- dockerd auto-started before lifecycle commands
- `docker compose up -d` as a postStartCommand (unless `.yoloai.yaml` overrides)
- port-forwarding from the compose file's `ports:` declarations

### `devcontainer`

For projects with `.devcontainer/devcontainer.json`. yoloAI reads this file as a complete description of the project's environment and satisfies it natively — without the devcontainer CLI and without nested containers.

**Why devcontainers and yoloAI compose well:** The devcontainer workflow that many users have adopted for agent safety (`--dangerously-skip-permissions` inside a devcontainer) has a real gap: the workspace is typically bind-mounted from the host, so destructive file operations reach the user's real files. Network access is unrestricted. There is no review step before changes land. These are exactly the problems yoloAI solves — the two concerns are orthogonal. A user with an existing devcontainer.json should be able to get yoloAI's safety with zero new configuration.

An additional benefit on macOS: standard devcontainer bind mounts go through Docker Desktop's VirtioFS layer (3–4x slower than native Linux). yoloAI's `:copy` mode puts the workdir in the container filesystem entirely, sidestepping this overhead.

**Why no devcontainer CLI / nested container:** Executing the full devcontainer flow (devcontainer CLI → pull image → create container inside sandbox) would put the developer and the AI agent in separate containers. VS Code would connect to the nested container; the agent runs in the outer sandbox — two environments, two file trees, no shared context. yoloAI running the lifecycle commands directly keeps everything in one place.

#### Field Mapping

| devcontainer.json field | yoloAI behaviour | Notes |
|---|---|---|
| `image` | Sandbox base image (wrapped) | See Image Resolution below |
| `build.dockerfile` | Sandbox base image (wrapped) | Resolved relative to `build.context` or workdir |
| `build.context` | Dockerfile build context | |
| `build.args` | Docker build args | Passed to `docker build --build-arg` |
| `forwardPorts` / `appPort` | Port mappings | Host port = container port |
| `remoteEnv` | Container env vars | Set in container environment |
| `containerEnv` | Container env vars | Set in container environment |
| `onCreateCommand` | Run once at creation | Tracked in sandbox-state.json; not re-run on restart or reset |
| `updateContentCommand` | Run once at creation | Run after `onCreateCommand` |
| `postCreateCommand` | Run once at creation | Run after `updateContentCommand` |
| `postStartCommand` | Run on every start | Equivalent to profile `setup:` commands |
| `mounts` | Passed through (with exceptions) | See Mounts Passthrough below |
| `workspaceFolder` | Workdir mount path | Overrides the default mirrored host path |
| `remoteUser` | Container user | User the agent runs as; see Remote User below |
| `containerUser` | Container user | Used if `remoteUser` absent; `remoteUser` takes precedence |
| `customizations.vscode.extensions` | VS Code workspace recommendations | **Only when `--vscode-tunnel` is active**; written to `.vscode/extensions.json` |
| `customizations.vscode.settings` | VS Code workspace settings | **Only when `--vscode-tunnel` is active**; written/merged into `.vscode/settings.json` |
| `features` | **Not supported** | Requires devcontainer CLI; use a profile Dockerfile instead. Warn and continue. |
| `runArgs` | Partial | `--cpus`, `--memory`, `--cap-add` parsed; unknown flags warned and skipped |
| `initializeCommand` | **Ignored** | Runs on the host before container creation — executing arbitrary host commands from `yoloai new` is out of scope. Warn and skip. |
| `postAttachCommand` | **Ignored** | No equivalent; attachment is not a sandbox lifecycle event. Warn and skip. |
| `dockerComposeFile` | **Error** | Multi-container Compose devcontainers are not supported; use a project with both devcontainer.json and docker-compose.yaml instead |
| `name` | **Ignored** | yoloAI uses the sandbox name |
| `waitFor` | **Ignored** | yoloAI always waits for all setup commands before launching the agent |
| `hostRequirements` | **Ignored** | Codespaces sizing hints; not applicable to local Docker |
| `shutdownAction` | **Ignored** | yoloAI manages sandbox lifecycle |

#### Image Resolution

**`image` field:** Used as the sandbox base. yoloAI wraps it with a thin build step to ensure yoloAI's runtime dependencies are present (tmux, gosu, jq, python3, git, curl). Most devcontainer base images already include these; the wrapper is a no-op if they're found.

The wrapper handles `gosu` specially — Alpine uses `su-exec` (symlinked as `gosu`), and distros where `gosu` is unavailable fall back to downloading the static binary:

```dockerfile
# Auto-generated wrapper — not written to disk
FROM <devcontainer-image>
RUN set -e; \
    need=""; \
    command -v tmux    >/dev/null || need="$need tmux"; \
    command -v jq      >/dev/null || need="$need jq"; \
    command -v python3 >/dev/null || need="$need python3"; \
    command -v git     >/dev/null || need="$need git"; \
    command -v curl    >/dev/null || need="$need curl"; \
    if [ -n "$need" ]; then \
      if   command -v apt-get >/dev/null; then apt-get update -qq && apt-get install -y --no-install-recommends $need; \
      elif command -v apk     >/dev/null; then apk add --no-cache $need; \
      elif command -v dnf     >/dev/null; then dnf install -y $need; \
      elif command -v yum     >/dev/null; then yum install -y $need; \
      elif command -v zypper  >/dev/null; then zypper install -y $need; \
      else echo "yoloAI: cannot install deps ($need) — unknown package manager" >&2; exit 1; \
      fi; \
    fi; \
    if command -v gosu >/dev/null; then : ; \
    elif command -v su-exec >/dev/null; then ln -s "$(command -v su-exec)" /usr/local/bin/gosu; \
    else \
      ARCH=$(uname -m); \
      case "$ARCH" in x86_64) ARCH=amd64;; aarch64) ARCH=arm64;; armv7l) ARCH=armv6;; esac; \
      curl -fsSL "https://github.com/tianon/gosu/releases/download/1.17/gosu-${ARCH}" \
        -o /usr/local/bin/gosu && chmod +x /usr/local/bin/gosu; \
    fi
```

The wrapped image is cached as `yoloai-devcontainer-<digest>`, where the digest comes from `docker inspect --format '{{.Id}}'` after pulling. This invalidates the cache when the upstream image updates, not just when its name changes.

**`build.dockerfile` field:** The Dockerfile is used directly. It does not need to `FROM yoloai-base` — yoloAI builds it and layers the same thin dependency check on top. Cached as `yoloai-devcontainer-<sha256-of-dockerfile-content>`.

**`--profile` override:** If `--profile` is specified alongside the `devcontainer` environment, the profile image takes precedence and the devcontainer `image:`/`build:` fields are ignored (with a note in output). All other devcontainer.json fields (lifecycle commands, ports, mounts, VS Code config) still apply. This lets users who already have a profile get devcontainer.json config without image conflicts.

**`--pull`:** Pass to force a fresh pull of the upstream image before wrapping. Without it, yoloAI uses the locally cached wrapper if the digest matches.

**Rebuild:** Rebuilt automatically if the source image or Dockerfile is newer than the cached wrapper, matching profile image behaviour.

**Implementation note — new caching infrastructure required:** Profile images currently have no staleness-tracking or caching scheme (only the base image does). The `yoloai-devcontainer-<digest>` caching requires new infrastructure: digest computation, per-image staleness tracking in `~/.yoloai/cache/`, and a `NeedsBuild()` equivalent for wrapped images. This is moderate new work, independent of the lifecycle command and VS Code injection features.

#### Remote User

devcontainer images are often built around a specific non-root user (`node`, `vscode`, `ubuntu`, etc.) with tools installed under that user's home directory. yoloAI normally runs as the `yoloai` user; running as the wrong user breaks PATH, tool discovery, and file permissions.

`remoteUser` (or `containerUser` if `remoteUser` is absent) maps to a `container_user` field in `runtime-config.json`. The entrypoint uses this in two places currently hardcoded to `yoloai`:
- The `usermod`/`groupmod` UID remap step
- The final `gosu <user>` exec that drops privileges before launching the agent

**Implementation scope is significant.** An audit of the codebase found 11 hardcoded `/home/yoloai` paths across 5 files that must all change before a different container user works correctly:
- Agent `StateDir` paths in `agent/agent.go` (4 agents: claude, gemini, opencode, codex)
- VS Code CLI mount target in `sandbox/create.go`
- Home-seed, tmux, and gitconfig mount targets in `sandbox/create.go`
- `entrypoint.py` `chown` and `HOME` env var assignments

This is a prerequisite for the devcontainer `image:` wrapping feature when the devcontainer image uses a non-`yoloai` user. It is not required for the lifecycle commands, port forwarding, or VS Code workspace injection features, which can be implemented independently first.

**`vscode` user:** The Microsoft devcontainer image family (`mcr.microsoft.com/devcontainers/*`) creates a `vscode` user with `NOPASSWD:ALL` sudo. yoloAI does not need to do anything special — the agent runs with passwordless sudo available, matching the devcontainer environment the user designed.

#### Workspace Folder

`workspaceFolder` specifies where the project appears inside the container. If set, yoloAI uses it as the workdir mount path (the `=<path>` override), overriding the default mirrored host path.

#### Mounts Passthrough

devcontainer.json `mounts` entries are passed through to the sandbox, evaluated by yoloAI on the host (where `${localEnv:HOME}` correctly resolves to the developer's home directory). The following are stripped with warnings:

- **Workspace path conflict:** any mount whose target matches the workdir mount path is dropped. yoloAI's own workdir mount already covers that path.
- **Docker socket** (`/var/run/docker.sock`, `//./pipe/docker_engine`): complete sandbox escape — stripped with an error-level warning.
- **Agent credential directories** (`~/.claude`, `~/.gemini`, `~/.codex`, etc.): yoloAI injects credentials via `/run/secrets`. A conflicting mount would break this. Stripped with a warning.

All other mounts pass through unchanged.

**Note on `${localEnv:HOME}` via Remote Tunnel:** yoloAI evaluates `mounts` entries on the host before the sandbox starts, so `${localEnv:HOME}` expands to the developer's actual home directory. This is safe. The scenario to avoid — where `${localEnv:HOME}` would resolve to the *sandbox* home — would only arise if VS Code's own devcontainer tooling processed the file after connecting via tunnel. yoloAI prevents that by not writing devcontainer.json into the workdir (see VS Code Workspace Injection below), so VS Code never sees it as a devcontainer spec to act on.

#### VS Code Workspace Injection (vscode-tunnel only)

When the `devcontainer` archetype is active and `--vscode-tunnel` is also active, yoloAI injects VS Code workspace files into the workdir copy at creation time:

- `customizations.vscode.extensions` → written to `.vscode/extensions.json` as `recommendations`
- `customizations.vscode.settings` → written/merged into `.vscode/settings.json`

**Why workspace files instead of writing devcontainer.json:** If yoloAI wrote a devcontainer.json into the workdir, VS Code would detect it and prompt "Reopen in Container" — which triggers VS Code's own devcontainer flow (nested container creation). Writing `.vscode/` files instead gives VS Code everything it needs to configure itself without triggering that flow.

**Why only when `--vscode-tunnel`:** Without the tunnel, the developer isn't inside the sandbox, so editor extensions and settings are irrelevant. In agent-only use, the agent doesn't use VS Code.

**Only for `:copy` and `:overlay`:** These modes give the agent a modifiable copy of the project; writing into the copy doesn't touch the original. For `:rw` mounts, the original workdir is live and yoloAI will not write into it.

**Merge behaviour:** If `.vscode/extensions.json` already exists, yoloAI merges recommendations rather than overwriting. Same for `.vscode/settings.json` — existing keys are preserved. This respects project-level VS Code config already checked in.

**Extensions are recommendations, not forced installs:** VS Code shows a "Install Recommended Extensions?" prompt the first time the workspace is opened. Dismissing it suppresses future prompts for that workspace. After installing, extensions persist in the sandbox's per-sandbox VS Code server directory across restarts.

#### Setup Command Execution Context

The current entrypoint runs setup commands (`setup:` in profile config) as **root**, before the `gosu` drop to the container user. `postStartCommand` from devcontainer.json should run as the container user (after UID remap), not root — this matches devcontainer semantics and is what users expect. The implementation must run devcontainer lifecycle commands after the privilege drop, not before. This is a distinct execution slot from the existing setup commands and will need a separate hook in the entrypoint.

#### Compose Detection

If `postStartCommand` contains `docker compose` (or `docker-compose`), the `devcontainer` archetype automatically implies `compose` archetype requirements: isolation=container-privileged, dockerd auto-start. This is the common case — a project has both `devcontainer.json` and a `docker-compose.yaml`, and the devcontainer lifecycle starts the services.

#### Error Handling

- `features` present: warn "Dev Container Features are not supported — use a profile Dockerfile to install equivalent packages" and continue without them
- `dockerComposeFile` present: error "Docker Compose devcontainers are not supported; use a project with devcontainer.json and docker-compose.yaml side by side instead"
- `initializeCommand` present: warn and skip
- `postAttachCommand` present: warn and skip
- Unknown `runArgs` flags: warn and skip (do not fail)
- `runArgs` with iptables caps + `--network-isolated` active: warn about conflict; pass caps through anyway
- Missing `image` and `build`: error "devcontainer.json has no image or build section; specify one or use --profile"
- Platform mismatch (e.g. Windows container image on Linux): error with clear message

#### Stored State

The path to the devcontainer.json and the resolved image digest are stored in `environment.json` as `devcontainer_source` and `devcontainer_image_digest`. `sandbox info` shows these so users can see what was used, and lifecycle commands know the image provenance.

### `apple`

For projects that require Xcode to build or test (iOS apps, macOS apps, Swift packages). Named `apple` rather than `ios` because macOS-target projects are equally covered.

**Why a separate archetype:** Apple platform development cannot run in a Linux Docker container. Xcode, the iOS Simulator, and the macOS SDK all require a real (or virtualised) macOS host.

**Why tart, not seatbelt, as the default:** seatbelt is a macOS process sandbox (`sandbox-exec`) — it restricts what the *agent process* can access, but the agent still runs directly on the host macOS without an isolated environment. It has no overlay mounts, no network isolation, and no capability to run a contained build environment. Tart provides a full Apple Silicon macOS VM with Xcode VirtioFS-mounted from the host, which gives the agent an isolated build environment that works for both iOS and macOS targets.

**Why not detect iOS vs macOS and choose accordingly:** The presence of `.xcodeproj`, `.xcworkspace`, or `Package.swift` doesn't reliably indicate the target platform — a project may target both iOS and macOS simultaneously, or Package.swift may not specify platform constraints. Parsing `project.pbxproj` to extract SDKROOT values is non-trivial and fragile. Since tart supports both targets and seatbelt only makes sense for macOS-only CLI tools that don't need a Simulator, defaulting to tart is the safe choice.

**Override to seatbelt:** Users with macOS-only CLI or library projects that don't need isolated Xcode builds can specify `--backend seatbelt` to use the lightweight host-process sandbox instead.

**Expands to:** backend=tart, os=mac. No Docker involved. Requires Apple Silicon macOS on the host.

**Auto-detection on non-macOS:** Detects `.xcodeproj`, `.xcworkspace`, or `Package.swift` at the workdir root, but warns: "This looks like an Apple platform project. The Tart backend requires Apple Silicon macOS." Does not hard-fail, in case the user is inspecting the project without intending to build.

---

## Auto-Detection

When neither `--env` nor `.yoloai.yaml env:` is specified, yoloAI inspects the workdir. Detection runs in priority order:

1. `.devcontainer/devcontainer.json` or `devcontainer.json` exists → `devcontainer`
2. `docker-compose.yaml` or `docker-compose.yml` exists (no devcontainer.json) → `compose`
3. `.xcodeproj`, `.xcworkspace`, or `Package.swift` at root → `apple`
4. Nothing detected → `simple`

**Overrides:**
- `.yoloai.yaml` in the project root with `env:` declared → use that, skip detection
- `--env <archetype>` on the command line → use that, skip detection
- `--env simple` → explicitly suppress auto-detection and use bare container
- `--backend seatbelt` alongside `apple` archetype → use lightweight host-process sandbox instead of Tart VM

### Transparency Rule

Auto-detection is never silent. When an archetype is inferred, yoloAI prints a causal chain — each bullet directly states what was detected and what decision it drove:

```
→ Detected .devcontainer/devcontainer.json (--vscode-tunnel active)
  Environment: devcontainer
  Because of this:
    · Using image mcr.microsoft.com/devcontainers/go:1.26-trixie (wrapped as yoloai-devcontainer-a3f9...)
    · isolation set to container-privileged (postStartCommand uses docker compose)
    · dockerd will auto-start before lifecycle commands (compose archetype requires it)
    · onCreateCommand "go mod download && make tools" will run once at first start
    · postStartCommand "docker compose up -d" will run on each start
    · .vscode/extensions.json written with 6 recommended extensions
    · .vscode/settings.json written with Go formatter/linter settings
    · Ports 8080, 5432, 8025, 4566 will be forwarded
  To suppress: --env simple   To inspect full config: --dry-run
```

`--dry-run` prints the complete expanded Layer 2 configuration (every effective flag, as if the user had typed them all) and exits without creating the sandbox. This lets users graduate from archetypes to explicit flags when they need precise control.

---

## Transparency in Ongoing Operation

The chosen archetype and its full expansion are stored in `environment.json` at creation time:
- `yoloai sandbox info` always shows what the archetype expanded to
- `yoloai start` / `yoloai restart` re-runs lifecycle commands using stored config without re-running detection

---

## Connecting VS Code to a Sandbox

Two independent methods. Neither requires the other.

### Remote Tunnel (`--vscode-tunnel`)

Starts a VS Code Remote Tunnel inside the sandbox. Accessible from VS Code on any machine via `https://vscode.dev/tunnel/<name>`. Authentication is seeded from `~/.yoloai/vscode-cli/` and persists per-sandbox across restarts. Runs in a dedicated tmux window alongside the agent.

Best for: remote machines, machines without Docker on the developer's local machine, or when the developer and agent need to share the same environment interactively.

### Container Attach (`yoloai sandbox <name> vscode`)

Opens a running sandbox in VS Code using "Attach to Running Container". Requires VS Code and Docker on the local machine. If `code` is on PATH, opens directly; otherwise prints instructions.

```
Opening VS Code attached to sandbox 'fix-bug'...
Container: yoloai-fix-bug
Workdir:   /home/user/projects/my-app
$ code --folder-uri "vscode-remote://attached-container+<hex-encoded-json>/home/user/projects/my-app"
```

The hex-encoded segment is `{"containerName":"yoloai-fix-bug"}` encoded as lowercase hex — the format VS Code Dev Containers expects for container attachment URIs.

Best for: local development where the developer wants the lightest-weight connection without a tunnel.

**Backend support:**

| Backend | Supported | Notes |
|---|---|---|
| Docker | Yes | Container name `yoloai-<name>` is directly attachable |
| Podman | Yes | VS Code Dev Containers supports Podman |
| containerd | No | VS Code does not support attaching to containerd containers |
| Tart | No | VM, not a Docker container |
| Seatbelt | No | Process sandbox, not a container |

---

## Project Spec (`.yoloai.yaml`)

A project can include a `.yoloai.yaml` at its root. Checked into the project repo. Not for secrets or machine-specific paths.

```yaml
# .yoloai.yaml
env: devcontainer          # explicit archetype; suppresses auto-detection

# Mounts evaluated by yoloAI on the host before sandbox start.
# Use this for credentials that aren't appropriate to put in devcontainer.json
# (e.g. age keys, SSH keys) — yoloAI evaluates ~ correctly on the host.
mounts:
  - ~/.config/sops/age:/home/yoloai/.config/sops/age:ro

# Warn if active profile or devcontainer image doesn't satisfy these.
requires:
  go: ">=1.26"
```

Mount precedence (highest to lowest): CLI flags > `.yoloai.yaml` > profile config > baked-in defaults.

---

## Relationship to Profiles

Profiles and environments serve different purposes and compose cleanly:

| | Profile | Environment |
|--|---------|------------|
| **Scope** | Per-user | Per-project |
| **Storage** | `~/.yoloai/profiles/` (not in repo) | `.yoloai.yaml` (in repo) |
| **Controls** | Image (Dockerfile), agent, model, resource limits | Orchestration, services, lifecycle, VS Code config |
| **Purpose** | Customize what toolchain is in the container | Describe what the project needs to run |

When `--profile` is specified with the `devcontainer` archetype, the profile image takes precedence over the devcontainer `image:` field. All other devcontainer.json config (lifecycle commands, ports, mounts, VS Code extensions) still applies.

---

## Open Questions

- **`--devcontainer` shorthand flag:** Keep as an alias for `--env devcontainer` for discoverability? Probably yes — it's a widely recognised term and discoverable in `yoloai new --help`.

- **`requires:` validation strictness:** Hard-fail at creation time if the profile doesn't satisfy `requires:` constraints, or warn and continue? Failing is safer but may block users with slightly-different toolchain versions.

- **`postStartCommand` string/array/object forms:** devcontainer.json allows `"cmd arg"` (string, run via `/bin/sh -c`), `["cmd", "arg"]` (array, direct exec), and `{"name": "cmd", "name2": "cmd2"}` (object, named parallel commands — all must succeed). The string and array forms map directly to yoloAI setup commands. The object form needs to be run with parallel execution semantics, which the current setup command runner (sequential `sh -c` per entry) does not support; simplest approach is to run each value sequentially as a shell command and warn that parallel semantics are not preserved.

- **`postCreateCommand` tracking across resets:** `yoloai reset` re-copies the workdir but preserves the container and its writable layer. The effects of one-time commands already live in that writable layer and persist across resets — no re-run needed, matching devcontainer behaviour. Confirm before implementing.

- **Archetype extensibility:** Should users be able to define custom archetypes in `~/.yoloai/`? Hold — risks recreating the complexity problem in a different form.

- **`apple` archetype name vs `ios`:** The archetype covers all Apple platform development (iOS, macOS, watchOS, visionOS). `apple` is more accurate but `ios` is what most users would search for. Could support both as aliases.

- **Implementation sequencing:** The `devcontainer` archetype has three mostly independent work streams: (1) lifecycle commands + port injection — no image changes needed, (2) VS Code workspace file injection — no image changes needed, (3) image wrapping with the dependency check layer — requires remoteUser work as a prerequisite when the devcontainer image uses a non-`yoloai` user. Streams 1 and 2 can ship first and cover the majority of real-world use cases including foley.

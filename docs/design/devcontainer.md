> **Design documents:** [Overview](README.md) | [Commands](commands.md) | [Config](config.md) | [Setup](setup.md) | [Security](security.md) | [Research](../dev/RESEARCH.md)

# Dev Container Integration

## Goal

Let users who already have a `.devcontainer/devcontainer.json` in their project use yoloAI without writing a separate profile. The devcontainer spec describes the environment; yoloAI provides the safety layer (copy mode, diff/apply, network isolation) on top of it. Additionally, let users who prefer working in VS Code attach to a running yoloAI sandbox to review the agent's work in their editor.

## Why devcontainers and yoloAI compose well

The devcontainer workflow that many users have adopted for agent safety (`--dangerously-skip-permissions` inside a devcontainer) has a real gap: the workspace is typically **bind-mounted from the host**, so destructive file operations reach the user's real files. Network access is unrestricted. There is no review step before changes land.

These are exactly the problems yoloAI solves. The two concerns are orthogonal:

- **devcontainer.json** defines the development *environment* (tools, packages, ports, env vars)
- **yoloAI** defines the *safety layer* (copy mode, diff/apply, network isolation, sandbox lifecycle)

A user with an existing devcontainer.json should be able to get yoloAI's safety with zero new configuration.

## Feature 1: `--devcontainer` flag on `yoloai new`

### Invocation

```
yoloai new fix-bug . --devcontainer
yoloai new fix-bug . --devcontainer .devcontainer/devcontainer.json
yoloai new fix-bug . --devcontainer --pull
```

Without an explicit path, yoloAI searches for `.devcontainer/devcontainer.json` or `devcontainer.json` in the workdir. `--devcontainer` without a path uses auto-detection; with a path, that file is used directly.

`--devcontainer` is mutually exclusive with `--profile` — they both define the sandbox environment. Error if both are specified: "flags --devcontainer and --profile are mutually exclusive".

### Field mapping

| devcontainer.json field | yoloAI equivalent | Notes |
|---|---|---|
| `image` | sandbox image | Used directly as the base |
| `build.dockerfile` | profile Dockerfile | Resolved relative to `build.context` (or workdir) |
| `build.context` | Dockerfile build context | |
| `build.args` | Docker build args | Passed to `docker build --build-arg` |
| `forwardPorts` / `appPort` | `--port` mappings | Host port = container port (same number) |
| `remoteEnv` | `env:` in config | Set in container environment |
| `containerEnv` | `env:` in config | Set in container environment |
| `onCreateCommand` | `post_create_commands` in meta.json | Run once after container creation |
| `updateContentCommand` | `post_create_commands` in meta.json | Run once after container creation |
| `postCreateCommand` | `post_create_commands` in meta.json | Run once after container creation |
| `postStartCommand` | `setup:` commands | Run on every sandbox start |
| `mounts` | `mounts:` in config | Bind-mount strings passed through |
| `workspaceFolder` | workdir mount path | Overrides the default mirrored host path |
| `remoteUser` | `container_user` in runtime-config.json | User the agent runs as inside the container; defaults to `yoloai` |
| `containerUser` | `container_user` in runtime-config.json | Same as `remoteUser`; `remoteUser` takes precedence if both are set |
| `features` | **not supported** | Dev Container Features require the devcontainer CLI; out of scope |
| `customizations.vscode` | **ignored** | VS Code-specific, not relevant to agent sandboxing |
| `runArgs` | partial | `--cpus`, `--memory`, `--cap-add` parsed; unknown flags warned and skipped |

### Image resolution

**`image` field:** Used as-is. yoloAI wraps it with a thin build step to ensure yoloAI's runtime dependencies are present (tmux, gosu, jq, python3, git, curl). Most devcontainer base images already include these; the build step is a no-op if they're found.

The wrapper detects the available package manager and handles `gosu` specially — Alpine uses `su-exec` (symlinked as `gosu`), and distros where `gosu` is unavailable fall back to downloading the static binary:

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

The resulting image is cached as `yoloai-devcontainer-<digest-of-image>`, where the digest is obtained via `docker inspect --format '{{.Id}}'` after pulling. This ensures the cache is invalidated when the upstream image is updated, not just when its name changes.

**`build.dockerfile` field:** The Dockerfile is used as a profile Dockerfile. It does not need to `FROM yoloai-base` — yoloAI builds it normally and layers the same thin dependency check on top.

The resulting image is cached as `yoloai-devcontainer-<sha256-of-dockerfile-content>` (hash of the Dockerfile file contents, which changes when the file changes).

**Rebuild:** Rebuilt automatically if the source image or Dockerfile is newer than the cached wrapper image, matching the behaviour of profile images.

### One-time vs per-start commands

devcontainer defines several one-time hooks (`onCreateCommand`, `updateContentCommand`, `postCreateCommand`) that run after container creation, and `postStartCommand` that runs on every start. yoloAI's `setup` commands run on every start. The mapping:

- `onCreateCommand`, `updateContentCommand`, `postCreateCommand` → recorded in `meta.json` as `post_create_commands`; run on first start only (tracked via `state.json`, same mechanism as `agent_files_initialized`). Run in order: `onCreateCommand`, then `updateContentCommand`, then `postCreateCommand`.
- `postStartCommand` → recorded in `meta.json` as `setup` commands; run on every start

Both are run before the agent is launched, matching devcontainer semantics.

### `remoteUser` / `containerUser`

devcontainer images are often built around a specific non-root user (`node`, `vscode`, `ubuntu`, etc.) with tools installed under that user's home directory. yoloAI normally runs as the `yoloai` user; running as the wrong user breaks PATH, tool discovery, and file permissions.

`remoteUser` (or `containerUser` if `remoteUser` is absent) maps to a `container_user` field in `runtime-config.json`. `entrypoint.py` uses this field in two places currently hardcoded to `yoloai`:

- The `usermod`/`groupmod` UID remap step — renames the target user to match the host UID
- The final `gosu <user>` exec that drops privileges before launching the agent

The home directory is derived from the named user's passwd entry at runtime rather than assumed to be `/home/yoloai`.

**Implementation note:** `sandbox-setup.py` must also be audited for hardcoded `/home/yoloai` paths and updated to use the configured user and home directory before this is implemented.

### `workspaceFolder`

devcontainer's `workspaceFolder` specifies where the project appears inside the container. If set, yoloAI uses it as the mount path for the workdir (the `=<path>` override), instead of mirroring the host path.

### Workspace mount mode

The default workdir mount mode is `:copy`, same as yoloAI normally. Users who want the agent to write directly to the host workdir (matching the devcontainer default behaviour) can opt in with `:rw`:

```
yoloai new fix-bug .:rw --devcontainer
```

The mount mode suffix works exactly as it does without `--devcontainer`. `:copy` is the recommended default — it preserves the diff/apply review step that makes yoloAI useful.

### `mounts` passthrough

devcontainer.json `mounts` entries are passed through to the sandbox with one exception: any entry whose target path matches the workspace mount path (from `workspaceFolder`, or the default mirrored host path) is dropped with a warning. yoloAI's own workdir mount already covers that path — letting devcontainer's mount win would bypass copy mode.

All other mounts pass through unchanged. The workspace will be present at the expected path; it will just be yoloAI's managed copy rather than the live host directory.

### Error handling

- Unknown `runArgs` flags: warn and skip (do not fail)
- `features` present: warn that Features are not supported; continue without them
- `dockerComposeFile` present: error: "Docker Compose devcontainers are not supported; use a profile with a Dockerfile instead"
- Unsupported `image` platform (e.g., Windows containers): error with clear message
- Missing `image` and `build`: error: "devcontainer.json has no image or build section"

### Stored state

The resolved devcontainer configuration is stored in `meta.json` as a `devcontainer_source` field (path to the devcontainer.json at creation time) so `sandbox info` can show it and lifecycle commands know the provenance of the sandbox image.

---

## Feature 2: `yoloai sandbox <name> vscode`

Open a running yoloAI sandbox in VS Code using the "Attach to Running Container" workflow. This feature is independent of `--devcontainer` — it works for any sandbox running on a Docker or Podman backend.

### Invocation

```
yoloai sandbox fix-bug vscode
```

### Behaviour

Requires the sandbox to be running. Prints the VS Code URI and, if `code` is on PATH, opens VS Code directly:

```
Opening VS Code attached to sandbox 'fix-bug'...

Container: yoloai-fix-bug
Workdir:   /home/user/projects/my-app

$ code --folder-uri "vscode-remote://attached-container+<hex-encoded-json>/home/user/projects/my-app"
```

The hex-encoded segment is the JSON `{"containerName":"yoloai-fix-bug"}` encoded as a lowercase hex string — this is the format VS Code Dev Containers expects for container attachment URIs.

If `code` is not on PATH, prints the URI and instructions for opening manually:

```
yoloai sandbox fix-bug vscode could not find 'code' on PATH.

To open in VS Code manually:
  1. Open VS Code
  2. Command Palette → "Dev Containers: Attach to Running Container"
  3. Select: yoloai-fix-bug
  4. Navigate to: /home/user/projects/my-app
```

### What the user sees in VS Code

VS Code Server runs inside the sandbox container. The user browses the agent's in-progress work directory (the yoloAI copy, not the host original). They can:
- Read and edit files while the agent is working
- Open a terminal inside the sandbox
- Use VS Code's diff view to inspect changes

The agent and the user's VS Code session share the same container. This is intentional — the user is observing and optionally interacting with the sandbox, not running a separate isolated session.

After reviewing, the user closes VS Code and uses `yoloai diff` / `yoloai apply` to land changes as usual.

### Backend support

| Backend | Supported | Notes |
|---|---|---|
| Docker | Yes | Container name `yoloai-<name>` is directly attachable |
| Podman | Yes | Same container naming; VS Code Dev Containers supports Podman |
| containerd | No | VS Code does not support attaching to containerd-managed containers |
| Tart | No | VM, not a Docker container |
| Seatbelt | No | Process sandbox, not a container |

Errors clearly when the backend does not support VS Code attachment.

---

## Complete workflow example

```
# Project already has .devcontainer/devcontainer.json
cd ~/projects/my-app

# Create sandbox using the project's devcontainer environment
yoloai new fix-bug . --devcontainer --prompt "fix the memory leak in server.go"

# Attach to watch the agent work in VS Code
yoloai sandbox fix-bug vscode

# When agent is done, review changes
yoloai diff fix-bug

# Apply what you want
yoloai apply fix-bug
```

The user's devcontainer environment (tools, packages, language runtime) is preserved. yoloAI adds copy-mode protection, diff/apply review, and optionally network isolation — without requiring any new configuration files.

---

## Out of scope

- **Dev Container Features** (`features:` field): These are pre-built installable components managed by the devcontainer CLI (`@devcontainers/cli`). Supporting them would require either shelling out to the devcontainer CLI or reimplementing Feature installation. Out of scope for v1; users who rely on Features should create a yoloAI profile with a Dockerfile that installs the equivalent packages directly.
- **VS Code extensions** (`customizations.vscode.extensions`): Agent sandboxing has no use for editor extensions.
- **`initializeCommand`**: Runs on the *host* before container creation. Executing arbitrary host commands as part of `yoloai new` is a security risk and outside yoloAI's model. Warn and skip.
- **Docker Compose devcontainers** (`dockerComposeFile`): Multi-container setups add significant complexity. Out of scope for v1.
- **`postAttachCommand`**: Runs when a tool attaches (VS Code, devcontainer CLI). No equivalent in yoloAI — attachment is not a sandbox lifecycle event. Warn and skip.

## Open questions

1. ~~**Caching strategy for wrapper images**~~ Resolved: the wrapper image is fixed at sandbox creation time. By default, `yoloai new` uses the locally cached wrapper if the digest matches the stored value; it does not pull the upstream image on every invocation. Pass `--pull` to force a fresh pull and rebuild. This keeps `yoloai new` fast on slow connections and makes the sandbox environment predictable. The digest is stored in `meta.json` alongside `devcontainer_source`.
2. ~~**`postCreateCommand` tracking across resets**~~ Resolved: `yoloai reset` re-copies the workdir but preserves the container and its writable layer. The effects of `post_create_commands` already live in that writable layer and persist across resets — no re-run needed, no divergence from devcontainer behaviour.
3. ~~**Conflict between devcontainer `mounts` and yoloAI safety**~~ Resolved: workspace-path mounts from `mounts` are dropped and replaced by yoloAI's own workdir mount. Users who want rw behaviour use `.:rw` on the CLI. See "Workspace mount mode" and "`mounts` passthrough" above.

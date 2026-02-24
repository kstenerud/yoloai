# Phase 3: Base Image and `yoloai build`

## Goal

Working `yoloai build` command that produces the `yoloai-base` Docker image. Includes the Dockerfile, container entrypoint script, embedded resources with seed-to-disk logic, and build command wiring.

## Prerequisites

- Phase 2 complete (Docker client wrapper)
- Docker daemon running (for verification)

## Files to Create

| File | Description |
|------|-------------|
| `resources/Dockerfile.base` | Base Docker image definition |
| `resources/entrypoint.sh` | Container entrypoint script (~50 lines) |
| `internal/docker/build.go` | `SeedResources`, `BuildBaseImage`, build context tar creation |
| `internal/docker/resources.go` | `go:embed` declarations for resources |
| `internal/docker/build_test.go` | Unit tests for `SeedResources` |

## Files to Modify

| File | Change |
|------|--------|
| `internal/cli/commands.go` | Wire `build` command to call `SeedResources` + `BuildBaseImage` |

## Resources

### `resources/Dockerfile.base`

```dockerfile
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    tmux \
    git \
    build-essential \
    python3 \
    curl \
    ca-certificates \
    gnupg \
    jq \
    && rm -rf /var/lib/apt/lists/*

# Node.js 22 LTS via NodeSource
ARG NODE_MAJOR=22
RUN mkdir -p /etc/apt/keyrings \
    && curl -fsSL https://deb.nodesource.com/gpgkey/nodesource-repo.gpg.key \
       | gpg --dearmor -o /etc/apt/keyrings/nodesource.gpg \
    && echo "deb [signed-by=/etc/apt/keyrings/nodesource.gpg] https://deb.nodesource.com/node_${NODE_MAJOR}.x nodistro main" \
       > /etc/apt/sources.list.d/nodesource.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/*

# Claude Code via npm
RUN npm install -g @anthropic-ai/claude-code

# gosu for dropping privileges
ARG GOSU_VERSION=1.17
RUN curl -fsSL "https://github.com/tianon/gosu/releases/download/${GOSU_VERSION}/gosu-$(dpkg --print-architecture)" \
      -o /usr/local/bin/gosu \
    && chmod +x /usr/local/bin/gosu \
    && gosu --version

# Non-root user (placeholder UID/GID — entrypoint adjusts at runtime)
RUN groupadd -g 1001 yoloai \
    && useradd -m -u 1001 -g yoloai -s /bin/bash yoloai

# Internal directory for sandbox state files
RUN mkdir -p /yoloai && chown yoloai:yoloai /yoloai

COPY entrypoint.sh /yoloai/entrypoint.sh
RUN chmod +x /yoloai/entrypoint.sh

ENTRYPOINT ["/yoloai/entrypoint.sh"]
```

**Notes:**
- `gosu` version pinned to 1.17 (stable, widely used in official Docker images). Can be bumped by editing `~/.yoloai/Dockerfile.base`.
- Node.js installed via NodeSource rather than the official Node Docker image so we keep Debian slim as the base (smaller, consistent).
- `--no-install-recommends` to minimize image size.
- Entrypoint runs as root initially (for UID/GID remapping), then drops to `yoloai` user via gosu.

### `resources/entrypoint.sh`

```bash
#!/bin/bash
set -euo pipefail

# --- Run as root ---

# Read config
CONFIG="/yoloai/config.json"
HOST_UID=$(jq -r '.host_uid' "$CONFIG")
HOST_GID=$(jq -r '.host_gid' "$CONFIG")
AGENT_COMMAND=$(jq -r '.agent_command' "$CONFIG")
STARTUP_DELAY=$(jq -r '.startup_delay' "$CONFIG")
SUBMIT_SEQUENCE=$(jq -r '.submit_sequence' "$CONFIG")

# Remap UID/GID to match host user
CURRENT_UID=$(id -u yoloai)
CURRENT_GID=$(id -g yoloai)

if [ "$CURRENT_GID" != "$HOST_GID" ]; then
    groupmod -g "$HOST_GID" yoloai 2>/dev/null || true
fi
if [ "$CURRENT_UID" != "$HOST_UID" ]; then
    usermod -u "$HOST_UID" yoloai 2>/dev/null || true
fi

# Fix ownership on container-managed directories
chown -R yoloai:yoloai /yoloai /home/yoloai

# Read secrets and export as env vars
if [ -d /run/secrets ]; then
    for secret in /run/secrets/*; do
        [ -f "$secret" ] || continue
        varname=$(basename "$secret")
        export "$varname"="$(cat "$secret")"
    done
fi

# --- Drop privileges and run as yoloai ---
exec gosu yoloai bash -c '
set -euo pipefail

AGENT_COMMAND='"'"'"$AGENT_COMMAND"'"'"'
STARTUP_DELAY='"$STARTUP_DELAY"'
SUBMIT_SEQUENCE='"'"'"$SUBMIT_SEQUENCE"'"'"'

# Start tmux session with logging and remain-on-exit
tmux new-session -d -s main -x 200 -y 50
tmux set-option -t main remain-on-exit on
tmux pipe-pane -t main "cat >> /yoloai/log.txt"

# Launch agent inside tmux
tmux send-keys -t main "$AGENT_COMMAND" Enter

# If prompt exists, deliver it after startup delay
if [ -f /yoloai/prompt.txt ]; then
    sleep "$STARTUP_DELAY"
    tmux load-buffer /yoloai/prompt.txt
    tmux paste-buffer -t main
    tmux send-keys -t main $SUBMIT_SEQUENCE
fi

# Block forever — container stops only on explicit docker stop
exec tmux wait-for yoloai-exit
'
```

**Notes:**
- The entrypoint runs the main body as root, then `exec gosu yoloai bash -c '...'` drops to the yoloai user for everything else.
- `tmux wait-for yoloai-exit` blocks indefinitely (nothing signals this channel). With `--init` (tini as PID 1), `docker stop` sends SIGTERM through tini, which terminates the `tmux wait-for` process cleanly.
- `remain-on-exit on` keeps the tmux session alive after the agent exits, allowing post-mortem inspection via `yoloai attach`.
- `tmux pipe-pane` captures all session output to `log.txt`.
- The prompt delivery uses `load-buffer`/`paste-buffer` (handles long/multiline prompts without shell escaping issues) followed by `send-keys` with the agent's submit sequence.

## Types and Signatures

### `internal/docker/resources.go`

```go
package docker

import "embed"

//go:embed resources/Dockerfile.base
var embeddedDockerfile []byte

//go:embed resources/entrypoint.sh
var embeddedEntrypoint []byte
```

**Note on embed paths:** `go:embed` paths are relative to the source file's directory. Since the `resources/` directory is at the project root and this file is in `internal/docker/`, the embed directives won't work with `resources/...`. Two options:

1. Move the embed declarations to a package at the project root level.
2. Move the resource files into `internal/docker/resources/`.

**Option 2 is cleaner** — keeps the embedded files next to the code that uses them. The files in `internal/docker/resources/` are the canonical embedded copies; `~/.yoloai/Dockerfile.base` and `entrypoint.sh` are the user-editable runtime copies seeded from these.

Adjusted plan: resource files live at `internal/docker/resources/Dockerfile.base` and `internal/docker/resources/entrypoint.sh`. The `resources/` directory at the project root is not needed.

### `internal/docker/build.go`

```go
package docker

import (
	"context"
	"io"
	"log/slog"
)

// SeedResources copies embedded Dockerfile.base and entrypoint.sh to the
// target directory if they don't already exist. Called before build to
// ensure user-editable copies are in place.
func SeedResources(targetDir string) error

// BuildBaseImage builds the yoloai-base Docker image from the Dockerfile
// and entrypoint in the given directory. Build output is streamed to the
// provided writer (typically os.Stderr for user-visible progress).
func BuildBaseImage(ctx context.Context, client Client, sourceDir string, output io.Writer, logger *slog.Logger) error
```

**`SeedResources` behavior:**
1. Create `targetDir` if it doesn't exist (with `0750` permissions).
2. Write `Dockerfile.base` to `<targetDir>/Dockerfile.base` if the file doesn't exist.
3. Write `entrypoint.sh` to `<targetDir>/entrypoint.sh` if the file doesn't exist.
4. Never overwrite — user edits are preserved.

**`BuildBaseImage` behavior:**
1. Create a tar archive in memory containing `Dockerfile.base` (renamed to `Dockerfile` in the tar — Docker expects `Dockerfile` by default) and `entrypoint.sh` from `sourceDir`.
2. Call `client.ImageBuild` with the tar as build context, tag `yoloai-base`, and `Remove: true` (clean up intermediate containers).
3. Stream the build output (JSON lines from Docker) to the output writer, extracting the `stream` field from each JSON message for human-readable output.
4. Check for error in the final JSON message.

### CLI wiring in `internal/cli/commands.go`

The `build` command:
1. Creates a Docker client via `docker.NewClient(ctx)`.
2. Calls `docker.SeedResources(~/.yoloai/)`.
3. Calls `docker.BuildBaseImage(ctx, client, ~/.yoloai/, os.Stderr, logger)`.
4. Prints "Base image yoloai-base built successfully" on success.

MVP: `yoloai build` with no arguments only. Profile argument is accepted by Cobra (already `MaximumNArgs(1)`) but returns "profiles not yet implemented" if provided.

## Implementation Steps

1. **Create `internal/docker/resources/` directory** with `Dockerfile.base` and `entrypoint.sh`.

2. **Create `internal/docker/resources.go`:**
   - `go:embed` declarations for both files.

3. **Create `internal/docker/build.go`:**
   - `SeedResources`: check file existence, write embedded content if absent.
   - `BuildBaseImage`: create tar build context, call `ImageBuild`, stream output.
   - Helper `createBuildContext(sourceDir string) (io.Reader, error)`: reads `Dockerfile.base` and `entrypoint.sh` from disk, creates an in-memory tar with `Dockerfile` (renamed) and `entrypoint.sh`.
   - Helper `streamBuildOutput(response, output)`: reads JSON lines from Docker build response, extracts `stream` fields, writes to output, checks for `error` field.

4. **Create `internal/docker/build_test.go`:**
   - Test `SeedResources` writes files to empty dir.
   - Test `SeedResources` does not overwrite existing files.
   - Test `createBuildContext` produces valid tar with expected entries.

5. **Wire `build` command in `internal/cli/commands.go`:**
   - Replace the stub `RunE` with real implementation.
   - Get home dir, construct `~/.yoloai/` path.
   - Create Docker client, seed resources, build image.

6. **Run `go mod tidy`.**

## Tests

### `internal/docker/build_test.go`

```go
func TestSeedResources_CreatesFiles(t *testing.T)
// SeedResources to empty temp dir → both files created with expected content

func TestSeedResources_DoesNotOverwrite(t *testing.T)
// Write custom content to Dockerfile.base, call SeedResources → content preserved

func TestCreateBuildContext_ValidTar(t *testing.T)
// Create temp dir with Dockerfile.base and entrypoint.sh
// Call createBuildContext, read the tar, verify:
// - Contains "Dockerfile" (not "Dockerfile.base")
// - Contains "entrypoint.sh"
// - File contents match source files
```

No integration tests in this phase — building the image requires Docker and takes minutes. The `yoloai build` command is verified manually.

## Verification

```bash
# Must compile
go build ./...

# Linter must pass
make lint

# Unit tests pass
make test

# Manual verification (requires Docker):
make build
./yoloai build
# Should stream Docker build output and produce yoloai-base image

docker images yoloai-base
# Should show the image

docker run --rm --init yoloai-base bash -c "claude --version"
# Should print Claude Code version

docker run --rm --init yoloai-base bash -c "node --version && jq --version && tmux -V && git --version"
# Should print versions of all installed tools
```

## Concerns

### 1. Entrypoint quoting complexity

The `exec gosu yoloai bash -c '...'` pattern requires careful quoting to pass variables from the root context into the user context. The shell variables (`AGENT_COMMAND`, `STARTUP_DELAY`, `SUBMIT_SEQUENCE`) are interpolated into the single-quoted bash -c string using quote-break-quote patterns. This is fragile but standard for Docker entrypoints. The alternative (writing a temp script) adds filesystem state. If the quoting proves brittle during testing, switch to writing `/tmp/yoloai-run.sh` and executing that.

### 2. NodeSource availability

NodeSource's GPG key and repository URL could change. The Dockerfile is user-editable in `~/.yoloai/`, so users can fix this without waiting for a yoloai release. Pin a specific Node major version via `ARG NODE_MAJOR=22`.

### 3. Image build time

First build takes 2-5 minutes (Node.js, Claude Code npm install). This is the expected cost of a full-featured base image. Subsequent rebuilds use Docker layer caching. The `SeedResources` / `BuildBaseImage` split means Phase 4a's `EnsureSetup` can skip the build when the image already exists.

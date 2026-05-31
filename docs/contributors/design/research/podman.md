# Podman Backend Research

Podman is a daemonless OCI container runtime with a Docker-compatible CLI. A user has requested support.
This document captures verified facts needed to decide whether and how to implement a Podman backend.

## Value Proposition

Users can already use Podman with yoloAI today by setting `DOCKER_HOST` to point at Podman's
Docker-compatible socket. The Docker backend works transparently in this configuration for most
operations. A dedicated Podman backend adds value in four areas:

1. **Automatic socket discovery** — no manual `DOCKER_HOST` configuration required.
2. **`--userns=keep-id` injection** — correct file ownership in rootless mode without user config.
3. **Correct build tooling** — shells out to `podman build` instead of `docker build` for
   secret-based profile builds (see Build Tooling section).
4. **Diagnostics and help text** — backend name shows as "podman" in error messages, `yoloai info`,
   and troubleshooting hints.

If automatic socket discovery and rootless UID mapping aren't important, users can continue using
the Docker backend with `DOCKER_HOST`.

## Architecture Fit

The `runtime.Runtime` interface fully isolates backend concerns. Adding Podman is self-contained in a
new `runtime/podman/` package.

Backend selection currently lives in two places:
- `yoloai.go:newRuntime()` — used by Go API callers
- `internal/cli/helpers.go:newRuntime()` — used by CLI commands

Adding Podman requires a new case in both, plus a new entry in `knownBackends` for help text.

### Code-Level Blockers in `sandbox/create.go`

Several places in `sandbox/create.go` hardcode `backend == "docker"` or `backend != "docker"` checks
that would reject a `"podman"` backend:

- **Line 567**: `:overlay` mode requires `m.backend != "docker"` check → must allow `"podman"`.
- **Line 561**: `NET_ADMIN` cap for network isolation requires `m.backend == "docker"` → must allow `"podman"`.
- **Line 574**: `cap_add`, `devices`, and `setup` recipe fields require `m.backend != "docker"` → must allow `"podman"`.

**Fix:** Introduce a helper like `isContainerBackend(backend)` that returns true for both `"docker"`
and `"podman"`, and use it in place of these string comparisons. This is a small change but blocking —
without it, Podman users cannot use `:overlay`, network isolation, or recipe fields.

## The SDK Problem

Docker has an official Go SDK (`github.com/docker/docker`). Podman does not.

**Options:**

1. **CLI via `os/exec`** — consistent with the Tart and Seatbelt backends. Straightforward but
   adds exec overhead per operation and requires stderr parsing for error mapping.

2. **Podman Docker-compatible API socket** — Podman v4+ exposes a Docker-compatible REST API at
   `$XDG_RUNTIME_DIR/podman/podman.sock` (rootless) or `/run/podman/podman.sock` (system-wide).
   This would allow reusing the Docker SDK client by pointing it at the Podman socket via
   `client.WithHost("unix:///run/user/<uid>/podman/podman.sock")`. Compatibility is high for
   basic container operations; edge cases exist in build (see below).

3. **`github.com/containers/podman/v5/pkg/bindings`** — Podman's own Go bindings. Uses the same
   REST API as option 2 but through a typed client. Adds a large dependency tree.

**Recommendation:** Option 2 (Docker-compat socket) is the lowest-effort path. Option 1 (CLI) is
the safest if compat socket coverage proves insufficient. Option 3 adds too much dependency weight.

## Podman Socket Discovery

Socket location varies by install method and user mode. Note that `$XDG_RUNTIME_DIR` defaults to
`/run/user/<uid>` on most Linux systems with systemd.

| Mode | Path |
|------|------|
| Rootless (most Linux installs) | `$XDG_RUNTIME_DIR/podman/podman.sock` |
| System-wide / root | `/run/podman/podman.sock` |
| Podman Desktop (macOS/Windows) | See below |
| `$DOCKER_HOST` or `$CONTAINER_HOST` | Wherever the env var points |

**Podman Desktop socket path:** Podman 5.0+ introduced the Apple hypervisor (`applehv`) backend
on macOS; the current default backend is `libkrun` (since ~Podman 5.6+). The socket path varies
by machine name and backend provider: `~/.local/share/containers/podman/machine/podman.sock`
(common default) or `~/.local/share/containers/podman/machine/<provider>/podman.sock`. The most
reliable discovery method is to run
`podman machine inspect --format '{{.ConnectionInfo.PodmanSocket.Path}}'` rather than guessing
paths. (Verified: the `ConnectionInfo.PodmanSocket.Path` field exists in Podman source.)

Discovery order: `$CONTAINER_HOST`, `$DOCKER_HOST`, rootless path, system path. For macOS, fall
back to `podman machine inspect` if static paths don't exist.

## Docker API Compatibility

Podman v4+/v5 implements the Docker REST API. Podman's current compat API targets Docker API
v1.44.0, with a minimum supported version of v1.24.0 (verified in `version/version.go`).
Podman 5.x is the current major version (as of 2025) and maintains the same compat API surface.
In practice:

**Well-supported (safe to reuse Docker backend logic):**
- Container create, start, stop, remove
- Container inspect and list
- Exec (non-interactive and interactive)
- Volume mounts, bind mounts
- Port mappings
- Network isolation (`--network=none`)
- Resource limits (CPU, memory)

**Known gaps / caveats:**
- **BuildKit**: Podman uses Buildah for builds, not BuildKit. `X-Registry-Config` headers and
  some build secret formats differ. `--secret` syntax is the same (`id=name,src=path`) as of
  Podman 4.x. Build streaming response format is compatible.
- **Container name prefix**: Both Docker and Podman's compat API return container names with a
  leading `/` (e.g., `/yoloai-sandbox-1`). Verified: Podman adds the prefix explicitly via
  `fmt.Sprintf("/%s", l.Name())` in `pkg/api/handlers/compat/containers.go`. The Docker backend
  already strips this prefix (`strings.TrimPrefix(name, "/")` in `runtime/docker/prune.go`), so
  no difference to handle.
- **`podman-docker` compatibility package**: Many distros offer a `podman-docker` package that
  installs a `docker` CLI shim pointing at Podman. Users with this installed may already have
  the Docker backend working via CLI shim — relevant because `buildProfileImageCLI` shells out
  to the `docker` binary.

### Error Handling Compatibility

The Docker backend uses `containerd/errdefs` for error type mapping (`IsNotFound`, `IsConflict`).
This works by inspecting HTTP status codes returned through the Docker SDK's error handling layer
(404 → NotFound, 409 → Conflict, verified in `vendor/github.com/containerd/errdefs/pkg/errhttp/http.go`).

**Verified against Podman's compat API** — Podman returns identical HTTP status codes:

- **404** for missing container (`utils.ContainerNotFound()` in `pkg/api/handlers/utils/errors.go`)
  and missing image (`utils.ImageNotFound()`). Matches Docker. ✓
- **304 Not Modified** for starting an already-running container (`containers_start.go:41`) and
  stopping an already-stopped container (`containers_stop.go:64`). Docker returns the same 304
  for both cases (via `errdefs.NotModified()` in `daemon/start.go` and `daemon/stop.go`). ✓
- **409 Conflict** for killing a stopped container, removing a running container, and name
  conflicts. Matches Docker. ✓

HTTP status code compatibility is **not a risk** for the compat socket approach. Both
implementations return identical codes for all error conditions relevant to yoloAI.

## Build Tooling

The Docker backend builds images two ways:

1. **SDK `ImageBuild()`** — used for base image and secret-free profile builds. Sends a tar
   context, streams JSON response. This goes through the Docker-compat socket and should work
   with Podman (Buildah handles the build server-side).

2. **CLI `docker build`** — used by `buildProfileImageCLI()` in `runtime/docker/build.go:313`
   when build secrets are needed. Hardcodes `exec.CommandContext(ctx, "docker", args...)` with
   `DOCKER_BUILDKIT=1` env var.

The CLI path is a problem for Podman:
- It invokes the `docker` binary directly — won't work unless `podman-docker` shim is installed.
- It sets `DOCKER_BUILDKIT=1` — Podman ignores this (uses Buildah), and it's harmless. Verified:
  Buildah has zero references to `DOCKER_BUILDKIT`; Podman only touches it to force `=0` for
  compose operations.

**Fix:** The Podman backend must override `BuildProfileImage` to shell out to `podman build`
instead of `docker build`. The `--secret` flag syntax is identical (`id=name,src=path`, verified
in Buildah source at `pkg/parse/parse.go`), so only the binary name changes. Alternatively, make
the CLI binary name configurable in the shared build logic.

## Reuse Architecture

**Recommendation: Embed `docker.Runtime` and override select methods.**

The Docker backend has 13 interface methods plus build helpers. Most work identically over the
Podman compat socket. A full independent implementation (~1,000 lines) would duplicate all of this
with no behavioral difference for most methods.

The recommended architecture:

```go
package podman

type Runtime struct {
    *docker.Runtime  // embed Docker runtime
}

func New(ctx context.Context) (*Runtime, error) {
    sock := discoverSocket()
    // Create Docker client pointed at Podman socket
    dockerRT, err := docker.NewWithSocket(ctx, sock)
    if err != nil {
        return nil, err
    }
    return &Runtime{Runtime: dockerRT}, nil
}

func (r *Runtime) Name() string { return "podman" }

func (r *Runtime) BuildProfileImage(...) error {
    // Shell out to `podman build` instead of `docker build`
}
```

This requires one change to the Docker backend: export a `NewWithSocket(ctx, socketPath)` constructor
(or accept `...client.Opt` overrides) so Podman can reuse the initialization logic with a different
endpoint. The existing `New()` would call `NewWithSocket` with the default Docker socket.

Note: `buildBaseImage` is a package-level function (not a method on `Runtime`) that takes a
`*dockerclient.Client` directly. The embedded `docker.Runtime` handles this transparently — the
Podman wrapper's `EnsureImage` calls the embedded method, which calls `buildBaseImage` with its
client. No override needed as long as the client points at the Podman socket.

Estimated size: ~100–150 lines for the Podman package (socket discovery + constructor + name +
build override).

## Rootless Containers and File Ownership

Podman's rootless mode runs containers as the invoking user via user namespaces. This affects
file ownership in mounted directories:

- **`:rw` and `:copy` mounts**: The container process sees files as owned by root (UID 0) inside
  the container, mapped to the host user outside. Reads and writes work correctly; the host sees
  the user's UID on all files. No ownership surprises.
- **`:overlay` mode**: Requires `CAP_SYS_ADMIN` inside the container for overlayfs. In rootless
  Podman, this capability is available within the user namespace but subject to the kernel's
  unprivileged overlayfs support (kernel 5.11+). Needs testing.
- **`--userns=auto`**: Podman's auto user namespace mapping allocates deterministic,
  non-overlapping UID ranges (first-fit from available subordinate IDs, not random). This would
  still break file ownership in bind mounts because the container UID won't match the host user.
  yoloAI should explicitly use `--userns=keep-id` (rootless) or omit `--userns` (system-wide)
  to preserve UID mapping. (Verified: `keep-id` maps host UID/GID identically into the container
  via `GetKeepIDMapping()` in `pkg/util/utils.go`.)

## Platform Support

| Platform | Status |
|----------|--------|
| Linux | Primary target. Rootless Podman widely available via distro packages. |
| macOS | Podman Desktop exists (uses a Linux VM like Docker Desktop). Less widely adopted. |
| Windows | Podman Desktop available but niche. WSL2 path is more common. |

Podman's Linux story is strongest. macOS/Windows are secondary.

## Overlay Mode Concern

The `:overlay` directory mode requires `CAP_SYS_ADMIN` inside the container. The Docker backend
grants this via `CapAdd: []string{"SYS_ADMIN"}` in the container config. This works the same
way in Podman on Linux. On rootless Podman, `SYS_ADMIN` is available within the user namespace
for kernel operations that don't require host-level privilege — overlayfs falls into this category
on kernel 5.11+. This needs integration testing on a rootless Podman setup before shipping.

Note: The `sandbox/create.go` backend checks must be updated to allow `"podman"` for overlay mode
(see Code-Level Blockers section above).

## Implementation Scope

Assuming the Docker-compat socket approach with embedding:

| Component | Effort | Notes |
|-----------|--------|-------|
| Socket discovery | Small | Check env vars, known paths; `podman machine inspect` fallback on macOS |
| `docker.NewWithSocket()` export | Small | Refactor existing `New()` to accept socket path |
| `runtime/podman/New()` | Small | Discover socket, call `docker.NewWithSocket()`, wrap |
| `Name()` | Trivial | Return `"podman"` |
| `BuildProfileImage()` override | Small | Shell out to `podman build` instead of `docker build` |
| All other Runtime methods | Zero | Inherited from embedded `docker.Runtime` |
| `sandbox/create.go` backend checks | Small | Replace `== "docker"` with `isContainerBackend()` |
| `--userns=keep-id` injection | Small | Add to `InstanceConfig` or hardcode in Podman `Create()` override |
| Backend registration | Trivial | Two switch cases, one `knownBackends` entry |
| Error handling verification | Done | Podman returns identical HTTP status codes — verified against source |
| Integration tests | Medium | Need a Podman-equipped CI environment |

## Resolved Questions

1. **HTTP status code compatibility**: ✓ Resolved. Podman's Docker-compat API returns identical
   HTTP status codes for all error cases. 404 for not-found, 304 for already-in-desired-state
   (start/stop), 409 for conflicts. Matches Docker exactly. Not a risk.

2. **`--userns` strategy**: ✓ Resolved. `--userns=keep-id` should be hardcoded. Verified that
   it maps host UID/GID identically into the container. `--userns=auto` uses deterministic
   first-fit allocation (not random), but would still break bind mount ownership.

## Open Questions

1. **`:overlay` on rootless Podman**: Does `CAP_SYS_ADMIN` + overlayfs work correctly in a
   rootless Podman container on a modern kernel? Needs hands-on testing.

## Resolved: CI Environment

GitHub Actions `ubuntu-24.04` (`ubuntu-latest`) ships Podman **4.9.3** pre-installed — v4+ has
the Docker-compat API we need. The socket is not started by default but activation is trivial:

```yaml
- name: Start Podman socket
  run: systemctl --user start podman.socket
```

`ubuntu-22.04` ships Podman 3.4.4 (pinned due to bugs) which predates the compat API
improvements — not usable. Podman CI must target `ubuntu-24.04`.

Source: `actions/runner-images` repo, `images/ubuntu/Ubuntu2404-Readme.md` and
`images/ubuntu/scripts/build/install-container-tools.sh`.

# Podman Backend Research

Podman is a daemonless OCI container runtime with a Docker-compatible CLI. A user has requested support.
This document captures verified facts needed to decide whether and how to implement a Podman backend.

## Architecture Fit

The `runtime.Runtime` interface fully isolates backend concerns. Adding Podman is self-contained in a
new `runtime/podman/` package — zero changes to `sandbox/`, `internal/cli/`, or the public API.
The existing Docker, Tart, and Seatbelt backends confirm the pattern works at scale.

Backend selection currently lives in two places:
- `yoloai.go:newRuntime()` — used by Go API callers
- `internal/cli/helpers.go:newRuntime()` — used by CLI commands

Adding Podman requires a new case in both, plus a new entry in `knownBackends` for help text.

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

Socket location varies by install method and user mode:

| Mode | Path |
|------|------|
| Rootless (most Linux installs) | `$XDG_RUNTIME_DIR/podman/podman.sock` |
| System-wide / root | `/run/podman/podman.sock` |
| Podman Desktop (macOS/Windows) | `~/.local/share/containers/podman/machine/qemu/podman.sock` |
| Custom `DOCKER_HOST` env | Wherever `$DOCKER_HOST` points |

Discovery should check in order: `$DOCKER_HOST`, rootless path, system path, Podman Desktop path.

## Docker API Compatibility

Podman v4+ implements the Docker REST API at v1.40+ compatibility level. In practice:

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
- **Container name prefix**: Docker SDK returns container names with a leading `/`
  (e.g., `/yoloai-sandbox-1`); Podman omits it. The Docker backend already strips this prefix
  (`strings.TrimPrefix(name, "/")`), so this is handled.
- **`DOCKER_HOST` env**: If a user has `DOCKER_HOST` set pointing at Podman's socket, the Docker
  backend already works today. The Podman backend would make this automatic.

## Rootless Containers and File Ownership

Podman's rootless mode runs containers as the invoking user via user namespaces. This affects
file ownership in mounted directories:

- **`:rw` and `:copy` mounts**: The container process sees files as owned by root (UID 0) inside
  the container, mapped to the host user outside. Reads and writes work correctly; the host sees
  the user's UID on all files. No ownership surprises.
- **`:overlay` mode**: Requires `CAP_SYS_ADMIN` inside the container for overlayfs. In rootless
  Podman, this capability is available within the user namespace but subject to the kernel's
  unprivileged overlayfs support (kernel 5.11+). Needs testing.
- **`--userns=auto`**: Podman's auto user namespace mapping (assigns random UID ranges) would
  break file ownership in bind mounts. yoloAI should explicitly use `--userns=keep-id` (rootless)
  or omit `--userns` (system-wide) to preserve UID mapping.

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

## Implementation Scope

Assuming the Docker-compat socket approach (option 2):

| Component | Effort | Notes |
|-----------|--------|-------|
| Socket discovery | Small | Check known paths, respect `$DOCKER_HOST` |
| `New()` / client init | Small | `client.WithHost(podmanSocket)` instead of default |
| `Create()` / `Start()` / `Stop()` / `Remove()` / `Inspect()` | Trivial | Reuse Docker backend logic verbatim |
| `Exec()` / `InteractiveExec()` | Trivial | Same |
| `EnsureImage()` / `ImageExists()` | Small | Build streaming may need minor adjustment |
| `Prune()` | Small | Same logic |
| Error mapping | Small | `containerd/errdefs` works with Podman's Docker-compat layer |
| `--userns=keep-id` injection | Small | Add to `InstanceConfig` or hardcode in Podman backend |
| Backend registration | Trivial | Two switch cases, one `knownBackends` entry |
| Integration tests | Medium | Need a Podman-equipped CI environment |

Assuming the CLI approach (option 1), effort increases by ~300–400 lines for output parsing
and error mapping.

## Open Questions

1. **Docker-compat socket coverage**: Does Podman's Docker-compatible API cover everything the
   Docker backend uses? Needs a systematic check of every Docker SDK call in `runtime/docker/`
   against Podman's compat layer documentation.

2. **`:overlay` on rootless Podman**: Does `CAP_SYS_ADMIN` + overlayfs work correctly in a
   rootless Podman container on a modern kernel? Needs hands-on testing.

3. **CI environment**: The integration tests require a running Podman daemon. GitHub Actions
   Ubuntu runners have Docker pre-installed; Podman requires `apt install podman` and socket
   activation setup. Is this acceptable for CI?

4. **`--userns` strategy**: Should `--userns=keep-id` be hardcoded in the Podman backend, or
   should it be configurable? `keep-id` is the right default for `:copy` and `:rw` mounts.

5. **Scope of reuse**: Is a `runtime/podman/` package that wraps the Docker backend with a
   different client init acceptable, or should it be a fully independent implementation?
   The former is ~100 lines; the latter is ~1,000.

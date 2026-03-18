# Containerd Runtime Backend Design

## Problem Statement

yoloAI's `--security` flag currently supports four values: `standard`, `gvisor`, `kata`, and
`kata-firecracker`. The first two work correctly with the Docker backend. The latter two do not —
Kata Containers 3.x networking is broken when containers are launched via Docker's shimv2 path.

### Root Cause (Verified on Ubuntu 24.04, Docker 29, Kata 3.28)

Kata 3.x dropped its standalone OCI runtime binary (`kata-runtime` no longer implements
`create/start/delete/kill`). It is now a containerd shimv2 only. When Docker registers a shimv2
runtime via `runtimeType` in `daemon.json`, Docker's networking stack (libnetwork) creates a veth
pair and network namespace for the container — but does **not** include the netns path in the OCI
spec passed to the shim. The OCI spec contains `{"type": "network"}` with no `path` field.

Kata's shim, receiving an empty netns path, creates a new empty namespace, finds no interfaces to
plumb into the VM, and starts QEMU with no network device. The container runs but has no network
connectivity.

This is not a configuration problem. It is a structural incompatibility between Docker's libnetwork
model and the containerd shimv2 protocol. Even with CRI enabled in containerd, the containerd image
store enabled in Docker, CNI plugins installed, and `vhost_net` loaded — the netns path is never
passed.

**Verified fix:** nerdctl uses containerd's native CNI-based networking. When nerdctl launches a
Kata container, it runs CNI plugins first, sets up a proper veth in a new netns, then passes the
netns path to the Kata shim. The VM gets `eth0` and full internet connectivity.

### Why Not Fix It in the Docker Backend?

Options considered and ruled out:

- **Register Kata as a classic OCI runtime** — Kata 3.x has no OCI runtime binary. Ruled out.
- **Use `runtimeType` + containerd image store** — Still doesn't pass netns. Ruled out (tested).
- **Wrapper script as OCI runtime** — Would require reimplementing OCI create/start/delete/kill/state
  protocol, a substantial and fragile shim. Ruled out.
- **NRI (Node Resource Interface) hooks** — Containerd plugin mechanism; limited to CRI path, not
  Docker's path. Ruled out.
- **Use nerdctl as a drop-in within the Docker backend** — Possible short-term workaround, but
  nerdctl uses a different image namespace (`default` vs Docker's `moby`), different network
  management, different container lifecycle. Would require yoloAI to juggle two CLIs and two
  namespaces. Ruled out as too fragile.

---

## Simplified UX Model (80/20)

The combination of backend × security mode × filesystem access mode produces many combinations —
most of which don't work. Rather than expose this complexity to users, yoloAI should present a
single orthogonal flag per concern, auto-select the correct backend, and error early on invalid
combinations.

### Isolation Flag

Replace `--security` with `--isolation`. Values describe the isolation model, not the
implementation:

| `--isolation` | Boundary | Attack surface | Underlying backend |
|---------------|----------|----------------|--------------------|
| *(omitted)*   | Container | Standard | docker (runc) |
| `container`   | Container | Standard | docker (runc) |
| `container-enhanced` | Container | Syscall-filtered | docker (gVisor/runsc) |
| `vm`          | Hardware VM | Standard | containerd (Kata + QEMU) |
| `vm-enhanced` | Hardware VM | Minimal device model | containerd (Kata + Firecracker) |

The two axes are explicit: **boundary type** (container vs vm) and **attack surface** (standard vs
enhanced). No implementation names leak — users never see "gVisor", "Kata", or "Firecracker"
unless they read the docs.

**Why `container` instead of `standard`?** With `--os mac` in the picture, `container` vs `vm`
is a meaningful contrast everywhere. On macOS, seatbelt is a container-like sandbox; Tart is a VM.
The naming is consistent across platforms.

**Why `vm-enhanced` instead of `vm-firecracker`?** Firecracker's value is its minimal device
model (smaller attack surface within the VM boundary) — the same relationship `container-enhanced`
has to `container`. Naming it `vm-enhanced` describes what it provides rather than what it is, and
keeps the 2×2 matrix symmetric.

### OS Flag (Mac)

On macOS, Docker runs a Linux VM under the hood. Most users are fine with this — they want a Linux
container, and Docker provides it. But some users specifically want an agent running *on macOS*,
because their code targets macOS APIs, uses macOS tooling (Xcode, notarization), or needs macOS
file semantics.

Add `--os` flag (values: `linux`, `mac`):

| `--os` | What the user gets | Underlying backend |
|--------|--------------------|--------------------|
| *(omitted)* | Linux container (default everywhere) | docker or containerd |
| `linux` | Linux container (explicit) | docker or containerd |
| `mac`   | macOS sandbox or VM | seatbelt or tart |

**Windows:** yoloAI runs inside WSL2 — there is no native Windows binary. Running inside WSL2
means yoloAI sees a Linux environment, so the `--os` flag has no `windows` value and no Windows
container (WCOW) support is planned. Docker Desktop, Podman Desktop, and Rancher Desktop all
expose their container runtimes inside WSL2 and work transparently.

`--os mac` selects the macOS-native backend; which one depends on `--isolation`:

| `--os mac` + `--isolation` | Backend | What the user gets |
|---------------------------|---------|-------------------|
| `container` (default) | seatbelt | macOS sandbox-exec (lightweight, low overhead) |
| `vm`                  | tart    | Full macOS VM |
| `container-enhanced`  | —       | **Error**: gVisor is Linux-only |
| `vm-enhanced`         | —       | **Error**: Firecracker is Linux-only |

Seatbelt is the right default for `--os mac`: it's the macOS equivalent of a standard runc
container — process-level sandboxing, fast startup, no VM overhead. Tart is for users who want
full VM-level isolation or need a clean macOS environment.

**Isolation symmetry across platforms:**

| `--isolation`        | Linux backend            | macOS backend (`--os mac`) | Windows (WSL2)                    |
|----------------------|--------------------------|----------------------------|-----------------------------------|
| `container`          | docker (runc)            | seatbelt (sandbox-exec)    | docker or podman (same as Linux)  |
| `container-enhanced` | docker (gVisor)          | *(unsupported)*            | docker (gVisor) — if gVisor installed in WSL2 |
| `vm`                 | containerd (Kata + QEMU) | tart (macOS VM)            | *(requires nested KVM — see below)* |
| `vm-enhanced`        | containerd (Kata + FC)   | *(unsupported)*            | *(requires nested KVM — see below)* |

**What happens to `--backend`?** It stays as a visible but secondary flag — present in `--help`
output but not in the quickstart docs. Its job is now narrower: override auto-selection within the
chosen isolation level. The primary cases:

- `--backend podman` — user prefers Podman (rootless, daemonless, systemd integration, corporate
  policy) even when Docker is present
- No other backends need explicit user selection; all others are auto-selected by `--isolation`
  and `--os`

Podman is a legitimate primary choice for many Linux users, not an obscure workaround. Hiding it
behind a config-only key would make it a second-class citizen.

**Config key: `container_backend` (not `backend`).** The global/profile config key for this
preference is `container_backend` (e.g., `container_backend: podman`). It only applies when
`--isolation container` or `container-enhanced` is in effect — for `vm`, `vm-enhanced`, and
`--os mac`, the backend is always auto-selected and `container_backend` is ignored. This scoping
prevents a stale `container_backend: podman` config entry from producing confusing errors when the
user later requests VM isolation.

### Backwards Compatibility

No backwards compatibility requirement. `--security` is replaced by `--isolation`. Migration:

| Old flag | New flag |
|----------|----------|
| *(omitted)* | *(omitted)* |
| `--security standard` | `--isolation container` |
| `--security gvisor` | `--isolation container-enhanced` |
| `--security kata` | `--isolation vm` |
| `--security kata-firecracker` | `--isolation vm-enhanced` |
| `--backend tart` | `--os mac --isolation vm` |
| `--backend seatbelt` | `--os mac` (`container` is the default for `--os mac`) |

### Backend Auto-Selection

`resolveBackend()` in `cli/helpers.go` currently reads `--backend` or config default. Extend it
with auto-detection and capability checking:

**General principle: explicit intent is never silently overridden.** CLI flags (`--backend`,
`--isolation`, `--os`) signal explicit intent — a failure to satisfy them is always an error, never
a silent fallback. Config values signal default preferences — a missing preferred runtime issues a
warning and falls back gracefully. This distinction is captured in `backendOverrideIsExplicit`
below.

```
// backendOverride: from --backend CLI flag or container_backend config key
// backendOverrideIsExplicit: true only when --backend was given on the CLI;
//   container_backend config is a preference, not a demand — treated as non-explicit
resolveBackend(isolation, os, backendOverride, backendOverrideIsExplicit):
  if os == "mac":
    switch isolation:
    case "vm":
      requireTart()  // error if tart not installed; no fallback
      return tart
    case "container-enhanced", "vm-enhanced":
      error (unsupported on macOS)
    default: // "container" or omitted
      return seatbelt

  switch isolation:
  case "vm", "vm-enhanced":
    // container_backend config is ignored here — backend is always containerd
    return containerd

  // container / container-enhanced: Docker or Podman
  if backendOverrideIsExplicit:
    // User said exactly what they want — validate it, never fall back
    requireBackend(backendOverride)  // error if not reachable
    if isolation == "container-enhanced":
      checkEnhancedSupport(backendOverride)  // error if gVisor not configured
    return backendOverride
  else:
    backend = detectContainerBackend()  // auto-detect; see below
    if isolation == "container-enhanced":
      checkEnhancedSupport(backend)  // error if gVisor not configured
    return backend
```

**`detectContainerBackend()`:** Tries the `container_backend` config preference first, warns and
falls back if unavailable. Only runs when no explicit `--backend` was given on the CLI:

```
detectContainerBackend(containerBackendConfig):
  preferred = containerBackendConfig  // "docker", "podman", or unset

  if preferred == "podman":
    if podmanSocketReachable() or podmanBinaryExists():
      return podman
    warn: "container_backend=podman not found; falling back to docker"
    if dockerSocketReachable():
      return docker
    error: no container runtime found. Install Docker or Podman.

  // Default order: docker first, podman second
  if dockerSocketReachable():
    return docker
  if preferred == "docker":
    // Explicit docker preference but docker not found; warn before trying podman
    warn: "container_backend=docker not found; falling back to podman"
  if podmanSocketReachable() or podmanBinaryExists():
    return podman
  error: no container runtime found. Install Docker or Podman.
```

Config values signal a default preference, not explicit intent — a missing preferred runtime is a
warning, not an error. Only CLI `--backend` signals explicit intent and fails hard when the
requested backend is unavailable. Docker is the default first choice because it is more common and
has broader compatibility (e.g., gVisor registration). Podman is the fallback for
rootless/daemonless environments.

**`podmanSocketReachable()`** checks the following paths in order (first reachable wins):
- `/run/podman/podman.sock` — standard Linux rootful Podman
- `/run/user/<uid>/podman/podman.sock` — standard Linux rootless Podman
- `/mnt/wsl/podman-sockets/podman-machine-default/podman-root.sock` — Podman Desktop on Windows (WSL2 machine, rootful)
- `/mnt/wsl/podman-sockets/podman-machine-default/podman-user.sock` — Podman Desktop on Windows (WSL2 machine, rootless)

The WSL2 paths are used by Podman Desktop for Windows when the WSL2 machine provider is active.
Without probing these paths, Podman Desktop users on Windows would never be auto-detected.

**`checkEnhancedSupport(backend)`:** Before creating a `container-enhanced` sandbox, verify the
chosen backend has gVisor configured — don't wait for a cryptic runtime error deep in the stack.
Always an error; never falls back to `container`:

```
checkEnhancedSupport(backend):
  switch backend:
  case docker:
    query docker info --format '{{json .Runtimes}}'
    if "runsc" not in runtimes:
      error: container-enhanced requires gVisor (runsc) registered in Docker.
             Register it: https://gvisor.dev/docs/user_guide/install/
  case podman:
    check /etc/containers/containers.conf and ~/.config/containers/containers.conf
    if "runsc" not in [engine.runtimes]:
      error: container-enhanced requires gVisor (runsc) registered in Podman.
             Add to containers.conf: [engine.runtimes] runsc = ["/usr/bin/runsc", "..."]
```

This check runs at `yoloai new` time only — sandbox lifecycle commands (`start`, `exec`, etc.)
read the backend from `meta.json` and skip re-detection.

On `yoloai sandbox start/stop/destroy/exec`, the backend is read from `meta.json` (stored at
creation time). No changes needed for lifecycle commands.

---

## Overlay Compatibility

`:overlay` (Linux overlayfs) is a filesystem access mode, not a security mode. It requires:
1. The container runtime can mount an overlayfs inside the container (needs `CAP_SYS_ADMIN` or
   user namespaces, and kernel support)
2. The container filesystem is accessible to the host for diff/apply (overlayfs upper layer must
   be on a host-visible path)

**Compatibility matrix:**

| Backend | `:overlay` | Notes |
|---------|-----------|-------|
| docker / `container` | Yes | Works. Requires `CAP_SYS_ADMIN`. |
| docker / `container-enhanced` | No | gVisor does not support mounting overlayfs inside the container. |
| containerd / `vm` | No | Overlayfs upper layer is inside the VM guest, not visible to the host. |
| containerd / `vm-enhanced` | No | Same as `vm`. |
| tart (`--os mac`, `vm`) | No | macOS VM. No overlayfs. |
| seatbelt (`--os mac`, `container`) | No | No container filesystem. |

**Rule:** `:overlay` only works with `--isolation container` (default or explicit) on Linux.

**User experience:** If a user specifies `:overlay` with an incompatible combination, yoloAI errors
at creation time with a clear message:

```
Error: :overlay directories require --isolation container (docker backend).
       --isolation container-enhanced uses gVisor, which does not support overlayfs inside the container.
       Use :copy instead, or switch to --isolation container.
```

**Documentation strategy:** `:overlay` is documented in the main guide with a short caveat:

> `:overlay` is a space-efficient alternative to `:copy` that uses Linux overlayfs for instant
> directory setup. It requires standard isolation (Docker, Linux host) and `CAP_SYS_ADMIN`.
> It does not work with `container-enhanced`, `vm`, or `vm-enhanced` isolation modes, or on macOS. If you're unsure, use `:copy`.

A detailed `:overlay` reference lives in an advanced section / separate doc covering the full
tradeoff (instant setup vs. host-visible upper layer, `CAP_SYS_ADMIN` requirement, container-only
diff workflow).

---

## Proposed Solution: Containerd Runtime Backend

Add a `containerd` backend to yoloAI's `runtime.Runtime` interface that talks directly to the
containerd API using the [containerd Go client](https://github.com/containerd/containerd/tree/main/client).

Docker and nerdctl are both frontends to containerd. By talking to containerd directly, yoloAI
gains full control over container lifecycle, networking (via CNI), and OCI runtime selection —
without being constrained by Docker's or nerdctl's protocol choices.

### What the Containerd Backend Owns

| Concern | Mechanism |
|---------|-----------|
| Image management | containerd image store (pull, push, check) |
| Container creation | containerd `client.NewContainer()` + task API |
| Networking | CNI plugins — same approach as nerdctl |
| OCI runtime selection | `WithRuntime(runtimeType, options)` on task creation |
| Exec | containerd task `Exec()` API |
| Interactive exec | PTY via containerd exec + OS pipe |
| Logs | containerd's log plugin or bind-mounted log file |
| Image building | Delegate to Docker (or buildkit directly) |

### Relationship to Existing Backends

```
runtime.Runtime (interface)
├── runtime/docker/        ← Docker SDK — container, container-enhanced (gVisor)
├── runtime/podman/        ← Podman compat socket — rootless (--backend podman, not exposed via --isolation)
├── runtime/containerd/    ← NEW: containerd API — vm (kata+qemu), vm-enhanced (kata+firecracker)
├── runtime/tart/          ← Tart VM — macOS (--os mac --isolation vm)
└── runtime/seatbelt/      ← sandbox-exec — macOS lightweight (--os mac --isolation container)
```

Podman is reachable via `--backend podman` in config only — it is not mapped to an `--isolation`
value and is not part of the auto-selection logic. It serves rootless Linux environments where
Docker is unavailable. Its status in the `--isolation` model is out of scope for this design.

The containerd backend is not a replacement for Docker — it is a parallel backend for isolation
modes that require it. The Docker backend continues to serve `container` and `container-enhanced`.

### Isolation Mode → Backend Mapping

| `--isolation` value  | Backend    | Mechanism |
|----------------------|------------|-----------|
| *(omitted)*          | docker     | runc (default) |
| `container`          | docker     | runc |
| `container-enhanced` | docker     | runsc (gVisor) |
| `vm`                 | containerd | `io.containerd.kata.v2` shimv2 + CNI |
| `vm-enhanced`        | containerd | `io.containerd.kata-fc.v2` shimv2 + CNI + devmapper |

### Image Namespace

containerd partitions images into namespaces. Docker uses `moby`; nerdctl defaults to `default`.

The containerd backend should use its **own namespace** (e.g., `yoloai`) to avoid conflicts with
either Docker or nerdctl. Images need to be imported into this namespace.

**Image building:** Docker's BuildKit (`docker buildx build`) is the most mature image builder.
Build the yoloAI image with Docker as before, then export and import it into the `yoloai` namespace:

```
docker build → docker save → containerd image import (namespace=yoloai)
```

Alternatively, BuildKit can be called directly without Docker. This is more complex and not
necessary initially.

### Networking

Use CNI plugins (already installed at `/opt/cni/bin/`). The containerd backend manages a CNI
configuration that creates a bridge network for yoloAI containers:

```json
{
  "cniVersion": "1.0.0",
  "name": "yoloai",
  "plugins": [
    {"type": "bridge", "bridge": "yoloai0", "isGateway": true, "ipMasq": true,
     "ipam": {"type": "host-local", "subnet": "10.88.0.0/16"}},
    {"type": "portmap", "capabilities": {"portMappings": true}},
    {"type": "firewall"}
  ]
}
```

The containerd backend calls CNI plugins directly (as nerdctl does) before creating the container
task, then passes the resulting netns path to the OCI spec.

**CNI teardown:** On container stop/remove, the backend must run CNI `DEL` to release the
allocated IP back to the host-local IPAM pool, remove the veth pair, and delete the network
namespace. Without this, the IP pool leaks and orphaned netns accumulate on the host. The CNI
state (allocated IP, interface name, netns path) must be persisted per-container at creation time
so teardown can reconstruct the correct `DEL` call even if the container process is gone. nerdctl
stores this in a per-container CNI state file; the containerd backend should do the same (e.g.,
`~/.yoloai/sandboxes/<name>/backend/cni-state.json`).

**Network isolation mode** (`--network isolated`): The containerd backend applies iptables rules
in the same way as the Docker backend — via the in-container entrypoint and ipset, which are
runtime-agnostic.

### OCI Runtime Selection

The containerd client's `WithRuntime` option maps to the shimv2 type:

```go
// vm (kata-qemu)
containerd.WithRuntime("io.containerd.kata.v2", &options.Options{
    ConfigPath: "/opt/kata/share/defaults/kata-containers/configuration-qemu.toml",
})

// vm-enhanced (kata-firecracker)
containerd.WithRuntime("io.containerd.kata-fc.v2", &options.Options{
    ConfigPath: "/opt/kata/share/defaults/kata-containers/configuration-fc.toml",
})
```

### Interactive Exec

The Docker backend shells out to `docker exec -it`. The containerd backend implements this natively:

1. Call containerd's task `Exec()` API
2. Set up a PTY (`github.com/creack/pty`)
3. Pipe stdin/stdout/stderr, forward terminal resize signals (SIGWINCH → containerd `ResizePTY`)

nerdctl's exec implementation is a useful reference. No nerdctl shim — see resolved OQ#6.

### Image Building

The containerd backend reuses the Docker backend's `EnsureImage()` for the build step, then
imports the result into the `yoloai` containerd namespace. The `yoloai-base` image is built once
and shared.

---

## Implementation Plan

### Phase 1: Containerd Backend Spike (verify the approach)

Before committing to implementation, verify the design works end-to-end:

1. Write a minimal Go program that uses the containerd client to:
   - Pull `alpine`
   - Run it with `io.containerd.kata.v2` runtime + CNI networking
   - Exec `ip addr` and verify `eth0` exists with an IP
   - Stop the container and verify CNI teardown releases the IP cleanly
2. If this works, proceed. If not, revisit the design before touching any user-facing flags.

### Phase 2: Rename `--security` → `--isolation`

1. Rename the flag everywhere (CLI, config, meta.json, docs).
2. Map new value names to backend/runtime selection.
3. Add `--os` flag; implement `resolveBackend()` logic.
4. Remove `--backend` from `yoloai new` help text (keep in config as escape hatch).
5. Add early error for `:overlay` + incompatible isolation/os combination.

No backwards compatibility handling needed.

### Phase 3: Containerd Backend (MVP)

Implement `runtime/containerd/` with:
- `New()` — connect to containerd socket, verify kata shim exists
- `Create()` — CNI setup + containerd container + task creation
- `Start()` — CNI setup + new task creation + task start (containerd tasks are terminal after exit;
  restart requires creating a new task from the existing container, not resuming the old one)
- `Stop()` — task kill + wait for exit + CNI teardown
- `Remove()` — container delete
- `Inspect()` — task status
- `Exec()` — non-interactive exec
- `InteractiveExec()` — native containerd task Exec() + PTY (`github.com/creack/pty`)
- `EnsureImage()` — Docker build + import into yoloai namespace
- `Logs()` — read from bind-mounted log file (same as Docker backend)
- `Prune()` — remove orphaned yoloai-* containers

### Phase 4: Auto-Selection

Extend `resolveBackend()` to auto-select `containerd` for `vm`/`vm-enhanced` isolation modes.
Update isolation validation to check for containerd prerequisites if vm is chosen.

### Phase 5: vm-enhanced (Kata + Firecracker)

Kata-Firecracker requires the devmapper snapshotter in containerd. This needs a pre-provisioned
thin-pool block device (or loop device). On Ubuntu 24.04, the recommended approach is a loop-based
thin pool via `dmsetup` — the snap-packaged containerd ships without devmapper support compiled in,
so the apt-installed containerd must be used. Implement after Phase 3 is stable.

---

## Open Questions

1. ~~**Image namespace strategy**~~ — **Resolved:** Use the `yoloai` namespace. Using `moby`
   would couple us to Docker's internal conventions and risk conflicts. The import step
   (`docker save | containerd image import --namespace yoloai`) is acceptable overhead.

2. ~~**Snapshotter**~~ — **Resolved:** Auto-select based on isolation mode: `overlayfs` for `vm`
   (Kata+QEMU uses virtio-fs, no block device needed), `devmapper` for `vm-enhanced` (Firecracker
   requires a block device for the guest kernel). devmapper must be pre-provisioned by the operator
   (it needs a dedicated block/loop device for its thin pool — not something yoloai can set up at
   runtime). At container creation time, query containerd for available snapshotters; if the
   required one is absent, error with setup instructions rather than silently failing.

3. ~~**containerd socket location**~~ — **Resolved:** The containerd backend has no functional
   dependency on Docker. containerd is a standalone CNCF daemon; Docker merely bundles and manages
   its own instance. The socket defaults to `/run/containerd/containerd.sock` whether containerd
   was installed independently or via Docker. The one remaining optional Docker dependency is image
   building: `EnsureImage()` currently delegates to `docker build` for convenience. This is
   separable — BuildKit can be invoked directly without Docker if needed. For now, document that
   `--isolation vm`/`vm-enhanced` requires containerd (not Docker), and that image building additionally
   requires either Docker or a standalone BuildKit daemon.

4. ~~**gVisor on containerd**~~ — **Resolved:** Keep gVisor on the Docker backend indefinitely.
   gVisor implements the classic OCI runtime interface (`create/start/delete/kill`), so Docker's
   libnetwork correctly passes the netns path and there is no networking problem. The containerd
   shimv2 path for gVisor exists and is maintained by Google, but migrating provides no
   user-visible benefit and adds implementation risk. Revisit only if a concrete problem forces it.

5. ~~**Windows/WSL**~~ — **Resolved:** yoloAI runs inside WSL2 on Windows — there is no native
   Windows binary. From inside WSL2, yoloAI sees a Linux environment; Docker Desktop, Podman
   Desktop, and Rancher Desktop all expose their runtimes via Unix sockets inside WSL2 and work
   transparently for `container` and `container-enhanced` isolation.

   For `vm` and `vm-enhanced`, the real blocker is KVM, not CNI. Kata requires hardware
   virtualization; WSL2 runs inside Hyper-V, so nested virtualization must be explicitly enabled
   on the Windows host — it is not on by default and requires a custom WSL2 kernel on some
   configurations. This makes `vm`/`vm-enhanced` effectively unsupported for most Windows users.

   Handle it explicitly: at prerequisite check time, detect WSL2 (via `/proc/version` or
   `WSL_DISTRO_NAME`) and check for `/dev/kvm`; if absent, error early with a clear message
   rather than letting Kata fail cryptically deep in the stack:

   ```
   VM isolation mode on WSL2 requires nested virtualization.
   Enable it on the Windows host:
     1. In PowerShell (admin): Set-VMProcessor -VMName <your-wsl-vm> -ExposeVirtualizationExtensions $true
     2. Restart WSL: wsl --shutdown
   ```

   If `/dev/kvm` is present after enabling nested virtualization, CNI works normally (same Linux
   kernel, same paths as native Linux).

   **Rancher Desktop** is a Docker Desktop alternative that uses containerd internally (socket at
   `/run/k3s/containerd/containerd.sock`). For `container`/`container-enhanced`, users enable
   its Docker-compatible socket and yoloAI works as with Docker Desktop. For `vm`/`vm-enhanced`,
   Rancher Desktop's containerd is k3s-internal and does not have Kata configured — a standalone
   containerd with Kata is still required.

6. ~~**InteractiveExec dependency on nerdctl**~~ — **Resolved:** Implement native PTY from the
   start; skip the nerdctl shim entirely. The shim would introduce edge cases around TTY handling,
   namespace mismatch, error propagation, and version skew that would just be ripped out later
   anyway. Native containerd + PTY is the right implementation and the cost of doing it once is
   lower than the cost of doing it twice.

7. ~~**Seatbelt + gVisor gap**~~ — **Resolved:** `--os mac` + `--isolation container-enhanced` is
   unsupported; error at creation time with a clear message listing what is available:

   ```
   --isolation container-enhanced is not available on macOS.
   Available isolation modes with --os mac:
     container  macOS sandbox-exec (seatbelt)
     vm         Full macOS VM (Tart)
   ```

   If a gVisor equivalent for macOS emerges in the future, it can be added then.

---

## Prerequisites (User-Facing)

When `--isolation vm` or `vm-enhanced` is requested and the containerd backend is auto-selected, yoloAI
should check at startup:

**Runtime prerequisites** (always required):
- `containerd` socket is reachable at `/run/containerd/containerd.sock`
- `containerd-shim-kata-v2` (or kata-qemu-v2 variant) is in PATH
- CNI plugins exist at `/opt/cni/bin/`
- `vhost_net` kernel module is loaded
- (`vm-enhanced` only) devmapper snapshotter is configured in containerd

**Image build prerequisite** (required only when the yoloai image needs to be built or updated):
- `docker` is available, OR a standalone BuildKit daemon is reachable

Docker is not required to *run* containers in vm/vm-enhanced mode — only to build the base image.
If the image already exists in the `yoloai` containerd namespace, the build step is skipped
entirely and Docker is not needed.

If any runtime prerequisite is missing, a clear error with the fix:

```
VM isolation mode requires additional setup:
  - Kata Containers: https://github.com/kata-containers/kata-containers
  - CNI plugins: sudo apt install containernetworking-plugins
  - vhost_net: sudo modprobe vhost_net && echo vhost_net | sudo tee /etc/modules-load.d/vhost_net.conf
```

If the image build prerequisite is missing when a build is needed:

```
The yoloai base image needs to be built but neither Docker nor BuildKit is available.
Install Docker (https://docs.docker.com/engine/install/) or run a standalone BuildKit daemon.
```

---

## Alternatives Considered

### Alternative A: nerdctl as a thin wrapper backend

Implement `runtime/nerdctl/` that shells out to `nerdctl` for all operations, similar to how
`runtime/tart/` shells out to `tart`.

**Pros:** Simple to implement. nerdctl is well-tested with Kata.
**Cons:** nerdctl is a user-facing tool, not a stable API. Image namespace management is awkward
(`--namespace moby` or `--namespace default`?). Interactive exec via shell-out is already how
Docker works, so no regression there. But building images via `nerdctl build` requires buildkit
separately; using Docker build requires namespace bridging.

This is a viable near-term approach if the containerd client proves too complex, but it couples
yoloAI to nerdctl's CLI conventions rather than containerd's stable API.

### Alternative B: Fix Docker's shimv2 networking

File a Docker upstream issue / PR to pass the libnetwork netns path in the OCI spec for shimv2
runtimes. This would fix the root cause for all shimv2 runtimes (not just Kata).

**Pros:** No new backend needed.
**Cons:** Upstream timelines are uncertain. Kata + Docker is not a priority use case for Docker
maintainers (they focus on runc). Not actionable in the near term.

### Alternative C: Keep Docker backend, use nerdctl only for interactive exec

For kata security modes, use Docker SDK for all container operations except `InteractiveExec`
(which shells out to nerdctl). The networking problem remains unsolved.

Not viable — the networking problem is fundamental, not just an exec problem.

### Alternative D: Keep `--security` flag name, just rename values

Rename `gvisor` → `container-enhanced` and `kata`/`kata-firecracker` → `vm`/`vm-enhanced` but keep `--security`.

**Cons:** `--security` is still the wrong abstraction. The flag name implies a purely adversarial
concern (what are you defending against?) when the real tradeoff is isolation strength vs.
performance and compatibility. `--isolation` better captures the design space as a 2×2 matrix:
`container`/`container-enhanced` vs `vm`/`vm-enhanced`.

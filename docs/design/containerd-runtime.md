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
├── runtime/docker/        ← Docker SDK — standard, gVisor
├── runtime/podman/        ← Podman compat socket — rootless
├── runtime/containerd/    ← NEW: containerd API — kata, kata-firecracker
├── runtime/tart/          ← Tart VM — macOS
└── runtime/seatbelt/      ← sandbox-exec — macOS lightweight
```

The containerd backend is not a replacement for Docker — it is a parallel backend for security
modes that require it. The Docker backend continues to serve `standard` and `gvisor`.

### Security Mode → Backend Mapping

| `--security` value | Backend | Mechanism |
|--------------------|---------|-----------|
| *(omitted)*        | docker  | runc (default) |
| `standard`         | docker  | runc |
| `gvisor`           | docker  | runsc OCI runtime |
| `kata`             | containerd | `io.containerd.kata.v2` shimv2 + CNI |
| `kata-firecracker` | containerd | `io.containerd.kata-fc.v2` shimv2 + CNI + devmapper |

Users continue to specify `--security kata` — they never see or configure the backend.

### Auto-Selection

`resolveBackend()` in `cli/helpers.go` currently reads the `--backend` flag or config default.
Extend it to auto-select `containerd` when `--security kata` or `--security kata-firecracker` is
specified, without requiring the user to pass `--backend containerd`.

On `yoloai sandbox start/stop/destroy/exec`, the backend is read from `environment.json` (already
stored at creation time). No changes needed there.

## Containerd Backend Design

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

**Network isolation mode** (`--network isolated`): The containerd backend applies iptables rules
in the same way as the Docker backend — via the in-container entrypoint and ipset, which are
runtime-agnostic.

### OCI Runtime Selection

The containerd client's `WithRuntime` option maps to the shimv2 type:

```go
// kata
containerd.WithRuntime("io.containerd.kata.v2", &options.Options{
    ConfigPath: "/opt/kata/share/defaults/kata-containers/configuration-qemu.toml",
})

// kata-firecracker
containerd.WithRuntime("io.containerd.kata-fc.v2", &options.Options{
    ConfigPath: "/opt/kata/share/defaults/kata-containers/configuration-fc.toml",
})
```

### Interactive Exec

The Docker backend shells out to `docker exec -it`. The containerd backend would need to:

1. Call containerd's task `Exec()` API
2. Set up a PTY (using `github.com/creack/pty` or similar)
3. Pipe stdin/stdout/stderr

This is the most complex piece — nerdctl's exec implementation is a reference. Alternatively,
shell out to `nerdctl exec -it --namespace yoloai` if nerdctl is available, deferring this
complexity to a later iteration.

### Image Building

The containerd backend reuses the Docker backend's `EnsureImage()` for the build step, then
imports the result into the `yoloai` containerd namespace. The `yoloai-base` image is built once
and shared.

## Implementation Plan

### Phase 1: Spike (verify the approach)

1. Write a minimal Go program that uses the containerd client to:
   - Pull `alpine`
   - Run it with `io.containerd.kata.v2` runtime + CNI networking
   - Exec `ip addr` and verify `eth0` exists with an IP
2. If this works, proceed. If not, revisit the design.

### Phase 2: Containerd Backend (MVP)

Implement `runtime/containerd/` with:
- `New()` — connect to containerd socket, verify kata shim exists
- `Create()` — CNI setup + containerd container + task creation
- `Start()` — task start
- `Stop()` — task kill + cleanup
- `Remove()` — container delete
- `Inspect()` — task status
- `Exec()` — non-interactive exec
- `InteractiveExec()` — shell out to nerdctl (temporary)
- `EnsureImage()` — Docker build + import into yoloai namespace
- `Logs()` — read from bind-mounted log file (same as Docker backend)
- `Prune()` — remove orphaned yoloai-* containers

### Phase 3: Auto-Selection

Extend `resolveBackend()` to auto-select `containerd` for kata security modes.
Update `--security` validation to check for containerd prerequisites if kata is chosen.

### Phase 4: Native Interactive Exec

Replace the nerdctl shim for `InteractiveExec` with a native containerd + PTY implementation.

### Phase 5: Kata-Firecracker

Kata-Firecracker requires the devmapper snapshotter (block device backing) in containerd. This is
a separate setup step with a known issue on Ubuntu 24.04. Implement after Phase 2 is stable.

## Open Questions

1. **Image namespace strategy**: Should the containerd backend use Docker's `moby` namespace
   (avoiding the import step) or its own `yoloai` namespace (cleaner isolation)? Using `moby`
   risks coupling to Docker's internal conventions.

2. **Snapshotter**: containerd defaults to overlayfs. Kata-Firecracker requires devmapper. Should
   the backend select the snapshotter per-container based on security mode, or require a global
   configuration?

3. **containerd socket location**: Defaults to `/run/containerd/containerd.sock`. On systems where
   Docker manages containerd, this is correct. But if containerd isn't installed independently,
   the socket may not exist. Should yoloAI require containerd as a separate dependency, or try to
   share Docker's containerd instance?

4. **gVisor on containerd**: gVisor currently works via Docker's OCI runtime registration. Should
   gVisor eventually move to the containerd backend too, for consistency? Or keep it on Docker
   since it works?

5. **Windows/WSL**: The containerd backend assumes Linux CNI plugins and a Linux containerd socket.
   WSL2 has containerd but CNI setup differs. This needs a platform compatibility note.

6. **InteractiveExec dependency on nerdctl**: Phase 2 shells out to nerdctl for interactive exec.
   This creates a soft dependency. Should nerdctl be a required prerequisite for the containerd
   backend, or should Phase 4 (native PTY) be prioritized to eliminate it?

## Prerequisites (User-Facing)

When `--security kata` is requested and the containerd backend is auto-selected, yoloAI should
check at startup:

- `containerd` socket is reachable at `/run/containerd/containerd.sock`
- `containerd-shim-kata-v2` (or kata-qemu-v2 variant) is in PATH
- CNI plugins exist at `/opt/cni/bin/`
- `vhost_net` kernel module is loaded

If any prerequisite is missing, a clear error with the fix:

```
kata security mode requires additional setup:
  - Kata Containers: https://github.com/kata-containers/kata-containers
  - CNI plugins: sudo apt install containernetworking-plugins
  - vhost_net: sudo modprobe vhost_net && echo vhost_net | sudo tee /etc/modules-load.d/vhost_net.conf
```

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

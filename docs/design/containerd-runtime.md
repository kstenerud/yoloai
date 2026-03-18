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

Replace `--security` with `--isolation` (values are user-visible; backends are not):

| `--isolation` | What the user gets | Underlying backend |
|---------------|--------------------|-------------------|
| *(omitted)*   | Standard container isolation (runc) | docker |
| `standard`    | Standard container isolation (runc) | docker |
| `enhanced`    | Syscall-level sandbox (gVisor/runsc) | docker |
| `vm`          | Full VM isolation (Kata + QEMU)      | containerd |
| `vm-fc`       | Full VM isolation (Kata + Firecracker) | containerd |

Users say `--isolation vm`. They never see "containerd" or "kata" unless they read the docs.

**Why rename `gvisor` → `enhanced`?** The implementation name leaks. A user who doesn't know what
gVisor is has no idea what `--security gvisor` provides. `enhanced` is self-explanatory: more
isolation than the default, at a performance cost.

**Why rename `kata` → `vm`?** Same reason. `vm` is the concept (hardware VM boundary); Kata is
the implementation. Also future-proofs: if a better VM runtime replaces Kata, the flag stays stable.

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

`--os mac` selects the macOS-native backend; which one depends on `--isolation`:

| `--os mac` + `--isolation` | Backend | What the user gets |
|---------------------------|---------|-------------------|
| `standard` (default) | seatbelt | macOS sandbox-exec (lightweight, low overhead) |
| `vm`                 | tart    | Full macOS VM |
| `enhanced`           | —       | **Error**: gVisor is Linux-only |
| `vm-fc`              | —       | **Error**: Firecracker is Linux-only |

Seatbelt is the right default for `--os mac`: it's the macOS equivalent of a standard runc
container — process-level sandboxing, fast startup, no VM overhead. Tart is for users who want
full VM-level isolation or need a clean macOS environment.

**Isolation symmetry across platforms:**

| `--isolation` | Linux backend | macOS backend (`--os mac`) |
|---------------|---------------|---------------------------|
| `standard`    | docker (runc) | seatbelt (sandbox-exec)   |
| `enhanced`    | docker (gVisor) | *(unsupported)*          |
| `vm`          | containerd (Kata) | tart (macOS VM)        |
| `vm-fc`       | containerd (Kata+FC) | *(unsupported)*     |

**What happens to `--backend`?** It becomes a config-only escape hatch for power users and is
removed from the `yoloai new` help text. The common case is covered by `--isolation` + `--os`.
Platform maintainers (CI, DevOps) who need explicit backend control can still set it in `config.yaml`.

### Backwards Compatibility

No backwards compatibility requirement. `--security` is replaced by `--isolation`. Migration:

| Old flag | New flag |
|----------|----------|
| *(omitted)* | *(omitted)* |
| `--security standard` | `--isolation standard` |
| `--security gvisor` | `--isolation enhanced` |
| `--security kata` | `--isolation vm` |
| `--security kata-firecracker` | `--isolation vm-fc` |
| `--backend tart` | `--os mac` |
| `--backend seatbelt` | *(seatbelt is a lightweight Mac sandbox; revisit if --os mac + lightweight flag is needed)* |

### Backend Auto-Selection

`resolveBackend()` in `cli/helpers.go` currently reads `--backend` or config default. Extend it:

```
resolveBackend(isolation, os):
  if os == "mac":
    return tart
  switch isolation:
  case "vm", "vm-fc":
    return containerd
  default:
    return docker
```

On `yoloai sandbox start/stop/destroy/exec`, the backend is read from `environment.json` (stored at
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
| docker / standard | Yes | Works. Requires `CAP_SYS_ADMIN`. |
| docker / enhanced (gVisor) | No | gVisor does not support mounting overlayfs inside the container (no FUSE or unprivileged overlay). |
| containerd / vm (Kata) | No | The agent runs inside a VM. The overlayfs upper layer is inside the VM's guest filesystem, not visible to the containerd host. |
| containerd / vm-fc (Kata+Firecracker) | No | Same as vm. |
| tart (macOS VM) | No | Tart is a macOS VM. No overlayfs. |
| seatbelt | No | No container filesystem. |

**Rule:** `:overlay` only works with `docker` backend + `standard` isolation (default or explicit).

**User experience:** If a user specifies `:overlay` with an incompatible combination, yoloAI errors
at creation time with a clear message:

```
Error: :overlay directories require --isolation standard (docker backend).
       --isolation enhanced uses gVisor, which does not support overlayfs inside the container.
       Use :copy instead, or switch to --isolation standard.
```

**Documentation strategy:** `:overlay` is documented in the main guide with a short caveat:

> `:overlay` is a space-efficient alternative to `:copy` that uses Linux overlayfs for instant
> directory setup. It requires standard isolation (Docker, Linux host) and `CAP_SYS_ADMIN`.
> It does not work with enhanced or VM isolation modes, or on macOS. If you're unsure, use `:copy`.

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
├── runtime/docker/        ← Docker SDK — standard, enhanced (gVisor)
├── runtime/podman/        ← Podman compat socket — rootless
├── runtime/containerd/    ← NEW: containerd API — vm (kata), vm-fc (kata-firecracker)
├── runtime/tart/          ← Tart VM — macOS
└── runtime/seatbelt/      ← sandbox-exec — macOS lightweight
```

The containerd backend is not a replacement for Docker — it is a parallel backend for isolation
modes that require it. The Docker backend continues to serve `standard` and `enhanced`.

### Isolation Mode → Backend Mapping

| `--isolation` value | Backend | Mechanism |
|---------------------|---------|-----------|
| *(omitted)*         | docker  | runc (default) |
| `standard`          | docker  | runc |
| `enhanced`          | docker  | runsc (gVisor) |
| `vm`                | containerd | `io.containerd.kata.v2` shimv2 + CNI |
| `vm-fc`             | containerd | `io.containerd.kata-fc.v2` shimv2 + CNI + devmapper |

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
// vm (kata-qemu)
containerd.WithRuntime("io.containerd.kata.v2", &options.Options{
    ConfigPath: "/opt/kata/share/defaults/kata-containers/configuration-qemu.toml",
})

// vm-fc (kata-firecracker)
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

---

## Implementation Plan

### Phase 1: Rename `--security` → `--isolation`

1. Rename the flag everywhere (CLI, config, meta.json, docs).
2. Map new value names to backend/runtime selection.
3. Add `--os` flag; route `mac` to Tart backend.
4. Remove `--backend` from `yoloai new` help text (keep in config as escape hatch).
5. Add early error for `:overlay` + incompatible isolation/os combination.

No backwards compatibility handling needed.

### Phase 2: Containerd Backend Spike (verify the approach)

1. Write a minimal Go program that uses the containerd client to:
   - Pull `alpine`
   - Run it with `io.containerd.kata.v2` runtime + CNI networking
   - Exec `ip addr` and verify `eth0` exists with an IP
2. If this works, proceed. If not, revisit the design.

### Phase 3: Containerd Backend (MVP)

Implement `runtime/containerd/` with:
- `New()` — connect to containerd socket, verify kata shim exists
- `Create()` — CNI setup + containerd container + task creation
- `Start()` — task start
- `Stop()` — task kill + cleanup
- `Remove()` — container delete
- `Inspect()` — task status
- `Exec()` — non-interactive exec
- `InteractiveExec()` — native containerd task Exec() + PTY (`github.com/creack/pty`)
- `EnsureImage()` — Docker build + import into yoloai namespace
- `Logs()` — read from bind-mounted log file (same as Docker backend)
- `Prune()` — remove orphaned yoloai-* containers

### Phase 4: Auto-Selection

Extend `resolveBackend()` to auto-select `containerd` for `vm`/`vm-fc` isolation modes.
Update isolation validation to check for containerd prerequisites if vm is chosen.

### Phase 5: vm-fc (Kata + Firecracker)

Kata-Firecracker requires the devmapper snapshotter (block device backing) in containerd. This is
a separate setup step with a known issue on Ubuntu 24.04. Implement after Phase 3 is stable.

---

## Open Questions

1. ~~**Image namespace strategy**~~ — **Resolved:** Use the `yoloai` namespace. Using `moby`
   would couple us to Docker's internal conventions and risk conflicts. The import step
   (`docker save | containerd image import --namespace yoloai`) is acceptable overhead.

2. ~~**Snapshotter**~~ — **Resolved:** Auto-select based on isolation mode: `overlayfs` for `vm`
   (Kata+QEMU uses virtio-fs, no block device needed), `devmapper` for `vm-fc` (Firecracker
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
   `--isolation vm`/`vm-fc` requires containerd (not Docker), and that image building additionally
   requires either Docker or a standalone BuildKit daemon.

4. ~~**gVisor on containerd**~~ — **Resolved:** Keep gVisor on the Docker backend indefinitely.
   gVisor implements the classic OCI runtime interface (`create/start/delete/kill`), so Docker's
   libnetwork correctly passes the netns path and there is no networking problem. The containerd
   shimv2 path for gVisor exists and is maintained by Google, but migrating provides no
   user-visible benefit and adds implementation risk. Revisit only if a concrete problem forces it.

5. ~~**Windows/WSL**~~ — **Resolved:** The real blocker on WSL2 is not CNI but KVM. Kata requires
   hardware virtualization; WSL2 runs inside Hyper-V, so nested virtualization must be explicitly
   enabled on the Windows host. If `/dev/kvm` is present, CNI works normally (same Linux kernel,
   same paths). Handle automatically: at prerequisite check time, detect WSL2 (via
   `/proc/version` or `WSL_DISTRO_NAME`) and check for `/dev/kvm`; if absent, error early with
   a clear message rather than letting Kata fail cryptically deep in the stack:

   ```
   VM isolation mode on WSL2 requires nested virtualization.
   Enable it on the Windows host:
     1. In PowerShell (admin): Set-VMProcessor -VMName <your-wsl-vm> -ExposeVirtualizationExtensions $true
     2. Restart WSL: wsl --shutdown
   ```

   `--isolation standard` and `enhanced` are unaffected — those use the Docker backend, which
   Docker Desktop manages transparently on Windows.

6. ~~**InteractiveExec dependency on nerdctl**~~ — **Resolved:** Implement native PTY from the
   start; skip the nerdctl shim entirely. The shim would introduce edge cases around TTY handling,
   namespace mismatch, error propagation, and version skew that would just be ripped out later
   anyway. Native containerd + PTY is the right implementation and the cost of doing it once is
   lower than the cost of doing it twice.

7. ~~**Seatbelt + gVisor gap**~~ — **Resolved:** `--os mac` + `--isolation enhanced` is
   unsupported; error at creation time with a clear message listing what is available:

   ```
   --isolation enhanced is not available on macOS.
   Available isolation modes with --os mac:
     standard  macOS sandbox-exec (seatbelt)
     vm        Full macOS VM (Tart)
   ```

   If a gVisor equivalent for macOS emerges in the future, it can be added then.

---

## Prerequisites (User-Facing)

When `--isolation vm` or `vm-fc` is requested and the containerd backend is auto-selected, yoloAI
should check at startup:

**Runtime prerequisites** (always required):
- `containerd` socket is reachable at `/run/containerd/containerd.sock`
- `containerd-shim-kata-v2` (or kata-qemu-v2 variant) is in PATH
- CNI plugins exist at `/opt/cni/bin/`
- `vhost_net` kernel module is loaded
- (`vm-fc` only) devmapper snapshotter is configured in containerd

**Image build prerequisite** (required only when the yoloai image needs to be built or updated):
- `docker` is available, OR a standalone BuildKit daemon is reachable

Docker is not required to *run* containers in vm/vm-fc mode — only to build the base image.
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

Rename `gvisor` → `enhanced` and `kata`/`kata-firecracker` → `vm`/`vm-fc` but keep `--security`.

**Cons:** `--security` is still the wrong abstraction. The flag name implies a purely adversarial
concern (what are you defending against?) when the real tradeoff is isolation strength vs.
performance and compatibility. `--isolation` better captures the spectrum: standard → enhanced → vm.

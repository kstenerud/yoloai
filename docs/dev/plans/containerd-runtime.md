# Containerd Runtime Implementation Plan

Design spec: [`docs/design/containerd-runtime.md`](../../design/containerd-runtime.md)

This is an iterative plan — phases are ordered by dependency, not by size. Each phase produces
working, shippable code. No backwards compatibility handling: this is beta, and existing sandboxes
using `--security` will break.

---

## Phase 0: Spike — verify containerd + Kata + CNI end-to-end

**Status: Complete.** The spike was run on the test VM (Ubuntu 24.04, containerd v2.2.2,
Kata 3.28). Key findings that affect the rest of this plan:

- **Module path:** Use `github.com/containerd/containerd/v2@v2.2.2` (not v1). All internal
  package paths changed: `pkg/namespaces`, `pkg/oci`, `pkg/cio` (see Step 2.1 for full list).
- **CNI plugin path:** Confirmed at `/opt/cni/bin/`.
- **Kata shimv2 type:** `io.containerd.kata.v2` — confirmed matches `/usr/bin/containerd-shim-kata-v2`.
- **Kata + overlayfs:** Works. `client.WithSnapshotter("overlayfs")` with
  `client.WithRuntime("io.containerd.kata.v2", nil)` runs a container successfully.
- **Task persistence:** Containerd tasks survive the calling process's exit — the shim manages
  them. A new process reconnects via `ctr.LoadContainer()` + `ctr.Task(ctx, nil)`.
- **Detach model:** `Start()` can return after `task.Start()` without calling `Wait()`. The task
  keeps running. `Stop()` reconnects and kills it. No background goroutine needed.
- **CNI netns:** `go-cni.Setup()` takes an existing netns path — the caller must create the
  namespace. Use `github.com/vishvananda/netns` (pure Go, same approach as nerdctl).

---

## Phase 1: Flag restructuring (no new backend)

Replace `--security` with `--isolation`, add `--os`, rename `container_backend` config key,
update `Meta.Isolation`. No containerd backend yet — `--isolation vm` will error with "not yet
implemented" at the end of this phase.

This phase is a breaking change and should be committed as a single logical unit after
`BREAKING-CHANGES.md` is updated.

### Step 1.1 — Config struct: rename `Backend` → `ContainerBackend`, `Security` → `Isolation`

**File: `config/config.go`**

```go
type YoloaiConfig struct {
    ContainerBackend   string `yaml:"container_backend"` // was: Backend / "backend"
    // ...
    Isolation          string `yaml:"isolation"`          // was: Security / "security"
    // ...
}
```

Update `knownSettings`:
```go
{"container_backend", ""},   // was: {"backend", "docker"} — now empty; auto-detect is the default
// ...
{"isolation", ""},           // was: {"security", ""}
```

Note: default for `container_backend` is now `""` (auto-detect), not `"docker"`. The `"docker"`
default moves into `resolveBackend()` as a fallback in auto-detection.

**File: `config/defaults.go`**

Update `DefaultConfigYAML` to use `container_backend:` and `isolation:` keys.

### Step 1.2 — Config validation: rename `ValidateSecurityMode` → `ValidateIsolationMode`

**File: `config/config.go` (or wherever `ValidateSecurityMode` lives)**

```go
func ValidateIsolationMode(mode string) error {
    switch mode {
    case "", "container", "container-enhanced", "vm", "vm-enhanced":
        return nil
    default:
        return fmt.Errorf("unknown isolation mode %q (valid: container, container-enhanced, vm, vm-enhanced)", mode)
    }
}
```

### Step 1.3 — Meta struct: `Security` → `Isolation`

**File: `sandbox/meta.go`**

```go
type Meta struct {
    // ...
    Isolation string `json:"isolation,omitempty"` // was: Security / "security"
    // ...
}
```

Remove the `Security` field entirely — no migration, no fallback. Existing sandboxes that were
created with `--security` will be broken; users recreate them.

### Step 1.4 — `sandbox/inspect.go`: update `Perms()` and status detection

`Perms(security string)` → `Perms(isolation string)`. Update the gVisor check:

```go
func Perms(isolation string) IsolationPerms {
    if isolation == "container-enhanced" {  // was: "gvisor"
        return IsolationPerms{Dir: 0777, ...}
    }
    return IsolationPerms{Dir: 0750, ...}
}
```

Update the user detection check (line ~158) similarly.

### Step 1.5 — `sandbox/create.go`: rename `securityRuntimeName` → `isolationContainerRuntime`

```go
func isolationContainerRuntime(isolation string) string {
    switch isolation {
    case "container-enhanced":
        return "runsc"
    case "vm":
        return "io.containerd.kata.v2"
    case "vm-enhanced":
        return "io.containerd.kata-fc.v2"
    default:
        return ""
    }
}
```

Update all references from `state.security` → `state.isolation` and `pr.security` →
`pr.isolation` throughout `create.go` and `create_prepare.go`.

Update the overlay incompatibility check. The existing gVisor check becomes an isolation check,
and a new containerd check is added (VM backends don't support overlayfs):
```go
// container-enhanced (gVisor) does not support overlayfs inside the container
if state.isolation == "container-enhanced" && hasOverlayDirs(state) {
    return fmt.Errorf(":overlay directories require --isolation container. " +
        "--isolation container-enhanced uses gVisor, which does not support overlayfs inside the container.")
}
```

Replace `isContainerBackend()` with `BackendCaps` — see Step 1.5b below. After that refactor, all
four `isContainerBackend` call sites in `launchContainer()` become `caps.<field>` references, and
containerd's capabilities are declared once in `backendCaps()` rather than scattered across
if-chains.

Also remove the `dockerNetworkMode` variable (create.go lines 678–681). Pass `state.networkMode`
directly to `InstanceConfig.NetworkMode`. Move the `"isolated"` → `""` translation into the Docker
runtime's `Create()` method, where it belongs. Containerd's `Create()` reads `"isolated"` directly
to decide whether to configure CNI network filtering.

Update `checkSecurityRuntime` → `checkIsolationPrerequisites` stub (full implementation in Step 2.9).

### Step 1.5b — `sandbox/create.go`: replace `isContainerBackend()` with `BackendCaps`

**New type in `sandbox/create.go`:**

```go
// BackendCaps describes what a runtime backend supports in launchContainer.
type BackendCaps struct {
    NetworkIsolation bool // supports --network=isolated (iptables-based domain filtering)
    OverlayDirs      bool // supports :overlay mount mode (overlayfs inside the container)
    CapAdd           bool // supports cap_add and devices via OCI spec
}

func backendCaps(backend string) BackendCaps {
    switch backend {
    case "docker", "podman":
        return BackendCaps{NetworkIsolation: true, OverlayDirs: true, CapAdd: true}
    case "containerd":
        return BackendCaps{NetworkIsolation: true, OverlayDirs: false, CapAdd: true}
    default: // tart, seatbelt
        return BackendCaps{}
    }
}
```

Delete `isContainerBackend()`. Add `caps := backendCaps(m.backend)` at the top of
`launchContainer()`. Replace each usage site:

| Old | New |
|---|---|
| `!isContainerBackend(m.backend)` (network isolation error, line 671) | `!caps.NetworkIsolation` |
| `isContainerBackend(m.backend)` (NET_ADMIN cap, line 707) | `caps.NetworkIsolation` |
| `!isContainerBackend(m.backend)` (overlay gate, line 724) | `!caps.OverlayDirs` |
| `!isContainerBackend(m.backend)` (cap_add/devices/setup gate, line 731) | `!caps.CapAdd` |

The `isContainerBackend` gate at line 739 (security/runtime validation) is replaced entirely
by `IsolationValidator` — see below. **`ContainerRuntime` is set unconditionally** before
validation, then the validator (per-backend via interface) decides whether prerequisites are
met. Replace the entire lines 738–752 block with:

```go
// Set the runtime identifier for both Docker (OCI --runtime name) and containerd (shimv2 type).
// isolationContainerRuntime returns "" for container/container isolation where the default suffices.
if runtimeName := isolationContainerRuntime(state.isolation); runtimeName != "" {
    instanceCfg.ContainerRuntime = runtimeName
}
// Validate that isolation prerequisites are met (delegates to runtime.IsolationValidator).
if err := checkIsolationPrerequisites(ctx, m.runtime, state.isolation); err != nil {
    return err
}
```

`checkIsolationPrerequisites` is a thin delegator in create.go:
```go
func checkIsolationPrerequisites(ctx context.Context, rt runtime.Runtime, isolation string) error {
    v, ok := rt.(runtime.IsolationValidator)
    if !ok {
        return nil
    }
    return v.ValidateIsolation(ctx, isolation)
}
```

Update the network isolation error message to avoid naming specific backends:
```go
return fmt.Errorf("--network=isolated is not supported by the %s backend", m.backend)
```

### Step 1.6 — `sandbox/lifecycle.go`: update all `meta.Security` references

Replace all `meta.Security` / `Perms(meta.Security)` / `state.security` with the new field name
and isolation values. Mechanical find-and-replace.

### Step 1.7 — CLI: replace `--security` with `--isolation`, add `--os`

**File: `internal/cli/commands.go`**

Remove:
```go
cmd.Flags().String("security", "", "OCI runtime security mode (standard, gvisor, kata, kata-firecracker)")
```

Add:
```go
cmd.Flags().String("isolation", "", "Isolation mode: container (default), container-enhanced, vm, vm-enhanced")
cmd.Flags().String("os", "", "Target OS: linux (default), mac")
```

Replace the macOS gVisor block:
```go
isolation, _ := cmd.Flags().GetString("isolation")
os, _ := cmd.Flags().GetString("os")
if os == "mac" && (isolation == "container-enhanced" || isolation == "vm-enhanced") {
    return sandbox.NewUsageError(fmt.Sprintf("--isolation %s is not available on macOS.\n"+
        "Available isolation modes with --os mac:\n"+
        "  container   macOS sandbox-exec (seatbelt)\n"+
        "  vm          Full macOS VM (Tart)", isolation))
}
```

Update `CreateOptions` and `sandbox/create_prepare.go` to read `Isolation` instead of `Security`.

### Step 1.8 — `resolveBackend()` overhaul

**File: `internal/cli/helpers.go`**

Add lightweight availability probes (no full client construction):

```go
// dockerAvailable returns true if the Docker socket is reachable.
func dockerAvailable() bool {
    // check $DOCKER_HOST or /var/run/docker.sock
}

// podmanAvailable returns true if a Podman socket is reachable.
func podmanAvailable() bool {
    // reuse podmanrt.SocketPath() or equivalent — stat only, no dial
}
```

**`podmanAvailable()` implementation note:** `podmanrt.discoverSocket()` returns an error if
no socket is found and surfaces the error to callers. Add a `SocketPath() (string, error)` to
`runtime/podman` that returns the first reachable socket path without logging, and use that.

Overhaul `resolveBackend()` to accept isolation and os, and return `(backend string, explicit bool)`:

```go
func resolveBackend(cmd *cobra.Command) (backend string, explicit bool) {
    isolation, _ := cmd.Flags().GetString("isolation")
    targetOS, _ := cmd.Flags().GetString("os")
    backendFlag, _ := cmd.Flags().GetString("backend")

    return resolveBackendFull(isolation, targetOS, backendFlag, backendFlag != "")
}

func resolveBackendFull(isolation, targetOS, backendFlag string, backendFlagExplicit bool) (string, bool) {
    // macOS path
    if targetOS == "mac" {
        switch isolation {
        case "vm":
            return "tart", true
        case "container-enhanced", "vm-enhanced":
            // error handled upstream in commands.go
            return "seatbelt", false
        default:
            return "seatbelt", false
        }
    }

    // vm/vm-enhanced: always containerd
    if isolation == "vm" || isolation == "vm-enhanced" {
        return "containerd", false
    }

    // container/container-enhanced: honour explicit --backend, then container_backend config, then auto-detect
    if backendFlagExplicit {
        return backendFlag, true
    }

    return detectContainerBackend(resolveContainerBackendConfig()), false
}

func detectContainerBackend(preference string) string {
    if preference == "podman" {
        if podmanAvailable() {
            return "podman"
        }
        fmt.Fprintf(os.Stderr, "Warning: container_backend=podman not found; falling back to docker\n")
    }
    if dockerAvailable() {
        return "docker"
    }
    if preference == "docker" {
        fmt.Fprintf(os.Stderr, "Warning: container_backend=docker not found; falling back to podman\n")
    }
    if podmanAvailable() {
        return "podman"
    }
    return "docker" // will fail hard in newRuntime() with a clear error
}

func resolveContainerBackendConfig() string {
    cfg, err := config.LoadConfig()
    if err == nil {
        return cfg.ContainerBackend
    }
    return ""
}
```

Update callers: `resolveBackend()` now returns `(string, bool)`. Update `withRuntime()` /
`withManager()` call sites accordingly.

`resolveBackendForSandbox()` is unchanged — it reads from `meta.Backend` which is set at
creation time and doesn't participate in auto-detection.

### Step 1.9 — `internal/cli/info.go`: add containerd placeholder to `knownBackends`

```go
{Name: "containerd", Description: "Linux VMs (Kata Containers) — requires vm/vm-enhanced isolation", ...}
```

Mark as unavailable if `/run/containerd/containerd.sock` doesn't exist.

### Step 1.10 — Add WSL2 Podman socket paths

**File: `runtime/podman/podman.go`** — add to `discoverSocket()`:

```go
// Podman Desktop on Windows (WSL2 machine provider)
wslPaths := []string{
    "/mnt/wsl/podman-sockets/podman-machine-default/podman-root.sock",
    "/mnt/wsl/podman-sockets/podman-machine-default/podman-user.sock",
}
for _, p := range wslPaths {
    if _, err := os.Stat(p); err == nil {
        return "unix://" + p, nil
    }
}
```

### Step 1.11 — `newRuntime()`: add containerd stub

**File: `internal/cli/helpers.go`**

```go
case "containerd":
    return nil, fmt.Errorf("containerd backend not yet implemented — use --isolation container or container-enhanced")
```

This makes Phase 1 self-consistent: `--isolation vm` resolves to containerd but errors cleanly.

### Step 1.12 — `BREAKING-CHANGES.md`

Document:
- `--security` removed; replaced by `--isolation` with new value names
- `backend:` config key renamed to `container_backend:` (no migration)
- `security` field in `environment.json` renamed to `isolation` with new values; existing sandboxes must be recreated
- `--security standard` → omit `--isolation` (container is the default)
- `--security gvisor` → `--isolation container-enhanced`
- `--security kata` → `--isolation vm`
- `--security kata-firecracker` → `--isolation vm-enhanced`

---

## Phase 2: Containerd backend — MVP (vm isolation)

Implement `runtime/containerd/` to support `--isolation vm` (Kata + QEMU). Firecracker (`vm-enhanced`) deferred to Phase 3.

### Step 2.1 — Add Go dependencies

```
go get github.com/containerd/containerd/v2@v2.2.2
go get github.com/containerd/containerd/api@v1.10.0
go get github.com/containerd/go-cni@v1.1.13
go get github.com/vishvananda/netns@latest
go get github.com/creack/pty@latest
```

**Module path is `github.com/containerd/containerd/v2` — not `v1`.** The installed daemon is
v2.2.2 and the v2 module has a different import path. Key packages used from this module:

| Purpose | Import path |
|---|---|
| Client | `github.com/containerd/containerd/v2/client` |
| OCI spec | `github.com/containerd/containerd/v2/pkg/oci` |
| IO (FIFOs) | `github.com/containerd/containerd/v2/pkg/cio` |
| Namespaces | `github.com/containerd/containerd/v2/pkg/namespaces` |
| Runtime options | `github.com/containerd/containerd/api/types/runtimeoptions/v1` |

**`github.com/containerd/containerd/api`** is a separate Go module (not a subdirectory of the
main client module) that provides protobuf types including `runtimeoptions.Options` for passing
kata configuration paths. It must be added explicitly.

**`github.com/containerd/go-cni`** is the CNI library used by nerdctl. Use it in preference to
the lower-level `github.com/containernetworking/cni/libcni` directly.

Run `go mod tidy` after adding all dependencies.

### Step 2.2 — `runtime/containerd/containerd.go` — struct, `New()`, `Name()`, `Close()`

```go
package containerdrt

import (
    "github.com/containerd/containerd/v2/client"
    "github.com/containerd/containerd/v2/pkg/namespaces"
)

type Runtime struct {
    client    *client.Client
    namespace string // always "yoloai"
}

func New(ctx context.Context) (*Runtime, error) {
    c, err := client.New("/run/containerd/containerd.sock")
    if err != nil {
        return nil, fmt.Errorf("connect to containerd: %w", err)
    }
    // Note: shim and CNI prerequisite checks are done in ValidateIsolation() at yoloai-new time,
    // not here — New() is called after validation passes and should only fail if the daemon
    // itself is unreachable.
    return &Runtime{client: c, namespace: "yoloai"}, nil
}

func (r *Runtime) withNamespace(ctx context.Context) context.Context {
    return namespaces.WithNamespace(ctx, r.namespace)
}

func (r *Runtime) Name() string { return "containerd" }
func (r *Runtime) Close() error { return r.client.Close() }
```

**Every containerd API call must carry the namespace in context** — use `r.withNamespace(ctx)`
at the start of each method.

### Step 2.3 — `runtime/containerd/cni.go` — CNI setup, teardown, state

**CNI netns creation:** `go-cni.Setup()` takes an already-existing netns path — it does not
create the namespace. The caller must create it first using `github.com/vishvananda/netns`:

```go
// createNetNS creates a named network namespace and returns its path.
// The namespace is created at /var/run/netns/<name> (standard Linux path).
func createNetNS(name string) (string, error) {
    // netns.NewNamed creates /var/run/netns/<name> and returns an fd
    ns, err := netns.NewNamed(name)
    if err != nil {
        return "", fmt.Errorf("create netns %s: %w", name, err)
    }
    ns.Close()
    return fmt.Sprintf("/var/run/netns/%s", name), nil
}

func deleteNetNS(name string) error {
    return netns.DeleteNamed(name)
}
```

**CNI configuration:** Use a shared config at `~/.yoloai/cni/yoloai.conflist` (written once on
first use). Do not write per-sandbox CNI configs — the config is the same for all containers.
Load it via `go-cni.New(cni.WithPluginConfDir(cniConfDir), cni.WithPluginDir([]string{"/opt/cni/bin"}))`.

CNI state persisted at `~/.yoloai/sandboxes/<name>/backend/cni-state.json`:

```json
{
  "netns_name": "yoloai-<name>",
  "netns_path": "/var/run/netns/yoloai-<name>",
  "interface": "eth0",
  "ip": "10.88.0.5/16"
}
```

```go
// setupCNI creates a network namespace, runs CNI ADD, persists state for teardown.
func setupCNI(sandboxDir, containerName string) (netnsPath string, err error) {
    nsName := "yoloai-" + containerName
    netnsPath, err = createNetNS(nsName)
    if err != nil {
        return "", err
    }
    // If CNI ADD fails, delete the netns to avoid leaking it.
    if err = runCNIAdd(netnsPath, sandboxDir, containerName); err != nil {
        deleteNetNS(nsName)
        return "", fmt.Errorf("CNI setup: %w", err)
    }
    // persist cni-state.json
    return netnsPath, nil
}

// teardownCNI reads cni-state.json and runs CNI DEL to release resources.
// Idempotent: no-op if cni-state.json doesn't exist.
func teardownCNI(sandboxDir string) error
```

### Step 2.4 — `runtime/containerd/lifecycle.go` — Create, Start, Stop, Remove, Inspect

**Task lifecycle model** (verified by spike):
- `task.Start()` + return: the shim manages the running container. The calling process can exit;
  the task keeps running.
- After task exit: the task moves to STOPPED state in containerd. It is NOT auto-deleted.
  `task.Delete()` must be called explicitly to clean up.
- Reconnect: `ctr.Task(ctx, nil)` loads an existing task (nil = don't reattach IO) from any
  process. This is how `Stop()` and `Inspect()` access tasks started by a previous `Start()`.

**`Create()`:**
1. `setupCNI(sandboxDir, name)` → get `netnsPath`
2. Look up image: `r.client.GetImage(ctx, cfg.ImageRef)` — error if not found with message
   "image not found; run 'yoloai setup' to build it". `Create()` does not pull; image
   management is owned by `EnsureImage()`.
3. Determine snapshotter based on runtime:
   ```go
   snapshotter := "overlayfs"  // for vm (Kata + QEMU)
   if cfg.ContainerRuntime == "io.containerd.kata-fc.v2" {
       snapshotter = "devmapper"  // for vm-enhanced (Kata + Firecracker)
   }
   ```
4. Create OCI spec with `netnsPath` set in the network namespace options, plus mounts,
   capabilities, and runtime
5. `client.NewContainer()` with:
   - `client.WithSnapshotter(snapshotter)`
   - `client.WithRuntime(cfg.ContainerRuntime, kataOpts)` where `kataOpts` is a
     `*runtimeoptions.Options{ConfigPath: "..."}` pointing to the appropriate kata config
   - `client.WithNewSpec(oci.WithImageConfig(img), oci.WithProcessArgs(...), ...)`

**Kata config path selection** (pass via `runtimeoptions.Options{ConfigPath: "..."}`):

| `ContainerRuntime` | ConfigPath |
|---|---|
| `"io.containerd.kata.v2"` | `/opt/kata/share/defaults/kata-containers/configuration-qemu.toml` |
| `"io.containerd.kata-fc.v2"` | `/opt/kata/share/defaults/kata-containers/configuration-fc.toml` |

Import: `runtimeoptions "github.com/containerd/containerd/api/types/runtimeoptions/v1"`.
Passing `nil` for options uses whatever default is registered in containerd's `config.toml` —
prefer explicit paths to avoid depending on containerd's registration.

**`Start()`:**
1. Load container: `ctr, err := r.client.LoadContainer(ctx, name)`
2. Check for an existing task — a stopped task must be deleted before creating a new one:
   ```go
   if existingTask, err := ctr.Task(ctx, nil); err == nil {
       status, _ := existingTask.Status(ctx)
       if status.Status == containerd.Running {
           return nil // already running, nothing to do
       }
       // task exists but stopped — delete it before creating a new task
       existingTask.Delete(ctx)
   }
   ```
3. Create task with null IO (agent logs go to bind-mounted `log.txt`, not containerd stdio):
   ```go
   task, err := ctr.NewTask(ctx, cio.NullIO)
   ```
4. `task.Start(ctx)` — shim takes over, process keeps running after this function returns
5. Return — no `task.Wait()` call. The task persists in containerd managed by the shim.

**`Stop()`:**
1. Load container; if not found, return nil (idempotent)
2. Load task via `ctr.Task(ctx, nil)`; if no task exists, skip to teardown
3. Subscribe to exit events and check status — **`task.Wait()` must be called before
   `task.Kill()`** to avoid missing the exit event:
   ```go
   exitCh, err := task.Wait(ctx)  // subscribe first
   if err != nil {
       return fmt.Errorf("wait task: %w", err)
   }
   status, _ := task.Status(ctx)
   if status.Status != containerd.Stopped {
       task.Kill(ctx, syscall.SIGTERM)
       // wait up to 10s, then SIGKILL
       select {
       case <-exitCh:
       case <-time.After(10 * time.Second):
           task.Kill(ctx, syscall.SIGKILL)
           <-exitCh
       }
   }
   ```
4. `task.Delete(ctx)` — always, whether it was running or already stopped
5. `teardownCNI(sandboxDir)` — idempotent if already torn down

**`Remove()`:**
1. Stop if running (idempotent)
2. `container.Delete(ctx, client.WithSnapshotCleanup)`
3. `teardownCNI(sandboxDir)` (idempotent if already done in Stop)

**`Inspect()`:**
1. Load container; if not found return `ErrNotFound`
2. Load task via `ctr.Task(ctx, nil)`; if not found return `InstanceInfo{Running: false}`
3. Check `task.Status(ctx)` — return `Running: status.Status == containerd.Running`

Note: a task in STOPPED state (natural exit, not yet reaped) returns `Running: false`.
This is correct — `Start()` will clean it up on the next restart.

### Step 2.5 — `runtime/containerd/exec.go` — Exec, InteractiveExec

**`Exec()`** — non-interactive, captures stdout:
1. Load container and task
2. Create a process spec for the exec (`specs.Process{Args: cmd, ...}`)
3. Create a `cio.Creator` that captures stdout/stderr to a buffer:
   ```go
   ioCreator := cio.NewCreator(cio.WithStreams(nil, &stdout, &stderr))
   ```
4. `process, err := task.Exec(ctx, execID, processSpec, ioCreator)`
5. `exitCh, _ := process.Wait(ctx)` then `process.Start(ctx)`
6. `<-exitCh` to get exit status
7. Return `ExecResult`

**`InteractiveExec()`** — PTY-attached. The shim creates a PTY inside the container and
bridges it to named FIFOs. On the host side, attach `os.Stdin`/`os.Stdout` directly to those
FIFOs — no host PTY pair needed. `creack/pty` is used only for raw mode and terminal size,
not for opening a PTY.

1. Set raw mode on the host terminal:
   ```go
   oldState, err := pty.MakeRaw(int(os.Stdin.Fd()))
   if err != nil { return err }
   defer pty.Restore(int(os.Stdin.Fd()), oldState)
   ```
2. Create a FIFO set with terminal flag in a temp dir:
   ```go
   fifoDir, _ := os.MkdirTemp("", "yoloai-exec-")
   defer os.RemoveAll(fifoDir)
   fifoSet, _ := cio.NewFIFOSetInDir(fifoDir, execID, true /* terminal */)
   ```
3. Attach using the real stdin/stdout — not a PTY pair:
   ```go
   ioAttach := cio.NewAttach(cio.WithTerminal, cio.WithStreams(os.Stdin, os.Stdout, nil))
   ```
4. Create process spec with `Terminal: true`
5. `exitCh, _ := process.Wait(ctx)` then `process.Start(ctx)` then
   `process.ResizePTY(ctx, initialCols, initialRows)` — send initial size after start, not before
6. Forward SIGWINCH in a goroutine: `process.ResizePTY(ctx, cols, rows)`
7. `<-exitCh` to wait for exit — raw mode is restored by the deferred call above

Reference: nerdctl's `pkg/taskutil/taskutil.go`. This is a non-trivial pattern — copy it
rather than inventing a new approach.

### Step 2.6 — `runtime/containerd/image.go` — EnsureImage, ImageExists

**`ImageExists()`:** check `r.client.GetImage(ctx, "yoloai-base")` in the `yoloai` namespace.

**`EnsureImage()`:** shell-exec the build pipeline:
```go
// 1. Check if image already exists — skip build if so and force=false
// 2. docker build -t yoloai-base <sourceDir>
// 3. docker save yoloai-base | ctr -n yoloai images import -
// If docker unavailable: error with the user-facing message from design doc
```

Keep this as shell-exec to avoid a Go import dependency on `runtime/docker`.

### Step 2.7 — `runtime/containerd/prune.go` — Prune

List all containers in the `yoloai` namespace. Remove any named `yoloai-*` that are not in
`knownInstances`. For each removed container, run `teardownCNI`.

### Step 2.8 — `runtime/containerd/logs.go` — Logs, DiagHint

**`Logs()`:** same as Docker backend — read from the bind-mounted `log.txt` in the sandbox dir.
The containerd backend does not use the containerd log API (logs go to the file via the agent).

**`DiagHint()`:**
```
check containerd task status: ctr -n yoloai tasks ls
check containerd logs: journalctl -u containerd
```

### Step 2.9 — Prerequisites check

**Architecture note:** The prerequisite logic for each backend is quite different (Docker queries
`docker info`, Podman checks PATH + rootless state, containerd checks shim binary + CNI + `/dev/kvm`).
Move this into the backends via an optional interface on `runtime.Runtime`:

```go
// In runtime/runtime.go
type IsolationValidator interface {
    ValidateIsolation(ctx context.Context, isolation string) error
}
```

Each runtime that supports validation implements this interface. `create.go`'s
`checkIsolationPrerequisites` type-asserts to it and calls it; if the backend doesn't implement
the interface, skip validation. This keeps backend-specific knowledge in the backend package and
avoids growing a multi-branch `checkIsolationPrerequisites` in `create.go`.

Docker and Podman runtimes implement `ValidateIsolation` for `container-enhanced` (gVisor check).
Containerd runtime implements it for `vm` and `vm-enhanced`. Phase 1 adds the interface to
`runtime/runtime.go` and the Docker/Podman implementations. Phase 2 adds the containerd
implementation below.

**`runtime/containerd/containerd.go`** — implement `ValidateIsolation()`:

```go
func (r *Runtime) ValidateIsolation(ctx context.Context, isolation string) error {
    missing := []string{}
    if _, err := os.Stat("/run/containerd/containerd.sock"); err != nil {
        missing = append(missing, "containerd socket not found at /run/containerd/containerd.sock")
    }
    if _, err := exec.LookPath("containerd-shim-kata-v2"); err != nil {
        missing = append(missing, "kata shim not found: install kata-containers")
    }
    if _, err := os.Stat("/opt/cni/bin/bridge"); err != nil {
        missing = append(missing, "CNI plugins not found: sudo apt install containernetworking-plugins")
    }
    if _, err := os.Stat("/dev/kvm"); err != nil {
        if isWSL2() {
            missing = append(missing, "nested virtualization not enabled — see WSL2 steps in docs")
        } else {
            missing = append(missing, "/dev/kvm not found: enable KVM in BIOS or check hypervisor settings")
        }
    }
    if isolation == "vm-enhanced" {
        // devmapper check deferred to Phase 3 — query containerd snapshotters
    }
    if len(missing) > 0 {
        return fmt.Errorf("VM isolation mode requires additional setup:\n  - %s",
            strings.Join(missing, "\n  - "))
    }
    return nil
}

func isWSL2() bool {
    data, _ := os.ReadFile("/proc/version")
    return strings.Contains(strings.ToLower(string(data)), "microsoft")
}
```

**`runtime/docker/docker.go`** and **`runtime/podman/podman.go`** — implement `ValidateIsolation()`
for `container-enhanced`. This is the existing `checkSecurityRuntime` / `checkEnhancedSupport`
logic, moved here from `sandbox/create.go`:

- Docker: query `docker info --format '{{range $k,$v := .Runtimes}}{{$k}}\n{{end}}'`; error if
  `"runsc"` not present
- Podman: check `containers.conf` for `runsc`; then check `podmanIsRootless()` and error if
  rootless (cgroupv2 delegation fails for runsc with rootless Podman — verified on test VM)

**`sandbox/create.go`** — `checkIsolationPrerequisites` becomes a thin delegator:

```go
func checkIsolationPrerequisites(ctx context.Context, rt runtime.Runtime, isolation string) error {
    v, ok := rt.(runtime.IsolationValidator)
    if !ok {
        return nil
    }
    return v.ValidateIsolation(ctx, isolation)
}
```

### Step 2.10 — Wire containerd into `newRuntime()`

**File: `internal/cli/helpers.go`**

Replace the stub from Phase 1:
```go
case "containerd":
    return containerdrt.New(ctx)
```

### Step 2.11 — Unit tests

**`runtime/containerd/cni_test.go`:** test state file read/write, idempotent teardown on missing state.

**`runtime/containerd/containerd_test.go`:** test `Name()`, namespace injection helper.

### Step 2.12 — Integration tests (guarded by `integration` build tag)

**`runtime/containerd/integration_test.go`** — requires a machine with containerd + Kata:
- Container lifecycle (create/start/stop/remove)
- Restart: stop then start again (verifies stopped-task cleanup in `Start()`)
- Exec (stdout capture, non-zero exit)
- InteractiveExec with PTY
- CNI: container gets an IP, teardown releases it (verify no netns leak after remove)
- Image import via `EnsureImage()`

Guard with `//go:build integration` and a runtime check that skips if
`/run/containerd/containerd.sock` or Kata shim is absent.

---

## Phase 3: vm-enhanced (Kata + Firecracker)

Deferred until Phase 2 is stable. The test VM already has Firecracker, devmapper, and the
`containerd-shim-kata-fc-v2` symlink set up. Requirements:

1. devmapper snapshotter configured in containerd (done on test VM)
2. `containerd-shim-kata-fc-v2` symlink → `containerd-shim-kata-v2` in PATH (done on test VM)
3. containerd config: devmapper snapshotter + kata-fc runtime entry (done on test VM)

**`Create()` already handles this** (Phase 2 implementation branches on `ContainerRuntime` to
select `devmapper` snapshotter and the fc config path). No additional backend changes needed.

Changes for Phase 3:
- Fill in the devmapper stub in `ValidateIsolation()` in `runtime/containerd/containerd.go`:
  when `isolation == "vm-enhanced"`, query containerd snapshotters via
  `r.client.SnapshotService("devmapper").Stat()` (or equivalent) and error if devmapper is
  absent or not in the `ok` state.
- Test on the test VM with Firecracker.

---

## File Change Summary

### Phase 1

| File | Change |
|------|--------|
| `config/config.go` | `Backend`→`ContainerBackend` (yaml:`container_backend`), `Security`→`Isolation` (yaml:`isolation`), update `knownSettings`, add `ValidateIsolationMode` |
| `config/defaults.go` | Update `DefaultConfigYAML` key names |
| `sandbox/meta.go` | `Security`→`Isolation` (json:`isolation`) |
| `sandbox/inspect.go` | `Perms("gvisor")`→`Perms("container-enhanced")`, rename type if desired |
| `sandbox/create.go` | `securityRuntimeName`→`isolationContainerRuntime`, new values, update all security refs, replace `isContainerBackend()` with `BackendCaps` + `backendCaps()`, remove `dockerNetworkMode` variable, update overlay/gVisor check to use isolation values, `checkSecurityRuntime`→`checkIsolationPrerequisites` stub |
| `sandbox/create_prepare.go` | `pr.security`→`pr.isolation`, `pr.securityExplicit`→`pr.isolationExplicit`, read `Isolation` from opts |
| `sandbox/lifecycle.go` | All `meta.Security`→`meta.Isolation` refs |
| `internal/cli/commands.go` | `--security`→`--isolation`, add `--os`, update macOS error |
| `internal/cli/helpers.go` | Overhaul `resolveBackend()`, add `detectContainerBackend()`, `dockerAvailable()`, `podmanAvailable()`, containerd stub in `newRuntime()` |
| `runtime/runtime.go` | Add `IsolationValidator` interface |
| `runtime/docker/docker.go` | Implement `ValidateIsolation()` (gVisor/docker info check) |
| `runtime/podman/podman.go` | Implement `ValidateIsolation()` (PATH check + rootless detection), add WSL2 socket paths to `discoverSocket()`, add `SocketPath()` export |
| `internal/cli/info.go` | Add containerd to `knownBackends` |
| `docs/BREAKING-CHANGES.md` | Document all breaking changes |

### Phase 2

| File | Change |
|------|--------|
| `go.mod` / `go.sum` | Add `github.com/containerd/containerd/v2`, `github.com/containerd/containerd/api`, `github.com/containerd/go-cni`, `github.com/vishvananda/netns`, `github.com/creack/pty` |
| `runtime/containerd/containerd.go` | **New.** Struct, `New()`, `Name()`, `Close()`, `withNamespace()` helper, `ValidateIsolation()` (shim binary, CNI, /dev/kvm) |
| `runtime/containerd/cni.go` | **New.** netns creation (vishvananda/netns), CNI setup/teardown, state persistence |
| `runtime/containerd/lifecycle.go` | **New.** `Create()` (with snapshotter selection), `Start()` (with stopped-task cleanup), `Stop()` (with status check), `Remove()`, `Inspect()` |
| `runtime/containerd/exec.go` | **New.** `Exec()`, `InteractiveExec()` with FIFO+raw-mode terminal |
| `runtime/containerd/image.go` | **New.** `EnsureImage()`, `ImageExists()` |
| `runtime/containerd/prune.go` | **New.** `Prune()` |
| `runtime/containerd/logs.go` | **New.** `Logs()`, `DiagHint()` |
| `runtime/containerd/containerd_test.go` | **New.** Unit tests |
| `runtime/containerd/cni_test.go` | **New.** CNI state unit tests |
| `runtime/containerd/integration_test.go` | **New.** Integration tests (build tag: integration) |
| `sandbox/create.go` | `checkIsolationPrerequisites()` becomes thin delegator to `IsolationValidator` |
| `internal/cli/helpers.go` | Replace containerd stub with real `containerdrt.New()` |
| `docs/dev/ARCHITECTURE.md` | Add `runtime/containerd/` to package map and file index |

---

## Commit Plan

1. **Phase 1, Steps 1.1–1.6:** Internal rename — config structs, meta, create/lifecycle (including `BackendCaps`, `checkIsolationPrerequisites` stub). No CLI changes yet. `make check` passes.
2. **Phase 1, Steps 1.7–1.8:** CLI: `--isolation`, `--os`, `resolveBackend()` overhaul. Add containerd stub. `make check` passes.
3. **Phase 1, Steps 1.9–1.12:** `IsolationValidator` interface (`runtime/runtime.go`), Docker and Podman `ValidateIsolation()` implementations, WSL2 socket paths, `knownBackends`, `BREAKING-CHANGES.md`.
4. **Phase 2, Step 2.1:** Add `go.mod` dependencies.
5. **Phase 2, Steps 2.2–2.8:** `runtime/containerd/` package — all files except prerequisites.
6. **Phase 2, Step 2.9:** Prerequisites check.
7. **Phase 2, Steps 2.10–2.12:** Wire containerd, unit + integration tests.
8. **Phase 3** (separate PR when ready): vm-enhanced / Firecracker.

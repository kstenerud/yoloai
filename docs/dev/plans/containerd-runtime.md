# Containerd Runtime Implementation Plan

Design spec: [`docs/design/containerd-runtime.md`](../../design/containerd-runtime.md)

This is an iterative plan — phases are ordered by dependency, not by size. Each phase produces
working, shippable code. No backwards compatibility handling: this is beta, and existing sandboxes
using `--security` will break.

---

## Phase 0: Spike — verify containerd + Kata + CNI end-to-end

Before touching any user-facing code, verify the core approach works on a real machine.

**Goal:** A standalone Go program (not part of yoloAI) that:
1. Connects to containerd at `/run/containerd/containerd.sock` using `github.com/containerd/containerd`
2. Runs an `alpine` container with `io.containerd.kata.v2` shimv2 + CNI networking
3. Execs `ip addr` inside and confirms `eth0` exists with an IP address
4. Stops the container and verifies CNI teardown releases the IP cleanly (no netns leak)

Place the spike in `_spike/containerd/main.go` (gitignored). Delete after Phase 3 is complete.

**If the spike fails:** Revisit the design before touching any user-facing code.

**Spike output to capture:**
- Exact Go import path and version of `github.com/containerd/containerd` that works
- CNI plugin path on the test machine (confirm `/opt/cni/bin/` or document alternate)
- Kata shimv2 type string (`io.containerd.kata.v2` — confirm it matches installed binary)
- Any gotchas with the containerd namespace API (context must carry namespace)

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

Update the overlay incompatibility check:
```go
if state.isolation == "container-enhanced" && hasOverlayDirs(state) { ... }
```

Update the non-container-backend guard to include `"containerd"` as a container backend:
```go
func isContainerBackend(backend string) bool {
    return backend == "docker" || backend == "podman" || backend == "containerd"
}
```

Update `checkSecurityRuntime` → `checkIsolationPrerequisites` (stub for now; full prerequisite
checking added in Phase 3).

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
if isolation == "container-enhanced" && os != "" && goruntime.GOOS == "darwin" {
    return sandbox.NewUsageError("--isolation container-enhanced is not available on macOS.\n" +
        "Available isolation modes with --os mac:\n" +
        "  container   macOS sandbox-exec (seatbelt)\n" +
        "  vm          Full macOS VM (Tart)")
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

### Step 2.1 — Add `github.com/containerd/containerd` dependency

```
go get github.com/containerd/containerd@v1.7.x
go get github.com/containernetworking/cni@latest
go get github.com/creack/pty@latest
```

Pin versions after the spike confirms which versions work. Run `go mod tidy`.

### Step 2.2 — `runtime/containerd/containerd.go` — struct, `New()`, `Name()`, `Close()`

```go
package containerd

type Runtime struct {
    client    *containerd.Client
    namespace string // always "yoloai"
}

func New(ctx context.Context) (*Runtime, error) {
    // connect to /run/containerd/containerd.sock
    // verify kata shim is available (containerd-shim-kata-v2 in PATH)
    // return Runtime with namespace "yoloai"
}

func (r *Runtime) Name() string { return "containerd" }
func (r *Runtime) Close() error { return r.client.Close() }
```

**Important:** every containerd API call must carry the namespace in context:
```go
ctx = namespaces.WithNamespace(ctx, r.namespace)
```

### Step 2.3 — `runtime/containerd/cni.go` — CNI setup, teardown, state

CNI state persisted at `~/.yoloai/sandboxes/<name>/backend/cni-state.json`:

```json
{
  "netns": "/var/run/netns/yoloai-<name>",
  "interface": "eth0",
  "ip": "10.88.0.5/16",
  "cni_result": { ... }
}
```

```go
// setupCNI creates a network namespace, runs CNI ADD, returns netns path.
// Persists state to cni-state.json for teardown.
func setupCNI(sandboxDir, name string) (netnsPath string, err error)

// teardownCNI reads cni-state.json and runs CNI DEL to release resources.
// Idempotent: no-op if cni-state.json doesn't exist.
func teardownCNI(sandboxDir string) error
```

CNI config file written to `~/.yoloai/sandboxes/<name>/backend/cni-conf.json` at first use
(or a global config at `~/.yoloai/cni/`). Use the `bridge` plugin with `host-local` IPAM —
same as nerdctl's default.

### Step 2.4 — `runtime/containerd/lifecycle.go` — Create, Start, Stop, Remove, Inspect

**`Create()`:**
1. `setupCNI(sandboxDir, name)` → get `netnsPath`
2. Pull image into `yoloai` namespace if not present
3. Create OCI spec with `netnsPath`, mounts, capabilities, `WithRuntime(ContainerRuntime)`
4. `client.NewContainer()` with the spec
5. Create task, **do not start** (Start is separate)

**`Start()`:**
1. Get container from containerd
2. Create a new task (containerd tasks are terminal after exit — recreate on restart)
3. Set up stdio FIFOs
4. `task.Start()`
5. Detach (yoloAI doesn't wait for the task to exit here)

**`Stop()`:**
1. Get task
2. `task.Kill(ctx, syscall.SIGTERM)` with timeout, then `SIGKILL`
3. `task.Wait()` to reap
4. `task.Delete()`
5. `teardownCNI(sandboxDir)`

**`Remove()`:**
1. Stop if running (idempotent)
2. `container.Delete(ctx, containerd.WithSnapshotCleanup)`
3. `teardownCNI(sandboxDir)` (idempotent if already done in Stop)

**`Inspect()`:**
1. Get container; if not found return `ErrNotFound`
2. Get task; if task not found return `InstanceInfo{Running: false}`
3. Check task status

### Step 2.5 — `runtime/containerd/exec.go` — Exec, InteractiveExec

**`Exec()`** — non-interactive, captures stdout:
1. Create exec process spec
2. `task.Exec()` with piped stdio
3. `proc.Start()`, read stdout, `proc.Wait()`
4. Return `ExecResult`

**`InteractiveExec()`** — PTY-attached:
1. `task.Exec()` with terminal=true
2. `pty.Open()` — get master/slave FDs
3. Set slave as proc stdio
4. `proc.Start()`
5. Copy stdin→master, master→stdout in goroutines
6. Forward `SIGWINCH` → `proc.ResizePTY()`
7. `proc.Wait()`

Reference: nerdctl's `pkg/taskutil/taskutil.go` for the PTY wiring pattern.

### Step 2.6 — `runtime/containerd/image.go` — EnsureImage, ImageExists

**`ImageExists()`:** check `client.GetImage(ctx, "yoloai-base")` in the `yoloai` namespace.

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

**File: `sandbox/create.go`** — in `checkIsolationPrerequisites()`:

```go
func checkIsolationPrerequisites(ctx context.Context, isolation, backend string, explicit bool) error {
    if isolation != "vm" && isolation != "vm-enhanced" {
        return checkEnhancedSupport(ctx, backend, isolation, explicit) // gVisor check (existing logic)
    }
    // vm/vm-enhanced: check containerd prerequisites
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
            missing = append(missing, "nested virtualization not enabled — see design doc for WSL2 steps")
        } else {
            missing = append(missing, "/dev/kvm not found: enable KVM in BIOS or check hypervisor settings")
        }
    }
    if len(missing) > 0 {
        return fmt.Errorf("VM isolation mode requires additional setup:\n  - %s", strings.Join(missing, "\n  - "))
    }
    return nil
}

func isWSL2() bool {
    data, _ := os.ReadFile("/proc/version")
    return strings.Contains(strings.ToLower(string(data)), "microsoft")
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
- Exec (stdout capture, non-zero exit)
- InteractiveExec with PTY
- CNI: container gets an IP, teardown releases it
- Image import via `EnsureImage()`

Guard with `//go:build integration` and a runtime check that skips if
`/run/containerd/containerd.sock` or Kata shim is absent.

---

## Phase 3: vm-enhanced (Kata + Firecracker)

Deferred until Phase 2 is stable. Requires:
1. devmapper snapshotter configured in containerd
2. `containerd-shim-kata-fc-v2` in PATH
3. Loop-based thin pool provisioned (Ubuntu 24.04: `dmsetup` + loop device)
4. Update `checkIsolationPrerequisites()` for vm-enhanced specifics
5. Test on a machine with Firecracker installed

The containerd backend itself needs no changes — `ContainerRuntime = "io.containerd.kata-fc.v2"`
and the devmapper snapshotter are the only differences from Phase 2.

---

## File Change Summary

### Phase 1

| File | Change |
|------|--------|
| `config/config.go` | `Backend`→`ContainerBackend` (yaml:`container_backend`), `Security`→`Isolation` (yaml:`isolation`), update `knownSettings`, add `ValidateIsolationMode` |
| `config/defaults.go` | Update `DefaultConfigYAML` key names |
| `sandbox/meta.go` | `Security`→`Isolation` (json:`isolation`) |
| `sandbox/inspect.go` | `Perms("gvisor")`→`Perms("container-enhanced")`, rename type if desired |
| `sandbox/create.go` | `securityRuntimeName`→`isolationContainerRuntime`, new values, update all security refs, `isContainerBackend` adds `"containerd"`, `checkSecurityRuntime`→`checkIsolationPrerequisites` stub |
| `sandbox/create_prepare.go` | `pr.security`→`pr.isolation`, `pr.securityExplicit`→`pr.isolationExplicit`, read `Isolation` from opts |
| `sandbox/lifecycle.go` | All `meta.Security`→`meta.Isolation` refs |
| `internal/cli/commands.go` | `--security`→`--isolation`, add `--os`, update macOS error |
| `internal/cli/helpers.go` | Overhaul `resolveBackend()`, add `detectContainerBackend()`, `dockerAvailable()`, `podmanAvailable()`, containerd stub in `newRuntime()` |
| `internal/cli/info.go` | Add containerd to `knownBackends` |
| `runtime/podman/podman.go` | Add WSL2 socket paths to `discoverSocket()` |
| `docs/BREAKING-CHANGES.md` | Document all breaking changes |

### Phase 2

| File | Change |
|------|--------|
| `go.mod` / `go.sum` | Add `github.com/containerd/containerd`, `github.com/containernetworking/cni`, `github.com/creack/pty` |
| `runtime/containerd/containerd.go` | **New.** Struct, `New()`, `Name()`, `Close()` |
| `runtime/containerd/cni.go` | **New.** CNI setup/teardown, state persistence |
| `runtime/containerd/lifecycle.go` | **New.** `Create()`, `Start()`, `Stop()`, `Remove()`, `Inspect()` |
| `runtime/containerd/exec.go` | **New.** `Exec()`, `InteractiveExec()` with PTY |
| `runtime/containerd/image.go` | **New.** `EnsureImage()`, `ImageExists()` |
| `runtime/containerd/prune.go` | **New.** `Prune()` |
| `runtime/containerd/logs.go` | **New.** `Logs()`, `DiagHint()` |
| `runtime/containerd/containerd_test.go` | **New.** Unit tests |
| `runtime/containerd/cni_test.go` | **New.** CNI state unit tests |
| `runtime/containerd/integration_test.go` | **New.** Integration tests (build tag: integration) |
| `sandbox/create.go` | Full `checkIsolationPrerequisites()`, `isWSL2()` |
| `internal/cli/helpers.go` | Replace containerd stub with real `containerdrt.New()` |
| `docs/dev/ARCHITECTURE.md` | Add `runtime/containerd/` to package map and file index |

---

## Commit Plan

1. **Phase 1, Steps 1.1–1.6:** Internal rename — config structs, meta, create/lifecycle. No CLI changes yet. `make check` passes.
2. **Phase 1, Steps 1.7–1.8:** CLI: `--isolation`, `--os`, `resolveBackend()` overhaul. Add containerd stub. `make check` passes.
3. **Phase 1, Steps 1.9–1.12:** Podman WSL2 paths, knownBackends, `BREAKING-CHANGES.md`.
4. **Phase 2, Step 2.1:** Add `go.mod` dependencies.
5. **Phase 2, Steps 2.2–2.8:** `runtime/containerd/` package — all files except prerequisites.
6. **Phase 2, Step 2.9:** Prerequisites check.
7. **Phase 2, Steps 2.10–2.12:** Wire containerd, unit + integration tests.
8. **Phase 3** (separate PR when ready): vm-enhanced / Firecracker.

---

## Open Implementation Questions

These need answers before or during implementation:

1. **Containerd client version:** The spike should confirm which version of `github.com/containerd/containerd` is compatible with the installed containerd daemon version. Containerd's Go client has historically had breaking API changes between minor versions.

2. **CNI config location:** Should the CNI bridge config be per-sandbox or shared? Shared (at `~/.yoloai/cni/`) is simpler. nerdctl uses `~/.config/cni/net.d/`. Decide during spike.

3. **Snapshot driver for vm (QEMU):** Kata with QEMU works with the default `overlayfs` snapshotter. Confirm during spike that no special snapshotter configuration is needed on a vanilla Ubuntu 24.04 install with apt-installed containerd.

4. **`podmanAvailable()` implementation:** `podmanrt.discoverSocket()` currently returns an error if no socket is found — it needs a path-only variant that doesn't surface the error to callers. Either export a `SocketPath() (string, error)` from `runtime/podman` or duplicate the stat logic in `helpers.go`. Decide at Step 1.8.

5. **FIFO management in `Start()`:** containerd tasks use FIFOs for stdio. The Docker backend uses the SDK's attach mechanism. The containerd backend needs to create FIFOs in a sandbox-local dir (e.g., `~/.yoloai/sandboxes/<name>/backend/`), attach them before `task.Start()`, and clean them up. Confirm the FIFO lifecycle with nerdctl source as reference.

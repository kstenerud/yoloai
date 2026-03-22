# Capability Registry Plan

## Problem

Capability checks and their remediation messages are written inline, ad-hoc, in each
runtime package. Today:

- `runtime/containerd/containerd.go` — `ValidateIsolation` hand-rolls checks for 6
  prerequisites (socket, kata shim, CNI, netns, KVM, devmapper) with inline error strings.
- `runtime/docker/docker.go` — `ValidateIsolation` checks for `runsc` with an inline
  install URL; no structured remediation.
- `runtime/podman/podman.go` — same pattern; checks rootless mode + `runsc`.
- Error messages are plain text blobs: accurate but not structured, not context-sensitive,
  and duplicated across backends.
- There is no overview command — users have no way to ask "what do I need for vm isolation?"
  before hitting a cryptic runtime error.
- As more backends and isolation modes are added, each author re-invents the same check +
  error-message pattern independently. Remediation advice drifts.

**Core goal:** centralize capability definitions — what it is, how to test for it, and how to
help the user fix it — so messaging is consistent and improvable without touching runtime
internals.

---

## Non-goals

- No daemon or privileged helper process. yoloai stays a standalone binary.
- No auto-apply of fixes without explicit user consent (`--fix` flag only, Phase 3+).
- No exhaustive environment fingerprinting. Cover the cases that actually matter: WSL2,
  running inside a Docker/LXC container, and bare-metal Linux/macOS.
- No capability inheritance tree or scoring. Simple slices.

---

## Interface change: `IsolationValidator` → `CapabilityProvider`

The existing `IsolationValidator` interface is replaced entirely:

```go
// Before (runtime/runtime.go)
type IsolationValidator interface {
    ValidateIsolation(ctx context.Context, isolation string) error
}

// After
type CapabilityProvider interface {
    // RequiredCapabilities returns the host capabilities needed for the given
    // isolation mode. An empty isolation string returns capabilities required
    // for the backend in any mode (e.g. daemon socket reachable).
    // Returns nil if the backend has no special requirements for this mode.
    RequiredCapabilities(isolation string) []caps.HostCapability
}
```

The `ValidateIsolation` method is removed from all backends. `system_check.go` and
`system_doctor.go` drive checks generically via `caps.RunChecks`. No per-backend
formatting code survives.

This is a breaking change to the `runtime.Runtime` interface and all its implementations.
Beta: acceptable.

---

## New package: `runtime/caps`

```
runtime/
  caps/
    caps.go       — HostCapability, FixStep, Environment, CheckResult types
    detect.go     — DetectEnvironment(): IsRoot, IsWSL2, InContainer, KVMGroup
    check.go      — RunChecks(), FormatError(), FormatDoctor()
    common.go     — shared HostCapability vars reused across backends
                    (GVisorRunsc, PodmanRootless, etc.)
```

The `caps` package imports only stdlib. Backend packages import `caps` and assemble
their own `[]caps.HostCapability` slices, closing over any backend-specific state
(e.g. a containerd client) in the `Check` functions they define locally.

### Core types

```go
// HostCapability describes one system prerequisite, how to test for it, and
// how to help the user satisfy it.
type HostCapability struct {
    ID      string  // stable machine-readable key, e.g. "kvm-device"
    Summary string  // short label, e.g. "KVM device access"
    Detail  string  // why it's needed; shown in --verbose / system doctor

    // Check returns nil if the capability is satisfied.
    Check func(ctx context.Context) error

    // Fix returns ordered remediation steps tailored to the host environment.
    // May return nil when no automated guidance is available.
    Fix func(env Environment) []FixStep
}

// FixStep is one discrete remediation action.
type FixStep struct {
    Description string // human explanation
    Command     string // shell command; empty if manual-only
    NeedsRoot   bool   // true when Command requires sudo or root
}

// CheckResult records the outcome of one capability check.
type CheckResult struct {
    Cap   HostCapability
    Err   error     // nil = satisfied
    Steps []FixStep // populated only when Err != nil
}

// Environment holds host context used by Fix functions to tailor advice.
// Detected once at startup; passed to all Fix calls.
type Environment struct {
    IsRoot      bool // os.Getuid() == 0
    IsWSL2      bool // /proc/version contains "microsoft"
    InContainer bool // /.dockerenv exists, or cgroup shows container runtime
    KVMGroup    bool // current user is a member of the "kvm" group
}
```

### Shared capabilities (`caps/common.go`)

Capabilities shared by more than one backend live here as package-level vars. Examples:

```go
// GVisorRunsc checks that the runsc binary is in PATH.
var GVisorRunsc = HostCapability{
    ID:      "gvisor-runsc",
    Summary: "gVisor runtime (runsc)",
    Detail:  "Required for --isolation container-enhanced.",
    Check: func(_ context.Context) error {
        _, err := exec.LookPath("runsc")
        return err
    },
    Fix: func(_ Environment) []FixStep {
        return []FixStep{{
            Description: "Install gVisor",
            Command:     "",
            NeedsRoot:   false,
            // Installation is platform-specific; link to docs rather than
            // a command that may be stale.
        }}
    },
}
```

Backend-specific capabilities (e.g. kata shim, CNI bridge, devmapper probe) are defined
as unexported vars inside their own backend package and assembled into the slice returned
by `RequiredCapabilities`.

### Environment detection (`caps/detect.go`)

```go
func DetectEnvironment() Environment
```

Uses injectable file paths for testability:

```go
var (
    procVersionPath = "/proc/version"      // isWSL2
    dockerEnvPath   = "/.dockerenv"        // InContainer
    cgroupPath      = "/proc/1/cgroup"     // InContainer (fallback)
    groupFilePath   = "/etc/group"         // KVMGroup
)
```

`InContainer` is true if `/.dockerenv` exists OR `/proc/1/cgroup` contains `docker`,
`lxc`, or `kubepods`. This covers Docker, Podman-in-Docker, and LXC containers. No
false-positive on bare-metal or VM guests.

### Check driver (`caps/check.go`)

```go
// RunChecks runs each capability's Check function and returns one result per cap.
// env is detected once by the caller and passed to Fix for failed checks.
func RunChecks(ctx context.Context, caps []HostCapability, env Environment) []CheckResult

// FormatError returns a single error describing all failed checks, suitable
// for use in runtime error paths. Returns nil if all checks passed.
func FormatError(results []CheckResult) error

// FormatDoctor writes a human-readable status table to w, with per-failure
// fix steps. Used by `yoloai system doctor`.
func FormatDoctor(w io.Writer, results []CheckResult)
```

---

## Per-backend `RequiredCapabilities`

### Docker (`runtime/docker/`)

```go
func (r *Runtime) RequiredCapabilities(isolation string) []caps.HostCapability {
    switch isolation {
    case "container-enhanced":
        return []caps.HostCapability{caps.GVisorRunsc}
    default:
        return nil
    }
}
```

The existing `dockerInfoOutput` var and runtime-list logic moves into a local
`caps.HostCapability` defined in `runtime/docker/caps.go`:

```go
var gvisorRegistered = caps.HostCapability{
    ID:      "gvisor-registered",
    Summary: "gVisor registered with Docker daemon",
    ...
    Check: func(ctx context.Context) error { /* uses dockerInfoOutput */ },
    Fix:   func(_ caps.Environment) []caps.FixStep { ... },
}
```

`container-enhanced` requires both `gvisorRegistered` AND `caps.GVisorRunsc` (binary in
PATH is a prerequisite for daemon registration). Order matters: binary check first.

### Podman (`runtime/podman/`)

```go
func (r *Runtime) RequiredCapabilities(isolation string) []caps.HostCapability {
    switch isolation {
    case "container-enhanced":
        var list []caps.HostCapability
        if r.rootless {
            list = append(list, podmanRootRequired) // local cap: explains the constraint
        }
        list = append(list, caps.GVisorRunsc)
        return list
    default:
        return nil
    }
}
```

`podmanRootRequired` is a local cap that always fails when rootless and explains why
(cgroup v2 delegation), with a Fix step pointing to root Podman or Docker.

### Containerd (`runtime/containerd/`)

Capabilities defined locally in `runtime/containerd/caps.go`:

```go
var (
    containerdSocket   caps.HostCapability  // net.Dial to socket
    kataShimV2         caps.HostCapability  // exec.LookPath
    kataFCShimV2       caps.HostCapability  // exec.LookPath
    cniBridge          caps.HostCapability  // os.Stat
    netnsCreation      caps.HostCapability  // netns.NewNamed probe
    kvmDevice          caps.HostCapability  // os.Stat + group check
    devmakerSnapshotter caps.HostCapability // client.SnapshotService probe; set in New()
)
```

The `var ( containerdSockPath = ... )` override vars stay in this package for testability
and are closed over by the respective `Check` functions.

```go
func (r *Runtime) RequiredCapabilities(isolation string) []caps.HostCapability {
    base := []caps.HostCapability{
        containerdSocket, kataShimV2, cniBridge, netnsCreation, kvmDevice,
    }
    switch isolation {
    case "vm-enhanced":
        return append(base,
            caps.HostCapability{ID: "kata-fc-shim", ...},  // kataFCShimV2
            r.devmakerCap(),  // captures r.client
        )
    default: // "vm"
        return base
    }
}
```

### Tart (`runtime/tart/`)

No isolation modes. `RequiredCapabilities` returns nil for all inputs. Tart's platform
prerequisites (macOS, Apple Silicon) are enforced in `New()` and are not expressible as
`HostCapability` (they're OS-level, not fixable at runtime).

### Seatbelt (`runtime/seatbelt/`)

Same as Tart — no isolation modes, `RequiredCapabilities` returns nil.

---

## Changes to `system_check.go`

The isolation check (step 4) replaces `IsolationValidator` with `CapabilityProvider`:

```go
if isolation != "" {
    r := checkResult{Name: "isolation"}
    err := withRuntime(ctx, backend, func(ctx context.Context, rt runtime.Runtime) error {
        cp, ok := rt.(runtime.CapabilityProvider)
        if !ok {
            return nil
        }
        capList := cp.RequiredCapabilities(isolation)
        env := caps.DetectEnvironment()
        results := caps.RunChecks(ctx, capList, env)
        return caps.FormatError(results)
    })
    ...
}
```

---

## New command: `yoloai system doctor`

```
yoloai system doctor [--isolation MODE] [--backend BACKEND] [--json]
```

Without `--isolation`, checks all supported isolation modes for the backend in sequence.
With `--isolation`, checks only that mode.

```
$ yoloai system doctor --isolation vm

Checking prerequisites for vm isolation (containerd):

  ✓  containerd socket           /run/containerd/containerd.sock
  ✓  kata-containers             containerd-shim-kata-v2 in PATH
  ✓  CNI plugins                 /opt/cni/bin/bridge
  ✗  KVM device access           /dev/kvm: permission denied
  ✗  network namespace creation  operation not permitted

To fix KVM device access:
  sudo usermod -aG kvm $USER
  newgrp kvm   # or log out and back in

To fix network namespace creation — choose one option:
  Option A — run yoloai with sudo (simple, no persistent setup):
    sudo yoloai new mybox --isolation vm ...
  Option B — grant capabilities to the binary (lost on reinstall):
    sudo setcap cap_sys_admin,cap_dac_override+ep $(which yoloai)
```

`FormatDoctor` handles all formatting. Exit 0 = all passed, 1 = failures present
(CI-compatible, same as `system check`).

---

## Implementation phases

### Phase 1 — types, detection, and interface replacement

1. Create `runtime/caps/` with `caps.go`, `detect.go`, `check.go`, `common.go`.
2. Replace `runtime.IsolationValidator` with `runtime.CapabilityProvider` in `runtime.go`.
3. Migrate each backend:
   - `runtime/containerd/` — create `caps.go`, migrate `ValidateIsolation` logic,
     implement `RequiredCapabilities`, delete `ValidateIsolation`.
   - `runtime/docker/` — create `caps.go`, migrate `ValidateIsolation` logic,
     implement `RequiredCapabilities`, delete `ValidateIsolation`.
   - `runtime/podman/` — same.
   - `runtime/tart/`, `runtime/seatbelt/` — add `RequiredCapabilities` returning nil.
4. Update `system_check.go` to use `CapabilityProvider`.
5. Update all tests: `containerd_test.go`, `docker_test.go`, `podman_test.go`.
6. New tests in `runtime/caps/caps_test.go` and `runtime/caps/detect_test.go`.

**Outcome:** no user-visible change; all capability logic centralized; messaging
consistent and improvable in one place.

### Phase 2 — `yoloai system doctor` command

1. Create `internal/cli/system_doctor.go` with `newSystemDoctorCmd()`.
2. Wire into the `system` subcommand.
3. Call `FormatDoctor` from `caps/check.go` for output.
4. Support `--json` output.
5. Update `docs/GUIDE.md` and `docs/design/commands.md`.

**Outcome:** users can proactively diagnose prerequisites before hitting a runtime error.

### Phase 3 — `--fix` flag (optional, post-release)

1. Add `--fix` to `system doctor`.
2. For `NeedsRoot=false` steps with a `Command`: run with user confirmation.
3. For `NeedsRoot=true` steps: print as copy-pasteable instructions only.
4. `--yes` flag to skip confirmation prompt.

**Deferred.** Phases 1 and 2 deliver the primary value.

---

## Testability

All `Check` functions close over injectable vars (file paths, function pointers) defined
in their own package — the same pattern used in `containerd_test.go` today. No global
mutable state is introduced in `runtime/caps/`.

`DetectEnvironment` uses injectable file path vars so it can be exercised in unit tests
without root or a real container.

`caps_test.go` tests each shared capability in isolation (injecting fake paths/funcs).
Backend tests (`containerd_test.go`, etc.) test `RequiredCapabilities` using the existing
mock-setup helpers, updated to replace `capNetAdminCheckFunc`-style vars with the new
check-function injection pattern in the local `caps.go` files.

---

## Files to create / modify

| Action  | Path |
|---------|------|
| Create  | `runtime/caps/caps.go` |
| Create  | `runtime/caps/detect.go` |
| Create  | `runtime/caps/check.go` |
| Create  | `runtime/caps/common.go` |
| Create  | `runtime/caps/caps_test.go` |
| Create  | `runtime/caps/detect_test.go` |
| Modify  | `runtime/runtime.go` — replace `IsolationValidator` with `CapabilityProvider` |
| Create  | `runtime/containerd/caps.go` — local capability definitions |
| Modify  | `runtime/containerd/containerd.go` — implement `RequiredCapabilities`, delete `ValidateIsolation` |
| Modify  | `runtime/containerd/containerd_test.go` — update to test `RequiredCapabilities` |
| Create  | `runtime/docker/caps.go` — local capability definitions |
| Modify  | `runtime/docker/docker.go` — implement `RequiredCapabilities`, delete `ValidateIsolation` |
| Modify  | `runtime/docker/docker_test.go` — update tests |
| Create  | `runtime/podman/caps.go` — local capability definitions |
| Modify  | `runtime/podman/podman.go` — implement `RequiredCapabilities`, delete `ValidateIsolation` |
| Modify  | `runtime/podman/podman_test.go` — update tests |
| Modify  | `runtime/tart/tart.go` — add `RequiredCapabilities` returning nil |
| Modify  | `runtime/seatbelt/seatbelt.go` — add `RequiredCapabilities` returning nil |
| Modify  | `internal/cli/system_check.go` — use `CapabilityProvider` |
| Create  | `internal/cli/system_doctor.go` |
| Modify  | `internal/cli/system.go` — register `doctor` subcommand |
| Modify  | `docs/GUIDE.md` — document `system doctor` |
| Modify  | `docs/design/commands.md` — add command to table |

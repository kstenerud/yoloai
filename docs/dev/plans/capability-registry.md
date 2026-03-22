# Capability Registry Plan

## Problem

Capability checks and their remediation messages are written inline, ad-hoc, in each
runtime package. Today:

- `runtime/containerd/containerd.go` — `ValidateIsolation` hand-rolls checks for 6
  prerequisites with inline error strings.
- `runtime/docker/docker.go` — `ValidateIsolation` checks for `runsc` with an inline
  install URL; no structured remediation.
- `runtime/podman/podman.go` — same pattern; checks rootless mode + `runsc`.
- Error messages are plain text blobs: not structured, not context-sensitive, duplicated.
- There is no overview command. Users have no way to ask "what can I run on this machine?"
  before hitting a cryptic runtime error.
- All failures look the same, whether the cause is a missing package (fixable in minutes)
  or a fundamental hardware or platform mismatch (permanently unavailable).

**Core goal:** centralize capability definitions so messaging is consistent, fixable vs
permanent failures are distinguished, and users get a clear picture of what they can run
and what it takes to unlock more.

---

## Non-goals

- No daemon or privileged helper process. yoloai stays a standalone binary.
- No auto-apply of fixes. Fix commands are advisory examples only.
- No exhaustive environment fingerprinting. Cover the cases that actually matter: WSL2,
  running inside a Docker/LXC container, and bare-metal Linux/macOS.
- No capability inheritance tree or scoring. Simple slices.

---

## Interface changes

### Two methods added to `Runtime`; `IsolationValidator` removed

`IsolationValidator` is removed. `RequiredCapabilities` and `SupportedIsolationModes`
are added directly to the `Runtime` interface — not as a separate optional interface.
All backends must implement both. A `BaseModeName` method is also added to support
`system doctor` output labelling:

```go
// In the Runtime interface (runtime/runtime.go):

// RequiredCapabilities returns the host capabilities needed for the given
// isolation mode. Returns nil if the backend has no special requirements
// for this mode.
RequiredCapabilities(isolation string) []caps.HostCapability

// SupportedIsolationModes returns the isolation modes this backend can
// potentially support. Returns nil if the backend has no isolation modes.
// Used by `system doctor` to discover what to check without the caller
// enumerating modes externally.
SupportedIsolationModes() []string

// BaseModeName returns the human label for this backend's default (no-isolation)
// mode, shown in `system doctor` output. e.g. "container", "vm", "process".
BaseModeName() string
```

Making these mandatory in `Runtime` means:
- `system doctor` calls `SupportedIsolationModes()` and `BaseModeName()` on every
  backend without type assertions — no backend silently produces no output.
- `system_check.go` calls `rt.RequiredCapabilities(isolation)` directly.
- Backends with no isolation modes (Tart, Seatbelt) return nil from both nil-returning
  methods — four lines of boilerplate, no logic.

`ValidateIsolation` is deleted from all backends. `system_check.go` and
`system_doctor.go` drive checks generically via `caps.RunChecks`. No per-backend
formatting code survives.

This is a breaking change to the `Runtime` interface and all its implementations.
Beta: acceptable.

---

## New package: `runtime/caps`

```
runtime/
  caps/
    caps.go       — HostCapability, FixStep, Environment, CheckResult,
                    Availability, BackendReport types
    detect.go     — DetectEnvironment(): IsRoot, IsWSL2, InContainer, KVMGroup
    check.go      — RunChecks(), ComputeAvailability(), FormatError(), FormatDoctor()
    common.go     — shared HostCapability constructors reused across backends
```

The `caps` package imports only stdlib. Backend packages import `caps` and assemble
their own `[]caps.HostCapability` slices. All capability structs are **fields on the
`Runtime` struct, constructed once in `New()`** with injected function pointers.
`RequiredCapabilities` simply returns slices of these pre-built fields — no allocation
or closure construction on each call.

### Core types

```go
// HostCapability describes one system prerequisite, how to test for it,
// whether a failure is permanent, and how to help the user fix it.
type HostCapability struct {
    ID      string // stable machine-readable key, e.g. "kvm-device"
    Summary string // short label, e.g. "KVM device access"
    Detail  string // why it's needed; shown in system doctor output

    // Check returns nil if the capability is satisfied.
    Check func(ctx context.Context) error

    // Permanent returns true when a failed check cannot be resolved within
    // the current environment — e.g. no KVM hardware, wrong OS. Called only
    // when Check returns a non-nil error. If nil, failures are assumed fixable.
    Permanent func(env Environment) bool

    // Fix returns ordered remediation steps tailored to the host environment.
    // Called only when Check fails and Permanent returns false (or is nil).
    // May return nil when no command-level guidance is available.
    Fix func(env Environment) []FixStep
}

// FixStep is one discrete remediation action.
type FixStep struct {
    Description string // human explanation
    Command     string // example shell command; empty if no command applies.
                       // Commands are illustrative (typically Debian/Ubuntu);
                       // the user is expected to adapt them to their distro.
    URL         string // reference URL for documentation; empty if not applicable
    NeedsRoot   bool   // true when the command typically requires sudo or root;
                       // used for display labelling only — never gates execution.
}

// Availability classifies a (backend, mode) combination after all checks run.
type Availability int

const (
    Ready       Availability = iota // all checks passed
    NeedsSetup                      // all failures are fixable
    Unavailable                     // at least one failure is permanent
)

// CheckResult records the outcome of one capability check.
type CheckResult struct {
    Cap         HostCapability
    Err         error     // nil = satisfied
    IsPermanent bool      // true when Err != nil and Cap.Permanent(env) == true
    Steps       []FixStep // populated only when Err != nil and not permanent
}

// BackendReport holds the full result for one (backend, mode) combination.
// It is the unit passed to FormatDoctor.
type BackendReport struct {
    Backend      string        // e.g. "docker", "containerd"
    Mode         string        // isolation mode label, or BaseModeName() for the base check
    IsBaseMode   bool          // true when Mode is the no-isolation base mode
    InitErr      error         // non-nil if backend New() failed; Results will be nil
    Results      []CheckResult // nil when InitErr != nil
    Availability Availability  // computed from Results; Unavailable when InitErr != nil
}

// Environment holds host context used by Permanent and Fix functions.
// Detected once per invocation; passed to all capability calls.
type Environment struct {
    IsRoot      bool // os.Getuid() == 0
    IsWSL2      bool // /proc/version contains "microsoft"
    InContainer bool // /.dockerenv exists, or cgroup shows container runtime
    KVMGroup    bool // current user is a member of the "kvm" group
}
```

### Shared capability constructors (`caps/common.go`)

Capabilities shared by more than one backend are expressed as constructor functions,
not package-level vars. This keeps them immutable and parallel-test-safe:

```go
// NewGVisorRunsc returns a capability that checks for the runsc binary in PATH.
// lookPath is injectable for testing (pass exec.LookPath in production).
func NewGVisorRunsc(lookPath func(string) (string, error)) HostCapability {
    return HostCapability{
        ID:      "gvisor-runsc",
        Summary: "gVisor runtime (runsc)",
        Detail:  "Required for --isolation container-enhanced.",
        Check: func(_ context.Context) error {
            _, err := lookPath("runsc")
            return err
        },
        Permanent: func(env Environment) bool {
            return env.InContainer // can't install binaries inside a container
        },
        Fix: func(_ Environment) []FixStep {
            return []FixStep{{
                Description: "Install gVisor",
                URL:         "https://gvisor.dev/docs/user_guide/install/",
                NeedsRoot:   true,
            }}
        },
    }
}
```

Each shared capability is a constructor. Backends call them **once in `New()`**, passing
their injectable function pointers, and store the result as a struct field.

### Environment detection (`caps/detect.go`)

```go
func DetectEnvironment() Environment
```

Uses injectable file paths for testability:

```go
var (
    procVersionPath = "/proc/version"  // IsWSL2
    dockerEnvPath   = "/.dockerenv"    // InContainer
    cgroupPath      = "/proc/1/cgroup" // InContainer (fallback)
    groupFilePath   = "/etc/group"     // KVMGroup
)
```

`InContainer` is true if `/.dockerenv` exists OR `/proc/1/cgroup` contains `docker`,
`lxc`, or `kubepods`. Covers Docker, Podman-in-Docker, and LXC. No false-positive on
bare-metal or VM guests.

### Check driver (`caps/check.go`)

```go
// RunChecks runs each capability's Check function and classifies each result
// as satisfied, fixable-failure, or permanent-failure.
// env is detected once by the caller and passed to Permanent and Fix.
func RunChecks(ctx context.Context, caps []HostCapability, env Environment) []CheckResult

// ComputeAvailability returns the aggregate availability of a result set:
// Unavailable if any check is permanent, NeedsSetup if any failed but all
// failures are fixable, Ready if all checks passed.
func ComputeAvailability(results []CheckResult) Availability

// FormatError returns a single error describing all failed checks, suitable
// for use in runtime error paths (e.g. sandbox creation). Returns nil if all
// checks passed.
func FormatError(results []CheckResult) error

// FormatDoctor writes the full three-tier summary table followed by per-failure
// fix details to w. Takes a slice of BackendReport covering all (backend, mode)
// combinations, including base modes. Handles empty slices gracefully
// (prints "No backends available to check.").
func FormatDoctor(w io.Writer, reports []BackendReport)
```

`FormatDoctor` owns the entire output: the summary table at the top and the expanded
fix sections below it. The CLI layer builds the `[]BackendReport` slice and hands it
to `FormatDoctor` — no formatting logic in `system_doctor.go` itself.

---

## Per-backend implementation

### Docker (`runtime/docker/`)

```go
func (r *Runtime) BaseModeName() string         { return "container" }
func (r *Runtime) SupportedIsolationModes() []string { return []string{"container-enhanced"} }

func (r *Runtime) RequiredCapabilities(isolation string) []caps.HostCapability {
    switch isolation {
    case "container-enhanced":
        // gvisorRunsc first: if the binary isn't present, registration can't work.
        return []caps.HostCapability{r.gvisorRunsc, r.gvisorRegistered}
    default:
        return nil
    }
}
```

`r.gvisorRunsc` and `r.gvisorRegistered` are `caps.HostCapability` fields set in
`New()` via injected function pointers. `gvisorRegistered` is defined in
`runtime/docker/caps.go` and wraps the existing `dockerInfoOutput` logic.

### Podman (`runtime/podman/`)

```go
func (r *Runtime) BaseModeName() string         { return "container" }
func (r *Runtime) SupportedIsolationModes() []string { return []string{"container-enhanced"} }

func (r *Runtime) RequiredCapabilities(isolation string) []caps.HostCapability {
    switch isolation {
    case "container-enhanced":
        // rootlessCheck first: it's a permanent blocker; surfacing it before
        // gvisorRunsc avoids a confusing "install runsc" suggestion when the
        // real answer is "rootless Podman can never run gVisor."
        return []caps.HostCapability{r.rootlessCheck, r.gvisorRunsc}
    default:
        return nil
    }
}
```

`r.rootlessCheck` is defined in `runtime/podman/caps.go`. When `r.rootless` is true,
`Check` returns a descriptive error and `Permanent` returns true — rootless Podman
cannot run gVisor. The user must switch to root Podman or use Docker instead.

### Containerd (`runtime/containerd/`)

```go
func (r *Runtime) BaseModeName() string         { return "container" }
func (r *Runtime) SupportedIsolationModes() []string { return []string{"vm", "vm-enhanced"} }

func (r *Runtime) RequiredCapabilities(isolation string) []caps.HostCapability {
    base := []caps.HostCapability{
        r.containerdSocket, // net.Dial probe
        r.kataShimV2,       // exec.LookPath
        r.cniBridge,        // os.Stat
        r.netnsCreation,    // netns.NewNamed probe
        r.kvmDevice,        // os.Stat + group check + heuristic permanence
    }
    switch isolation {
    case "vm-enhanced":
        return append(base, r.kataFCShimV2, r.devmapperSnapshotter)
    default: // "vm"
        return base
    }
}
```

All capability fields are set in `New()` with injected function pointers.
`devmapperSnapshotter` requires `r.client` (the containerd API client) and so can only
be built after `New()` succeeds. If `New()` fails (e.g. socket unreachable), no
capabilities are set and `system doctor` shows the backend as unavailable with the
`New()` error — which is correct.

`kvmDevice.Permanent` returns true when `/dev/kvm` is absent AND `/proc/cpuinfo` shows
no `vmx` or `svm` flags. **This is a best-effort heuristic**, not a definitive test:
a VM guest without KVM passthrough may lack these flags even though the physical host
supports virtualization. When `kvmDevice` is classified as permanent, the fix message
should hedge: "KVM hardware not detected — if running in a VM, enabling KVM passthrough
in your hypervisor may resolve this."

Note: the existing "devmaker" typo throughout the codebase is corrected to "devmapper"
as part of this work.

### Tart (`runtime/tart/`) and Seatbelt (`runtime/seatbelt/`)

```go
// Tart
func (r *Runtime) BaseModeName() string              { return "vm" }
func (r *Runtime) SupportedIsolationModes() []string { return nil }
func (r *Runtime) RequiredCapabilities(_ string) []caps.HostCapability { return nil }

// Seatbelt
func (r *Runtime) BaseModeName() string              { return "process" }
func (r *Runtime) SupportedIsolationModes() []string { return nil }
func (r *Runtime) RequiredCapabilities(_ string) []caps.HostCapability { return nil }
```

Platform prerequisites (macOS + Apple Silicon for Tart; macOS for Seatbelt) are
enforced in `New()`. If `New()` fails, `system doctor` shows the backend as unavailable
with the `New()` error.

---

## `system doctor` command

```
yoloai system doctor [--isolation MODE] [--backend BACKEND] [--json]
```

**Default (no flags):** attempts every known backend, collects `SupportedIsolationModes()`
from each, runs all capabilities, and renders a three-tier summary. `system doctor` always
attempts all backends — including those not installed — so the output gives a complete
picture of what the machine can and cannot run. A backend that fails `New()` is shown
under "Not available" with its instantiation error.

**With `--isolation` / `--backend`:** scopes to that combination only.

### Backend discovery

`system_doctor.go` contains a static list of all known backends. This is the **one place
in the CLI that imports all backend packages**:

```go
type backendEntry struct {
    name    string
    newFunc func(ctx context.Context) (runtime.Runtime, error)
}

var allBackends = []backendEntry{
    {"docker",     func(ctx context.Context) (runtime.Runtime, error) { return dockerrt.New(ctx) }},
    {"podman",     func(ctx context.Context) (runtime.Runtime, error) { return podmanrt.New(ctx) }},
    {"containerd", func(ctx context.Context) (runtime.Runtime, error) { return containerdrt.New(ctx) }},
    {"tart",       func(ctx context.Context) (runtime.Runtime, error) { return tartrt.New(ctx) }},
    {"seatbelt",   func(ctx context.Context) (runtime.Runtime, error) { return seatbeltrt.New(ctx) }},
}
```

For each entry, `system_doctor.go`:
1. Calls `newFunc(ctx)` — captures `InitErr` if it fails.
2. If successful, calls `rt.BaseModeName()` and adds a base-mode `BackendReport`
   (with nil `RequiredCapabilities("")`results — base mode always shows as Ready if
   `New()` succeeded).
3. For each mode in `rt.SupportedIsolationModes()`: calls `rt.RequiredCapabilities(mode)`,
   runs `caps.RunChecks`, calls `caps.ComputeAvailability`, builds a `BackendReport`.
4. Passes the full `[]BackendReport` to `caps.FormatDoctor`.

### Output format

```
$ yoloai system doctor

Ready to use:
  docker          container (default)
  docker          container-enhanced
  podman          container (default)

Needs setup:
  containerd      vm              2 of 5 checks failing
  containerd      vm-enhanced     3 of 6 checks failing

Not available on this machine:
  podman          container-enhanced   rootless Podman cannot run gVisor
  tart            vm (default)         requires macOS with Apple Silicon
  seatbelt        process (default)    requires macOS

────────────────────────────────────────────────────
Needs setup: containerd / vm

  ✓  containerd socket           /run/containerd/containerd.sock
  ✓  kata-containers             containerd-shim-kata-v2 in PATH
  ✓  CNI plugins                 /opt/cni/bin/bridge
  ✗  KVM device access           not in kvm group
  ✗  network namespace creation  operation not permitted

To fix KVM device access:
  (requires root)  sudo usermod -aG kvm $USER
                   newgrp kvm   # or log out and back in

To fix network namespace creation — choose one option:
  Option A — run as root (simplest):
    sudo yoloai new mybox --isolation vm ...
  Option B — grant capability to binary (lost on reinstall):
  (requires root)  sudo setcap cap_sys_admin,cap_dac_override+ep $(which yoloai)

Note: example commands assume Debian/Ubuntu. Adapt as needed for your distro.
```

`FormatDoctor` owns all output. Fix commands are advisory examples, never executed.
`NeedsRoot=true` steps are labelled `(requires root)`.

Exit codes: 0 = all attempted backends/modes are Ready, 1 = any NeedsSetup or
Unavailable entries present (CI-compatible with `system check`).

---

## Changes to `system_check.go`

```go
if isolation != "" {
    r := checkResult{Name: "isolation"}
    err := withRuntime(ctx, backend, func(ctx context.Context, rt runtime.Runtime) error {
        capList := rt.RequiredCapabilities(isolation)
        if len(capList) == 0 {
            return nil // backend has no requirements for this mode
        }
        env := caps.DetectEnvironment()
        results := caps.RunChecks(ctx, capList, env)
        return caps.FormatError(results)
    })
    ...
}
```

---

## Implementation phases

### Phase 1 — types, detection, and interface replacement

1. Create `runtime/caps/` with `caps.go`, `detect.go`, `check.go`, `common.go`.
2. Remove `IsolationValidator` from `runtime/runtime.go`; add `RequiredCapabilities`,
   `SupportedIsolationModes`, and `BaseModeName` to the `Runtime` interface.
3. Migrate each backend:
   - `runtime/containerd/` — create `caps.go`; construct all capability fields in
     `New()` with injectable function pointers; implement the three new `Runtime`
     methods; delete `ValidateIsolation`; fix "devmaker" → "devmapper" typo throughout.
   - `runtime/docker/` — same pattern.
   - `runtime/podman/` — same pattern.
   - `runtime/tart/`, `runtime/seatbelt/` — implement the three methods with nil/
     string returns as shown above.
4. Update `system_check.go` to call `rt.RequiredCapabilities` directly.
5. Update all tests. Backend tests mock via fields set in constructors, not package-level
   vars. `runtime/caps/caps_test.go` injects fake function pointers into constructors.

**Outcome:** no user-visible change; all capability logic centralized; permanent vs fixable
failures distinguished; messaging consistent and improvable in one place.

### Phase 2 — `yoloai system doctor` command

1. Create `internal/cli/system_doctor.go`:
   - Define `backendEntry` and `allBackends` (imports all five backend packages).
   - Build `[]caps.BackendReport` by iterating `allBackends`.
   - Call `caps.FormatDoctor` for human output; `--json` marshals the report slice.
2. Wire into the `system` subcommand.
3. Update `docs/GUIDE.md` and `docs/design/commands.md`.

**Outcome:** users get a complete picture of what their machine can run and what it takes
to unlock more, before hitting any runtime errors.

---

## Testability

All `Check`, `Permanent`, and `Fix` functions close over injected dependencies set at
construction time in `New()`. No package-level mutable state in `runtime/caps/`.
Shared caps (`caps/common.go`) are constructors, so tests pass fake function pointers
directly:

```go
cap := caps.NewGVisorRunsc(func(name string) (string, error) {
    return "", errors.New("not found")
})
```

Backend tests construct a `Runtime` with test-specific capability fields rather than
using package-level override vars. This replaces the current `var capNetAdminCheckFunc`
pattern with dependency injection at construction.

`DetectEnvironment` uses injectable file path vars (`procVersionPath` etc.) so it can
be exercised without root or a real container.

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
| Modify  | `runtime/runtime.go` — remove `IsolationValidator`; add `RequiredCapabilities`, `SupportedIsolationModes`, `BaseModeName` to `Runtime` |
| Create  | `runtime/containerd/caps.go` — capability field definitions and constructors |
| Modify  | `runtime/containerd/containerd.go` — build caps in `New()`, implement three new methods, delete `ValidateIsolation`, fix devmapper typo |
| Modify  | `runtime/containerd/containerd_test.go` — use injected constructors |
| Create  | `runtime/docker/caps.go` |
| Modify  | `runtime/docker/docker.go` — same pattern |
| Modify  | `runtime/docker/docker_test.go` |
| Create  | `runtime/podman/caps.go` |
| Modify  | `runtime/podman/podman.go` — same pattern |
| Modify  | `runtime/podman/podman_test.go` |
| Modify  | `runtime/tart/tart.go` — add `BaseModeName`, nil `SupportedIsolationModes`, nil `RequiredCapabilities` |
| Modify  | `runtime/seatbelt/seatbelt.go` — same |
| Modify  | `internal/cli/system_check.go` — call `rt.RequiredCapabilities` directly |
| Create  | `internal/cli/system_doctor.go` — `allBackends` registry, report builder, command |
| Modify  | `internal/cli/system.go` — register `doctor` subcommand |
| Modify  | `docs/GUIDE.md` — document `system doctor` |
| Modify  | `docs/design/commands.md` — add command to table |

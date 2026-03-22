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

### `IsolationValidator` → `CapabilityProvider` (in `runtime/runtime.go`)

`IsolationValidator` is removed. `CapabilityProvider` replaces it and adds discovery:

```go
// CapabilityProvider is an optional interface implemented by Runtime backends
// that have host-level prerequisites beyond basic daemon connectivity.
type CapabilityProvider interface {
    // RequiredCapabilities returns the host capabilities needed for the given
    // isolation mode. An empty string returns capabilities for the base backend
    // (e.g. daemon socket reachable). Returns nil if no special requirements
    // exist for this mode.
    RequiredCapabilities(isolation string) []caps.HostCapability

    // SupportedIsolationModes returns the isolation modes this backend can
    // potentially support. Used by `system doctor` to discover what to check
    // without requiring the caller to enumerate modes externally.
    SupportedIsolationModes() []string
}
```

`ValidateIsolation` is deleted from all backends. `system_check.go` and
`system_doctor.go` drive checks generically via `caps.RunChecks`. No per-backend
formatting code survives.

This is a breaking change to the `runtime.Runtime` interface and all its implementations.
Beta: acceptable.

---

## New package: `runtime/caps`

```
runtime/
  caps/
    caps.go       — HostCapability, FixStep, Environment, CheckResult, Availability types
    detect.go     — DetectEnvironment(): IsRoot, IsWSL2, InContainer, KVMGroup
    check.go      — RunChecks(), FormatError(), FormatDoctor()
    common.go     — shared HostCapability constructors reused across backends
```

The `caps` package imports only stdlib. Backend packages import `caps` and assemble
their own `[]caps.HostCapability` slices, closing over any backend-specific state
(e.g. a containerd client) in the `Check` functions they define locally.

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
    Available    Availability = iota // all checks passed
    NeedsSetup                       // all failures are fixable
    Unavailable                      // at least one failure is permanent
)

// CheckResult records the outcome of one capability check.
type CheckResult struct {
    Cap          HostCapability
    Err          error        // nil = satisfied
    IsPermanent  bool         // true when Err != nil and Cap.Permanent(env) == true
    Steps        []FixStep    // populated only when Err != nil and not permanent
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
not package-level vars. This keeps them immutable and injectable-testable:

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

Each shared capability is a constructor, not a var. Backends call them at
`RequiredCapabilities` time, passing their injectable function pointers.

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

// Availability returns the aggregate availability of a result set.
func Availability(results []CheckResult) Availability

// FormatError returns a single error describing all failed checks, suitable
// for use in runtime error paths (e.g. sandbox creation). Returns nil if all
// checks passed.
func FormatError(results []CheckResult) error

// FormatDoctor writes a human-readable status table and fix steps to w.
// Used by `yoloai system doctor`. Handles empty results gracefully.
func FormatDoctor(w io.Writer, label string, results []CheckResult)
```

---

## Per-backend `RequiredCapabilities`

### Docker (`runtime/docker/`)

```go
func (r *Runtime) SupportedIsolationModes() []string {
    return []string{"container-enhanced"}
}

func (r *Runtime) RequiredCapabilities(isolation string) []caps.HostCapability {
    switch isolation {
    case "container-enhanced":
        return []caps.HostCapability{
            r.gvisorRunsc,      // caps.NewGVisorRunsc(r.lookPath) — set in New()
            r.gvisorRegistered, // local cap: checks daemon runtime list
        }
    default:
        return nil
    }
}
```

`gvisorRunsc` and `gvisorRegistered` are set in `New()` (or `new` constructor) with
injectable function pointers, so tests can swap them without touching shared state.
`gvisorRegistered` is a local capability defined in `runtime/docker/caps.go` that
wraps the existing `dockerInfoOutput` logic.

### Podman (`runtime/podman/`)

```go
func (r *Runtime) SupportedIsolationModes() []string {
    return []string{"container-enhanced"}
}

func (r *Runtime) RequiredCapabilities(isolation string) []caps.HostCapability {
    switch isolation {
    case "container-enhanced":
        return []caps.HostCapability{
            r.gvisorRunsc,         // caps.NewGVisorRunsc(r.lookPath)
            r.gvisorRootlessCheck, // local cap (see below)
        }
    default:
        return nil
    }
}
```

`gvisorRootlessCheck` is a local capability in `runtime/podman/caps.go`. When rootless,
`Check` returns a descriptive error. `Permanent` returns true — rootless Podman cannot
run gVisor; the user must switch to root Podman or Docker. This is correctly classified
as permanently unavailable (not fixable within the current runtime configuration), so
`system doctor` shows it under "Not available" rather than "Needs setup."

### Containerd (`runtime/containerd/`)

```go
func (r *Runtime) SupportedIsolationModes() []string {
    return []string{"vm", "vm-enhanced"}
}

func (r *Runtime) RequiredCapabilities(isolation string) []caps.HostCapability {
    base := []caps.HostCapability{
        r.containerdSocket, // net.Dial probe
        r.kataShimV2,       // exec.LookPath
        r.cniBridge,        // os.Stat
        r.netnsCreation,    // netns.NewNamed probe
        r.kvmDevice,        // os.Stat + group check; Permanent when no KVM hardware
    }
    switch isolation {
    case "vm-enhanced":
        return append(base, r.kataFCShimV2, r.devmakerSnapshotter)
    default: // "vm"
        return base
    }
}
```

All capabilities are set in `New()` (or a constructor) with injectable function pointers.
`kvmDevice.Permanent` returns true when `/dev/kvm` doesn't exist AND the machine has no
hardware virtualization (checked via `/proc/cpuinfo` flags: `vmx` or `svm`). Absence from
the kvm group is fixable; absence of the hardware is not.

`devmakerSnapshotter` (note: "devmapper" throughout — the current "devmaker" typo in the
codebase is fixed here) is built by `New()` since it needs `r.client`.

### Tart (`runtime/tart/`)

```go
func (r *Runtime) SupportedIsolationModes() []string { return nil }

func (r *Runtime) RequiredCapabilities(_ string) []caps.HostCapability { return nil }
```

Tart's platform prerequisites (macOS, Apple Silicon) are enforced in `New()` and
permanently prevent instantiation on other platforms. There are no isolation modes.
`system doctor` shows Tart as unavailable when `New()` fails.

### Seatbelt (`runtime/seatbelt/`)

Same as Tart — no isolation modes, no special host capabilities.

---

## `system doctor` command

```
yoloai system doctor [--isolation MODE] [--backend BACKEND] [--json]
```

**Default (no flags):** instantiates every known backend (ignoring connection errors),
collects `SupportedIsolationModes()` from each, runs all capabilities, and renders a
three-tier summary. This gives the user a complete picture of what their machine can run.

**With `--isolation` / `--backend`:** scopes the check to that combination only.

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
  tart            (all modes)          requires macOS with Apple Silicon

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

`FormatDoctor` handles all output. Fix commands are advisory examples, never executed.
`NeedsRoot=true` steps are labelled `(requires root)`. Backends with nil capabilities
and successful `New()` show as "Ready to use" under their default isolation mode.
Backends that fail `New()` show under "Not available" with the instantiation error.

Exit codes: 0 = all checked backends/modes are ready, 1 = any failures present
(CI-compatible with `system check`).

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
        env := caps.DetectEnvironment()
        results := caps.RunChecks(ctx, cp.RequiredCapabilities(isolation), env)
        return caps.FormatError(results)
    })
    ...
}
```

---

## Implementation phases

### Phase 1 — types, detection, and interface replacement

1. Create `runtime/caps/` with `caps.go`, `detect.go`, `check.go`, `common.go`.
2. Replace `runtime.IsolationValidator` with `runtime.CapabilityProvider` in `runtime.go`.
3. Migrate each backend:
   - `runtime/containerd/` — create `caps.go`, migrate all check logic,
     implement `RequiredCapabilities` and `SupportedIsolationModes`, delete `ValidateIsolation`.
     Fix "devmaker" → "devmapper" typo throughout.
   - `runtime/docker/` — same.
   - `runtime/podman/` — same.
   - `runtime/tart/`, `runtime/seatbelt/` — implement `CapabilityProvider` returning nil.
4. Update `system_check.go` to use `CapabilityProvider`.
5. Update all tests. Backend tests now mock via injected constructors, not package-level
   vars. Shared cap tests in `runtime/caps/caps_test.go` inject fake `lookPath` etc.

**Outcome:** no user-visible change; all capability logic centralized; permanent vs fixable
failures distinguished; messaging consistent and improvable in one place.

### Phase 2 — `yoloai system doctor` command

1. Create `internal/cli/system_doctor.go` with `newSystemDoctorCmd()`.
2. Wire into the `system` subcommand.
3. Implement the three-tier output: Ready / Needs setup / Not available.
4. Support `--json` output (array of `{backend, mode, availability, results}`).
5. Update `docs/GUIDE.md` and `docs/design/commands.md`.

**Outcome:** users get a complete picture of what their machine can run and what it takes
to unlock more, before hitting any runtime errors.

---

## Testability

All `Check`, `Permanent`, and `Fix` functions close over injected dependencies set at
construction time. No package-level mutable state in `runtime/caps/`. Shared caps
(`caps/common.go`) are constructors, not vars, so tests pass fake function pointers
directly:

```go
cap := caps.NewGVisorRunsc(func(name string) (string, error) {
    return "", errors.New("not found") // simulate missing binary
})
```

Backend constructors (`New()` or test helpers) wire injectable functions when creating
capability instances. This replaces the current `var capNetAdminCheckFunc = ...` pattern
with proper dependency injection at object construction.

`DetectEnvironment` uses injectable file path vars so it can be exercised in unit tests
without root or a real container.

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
| Create  | `runtime/containerd/caps.go` — local capability definitions and constructors |
| Modify  | `runtime/containerd/containerd.go` — implement `CapabilityProvider`, delete `ValidateIsolation`, fix devmaker→devmapper typo |
| Modify  | `runtime/containerd/containerd_test.go` — update to use injected constructors |
| Create  | `runtime/docker/caps.go` |
| Modify  | `runtime/docker/docker.go` — implement `CapabilityProvider`, delete `ValidateIsolation` |
| Modify  | `runtime/docker/docker_test.go` |
| Create  | `runtime/podman/caps.go` |
| Modify  | `runtime/podman/podman.go` — implement `CapabilityProvider`, delete `ValidateIsolation` |
| Modify  | `runtime/podman/podman_test.go` |
| Modify  | `runtime/tart/tart.go` — implement `CapabilityProvider` (nil returns) |
| Modify  | `runtime/seatbelt/seatbelt.go` — implement `CapabilityProvider` (nil returns) |
| Modify  | `internal/cli/system_check.go` — use `CapabilityProvider` |
| Create  | `internal/cli/system_doctor.go` |
| Modify  | `internal/cli/system.go` — register `doctor` subcommand |
| Modify  | `docs/GUIDE.md` — document `system doctor` |
| Modify  | `docs/design/commands.md` — add command to table |

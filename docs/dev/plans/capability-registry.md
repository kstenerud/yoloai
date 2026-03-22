# Capability Registry Plan

## Problem

Capability checks and their remediation messages are written inline, ad-hoc, in each
runtime package. Today:

- `runtime/containerd/containerd.go` — `ValidateIsolation` hand-rolls checks for 6
  prerequisites (socket, kata shim, CNI, netns, KVM, devmapper) with inline error strings.
- Error messages are plain text blobs: accurate but not structured, and not context-sensitive.
- There is no overview command — users have no way to ask "what do I need for vm isolation?"
  before hitting an error.
- As more backends and isolation modes are added, each author re-invents the same check +
  error-message pattern independently. Remediation advice drifts.

**Core goal:** centralize capability definitions — what it is, how to test for it, and how to
help the user fix it — in one place, so messaging is consistent and easy to improve without
touching runtime internals.

---

## Non-goals

- No daemon or privileged helper process. yoloai stays a standalone binary.
- No auto-apply of fixes without explicit user consent (`--fix` flag only, Phase 3+).
- No exhaustive environment fingerprinting. Cover the cases that actually matter: WSL2,
  running inside a Docker/LXC container, and bare-metal Linux.
- No capability inheritance tree or scoring. Simple slices.

---

## Proposed design

### New package: `runtime/caps`

```
runtime/
  caps/
    caps.go          — HostCapability, FixStep, Environment types
    detect.go        — Environment detection (IsRoot, IsWSL2, InContainer, KVMGroup)
    registry.go      — per-backend capability slices (containerd vm, vm-enhanced; Docker overlay)
    check.go         — RunChecks() driver; used by system doctor and ValidateIsolation wrapper
```

#### Core types

```go
// HostCapability describes one system prerequisite needed by a backend mode,
// how to test for it, and how to help the user satisfy it.
type HostCapability struct {
    // ID is a stable, machine-readable identifier. e.g. "kvm-device", "netns-creation".
    ID string

    // Summary is a short human label. e.g. "KVM device access".
    Summary string

    // Detail explains why the capability is needed and what breaks without it.
    Detail string

    // Check returns nil if the capability is present.
    Check func(ctx context.Context) error

    // Fix returns ordered remediation steps given the host environment.
    // May return nil if no automated guidance is available.
    Fix func(env Environment) []FixStep
}

// FixStep is one discrete action in a remediation sequence.
type FixStep struct {
    Description string  // human explanation
    Command     string  // optional: shell command that satisfies this step, empty if manual-only
    NeedsRoot   bool    // true when Command must be run as root or with sudo
}

// CheckResult records the outcome of one capability check.
type CheckResult struct {
    Cap    HostCapability
    Err    error        // nil = satisfied
    Steps  []FixStep    // populated only when Err != nil
}

// Environment holds host context used by Fix functions to tailor advice.
type Environment struct {
    IsRoot      bool   // os.Getuid() == 0
    IsWSL2      bool   // /proc/version contains "microsoft"
    InContainer bool   // /.dockerenv present, or cgroup shows container runtime
    KVMGroup    bool   // current user is in the "kvm" group
}
```

#### Registry

`registry.go` exports named capability slices (not a map — keeps it simple):

```go
// ContainerdVM returns the capabilities required for --isolation=vm.
func ContainerdVM() []HostCapability { ... }

// ContainerdVMEnhanced returns the capabilities required for --isolation=vm-enhanced.
// This is ContainerdVM() plus devmapper.
func ContainerdVMEnhanced() []HostCapability { ... }
```

Each `HostCapability` in these slices packages its own `Check` and `Fix` functions.
The existing `ValidateIsolation` logic in containerd.go migrates here capability by
capability.

#### Example: KVM capability definition

```go
var KVMDevice = HostCapability{
    ID:      "kvm-device",
    Summary: "KVM device access",
    Detail:  "Kata Containers requires /dev/kvm to run hardware-accelerated VMs.",
    Check: func(_ context.Context) error {
        _, err := os.Stat("/dev/kvm")
        return err
    },
    Fix: func(env Environment) []FixStep {
        if env.IsWSL2 {
            return []FixStep{{
                Description: "Enable nested virtualisation in WSL2",
                Command:     "",
                NeedsRoot:   false,
            }}
        }
        steps := []FixStep{{
            Description: "Add your user to the kvm group",
            Command:     "sudo usermod -aG kvm $USER",
            NeedsRoot:   true,
        }}
        if !env.KVMGroup {
            steps = append(steps, FixStep{
                Description: "Activate group membership without logging out",
                Command:     "newgrp kvm",
                NeedsRoot:   false,
            })
        }
        return steps
    },
}
```

#### Check driver

```go
// RunChecks runs a slice of capabilities against ctx and returns one result per cap.
// env is pre-detected and passed to Fix for all failed checks.
func RunChecks(ctx context.Context, caps []HostCapability, env Environment) []CheckResult
```

---

### Changes to existing code

#### `runtime/containerd/containerd.go` — `ValidateIsolation`

Becomes a thin wrapper:

```go
func (r *Runtime) ValidateIsolation(ctx context.Context, isolation string) error {
    var capList []caps.HostCapability
    switch isolation {
    case "vm-enhanced":
        capList = caps.ContainerdVMEnhanced(r.client)
    default: // "vm"
        capList = caps.ContainerdVM()
    }
    env := caps.DetectEnvironment()
    results := caps.RunChecks(ctx, capList, env)
    return caps.FormatError(results) // nil if all passed
}
```

The `var ( containerdSockPath = ... )` override variables stay in `containerd.go` for
testability, but the check functions are injected into the capability structs via the
registry constructor, not hardcoded.

#### `internal/cli/system_check.go` — `--isolation` check

No structural change needed — it still calls `ValidateIsolation`. Error messages improve
automatically when `FormatError` improves.

---

### New command: `yoloai system doctor`

```
yoloai system doctor [--isolation MODE] [--backend BACKEND]
```

Runs all applicable capability checks and prints a structured status table:

```
$ yoloai system doctor --isolation vm

Checking prerequisites for vm isolation (containerd):

  ✓  containerd socket          /run/containerd/containerd.sock
  ✓  kata-containers            containerd-shim-kata-v2 in PATH
  ✓  CNI plugins                /opt/cni/bin/bridge
  ✗  KVM device access          /dev/kvm: permission denied
  ✗  network namespace creation  operation not permitted

To fix KVM device access:
  sudo usermod -aG kvm $USER
  newgrp kvm   # or log out and back in

To fix network namespace creation — choose one option:
  Option A — run yoloai with sudo (simple, no setup):
    sudo yoloai new mybox --isolation vm ...
  Option B — grant capabilities to the binary (must redo after reinstall):
    sudo setcap cap_sys_admin,cap_dac_override+ep $(which yoloai)
```

Exit codes: 0 = all passed, 1 = one or more failed (compatible with CI use).

The `--fix` flag (Phase 3) would offer to run `Command` steps that `NeedsRoot=false`
automatically, and print the root-requiring steps for the user to copy-paste.

---

## Implementation phases

### Phase 1 — types and migration (no new user-visible features)

1. Create `runtime/caps/` package with `HostCapability`, `FixStep`, `Environment`,
   `CheckResult`, `RunChecks`, `FormatError`.
2. Create `runtime/caps/detect.go` with `DetectEnvironment()`:
   - `IsRoot`: `os.Getuid() == 0`
   - `IsWSL2`: existing `isWSL2()` logic, moved to shared location
   - `InContainer`: check for `/.dockerenv` or `/proc/1/cgroup` content (docker/lxc/podman)
   - `KVMGroup`: parse `/etc/group` for `kvm` entry and check current uid
3. Create `runtime/caps/registry.go` with `ContainerdVM()` and `ContainerdVMEnhanced()`,
   migrating all check logic out of `ValidateIsolation`.
4. Rewrite `ValidateIsolation` in containerd.go as a thin wrapper (see above).
5. Existing tests in `containerd_test.go` continue to pass — they already test via
   `ValidateIsolation` which remains the public interface.
6. New tests in `runtime/caps/caps_test.go` cover each capability in isolation and
   `DetectEnvironment` with injectable paths.

**Outcome:** no user-visible change; capability logic centralised; messaging improvable
in one place.

### Phase 2 — `yoloai system doctor` command

1. Add `newSystemDoctorCmd()` in `internal/cli/system_doctor.go`.
2. Wire into the `system` subcommand alongside `check`, `build`, etc.
3. Pretty-print `CheckResult` slice with ✓/✗ table and per-failure fix steps.
4. Support `--json` for machine-readable output (consistent with `system check`).
5. Update `docs/GUIDE.md` and `docs/design/commands.md` with the new command.

**Outcome:** users can proactively diagnose prerequisites before hitting a runtime error.

### Phase 3 — `--fix` flag (optional, future)

1. Add `--fix` to `system doctor`. When set:
   - Run `NeedsRoot=false` fix commands automatically (with confirmation prompt).
   - Print `NeedsRoot=true` commands as copy-pasteable instructions.
2. Gate behind a `--yes` flag to skip confirmation.

**This phase is optional and can be deferred.** Phases 1 and 2 deliver the primary value.

---

## Testability

The override-variable pattern used in `containerd_test.go` is preserved: each `Check`
function in the registry accepts injected paths/functions via the registry constructor
or via package-level `var` overrides (same pattern as today). No global mutable state
is introduced in the new package; override vars stay in `containerd.go` and are passed
into the capability structs when the registry is constructed.

`DetectEnvironment` reads from injectable paths (`procVersionPath`, `dockerEnvPath`,
`groupFilePath`) so it can be exercised without root or a real container.

---

## Files to create / modify

| Action  | Path |
|---------|------|
| Create  | `runtime/caps/caps.go` |
| Create  | `runtime/caps/detect.go` |
| Create  | `runtime/caps/registry.go` |
| Create  | `runtime/caps/check.go` |
| Create  | `runtime/caps/caps_test.go` |
| Modify  | `runtime/containerd/containerd.go` — thin-wrap `ValidateIsolation` |
| Modify  | `runtime/containerd/containerd_test.go` — update mocks if needed |
| Create  | `internal/cli/system_doctor.go` |
| Modify  | `internal/cli/system.go` — register `doctor` subcommand |
| Modify  | `docs/GUIDE.md` — document `system doctor` |
| Modify  | `docs/design/commands.md` — add command to table |

---

## Open questions

- **Docker overlay capability**: `runtime/docker/docker.go` also has capability-related
  logic (apparmor=unconfined when SYS_ADMIN is in CapAdd). Should Docker's prerequisites
  (e.g. "daemon reachable") be expressed as `HostCapability` slices too, or is the
  registry containerd-only for now? Recommendation: containerd only for Phase 1, extend
  if a second backend needs it.

- **Kata config path**: `kataConfigPath()` in containerd is a pure function today; it
  doesn't need to become a capability. Leave it alone.

- **`system check --isolation`**: today it calls `ValidateIsolation` and shows a raw
  error string. After Phase 1, the error message improves automatically. After Phase 2,
  `system doctor` is the better tool for interactive diagnosis. No change needed to
  `system check` itself.

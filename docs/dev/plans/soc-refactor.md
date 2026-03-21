# Separation-of-Concerns Refactor Plan

Addresses four architectural issues identified in a code review:

1. **Behavioral `BackendCaps` checks scattered through the sandbox layer** — the
   `Runtime` interface leaks backend-specific decisions back to its callers.
2. **`sandbox/create.go` is a 1811-line god file** — one file drives argument
   validation, state building, profile resolution, runtime config construction,
   agent file seeding, and prompt delivery.
3. **`isolationContainerRuntime()`/`isolationSnapshotter()` live in the wrong
   package** — these are backend-specific mappings that belong in `runtime/`, not
   `sandbox/`.
4. **`detectContainerBackend()` writes to stderr** — a resolution helper should
   not have I/O side effects.

Issues are independent and can be implemented in any order. Estimated impact:
high (issue 1), medium (issues 2–3), low (issue 4).

---

## Issue 1 — Behavioral `BackendCaps` replaced with interface methods

### Background

`BackendCaps` has five fields. Three are validation gates that surface user-facing
errors early (`NetworkIsolation`, `OverlayDirs`, `CapAdd`). Those are fine. Two
drive behaviour changes inside the sandbox layer:

| Cap field | Where checked | What it does |
|---|---|---|
| `NeedsHomeSeedConfig` | `create.go:437` | Decides whether to rewrite `.claude.json` install method after seeding |
| `RewritesCopyWorkdir` | `create_prepare.go:463`, `create_prepare.go:261` | Rewrites `:copy` mount paths; changes localhost-URL error hint text |

Both of these encode backend-specific knowledge (seatbelt runs agents natively on
the host; Docker/Tart run them inside a container) but the logic for acting on that
knowledge lives in `sandbox/`. That's backwards: the backend knows what it needs;
the sandbox should not have to infer it from a capability flag.

### Plan

**Step 1.1 — Add two methods to the `runtime.Runtime` interface** (`runtime/runtime.go`):

```go
// ShouldSeedHomeConfig reports whether this backend requires patching the
// home-seed .claude.json install method from "native" to "npm-global".
// Returns false for process-based backends (e.g. seatbelt) that run the
// host's native agent installation and do not use the npm copy in the image.
ShouldSeedHomeConfig() bool

// ResolveCopyMount returns the mount path the agent sees for a :copy directory.
// For container/VM backends, the copy is bind-mounted at the original host path
// inside the container, so this returns hostPath unchanged.
// For process-based backends (seatbelt), the agent runs directly on the host
// and sees the copy at its sandbox location, so this returns the rewritten path.
ResolveCopyMount(sandboxName, hostPath string) string
```

**Step 1.2 — Implement on each backend**:

- `docker/docker.go`: `ShouldSeedHomeConfig()` returns `true`;
  `ResolveCopyMount()` returns `hostPath` unchanged.
- `tart/tart.go`: Same as docker.
- `containerd/containerd.go`: Same as docker.
- `podman/podman.go`: Same as docker.
- `seatbelt/seatbelt.go`: `ShouldSeedHomeConfig()` returns `false`;
  `ResolveCopyMount()` returns the sandbox copy directory path.

Note: `seatbelt` currently does not import `sandbox/`, and no runtime backend does.
Adding that dependency would invert the layering — backends are lower-level than
the sandbox orchestration layer. Instead, move `EncodePath` from `sandbox/paths.go`
to `config/pathutil.go` (or a new `config/encode.go`). Both `sandbox/` and
`runtime/seatbelt` already import `config`, so both can call `config.EncodePath()`
without any layering problem. Update `sandbox/paths.go`'s `WorkDir()` to call
`config.EncodePath()` instead of the local copy.

**Step 1.3 — Update callers in `sandbox/`**:

In `sandbox/create.go` (the `NeedsHomeSeedConfig` check at line 437):

```go
// Before:
if m.runtime.Capabilities().NeedsHomeSeedConfig {
    if err := ensureHomeSeedConfig(agentDef, sandboxDir); err != nil { ... }
}

// After:
if m.runtime.ShouldSeedHomeConfig() {
    if err := ensureHomeSeedConfig(agentDef, sandboxDir); err != nil { ... }
}
```

Note: `ensureHomeSeedConfig(agentDef, sandboxDir)` computes its own target path
(`sandboxDir/home-seed/.claude.json`) and does not need a path from the runtime.
Its signature does not change.

In `sandbox/create_prepare.go`, replace the `RewritesCopyWorkdir` mount-path
rewriting block (line 463):

```go
// Before:
if m.runtime.Capabilities().RewritesCopyWorkdir {
    if workdir.Mode == "copy" { workdir.MountPath = WorkDir(...) }
    for _, ad := range auxDirs { ... }
}

// After:
if workdir.Mode == "copy" {
    workdir.MountPath = m.runtime.ResolveCopyMount(opts.Name, workdir.Path)
}
for _, ad := range auxDirs {
    if ad.Mode == "copy" {
        ad.MountPath = m.runtime.ResolveCopyMount(opts.Name, ad.Path)
    }
}
```
(For container backends `ResolveCopyMount` returns `hostPath` unchanged, so the
assignment is a no-op — the behaviour is identical to the old guard.)

Replace the localhost-URL hint (line 261):

```go
// Before:
caps := m.runtime.Capabilities()
if !caps.RewritesCopyWorkdir { ... }

// After:
// ShouldSeedHomeConfig is false iff the backend runs agents directly on the host
// (currently only seatbelt), where localhost resolves correctly.
// Container/VM backends isolate the agent in a network namespace where localhost
// refers to the container, not the host.
if m.runtime.ShouldSeedHomeConfig() {
    caps := m.runtime.Capabilities()
    hint := "use the host's routable IP instead"
    if caps.NetworkIsolation {
        hint = "use host.docker.internal instead"
    }
    // ... same error logic
}
```

**Step 1.4 — Remove `NeedsHomeSeedConfig` and `RewritesCopyWorkdir` from
`BackendCaps`** once all callers are migrated. Update each backend's
`Capabilities()` return value.

**Step 1.5 — Update `mockRuntime` in `sandbox/manager_test.go`** to implement the
two new methods.

**Step 1.6 — `make check`** must pass.

---

## Issue 2 — Split `sandbox/create.go` into focused files

### Background

`create.go` (1811 lines) + `create_prepare.go` (556 lines) together orchestrate
several distinct concerns. `create_prepare.go` was a partial extraction; this plan
completes it.

The existing top-level structure:

| File | Function | Lines (approx) | Concern |
|---|---|---|---|
| `create.go` | — | 1–55 | Helpers: `mkdirAllPerm`, `writeFilePerm`, mount-mode constants |
| `create.go` | — | 56–105 | Isolation mapping (moved to `runtime/` by Issue 3) |
| `create.go` | `Create()` | 201–252 | Entry point: lock → `prepareSandboxState` → `launchContainer` → return |
| `create.go` | `prepareSandboxState()` | 284–642 | Validation + state building + prompt reading + config building + seeding + workdir setup — all mixed |
| `create.go` | `launchContainer()` | 644–778 | Secrets dir + mounts + InstanceConfig construction + `rt.Create()` + `rt.Start()` + health check |
| `create.go` | — | 780–1811 | Private helpers scattered across all concerns |
| `create_prepare.go` | `resolveProfileConfig()` | 35–300 | Profile chain resolution, env merging, localhost hint |

Important clarifications:
- **Prompt delivery is passive** — the prompt is baked into `agentCommand` via
  `buildAgentCommand()` (called inside `prepareSandboxState()`) and/or written to
  `prompt.txt` and bind-mounted into the container. There is no post-launch
  "deliver prompt" step. The agent reads it on startup from the container config.
- **Attach is in the CLI layer**, not `sandbox/`. `Create()` returns the sandbox
  name; the CLI command handles `--attach` separately.
- **`launchContainer()`** does NOT contain prompt delivery. Its scope is:
  secrets setup, mounts, InstanceConfig construction, `rt.Create()`/`rt.Start()`,
  health check.

The problem is primarily in `prepareSandboxState()` (which mixes validation, state
building, prompt/config assembly, and workdir seeding) and `launchContainer()`
(which mixes secrets/mount prep with InstanceConfig construction and runtime calls).

### Plan

**Step 2.1 — Extract `buildAndStart()` from `launchContainer()` into `sandbox/create_instance.go`**

The second half of `launchContainer()` constructs `runtime.InstanceConfig` and
calls `rt.Create()` / `rt.Start()`. Extract this into a new function:

```go
// buildAndStart constructs the runtime InstanceConfig from sandboxState and
// starts the instance. Extracted from launchContainer().
func (m *Manager) buildAndStart(ctx context.Context, state *sandboxState, mounts []runtime.MountSpec, ports []runtime.PortMapping) error
```

This function owns: caps validation (`NetworkIsolation`, `OverlayDirs`, `CapAdd`),
`parseResourceLimits`, `InstanceConfig` construction,
`runtime.IsolationContainerRuntime` and `runtime.IsolationSnapshotter` calls
(after Issue 3 moves them), `rt.Create()`, `rt.Start()`, health-check wait.

Note: `checkIsolationPrerequisites` is called in `Create()` at lines 220–223,
before `prepareSandboxState()`. It stays in `Create()` and is not extracted.

After extraction, `launchContainer()` owns only: `createSecretsDir`, `buildMounts`,
`parsePortBindings`, and a call to `buildAndStart`. That is a coherent
concern — "assemble the runtime inputs and start the instance" — but it is now
small enough to be readable. `launchContainer()` is kept (not deleted).

**Step 2.2 — Extract `seedSandbox()` from `prepareSandboxState()` into `sandbox/create_seed.go`**

`prepareSandboxState()` mixes validation/state-building with filesystem seeding.
Extract the seeding portion as an internal sub-function called by
`prepareSandboxState()`:

```go
// seedSandbox copies workdirs, agent state files, and seeds home config.
// Returns agentFilesInitialized so the caller can persist it to SandboxState.
// Extracted from prepareSandboxState().
func (m *Manager) seedSandbox(ctx context.Context, opts *CreateOptions, state *sandboxState, agentDef *agent.Definition) (agentFilesInitialized bool, err error)
```

This function owns: `setupWorkdir` and `setupAuxDirs` calls (both already defined
in `create_prepare.go`), the `ResolveCopyMount` rewriting block (after Issue 1),
`copyAgentFiles`, `ensureHomeSeedConfig`.

`seedSandbox()` is called from within `prepareSandboxState()`, not from `Create()`.
This is critical: `setupWorkdir` and `setupAuxDirs` return values (`workCopyDir`,
`baselineSHA`, `dirMetas`) that are used to build `meta` inside
`prepareSandboxState()`, which is then passed to `SaveMeta()`. Hoisting
`seedSandbox()` to `Create()` would break this ordering because the meta build
depends on the workdir setup results. The seeding is a sub-step of state
preparation, not a peer step.

Note on `agentFilesInitialized`: currently a local variable in
`prepareSandboxState()` set at line 427 and used at line 572 to call
`SaveSandboxState(sandboxDir, &SandboxState{AgentFilesInitialized: true})`.
After extraction, `seedSandbox()` returns `(bool, error)` and
`prepareSandboxState()` passes the result to `SaveSandboxState()`. No change to
`sandboxState` struct is needed.

After extraction, `prepareSandboxState()` is still the sole caller of `seedSandbox()`
and still handles meta building and saving — but the seeding block is now a
single named call instead of 30+ inlined lines.

**Step 2.3 — `Create()` is unchanged**

`Create()` currently calls `prepareSandboxState()` then `launchContainer()`.
Neither call site changes — the improvement is internal to those functions.
The `Create()` body is already the right shape; making its called functions
single-concern is the goal, not restructuring `Create()` itself.
```

**Step 2.4 — Keep `create_prepare.go` for `prepareSandboxState` and profile resolution**

No rename needed. After Step 2.2, `prepareSandboxState()` is a clean
single-concern function. `resolveProfileConfig()` stays in the same file.

**Step 2.5 — `make check`** must pass.

---

## Issue 3 — Move isolation mappings to `runtime/`

### Background

`isolationContainerRuntime()` and `isolationSnapshotter()` in `sandbox/create.go`
map user-facing isolation mode strings (`container-enhanced`, `vm`, `vm-enhanced`)
to OCI runtime names (`runsc`, `io.containerd.kata.v2`) and snapshotter names
(`devmapper`). These strings are backend-specific knowledge. They belong in
`runtime/`, not `sandbox/`.

### Plan

**Step 3.1 — Add exported functions to `runtime/` package** (`runtime/isolation.go`,
new file):

```go
package runtime

// IsolationContainerRuntime returns the OCI runtime name for the given isolation
// mode, or "" for the backend default (standard runc).
func IsolationContainerRuntime(isolation string) string {
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

// IsolationSnapshotter returns the containerd snapshotter for the given isolation
// mode, or "" to use the backend default (overlayfs).
func IsolationSnapshotter(isolation string) string {
    if isolation == "vm-enhanced" {
        return "devmapper"
    }
    return ""
}
```

**Step 3.2 — Update callers**

In `sandbox/create_instance.go` (after Issue 2) or `sandbox/create.go`:

```go
// Before:
instanceCfg.ContainerRuntime = isolationContainerRuntime(state.isolation)
instanceCfg.Snapshotter = isolationSnapshotter(state.isolation)

// After:
instanceCfg.ContainerRuntime = runtime.IsolationContainerRuntime(state.isolation)
instanceCfg.Snapshotter = runtime.IsolationSnapshotter(state.isolation)
```

**Step 3.3 — Delete the private functions** from `sandbox/create.go`.

**Step 3.4 — `make check`** must pass.

---

## Issue 4 — Remove stderr side effect from `detectContainerBackend()`

### Background

`detectContainerBackend()` (`internal/cli/helpers.go:103`) calls
`fmt.Fprintf(os.Stderr, ...)` directly. A resolution helper should return a
value; I/O is the caller's responsibility.

### Plan

**Step 4.1 — Change signature to return a warning string**:

The current function (helpers.go:103) emits warnings inline with `fmt.Fprintf`.
Restructure to accumulate warnings as return values while preserving the exact
same fallback logic:

```go
// detectContainerBackend picks docker or podman based on a config preference
// and socket availability. Returns the chosen backend and an optional warning
// message (non-empty when the preferred backend was unavailable).
func detectContainerBackend(preference string) (backend string, warning string) {
    if preference == "podman" {
        if podmanrt.SocketExists() {
            return "podman", ""
        }
        warning = "Warning: container_backend=podman not found; falling back to docker"
    }
    if dockerAvailable() {
        return "docker", warning
    }
    if preference == "docker" {
        warning = "Warning: container_backend=docker not found; falling back to podman"
    }
    if podmanrt.SocketExists() {
        return "podman", warning
    }
    return "docker", warning // will fail hard in newRuntime() with a clear error
}
```

**Step 4.2 — Update the two call sites** to emit the warning themselves:

```go
// In resolveBackend() and resolveBackendForSandbox():
backend, warn := detectContainerBackend(pref)
if warn != "" {
    fmt.Fprintln(os.Stderr, warn)
}
return backend
```

**Step 4.3 — `make check`** must pass.

---

## Non-issues (explicitly deferred)

**`Manager` I/O coupling**: `Manager` holds `input`/`output`/`scanner` used by
the interactive setup wizard. This is a real SoC smell but fixing it requires
extracting the wizard into the CLI layer, which is a larger refactor with higher
risk of regression. Defer until there is a concrete motivating need (e.g. a
library consumer of `sandbox.Manager`).

**Config load/validate separation**: `config/config.go` mixes loading and
validation. The config-revamp plan (`plans/config-revamp.md`) already addresses
this. No further action here.

---

## File Change Summary

| File | Change |
|---|---|
| `runtime/runtime.go` | Add `ShouldSeedHomeConfig()` and `ResolveCopyMount()` to `Runtime` interface. Remove `NeedsHomeSeedConfig` and `RewritesCopyWorkdir` from `BackendCaps`. |
| `runtime/isolation.go` | **New.** `IsolationContainerRuntime()` and `IsolationSnapshotter()`. |
| `runtime/docker/docker.go` | Implement `ShouldSeedHomeConfig()` (returns `true`), `ResolveCopyMount()` (returns `hostPath`). Remove caps fields. |
| `runtime/tart/tart.go` | Same as docker. |
| `runtime/containerd/containerd.go` | Same as docker. |
| `runtime/podman/podman.go` | Same as docker. |
| `config/encode.go` | **New.** Move `EncodePath()` (and `DecodePath()`) from `sandbox/paths.go` to `config/`. Both `sandbox/` and `runtime/seatbelt` import `config` already. |
| `sandbox/paths.go` | Update `WorkDir()` to call `config.EncodePath()` instead of local copy. |
| `runtime/seatbelt/seatbelt.go` | `ShouldSeedHomeConfig()` returns `false`. `ResolveCopyMount()` computes path via `config.EncodePath()`. Remove caps fields. |
| `sandbox/create.go` | Replace `NeedsHomeSeedConfig` cap check (line 437) with `ShouldSeedHomeConfig()`. Remove `isolationContainerRuntime`, `isolationSnapshotter`. `Create()` body unchanged. |
| `sandbox/create_prepare.go` | Replace `RewritesCopyWorkdir` caps checks with `ResolveCopyMount` calls. `prepareSandboxState()` calls `seedSandbox()` internally. |
| `sandbox/create_instance.go` | **New.** `buildAndStart()` — extracted from `launchContainer()`: `InstanceConfig` construction + `rt.Create()` + `rt.Start()` + health check. |
| `sandbox/create_seed.go` | **New.** `seedSandbox()` — called from within `prepareSandboxState()`: workdir setup, agent files, home config seeding. Returns `(bool, error)`. |
| `sandbox/manager_test.go` | Add `ShouldSeedHomeConfig()` and `ResolveCopyMount()` to `mockRuntime`. |
| `internal/cli/helpers.go` | `detectContainerBackend()` returns `(string, string)`. Callers emit the warning. |

---

## Commit Plan

Each issue is independent; commit in this order to keep diffs reviewable:

1. **Issue 3** (isolation mappings) — pure mechanical move, no logic changes, easiest to review.
2. **Issue 4** (`detectContainerBackend` side effect) — two-line signature change + two call sites.
3. **Issue 1** (behavioral caps → interface methods) — touches runtime interface and all backends; keep in one commit so the interface is never broken mid-way.
4. **Issue 2** (create.go split) — after Issue 1 since `create_seed.go` uses the new `ShouldSeedHomeConfig` and `ResolveCopyMount` methods; keeps each new file self-contained.

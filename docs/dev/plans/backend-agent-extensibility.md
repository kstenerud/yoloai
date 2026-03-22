# Backend and Agent Extensibility Refactor

Addresses five architectural issues that will cause friction when adding new backends
or agents. Each issue is independent and can be implemented in any order.

---

## Issue 1 — `meta.Backend` string comparisons outside the dispatch layer

**Problem:** Three locations check `meta.Backend == "seatbelt"` (or `backendName ==
"seatbelt"`) to decide whether the sandbox lives on the host filesystem or inside a
container/VM. A fourth checks `switch backendName` with cases `"docker"` and `"podman"`
to pick the right log-fetch CLI. Adding any new process-based backend (e.g. SSH) or
any new container backend requires finding and updating these scattered checks.

The checks belong to two distinct questions:
- **"Does the sandbox state live on the host filesystem?"** — seatbelt yes,
  all container/VM backends no.
- **"Which CLI do I use to fetch container logs?"** — docker vs podman, and
  irrelevant for non-container backends.

**Affected locations:**

| File | Line | Question asked |
|------|------|----------------|
| `sandbox/context.go` | 51 | host-filesystem sandbox? |
| `sandbox/context.go` | 107 | host-filesystem sandbox? |
| `internal/mcpsrv/proxy.go` | 154 | host-filesystem sandbox? |
| `internal/cli/sandbox_bugreport.go` | 214 | which container CLI for logs? |
| `internal/cli/sandbox_bugreport.go` | 284 | host-filesystem sandbox (tmux)? |

Note: `internal/cli/helpers.go:111,120` checks backend preference strings but those
are routing/config logic, not behavioral branching — no change needed there.

**Fix:**

Add a `HostFilesystem bool` field to `sandbox.Meta` and populate it at sandbox
creation time from a new `BackendCaps` field:

```go
// runtime/runtime.go — add to BackendCaps
type BackendCaps struct {
    NetworkIsolation bool
    OverlayDirs      bool
    CapAdd           bool
    HostFilesystem   bool // true when sandbox state lives on the host (seatbelt, future SSH)
}
```

Backend values:

| Backend | `HostFilesystem` |
|---------|-----------------|
| Docker | `false` |
| Podman | `false` |
| Containerd | `false` |
| Tart | `false` |
| Seatbelt | `true` |

Store in `Meta` at creation time (in `sandbox/create.go` where Meta is built):

```go
// sandbox/meta.go or sandbox/create.go
meta.HostFilesystem = rt.Capabilities().HostFilesystem
```

Replace all `meta.Backend == "seatbelt"` and `backendName == "seatbelt"` with
`meta.HostFilesystem`.

For the `sandbox_bugreport.go:214` container-log case: the `switch backendName` picks
between `docker logs` and `podman logs`. Leave this as-is. The log path is already
guarded by a `meta.Backend == "docker" || "podman"` check that is correct by
definition — only Docker-compatible backends produce container logs retrievable this
way. Adding `ContainerLogCmd` to `BackendCaps` would be over-engineering a two-case
switch that will only ever have two cases.

**Files to change:** `runtime/runtime.go`, all backend `Capabilities()` methods,
`sandbox/meta.go` (or wherever Meta is built), `sandbox/context.go`,
`internal/mcpsrv/proxy.go`, `internal/cli/sandbox_bugreport.go`.

---

## Issue 2 — Agent-specific switch statements in `sandbox/create.go`

**Problem:** When a new agent is added, `sandbox/create.go` must be updated in
multiple places with agent-specific logic. Currently:

| Location | Agent | What it does |
|----------|-------|--------------|
| `create.go:1349` | `"shell"` | routes to `ensureShellContainerSettings()` |
| `create.go:1366–1382` | `"claude"` | `skipDangerousModePermissionPrompt`, disables sandbox, sets `preferredNotifChannel`, injects idle hook |
| `create.go:1384–1397` | `"gemini"` | disables `folderTrust` |
| `create.go:1415–1426` | `"claude"` | same Claude settings inside `ensureShellContainerSettings()` |
| `create.go:1428–1441` | `"gemini"` | same Gemini settings inside `ensureShellContainerSettings()` |
| `create_seed.go:22` | `"claude"` | warns about short-lived OAuth credentials |

**Fix:**

Add an `ApplySettings` function field to `agent.Definition` that owns the agent's
settings transformation entirely:

```go
// agent/agent.go
type Definition struct {
    // ... existing fields ...

    // ApplySettings patches the agent's settings map before it is written to disk.
    // Called with the parsed settings map; should mutate it in place.
    // Nil means no patches are needed.
    ApplySettings func(settings map[string]any)

    // ShortLivedOAuthWarning, if true, warns users when an OAuth credential file is
    // copied into the sandbox (used by Claude Code which uses short-lived tokens).
    ShortLivedOAuthWarning bool
}
```

Each agent definition sets its own `ApplySettings` inline:

```go
// Claude
ApplySettings: func(s map[string]any) {
    s["skipDangerousModePermissionPrompt"] = true
    s["sandbox"] = map[string]any{"enabled": false}
    s["preferredNotifChannel"] = "terminal_bell"
},

// Gemini
ApplySettings: func(s map[string]any) {
    s["security"] = map[string]any{"folderTrust": map[string]any{"enabled": false}}
},
```

**`injectIdleHook` and package boundaries:** `injectIdleHook` currently lives in the
sandbox layer. `agent/` cannot import `sandbox/`, so it cannot be called from an
`ApplySettings` closure defined in `agent/agent.go`. Resolution: move `injectIdleHook`
into the `agent/` package — it is agent-specific logic (it embeds Claude's idle
detection hook into the settings map) and has no sandbox dependencies. Once moved, the
Claude `ApplySettings` closure can call it directly.

`sandbox/create.go` becomes:

```go
if agentDef.ApplySettings != nil {
    agentDef.ApplySettings(settings)
}
```

No agent names in the sandbox layer. No agent-specific terminology on `Definition`.
The `ensureShellContainerSettings()` duplication goes away because it iterates over
the same `Definition` field. `ShortLivedOAuthWarning` stays as a bool because it
drives a user-visible warning, not a settings file mutation.

The `if agentDef.Name == "shell"` routing check at `create.go:1349` should also be
replaced. The shell agent is special because it seeds home configs for *all* real
agents. This is better expressed as a field:

```go
SeedsAllAgents bool // true for the shell agent only
```

**Files to change:** `agent/agent.go` (add fields, populate for each agent),
`sandbox/create.go` (replace all switch/if blocks with field reads),
`sandbox/create_seed.go` (replace name check with `ShortLivedOAuthWarning`).

---

## Issue 3 — `ShouldSeedHomeConfig()` rename to `AgentProvisionedByBackend()`

**Problem:** The method name is opaque. The actual question is: "is the agent binary
already present on the target, or does this backend provision it as part of image
build?" The current name suggests seeding config but doesn't communicate the
architectural distinction.

**Affected locations:**

| File | Line | Role |
|------|------|------|
| `runtime/runtime.go` | interface | definition |
| `runtime/docker/docker.go` | 333 | returns `true` |
| `runtime/containerd/containerd.go` | 42 | returns `true` |
| `runtime/tart/tart.go` | 71 | returns `true` |
| `runtime/seatbelt/seatbelt.go` | 75 | returns `false` |
| `sandbox/create_prepare.go` | 260 | call site |
| `sandbox/create_seed.go` | 44 | call site |

Podman inherits from Docker — no separate implementation.

Current return values after rename:

| Backend | `AgentProvisionedByBackend()` |
|---------|------------------------------|
| Docker | `true` |
| Podman | `true` (inherits) |
| Containerd | `true` |
| Tart | `true` |
| Seatbelt | `false` |

**Fix:** Rename the interface method and all implementations to
`AgentProvisionedByBackend() bool`. Returns `true` when the backend provisions the
agent as part of image build (docker/containerd/tart), `false` when the agent is
already present on the target (seatbelt). The positive form is preserved — call sites
read `if m.runtime.AgentProvisionedByBackend()` instead of
`if m.runtime.ShouldSeedHomeConfig()` — avoiding the double negative that
`AgentPreinstalled()` would require.

**Files to change:** `runtime/runtime.go`, `runtime/docker/docker.go`,
`runtime/containerd/containerd.go`, `runtime/tart/tart.go`,
`runtime/seatbelt/seatbelt.go`, `sandbox/create_prepare.go`,
`sandbox/create_seed.go`.

---

## Issue 4 — `EnsureImage`/`ImageExists` rename to `Setup`/`IsReady`

**Problem:** Container-specific terminology. `EnsureImage` and `ImageExists` have no
natural meaning for process-based backends (seatbelt: "ensure prerequisites installed
on host"), future SSH backends, or any non-image backend. Seatbelt already implements
`EnsureImage` as a binary-check no-op, which is confusing.

**Affected locations:**

`EnsureImage` call sites:

| File | Line |
|------|------|
| `internal/cli/system.go` | 136 |
| `sandbox/profile_build.go` | 52 |
| `sandbox/manager.go` | 163 |

`ImageExists` call sites:

| File | Line |
|------|------|
| `internal/cli/system_check.go` | 77 |
| `runtime/tart/build.go` | 83, 95, 110 (internal self-calls) |

Backend implementations of both: Docker, Podman (inherits), Containerd, Tart,
Seatbelt.

**Fix:** Rename:
- `EnsureImage` → `Setup` ("prepare this backend for launching agents")
- `ImageExists` → `IsReady` ("is this backend ready to launch agents?")

Updated interface:

```go
// Setup prepares the backend for launching agents (builds/pulls images, checks
// prerequisites). sourceDir is the profile directory containing build instructions
// (Dockerfile etc.); ignored by backends that don't build images. force=true
// rebuilds even if already ready.
Setup(ctx context.Context, sourceDir string, output io.Writer, logger *slog.Logger, force bool) error

// IsReady returns true if the backend is ready to launch agents (image built,
// prerequisites present, etc.). Each backend determines readiness by its own
// internal criteria — callers do not pass an image reference.
IsReady(ctx context.Context) (bool, error)
```

**Tart internal self-calls:** `tart/build.go` calls `r.ImageExists(ctx, name)` twice
with specific VM names (`provisionedImageName`, `baseImage`) to check for individual
VMs during the build process. These are asking "does *this specific named VM* exist?"
— a different question from "is the backend generally ready?". After the rename, these
become a private `vmExists(ctx, name string) (bool, error)` helper inside
`tart/build.go`, not on the interface. `r.IsReady(ctx)` on Tart answers "does the
provisioned yoloai-base VM exist?" — encapsulating the same check it performs today.

**`system_check.go`:** Replace `rt.ImageExists(ctx, "yoloai-base")` with
`rt.IsReady(ctx)`. The imageRef argument was a Docker implementation detail leaking
into the call site.

**`sourceDir` in `Setup`:** Already passed as `""` in some call sites and ignored by
non-Docker backends. This is documented in the updated comment above; no signature
change required.

**Files to change:** `runtime/runtime.go`, all backend implementations,
`internal/cli/system.go`, `internal/cli/system_check.go`, `sandbox/profile_build.go`,
`sandbox/manager.go`, `runtime/tart/build.go` (extract private `vmExists` helper).

---

## Issue 5 — `backendName` passed to determine tmux socket path in bugreport

**Problem:** `writeBugReportTmuxCapture()` in `internal/cli/sandbox_bugreport.go:284`
receives `backendName` and checks `if backendName == "seatbelt"` to decide between a
per-sandbox tmux socket and the global tmux session. The tmux socket path is already
written into `runtime-config.json` at sandbox creation time via `PreferredTmuxSocket()`.

**Fix:** Read the socket path from the tmux socket stored in `runtime-config.json` at
sandbox creation time, instead of re-deriving it from the backend name. This removes
the `backendName` parameter from `writeBugReportTmuxCapture()` entirely.

Also rename `PreferredTmuxSocket()` → `TmuxSocket(sandboxDir string)` on the Runtime
interface, passing the sandbox directory so backends that use per-sandbox sockets can
construct the full path. The parameter is `sandboxDir` rather than `name` because
`runtime/seatbelt` cannot import `sandbox.Dir(name)` (wrong dependency direction) —
it needs the resolved directory path directly. Docker and Containerd ignore the
parameter and return `/tmp/yoloai-tmux.sock`; Seatbelt returns
`filepath.Join(sandboxDir, "tmux", "tmux.sock")`. The call site in `sandbox/create.go`
passes the sandbox directory it already holds.

**Files to change:** `runtime/runtime.go`, all backend implementations
(`runtime/docker/docker.go`, `runtime/containerd/containerd.go`,
`runtime/tart/tart.go`, `runtime/seatbelt/seatbelt.go`), `sandbox/create.go`
(call site), `internal/cli/sandbox_bugreport.go`.

---

## Issue 6 — Preserve `InstanceConfig` split optionality (low-cost, do now)

**Context:** `InstanceConfig` is a flat struct mixing universal fields with
container/VM fields with containerd-specific fields. A full split (base struct +
per-backend extensions) is not worth doing until a backend with truly divergent
invocation needs is being implemented (e.g. Kubernetes, cloud VM). However, three
small changes now make that future split cheap.

**Fix A — Group fields with comments in `runtime/runtime.go`:**

```go
type InstanceConfig struct {
    // Universal — all backends.
    Name        string
    WorkingDir  string
    Mounts      []MountSpec
    Ports       []PortMapping
    NetworkMode string
    Resources   *ResourceLimits

    // Container/VM backends (Docker, Podman, containerd, Tart).
    // Ignored by process-based and remote backends.
    ImageRef   string
    CapAdd     []string
    Devices    []string
    UseInit    bool
    UsernsMode string

    // containerd-specific. Ignored by all other backends.
    ContainerRuntime string
    Snapshotter      string
}
```

Zero behavior change. When the split eventually happens, the lines are already drawn.

**Fix B — Verify all `InstanceConfig` construction uses named fields:**

Grep for `InstanceConfig{` across all non-test `.go` files and confirm every
construction uses named fields (not positional). Positional literals break silently
when fields are reordered or inserted; named fields make adding or reordering fields
zero-cost at call sites. Fix any positional constructions found.

**Fix C — Document the extension convention in `docs/dev/CODING-STANDARD.md`:**

Add a short paragraph establishing the pattern to follow when adding new backends:

> **Backend-specific params in `New()`, not `InstanceConfig`.** Construction-time
> params specific to one backend (SSH host/key, Kubernetes namespace/kubeconfig, AWS
> region/AMI) belong in `New()`, not in `InstanceConfig`. Per-invocation params that
> are universal or translatable across backends belong in `InstanceConfig`. If a new
> backend needs per-invocation params with no `InstanceConfig` analog, introduce an
> optional interface (precedent: `IsolationValidator`, `UsernsProvider`) rather than
> widening `InstanceConfig`.

**Files to change:** `runtime/runtime.go` (field grouping), any call sites with
positional construction (TBD from grep), `docs/dev/CODING-STANDARD.md`.

---

## Issue 7 — `meta.json` has no version field

**Problem:** The `Meta` struct (`sandbox/meta.go`) has no version field. Any schema
change — adding a required field, removing a field, changing a field type — silently
breaks existing sandboxes: old `meta.json` files either fail to deserialize or
deserialize with wrong zero values. Issue 1 of this plan is the first change that adds
a field to `Meta` (`HostFilesystem bool`), making this immediately relevant.

**Fix:** Add `Version int` as the first field and implement a migration path in
`LoadMeta`:

```go
type Meta struct {
    Version int `json:"version"` // bump when schema changes; current = 1
    // ... existing fields
}
```

In `LoadMeta`, after unmarshalling, apply migrations and return an error if the version
is unrecognised:

```go
const metaVersion = 1

func migrate(m *Meta) error {
    if m.Version > metaVersion {
        return fmt.Errorf("sandbox was created with a newer version of yoloai "+
            "(meta version %d, this binary knows %d); upgrade yoloai to use it",
            m.Version, metaVersion)
    }
    if m.Version < 1 {
        // v0 → v1: bootstrap HostFilesystem from backend name.
        m.HostFilesystem = (m.Backend == "seatbelt")
        m.Version = 1
    }
    return nil
}
```

Missing `Version` (old files) deserialises as 0 and is migrated forward. A version
higher than the binary knows is a hard error with a clear message — the user should
not silently run an old binary against a sandbox created by a newer one.

**Files to change:** `sandbox/meta.go`.

---

## Issue 8 — Untyped errors in `internal/cli/` defeat the exit-code system

**Problem:** The codebase has a typed error system (`config/errors.go`: `UsageError`,
`ConfigError`, `DependencyError`, `PlatformError`, `AuthError`, `PermissionError`)
that drives exit codes via type assertion in `root.go`. But `internal/cli/` almost
exclusively uses plain `fmt.Errorf`, so nearly all CLI errors exit 1 regardless of
their actual nature. The system exists but isn't used where it matters most.

**Examples of mistyped errors:**

| File | Line | Current | Should be |
|------|------|---------|-----------|
| `internal/cli/system.go` | 49 | `fmt.Errorf("--all and --backend are mutually exclusive")` | `NewUsageError(...)` → exit 2 |
| `internal/cli/system.go` | 77 | `fmt.Errorf("profile %q does not exist", ...)` | `NewConfigError(...)` → exit 3 |
| `internal/cli/diff.go` | 81 | `fmt.Errorf(...)` unsupported feature | `NewPlatformError(...)` → exit 6 |

**Fix:** Audit all `fmt.Errorf` calls in `internal/cli/` and replace with the
appropriate typed constructor where the error has a clear exit-code category:

- Bad flags / argument validation → `NewUsageError` (exit 2)
- Missing config / profile not found → `NewConfigError` (exit 3)
- Feature not available on this platform/backend → `NewPlatformError` (exit 6)
- Missing API key / credential → `NewAuthError` (exit 7)
- Permission denied → `NewPermissionError` (exit 8)
- Operational failures (I/O, network, unexpected) → plain `fmt.Errorf` is correct

**UsageError vs ConfigError boundary:** UsageError means the user passed a bad
argument or flag (wrong type, mutually exclusive flags, unknown name). ConfigError
means a config *file* is malformed, missing a required key, or contains an invalid
value — the file itself is the problem, not the argument referencing it. "Profile does
not exist" is UsageError (bad argument); "profile config.yaml is missing `agent` key"
is ConfigError.

Also add a note to `docs/dev/CODING-STANDARD.md` documenting this convention so new
CLI commands get it right the first time.

**Files to change:** All files in `internal/cli/` with mistyped errors (audit
required), `docs/dev/CODING-STANDARD.md`.

---

## Issue 9 — `os.Setenv` mutation during sandbox creation

**Problem:** `sandbox/create.go:190` mutates the live process environment to inject
credential defaults:

```go
if os.Getenv(k) == "" {
    _ = os.Setenv(k, v)
}
```

`os.Setenv` is not goroutine-safe and affects all goroutines in the process. The
intent is to make credentials available when building the container env config, but
this should be done via a local map rather than mutating the process environment.

**Fix:** Accumulate overrides in a local `map[string]string` and pass it to the env
config builder, rather than writing to `os.Environ`. The local map is merged with
`os.Environ` only at the point where the container's environment slice is assembled.
**Precedence:** host env vars take priority — a key already set in `os.Environ` is
not overridden by the credential defaults, preserving the existing `if os.Getenv(k) == ""`
behaviour.

**Files to change:** `sandbox/create.go` (credential injection and env assembly).

---

## Issue 10 — Unused sentinel errors in `sandbox/errors.go`

**Problem:** `sandbox/errors.go` defines sentinel errors that appear to be unused:
`ErrDockerUnavailable`, `ErrMissingAPIKey`, `ErrContainerNotRunning`, `ErrNoChanges`.
Dead exports create false impressions about the API surface and mislead future
contributors who might expect them to be returned somewhere.

**Fix:** Grep for each sentinel across all `.go` files. Remove any that have no call
sites outside of their own definition. If any are intentionally reserved for future
use, add a comment saying so.

**Files to change:** `sandbox/errors.go`.

---

## Implementation order

These are mostly independent. Suggested order by risk/reward:

1. **Issue 6** (`InstanceConfig` grouping + convention doc) — zero behavior change,
   preserves future optionality.
2. **Issue 10** (unused sentinel errors) — pure cleanup, no behavior change.
3. **Issues 3 + 4** (rename `ShouldSeedHomeConfig`, `EnsureImage`, `ImageExists`) —
   combine into a single pass since both touch `runtime/runtime.go` and all backend
   packages. Pure renames, zero behavior change.
4. **Issue 5** (tmux socket in bugreport + rename `PreferredTmuxSocket`) — touches
   runtime interface and all backends; do as part of the same pass as Issues 3+4 if
   convenient, or separately.
5. **Issue 7** (`meta.json` versioning) — must land before Issue 1 adds
   `HostFilesystem` to `Meta`, since the v0→v1 migration bootstraps from backend name.
6. **Issue 1** (`HostFilesystem` cap + `meta.HostFilesystem`) — medium scope, removes
   the most fragile future breakage. Depends on Issue 7.
7. **Issue 9** (`os.Setenv` mutation) — self-contained, do alongside Issue 1 or 2.
8. **Issue 8** (typed errors in CLI) — audit-heavy but mechanical; do as a single pass.
9. **Issue 2** (`ApplySettings` on agent Definition) — largest change, highest reward
   for agent extensibility.

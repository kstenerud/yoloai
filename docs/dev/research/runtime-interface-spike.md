# Runtime interface spike — W11 catalog

**Status:** Spike complete. Verdict: **proceed** with `BackendDescriptor` extraction. The plan's abort threshold (">30% of `Capabilities()` values dynamic") is not hit — `BackendCaps` is 100% statically declared across all backends.

This document catalogs every method on the `runtime.Runtime` interface and classifies it as **static** (constant per-backend, candidate for `BackendDescriptor`), **dynamic** (depends on host probing or per-call inputs, must stay as an interface method), or **lifecycle** (core operation, stays on a narrowed `Runtime`/`Lifecycle` interface).

## Backends surveyed

`runtime/docker`, `runtime/podman` (embeds docker.Runtime), `runtime/tart`, `runtime/seatbelt`, `runtime/containerd`.

## Method classification

### Lifecycle (≤14 — stay on the core interface)

| Method | Returns | Notes |
|---|---|---|
| `Setup` | error | Image/VM build, host prereq checks. |
| `IsReady` | (bool, error) | Backend-internal readiness check. |
| `Create` | error | Provisions an instance from `InstanceConfig`. |
| `Start`, `Stop`, `Remove` | error | Lifecycle transitions. |
| `Inspect` | (InstanceInfo, error) | Returns `Running`. |
| `Exec` | (ExecResult, error) | Non-interactive in-instance exec. |
| `GitExec` | (string, error) | Backend handles host↔VM path translation. |
| `InteractiveExec` | error | TTY exec. |
| `Prune` | (PruneResult, error) | Orphan cleanup. |
| `Close` | error | Releases connections. |
| `Logs` | string | Last-N lines of instance logs. |
| `DiagHint` | string | Backend-specific failure-diagnosis hint. |

Total: 14 methods. This matches the plan's "≤12" target after dropping `Logs`+`DiagHint` if the audit determines they belong on a separate `Diagnostics` interface (current callers always pair them). Recommend keeping them on the core interface — every backend implements them.

### Static facts (candidates for `BackendDescriptor`)

All five backends return compile-time constant values:

| Fact | docker | podman | tart | seatbelt | containerd |
|---|---|---|---|---|---|
| `Name()` | `r.binaryName` (≈"docker") | `"podman"` (via embedded docker) | `"tart"` | `"seatbelt"` | `"containerd"` |
| `BaseModeName()` | `"container"` | `"container"` | `"vm"` | `"process"` | `"vm"` |
| `AgentProvisionedByBackend()` | `true` | `true` | `true` | `false` | `true` |
| `SupportedIsolationModes()` | `["container-enhanced","container-privileged"]` | same | `nil` | `nil` | `["vm","vm-enhanced"]` |
| `Capabilities()` (4 bool fields) | `{NetworkIsolation:true, OverlayDirs:true, CapAdd:true, HostFilesystem:false}` | same | `{NetworkIsolation:false, OverlayDirs:false, CapAdd:false, HostFilesystem:false}` | `{HostFilesystem:true}` | `{NetworkIsolation:true, OverlayDirs:false, CapAdd:true, HostFilesystem:false}` |
| `PrepareAgentCommand()` prefix shape | unchanged | unchanged | wraps with `node@24` script | wraps with swift-wrapper | unchanged |

**Verdict:** all rows are static per-backend across the matrix. `BackendCaps` itself is 100% static (0% dynamic). The descriptor refactor is unambiguously feasible.

Caveat — `Name()` on the docker backend reads `r.binaryName`, set at construction. It's static after `New()` but technically per-instance. In practice the registry already separates the docker and podman factories, so the binary name is fixed per registered backend. Treat it as static.

After W1b lands and `PrepareAgentCommand` disappears, the remaining static facts compress to 5 fields: `Name`, `BaseModeName`, `AgentProvisionedByBackend`, `SupportedIsolationModes`, `Capabilities`.

### Dynamic methods (stay as interface methods, candidates for optional interfaces)

| Method | Why dynamic | Optional-interface candidate? |
|---|---|---|
| `TmuxSocket(sandboxDir)` | seatbelt's return value depends on `sandboxDir`; others are static strings. | Yes — name `TmuxSocketProvider`. Default fallback: return `""`. |
| `AttachCommand(socket, rows, cols, isolation)` | Every backend constructs the command from per-call inputs (terminal dims, isolation mode). | Yes — name `AttachCommander`. Every backend implements it though, so optionality buys little. Keep on core. |
| `ResolveCopyMount(sandboxName, hostPath)` | Seatbelt rewrites; others return `hostPath` unchanged (could be a default). | Yes — `ResolveCopyMounter`. Default: identity. |
| `RequiredCapabilities(isolation)` | Returns cached `HostCapability` slices computed in `New()` via host probing (`runsc` lookup, CNI plugin caps, KVM device). | Yes — `IsolationCapabilityProvider`. Default: nil. |

Existing optional interfaces (already in `runtime/runtime.go`):

- `UsernsProvider` — only podman implements (rootless `keep-id`).
- `WorkDirSetup` — only tart implements (copies workdir to VM-local storage).
- `StdioExecer` — only docker (inherited by podman) implements.

### Proposed final shape

Static facts move to `BackendDescriptor`:

```go
type BackendDescriptor struct {
    Name                      string
    BaseModeName              string
    AgentProvisionedByBackend bool
    SupportedIsolationModes   []string
    Capabilities              BackendCaps
}
```

Backends register `(Factory, Descriptor)` tuples via `runtime.Register`. Callers that only need static facts hold `BackendDescriptor`, not `Runtime`.

Core `Runtime` interface (post-W11):

```
Setup, IsReady, Create, Start, Stop, Remove, Inspect,
Exec, GitExec, InteractiveExec, Prune, Close, Logs, DiagHint
```

= 14 methods. Optional interfaces (`UsernsProvider`, `WorkDirSetup`, `StdioExecer`, `TmuxSocketProvider`, `ResolveCopyMounter`, `IsolationCapabilityProvider`, `AttachCommander`) implemented by the subset of backends that need them.

`AttachCommand` is an edge case — every backend implements it today. Including it in the core interface keeps mocks simple. Leaving it as optional would force every callsite to type-assert. Recommendation: **keep on core**.

### Methods that should disappear

- `PrepareAgentCommand` — removed by W1b. The wrapped string is stored in `runtime-config.json::agent_command_final` at creation; restart reads it verbatim.

## Spike abort criterion

Plan threshold: ">30% of `Capabilities()` values across the matrix turn out to be dynamic (host-probed at runtime)".

Observed: **0/20 `BackendCaps` field values are dynamic** (4 fields × 5 backends, all compile-time constants in the `Capabilities()` body). Spike threshold not hit — proceed.

## Recommendation for W11 implementation

Land in additive steps to keep mid-refactor builds green:

1. Add `BackendDescriptor` struct in `runtime/runtime.go`.
2. Each backend exposes a `func (r *Runtime) Descriptor() BackendDescriptor` returning the static-fact bundle.
3. Update `runtime.Register` signature to accept `(Factory, Descriptor)` tuples; keep a deprecated single-arg overload for the migration window.
4. Migrate callers from `rt.Capabilities()`, `rt.Name()`, etc. to `desc.Capabilities`, `desc.Name`, etc. — done file-by-file.
5. Once no caller uses the interface methods, remove them.
6. Extract the dynamic-but-not-universal methods (`TmuxSocket`, `ResolveCopyMount`, `RequiredCapabilities`) into optional interfaces with documented default fallbacks at call sites.

The migration is mostly mechanical once step 1–3 land; the risk is step 5 (interface narrowing), which is the pre-merge abort point per the plan.

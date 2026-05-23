# CLI over Pluggable Backends: Comparator Research

<!-- ABOUTME: Empirical study of how comparable Go projects structure the CLI/backend boundary. -->
<!-- ABOUTME: Covers Docker, kubectl, Terraform, containerd/nerdctl, Podman, and HashiCorp tools. -->

**Purpose:** Inform the yoloAI architectural decision about how to structure the CLI layer relative to
the pluggable runtime backend layer (`runtime.Runtime`). Written from the perspective of a senior
engineer planning a rearchitecture during beta, before leaks compound.

**Date:** May 2026

---

## yoloAI Current State (Baseline)

Before surveying comparators, the current yoloAI state is documented so patterns can be evaluated
against concrete reality rather than a hypothetical.

**What exists:**

- `runtime.Runtime` interface in `runtime/runtime.go` — clean, 19-method interface with capability
  flags (`BackendCaps`), optional interfaces (`CopyMountResolver`, `UsernsProvider`, `StdioExecer`,
  `CachePruner`, etc.), and a `BackendDescriptor` struct.
- A public `yoloai.Client` API at the repo root (`yoloai.go`) with `Run/Apply/Diff/Destroy`.
- `internal/cli/` — Cobra command handlers that build orchestration directly against `sandbox` and
  `runtime` packages. The public `yoloai.Client` is not used by the CLI.

**Confirmed leaks (from source code):**

1. **`system_runtime.go`** — entire `yoloai system runtime` subcommand imports
   `github.com/kstenerud/yoloai/runtime/tart` directly; type-asserts `runtime.Runtime` to
   `*tart.Runtime`; calls `tart.ResolveRuntimeVersions`, `tart.GenerateCacheKey`,
   `tart.AcquireBaseLock`, `tart.QueryAvailableRuntimes`. This command is structurally Tart-only
   and is guarded by `runtime.GOOS != "darwin"` checks and explicit Tart availability checks at the
   top of each handler.

2. **`helpers.go` `resolveBackend()`** — contains routing logic that names "tart", "seatbelt",
   "containerd", "docker", "podman" as string literals; routes `--os mac --isolation vm` → tart,
   `--os mac` → seatbelt, `isolation == vm` → containerd. This is routing policy embedded in the
   CLI layer.

3. **`sandbox_vscode.go`** — queries `runtime.Descriptor(meta.Backend).Capabilities.ContainerAttach`
   to guard a feature. This is the capability pattern used correctly — the leak is avoided by
   checking a flag rather than naming a backend.

4. **`InstanceConfig`** — has a `ContainerRuntime` field documented "containerd-specific. Ignored by
   all other backends." This is backend-specific knowledge in the shared config struct.

The leak spectrum visible in yoloAI already spans three categories that reappear across all
comparators:

- **Unavoidable routing leaks** — something must decide which backend to use; naming backends is
  acceptable at the selection point.
- **Feature-gating leaks** — backend names or capabilities appearing in error messages and command
  availability checks.
- **Tightly-coupled leaks** — CLI importing a concrete backend package and type-asserting to a
  private type.

---

## Comparator 1: Docker CLI (`docker/cli`) + Engine (`moby/moby`)

### Structural pattern

```
User
  │
  ▼
docker/cli  (github.com/docker/cli)
  │  uses APIClient interface (from moby/moby/client)
  │  ALL operations go through REST API client
  ▼
Docker daemon (dockerd / moby/moby)
  │
  ▼
containerd → runc/kata/gvisor
```

The `docker/cli` repository is a separate Go module from `moby/moby`. The CLI communicates
exclusively through the `client.APIClient` interface defined in the moby module. There is no direct
import of moby internals in docker/cli commands.

Source: `docker/cli/cli/command/cli.go` — `DockerCli` struct holds an `APIClient` field. All
commands call `dockerCli.Client().<Method>()`. Verified via WebFetch of that file: "All interactions
funnel through the API client layer" with no direct engine invocations.

Source: `docker/cli/cli/command/container/run.go` — `dockerCLI.Client().Ping()`,
`dockerCli.Client().ContainerStart()`, `dockerCli.Client().ContainerAttach()`. No engine bypasses.

### Concrete leaks

**OS-detection leak (confirmed, in source):** `BuildKitEnabled()` in `cli.go` checks
`ServerInfo.OSType != "windows"` — the CLI needs to know that BuildKit is disabled for Windows
Container workloads. This is a case where the CLI must adapt to a backend-specific reality that
cannot be abstracted away, but it reads from a server-reported field rather than hard-coding
platform names. The approach: ask the server what it is, then conditionally adjust.

**Transport selection (not a leak, but worth noting):** The CLI connects via Unix socket, Windows
named pipe, or TCP depending on the local OS. This is client-side transport selection, not engine
knowledge leaking upward.

**BuildKit vs legacy builder:** The `--buildkit` flag and `DOCKER_BUILDKIT` env var are user-visible
hooks that expose a backend architectural choice. The CLI cannot fully hide that two build backends
exist.

### What survived

The REST API boundary has held for years across dramatic internal changes (OCI, BuildKit, containerd
as runtime). The CLI was split from the engine (moby/moby → docker/cli) in 2017 and the API
boundary has remained clean at the structural level since then.

### What broke down

BuildKit introduced a second build path that the CLI cannot fully abstract. The `docker buildx`
plugin system surfaces this — `docker build` and `docker buildx build` are meaningfully different
despite sharing a CLI surface. This is not a failure of the abstraction per se; it reflects that a
fundamentally new capability genuinely needed a different API shape.

### Takeaway for yoloAI

A typed REST/gRPC interface between CLI and backend prevents structural leaks. OS/backend-specific
behavior is read from server-reported metadata rather than hard-coded client-side — the server
declares its capabilities, the client adjusts. When a fundamentally new feature cannot be expressed
through the existing interface shape, a new command group is acceptable (docker buildx), but only as
a last resort.

---

## Comparator 2: kubectl + client-go

### Structural pattern

```
User
  │
  ▼
kubectl  (k8s.io/kubectl)
  │  uses client-go for all resource CRUD
  │  uses RemoteExecutor interface for streaming
  ▼
client-go  (k8s.io/client-go)
  │  uses typed API and REST client
  ▼
kube-apiserver
  │
  ▼
kubelet / container runtime (CRI)
```

kubectl uses client-go for resource operations (create, get, list, update, delete). The boundary is
enforced by module separation: kubectl imports client-go as a library, not kube internals.

For interactive streaming (exec, port-forward, attach), the client-go `tools/portforward` and
`tools/remotecommand` packages provide abstraction via `httpstream.Dialer` and
`httpstream.Connection` interfaces.

Source: `kubernetes/client-go/tools/portforward/portforward.go` — uses `httpstream.Dialer` for
protocol negotiation; the dialer returns an `httpstream.Connection` that isolates callers from
transport details.

### Concrete leaks

**SPDY/WebSocket protocol leak (confirmed via KEP-4006):** `kubectl exec` in
`k8s.io/kubectl/pkg/cmd/exec/exec.go` contains explicit SPDY vs WebSocket selection:
```go
exec, err := remotecommand.NewSPDYExecutor(config, "POST", url)
// fallback:
remotecommand.NewWebSocketExecutor(config, "GET", url.String())
```
The methods use different HTTP verbs (POST for SPDY, GET for WebSocket per RFC 6455). The code
checks `httpstream.IsUpgradeFailure(err)` to decide whether to fall back. This is transport-protocol
knowledge leaking into the command layer. The abstraction (`RemoteExecutor` interface) was designed
to hide this, but the `FallbackExecutor` pattern means the CLI must know about both protocol
generations during the multi-year SPDY→WebSocket transition (KEP-4006, opened 2023).

Source: GitHub issue `kubernetes/kubernetes#89163` ("Support kubectl exec/attach using Websocket"),
GitHub `kubernetes/enhancements/keps/sig-api-machinery/4006-transition-spdy-to-websockets`.

**Goroutine leak in client-go (confirmed):** GitHub issue `kubernetes/kubernetes#105830` — "client-go
v0.21 leaking go routines when using SPDYExecutor" — demonstrates that the streaming abstraction had
resource management problems that persisted for years due to the complexity of managing two
concurrent protocol implementations.

**Protocol-specific HTTP verbs:** SPDY uses POST; WebSocket requires GET. This verb difference
cannot be hidden inside the dialer abstraction because the HTTP method must appear in the request
before the connection is upgraded. The abstraction boundary could not fully contain this.

### What survived

The resource CRUD boundary (List/Get/Create/Update/Delete via typed API) has held cleanly for the
entire Kubernetes history. The client-go module separation ensures kubectl never reaches into
kube-apiserver internals for standard resource operations.

### What broke down

Streaming operations (exec, attach, port-forward) require protocol-level knowledge that the
`httpstream` abstraction cannot fully contain. The multi-year SPDY→WebSocket transition forced the
CLI layer to be aware of both protocols simultaneously. The `FallbackExecutor` pattern is a symptom
of the abstraction boundary failing to hold under protocol evolution.

### Takeaway for yoloAI

The resource/streaming split in kubectl maps to yoloAI's lifecycle/interactive split. Clean
interfaces work well for lifecycle operations (Create, Start, Stop, Inspect). Interactive operations
(attach, exec with TTY) require streaming and are more likely to require protocol-specific knowledge
in the CLI. Plan for this leak category specifically: the `InteractiveExec` and `AttachCommand`
methods on `runtime.Runtime` encode some of this backend-specific knowledge into the interface shape
rather than leaking it into the CLI, which is the right approach, but the shape of those methods may
need to evolve as backends differ in their interactive capabilities.

---

## Comparator 3: Terraform / OpenTofu

### Structural pattern

```
User
  │
  ▼
Terraform CLI  (cmd/terraform)
  │  HCL parsing, plan/apply orchestration
  ▼
Provider plugins  (via go-plugin / gRPC)
  │  gRPC boundary: tfplugin6 proto
  ▼
Provider binary  (implements gRPC server)
  │
  ▼
Cloud/infrastructure API
```

Providers are separate executables. Terraform core communicates with them exclusively via a gRPC
protocol (`tfplugin6.proto`). The go-plugin library (by HashiCorp) handles process launch, health
checks, and the gRPC connection. Core needs to know nothing about provider internals — it sends
generic CRUD operations (`PlanResourceChange`, `ApplyResourceChange`) and the provider translates
them to cloud-specific calls.

Source: `github.com/hashicorp/terraform/blob/main/docs/plugin-protocol/README.md` — "Terraform
plugins are normal executable programs that, when launched, expose gRPC services on a server
accessed via the loopback interface."

### Concrete leaks

**Backend coupling in core (confirmed, OpenTofu issue #382):** State storage backends (S3, GCS, etc.)
are compiled into the core binary, not plugin-style. OpenTofu issue #382 ("Backends as plugins")
identifies this explicitly: "a user who wants or needs to upgrade to a new version of the backend
are forced to upgrade to a new version [of core]." The issue notes that unlike providers (which have
stable gRPC isolation), backends have no plugin boundary — they are "an integral part of the
OpenTofu core." This is a structural leak that was never addressed by the plugin architecture
because the plugin architecture was only applied to resource providers, not state backends.

**Protocol versioning negotiation:** Core must know supported protocol versions ("the plugin handshake
includes a negotiation step where client and server can work together to select a mutually-supported
major version"). This is a minimal and necessary leak — something must negotiate the boundary.

**Shared protobuf stubs:** Both provider SDK and core must have access to the generated protobuf
stubs. This creates a de facto coupling at the SDK level even though the process boundary is clean.

### What survived

The provider plugin gRPC boundary has been extraordinarily durable. The same tfplugin6 protocol
works for all major cloud providers (AWS, Azure, GCP). Adding new resource types never requires
changing core. This is the gold standard for backend extensibility at scale.

### What broke down

State backends were never brought under the plugin model. This is Terraform's most significant
architectural debt. OpenTofu is actively working on pluggable backends (issue #382) precisely because
the in-core-binary coupling limits independent iteration. The lack of checksums for backend binaries
in lockfiles (noted in issue #382) further demonstrates the inconsistency between the well-designed
provider boundary and the underdeveloped backend boundary.

### Takeaway for yoloAI

The gRPC out-of-process plugin approach is not appropriate for yoloAI (backends are not separate
teams; the overhead would be disproportionate). But the lesson is valuable: **the provider boundary
works because it is enforced by a process boundary with a well-defined protocol.** In-process
backends (yoloAI's current architecture) rely on interface discipline alone, which can erode.
Terraform's backend debt (state backends without a plugin boundary) is the direct analogue of
yoloAI's Tart-specific CLI commands — the architectural exception that was expedient and then
accumulated.

---

## Comparator 4: containerd / nerdctl

### Structural pattern

```
User
  │
  ▼
nerdctl  (containerd/nerdctl)
  │  calls containerd gRPC APIs only
  ▼
containerd daemon
  │  Shim Manager
  ▼
containerd-shim-<runtime>-v2  (separate binary)
  │  TTRPC protocol: TaskService v3 / SandboxService v1
  ▼
runc / kata-containers / gvisor / etc.
```

nerdctl is a Docker-compatible CLI for containerd. It communicates with the containerd daemon
exclusively through containerd's gRPC client APIs. The daemon handles shim discovery and
management — it spawns the appropriate shim binary (named `containerd-shim-<runtime>-v2`) based on
configuration. Neither nerdctl nor containerd's core API knows the internals of runc vs kata vs
gvisor.

Source: `deepwiki.com/containerd/containerd/5.1-shim-architecture` — "A CLI like nerdctl requires
minimal shim awareness. The CLI sends standard gRPC requests to containerd; the daemon handles shim
selection and management."

### Concrete leaks (intentional, user-facing)

**`--snapshotter` flag** — nerdctl exposes `--snapshotter=(overlayfs|native|btrfs|stargz|nydus|...)`,
labeled "🤓 nerdctl specific" in the command reference. This is an intentional exposure of a
containerd-internal configuration axis.

**`--runtime` flag** — `nerdctl run --runtime=io.containerd.runsc.v1` exposes the shim binary naming
convention directly to users. Users must know that `io.containerd.runsc.v1` is the gVisor shim
identifier.

**`--cgroup-manager` flag** — exposes containerd's cgroup management strategy (cgroupfs, systemd,
none).

The nerdctl developers acknowledge this: they mark these flags as "nerdctl specific" (🤓 vs 🐳 for
Docker-compatible) as a way of documenting that these flags expose containerd-native capabilities
without pretending they are generic. This is a principled decision to surface power-user access
rather than abstract it away.

Source: `github.com/containerd/nerdctl/blob/main/docs/command-reference.md`

### What survived

The containerd gRPC API boundary has kept nerdctl from needing any shim-specific code. Adding a new
runtime shim (e.g., nerdbox, Kata Containers) requires no changes to nerdctl — the operator installs
the shim binary and references it by name in the `--runtime` flag.

### What broke down

The `--snapshotter` flag's semantics vary between snapshotters in ways that the flag itself cannot
communicate. Users must understand the operational characteristics of each snapshotter. This is a
documentation burden that the abstraction cannot eliminate.

**macOS issue `containerd/nerdctl#4572`:** "macOS: use nerdbox as the default runtime, erofs as the
default snapshotter" — shows that even the defaults need to be platform-specific, because the
optimal configuration differs by OS. This is the platform-specific-defaults leak: you cannot have
one default that is correct everywhere.

### Takeaway for yoloAI

nerdctl's approach of marking backend-specific flags explicitly (🤓) rather than hiding them is
worth emulating. When a flag necessarily exposes a backend-specific capability, document it as such
rather than pretending it is generic. The `--snapshotter` and `--runtime` pattern maps to yoloAI's
`--isolation` and `--os` flags — these are user-facing backend selection controls that cannot be
fully hidden, but can be documented and their backend implications made explicit.

---

## Comparator 5: Podman

### Structural pattern

Podman has two execution modes — local (ABI) and remote (Tunnel) — that share the same CLI:

```
User
  │
  ▼
Podman CLI (cmd/podman)
  │  flags → Options structs
  ▼
ContainerEngine interface  (pkg/domain/entities)
  ├── ABI implementation  (pkg/domain/infra/abi)  → libpod directly
  └── Tunnel implementation  (pkg/domain/infra/tunnel)  → REST API
```

The CLI never calls libpod directly. All operations go through `registry.ContainerEngine()`:

```go
report, err := registry.ContainerEngine().ContainerRun(registry.Context(), runOpts)
```

Source: `github.com/containers/podman/blob/main/cmd/podman/containers/run.go` — confirmed via
WebFetch.

The ABI implementation calls libpod directly (in-process). The Tunnel implementation calls the
Podman REST API. The CLI is identical for both. This is the same pattern yoloAI uses between its
CLI and `runtime.Runtime`, but Podman has committed to it consistently across all commands.

### Concrete leaks

**Command availability filtering by mode (confirmed):** From `deepwiki.com/containers/podman`:
"The CLI handles this transparently by checking the `EngineMode` annotation during registration.
If a command is not supported in the current mode..., the CLI marks it as hidden or returns a
specialized error." This is a deliberate, controlled leak — the CLI knows which mode it is in and
adjusts command availability. Not all commands work in both ABI and Tunnel modes.

**`podman machine` subcommand:** On macOS and Windows, `podman machine` manages a Linux VM (QEMU or
Apple Virtualization.framework via vfkit). The `machine` subcommand is explicitly VM-management
surface that would not exist if Podman ran natively. Podman 5.0 (2024) rewrote `podman machine`
with hypervisor abstraction (Applehv, QEMU, WSL) — the VM backend selection is exposed to the
user as a configuration option. This is the equivalent of yoloAI's Tart-specific `system runtime`
commands: backend-specific features get their own named subcommand rather than trying to squeeze
through the generic interface.

Source: InfoQ, "Podman 5 Improves Performance and Stability on Mac and Windows through Partial
Rewrite" (May 2024).

**`UsernsMode`:** Podman rootless requires `keep-id` user namespace mode. yoloAI already handles
this via the `UsernsProvider` optional interface on `runtime.Runtime`. Podman handles the same
problem at the libpod/ABI layer, not the CLI layer.

### What survived

The `ContainerEngine` interface boundary has been sustained across the ABI/Tunnel split for years.
Adding a new backend (if one were added) would mean implementing `ContainerEngine` in a new
package, not modifying CLI commands.

### What broke down

The `EngineMode` annotation pattern is a pragmatic admission that not all commands are meaningful
in all modes. Rather than having the `ContainerEngine` interface return "not supported" errors at
runtime, Podman hides commands at registration time. This is the interface-capability-flag approach
vs. the optional-interface approach. Both leak backend knowledge into the CLI, but in different ways:
the flag approach is explicit and visible in help text; the optional-interface approach fails
silently at runtime.

### Takeaway for yoloAI

Podman's `podman machine` pattern validates yoloAI's `system runtime` pattern: when backend-specific
management operations are genuinely irreducible to the generic interface, give them a named
subcommand that is explicitly scoped to that backend. The alternative — forcing these operations
through the generic interface — would make the interface unwieldy for all backends. The `EngineMode`
annotation pattern is also instructive: hiding commands per backend context in help text is preferable
to letting them fail at runtime with confusing errors.

---

## Comparator 6: HashiCorp Tools (Vault, Consul, Nomad)

### Structural pattern

All three tools follow the same architecture:

```
User
  │
  ▼
CLI (command/ package)
  │  uses Go API client library
  ▼
Go API client library (api/ package)
  │  HTTP to all endpoints
  ▼
Server HTTP API (/v1/...)
```

Vault CLI: every command calls `c.Client()` (returns `*api.Client`), then invokes API methods. The
CLI is explicitly documented as "just a wrapper around a robust HTTP-based API." Source: HashiCorp
support article "Translate Vault CLI commands to HTTP API."

Nomad CLI: `Meta.Client()` returns the Go API client. Commands invoke `client.Jobs().Register()`,
`client.Nodes().List()`, etc. Source: `deepwiki.com/hashicorp/nomad/4.3-command-line-interface` —
"The CLI uses the Go API client, which in turn communicates with the HTTP API server."

Consul follows the same pattern.

The key architectural property: the CLI has zero knowledge of server internals. The HTTP API is the
only boundary. The Go API client library is the idiomatic way to consume that API in Go programs,
but it is not special — anything that can make HTTP calls can use the server.

### Concrete leaks

**Secret-engine-specific paths:** Vault's CLI has commands like `vault kv get` which are specialized
for the KV secrets engine. These commands have knowledge of KV-specific path conventions and response
shapes. This is unavoidable — a CLI that surfaces the full Vault API will have commands that
understand specific secret engines.

**Nomad `job run` semantic complexity:** The `job_run.go` command handles "monitor mode" — polling
evaluations and deployments until stable. This orchestration logic lives in the CLI command, not in
the API client. The API client only knows about individual resource operations; the multi-step
orchestration (submit → evaluate → deploy → stabilize) is CLI-layer logic. This is a form of leak
where the CLI knows the stateful orchestration flow of the backend system.

### What survived

The HTTP API boundary has kept all three CLIs clean of server internals for multi-year evolution.
Vault's CLI has never needed to change due to storage backend changes (Consul vs Raft vs etc.) —
the storage backend is entirely behind the HTTP API.

### What broke down

No significant architectural breakdowns in this comparator. The HTTP-first approach has been
remarkably durable. The only honest criticism is that it requires a running server, which makes
local testing harder. Vault Agent, Consul Template, and Nomad Pack are all additional Go programs
that consume the same API — this is the intended extensibility story.

### Takeaway for yoloAI

The HTTP-first pattern is not applicable to yoloAI's primary CLI use case (the CLI and the backend
run in the same process). But the pattern becomes relevant if yoloAI adds an HTTP API or MCP server
in the future: the public `yoloai.Client` API would become the Go analogue of the HashiCorp Go
client libraries — a first-class, public API that the CLI consumes rather than bypasses. The fact
that the CLI bypasses `yoloai.Client` today is the direct analogue of Nomad's CLI calling the HTTP
API directly rather than using the published Go library.

---

## Comparator 7: GitHub CLI (`gh`)

### Structural pattern

```
User
  │
  ▼
gh (github.com/cli/cli)
  │  uses api.Client for GitHub API calls
  │  uses git package for local git operations
  ▼
api.Client  (pkg/api)
  │  REST or GraphQL depending on endpoint
  ▼
GitHub API (REST v3 + GraphQL v4)
```

gh is a Cobra-based CLI that uses an API client abstraction for all GitHub interactions. Source:
`github.com/cli/cli/blob/trunk/pkg/cmd/pr/create/create.go` — confirmed via WebFetch: "The code
calls methods like `api.SuggestedReviewerActorsForRepo()` rather than constructing requests
directly."

**Feature detection pattern:** gh uses a `fd.Detector` to determine API capabilities: if
`ApiActorsSupported` is true, it uses GraphQL-based reviewer selection; otherwise it falls back to
legacy ID-based selection for GitHub Enterprise Server. This is backend-version-aware code in the
CLI layer, but it reads from a capability flag rather than hard-coding API version numbers.

**IOStreams abstraction:** `pkg/iostreams/iostreams.go` defines an `IOStreams` struct that abstracts
stdin/stdout/stderr and terminal capabilities (TTY detection, color, pager). All commands receive an
`IOStreams` rather than calling OS I/O directly. This pattern is directly applicable to any CLI
that needs to support both interactive and pipe modes.

### Concrete leaks

**REST vs GraphQL awareness:** Some commands use REST (issues), some use GraphQL (PR creation with
reviewers), some use both. The API surface is not uniform. The abstraction (`api.Client`) provides
both surfaces, but commands must know which one to use for their specific operation.

**Enterprise vs github.com divergence:** The feature detection pattern (`ApiActorsSupported`) means
the CLI contains branch logic based on the server's capabilities. This is the server-capability
detection approach — query the server for what it supports, then adapt. This mirrors Docker CLI's
`ServerInfo.OSType` check.

### Takeaway for yoloAI

The IOStreams pattern is worth adopting for any future library or HTTP API surface built on top of
yoloAI. The feature detection pattern (query backend descriptor, adapt behavior) is already
present in yoloAI's `BackendCaps` and the `ContainerAttach` capability check in
`sandbox_vscode.go`. This is the right approach and should be extended rather than replaced with
explicit backend name checks.

---

## Synthesis

### Three Applicable Patterns

#### Pattern A: Interface + Capability Flags (current trajectory, with discipline)

The `runtime.Runtime` interface already exists. `BackendCaps` and optional interfaces
(`CopyMountResolver`, `StdioExecer`, etc.) already handle optional features. The CLI queries
capabilities (`ContainerAttach`, `OverlayDirs`) rather than checking backend names.

**What it requires:** Consistent application across all commands. The CLI layer must never import
concrete backend packages except at the single registration/selection point (`helpers.go`
`newRuntime()`). Backend-specific subcommands (`system runtime`) are modeled as capability-gated
extensions of the CLI, not as leaks.

**Tradeoffs:**
- Pro: No runtime overhead, no process boundary, direct Go calls.
- Pro: Adding a new backend means implementing the interface; the CLI works automatically.
- Con: Interface discipline erodes under schedule pressure. One expedient `(*tart.Runtime)` type
  assertion opens the door to more.
- Con: The `InstanceConfig` struct accumulates backend-specific fields. Each new backend wants its
  own config knobs that do not apply to others.

**Where leaks remain:** Routing logic (backend selection) must name backends somewhere. The
`resolveBackend()` function in `helpers.go` is the correct single location for this. If a new
backend adds genuinely irreducible management operations (like Tart's simulator runtimes), those
become backend-specific subcommands with explicit platform/backend guards. The leak is contained
to the subcommand, not distributed through the generic commands.

**yoloAI fit:** High. Already partially implemented. Requires enforcement (no concrete backend
imports outside `newRuntime()`'s package) and the CLI using `yoloai.Client` or `sandbox.Manager`
for orchestration rather than calling `runtime.Runtime` directly.

---

#### Pattern B: Capability-Gated Subcommand Groups (Podman `podman machine` model)

Backend-specific features that cannot be expressed through the generic `runtime.Runtime` interface
get their own named subcommand group that is explicitly scoped to a backend or platform. The generic
commands remain clean. The backend-specific subcommands import concrete backend packages freely
because they are already explicitly backend-scoped.

**Structure for yoloAI:**
```
yoloai system runtime ...   ← Tart-only, explicit; currently exists, correctly gated
yoloai system docker ...    ← Docker-specific management (hypothetical)
yoloai system seatbelt ...  ← Seatbelt-specific management (hypothetical)
```

The generic commands (`new`, `attach`, `diff`, `apply`, `destroy`) stay clean. Backend-specific
commands are documented and user-visible as backend-specific.

**Tradeoffs:**
- Pro: Generic commands stay clean by structural enforcement, not just convention.
- Pro: Backend-specific commands can be as rich as needed without polluting the generic interface.
- Pro: Users know exactly where backend-specific controls live.
- Con: Adds surface area to the CLI. Users may not discover backend-specific commands.
- Con: Still requires a selection/routing mechanism for the generic commands.

**Where leaks remain:** The routing in `resolveBackend()` still names backends. The capability
flags in `BackendCaps` are still read by generic command code (e.g., `sandbox_vscode.go`). These
are acceptable leaks — they are contained to defined locations.

**yoloAI fit:** High, and already partially implemented (`system runtime` exists). The pattern to
enforce is: backend-specific operations → backend-specific subcommand; generic operations → clean
interface.

---

#### Pattern C: Orchestration Client API as the CLI Boundary (HashiCorp / Docker model)

The CLI does not call `runtime.Runtime` or `sandbox.Manager` directly. It calls the public
`yoloai.Client` API, which is the same API available to any Go program embedding yoloAI. The
`yoloai.Client` encapsulates orchestration logic. The CLI becomes a thin presentation layer over the
client API.

**Structure:**
```
internal/cli/  →  yoloai.Client (yoloai.go)  →  sandbox.Manager  →  runtime.Runtime  →  backends
```

This matches how Vault CLI uses `api.Client`, how Nomad CLI uses `nomad/api`, and the documented
intent of `yoloai.Client` (`yoloai.go` header: "for embedding yoloAI in Go programs without
interacting with the CLI or sandbox package").

**Tradeoffs:**
- Pro: Creates a verifiable API contract. The CLI uses the same API as any future MCP server or HTTP
  API wrapper, so regressions in the library surface are caught by CLI tests.
- Pro: Forces orchestration logic into `yoloai.Client`, preventing drift between "what the CLI does"
  and "what the library does."
- Pro: Directly enables future MCP server or HTTP API wrapper with zero duplication.
- Con: Requires `yoloai.Client` to be rich enough to express all CLI operations. Complex operations
  (interactive attach, overlay diff, selective apply) may need new API methods.
- Con: The backend-specific subcommands (`system runtime`) still cannot go through a generic client
  API without making the API Tart-specific. These remain as CLI-direct-to-backend code.
- Con: `yoloai.Client` becomes a bottleneck — every new feature needs a library method before the
  CLI can use it.

**Where leaks remain:** Backend-specific subcommands still import concrete backends. The `Options`
struct on `yoloai.Client` must expose backend selection (currently it does: `Options.Backend`).
Any backend-specific feature that cannot be abstracted through the client API will tempt direct
bypass.

**yoloAI fit:** Medium-high. The `yoloai.Client` already exists but is bypassed by the CLI. Moving
to this pattern requires a deliberate step: expand `yoloai.Client` to cover the full CLI surface,
then wire the CLI through it. This is the most work upfront but the clearest long-term boundary.

---

### yoloAI-Specific Concerns and Pattern Fit

| Concern | A (Interface + Caps) | B (Capability Subcommands) | C (Client API Boundary) |
|---|---|---|---|
| Backend-specific commands (Tart runtime mgmt) | Contains leak to import scope | Correct pattern — explicit subcommand | Cannot eliminate — still direct backend access |
| Interactive attach (TTY, streaming) | Interface method; backend implements | Same | Needs `yoloai.Client.Attach()` method |
| Future MCP server / HTTP API | Must duplicate orchestration logic | Must duplicate orchestration logic | Zero duplication — MCP uses same API |
| Backend routing naming backends | Confined to `resolveBackend()` | Same | Confined to `yoloai.Client` options |
| `InstanceConfig` backend-specific fields | Grows over time | Same | Same; `yoloai.Client` options grow instead |
| New backend with unique management needs | Tempts interface bloat | Explicit subcommand, clean | Tempts `yoloai.Client` method proliferation |
| Preventing erosion under pressure | Requires code-review enforcement | Structurally enforced | Requires `yoloai.Client` completeness |

### Which Leaks Each Pattern Cannot Contain

**Pattern A** cannot contain:
- Developer pressure to type-assert to concrete backends for one-off features. The pattern requires
  social/review enforcement, not structural enforcement.
- `InstanceConfig` field accumulation for backend-specific knobs.
- Routing logic naming specific backends (this is inherent, not a failure).

**Pattern B** cannot contain:
- Routing in the generic commands still names backends.
- Capability flags in generic commands still query backend-specific behavior.
- Platform-detection code (`runtime.GOOS == "darwin"`) in the CLI guards.

**Pattern C** cannot contain:
- Backend-specific subcommands (Tart runtime mgmt) — these cannot go through a generic client API
  without making the API platform-specific.
- Protocol-level streaming details (the `yoloai.Client.Attach()` method still needs to express
  backend-specific TTY handling).
- `yoloai.Client.Options.Backend` names specific backends — the selection leak is pushed into the
  client API options rather than eliminated.

### Recommended Combination

The comparator evidence suggests the most pragmatic architecture for yoloAI is **Patterns B + C
combined:**

1. **Wire the CLI through `yoloai.Client`** for all generic operations (Pattern C). This enforces
   the library boundary, prevents orchestration drift, and directly enables a future MCP server or
   HTTP wrapper. The `yoloai.Client` becomes the real boundary, not a documentation aspiration.

2. **Backend-specific subcommands explicitly use concrete backends** (Pattern B) and are documented
   as such. These are not failures of the abstraction; they are acknowledged feature extensions for
   specific backends. The `system runtime` pattern is correct — extend it consistently.

3. **Keep `BackendCaps` and optional interfaces** (Pattern A) for the runtime.Runtime layer. This
   remains the right intra-process abstraction for backends. The optional interface pattern (as used
   in yoloAI's `StdioExecer`, `CachePruner`, etc., and documented in the DoltHub interface
   extension blog post) is the idiomatic Go way to express optional backend capabilities without
   interface bloat.

The honest assessment: even with this combination, three leak categories will remain:

- **Backend naming in routing** — `resolveBackend()` or `yoloai.Client` option selection will
  always name specific backends. This is inherent.
- **Platform guards** — `runtime.GOOS == "darwin"` checks will always appear near backend-specific
  subcommands. These are correct and should remain visible.
- **Backend-specific config knobs** — `InstanceConfig.ContainerRuntime` (containerd-specific) and
  similar fields will accumulate. Document which fields apply to which backends; consider a
  `BackendSpecific map[string]any` escape hatch over time rather than proliferating named fields.

---

## Source Index

- Docker CLI source: `github.com/docker/cli/blob/master/cli/command/cli.go`,
  `github.com/docker/cli/blob/master/cli/command/container/run.go`
- Docker CLI API communication deep-dive: `deepwiki.com/docker/cli/4-engine-api-communication`
- Moby project relationship: `github.com/moby/moby/issues/38063`
- kubectl exec streaming: `k8s.io/kubectl/pkg/cmd/exec/exec.go`
- kubectl port-forward: `k8s.io/client-go/tools/portforward/portforward.go`
- SPDY→WebSocket KEP: `github.com/kubernetes/enhancements/tree/master/keps/sig-api-machinery/4006-transition-spdy-to-websockets`
- SPDY goroutine leak: `github.com/kubernetes/kubernetes/issues/105830`
- WebSocket support issue: `github.com/kubernetes/kubernetes/issues/89163`
- Terraform plugin protocol: `github.com/hashicorp/terraform/blob/main/docs/plugin-protocol/README.md`
- OpenTofu backends-as-plugins: `github.com/opentofu/opentofu/issues/382`
- containerd shim architecture: `deepwiki.com/containerd/containerd/5.1-shim-architecture`
- nerdctl command reference: `github.com/containerd/nerdctl/blob/main/docs/command-reference.md`
- nerdctl macOS defaults issue: `github.com/containerd/nerdctl/issues/4572`
- Podman run.go: `github.com/containers/podman/blob/main/cmd/podman/containers/run.go`
- Podman command structure: `deepwiki.com/containers/podman/11.1-command-structure-and-registration`
- Podman machine architecture: `redhat.com/en/blog/podman-mac-machine-architecture`
- Podman 5.0 machine rewrite: InfoQ "Podman 5 Improves Performance and Stability" (May 2024)
- Nomad CLI: `deepwiki.com/hashicorp/nomad/4.3-command-line-interface`,
  `github.com/hashicorp/nomad/blob/main/command/job_run.go`
- Vault CLI: `github.com/hashicorp/vault/blob/main/command/kv_get.go`
- HashiCorp go-plugin: `github.com/hashicorp/go-plugin`,
  `eli.thegreenplace.net/2023/rpc-based-plugins-in-go/`
- GitHub CLI: `github.com/cli/cli/blob/trunk/pkg/cmd/pr/create/create.go`,
  `github.com/cli/cli/blob/trunk/pkg/iostreams/iostreams.go`
- Go interface extension pattern: `dolthub.com/blog/2022-09-12-golang-interface-extension/`
- Go CLI architecture advice: `blog.carlana.net/post/2020/go-cli-how-to-and-advice/`

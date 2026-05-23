<!-- ABOUTME: Design for yoloAI's CLI/orchestration/backend layering: how leaks above -->
<!-- ABOUTME: the runtime boundary are contained, and what discipline the architecture imposes. -->

> **Design documents:** [README](README.md) | [Commands](commands.md) | [Config](config.md) | [Setup](setup.md) | [Security](security.md)
> **Backing research:** [Leak Audit](../dev/research/layering-leak-audit.md) | [Comparator Research](../dev/research/layering-comparators.md)

# yoloAI Layering Architecture

**Status:** Proposed (May 2026, public beta).
**Authority:** This document defines the upper-layer architecture for yoloAI: how the CLI relates to the orchestration core (`yoloai.Client`), and how both relate to the pluggable runtime backends. It does **not** redesign the runtime interface itself — that work is tracked separately in [architecture-remediation.md W11](../dev/plans/architecture-remediation.md).

---

## 1. Why this design exists

yoloAI integrates many independently-idiosyncratic technologies — Docker, Podman, Tart, Seatbelt, containerd, Kata, gVisor, Firecracker, plus multiple agents and three host operating systems. The maintenance cost of this surface is dominated by **how cleanly the layers are kept apart**, not by the technologies themselves.

The empirical [leak audit](../dev/research/layering-leak-audit.md) shows that backend-specific knowledge is already bleeding above the runtime boundary (31 findings; 8 HIGH severity). The most consequential structural fact: **`internal/cli/` and `yoloai.Client` are parallel implementations of the same product, not a layered one.** The CLI never calls Client. Each new feature must be implemented twice — or, more commonly, only in the CLI, leaving the public API incomplete.

Beta is the right time to fix this. After v1, the surface becomes a contract.

---

## 2. Goals and non-goals

### Goals

- **One orchestration core.** Every CLI command, and every future surface (MCP server, HTTP API, library), routes through the same Go API.
- **Contain backend leaks in named, justified locations.** Routing/selection logic names backends in one chokepoint; backend-specific operations live in explicitly-scoped subcommand groups.
- **Capability flags and optional interfaces replace backend-name checks** wherever a feature isn't inherently backend-specific.
- **No concrete backend imports outside the runtime registration chokepoint and explicitly backend-scoped subcommands.**
- **Single source of truth for backend metadata.** One registry drives the CLI's backend table, the interactive setup wizard, the bug report writer, and any future surface.

### Non-goals

- Redesigning `runtime.Runtime` itself. Capability flags and optional interfaces are already the pattern there ([W11](../dev/plans/architecture-remediation.md)).
- Splitting `yoloai.Client` into multiple packages.
- Process-boundary plugins (Terraform-style gRPC providers). Backends remain in-process Go code.
- Network isolation, agent abstraction, profile/Dockerfile system — orthogonal concerns.
- A daemon mode or HTTP server. These are enabled by this design but not delivered by it.

---

## 3. Architectural principles

These principles are the tiebreakers for design questions that arise later. They cite [`docs/dev/principles/`](../dev/principles/README.md) where applicable.

1. **Layering is a structural commitment, not a convention.** A "thin CLI" that bypasses its core whenever convenient is worse than no separation at all — it creates two implementations to keep in sync. Either the CLI goes through `yoloai.Client`, or the Client is removed.

2. **Capability detection beats backend naming.** If a generic command needs to behave differently per backend, the difference should be expressed as a capability flag or optional interface query — never as a backend-name string comparison.

3. **Acknowledged leaks beat hidden ones.** When a feature is genuinely backend-specific (Tart's Apple simulator runtimes), give it a backend-named subcommand. Do not smuggle it through the generic interface with a backend type assertion. This is nerdctl's 🤓 / 🐳 convention and Podman's `machine` model — comparators agree.

4. **One source of truth per concept.** Backend metadata, isolation modes, and routing logic each live in exactly one place. The CLI consumes them; it does not maintain parallel copies.

5. **The presentation layer owns presentation.** TTY detection, color, verbosity, prompts, output format, exit codes — these belong in the CLI. The orchestration core returns data, not formatted strings.

---

## 4. Proposed architecture

### Three layers

```
                        ┌──────────────────────────────────────────────┐
   Presentation         │  internal/cli/   (Cobra commands, IOStreams) │
   (one per surface)    │  future: internal/mcp/, internal/http/       │
                        └──────────────────┬───────────────────────────┘
                                           │  yoloai.Client API
                        ┌──────────────────▼───────────────────────────┐
   Orchestration        │  yoloai.Client (yoloai.go + internal pkgs)   │
   (single core)        │  Run, Apply, Diff, Destroy, List, Inspect... │
                        │  sandbox.Manager, state machine, archetypes  │
                        └──────────────────┬───────────────────────────┘
                                           │  runtime.Runtime interface
                        ┌──────────────────▼───────────────────────────┐
   Backend abstraction  │  runtime/ — Runtime interface, BackendCaps,  │
   (one per backend)    │  optional capability interfaces, Descriptor  │
                        │     docker  podman  tart  seatbelt  containerd │
                        └──────────────────────────────────────────────┘
```

This is the standard layering used by Docker (`docker/cli` → Engine API → containerd), Podman (`cmd/podman` → `ContainerEngine` interface → libpod or REST), HashiCorp tools (CLI → API client → server), and GitHub CLI (`cli/cli` → `api.Client` → GitHub REST/GraphQL). The comparator research found no project of comparable scope sustained a clean architecture by other means.

### Pattern combination

Per the [comparator research synthesis](../dev/research/layering-comparators.md#synthesis), three patterns combine to address yoloAI's specific shape:

- **Pattern C — Orchestration Client API as the CLI boundary.** The CLI consumes `yoloai.Client`. This eliminates the parallel-implementations problem and enables future MCP/HTTP surfaces with zero duplication. This is the work-heavy refactor.

- **Pattern B — Capability-gated subcommand groups.** Backend-specific operations live in explicitly-named subcommand groups (`yoloai system tart`, hypothetically `yoloai system kata`). These import their backend's package freely; the scoping is explicit. Generic commands stay clean by structural exclusion, not convention.

- **Pattern A — Capability flags + optional interfaces.** At the runtime layer, `BackendCaps` and optional interfaces (`CopyMountResolver`, `StdioExecer`, `UsernsProvider`, etc.) carry backend-specific behavior. Generic commands query capabilities; they don't switch on backend name.

### Layer responsibilities

| Layer | Owns | Does not own |
|---|---|---|
| **Presentation** (`internal/cli/`) | Cobra command definitions, flag parsing, argument validation, output formatting (text/JSON), TTY/color detection, interactive prompts, exit codes, progress indicators, help text | Orchestration policy, state machine logic, backend selection, runtime-interface knowledge |
| **Orchestration** (`yoloai.Client`) | Operation semantics (Run/Apply/Diff/...), workflow sequencing, state machine transitions, options validation, error typing, backend selection, runtime wiring | TTY/output, command-name vocabulary, argv parsing, progress display |
| **Backend** (`runtime/`) | The `Runtime` interface, per-backend implementations, `BackendDescriptor` metadata, capability flags, optional capability interfaces, availability probing | Sandbox state machine, copy/diff/apply policy, agent lifecycle |

### Where each leak category lives

| Leak category | Where it's allowed | Why |
|---|---|---|
| Backend names in routing/selection | `yoloai.Client` options resolution + `runtime.Registered()` iteration | Selection inherently must name choices |
| Backend names in user help text | Help files describing a flag whose values map to impl tech (`--isolation container-enhanced (gVisor)`) | nerdctl pattern — honest documentation |
| Backend names in backend-scoped subcommands | `yoloai system <backend> ...` | Explicit scope makes the import honest |
| Concrete-backend type assertions | Backend-scoped subcommands only; never in generic commands or `sandbox/` | The optional-interface pattern handles generic cases |
| Backend-specific config knobs | `InstanceConfig` named fields (current), trending toward `BackendSpecific map[string]any` over time | Some are inherent; cap proliferation by the escape hatch |
| Platform guards (`runtime.GOOS == "darwin"`) | Anywhere needed; correct and visible | Host-OS detection isn't a backend leak |

### Where leaks are not allowed

- `internal/cli/` importing `runtime/docker`, `runtime/tart`, etc. **except**:
  - The single chokepoint that registers and selects backends.
  - Backend-scoped subcommand packages (e.g., `internal/cli/tart/`).
- `sandbox/` importing any concrete backend package. Optional interfaces or `BackendDescriptor` calls replace these.
- Hard-coded backend tables in CLI files (`knownBackends`, `availableBackends`). Drive from `runtime.Registered()`.
- Backend-name string checks (`if backend == "docker"`) outside the runtime registry. Use capability queries.

---

## 5. Concrete designs

### 5.1 `yoloai.Client` as the CLI's spine (Pattern C)

The CLI's command handlers shrink to: parse flags → build `yoloai.<Operation>Options` → call `client.<Operation>(ctx, opts)` → format the returned data → exit code from typed error.

**What `yoloai.Client` must expose** to subsume current CLI behavior (gaps identified by [audit §4](../dev/research/layering-leak-audit.md#4-cli-orchestration-overlap)):

- Lifecycle: `Run`, `Start`, `Stop`, `Restart`, `Destroy`, `List`, `Inspect`. *(Partial today.)*
- Review: `Diff` (with overlay path), `Apply` (with overlay, format-patch, selective, squash, export paths). *(Today's Client lacks overlay and format-patch — major gap.)*
- Interactive: `Attach`, `Exec`. The streaming TTY shape comes from the kubectl comparator — protocol-level details may need to leak through the API signature (e.g., explicit stdin/stdout/stderr streams) rather than being hidden.
- Filesystem/state: `Reset`, `Baseline`.
- Introspection: `Backends() []BackendDescriptor`, `Profiles()`, `Version()`.
- Diagnostics: `BugReport(ctx, opts) (BugReport, error)` returning structured data; the CLI renders.

**Options structs** mirror flag groups. Each `<Op>Options` is a Go struct with named fields. Flags map 1:1 (CLI does the parsing); JSON/MCP surfaces map similarly. Defaulting and validation live in `yoloai.Client`, not the CLI.

**Output**: Client methods return typed data (`SandboxInfo`, `DiffResult`, etc.). The CLI formats. This is the kubectl/client-go boundary that survived 10+ years.

**What the CLI keeps**: flag parsing, presentation, `IOStreams`, interactive confirmation prompts, progress display, exit-code mapping from typed errors, the `--json` switch (which selects formatter, not behavior).

### 5.2 Capability-scoped subcommands (Pattern B)

Operations that cannot be expressed generically get their own subcommand group, explicitly backend-scoped:

```
yoloai system tart runtime create ...    (Apple simulator runtimes)
yoloai system tart runtime list
yoloai system tart base list
(hypothetical) yoloai system kata vm-inspect
(hypothetical) yoloai system docker network-prune
```

**Convention**: `yoloai system <backend> <feature> ...`. The leading `system` distinguishes administrative subcommands from sandbox operations. The `<backend>` is explicit. Within these packages, importing `runtime/<backend>` directly and calling typed APIs is **honest**, not a leak — the command path declares its scope.

**Today's `yoloai system runtime` is mis-scoped.** It's effectively `yoloai system tart` but reads as generic. [Recommendation L19](../dev/research/layering-leak-audit.md#6-recommendations) is to rename, removing the false-generic surface.

**Test for "should this become a backend-scoped subcommand?"** — if a feature requires importing a specific backend package or type-asserting `runtime.Runtime` to a concrete type, it either (a) becomes a backend-scoped subcommand, or (b) gets an optional interface (`AppleSimulatorRuntimes`, `NetworkAttachable`, etc.) that `sandbox/` can detect generically. (b) is preferred for features that compose with generic operations; (a) for features that stand alone.

### 5.3 Runtime layer: capabilities and probes (Pattern A)

`BackendDescriptor` is the **single source of truth** for backend metadata. It grows:

- **Existing**: `Name`, `Description`, capability flags (`ContainerAttach`, `OverlayDirs`, etc.).
- **Adds** (from audit recommendations):
  - `Probe(ctx) (Available bool, Reason string)` — replaces `dockerAvailable()` and `podmanrt.SocketExists()` (L3, L4).
  - `VersionString(ctx) (string, error)` — replaces the hard-coded version-query table in `bugreport_writer.go` (L16).
  - `CleanupHint(image string) string` — replaces the unconditional Docker cleanup hint after profile delete (L14).
  - `HostFromContainerHostname() string` — replaces `host.docker.internal` literals in help and error text (L15, L30).
  - `Platforms []string`, `Requires string`, `Notes string` — populates the `info.go` table from the descriptor (L8).

Optional interfaces ([Go interface extension pattern](https://www.dolthub.com/blog/2022-09-12-golang-interface-extension/)):

- `AppleSimulatorRuntimes` (currently inlined in Tart) — lets `sandbox/create.go` detect via `if irt, ok := rt.(AppleSimulatorRuntimes); ok { ... }` instead of importing `runtime/tart` (L28).
- Future: `KataConfigurable`, etc. Each new backend-specific feature surface gets either a backend-scoped subcommand (§5.2) or an optional interface, never a string check or type assertion in generic code.

Isolation helpers (`runtime/isolation.go`) grow:

- `SupportsOverlayDirs(isolation string) bool` — replaces the `"container-enhanced"` string check in `sandbox/create_instance.go` (L29).
- `IsolationAvailability(mode, hostOS) (Available, Reason, Link)` — replaces the gVisor-named bug-link in `validateIsolationOSCombo` (L7).

### 5.4 The single backend chokepoint

Routing logic lives in **one** function: `yoloai.Client.resolveBackend(opts) string`. It names backends. It is the only place outside `runtime/` that does. `internal/cli/helpers.go`'s current `resolveBackend` becomes a thin caller. The duplicate in `yoloai.go` (L2) is deleted.

`helpers.go`'s `dockerAvailable()`, the `podmanrt` named import, and any other backend-detection logic moves into the respective backend's `Probe()`.

### 5.5 Acknowledged inherent leaks

These remain after the refactor and are accepted as inherent. They are documented here so future contributors do not chase them as bugs:

1. **Backend names in `Options.Backend`** — selection must name choices.
2. **Backend impl names in `--isolation` help text** — honest documentation per nerdctl convention.
3. **Backend-named subcommand paths** (`yoloai system tart ...`) — by design.
4. **Platform guards on backend-scoped subcommands** (`if runtime.GOOS != "darwin" { return err }` on Tart commands) — correct.
5. **BackendSpecific config knobs** — `InstanceConfig.ContainerRuntime`, `InstanceConfig.TartImage`. Constrain growth via a typed `BackendSpecific map[string]any` escape hatch when the named-field count crosses a threshold (~4–5).

The architectural commitment is: **everything else is fixable**, and any new instance is treated as a bug.

---

## 6. Public API stability — decoupled

`yoloai.Client` being the CLI's spine (architectural) is **separate** from `yoloai.Client` being declared externally stable (versioning commitment).

**Recommended:** treat `yoloai.Client` as **internal-grade** for the duration of the refactor and through the immediate v1 release. The package stays at the repo root (since Go's `internal/` convention would prevent external use even when ready), but documentation and `ARCHITECTURE.md` describe it as "the orchestration spine — used by the CLI; not yet under stability guarantees."

**When to flip the switch:**
- A real external consumer (MCP server, HTTP wrapper, library use) materializes, **or**
- A user explicitly requests it with a use case we want to support.

At that point, add a BREAKING-CHANGES.md policy section for `yoloai.Client`, and treat changes as semver-major from there. Until then, the surface evolves freely.

This resolves [OPEN_QUESTIONS.md #101](../dev/OPEN_QUESTIONS.md) — the question stops being "does an external consumer exist?" and becomes "is the CLI a thin shell?", with the stability declaration deferred until there's a consumer to support.

---

## 7. Decisions

Recorded resolutions, with evidence. All decisions below are locked in for the rearchitecture as of 2026-05-23.

| # | Decision | Resolution | Evidence |
|---|---|---|---|
| **D1** | Rename `yoloai system runtime` → `yoloai system tart`? | **Yes.** Current name is mis-scoped (reads generic, is Tart-only). | Audit L19; comparators §5 (Podman `machine`). |
| **D2** | Move `EmbeddedTmuxConf` out of `runtime/docker`? | **Yes** — into `internal/resources/tmux` or `sandbox/tmuxconf`. Used by every backend. | Audit L27. |
| **D3** | `yoloai.Client` stability declaration? | **Defer.** Internal-grade through v1; declare external only when a consumer appears. | §6; OPEN_QUESTIONS #101. |
| **D4** | Add `BackendSpecific map[string]any` escape hatch to `InstanceConfig` now? | **Wait** — until named fields cross ~4–5 backend-specific entries. Premature now. | §5.5; comparator synthesis. |
| **D5** | Backend-scoped subcommand naming: top-level `yoloai <backend> ...` or `yoloai system <backend> ...`? | **Keep `system <backend>`** — administrative ops are not sandbox lifecycle ops. | §5.2; commands.md convention. |
| **D6** | `--security` flag deprecation note in BREAKING-CHANGES.md? | **Conditional** — yes if `--security` shipped in any released version; no otherwise. Verify before W-L9. | Audit Q4. |
| **D7** | Expose impl tech in `--isolation` long help (`vm (Kata+QEMU)`) vs hide it? | **Keep as-is.** Power users benefit from knowing what powers each mode (nerdctl pattern); abstractions that lie are worse. | §5.5; comparators §4. |
| **D8** | Move `--runtime` (Apple simulator) out of `sandbox/create.go` — optional interface or tart-scoped extraction? | **Optional interface** (`AppleSimulatorRuntimes`). Matches yoloAI's existing pattern. | Audit L28; §5.3. |
| **D9** | Q100: dual command dispatch — keep both `yoloai diff <name>` and `yoloai sandbox <name> diff`, or deprecate one? | **Keep both.** Both paths pass an explicit name to the same Client method; cost is presentation test surface, not API shape. Q100 closed as deferred-indefinitely. | OPEN_QUESTIONS #100; layering-open-questions.md §Q100. |
| **D10** | MCP Go SDK choice: official `modelcontextprotocol/go-sdk` vs community `mark3labs/mcp-go`? | **Stay on `mark3labs/mcp-go`** (currently v0.54.0). Migrate to official SDK when either yoloai needs elicitation or the official's open #958 (panic-handling) closes. | [`mcp-sdk-evaluation.md`](../dev/research/mcp-sdk-evaluation.md). |
| **D11** | Q94: how to surface Tart's concurrent-VM limit — hard-code "2" or detect from Tart? | **Detect from Tart.** Read stderr/`vm.log` for `"The number of VMs exceeds the system limit"`; convert to typed `ErrConcurrentVMLimit`. No hard-coded number — tracks Apple's policy as it evolves. Verification on macOS required before commit. | [`tart-limit-detection.md`](../dev/research/tart-limit-detection.md). |
| **D12** | Q94: Xcode installation in Tart base images? | **User prerequisite.** Pre-installing inflates download (Xcode is ~30 GB); lazy install needs Apple ID interaction. Document; revisit if Tart usage shows friction. | Audit Q94; OPEN_QUESTIONS #94. |
| **D13** | `exit-codes.md` OQ1: wrap sentinel errors with `%w` vs replace with typed errors at origin? | **Replace at origin.** Aligns with W7 (typed errors → `yoerrors/`). | exit-codes.md OQ1. |
| **D14** | Package paths: migrate `sandbox/` → `internal/orchestration/` and `runtime/` → `internal/runtime/`? | **In scope, deferred to after W-L8e.** Mechanical but disruptive (every import path changes); cleaner as one rename PR once W-L8 is stable. Tracked as W-L12. | layering-greenfield.md §8. |
| **D15** | CLI directory reorg: `internal/cli/*.go` flat → grouped subdirs? | **In scope, deferred to after W-L8e.** Same posture as D14 — mechanical, lower priority than the architectural work. Tracked as W-L13. | layering-greenfield.md §8. |
| **D16** | `yoloai mcp` command group placement: Lifecycle (current) or Admin (greenfield)? | **Move to Admin.** MCP server is administrative surface, not sandbox lifecycle. | layering-greenfield.md §4. |
| **D17** | Q77 `yoloai wait` command — add as Client method during the refactor? | **Yes.** Include `Client.Wait(ctx, name, opts) (exitCode int, err error)` in W-L8a's catalog. Enables CI/scripting workflows. | OPEN_QUESTIONS #77; §9.2. |
| **D18** | Q93 (MCP-in-container), Q97 (network allowlist audit), Q102 (setup_test.go size), network-isolation OQs — address during the refactor? | **Defer all.** None affect architecture shape. Park as discovered-findings if surfaced mid-workstream. | layering-open-questions.md §INFLUENCES. |
| **D19** | Discovered-findings file (`docs/dev/discovered-findings.md`)? | **Create empty with header.** Done 2026-05-23. | [`discovered-findings.md`](../dev/discovered-findings.md). |

---

## 8. What this design accepts as residual

The honest assessment from the comparator research is that even a well-executed combination of Patterns B + C + A leaves leak categories that **no architecture in this space has fully solved**:

- **Streaming/interactive protocol leaks.** kubectl's SPDY→WebSocket transition (KEP-4006, 2023–) demonstrates that even Kubernetes' typed-resource boundary can't contain protocol evolution in streaming operations. yoloAI's `Attach` / `InteractiveExec` may need backend-specific shape over time.
- **Backend-specific defaults.** nerdctl's macOS-defaults issue ([containerd/nerdctl#4572](https://github.com/containerd/nerdctl/issues/4572)) shows that one default can't be correct on every platform. yoloAI's per-OS default selection is the same shape.
- **Documentation burden.** Capability flags and mode-name help text describe what each option does; users still have to read it. No abstraction eliminates that.

The design's promise is not "no leaks." It is: **leaks are named, located, and motivated**. Any new instance is a bug, not an accumulation.

---

## 9. Forward-looking feature compatibility

The architecture must *tolerate* the following near-term features without redesign. The features themselves are not delivered by this design — they ship after the refactor — but the Client API surface (W-L8a) is designed with their shape in mind so adding them is a method addition, not a redesign.

### 9.1 Branch-aware apply

The agent may decide mid-session that work belongs on a feature branch. Inside the sandbox, the agent creates branches and commits to them. `yoloai apply` must mirror the sandbox's branch topology onto the host, not just apply commits to the host's current HEAD.

**Semantics:**

- Branches that exist in the sandbox but not on the host are created on the host pointing at the matching base commit, then their commits are applied via `git format-patch` + `git am`.
- WIP changes (uncommitted in the sandbox) are applied after committed changes, on the currently-active branch — which, after apply, is the same branch on the host as in the sandbox.
- **Branch-name conflict with the host: error.** No silent overwrite, no auto-suffix.
- **Merge conflict during apply: error.** We are not reimplementing git's merge logic.
- **Branch deletion in the sandbox does not propagate to the host.** Apply is additive.
- **Branch rename:** out of scope (nice-to-have, not planned).

**Implications for the Client API (W-L8a):**

- `ApplyOptions` includes a mode that triggers branch-mirroring (e.g. `MirrorBranches bool`, or a richer `BranchPolicy` if granularity is needed later).
- `ApplyResult` reports which branches were created/updated so the CLI can render a summary.
- `DiffResult` (and `yoloai diff` output) indicates when the sandbox's HEAD has diverged from the baseline branch, so the user knows a branch-aware apply will occur.

**Status:** Deferred. Implementation lands after the layering refactor. The architecture is responsible for not painting this feature into a corner.

### 9.2 `yoloai wait` (Q77)

A Client method that blocks until the sandbox's agent exits, returning the agent's exit code. Enables CI / scripting workflows ("run agent, wait, diff, apply, destroy" as one pipeline).

**Implication for the Client API:** include `Wait(ctx, name string, opts WaitOptions) (exitCode int, err error)` in W-L8a's catalog. Small addition; lands with the first wave of Client methods.

### 9.3 Additional presentation surfaces

The architecture is designed so that future surfaces (MCP server completion, potential HTTP API, library use) consume `yoloai.Client` exclusively. Adding a new surface is a new directory under `internal/<surface>/` and method wiring — not a Client redesign. MCP already exists but bypasses Client; W-L8b/d migrates it to the same surface as the CLI.

**Test for "is this surface compatible with the architecture?":** Can it be expressed as `parse-input → Client.Method(opts) → format-output`? If yes, add the surface. If no, the Client surface is missing the operation — extend the Client first.

---

## 10. References

- [Layering Leak Audit](../dev/research/layering-leak-audit.md) — empirical inventory of all 31 findings (L1–L31) with verdicts.
- [Comparator Research](../dev/research/layering-comparators.md) — Docker, kubectl, Terraform, containerd/nerdctl, Podman, HashiCorp, GitHub CLI.
- [W11 — Runtime interface split](../dev/plans/architecture-remediation.md#w11) — the lower-layer companion refactor.
- [OPEN_QUESTIONS.md #101](../dev/OPEN_QUESTIONS.md) — the original question this design resolves.
- [Implementation plan](../dev/plans/layering-refactor.md) — phased workstreams to land this design.

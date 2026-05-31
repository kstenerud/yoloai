<!-- ABOUTME: Pre-rearchitecture research report: open questions that affect the layering -->
<!-- ABOUTME: refactor + prior architectural models to reuse. Written 2026-05-23. -->

# Layering Refactor — Open Questions & Prior Models

**Purpose.** Pre-rearchitecture scan before kicking off W-L1. Two deliverables:
(1) unresolved decisions that constrain, block, or shape the layering refactor, and
(2) prior architectural work to reuse rather than reinvent.

**Date:** 2026-05-23.

---

## Executive Summary

Twelve unresolved questions affect the rearchitecture. One BLOCKS (Q100 — dual command
dispatch), which must be resolved before W-L8a can define the canonical Client method
surface. Three CONSTRAIN: Q94 macOS VM backend sub-questions limit the completeness of
`AppleSimulatorRuntimes` (W-L7); `backend-agent-extensibility.md` Issues 1–2 (not tracked
in OPEN_QUESTIONS) are unfinished siblings of W-L work that must be sequenced; and
`mcp-server.md` open question 3 (MCP SDK choice) affects whether the MCP server can
consume `yoloai.Client` as designed. Eight items are INFLUENCES — useful to know but
not gating.

The top concern is the dual-dispatch decision (Q100): the layering plan's W-L8a
explicitly designs the Client API surface, but that surface is different depending on
whether verb-first commands (`yoloai diff <name>`) or name-first commands
(`yoloai sandbox <name> diff`) are both canonical. If one is deprecated, the
`<Op>Options` struct shape simplifies. Decide before W-L8a.

The prior architectural work is extensive and largely directly usable. W11 (runtime
interface spike + implementation) is complete and aligns exactly with W-L3–L7. The
capability registry (`runtime/caps/`), optional interface pattern, and `BackendDescriptor`
registry are all in place. The development principles (boundary discipline §2, parse
don't validate §4, fail fast §5) are the direct rationale for every W-L workstream.
The main refactor (W-L8) has strong precedent from how W12 split `sandbox/`.

---

## Section 1: Open Questions

### BLOCKS

---

#### Q100 — Dual command dispatch (`yoloai diff <name>` vs `yoloai sandbox <name> diff`)

**Source:** `docs/contributors/design/unresolved-questions.md:252`

**The question:** Both dispatch paths exist (verb-first and name-first), implemented via
custom argument parsing in `internal/cli/commands.go` and `envname.go`. The question is
whether to keep both, deprecate one, or decide based on usage data that doesn't exist yet.

**Why it affects the rearchitecture:** W-L8a is a design review checkpoint: "map every CLI
command to a Client method." The Client method surface is *different* depending on how
dispatch resolves. If name-first dispatch survives, every operation may need a
`target.SandboxName` first-class field in `<Op>Options`. If verb-first is deprecated,
name-first becomes the canonical path and options structs simplify. The layering plan
(layering-refactor.md:218) calls out that "Streaming/interactive operations have explicit
stream-arg parameters in their signatures" — but this is only the streaming shape, not
the dispatch shape.

The greenfield layout (layering-greenfield.md:213) notes that dual dispatch is "a
separate decision tracked in OPEN_QUESTIONS #100" — explicitly not decided by the
layering architecture.

**Severity: BLOCKS.** W-L8a cannot produce an approved API surface without knowing
whether both dispatch paths continue. The Client method signatures differ.

**Recommended next step:** Decide now. The question asks for usage data, but that data
will not arrive before W-L8 needs to start. The practical decision is: (a) keep both
indefinitely and design the Client to support both (minor extra complexity); or (b) pick
one as canonical and document the other as a long-term deprecation. Either answer unblocks
W-L8a — the indecision blocks it.

---

### CONSTRAINS

---

#### Q94 — macOS VM backend sub-questions

**Source:** `docs/contributors/design/unresolved-questions.md:207–214`

**The question:** Six Tart-specific sub-questions remain open: image download/size, 2-VM
limit enforcement, Xcode installation, agent compatibility inside macOS VMs,
diff/apply workflow correctness with VirtioFS, and startup-time UX impact.

**Why it affects the rearchitecture:** W-L7 proposes an `AppleSimulatorRuntimes` optional
interface. The interface shape is `ConfigureSimulatorRuntimes(ctx, opts) error` — but
that shape is designed without knowing whether Xcode installation, VM limits, or agent
differences require additional methods. If Xcode or 2-VM-limit enforcement need to be
surfaced through the generic interface (rather than handled internally in `runtime/tart/`),
the interface needs more methods. If they stay internal to Tart, W-L7 lands as planned.

The diff/apply VirtioFS question is lower risk — `WorkDirSetup` is already an optional
interface for Tart's copy-path rewrite; if diff/apply needs adjustment it is a Tart
internals change, not an interface-boundary change.

**Severity: CONSTRAINS.** W-L7 can proceed with the current shape and adjust if a
Tart-specific method proves to be needed generically. But if any of the 6 sub-questions
requires a new optional interface (e.g. `VMCountEnforcer`, `XcodeProvisioner`), W-L7's
scope expands.

**Recommended next step:** Decide the 2-VM limit and Xcode installation before W-L7
lands. The others can be addressed post-W-L7. The 2-VM limit question in particular —
whether `yoloai.Client` surfaces a typed error or whether it's purely Tart-internal —
affects whether Q94 adds to the Client's error taxonomy (part of W-L8a).

---

#### Backend-agent-extensibility Issues 1–2 (untracked)

**Source:** `docs/contributors/design/plans/backend-agent-extensibility.md` (entire file); not in
OPEN_QUESTIONS.md but directly layering-adjacent.

**The question:** This plan has 10 issues. Issues 1–2 are the most layering-relevant:
- Issue 1: `meta.Backend == "seatbelt"` comparisons — replace with `meta.HostFilesystem`
  (a `BackendCaps`-derived field stored at creation). Depends on Issue 7 (meta versioning).
- Issue 2: Agent-specific switch statements in `sandbox/create.go` — replace with
  `agent.Definition.ApplySettings` function field.

**Why it affects the rearchitecture:** Issue 1 is effectively a mini-version of the
W-L capability-detection work, but for a field that lives in persisted metadata rather
than in the live `BackendDescriptor`. If W-L work touches `sandbox/create.go` (which
W-L8b will, heavily), Issue 2 is in the same file. Landing them in the wrong order
means either (a) W-L8b rewrites code that Issue 2 is about to move, or (b) Issue 2
lands first and W-L8b has a cleaner target.

Similarly, Issue 7 (meta versioning) is a schema change to `environment.json` that
both backend-agent-extensibility and W-L8b may touch concurrently.

**Severity: CONSTRAINS.** The sequencing matters. Issue 2 (ApplySettings) should land
before W-L8b migrates `new` command orchestration into `yoloai.Client`, so the Client's
`Run()` method calls a clean `ApplySettings` interface rather than agent-name switches.
Issue 1 can land anytime — it's localized to `sandbox/context.go` and `mcpsrv/proxy.go`.

**Recommended next step:** Land backend-agent-extensibility Issues 1–7 (the mechanical
ones) before W-L8b starts. Issue 8 (typed errors in CLI) is also directly relevant —
W-L8c establishes exit-code mapping conventions that should be consistent with the
typed-error taxonomy. Coordinate these two plans explicitly.

---

#### MCP server SDK choice (mcp-server.md open question 3)

**Source:** `docs/contributors/design/plans/mcp-server.md:472–477`

**The question:** Whether to use the official Anthropic Go MCP SDK (if stable) or
`github.com/mark3labs/mcp-go`. Also: verify that the chosen SDK supports the
elicitation API.

**Why it affects the rearchitecture:** The layering design requires the MCP server to
call `yoloai.Client` exclusively (layering.md §4, layering-greenfield.md:264). The
MCP server's internal structure doesn't affect the layering — but if the SDK choice
forces a particular interface shape for tool handlers (request/response types, streaming
idioms), those shapes may leak into what `yoloai.Client` methods return. The
`internal/mcpsrv/` package is currently a parallel orchestration path; W-L8 is
designed to make it consume `yoloai.Client` instead. If the SDK changes the shape of
that consumption, it could force changes to Client method return types during W-L8.

**Severity: CONSTRAINS.** The MCP server refactor (to consume Client) can happen before
or after W-L8, but the Client method shapes for streaming operations (Attach, Exec)
are affected by what the MCP SDK can consume. Decide the SDK before W-L8a's design
review checkpoint.

**Recommended next step:** Resolve SDK choice before W-L8a. It is a short research
task (check whether `github.com/modelcontextprotocol/sdk-go` is published and stable).

---

### INFLUENCES

---

#### Q77 — `yoloai wait` command

**Source:** `docs/contributors/design/unresolved-questions.md:171`; also `docs/contributors/design/plans/README.md:69–82`

**The question:** No `yoloai wait <name>` command. Scripting must poll `yoloai list --json`.
The command would block until the agent exits, returning the agent's exit code.

**Why it influences the rearchitecture:** `wait` is among the missing methods on
`yoloai.Client` that W-L8a must catalog (layering-refactor.md §W-L8a steps 1–3). The
Client API surface design should include `Wait(ctx, name, timeout) error`. This isn't a
blocker — W-L8a explicitly has a step to "note gaps: methods or options that don't exist
today" — but the implementer should not forget this one when cataloging.

**Severity: INFLUENCES.** Informational for W-L8a's surface catalog.

**Recommended next step:** Note during W-L8a. No pre-decision required.

---

#### Q93 — MCP server inside containers

**Source:** `docs/contributors/design/unresolved-questions.md:203`

**The question:** MCP servers inside sandboxes don't work: stdio servers need binaries
installed in the container; network servers reference `localhost` which resolves to the
container. Possible solutions: custom profiles with MCP deps, or host-network passthrough.

**Why it influences the rearchitecture:** The MCP proxy (`yoloai mcp proxy`) is a
backend-specific feature that currently uses `internal/mcpsrv/proxy.go:225`'s
`exec.Command("docker", ...)` — a leak flagged in architecture-remediation.md W10.
The layering design requires `StdioExecer` optional interface to replace this. If Q93's
resolution requires a new optional interface (e.g., `MCPBridge`) for host-network
passthrough, that interface belongs in the layering work. But the question is currently
marked low-priority (no user reports as blocker).

**Severity: INFLUENCES.** The `docker exec`-style leak in `proxy.go` is already
tracked in W10 and W-L8. Q93 is informational — if host-network passthrough turns out
to be the right MCP solution, it would be an optional interface addition, fitting
naturally into the W-L pattern.

**Recommended next step:** No decision required before W-L1. Address the `docker exec`
leak as part of W10 / W-L8b.

---

#### Q97 — Network allowlist audit

**Source:** `docs/contributors/design/unresolved-questions.md:218`

**The question:** Gemini was missing `oauth2.googleapis.com`; similar gaps likely exist
for other agents. All agents need traffic capture during full sessions.

**Why it influences the rearchitecture:** The allowlist management commands
(`sandbox allow`, `sandbox deny`, `sandbox allowed`) are part of the CLI surface
that W-L8b will migrate into `yoloai.Client`. The Client method shapes for allowlist
operations (`Allow(ctx, name, domains []string) error`, `Deny(...)`, `Allowed(...)`)
should be designed correctly regardless of what the audit reveals about specific domains.
The audit affects *values* (which domains), not the Client *API shape*.

**Severity: INFLUENCES.** Does not affect the layering architecture. Run the audit
in parallel with the rearchitecture. The CLI surface for network management is already
defined; no new Client methods are needed.

**Recommended next step:** Parallelize the audit with the rearchitecture.

---

#### Q102 — `sandbox/setup_test.go` 632 lines

**Source:** `docs/contributors/design/unresolved-questions.md:256`

**The question:** Whether `sandbox/setup_test.go`'s 632 lines are justified exhaustive
coverage or a refactoring signal.

**Why it influences the rearchitecture:** W-L8b migrates orchestration logic out of CLI
into `yoloai.Client`. If `sandbox/setup.go`'s interactive setup flow ends up partially
in the Client (e.g., backend selection), tests from `setup_test.go` must follow. If the
test is already too large, migrating it will be painful.

**Severity: INFLUENCES.** Resolve this during W-L3 or before W-L8b starts. It is a
read-then-decide task (OPEN_QUESTIONS.md gives the complete decision process).

---

#### network-isolation.md Open Questions (OQ1–OQ3)

**Source:** `docs/contributors/design/network-isolation.md:289–292`

**The questions:** (1) Sandbox interface discovery with user-defined Docker networks;
(2) CNI return path for Kata; (3) conflict with user iptables setup.

**Why it influences the rearchitecture:** Network isolation enforcement is backend-
and platform-specific. The layering design places iptables rules in a capability-gated
path; `SupportsOverlayDirs` and `IsolationAvailability` in `runtime/isolation.go` are
the pattern. OQ1–OQ3 don't change the layering shape — they are runtime implementation
details inside `runtime/docker/` and `runtime/containerd/`. However, OQ3 (iptables
conflict with user rules) might require `IsolationAvailability` to return a new reason
code. Low risk.

**Severity: INFLUENCES.** Proceed with W-L6 as planned; flag OQ3 in the discovered-
findings policy if encountered during implementation.

---

#### exit-codes.md Open Questions

**Source:** `docs/contributors/archive/plans/exit-codes.md:271–277`

**The questions:** (1) Wrap vs. replace sentinel errors; (2) `--json` error envelope;
(3) `golang.org/x/term` dependency for non-TTY guard.

**Why it influences the rearchitecture:** W-L8a designs the `<Op>Options` struct shape
and the return types from `yoloai.Client` methods. The exit-code taxonomy (typed errors
driving exit codes in the CLI) is the presentation layer's concern, not the Client's —
but the *types* the Client returns must be mappable to exit codes. If sentinel errors are
replaced by typed errors (OQ1), that's consistent with W7 (typed errors → `yoerrors/`)
and should be complete before W-L8b finalizes the Client's error surface.

**Severity: INFLUENCES.** Resolve exit-codes OQ1 alongside W-L8a. It is a small
decision with an obvious answer (replace at origin, per the plan's own proposal).

---

#### backend-agent-extensibility Issues 8–10

**Source:** `docs/contributors/design/plans/backend-agent-extensibility.md` Issues 8–10

**Issues 8–10:** typed errors in CLI (Issue 8), `os.Setenv` mutation in
`sandbox/create.go` (Issue 9), unused sentinel errors in `sandbox/errors.go` (Issue 10).

**Why it influences the rearchitecture:** Issue 8 (typed errors) overlaps with W7 and
the exit-codes plan. Issue 9 (`os.Setenv`) is a correctness issue in code that W-L8b
will touch (`sandbox/create.go`). Issue 10 (unused sentinels) is pure cleanup.

**Severity: INFLUENCES.** Issue 9 should be fixed before W-L8b migrates create logic
into `yoloai.Client` — otherwise the mutation follows the logic into the Client layer.
Issues 8 and 10 are cleanup that parallelizes well.

---

## Section 2: Prior Architectural Models

### Theme 1: Runtime interface design (capabilities, descriptor, optional interfaces)

**D7 — Pluggable `runtime.Runtime` interface** (`docs/contributors/decisions/README.md:145`)
> "No backend-specific types leak outside their package."
Directly establishes the foundational rule that W-L2–L7 enforce more rigorously.

**W11 — Runtime interface split** (`docs/contributors/design/plans/architecture-remediation.md:276–309`)
> `BackendDescriptor` holds static facts; `Runtime` interface narrowed to lifecycle
> operations; optional interfaces for backend-specific capabilities.
**Status: Landed.** The spike confirmed 0% dynamic `BackendCaps` fields. The
`(Factory, Descriptor)` registry is in place. W-L3–L7 extend the descriptor (adding
`Probe`, `VersionString`, `CleanupHint`, `HostFromContainerHostname`, etc.) — these are
additive changes to an already-stable structure.

**runtime-interface-spike.md** (`docs/contributors/design/research/runtime-interface-spike.md`)
> 14 lifecycle methods on core `Runtime`; 5 static facts on `BackendDescriptor`; 7
> optional-interface candidates (`TmuxSocketProvider`, `ResolveCopyMounter`, etc.).
**Status: Recommendation complete; W11 implemented; W-L3–L7 consume the conclusions.**
The spike's `BackendDescriptor` shape (Name, BaseModeName, AgentProvisionedByBackend,
SupportedIsolationModes, Capabilities) is the baseline W-L3 extends. `AttachCommand` was
recommended to stay on core; the spike's note about `TmuxSocket` needing `sandboxDir` is
implemented in `backend-agent-extensibility.md` Issue 5.

**capability-registry.md** (`docs/contributors/design/plans/capability-registry.md`)
> `HostCapability` struct with injectable function pointers; `runtime/caps/` package;
> `RequiredCapabilities`/`SupportedIsolationModes`/`BaseModeName` on `Runtime`.
**Status: Fully implemented** (ARCHITECTURE.md confirms `runtime/caps/` with `caps.go`,
`detect.go`, `check.go`, `common.go`). W-L3's `Probe()` and `VersionString()` additions
to `BackendDescriptor` extend this pattern without duplicating it.

**development-principles.md §2 — Boundary discipline** (`docs/contributors/principles/development-principles.md:72`)
> "No business logic at the interface layer. No backend types past the runtime boundary."
> The import direction is strict: CLI → sandbox → runtime → backend SDK.
Directly states the rule W-L enforces mechanically.

---

### Theme 2: Package boundary decisions

**W12 — sandbox/ carve-up** (`docs/contributors/design/plans/architecture-remediation.md:313–334`)
> `sandbox/archetype/`, `sandbox/patch/`, `sandbox/store/` carved as subpackages.
**Status: Landed** (ARCHITECTURE.md shows these subpackages exist). This is the closest
precedent for the W-L8 `yoloai.Client` migration — the same technique (extract a
self-contained concern, verify import graph, pass `make check` after each step) applies.
The lesson from W12: extract in dependency order; `store/` (leaf) first, `patch/` next,
`archetype/` last.

**soc-refactor.md — Issue 3: isolation mappings to `runtime/`** (`docs/contributors/design/plans/soc-refactor.md:268`)
> `isolationContainerRuntime()` and `isolationSnapshotter()` moved to `runtime/isolation.go`.
**Status: Landed.** `runtime/isolation.go` exists (ARCHITECTURE.md:162). W-L6 extends
this file with `SupportsOverlayDirs()` and `IsolationAvailability()`. The approach is
validated.

**backend-agent-extensibility.md Issue 6 — `InstanceConfig` grouping** (`docs/contributors/design/plans/backend-agent-extensibility.md:309`)
> Fields grouped with comments (Universal / Container-VM / containerd-specific). Named
> fields at construction sites. Extension convention in `standards/GO.md`.
**Status: Designed, implementation status unknown.** W-L8b will touch `InstanceConfig`
indirectly when migrating `new` command logic into the Client. The grouping is the right
model for keeping the struct maintainable as backends diverge.

**development-principles.md §2 — Import direction map**
> `cmd/yoloai → internal/cli → sandbox + agent + config + workspace → runtime/<backend>`
> "No reverse imports. No circular imports."
The concrete import graph that W-L enforces. W-L10's test/linter rule makes this mechanical.

---

### Theme 3: CLI architecture

**soc-refactor.md Issue 4 — `detectContainerBackend()` side effect** (`docs/contributors/design/plans/soc-refactor.md:333`)
> Changed signature to return `(backend, warning string)` instead of printing to stderr.
**Status: Designed.** This is the exact shape that W-L8a's "Presentation layer owns
presentation" rule demands — helpers return data, callers do I/O. W-L8 will enforce this
across all Client method return values.

**layering-comparators.md (referenced by layering.md §3)** — kubectl `IOStreams` pattern
> Streaming/interactive operations take explicit `io.Reader`/`io.Writer` rather than
> hiding TTY. This is cited in the layering refactor plan (layering-refactor.md:391) as
> the open question for W-L8a streaming shape.
The precedent from kubectl is: pass explicit streams. `internal/cli/commands.go`'s
`attachToSandbox` / `waitForTmux` helpers are the current attach path; W-L8b should
extract these into the Client with explicit stream parameters.

**layering-greenfield.md §2 — `internal/cli/format/` pattern**
> Formatter interface with `text.go` and `json.go` implementations. Generic commands in
> `internal/cli/commands/` call `yoloai.Client` exclusively, format the result.
Target shape for W-L8c and W-L8d. The conventions W-L8c must establish (IOStreams,
error-to-exit-code mapping, `--json` switch selects formatter) should match this pattern.

---

### Theme 4: Cross-cutting principles

**development-principles.md §9 — Plan-then-execute on cleanup** (D19)
> "Audit → numbered work plan → each item a discrete commit. Phases 1–6 + W11 + W12
> landed as coherent shape 2026-05-20."
The W-L plan is this principle applied again. The key lesson from the architecture-
remediation execution: each workstream was independently shippable; the `make check`
gate blocked partial merges; discovered-findings went to `docs/contributors/design/unresolved-findings.md`
without expanding scope.

**development-principles.md §4 — Parse, don't validate** (D10 / D6)
> Parsed types (`SandboxName`, `ResolvedPath`, `BackendDescriptor`) whose existence
> proves invariants; parsers live in dedicated packages.
W-L8a's `<Op>Options` structs should follow this: each option is a parsed type, not a
raw string. `SandboxName`, `MountMode`, `AllowedDomain`, `AgentName` already exist as
parsed types (development-principles.md:163). W-L8a should use them rather than `string`.

**development-principles.md §8 — No half-finished implementations**
> "A feature is either shipped (works, has tests, is documented) or removed."
Each W-L workstream must end in a shippable state with passing tests. W-L8b's guidance
("Add tests for moved logic before deleting old paths") is this principle applied to the
migration.

**architecture-remediation.md — Discovered-findings policy**
> "Mid-workstream discoveries that weren't in the original audit go in
> `docs/contributors/design/unresolved-findings.md`. Critical escalates; everything else parks."
The W-L plan inherits this policy verbatim (layering-refactor.md:41). There is no
dedicated `discovered-findings.md` yet; it will be created on first mid-workstream hit.

---

### Theme 5: Backend abstraction

**D7 — Backend registry evolution** (`docs/contributors/decisions/README.md:158`)
> "W11 of the 2026-05 architecture remediation registers `(factory, descriptor)` tuples
> so adding a backend is purely additive."
The registry (`runtime.Register`, `runtime.Descriptors()`) is the model W-L3 builds on
to drive `info.go`, `setup.go`, and `bugreport_writer.go` from one source.

**soc-refactor.md Issue 1 — BackendCaps behavioral flags → interface methods**
(`docs/contributors/design/plans/soc-refactor.md:24`)
> `NeedsHomeSeedConfig` and `RewritesCopyWorkdir` removed from `BackendCaps` (a boolean
> flag); replaced with `ShouldSeedHomeConfig()` and `ResolveCopyMount()` interface methods.
> "Adding a new process-based backend requires no `BackendCaps` edits."
**Status: Designed; implementation unclear.** The W-L design references `soc-refactor.md`
as a completed W12 step, but the file is explicitly labelled "W12's detailed design" —
not "W12 completed." Verify whether soc-refactor.md Issue 1 is in the codebase before
W-L7 tries to add `AppleSimulatorRuntimes`. If it is not landed, the `BackendCaps`
approach (which soc-refactor.md rejects) may still be the code W-L7 has to work around.

**backend-agent-extensibility.md Issue 3–4 — Interface rename**
> `ShouldSeedHomeConfig()` → `AgentProvisionedByBackend()` and
> `EnsureImage`/`ImageExists` → `Setup`/`IsReady`.
**Status: Already landed in `runtime/runtime.go`.** All four renames are present:
`Setup(ctx, sourceDir, ...)`, `IsReady(ctx)`, `TmuxSocket(sandboxDir)` (with the
per-sandbox arg the SSH design wanted), and `AgentProvisionedByBackend` as a
`BackendDescriptor` field. The W-L11 workstream that was briefly added to lift these
renames out of the SSH design doc was removed once verification confirmed the
work was complete. No work remains.

---

## Recommended Pre-Rearchitecture Decisions

These are decisions the user should make before kicking off W-L1, in rough priority order.

**1. Resolve Q100 (dual command dispatch) before W-L8a.** (BLOCKS W-L8a)
This is the single blocking decision. Pick: keep both indefinitely, or start deprecating
one. Either answer unlocks the API surface design. A decision is needed regardless of
usage data — the refactor cannot wait for telemetry that doesn't exist.

**2. Verify soc-refactor.md Issue 1 landing status before W-L7.**
`ARCHITECTURE.md` and the code may not match the plan's stated completion status. Check
whether `ShouldSeedHomeConfig()` and `ResolveCopyMount()` exist as interface methods
(not `BackendCaps` fields). If not landed, add to the W-L7 scope or land as a prerequisite.

**3. Sequence backend-agent-extensibility Issues 2 and 9 before W-L8b.**
Issue 2 (`ApplySettings` on `agent.Definition`) and Issue 9 (`os.Setenv` fix) both
touch `sandbox/create.go`, which W-L8b migrates into the Client. Landing them before
W-L8b means the Client's `Run()` method gets the clean interface from the start.
This is a sequencing decision with a clear answer: do them in W-L (Phase 1 or early
Phase 2) as prerequisites to W-L8b.

**4. Decide the MCP SDK before W-L8a.**
Resolve mcp-server.md OQ3 (official Anthropic Go SDK vs. `mcp-go`). The choice
affects the streaming shapes the Client must return. This is a short research task
(check `github.com/modelcontextprotocol/sdk-go` stability).

**5. Decide Q94's 2-VM limit and Xcode installation sub-questions before W-L7.**
These two Tart sub-questions determine whether `AppleSimulatorRuntimes` needs more
methods than the current W-L7 design provides. The others (agent compatibility,
diff/apply with VirtioFS) can be deferred.

**6. Resolve exit-codes.md OQ1 (replace vs. wrap sentinels) before W-L8a.**
The Client method error surface must be consistent with the exit-code taxonomy. OQ1
has an obvious answer (`replace at origin`) — just decide it explicitly so W-L8a's
error type designs are consistent.

**7. Fix `os.Setenv` (backend-agent-extensibility Issue 9) before W-L8b.**
This is a correctness issue (`os.Setenv` is not goroutine-safe) in `sandbox/create.go`.
W-L8b migrates this code into `yoloai.Client` which will be called from multiple
surfaces. The fix is small and self-contained; there's no reason to migrate the bug.

---

## References

- `docs/contributors/design/layering.md` — architecture this scan supports
- `docs/contributors/design/layering-greenfield.md` — target package layout
- `docs/contributors/design/plans/layering-refactor.md` — phased workstreams (W-L1–L11)
- `docs/contributors/design/research/layering-leak-audit.md` — empirical findings (L1–L31)
- `docs/contributors/design/unresolved-questions.md` — primary source for open questions
- `docs/contributors/design/plans/architecture-remediation.md` — sibling refactor (W1–W14)
- `docs/contributors/design/plans/backend-agent-extensibility.md` — extensibility issues (10 total)
- `docs/contributors/design/plans/soc-refactor.md` — W12 detailed design
- `docs/contributors/design/plans/capability-registry.md` — capability-registry design (implemented)
- `docs/contributors/design/plans/mcp-server.md` — MCP server plan
- `docs/contributors/archive/plans/exit-codes.md` — exit code taxonomy
- `docs/contributors/design/research/runtime-interface-spike.md` — W11 spike (complete; verdict: proceed)
- `docs/contributors/decisions/README.md` — D-numbered decision log
- `docs/contributors/principles/development-principles.md` — §2, §4, §5, §8, §9 most relevant
- `docs/contributors/architecture/README.md` — code navigation guide

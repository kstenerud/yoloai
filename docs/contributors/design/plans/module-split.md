# Module split: sandbox substrate vs. agent orchestration

**Status:** Active on the `module-split` branch (cut from `main` after `multi-workdir`
merged). The agent map, the backend-leakage map, and the Phase 0 **first cut**
(`FilesystemLocality` + git routing) have landed here; remaining phases below. Will earn
a D-number in `working-notes.md` when adopted.

**Premise (from the coupling map, 2026-06-13):** yoloAI is already, structurally,
a reusable *sandbox substrate* under an *agent-specific* shell. The substrate's
behavior is agent-free today; the only "baggage" tying the two together is a
handful of concentrated, cheap edges. This doc designs the split so the substrate
(and each of its refinements) can be consumed **without the parts a given consumer
doesn't need** — a clean capability DAG, not four co-equal libraries.

## Goal

Let a consumer take exactly the capability it wants and pull in **only** that
capability's dependencies:

- run an isolated process and move bytes in/out → without git, without PTY, without agents;
- add the copy/diff/apply review workflow → without pulling in PTY/tmux;
- add an interactive terminal session → without pulling in git;
- add agent orchestration → on top of any of the above.

The test of success is mechanical: **what does `go list -deps` pull for each
entry point?** A headless task runner must not transitively import `creack/pty`
or tmux; a diff/apply consumer must not import the agent package; the substrate
must not import upward at all.

## What the map found (recap)

The substrate is ~90% decoupled already. Facts that shape this design:

- The `runtime.Runtime` interface has **zero** agent methods. Agent appears only as
  **3 declarative `BackendDescriptor` fields** (`AgentProvisionedByBackend`,
  `AgentInstallMethod`, `AgentLaunchPrefix`) and **1 optional, type-asserted
  interface** (`AgentCommandPreparer`). None of it drives runtime behavior.
- The copy/diff/apply workflow (`patch` + `workspace` + `git`) is **completely
  agent- and tmux-free** and is already its own package — a consumer using only the
  runtime never pulls it.
- **PTY is already optional at the runtime contract**: `InteractiveExec` takes
  `IOStreams{TTY, Rows, Cols}` and the *caller* decides whether to allocate one.
  The runtime mandates no terminal.
- The only **upward import edges** from substrate into the agent package are **two
  persisted-metadata fields**: `store.Environment.AgentType` and
  `runtimeconfig.ContainerConfig.Idle (agent.IdleSupport)`.
- "tmux everywhere" is **not** a substrate property — it's a *launch-layer*
  convention (the Python entrypoint always creates a tmux session and launches the
  agent into it). The substrate already supports non-TTY exec.

So the work is: close two import edges, relocate PTY so it isn't pulled by default,
introduce a session abstraction so tmux is one strategy rather than the universal
substrate, and (optionally) rename packages so names match roles.

## The layered capability model

The organizing principle the user named: **a refinement, not an absolute
requirement.** The git copy/diff/apply flow is a *refinement of data transfer*;
the interactive terminal is a *refinement of exec*. Each refinement sits in its own
package, depends only on the substrate primitive it refines, and is pulled in only
by consumers that opt into it.

```
products      yoloai (lib + CLI) · daemon · MCP server · [future: headless runner · non-agent task]
                        \                |               /
orchestration           agent (defs · provision · prompt delivery · idle detect · lifecycle)
                  ______/    |     \_________________________________
                 /           |             |            |            \
refinements  copyflow     session       netpolicy    envsetup    interfaces
 (each       · diff/apply  · PTY          · isolation  · archetypes · MCP
  optional,  · tags        · tmux session · ports                   · VS Code
  own deps,  · overlay     · persistence                            · CLI
  NO         · auto-commit  · idle/monitor
  sideways   · creds
  edges)         \           |             |            |            /
substrate                create · start · stop · destroy · exec · transfer(files+streams)
 (irreducible)                              |
backends                docker · containerd · tart · seatbelt · apple
                                            |
foundations             fileutil · config · process-prim
```

Strict rules of the DAG (this is the whole point):

1. **Substrate imports nothing above it.** No agent, no copyflow, no session, no tmux.
2. **Refinements depend down, never sideways.** `copyflow` depends on `transfer` (+ git),
   *not* on `session`/PTY. `session` depends on `exec` (+ PTY), *not* on `copyflow`/git.
   A headless diff/apply consumer pulls no terminal code; an interactive PTY consumer
   pulls no git.
3. **Optional substrate capabilities are pulled only by opting in.** PTY is the prime
   example — see below.
4. **Agent metadata lives in the agent layer**, never in substrate packages.

## Irreducible core vs. refinements

The capability inventory (2026-06-13) makes the cut concrete: the genuinely
essential substrate is just **lifecycle (create/start/stop/destroy) + exec +
transfer (files & streams) + one backend.** Everything else is a refinement.

The sharp finding is that several capabilities feel essential only because
*current convention* makes them mandatory — they are **not** substrate
requirements:

- **tmux** is essential only to the *interactive-session* refinement (the launch
  convention runs every agent through it).
- **git** is essential only to *copy/diff/apply*.
- the **Python monitor** is essential only to *status/idle observability*.

A headless, non-agent consumer needs none of the three. The goal is to shrink the
mandatory core to lifecycle+exec+transfer and make the rest opt-in — at which
point the inventory below stops being baggage and becomes a menu.

### Refinement inventory

Each row is opt-in. **"Drags"** is what a consumer inherits by importing it — the
reason it must not live in core. **"Status"** is how separable it is today
(`clean` = own package / pure config; `partial` = factored but still reaches into
core or a backend; `tangled`/`leak` = needs real work to lift out).

| Refinement | Refines | Drags (ext lib · host bin) | Status today |
|---|---|---|---|
| copy/diff/apply | transfer | host `git` | clean (own pkg `patch`) |
| tags / checkpoints | diff/apply | host `git` | clean |
| overlay (`:overlay`) | the copy strategy | — (kernel overlayfs) | **tangled** (threads mount/patch/reset; `apply_overlay.go` mirrors `apply.go`) |
| auto-commit loop | copy workflow | host `git`, `bash` | clean (config flag, default off) |
| agent credential injection | transfer | — | clean (secret bind-mount) |
| profiles / image build | provisioning | host `docker`/`buildctl` | clean (separate phase; no-op on tart/seatbelt) |
| PTY allocation | exec | `creack/pty`, `x/term` | **leak** (lives in `internal/runtime` core today) |
| interactive session + persistence | exec | host `tmux`, `bash` | **tangled** (mandatory launch convention) |
| idle detection / monitoring | exec | host `python3`, `tmux`, `bash` | clean (instrumental; sandbox runs without it) |
| network isolation / allowlist | networking | `go-cni`, `netns` · host `iptables`/`dig`/`ipset` | partial (clean from non-CNI backends; threaded into containerd startup) |
| port forwarding | networking | via CNI portmap | partial |
| archetype detection | env setup | — | clean (pure config hint) |
| in-place reset | stop-restart reset | host `rsync` | clean (upgrades to restart when absent) |
| MCP server | consumer interface | `mark3labs/mcp-go` | partial (own pkg `mcpsrv`; calls full Client) |
| VS Code tunnel/attach | consumer interface | host `code` | partial |

**Core-only deps** — what survives when every refinement is stripped:
`golang.org/x/sys`, `yaml.v3`, plus the one backend in use. The only confirmed
*core leak* is `creack/pty` (PTY) sitting in `internal/runtime` — Phase B's target.

The three rows that are more than naming + a depguard fence — **overlay**,
**interactive session/tmux**, and **network isolation** — are the real refactor
weight; the rest are largely already-clean capabilities awaiting a name and a
fence.

## The second leakage axis: backend idiosyncrasies (esp. Tart)

The coupling map measured ONE axis — *does the substrate import the agent/PTY?* —
and answered "barely." But there is an **orthogonal** axis it never tested: *do
higher layers special-case the backends?* A substrate can be perfectly agent-free
and still riddled with backend-specific assumptions bleeding upward.
`backend-idiosyncrasies.md` (~2200 lines, a large fraction Tart) is the evidence
that this axis is real and deep — and the agent-map's "90% clean" verdict is silent
about it.

The leaks are **model-level, not just quirk-level.** The one that matters most for
this split: **Tart inverts filesystem locality.** On Docker/Seatbelt the work copy
lives on the host, so git runs on the host. On Tart the work copy lives *inside the
VM* — VirtioFS corrupts git repos, so git must run in-VM, and a host-side change
probe is blind to the in-VM workdir (both are catalog entries). So the copy/diff/apply
"refinement" classified as clean above is clean of *agents* but **not** of *backends*:
it is built around host-filesystem access and special-cases Tart via in-VM git
dispatch, `WorkDirSetup` deferred baselines, `ResolveCopyMountFor`, and
guest-mount-path translation. The refinement knows about Tart.

**The visible symptom is optional-interface proliferation** — **14** type-asserted
optional interfaces on the runtime (`runtime_optional.go`). But only **~4 are
decision-driving** (a higher layer branches *core logic* on their presence):
**`GitExecer`** and **`WorkDirSetup`** (both = the `FilesystemLocality` decision — *both now
converted to the property*), plus `StdioExecer` and `UsernsProvider`. Refined by
implementation: `CopyMountResolver`/`GuestMountResolver` turned out to be **operations, not
decisions** — reached only through `Resolve*For` helpers with an identity default, no
branch — so they join the ~9 legitimately-optional *operations* (`VMCensusReporter`,
`DiskUsageReporter`, `CachePruner`, `StaleBasePruner`, `LogTailer`, `AppleSimulatorRuntimes`,
`AgentCommandPreparer`, the two resolvers). So the reducible leak was ~4 detections, of
which the two locality ones are done; `StdioExecer`/`UsernsProvider` remain.

**Why this is the sharpest risk to the split.** A module boundary that looks clean
but doesn't seal these seams is *worse* than today: it presents a false abstraction
that future work — and AI agents — trust, then get burned by Tart again. The catalog
exists because the backend contract lives as **2200 lines of prose, not in the type
system or tests**, so even careful work misses cases. Relocating code into tidy
modules without sealing the seams multiplies that trap rather than removing it.

### What the plan must add

1. **A backend-leakage map** — the analog of the agent map, run *before* drawing the
   substrate/refinement boundary. **Done (2026-06-13) — see
   [research/backend-leakage-map.md](../research/backend-leakage-map.md).** It revised this
   section's framing: there are **0 backend-identity logic leaks** above the runtime (the
   runtime already hides identity), git execution is **already routed** backend-correctly
   via `GitExecer`/`git.NewSandbox`, and the real leak is **capability-by-type-assertion**
   (14 optional interfaces) plus **implicit inference** (`mountPath != hostPath`) — not
   identity-branching. Most of it collapses into one property, `FilesystemLocality`.
2. **Backend-seam ownership.** Each idiosyncrasy gets a named owner layer. Sealing it
   *inside the backend implementation* (so no higher layer sees it) is the default — e.g.
   the filesystem-locality seam becomes a substrate primitive, *"run git/exec against a
   tracked dir's work copy, backend-correctly"*, and copyflow rides on it without touching
   the filesystem. But sealing only works when the *consequence* doesn't ripple up; when
   it does (the host can't see sandbox-side changes; in-place reset is impossible), the
   leak must instead be **modeled as a typed property** — see *Remediation* below.
3. **An executable backend-conformance suite.** The contract each backend must satisfy,
   as tests, so a new backend or a Tart change cannot *silently* violate an assumption a
   higher layer made. This moves the catalog's constraints from prose into a contract
   the tests enforce — the antidote to "the model couldn't account for it." Genuinely
   irreducible differences (Tart has no in-place reset; in-VM vs. host locality) stay in
   the contract as **explicit, typed capabilities**, never ad-hoc type-asserts.

The goal is **not** zero leaks — some backend differences are irreducible. The goal is
that **every leak is explicit, typed, and conformance-tested, never ad-hoc** — so the
clean module boundary is *honest* rather than a fresh false abstraction. This work is a
**prerequisite** for the substrate/refinement cut, not a follow-up: you cannot draw the
boundary correctly until you know where the backends actually leak across it.

### Remediation: model properties, not backend identity (the interface redesign)

The map's output is not just a list of seams — it is the input to **re-deriving the
backend interface itself.** For each leak, pick one of three strategies:

- **Seal** — absorb it inside the backend impl; the higher layer never knows (mount-path
  translation, exec stabilization delays, VM-wedge recovery, "run git against the work copy").
- **Model as a property** — when the consequence ripples up and *cannot* be hidden, expose
  it as a typed descriptor property and make the higher layer a pure function of it.
  *Example:* `FilesystemLocality{HostSide, SandboxSide}` replaces every "is this Tart?"
  with "are the work files host-side or sandbox-side?" — the host change-probe, in-place
  reset, and host-vs-in-VM git all key off the *property*.
- **Irreducible branch** — genuinely unavoidable; minimize and document.

**Governing rule (sharpened by the map): no higher layer may *detect* a decision-driving
capability — by backend-identity, by `rt.(SomeInterface)` type-assertion, or by implicit
inference (`mountPath != hostPath`). It must read a named, semantic property.** Identity
tokens above the runtime are a defect (grep-checkable) — the map found **zero**, so that
battle is already won; the live front is capability-*detection*. Optional *operations*
(prune/logs/census, and — confirmed by implementation — the `CopyMountResolver`/
`GuestMountResolver` path resolvers, which have identity defaults) may remain call-if-present.
The headline property, `FilesystemLocality`, is the **decision** that gates the two in-sandbox
operations `GitExecer` (git) and `WorkDirSetup` (deferred baseline). It is **orthogonal to
`BackendCaps.HostFilesystem`** (a state-location axis — seatbelt is HostFilesystem=true yet
HostSide), and `mountPath != hostPath` is a separate copy-relocation concern, not locality.
**Landed (this branch):** the property is declared by all backends and now drives **both** the
git routing in `git.NewSandbox` and the baseline-deferral / in-place-reset decisions in
`prepare_dirs.go`/`vmworkdir.go`/`reset.go` (replacing five `rt.(WorkDirSetup)` type-asserts).
The host-assuming change-probe (`status.go`, blind to the in-VM workdir on Tart) and the two
remaining non-locality decision-drivers (`StdioExecer`/`UsernsProvider`) are next.

**This flips the interface's design direction** — from bottom-up (each backend's quirks
bubble up as an optional type-asserted interface or a name-check) to top-down (enumerate
the *decisions* higher layers make that depend on backend variance, name the property
behind each; the interface *is* that property set plus the operations). `BackendDescriptor`/
`BackendCaps` is the seed: one property can replace several scattered *detections*
(`FilesystemLocality` gates the two in-sandbox operations `GitExecer` + `WorkDirSetup` and
already replaced both their type-asserts — though it is **orthogonal to** `HostFilesystem`, a separate
state-location axis, not a facet of it), and the grab-bag of optional interfaces shrinks to
the genuinely-optional *operations*.

**The discipline that keeps this from being the type-asserts repainted:** a property must
be **semantic** (a consequence the higher layer reasons about) and **answerable by a
backend yoloAI has never heard of** — never mechanistic. `FilesystemLocality` passes (any
backend can declare host/sandbox-side); `UsesVirtioFS` fails (it just encodes "Tart" in a
trench coat). Properties must be orthogonal and minimal, derived from higher-layer
decisions, not backend internals.

This is also the durable fix for "the model couldn't account for it": `if locality ==
SandboxSide` is self-describing — an agent or a human adapts from the contract — whereas
`if backend == tart` forces them back into the 2200-line catalog. Property-oriented
modeling moves the backend contract **into the types**. And the property set becomes the
**axes of the conformance suite**: a backend declaring `locality=SandboxSide` must pass the
sandbox-side conformance tests, so the matrix is generated from the properties rather than
hand-maintained per backend.

## The "don't pull along" mechanism (Go specifics)

Importing a Go package pulls its entire transitive dependency set. So "don't pull
along" is enforced by **package boundaries + import discipline**, made mechanical:

- **PTY must leave the substrate core.** Today `interactive_pty.go` lives in
  `internal/runtime`, so importing the runtime transitively pulls `creack/pty`. Move
  PTY allocation into its own package (`runtime/pty` or the `session` refinement) so
  the core lifecycle/exec/transfer surface pulls no terminal library. Backends expose
  exec; the *PTY refinement* wraps it. Verification: `go list -deps <core>` must not
  contain `creack/pty`.
- **copyflow/git already separate** — keep it that way; never let the substrate import it.
- **Capability = optional interface, type-asserted.** The runtime already uses this
  (`WorkDirSetup`, `CopyMountResolver`, `GuestMountResolver`, `AgentCommandPreparer`).
  Lean into it: the *core* `Runtime`/`Backend` contract stays minimal; every "only some
  consumers/backends need this" capability is an optional interface reached via
  type-assert, not a required method. This keeps both the contract and the dependency
  set minimal.
- **Depguard fences encode the DAG.** The project already uses depguard scopes. Add
  rules that *fail the build* on a forbidden edge: substrate → {agent, copyflow,
  session, tmux, pty}; copyflow ↔ session; etc. The DAG becomes machine-checked, so it
  can't silently re-tangle.
- **Package boundaries first; `go.mod`-per-module is an optional upgrade.** Strict
  packages + depguard give *conceptual* separability (disciplined, build-checked). A
  separate `go.mod` per layer gives *enforced* separability (a consumer literally
  `go get`s one module and the compiler guarantees no baggage). You cannot split
  `go.mod` until the import DAG is already clean — so step 1 is identical either way.
  Defer the `go.mod` split until a real external consumer wants just one piece; until
  then depguard is the cheap 95%.

## Package naming reconsideration

The structural rename worth making is one: **today `internal/sandbox` is the *agent
orchestration* package** (Engine, create, launch, lifecycle) — not "the sandbox."
The isolated environment is the sandbox; the thing that runs agents *in* one is an
orchestrator. The name currently points at the wrong layer, which is exactly the
confusion this split clarifies.

Proposed (for discussion — names are bikeshed-prone; the DAG matters more):

| Current | Role today | Issue | Proposed | Why |
|---|---|---|---|---|
| `internal/runtime` (`Runtime` iface) | backend abstraction (the substrate core) | `runtime.Runtime` stutters; generic | keep pkg `runtime`; iface `Runtime`→`Backend` (`runtime.Backend`) | reads clean; tiny churn |
| `internal/sandbox` | **agent orchestration** | misnomer — it's the agent *runner* | `internal/orchestrator` (or `agentrun`); the substrate reclaims the "sandbox" noun | name matches layer |
| `internal/sandbox/patch` | copy/diff/apply review flow | "patch" undersells the workflow | `internal/copyflow` (or `worktree`/`review`) | names the refinement |
| `internal/runtimeconfig` | entrypoint JSON cfg (agent-coupled) | mixes substrate launch + agent launch + idle | split: substrate launch cfg stays; agent/idle fields → agent layer | kills an import edge |
| `internal/sandbox/store` | persisted metadata (+ `AgentType`) | mixes substrate identity + agent metadata | substrate identity in `store`; agent metadata → agent-owned sidecar | kills the other import edge |
| `internal/workspace` | host-side copy/dir mechanics | fine | keep (or fold into `copyflow`) | — |
| `internal/agent` | agent definitions | fine | keep | — |

Recommendation: **do the DAG/optionality work first** (high value, low bikeshed) and
treat the **renames as a separate, clearly-scoped, mechanical pass** — they touch many
imports but no behavior, so they make clean isolated commits and shouldn't block (or be
blocked by) the structural work. The one rename that carries real conceptual weight is
`sandbox` (orchestration) → something agent-named while the substrate reclaims `sandbox`;
the rest are polish.

## The agent decoupling (close the two edges)

- **`store.Environment.AgentType` / `Model`** → move agent identity out of the substrate
  metadata. Either the substrate `store` holds only substrate identity (name, principal,
  backend, dirs, isolation, …) and the agent layer persists `AgentType`/`Model` in its
  own sidecar, or `store` is generic over an opaque "payload" the agent layer owns. Net:
  `store` no longer imports `agent`.
- **`runtimeconfig.Idle (agent.IdleSupport)`** → the entrypoint config splits into a
  substrate part (mounts, tmux socket, copy dirs, network) and an agent-launch part
  (`AgentCommand`, `AgentLaunchPrefix`, `StartupDelay`, `ReadyPattern`, `SubmitSequence`,
  `Idle`). Only the agent part imports `agent`.
- **The 3 `BackendDescriptor` agent fields + `AgentCommandPreparer`** are already
  declarative/optional and don't import `agent`. Decide: rename to generic terms
  (`PrebuiltCommand*`/`LaunchPrefix`) so the substrate carries no "agent" vocabulary, or
  leave them as harmless named metadata. Low stakes; lean rename for vocabulary purity.

## The session abstraction (PTY-optional; tmux as one strategy)

tmux currently does **two unrelated jobs**: (1) session *persistence* across host
disconnect, and (2) *PTY provision + I/O channel* (input via `send-keys`/`paste-buffer`,
output via `capture-pane`). Split them:

- **Process persistence is already free** — the agent runs inside a long-lived
  container/VM. Only the *interactive session + scrollback* needs a persistence strategy.
- Introduce a **`session` capability** with two strategies, chosen by the consumer/agent:
  - **PTY session** (interactive agents needing a TTY): a detached PTY. tmux is the
    default strategy; `dtach`/`abduco`/a yoloAI-owned `creack/pty` broker are alternatives
    to evaluate later (all sidegrades for the interactive case — do not rip out tmux just
    for cleanliness).
  - **Stream session** (headless agents): durable stdio — stdout to a log, input via a
    fifo/socket. **No PTY, no tmux.** The substrate already supports non-TTY exec; nothing
    is in the way.
- The agent declares which it needs via a capability flag (mirrors the existing
  `IdleSupport` pattern). tmux becomes *one strategy of one refinement*, not the universal
  substrate — and "agents that don't need a PTY" (and non-agent sandboxed tasks) get a
  first-class path.

This is the single most valuable architectural outcome of the split: the piece that is
both load-bearing today *and* not yet decoupled.

## Phasing (the future feature branch)

Each phase is independently mergeable and green under `make check`.

- **0 — backend-leakage map → interface re-derivation (PREREQUISITE, in progress).** Run the
  backend-leakage map (**done** — [research/backend-leakage-map.md](../research/backend-leakage-map.md));
  classify each leak *seal | model-as-property | irreducible*, and re-derive the backend
  interface as the resulting **property set** (starting with `FilesystemLocality` — which is
  orthogonal to `HostFilesystem`, not a unification). **Done so far:** the property is
  declared by all backends and drives **git routing** (`git.NewSandbox`) and the
  **baseline-deferral / in-place-reset** decisions (`prepare_dirs.go`/`vmworkdir.go`/
  `reset.go` — five `rt.(WorkDirSetup)` type-asserts), each with a conformance guard + tests.
  `CopyMountResolver`/`GuestMountResolver` were found to be *operations* (identity default),
  not conversions. **Remaining:** the host-side change probe in `status.go` (behavior-changing
  on Tart → needs Tart validation); the two non-locality decision-drivers
  (`StdioExecer`/`UsernsProvider`); a grep-level "no backend-identity above the runtime" fence;
  and the first conformance-suite slice keyed off the properties across docker/tart/seatbelt.
  This phase decides where the substrate/refinement boundary can honestly fall — the cut below
  depends on it.
- **A — close the import edges.** Relocate `AgentType`/`Model` and the idle/agent-launch
  config out of substrate packages; `store` and the substrate half of `runtimeconfig` no
  longer import `agent`. (Smallest, highest-signal; proves the substrate can be agent-free.)
- **B — extract PTY.** Move `creack/pty` usage out of `internal/runtime` core into its own
  package; assert via `go list -deps` that the core exec/transfer surface pulls no terminal
  library.
- **C — session abstraction.** Introduce the `session` capability with PTY-session (tmux
  strategy) and stream-session (no-PTY) implementations; route the launch layer through it;
  add an agent capability flag. tmux stops being mandatory.
- **D — depguard fences.** Encode the DAG as build-failing rules (substrate → upward = error;
  copyflow ↔ session = error; etc.). Locks in the structure.
- **E — renames (optional, mechanical).** Apply the naming table as isolated commits.
- **F — `go.mod`-per-module (optional).** Only if/when an external consumer wants one piece.

## Open questions

- **Enforcement strength:** depguard-only (conceptual) vs `go.mod`-per-module (enforced).
  Recommend depguard now, `go.mod` on real-consumer demand.
- **`copyflow`/`session` as packages vs modules:** packages first; modules only under F.
- **Substrate store shape:** opaque-payload generic vs agent-owned sidecar file for
  `AgentType`/`Model` (affects the `environment.json` schema — would be another versioned
  migration, like the v1→v2 reshape).
- **How far to push the rename:** just `sandbox`→orchestrator (high value) vs the full table.
- **Scope of the tangled refinements:** `overlay`, `interactive-session/tmux`, and
  `network-isolation` are the only entries needing real untangling (not just a name + a
  fence). The session/tmux work IS in scope (Phase C — it's the substrate/agent boundary).
  Overlay and network-isolation untangling are *internal-cleanliness* of a refinement, not
  the substrate/agent cut — recommend **deferring** them unless a concrete consumer needs an
  overlay-free or network-free build. Flag, don't bundle.

## Non-goals (YAGNI)

- Publishing separate external libraries / separate repos.
- Building the hypothetical headless-runner or non-agent consumer now (the split *enables*
  them; it doesn't require building them).
- Replacing tmux for interactive agents (it works; alternatives are sidegrades). The win is
  *adding* the no-PTY path, not removing tmux.

## Sequencing — status

Done: (1) `multi-workdir` releasetest passed (Linux + Mac) → (2) merged to `main`
(fast-forward to `a631456`) → (3) `module-split` branch cut → (4) this doc, the
[backend-leakage map](../research/backend-leakage-map.md), and the Phase 0 first cut
committed here.

**Next:** finish Phase 0 (the remaining SandboxSide operations, the `status.go` change
probe — which changes Tart behavior and so wants Tart validation, not just Linux
`make check` — and the first conformance-suite slice), then Phases A–F.

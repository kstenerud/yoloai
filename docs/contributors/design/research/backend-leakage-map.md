# Backend-leakage map (Phase 0 of the module split)

**Date:** 2026-06-13. **Status:** Research; feeds
[plans/module-split.md](../plans/module-split.md) Phase 0. Read-only inventory of
where backend-specific behavior leaks *above* the `internal/runtime` layer, so the
backend interface can be re-derived as a set of semantic **properties**.

## Headline findings (verified)

1. **Zero backend-identity logic leaks above the runtime.** A grep for
   `Backend{Tart,Docker,…}` / `.BackendType ==` / `== "tart"` outside `internal/runtime`
   returns only: doc comments, the empty-`BackendType` lazy selector (an emptiness check,
   not identity), and `tart_bases.go` (the `yoloai tart` base-image *feature command*,
   legitimately Tart-bound). **The runtime already hides backend identity from core
   logic.** So the rule "no backend-name above the runtime" is already *satisfied* — it
   was the wrong target.

2. **The real leak is capability-by-type-assertion.** There are **14** optional,
   type-asserted interfaces in `internal/runtime/runtime_optional.go`. Each is a place a
   higher layer detects a capability by `rt.(SomeInterface)` rather than reading a named
   property. **14 is the leakage metric.**

3. **A second, subtler leak is capability-by-implicit-inference.** `copyGitWorkDir`
   (`patch/diff.go`) infers filesystem locality from `mountPath != hostPath`; callers
   branch on `DirMode`. The property (host-side vs sandbox-side files) is real but
   *unnamed* — if metadata is lost, the inference breaks silently.

4. **Filesystem locality is already half-sealed.** `git.NewSandbox(layout, rt, name)`
   dispatches git to `runtime.GitExecer` (Tart → in-VM git) else host git — verified by
   `TestNewSandbox_DispatchesToGitExecer`. Diff, baseline, and tag operations route
   through it and are already backend-correct. The host-assuming **residue is narrow**,
   concentrated in **host-side change detection** (`status.go`'s work-probe), which the
   catalog confirms is *blind to the in-VM workdir* on Tart.

## The 14 optional interfaces, classified

The key distinction: a **decision-driving capability** (a higher layer branches core
logic on it → should be a named **property**) vs. an **optional operation** (the backend
either can or can't perform a maintenance/diagnostic action → fine to keep as a
call-if-present interface; no core-logic branch hangs on it).

### Decision-driving → resolve by injection (preferred) or a declared property

A property + branch is only the *fallback*. The preferred resolution is **injection** — shape
the interface so the decision isn't made at the call site (see the "inject the implementation"
ladder in the plan doc). The "Resolution" column reflects that:

| Interface | Drives | Resolution |
|---|---|---|
| `GitExecer` | run git on host vs in-VM | **`FilesystemLocality`** property — the change-probe consequence ripples (declared fact also feeds conformance/messaging). **DONE** (rung-1 injection at `git.NewSandbox`) |
| `WorkDirSetup` | host baseline vs VM-deferred baseline | `FilesystemLocality` — **DONE**; a `BaselineStrategy` *injection* is the tighter form (could-be-tighter) |
| `StdioExecer` | bridge stdio to an in-sandbox process (MCP) | **already correct** — one wrapper (`Engine.StdioExec`) returns a typed *unsupported* error; the MCP proxy just calls a public exec verb and lets it surface. No higher layer branches. **No change.** |
| `UsernsProvider` | exec user (`keep-id` vs `yoloai`) | **already injection-of-a-value** — the caller uses the returned mode; no property/branch. **No change.** |
| `IsolationCapabilityProvider` | host caps required per isolation mode | **already value-injection** — `RequiredCapabilitiesFor` returns the cap list, validated generically. **No change.** |

**Audit conclusion (2026-06-13):** only `GitExecer` + `WorkDirSetup` were genuine
decision-driving leaks — both now resolved via `FilesystemLocality`. The other five
(`CopyMountResolver`, `GuestMountResolver`, `UsernsProvider`, `StdioExecer`,
`IsolationCapabilityProvider`) were *already* operations or value-injection. The original
"~6 decision-driving" over-counted by tunnel-visioning on type-asserts; the real number was
**2**. The decision-driving conversion work is complete (and the change-probe turned out to be
*already* runtime-aware — `89a30cc`); what remains is a grep fence and a conformance slice.

`FilesystemLocality{HostSide,SandboxSide}` is the headline **decision property**; it gates the
two in-sandbox **operations** `GitExecer` (git) and `WorkDirSetup` (deferred baseline) — the
property says "this backend keeps the work copy in the sandbox", the interfaces remain the
*how*. **Refined by implementation (2026-06-13):** `CopyMountResolver`/`GuestMountResolver`
are NOT locality decisions — they are reached only through `Resolve*For` helpers with an
identity default (no branch), so they are *operations* and belong in the call-if-present list
below, not here.

**Two corrections the implementation audit forced (2026-06-13):**
- It is **orthogonal to `BackendCaps.HostFilesystem`, not a unification.** `HostFilesystem`
  means "sandbox *state* lives on the host" (seatbelt only); locality is about the *work
  copy*. Seatbelt is `HostFilesystem=true` **and** `LocalityHostSide`; tart is
  `HostFilesystem=false` and `LocalitySandboxSide`. Independent axes.
- `copyGitWorkDir`'s `mountPath != hostPath` is **copy-relocation** (true for both seatbelt
  *and* tart), **not** a locality inference. The real implicit locality detection was the
  `rt.(GitExecer)` type-assertion in `git.NewSandbox`.

**Landed (commits on this branch):** `FilesystemLocality` added to `BackendCaps`, declared by
all six backends (tart=SandboxSide, rest=HostSide). It now drives **both** locality decisions:
`git.NewSandbox` routing (replacing the `GitExecer` type-assert) **and** the baseline-deferral
/ in-place-reset decisions in `prepare_dirs.go`/`vmworkdir.go`/`reset.go` (replacing five
`rt.(WorkDirSetup)` type-asserts) — each with a conformance guard (SandboxSide ⟹ implements
the operation) and tests proving the property, not the interface, decides. A test-mock
cleanup followed: mocks now declare locality matching the backend model they represent
(Docker-like = HostSide, Tart-like = SandboxSide) — a blanket "implements GitExec ⟹
SandboxSide" was too coarse. **Remaining:** a grep fence and a conformance slice. (The
change-probe is already runtime-aware — `89a30cc`; `StdioExecer`/`UsernsProvider`/the
resolvers are already operations / value-injection.)

### Optional operations → stay optional interfaces (call-if-present)

`VMCensusReporter`, `DiskUsageReporter`, `CachePruner`, `StaleBasePruner`, `LogTailer`,
`AppleSimulatorRuntimes`, `AgentCommandPreparer`. These are diagnostics / maintenance /
feature operations the backend performs; no higher layer makes a *logic* decision from
their presence (it just calls them if available, for `doctor`/`disk`/`prune`/logs/sim).
`AgentCommandPreparer` is borderline — a deterministic command transform already partly
captured by `BackendDescriptor.AgentLaunchPrefix`; could fold into a descriptor field.

So the reducible target is **~4 decision-driving interfaces → ~3 properties**, dominated
by `FilesystemLocality` (which gates `GitExecer` + `WorkDirSetup` — both now converted). The
other ~9 (incl. the two path resolvers) are legitimately optional operations and need no
change beyond, optionally, a `Supports*` flag for symmetry.

## The existing capability seed (`BackendDescriptor` / `BackendCaps`)

Already-declared proto-properties read above the runtime: `HostFilesystem` (a *separate*
state-location axis — NOT locality), `ContainerAttach`, `CapAdd`, `SupportedIsolationModes`,
`Architectures`, `IsolationTargetOnly`, `AgentProvisionedByBackend`, `HostFromContainer`,
`VMRuntimeDir`. The redesign **extends this struct** with the decision-driving properties
above rather than inventing a new mechanism — the descriptor is already the right home.

## Implications for the module-split plan

- **Reclassify the backend axis as "mostly clean, mostly already-sealed."** Like the agent
  axis, the substrate is further along than feared: identity is hidden, git execution is
  routed. The work is **naming implicit properties**, not untangling deep coupling.
- **`FilesystemLocality` is the one property worth the effort** — it gates the two in-sandbox
  operations `GitExecer` and `WorkDirSetup` (it has replaced both their type-asserts), and it
  makes the catalog's "host probe blind to in-VM workdir" a typed fact instead of tribal
  knowledge. (It is orthogonal to `HostFilesystem`, and distinct from the `mountPath !=
  hostPath` copy-relocation inference — see the corrections above.)
- **Change-detection residue — already closed** (`89a30cc`): `detectWorkdirChanges` routes
  the work-probe through the runtime-aware git (in-VM for Tart; `WorkUnknown` when the VM is
  stopped). Only the broken-sandbox *recovery* fallback (`DetectChanges`) stays host-side, by
  design (the backend is unknown there).
- **Revised governing rule** (the original "no backend-identity checks" is already met):
  *no higher layer may detect a capability by type-assertion or by implicit inference for a
  decision-driving concern — it must read a named, semantic property.* Optional operations
  may remain call-if-present.
- **Conformance suite axes** = the property values: a backend declaring
  `FilesystemLocality=SandboxSide` must pass the sandbox-side diff/apply/change-probe
  conformance tests.

## Method / caveats

Inventories 1, 2, and 4 (interface count, identity-grep, descriptor fields) were verified
directly. The host-assuming-residue count is characterized as "narrow / concentrated in
change-detection" rather than an exact tally — the precise per-site audit of `patch/**`
and `status.go` is the first concrete task of Phase 0 (it's small). The decision-driving
vs. operation split is this doc's synthesis, grounded in each interface's above-runtime
callers.

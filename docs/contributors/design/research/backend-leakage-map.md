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

### Decision-driving → become properties

| Interface | Drives | Property |
|---|---|---|
| `GitExecer` | run git on host vs in-VM | **`FilesystemLocality{HostSide,SandboxSide}`** |
| `WorkDirSetup` | host baseline vs VM-deferred baseline | …same |
| `CopyMountResolver` | copy mount target = host path vs VM-local | …same |
| `GuestMountResolver` | mount visible at container path vs re-rooted guest path | …same |
| `StdioExecer` | can bridge stdio to an in-sandbox process (MCP) | **`StdioBridge bool`** |
| `UsernsProvider` | exec user (`keep-id` vs `yoloai`) | **`UsernsMode string`** |
| `IsolationCapabilityProvider` | host caps required per isolation mode | **`IsolationCaps map[IsolationMode][]HostCapability`** |

`FilesystemLocality` is the headline: it absorbs **four** of the 14 interfaces plus the
implicit `mountPath != hostPath` inference plus the existing `BackendCaps.HostFilesystem`
flag. That single property is most of the backend-axis leak.

### Optional operations → stay optional interfaces (call-if-present)

`VMCensusReporter`, `DiskUsageReporter`, `CachePruner`, `StaleBasePruner`, `LogTailer`,
`AppleSimulatorRuntimes`, `AgentCommandPreparer`. These are diagnostics / maintenance /
feature operations the backend performs; no higher layer makes a *logic* decision from
their presence (it just calls them if available, for `doctor`/`disk`/`prune`/logs/sim).
`AgentCommandPreparer` is borderline — a deterministic command transform already partly
captured by `BackendDescriptor.AgentLaunchPrefix`; could fold into a descriptor field.

So the reducible target is **~6 decision-driving interfaces → ~3 properties**, dominated
by `FilesystemLocality`. The other ~7 are legitimately optional and need no change beyond,
optionally, a `Supports*` flag for symmetry.

## The existing capability seed (`BackendDescriptor` / `BackendCaps`)

Already-declared proto-properties read above the runtime: `HostFilesystem` (→ folds into
`FilesystemLocality`), `ContainerAttach`, `CapAdd`, `SupportedIsolationModes`,
`Architectures`, `IsolationTargetOnly`, `AgentProvisionedByBackend`, `HostFromContainer`,
`VMRuntimeDir`. The redesign **extends this struct** with the decision-driving properties
above rather than inventing a new mechanism — the descriptor is already the right home.

## Implications for the module-split plan

- **Reclassify the backend axis as "mostly clean, mostly already-sealed."** Like the agent
  axis, the substrate is further along than feared: identity is hidden, git execution is
  routed. The work is **naming implicit properties**, not untangling deep coupling.
- **`FilesystemLocality` is the one property worth the effort** — it unifies four
  interfaces, the implicit inference, and `HostFilesystem`, and it makes the catalog's
  "host probe blind to in-VM workdir" a typed fact instead of tribal knowledge.
- **Close the change-detection residue**: route `status.go`'s work-probe through the
  backend (or gate it on `FilesystemLocality == HostSide`) so a dirty Tart sandbox isn't
  reported clean.
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

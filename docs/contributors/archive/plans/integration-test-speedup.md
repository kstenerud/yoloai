> **ABOUTME:** Cut the macOS integration+smoke wall-clock (approaching an hour) by amortizing the
> conformance suite's per-subtest VM boots and introducing bounded parallelism, without weakening
> the per-test isolation that makes a failure legible.

# Integration & smoke test speedup

- **Status:** IMPLEMENTED — designed 2026-07-19 against the real harness (`conformance_iface.go`);
  levers 1–2 built and verified on Linux (docker parallel: 3.0 s, was 6.2 s serial; sharing pinned
  by `conformance_share_test.go`); **Mac-check phase completed 2026-07-19 on Apple Silicon
  hardware** — measurements, the concurrent-exec verdict, and the levers 3–5 outcomes are in
  "Mac-check results" below.
- **Depends on:** —
- **Rides:** **any** release — test-only; no shipped behavior changes. (It is release-*blocking* by
  owner direction, not by the release-kind rule.)

**Concurrency model, as built (a refinement of levers 1–2).** A shared read-only instance that held
a gate token for its whole life would starve the mutating subtests at a single free slot (tart with
one foreign VM). So the first implementation couples the two levers by backend class:
**`SharesReadOnlyInstance` backends run serially** — the shared instance is scoped to a `ReadOnly`
subtest so its slot frees before the mutating boots — while **container backends parallelise**. The
free-slot census still guards every VM run (fail-loud at zero free). Parallelising exec *against* the
shared instance (lever 2 on VM backends) stays deferred to the Mac's verification that the backend
supports concurrent exec — the one open question below.

The macOS integration run (`make integration` + the tart/apple/seatbelt suites) plus the smoke
matrix is approaching **an hour**. Nearly all of it is real VM/container boots, and the single
biggest contributor is the shared conformance suite booting a fresh instance for **every subtest**,
strictly serially, on backends where one boot costs ~90–118 s.

## Where the hour goes (measured against the source, not estimated)

The shared suite is `runtime/runtimetest/conformance_iface.go`
(`RunInterfaceConformance`). Every `t.Run` subtest calls `setup(t)` for a fresh fixture, and each
instance-creating subtest does its own `Create` (+`Start`) — the expensive op. Counting the
instance-creating subtests per backend (a boot = one `NewSleeper`/`sleeper` call):

| Backend | Instance-creating subtests | Per boot | Suite cost |
| --- | --- | --- | --- |
| **tart** | **10** (Stdio + Mounts skip) | multi-GB clone + macOS boot, ~90–118 s | **~15–20 min** |
| **apple** | **12** (Stdio skips; Mounts run) | container/VM boot, lighter | **~5–10 min** |
| docker / podman / containerd | 14 (all sections) | ~1–2 s | seconds each |
| seatbelt | 10 | tmux/process, seconds | minor |

Two structural facts gate everything, both verified in-tree:

- **`t.Parallel()` appears in zero integration/e2e test files.** Every backend and every subtest runs
  strictly serial.
- **Build/image caching is already correct** — `make integration` builds the base once; apple uses a
  `sync.Once` sleep-image; tart conformance reuses `yoloai-base`. **Do not spend effort here.**

Beyond the conformance suite, two more repeat the VM cost: `internal/orchestrator/integration_tart_test.go`
(`TestIntegrationTart_*`, five tests each booting a VM serially, ~7–8 min) and the smoke matrix,
which re-runs create→agent→diff/apply scenarios on tart/apple that overlap what conformance already
proves (`scripts/smoke_test.py`; tart `new` ~118 s each).

## The design

Ranked by wall-clock return. Levers 1–2 are the core; 3–5 are follow-ons.

### 1. Amortize the read-only subtests onto one shared running instance (the big VM win)

**Sharing is a per-backend opt-in, worth its isolation cost only where a boot is expensive.** The VM
backends (tart, apple) share; docker/podman/containerd/seatbelt boot in ~1–2 s and skip sharing
entirely, running every subtest isolated and parallel (lever 2). This confines the isolation risk to
the two backends that actually benefit.

For a sharing backend, most conformance subtests never mutate their instance — they exec a command
against a running sleeper and read the result. Booting a separate instance for each is the waste.
Split the suite's subtests into two classes, by whether they change instance state:

- **Read-only (shareable)** — exec against a running instance, assert output/exit, no state change:
  `ExecSimple`, `ExecNonZeroExit`, `InteractiveExecZeroExit`, `InteractiveExecNonZeroExit`,
  `Stdio/PipesOutput`, `Stdio/NonZeroExit`, `InspectRunning`. Plus `InspectNotFound`, which needs no
  instance at all.
- **Mutating / bespoke-config (must stay isolated)** — `CreateStartStopRemove`, `InspectStopped`,
  `StopIdempotent`, `RemoveIdempotent`, `ExecOnStopped` (all Stop/Remove the instance), and both
  `Mounts/*` (each needs a *different* `MountSpec` supplied at `Create`, so they cannot share a
  generic sleeper). These keep their own fresh instance, exactly as today.

Boot **one** shared read-only sleeper per backend run and route the read-only subtests at it; leave
the mutating subtests untouched. On tart the shareable set present (Stdio skips) is 5 —
`ExecSimple`, `ExecNonZeroExit`, `InteractiveExecZeroExit`, `InteractiveExecNonZeroExit`,
`InspectRunning` — collapsing 5 boots to 1 saves **4 × ~100 s ≈ ~7 min**. Apple saves the same 4
lighter boots; the container backends save 6–7 boots of ~1–2 s (marginal alone, but compounds with
lever 2).

**The isolation contract must be explicit and enforced, or this trades minutes for flaky debugging.**
The shared instance is handed to read-only subtests as a *running, never-mutated* fixture. Guardrails:

- The shared sleeper is created once at the `RunInterfaceConformance` scope and its teardown is
  registered on the **parent** `t` (not a subtest `t`), so it outlives the subtests that borrow it.
- Read-only subtests receive the shared instance name and must not call `Stop`/`Remove`/`Create` on
  it — this is a suite-internal invariant; document it at the split and keep the mutating list as the
  single source of truth for "needs its own instance."
- If a backend cannot guarantee a stable running instance across concurrent execs (see lever 2), it
  opts out of sharing and falls back to per-subtest boots — correctness first.

### 2. Opt-in parallelism, bounded by a VM-concurrency semaphore

No integration test parallelizes today. The independent subtests (and the mutating ones, which each
own their instance) can run concurrently:

- **Container backends (docker/podman/containerd/seatbelt):** `t.Parallel()` on the independent
  subtests. Boots are cheap but numerous; parallelism roughly halves these suites at near-zero risk.
- **apple (and any other VM backend):** **no hard concurrency restriction** — start **unbounded**
  (a soft cap invented before a symptom is just a slower test for no reason) and add one only if
  contention actually appears. Apple's `container` VMs do not count against tart's macOS limit.
- **tart: a *dynamic* semaphore sized to the free VM slots, not a static 2.** macOS enforces a hard
  2-VM limit (Virtualization.framework), and the host may already be running a VM this test cannot
  shut down. So the cap is computed at suite start from a live census: **free = `Limit − occupied`**.
  One foreign VM present → free = 1 → the tart suite runs serially (slow, but it runs — the required
  behavior). free = 0 → **fail loudly with the census** (which VM holds the slot) rather than hang.

  This reuses machinery that already exists: `runtime/tart/census.go`'s
  `Runtime.VMCensus(ctx) → runtime.VMCensus{Limit, Slots}` (optional interface
  `runtime.VMCensusReporter`), where each `VMSlot` carries an `Owned` flag distinguishing yoloai's
  own `yoloai-test-*` VMs from a foreign sandbox. The smoke harness already consumes the same data
  via `doctor --json` (`parse_vm_census` → `plan_tart_slots`, whose `zero_free_blocks` /
  `clamps_to_free` cases are the Python counterpart of this exact logic). The conformance harness
  calls `VMCensus` on the tart runtime directly.

  Note the graceful degradation: when slots are scarce, **lever 1 matters more, not less** — the
  read-only subtests collapse to one shared boot that fits in a single slot, and the mutating
  subtests serialize through the semaphore. The design's worst case is "slow but correct."

Parallelism and lever 1 interact: concurrent execs against the *one* shared read-only instance are
fine for backends that support concurrent exec (docker/tart do), but this is the risk surface — hence
the per-backend opt-out above.

### Mechanism: how a backend declares its policy

Both levers are per-backend-type policy, so the two knobs live on the `InterfaceBackend` test
fixture (alongside the existing `SkipMounts` / `SkipStdio` / `NewSleeper`), never on the shipped
`runtime.BackendDescriptor` — a test-tuning concurrency cap does not belong on a production type:

- **`SharesReadOnlyInstance bool`** — VM backends (tart, apple) set it; container backends leave it
  false and run every subtest isolated-and-parallel. The read-only/mutating classification stays
  *internal to the suite* (two explicit subtest groups, not a per-subtest boolean the backend sees);
  a sharing backend consults it, a non-sharing backend never does.
- **`MaxConcurrentInstances int`** (0 = unbounded) — the *static* soft cap: apple and containers pass
  0. tart is the exception: rather than a static number, a backend implementing
  `runtime.VMCensusReporter` has its cap computed *dynamically* from the live census (free =
  `Limit − occupied`) at suite start, so the one hard constraint in the system reads its value from
  the machine that enforces it instead of a hard-coded literal that can drift.

tart's 2-VM limit is arguably also a production fact (the orchestrator launching sandboxes hits the
same wall — which is why `VMCensus` exists for `doctor`), but enforcing it outside tests is a
separate concern; this plan only *reads* the census, it does not move the limit onto the descriptor.

### 3. Trim redundant slow-backend coverage across tiers

tart is booted in conformance **and** `TestIntegrationTart_*` **and** the smoke matrix
(`stop_start`+restart, `tag_transfer`, `clone`, `isolation_check`). Smoke's create→agent→diff/apply
overlaps what conformance already proves for create/exec/mounts. For the **expensive VM backends
only**, run **one** representative end-to-end smoke scenario (`stop_start`, which also covers restart
+ credential re-injection) on tart/apple, and let the cheaper scenarios (`tag_transfer`, `clone`) run
on docker/seatbelt where a boot is seconds. Each tart scenario dropped ≈ ~118 s. **Log what was
dropped** so the reduced matrix doesn't read as full coverage.

### 4. Consolidate `TestIntegrationTart_*`

Five tests each boot a fresh VM serially (~7–8 min). The booting ones (`FullLifecycle`,
`ResetRefreshesVMWorkDir`, `VMLocalStorageVerification`) may be able to share one booted VM for their
read-only assertions; `MultipleAuxDirs`/`GitCorruption` already note "Create provisions but does not
boot." Lower confidence than 1–3 (these are lifecycle tests); investigate whether a shared booted VM
is safe before assuming 1–2 boots saved.

### 5. (folded from the test audit) Un-gate the VM-free tart/seatbelt tests

`TestTart_New_ReturnsRuntime`, `_Descriptor_*`, `_InspectNotFound`, `_RemoveIdempotent_*` and the
seatbelt equivalents carry `//go:build integration` but need no VM. **They cannot simply be untagged:**
each calls `New()` via `tartSetup`, which `require.NoError`s, so on a non-macOS host the untagged test
would *fail* `make check`, not skip. To move them out of the integration gate they need a macOS
platform-skip (`if !isMacOS() { t.Skip }`). Even then they only *run* on a Mac — so this is a
**Mac-side hygiene item**, not a Linux win, and belongs in the Mac-check phase below.

## What not to touch

Build/image caching (already correct — see above). Re-verifying it would burn review time on a solved
problem.

## Risks

- **Isolation regression (the main one).** A shared read-only instance that a mis-classified subtest
  mutates makes later subtests flaky and the failure non-local. Mitigation: the mutating list is the
  single source of truth; a subtest is shareable only if it appears nowhere in it, and the shared
  instance is never handed a `Stop`/`Remove`/`Create` call. Prefer per-boot fallback over a clever
  share when in doubt.
- **Concurrent-exec races** against the shared instance on a backend that serializes exec. Guard with
  the per-backend opt-out.
- **VM host exhaustion** under parallelism — for **tart**, bounded by the dynamic free-slot semaphore
  from `VMCensus` (never exceeds `Limit`); other VM backends have no fixed cap and are bounded by host
  resources.
- **A foreign tart VM the run cannot control** (the owner's case: a sandbox that won't shut down). The
  free-slot census handles it — one foreign VM → the suite runs serially rather than trying to boot a
  third VM and failing mid-run; zero free → it fails at the *start* with the census naming the
  occupant, not deep in a subtest. The foreign VM is never counted as ours (`VMSlot.Owned`) so prune/
  cleanup never touches it.
- **Reduced smoke matrix hiding a real gap** — mitigated by `log()`-ing every dropped scenario.

## Phasing (Linux implement + verify → Mac check)

Matches the owner's "the mac side gets a check of its own to ensure the revamp works as-is (or fix
it)":

1. **Linux phase** — implement levers 1–2 in the shared harness and verify against
   docker/podman/containerd (all Linux-runnable). The harness change is platform-agnostic Go, so it
   compiles and its container-backend behavior is fully exercisable here. This is where the
   share/parallel mechanism and the isolation contract get proven.
2. **Mac check** — on a Mac, confirm the revamped harness works for tart/apple/seatbelt as-is (the VM
   backends the speedup is *for*), fix any VM-specific issue (concurrent-exec support, boot timing,
   the semaphore cap), and land levers 3–5 (smoke-matrix trim, `TestIntegrationTart_*` consolidation,
   the un-gate with platform-skip). Measure the before/after wall-clock.

## Estimated impact

Levers 1+2 target the ~15–20 min tart conformance suite and its apple twin: realistically **~10–15
min off the macOS run** with isolation preserved, before lever 3 trims several more minutes of
redundant smoke boots.

## Mac-check results (2026-07-19, M-series host, 2 free VM slots)

Both VM backends now set `SharesReadOnlyInstance: true`. Wall-clock for the tart conformance
suite, same host, same base image, back to back:

| Configuration | Wall-clock | Boots |
| --- | --- | --- |
| Lever off (per-subtest boots, 2-wide parallel via the census gate) | **3 m 55 s** | 10 |
| Lever on (shared read-only instance, suite serial) | **2 m 33 s** | 6 |

Boots on this host cost ~25–45 s (clone + boot), well under the design's 90–118 s estimate, and
the census-gated parallelism already landed from Linux — so the honest "before" is the 3 m 55 s
parallel run, not the old ~15–20 min serial harness. Sharing still wins by ~35 % *against* that
parallel baseline: the read-only group costs one 29 s boot instead of five.

**apple:** the lever-off configuration does not run at all on hardware — `appleSetup` isolates HOME
via `t.Setenv`, which panics under the non-sharing path's `t.Parallel()` (filed as DF147; invisible
to the Linux phase because apple only runs on macOS). With sharing on, the suite is green in
**30.6 s**. seatbelt's fixture captures its runtime at parent scope and is unaffected.

**Concurrent-exec question — settled: tart Exec against one running VM is concurrency-safe.** A
hardware probe ran 8 goroutines × 5 rounds of `Exec` (40 total) against a single running clone;
zero failures, every output exact. **The shared read-only subtests stay serial anyway:** measured
post-boot they cost 0.02–0.10 s each, so parallelising them would recover well under one second.
The verdict matters only if the harness ever wants to overlap the shared instance with the
mutating boots; that refinement stays unbuilt (its ceiling on this host is ~30 s).

**Lever 3 — done.** `tag_transfer` is trimmed from the expensive VM backends
(`EXPENSIVE_VM_BACKENDS = {tart, apple}` in `scripts/smoke_test.py`): it boots a second sandbox
per backend and its git-transfer plumbing is backend-agnostic, so it runs only where a boot costs
seconds; tart/apple keep `stop_start` as their end-to-end scenario. `isolation_check` is *not*
trimmed (guest network isolation is per-backend coverage nothing else proves); `clone` already ran
only on the default docker backend. The trim is logged at scheduling ("trimmed on expensive VM
backends…") and pinned by `test_tag_transfer_trimmed_on_expensive_vm_backends_only`.

**Lever 4 — investigated on hardware, declined.** The suite measured **6 m 46 s** all-green on
this host (FullLifecycle 114 s, GitCorruption 104 s, ResetRefreshesVMWorkDir 99 s, MultipleAuxDirs
48 s, VMLocalStorageVerification 41 s), confirming the ~7–8 min estimate. But in all three booting
tests the boots are themselves the behavior under test (Start, restart-recreate-from-staging,
reset-recreate); a shared booted VM would erase the lifecycle transitions being pinned. The only
mergeable piece (VMLocalStorageVerification's assertions into FullLifecycle's first started state)
saves one ~41 s boot at real failure-legibility cost — declined.

**Lever 5 — done.** The VM-free tart/seatbelt basics moved untagged
(`runtime/{tart,seatbelt}/backend_basics_test.go`), with the platform guard in the shared setup
helpers (`setup_test.go`): skip off macOS (structurally impossible), and on macOS an absent
backend fails per D112 via `testutil.RequireBackend` (carve-out honored). They now run in every
macOS `make check`.

## Open questions

None — the concurrent-exec question was the last one, settled above.

**Resolved during design:** the shared-instance split is two explicit subtest groups (not a
per-subtest boolean); sharing is a per-backend opt-in (VM backends only); apple starts unbounded; and
tart's cap is the dynamic free-slot census, not a static 2. See levers 1–2.

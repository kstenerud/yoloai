> **ABOUTME:** Cut the macOS integration+smoke wall-clock (approaching an hour) by amortizing the
> conformance suite's per-subtest VM boots and introducing bounded parallelism, without weakening
> the per-test isolation that makes a failure legible.

# Integration & smoke test speedup

- **Status:** PLANNED — designed 2026-07-19 against the real harness (`conformance_iface.go`);
  levers 1–2 are the load-bearing changes, 3–5 are follow-ons. Not yet built.
- **Depends on:** —
- **Rides:** **any** release — test-only; no shipped behavior changes. (It is release-*blocking* by
  owner direction, not by the release-kind rule.)

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

Most conformance subtests never mutate their instance — they exec a command against a running
sleeper and read the result. Booting a separate instance for each is the waste. Split the suite's
subtests into two classes, by whether they change instance state:

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
- **tart:** has a **hard 2-VM cap** (established fact — the tart/host limit smoke already enforces via
  `YOLOAI_SMOKE_VM_CONCURRENCY=2`). Gate tart concurrency behind a **semaphore of 2**, never raw
  `t.Parallel()`. Even 2-wide, applied to the mutating subtests that still boot, it compounds with
  lever 1.
- **apple (and any other VM backend):** **no hard concurrency restriction** — bounded only by host
  resources, not a fixed cap. Apple can parallelize more freely than tart; pick a resource-sane cap
  if needed, but it is *not* held to tart's 2. (Smoke's uniform `vm_concurrency=2` is a conservative
  default across VM backends; the actual hard limit is tart's alone.)

Parallelism and lever 1 interact: concurrent execs against the *one* shared read-only instance are
fine for backends that support concurrent exec (docker/tart do), but this is the risk surface — hence
the per-backend opt-out above.

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
- **VM host exhaustion** under parallelism — for **tart**, bounded by its hard 2-VM semaphore
  (matching smoke); other VM backends have no fixed cap and are bounded by host resources.
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

## Open questions

- Does tart support concurrent `Exec` against one running VM reliably enough to parallelize the shared
  read-only subtests, or should VM backends share-but-serialize (lever 1 without lever 2's parallelism
  on the shared instance)? Settle on the Mac.
- Is the shared-instance split best expressed as two explicit subtest groups in
  `RunInterfaceConformance`, or as a per-subtest `shareable bool` the suite dispatches on? The former
  is more legible; the latter keeps each subtest self-describing. Decide during the Linux build.

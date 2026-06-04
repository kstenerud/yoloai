<!-- ABOUTME: Active queue of open design critiques for yoloAI. Resolved items drain to -->
<!-- ABOUTME: resolved-critiques.md; deferred to deferred-critiques.md; abandoned to abandoned-critiques.md. -->

# Open critiques

Active design critiques awaiting action. Each is drained to one of three co-located sinks once
settled: [`resolved-critiques.md`](resolved-critiques.md) (applied),
[`deferred-critiques.md`](deferred-critiques.md) (parked with a `Trigger:`), or
[`abandoned-critiques.md`](abandoned-critiques.md) (dropped with a `Why:`). Keep only live items
here — resolved entries belong in the sink, not as stubs.

> The **2026-05-30 Post-F1-Close round** is fully drained: G1/G2/G3/G4/G5/G6/G8 →
> [`resolved-critiques.md`](resolved-critiques.md); G7 (extension residue) →
> [`abandoned-critiques.md`](abandoned-critiques.md) (D66); the D53 read-model reshape closed with
> commit `2916e24`; carried-forward findings F6/F7/F9 done 2026-06-01.

> The **2026-06-03 Public-API "right reasons" round (A1–A4)** is fully drained: A1 (mirror-vs-alias)
> and A2/A3 (surface split on backend-liveness; `SystemClient` junk drawer; homeless agent noun) →
> [`resolved-critiques.md`](resolved-critiques.md) (A2/A3 implemented C1–C4, D67); A4 (public
> struct-tags premise) → [`abandoned-critiques.md`](abandoned-critiques.md), with the genuine CLI
> `--json` residual tracked as [`unresolved-findings.md`](unresolved-findings.md) DF17.

---

## 2026-06-03 Internal-code round (IC1–IC15)

Code-quality critique of the internal layers (root public package + `internal/sandbox`,
`internal/runtime`, `internal/config`, `internal/cli`), distinct from the public-API "A"/"G"
contract rounds above. Found via a whole-file read pass; `file:line` anchors are as of commit
`c122151` and will drift. Severity is maintainability/correctness impact, not contract shape.

### HIGH

**IC1 — `lifecycle/restart.go` prompt-delivery machinery is copy-pasted.** *(verified)*
- **Smell.** Four building blocks duplicated across six functions.
- **Evidence.** `sendResumePrompt` (303) and `sendCustomPrompt` (396) are identical except the temp
  filename and the preamble; `prepareResumeFiles` (479) and `prepareCustomPromptFiles` (437) are
  identical except the resume preamble; the runtime-config.json `ReadFile`+`Unmarshal` pair recurs
  6× (129, 243, 268, 359, 447, 502); the `agent.GetAgent`+"unknown agent %q" block recurs ~6×
  (98, 278, 369, 457; `start.go:208`).
- **Direction.** Extract `deliverPromptViaTmux(...)`, a single prepare-files helper taking the
  prompt text, `loadContainerConfig(dir)`, and `requireAgent(meta)`.

**IC2 — config handler layer is structurally triplicated.** *(verified)*
- **Smell.** ~250 lines that differ only by struct type.
- **Evidence.** `yoloaiScalarHandler`/`yoloaiExpandedSeqHandler`/`yoloaiStringMapHandler`/
  `handleYoloaiTart` (`config.go:183–244`) are mirrors of `profile*` equivalents
  (`profile.go:172–233`); the `YoloaiConfig`/`ProfileConfig`/`MergedConfig` field lists are a third
  near-identical copy. `ProfileConfig` is essentially `YoloaiConfig` plus workdir/directories/backend.
- **Direction.** Generic field-pointer handlers over a shared interface, or fold profile parsing
  onto the yoloai parser.
- **Decision (2026-06-04).** **Fold** — parse `ProfileConfig` through the `YoloaiConfig` path
  (profile is the superset + workdir/directories/backend), eliminating the mirrored handler set and
  one of the three struct field-lists. Higher-risk because it touches the global-vs-profile routing
  `isGlobalKey` guards; do it as an isolated commit with the existing config tests green before/after.

**IC3 — `config.go` is two files in one (1210 lines).**
- **Smell.** A typed config domain model bolted to a generic dotted-path YAML-node CRUD engine that
  never references the model.
- **Evidence.** Model + handlers (19–536) vs. node-walking machinery (656–1210:
  `setYAMLField`/`deleteYAMLField`/`getOrCreateMapping`/`mergeNodes`/`sortMappingNode*`/
  `splitDottedPath`/`FindYAMLValue`).
- **Direction.** Split the node machinery into `yamlnode.go`; leave model + handlers in `config.go`.

**IC4 — `runtime.Runtime` is a grab-bag (627 lines).**
- **Smell.** A 16-method core plus 14 optional interfaces, several single-backend, with no legible
  taxonomy.
- **Evidence.** tart-only: `AppleSimulatorRuntimes`, `VMCensusReporter`, `WorkDirSetup`,
  `GuestMountResolver`; docker-only: `StdioExecer`. The type-assert + `*For`-fallback pattern is the
  right "no backend types leak" tax, but it has drifted.
- **Direction.** Group optionals into `runtime_optional.go` with documented categories
  (lifecycle-core / capability-probes / path-translators); consider collapsing the three tart
  path-translators (`GuestMountResolver`+`CopyMountResolver`+`WorkDirSetup`) into one
  `GuestPathTranslator`.
- **Decision (2026-06-04).** Go **semantic**, not just cosmetic: regroup the optionals into
  `runtime_optional.go` with a documented taxonomy AND collapse the three tart path-translators
  (`GuestMountResolver`+`CopyMountResolver`+`WorkDirSetup`) into one `GuestPathTranslator`. This
  changes the interface contract, so confirm the three are genuinely one concern (host↔guest path
  translation) and that only Tart implements them before merging.

**IC5 — `create.go` pipeline threads giant positional parameter lists.**
- **Smell.** Long scalar param lists invite transposition bugs.
- **Evidence.** `buildConfigAndEnvironment` (14 params), `buildSandboxStateResult` (18 params),
  `resolveProfileAndArchetype` returns an 8-value tuple with two bare `bool`s.
- **Direction.** Carry pipeline state in an intermediate struct (e.g. `resolvedCreateInputs`).

### MEDIUM

**IC6 — backend descriptors use raw string literals instead of the public consts.**
- **Evidence.** `Type: "tart"` (`tart.go:42`), `"docker"` (`docker.go:39`), etc., plus
  `Register("tart", …)`; the registry `panic`s on `name != descriptor.Type` *only because* the
  literal is hand-duplicated.
- **Direction.** Use the typed consts (`BackendDocker`/`BackendTart`/…); the mismatch panic then
  becomes structurally impossible.
- **Done (2026-06-04).** All five descriptors + `Register` calls use the `runtime.Backend*` consts.
  Took the simplification one step further: since the registry key was always `== descriptor.Type`,
  dropped `Register`'s redundant `name` parameter (now keys on `descriptor.Type`) and deleted the
  now-impossible mismatch panic entirely.

**IC7 — option→internal mapping convention is incoherent.**
- **Evidence.** `SandboxRunOptions.materialize()`, several `*.toInternal()`, and inline
  field-by-field mapping (`SystemBuildOptions`, `Workdir*Options`, `AgentLogsOptions`) with no rule
  for which form applies.
- **Direction.** Pick one convention (e.g. `toInternal()` whenever an internal counterpart exists,
  inline only when none) and document it.
- **Decision (2026-06-04).** Standardize on **`toInternal()` whenever an internal counterpart type
  exists**; inline mapping only when there is no internal struct to map to. Record the rule next to
  the option types so future additions follow it.

**IC8 — `tart.go` (1568 lines) has clean extraction seams left unsplit.**
- **Evidence.** Base-image/Xcode provisioning (1267–1472) and the VirtioFS mount/symlink subsystem
  (735–1027) are independently testable concerns sitting in the lifecycle file.
- **Direction.** Move provisioning to `build.go`, mount/symlink to `tart/mounts.go`; ~700 lines of
  genuine lifecycle remain.

**IC9 — CLI `WithClient → Sandbox(name) → Workdir()` boilerplate repeats ~22×.**
- **Evidence.** `workflow/diff.go` opens the full prologue 10× across its five log helpers; the
  `sb, err := c.Sandbox(name); if err != nil { return err }` prologue recurs 22× across `cli/`.
- **Direction.** Add `cliutil.WithWorkdir(cmd, name, fn)` / `WithSandbox` folding
  backend-resolve + Client + Sandbox + Workdir into one call.
- **Done (2026-06-04).** Added `cliutil.WithSandbox(cmd, name, fn(ctx, *Sandbox))` and the narrower
  `cliutil.WithWorkdir(cmd, name, fn(ctx, *Workdir))`. Migrated the matching prologue across
  `workflow/` (diff/apply*/baseline/attach), `sandboxcmd/` (info/exec/allow/deny/allowed/
  terminal_snapshot), and `lifecycle/` (reset/restart/start) — net ~−127 lines. Sites where the
  `*Client` itself is needed (clone/destroy/stop/mcp, cross-sandbox loops) or that use the
  backend-less `cliutil.Client(cmd)` reader path were intentionally left. `WithSandbox` also folds
  in `SandboxErrorHint` on the lookup error, so the dir/`destroy`-hint is now consistent across all
  per-sandbox commands (previously only `attach` did it).

**IC10 — `cliutil.System()` panics on a should-be-impossible error.**
- **Evidence.** `cliutil/client.go:163` returns no error and `panic`s if `NewClient` fails; every
  `system` subcommand routes through it, so a future DataDir-validation change turns a user error
  into a crash.
- **Direction.** Return `(*yoloai.System, error)` like `cliutil.Client()` already does.
- **Done (2026-06-04).** `cliutil.System()` now returns `(*yoloai.System, error)`; all ~30 handler
  call sites extract-and-error-check. Three stateless verbs that some non-error-returning callers
  needed (`Archetypes`, `AgentTypes`, no-probe `BackendTypes`) were also exposed as package-level
  free functions in `discovery.go` (precedent: `yoloai.Archetypes()`), so help-text generators and
  the `new` flag-description avoid the fallible handle entirely. `gate.go` extracted
  `initFreshDataDir`/`checkDataDirStatus` to stay under the cyclop limit; the `tart` subpackage's
  `pkgClient func() (*yoloai.System, error)` propagated the signature change.

**IC11 — `status.InspectSandbox` hand-rolls change-detection that `detectWorkdirChanges` owns.**
- **Evidence.** `status/status.go:302–323` inlines the workdir+aux change logic while
  `InspectSandboxWithBackend` calls the extracted `detectWorkdirChanges`; the two diverge subtly
  (`InspectSandbox` can report "no"; the helper short-circuits on "-").
- **Direction.** Have `InspectSandbox` call the shared helper.
- **Done (2026-06-04).** `InspectSandbox` now calls `detectWorkdirChanges`. On trace the inline and
  helper forms were already behaviorally identical (both can return "yes"/"no"/"-"), so this is a
  pure DRY collapse, no behavior change. (Aside: `InspectSandbox` and `InspectSandboxWithBackend`
  are now near-duplicates differing only in nil-`rt` handling — a possible future unify, out of
  scope here.)

**IC12 — reset's in-place path uses host `rsync`/`git`. (original "bypasses runtime seam" premise ABANDONED).**
- **Investigated 2026-06-04 (read of `reset.go` + `runtime.go` GitExecFor).** The original framing
  ("bypasses the runtime exec seam, inconsistent with `runtime.GitExecFor`") is **wrong**.
  `resetInPlace` only runs for non-Tart backends (Tart guarded out at `reset.go:406`, force-upgraded
  to restart at `:85`); for those backends a `:copy` workdir is a **host directory bind-mounted**
  into the container. `rsyncDir` is therefore a host→host sync of the bind-mounted copy while the
  agent keeps running — it is intentionally NOT a container exec, and `GitExecFor` (in-container vs
  host git) is irrelevant since this path is non-Tart by construction and `GitExecFor` would resolve
  to `hostGitExec` anyway. The host-side design is deliberate: it avoids disrupting the live
  container, and the one case where host-side access is impossible (Tart, workdir inside the VM) is
  explicitly excluded.
- **Residual (LOW) — keep.** (a) **Undeclared host `rsync` dependency**: in-place reset requires
  `rsync` on container backends, but `CLAUDE.md` claims "no runtime deps — just the binary and
  Docker"; the lifecycle tests `exec.LookPath("rsync")` and skip when absent
  (`lifecycle_test.go:786/881/940`). Either document the dependency or replace rsync with an
  in-process differential sync. (b) The **rsync-vs-`CopyDir` choice is correct but undocumented** —
  in-place must do a differential update with no window where the tree vanishes under the live
  container (vs the restart path's `RemoveAll`+`CopyDir`); add a one-line *why* at `rsyncDir`.
- **Spun off as IC16 (tangential):** see below.

**IC16 — two near-identical host-git wrappers coexist (LOW, dedup-only).**
- **Verified 2026-06-04.** `workspace/git.go:18` and `runtime.hostGitExec` (`runtime.go:576`) are
  near-duplicate host-git entry points; **both correctly disable repo hooks**
  (`core.hooksPath=/dev/null`), so there is NO latent hook-firing bug (the initial concern was
  wrong). Pure DRY: `workspace.*` serves create/reset host-side git; `hostGitExec` serves
  diff/apply via `GitExecFor`'s host fallback.
- **Direction.** Low priority — consider routing `workspace`'s git wrapper through `hostGitExec`
  (or vice versa) so there is one host-git chokepoint, but the duplication is benign as-is.

### LOW — cleanup sweep

**IC13 — stale ABOUTME/doc comments.**
- `create.go:1` ("machine-id generation and directory/file write utilities" — that code is gone;
  the file is now the create-pipeline orchestrator); `client.go:1` lists `Apply`/`Diff` (moved to
  `Workdir`); `discovery.go:113` references `Backends(...)` (renamed `BackendTypes` in D68);
  `config.go:671` "for use by CLI commands" (the only non-test caller is the library's own
  `system_config.go`, not CLI).
- **Done (2026-06-04).** All four comments corrected.

**IC14 — naming nits.**
- `Engine` receiver is still `m` (leftover from `Manager`); its doc reads "configures a Engine"
  (`engine.go:36,40,53,69`). `BackendType`-typed fields named `Name` (`system.go:131`,
  `diagnostics.go:55`) violate the D68 Type-vs-Name rule. Top-level `Info` vs `SystemInfo` ambiguity
  → consider `SandboxInfo`. `DeleteProfileResult` is verb-first → `ProfileDeleteResult`.
  `Files.Import/Export` take bare `force bool` and `ProfileAdmin.*` take bare `name string`,
  breaking the `<Noun><Verb>Options` convention just established.
- **Done (2026-06-04).** `Engine` receiver `m`→`e` + "an Engine" grammar; `BackendDiskUsage.Name`
  and `BackendDiagnostic.Name` → `.Type` (D68 rule); `DeleteProfileResult`→`ProfileDeleteResult`.
- **Deferred (2026-06-04).** `Info`→`SandboxInfo` is a 61-site rename of a public type that ships
  on `main` — a real breaking change needing a BREAKING-CHANGES entry; parked as its own item, not
  folded into a LOW sweep. **Trigger:** next intentional public-API breaking batch.
- **Won't do (2026-06-04).** Wrapping single-primitive args (`force bool`, `name string`) in
  `<Noun><Verb>Options` structs is YAGNI — the convention is for multi-field params, not a struct
  per lone bool/string. The bare named args read fine; consequence-naming the bool is the only
  latent nit and not worth the churn.

**IC15 — dead / no-op code.**
- `client.go:475 resolveProfileFromConfig()` hardcodes `return ""`; `profile.go:464–545` legacy
  `extends`-chain scaffolding (`ResolveProfileChain`/`loadProfileLegacy`/`formatCycle`) for an
  inheritance feature the comment says is unsupported, with `LoadMergedConfig` still routing through
  it to build a chain that is always `["base", name]`; `engine.go:83 NewEngine` panics when
  `WithLayout` is omitted (the one panic in an otherwise error-returning package — a fallible
  constructor would surface it to embedders); six `prune.go` functions redeclare an anonymous
  `interface{ Write([]byte)(int,error) }` instead of `io.Writer`.
- **Done (2026-06-04).** Removed the no-op `resolveProfileFromConfig()` + its call (the create
  pipeline already resolves an empty `Profile`); six `prune.go` signatures now take `io.Writer`.
- **Reframed — not dead (2026-06-04, verified).** `ResolveProfileChain` is *live, load-bearing*
  code: 5+ production callers (`create/prepare_profile.go`, `profiles/profile_build.go`,
  `lifecycle/lifecycle.go`+`restart.go`, `system.go`, `profile.go`) plus a passing multi-level +
  cycle-detection test suite. Even with inheritance "legacy only", it performs profile-existence
  validation and produces the `["base", name]` chain every load path consumes — removing it breaks
  the build and tests. Same mis-diagnosis pattern as IC12.
- **Won't do — consistent convention (2026-06-04).** `NewEngine`'s panic on missing `WithLayout`
  mirrors `config.NewLayout`'s panic on empty input (engine.go's own comment cites it): an internal
  constructor guarding a programmer invariant, not embedder-facing (`sandbox` is `internal/`;
  embedders go through `yoloai.Client`). Converting to `(*Engine, error)` would make it *inconsistent*
  with `NewLayout` and force 13 call sites to handle an error that can't occur in correct code.

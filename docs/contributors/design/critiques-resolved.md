> **ABOUTME:** History sink for critiques that were resolved and applied.
> Companion to critiques-unresolved.md, which holds the active working queue.

# Resolved critiques

History of critiques that have been addressed and applied. Items are moved here from
[`critiques-unresolved.md`](critiques-unresolved.md) once resolved, so the active file stays
a working set. Newest first.

## AC1–AC14 (2026-06-10 Apple `container` backend round) — spike re-verified live; build-context + wizard-clear fixes applied

- **Severity:** design-review (pre-implementation; no production code yet). **Resolved:** 2026-06-10 on
  `apple-container-backend`. An independent adversarial agent re-ran the spike against `container` 1.0.0
  (macOS 26, Apple Silicon), read the Swift source at `~/Devel/container`, and checked every cited
  yoloAI internal. Core capability claims (Q1–Q4) all **CONFIRMED**; corrections applied to
  `plans/apple-container-backend.md` + `research/apple-container.md`.
- **AC1 (HIGH, new) — `container build .` drops a relative context** (empty 2-byte context → every
  `COPY` fails; yoloAI's base Dockerfile has 8). Fix applied: `Setup` builds with an **absolute**
  context path; **no Docker daemon needed** (kills the feared docker-coupling fallback, "no Docker
  Desktop" survives). Flagged for `backend-idiosyncrasies.md` at implementation.
- **AC2 (MED) — wizard `Set(key,"")` does NOT clear** (`mergeStringField` treats empty override as
  "keep base"). Fix applied: preset writer uses `DeleteConfigField`/`Reset` (primitive exists,
  `yamlnode.go:249`).
- **AC3 (MED) — build needs `container builder start`** first (separate builder VM, cold-start). Added
  to the `Setup` step.
- **AC6 — inspect mounts are nested** (`type:{virtiofs:{}}` + `options[]`, not flat). Parser note added
  to the `Inspect` row + research Q4.
- **AC10 — allowlist DNS is the vmnet gateway** (`192.168.64.1`), not host resolv.conf; default-deny
  must ACCEPT gateway:53. Network-isolation section updated; flagged for a live end-to-end test (raw
  iptables alone isn't enough).
- **AC8 — source-path drift** corrected to `Sources/Services/…`; noted the non-env
  `~/.config/container/config.toml`.
- **AC14 — macOS-15 runs with limitations; some features want M3.** Kept the strict `macOS≥26` probe
  gate (safe over-gate); M3 caveat to note in GUIDE. Confirmed: no `--privileged`, `--cap-add ALL`
  → full caps, 0.66s boot, kernel 6.18.15, no suspend subcommand, `--label`/`-p`/`--init` at create.
- **CONFIRMED (tried hard to break, couldn't):** Q1–Q4 (live `:rw` both directions, `:ro`,
  overlay+CAP_SYS_ADMIN, in-guest iptables), `machine`≠`container` home-mount (`defaultHomeMount=.rw`
  machine-only), XPC daemon / no socket env, SSH_AUTH_SOCK forwarded (the "don't forward" caution is
  justified), vm-tier modeling, literal mount paths, and all cited yoloAI internals (probe.go routing +
  alphabetical fallback, dockerhost socket order, setup.go writes only `container_backend`, single-Probe
  descriptor → the installed/running split is genuine cross-cutting work).

## T1–T15 (2026-06-04 Testing-critique round) — test-suite placement, dedup, and error-path coverage

- **Severity:** test-health (no production behavior change). **Resolved:** 2026-06-04 on
  `layering-refactor`, one commit per item, `make check` green throughout. Critique of 168 Go test
  files (~33.6k LOC) across the 4 tiers. The suite was healthy on §6/§7/§8 (real backends, no daemon
  mocks, manual fakes); weaknesses clustered in cross-backend/e2e **duplication**, lagging
  **error-path coverage**, and a few **mis-placed/over-gated** tests. **T7 (broad `t.Parallel`
  adoption) carried over** — it remains the sole live item in
  [`critiques-unresolved.md`](critiques-unresolved.md).
- **Recurring lesson:** several citations were *stale on inspection* — the named anti-pattern had
  already been fixed (T3 was correctly a unit test, not mis-gated; T11's destroy/tart-stop tests
  already asserted real post-conditions; T2's "5 identical backends" was really only 2 twins). Verify
  the test's current state before "fixing" it.
- **T1** — dropped the gratuitous Docker gate on `patch/{apply,diff}_test.go` `:copy` paths
  (`getTestRuntime`→`hostGitRuntime()` nil); ~67 tests moved to the unit suite (30d50e7).
- **T3** — `tart/stop_integration_test.go`→`stop_escalation_test.go` + ABOUTME corrected (it uses a
  fake tart binary, no VM — belongs in the unit suite) (827bfa5).
- **T2** — scope corrected: only docker+podman are genuine twins (podman embeds `*docker.Runtime`).
  Extracted the docker-compatible behavioral table into `runtimetest.RunConformance` (keyed on
  `DockerCompatRuntime`); docker/podman tests became thin `_test`-package setup closures. containerd/
  seatbelt/tart left distinct (different constructors/creation models). Compile-checked under
  `-tags integration`; run needs daemons + `yoloai-base`.
- **T4** — trimmed the e2e ice-cream-cone: pushed error→exit-code mapping to a unit test
  (`TestErrorExitCode`), deleted `workflow_test.go` (integration owns it), collapsed `error_test.go`
  to one binary error-contract smoke, dropped help/version e2e (covered by `TestExecute_RunsGate`).
- **T5** — collapsed the two e2e bugreport tests to one flag-path smoke (asserts the `--bugreport`
  flag-unique Live-log/Exit-code sections); integration subcommand pair kept; redaction matrix stays
  unit-owned.
- **T6** — collapsed three duplicate "sandbox not found" tests to one canonical case (743e23a).
- **T8** — removed ~30 vestigial `t.Setenv("HOME")` sites; inject the layout directly (1541d37).
- **T9** — strengthened `TestWithNamespace`; deleted misleading `TestKataConfigPath`; kept+reframed
  `names_test.go` as a wire-format golden guard. Follow-up: the weak isWSL2 tests were the only thing
  keeping dead `isWSL2`/`procVersionIsWSL2` compiling — deleted the whole WSL2 detection.
- **T10** — enriched `TestMeta_SaveLoadRoundTrip` to cover every persisted field; deleted four subset
  round-trips + two literal-dup tests; kept distinct-logic (omitempty/version/migration) and the
  legit unit-vs-integration diff split.
- **T11** — strengthened the real bare-`assert.Error` sites to pin specific errors:
  `StreamLogs`/`ReadStoredPrompt`/`Clone`→`ErrorIs(ErrSandboxNotFound)`/`ErrorAs(*UsageError)`; three
  `patchConfig*_MissingConfig`→`ErrorIs(fs.ErrNotExist)`.
- **T12** — added `new.go` usage-error unit tests (parse positional / flag conflicts / port / env)
  (d36c04c).
- **T13** — promoted cheap error paths: six `Reset` `_, _ =` discards→`require.Error` on the
  propagated downstream failure; `HasUncommittedChanges` `*runtime.ExecError` branch covered (exit-1
  = dirty, non-1 = surfaced error) via a GitExecer fake (19a3c0c). Live-backend error paths +
  Seatbelt/Tart run coverage split to [`findings-unresolved.md`](findings-unresolved.md) DF18.
- **T14** — `public_api_test.go`→`internal_leak_fence_test.go` (it's a go/packages static fence, not
  behavioral API tests) (b9ab232).
- **T15** — `TestSystemClient_*`→`TestSystem_*` post-D67 rename (9addc32).

## R1–R6 (2026-06-04 Post-D67/D68 public-surface residue round) — rename-residue + doc-rot on the root package

- **Severity:** doc/cosmetic, one correctness-of-godoc (R1). **Resolved:** 2026-06-04 on
  `layering-refactor`. Found via a whole-file read pass of the root `yoloai` package (the surface most
  churned by A2/A3, D67, D68). `internal/` not re-swept (IC1–IC16 covered it). `make check` green.
- **R1** — `Clone`'s godoc was stranded above `SandboxCloneOptions` (the options struct had been
  inserted between the doc and `func Clone`, leaving the method undocumented and the type-doc opening
  with a method paragraph). Split the block: type-doc stays on the struct, behavior paragraph moved
  down to `func (c *Client) Clone` (`client.go`).
- **R2** — stale pre-D68 field names in doc comments fixed: `SystemCheckOptions.Backend`→`.BackendType`
  and `.Agent`→`.AgentType` (`types.go`), "descriptor fields (`Name`…)"→`Type` (`discovery.go`).
- **R3** — dropped redundant identity casts `BackendType(backend)` where the param was already
  `BackendType` (`system.go`, 2 sites).
- **R4** — added the `Files()` sub-handle to the `Sandbox` sub-handle lists (ABOUTME + struct doc),
  which enumerated only Workdir/Network/Agent (`sandbox.go`).
- **R5** — renamed the stuttering `Agent().AgentLog()` → `Agent().TerminalLog()` (follows the sibling
  convention — `Logs`/`ContainerLogs`/`Prompt` don't repeat the handle noun; reads as the recorded
  counterpart to `CaptureTerminal`). Updated 3 callers (mcpsrv, `log.go`, `bugreport.go`) and the live
  docs (BREAKING-CHANGES, architecture nav). Internal `ReadAgentLog` unchanged (different layer).
- **R6** — documented the `.Type` vs `.BackendType` field-name split as a clarification under D68:
  descriptor/identity struct → `Type`; reference-among-data → `<Kind>Type`.

## IC1–IC16 (2026-06-03 Internal-code round) — maintainability/correctness cleanup of the internal layers

- **Severity:** maintainability/correctness (not contract shape). **Resolved:** 2026-06-04 on
  `layering-refactor`, one commit per item. Found via a whole-file read pass of the root public
  package + `internal/{sandbox,runtime,config,cli}`. `file:line` anchors in the original entries
  were as of `c122151` and have drifted.
- **A recurring lesson (IC12 / IC15 / IC16): apparent duplication kept being benign.** Three separate
  "this looks like dead/dup code" findings turned out to be load-bearing on closer reading — the
  proposed dedup/removal would each have been a regression. Verify role before merging seams.

**Resolved (applied):**
- **IC1** (`d8d29e0`) — deduped `lifecycle` restart/reset prompt-delivery: extracted the shared
  tmux-deliver + prepare-files + `loadContainerConfig` + `requireAgent` helpers (was 4 blocks ×6).
- **IC2** (`b5c44e1`) — folded `ProfileConfig` onto `YoloaiConfig` by embedding; `LoadProfile`
  dispatches common keys through `yoloaiConfigHandlers` + 3 profile-only handlers, deleting the
  mirrored handler set. Leniency (ignore-unknown-keys) and `isGlobalKey` routing preserved. −160 LOC.
- **IC3** (`efe6800`) — split `config.go`'s dotted-path YAML-node CRUD engine into `yamlnode.go`;
  typed model + handlers stay in `config.go`.
- **IC4** (`82f52fc`) — regrouped ~13 runtime optional interfaces + `*For` helpers into
  `runtime_optional.go` under a 3-section taxonomy. **Collapse of the 3 tart path-translators
  declined**: `CopyMountResolver` is also implemented by Seatbelt, so a single `GuestPathTranslator`
  would force no-op stubs — defeating the optional-interface pattern. (Per user: regroup, keep all 3.)
- **IC5** (`7f6da43`) — threaded create-pipeline state through a `resolvedCreateInputs` struct:
  `resolveProfileAndArchetype` 8-tuple → `(*resolvedCreateInputs, error)`; `buildConfigAndEnvironment`
  14→11 params; `buildSandboxStateResult` 18→12.
- **IC6** (`7593315`) — backend descriptors use the typed `runtime.Backend*` consts; registry now
  keys on `descriptor.Type` (dropped the redundant `name` param and the now-impossible mismatch panic).
- **IC7** (`5ee9530`) — documented the option→internal mapping convention at the top of
  `sandbox_options.go` and made code follow it: `toInternal()` when one internal counterpart exists
  (added for `AgentLogsOptions`, `WorkdirExportOptions`); inline only on fan-out/field-spread;
  `materialize` stays for the public→public fold.
- **IC8** (`8f12b71`) — carved `tart.go` base-image provisioning into `build.go` and the VirtioFS
  mount/symlink subsystem into `mounts.go` (pure move).
- **IC9** (`a226b98`) — added `cliutil.WithSandbox`/`WithWorkdir`, folding the
  resolve→Client→Sandbox→Workdir prologue (~22×) into one call; net ~−127 LOC and consistent
  `SandboxErrorHint` on lookup failure.
- **IC10** (`74542bf`) — `cliutil.System()` returns `(*yoloai.System, error)` instead of panicking;
  exposed `Archetypes`/`AgentTypes`/`BackendTypes` as package-level free funcs for the
  non-error-returning help-text callers.
- **IC11** (`26ff9b3`) — `status.InspectSandbox` now calls the shared `detectWorkdirChanges` (the
  inline and helper forms were already behaviorally identical — pure DRY).
- **IC12** (`37cf399`) — original "bypasses the runtime exec seam" premise **abandoned** (the host
  rsync is deliberate: a host→host resync of the bind-mounted `:copy` while the container stays live,
  Tart excluded upstream). Two residuals applied: documented the narrow host-`rsync` dependency in
  `CLAUDE.md`; added a *why* comment at `rsyncDir` (differential `--delete` avoids the wipe-then-copy
  window the restart path has).
- **IC13** (`8b9a44a`) — corrected four stale ABOUTME/doc comments.
- **IC14** (`8b9a44a` + the `Info`→`SandboxInfo` follow-up) — naming nits: `Engine` receiver
  `m`→`e`; `BackendDiskUsage`/`BackendDiagnostic` `.Name`→`.Type` (D68 rule);
  `DeleteProfileResult`→`ProfileDeleteResult`. The top-level inspect/list result type
  `Info`→`SandboxInfo` (disambiguates from `SystemInfo`) was first deferred as "breaking, needs its
  own batch," then revived and applied once the user noted the branch is already a cumulative
  breaking delta vs `main` until merge. On verification the type is **branch-new** (the root API on
  `main` is only `Options`/`RunOptions`/`ApplyOptions`/`NewWithOptions`), so the rename is new-API
  naming, **not** a migration off a shipped name — `BREAKING-CHANGES.md` updated for living-doc
  consistency only, no new migration entry. (See abandoned for the one sub-point not applied.)
- **IC15** (`8b9a44a`) — removed the no-op `resolveProfileFromConfig()`; six `prune.go` signatures
  now take `io.Writer` instead of an anonymous writer interface. (`ResolveProfileChain` reframed as
  live, load-bearing — NOT dead; `NewEngine` panic kept — see abandoned.)
- **IC16** (`37cf399`, spun off IC12) — see abandoned: the two host-git wrappers are deliberately
  distinct, not a dedup target.

**Sub-points not applied** drained to [`critiques-abandoned.md`](critiques-abandoned.md): IC14
single-primitive `Options` wrapping, IC15 `NewEngine` panic, and IC16 host-git unification. (IC14's
`Info`→`SandboxInfo` rename was initially deferred but has since been applied — see the IC14 bullet
above.)

## A2/A3 (2026-06-03 Public-API round) — surface split on backend-liveness; `SystemClient` junk drawer; agent noun homeless

- **Severity:** MAJOR (contract shape). **Resolved:** 2026-06-03 (commits `d636297` C1 → `bb41cbe` C4 on `layering-refactor`).
- **Root cause.** `NewWithOptions` eagerly opened the backend, so a `Client` was always backend-bound;
  the layout-only `SystemClient` carried the backend-free per-sandbox readers. Same noun (sandbox X)
  was split across two handles by an implementation property, and the agent-interaction verbs were
  smeared across both by the same axis. D53's third noun (**agent**) had no handle.
- **Resolution — one lazy `Client`, sub-handles by concern.**
  - **Lazy backend init (C1).** `NewWithOptions` no longer opens a runtime; it stores the resolved
    backend and opens once, mutex-guarded, on the first backend-bound op (`ensure`/`tryEnsure` in
    `yoloai.go`). `Options.Backend` is now optional — a backend-less `Client` serves every
    backend-free op without connecting; a backend-bound op on one returns the typed sentinel
    `ErrBackendRequired` instead of the old panic-footgun. `Close()` is a no-op when nothing opened.
  - **Agent noun gets a home (C2).** New `Sandbox(name).Agent()` sub-handle (pure, no IO, mirrors
    `Workdir()`) homes `Prompt`, `AgentLog`, `Logs`, `SendInput`, `Attach`, `CaptureTerminal`,
    `ContainerLogs` — `Prompt`/`AgentLog`/`Logs` relocated off `SystemClient`.
  - **Per-sandbox readers consolidate (C3).** `Metadata` (was `SandboxMetadata(name)`),
    `VscodeAttach`, `Unlock`, `Files`, and the path getters become flat `Sandbox` methods.
    `RequireSandbox` dropped — `Client.Sandbox(name)` already validates existence
    (`ErrSandboxNotFound`).
  - **`SystemClient` collapses (C4).** Renamed to `System`, reached only via `Client.System()`;
    `NewSystemClient` + `SystemOptions` removed. It now holds honest host/fleet/admin only
    (`DiskUsage`/`Doctor`/`Build`/`Check`/`Prune`/`EmptyTrash`/`Config`/`Profiles`/discovery/migration).
- **CLI.** `cliutil.Client(cmd)` builds a backend-less `Client` for reads; `cliutil.System()` returns
  its admin sub-handle; `cliutil.WithClient(cmd, backend, fn)` stays for container-driving commands.
  Backend now opens lazily exactly when the first backend-bound CLI op runs, not at process start.
- **Tracked in** [`BREAKING-CHANGES.md`](../../BREAKING-CHANGES.md) ("`SystemClient` collapsed into
  `Client.System()`") and decision **D67**.

## A1 (2026-06-03 Public-API round) — mirror-vs-alias decided by implementation convenience

- **Severity:** MINOR. **Resolved:** 2026-06-03 (no code; principle).
- **Resolution.** Settled by the "alias by default, mirror on demand" principle in
  [`development-principles.md`](../principles/development-principles.md) §4: flat embedder-held
  structs are consciously aliased (the F1 detector guards leaks; mirroring would be duplication),
  and a hand-written mirror is introduced only when a field must be dropped/renamed or a nested
  internal type would otherwise become un-nameable. The prior inconsistency was accidental, not a
  contract decision; the principle makes the rule explicit rather than per-type effort-driven.

## G6 (2026-05-30 critique) — First-run setup UX leaked into the library contract

- **Severity:** MINOR. **Resolved:** 2026-06-03.
- **Resolution — full collapse, not demotion.** The entire setup vocabulary left the public
  contract: `SystemClient.Setup`, `SystemClient.SetupStatus`, and the types `SetupOptions`,
  `SetupStatus`, `SetupChoice`, `TmuxConfigClass` (+ `TmuxConfigNone/Small/Large`) are gone, and
  `internal/sandbox/setup.go` (`Engine.SetupStatus`/`Engine.ApplySetup` and helpers) was deleted.
  The decisive fact: the two things `Setup`/`ApplySetup` actually did were already covered
  elsewhere — config-writing duplicates `Config().Set`, and defaults materialization
  (`defaults/tmux.conf`, `defaults/config.yaml`) is done unconditionally by
  `Engine.ensureDefaultsDir` via `EnsureSetup`. What remained — tmux-config classification,
  available-backend/agent enumeration, auto-pick, prompt copy — is **CLI onboarding policy**
  (development-principles §2: policy owns what/why), so it moved wholesale into
  `internal/cli/system/setup.go`. The CLI wizard now inspects the host itself
  (`classifyTmuxConfig` reads `cliutil.Layout().HomeDir`, `tmuxres.Embedded()` for the `[p]`
  preview), enumerates choices through the existing public discovery verbs
  (`SystemClient.Backends`/`Agents`), and writes the three answers via `Config().Set`
  (`tmux_conf` / `container_backend` / `agent`).
- **Left the library orthogonal.** The contract is now discovery (`Agents`/`Backends`) + config
  (`Config().Get/Set/Reset`) + just-works defaults (`EnsureSetup`) — no setup-wizard verb.
- **W10 (no hardcoded backend names in dispatch).** Rather than hardcode the containerd exclusion
  in the CLI, a catalog fact was added: `BackendDescriptor.IsolationTargetOnly` (set `true` on
  containerd — reached only via `--isolation vm`/`vm-enhanced`), surfaced on public `BackendInfo`
  alongside the now-also-surfaced `Architectures`. The wizard filters on those facts
  (`Platforms ∋ GOOS && (Architectures empty || ∋ GOARCH) && !IsolationTargetOnly`).
- **Accepted behavior change.** `defaults/tmux.conf` now materializes lazily at first sandbox op
  (via `EnsureSetup`) rather than at `yoloai system setup` time — more aligned with the
  declarative-defaults stance ([[feedback_library_sets_defaults_app_owns_setup_state]]).
- This closes the last Layer-1 public-contract finding from the round.

## G5 (2026-05-30 critique) — The agent-interaction surface was bound to caller stdio, not contracted for an embedder

- **Severity:** MAJOR. **Resolved:** 2026-06-03.
- **The finding split into three surfaces; each is now resolved:**
  - **PTY bridge (conversation) — already decoupled (no code change).** The critique's
    central claim ("a daemon can't hand the engine an `io.Writer`; it needs a PTY bridged over a
    socket") was **stale**. `IOStreams` (aliased at `yoloai.go` as `runtime.IOStreams`) already
    carries `In io.Reader`/`Out io.Writer`/`Err io.Writer`/`TTY`/`Rows,Cols`/`Term`/`Resize
    <-chan TermSize` — the library never touches the process stdin FD or `$TERM`. `Sandbox.Attach`
    is tmux-backed (persistent, multi-client re-attach), so a daemon wires `IOStreams` to a
    websocket→xterm.js exactly as the CLI wires it to the local terminal. The prompt path's `"-"`
    stdin sentinel is likewise already injectable via `Options.Input`. So the PTY half was a faithful
    embedder contract before this round; the finding's premise no longer held.
  - **Activity stream (observation) — carved into the library (this round).** The transport that
    lived in `internal/cli/sandboxcmd/log.go` (open the four JSONL sources, backlog read,
    merge-sort, follow tail-poll, terminal done-detection via `agent-status.json`) moved down to
    `internal/sandbox/logstream.go` (`StreamLogs`) and is exposed as the public verb
    **`SystemClient.Logs(ctx, name, LogOptions) (<-chan LogEvent, error)`** — a subscribable,
    time-ordered stream, sibling to `AgentLog`. `LogEvent` carries the **verbatim** JSONL line
    (`Raw`) plus the two projections the transport must parse to order/filter (`Time`, `Level`);
    the library does **not** reshape the payload — a consumer that wants a richer view parses `Raw`
    itself (the user's "raw until it has to change" principle). The CLI keeps only rendering
    (`parseLogRecord`/`formatRecord`/`levelCode`/`terminalWidth`/`stripANSI`) and `--since`
    local-tz input parsing. No speculative daemon-side converter helpers (YAGNI).
  - **Structured file exchange — already done (G7).** `Sandbox.Files()` + the mcpsrv file-exchange
    carve gave this surface a public home; tracked under G7's verb series, not re-done here.
- **Decision shape (per the user).** Two complementary surfaces (PTY bridge + activity stream) plus
  the file-exchange sub-handle — *not* a single terminal-flavored seam, and *not* an event-union
  reshape of the log payload. Prompt injection stays internal (the one-time `RunOptions.Prompt`;
  re-delivery is internal lifecycle), so there is no `SendPrompt` verb.

## G3 (2026-05-30 critique) — Public Options field-naming inconsistent on a soon-to-be-versioned contract

- **Severity:** MINOR. **Resolved:** 2026-06-03.
- **Resolution.** Audited the public `*Options` structs against the `feedback_dangerous_option_naming`
  rule (name the *consequence*, like `AbandonUnappliedWork`). The only generic safety-override misnomer
  was `CreateOptions.Force` → renamed `AbandonUnappliedWork` (mirrors `DestroyOptions.AbandonUnappliedWork`;
  the CLI `--force` flag maps to it). All other toggles (`Replace`, `Overwrite`, `AllowDirtyWorkdir`,
  `RestartContainer`, and the benign behavioral flags) already comply.
- **Propagation (per user feedback).** Naming clarity must not stop at the public boundary — the rename
  was carried inward through `internal/sandbox/create.Options.Force` → `AbandonUnappliedWork` and its
  call sites, so the "force" misnomer is gone from the guts too. Tracked in BREAKING-CHANGES.md.

## G4 (2026-05-30 critique) — Scattered surface inconsistencies (enum homes, dual-residence error, nil-vs-empty slices)

- **Severity:** MINOR. **Resolved:** 2026-06-03 (three sub-items).
- **G4a — error re-export.** Added a dedicated root `errors.go` aliasing every catchable `yoerrors`
  type plus `ExitCoder` into `yoloai.*`, so embedders match errors with `errors.As`/`Is` against
  `yoloai.*` alone and never import `yoerrors`. The canonical defs stay in `yoerrors` (internal packages
  construct them without an import cycle); `DirtyWorkdirError`/`DirtyDir` aliases moved out of `names.go`.
- **G4b — nil-vs-empty slices.** Normalized the public List/slice surfaces to return `[]T{}` (not `nil`)
  on the empty-*success* path, for JSON `[]` rather than `null`: `tartVersionsToPublic`, `listTartBases`,
  `Workdir.Tags`, `SystemClient.Doctor`, and the `collectExchangeGlobs`/`parseBaselineLog` helpers.
  Error paths still return `nil`; the internal `tartVersionsToInternal` (feeds the backend, not a JSON
  surface) stays `nil`-for-`nil`.
- **G4c — dead clone Overwrite field.** `CloneOptions.Overwrite` was inert. Wired it into `Client.Clone`,
  which now destroys a pre-existing destination (resolving that destination's *own* recorded backend,
  which may differ from the source's) before the disk-only copy. Removed the dead internal
  `sandbox.CloneOptions.Force`; the CLI dropped its hand-rolled `forceDestroyIfExists` and passes
  `Overwrite: force`.
- **Enum homes (assessed, no change).** Shared enums in `names.go` vs handle-local `DomainSource`/
  `ApplyMode` beside their handles was judged already-principled; left as-is.

## G8 (2026-05-30 critique) — `store.Meta` is a vague name; comments reference a phantom `meta.json`

- **Severity:** MINOR. **Resolved:** 2026-06-01 (two passes).
- **G8(b) — public name (done earlier, D55/G1(b)).** When `store.Meta` was carved to the
  public read-model it was named `yoloai.Environment` (artifact-aligned to `environment.json`,
  paired with `State`), not `Meta` — exactly the recommendation.
- **G8(a) + internal rename (2026-06-01).** Closed the type/file/comment trio end-to-end:
  - **Type/file:** `store.Meta`/`WorkdirMeta`/`DirMeta` → `store.Environment`/`WorkdirEnvironment`/
    `DirEnvironment`; `LoadMeta`/`SaveMeta` → `LoadEnvironment`/`SaveEnvironment`; source file
    `store/meta.go` → `store/environment.go`. Internal `state.SandboxState.Meta` field also renamed
    to `Environment` for consistency.
  - **Phantom comments:** every `meta.json` comment (lifecycle, launch, create, patch, status,
    inspect, cliutil, tests) replaced with `environment.json` — the file is never named `meta.json`
    anywhere in code (the `EnvironmentFile = "environment.json"` constant is the only filename).
  - **Public field (breaking):** the public `yoloai.Info.Meta` field → `Info.Environment`, JSON tag
    `"meta"` → `"environment"`. `sandbox info`/`list` `--json` now nest settings under `"environment"`.
    Tracked in BREAKING-CHANGES.md (layer-1 reshape section).
- **Scope note.** Local variable identifiers named `meta` (and helper func names `buildMeta`/
  `buildConfigAndMeta`) were left as-is — the critique targeted the type, file, and public field, not
  internal plumbing var names. `make check` green; F1 leak detector still empty/honest.

## G2 (2026-05-30 critique) — depguard fenced only the façade, not the leaf it leaks through

- **Severity:** MAJOR. **Resolved:** 2026-06-01 (D57, `.golangci.yml`).
- **Resolution.** The `cli-sandbox-facade-scope` rule's three leaf allow-entries
  (`internal/sandbox/{store,patch,archetype}`) were dropped; the `deny` on
  `internal/sandbox` now matches the whole subtree by prefix. Rule renamed
  `cli-sandbox-facade-scope` → `cli-sandbox-scope`. Twin runtime fence
  `cli-runtime-scope` (G7) added separately, denying `internal/runtime` to
  cli+mcpsrv (only `internal/cli/system/tart/` exempt). Net: non-test
  `internal/cli/**`/`internal/mcpsrv/**` may now reach sandbox/runtime behavior
  *only* through the public `yoloai` spine — the CLI is finally a faithful proxy
  for a separate-module daemon.
- **Divergence from the critique's prescribed sequence (per §13/D54).** The critique
  said to "sequence after G1(b)" — i.e. first promote `store.Meta` to a public
  read-model, *then* drop the allow-entry. We took a different path that reached the
  same end: the **G7 verb series** gave every CLI/mcpsrv leaf reach-in a public verb
  (e.g. `printCreateSummary` now takes a public `SandboxMetadata`, not `*store.Meta`;
  metadata/log/discovery/prompt reads route through `SystemClient`/`Client`/`Sandbox`).
  So the CLI stopped importing the leaves entirely **without** requiring a single
  field-for-field `store.Meta` mirror first. Verified: zero non-test leaf imports in
  cli+mcpsrv; a 3-import probe yields 3 `cli-sandbox-scope` denials; `make check` green.
- **Still open (tracked elsewhere, not G2).** The deeper read-model reshape the
  critique's G1(b) gestured at — collapsing `store.Meta` into the three-noun public
  view (identity/posture + embedded resolved-config echo, dropping pile-3 mechanism) —
  remains future work under D53; it is a *public-surface shape* question, distinct from
  this *import-fence* finding. `internal/config` still has no analogous CLI fence (7
  cli files import it); that is genuine future work, not part of G2.

## G1 (2026-05-30 critique) — the F1 leak detector was blind to type aliases; "F1 closed" was false

- **Severity:** CRITICAL. **Resolved:** 2026-05-31 (D55), drained 2026-06-01.
- **The lie.** `TestPublicAPI_NoInternalLeaks` treated every alias as terminal — `walkObj`
  blanket-returned on `IsAlias()` and `walkType` had no `*types.Alias` arm — so `type X =
  internal.Y` silenced the detector for the entire field tree beneath `X`. `f1KnownLeaks`
  empty meant "no leaks *not hidden behind an alias*", not "no leaks". The concrete hidden
  leak: `yoloai.Info` (`type Info = sandbox.Info` → `status.Info`) carried `Meta *store.Meta`,
  an internal iceberg (`WorkdirMeta`/`[]DirMeta`/`DirMode`/`IsolationMode`/`AgentName`/
  `*ResourceLimits`), returned from the four most central entry points (Run/List/Inspect/
  ListAcrossBackends). Bigger than the `MergedConfig` leak D52 had just closed.
- **G1(a) — detector made honest (truth first, committed `3e29ac4`).** `walkType` gained a
  `*types.Alias` case that unwraps via `types.Unalias` and recurses; `walkObj` now descends
  into an aliased named type's exported fields instead of bailing. The existing `aliased`
  de-dup still prevents flagging the sanctioned alias *target*; what surfaces is fields that
  reference *other* un-aliased internal types. Landing this turned the test red on
  `store.Meta` — that red was the truth, produced *before* any cleanup.
- **G1(b)-1 — store.Meta → public `Environment` (committed `d5f8787`).** `Info` de-aliased
  from `sandbox.Info` to a hand-written struct; `Meta` retyped to `*yoloai.Environment`
  (environment.go), a field-for-field mirror with byte-identical JSON; converters wired at
  Inspect/Run/List/pollUntilDone/ListAcrossBackends. `store.Meta` stayed internal.
- **G1(b)-2 — caps doctor tree → public `BackendReport` (committed `d335b10`).** Removed
  `type BackendReport = caps.BackendReport`; hand-written mirrors (Availability/BackendReport/
  CapabilityCheck/Capability/FixStep) + converter in doctor_report.go; `Capability` drops the
  func fields. JSON + human output byte-unchanged.
- **Outcome (committed `f2c6ba6`).** `f1KnownLeaks` is now empty **and** honest — the detector
  can no longer be fooled by an alias. The decision from the critique's step 2 (promote vs.
  park `store.Meta`) was **promote**, not a documented deferral.
- **Known residual limit (not a lie).** The *named-type* case still reports-but-doesn't-descend:
  a named type is reported as a unit, and its fields are walked only at its own declaration. A
  brand-new internal type hung off an already-public named struct would not be auto-flagged.
  This is a structural bound of the walker, documented here so it stays eyes-open — distinct
  from the alias blind spot, which was the actual defect and is fixed.
- **Still open (tracked elsewhere, not G1).** The `store.Meta`→`Environment` carve is the
  *field-for-field mirror*, not D53's three-noun reshape (expose identity/posture, embed a
  resolved-config view, drop pile-3 mechanism) — that read-model refinement remains future
  work. The G7 missing-verb series and G2 depguard fence (both since resolved) were the
  consumption-side twin of this finding.

_(no older entries)_

# Critique — 2026-05-27 Greenfield Hindsight Pass

## Summary

The architecture is in mostly good shape after W-L1..W-L14: the runtime-backend abstraction is clean, the typed-error surface is consistent, the W-L10 forbidigo allowlist actually enforces what the principles claim, and the recent Q-Y typed-name work (BackendName/AgentName/MountSpec/PortMapping) gives the public API real teeth. The places I would redo with hindsight cluster around two seams that aren't yet finished: (a) the `internal/sandbox` package is still the god-package — `Manager` has 75 methods, `create.go` is 2019 lines / 68 functions, and a parallel `DirArg`/`DirSpec` shape exposes that the "DirMode is typed" decision didn't reach the persisted `WorkdirMeta.Mode` (still `string`); and (b) the "no ambient HOME" win from Q-W is undermined in practice by 37 call sites that reconstruct `homeDir := filepath.Dir(layout.DataDir)` — embedders with non-conventional DataDir get a wrong home. The Python boundary is well-typed for the pure-helper surface, but the imperative `sandbox-setup.py` body (1290 lines) is still entirely outside the test net. Finally, the public `yoloai.Client` surface leaks internal types (`sandbox.CreateOptions`, `patch.ApplyResult`, `sandbox.StartOptions`) — those types live under `internal/` so embedders can't actually import them, which means today the only "embedder" of `yoloai` that compiles is `internal/cli`. The package is being kept honest by `internal/` enforcement, but the typed-name work begun in W-L8b hasn't finished the job: the Client's primary surface is still a thin façade over `sandbox.Manager`.

## Findings

### Public API surface

#### F1 — `yoloai.Client.Create`, `Apply`, `Start`, `Reset`, `Clone` leak internal-package types in their public signatures

- **Severity:** HIGH
- **Where:** `yoloai.go:291,306,350,359,452,503,511` (Apply, ApplyWithOptions, Clone, Create, OverlayPatch, Start, Reset)
- **Observation:** Client.Create takes `sandbox.CreateOptions` — a type defined in `internal/sandbox`. Client.Apply returns `*patch.ApplyResult` — defined in `internal/sandbox/patch`. Client.Clone takes `sandbox.CloneOptions`; Start takes `sandbox.StartOptions`; Reset takes `sandbox.ResetOptions`. The `IOStreams` field is exposed as `type IOStreams = runtime.IOStreams` — alias to an `internal/` type. The package doc claims "external embedders use it as the entry point" (`yoloai.go:4`), but external embedders literally cannot build code that calls `Create` — they can't construct a `sandbox.CreateOptions` because `internal/sandbox` is unreachable. Today the only callers are `internal/cli` (allowed) and `internal/mcpsrv` (also `internal/`).
- **Why it bothers me:** This is the W-L8b/c/d migration done halfway. Q-Y added typed BackendName/AgentName at the root, but the bigger and more important Options structs still live in internal. Either (a) yoloai.Client is for embedders, in which case Options types must live at the root package, or (b) it's internal-only, in which case the doc is wrong and the `internal/cli/cliutil.WithClient` shim is purely cosmetic. The `api_surface.go:1`-2760 design clearly intended (a) — every Op is documented with public `<Op>Options` shapes. The implementation didn't catch up.
- **Greenfield alternative:** Define every Options struct at the `yoloai` package root. The internal type can be a different shape if it makes orchestration easier; the public type is the contract. `internal/sandbox` becomes an implementation detail that the public Client adapts to. Same shape as the kubectl `genericiooptions.IOStreams` reference in `api_surface.go:83`.
- **Migration cost:** Multi-week. ~20 Options structs to define at root + adapter funcs. Best done in one go since each migration breaks the CLI's existing call sites.

#### F2 — `yoloai.Client` has 39 methods at the root; the Sandbox sub-handle pattern stopped at 1

- **Severity:** MED
- **Where:** `yoloai.go:198-621` (39 Client methods); `sandbox.go:23` (Sandbox handle); `network.go` (Network sub-handle); `api_surface.go` describes `Workdir()`, `Files()`, `Network()` as the design.
- **Observation:** Q-G/Shape B (api_surface.go:16) explicitly resolved sub-handles as the structural shape: `client.Sandbox(name).Network().Allow(...)`. Today only `Network()` is wired. The Client root still carries `DiffOverlay`, `OverlayPatch`, `UpdateOverlayBaseline`, `ListCommitsOverlay`, `GenerateFormatPatch`, `GenerateFormatPatchForRefs`, `GenerateWIPDiff`, `ListCommits`, `ListCommitsWithStats`, `ResolveCommitRefs`, `AdvanceBaseline`, `HasUncommittedChanges`, `ContainerLogs`, `SandboxDir`, `SendInput`, `StdioExec`, `Exec`, `Attach` — all naturally `Sandbox(name).Workdir().X` / `Sandbox(name).Logs.X` / `Sandbox(name).Exec.X` shaped operations. `CaptureTerminal` did move under the handle (sandbox.go:53) but it's the only one.
- **Why it bothers me:** Walking the Client surface today, you see `c.DiffOverlay(ctx, name, ...)` next to `c.Sandbox(name).Network().Allow(...)`. The two shapes coexist with no rationale visible at the call site. Either pattern is fine; mixing them is the worst outcome — the reader has to remember which operations live where.
- **Greenfield alternative:** Land the remaining sub-handles in one PR. The 2026-05-25 api_surface.go design already laid them out. Drop the name+arg duplicates at the Client root once Sandbox(name) is the canonical entry point for per-sandbox ops.
- **Migration cost:** Week. Mostly mechanical re-rooting of methods; the tests follow.

#### F3 — `Client.Create` takes the raw internal struct; `Run` takes a public `RunOptions` — both exist for the same operation

- **Severity:** MED
- **Where:** `yoloai.go:153-189` (RunOptions, 8 fields), `yoloai.go:224-269` (Run), `yoloai.go:359` (Create taking `sandbox.CreateOptions` with 30 fields).
- **Observation:** Two creation entry points: `Run` is "the convenience" with a curated 8-field public surface; `Create` is the escape hatch that takes the full `sandbox.CreateOptions` (30 fields including `Archetype`, `VscodeTunnel`, `CapAdd`, `Devices`, `Setup`, `Force`, `Yes`, `Passthrough`, `Debug`, ...). The CLI's `yoloai new` always calls `Create` (`internal/cli/lifecycle/new.go:280`) because Run can't carry --archetype, --runtime, --cpus, --memory, --port, -d, --network-isolated, --env, etc.
- **Why it bothers me:** Either there's one public creation function and `RunOptions` should be deprecated, or there's a two-tier surface (basic + advanced) and the boundary needs documenting. Today `Run` is a trap — embedders who use it find out half their flags don't work and have to fall back to `Create`, where the type lives behind an `internal/` boundary they can't reach.
- **Greenfield alternative:** Pick one. The `api_surface.go:497` "RunOptions design" comment notes "subset with sandbox.CreateOptions (the full `yoloai new` flag set)" — but the subset trap is exactly what F1 calls out. Land a public `CreateOptions` at root, make `Run` a sugar wrapper that fills in defaults.
- **Migration cost:** Folds into F1.

#### F4 — `Options.Backend == ""` semantics: silent fallback to config-then-docker hides multi-backend ambiguity

- **Severity:** LOW-MED
- **Where:** `yoloai.go:121-124` (`backend = BackendName(resolveBackendFromConfig(ctx, layout))`), `yoloai.go:645-652` (resolveBackendFromConfig).
- **Observation:** When `opts.Backend == ""`, NewWithOptions silently calls into config + `runtime.SelectContainerBackend` and may end up on any of docker/podman/containerd. The Client then carries that backend for its whole lifetime, but the embedder has no idea which one was picked (no field on Client exposes the resolved backend). If a Run lands on a Podman socket the embedder didn't know existed, the user gets podman behavior with no signal.
- **Why it bothers me:** Library code is supposed to be deterministic given its inputs. The §12 ambient-config principle was applied to HOME; the same logic applies to "which backend will be used for this operation" — the answer shouldn't be "depends on what's installed."
- **Greenfield alternative:** `Backend == ""` should either (a) be a hard error ("required field"), matching Q-W.5's treatment of `DataDir`, or (b) the resolved backend must be readable: `Client.Backend() BackendName`. (a) is the cleaner shape; the CLI does its own resolution at the flag boundary (`cliutil.ResolveBackend`), so it can pass the resolved value explicitly.
- **Migration cost:** Day. The CLI already resolves the backend before calling NewWithOptions in some paths.

### Internal package boundaries / dependency graph

#### F5 — `internal/sandbox` is the god-package; Manager has 75 methods, create.go is 2019 lines

- **Severity:** HIGH
- **Where:** `internal/sandbox/create.go` (2019 lines, 68 functions); `internal/sandbox/lifecycle.go` (1475 lines, 47 functions); `internal/sandbox/manager.go` (`grep ^func` says 75 methods on Manager).
- **Observation:** A single `*sandbox.Manager` holds the runtime, layout, logger, I/O streams, and 75 methods spanning every concern from `EnsureSetup` → `Create` → `Stop` → `applyAgentChoice` → `CaptureTerminal` → `NeedsConfirmation` → `applyVscodeTunnelOption` → `sendResumePrompt`. The `Manager.Create` orchestration alone spans `prepareSandboxState` → `resolveProfileAndArchetype` → `validateAndLoadConfig` → `resolveProfileConfig` → `applyConfigDefaults` → `resolveAndApplyArchetype` → `applyDevcontainerArchetype` → `parseAndValidateDirs` → `createAndSeedSandbox` → `setupAllWorkdirs` → `buildConfigAndMeta` → `resolveAgentParams` → `buildLifecycleConfig` → `buildContainerConfig` → `buildMeta` → `writeStatFiles` → `launchContainer` → `buildAndStart` → `buildMounts` (decomposed into buildWorkdirMounts / buildAuxDirMounts / buildSingleAuxDirMount / buildAgentMounts / buildVscodeMounts / buildHomeSeedMounts / buildSystemMounts / buildGitAndTmuxMounts) → `buildInstanceConfig` → `verifyInstanceRunning`. The decomposition is real but everything is a Manager method even when the function doesn't need a Manager — see `mkdirAllPerm`, `writeFilePerm`, `ensureMachineID`, `applyDirSuffix`, `effectiveUID/GID`, `sanitizeTunnelName`, `shellEscapeForDoubleQuotes` — these are all package-level helpers in `create.go` that have nothing to do with sandbox state.
- **Why it bothers me:** The decomposition into 68 functions was done correctly (each function has a name; the data flow through `sandboxState` is explicit). But all 68 still live in one file because they share the package-private `sandboxState` type and Manager. The next reader has to load the whole file to follow Create. With hindsight, "create" deserves its own package — same as W-L13 did for archetype/patch/store. The CLI subpackage carve (`internal/cli/lifecycle`, `internal/cli/workflow`, `internal/cli/sandboxcmd`) is the model.
- **Greenfield alternative:** Carve `internal/sandbox/create/` (the orchestration, sandboxState, buildContainerConfig, validateAndLoadConfig, etc.), `internal/sandbox/lifecycle/` (Start/Stop/Destroy/Reset and their helpers), `internal/sandbox/mounts/` (the 8 buildXMounts functions — they're a coherent subsystem already), `internal/sandbox/manager/` (the 8-method orchestrator). Manager becomes a thin façade that holds the runtime+layout+logger and dispatches to subpackages.
- **Migration cost:** Multi-week. The shared `sandboxState` would need careful exposure. But it would mirror the work W-L13 already did for CLI, and the seam was already validated by W12.

#### F6 — `internal/sandbox/patch` imports `internal/sandbox` (subpackage importing parent)

- **Severity:** MED
- **Where:** `internal/sandbox/patch/apply.go:18` (`"github.com/kstenerud/yoloai/internal/sandbox"`); used at apply.go:47 (`sandbox.AcquireLock`), apply.go:168/187/192/274/281/290 (`sandbox.ExecInContainer`), diff.go:301/343/360.
- **Observation:** `internal/sandbox/patch/` is a subpackage of `internal/sandbox/`, but in Go that's just a naming convention — the import graph is what matters. The patch package imports its own parent for `AcquireLock` and `ExecInContainer`. ARCHITECTURE.md line 339 notes "leaf subpackage; imports only stdlib, `config`, `internal/fileutil`, `internal/yoerrors`" for `store/` — and `patch/` is explicitly not described as a leaf. But the principle reader expects subpackage → parent imports to be the cleanly cut shape (like `store/`), not the inverted shape (like `patch/`).
- **Why it bothers me:** When `patch/apply.go` reaches back into `sandbox.AcquireLock` and `sandbox.ExecInContainer`, both of those should be moved into a leaf-shaped package that both can import. `internal/locking` already exists for the lock primitive — the per-sandbox lock layer (`sandbox.AcquireLock`) should live there or in `store/`. `ExecInContainer` is a runtime-helper that takes `*store.Meta` to derive ContainerUser — it should be in `internal/runtime/exec` or `store/` itself.
- **Greenfield alternative:** Move `AcquireLock` to `internal/sandbox/store` (it already takes a `config.Layout` and a name; nothing sandbox-specific about it beyond the file path). Move `ExecInContainer` to `internal/runtime/runner.go` or back to `internal/runtime/exec.go`. The patch package then imports `store` and `runtime` cleanly, no parent reference.
- **Migration cost:** Day. Both functions are self-contained.

#### F7 — `internal/cli/profiles/` is an empty directory left over from refactor

- **Severity:** LOW
- **Where:** `internal/cli/profiles/` (empty directory; the actual code is in `internal/cli/profile/`).
- **Observation:** Two directories: `profile/profile.go` exists; `profiles/` (plural) is empty. Likely the result of an aborted rename/move during W-L13.
- **Why it bothers me:** Empty directories in `git ls-tree` are uncommon (git doesn't track them unless there's a `.gitkeep`); this one trips ide scans and obscures the canonical location of profile code. Future contributors will guess which one is real.
- **Greenfield alternative:** `rm -rf internal/cli/profiles/` — or if there's a reason it exists, add a `.gitkeep` with a comment.
- **Migration cost:** Trivial.

#### F8 — Manager construction couples I/O streams (logger, input, output) to the orchestrator

- **Severity:** MED
- **Where:** `internal/sandbox/manager.go:18-27` (Manager struct holding `logger`, `input io.Reader`, `output io.Writer`); `NewManager(rt, logger, input, output, opts...)`.
- **Observation:** Manager holds output writer / input reader as construction-time fields. Every method that prints user-facing text (`Sandbox %s started\n`, "Tip: enable shell completions...", warnings about devcontainer mounts being skipped) writes to `m.output`. This couples library orchestration to a stream-aware presentation layer. The `Q-F` principle (api_surface.go) says "library functions return data, CLI prints" — but `Manager.applyVscodeTunnelOption` does `fmt.Fprintln(m.output, "VS Code tunnel enabled")` and `Manager.handleSuspendedResume` does `fmt.Fprintf(m.output, "Sandbox %s resumed\n", name)`.
- **Why it bothers me:** Embedders who use Q-F's pattern (yoloai.SystemClient.Setup returns nil; CLI prints) can't apply that pattern to Create/Start/Reset because the Manager always writes to its output Writer. An HTTP server tenant constructing the Client gets these strings on whatever Output they passed in (the Client defaults to `io.Discard`, so most of these messages just vanish — meaning the CLI is the only consumer that gets them, and the library has become "CLI handlers stuffed inside Manager").
- **Greenfield alternative:** Manager returns structured results (e.g., `StartResult{Action: ActionRestarted | ActionResumed | ActionWasRunning, Message: ""}`). CLI renders the result. `OnProgress` already exists on RunOptions; extend the pattern.
- **Migration cost:** Multi-week. Lots of small message rewrites + parallel CLI render code.

#### F9 — `sandbox.DirArg` and `sandbox.DirSpec` are parallel types with `DirArgToSpec` glue

- **Severity:** LOW
- **Where:** `internal/sandbox/parse.go:11-44` (DirArg + DirArgToSpec), `internal/sandbox/create.go:103-108` (DirSpec).
- **Observation:** DirArg has fields {Path, MountPath, Mode string, Force bool}. DirSpec has fields {Path, Mode DirMode, MountPath, Force bool}. The only structural difference is `Mode string` vs `Mode DirMode`. ParseDirArg returns DirArg; CreateOptions takes []DirSpec; the CLI uses `sandbox.DirArgToSpec(parsed)` (`internal/cli/lifecycle/new.go:272`) at the boundary to convert.
- **Why it bothers me:** Two structurally identical types is a smell. Both have an `Apply` opportunity if there's a real difference (parsed-but-unvalidated vs validated). But the conversion `DirArgToSpec` does no validation — it just casts `string → DirMode`. Picking one type — preferably DirSpec, since DirMode is the typed contract — would remove the glue.
- **Greenfield alternative:** Make ParseDirArg return DirSpec directly. Delete DirArg and DirArgToSpec.
- **Migration cost:** Day.

### Persisted-state typing

#### F10 — `WorkdirMeta.Mode` and `DirMeta.Mode` are `string` while `DirSpec.Mode` is `DirMode`

- **Severity:** MED
- **Where:** `internal/sandbox/store/meta.go:57-72` (Mode string in WorkdirMeta and DirMeta); `internal/sandbox/create.go:93-99` (DirMode constants).
- **Observation:** The persisted meta type holds `Mode string`. Everywhere it's read, you see `meta.Workdir.Mode == "copy"` / `== "overlay"` / `== "rw"` — 10+ string comparisons (`lifecycle.go:537,547,584,674,703,892`; `create.go:288,311`; `inspect.go:349`; `patch/apply.go:614`). With `exhaustive` linter on (`.golangci.yml:34`), typing this would get free exhaustiveness for switch statements.
- **Why it bothers me:** Q-Y / Parse-don't-validate §4 (development-principles.md:174) listed Mount Mode as one of the canonical surfaces for typing. The runtime/wire format stays compatible (`json:"mode"` of a `DirMode string` serializes identically), but every consumer gets compile-time safety. Today a typo (`if meta.Workdir.Mode == "Copy"`) compiles silently.
- **Greenfield alternative:** Change `Mode string` to `Mode DirMode` in `store.WorkdirMeta` and `store.DirMeta`. Convert the string comparison sites to switches over the typed enum; `exhaustive` will surface anywhere a case is missed.
- **Migration cost:** Day. Mechanical.

#### F11 — `meta.Isolation` is `string`, parsed by `config.ValidateIsolationMode(string) error`

- **Severity:** MED
- **Where:** `internal/sandbox/store/meta.go:50` (`Isolation string`); `internal/config/config.go:94` (`ValidateIsolationMode(mode string) error`); 5+ string comparisons (`internal/runtime/isolation.go:86-115`, `internal/runtime/docker/docker.go:527`, `internal/sandbox/inspect.go:171,197`).
- **Observation:** `api_surface.go:125-133` defines `IsolationMode string` with the five typed constants. The internal layer never adopts it — Isolation flows as `string` from CLI (`--isolation` flag) through validate → Meta → Backend.SupportedIsolationModes (`[]string`) → IsolationContainerRuntime(string)→runtime name. Backend descriptor's `SupportedIsolationModes []string` should be `[]IsolationMode`.
- **Why it bothers me:** Same shape as F10. The typed enum was designed but never reached the implementation. With Go's type-name+string aliases (`type IsolationMode string`), there's no runtime cost.
- **Greenfield alternative:** Introduce `IsolationMode` at `internal/runtime/isolation.go` (parallel to `BackendName` in `internal/runtime`). Cascade it through `BackendDescriptor.SupportedIsolationModes`, `IsolationContainerRuntime(IsolationMode)`, `meta.Isolation`. Public re-export at `yoloai.IsolationMode` (already designed in api_surface.go).
- **Migration cost:** Day.

#### F12 — Persisted file names migrated but the package-level constants doc the legacy names

- **Severity:** LOW
- **Where:** `internal/sandbox/store/paths.go:30-44` (EnvironmentFile/SandboxStateFile/RuntimeConfigFile/AgentStatusFile each followed by "(was ...)" comments).
- **Observation:** Every persisted-file constant carries a "(was X.json)" comment documenting the rename. Looking at git history, the old names were removed during the public beta. D16 (working-notes.md:319) said "Remove all legacy backwards-compat shims" — the comments survive but the legacy compat code does not. So the comments now read like archaeology, not API contract.
- **Why it bothers me:** Comments aren't free — every reader has to parse "(was ...)" and decide whether the old name matters. With public-beta breaking changes documented in `docs/BREAKING-CHANGES.md`, the inline annotations are duplicate documentation. The development principles §8 ("No half-finished implementations") implicitly covers this — remove the legacy reference once the legacy is gone.
- **Greenfield alternative:** Drop the parenthetical "(was X)" from each constant. The breaking-change history is in BREAKING-CHANGES.md, not in the const docstring.
- **Migration cost:** Trivial.

### The `filepath.Dir(layout.DataDir)` anti-pattern (Q-W escape hatch)

#### F13 — 37 sites compute "user home" as `filepath.Dir(layout.DataDir)`, breaking the §12 contract

- **Severity:** HIGH
- **Where:** 37 occurrences. Selected: `internal/sandbox/create_prepare.go:78,87,280,302,718,886`; `internal/sandbox/lifecycle.go:256,329`; `internal/cli/lifecycle/new.go:252`; `internal/cli/workflow/apply.go:80`; `internal/config/pathutil.go:18,63`. The pattern is documented as the official escape hatch in `pathutil.go:18`: "callers derive it from `filepath.Dir(layout.DataDir)`."
- **Observation:** The §12 win (api_surface.go:2497-2522, principles/development-principles.md §12) was supposed to break the library's dependence on ambient HOME. The Client takes an explicit `DataDir`. But every place that needs to expand `~/something` (config paths, prompt-file paths, devcontainer paths, archetype loading, agent_files) reconstructs "the user's HOME" by calling `filepath.Dir(layout.DataDir)`. This assumes `layout.DataDir == "$HOME/.yoloai"`. If an embedder passes `DataDir = "/var/lib/yoloai"`, then `filepath.Dir(...) == "/var/lib"` — which is then used as `homeDir` for `~/.gitconfig` expansion in mounts (`create_prepare.go:280`), seed-file expansion (`lifecycle.go:256` via copySeedFiles), and config-mount resolution. Tilde-paths suddenly resolve to `/var/something`. This silently produces wrong behavior with no error.
- **Why it bothers me:** This is the exact "Works on my machine because $HOME is sensible; broken for daemon at $HOME=/root/" failure mode §12 was supposed to eliminate. The text of the principle in `development-principles.md:444` is "library code never reads ambient process state" — but in practice, library code reads ambient state *through Layout*. Q-W.5's victory was prevented from being real by the lack of a `HomeDir` field on `Layout`.
- **Greenfield alternative:** Add `HomeDir string` to `config.Layout`. CLI startup sets it from `os.UserHomeDir()` (the licensed call). Embedders set it explicitly — and can set it independently of DataDir for daemon scenarios. The 37 call sites become `m.layout.HomeDir` (or `layout.HomeDir`) instead of `filepath.Dir(m.layout.DataDir)`. Add a forbidigo rule banning `filepath.Dir(.*\.DataDir)`.
- **Migration cost:** Day. Mechanical search/replace + a Layout field + a CLI startup line + an embedder doc update + a linter rule.

#### F14 — Embedder can construct a Layout with empty DataDir; NewManager panics, NewWithOptions errors — inconsistency

- **Severity:** LOW
- **Where:** `internal/config/layout.go:30-36` (no validation on `NewLayout`); `internal/sandbox/manager.go:75-82` (NewManager panics if Layout.DataDir == ""); `yoloai.go:115-117` (NewWithOptions returns error if DataDir == "").
- **Observation:** Two equivalent failure modes get opposite treatment. Same invariant ("DataDir is required") is enforced as `panic` deep in the library and as a returned error at the public boundary.
- **Why it bothers me:** Either it's a programming bug (panic) or a runtime input error (error). It can't be both. With Q-X (api_surface.go drops `UnrecoverableNotImplemented` because the CLI handles programming bugs via panic+recover), the project's stance is "panic is for programming bugs." That makes `manager.go:81` correct in spirit and `yoloai.go:116` wrong — but the public boundary's `*UsageError` shape protects callers, which is also a valid stance. Pick one.
- **Greenfield alternative:** Make `config.NewLayout` itself reject empty DataDir (`func NewLayout(dataDir string) Layout` → `func NewLayout(dataDir string) (Layout, error)`). Then the panic in Manager becomes a self-evident invariant: "Layout exists ⇒ DataDir is non-empty."
- **Migration cost:** Day.

### Error handling

#### F15 — `internal/runtime/errs.go:31,49` uses `strings.Contains(err.Error(), "permission denied")` despite W8

- **Severity:** LOW (documented as irreducible at a chokepoint — calibrating)
- **Where:** `internal/runtime/errs.go:24-50`.
- **Observation:** W8 of the architecture remediation (working-notes.md:367) "moved typed errors to internal/yoerrors — single source for error categorisation, no more `errors.Is(err, fmt.Errorf("not running"))` text-match anti-patterns." But `IsPermissionDenied` and `IsAddressInUse` still text-match. The package doc comment justifies it: "ABOUTME: Text-match fallbacks are documented as irreducible." The reasoning is sound (Docker SDK and containerd shim errors don't wrap the syscall error in a form errors.Is can detect).
- **Why it bothers me:** Calibration finding only — this looks like a violation of W8 but is actually the principled exception. The function comments explain why; the package ABOUTME documents it; the only complaint I'd voice is the package name (`errs` is opaque vs `runtimerr` or just leaving them on `runtime`).
- **Greenfield alternative:** No change needed; possibly inline these into `runtime/runtime.go` to remove the separate file. The 2 funcs don't justify a sibling file.
- **Migration cost:** Trivial; structural-only change to merge files.

#### F16 — `yoerrors` has 9 distinct typed errors mapped to 9 distinct exit codes; the mapping table lives in `internal/cli/root.go`

- **Severity:** LOW
- **Where:** `internal/yoerrors/errors.go` defines UsageError/ConfigError/ActiveWorkError/DependencyError/PlatformError/AuthError/PermissionError/SandboxLockedError/DiskSpaceError/ResourceLimitError; `internal/cli/root.go:152-205` maps each to exit codes 2/3/4/5/6/7/8/9/10/11.
- **Observation:** The exit-code-to-typed-error mapping is a 50-line `if/else if` cascade in `errorExitCode`. Each `errors.AsType[*sandbox.XxxError](err)` repeats the same shape. The decision "should this error be exit code N" is split between two files — the type lives in yoerrors, the code lives in cli/root.
- **Why it bothers me:** The `errors.AsType` generic helper (Go 1.26 stdlib) is a nice tightening over the old `var x *Foo; errors.As(err, &x)`. But the cascade reads as "type taxonomy in two places." A `type ExitCoder interface { ExitCode() int }` on the error itself would let `errorExitCode` shrink to ~5 lines.
- **Greenfield alternative:** Add `ExitCode() int` to each yoerrors type. `errorExitCode` becomes `if e, ok := errors.AsType[ExitCoder](err); ok { return e.ExitCode() }; if IsDiskSpaceError(err) { return 10 }; return 1`. The exit-code constant lives next to the error type.
- **Migration cost:** Day. The exit-code values + the interface declaration; everything else is method addition.

#### F17 — `ErrSandboxNotFound` / `ErrContainerNotRunning` / `ErrMissingAPIKey` are sentinel errors re-exported through three layers; none at the public root

- **Severity:** LOW
- **Where:** `internal/sandbox/store/paths.go:27` (`var ErrSandboxNotFound = errors.New("sandbox not found")`); `internal/sandbox/errors.go:16` (`ErrSandboxNotFound = store.ErrSandboxNotFound`); no `yoloai.ErrSandboxNotFound` re-export.
- **Observation:** The sentinel is defined in `store`, re-exported in `sandbox`, and missing entirely from the `yoloai` root. The api_surface.go design (1503) names it as a public-API sentinel. Embedders that want to `errors.Is(err, yoloai.ErrSandboxNotFound)` find no such symbol; they'd have to either reach into the internal package (which Go won't let them) or string-match.
- **Why it bothers me:** F1 covers the bigger issue; this is the tail end. If the public Client surface is going to grow, sentinels need their own public homes.
- **Greenfield alternative:** Re-export sentinels at the `yoloai` root the same way Q-Y re-exported the name types. `yoloai.ErrSandboxNotFound`, `yoloai.ErrContainerNotRunning`, `yoloai.ErrMissingAPIKey`.
- **Migration cost:** Trivial.

### Runtime / backend abstraction

#### F18 — `runtime.Runtime` has 18 methods + 8 optional interfaces; some "core" methods are not universally meaningful

- **Severity:** MED
- **Where:** `internal/runtime/runtime.go:232-327` (Runtime interface); optional: `UsernsProvider`, `WorkDirSetup`, `CopyMountResolver`, `AppleSimulatorRuntimes`, `IsolationCapabilityProvider`, `StdioExecer`, `CachePruner`, `DiskUsageReporter` (8 optional, not the 5 cited in the task brief).
- **Observation:** Several of the 18 "core" methods are conditionally useful. `Logs(ctx, name, tail) string` returns "" for Tart (VM doesn't have docker-style logs). `DiagHint(name) string` is backend-specific text. `PrepareAgentCommand(cmd) string` is a tail-end wrapper for backend-specific PATH overrides (often empty). `TmuxSocket(sandboxDir) string` returns "" for backends that use the uid-default. `AttachCommand(...) []string` produces a backend-specific exec command. `GitExec` exists because tart needs path translation that docker doesn't. The interface is "everything every backend might need" rather than "the minimum every backend must provide" — which is the kubectl/Cobra-style mistake of growing the interface until it can't shrink.
- **Why it bothers me:** When a backend's implementation is "return empty / ignore / passthrough," that's a signal the method belongs on an optional interface. ARCHITECTURE.md line 401 already documents this pattern. The bar for "core" should be: every backend must implement non-trivially. Today `Logs`, `DiagHint`, `TmuxSocket`, `PrepareAgentCommand` have at least one non-trivial implementation but the trivial ones outnumber them.
- **Greenfield alternative:** Move `Logs`, `DiagHint`, `TmuxSocket`, `PrepareAgentCommand` to optional interfaces (`LogTailer`, `DiagHinter`, `TmuxSocketResolver`, `AgentCommandPreparer`). Trivial backends drop them entirely; callers use the existing `ResolveCopyMountFor` / `PruneCacheFor` pattern. Possibly move `GitExec` too — the only consumer is patch/, and the tart-specific path translation is the optional-interface case.
- **Migration cost:** Week. Mechanical, but touches every backend.

#### F19 — Tart-on-Apple-Silicon is hard-coded in `sandbox/setup.go:153`, not declared by the descriptor

- **Severity:** LOW-MED
- **Where:** `internal/sandbox/setup.go:142-159` (`if desc.Name == "tart" && hostArch != "arm64" { continue }`).
- **Observation:** The setup wizard's "available backends" enumeration filters by `desc.Platforms` (GOOS-granular) and then a hard-coded backend-name + arch check. Comment line 139-141 acknowledges this: "The Apple Silicon constraint for tart is applied here rather than via Platforms (which is GOOS-granular)." This is exactly the backend-name leak the W10 audit (working-notes.md:159) closed — except this one slipped through.
- **Why it bothers me:** Tart's descriptor knows it's Apple-Silicon-only. The descriptor today says `Platforms: []string{"darwin"}` — but darwin includes amd64 Intel Macs. The descriptor should be `Platforms: []PlatformConstraint{{OS: "darwin", Arch: "arm64"}}` or carry a separate `Architectures []string` field. Then `availableBackends` filters by descriptor only; no backend-name knowledge in the caller.
- **Greenfield alternative:** Add `Architectures []string` to `BackendDescriptor` (`internal/runtime/runtime.go:113`). Tart declares `["arm64"]`; others omit (any). `availableBackends` becomes a pure descriptor walk.
- **Migration cost:** Day.

#### F20 — `IsolationContainerRuntime`, `IsolationSnapshotter`, `IsolationEnforcesInSandboxIptables`, `SupportsOverlayDirs` are 4 string-switches in `runtime/isolation.go`

- **Severity:** LOW
- **Where:** `internal/runtime/isolation.go:10-66`.
- **Observation:** Four functions, each `switch isolation { case "...": ... }`. The isolation mode is the discriminator of a closed sum: container / container-enhanced / container-privileged / vm / vm-enhanced. With F11's typed `IsolationMode` and `exhaustive` linter (`.golangci.yml:34` already enabled with `default-signifies-exhaustive`), these would be free-checked. Today a new isolation mode could be added in one place but missed in the other three.
- **Why it bothers me:** Same calibration as F10 — the typed enum exists in api_surface.go but never reached the implementation.
- **Greenfield alternative:** Folds into F11. After IsolationMode is typed, each function takes IsolationMode; switches become exhaustive.
- **Migration cost:** Folds into F11.

### CLI / library seam

#### F21 — `cliutil.ResolveBackend` reaches into `runtime.SelectContainerBackend` and into config; same logic exists in `yoloai.resolveBackendFromConfig`

- **Severity:** MED
- **Where:** `internal/cli/cliutil/client.go:60-100` (ResolveBackend in CLI); `yoloai.go:645-652` (resolveBackendFromConfig in library).
- **Observation:** The CLI does isolation/OS-based routing + config preference + auto-detection. The library does config preference + auto-detection. They overlap on the config+auto-detect path but the CLI also implements isolation/OS routing. The CLI calls NewWithOptions with its resolved backend; if it didn't, the library would re-do part of the resolution. The result: an embedder that wants the same isolation/OS routing as the CLI has to re-implement it.
- **Why it bothers me:** Two separate "backend resolution" implementations — the CLI's is richer (it knows about --isolation and --os flags), the library's is the bare-bones default. If a library consumer wants to pass `--isolation vm`-like preferences, they have to know to call into `runtime.SelectContainerBackend` themselves.
- **Greenfield alternative:** Move the full isolation/OS routing into `runtime.SelectContainerBackend(ctx, preferred, isolation, os)`. The CLI calls it with all three values; the library defaults `isolation` and `os` to empty. The Client could expose `Options.Isolation` and `Options.OS` as preferences.
- **Migration cost:** Day.

#### F22 — `Client.Sandbox(name)` does no validation; the api_surface.go design called for strict validation

- **Severity:** LOW
- **Where:** `sandbox.go:23-25` (1-line constructor, no error path); `api_surface.go:13-15` (strict-validation design: "errors with ErrSandboxNotFound for missing names").
- **Observation:** The Q-G "Shape B" resolution explicitly chose strict validation over lazy validation: "Rejected because GCS's motivation — defer a network round-trip — doesn't apply locally, and the lazy pattern lands errors downstream of where the name was typed. Strict resource validation fits 'parse, don't validate' §4 better." (api_surface.go:25-29). Today's implementation is lazy (`func (c *Client) Sandbox(name string) *Sandbox { return &Sandbox{c: c, name: name} }`) — exactly the GCS pattern the design rejected.
- **Why it bothers me:** The design doc and the implementation disagree. Either the design needs updating, or the implementation does. Today, `Client.Sandbox("does-not-exist").Network().Allow(ctx, "...")` returns the error from `requireIsolated` deep down (network.go:227-244), not from `Sandbox(name)` where the user typed it.
- **Greenfield alternative:** Either (a) change `Sandbox(name) (*Sandbox, error)` to do `store.RequireSandboxDir + LoadMeta` upfront, matching api_surface.go; or (b) update api_surface.go to acknowledge the lazy-validation choice with the rationale. (a) matches §4 parse-don't-validate better.
- **Migration cost:** Day.

#### F23 — `yoloai ls`, `system doctor`, `system info`, `sandbox <name> allow` all call into `internal/sandbox` and `cliutil.NewRuntime` directly, bypassing the Client

- **Severity:** MED
- **Where:** `internal/cli/sandboxcmd/list.go:131` (`sandbox.ListSandboxesMultiBackend(ctx, cliutil.Layout(), cliutil.NewRuntime)`); ARCHITECTURE.md:39 documents the four exceptions; `.golangci.yml:108-111` allowlists the chokepoint.
- **Observation:** `yoloai ls` walks every backend that has sandboxes — Docker container in one, containerd VM in another, Tart VM in a third. The current shape: CLI gets a function pointer `cliutil.NewRuntime` and passes it through to `sandbox.ListSandboxesMultiBackend`, which iterates backends and calls the function per backend. This pattern (`func(ctx, BackendName) (Runtime, error)` parameter) leaks runtime construction into the CLI, plus the four allowlist exceptions documented at ARCHITECTURE.md:39.
- **Why it bothers me:** The cross-backend enumeration is real; the question is whether `yoloai.Client` or `yoloai.SystemClient` should expose it. Today SystemClient knows how to iterate descriptors for DiskUsage/Prune/Build, but List/Doctor/Info/Allow are CLI-only paths. With hindsight, `SystemClient.ListSandboxesAllBackends(ctx) ([]Info, []BackendName, error)` would close the gap; the CLI wouldn't need the function-pointer dance.
- **Greenfield alternative:** Add `SystemClient.ListAcrossBackends` (and analogues for doctor/info/allow). Drop the four allowlist exceptions. The function-pointer parameter on `ListSandboxesMultiBackend` becomes internal SystemClient implementation detail.
- **Migration cost:** Week. Each cross-backend operation needs to move from CLI helper to SystemClient method.

### Python boundary

#### F24 — `sandbox-setup.py` (1290 lines) has no test coverage; only the pure helpers are tested

- **Severity:** MED
- **Where:** `internal/runtime/monitor/sandbox-setup.py` (1290 lines); `internal/runtime/monitor/setup_helpers.py` (141 lines, tested in `tests/test_setup_helpers.py`).
- **Observation:** The W3 extraction (working-notes.md:294-296) carved pure helpers out of `sandbox-setup.py` into `setup_helpers.py` for mypy + pytest. That covers 141 lines. The remaining 1149 lines of `sandbox-setup.py` are imperative — they call tmux, subprocess, write secrets, set environment variables, start the agent. No tests run against this surface. The surface area is huge (Docker / Seatbelt / Tart all dispatch through it) and the failure modes are real (DF2/DF3/DF4/DF6/DF8 all involved subtle bugs in this path).
- **Why it bothers me:** The testability work for the pure surface was the easy half. The hard half is the imperative core. Per testing-principles §3 ("test at the right layer"), the right layer for "did sandbox-setup.py correctly assemble the tmux preamble and launch the agent" is integration tests against a real container — which exist (`sandbox/integration_test.go`), but they pass/fail on the whole flow, not on intermediate state.
- **Greenfield alternative:** Continue the W3 pattern. Extract more pure helpers as candidates: the secret-load + env-mutation + tmux-paste sequence; the credential-overrides-vs-host-env priority logic; the on_create vs on_start dispatch; the agent-command shell composition. Each carve makes the imperative remainder smaller. Eventually `sandbox-setup.py main()` should be a 50-line orchestration over 90% pure-helper code.
- **Migration cost:** Multi-week. Incremental; each carve is its own PR.

#### F25 — Python `runtime-config.json` schema version bumping is documented but the failure mode on mismatch is brittle

- **Severity:** LOW
- **Where:** `internal/runtime/monitor/setup_helpers.py:25-46` (`RUNTIME_CONFIG_SCHEMA_VERSION = 1`; mismatch raises RuntimeError); `internal/sandbox/create.go:202` (Go constant `runtimeConfigSchemaVersion = 1`); `internal/sandbox/inspect.go:289-294` (Go reader: schema mismatch → log + treat-as-stale).
- **Observation:** The Go writer and Python reader both carry a hardcoded `1`. Doc says "bumped together by W2." There's no test that catches "I changed the Go constant but forgot the Python constant" — the cost shows up at runtime when a freshly-created sandbox can't read its own config.
- **Why it bothers me:** The agent-status.json schema version has the same issue. Two-language hardcoded constants are a class of bug; a single source of truth would close it.
- **Greenfield alternative:** A `//go:generate` step that writes a small Python file with `RUNTIME_CONFIG_SCHEMA_VERSION = N`, generated from the Go constant. Or a `make check` step that greps both files and verifies the integer matches.
- **Migration cost:** Day.

### Tests / quality

#### F26 — Integration tests on every backend × every flow → CI matrix risk

- **Severity:** LOW
- **Where:** `internal/sandbox/integration_test.go` (927 lines), `internal/sandbox/integration_tart_test.go` (392 lines), `internal/runtime/docker/integration_test.go`, `internal/runtime/seatbelt/integration_test.go`, `internal/runtime/tart/integration_test.go`, `internal/runtime/containerd/integration_test.go`.
- **Observation:** Each backend has its own integration test suite plus the sandbox suite runs against multiple backends. Each suite calls `EnsureSetup` once in `integration_main_test.go` (good — image build is amortized). The integration suite is gated by build tags and requires the backend's daemon running.
- **Why it bothers me:** Calibration finding only. The shape is right (real backends; behavioural tests; the W5 backend-parametrized suite is well-designed). The risk to monitor: as backends are added, suite × backend grows quadratically. The W-L10 layering test should be in this category — it's not yet present at `internal/cli/layering_test.go` (the plan said it would be); only forbidigo/depguard cover the layering today. That's fine but worth recording so the next reader knows it's by design.
- **Greenfield alternative:** Keep the current shape. Possibly add a structural test that asserts the runtime.Runtime interface is implemented identically across all registered backends (no method behavior divergence beyond what's documented).
- **Migration cost:** N/A.

### Config / state

#### F27 — Config layout mixes YAML (config.yaml, state.yaml) and JSON (environment.json, runtime-config.json, agent-status.json, sandbox-state.json)

- **Severity:** LOW
- **Where:** `~/.yoloai/config.yaml`, `~/.yoloai/state.yaml`, `~/.yoloai/defaults/config.yaml`, `~/.yoloai/profiles/<name>/config.yaml`; per-sandbox: `environment.json`, `runtime-config.json`, `agent-status.json`, `sandbox-state.json`.
- **Observation:** YAML for "what the user edits" (config files), JSON for "what the program writes" (state/runtime/agent files). The boundary is consistent and the rationale (YAML for human editing with comment preservation via `yaml.Node`; JSON for fast machine-to-machine, including Python interop) is sound.
- **Why it bothers me:** Calibration finding. This is one of the good shapes — the split matches the audience. Worth recording so future contributors don't try to "unify" it.
- **Greenfield alternative:** None — keep the split.
- **Migration cost:** N/A.

#### F28 — `~/.yoloai/state.yaml` carries one field (`setup_complete`); it's a separate file because of conceptual layering

- **Severity:** LOW
- **Where:** `internal/config/state.go` (`LoadState`/`SaveState`); the file holds `setup_complete: true/false` only.
- **Observation:** A single-field YAML file. D13 (working-notes.md:259) distinguishes "global config" (`config.yaml` — user preferences like tmux_conf, model_aliases) from "operational state" (`state.yaml` — what the program has done, like setup_complete). The split is principled.
- **Why it bothers me:** Today there's only one operational-state field. The split pays its rent if more operational state is coming (and it likely is — e.g., last-used backend, last-seen yoloai version for migration prompts). Worth keeping but worth recording that today the file feels lightweight.
- **Greenfield alternative:** No change. The split is right; just observing.
- **Migration cost:** N/A.

### Naming

#### F29 — `IsolationPerms` / `SecurityPerms` — two names for the same type

- **Severity:** LOW
- **Where:** `internal/sandbox/inspect.go:182-191`.
- **Observation:** `type IsolationPerms struct { ... }` followed by `type SecurityPerms = IsolationPerms`. Comment: "SecurityPerms is an alias for IsolationPerms for backwards compatibility within the sandbox package."
- **Why it bothers me:** D16 (working-notes.md:319) said "Remove all legacy backwards-compat shims" in public beta. This is a one-file alias in an internal package — not blocked by the principle, but redundant. There's no public surface depending on `SecurityPerms`.
- **Greenfield alternative:** Delete `type SecurityPerms = IsolationPerms`; rename any in-package callers.
- **Migration cost:** Trivial.

### Calibration: things that look weird but have written-down reasons

These are checks I made to verify the design — they're not findings, they're notes that "I looked at this and the existing rationale holds."

- **`runtime/errs.go` text-matching error strings (F15 above)** — `IsPermissionDenied` / `IsAddressInUse` are documented as the irreducible-at-chokepoint exception. W8's principle still holds; this is the principled exception.
- **`yoloai.Client.Apply` returning `(nil, nil)` for no-op** — Q-P (api_surface.go) explicitly dropped the `ErrNoChanges` sentinel in favor of "branch on result == nil." Worked example for §1 §Robustness Principle.
- **`runtime.Runtime.AppleSimulatorRuntimes` as an optional interface** — D7 + the W11 step-3 split. The tart-specific code lives behind a type assertion in `sandbox/create.go:548`; the principle that backend-specific types don't leak past the runtime/ boundary holds.
- **`exec.Command(ctx, ...)` NOT used in Tart's Run path** — backend-idiosyncrasies.md "Tart: tart run needs exec.Command" entry documents this. Looks like a bug, isn't.
- **The `--isolation container-enhanced` rejection on macOS in `IsolationAvailability`** — D17 + backend-idiosyncrasies.md "gVisor on macOS". Principle §5 §Fail fast.
- **The `panic` in `sandbox.NewManager` when Layout is missing** — Q-W.5 + §12 invariant: the call site should pass WithLayout; missing it is a programming bug, panic is correct.
- **The `runtime-config.json` schema_version field** — W2's design. Calibration partial: F25 above adds the "single source of truth" finding, but the schema-version field itself is right.
- **The `errors.AsType` generic helper used throughout root.go** — Go 1.26 stdlib (go.mod:3 pins `go 1.26.1`). This is the idiomatic post-generics replacement for the `var x *Foo; errors.As(err, &x)` dance and reads cleanly.

### Misc

#### F30 — `Client.Run`'s `pollUntilDone` polls at 5-second intervals; no configurability, no exponential backoff

- **Severity:** LOW
- **Where:** `yoloai.go:198-222`.
- **Observation:** Hardcoded `case <-time.After(5 * time.Second)`. A long-running agent emits "progress" callbacks every 5s; a fast-finishing one waits a full 5s before observing completion.
- **Why it bothers me:** Embedders who want different polling cadence have no hook. Worth flagging because `RunOptions.Wait` is a documented feature.
- **Greenfield alternative:** `RunOptions.PollInterval time.Duration` with default 5s. Or watch `agent-status.json` via fsnotify for instant detection.
- **Migration cost:** Day for the simple parameter; week for fsnotify.

#### F31 — `effectiveUID()` / `effectiveGID()` read `SUDO_UID` / `SUDO_GID` env vars in library code, not at the CLI boundary

- **Severity:** LOW
- **Where:** `internal/sandbox/create.go:1073-1090` (effectiveUID/GID); `internal/sandbox/create.go:1311` (sudoParentEnv).
- **Observation:** The `sudoParentEnv` + `effectiveUID/GID` pattern reads ambient env vars to recover the "real user" when yoloai is invoked via sudo. Documented as one of the §12 exceptions ("CLI legitimately reads several env vars at startup: SUDO_USER / SUDO_UID / SUDO_GID for chown-on-sudo," development-principles.md:445). But it's done in library code (`internal/sandbox/create.go`), not CLI startup.
- **Why it bothers me:** Same shape as F13 — §12's allowed exception is documented at the CLI layer, but the actual reads happen in `internal/sandbox`. An embedder who never runs under sudo gets bogus values when sudo env vars happen to be set in the parent process — exactly the "Hyrum's law" scenario §12's principle warns about.
- **Greenfield alternative:** Move the SUDO_* reads to CLI startup. Pack into `config.Layout` (or a sibling `RuntimeIdentity` struct). Pass into Manager construction.
- **Migration cost:** Day.

---

## Recommended ordering (top 5)

If only five findings can land, in priority order:

1. **F13** — Add `HomeDir` to `config.Layout`; ban `filepath.Dir(layout.DataDir)`. This is the biggest live correctness landmine: an embedder with a non-conventional DataDir gets wrong tilde-expansion at 37 sites. Low effort, high payoff.

2. **F1** — Promote `CreateOptions`, `StartOptions`, `ResetOptions`, `CloneOptions`, `ApplyResult`, `PatchSet`, etc. to the public `yoloai` package root. The Client's public-API claim is empty until this lands; embedders today literally can't compile against `Client.Create`. Aligns the implementation with `api_surface.go`'s intent.

3. **F5** — Carve `internal/sandbox` into `create/`, `lifecycle/`, `mounts/`, `manager/` subpackages. The package has the same shape `internal/cli` had pre-W-L13 — it's the next obvious carve target. Multi-week but mechanical.

4. **F10 + F11** — Type `WorkdirMeta.Mode`/`DirMeta.Mode` as `DirMode`, type `meta.Isolation` as `IsolationMode`. The `exhaustive` linter is on; one day of work buys compile-time safety everywhere these strings are compared (10+ sites for Mode, 5+ for Isolation). Direct application of §4 "parse, don't validate."

5. **F2** — Finish the sub-handle pattern (`Sandbox(name).Workdir()`, `Sandbox(name).Logs`, `Sandbox(name).Exec`). Today the Client surface mixes the two shapes; the design was clear, the implementation stopped at 1 of N. One week; mostly mechanical re-rooting of the existing methods.

Findings 6-31 don't form ordered dependencies; pick by your taste for cleanup. F24 (Python imperative core untested) is the biggest "no progress without sustained work" item, but it's incremental — every helper carve makes the bug class smaller without a single big lift.

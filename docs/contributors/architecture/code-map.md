> **ABOUTME:** Package-by-package map of the implemented codebase — the mapped packages' purpose,
> key public types, and which file a given CLI command dispatches into. The where-does-this-live
> companion to overview.md's how-it-fits and data-flows.md's runtime call chains.

# Code Map

The source of truth for **where the code lives** — every package and file, the key public types, and which CLI command dispatches to which code. For the conceptual view of how the layers fit, see [overview.md](overview.md); for runtime call chains, see [data-flows.md](data-flows.md).

## Package Map

```
client.go                → Orchestration spine: Client (CreateSandbox, ListSandboxes, EnsureSetup)
backend.go               → Package-level backend selection (SelectBackend, IsolationAvailability)
sandbox.go               → Sandbox handle: lifecycle + flat readers (Inspect, Unlock, VscodeAttach, paths) + its option/read-model types
system.go                → Orchestration spine: System (DiskUsage, Prune, Build, Check)
runtime_imports_linux.go → Linux-specific backend registration (containerd)
yoerrors/                → Public typed error sentinels (top-level pkg; re-exported via the yoloai package)
cmd/yoloai/              → Binary entry point
internal/agent/          → Agent plugin definitions (Aider, Claude, Codex, Gemini, OpenCode, test, idle)
internal/cli/            → Cobra command tree and CLI plumbing
internal/cli/extension/  → User-defined custom commands (YAML-based extensions, the 'x' command) — CLI-private
internal/config/         → Configuration loading, profiles, migration, state, path utilities
internal/fileutil/       → os.MkdirAll / os.WriteFile wrappers for sudo ownership fix
internal/locking/        → Per-sandbox advisory locks (Q-T)
internal/mcpsrv/         → MCP server exposing sandbox operations as tools for outer agents
runtime/        → Pluggable runtime interface, backend registry, isolation mapping, exec helpers
runtime/caps/   → Capability detection system (host probing, fix instructions, doctor output)
runtime/docker/      → Docker implementation of runtime.Backend
runtime/podman/      → Podman implementation (embeds Docker runtime, overrides socket discovery and rootless support)
runtime/tart/        → Tart (macOS VM) implementation of runtime.Backend
runtime/seatbelt/    → Seatbelt (macOS sandbox-exec) implementation of runtime.Backend
runtime/containerd/  → Containerd implementation of runtime.Backend (Kata Containers VM isolation)
runtime/apple/       → Apple `container` CLI implementation of runtime.Backend (per-container Linux VMs, macOS 26+)
runtime/monitor/     → Embedded monitoring scripts shared across all backends (sandbox-setup.py, status-monitor.py, diagnose-idle.sh)
runtime/ptybridge/   → Shared local-PTY exec bridge used by the process/VM backends (apple, tart, seatbelt)
runtime/runtimetest/ → Backend-agnostic conformance suite (build tag `integration`) every backend runs against
internal/broker/     → Credential-injector host + reverse proxy (D105/D106): swaps an agent's placeholder credential for the real one
internal/credential/ → CredentialSource/Apply/CredentialBinding — the resolve+inject primitives internal/broker composes
internal/netpolicy/  → Network-allowlist composition and enforcement-strategy capability checks (ip-filter vs egress-proxy)
internal/netpolicycfg/ → Per-sandbox netpolicy.json persistence (D90) — kept out of store.Environment
internal/sysexec/    → The single licensed subprocess site (DEV §12): every exec.Command in yoloai routes through here with an explicit env
internal/orchestrator/             → Façade (package orchestrator): Engine deps-holder + alias re-exports; clone, parse, setup, terminal/attach
internal/orchestrator/create/      → Leaf: sandbox-creation orchestration (Run = prepare → seed → build) + context files
internal/orchestrator/lifecycle/   → Leaf: Start/Stop/Destroy/Reset/NeedsConfirmation free functions + restart/relaunch + Notice types
internal/orchestrator/status/      → Leaf: sandbox read-model — DetectStatus, InspectSandbox, ListSandboxes, work-data probing
internal/orchestrator/launch/      → Leaf: shared launch primitives (instance build/start, Teardown, vm-workdir, CheckIsolationPrerequisites)
internal/orchestrator/mounts/      → Leaf: mount-spec construction from DirSpec/Meta
internal/orchestrator/invocation/  → Leaf: agent invocation/command assembly
internal/envsetup/       → Layer (D91): stages agent-specific sandbox contents host-side — secret-dir, seed files, settings, agent-files, keychain credential sourcing (the substrate's dual). Was internal/orchestrator/provision/.
internal/orchestrator/profiles/    → Leaf: profile image building (dependency order, staleness)
internal/orchestrator/runtimeconfig/ → Leaf: ContainerConfig assembly for the runtime layer
internal/orchestrator/archetype/   → Project archetype detection (devcontainer, compose, apple, simple) + .yoloai.yaml + VS Code workspace injection
copyflow/       → Git-format diff/apply machinery for :copy, :overlay, and :rw modes
internal/orchestrator/state/       → Leaf: shared value types (DirSpec, State, Deps, IsolationPerms/Perms) every F5 leaf imports
store/       → On-disk sandbox state: paths, Meta record, SandboxState completion flags
internal/testutil/            → Shared test helpers (git, fixtures, home isolation, container polling) — test use only
internal/workspace/           → Workspace utilities (copy, git, safety checks, tags)
test/e2e/                → End-to-end tests against the compiled binary (build tag: e2e)
```

Public Go surface is the **`yoloai` package only** (W-L12). Every other Go package lives under `internal/` and is unreachable from external imports by the Go compiler itself. `cmd/yoloai` is the binary entry, not a library.

Dependency direction (W-L8 + W-L12 shape): `cmd/yoloai` → `internal/cli` → `yoloai` (Client + System) → `internal/orchestrator` + `copyflow` + `store` + `runtime`; `internal/orchestrator` → `internal/orchestrator/archetype` + `store` + `runtime` + `internal/agent` + `internal/workspace`; `copyflow` → `store` + `runtime` + `internal/config` + `internal/fileutil` + `internal/git`; `store` and `internal/orchestrator/state` are leaves (`state` holds the shared `DirSpec`/`State`/`Deps`/`Perms` value types so the F5 subpackages depend on it without importing the `orchestrator` façade). Post-F5 the `orchestrator` package is a thin **façade**: it re-exports leaf types/functions via `type X = leaf.X` / `var X = leaf.X` aliases and holds the `Engine` deps-holder, while orchestration lives in the leaves. The F5 DAG is `state ← {mounts, invocation, provision, profiles, runtimeconfig} ← launch ← {create, lifecycle}` (create/ and lifecycle/ are siblings — neither imports the other; their one-time shared check `CheckIsolationPrerequisites` lives in `launch/`) `← orchestrator` (façade). Methods were **dissolved** into free functions taking `state.Deps`, not left as thin delegators; `yoloai.Client`/`Sandbox` call e.g. `lifecycle.Stop(ctx, deps, name)` and `create.Run(ctx, deps, ...)` directly. `internal/agent` stands alone; `internal/mcpsrv` depends on `yoloai` (not `orchestrator.Engine`). The CLI reaches into neither the `internal/orchestrator` **façade** package nor any of its leaf subpackages (`archetype`/`status`/…), nor the lifted-out substrate packages `store` and `copyflow`, nor `runtime/*` — every command goes through `yoloai.Client`, `yoloai.System`, and the `yoloai.*` re-exports. (G7 gave every former leaf reach-in a public verb — sandbox-metadata reads, agent-log/file paths, agent/model/backend discovery, stored-prompt get/set, the git-tag-on-apply — so the leaves are no longer consumer surfaces.) The `withRuntime`/`withManager` helpers were removed in W-L10. Cross-backend enumeration (`ls`, `doctor`, `system info`) goes through `System.AllSandboxes` / `Doctor` / `Info` (F23); backend availability probing goes through `yoloai.System.CheckBackend` (on `discovery.go`); `cliutil` has no `NewRuntime`. Depguard (`.golangci.yml`) enforces the boundary going forward with two twin rules over non-test `internal/cli/**` and `internal/mcpsrv/**`: `cli-sandbox-scope` denies `internal/orchestrator` (façade + nested leaves) plus the two lifted substrate packages `store` and `copyflow` by explicit deny entries (F1 Half-B + G2) — and `cli-runtime-scope` denies `runtime` with no exemptions; `internal/cli/system/tart/` speaks the public `yoloai` surface (W-L13/G7).

## File Index

### `client.go` / `backend.go` / `sandbox.go` / `system.go` / `runtime_imports_linux.go`

| File | Purpose |
|------|---------|
| `client.go` | Orchestration spine — `Client` and its root methods (`ListSandboxes`, `CreateSandbox`, `EnsureSetup`). Since D74 the `Client` is a thin factory: `NewClient` validates options and builds the single eager `*orchestrator.Engine` (which owns the lazy backend connection); the per-sandbox handles route backend-bound work through that Engine. `CreateSandbox` provisions a dormant `*Sandbox` handle (no launch); cloning + overwrite-teardown live on `Sandbox.Clone` / `Engine.DestroyForOverwrite`. Registers Docker, Podman, Seatbelt, and Tart backends via blank imports. |
| `client_options.go` | `ClientCreateOptions` — the construction-time config `NewClient` takes (data/home dirs, optional `BackendType`, IO, env snapshot, principal). |
| `sandbox_options.go` | The public sandbox option types: `SandboxCreateOptions` (the surface `Client.CreateSandbox` takes), plus `toInternal` mapping and port formatting. |
| `system_config.go` | `ConfigAdmin` sub-handle (`Client.System().Config()`): `Effective`/`Get`/`Set`/`Reset` over the config files. |
| `types.go` | Public type surface: re-exports of internal enums (`BackendType`, `AgentType`, `PruneItemKind`, `LogSource`), spec types (`DirSpec`, `MountSpec`, `PortMapping`), and orchestration result types (`Notice`, `DestroyResult`, `StartResult`, `ResetResult`). |
| `backend.go` | Package-level backend-selection functions (`SelectBackend`, `SelectContainerBackend`, `IsolationAvailability`). Backend has no handle — its catalog metadata lives in `discovery.go` and its reports in `doctor_report.go`. |
| `sandbox.go` | The `Sandbox` handle (returned by `Client.Sandbox(name)`) — lifecycle (`Start`/`Stop`/`Restart`/`Reset`/`Destroy`/`Inspect`/`Exec`/`HasActiveWork`) and flat readers (`Metadata`, `Unlock`, `VscodeAttach`, the runtime-free path getters) plus its option/read-model types (`Info`/`Status`/`AgentStatus`, `SandboxStart`/`SandboxReset`/`SandboxDestroy`/`SandboxExecOptions`). Sub-handle accessors (`Agent()`/`Workdir()`/`Network()`/`Files()`) live here, colocated with their `Sandbox` receiver per Go convention (a method belongs in its receiver's file, not its return type's); the sub-handle *types* and their own methods live in their respective files (`agent.go`/`workdir.go`/`network.go`/`files.go`). |
| `system.go` | Orchestration spine — `System` for admin/cross-backend operations (`DiskUsage`, `Prune`, `Build`, `Check`). Reached only via `Client.System()` (no standalone constructor). Iterates registered backends internally. |
| `runtime_imports_linux.go` | Linux-only blank import of `runtime/containerd` to register the containerd backend. |

### `cmd/yoloai/`

| File | Purpose |
|------|---------|
| `main.go` | Entry point. Sets up signal context, calls `cli.Execute`, exits with code. Build-time version/commit/date vars. |

### `agent/`

| File | Purpose |
|------|---------|
| `agent.go` | `Definition` struct and built-in agent registry (`aider`, `claude`, `codex`, `gemini`, `opencode`, `test`, `idle`). `GetAgent()` lookup. |
| `agent_test.go` | Unit tests for agent definitions. |

### `internal/cli/`

After W-L13, `internal/cli/` is a slim Cobra entry point. The bulk
of command code lives in scoped subpackages so the root contains
only orchestration, error handling, and subcommand registration.

#### Root (`internal/cli/`)

| File | Purpose |
|------|---------|
| `root.go` | `Execute()` entry point, `NewRootCmd()` builder (exported so subpackage tests can construct the full CLI tree for integration checks), global flags (`-v`, `-q`, `--json`, `--bugreport`, etc.), error→exit-code mapping, bug-report file open/close orchestration. |
| `commands.go` | `registerCommands()` — sets up the four help groups and wires each subpackage's exported `NewCmd` constructor onto the root. The only file that imports every command subpackage. |
| `runtime_imports_linux.go` | Linux-only blank import of `runtime/containerd` so the backend self-registers on Linux builds. |
| `integration_test.go`, `integration_main_test.go` | Cross-subpackage CLI integration tests that drive Cobra end to end. |

#### Foundation (`internal/cli/cliutil/`)

Helpers shared by every command subpackage. Importing cliutil is
allowed from anywhere under internal/cli/; nothing in the cli tree
should import the root cli package back (the few tests that need
`cli.NewRootCmd` use the external `_test` package convention).

| File | Purpose |
|------|---------|
| `client.go` | `NewRuntime`, `WithClient`, `Client` (backend-less), `System`, `AttachToSandboxByName`, `ResolveBackend`/`ResolveBackendForSandbox`, `ResolveAgent`, `ResolveModel`, `ResolveProfile`, `Coalesce`, `FlagStr`, `SandboxErrorHint`. The chokepoint that turns CLI flags into a `yoloai.Client` (use `cliutil.Client(cmd)` for backend-less reads, `cliutil.System()` for the admin sub-handle). |
| `layout.go` | `Layout()` / `SetRootLayout` / `LayoutForDataDir` — points the library `config.Layout` at `$HOME/.yoloai/library` (or `DIR/library` under `--data-dir`) and threads it downward. The only sanctioned `os.UserHomeDir` call site (allowlisted in `.golangci.yml`). |
| `clipaths.go` | `TopDir()`, `CLIDir()`, `CLIExtensionsDir()`, `CLIStatePath()`, `CLISchemaVersionPath()` + the `library`/`cli` namespace constants — the CLI-side `TOP/cli` paths that sit beside the library namespace (D60). |
| `clischema.go` | CLI realm versioning: `CLIStatus()` (read-only realm check via `config.RealmStatus`), `CreateFreshCLI()` (fresh-init + stamp), and `MigrateCLI()` — the mutation-only, one-shot flat→namespaced relocation invoked **only** by `yoloai system migrate`. Errors on an unrecognized `TOP` rather than mangling it. See D60/D61. |
| `clistate.go` | `CLIState` (`first_run_tip_shown`), `LoadCLIState()`/`SaveCLIState()`, `MaybeShowFirstRunTip()` — CLI app state under `TOP/cli/state.yaml` (replaces the library's removed `setup_complete`). |
| `name.go` | `ResolveName` and `EnvSandboxName` — sandbox-name resolution from args / `YOLOAI_SANDBOX`. |
| `json.go` | `--json` flag helpers: `JSONEnabled`, `WriteJSON`, `WriteJSONError`, `EffectiveYes`. |
| `streams.go`, `terminal.go` | `IOStreams()` (PTY-sized terminal binding for Client.Attach) and `SetTerminalTitle` (OSC-0 + tmux window rename). |
| `lowdisk.go` | `WarnIfLowDisk`, `HumanBytes` — free-space courtesy check used by new/clone/build/disk. |
| `confirm.go` | `Confirm()` — context-aware y/N prompt with stdin/context racing. Moved here from `internal/orchestrator` (B3); prompting is CLI-tier, not domain. |
| `format.go` | `FormatAge`, `FormatSize`, `FormatDiskUsage` — human-readable age/size rendering for CLI display. Domain returns structured data (`Info.DiskUsageBytes`); the CLI renders it. |
| `groups.go` | Exported help group IDs (`GroupLifecycle`, `GroupWorkflow`, `GroupSandboxTools`, `GroupAdmin`) — referenced by every subpackage that registers a top-level command. |
| `buildinfo.go` | `SetBuildInfo` + `Version`/`Commit`/`Date` globals — set once in `Execute()` so subpackages (bug-report, version) can read build metadata without threading it through cobra calls. |
| `check.go` | `CheckBackend` — best-effort backend-availability probe used by `ls`, `doctor`, `system tart` gating. |
| `logger.go` | Multi-sink slog logger. Fans records to stderr, `cli.jsonl`, and the bug-report temp file. |

#### Lifecycle (`internal/cli/lifecycle/`)

`new`, `clone`, `start`, `stop`, `restart`, `destroy`, `reset` — all
sandbox lifecycle commands. Self-contained: no cross-subpackage
helpers needed; each constructor is exported (`NewNewCmd`,
`NewCloneCmd`, `NewStartCmd`, `NewStopCmd`, `NewRestartCmd`,
`NewDestroyCmd`, `NewResetCmd`).

#### Workflow (`internal/cli/workflow/`)

`attach`, `diff`, `apply` (with `apply_export`, `apply_format_patch`,
`apply_overlay`, `apply_selective`, `apply_squash` backends),
`baseline`, `files`. The apply family shares package-private
helpers (`applyResult`, `buildTagsByCommit`, `hasOverlayDirs`,
`requireOverlayRunning`, `looksLikeRef`) — that's why they belong
in one subpackage rather than spread across several.

#### Sandbox tools (`internal/cli/sandboxcmd/`)

The `yoloai sandbox …` parent + every subcommand, plus the
top-level shortcuts that delegate to it (`yoloai ls`, `yoloai log`,
`yoloai exec`, `yoloai vscode`).

| File | Purpose |
|------|---------|
| `sandbox.go` | `yoloai sandbox` parent with name-first dispatch. |
| `aliases.go` | Top-level shortcut commands (`ls`, `log`, `exec`, `vscode`) that delegate to the corresponding sandbox subcommand impl. |
| `list.go`, `log.go`, `exec.go` | The actual `sandbox list`/`log`/`exec` implementations. `log.go` is rendering-only: it consumes the `yoloai.System.Logs` activity stream (transport lives in `internal/orchestrator/logstream.go`) and pretty-prints the verbatim JSONL frames. |
| `info.go`, `prompt.go`, `vscode.go`, `unlock.go`, `bugreport.go` | Other per-sandbox subcommands. `bugreport.go` exports `WriteSandboxSectionsForFlag` so `root.go`'s `--bugreport` finalizer can include sandbox sections. |
| `allow.go`, `allowed.go`, `deny.go`, `network.go` | Network allowlist commands and their shared helpers (`loadIsolatedMeta`, `saveNetworkAllowlist`, `tryLivePatchNetwork`). |
| `ansi.go` | `stripANSI` — used by `log.go` and `bugreport.go` for readable terminal output. |

#### Admin (`internal/cli/system/`)

`yoloai system …` parent and every subcommand. Largest cluster
after sandboxcmd.

| File | Purpose |
|------|---------|
| `system.go` | Parent + `build` + `setup` wiring. |
| `build`/`prune`/`check`/`disk`/`info`/`setup`/`completion` and `backends_agents.go` | Each system subcommand. |
| `tart/` | Nested subpackage — the one sanctioned importer of `runtime/tart` (depguard `cli-backend-scope` rule). |

#### Single-command subpackages

| Subpackage | Command | Notes |
|------------|---------|-------|
| `mcp/` | `yoloai mcp serve|proxy` | MCP server + proxy. |
| `doctorcmd/` | `yoloai doctor` | Capability report + read-only repair advisory (reclaimable-now / reclaimable-space / unreviewed-work / trash). Promoted from `system doctor`. |
| `profile/` | `yoloai profile create/list/info/delete` | Profile management. |
| `configcmd/` | `yoloai config get/set/reset` | Suffixed to avoid collision with `internal/config`. |
| `xcmd/` | `yoloai x` | Extension runner (loads user YAML, builds Cobra commands dynamically). |
| `helpcmd/` | `yoloai help [topic]` | Topic-based help with embedded markdown (`help/*.md`) and Levenshtein suggestions. |
| `versioncmd/` | `yoloai version` | Build-time version display. |
| `bugreport/` | (no command) | Bug-report writer library — `WriteHeader`, `WriteSystem`, `WriteBackends`, `WriteConfig`, `WriteLiveLog`, `WriteExit`, `SanitizeJSONLBytes`. Used by `root.go`'s `--bugreport` orchestration and by `sandboxcmd/bugreport.go`. |

### `internal/fileutil/`

| File | Purpose |
|------|---------|
| `fileutil.go` | `MkdirAll()`, `WriteFile()`, `OpenFile()` — wrappers that fix file ownership via `os.Lchown(SUDO_UID, SUDO_GID)` when yoloai is invoked via sudo. |

### `internal/mcpsrv/`

MCP server exposing sandbox operations as tools for outer agents driving two-layer agentic workflows.

| File | Purpose |
|------|---------|
| `server.go` | `Server` struct backed by `orchestrator.Engine`. `New()` creates the MCP server with registered tool handlers. |
| `tools.go` | MCP tool definitions: sandbox lifecycle, observation, refinement, and file exchange tools. |
| `proxy.go` | MCP proxy — forwards MCP protocol between outer agent and inner MCP server running inside a sandbox. |

### `internal/sysexec/`

The single licensed subprocess site named by `development-principles.md` §12: every `exec.Command`/`exec.CommandContext` in yoloai routes through here, never called directly elsewhere (forbidigo bans the raw calls, including in tests).

| File | Purpose |
|------|---------|
| `sysexec.go` | `Command`/`CommandContext` — build an `*exec.Cmd` with an explicit, non-nil `Env` (a nil env would make the child inherit the parent's full ambient environment, the exact leak §12 forbids; pass an empty slice for "no environment"). `Curated()` builds a subprocess env from an allowlist over the layout env plus overrides. `GitEnv()` is the shared curated-env allowlist for host-side git invocations. |

### `internal/broker/`

Credential brokering (D105/D106): an always-on, per-sandbox reverse proxy so a real API key never enters the sandbox — the agent holds only a placeholder credential, and the proxy swaps it for the real one on the way out.

| File | Purpose |
|------|---------|
| `injector.go` | `Injector` — the reverse proxy itself (implements `http.Handler`): verifies the inbound placeholder token (constant-time compare, 403 on mismatch — stops a co-resident container from using the injector as an unauthenticated relay), strips the placeholder-carrying headers, and injects the real credential via `internal/credential.ApplyTo` before forwarding to the one configured `Upstream`. |
| `host.go` | `InjectorHost` interface and `SidecarHost`, the CLI implementation: spawns the injector as a detached, `Setsid`'d child process (survives the CLI exiting) via `internal/sysexec`, tracks it by PID+address in a per-sandbox `injector.json` record, and respawns it (reusing the same bind port) if the recorded process died — the reconcile path. `PlaceholderToken` get-or-creates the per-sandbox placeholder secret the launch path hands the agent. |
| `sidecar.go` | `RunSidecar` — the body of the out-of-process injector: reads its `SidecarConfig` (with the real secret) from stdin, never argv/env, binds its listener, writes the resolved address back as a handshake, then serves until its context is cancelled. Dispatched to from `cmd/yoloai` under the hidden `__inject` argv[1]. |
| `reap.go` | `ReapOrphanInjectors` — the host-orphan half of `yoloai system prune` (DF71): enumerates running `__inject` processes and kills any not in the caller's keep-set, backstopping a broker leaked by a crash or SIGKILL whose `injector.json` record is gone. |

### `internal/credential/`

The resolve-and-inject primitives `internal/broker` composes into the proxy — general enough to cover LLM API keys, git, and package-registry auth alike, not proxy-specific.

| File | Purpose |
|------|---------|
| `source.go` | `CredentialSource` — a closed interface (`StaticSource` \| `RefreshingSource` \| reserved `MintingSource`) yielding the current secret value, refreshing short-lived tokens transparently before they expire. |
| `apply.go` | `Apply` — a closed interface (`HeaderSet` \| `BasicAuth` \| reserved `RequestSigner`) injecting a resolved credential into an outbound request. `RequestSigner` is reserved to run last (it must see every other transform's output) but returns `ErrNotImplemented` until built. |
| `binding.go` | `CredentialBinding` ties a `Destination` (request host to match) to an `Apply`+`Source`; `ApplyTo` runs every matching binding against a request in two passes (non-signers, then signers). |
| `errors.go` | `ErrNotImplemented` — returned by the reserved `RequestSigner`/`MintingSource` variants so the closed interface sets need not break later to add them. |

### `internal/netpolicy/`

Network-allowlist composition and the capability model for whether an enforcement strategy can actually work on a given (backend, isolation-mode) pair.

| File | Purpose |
|------|---------|
| `strategy.go` | `Strategy` (`StrategyIPFilter`, the only one shipped; reserved `StrategyEgressProxy`) and `CanEnforce()` — reports whether a strategy can enforce the allowlist for a backend/isolation combination, e.g. refusing `ip-filter` under gVisor (`container-enhanced`) because its userspace netstack ignores in-sandbox iptables rules rather than silently no-op'ing. |
| `compose.go` | `Compose()` resolves the effective network mode and allowlist from the raw mode string plus the agent's built-in domains and the user's added domains; `WithProvenance()` tags each resulting domain as agent-required vs. user-added so callers can warn before removing one the agent needs. |

### `internal/netpolicycfg/`

Per-sandbox network-policy persistence (`netpolicy.json`), split out from the substrate's `store.Environment` (D90) so netpolicy owns its own record.

| File | Purpose |
|------|---------|
| `netpolicycfg.go` | `Netpolicy` struct (mode + composed allowlist) and `Save`/`Load` for `netpolicy.json`. A sandbox with default non-isolated networking writes no record — `Load` returns a zero-value `Netpolicy` when the file is absent. |

### `internal/testutil/`

Shared test helpers — a non-`_test.go` package importable by test files across all packages. Not included in production builds (nothing in the main binary imports it).

| File | Purpose |
|------|---------|
| `git.go` | `InitGitRepo`, `GitAdd`, `GitCommit`, `GitRevParse`, `RunGit`, `WriteFile` — git and filesystem helpers. |
| `fixtures.go` | `GoProject(t)`, `AuxDir(t, name)`, `MultiFileProject(t)` — create temp project directories with committed git state. |
| `home.go` | `IsolatedHome(t)` — `t.Setenv("HOME", t.TempDir())` for per-test sandbox isolation. |
| `wait.go` | `WaitForActive`, `WaitForStopped` — poll `rt.Inspect` at 200ms intervals instead of `time.Sleep`. |

### `config/`

| File | Purpose |
|------|---------|
| `config.go` | `YoloaiConfig` struct, `LoadBakedInDefaults()`, `LoadDefaultsConfig()`, `mergeConfigs()`, `LoadGlobalConfig()`, `UpdateConfigFields()`, `DeleteConfigField()`, `UpdateGlobalConfigFields()`, `DeleteGlobalConfigField()`, `GetEffectiveConfig()`, `GetConfigValue()`, `IsGlobalKey()`. Two load paths: profile path (baked-in + profile config.yaml) and defaults path (baked-in + defaults/config.yaml). YAML comment-preserving via `yaml.Node`. |
| `defaults.go` | `DefaultConfigYAML` — baked-in defaults YAML (authoritative source of truth for all defaults). `DefaultGlobalConfigYAML` — default global config content. `GenerateScaffoldConfig()` — generates commented-out scaffold from baked-in YAML. |
| `dirs.go` | Shared sandbox subdirectory name constants (`BackendDirName`, `BinDirName`, `TmuxDirName`, `AgentRuntimeDirName`). The DataDir-rooted path helpers (`SandboxesDir()`, `ProfilesDir()`, `CacheDir()`, `DefaultsDir()`, …) are `Layout` methods in `layout.go`. |
| `profile.go` | `ProfileConfig`, `LoadProfile()`, `MergedConfig` — profile loading, inheritance chain resolution, config merging. |
| `schema.go` | `ReadSchemaVersion()` / `WriteSchemaVersion()` — plain-text-integer layout stamp. `LayoutStatus` + `RealmStatus(dataDir, version)` — the pure read-only realm check (absent/empty→Fresh, `<`→Migrate, `==`→OK, `>`→error) shared by both realms. `CreateFreshLibrary(layout)` fresh-inits + stamps; `MigrateLibrary(layout)` brings the library DataDir up to version (v0→v1 no-op today). The engine no longer auto-migrates — the startup gate + `yoloai system migrate` drive these (see D60/D61). |
| `pathutil.go` | `ExpandPath()` — tilde and `${VAR}` expansion for config paths. |
| `errors.go` | `UsageError` (exit 2), `ConfigError` (exit 3), sentinel errors. |
| `names.go` | Name validation constants and regex (`ValidNameRe`, `MaxNameLength`). |
| `encode.go` | Safe ASCII caret encoding for filesystem-safe names (keys and values). |
| `homedir.go` | Home directory detection and expansion, respecting `SUDO_USER` when running under sudo. |

### `extension/`

| File | Purpose |
|------|---------|
| `extension.go` | Loading, validation, and types for user-defined custom commands stored as YAML files in `~/.yoloai/cli/extensions/` (the dir path is supplied by the CLI via `cliutil.CLIExtensionsDir()`; extensions are CLI app state, not library state — see D60). |

### `runtime/`

| File | Purpose |
|------|---------|
| `runtime.go` | `Backend` interface — pluggable backend abstraction. Generic types: `MountSpec`, `PortMapping`, `InstanceConfig`, `InstanceInfo`, `ExecResult`, `BackendCaps`, `ResourceLimits`, `PruneItem`, `PruneResult`. Optional interfaces: `UsernsProvider`, `WorkDirSetup`. Sentinel errors: `ErrNotFound`, `ErrNotRunning`. |
| `registry.go` | Backend registry. `Register(name, factory, descriptor)` called by each backend's `init()` with a `(Factory, BackendDescriptor)` tuple. `New()` instantiates a Runtime by name. `Descriptor(name)` and `Descriptors()` return static facts without instantiating. `Available()` lists registered backend names. |
| `isolation.go` | `IsolationContainerRuntime()` — maps isolation modes to OCI runtimes (e.g., `container-enhanced` → `runsc`, `vm` → `kata`). `IsolationSnapshotter()` — maps to containerd snapshotters. |
| `exec.go` | `RunCmdExec()`, `RunCmdExecRaw()` — shared helpers for running `exec.Cmd` and building `ExecResult`. |

### `runtime/caps/`

Dynamic capability detection system. Probes the host, checks backend prerequisites, provides fix instructions.

| File | Purpose |
|------|---------|
| `caps.go` | Core types: `HostCapability` (check function + fix steps + permanence), `FixStep`, `Availability` (Ready/NeedsSetup/Unavailable), `CheckResult`, `BackendReport`, `Environment`. |
| `check.go` | `RunChecks()` — runs capability checks and returns results. `ComputeAvailability()`, `FormatError()`, `FormatDoctor()` — output formatters. |
| `common.go` | Shared `HostCapability` constructors reused across backends (e.g., Docker socket, Tart binary). Each takes injectable function pointers for testability. |
| `detect.go` | `DetectEnvironment()` — probes host (root, WSL2, container, KVM group) using injectable file path vars. |

### `runtime/docker/`

| File | Purpose |
|------|---------|
| `docker.go` | `Runtime` struct — implements `Runtime` interface, wraps Docker SDK. Registers itself via `init()`. |
| `build.go` | `Setup()` / `IsReady()` — builds `yoloai-base` image. `NeedsBuild()` / `RecordBuildChecksum()` for rebuild detection. Tar context creation, build output streaming. |
| `caps.go` | `HostCapability` constructors for Docker backend — gVisor runsc binary and gVisor registered with Docker daemon. |
| `resources.go` | `//go:embed` for Dockerfile, entrypoint.sh, tmux.conf. Imports `sandbox-setup.py`, `status-monitor.py`, and `diagnose-idle.sh` from `runtime/monitor`. `EmbeddedTmuxConf()` — accessor used by setup wizard. |
| `resources/Dockerfile` | Container Dockerfile (embedded at compile time). |
| `resources/entrypoint.sh` | Root container entrypoint script (embedded at compile time). Handles UID/GID remapping, iptables, overlayfs, then invokes `sandbox-setup.py`. |
| `resources/tmux.conf` | Default tmux config (embedded at compile time). |
| `prune.go` | `Prune()` — finds and removes orphaned `yoloai-*` Docker containers and dangling images. |

### `runtime/podman/`

| File | Purpose |
|------|---------|
| `podman.go` | `Runtime` struct — embeds `docker.Runtime`, overrides socket discovery (`$CONTAINER_HOST`, `$XDG_RUNTIME_DIR/podman/podman.sock`, `/run/podman/podman.sock`, `podman machine inspect` on macOS) and `Create()` to inject `--userns=keep-id` for rootless file ownership. Registers via `init()`. |
| `caps.go` | `HostCapability` constructors for Podman backend — rootless check and gVisor runsc. |

### `runtime/tart/`

| File | Purpose |
|------|---------|
| `tart.go` | `Runtime` struct — implements `Runtime` interface, shells out to `tart` CLI. VM lifecycle via `tart clone/run/stop/delete`, exec via `tart exec`. PID file + `tart list` for process management. Registers via `init()`. |
| `build.go` | `Setup()` / `IsReady()` — pulls Cirrus Labs macOS base image, provisions dev tools via `tart exec` (Homebrew, Node.js, Xcode CLI tools, tmux, git, jq, ripgrep). Supports `defaults.tart.image` config override. |
| `runtime.go` | `RuntimeVersion` type for Apple simulator runtimes. `ParseRuntime()` parses `platform[:version]` format. |
| `runtime_copy.go` | `CopyRuntimeToVM()` — downloads and installs runtimes using `xcodebuild -downloadPlatform`. |
| `prune.go` | `Prune()` — finds and removes orphaned `yoloai-*` Tart VMs. |
| `resources.go` | `//go:embed` for tmux.conf. |
| `platform.go` | Platform detection helpers (macOS, Apple Silicon). Testable via variable overrides. |
| `base_lock.go` / `base_lock_windows.go` | File locking for base image operations (prevents concurrent provisioning). |

### `runtime/seatbelt/`

| File | Purpose |
|------|---------|
| `seatbelt.go` | `Runtime` struct — implements `Runtime` interface using macOS `sandbox-exec`. PID file management, background process, per-sandbox tmux socket. Registers via `init()`. |
| `profile.go` | `GenerateProfile()` — builds SBPL (Seatbelt Profile Language) profiles from `InstanceConfig`. Maps mounts to file-access rules, controls network. |
| `build.go` | `Setup()` / `IsReady()` — verifies prerequisites (sandbox-exec, tmux, jq). No image to build. |
| `prune.go` | No-op `Prune()` implementation (no central registry to scan). |
| `resources.go` | `//go:embed` for tmux.conf. |
| `platform.go` | Platform detection (macOS only, no Apple Silicon requirement). Testable via variable override. |
| `resources/tmux.conf` | Default tmux config (embedded at compile time). |

### `runtime/containerd/`

| File | Purpose |
|------|---------|
| `containerd.go` | `Runtime` struct — implements `Runtime` interface using containerd v2 client. Connects to `/run/containerd/containerd.sock`. All API calls use the `yoloai` containerd namespace. Registers via `init()` on Linux only. |
| `caps.go` | `HostCapability` constructors — Kata shim, CNI bridge, network namespace creation, KVM device, devmapper snapshotter, Firecracker shim. |
| `lifecycle.go` | `Create()` (CNI setup, snapshotter selection, Kata config path), `Start()` (stopped-task cleanup), `Stop()` (SIGTERM + 10s timeout), `Remove()`, `Inspect()`. Task persistence via containerd shim — tasks survive the calling process. |
| `exec.go` | `Exec()` (stdout capture via FIFO), `InteractiveExec()` (PTY via `Terminal: true` + FIFO set, raw mode via `golang.org/x/term`, SIGWINCH forwarding). |
| `cni.go` | CNI network namespace creation (`vishvananda/netns`), CNI ADD/DEL via `containerd/go-cni`, per-sandbox state at `backend/cni-state.json`. Idempotent teardown. |
| `image.go` | `Setup()` — builds via `docker build` + `ctr images import`; `IsReady()` — checks containerd image store in `yoloai` namespace. |
| `prune.go` | `Prune()` — lists containers in `yoloai` namespace, removes orphaned `yoloai-*` containers, tears down their CNI namespaces. |
| `logs.go` | `Logs()` — reads bind-mounted `log.txt`. `DiagHint()` — points to `ctr -n yoloai tasks ls` and `journalctl -u containerd`. |

### `runtime/apple/`

| File | Purpose |
|------|---------|
| `apple.go` | `Runtime` struct — implements `runtime.Backend` by shelling out to Apple's `container` CLI (per-container Linux VMs, macOS 26+ / Apple Silicon only; gated by `minMacOSMajor`). Lifecycle (`Create`/`Start`/`Stop`/`Remove`/`Inspect`), `Exec`/`InteractiveExec` (the latter via `ptybridge.Exec` with `WithRemotePTY`, since `container exec -t` forces ONLCR on the bridge slave), `GitExec` (dispatches host-side work-copy git into the guest — `GitExecInConfinement: true`, mirroring the container backends), and `Setup`/`IsReady`/`buildBaseImage`. Registers via `init()`. |
| `reach.go` | `InjectorReach` — apple puts every sandbox on a shared vmnet "default" network whose gateway is both host-bindable and the guest's default route, so the credential injector binds and the agent dials the same IP (gateway-IP-for-both, like Docker Engine/containerd). Falls back to `ErrInjectorUnsupported` when the vmnet bridge isn't up. |
| `prune.go` | `PruneCache` implementing `runtime.CachePruner` — dangling/unused image prune plus build-cache reclaim (the `container` CLI has no cache-prune command, so reclaim is deleting-and-recreating the builder); reclaim is measured as the before/after `container system df` delta rather than trusted per-category figures. |

### `runtime/ptybridge/`

| File | Purpose |
|------|---------|
| `bridge.go` | `Exec()` — runs a child command under a locally-allocated PTY and copies it to the caller's `IOStreams`, the shared exec-bridging model for backends with no docker-API-style exec socket (apple, tart, seatbelt). Isolated in its own package so backends that don't need it avoid pulling in `github.com/creack/pty`. `WithRemotePTY` strips a redundant CR that a remote-PTY exec CLI (apple's `container exec -t`) injects via forced ONLCR on the local bridge slave — tart and seatbelt don't need it. |

### `runtime/runtimetest/`

Build-tag-`integration` shared conformance suite; `docs/contributors/architecture/testing.md` describes it as the one behavioral suite every backend runs against.

| File | Purpose |
|------|---------|
| `conformance_iface.go` | `RunInterfaceConformance` — the universal `runtime.Backend` contract exercised through interface methods only (lifecycle, exec exit-codes, exec-on-stopped, idempotency, `IsReady`, capability-gated `Mounts`/`Stdio` sections). A backend that can't honor a section declares it skipped (with a reason) via `InterfaceBackend.SkipMounts`/`SkipStdio` rather than forcing an inapplicable assertion. Each backend supplies its own `Sleeper` (how it keeps a long-running instance alive for exec tests) since that's genuinely backend-specific. |
| `conformance.go` | `DockerCompatRuntime`/`SetupFunc` — the narrower conformance table for docker-API-compatible backends (docker, podman) that also exposes the docker SDK client, for assertions the `runtime.Backend` interface alone can't reach (host-config facts like resource limits and port bindings). |

### `runtime/monitor/`

| File | Purpose |
|------|---------|
| `monitor.go` | `//go:embed` for `sandbox-setup.py`, `status-monitor.py`, and `diagnose-idle.sh`. Shared across all runtime backends. Exported accessors for each embedded script. |
| `sandbox-setup.py` | Consolidated setup script run inside containers/VMs: git baseline, agent launch, tmux pane setup. |
| `status-monitor.py` | Writes `agent-status.json` with idle detection and agent process health. |
| `diagnose-idle.sh` | Diagnostic script for idle detection troubleshooting. |

### `orchestrator/` (façade)

Post-F5, `package orchestrator` is a thin façade. Orchestration lives in the leaf
subpackages below; the root holds the `Engine` deps-holder plus alias files
(`type X = leaf.X` / `var X = leaf.X`) that keep the public API stable, and a
few helpers not yet carved out (clone, parse, setup, terminal/attach).

| File | Purpose |
|------|---------|
| `engine.go` | `Engine` struct — owns the **lazy backend connection** (D74): built eagerly from layout-only state with `runtime` nil (`NewEngine`), opens once on the first backend-bound method via mutex-guarded `ensure`/`TryEnsure` (`NewEngineWithRuntime` injects an already-open runtime for tests + ephemeral overwrite). A backend-less Engine returns `ErrBackendRequired` from backend-bound verbs and still serves host-only reads. `EnsureSetup()` for first-run auto-setup, `Inspect`/`List`/`SendInput`/`Runtime()` here; the lifecycle/create verbs (`Start`/`Stop`/`Restart`/`Reset`/`Destroy`/`NeedsConfirmation`/`Create`/`DestroyForOverwrite`) are self-ensuring Engine methods in `engine_lifecycle.go` over the leaf free functions. The workdir/files/network verbs are likewise Engine methods (`engine_workdir.go`/`engine_files.go`/`engine_network.go`, D74 Stage 2) so the `Workdir`/`Files`/`Network` sub-handles never thread `layout`/`runtime`. |
| `aliases.go` | Type/const aliases re-exporting the `create/` leaf's public symbols (CreateOptions, etc.) into package orchestrator. |
| `inspect.go` | Façade re-exports of the read-model — `type Info = status.Info`, `var InspectSandbox/ListSandboxes/DetectStatus = status.…`, Status/AgentStatus/WorkDataState constants. Implementation in `status/`. |
| `lifecycle.go` | Façade re-exports of lifecycle — `type StartOptions/ResetOptions = lifecycle.…`, `var PatchConfigAllowedDomains = lifecycle.…`. Implementation in `lifecycle/`. |
| `notice.go` | Façade re-exports of `Notice`/`NoticeLevel`/`DestroyResult`/`StartResult`/`ResetResult` from `lifecycle/`. |
| `profile_build.go` | Façade re-exports of profile image-build helpers; implementation in `profiles/`. |
| `clone.go` | `Engine.Clone()` — deep-copies an existing sandbox state dir to a new name, preserving agent state/workdir, resetting identity. |
| `terminal.go` | Non-interactive tmux capture-pane wrapper for diagnostics. |
| `attach.go` | Attach-readiness helpers — polls `sandbox.jsonl` / tmux `has-session`. |
| `prune.go` | `PruneTempFiles()` — cleans stale `/tmp/yoloai-*` dirs. |
| `tags.go` | Git tag info — `TagInfo`, commit matching, delegates to `workspace`. |
| `fileutil.go` | Path-expansion + JSON read/write wrappers. |
| `setup.go` | `RunSetup()`, `runNewUserSetup()` — interactive first-run setup. |
| `errors.go` | Sentinel errors; `ErrSandboxNotFound` re-exported from `store`. |
| `*_test.go` | Façade + remaining-helper unit tests. `integration_test.go` has the `integration` build tag. |

### `orchestrator/create/`, `lifecycle/`, `status/`, `launch/`

The F5 orchestration leaves. Functions take `state.Deps` (runtime + layout +
input) rather than hanging off `Engine`. DAG: `state ← {mounts, invocation,
provision, profiles, runtimeconfig} ← launch ← {create, lifecycle}`.

| Package | Purpose |
|---------|---------|
| `create/` | `Run()` orchestrates creation (prepare → seed → build) via `create_prepare.go`; `context.go` writes `context.md` + inlines env into the agent instruction file. |
| `lifecycle/` | `Start/Stop/Destroy/Reset/NeedsConfirmation` free functions. `recreateContainer()`/`relaunchAgent()` for restart; `resetInPlace()` for in-place resets; overlay/cache clearing; `PatchConfigAllowedDomains`. `notice.go` defines the `Notice`/result types. |
| `status/` | Read-model: `DetectStatus()` (reads `agent-status.json`, falls back to tmux exec), `InspectSandbox()`, `ListSandboxes()`, work-data probing, `DirSize()`. Returns structured data (`Info.DiskUsageBytes`); rendering is the CLI's job. |
| `launch/` | Shared launch primitives both create/ and lifecycle/ use: instance build/start, `Teardown`, vm-workdir resolution, and `CheckIsolationPrerequisites` (host-capability gate, homed here so create/ and lifecycle/ stay siblings). |
| `mounts/`, `invocation/`, `provision/`, `profiles/`, `runtimeconfig/` | Lower leaves: mount-spec construction, agent invocation assembly, agent-files seeding + keychain sourcing, profile image building, and runtime `ContainerConfig` assembly respectively. |

### `orchestrator/archetype/`

Environment archetype detection, devcontainer.json parsing, `.yoloai.yaml` loading, and VS Code workspace injection. Imported by `orchestrator/` (one-way; archetype/ does not import orchestrator/).

| File | Purpose |
|------|---------|
| `archetype.go` | `Archetype` type, constants (simple/compose/devcontainer/apple), `ParseArchetype()`, `ValidArchetypes()`, `DetectArchetype()` — auto-detects project type from workdir signals. |
| `devcontainer.go` | `LifecycleCmd` (string/array/object unmarshaling), `DevcontainerConfig` struct, `LoadDevcontainer()`, `ExtractPorts()`, `FilterMounts()`, `MergedEnv()`, `ParsedRunArgs()`, `WarnIgnoredFields()`, `PostStartCommandUsesCompose()`, `DockerComposeFilePresent()`, `LifecycleCmdToJSON()`. |
| `yoloaiyaml.go` | `YoloAIProjectConfig` struct, `LoadYoloAIYaml()` — loads `.yoloai.yaml` project config with archetype declaration, extra mounts, and requires constraints. |
| `vscode.go` | `InjectVSCodeWorkspace()` — writes `.vscode/extensions.json` and `.vscode/settings.json` from devcontainer.json customizations into the workdir copy. Existing keys win. |

### `copyflow/`

Git-format diff and apply machinery. Imports `orchestrator` (for exec helpers and locks) and `store` (for Meta and path helpers).

| File | Purpose |
|------|---------|
| `diff.go` | `GenerateDiff()`, `GenerateMultiDiff()`, `GenerateOverlayDiff()`, `GenerateCommitDiff()`, `ListCommitsWithStats()`, `DiffContext`, `LoadAllDiffContexts()` — diff generation for `:copy`, `:overlay`, and `:rw` modes. |
| `apply.go` | `ApplyAll()`, `ApplySeries()`, `GeneratePatch()`, `GenerateFormatPatch()`, `GenerateFormatPatchForRefs()`, `GenerateMultiPatch()`, `GenerateOverlayPatch()`, `GenerateUncommittedDiff()`, `UpdateOverlayBaselineToHEAD()`, `UpdateOverlayBaseline()`, `AdvanceBaseline()`, `AdvanceBaselineTo()`, `HasUncommittedChanges()`, `ListCommitsBeyondBaseline()`, `ResolveRef()`, `ResolveRefs()`. |
| `apply_overlay.go` | `ApplyOverlay()` — net-diff apply for `:overlay` workdirs (capture upper-layer diff inside the container → apply to host → advance overlay baseline). |
| `export.go` | `Export()` — write the sandbox's changes as patch files to a directory (the `apply --patches` flow); copy → format-patch (+ `uncommitted.diff`), overlay → upper-layer diffs. |

### `store/`

On-disk sandbox state — paths, metadata, and creation-completion flags. Leaf subpackage; imports only stdlib, `config`, `internal/fileutil`, `yoerrors`. Imported by `orchestrator`, `copyflow`, and most external callers.

| File | Purpose |
|------|---------|
| `paths.go` | `EncodePath()` / `DecodePath()` — caret encoding for filesystem-safe names. `InstanceName(principal, name)` — principal-aware runtime handle: `yoloai-<name>` for the default `""` principal, `yoloai-<principal>-<name>` otherwise (D62). `Dir()`, `WorkDir()`, `RequireSandboxDir()`. `OverlayUpperDir()` / `OverlayOvlworkDir()` for `:overlay` mount paths. `ValidateName()` delegates to `config.ParseSandboxName` (containerd-conformant grammar). Centralized filename constants (`EnvironmentFile`, `RuntimeConfigFile`, `AgentStatusFile`, `SandboxStateFile`, etc.) and `ErrSandboxNotFound`. |
| `environment.go` | `Environment` / `WorkdirEnvironment` / `DirEnvironment` structs, `SaveEnvironment()` / `LoadEnvironment()` — sandbox metadata persistence as `environment.json`. `Environment.BackendType` records which runtime backend was used; `Environment.Principal` records the owning principal (D62). |
| `sandbox_state.go` | `SandboxState` struct, `LoadSandboxState()`, `SaveSandboxState()` — per-sandbox runtime state (`sandbox-state.json`, legacy: `state.json`). Tracks `agent_files_initialized` and `on_create_commands_done`. Separate from `Environment` which is immutable after creation. |

### `workspace/`

| File | Purpose |
|------|---------|
| `apply.go` | `ApplyPatch()`, `ApplyFormatPatch()` — apply patches to host working directories. |
| `copy.go` | `CopyDir()` — walk-based directory copy preserving symlinks, permissions, and times. |
| `copy_darwin.go` | macOS `clonefile(2)` for copy-on-write clones on APFS. Falls back to walk-based copy. |
| `copy_other.go` | Non-macOS stub (always uses walk-based copy). |
| `diff.go` | `GenerateDiff()`, `DiffResult` — git diff generation for workspace directories. |
| `git.go` | Git command helpers with hooks disabled (`GIT_HOOKS_DISABLED=1`). `NewGitCmd()`, `RunGit()`, `GitInit()`, `GitBaseline()`. |
| `safety.go` | `CheckDangerousDir()` — validates directories are safe to operate on (not `/`, not `$HOME`). |
| `tags.go` | `CommitExists()`, commit metadata matching for tag operations. |

## Key Types

### `yoloai.Client`
High-level public API for library consumers. Wraps `orchestrator.Engine` and a `runtime.Backend`. The `Client` root provides `CreateSandbox()`, `ListSandboxes()`; per-sandbox verbs (`Start`, `Wait`, `Inspect`, `Stop`, `Destroy`, `Clone`, `Workdir().Diff/Apply`, …) live on the `*Sandbox` handle. Configured via `ClientCreateOptions` (backend, logger, output, input). `SandboxCreateOptions` mirrors CLI flags for `yoloai new`.

### `orchestrator.Engine`
Central orchestrator. Holds a `runtime.Backend`, backend name, logger, and I/O streams. All sandbox operations go through it: `Create()`, `Start()`, `Stop()`, `Destroy()`, `Reset()`, `Clone()`, `Inspect()`, `List()`, `EnsureSetup()`. The backend name is stored so it can be persisted in `Environment` at sandbox creation time.

### `store.Environment` / `store.WorkdirEnvironment` / `store.DirEnvironment`
Persisted as `environment.json` in each sandbox dir. Records creation-time state: agent, model, profile, workdir path/mode/baseline SHA, auxiliary directories (via `Directories` field), network mode/allow, ports, resources, mounts, backend. Each directory (workdir and aux dirs) has its own `DirEnvironment` with host path, mount path, mode, and baseline SHA. Lives in `store`. The public `yoloai.Environment` read-model (carried on `Info.Environment`) is a hand-written field-for-field mirror.

### `store.SandboxState`
Per-sandbox runtime state persisted as `sandbox-state.json` (legacy: `state.json`). Tracks mutable state like `agent_files_initialized` (boolean). Separate from `Meta` which is immutable after creation. Lives in `store`.

### `orchestrator.CreateOptions` / `orchestrator.DirSpec`
Internal parameters for `Engine.Create()`. `DirSpec` specifies a directory path, mount mode (copy/overlay/rw/ro), and per-directory safety acks (`AllowDirty`, `AllowDangerousPath`). `CreateOptions` includes name, workdir `DirSpec`, auxiliary `DirSpec` list, agent, model, prompt, network, ports, profile, replace, passthrough args. The **public** creation surface is `yoloai.SandboxCreateOptions` (root `sandbox_options.go`); `Client.CreateSandbox` maps it onto this internal struct via `toInternal()`. A dirty workdir surfaces as `*yoerrors.DirtyWorkdirError` (never an in-library prompt — D24).

### `copyflow.DiffOptions` / `copyflow.DiffResult`
Input/output for `copyflow.GenerateDiff()` / `copyflow.GenerateMultiDiff()`. Supports path filtering and stat-only mode. `DiffResult` carries the diff text, workdir, mode, and empty flag. Lives in `copyflow`.

### `orchestrator.CloneOptions`
Parameters for `Engine.Clone()`. Source and destination sandbox names, optional overrides.

### `archetype.Archetype` / `archetype.DevcontainerConfig` / `archetype.YoloAIProjectConfig`
Project-archetype detection types. Lives in `orchestrator/archetype`.

### `agent.Definition`
Describes an agent's commands (interactive/headless), prompt delivery mode, API key env vars (`APIKeyEnvVars`), auth hint env vars (`AuthHintEnvVars`), `AuthOptional` flag, seed files, state directory, tmux submit sequence, `ReadyPattern`, model flag/aliases/prefixes (`ModelPrefixes`), network allowlist, `ContextFile` (native instruction file for sandbox context injection), `AgentFilesExclude` (glob patterns to skip when copying agent_files), and `IdleSupport`. Built-in: `aider`, `claude`, `codex`, `gemini`, `opencode`, `test`, and `idle`.

### `runtime.Backend`
Pluggable backend interface for backend abstraction. Core methods: `Setup()`, `IsReady()`, `Create()`, `Start()`, `Stop()`, `Remove()`, `Inspect()`, `Exec()`, `InteractiveExec()`, `Prune()`, `Close()`, `DiagHint()`, `Descriptor()`, `TmuxSocket()`, `AttachCommand()`. (`Logs`, `PrepareAgentCommand`, `GitExec` are optional interfaces — see below — F18: only methods every backend implements non-trivially are core.) Static per-backend facts (Name, BaseModeName, AgentProvisionedByBackend, SupportedIsolationModes, Capabilities) are bundled into `BackendDescriptor` returned by `Descriptor()`. Allows swapping container/VM backends.

### `runtime.BackendDescriptor`
Bundles each backend's static facts: `Name`, `BaseModeName`, `AgentProvisionedByBackend`, `SupportedIsolationModes`, `Capabilities`. Returned by `Backend.Descriptor()`; values are compile-time constants per backend.

### `runtime.BackendCaps`
Declares what features a backend supports: `NetworkIsolation`, `OverlayDirs`, `CapAdd`, `HostFilesystem`. Embedded in `BackendDescriptor`. Used by sandbox logic to gate features without string-comparing backend names.

### `runtime.Factory` / Backend Registry
`Factory` is `func(context.Context) (Backend, error)`. Backends register `(Factory, BackendDescriptor)` tuples via `runtime.Register(name, factory, descriptor)` in their `init()` functions. `runtime.New(ctx, name)` creates a Backend by name; `runtime.Descriptor(name)` returns the static descriptor without instantiating; `runtime.Descriptors()` enumerates all registered descriptors. `runtime.Available()` lists registered backend names. Platform-specific backends (containerd on Linux, tart/seatbelt on macOS) only register on their supported platforms.

### Optional Runtime interfaces
Optional interfaces extend the core Runtime with backend-specific capabilities. Callers use type assertion or helper functions (`ResolveCopyMountFor`, `RequiredCapabilitiesFor`, `LogsFor`, `PrepareAgentCommandFor`, `GitExecFor`, …) that fall back to documented defaults when the backend doesn't implement them.

- `UsernsProvider` — Podman rootless `keep-id` mode.
- `WorkDirSetup` — Tart VM-local workdir copies.
- `StdioExecer` — Docker/Podman MCP-proxy stdio bridging.
- `CopyMountResolver` — Seatbelt and Tart rewrite `:copy` mount paths; container backends use the host path unchanged.
- `IsolationCapabilityProvider` — Docker/Podman/containerd declare per-isolation prerequisite capabilities; tart/seatbelt have none.
- `LogTailer` (`LogsFor`, default `""`) — Docker/containerd tail instance logs; VM/process backends (Tart/Seatbelt) write logs to files and don't implement it.
- `AgentCommandPreparer` (`PrepareAgentCommandFor`, default = passthrough) — Tart (node PATH) and Seatbelt (Swift wrapper) wrap the agent launch command; Docker/containerd need no wrapping.
- `GitExecer` (`GitExecFor`, default = run git on the host) — Tart runs git inside the VM and translates host work paths; the host-git backends (Docker/Podman/containerd/Seatbelt) use the default.

### `runtime.InstanceConfig`
Configuration for `Runtime.Create()`. Describes image, working directory, mounts, ports, network mode, resource limits, capabilities, devices, user namespace mode, and container runtime (OCI/Kata). `Labels` carries `com.yoloai.sandbox` (always) and `com.yoloai.principal` (non-default principals only) so an embedder can attribute and enumerate instances by owner — Docker/containerd apply them natively, Tart/Seatbelt persist them in their JSON config (D62).

### `caps.HostCapability`
Describes one system prerequisite: check function, permanence assessment, and remediation steps. Used by `system doctor` and `system check`.

### `caps.BackendReport`
Full check result for one (backend, isolation mode) combination. Contains `CheckResult` list, `Availability` classification (Ready/NeedsSetup/Unavailable), and optional `InitErr` when backend creation fails.

### `caps.Environment`
Host context: `IsRoot`, `IsWSL2`, `InContainer`, `KVMGroup`. Detected once per invocation, passed to all capability checks.

## Command → Code Map

| CLI Command | Entry Point | Core Logic |
|-------------|-------------|------------|
| `yoloai new` | `cli/lifecycle/new.go:NewNewCmd` | `yoloai.Client.CreateSandbox()` (→ `create.Run` in `orchestrator/create/create.go`) |
| `yoloai attach` | `cli/workflow/attach.go:NewAttachCmd` | `yoloai.Client.Attach()` (PTY-sized via `cliutil.IOStreams`) |
| `yoloai diff` | `cli/workflow/diff.go:NewDiffCmd` | `yoloai.Client.GenerateMultiPatch()` (→ `copyflow.GenerateMultiDiff` in `copyflow/diff.go`) |
| `yoloai apply` | `cli/workflow/apply.go:NewApplyCmd` | `yoloai.Client.GeneratePatch()` / `ApplyPatch()` / `GenerateFormatPatch()` |
| `yoloai start` | `cli/lifecycle/start.go:NewStartCmd` | `yoloai.Client.Start()` |
| `yoloai stop` | `cli/lifecycle/stop.go:NewStopCmd` | `yoloai.Client.Stop()` |
| `yoloai destroy` | `cli/lifecycle/destroy.go:NewDestroyCmd` | `yoloai.Client.Destroy()` |
| `yoloai reset` | `cli/lifecycle/reset.go:NewResetCmd` | `yoloai.Client.Reset()` |
| `yoloai restart` | `cli/lifecycle/restart.go:NewRestartCmd` | `yoloai.Client.Restart()` |
| `yoloai clone` | `cli/lifecycle/clone.go:NewCloneCmd` | `yoloai.Sandbox.Clone()` |
| `yoloai system info` | `cli/system/info.go` | Version, paths, disk usage, backend availability |
| `yoloai system agents` | `cli/system/backends_agents.go` | Lists agent definitions from `agent` package |
| `yoloai system backends` | `cli/system/backends_agents.go` | Probes each backend via `cliutil.CheckBackend` |
| `yoloai system build` | `cli/system/system.go` | `yoloai.System.BuildImage()` |
| `yoloai system setup` | `cli/system/system.go` + `cli/system/setup.go` (the wizard owns host inspection, prompts, auto-pick) | `yoloai.System.Config().Set()` (writes `tmux_conf`/`container_backend`/`agent`); `Backends()`/`Agents()` for choices — no library setup verb |
| `yoloai system check` | `cli/system/check.go` | `yoloai.System.CheckPrerequisites()` |
| `yoloai doctor` | `cli/doctorcmd/doctor.go` | `System.Doctor()` (→ `caps.RunChecks()` + `caps.FormatDoctor()`) + a dry-run `System.Prune()` and `DiskUsage()` for the advisory sections |
| `yoloai system prune` | `cli/system/prune.go` | `yoloai.System.Prune()` |
| `yoloai system tart` | `cli/system/tart/tart.go` | `tart.RuntimeVersion` / `tart.CopyRuntimeToVM()` / `tart.Runtime.ListVMs` / `tart.Runtime.DeleteVM` |
| `yoloai system completion` | `cli/system/completion.go` | Cobra's built-in completion generators |
| `yoloai mcp serve` | `cli/mcp/mcp.go` | `mcpsrv.New()` — MCP server on stdio |
| `yoloai mcp proxy` | `cli/mcp/mcp.go` | MCP proxy through sandbox |
| `yoloai sandbox list` | `cli/sandboxcmd/list.go` | `yoloai.Client.ListSandboxes()` (→ `status.ListSandboxes` in `orchestrator/status/`, re-exported via the façade) |
| `yoloai sandbox <name> info` | `cli/sandboxcmd/info.go` | `yoloai.Client.Inspect()` |
| `yoloai sandbox <name> log` | `cli/sandboxcmd/log.go` | `yoloai.Sandbox.Agent().Logs()` (→ `sandbox.StreamLogs` in `logstream.go`) for the structured activity stream; `Sandbox.Agent().TerminalLog()` for `--agent`. CLI keeps only rendering + `--since` parsing. |
| `yoloai sandbox <name> exec` | `cli/sandboxcmd/exec.go` | `yoloai.Client.Exec()` |
| `yoloai sandbox <name> prompt` | `cli/sandboxcmd/prompt.go` | Reads `prompt.txt` from sandbox dir |
| `yoloai sandbox <name> bugreport` | `cli/sandboxcmd/bugreport.go` | Forensic diagnostic collection (calls `bugreport.Write*`) |
| `yoloai sandbox <name> allow` | `cli/sandboxcmd/allow.go` | `orchestrator.PatchConfigAllowedDomains()` + `tryLivePatchNetwork` ipset update |
| `yoloai sandbox <name> allowed` | `cli/sandboxcmd/allowed.go` | `sandbox.LoadMeta()` — pure file read |
| `yoloai sandbox <name> deny` | `cli/sandboxcmd/deny.go` | `orchestrator.PatchConfigAllowedDomains()` + `tryLivePatchNetwork` ipset removal |
| `yoloai sandbox <name> vscode` | `cli/sandboxcmd/vscode.go` | Builds `vscode-remote://attached-container+<hex>/<path>` URI and launches `code --folder-uri` |
| `yoloai files` | `cli/workflow/files.go:NewFilesCmd` | File exchange via `~/.yoloai/library/sandboxes/<name>/files/` |
| `yoloai baseline` | `cli/workflow/baseline.go:NewBaselineCmd` | `yoloai.Client.AdvanceBaseline()` / `ResolveCommitRefs()` |
| `yoloai profile` | `cli/profile/profile.go:NewCmd` | Profile create/list/info/delete |
| `yoloai help` | `cli/helpcmd/help.go:NewCmd` | Topic-based help with embedded markdown |
| `yoloai config get/set/reset` | `cli/configcmd/config.go:NewCmd` | `config.{Get,Update,Delete}…Config…` routed via `config.IsGlobalKey()` |
| `yoloai ls` / `log` / `exec` / `vscode` | `cli/sandboxcmd/aliases.go` | Shortcuts that delegate to the matching `sandbox <verb>` impl in the same subpackage |
| `yoloai x` | `cli/xcmd/x.go:NewCmd` | User-defined extensions from `~/.yoloai/cli/extensions/` |
| `yoloai version` | `cli/versioncmd/version.go:NewCmd` | Prints build-time version info (reads `cliutil.Version` etc.) |


# Architecture

Code navigation guide for the yoloAI codebase — the source of truth for **where the code lives** (file paths, type catalogs, call chains). Focused on the implemented code, not aspirational features (see [design/](../design/README.md) for those). For the conceptual, diagram-first view of *how the pieces fit*, see [overview.md](overview.md).

## Package Map

```
client.go                → Orchestration spine: Client (Run, List, Clone, Create, EnsureSetup)
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
internal/runtime/        → Pluggable runtime interface, backend registry, isolation mapping, exec helpers
internal/runtime/caps/   → Capability detection system (host probing, fix instructions, doctor output)
internal/runtime/docker/      → Docker implementation of runtime.Runtime
internal/runtime/podman/      → Podman implementation (embeds Docker runtime, overrides socket discovery and rootless support)
internal/runtime/tart/        → Tart (macOS VM) implementation of runtime.Runtime
internal/runtime/seatbelt/    → Seatbelt (macOS sandbox-exec) implementation of runtime.Runtime
internal/runtime/containerd/  → Containerd implementation of runtime.Runtime (Kata Containers VM isolation)
internal/runtime/monitor/     → Embedded monitoring scripts shared across all backends (sandbox-setup.py, status-monitor.py, diagnose-idle.sh)
internal/sandbox/             → Façade (package sandbox): Engine deps-holder + alias re-exports; clone, parse, setup, terminal/attach
internal/sandbox/create/      → Leaf: sandbox-creation orchestration (Run = prepare → seed → build) + context files
internal/sandbox/lifecycle/   → Leaf: Start/Stop/Destroy/Reset/NeedsConfirmation free functions + restart/relaunch + Notice types
internal/sandbox/status/      → Leaf: sandbox read-model — DetectStatus, InspectSandbox, ListSandboxes, work-data probing
internal/sandbox/launch/      → Leaf: shared launch primitives (instance build/start, Teardown, vm-workdir, CheckIsolationPrerequisites)
internal/sandbox/mounts/      → Leaf: mount-spec construction from DirSpec/Meta
internal/sandbox/invocation/  → Leaf: agent invocation/command assembly
internal/sandbox/provision/   → Leaf: agent-files seeding + keychain credential sourcing
internal/sandbox/profiles/    → Leaf: profile image building (dependency order, staleness)
internal/sandbox/runtimeconfig/ → Leaf: ContainerConfig assembly for the runtime layer
internal/sandbox/archetype/   → Project archetype detection (devcontainer, compose, apple, simple) + .yoloai.yaml + VS Code workspace injection
internal/sandbox/patch/       → Git-format diff/apply machinery for :copy, :overlay, and :rw modes
internal/sandbox/state/       → Leaf: shared value types (DirSpec, State, Deps, IsolationPerms/Perms) every F5 leaf imports
internal/sandbox/store/       → On-disk sandbox state: paths, Meta record, SandboxState completion flags
internal/testutil/            → Shared test helpers (git, fixtures, home isolation, container polling) — test use only
internal/workspace/           → Workspace utilities (copy, git, safety checks, tags)
test/e2e/                → End-to-end tests against the compiled binary (build tag: e2e)
```

Public Go surface is the **`yoloai` package only** (W-L12). Every other Go package lives under `internal/` and is unreachable from external imports by the Go compiler itself. `cmd/yoloai` is the binary entry, not a library.

Dependency direction (W-L8 + W-L12 shape): `cmd/yoloai` → `internal/cli` → `yoloai` (Client + System) → `internal/sandbox` + `internal/sandbox/patch` + `internal/sandbox/store` + `internal/runtime`; `internal/sandbox` → `internal/sandbox/archetype` + `internal/sandbox/store` + `internal/runtime` + `internal/agent` + `internal/workspace`; `internal/sandbox/patch` → `internal/sandbox` + `internal/sandbox/store`; `internal/sandbox/store` and `internal/sandbox/state` are leaves (`state` holds the shared `DirSpec`/`State`/`Deps`/`Perms` value types so the F5 subpackages depend on it without importing the `sandbox` façade). Post-F5 the `sandbox` package is a thin **façade**: it re-exports leaf types/functions via `type X = leaf.X` / `var X = leaf.X` aliases and holds the `Engine` deps-holder, while orchestration lives in the leaves. The F5 DAG is `state ← {mounts, invocation, provision, profiles, runtimeconfig} ← launch ← {create, lifecycle}` (create/ and lifecycle/ are siblings — neither imports the other; their one-time shared check `CheckIsolationPrerequisites` lives in `launch/`) `← sandbox` (façade). Methods were **dissolved** into free functions taking `state.Deps`, not left as thin delegators; `yoloai.Client`/`Sandbox` call e.g. `lifecycle.Stop(ctx, deps, name)` and `create.Run(ctx, deps, ...)` directly. `internal/agent` stands alone; `internal/mcpsrv` depends on `yoloai` (not `sandbox.Engine`). The CLI reaches into neither the `internal/sandbox` **façade** package nor any of its leaf subpackages (`store`/`patch`/`archetype`/`status`/…) nor `internal/runtime/*` — every command goes through `yoloai.Client`, `yoloai.System`, and the `yoloai.*` re-exports. (G7 gave every former leaf reach-in a public verb — sandbox-metadata reads, agent-log/file paths, agent/model/backend discovery, stored-prompt get/set, the git-tag-on-apply — so the leaves are no longer consumer surfaces.) The `withRuntime`/`withManager` helpers were removed in W-L10. Cross-backend enumeration (`ls`, `doctor`, `system info`) goes through `System.ListAcrossBackends` / `Doctor` / `Info` (F23); the only remaining `cliutil.NewRuntime` callers are `cliutil.CheckBackend` (the availability-probe chokepoint, used by a few read-only displays) and the backend-scoped `system tart` subtree. Depguard (`.golangci.yml`) enforces the boundary going forward with two twin rules over non-test `internal/cli/**` and `internal/mcpsrv/**`: `cli-sandbox-scope` denies the `internal/sandbox` subtree by prefix — façade *and* all leaves, no allow-list (F1 Half-B + G2) — and `cli-runtime-scope` denies `internal/runtime` (only `internal/cli/system/tart/` is exempt, W-L13/G7).

## File Index

### `client.go` / `backend.go` / `sandbox.go` / `system.go` / `runtime_imports_linux.go`

| File | Purpose |
|------|---------|
| `client.go` | Orchestration spine — `Client` and its root methods (`Run`, `List`, `Clone`, `Create`, `EnsureSetup`) plus `SandboxCloneOptions` and the lazy-runtime construction helpers (`NewClient`, `ensure`, `newRuntime`). Registers Docker, Podman, Seatbelt, and Tart backends via blank imports. |
| `client_options.go` | `ClientCreateOptions` — the construction-time config `NewClient` takes (data/home dirs, optional `BackendType`, IO, env snapshot, principal). |
| `sandbox_options.go` | The public sandbox option types: `SandboxCreateOptions` (the advanced surface `Client.CreateSandbox` takes) and `SandboxRunOptions` (the curated `Client.Run` sugar), plus `toInternal`/`materialize` mapping and port formatting. |
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
| `runtime_imports_linux.go` | Linux-only blank import of `internal/runtime/containerd` so the backend self-registers on Linux builds. |
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
| `confirm.go` | `Confirm()` — context-aware y/N prompt with stdin/context racing. Moved here from `internal/sandbox` (B3); prompting is CLI-tier, not domain. |
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
| `list.go`, `log.go`, `exec.go` | The actual `sandbox list`/`log`/`exec` implementations. `log.go` is rendering-only: it consumes the `yoloai.System.Logs` activity stream (transport lives in `internal/sandbox/logstream.go`) and pretty-prints the verbatim JSONL frames. |
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
| `tart/` | Nested subpackage — the one sanctioned importer of `internal/runtime/tart` (depguard `cli-backend-scope` rule). |

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
| `server.go` | `Server` struct backed by `sandbox.Engine`. `New()` creates the MCP server with registered tool handlers. |
| `tools.go` | MCP tool definitions: sandbox lifecycle, observation, refinement, and file exchange tools. |
| `proxy.go` | MCP proxy — forwards MCP protocol between outer agent and inner MCP server running inside a sandbox. |

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
| `runtime.go` | `Runtime` interface — pluggable backend abstraction. Generic types: `MountSpec`, `PortMapping`, `InstanceConfig`, `InstanceInfo`, `ExecResult`, `BackendCaps`, `ResourceLimits`, `PruneItem`, `PruneResult`. Optional interfaces: `UsernsProvider`, `WorkDirSetup`. Sentinel errors: `ErrNotFound`, `ErrNotRunning`. |
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

### `runtime/monitor/`

| File | Purpose |
|------|---------|
| `monitor.go` | `//go:embed` for `sandbox-setup.py`, `status-monitor.py`, and `diagnose-idle.sh`. Shared across all runtime backends. Exported accessors for each embedded script. |
| `sandbox-setup.py` | Consolidated setup script run inside containers/VMs: git baseline, agent launch, tmux pane setup. |
| `status-monitor.py` | Writes `agent-status.json` with idle detection and agent process health. |
| `diagnose-idle.sh` | Diagnostic script for idle detection troubleshooting. |

### `sandbox/` (façade)

Post-F5, `package sandbox` is a thin façade. Orchestration lives in the leaf
subpackages below; the root holds the `Engine` deps-holder plus alias files
(`type X = leaf.X` / `var X = leaf.X`) that keep the public API stable, and a
few helpers not yet carved out (clone, parse, setup, terminal/attach).

| File | Purpose |
|------|---------|
| `engine.go` | `Engine` struct — slim deps-holder (`runtime.Runtime`, layout, input). `EnsureSetup()` / `EnsureSetupNonInteractive()` for first-run auto-setup. Lifecycle/create methods were dissolved into leaf free functions; `SendInput()` remains here. |
| `aliases.go` | Type/const aliases re-exporting the `create/` leaf's public symbols (CreateOptions, etc.) into package sandbox. |
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
| `errors.go` | Sentinel errors; `ErrSandboxNotFound` re-exported from `sandbox/store`. |
| `*_test.go` | Façade + remaining-helper unit tests. `integration_test.go` has the `integration` build tag. |

### `sandbox/create/`, `lifecycle/`, `status/`, `launch/`

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

### `sandbox/archetype/`

Environment archetype detection, devcontainer.json parsing, `.yoloai.yaml` loading, and VS Code workspace injection. Imported by `sandbox/` (one-way; archetype/ does not import sandbox/).

| File | Purpose |
|------|---------|
| `archetype.go` | `Archetype` type, constants (simple/compose/devcontainer/apple), `ParseArchetype()`, `ValidArchetypes()`, `DetectArchetype()` — auto-detects project type from workdir signals. |
| `devcontainer.go` | `LifecycleCmd` (string/array/object unmarshaling), `DevcontainerConfig` struct, `LoadDevcontainer()`, `ExtractPorts()`, `FilterMounts()`, `MergedEnv()`, `ParsedRunArgs()`, `WarnIgnoredFields()`, `PostStartCommandUsesCompose()`, `DockerComposeFilePresent()`, `LifecycleCmdToJSON()`. |
| `yoloaiyaml.go` | `YoloAIProjectConfig` struct, `LoadYoloAIYaml()` — loads `.yoloai.yaml` project config with archetype declaration, extra mounts, and requires constraints. |
| `vscode.go` | `InjectVSCodeWorkspace()` — writes `.vscode/extensions.json` and `.vscode/settings.json` from devcontainer.json customizations into the workdir copy. Existing keys win. |

### `sandbox/patch/`

Git-format diff and apply machinery. Imports `sandbox/` (for exec helpers and locks) and `sandbox/store` (for Meta and path helpers).

| File | Purpose |
|------|---------|
| `diff.go` | `GenerateDiff()`, `GenerateMultiDiff()`, `GenerateOverlayDiff()`, `GenerateCommitDiff()`, `ListCommitsWithStats()`, `DiffContext`, `LoadAllDiffContexts()` — diff generation for `:copy`, `:overlay`, and `:rw` modes. |
| `apply.go` | `ApplyAll()`, `ApplySeries()`, `GeneratePatch()`, `GenerateFormatPatch()`, `GenerateFormatPatchForRefs()`, `GenerateMultiPatch()`, `GenerateOverlayPatch()`, `GenerateUncommittedDiff()`, `UpdateOverlayBaselineToHEAD()`, `UpdateOverlayBaseline()`, `AdvanceBaseline()`, `AdvanceBaselineTo()`, `HasUncommittedChanges()`, `ListCommitsBeyondBaseline()`, `ResolveRef()`, `ResolveRefs()`. |
| `apply_overlay.go` | `ApplyOverlay()` — net-diff apply for `:overlay` workdirs (capture upper-layer diff inside the container → apply to host → advance overlay baseline). |
| `export.go` | `Export()` — write the sandbox's changes as patch files to a directory (the `apply --patches` flow); copy → format-patch (+ `uncommitted.diff`), overlay → upper-layer diffs. |

### `sandbox/store/`

On-disk sandbox state — paths, metadata, and creation-completion flags. Leaf subpackage; imports only stdlib, `config`, `internal/fileutil`, `yoerrors`. Imported by `sandbox/`, `sandbox/patch/`, and most external callers.

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
High-level public API for library consumers. Wraps `sandbox.Engine` and `runtime.Runtime`. Provides `Run()`, `Diff()`, `Apply()`, `List()`, `Inspect()`, `Stop()`, `Destroy()`. Configured via `ClientCreateOptions` (backend, logger, output, input). `SandboxRunOptions` mirrors CLI flags for `yoloai new`.

### `sandbox.Engine`
Central orchestrator. Holds a `runtime.Runtime`, backend name, logger, and I/O streams. All sandbox operations go through it: `Create()`, `Start()`, `Stop()`, `Destroy()`, `Reset()`, `Clone()`, `Inspect()`, `List()`, `EnsureSetup()`. The backend name is stored so it can be persisted in `Environment` at sandbox creation time.

### `store.Environment` / `store.WorkdirEnvironment` / `store.DirEnvironment`
Persisted as `environment.json` in each sandbox dir. Records creation-time state: agent, model, profile, workdir path/mode/baseline SHA, auxiliary directories (via `Directories` field), network mode/allow, ports, resources, mounts, backend. Each directory (workdir and aux dirs) has its own `DirEnvironment` with host path, mount path, mode, and baseline SHA. Lives in `sandbox/store`. The public `yoloai.Environment` read-model (carried on `Info.Environment`) is a hand-written field-for-field mirror.

### `store.SandboxState`
Per-sandbox runtime state persisted as `sandbox-state.json` (legacy: `state.json`). Tracks mutable state like `agent_files_initialized` (boolean). Separate from `Meta` which is immutable after creation. Lives in `sandbox/store`.

### `sandbox.CreateOptions` / `sandbox.DirSpec`
Internal parameters for `Engine.Create()`. `DirSpec` specifies a directory path, mount mode (copy/overlay/rw/ro), and per-directory safety acks (`AllowDirty`, `AllowDangerousPath`). `CreateOptions` includes name, workdir `DirSpec`, auxiliary `DirSpec` list, agent, model, prompt, network, ports, profile, replace, passthrough args. The **public** creation surface is `yoloai.SandboxCreateOptions` (root `sandbox_options.go`); `Client.CreateSandbox` maps it onto this internal struct via `toInternal()`. A dirty workdir surfaces as `*yoerrors.DirtyWorkdirError` (never an in-library prompt — D24).

### `patch.DiffOptions` / `patch.DiffResult`
Input/output for `patch.GenerateDiff()` / `patch.GenerateMultiDiff()`. Supports path filtering and stat-only mode. `DiffResult` carries the diff text, workdir, mode, and empty flag. Lives in `sandbox/patch`.

### `sandbox.CloneOptions`
Parameters for `Engine.Clone()`. Source and destination sandbox names, optional overrides.

### `archetype.Archetype` / `archetype.DevcontainerConfig` / `archetype.YoloAIProjectConfig`
Project-archetype detection types. Lives in `sandbox/archetype`.

### `agent.Definition`
Describes an agent's commands (interactive/headless), prompt delivery mode, API key env vars (`APIKeyEnvVars`), auth hint env vars (`AuthHintEnvVars`), `AuthOptional` flag, seed files, state directory, tmux submit sequence, `ReadyPattern`, model flag/aliases/prefixes (`ModelPrefixes`), network allowlist, `ContextFile` (native instruction file for sandbox context injection), `AgentFilesExclude` (glob patterns to skip when copying agent_files), and `IdleSupport`. Built-in: `aider`, `claude`, `codex`, `gemini`, `opencode`, `test`, and `idle`.

### `runtime.Runtime`
Pluggable runtime interface for backend abstraction. Core methods: `Setup()`, `IsReady()`, `Create()`, `Start()`, `Stop()`, `Remove()`, `Inspect()`, `Exec()`, `InteractiveExec()`, `Prune()`, `Close()`, `DiagHint()`, `Descriptor()`, `TmuxSocket()`, `AttachCommand()`. (`Logs`, `PrepareAgentCommand`, `GitExec` are optional interfaces — see below — F18: only methods every backend implements non-trivially are core.) Static per-backend facts (Name, BaseModeName, AgentProvisionedByBackend, SupportedIsolationModes, Capabilities) are bundled into `BackendDescriptor` returned by `Descriptor()`. Allows swapping container/VM backends.

### `runtime.BackendDescriptor`
Bundles each backend's static facts: `Name`, `BaseModeName`, `AgentProvisionedByBackend`, `SupportedIsolationModes`, `Capabilities`. Returned by `Runtime.Descriptor()`; values are compile-time constants per backend.

### `runtime.BackendCaps`
Declares what features a backend supports: `NetworkIsolation`, `OverlayDirs`, `CapAdd`, `HostFilesystem`. Embedded in `BackendDescriptor`. Used by sandbox logic to gate features without string-comparing backend names.

### `runtime.Factory` / Backend Registry
`Factory` is `func(context.Context) (Runtime, error)`. Backends register `(Factory, BackendDescriptor)` tuples via `runtime.Register(name, factory, descriptor)` in their `init()` functions. `runtime.New(ctx, name)` creates a Runtime by name; `runtime.Descriptor(name)` returns the static descriptor without instantiating; `runtime.Descriptors()` enumerates all registered descriptors. `runtime.Available()` lists registered backend names. Platform-specific backends (containerd on Linux, tart/seatbelt on macOS) only register on their supported platforms.

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
| `yoloai new` | `cli/lifecycle/new.go:NewNewCmd` | `yoloai.Client.CreateSandbox()` (→ `create.Run` in `sandbox/create/create.go`) |
| `yoloai attach` | `cli/workflow/attach.go:NewAttachCmd` | `yoloai.Client.Attach()` (PTY-sized via `cliutil.IOStreams`) |
| `yoloai diff` | `cli/workflow/diff.go:NewDiffCmd` | `yoloai.Client.GenerateMultiPatch()` (→ `sandbox.GenerateMultiDiff` in `sandbox/diff.go`) |
| `yoloai apply` | `cli/workflow/apply.go:NewApplyCmd` | `yoloai.Client.GeneratePatch()` / `ApplyPatch()` / `GenerateFormatPatch()` |
| `yoloai start` | `cli/lifecycle/start.go:NewStartCmd` | `yoloai.Client.Start()` |
| `yoloai stop` | `cli/lifecycle/stop.go:NewStopCmd` | `yoloai.Client.Stop()` |
| `yoloai destroy` | `cli/lifecycle/destroy.go:NewDestroyCmd` | `yoloai.Client.Destroy()` |
| `yoloai reset` | `cli/lifecycle/reset.go:NewResetCmd` | `yoloai.Client.Reset()` |
| `yoloai restart` | `cli/lifecycle/restart.go:NewRestartCmd` | `yoloai.Client.Restart()` |
| `yoloai clone` | `cli/lifecycle/clone.go:NewCloneCmd` | `yoloai.Client.CloneSandbox()` |
| `yoloai system info` | `cli/system/info.go` | Version, paths, disk usage, backend availability |
| `yoloai system agents` | `cli/system/backends_agents.go` | Lists agent definitions from `agent` package |
| `yoloai system backends` | `cli/system/backends_agents.go` | Probes each backend via `cliutil.CheckBackend` |
| `yoloai system build` | `cli/system/system.go` | `yoloai.System.BuildImage()` |
| `yoloai system setup` | `cli/system/system.go` + `cli/system/setup.go` (the wizard owns host inspection, prompts, auto-pick) | `yoloai.System.Config().Set()` (writes `tmux_conf`/`container_backend`/`agent`); `Backends()`/`Agents()` for choices — no library setup verb |
| `yoloai system check` | `cli/system/check.go` | `yoloai.System.Check()` |
| `yoloai doctor` | `cli/doctorcmd/doctor.go` | `System.Doctor()` (→ `caps.RunChecks()` + `caps.FormatDoctor()`) + a dry-run `System.Prune()` and `DiskUsage()` for the advisory sections |
| `yoloai system prune` | `cli/system/prune.go` | `yoloai.System.Prune()` |
| `yoloai system tart` | `cli/system/tart/tart.go` | `tart.RuntimeVersion` / `tart.CopyRuntimeToVM()` / `tart.Runtime.ListVMs` / `tart.Runtime.DeleteVM` |
| `yoloai system completion` | `cli/system/completion.go` | Cobra's built-in completion generators |
| `yoloai mcp serve` | `cli/mcp/mcp.go` | `mcpsrv.New()` — MCP server on stdio |
| `yoloai mcp proxy` | `cli/mcp/mcp.go` | MCP proxy through sandbox |
| `yoloai sandbox list` | `cli/sandboxcmd/list.go` | `yoloai.Client.ListSandboxes()` (→ `status.ListSandboxes` in `sandbox/status/`, re-exported via the façade) |
| `yoloai sandbox <name> info` | `cli/sandboxcmd/info.go` | `yoloai.Client.Inspect()` |
| `yoloai sandbox <name> log` | `cli/sandboxcmd/log.go` | `yoloai.Sandbox.Agent().Logs()` (→ `sandbox.StreamLogs` in `logstream.go`) for the structured activity stream; `Sandbox.Agent().TerminalLog()` for `--agent`. CLI keeps only rendering + `--since` parsing. |
| `yoloai sandbox <name> exec` | `cli/sandboxcmd/exec.go` | `yoloai.Client.Exec()` |
| `yoloai sandbox <name> prompt` | `cli/sandboxcmd/prompt.go` | Reads `prompt.txt` from sandbox dir |
| `yoloai sandbox <name> bugreport` | `cli/sandboxcmd/bugreport.go` | Forensic diagnostic collection (calls `bugreport.Write*`) |
| `yoloai sandbox <name> allow` | `cli/sandboxcmd/allow.go` | `sandbox.PatchConfigAllowedDomains()` + `tryLivePatchNetwork` ipset update |
| `yoloai sandbox <name> allowed` | `cli/sandboxcmd/allowed.go` | `sandbox.LoadMeta()` — pure file read |
| `yoloai sandbox <name> deny` | `cli/sandboxcmd/deny.go` | `sandbox.PatchConfigAllowedDomains()` + `tryLivePatchNetwork` ipset removal |
| `yoloai sandbox <name> vscode` | `cli/sandboxcmd/vscode.go` | Builds `vscode-remote://attached-container+<hex>/<path>` URI and launches `code --folder-uri` |
| `yoloai files` | `cli/workflow/files.go:NewFilesCmd` | File exchange via `~/.yoloai/library/sandboxes/<name>/files/` |
| `yoloai baseline` | `cli/workflow/baseline.go:NewBaselineCmd` | `yoloai.Client.AdvanceBaseline()` / `ResolveCommitRefs()` |
| `yoloai profile` | `cli/profile/profile.go:NewCmd` | Profile create/list/info/delete |
| `yoloai help` | `cli/helpcmd/help.go:NewCmd` | Topic-based help with embedded markdown |
| `yoloai config get/set/reset` | `cli/configcmd/config.go:NewCmd` | `config.{Get,Update,Delete}…Config…` routed via `config.IsGlobalKey()` |
| `yoloai ls` / `log` / `exec` / `vscode` | `cli/sandboxcmd/aliases.go` | Shortcuts that delegate to the matching `sandbox <verb>` impl in the same subpackage |
| `yoloai x` | `cli/xcmd/x.go:NewCmd` | User-defined extensions from `~/.yoloai/cli/extensions/` |
| `yoloai version` | `cli/versioncmd/version.go:NewCmd` | Prints build-time version info (reads `cliutil.Version` etc.) |

## Data Flow

### Sandbox Creation (`yoloai new`)

```
NewNewCmd (cli/lifecycle/new.go)
  → cliutil.WithClient (cli/cliutil/client.go)
    → yoloai.Client.CreateSandbox  (calls create.Run(ctx, deps, opts) in sandbox/create/create.go)
      → EnsureSetup: create dirs, seed resources, build image, write config.yaml
      → prepareSandboxState (sandbox/create/create.go):
          resolve profile chain → applyConfigDefaults
          → resolveAndApplyArchetype: load .yoloai.yaml → CLI > yaml > auto-detect priority
              → devcontainer: load devcontainer.json → merge ports/env/mounts, set workspaceFolder
              → compose: set isolation=container-privileged, archetypeDockerDRequired=true
              → transparency output (signals, bullets, suppress hint)
          → validate name/agent/workdir/auxdirs (DirSpecs already parsed upstream by cliutil.ParseDirArg) → safety checks
          → :copy dirs: copyDir (cp -rp / clonefile on macOS) → removeGitDirs → gitBaseline
          → :overlay dirs: createOverlayDirs (upper/ovlwork in sandbox state)
          → seed phase (provision/ leaf):
              copySeedFiles → copyAgentFiles → ensureContainerSettings → seedHomeConfig
          → readPrompt → resolveModel → buildAgentCommand
          → SaveMeta (environment.json) → SaveSandboxState (sandbox-state.json)
          → write prompt.txt, log.txt, runtime-config.json
          → WriteContextFiles (context.md + agent instruction file — sandbox/create/context.go)
      → buildAndStart (sandbox/launch/launch.go):
          createSecretsDir (config env vars + API keys from layout.Env host snapshot; staged under layout.SecretsStagingDir, "" = os.TempDir — D63)
          → buildMounts (workdir + aux dirs, overlay mount configs for :overlay dirs)
          → runtime.Create (with CAP_SYS_ADMIN for :overlay) → runtime.Start
          → runtime.Inspect (verify running) → cleanup secrets
```

### Runtime Backend Initialization

```
runtime.New(ctx, "docker")  (runtime/registry.go)
  → lookup factory in backends map (populated by init() registrations)
  → factory(ctx) → e.g. docker.New(ctx) → Docker SDK ping → DockerRuntime
```

Backends register themselves at import time via blank imports:
- `client.go`: imports docker, podman, seatbelt, tart
- `runtime_imports_linux.go`: imports containerd (Linux only)
- `internal/cli/runtime_imports_linux.go`: same for CLI binary

### Diff (`yoloai diff`)

```
newDiffCmd (cli/diff.go)
  → GenerateMultiDiff (sandbox/diff.go)
    → loadDiffContext: LoadMeta → resolve all directories from meta.Directories
    → For each directory:
      → :copy/:overlay mode: stageUntracked (git add -A) → git diff --binary <baseline>
      → :rw mode: git diff HEAD on live host dir
    → Combine diffs with directory-prefixed headers
```

### Apply (`yoloai apply`)

Two modes — squash and selective:

**Squash (default):**
```
applySquash (cli/apply.go)
  → For each :copy/:overlay directory in meta.Directories:
    → GeneratePatch (sandbox/apply.go): git diff --binary against baseline
    → CheckPatch: git apply --check
    → Confirm with user
    → ApplyPatch: git apply
    → AdvanceBaseline: update environment.json baseline SHA to HEAD
```

**Selective (commit refs):**
```
applySelectedCommits (cli/apply.go)
  → For each :copy/:overlay directory in meta.Directories:
    → ResolveRefs (sandbox/apply.go): resolve short SHAs / ranges
    → GenerateFormatPatchForRefs: git format-patch per commit
    → ApplyFormatPatch: git am --3way
    → AdvanceBaselineTo: advance baseline to contiguous prefix
```

### Overlay Mount Flow (`:overlay` directories)

Overlay mode uses Linux kernel overlayfs for instant setup with the diff/apply workflow:

```
create_prepare.go:
  → createOverlayDirs: create upper/ovlwork dirs in sandbox state

create_instance.go:
  → buildMounts: build overlay mount configs for runtime-config.json, add CAP_SYS_ADMIN

entrypoint.sh (Docker container, root phase):
  → mount overlayfs using runtime-config.json overlay_mounts
sandbox-setup.py (container, user phase):
  → git baseline (git init + commit) in mounted directories

diff.go / apply.go:
  → exec git commands inside container for overlay dirs (same as :copy)

lifecycle.go (reset):
  → clearOverlayDirs: rm -rf upper/ovlwork for instant reset
```

### Container Start/Restart (`yoloai start`)

```
lifecycle.Start(ctx, deps, name, opts) (sandbox/lifecycle/lifecycle.go)
  → DetectStatus (sandbox/status/status.go): runtime.Inspect + status file read
  → StatusActive: no-op
  → StatusDone/Failed: relaunchAgent via tmux respawn-pane
  → StatusStopped: runtime.Start
  → StatusRemoved: recreateContainer (rebuild state from environment.json via runtime.Create + runtime.Start)
```

### Capability Detection + Repair Advisory (`yoloai doctor`)

```
doctorcmd.NewCmd (cli/doctorcmd/doctor.go)
  → System.Doctor() — capability report:
    → caps.DetectEnvironment() — probe host (root, WSL2, container, KVM group)
    → For each registered backend:
      → runtime.New(ctx, name) — try to connect
      → rt.RequiredCapabilities(baseMode) — get base checks
      → For each rt.SupportedIsolationModes():
        → rt.RequiredCapabilities(mode) — get mode-specific checks
      → caps.RunChecks(capabilities, env) → []CheckResult
      → caps.ComputeAvailability(results) → Ready/NeedsSetup/Unavailable
    → caps.FormatDoctor(reports, output) — render table with fix instructions
  → System.Prune({DryRun:true}) + System.DiskUsage() — read-only advisory:
    → Reclaimable now    (RemovedItems)              → "yoloai system prune"
    → Reclaimable cached (CachedBytes, no rebuild)   → "yoloai system prune"
    → Reclaimable images (ImageBytes, forces build)  → "yoloai system prune --images"
    → Unreviewed work    (RefusedDataBearing)  → "yoloai diff / yoloai destroy"
    → Trash              (TrashContents)       → recover with mv / reclaim via prune
```

doctor is **pure read + delegate**: it never deletes or quarantines —
it only reports and prints the command that does the work. Exit code is 1
only when a backend NeedsSetup; advisory sections never affect it.

### Sandbox-dir recoverability classification (`System.Prune`)

Prune classifies every dir under `sandboxes/` by *recoverability*, not by
"brokenness". The bulk path only ever **removes** zero-stakes items; anything
that might hold user data is refused-and-reported or quarantined, never
silently deleted. The classifier (`classifySandboxes` in `system.go`)
crosses the `store.LoadMeta` failure kind with `sandbox.ProbeWorkData`:

```
meta loads cleanly                                   → known     (untouched; used for backend orphan matching)
data detected (ProbeWorkData = WorkDataPresent)      → refuse    (RefusedDataBearing — user runs diff/destroy)
missing meta + no work dir   (never-init)            → delete    (RemovedItems, PruneKindSandboxDir)
corrupt / version-too-new meta, no detectable data   → trash     (Trashed — quarantined to TrashDir, recover with mv)
```

`ProbeWorkData(sandboxDir)` (package `sandbox`) detects work **host-side, no
container needed**: copy-mode dirs via `detectChanges` on `work/<enc>/.git`;
overlay-mode dirs via a non-empty `work/<enc>/upper/`. It returns
WorkDataNone / WorkDataPresent / WorkDataAmbiguous so corrupt-meta dirs with
ambiguous content default to trash (the safe choice), not deletion.

Quarantine is a plain `os.Rename` into `~/.yoloai/library/trash/<name>`
(`store.QuarantineSandbox`); there is no dedicated restore command — recover
with `mv`. The CLI confirms before emptying trash (it may hold wanted data);
`--yes` skips the prompt.

## Host Directory Layout

The CLI splits `~/.yoloai/` into two namespaces: `library/` (everything the
embeddable engine owns — what the library `Layout` is pointed at) and `cli/`
(CLI-only app state). The split is a CLI convention; an embedder that passes an
explicit `DataDir` gets the engine subtree directly under that path, with no
`library/` segment (see D60). Each namespace carries its own plain-text-integer
`.schema-version` stamp.

**Startup gate (D61).** The root `PersistentPreRunE` runs a read-only migration
gate (`internal/cli/gate.go`) before any command touches the data dir. It
create-freshes a genuinely new install (absent/empty `TOP`), fails fast with
"run `yoloai system migrate`" when a realm is out of date, surfaces an
inconsistent-data-dir error when exactly one realm is uninitialized, or proceeds.
It never migrates silently — all mutation of an existing dir lives in the
explicit `yoloai system migrate` command (`internal/cli/system/migrate.go`).
`version`, `help`, `completion`, and `migrate` are gate-exempt via the
`cliutil.AnnotationSkipMigrationGate` annotation.

```
~/.yoloai/
├── cli/                     # CLI-only app state (not the library's)
│   ├── .schema-version      # CLI realm stamp (plain int; cliutil CLIStatus/MigrateCLI)
│   ├── state.yaml           # CLI state (first_run_tip_shown)
│   └── extensions/
│       └── <name>.yaml      # User-defined extension commands
└── library/                 # Engine-owned — see "library/ contents" below
```

`library/` is what the library `Layout` resolves to (or the embedder's explicit
`DataDir`):

```
library/
├── .schema-version      # Library realm stamp (plain int; config.RealmStatus/MigrateLibrary)
├── config.yaml              # Global config (tmux_conf, model_aliases)
├── defaults/
│   ├── config.yaml          # User defaults (agent, model, isolation, etc.; active when no --profile)
│   └── tmux.conf            # Optional; written by setup when baked-in tmux config is in use
├── profiles/
│   └── <name>/
│       ├── config.yaml      # Profile settings (merged over baked-in defaults, not over defaults/)
│       ├── Dockerfile       # Optional; FROM yoloai-base
│       └── tmux.conf        # Optional tmux config override
├── sandboxes/
│   └── <name>/
│       ├── environment.json   # Sandbox metadata (agent, workdir, baseline SHA)
│       ├── sandbox-state.json # Per-sandbox runtime state (agent_files_initialized, etc.)
│       ├── runtime-config.json # Runtime config (agent cmd, tmux settings)
│       ├── agent-status.json  # Agent status (written by status monitor)
│       ├── context.md         # Sandbox environment description (dirs, network, resources)
│       ├── prompt.txt         # Agent prompt (if provided)
│       ├── log.txt            # Session log
│       ├── monitor.log        # Status monitor debug log
│       ├── bin/               # Executable scripts
│       │   ├── sandbox-setup.py   # Consolidated setup script (all backends)
│       │   ├── status-monitor.py  # Idle detection monitor
│       │   └── diagnose-idle.sh   # Idle detection diagnostic
│       ├── tmux/              # Tmux runtime
│       │   ├── tmux.conf      # Tmux configuration
│       │   └── tmux.sock      # Per-sandbox tmux socket (seatbelt)
│       ├── backend/           # Backend-specific files
│       │   ├── instance.json  # Backend instance config
│       │   ├── profile.sb     # SBPL sandbox profile (seatbelt)
│       │   ├── pid            # Process ID file
│       │   └── stderr.log     # Backend stderr log
│       ├── agent-runtime/     # Mounted at agent's StateDir (e.g., ~/.claude/, ~/.gemini/)
│       ├── files/             # Bidirectional file exchange (shared files directory)
│       ├── cache/             # Agent cache (HTTP responses, cloned repos)
│       ├── home-seed/         # Files symlinked into sandbox HOME
│       ├── home/              # Sandbox HOME directory (seatbelt)
│       └── work/
│           └── <caret-encoded-path>/  # Copy of workdir with internal git repo
└── cache/                   # Global cache directory (e.g., overlay detection, base image checksum)
```

## Where to Change

**Add a new CLI command:**
1. Put the command code in the right subpackage under `internal/cli/`:
   - `lifecycle/` for create/start/stop/destroy verbs on a sandbox
   - `workflow/` for diff/apply/attach/files-style verbs
   - `sandboxcmd/` for a new `yoloai sandbox <verb>` subcommand (and any matching top-level alias in `sandboxcmd/aliases.go`)
   - `system/` for a `yoloai system <verb>` subcommand
   - one of the single-command subpackages (`profile/`, `configcmd/`, `mcp/`, `xcmd/`, `helpcmd/`, `versioncmd/`) if it's a peer of those
   - a brand-new subpackage if the command isn't a natural fit for any of the above
2. Add an exported constructor (`NewXxxCmd() *cobra.Command`) on that subpackage and tag the returned command with the appropriate `cliutil.Group<Lifecycle|Workflow|SandboxTools|Admin>` ID.
3. Wire it into `internal/cli/commands.go:registerCommands()` under its help group.
4. If the command needs a `yoloai.Client`, use `cliutil.WithClient`; for cross-backend admin work use `cliutil.System()`. Do not construct `sandbox.Engine` or raw `runtime.Runtime` directly — `.golangci.yml` enforces this via depguard / forbidigo.

**Add a new agent:**
1. Add a new entry to the `agents` map in `agent/agent.go`
2. Define: commands, prompt mode, API key env vars, auth hint env vars, `AuthOptional`, seed files, state dir, submit sequence, startup delay, ready pattern, idle support, model flag/aliases/prefixes, network allowlist, context file, agent files exclude patterns

**Change agent files seeding:**
1. Config parsing: `parseAgentFilesNode()` in `config/config.go`
2. Copy logic: `copyAgentFiles()` in `sandbox/agent_files.go`
3. Exclusion patterns: `AgentFilesExclude` in agent definitions (`agent/agent.go`)
4. State tracking: `SandboxState.AgentFilesInitialized` in `sandbox/sandbox_state.go`

**Add a new CLI flag to an existing command:**
1. Add `cmd.Flags().XxxP(...)` in the command's `newXxxCmd()` function
2. Read it with `cmd.Flags().GetXxx(...)` in the `RunE` handler

**Change container setup (Dockerfile, entrypoint):**
1. Edit files in `runtime/docker/resources/`
2. They're embedded at compile time via `//go:embed` in `runtime/docker/resources.go`

**Change the default tmux config:**
1. Edit `internal/resources/tmux/tmux.conf` (neutral location — shared by setup wizard and Docker image build)
2. `tmuxres.Embedded()` exposes the bytes; `sandbox/setup.go` uses it to write `defaults/tmux.conf` and `runtime/docker` ships it inside the image

**Change shared monitoring scripts (sandbox-setup.py, status-monitor.py):**
1. Edit files in `runtime/monitor/`
2. They're embedded at compile time via `//go:embed` in `runtime/monitor/monitor.go`
3. Imported by `runtime/docker/resources.go` and other backend resource files

**Change how sandbox state is persisted:**
1. Modify `Environment` / `DirEnvironment` in `sandbox/store/environment.go`
2. Update `prepareSandboxState()` in `sandbox/create_prepare.go` where the environment is populated
3. Update any consumers that `LoadEnvironment()` and use the changed fields (e.g., diff, apply, inspect, reset)
4. If the field is public, mirror it onto `yoloai.Environment` in `environment.go` and update `environmentFromStore`

**Change diff/apply behavior:**
1. Diff generation: `sandbox/diff.go`
2. Patch generation and application: `sandbox/apply.go`
3. CLI presentation: `internal/cli/diff.go` and `internal/cli/apply.go`

**Change container creation (mounts, networking):**
1. Mount construction: `mounts.Build()` (`sandbox/mounts/`) → populates `runtime.MountSpec`
2. Container config: `buildAndStart()` in `sandbox/launch/launch.go` → builds `runtime.InstanceConfig`
3. Port parsing: `parsePortBindings()` in `sandbox/launch/launch.go` → populates `runtime.PortMapping`
4. Runtime creation: `runtime.Create()` dispatched to the active backend

**Change sandbox status detection:**
1. `DetectStatus()` in `sandbox/status/status.go` — reads `agent-status.json` from sandbox dir (written by status monitor), falls back to legacy `status.json` then `runtime.Exec()` for old sandboxes
2. Status constants are in the same file

**Change config handling:**
1. Defaults config: `LoadDefaultsConfig()` (baked-in + defaults/config.yaml merge) / `UpdateConfigFields()` / `DeleteConfigField()` in `config/config.go`
2. Baked-in defaults: `LoadBakedInDefaults()` in `config/config.go`; add new default values to `DefaultConfigYAML` in `config/defaults.go`
3. Global config: `LoadGlobalConfig()` / `UpdateGlobalConfigFields()` / `DeleteGlobalConfigField()` in `config/config.go`
4. `IsGlobalKey()` determines routing — add new global keys to `globalKnownSettings` or `globalKnownCollectionSettings`
5. Add new profile/defaults fields to `YoloaiConfig` struct and the YAML node walker in `config/config.go`
6. CLI `config get/set/reset` commands in `internal/cli/config.go` route via `config.IsGlobalKey()`
7. Defaults config at `~/.yoloai/library/defaults/config.yaml`, global config at `~/.yoloai/library/config.yaml`
8. The library no longer tracks setup ceremony (`setup_complete` removed, D60); the CLI's first-run-tip flag lives in `~/.yoloai/cli/state.yaml` via `cliutil.LoadCLIState()`/`SaveCLIState()`

**Add a new runtime backend:**
1. Create `runtime/<name>/` package
2. Implement the `runtime.Runtime` interface (see `runtime/podman/` for an example that embeds an existing backend and overrides only what differs)
3. Declare a package-level `var descriptor = runtime.BackendDescriptor{...}` and call `runtime.Register(name, factory, descriptor)` in your package's `init()` function. The descriptor's `Type` must match the registration name. Return the same `descriptor` from your `Descriptor()` method.
4. Add a blank import in the appropriate platform file (`client.go` for all platforms, or a `_linux.go` / `_darwin.go` file for platform-specific backends)
5. Backend is selectable via `--backend` flag (on new/build/setup) or `backend` config. Lifecycle commands read backend from sandbox `environment.json`

**Add capability checks for a backend:**
1. Create `runtime/<name>/caps.go` with `HostCapability` constructors
2. Implement `RequiredCapabilities(isolation)` and `SupportedIsolationModes()` on your Runtime
3. Shared capability constructors live in `runtime/caps/common.go`

**Add MCP tools for outer agents:**
1. Add tool registration in `internal/mcpsrv/tools.go`
2. Tool handlers use `sandbox.Engine` for all sandbox operations

## Testing

Three tiers of tests, each requiring progressively more infrastructure:

| Tier | Build tag | Requires | Run with |
|------|-----------|----------|----------|
| Unit | *(none)* | Nothing | `make test` / `go test ./...` |
| Integration | `integration` | Docker daemon + `yoloai-base` image | `make integration` |
| E2E | `e2e` | Docker daemon + compiled binary | `make e2e` |

**Run all checks (preferred — gofmt, lint, tidy, unit tests):**
```
make check
```

**Run unit tests:**
```
go test ./...
```

**Run integration tests (requires Docker):**
```
make integration
# or: go test -tags=integration -v -count=1 -timeout=10m ./sandbox/ ./runtime/docker/ ./internal/cli/
```

**Run E2E tests (requires Docker, compiles binary first):**
```
make e2e
# or: go test -tags=e2e -v -count=1 -timeout=15m ./test/e2e/
```

**Run tests for a specific package:**
```
go test ./sandbox/
go test ./runtime/docker/
go test ./agent/
```

**Build:**
```
go build -o yoloai ./cmd/yoloai/
```

**Lint:**
```
golangci-lint run
```

### Test infrastructure

**`internal/testutil/`** — shared non-test package importable by all test files:
- `git.go`: `InitGitRepo`, `GitAdd`, `GitCommit`, `GitRevParse`, `RunGit`, `WriteFile`
- `fixtures.go`: `GoProject(t)` (git-initialized Go project), `AuxDir(t, name)`, `MultiFileProject(t)`
- `home.go`: `IsolatedHome(t)` — sets `HOME` to `t.TempDir()`
- `wait.go`: `WaitForActive`, `WaitForStopped` — poll `rt.Inspect` instead of sleeping

**`TestMain` pattern** — each integration package has `integration_main_test.go` that connects to Docker and calls `EnsureSetup` once before any tests run. Per-test `integrationSetup(t)` still uses `IsolatedHome(t)` for sandbox isolation, but subsequent `EnsureImage` calls hit the image cache.

Integration test files:
- `sandbox/integration_test.go` — full sandbox lifecycle; includes `TestIntegration_AgentStubWorkflow` (agent runs in container → diff → apply)
- `runtime/docker/docker_integration_test.go` — Docker runtime operations
- `internal/cli/integration_test.go` — CLI commands via Cobra

E2E test files (`test/e2e/`):
- `helpers_test.go` — `TestMain` (builds binary), `runYoloai`, `e2eSetup`
- `workflow_test.go` — `new` / `ls` / `destroy` lifecycle
- `json_test.go` — `--json` output shape contracts
- `error_test.go` — exit codes and error messages
- `bugreport_test.go` — bug report generation

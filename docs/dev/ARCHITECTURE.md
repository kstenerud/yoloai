# Architecture

Code navigation guide for the yoloAI codebase. Focused on the implemented code, not aspirational features (see [design/](../design/README.md) for those).

## Package Map

```
yoloai.go                → Orchestration spine: Client (Run, Diff, Apply, Stop, Destroy, ...)
system_client.go         → Orchestration spine: SystemClient (DiskUsage, Prune, Build, Check)
runtime_imports_linux.go → Linux-specific backend registration (containerd)
cmd/yoloai/              → Binary entry point
internal/agent/          → Agent plugin definitions (Aider, Claude, Codex, Gemini, OpenCode, test, idle)
internal/cli/            → Cobra command tree and CLI plumbing
internal/config/         → Configuration loading, profiles, migration, state, path utilities
internal/extension/      → User-defined custom commands (YAML-based extensions)
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
internal/sandbox/             → Core logic: Manager, create, lifecycle, clone, inspect
internal/sandbox/archetype/   → Project archetype detection (devcontainer, compose, apple, simple) + .yoloai.yaml + VS Code workspace injection
internal/sandbox/patch/       → Git-format diff/apply machinery for :copy, :overlay, and :rw modes
internal/sandbox/store/       → On-disk sandbox state: paths, Meta record, SandboxState completion flags
internal/testutil/            → Shared test helpers (git, fixtures, home isolation, container polling) — test use only
internal/workspace/           → Workspace utilities (copy, git, safety checks, tags)
internal/yoerrors/            → Typed error sentinels exported via the yoloai package
test/e2e/                → End-to-end tests against the compiled binary (build tag: e2e)
```

Public Go surface is the **`yoloai` package only** (W-L12). Every other Go package lives under `internal/` and is unreachable from external imports by the Go compiler itself. `cmd/yoloai` is the binary entry, not a library.

Dependency direction (W-L8 + W-L12 shape): `cmd/yoloai` → `internal/cli` → `yoloai` (Client + SystemClient) → `internal/sandbox` + `internal/sandbox/patch` + `internal/sandbox/store` + `internal/runtime`; `internal/sandbox` → `internal/sandbox/archetype` + `internal/sandbox/store` + `internal/runtime` + `internal/agent` + `internal/workspace`; `internal/sandbox/patch` → `internal/sandbox` + `internal/sandbox/store`; `internal/sandbox/store` is a leaf (only imports stdlib, `internal/config`, other `internal/*`); `internal/agent` stands alone; `internal/mcpsrv` depends on `yoloai` (not `sandbox.Manager`). The CLI doesn't reach into `internal/sandbox/*` or `internal/runtime/*` for orchestration — every command goes through `yoloai.Client` or `yoloai.SystemClient`. The `withRuntime`/`withManager` helpers were removed in W-L10. A small set of CLI commands still call `newRuntime(ctx, backend)` directly for multi-backend enumeration (`ls`, `system doctor`, `system info`, `sandbox <name> allow`) and for the backend-scoped `system tart` subtree. Depguard (`.golangci.yml`) enforces the boundary going forward.

## File Index

### `yoloai.go` / `system_client.go` / `runtime_imports_linux.go`

| File | Purpose |
|------|---------|
| `yoloai.go` | Orchestration spine — `Client` and its sandbox-scoped methods (`Run`, `Diff`, `Apply`, `Stop`, `Destroy`, `List`, `Inspect`, `Attach`, `Exec`, `Clone`, `Reset`, `Restart`, `Create`, `Start`, plus the diff/apply variants). Registers Docker, Podman, Seatbelt, and Tart backends via blank imports. |
| `system_client.go` | Orchestration spine — `SystemClient` for admin/cross-backend operations (`DiskUsage`, `Prune`, `Build`, `Check`). Reached via `Client.System()` or `NewSystemClient(layout)`. Iterates registered backends internally. |
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
| `client.go` | `NewRuntime`, `WithClient`, `NewSystemClient`, `AttachToSandboxByName`, `ResolveBackend`/`ResolveBackendForSandbox`, `ResolveAgent`, `ResolveModel`, `ResolveProfile`, `Coalesce`, `FlagStr`, `SandboxErrorHint`. The chokepoint that turns CLI flags into a `yoloai.Client` / `SystemClient`. |
| `layout.go` | `Layout()` / `SetRootLayout` — resolves `$HOME/.yoloai` once at process startup and threads the `config.Layout` downward. The only sanctioned `os.UserHomeDir` call site (allowlisted in `.golangci.yml`). |
| `name.go` | `ResolveName` and `EnvSandboxName` — sandbox-name resolution from args / `YOLOAI_SANDBOX`. |
| `json.go` | `--json` flag helpers: `JSONEnabled`, `WriteJSON`, `WriteJSONError`, `EffectiveYes`. |
| `streams.go`, `terminal.go` | `IOStreams()` (PTY-sized terminal binding for Client.Attach) and `SetTerminalTitle` (OSC-0 + tmux window rename). |
| `lowdisk.go` | `WarnIfLowDisk`, `HumanBytes` — free-space courtesy check used by new/clone/build/disk. |
| `groups.go` | Exported help group IDs (`GroupLifecycle`, `GroupWorkflow`, `GroupSandboxTools`, `GroupAdmin`) — referenced by every subpackage that registers a top-level command. |
| `buildinfo.go` | `SetBuildInfo` + `Version`/`Commit`/`Date` globals — set once in `Execute()` so subpackages (bug-report, version) can read build metadata without threading it through cobra calls. |
| `check.go` | `CheckBackend` — best-effort backend-availability probe used by `ls`, `system doctor`, `system tart` gating. |
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
| `list.go`, `log.go`, `exec.go` | The actual `sandbox list`/`log`/`exec` implementations. |
| `info.go`, `prompt.go`, `vscode.go`, `unlock.go`, `bugreport.go` | Other per-sandbox subcommands. `bugreport.go` exports `WriteSandboxSectionsForFlag` so `root.go`'s `--bugreport` finalizer can include sandbox sections. |
| `allow.go`, `allowed.go`, `deny.go`, `network.go` | Network allowlist commands and their shared helpers (`loadIsolatedMeta`, `saveNetworkAllowlist`, `tryLivePatchNetwork`). |
| `ansi.go` | `stripANSI` — used by `log.go` and `bugreport.go` for readable terminal output. |

#### Admin (`internal/cli/system/`)

`yoloai system …` parent and every subcommand. Largest cluster
after sandboxcmd.

| File | Purpose |
|------|---------|
| `system.go` | Parent + `build` + `setup` wiring. |
| `build`/`prune`/`check`/`disk`/`doctor`/`info`/`setup`/`completion` and `backends_agents.go` | Each system subcommand. |
| `tart/` | Nested subpackage — the one sanctioned importer of `internal/runtime/tart` (depguard `cli-backend-scope` rule). |

#### Single-command subpackages

| Subpackage | Command | Notes |
|------------|---------|-------|
| `mcp/` | `yoloai mcp serve|proxy` | MCP server + proxy. |
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
| `server.go` | `Server` struct backed by `sandbox.Manager`. `New()` creates the MCP server with registered tool handlers. |
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
| `dirs.go` | `YoloaiDir()`, `SandboxesDir()`, `ProfilesDir()`, `CacheDir()`, `DefaultsDir()`, `DefaultsConfigPath()`, `ExtensionsDir()` — centralized path helpers. Shared sandbox subdirectory name constants (`BackendDirName`, `BinDirName`, `TmuxDirName`, `AgentRuntimeDirName`). |
| `profile.go` | `ProfileConfig`, `LoadProfile()`, `MergedConfig` — profile loading, inheritance chain resolution, config merging. |
| `migration.go` | `CheckDefaultsDir()` — verifies `~/.yoloai/defaults/` exists; returns migration instructions if not. |
| `state.go` | `LoadState()`, `SaveState()` — read/write `~/.yoloai/state.yaml` containing global state like `setup_complete`. |
| `pathutil.go` | `ExpandPath()` — tilde and `${VAR}` expansion for config paths. |
| `errors.go` | `UsageError` (exit 2), `ConfigError` (exit 3), sentinel errors. |
| `names.go` | Name validation constants and regex (`ValidNameRe`, `MaxNameLength`). |
| `encode.go` | Safe ASCII caret encoding for filesystem-safe names (keys and values). |
| `homedir.go` | Home directory detection and expansion, respecting `SUDO_USER` when running under sudo. |

### `extension/`

| File | Purpose |
|------|---------|
| `extension.go` | Loading, validation, and types for user-defined custom commands stored as YAML files in `~/.yoloai/extensions/`. |

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

### `sandbox/`

| File | Purpose |
|------|---------|
| `manager.go` | `Manager` struct — central orchestrator. Holds a `runtime.Runtime`. `EnsureSetup()` / `EnsureSetupNonInteractive()` for first-run auto-setup (dirs, resources, image, config). |
| `create.go` | `Create()` — top-level sandbox creation orchestrator. Calls prepare, seed, and instance-building phases. |
| `create_prepare.go` | `prepareSandboxState()` — profile resolution, directory validation, safety checks, workdir copy, git baseline, meta persistence. |
| `create_instance.go` | `buildAndStart()` — constructs `runtime.InstanceConfig` from sandbox state, builds mounts, creates and starts the container/VM instance. |
| `create_seed.go` | `seedSandbox()` — copies seed files, agent config files, home config into sandbox directories. |
| `lifecycle.go` | `Start()`, `Stop()`, `Destroy()`, `Reset()` — sandbox lifecycle. `recreateContainer()` and `relaunchAgent()` for restart scenarios. `resetInPlace()` for in-place resets (default). `clearCacheAndFiles()` clears cache/files dirs. `clearOverlayDirs()` clears upper/ovlwork for instant `:overlay` reset. |
| `clone.go` | `Clone()` — clone an existing sandbox with new name. `CloneOptions` configures source/dest. |
| `inspect.go` | `DetectStatus()` — reads `agent-status.json` written by in-container monitor; falls back to exec-based tmux query for old sandboxes. `InspectSandbox()`, `ListSandboxes()` — metadata + live status. |
| `parse.go` | `ParseDirArg()` — parses `path:copy`, `path:overlay`, `path:rw`, `path:force` suffixes into `DirSpec`. |
| `context.go` | `GenerateContext()` — builds markdown description of sandbox environment (dirs, network, resources). `WriteContextFiles()` — writes `context.md` and inlines context into agent instruction file (e.g., `CLAUDE.md`). |
| `agent_files.go` | `copyAgentFiles()` — copies files from host into sandbox `agent-runtime/` per `agent_files` config. Handles string/list forms, exclusion patterns, first-run tracking via `store.SandboxState`. |
| `profile_build.go` | Profile image building — ensures profile images are built in dependency order (base → parent → child). Staleness detection. |
| `prune.go` | `PruneTempFiles()` — cleans up stale `/tmp/yoloai-*` temporary directories. |
| `tags.go` | Git tag information — `TagInfo`, commit matching helpers, delegates to `workspace` package. |
| `fileutil.go` | Path expansion wrappers (delegates to `config.ExpandPath` and `internal/fileutil`). JSON read/write helpers. |
| `keychain_darwin.go` | macOS Keychain integration — reads credentials via `security find-generic-password` when seed files are missing. |
| `keychain_other.go` | Non-macOS stub for Keychain integration. |
| `lock_unix.go` / `lock_windows.go` | Per-sandbox advisory file locking via flock(2) on Unix/macOS; no-op on Windows. `AcquireLock` is exported for use from `sandbox/patch`. |
| `setup.go` | `RunSetup()`, `runNewUserSetup()` — interactive first-run setup: tmux config, default backend, default agent. |
| `confirm.go` | `Confirm()` — context-aware y/N interactive prompt with stdin/context racing. |
| `errors.go` | Sentinel errors (`ErrSandboxNotFound`, `ErrSandboxExists`, `ErrNoChanges`, etc.); `ErrSandboxNotFound` is re-exported from `sandbox/store` for visibility. |
| `*_test.go` | Unit tests for each file above. `integration_test.go` has the `integration` build tag. |

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
| `apply.go` | `ApplyAll()`, `GeneratePatch()`, `GenerateFormatPatch()`, `GenerateMultiPatch()`, `GenerateOverlayPatch()`, `GenerateUncommittedDiff()`, `UpdateOverlayBaselineToHEAD()`, `UpdateOverlayBaseline()`, `AdvanceBaseline()`, `AdvanceBaselineTo()`, `HasUncommittedChanges()`, `ListCommitsBeyondBaseline()`, `ResolveRef()`, `ResolveRefs()`. |

### `sandbox/store/`

On-disk sandbox state — paths, metadata, and creation-completion flags. Leaf subpackage; imports only stdlib, `config`, `internal/fileutil`, `internal/yoerrors`. Imported by `sandbox/`, `sandbox/patch/`, and most external callers.

| File | Purpose |
|------|---------|
| `paths.go` | `EncodePath()` / `DecodePath()` — caret encoding for filesystem-safe names. `InstanceName()`, `Dir()`, `WorkDir()`, `RequireSandboxDir()`. `OverlayUpperDir()` / `OverlayOvlworkDir()` for `:overlay` mount paths. Centralized filename constants (`EnvironmentFile`, `RuntimeConfigFile`, `AgentStatusFile`, `SandboxStateFile`, etc.) and `ErrSandboxNotFound`. |
| `meta.go` | `Meta` / `WorkdirMeta` / `DirMeta` structs, `SaveMeta()` / `LoadMeta()` — sandbox metadata persistence as `environment.json` (legacy: `meta.json`). `Meta.Backend` records which runtime backend was used. |
| `sandbox_state.go` | `SandboxState` struct, `LoadSandboxState()`, `SaveSandboxState()` — per-sandbox runtime state (`sandbox-state.json`, legacy: `state.json`). Tracks `agent_files_initialized` and `on_create_commands_done`. Separate from `Meta` which is immutable after creation. |

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
High-level public API for library consumers. Wraps `sandbox.Manager` and `runtime.Runtime`. Provides `Run()`, `Diff()`, `Apply()`, `List()`, `Inspect()`, `Stop()`, `Destroy()`. Configured via `Options` (backend, logger, output, input). `RunOptions` mirrors CLI flags for `yoloai new`.

### `sandbox.Manager`
Central orchestrator. Holds a `runtime.Runtime`, backend name, logger, and I/O streams. All sandbox operations go through it: `Create()`, `Start()`, `Stop()`, `Destroy()`, `Reset()`, `Clone()`, `Inspect()`, `List()`, `EnsureSetup()`. The backend name is stored so it can be persisted in `Meta` at sandbox creation time.

### `store.Meta` / `store.WorkdirMeta` / `store.DirMeta`
Persisted as `environment.json` (legacy: `meta.json`) in each sandbox dir. Records creation-time state: agent, model, profile, workdir path/mode/baseline SHA, auxiliary directories (via `Directories` field), network mode/allow, ports, resources, mounts, backend. Each directory (workdir and aux dirs) has its own `DirMeta` with host path, mount path, mode, and baseline SHA. Lives in `sandbox/store`.

### `store.SandboxState`
Per-sandbox runtime state persisted as `sandbox-state.json` (legacy: `state.json`). Tracks mutable state like `agent_files_initialized` (boolean). Separate from `Meta` which is immutable after creation. Lives in `sandbox/store`.

### `sandbox.CreateOptions` / `sandbox.DirSpec`
Internal parameters for `Manager.Create()`. `DirSpec` specifies a directory path, mount mode (copy/overlay/rw/ro), and per-directory safety acks (`AllowDirty`, `AllowDangerousPath`). `CreateOptions` includes name, workdir `DirSpec`, auxiliary `DirSpec` list, agent, model, prompt, network, ports, profile, replace, passthrough args. The **public** creation surface is `yoloai.CreateOptions` (root `create_options.go`); `Client.Create` maps it onto this internal struct via `toInternal()`. A dirty workdir surfaces as `*yoerrors.DirtyWorkdirError` (never an in-library prompt — D24).

### `patch.DiffOptions` / `patch.DiffResult`
Input/output for `patch.GenerateDiff()` / `patch.GenerateMultiDiff()`. Supports path filtering and stat-only mode. `DiffResult` carries the diff text, workdir, mode, and empty flag. Lives in `sandbox/patch`.

### `sandbox.CloneOptions`
Parameters for `Manager.Clone()`. Source and destination sandbox names, optional overrides.

### `archetype.Archetype` / `archetype.DevcontainerConfig` / `archetype.YoloAIProjectConfig`
Project-archetype detection types. Lives in `sandbox/archetype`.

### `agent.Definition`
Describes an agent's commands (interactive/headless), prompt delivery mode, API key env vars (`APIKeyEnvVars`), auth hint env vars (`AuthHintEnvVars`), `AuthOptional` flag, seed files, state directory, tmux submit sequence, `ReadyPattern`, model flag/aliases/prefixes (`ModelPrefixes`), network allowlist, `ContextFile` (native instruction file for sandbox context injection), `AgentFilesExclude` (glob patterns to skip when copying agent_files), and `IdleSupport`. Built-in: `aider`, `claude`, `codex`, `gemini`, `opencode`, `test`, and `idle`.

### `runtime.Runtime`
Pluggable runtime interface for backend abstraction. Core methods: `Setup()`, `IsReady()`, `Create()`, `Start()`, `Stop()`, `Remove()`, `Inspect()`, `Exec()`, `GitExec()`, `InteractiveExec()`, `Prune()`, `Close()`, `Logs()`, `DiagHint()`, `Descriptor()`, `TmuxSocket()`, `AttachCommand()`, `PrepareAgentCommand()`. Static per-backend facts (Name, BaseModeName, AgentProvisionedByBackend, SupportedIsolationModes, Capabilities) are bundled into `BackendDescriptor` returned by `Descriptor()`. Allows swapping container/VM backends.

### `runtime.BackendDescriptor`
Bundles each backend's static facts: `Name`, `BaseModeName`, `AgentProvisionedByBackend`, `SupportedIsolationModes`, `Capabilities`. Returned by `Runtime.Descriptor()`; values are compile-time constants per backend.

### `runtime.BackendCaps`
Declares what features a backend supports: `NetworkIsolation`, `OverlayDirs`, `CapAdd`, `HostFilesystem`. Embedded in `BackendDescriptor`. Used by sandbox logic to gate features without string-comparing backend names.

### `runtime.Factory` / Backend Registry
`Factory` is `func(context.Context) (Runtime, error)`. Backends register `(Factory, BackendDescriptor)` tuples via `runtime.Register(name, factory, descriptor)` in their `init()` functions. `runtime.New(ctx, name)` creates a Runtime by name; `runtime.Descriptor(name)` returns the static descriptor without instantiating; `runtime.Descriptors()` enumerates all registered descriptors. `runtime.Available()` lists registered backend names. Platform-specific backends (containerd on Linux, tart/seatbelt on macOS) only register on their supported platforms.

### Optional Runtime interfaces
Five optional interfaces extend the core Runtime with backend-specific capabilities. Callers use type assertion or helper functions (`ResolveCopyMountFor`, `RequiredCapabilitiesFor`) that fall back to documented defaults when the backend doesn't implement them.

- `UsernsProvider` — Podman rootless `keep-id` mode.
- `WorkDirSetup` — Tart VM-local workdir copies.
- `StdioExecer` — Docker/Podman MCP-proxy stdio bridging.
- `CopyMountResolver` — Seatbelt and Tart rewrite `:copy` mount paths; container backends use the host path unchanged.
- `IsolationCapabilityProvider` — Docker/Podman/containerd declare per-isolation prerequisite capabilities; tart/seatbelt have none.

### `runtime.InstanceConfig`
Configuration for `Runtime.Create()`. Describes image, working directory, mounts, ports, network mode, resource limits, capabilities, devices, user namespace mode, and container runtime (OCI/Kata).

### `caps.HostCapability`
Describes one system prerequisite: check function, permanence assessment, and remediation steps. Used by `system doctor` and `system check`.

### `caps.BackendReport`
Full check result for one (backend, isolation mode) combination. Contains `CheckResult` list, `Availability` classification (Ready/NeedsSetup/Unavailable), and optional `InitErr` when backend creation fails.

### `caps.Environment`
Host context: `IsRoot`, `IsWSL2`, `InContainer`, `KVMGroup`. Detected once per invocation, passed to all capability checks.

## Command → Code Map

| CLI Command | Entry Point | Core Logic |
|-------------|-------------|------------|
| `yoloai new` | `cli/lifecycle/new.go:NewNewCmd` | `yoloai.Client.Create()` (→ `sandbox.Manager.Create` in `sandbox/create.go`) |
| `yoloai attach` | `cli/workflow/attach.go:NewAttachCmd` | `yoloai.Client.Attach()` (PTY-sized via `cliutil.IOStreams`) |
| `yoloai diff` | `cli/workflow/diff.go:NewDiffCmd` | `yoloai.Client.GenerateMultiPatch()` (→ `sandbox.GenerateMultiDiff` in `sandbox/diff.go`) |
| `yoloai apply` | `cli/workflow/apply.go:NewApplyCmd` | `yoloai.Client.GeneratePatch()` / `ApplyPatch()` / `GenerateFormatPatch()` |
| `yoloai start` | `cli/lifecycle/start.go:NewStartCmd` | `yoloai.Client.Start()` |
| `yoloai stop` | `cli/lifecycle/stop.go:NewStopCmd` | `yoloai.Client.Stop()` |
| `yoloai destroy` | `cli/lifecycle/destroy.go:NewDestroyCmd` | `yoloai.Client.Destroy()` |
| `yoloai reset` | `cli/lifecycle/reset.go:NewResetCmd` | `yoloai.Client.Reset()` |
| `yoloai restart` | `cli/lifecycle/restart.go:NewRestartCmd` | `yoloai.Client.Restart()` |
| `yoloai clone` | `cli/lifecycle/clone.go:NewCloneCmd` | `yoloai.Client.Clone()` |
| `yoloai system info` | `cli/system/info.go` | Version, paths, disk usage, backend availability |
| `yoloai system agents` | `cli/system/backends_agents.go` | Lists agent definitions from `agent` package |
| `yoloai system backends` | `cli/system/backends_agents.go` | Probes each backend via `cliutil.CheckBackend` |
| `yoloai system build` | `cli/system/system.go` | `yoloai.SystemClient.Build()` |
| `yoloai system setup` | `cli/system/system.go` + `cli/system/setup.go` (wizard) | `yoloai.SystemClient.Setup()` |
| `yoloai system check` | `cli/system/check.go` | `yoloai.SystemClient.Check()` |
| `yoloai system doctor` | `cli/system/doctor.go` | `caps.RunChecks()` + `caps.FormatDoctor()` in `runtime/caps/` |
| `yoloai system prune` | `cli/system/prune.go` | `yoloai.SystemClient.Prune()` |
| `yoloai system tart` | `cli/system/tart/tart.go` | `tart.RuntimeVersion` / `tart.CopyRuntimeToVM()` / `tart.Runtime.ListVMs` / `tart.Runtime.DeleteVM` |
| `yoloai system completion` | `cli/system/completion.go` | Cobra's built-in completion generators |
| `yoloai mcp serve` | `cli/mcp/mcp.go` | `mcpsrv.New()` — MCP server on stdio |
| `yoloai mcp proxy` | `cli/mcp/mcp.go` | MCP proxy through sandbox |
| `yoloai sandbox list` | `cli/sandboxcmd/list.go` | `yoloai.Client.List()` (→ `sandbox.ListSandboxes` in `sandbox/inspect.go`) |
| `yoloai sandbox <name> info` | `cli/sandboxcmd/info.go` | `yoloai.Client.Inspect()` |
| `yoloai sandbox <name> log` | `cli/sandboxcmd/log.go` | Structured JSONL log display with filtering |
| `yoloai sandbox <name> exec` | `cli/sandboxcmd/exec.go` | `yoloai.Client.Exec()` |
| `yoloai sandbox <name> prompt` | `cli/sandboxcmd/prompt.go` | Reads `prompt.txt` from sandbox dir |
| `yoloai sandbox <name> bugreport` | `cli/sandboxcmd/bugreport.go` | Forensic diagnostic collection (calls `bugreport.Write*`) |
| `yoloai sandbox <name> allow` | `cli/sandboxcmd/allow.go` | `sandbox.PatchConfigAllowedDomains()` + `tryLivePatchNetwork` ipset update |
| `yoloai sandbox <name> allowed` | `cli/sandboxcmd/allowed.go` | `sandbox.LoadMeta()` — pure file read |
| `yoloai sandbox <name> deny` | `cli/sandboxcmd/deny.go` | `sandbox.PatchConfigAllowedDomains()` + `tryLivePatchNetwork` ipset removal |
| `yoloai sandbox <name> vscode` | `cli/sandboxcmd/vscode.go` | Builds `vscode-remote://attached-container+<hex>/<path>` URI and launches `code --folder-uri` |
| `yoloai files` | `cli/workflow/files.go:NewFilesCmd` | File exchange via `~/.yoloai/sandboxes/<name>/files/` |
| `yoloai baseline` | `cli/workflow/baseline.go:NewBaselineCmd` | `yoloai.Client.AdvanceBaseline()` / `ResolveCommitRefs()` |
| `yoloai profile` | `cli/profile/profile.go:NewCmd` | Profile create/list/info/delete |
| `yoloai help` | `cli/helpcmd/help.go:NewCmd` | Topic-based help with embedded markdown |
| `yoloai config get/set/reset` | `cli/configcmd/config.go:NewCmd` | `config.{Get,Update,Delete}…Config…` routed via `config.IsGlobalKey()` |
| `yoloai ls` / `log` / `exec` / `vscode` | `cli/sandboxcmd/aliases.go` | Shortcuts that delegate to the matching `sandbox <verb>` impl in the same subpackage |
| `yoloai x` | `cli/xcmd/x.go:NewCmd` | User-defined extensions from `~/.yoloai/extensions/` |
| `yoloai version` | `cli/versioncmd/version.go:NewCmd` | Prints build-time version info (reads `cliutil.Version` etc.) |

## Data Flow

### Sandbox Creation (`yoloai new`)

```
NewNewCmd (cli/lifecycle/new.go)
  → cliutil.WithClient (cli/cliutil/client.go)
    → yoloai.Client.Create  (wraps sandbox.Manager.Create in sandbox/create.go)
      → EnsureSetup: create dirs, seed resources, build image, write config.yaml
      → prepareSandboxState (sandbox/create_prepare.go):
          resolve profile chain → applyConfigDefaults
          → resolveAndApplyArchetype: load .yoloai.yaml → CLI > yaml > auto-detect priority
              → devcontainer: load devcontainer.json → merge ports/env/mounts, set workspaceFolder
              → compose: set isolation=container-privileged, archetypeDockerDRequired=true
              → transparency output (signals, bullets, suppress hint)
          → ParseDirArg → validate name/agent/workdir/auxdirs → safety checks
          → :copy dirs: copyDir (cp -rp / clonefile on macOS) → removeGitDirs → gitBaseline
          → :overlay dirs: createOverlayDirs (upper/ovlwork in sandbox state)
          → seedSandbox (sandbox/create_seed.go):
              copySeedFiles → copyAgentFiles → ensureContainerSettings → seedHomeConfig
          → readPrompt → resolveModel → buildAgentCommand
          → SaveMeta (environment.json) → SaveSandboxState (sandbox-state.json)
          → write prompt.txt, log.txt, runtime-config.json
          → WriteContextFiles (context.md + agent instruction file)
      → buildAndStart (sandbox/create_instance.go):
          createSecretsDir (config env vars + API keys from host env)
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
- `yoloai.go`: imports docker, podman, seatbelt, tart
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
Manager.Start (sandbox/lifecycle.go)
  → DetectStatus (sandbox/inspect.go): runtime.Inspect + status file read
  → StatusActive: no-op
  → StatusDone/Failed: relaunchAgent via tmux respawn-pane
  → StatusStopped: runtime.Start
  → StatusRemoved: recreateContainer (rebuild state from environment.json via runtime.Create + runtime.Start)
```

### Capability Detection (`yoloai system doctor`)

```
newSystemDoctorCmd (cli/system_doctor.go)
  → caps.DetectEnvironment() — probe host (root, WSL2, container, KVM group)
  → For each registered backend:
    → runtime.New(ctx, name) — try to connect
    → rt.RequiredCapabilities(baseMode) — get base checks
    → For each rt.SupportedIsolationModes():
      → rt.RequiredCapabilities(mode) — get mode-specific checks
    → caps.RunChecks(capabilities, env) → []CheckResult
    → caps.ComputeAvailability(results) → Ready/NeedsSetup/Unavailable
  → caps.FormatDoctor(reports, output) — render table with fix instructions
```

## Host Directory Layout

```
~/.yoloai/
├── config.yaml              # Global config (tmux_conf, model_aliases)
├── state.yaml               # Global state (setup_complete)
├── defaults/
│   ├── config.yaml          # User defaults (agent, model, isolation, etc.; active when no --profile)
│   └── tmux.conf            # Optional; written by setup when baked-in tmux config is in use
├── profiles/
│   └── <name>/
│       ├── config.yaml      # Profile settings (merged over baked-in defaults, not over defaults/)
│       ├── Dockerfile       # Optional; FROM yoloai-base
│       └── tmux.conf        # Optional tmux config override
├── extensions/
│   └── <name>.yaml          # User-defined extension commands
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
4. If the command needs a `yoloai.Client`, use `cliutil.WithClient`; for cross-backend admin work use `cliutil.NewSystemClient()`. Do not construct `sandbox.Manager` or raw `runtime.Runtime` directly — `.golangci.yml` enforces this via depguard / forbidigo.

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
1. Modify `Meta` / `DirMeta` in `sandbox/meta.go`
2. Update `prepareSandboxState()` in `sandbox/create_prepare.go` where meta is populated
3. Update any consumers that `LoadMeta()` and use the changed fields (e.g., diff, apply, inspect, reset)

**Change diff/apply behavior:**
1. Diff generation: `sandbox/diff.go`
2. Patch generation and application: `sandbox/apply.go`
3. CLI presentation: `internal/cli/diff.go` and `internal/cli/apply.go`

**Change container creation (mounts, networking):**
1. Mount construction: `buildAndStart()` in `sandbox/create_instance.go` → populates `runtime.MountSpec`
2. Container config: `buildAndStart()` → builds `runtime.InstanceConfig`
3. Port parsing: `parsePortBindings()` in `sandbox/create.go` → populates `runtime.PortMapping`
4. Runtime creation: `runtime.Create()` dispatched to the active backend

**Change sandbox status detection:**
1. `DetectStatus()` in `sandbox/inspect.go` — reads `agent-status.json` from sandbox dir (written by status monitor), falls back to legacy `status.json` then `runtime.Exec()` for old sandboxes
2. Status constants are in the same file

**Change config handling:**
1. Defaults config: `LoadDefaultsConfig()` (baked-in + defaults/config.yaml merge) / `UpdateConfigFields()` / `DeleteConfigField()` in `config/config.go`
2. Baked-in defaults: `LoadBakedInDefaults()` in `config/config.go`; add new default values to `DefaultConfigYAML` in `config/defaults.go`
3. Global config: `LoadGlobalConfig()` / `UpdateGlobalConfigFields()` / `DeleteGlobalConfigField()` in `config/config.go`
4. `IsGlobalKey()` determines routing — add new global keys to `globalKnownSettings` or `globalKnownCollectionSettings`
5. Add new profile/defaults fields to `YoloaiConfig` struct and the YAML node walker in `config/config.go`
6. CLI `config get/set/reset` commands in `internal/cli/config.go` route via `config.IsGlobalKey()`
7. Defaults config at `~/.yoloai/defaults/config.yaml`, global config at `~/.yoloai/config.yaml`
8. Global state like `setup_complete` is stored in `~/.yoloai/state.yaml` via `LoadState()`/`SaveState()` in `config/state.go`

**Add a new runtime backend:**
1. Create `runtime/<name>/` package
2. Implement the `runtime.Runtime` interface (see `runtime/podman/` for an example that embeds an existing backend and overrides only what differs)
3. Declare a package-level `var descriptor = runtime.BackendDescriptor{...}` and call `runtime.Register(name, factory, descriptor)` in your package's `init()` function. The descriptor's `Name` must match the registration name. Return the same `descriptor` from your `Descriptor()` method.
4. Add a blank import in the appropriate platform file (`yoloai.go` for all platforms, or a `_linux.go` / `_darwin.go` file for platform-specific backends)
5. Backend is selectable via `--backend` flag (on new/build/setup) or `backend` config. Lifecycle commands read backend from sandbox `environment.json`

**Add capability checks for a backend:**
1. Create `runtime/<name>/caps.go` with `HostCapability` constructors
2. Implement `RequiredCapabilities(isolation)` and `SupportedIsolationModes()` on your Runtime
3. Shared capability constructors live in `runtime/caps/common.go`

**Add MCP tools for outer agents:**
1. Add tool registration in `internal/mcpsrv/tools.go`
2. Tool handlers use `sandbox.Manager` for all sandbox operations

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

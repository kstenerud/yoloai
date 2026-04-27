# Architecture

Code navigation guide for the yoloAI codebase. Focused on the implemented code, not aspirational features (see [design/](../design/README.md) for those).

## Package Map

```
yoloai.go                → High-level public Go API (Client, Run, Diff, Apply)
runtime_imports_linux.go → Linux-specific backend registration (containerd)
cmd/yoloai/              → Binary entry point
agent/                   → Agent plugin definitions (Aider, Claude, Codex, Gemini, OpenCode, test, idle)
config/                  → Configuration loading, profiles, migration, state, path utilities
extension/               → User-defined custom commands (YAML-based extensions)
internal/cli/            → Cobra command tree and CLI plumbing
internal/fileutil/       → os.MkdirAll / os.WriteFile wrappers for sudo ownership fix
internal/mcpsrv/         → MCP server exposing sandbox operations as tools for outer agents
internal/testutil/       → Shared test helpers (git, fixtures, home isolation, container polling) — test use only
runtime/                 → Pluggable runtime interface, backend registry, isolation mapping, exec helpers
runtime/caps/            → Capability detection system (host probing, fix instructions, doctor output)
runtime/docker/          → Docker implementation of runtime.Runtime
runtime/podman/          → Podman implementation (embeds Docker runtime, overrides socket discovery and rootless support)
runtime/tart/            → Tart (macOS VM) implementation of runtime.Runtime
runtime/seatbelt/        → Seatbelt (macOS sandbox-exec) implementation of runtime.Runtime
runtime/containerd/      → Containerd implementation of runtime.Runtime (Kata Containers VM isolation)
runtime/monitor/         → Embedded monitoring scripts shared across all backends (sandbox-setup.py, status-monitor.py, diagnose-idle.sh)
sandbox/                 → Core logic: create, lifecycle, diff, apply, clone, inspect
workspace/               → Workspace utilities (copy, git, safety checks, tags)
test/e2e/                → End-to-end tests against the compiled binary (build tag: e2e)
```

Dependency direction: `cmd/yoloai` → `cli` → `sandbox` + `runtime`; `sandbox` → `runtime` + `agent` + `workspace`; `agent` stands alone. Top-level `yoloai.go` → `sandbox` + `runtime` + `config` (public API for library consumers).

## File Index

### `yoloai.go` / `runtime_imports_linux.go`

| File | Purpose |
|------|---------|
| `yoloai.go` | High-level public Go API: `Client`, `New()`, `Run()`, `Diff()`, `Apply()`, `List()`, `Inspect()`, `Stop()`, `Destroy()`. Registers Docker, Podman, Seatbelt, and Tart backends via blank imports. |
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

| File | Purpose |
|------|---------|
| `root.go` | Root Cobra command, global flags (`-v`, `-q`, `--no-color`, `--json`), `Execute()` with exit code mapping, bug report system. |
| `commands.go` | `registerCommands()` — registers all subcommands. Also contains `newNewCmd`, `newLsAliasCmd`, `newLogAliasCmd`, `newExecAliasCmd`, `newCompletionCmd`, `newVersionCmd`, and `attachToSandbox`/`waitForTmux` helpers. |
| `config.go` | `yoloai config get/set/reset` — read, write, and delete config values via dotted paths. Routes global keys (tmux_conf, model_aliases) to `~/.yoloai/config.yaml`, other keys to `~/.yoloai/defaults/config.yaml`. |
| `files.go` | `yoloai files put/get/ls/rm/path` — bidirectional file exchange between host and sandbox via `~/.yoloai/sandboxes/<name>/files/`. Uses name-first dispatch. |
| `profile.go` | `yoloai profile create/list/info/delete` — profile management commands. |
| `restart.go` | `yoloai restart` — stop + start a sandbox, with `--attach` and `--resume` support. |
| `system.go` | `yoloai system` parent command with `build` and `setup` subcommands. |
| `system_info.go` | `yoloai system info` — displays version, paths, disk usage, and backend availability. |
| `system_prune.go` | `yoloai system prune` — remove orphaned backend resources and stale temp files. |
| `system_check.go` | `yoloai system check` — verifies prerequisites for CI/CD pipelines. Checks backend connectivity, base image, and agent credentials. Exits 1 on failure. |
| `system_doctor.go` | `yoloai system doctor` — shows what backends and isolation modes are available on the current machine, with fix instructions for missing prerequisites. |
| `system_mcp.go` | `yoloai mcp serve` — starts the orchestration MCP server on stdio. `yoloai mcp proxy` — proxies an inner MCP server through a sandbox. |
| `system_runtime.go` | `yoloai system runtime` — manage Apple simulator runtime base images (pre-create, list, remove). |
| `help.go` | `yoloai help [topic]` — topic-based help system with embedded markdown content and fuzzy suggestion. |
| `help/` | Embedded markdown help topic files (quickstart, agents, workflow, config, etc.). |
| `apply.go` | `yoloai apply` — apply changes back to host. Squash and selective-commit modes, `--export` for `.patch` files. |
| `attach.go` | `yoloai attach` — attach to sandbox tmux session via `runtime.InteractiveExec`. |
| `diff.go` | `yoloai diff` — show agent changes. Supports `--stat`, `--log`, commit refs, and ranges. |
| `destroy.go` | `yoloai destroy` — stop and remove sandbox with confirmation logic. |
| `sandbox_cmd.go` | `yoloai sandbox` parent command with name-first dispatch. Subcommands: list, info, log, exec, prompt, allow, allowed, deny, bugreport, clone. |
| `sandbox_info.go` | `yoloai sandbox <name> info` — display sandbox config, meta, and status. |
| `sandbox_clone.go` | `yoloai clone` / `yoloai sandbox clone` — clone a sandbox. |
| `sandbox_prompt.go` | `yoloai sandbox <name> prompt` — show the prompt text for a sandbox. |
| `sandbox_bugreport.go` | `yoloai sandbox <name> bugreport` — forensic bug report tool collecting static diagnostics. |
| `exec.go` | `yoloai sandbox exec` — run commands inside a running sandbox container. |
| `sandbox_network.go` | Shared helpers for network allowlist management: `loadIsolatedMeta`, `saveNetworkAllowlist`, `tryLivePatchNetwork`. |
| `sandbox_allow.go` | `yoloai <name> allow <domain>...` — add domains to an isolated sandbox's allowlist at runtime. |
| `sandbox_allowed.go` | `yoloai <name> allowed` — show the current network allowlist for a sandbox. |
| `sandbox_deny.go` | `yoloai <name> deny <domain>...` — remove domains from the allowlist. |
| `list.go` | `yoloai sandbox list` / `yoloai ls` — tabular listing of all sandboxes with status. |
| `log.go` | `yoloai sandbox log` / `yoloai log` — structured JSONL log display with level/source/since filtering, follow mode, raw JSONL output, and agent terminal output. |
| `info.go` | Backend and agent info commands (`system backends`, `system agents`), plus shared helpers (`checkBackend`, `knownBackends`). |
| `json.go` | `--json` flag helpers: `jsonEnabled()`, `writeJSON()`, `writeJSONError()`, `requireYesForJSON()`. Used by all commands for JSON output mode. |
| `reset.go` | `yoloai reset` — re-copy workdir from host, reset git baseline. |
| `start.go` | `yoloai start` — start a stopped sandbox (recreates container if removed). |
| `stop.go` | `yoloai stop` — stop a running sandbox. |
| `envname.go` | `resolveName()` — resolves sandbox name from args or `YOLOAI_SANDBOX` env var. |
| `helpers.go` | `withRuntime()`, `withManager()` — create Runtime / Manager for command handlers. `resolveBackend()` reads `--backend` flag (on new/build/setup). `resolveBackendForSandbox()` reads `environment.json`. `resolveBackendFromConfig()` reads config default. |
| `x.go` | `yoloai x` — extension runner. Loads user-defined extension YAML files, builds Cobra commands dynamically. |
| `ansi.go` | ANSI escape sequence and control character stripping for readable log output. |
| `bugreport_writer.go` | Bug report section writers and sanitization helpers. Shared by `--bugreport` flag and sandbox bugreport command. |
| `logger.go` | Multi-sink slog logger. Fans records to N independent sinks (stderr, cli.jsonl, bugreport temp file). |
| `runtime_imports_linux.go` | Linux-specific runtime imports (containerd registration). |

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
| `registry.go` | Backend registry. `Register()` called by each backend's `init()`. `New()` creates a Runtime by name. `Available()` lists registered backends. |
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
| `diff.go` | `GenerateDiff()`, `GenerateMultiDiff()`, `GenerateDiffStat()`, `GenerateCommitDiff()`, `ListCommitsWithStats()` — diff generation for `:copy`, `:overlay`, and `:rw` modes. |
| `apply.go` | `GeneratePatch()`, `CheckPatch()`, `ApplyPatch()` — squash apply via `git apply`. `GenerateFormatPatch()`, `ApplyFormatPatch()` — per-commit apply via `git am`. `ApplyAll()` — apply all directories. `ListCommitsBeyondBaseline()`, `AdvanceBaseline()`, `AdvanceBaselineTo()`. |
| `inspect.go` | `DetectStatus()` — reads `agent-status.json` written by in-container monitor; falls back to exec-based tmux query for old sandboxes. `InspectSandbox()`, `ListSandboxes()` — metadata + live status. |
| `meta.go` | `Meta` / `WorkdirMeta` / `DirMeta` structs, `SaveMeta()` / `LoadMeta()` — sandbox metadata persistence as `environment.json` (legacy: `meta.json`). `Meta.Backend` records which runtime backend was used. |
| `paths.go` | `EncodePath()` / `DecodePath()` — caret encoding for filesystem-safe names. `InstanceName()`, `Dir()`, `WorkDir()`, `RequireSandboxDir()`. `OverlayUpperDir()` / `OverlayOvlworkDir()` for `:overlay` mount paths. Centralized filename constants (`EnvironmentFile`, `RuntimeConfigFile`, `AgentStatusFile`, `SandboxStateFile`, etc.). |
| `parse.go` | `ParseDirArg()` — parses `path:copy`, `path:overlay`, `path:rw`, `path:force` suffixes into `DirSpec`. |
| `context.go` | `GenerateContext()` — builds markdown description of sandbox environment (dirs, network, resources). `WriteContextFiles()` — writes `context.md` and inlines context into agent instruction file (e.g., `CLAUDE.md`). |
| `sandbox_state.go` | `SandboxState` struct, `LoadSandboxState()`, `SaveSandboxState()` — per-sandbox runtime state (`sandbox-state.json`, legacy: `state.json`). Tracks `agent_files_initialized`. |
| `agent_files.go` | `copyAgentFiles()` — copies files from host into sandbox `agent-runtime/` per `agent_files` config. Handles string/list forms, exclusion patterns, first-run tracking via `SandboxState`. |
| `profile_build.go` | Profile image building — ensures profile images are built in dependency order (base → parent → child). Staleness detection. |
| `prune.go` | `PruneTempFiles()` — cleans up stale `/tmp/yoloai-*` temporary directories. |
| `tags.go` | Git tag information — `TagInfo`, commit matching helpers, delegates to `workspace` package. |
| `fileutil.go` | Path expansion wrappers (delegates to `config.ExpandPath` and `internal/fileutil`). JSON read/write helpers. |
| `keychain_darwin.go` | macOS Keychain integration — reads credentials via `security find-generic-password` when seed files are missing. |
| `keychain_other.go` | Non-macOS stub for Keychain integration. |
| `lock_unix.go` / `lock_windows.go` | Per-sandbox advisory file locking via flock(2) on Unix/macOS; no-op on Windows. |
| `setup.go` | `RunSetup()`, `runNewUserSetup()` — interactive first-run setup: tmux config, default backend, default agent. |
| `confirm.go` | `Confirm()` — context-aware y/N interactive prompt with stdin/context racing. |
| `errors.go` | Sentinel errors (`ErrSandboxNotFound`, `ErrSandboxExists`, `ErrNoChanges`, etc.). |
| `*_test.go` | Unit tests for each file above. `integration_test.go` has the `integration` build tag. |

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

### `sandbox.Meta` / `sandbox.WorkdirMeta` / `sandbox.DirMeta`
Persisted as `environment.json` (legacy: `meta.json`) in each sandbox dir. Records creation-time state: agent, model, profile, workdir path/mode/baseline SHA, auxiliary directories (via `Directories` field), network mode/allow, ports, resources, mounts, backend. Each directory (workdir and aux dirs) has its own `DirMeta` with host path, mount path, mode, and baseline SHA.

### `sandbox.SandboxState`
Per-sandbox runtime state persisted as `sandbox-state.json` (legacy: `state.json`). Tracks mutable state like `agent_files_initialized` (boolean). Separate from `Meta` which is immutable after creation.

### `sandbox.CreateOptions` / `sandbox.DirSpec`
All parameters for `Manager.Create()`. `DirSpec` specifies a directory path and its mount mode (copy/overlay/rw/force). `CreateOptions` includes name, workdir `DirSpec`, auxiliary `DirSpec` list, agent, model, prompt, network, ports, profile, replace, attach, passthrough args.

### `sandbox.DiffOptions` / `sandbox.DiffResult`
Input/output for `GenerateDiff()` / `GenerateMultiDiff()`. Supports path filtering and stat-only mode. `DiffResult` carries the diff text, workdir, mode, and empty flag.

### `sandbox.CloneOptions`
Parameters for `Manager.Clone()`. Source and destination sandbox names, optional overrides.

### `agent.Definition`
Describes an agent's commands (interactive/headless), prompt delivery mode, API key env vars (`APIKeyEnvVars`), auth hint env vars (`AuthHintEnvVars`), `AuthOptional` flag, seed files, state directory, tmux submit sequence, `ReadyPattern`, model flag/aliases/prefixes (`ModelPrefixes`), network allowlist, `ContextFile` (native instruction file for sandbox context injection), `AgentFilesExclude` (glob patterns to skip when copying agent_files), and `IdleSupport`. Built-in: `aider`, `claude`, `codex`, `gemini`, `opencode`, `test`, and `idle`.

### `runtime.Runtime`
Pluggable runtime interface for backend abstraction. Methods: `Setup()`, `IsReady()`, `Create()`, `Start()`, `Stop()`, `Remove()`, `Inspect()`, `Exec()`, `GitExec()`, `InteractiveExec()`, `Prune()`, `Close()`, `Logs()`, `DiagHint()`, `Capabilities()`, `AgentProvisionedByBackend()`, `ResolveCopyMount()`, `Name()`, `TmuxSocket()`, `AttachCommand()`, `RequiredCapabilities()`, `SupportedIsolationModes()`, `BaseModeName()`, `PrepareAgentCommand()`. Allows swapping container/VM backends.

### `runtime.BackendCaps`
Declares what features a backend supports: `NetworkIsolation`, `OverlayDirs`, `CapAdd`, `HostFilesystem`. Used by sandbox logic to gate features without string-comparing backend names.

### `runtime.Factory` / Backend Registry
`Factory` is `func(context.Context) (Runtime, error)`. Backends register via `runtime.Register()` in their `init()` functions. `runtime.New(ctx, name)` creates a Runtime by name. `runtime.Available()` lists registered backends. Platform-specific backends (containerd on Linux, tart/seatbelt on macOS) only register on their supported platforms.

### `runtime.UsernsProvider` / `runtime.WorkDirSetup`
Optional interfaces. `UsernsProvider` is implemented by Podman for rootless `keep-id` mode. `WorkDirSetup` is implemented by Tart for VM-local workdir copies.

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
| `yoloai new` | `cli/commands.go:newNewCmd` | `sandbox.Manager.Create()` in `sandbox/create.go` |
| `yoloai attach` | `cli/attach.go:newAttachCmd` | `runtime.InteractiveExec` + `tmux attach` via `cli/commands.go:attachToSandbox` |
| `yoloai diff` | `cli/diff.go:newDiffCmd` | `sandbox.GenerateMultiDiff()` in `sandbox/diff.go` |
| `yoloai apply` | `cli/apply.go:newApplyCmd` | `sandbox.GeneratePatch()` / `ApplyPatch()` / `ApplyFormatPatch()` in `sandbox/apply.go` |
| `yoloai start` | `cli/start.go:newStartCmd` | `sandbox.Manager.Start()` in `sandbox/lifecycle.go` |
| `yoloai stop` | `cli/stop.go:newStopCmd` | `sandbox.Manager.Stop()` in `sandbox/lifecycle.go` |
| `yoloai destroy` | `cli/destroy.go:newDestroyCmd` | `sandbox.Manager.Destroy()` in `sandbox/lifecycle.go` |
| `yoloai reset` | `cli/reset.go:newResetCmd` | `sandbox.Manager.Reset()` in `sandbox/lifecycle.go` |
| `yoloai restart` | `cli/restart.go:newRestartCmd` | `sandbox.Manager.Stop()` + `sandbox.Manager.Start()` in `sandbox/lifecycle.go` |
| `yoloai clone` | `cli/sandbox_clone.go` | `sandbox.Manager.Clone()` in `sandbox/clone.go` |
| `yoloai system info` | `cli/system_info.go:newSystemInfoCmd` | Version, paths, disk usage, backend availability |
| `yoloai system agents` | `cli/info.go:newSystemAgentsCmd` | Lists agent definitions from `agent` package |
| `yoloai system backends` | `cli/info.go:newSystemBackendsCmd` | Probes each backend via `runtime.New()` |
| `yoloai system build` | `cli/system.go:newSystemBuildCmd` | `runtime.Setup()` via active backend |
| `yoloai system setup` | `cli/system.go:newSystemSetupCmd` | `sandbox.Manager.RunSetup()` in `sandbox/setup.go` |
| `yoloai system check` | `cli/system_check.go:newSystemCheckCmd` | Verifies backend connectivity, base image, agent credentials |
| `yoloai system doctor` | `cli/system_doctor.go:newSystemDoctorCmd` | `caps.RunChecks()` + `caps.FormatDoctor()` in `runtime/caps/` |
| `yoloai system prune` | `cli/system_prune.go:newSystemPruneCmd` | `runtime.Prune()` + `sandbox.PruneTempFiles()` |
| `yoloai system runtime` | `cli/system_runtime.go` | `tart.RuntimeVersion` / `tart.CopyRuntimeToVM()` |
| `yoloai mcp serve` | `cli/system_mcp.go` | `mcpsrv.New()` — MCP server on stdio |
| `yoloai mcp proxy` | `cli/system_mcp.go` | MCP proxy through sandbox |
| `yoloai sandbox list` | `cli/list.go:newSandboxListCmd` | `sandbox.ListSandboxes()` in `sandbox/inspect.go` |
| `yoloai sandbox <name> info` | `cli/sandbox_info.go` | `sandbox.InspectSandbox()` in `sandbox/inspect.go` |
| `yoloai sandbox <name> log` | `cli/log.go:runLog` | Structured JSONL log display with filtering |
| `yoloai sandbox <name> exec` | `cli/exec.go` | `runtime.InteractiveExec` into running container |
| `yoloai sandbox <name> prompt` | `cli/sandbox_prompt.go` | Reads `prompt.txt` from sandbox dir |
| `yoloai sandbox <name> bugreport` | `cli/sandbox_bugreport.go` | Forensic diagnostic collection |
| `yoloai sandbox <name> allow` | `cli/sandbox_allow.go` | `sandbox.PatchConfigAllowedDomains()` + `tryLivePatchNetwork` ipset update |
| `yoloai sandbox <name> allowed` | `cli/sandbox_allowed.go` | `sandbox.LoadMeta()` — pure file read |
| `yoloai sandbox <name> deny` | `cli/sandbox_deny.go` | `sandbox.PatchConfigAllowedDomains()` + `tryLivePatchNetwork` ipset removal |
| `yoloai files` | `cli/files.go:newFilesCmd` | File exchange via `~/.yoloai/sandboxes/<name>/files/` |
| `yoloai profile` | `cli/profile.go:newProfileCmd` | Profile create/list/info/delete |
| `yoloai help` | `cli/help.go:newHelpCmd` | Topic-based help with embedded markdown |
| `yoloai config get` | `cli/config.go:newConfigGetCmd` | `config.GetEffectiveConfig()` / `config.GetConfigValue()` |
| `yoloai config set` | `cli/config.go:newConfigSetCmd` | `config.UpdateConfigFields()` or `config.UpdateGlobalConfigFields()` via `config.IsGlobalKey()` |
| `yoloai config reset` | `cli/config.go:newConfigResetCmd` | `config.DeleteConfigField()` or `config.DeleteGlobalConfigField()` via `config.IsGlobalKey()` |
| `yoloai ls` | `cli/commands.go:newLsAliasCmd` | Shortcut for `sandbox list` |
| `yoloai log` | `cli/commands.go:newLogAliasCmd` | Shortcut for `sandbox log` |
| `yoloai exec` | `cli/commands.go:newExecAliasCmd` | Shortcut for `sandbox exec` |
| `yoloai x` | `cli/x.go:newExtensionCmd` | User-defined extensions from `~/.yoloai/extensions/` |
| `yoloai completion` | `cli/commands.go:newCompletionCmd` | Cobra's built-in completion generators |
| `yoloai version` | `cli/commands.go:newVersionCmd` | Prints build-time version info |

## Data Flow

### Sandbox Creation (`yoloai new`)

```
newNewCmd (cli/commands.go)
  → withRuntime (cli/helpers.go)
    → Manager.Create (sandbox/create.go)
      → EnsureSetup: create dirs, seed resources, build image, write config.yaml
      → prepareSandboxState (sandbox/create_prepare.go):
          resolve profile chain → ParseDirArg → validate name/agent/workdir/auxdirs → safety checks
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
1. Create `internal/cli/<command>.go` with `newXxxCmd() *cobra.Command`
2. Register it in `internal/cli/commands.go:registerCommands()` under the appropriate group
3. If the command needs a Manager, use `withManager()` or `withRuntime()` from `helpers.go`

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
3. `EmbeddedTmuxConf()` in `runtime/docker/resources.go` is called by the setup wizard to write `defaults/tmux.conf` when the baked-in tmux config is in use

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
3. Call `runtime.Register(name, factory)` in your package's `init()` function
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

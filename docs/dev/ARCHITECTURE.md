# Architecture

Code navigation guide for the yoloAI codebase. Focused on the implemented code, not aspirational features (see [design/](../design/README.md) for those).

## Package Map

```
cmd/yoloai/              → Binary entry point
agent/                   → Agent plugin definitions (Aider, Claude, Codex, Gemini, OpenCode, test, shell)
config/                  → Configuration loading, profiles, migration, state, path utilities
extension/               → User-defined custom commands (YAML-based extensions)
internal/cli/            → Cobra command tree and CLI plumbing
runtime/                 → Pluggable runtime interface (backend-agnostic types and errors)
runtime/docker/          → Docker implementation of runtime.Runtime
runtime/tart/            → Tart (macOS VM) implementation of runtime.Runtime
runtime/seatbelt/        → Seatbelt (macOS sandbox-exec) implementation of runtime.Runtime
sandbox/                 → Core logic: create, lifecycle, diff, apply, inspect
workspace/               → Workspace utilities (copy, git, safety checks)
```

Dependency direction: `cmd/yoloai` → `cli` → `sandbox` + `runtime`; `sandbox` → `runtime` + `agent`; `agent` stands alone.

## File Index

### `cmd/yoloai/`

| File | Purpose |
|------|---------|
| `main.go` | Entry point. Sets up signal context, calls `cli.Execute`, exits with code. Build-time version/commit/date vars. |

### `agent/`

| File | Purpose |
|------|---------|
| `agent.go` | `Definition` struct and built-in agent registry (`aider`, `claude`, `codex`, `gemini`, `opencode`, `test`, `shell`). `GetAgent()` lookup. |
| `agent_test.go` | Unit tests for agent definitions. |

### `internal/cli/`

| File | Purpose |
|------|---------|
| `root.go` | Root Cobra command, global flags (`-v`, `-q`, `--no-color`, `--json`), `Execute()` with exit code mapping (JSON errors to stderr when `--json`). |
| `commands.go` | `registerCommands()` — registers all subcommands. Also contains `newNewCmd`, `newLsAliasCmd`, `newLogAliasCmd`, `newExecAliasCmd`, `newCompletionCmd`, `newVersionCmd`, and `attachToSandbox`/`waitForTmux` helpers. |
| `config.go` | `yoloai config get/set/reset` — read, write, and delete config values via dotted paths. Routes global keys (tmux_conf, model_aliases) to `~/.yoloai/config.yaml`, profile keys to `~/.yoloai/profiles/base/config.yaml`. |
| `files.go` | `yoloai files put/get/ls/rm/path` — bidirectional file exchange between host and sandbox via `~/.yoloai/sandboxes/<name>/files/`. |
| `profile.go` | `yoloai profile create/list/info/delete` — profile management commands. |
| `restart.go` | `yoloai restart` — stop + start a sandbox, with `--attach` and `--resume` support. |
| `system_prune.go` | `yoloai system prune` — remove orphaned backend resources and stale temp files. |
| `help.go` | `yoloai help [topic]` — topic-based help system with embedded markdown content. |
| `ansi.go` | ANSI escape code stripping helpers for terminal output processing. |
| `help/` | Embedded markdown help topic files (quickstart, agents, workflow, config, etc.). |
| `apply.go` | `yoloai apply` — apply changes back to host. Squash and selective-commit modes, `--export` for `.patch` files. |
| `attach.go` | `yoloai attach` — attach to sandbox tmux session via `runtime.InteractiveExec`. |
| `diff.go` | `yoloai diff` — show agent changes. Supports `--stat`, `--log`, commit refs, and ranges. |
| `destroy.go` | `yoloai destroy` — stop and remove sandbox with confirmation logic. |
| `system.go` | `yoloai system` parent command with `build` and `setup` subcommands. |
| `system_info.go` | `yoloai system info` — displays version, paths, disk usage, backend availability. |
| `sandbox_cmd.go` | `yoloai sandbox` parent command grouping sandbox inspection subcommands. |
| `sandbox_info.go` | `yoloai sandbox info` — display sandbox config, meta, and status. |
| `exec.go` | `yoloai sandbox exec` — run commands inside a running sandbox container. |
| `sandbox_network.go` | `yoloai sandbox network` parent command for network allowlist management. |
| `sandbox_network_add.go` | `yoloai sandbox network add` — add domains to a network-isolated sandbox at runtime. |
| `sandbox_network_list.go` | `yoloai sandbox network list` — show allowed domains for a sandbox. |
| `sandbox_network_remove.go` | `yoloai sandbox network remove` — remove domains from the allowlist. |
| `list.go` | `yoloai sandbox list` / `yoloai ls` — tabular listing of all sandboxes with status. |
| `log.go` | `yoloai sandbox log` / `yoloai log` — display sandbox session log (log.txt). |
| `info.go` | Backend and agent info commands (`system backends`, `system agents`), plus shared helpers (`checkBackend`, `knownBackends`). |
| `json.go` | `--json` flag helpers: `jsonEnabled()`, `writeJSON()`, `writeJSONError()`, `requireYesForJSON()`. Used by all commands for JSON output mode. |
| `json_test.go` | Unit tests for JSON helpers. |
| `reset.go` | `yoloai reset` — re-copy workdir from host, reset git baseline. |
| `start.go` | `yoloai start` — start a stopped sandbox (recreates container if removed). |
| `stop.go` | `yoloai stop` — stop a running sandbox. |
| `envname.go` | `resolveName()` — resolves sandbox name from args or `YOLOAI_SANDBOX` env var. |
| `helpers.go` | `withRuntime()`, `withManager()` — create Runtime / Manager for command handlers. `resolveBackend()` reads `--backend` flag (on new/build/setup). `resolveBackendForSandbox()` reads `environment.json`. `resolveBackendFromConfig()` reads config default. |
| `x.go` | `yoloai x` — extension runner. Loads user-defined extension YAML files, builds Cobra commands dynamically. |
| `x_test.go` | Tests for extension runner. |
| `envname_test.go` | Tests for name resolution. |

### `config/`

| File | Purpose |
|------|---------|
| `config.go` | `YoloaiConfig` struct, `LoadConfig()`, `UpdateConfigFields()`, `DeleteConfigField()`, global config loading/updating. `IsGlobalKey()` routes config commands. YAML comment-preserving via `yaml.Node`. |
| `config_aliases.go` | Model alias resolution helpers for config-level aliases. |
| `profile.go` | `ProfileConfig`, `LoadProfile()`, `MergedConfig` — profile loading, inheritance chain resolution, config merging. |
| `migration.go` | `MigrateIfNeeded()`, `MigrateGlobalSettings()` — handles migration from old config structure. |
| `state.go` | `LoadState()`, `SaveState()` — read/write `~/.yoloai/state.yaml` containing global state like `setup_complete`. |
| `pathutil.go` | `ExpandPath()` — tilde and `${VAR}` expansion for config paths. |
| `errors.go` | `UsageError` (exit 2), `ConfigError` (exit 3), sentinel errors. |
| `names.go` | Name validation constants and regex (`ValidNameRe`, `MaxNameLength`). |
| `defaults.go` | Default YAML content for config files (`DefaultConfigYAML`, `DefaultGlobalConfigYAML`). |

### `runtime/`

| File | Purpose |
|------|---------|
| `runtime.go` | `Runtime` interface — pluggable backend abstraction. Generic types: `MountSpec`, `PortMapping`, `InstanceConfig`, `InstanceInfo`, `ExecResult`. Sentinel errors: `ErrNotFound`, `ErrNotRunning`. |

### `runtime/docker/`

| File | Purpose |
|------|---------|
| `docker.go` | `DockerRuntime` struct — implements `Runtime` interface, wraps Docker SDK. `NewDockerRuntime()` with daemon ping. |
| `build.go` | `EnsureImage()` — builds `yoloai-base` image. `NeedsBuild()` / `RecordBuildChecksum()` for rebuild detection. Tar context creation, build output streaming. Moved from old `internal/docker/build.go`. |
| `resources.go` | `//go:embed` for Dockerfile, entrypoint.sh, tmux.conf. Imports `sandbox-setup.py` and `status-monitor.py` from `runtime/monitor`. `SeedResources()` writes them to `~/.yoloai/profiles/base/` respecting user customizations. |
| `resources/Dockerfile` | Container Dockerfile (embedded at compile time). |
| `resources/entrypoint.sh` | Root container entrypoint script (embedded at compile time). Handles UID/GID remapping, iptables, overlayfs, then invokes `sandbox-setup.py`. |
| `resources/tmux.conf` | Default tmux config (embedded at compile time). |
| `prune.go` | `Prune()` — finds and removes orphaned `yoloai-*` Docker containers and dangling images. |
| `build_test.go` | Unit tests for build/seed logic. |
| `docker_integration_test.go` | Integration tests requiring Docker daemon. Build tag: `integration`. |

### `runtime/tart/`

| File | Purpose |
|------|---------|
| `tart.go` | `Runtime` struct — implements `Runtime` interface, shells out to `tart` CLI. VM lifecycle via `tart clone/run/stop/delete`, exec via `tart exec`. PID file + `tart list` for process management. |
| `build.go` | `EnsureImage()` — pulls Cirrus Labs macOS base image, provisions dev tools via `tart exec` (Homebrew, Node.js, Xcode CLI tools, tmux, git, jq, ripgrep). Supports `defaults.tart.image` config override. |
| `resources.go` | `//go:embed` for tmux.conf. |
| `platform.go` | Platform detection helpers (macOS, Apple Silicon). Testable via variable overrides. |
| `tart_test.go` | Unit tests for arg building, error mapping, network flags, mount symlinks. |

### `runtime/seatbelt/`

| File | Purpose |
|------|---------|
| `seatbelt.go` | `Runtime` struct — implements `Runtime` interface using macOS `sandbox-exec`. PID file management, background process, per-sandbox tmux socket. |
| `profile.go` | `GenerateProfile()` — builds SBPL (Seatbelt Profile Language) profiles from `InstanceConfig`. Maps mounts to file-access rules, controls network. |
| `build.go` | `EnsureImage()` / `ImageExists()` — verifies prerequisites (sandbox-exec, tmux, jq). No image to build. |
| `resources.go` | `//go:embed` for tmux.conf. |
| `platform.go` | Platform detection (macOS only, no Apple Silicon requirement). Testable via variable override. |
| `resources/tmux.conf` | Default tmux config (embedded at compile time). |
| `seatbelt_test.go` | Unit tests for profile generation, platform detection, tmux socket injection. |

### `sandbox/`

| File | Purpose |
|------|---------|
| `manager.go` | `Manager` struct — central orchestrator. Holds a `runtime.Runtime`. `EnsureSetup()` / `EnsureSetupNonInteractive()` for first-run auto-setup (dirs, resources, image, config). |
| `create.go` | `Create()` — full sandbox creation: validate, safety checks, copy workdir, git baseline, seed files, build mounts, launch container. Also contains `launchContainer()`, `buildMounts()`, `createSecretsDir()` (writes config env vars + API keys from host env to /run/secrets/), `copySeedFiles()`, `createOverlayDirs()` (creates upper/ovlwork dirs for `:overlay` mode). On macOS, when a seed file with `KeychainService` set is not found on disk, the system falls back to reading credentials from the macOS Keychain (via `security find-generic-password`). Platform-specific code is in `keychain_darwin.go` / `keychain_other.go`. |
| `lifecycle.go` | `Start()`, `Stop()`, `Destroy()`, `Reset()` — sandbox lifecycle. `recreateContainer()` and `relaunchAgent()` for restart scenarios. `resetInPlace()` for `--no-restart` resets. `clearOverlayDirs()` clears upper/ovlwork for instant `:overlay` reset. |
| `diff.go` | `GenerateDiff()`, `GenerateDiffStat()`, `GenerateCommitDiff()`, `ListCommitsWithStats()` — diff generation for `:copy`, `:overlay`, and `:rw` modes. |
| `apply.go` | `GeneratePatch()`, `CheckPatch()`, `ApplyPatch()` — squash apply via `git apply`. `GenerateFormatPatch()`, `ApplyFormatPatch()` — per-commit apply via `git am`. `ListCommitsBeyondBaseline()`, `AdvanceBaseline()`, `AdvanceBaselineTo()`. |
| `inspect.go` | `DetectStatus()` — reads bind-mounted status file written by in-container monitor; falls back to exec-based tmux query for old sandboxes. `InspectSandbox()`, `ListSandboxes()` — metadata + live status. `execInContainer()` helper uses `runtime.Exec()`. |
| `meta.go` | `Meta` / `WorkdirMeta` structs, `SaveMeta()` / `LoadMeta()` — sandbox metadata persistence as `environment.json` (legacy: `meta.json`). `Meta.Backend` records which runtime backend was used to create the sandbox. |
| `paths.go` | `EncodePath()` / `DecodePath()` — caret encoding for filesystem-safe names. `InstanceName()` (and deprecated alias `ContainerName()`), `Dir()`, `WorkDir()`, `RequireSandboxDir()`. `OverlayUpperDir()` / `OverlayOvlworkDir()` for `:overlay` mount paths. Centralized filename constants (`EnvironmentFile`, `RuntimeConfigFile`, `AgentStatusFile`, `SandboxStateFile`, etc.). |
| `parse.go` | `ParseDirArg()` — parses `path:copy`, `path:overlay`, `path:rw`, `path:force` suffixes into `DirArg`. |
| `context.go` | `GenerateContext()` — builds markdown description of sandbox environment (dirs, network, resources). `WriteContextFiles()` — writes `context.md` to sandbox dir and inlines context into agent instruction file (e.g., `CLAUDE.md`). |
| `sandbox_state.go` | `SandboxState` struct, `LoadSandboxState()`, `SaveSandboxState()` — per-sandbox runtime state (`sandbox-state.json`, legacy: `state.json`). Tracks `agent_files_initialized`. |
| `agent_files.go` | `copyAgentFiles()` — copies files from host into sandbox `agent-runtime/` per `agent_files` config. Handles string/list forms, exclusion patterns, first-run tracking via `SandboxState`. |
| `profile_build.go` | Profile image building — Docker-specific logic for building `yoloai-<profile>` images from profile Dockerfiles. Staleness detection. |
| `prune.go` | `PruneTempFiles()` — cleans up stale `/tmp/yoloai-*` temporary directories. |
| `keychain_darwin.go` | macOS Keychain integration — reads credentials via `security find-generic-password` when seed files are missing. |
| `keychain_other.go` | Non-macOS stub for Keychain integration. |
| `setup.go` | `RunSetup()`, `runNewUserSetup()` — interactive tmux configuration setup. Classifies user's tmux config, prompts for preferences. |
| `confirm.go` | `Confirm()` — simple y/N interactive prompt. |
| `errors.go` | Sentinel errors (`ErrSandboxNotFound`, `ErrSandboxExists`, etc.). |
| `*_test.go` | Unit tests for each file above. `integration_test.go` has the `integration` build tag. |

## Key Types

### `sandbox.Manager`
Central orchestrator. Holds a `runtime.Runtime`, backend name, logger, and I/O streams. All sandbox operations go through it: `Create()`, `Start()`, `Stop()`, `Destroy()`, `Reset()`, `EnsureSetup()`. The backend name is stored so it can be persisted in `Meta` at sandbox creation time.

### `sandbox.Meta` / `sandbox.WorkdirMeta` / `sandbox.DirMeta`
Persisted as `environment.json` (legacy: `meta.json`) in each sandbox dir. Records creation-time state: agent, model, profile, workdir path/mode/baseline SHA, auxiliary directories (via `Directories` field), network mode/allow, ports, resources, mounts, backend. Each directory (workdir and aux dirs) has its own `DirMeta` with host path, mount path, mode, and baseline SHA.

### `sandbox.SandboxState`
Per-sandbox runtime state persisted as `sandbox-state.json` (legacy: `state.json`). Tracks mutable state like `agent_files_initialized` (boolean). Separate from `Meta` which is immutable after creation.

### `sandbox.AgentFilesConfig`
Parsed from `agent_files` in config. Two forms: string (base directory) or list (explicit file paths). Used by `copyAgentFiles()` during sandbox creation.

### `sandbox.ProfileConfig` / `sandbox.MergedConfig`
Profile configuration loaded from `profile.yaml`. `MergedConfig` is the result of merging the inheritance chain (base → profiles → CLI flags). Used to resolve effective settings for sandbox creation.

### `sandbox.ResourceLimits`
CPU and memory limits (`CPUs string`, `Memory string`). Stored in `Meta`, applied via `runtime.InstanceConfig`.

### `sandbox.CreateOptions`
All parameters for `Manager.Create()`. Mirrors CLI flags: name, workdir, auxiliary directories (`AuxDirs`), agent, model, prompt, network, ports, replace, attach, passthrough args.

### `sandbox.DiffOptions` / `sandbox.DiffResult`
Input/output for `GenerateDiff()`. Supports path filtering and stat-only mode. `DiffResult` carries the diff text, workdir, mode, and empty flag.

### `agent.Definition`
Describes an agent's commands (interactive/headless), prompt delivery mode, API key env vars (`APIKeyEnvVars`), auth hint env vars (`AuthHintEnvVars`), `AuthOptional` flag, seed files, state directory, tmux submit sequence, `ReadyPattern`, model flag/aliases/prefixes (`ModelPrefixes`), network allowlist, `ContextFile` (native instruction file for sandbox context injection), and `AgentFilesExclude` (glob patterns to skip when copying agent_files). Built-in: `aider`, `claude`, `codex`, `gemini`, `opencode`, `test`, and `shell`.

### `runtime.Runtime`
Pluggable runtime interface for backend abstraction. Methods: `Create()`, `Start()`, `Stop()`, `Remove()`, `Inspect()`, `Exec()`, `InteractiveExec()`, `EnsureImage()`, `Close()`. Allows swapping container/VM backends.

### `runtime.InstanceConfig`
Configuration for `Runtime.Create()`. Describes image, command, working directory, environment variables, mounts, ports, network mode, and resource limits for a container or VM instance.

### `runtime.DockerRuntime`
Docker implementation of `Runtime` interface. Wraps Docker SDK client. Defined in `runtime/docker/`.

### `runtime.TartRuntime`
Tart (macOS VM) implementation of `Runtime` interface. Shells out to `tart` CLI for all operations. PID-based process management with `tart list` cross-check. VirtioFS mounts with symlink path mapping. Defined in `runtime/tart/`.

## Command → Code Map

| CLI Command | Entry Point | Core Logic |
|-------------|-------------|------------|
| `yoloai new` | `cli/commands.go:newNewCmd` | `sandbox.Manager.Create()` in `sandbox/create.go` |
| `yoloai attach` | `cli/attach.go:newAttachCmd` | `runtime.InteractiveExec` + `tmux attach` via `cli/commands.go:attachToSandbox` |
| `yoloai diff` | `cli/diff.go:newDiffCmd` | `sandbox.GenerateDiff()` in `sandbox/diff.go` |
| `yoloai apply` | `cli/apply.go:newApplyCmd` | `sandbox.GeneratePatch()` / `ApplyPatch()` / `ApplyFormatPatch()` in `sandbox/apply.go` |
| `yoloai start` | `cli/start.go:newStartCmd` | `sandbox.Manager.Start()` in `sandbox/lifecycle.go` |
| `yoloai stop` | `cli/stop.go:newStopCmd` | `sandbox.Manager.Stop()` in `sandbox/lifecycle.go` |
| `yoloai destroy` | `cli/destroy.go:newDestroyCmd` | `sandbox.Manager.Destroy()` in `sandbox/lifecycle.go` |
| `yoloai reset` | `cli/reset.go:newResetCmd` | `sandbox.Manager.Reset()` in `sandbox/lifecycle.go` |
| `yoloai system info` | `cli/system_info.go:newSystemInfoCmd` | Version, paths, disk usage, backend availability |
| `yoloai system agents` | `cli/info.go:newSystemAgentsCmd` | Lists agent definitions from `agent` package |
| `yoloai system backends` | `cli/info.go:newSystemBackendsCmd` | Probes each backend via `newRuntime()` |
| `yoloai system build` | `cli/system.go:newSystemBuildCmd` | `runtime.EnsureImage()` via active backend (`runtime/docker/build.go` or `runtime/tart/build.go`) |
| `yoloai system setup` | `cli/system.go:newSystemSetupCmd` | `sandbox.Manager.RunSetup()` in `sandbox/setup.go` |
| `yoloai sandbox list` | `cli/list.go:newSandboxListCmd` | `sandbox.ListSandboxes()` in `sandbox/inspect.go` |
| `yoloai sandbox info` | `cli/sandbox_info.go:newSandboxInfoCmd` | `sandbox.InspectSandbox()` in `sandbox/inspect.go` |
| `yoloai sandbox log` | `cli/log.go:newSandboxLogCmd` | Reads `log.txt` from sandbox dir |
| `yoloai sandbox exec` | `cli/exec.go:newSandboxExecCmd` | `runtime.InteractiveExec` into running container |
| `yoloai sandbox network add` | `cli/sandbox_network_add.go:newSandboxNetworkAddCmd` | `sandbox.PatchConfigAllowedDomains()` + `runtime.Exec` ipset update |
| `yoloai sandbox network list` | `cli/sandbox_network_list.go:newSandboxNetworkListCmd` | `sandbox.LoadMeta()` — pure file read |
| `yoloai sandbox network remove` | `cli/sandbox_network_remove.go:newSandboxNetworkRemoveCmd` | `sandbox.PatchConfigAllowedDomains()` + `runtime.Exec` ipset update |
| `yoloai restart` | `cli/restart.go:newRestartCmd` | `sandbox.Manager.Stop()` + `sandbox.Manager.Start()` in `sandbox/lifecycle.go` |
| `yoloai system prune` | `cli/system_prune.go:newSystemPruneCmd` | `sandbox.Prune()` in `sandbox/prune.go` |
| `yoloai files` | `cli/files.go:newFilesCmd` | File exchange via `~/.yoloai/sandboxes/<name>/files/` |
| `yoloai profile` | `cli/profile.go:newProfileCmd` | Profile create/list/info/delete |
| `yoloai help` | `cli/help.go:newHelpCmd` | Topic-based help with embedded markdown |
| `yoloai ls` | `cli/commands.go:newLsAliasCmd` | Shortcut for `sandbox list` (calls `runList`) |
| `yoloai log` | `cli/commands.go:newLogAliasCmd` | Shortcut for `sandbox log` (calls `runLog`) |
| `yoloai exec` | `cli/commands.go:newExecAliasCmd` | Shortcut for `sandbox exec` |
| `yoloai config get` | `cli/config.go:newConfigGetCmd` | `sandbox.GetEffectiveConfig()` / `sandbox.GetConfigValue()` |
| `yoloai config set` | `cli/config.go:newConfigSetCmd` | `sandbox.UpdateConfigFields()` or `sandbox.UpdateGlobalConfigFields()` via `IsGlobalKey()` |
| `yoloai config reset` | `cli/config.go:newConfigResetCmd` | `sandbox.DeleteConfigField()` or `sandbox.DeleteGlobalConfigField()` via `IsGlobalKey()` |
| `yoloai completion` | `cli/commands.go:newCompletionCmd` | Cobra's built-in completion generators |
| `yoloai x` | `cli/x.go:newExtensionCmd` | Loads and runs user-defined extensions from `~/.yoloai/extensions/` |
| `yoloai version` | `cli/commands.go:newVersionCmd` | Prints build-time version info |

## Data Flow

### Sandbox Creation (`yoloai new`)

```
newNewCmd (cli/commands.go)
  → withRuntime (cli/helpers.go)
    → Manager.Create (sandbox/create.go)
      → EnsureSetup: create dirs, seed resources, build image, write config.yaml
      → prepareSandboxState:
          ParseDirArg → validate name/agent/workdir/auxdirs → safety checks
          → :copy dirs: copyDir (cp -rp) → removeGitDirs → gitBaseline (git init + commit)
          → :overlay dirs: createOverlayDirs (upper/ovlwork in sandbox state)
          → copySeedFiles → copyAgentFiles → ensureContainerSettings
          → readPrompt → resolveModel → buildAgentCommand
          → SaveMeta (environment.json) → SaveSandboxState (sandbox-state.json) → write prompt.txt, log.txt, runtime-config.json
          → WriteContextFiles (context.md + agent instruction file)
      → launchContainer:
          createSecretsDir (config env vars + API keys from host env)
          → buildMounts (workdir + aux dirs, overlay mount configs for :overlay dirs)
          → runtime.Create (with CAP_SYS_ADMIN for :overlay) → runtime.Start
          → runtime.Inspect (verify running) → cleanup secrets
```

### Diff (`yoloai diff`)

```
newDiffCmd (cli/diff.go)
  → GenerateDiff (sandbox/diff.go)
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
create.go:
  → createOverlayDirs: create upper/ovlwork dirs in sandbox state
  → buildMounts: build overlay mount configs for runtime-config.json, add CAP_SYS_ADMIN

entrypoint.sh (Docker container, root phase):
  → mount overlayfs using runtime-config.json overlay_mounts
sandbox-setup.py (Docker container, user phase):
  → git baseline (git init + commit) in mounted directories

diff.go / apply.go:
  → exec git commands inside container for overlay dirs (same as :copy)

lifecycle.go (reset):
  → clearOverlayDirs: rm -rf upper/ovlwork for instant reset
```

### Container Start/Restart (`yoloai start`)

```
Manager.Start (sandbox/lifecycle.go)
  → DetectStatus (sandbox/inspect.go): runtime.Inspect + tmux query
  → StatusActive: no-op
  → StatusDone/Failed: relaunchAgent via tmux respawn-pane
  → StatusStopped: runtime.Start
  → StatusRemoved: recreateContainer (rebuild state from environment.json via runtime.Create + runtime.Start)
```

## Host Directory Layout

```
~/.yoloai/
├── config.yaml              # Global config (tmux_conf, model_aliases)
├── state.yaml               # Global state (setup_complete)
├── profiles/
│   └── base/
│       ├── config.yaml      # Profile defaults (agent, model, backend, env, etc.)
│       ├── Dockerfile       # Seeded from embedded, user-customizable
│       ├── entrypoint.sh    # Root entrypoint, seeded from embedded
│       ├── sandbox-setup.py # User-phase setup, seeded from embedded
│       ├── tmux.conf        # Seeded from embedded, user-customizable
│       ├── .checksums       # Tracks seeded file checksums
│       └── .last-build-checksum  # Tracks last image build inputs
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
│       ├── home-seed/         # Files symlinked into sandbox HOME
│       ├── home/              # Sandbox HOME directory (seatbelt)
│       └── work/
│           └── <caret-encoded-path>/  # Copy of workdir with internal git repo
└── cache/                   # (future) Cache directory
```

## Where to Change

**Add a new CLI command:**
1. Create `internal/cli/<command>.go` with `newXxxCmd() *cobra.Command`
2. Register it in `internal/cli/commands.go:registerCommands()` under the appropriate group
3. If the command needs a Manager, use `withManager()` or `withRuntime()` from `helpers.go`

**Add a new agent:**
1. Add a new entry to the `agents` map in `agent/agent.go`
2. Define: commands, prompt mode, API key env vars, auth hint env vars, `AuthOptional`, seed files, state dir, submit sequence, startup delay, ready pattern, model flag/aliases/prefixes, network allowlist, context file, agent files exclude patterns

**Change agent files seeding:**
1. Config parsing: `parseAgentFilesNode()` in `sandbox/config.go`
2. Copy logic: `copyAgentFiles()` in `sandbox/agent_files.go`
3. Exclusion patterns: `AgentFilesExclude` in agent definitions (`agent/agent.go`)
4. State tracking: `SandboxState.AgentFilesInitialized` in `sandbox/sandbox_state.go`

**Add a new CLI flag to an existing command:**
1. Add `cmd.Flags().XxxP(...)` in the command's `newXxxCmd()` function
2. Read it with `cmd.Flags().GetXxx(...)` in the `RunE` handler

**Change container setup (Dockerfile, entrypoint):**
1. Edit files in `runtime/docker/resources/`
2. They're embedded at compile time via `//go:embed` in `runtime/docker/resources.go`
3. `SeedResources()` in `runtime/docker/build.go` handles deploying them to `~/.yoloai/profiles/base/`

**Change how sandbox state is persisted:**
1. Modify `Meta` / `DirMeta` in `sandbox/meta.go`
2. Update `prepareSandboxState()` in `sandbox/create.go` where meta is populated
3. Update any consumers that `LoadMeta()` and use the changed fields (e.g., diff, apply, inspect, reset)

**Change diff/apply behavior:**
1. Diff generation: `sandbox/diff.go`
2. Patch generation and application: `sandbox/apply.go`
3. CLI presentation: `internal/cli/diff.go` and `internal/cli/apply.go`

**Change container creation (mounts, networking):**
1. Mount construction: `buildMounts()` in `sandbox/create.go` → populates `runtime.MountSpec` from meta.Directories (workdir + aux dirs)
2. Container config: `launchContainer()` in `sandbox/create.go` → builds `runtime.InstanceConfig`
3. Port parsing: `parsePortBindings()` in `sandbox/create.go` → populates `runtime.PortMapping`
4. Runtime creation: `runtime.Create()` in `runtime/docker/docker.go`

**Change sandbox status detection:**
1. `DetectStatus()` in `sandbox/inspect.go` — reads `agent-status.json` from sandbox dir (written by status monitor), falls back to legacy `status.json` then `runtime.Exec()` for old sandboxes
2. Status constants are in the same file

**Change config handling:**
1. Profile config: `LoadConfig()` / `UpdateConfigFields()` / `DeleteConfigField()` in `sandbox/config.go`
2. Global config: `LoadGlobalConfig()` / `UpdateGlobalConfigFields()` / `DeleteGlobalConfigField()` in `sandbox/config.go`
3. `IsGlobalKey()` determines routing — add new global keys to `globalKnownSettings` or `globalKnownCollectionSettings`
4. Add new profile fields to `YoloaiConfig` struct and the YAML node walker
5. CLI `config get/set/reset` commands in `internal/cli/config.go` route via `IsGlobalKey()`
6. Profile config at `~/.yoloai/profiles/base/config.yaml`, global config at `~/.yoloai/config.yaml`
7. Global state like `setup_complete` is stored in `~/.yoloai/state.yaml` via `LoadState()`/`SaveState()` in `sandbox/state.go`

**Add a new runtime backend:**
1. Create `runtime/<name>/` package
2. Implement the `runtime.Runtime` interface
3. Register in `cli/helpers.go:newRuntime()` — switch on the backend name resolved by `resolveBackend()`
4. Backend is selectable via `--backend` flag (on new/build/setup) or `backend` config. Lifecycle commands read backend from sandbox `environment.json`.

## Testing

**Run all checks (preferred — gofmt, lint, tidy, tests):**
```
make check
```

**Run all unit tests:**
```
go test ./...
```

**Run integration tests (requires Docker):**
```
go test -tags integration ./...
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

Integration tests use the `//go:build integration` build tag and are in:
- `runtime/docker/docker_integration_test.go`
- `sandbox/integration_test.go`

All other `_test.go` files are standard unit tests that run without Docker or other runtime backends.

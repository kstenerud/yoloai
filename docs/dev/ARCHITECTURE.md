# Architecture

Code navigation guide for the yoloAI codebase. Focused on the implemented code, not aspirational features (see [design/](../design/README.md) for those).

## Package Map

```
cmd/yoloai/          → Binary entry point
internal/agent/      → Agent plugin definitions (Claude, Gemini, Codex, test, shell)
internal/cli/        → Cobra command tree and CLI plumbing
internal/runtime/    → Pluggable runtime interface (backend-agnostic types and errors)
internal/runtime/docker/ → Docker implementation of runtime.Runtime
internal/runtime/tart/       → Tart (macOS VM) implementation of runtime.Runtime
internal/runtime/seatbelt/   → Seatbelt (macOS sandbox-exec) implementation of runtime.Runtime
internal/sandbox/    → Core logic: create, lifecycle, diff, apply, inspect, config
```

Dependency direction: `cmd/yoloai` → `cli` → `sandbox` + `runtime`; `sandbox` → `runtime` + `agent`; `agent` stands alone.

## File Index

### `cmd/yoloai/`

| File | Purpose |
|------|---------|
| `main.go` | Entry point. Sets up signal context, calls `cli.Execute`, exits with code. Build-time version/commit/date vars. |

### `internal/agent/`

| File | Purpose |
|------|---------|
| `agent.go` | `Definition` struct and built-in agent registry (`aider`, `claude`, `codex`, `gemini`, `opencode`, `test`, `shell`). `GetAgent()` lookup. |
| `agent_test.go` | Unit tests for agent definitions. |

### `internal/cli/`

| File | Purpose |
|------|---------|
| `root.go` | Root Cobra command, global flags (`-v`, `-q`, `--no-color`), `Execute()` with exit code mapping. |
| `commands.go` | `registerCommands()` — registers all subcommands. Also contains `newNewCmd`, `newLsAliasCmd`, `newLogAliasCmd`, `newCompletionCmd`, `newVersionCmd`, and `attachToSandbox`/`waitForTmux` helpers. |
| `config.go` | `yoloai config get/set` — read and write `config.yaml` values via dotted paths. |
| `apply.go` | `yoloai apply` — apply changes back to host. Squash and selective-commit modes, `--export` for `.patch` files. |
| `attach.go` | `yoloai attach` — attach to sandbox tmux session via `runtime.InteractiveExec`. |
| `diff.go` | `yoloai diff` — show agent changes. Supports `--stat`, `--log`, commit refs, and ranges. |
| `destroy.go` | `yoloai destroy` — stop and remove sandbox with confirmation logic. |
| `system.go` | `yoloai system` parent command with `build` and `setup` subcommands. |
| `system_info.go` | `yoloai system info` — displays version, paths, disk usage, backend availability. |
| `sandbox_cmd.go` | `yoloai sandbox` parent command grouping sandbox inspection subcommands. |
| `sandbox_info.go` | `yoloai sandbox info` — display sandbox config, meta, and status. |
| `exec.go` | `yoloai sandbox exec` — run commands inside a running sandbox container. |
| `list.go` | `yoloai sandbox list` / `yoloai ls` — tabular listing of all sandboxes with status. |
| `log.go` | `yoloai sandbox log` / `yoloai log` — display sandbox session log (log.txt). |
| `info.go` | Backend and agent info commands (`system backends`, `system agents`), plus shared helpers (`checkBackend`, `knownBackends`). |
| `reset.go` | `yoloai reset` — re-copy workdir from host, reset git baseline. |
| `start.go` | `yoloai start` — start a stopped sandbox (recreates container if removed). |
| `stop.go` | `yoloai stop` — stop a running sandbox. |
| `envname.go` | `resolveName()` — resolves sandbox name from args or `YOLOAI_SANDBOX` env var. |
| `helpers.go` | `withRuntime()`, `withManager()` — create Runtime / Manager for command handlers. `resolveBackend()` reads `--backend` flag (on new/build/setup). `resolveBackendForSandbox()` reads `meta.json`. `resolveBackendFromConfig()` reads config default. |
| `pager.go` | `RunPager()` — pipe output through `$PAGER` or `less` when stdout is a TTY. |
| `envname_test.go` | Tests for name resolution. |
| `pager_test.go` | Tests for pager. |

### `internal/runtime/`

| File | Purpose |
|------|---------|
| `runtime.go` | `Runtime` interface — pluggable backend abstraction. Generic types: `MountSpec`, `PortMapping`, `InstanceConfig`, `InstanceInfo`, `ExecResult`. Sentinel errors: `ErrNotFound`, `ErrNotRunning`. |

### `internal/runtime/docker/`

| File | Purpose |
|------|---------|
| `docker.go` | `DockerRuntime` struct — implements `Runtime` interface, wraps Docker SDK. `NewDockerRuntime()` with daemon ping. |
| `build.go` | `EnsureImage()` — builds `yoloai-base` image. `NeedsBuild()` / `RecordBuildChecksum()` for rebuild detection. Tar context creation, build output streaming. Moved from old `internal/docker/build.go`. |
| `resources.go` | `//go:embed` for Dockerfile.base, entrypoint.sh, tmux.conf. `SeedResources()` writes them to `~/.yoloai/` respecting user customizations. Moved from old `internal/docker/resources.go`. |
| `resources/Dockerfile.base` | Container Dockerfile (embedded at compile time). |
| `resources/entrypoint.sh` | Container entrypoint script (embedded at compile time). |
| `resources/tmux.conf` | Default tmux config (embedded at compile time). |
| `build_test.go` | Unit tests for build/seed logic. |
| `docker_integration_test.go` | Integration tests requiring Docker daemon. Build tag: `integration`. |

### `internal/runtime/tart/`

| File | Purpose |
|------|---------|
| `tart.go` | `Runtime` struct — implements `Runtime` interface, shells out to `tart` CLI. VM lifecycle via `tart clone/run/stop/delete`, exec via `tart exec`. PID file + `tart list` for process management. |
| `build.go` | `EnsureImage()` — pulls Cirrus Labs macOS base image, provisions dev tools via `tart exec` (Homebrew, Node.js, Xcode CLI tools, tmux, git, jq, ripgrep). Supports `defaults.tart.image` config override. |
| `resources.go` | `//go:embed` for setup.sh. |
| `platform.go` | Platform detection helpers (macOS, Apple Silicon). Testable via variable overrides. |
| `resources/setup.sh` | Post-boot setup script (embedded at compile time). Creates mount symlinks, injects secrets, launches tmux + agent. |
| `tart_test.go` | Unit tests for arg building, error mapping, network flags, mount symlinks. |

### `internal/runtime/seatbelt/`

| File | Purpose |
|------|---------|
| `seatbelt.go` | `Runtime` struct — implements `Runtime` interface using macOS `sandbox-exec`. PID file management, background process, per-sandbox tmux socket. |
| `profile.go` | `GenerateProfile()` — builds SBPL (Seatbelt Profile Language) profiles from `InstanceConfig`. Maps mounts to file-access rules, controls network. |
| `build.go` | `EnsureImage()` / `ImageExists()` — verifies prerequisites (sandbox-exec, tmux, jq). No image to build. |
| `resources.go` | `//go:embed` for entrypoint.sh and tmux.conf. |
| `platform.go` | Platform detection (macOS only, no Apple Silicon requirement). Testable via variable override. |
| `resources/entrypoint.sh` | Entrypoint script (embedded at compile time). Sets up HOME redirection, secrets, per-sandbox tmux socket, launches agent. |
| `resources/tmux.conf` | Default tmux config (embedded at compile time). |
| `seatbelt_test.go` | Unit tests for profile generation, platform detection, tmux socket injection. |

### `internal/sandbox/`

| File | Purpose |
|------|---------|
| `manager.go` | `Manager` struct — central orchestrator. Holds a `runtime.Runtime`. `EnsureSetup()` / `EnsureSetupNonInteractive()` for first-run auto-setup (dirs, resources, image, config). |
| `create.go` | `Create()` — full sandbox creation: validate, safety checks, copy workdir, git baseline, seed files, build mounts, launch container. Also contains `launchContainer()`, `buildMounts()`, `createSecretsDir()` (writes config env vars + API keys from host env to /run/secrets/), `copySeedFiles()`. On macOS, when a seed file with `KeychainService` set is not found on disk, the system falls back to reading credentials from the macOS Keychain (via `security find-generic-password`). Platform-specific code is in `keychain_darwin.go` / `keychain_other.go`. |
| `lifecycle.go` | `Start()`, `Stop()`, `Destroy()`, `Reset()` — sandbox lifecycle. `recreateContainer()` and `relaunchAgent()` for restart scenarios. `resetInPlace()` for `--no-restart` resets. |
| `diff.go` | `GenerateDiff()`, `GenerateDiffStat()`, `GenerateCommitDiff()`, `ListCommitsWithStats()` — diff generation for both `:copy` and `:rw` modes. |
| `apply.go` | `GeneratePatch()`, `CheckPatch()`, `ApplyPatch()` — squash apply via `git apply`. `GenerateFormatPatch()`, `ApplyFormatPatch()` — per-commit apply via `git am`. `ListCommitsBeyondBaseline()`, `AdvanceBaseline()`, `AdvanceBaselineTo()`. |
| `inspect.go` | `DetectStatus()` — queries runtime + tmux for sandbox state. `InspectSandbox()`, `ListSandboxes()` — metadata + live status. `execInContainer()` helper uses `runtime.Exec()`. |
| `meta.go` | `Meta` / `WorkdirMeta` structs, `SaveMeta()` / `LoadMeta()` — sandbox metadata persistence as `meta.json`. `Meta.Backend` records which runtime backend was used to create the sandbox. |
| `paths.go` | `EncodePath()` / `DecodePath()` — caret encoding for filesystem-safe names. `InstanceName()` (and deprecated alias `ContainerName()`), `Dir()`, `WorkDir()`, `RequireSandboxDir()`. |
| `parse.go` | `ParseDirArg()` — parses `path:copy`, `path:rw`, `path:force` suffixes into `DirArg`. |
| `safety.go` | `IsDangerousDir()`, `CheckPathOverlap()`, `CheckDirtyRepo()` — pre-creation safety checks. |
| `config.go` | `LoadConfig()`, `UpdateConfigFields()`, `ConfigPath()`, `ReadConfigRaw()`, `GetConfigValue()`, `GetEffectiveConfig()` — read/write `~/.yoloai/config.yaml` preserving YAML comments via `yaml.Node`. Dotted-path get/set with default fallback for CLI `config get/set` commands. |
| `setup.go` | `RunSetup()`, `runNewUserSetup()` — interactive tmux configuration setup. Classifies user's tmux config, prompts for preferences. |
| `confirm.go` | `Confirm()` — simple y/N interactive prompt. |
| `errors.go` | `UsageError` (exit 2), `ConfigError` (exit 3), sentinel errors (`ErrSandboxNotFound`, `ErrSandboxExists`, etc.). |
| `*_test.go` | Unit tests for each file above. `integration_test.go` has the `integration` build tag. |

## Key Types

### `sandbox.Manager`
Central orchestrator. Holds a `runtime.Runtime`, backend name, logger, and I/O streams. All sandbox operations go through it: `Create()`, `Start()`, `Stop()`, `Destroy()`, `Reset()`, `EnsureSetup()`. The backend name is stored so it can be persisted in `Meta` at sandbox creation time.

### `sandbox.Meta` / `sandbox.WorkdirMeta` / `sandbox.DirectoryMeta`
Persisted as `meta.json` in each sandbox dir. Records creation-time state: agent, model, workdir path/mode/baseline SHA, auxiliary directories (via `Directories` field), network mode, ports, backend. Each directory (workdir and aux dirs) has its own `DirectoryMeta` with host path, mount path, mode, and baseline SHA.

### `sandbox.CreateOptions`
All parameters for `Manager.Create()`. Mirrors CLI flags: name, workdir, auxiliary directories (`AuxDirs`), agent, model, prompt, network, ports, replace, attach, passthrough args.

### `sandbox.DiffOptions` / `sandbox.DiffResult`
Input/output for `GenerateDiff()`. Supports path filtering and stat-only mode. `DiffResult` carries the diff text, workdir, mode, and empty flag.

### `agent.Definition`
Describes an agent's commands (interactive/headless), prompt delivery mode, API key env vars, seed files, state directory, tmux submit sequence, model flag/aliases. Built-in: `aider`, `claude`, `codex`, `gemini`, `opencode`, `test`, and `shell`.

### `runtime.Runtime`
Pluggable runtime interface for backend abstraction. Methods: `Create()`, `Start()`, `Stop()`, `Remove()`, `Inspect()`, `Exec()`, `InteractiveExec()`, `EnsureImage()`, `Close()`. Allows swapping container/VM backends.

### `runtime.InstanceConfig`
Configuration for `Runtime.Create()`. Describes image, command, working directory, environment variables, mounts, ports, network mode, and resource limits for a container or VM instance.

### `runtime.DockerRuntime`
Docker implementation of `Runtime` interface. Wraps Docker SDK client. Defined in `internal/runtime/docker/`.

### `runtime.TartRuntime`
Tart (macOS VM) implementation of `Runtime` interface. Shells out to `tart` CLI for all operations. PID-based process management with `tart list` cross-check. VirtioFS mounts with symlink path mapping. Defined in `internal/runtime/tart/`.

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
| `yoloai ls` | `cli/commands.go:newLsAliasCmd` | Shortcut for `sandbox list` (calls `runList`) |
| `yoloai log` | `cli/commands.go:newLogAliasCmd` | Shortcut for `sandbox log` (calls `runLog`) |
| `yoloai config get` | `cli/config.go:newConfigGetCmd` | `sandbox.GetEffectiveConfig()` / `sandbox.GetConfigValue()` |
| `yoloai config set` | `cli/config.go:newConfigSetCmd` | `sandbox.UpdateConfigFields()` |
| `yoloai completion` | `cli/commands.go:newCompletionCmd` | Cobra's built-in completion generators |
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
          → copyDir (cp -rp) for each :copy directory → removeGitDirs → gitBaseline (git init + commit)
          → copySeedFiles → ensureContainerSettings
          → readPrompt → resolveModel → buildAgentCommand
          → SaveMeta (meta.json with Directories field) → write prompt.txt, log.txt, config.json
      → launchContainer:
          createSecretsDir (config env vars + API keys from host env)
          → buildMounts (workdir + aux dirs) → runtime.Create → runtime.Start
          → runtime.Inspect (verify running) → cleanup secrets
```

### Diff (`yoloai diff`)

```
newDiffCmd (cli/diff.go)
  → GenerateDiff (sandbox/diff.go)
    → loadDiffContext: LoadMeta → resolve all directories from meta.Directories
    → For each directory:
      → :copy mode: stageUntracked (git add -A) → git diff --binary <baseline>
      → :rw mode: git diff HEAD on live host dir
    → Combine diffs with directory-prefixed headers
```

### Apply (`yoloai apply`)

Two modes — squash and selective:

**Squash (default):**
```
applySquash (cli/apply.go)
  → For each :copy directory in meta.Directories:
    → GeneratePatch (sandbox/apply.go): git diff --binary against baseline
    → CheckPatch: git apply --check
    → Confirm with user
    → ApplyPatch: git apply
    → AdvanceBaseline: update meta.json baseline SHA to HEAD
```

**Selective (commit refs):**
```
applySelectedCommits (cli/apply.go)
  → For each :copy directory in meta.Directories:
    → ResolveRefs (sandbox/apply.go): resolve short SHAs / ranges
    → GenerateFormatPatchForRefs: git format-patch per commit
    → ApplyFormatPatch: git am --3way
    → AdvanceBaselineTo: advance baseline to contiguous prefix
```

### Container Start/Restart (`yoloai start`)

```
Manager.Start (sandbox/lifecycle.go)
  → DetectStatus (sandbox/inspect.go): runtime.Inspect + tmux query
  → StatusRunning: no-op
  → StatusDone/Failed: relaunchAgent via tmux respawn-pane
  → StatusStopped: runtime.Start
  → StatusRemoved: recreateContainer (rebuild state from meta.json via runtime.Create + runtime.Start)
```

## Host Directory Layout

```
~/.yoloai/
├── config.yaml              # Global config (setup_complete, defaults)
├── Dockerfile.base          # Seeded from embedded, user-customizable
├── entrypoint.sh            # Seeded from embedded, user-customizable
├── tmux.conf                # Seeded from embedded, user-customizable
├── .resource-checksums      # Tracks seeded file checksums
├── .last-build-checksum     # Tracks last image build inputs
├── sandboxes/
│   └── <name>/
│       ├── meta.json        # Sandbox metadata (agent, workdir, baseline SHA)
│       ├── config.json      # Container runtime config (agent cmd, tmux settings)
│       ├── prompt.txt       # Agent prompt (if provided)
│       ├── log.txt          # Session log
│       ├── agent-state/     # Mounted at agent's StateDir (e.g., /home/yoloai/.claude/, /home/yoloai/.gemini/)
│       ├── home-seed/       # Files mounted individually into /home/yoloai/
│       └── work/
│           └── <caret-encoded-path>/  # Copy of workdir with internal git repo
├── profiles/                # (future) Profile directories
└── cache/                   # (future) Cache directory
```

## Where to Change

**Add a new CLI command:**
1. Create `internal/cli/<command>.go` with `newXxxCmd() *cobra.Command`
2. Register it in `internal/cli/commands.go:registerCommands()` under the appropriate group
3. If the command needs a Manager, use `withManager()` or `withRuntime()` from `helpers.go`

**Add a new agent:**
1. Add a new entry to the `agents` map in `internal/agent/agent.go`
2. Define: commands, prompt mode, API key env vars, seed files, state dir, submit sequence, startup delay, model aliases

**Add a new CLI flag to an existing command:**
1. Add `cmd.Flags().XxxP(...)` in the command's `newXxxCmd()` function
2. Read it with `cmd.Flags().GetXxx(...)` in the `RunE` handler

**Change container setup (Dockerfile, entrypoint):**
1. Edit files in `internal/runtime/docker/resources/`
2. They're embedded at compile time via `//go:embed` in `internal/runtime/docker/resources.go`
3. `SeedResources()` in `internal/runtime/docker/build.go` handles deploying them to `~/.yoloai/`

**Change how sandbox state is persisted:**
1. Modify `Meta` / `DirectoryMeta` in `internal/sandbox/meta.go`
2. Update `prepareSandboxState()` in `internal/sandbox/create.go` where meta is populated
3. Update any consumers that `LoadMeta()` and use the changed fields (e.g., diff, apply, inspect, reset)

**Change diff/apply behavior:**
1. Diff generation: `internal/sandbox/diff.go`
2. Patch generation and application: `internal/sandbox/apply.go`
3. CLI presentation: `internal/cli/diff.go` and `internal/cli/apply.go`

**Change container creation (mounts, networking):**
1. Mount construction: `buildMounts()` in `internal/sandbox/create.go` → populates `runtime.MountSpec` from meta.Directories (workdir + aux dirs)
2. Container config: `launchContainer()` in `internal/sandbox/create.go` → builds `runtime.InstanceConfig`
3. Port parsing: `parsePortBindings()` in `internal/sandbox/create.go` → populates `runtime.PortMapping`
4. Runtime creation: `runtime.Create()` in `internal/runtime/docker/docker.go`

**Change sandbox status detection:**
1. `DetectStatus()` in `internal/sandbox/inspect.go` — queries `runtime.Inspect()` and tmux via `runtime.Exec()`
2. Status constants are in the same file

**Change config.yaml handling:**
1. `LoadConfig()` / `UpdateConfigFields()` in `internal/sandbox/config.go`
2. Add new fields to `YoloaiConfig` struct and the YAML node walker
3. CLI `config get/set` commands in `internal/cli/config.go`

**Add a new runtime backend:**
1. Create `internal/runtime/<name>/` package
2. Implement the `runtime.Runtime` interface
3. Register in `cli/helpers.go:newRuntime()` — switch on the backend name resolved by `resolveBackend()`
4. Backend is selectable via `--backend` flag (on new/build/setup) or `defaults.backend` config. Lifecycle commands read backend from sandbox `meta.json`.

## Testing

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
go test ./internal/sandbox/
go test ./internal/runtime/docker/
go test ./internal/agent/
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
- `internal/runtime/docker/docker_integration_test.go`
- `internal/sandbox/integration_test.go`

All other `_test.go` files are standard unit tests that run without Docker or other runtime backends.

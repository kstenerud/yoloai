# Where to Change

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
4. If the command needs a `yoloai.Client`, use `cliutil.WithClient`; for cross-backend admin work use `cliutil.System()`. Do not construct `orchestrator.Engine` or a raw `runtime.Backend` directly — `.golangci.yml` enforces this via depguard / forbidigo.

**Add a new agent:**
1. Add a new entry to the `agents` map in `agent/agent.go`
2. Define: commands, prompt mode, API key env vars, auth hint env vars, `AuthOptional`, seed files, state dir, submit sequence, startup delay, ready pattern, idle support, model flag/aliases/prefixes, network allowlist, context file, agent files exclude patterns

**Change agent files seeding:**
1. Config parsing: `parseAgentFilesNode()` in `config/config.go`
2. Copy logic: `copyAgentFiles()` in `orchestrator/agent_files.go`
3. Exclusion patterns: `AgentFilesExclude` in agent definitions (`agent/agent.go`)
4. State tracking: `SandboxState.AgentFilesInitialized` in `orchestrator/sandbox_state.go`

**Add a new CLI flag to an existing command:**
1. Add `cmd.Flags().XxxP(...)` in the command's `newXxxCmd()` function
2. Read it with `cmd.Flags().GetXxx(...)` in the `RunE` handler

**Change container setup (Dockerfile, entrypoint):**
1. Edit files in `runtime/docker/resources/`
2. They're embedded at compile time via `//go:embed` in `runtime/docker/resources.go`

**Change the default tmux config:**
1. Edit `internal/resources/tmux/tmux.conf` (neutral location — shared by setup wizard and Docker image build)
2. `tmuxres.Embedded()` exposes the bytes; `orchestrator/setup.go` uses it to write `defaults/tmux.conf` and `runtime/docker` ships it inside the image

**Change shared monitoring scripts (sandbox-setup.py, status-monitor.py):**
1. Edit files in `runtime/monitor/`
2. They're embedded at compile time via `//go:embed` in `runtime/monitor/monitor.go`
3. Imported by `runtime/docker/resources.go` and other backend resource files

**Change how sandbox state is persisted:**
1. Modify `Environment` / `DirEnvironment` in `store/environment.go`
2. Update `prepareSandboxState()` in `orchestrator/create_prepare.go` where the environment is populated
3. Update any consumers that `LoadEnvironment()` and use the changed fields (e.g., diff, apply, inspect, reset)
4. If the field is public, mirror it onto `yoloai.Environment` in `environment.go` and update `environmentFromStore`

**Change diff/apply behavior:**
1. Diff generation: `copyflow/diff.go`
2. Patch generation and application: `copyflow/apply.go`
3. CLI presentation: `internal/cli/diff.go` and `internal/cli/apply.go`

**Change container creation (mounts, networking):**
1. Mount construction: `mounts.Build()` (`orchestrator/mounts/`) → populates `runtime.MountSpec`
2. Container config: `buildAndStart()` in `orchestrator/launch/launch.go` → builds `runtime.InstanceConfig`
3. Port parsing: `parsePortBindings()` in `orchestrator/launch/launch.go` → populates `runtime.PortMapping`
4. Runtime creation: `runtime.Create()` dispatched to the active backend

**Change sandbox status detection:**
1. `DetectStatus()` in `orchestrator/status/status.go` — reads `agent-status.json` from sandbox dir (written by status monitor), falls back to legacy `status.json` then `runtime.Exec()` for old sandboxes
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
2. Implement the `runtime.Backend` interface (see `runtime/podman/` for an example that embeds an existing backend and overrides only what differs)
3. Declare a package-level `var descriptor = runtime.BackendDescriptor{...}` and call `runtime.Register(name, factory, descriptor)` in your package's `init()` function. The descriptor's `Type` must match the registration name. Return the same `descriptor` from your `Descriptor()` method.
4. Add a blank import in the appropriate platform file (`client.go` for all platforms, or a `_linux.go` / `_darwin.go` file for platform-specific backends)
5. Backend is selectable via `--backend` flag (on new/build/setup) or `backend` config. Lifecycle commands read backend from sandbox `environment.json`

**Add capability checks for a backend:**
1. Create `runtime/<name>/caps.go` with `HostCapability` constructors
2. Implement `RequiredCapabilities(isolation)` and `SupportedIsolationModes()` on your Runtime
3. Shared capability constructors live in `runtime/caps/common.go`

**Add MCP tools for outer agents:**
1. Add tool registration in `internal/mcpsrv/tools.go`
2. Tool handlers use `orchestrator.Engine` for all sandbox operations


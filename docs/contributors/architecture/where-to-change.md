> **ABOUTME:** Task-oriented recipes mapping a common change — new CLI command, new agent, new
> runtime backend, a config field — onto the files it touches and the doc surfaces that must be
> swept alongside it. The fast path for "I want to change X, where do I start."

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
1. Add a new entry to the `agents` map in `internal/agent/agent.go`
2. Define: commands, prompt mode, API key env vars, auth hint env vars, `AuthOptional`, seed files, state dir, submit sequence, startup delay, ready pattern, idle support, model flag/aliases/prefixes, network allowlist, context file, agent files exclude patterns
3. Sweep the doc surfaces that name agents verbatim — see `docs/contributors/procedures/pull-requests.md` ("The name sweep — the surfaces") for the full surface list and why each one drifts independently of the code: `internal/cli/helpcmd/help/*.md`, cobra `Short`/`Long` strings, `docs/GUIDE.md`, `README.md`.

**Change agent files seeding:**
1. Config parsing: `parseAgentFilesNode()` in `internal/config/config.go`
2. Copy logic: `CopyAgentFiles()` in `internal/envsetup/agent_files.go`
3. Exclusion patterns: `AgentFilesExclude` in agent definitions (`internal/agent/agent.go`)
4. State tracking: `SandboxState.AgentFilesInitialized` in `store/sandbox_state.go`

**Add a new CLI flag to an existing command:**
1. Add the flag on the `*cobra.Command` the command's constructor builds (`NewDiffCmd`, `NewApplyCmd`, …)
2. Read it with `cmd.Flags().GetXxx(...)` in the `RunE` handler
3. Sweep the doc surfaces that name flags verbatim — see `docs/contributors/procedures/pull-requests.md` ("The name sweep — the surfaces") for the full surface list and why each one drifts independently of the code: `internal/cli/helpcmd/help/*.md` (`//go:embed`'ed **shipped UI** despite living under `internal/` — check every block, the settings table and the examples drift separately; wrap at 80 columns per `../standards/cli.md`), cobra `Short`/`Long` strings, `docs/GUIDE.md`, `README.md`.

**Change container setup (Dockerfile, entrypoint):**
1. Edit files in `runtime/docker/resources/`
2. They're embedded at compile time via `//go:embed` in `runtime/docker/resources.go`

**Change the default tmux config:**
1. Edit `internal/resources/tmux/tmux.conf` (neutral location — shared by setup wizard and Docker image build)
2. `tmuxres.Embedded()` exposes the bytes; `internal/orchestrator/engine.go` uses it to write `defaults/tmux.conf` and `runtime/docker` ships it inside the image

**Change shared monitoring scripts (sandbox-setup.py, status-monitor.py):**
1. Edit files in `runtime/monitor/`
2. They're embedded at compile time via `//go:embed` in `runtime/monitor/monitor.go`
3. Imported by `runtime/docker/resources.go` and other backend resource files

**Change how sandbox state is persisted:**
1. Modify `Environment` / `DirEnvironment` in `store/environment.go`
2. Update `prepareSandboxState()` in `internal/orchestrator/create/create.go` where the environment is populated
3. Update any consumers that `LoadEnvironment()` and use the changed fields (e.g., diff, apply, inspect, reset)
4. If the field is public, mirror it onto `yoloai.Environment` in `environment.go` and update `environmentFromStore`

**Change diff/apply behavior:**
1. Diff generation: `copyflow/diff.go`
2. Patch generation and application: `copyflow/apply.go`
3. CLI presentation: `internal/cli/workflow/diff.go` and `internal/cli/workflow/apply.go`

**Change container creation (mounts, networking):**
1. Mount construction: `mounts.Build()` (`internal/orchestrator/mounts/mounts.go`) → populates `runtime.MountSpec`
2. Container config: `buildAndStart()` in `internal/orchestrator/launch/launch.go` → builds `runtime.InstanceConfig`
3. Port parsing: `parsePortBindings()` in `internal/orchestrator/launch/launch.go` → populates `runtime.PortMapping`
4. Runtime creation: `runtime.Create()` dispatched to the active backend

**Change sandbox status detection:**
1. `DetectStatus()` in `internal/orchestrator/status/status.go` — reads `agent-status.json` from sandbox dir (written by status monitor), falls back to legacy `status.json` then `runtime.Exec()` for old sandboxes
2. Status constants are in the same file

**Change config handling:**
1. Defaults config: `LoadDefaultsConfig()` (baked-in + defaults/config.yaml merge) in `internal/config/config.go`; `UpdateConfigFields()` / `DeleteConfigField()` in `internal/config/yamlnode.go`
2. Baked-in defaults: `LoadBakedInDefaults()` in `internal/config/config.go`; add new default values to `DefaultConfigYAML` in `internal/config/defaults.go`
3. Global config: `LoadGlobalConfig()` in `internal/config/config.go`; `UpdateGlobalConfigFields()` / `DeleteGlobalConfigField()` in `internal/config/yamlnode.go`
4. `IsGlobalKey()` (in `internal/config/config.go`) determines routing — add new global keys to `globalKnownSettings` or `globalKnownCollectionSettings`
5. Add new profile/defaults fields to `YoloaiConfig` struct and the YAML node walker in `internal/config/config.go`
6. CLI `config get/set/reset` commands in `internal/cli/configcmd/config.go` route via `config.IsGlobalKey()`
7. Defaults config at `~/.yoloai/library/defaults/config.yaml`, global config at `~/.yoloai/library/config.yaml`
8. The library no longer tracks setup ceremony (`setup_complete` removed, D60); the CLI's first-run-tip flag lives in `~/.yoloai/cli/state.yaml` via `cliutil.LoadCLIState()`/`SaveCLIState()`
9. **Sweep the doc surfaces that name config keys verbatim.** A renamed or removed config key drifts silently — no compiler, linter, or test catches it (this is exactly how PR #36 shipped a help topic advertising a dead key). See `docs/contributors/procedures/pull-requests.md` ("The name sweep — the surfaces") for the full surface list and the reasoning; concretely for config keys, check:
   - `internal/cli/helpcmd/help/*.md` — `//go:embed`'ed **shipped UI** despite living under `internal/`. Check *every* block in the file, not just the first hit: a settings table and its usage examples name the same key independently and drift independently. Wrap at 80 columns (`../standards/cli.md`).
   - cobra `Short`/`Long` strings in `internal/cli/**` (e.g. `internal/cli/configcmd/config.go`).
   - `docs/GUIDE.md` — both the reference tables and the prose examples.
   - `README.md`.
   - Append-only history (`docs/BREAKING-CHANGES.md`, `docs/contributors/archive/**`, `docs/contributors/decisions/**`, the `*-resolved`/`*-deferred`/`*-abandoned` sinks) is exempt from this sweep — but a renamed/removed key still needs its own `docs/BREAKING-CHANGES.md` entry (rule 1, not exempt).

**Add a new runtime backend:**
1. Create `runtime/<name>/` package
2. Implement the `runtime.Backend` interface (see `runtime/podman/` for an example that embeds an existing backend and overrides only what differs)
3. Declare a package-level `var descriptor = runtime.BackendDescriptor{...}` and call `runtime.Register(name, factory, descriptor)` in your package's `init()` function. The descriptor's `Type` must match the registration name. Return the same `descriptor` from your `Descriptor()` method.
4. Add a blank import in the appropriate platform file (`client.go` for all platforms, or a `_linux.go` / `_darwin.go` file for platform-specific backends)
5. Backend is selectable via `--backend` flag (on new/build/setup) or `backend` config. Lifecycle commands read backend from sandbox `environment.json`

**Add capability checks for a backend:**
1. Create `runtime/<name>/caps.go` with `HostCapability` constructors
2. List the modes in your descriptor's `SupportedIsolationModes` field — it is data on
   `runtime.BackendDescriptor`, not a method to implement
3. Implement `RequiredCapabilities(isolation)` only if the backend has prerequisites: it is the
   *optional* `runtime.IsolationCapabilityProvider` interface, reached through
   `runtime.RequiredCapabilitiesFor`. A backend that omits it has no isolation-mode
   prerequisites. Note the base mode is never capability-checked — `doctor` reports it Ready
   outright
4. Shared capability constructors live in `runtime/caps/common.go`

**Add MCP tools for outer agents:**
1. Add tool registration in `internal/mcpsrv/tools.go`
2. Tool handlers use `orchestrator.Engine` for all sandbox operations


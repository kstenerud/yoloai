# Architecture

Code navigation guide for the yoloAI codebase. Focused on the implemented code, not aspirational features (see DESIGN.md for those).

## Package Map

```
cmd/yoloai/          → Binary entry point
internal/agent/      → Agent plugin definitions (Claude, test)
internal/cli/        → Cobra command tree and CLI plumbing
internal/docker/     → Docker SDK wrapper, image building, embedded resources
internal/sandbox/    → Core logic: create, lifecycle, diff, apply, inspect, config
```

Dependency direction: `cmd/yoloai` → `cli` → `sandbox` + `docker`; `sandbox` → `docker` + `agent`; `agent` stands alone.

## File Index

### `cmd/yoloai/`

| File | Purpose |
|------|---------|
| `main.go` | Entry point. Sets up signal context, calls `cli.Execute`, exits with code. Build-time version/commit/date vars. |

### `internal/agent/`

| File | Purpose |
|------|---------|
| `agent.go` | `Definition` struct and built-in agent registry (`claude`, `test`). `GetAgent()` lookup. |
| `agent_test.go` | Unit tests for agent definitions. |

### `internal/cli/`

| File | Purpose |
|------|---------|
| `root.go` | Root Cobra command, global flags (`-v`, `-q`, `--no-color`), `Execute()` with exit code mapping. |
| `commands.go` | `registerCommands()` — registers all subcommands. Also contains `newNewCmd`, `newBuildCmd`, `newCompletionCmd`, `newVersionCmd`, and `attachToSandbox`/`waitForTmux` helpers. |
| `apply.go` | `yoloai apply` — apply changes back to host. Squash and selective-commit modes, `--export` for `.patch` files. |
| `attach.go` | `yoloai attach` — attach to sandbox tmux session via `docker exec`. |
| `diff.go` | `yoloai diff` — show agent changes. Supports `--stat`, `--log`, commit refs, and ranges. |
| `destroy.go` | `yoloai destroy` — stop and remove sandbox with confirmation logic. |
| `exec.go` | `yoloai exec` — run commands inside a running sandbox container. |
| `list.go` | `yoloai list` — tabular listing of all sandboxes with status. |
| `log.go` | `yoloai log` — display sandbox session log (log.txt). |
| `reset.go` | `yoloai reset` — re-copy workdir from host, reset git baseline. |
| `setup.go` | `yoloai setup` — re-run interactive first-run setup. |
| `show.go` | `yoloai show` — display sandbox config, meta, and status. |
| `start.go` | `yoloai start` — start a stopped sandbox (recreates container if removed). |
| `stop.go` | `yoloai stop` — stop a running sandbox. |
| `envname.go` | `resolveName()` — resolves sandbox name from args or `YOLOAI_SANDBOX` env var. |
| `helpers.go` | `withClient()`, `withManager()` — create Docker client / Manager for command handlers. |
| `pager.go` | `RunPager()` — pipe output through `$PAGER` or `less` when stdout is a TTY. |
| `envname_test.go` | Tests for name resolution. |
| `pager_test.go` | Tests for pager. |

### `internal/docker/`

| File | Purpose |
|------|---------|
| `client.go` | `Client` interface (subset of Docker SDK), `NewClient()` with daemon ping. |
| `resources.go` | `//go:embed` for Dockerfile.base, entrypoint.sh, tmux.conf. `SeedResources()` writes them to `~/.yoloai/` respecting user customizations. |
| `build.go` | `BuildBaseImage()` — builds `yoloai-base` image. `NeedsBuild()` / `RecordBuildChecksum()` for rebuild detection. Tar context creation, build output streaming. |
| `resources/Dockerfile.base` | Container Dockerfile (embedded at compile time). |
| `resources/entrypoint.sh` | Container entrypoint script (embedded at compile time). |
| `resources/tmux.conf` | Default tmux config (embedded at compile time). |
| `build_test.go` | Unit tests for build/seed logic. |
| `client_integration_test.go` | Integration tests requiring Docker daemon. Build tag: `integration`. |

### `internal/sandbox/`

| File | Purpose |
|------|---------|
| `manager.go` | `Manager` struct — central orchestrator. `EnsureSetup()` / `EnsureSetupNonInteractive()` for first-run auto-setup (dirs, resources, image, config). |
| `create.go` | `Create()` — full sandbox creation: validate, safety checks, copy workdir, git baseline, seed files, build mounts, launch container. Also contains `launchContainer()`, `buildMounts()`, `createSecretsDir()`, `copySeedFiles()`. |
| `lifecycle.go` | `Start()`, `Stop()`, `Destroy()`, `Reset()` — sandbox lifecycle. `recreateContainer()` and `relaunchAgent()` for restart scenarios. `resetInPlace()` for `--no-restart` resets. |
| `diff.go` | `GenerateDiff()`, `GenerateDiffStat()`, `GenerateCommitDiff()`, `ListCommitsWithStats()` — diff generation for both `:copy` and `:rw` modes. |
| `apply.go` | `GeneratePatch()`, `CheckPatch()`, `ApplyPatch()` — squash apply via `git apply`. `GenerateFormatPatch()`, `ApplyFormatPatch()` — per-commit apply via `git am`. `ListCommitsBeyondBaseline()`, `AdvanceBaseline()`, `AdvanceBaselineTo()`. |
| `inspect.go` | `DetectStatus()` — queries Docker + tmux for sandbox state. `InspectSandbox()`, `ListSandboxes()` — metadata + live status. `execInContainer()` helper. |
| `meta.go` | `Meta` / `WorkdirMeta` structs, `SaveMeta()` / `LoadMeta()` — sandbox metadata persistence as `meta.json`. |
| `paths.go` | `EncodePath()` / `DecodePath()` — caret encoding for filesystem-safe names. `ContainerName()`, `Dir()`, `WorkDir()`, `RequireSandboxDir()`. |
| `parse.go` | `ParseDirArg()` — parses `path:copy`, `path:rw`, `path:force` suffixes into `DirArg`. |
| `safety.go` | `IsDangerousDir()`, `CheckPathOverlap()`, `CheckDirtyRepo()` — pre-creation safety checks. |
| `config.go` | `loadConfig()`, `updateConfigFields()` — read/write `~/.yoloai/config.yaml` preserving YAML comments via `yaml.Node`. |
| `setup.go` | `RunSetup()`, `runNewUserSetup()` — interactive tmux configuration setup. Classifies user's tmux config, prompts for preferences. |
| `confirm.go` | `Confirm()` — simple y/N interactive prompt. |
| `errors.go` | `UsageError` (exit 2), `ConfigError` (exit 3), sentinel errors (`ErrSandboxNotFound`, `ErrSandboxExists`, etc.). |
| `*_test.go` | Unit tests for each file above. `integration_test.go` has the `integration` build tag. |

## Key Types

### `sandbox.Manager`
Central orchestrator. Holds a `docker.Client`, logger, and I/O streams. All sandbox operations go through it: `Create()`, `Start()`, `Stop()`, `Destroy()`, `Reset()`, `EnsureSetup()`.

### `sandbox.Meta` / `sandbox.WorkdirMeta`
Persisted as `meta.json` in each sandbox dir. Records creation-time state: agent, model, workdir path/mode/baseline SHA, network mode, ports.

### `sandbox.CreateOptions`
All parameters for `Manager.Create()`. Mirrors CLI flags: name, workdir, agent, model, prompt, network, ports, replace, attach, passthrough args.

### `sandbox.DiffOptions` / `sandbox.DiffResult`
Input/output for `GenerateDiff()`. Supports path filtering and stat-only mode. `DiffResult` carries the diff text, workdir, mode, and empty flag.

### `agent.Definition`
Describes an agent's commands (interactive/headless), prompt delivery mode, API key env vars, seed files, state directory, tmux submit sequence, model flag/aliases. Built-in: `claude` and `test`.

### `docker.Client`
Interface wrapping the Docker SDK methods used by yoloAI: image build/inspect, container CRUD, exec, ping/close. Defined for testability.

## Command → Code Map

| CLI Command | Entry Point | Core Logic |
|-------------|-------------|------------|
| `yoloai new` | `cli/commands.go:newNewCmd` | `sandbox.Manager.Create()` in `sandbox/create.go` |
| `yoloai attach` | `cli/attach.go:newAttachCmd` | `docker exec` + `tmux attach` via `cli/commands.go:attachToSandbox` |
| `yoloai diff` | `cli/diff.go:newDiffCmd` | `sandbox.GenerateDiff()` in `sandbox/diff.go` |
| `yoloai apply` | `cli/apply.go:newApplyCmd` | `sandbox.GeneratePatch()` / `ApplyPatch()` / `ApplyFormatPatch()` in `sandbox/apply.go` |
| `yoloai start` | `cli/start.go:newStartCmd` | `sandbox.Manager.Start()` in `sandbox/lifecycle.go` |
| `yoloai stop` | `cli/stop.go:newStopCmd` | `sandbox.Manager.Stop()` in `sandbox/lifecycle.go` |
| `yoloai destroy` | `cli/destroy.go:newDestroyCmd` | `sandbox.Manager.Destroy()` in `sandbox/lifecycle.go` |
| `yoloai reset` | `cli/reset.go:newResetCmd` | `sandbox.Manager.Reset()` in `sandbox/lifecycle.go` |
| `yoloai list` | `cli/list.go:newListCmd` | `sandbox.ListSandboxes()` in `sandbox/inspect.go` |
| `yoloai show` | `cli/show.go:newShowCmd` | `sandbox.InspectSandbox()` in `sandbox/inspect.go` |
| `yoloai log` | `cli/log.go:newLogCmd` | Reads `log.txt` from sandbox dir |
| `yoloai exec` | `cli/exec.go:newExecCmd` | `docker exec` into running container |
| `yoloai build` | `cli/commands.go:newBuildCmd` | `docker.BuildBaseImage()` in `docker/build.go` |
| `yoloai setup` | `cli/setup.go:newSetupCmd` | `sandbox.Manager.RunSetup()` in `sandbox/setup.go` |
| `yoloai completion` | `cli/commands.go:newCompletionCmd` | Cobra's built-in completion generators |
| `yoloai version` | `cli/commands.go:newVersionCmd` | Prints build-time version info |

## Data Flow

### Sandbox Creation (`yoloai new`)

```
newNewCmd (cli/commands.go)
  → withManager (cli/helpers.go)
    → Manager.Create (sandbox/create.go)
      → EnsureSetup: create dirs, seed resources, build image, write config.yaml
      → prepareSandboxState:
          ParseDirArg → validate name/agent/workdir → safety checks
          → copyDir (cp -rp) → removeGitDirs → gitBaseline (git init + commit)
          → copySeedFiles → ensureContainerSettings
          → readPrompt → resolveModel → buildAgentCommand
          → SaveMeta (meta.json) → write prompt.txt, log.txt, config.json
      → launchContainer:
          createSecretsDir (temp files from env vars)
          → buildMounts → ContainerCreate → ContainerStart
          → verify container is running → cleanup secrets
```

### Diff (`yoloai diff`)

```
newDiffCmd (cli/diff.go)
  → GenerateDiff (sandbox/diff.go)
    → loadDiffContext: LoadMeta → resolve workDir + baselineSHA
    → :copy mode: stageUntracked (git add -A) → git diff --binary <baseline>
    → :rw mode: git diff HEAD on live host dir
```

### Apply (`yoloai apply`)

Two modes — squash and selective:

**Squash (default):**
```
applySquash (cli/apply.go)
  → GeneratePatch (sandbox/apply.go): git diff --binary against baseline
  → CheckPatch: git apply --check
  → Confirm with user
  → ApplyPatch: git apply
  → AdvanceBaseline: update meta.json baseline SHA to HEAD
```

**Selective (commit refs):**
```
applySelectedCommits (cli/apply.go)
  → ResolveRefs (sandbox/apply.go): resolve short SHAs / ranges
  → GenerateFormatPatchForRefs: git format-patch per commit
  → ApplyFormatPatch: git am --3way
  → AdvanceBaselineTo: advance baseline to contiguous prefix
```

### Container Start/Restart (`yoloai start`)

```
Manager.Start (sandbox/lifecycle.go)
  → DetectStatus (sandbox/inspect.go): Docker inspect + tmux query
  → StatusRunning: no-op
  → StatusDone/Failed: relaunchAgent via tmux respawn-pane
  → StatusStopped: ContainerStart
  → StatusRemoved: recreateContainer (rebuild state from meta.json, relaunch)
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
│       ├── agent-state/     # Mounted at agent's StateDir (e.g., /home/yoloai/.claude/)
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
3. If the command needs a Manager, use `withManager()` from `helpers.go`

**Add a new agent:**
1. Add a new entry to the `agents` map in `internal/agent/agent.go`
2. Define: commands, prompt mode, API key env vars, seed files, state dir, submit sequence, startup delay, model aliases

**Add a new CLI flag to an existing command:**
1. Add `cmd.Flags().XxxP(...)` in the command's `newXxxCmd()` function
2. Read it with `cmd.Flags().GetXxx(...)` in the `RunE` handler

**Change container setup (Dockerfile, entrypoint):**
1. Edit files in `internal/docker/resources/`
2. They're embedded at compile time via `//go:embed` in `internal/docker/resources.go`
3. `SeedResources()` in `internal/docker/build.go` handles deploying them to `~/.yoloai/`

**Change how sandbox state is persisted:**
1. Modify `Meta` / `WorkdirMeta` in `internal/sandbox/meta.go`
2. Update `prepareSandboxState()` in `internal/sandbox/create.go` where meta is populated
3. Update any consumers that `LoadMeta()` and use the changed fields

**Change diff/apply behavior:**
1. Diff generation: `internal/sandbox/diff.go`
2. Patch generation and application: `internal/sandbox/apply.go`
3. CLI presentation: `internal/cli/diff.go` and `internal/cli/apply.go`

**Change container creation (mounts, networking):**
1. Mount construction: `buildMounts()` in `internal/sandbox/create.go`
2. Container config: `launchContainer()` in `internal/sandbox/create.go`
3. Port parsing: `parsePortBindings()` in `internal/sandbox/create.go`

**Change sandbox status detection:**
1. `DetectStatus()` in `internal/sandbox/inspect.go` — queries Docker and tmux
2. Status constants are in the same file

**Change config.yaml handling:**
1. `loadConfig()` / `updateConfigFields()` in `internal/sandbox/config.go`
2. Add new fields to `yoloaiConfig` struct and the YAML node walker

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
go test ./internal/docker/
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
- `internal/docker/client_integration_test.go`
- `internal/sandbox/integration_test.go`

All other `_test.go` files are standard unit tests that run without Docker.

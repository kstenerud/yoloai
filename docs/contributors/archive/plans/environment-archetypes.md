# Environment Archetypes

Implements the two-layer environment model from `docs/contributors/design/environments.md`.
Streams 1 (lifecycle commands + port injection) and 2 (VS Code workspace injection)
only. Stream 3 (devcontainer image wrapping, `remoteUser`) is explicitly deferred.

**Before starting:** read `docs/contributors/design/environments.md` in full. It is the
authoritative spec. This plan is the implementation decomposition of that spec.

**Quality gate:** `make check` must pass after every phase before committing.
One commit per phase.

---

## Out of scope (do not implement)

- `image:` / `build:` fields from devcontainer.json (stream 3 — image wrapping)
- `remoteUser` / `containerUser` — requires changing 11 hardcoded `/home/yoloai`
  paths across `agent/agent.go`, `sandbox/create.go`, and `entrypoint.py`
- Actual version checking for `requires:` (no practical way to introspect image
  without running it; parse and warn only)
- `yoloai reconfigure --archetype` (not in scope of this plan)

---

## Phase 1 — Data model and parsers

No behavior changes. All items are independent and can be written in parallel.
All new files need a purpose comment near the top with lines prefixed `ABOUTME: `.

### 1a. `sandbox/devcontainer.go` — new file

Parse the devcontainer.json field subset defined in the design doc field mapping table.

**Types:**

```go
// LifecycleCmd holds one devcontainer lifecycle command in any of the three
// spec-defined forms: string, []string, or map[string]any (parallel named commands).
type LifecycleCmd struct{ raw any }

// UnmarshalJSON accepts string, []string, or object forms.
func (c *LifecycleCmd) UnmarshalJSON(b []byte) error

// IsZero reports whether no command was specified.
func (c *LifecycleCmd) IsZero() bool

// DevcontainerConfig holds the parsed subset of devcontainer.json that yoloAI uses.
type DevcontainerConfig struct {
    // Image resolution (stream 3 — not used yet, parsed for future)
    Image string `json:"image,omitempty"`
    // Build is intentionally omitted for now — just detect its presence
    BuildPresent bool `json:"-"`

    ForwardPorts []int    `json:"forwardPorts,omitempty"`
    AppPort      []int    `json:"appPort,omitempty"`
    RemoteEnv    map[string]string `json:"remoteEnv,omitempty"`
    ContainerEnv map[string]string `json:"containerEnv,omitempty"`

    OnCreateCommand        LifecycleCmd `json:"onCreateCommand,omitempty"`
    UpdateContentCommand   LifecycleCmd `json:"updateContentCommand,omitempty"`
    PostCreateCommand      LifecycleCmd `json:"postCreateCommand,omitempty"`
    PostStartCommand       LifecycleCmd `json:"postStartCommand,omitempty"`

    Mounts          []string `json:"mounts,omitempty"`
    WorkspaceFolder string   `json:"workspaceFolder,omitempty"`

    // Not used yet (stream 3)
    RemoteUser    string `json:"remoteUser,omitempty"`
    ContainerUser string `json:"containerUser,omitempty"`

    Features        map[string]any  `json:"features,omitempty"`
    RunArgs         []string        `json:"runArgs,omitempty"`
    InitializeCommand LifecycleCmd  `json:"initializeCommand,omitempty"`
    PostAttachCommand LifecycleCmd  `json:"postAttachCommand,omitempty"`
    DockerComposeFile any           `json:"dockerComposeFile,omitempty"` // string or []string

    Customizations struct {
        VSCode struct {
            Extensions []string       `json:"extensions,omitempty"`
            Settings   map[string]any `json:"settings,omitempty"`
        } `json:"vscode,omitempty"`
    } `json:"customizations,omitempty"`

    Name            string `json:"name,omitempty"`
    WaitFor         string `json:"waitFor,omitempty"`
    HostRequirements any    `json:"hostRequirements,omitempty"`
    ShutdownAction  string `json:"shutdownAction,omitempty"`
}
```

**Functions:**

`LoadDevcontainer(path string) (*DevcontainerConfig, error)` — read and JSON-decode
the file. Detect presence of a `build` key (set `BuildPresent = true`) without
fully parsing it.

`(dc *DevcontainerConfig) ExtractPorts() []string` — union of `forwardPorts` and
`appPort`, converted to `"<port>:<port>"` strings for compatibility with
`parsePortBindings`.

`(dc *DevcontainerConfig) FilterMounts(workdirMountPath string) (mounts []string, warnings []string)` —
evaluate each entry in `dc.Mounts`:
- Expand `${localEnv:HOME}` to `os.UserHomeDir()`
- Strip with error-level warning: docker socket sources (`/var/run/docker.sock`,
  `//./pipe/docker_engine`)
- Strip with warning: agent credential dirs (sources matching `~/.claude`,
  `~/.gemini`, `~/.codex`, `~/.local/share/opencode`, `~/.codex`)
- Strip with warning: any mount whose target equals `workdirMountPath`
- Pass everything else through unchanged
Return safe mounts and accumulated warning strings (caller prints them).

`(dc *DevcontainerConfig) MergedEnv() map[string]string` — merge `remoteEnv` and
`containerEnv` (remoteEnv takes precedence on conflict).

`(dc *DevcontainerConfig) ParsedRunArgs() (cpus string, memory string, capAdd []string, unknownWarnings []string)` —
parse `--cpus`, `--memory`, `--cap-add` from `runArgs`; collect anything else
into `unknownWarnings`.

`(dc *DevcontainerConfig) PostStartCommandUsesCompose() bool` — returns true if
any string form of `postStartCommand` contains `docker compose` or `docker-compose`.

**Validation helpers (called from archetype expansion, not from Load):**

`(dc *DevcontainerConfig) WarnIgnoredFields(w io.Writer)` — print warnings for
`features`, `initializeCommand`, `postAttachCommand`, `waitFor`, `hostRequirements`,
`shutdownAction`, `name` (all ignored).

`(dc *DevcontainerConfig) DockerComposeFilePresent() bool` — true if
`dockerComposeFile` is non-nil/non-empty.

**Tests (`sandbox/devcontainer_test.go`):**
- All three `LifecycleCmd` forms (string, array, object)
- `IsZero` for absent field
- `ExtractPorts` with forwardPorts, appPort, both
- `FilterMounts` strips docker socket, credential dirs, workdir conflict; passes others
- `FilterMounts` expands `${localEnv:HOME}`
- `MergedEnv` merge and precedence
- `ParsedRunArgs` known and unknown flags
- `PostStartCommandUsesCompose` true/false cases
- `DockerComposeFilePresent` string and array forms

---

### 1b. `sandbox/yoloaiyaml.go` — new file

`YoloAIProjectConfig` struct:
```go
type YoloAIProjectConfig struct {
    Archetype string            `yaml:"archetype,omitempty"`
    Mounts    []string          `yaml:"mounts,omitempty"`
    Requires  map[string]string `yaml:"requires,omitempty"`
}
```

`LoadYoloAIYaml(workdir string) (*YoloAIProjectConfig, bool, error)` — look for
`.yoloai.yaml` in `workdir`. Returns `(nil, false, nil)` if not found. Returns
error only for parse failures. Validates `Archetype` against known values if
non-empty (unknown archetype → error). Expands tilde in each `Mounts` entry via
`config.ExpandPath`.

**Tests (`sandbox/yoloaiyaml_test.go`):**
- Missing file → `(nil, false, nil)`
- Valid file with all fields
- Unknown archetype → error
- Tilde expansion in mounts

---

### 1c. `sandbox/archetype.go` — new file

```go
type Archetype string

const (
    ArchetypeSimple       Archetype = "simple"
    ArchetypeCompose      Archetype = "compose"
    ArchetypeDevcontainer Archetype = "devcontainer"
    ArchetypeApple        Archetype = "apple"
)
```

`ParseArchetype(s string) (Archetype, error)` — validate against known set.

`DetectArchetype(workdir string) (Archetype, []string)` — inspect workdir,
return archetype + signal strings (human-readable, used in transparency output).
Detection priority (first match wins):
1. `.devcontainer/devcontainer.json` or `devcontainer.json` → `devcontainer`,
   signal: `"found .devcontainer/devcontainer.json"`
2. `docker-compose.yaml` or `docker-compose.yml` (and no devcontainer.json) →
   `compose`, signal: `"found docker-compose.yaml"`
3. `.xcodeproj`, `.xcworkspace`, or `Package.swift` at root → `apple`,
   signal: `"found <filename>"`
4. Nothing → `simple`, signal: `"no project signals detected"`

`ValidArchetypes() []string` — returns sorted list of valid archetype names
(for help text and error messages).

**Tests (`sandbox/archetype_test.go`):**
- Each detection signal in isolation
- Priority: devcontainer.json beats docker-compose.yaml
- Priority: devcontainer.json beats Xcode files
- No signals → simple
- ParseArchetype valid and invalid

---

### 1d. Data model field additions — small edits to existing files

**`sandbox/meta.go`** — add to `Meta` struct:
```go
Archetype string `json:"archetype,omitempty"`
```

**`sandbox/sandbox_state.go`** — add to `SandboxState` struct:
```go
OnCreateCommandsDone bool `json:"on_create_commands_done,omitempty"`
```

**`sandbox/create.go`** `CreateOptions` struct — add:
```go
Archetype string // archetype name; empty = auto-detect
```

**`internal/cli/commands.go`** — in `newNewCmd()`, add flag and wire it:
```go
cmd.Flags().String("archetype", "", fmt.Sprintf("Environment archetype (%s)", strings.Join(sandbox.ValidArchetypes(), "|")))
```
In the `RunE` handler, read and pass to `CreateOptions.Archetype`.

---

## Phase 2 — Archetype resolution, expansion, and transparency

All logic lives in `sandbox/create_prepare.go`, early in `prepareSandboxState`,
before any directory operations.

### Step-by-step in `prepareSandboxState`

**Step 1: Resolve archetype**

```
priority: opts.Archetype (CLI) > yoloaiyaml.Archetype > DetectArchetype(workdir)
```

Load `.yoloai.yaml` unconditionally (tolerate missing). Resolve archetype string
to `Archetype` type.

**Step 2: Platform check**

If `archetype == ArchetypeApple` and the host is not Apple Silicon macOS (check
via `tart.IsAppleSilicon()` which already exists in `runtime/tart/platform.go`):
return error:
```
the "apple" archetype requires Apple Silicon macOS (Tart backend).
Use --archetype simple for agent-only work on this project.
```

**Step 3: `requires:` validation**

If `.yoloai.yaml` has `requires:` entries: print a warning for each entry:
```
Warning: requires: go >=1.26 — version verification not yet implemented; continuing.
```
Then prompt "Continue anyway? [y/N]" using `sandbox.Confirm()`. Honor `--yes`
(skip prompt if `opts.Yes` is true).

**Step 4: Archetype expansion**

Apply archetype-specific changes to opts and state:

**`compose`:**
- If `opts.Isolation` is empty or `"container"`: set `opts.Isolation = "container-privileged"`
- Set `state.dockerdRequired = true` (new field on sandboxState)

**`devcontainer`:**
- Load devcontainer.json from `.devcontainer/devcontainer.json` or `devcontainer.json`
  (first found). Store in `state.devcontainer`.
- Error if `dc.DockerComposeFilePresent()`: return error about unsupported multi-container devcontainer
- Call `dc.WarnIgnoredFields(m.output)` to print warnings for unsupported fields
- Warn for any `unknownWarnings` from `dc.ParsedRunArgs()`
- If `dc.PostStartCommandUsesCompose()`: apply compose expansion (same as `compose` archetype)
- Merge `dc.MergedEnv()` into `opts.Env` (existing env wins on conflict — user's explicit --env flags take precedence)
- Merge `dc.ExtractPorts()` into `opts.Ports` (dedup)
- Apply parsed runArgs: if cpus non-empty and `opts.Resources.CPUs` is zero, set it; same for memory; append cap-add entries
- If `dc.WorkspaceFolder != ""`: set `opts.WorkdirMountPath = dc.WorkspaceFolder`
- Store filtered mounts for later (phase 6): `state.devcontainerMounts, state.devcontainerMountWarnings`

**`apple`:**
- Set `opts.Backend = "tart"` (if not already set)

**`simple`:** no-op.

Save resolved archetype to `state.archetype` (new field on sandboxState, type `Archetype`).

**Step 5: Transparency output**

After resolution and expansion, print the causal chain. Only print when not in
quiet mode (`m.quiet` — check however other commands check this) and not in JSON
mode.

Format:
```
→ Detected docker-compose.yaml
  Archetype: compose
  Because of this:
    · isolation set to container-privileged (Compose requires nested Docker)
    · dockerd will auto-start before lifecycle commands
  To suppress: --archetype simple   To inspect: --dry-run (not yet implemented)
```

Print per-archetype bullets based on what expansion actually set (don't print
bullets for things that weren't changed).

For JSON mode, add an `inference` key to whatever top-level JSON object `yoloai new`
emits (if any). If `yoloai new` doesn't emit JSON yet, skip the JSON inference
field — just suppress the text output.

**Step 6: Persist to Meta**

Set `Meta.Archetype = string(state.archetype)` when building Meta in
`prepareSandboxState` (where other Meta fields are set).

**Add `sandboxState` fields** (the internal struct, not `SandboxState`):
```go
archetype              Archetype
dockerdRequired        bool
devcontainer           *DevcontainerConfig
devcontainerMounts     []string
devcontainerMountWarnings []string
```

**Tests (`sandbox/archetype_resolution_test.go` or inline in `create_prepare` tests):**
- CLI flag overrides .yoloai.yaml which overrides auto-detection
- Apple + non-macOS → error
- requires: warns and prompts; --yes skips prompt
- compose expansion sets isolation
- devcontainer expansion merges env and ports
- devcontainer compose-detection triggers compose expansion

---

## Phase 3 — dockerd auto-start and lifecycle command execution

Two subparts: Go (writes lifecycle config to runtime-config.json) and Python
(reads and executes).

### 3a. runtime-config.json schema extension

Find where runtime-config.json is written (likely `sandbox/create_instance.go`
or `sandbox/create_prepare.go` — look for `RuntimeConfigFile` or `runtime-config.json`).
Add a `lifecycle` section:

```json
"lifecycle": {
  "dockerd_required": false,
  "on_create_done": false,
  "on_create": [...],
  "on_start": [...]
}
```

Each command entry in the arrays is one of:
```json
{"type": "string", "cmd": "go mod download && make tools"}
{"type": "array",  "cmd": ["go", "mod", "download"]}
{"type": "object", "cmd": {"download": "go mod download", "tools": "make tools"}}
```

**Go helper:** `lifecycleCmdToJSON(lc LifecycleCmd) map[string]any` — convert a
`LifecycleCmd` to the above JSON representation.

**Population logic** (in the same place runtime-config.json is written, after
archetype expansion):
- `dockerd_required`: `state.dockerdRequired`
- `on_create_done`: `state.OnCreateCommandsDone` (loaded from SandboxState earlier)
- `on_create`: `[onCreateCommand, updateContentCommand, postCreateCommand]` filtered
  to non-zero entries, in that order
- `on_start`: `[postStartCommand]` filtered to non-zero entries

This applies only when `state.archetype == ArchetypeDevcontainer` (or compose
sub-detection). For other archetypes, the `lifecycle` section is omitted or empty.

### 3b. `runtime/monitor/sandbox-setup.py` additions

Add these functions. Insert the `run_lifecycle_commands()` call in the main
execution path after git baseline setup but before agent launch.

```python
def start_dockerd(log):
    """Start the Docker daemon and wait for it to be ready."""
    import shutil, time
    if shutil.which("docker") is None:
        log("dockerd: docker not found, skipping")
        return
    # Check if already running
    r = subprocess.run(["docker", "info"], capture_output=True)
    if r.returncode == 0:
        log("dockerd: already running")
        return
    log("dockerd: starting...")
    subprocess.Popen(
        ["sudo", "dockerd", "--storage-driver=fuse-overlayfs"],
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
    )
    # Poll socket until ready (30s timeout)
    deadline = time.time() + 30
    while time.time() < deadline:
        r = subprocess.run(["docker", "info"], capture_output=True)
        if r.returncode == 0:
            log("dockerd: ready")
            return
        time.sleep(0.5)
    log("dockerd: timed out waiting for daemon")
    # Don't hard-fail — lifecycle commands will fail with a clear error


def run_lifecycle_command(cmd_entry, log):
    """Run one lifecycle command entry (string, array, or object form).

    Object form runs all values in parallel; fails if any exit non-zero.
    Returns True on success, False on failure.
    """
    from concurrent.futures import ThreadPoolExecutor, as_completed

    kind = cmd_entry.get("type")
    cmd  = cmd_entry.get("cmd")

    if kind == "string":
        r = subprocess.run(["sh", "-c", cmd])
        if r.returncode != 0:
            log(f"lifecycle command failed (exit {r.returncode}): {cmd}")
            return False
    elif kind == "array":
        r = subprocess.run(cmd)
        if r.returncode != 0:
            log(f"lifecycle command failed (exit {r.returncode}): {cmd}")
            return False
    elif kind == "object":
        failures = []
        def run_one(name, subcmd):
            r = subprocess.run(["sh", "-c", subcmd] if isinstance(subcmd, str) else subcmd)
            return name, r.returncode
        with ThreadPoolExecutor() as pool:
            futures = {pool.submit(run_one, n, c): n for n, c in cmd.items()}
            for fut in as_completed(futures):
                name, rc = fut.result()
                if rc != 0:
                    failures.append(f"{name} (exit {rc})")
        if failures:
            log(f"lifecycle commands failed: {', '.join(failures)}")
            return False
    return True


def run_lifecycle_commands(cfg, yoloai_dir, log):
    """Run lifecycle commands from the runtime config.

    On-create commands run once (guarded by marker file).
    On-start commands run on every start.
    dockerd is started first if required.
    """
    lifecycle = cfg.get("lifecycle")
    if not lifecycle:
        return

    if lifecycle.get("dockerd_required"):
        start_dockerd(log)

    marker = os.path.join(yoloai_dir, "lifecycle-on-create-done")

    if not lifecycle.get("on_create_done") and not os.path.exists(marker):
        log("lifecycle: running on-create commands")
        for entry in lifecycle.get("on_create", []):
            if not run_lifecycle_command(entry, log):
                log("lifecycle: on-create command failed; skipping remaining on-create commands")
                break
        else:
            # All on-create commands succeeded — write marker
            try:
                open(marker, "w").close()
            except OSError as e:
                log(f"lifecycle: could not write marker: {e}")

    log("lifecycle: running on-start commands")
    for entry in lifecycle.get("on_start", []):
        if not run_lifecycle_command(entry, log):
            log("lifecycle: on-start command failed")
            # Continue with remaining on-start commands (partial start is better than none)
```

Insert into the main flow (find the right location by searching for agent launch):
```python
run_lifecycle_commands(cfg, yoloai_dir, log_fn)
```

### 3c. Marker file → Go on next start

In `sandbox/lifecycle.go` `Start()`, after loading `SandboxState`:
```go
markerPath := filepath.Join(dir, "lifecycle-on-create-done")
if _, err := os.Stat(markerPath); err == nil && !state.OnCreateCommandsDone {
    state.OnCreateCommandsDone = true
    if err := SaveSandboxState(dir, state); err != nil {
        // log but don't fail
    }
}
```

This ensures `on_create_done: true` is reflected in the next runtime-config.json
write (via the field population logic from 3a), so Python skips on-create commands
on subsequent starts.

---

## Phase 4 — Mounts passthrough and `.yoloai.yaml` mounts

In `sandbox/create_prepare.go`, after archetype expansion (step 4) and before
building the final mount list:

**Devcontainer mounts:** if `state.devcontainerMounts` is non-empty:
- Print each warning from `state.devcontainerMountWarnings`
- Append each safe mount string to `opts.Mounts`

**`.yoloai.yaml` mounts:** if `.yoloai.yaml` was loaded and has `Mounts`:
- Append each entry to `opts.Mounts` (already expanded by `LoadYoloAIYaml`)

Mount deduplication: identical mount strings are deduplicated (use a seen-set).
CLI-specified mounts (already in `opts.Mounts` before this phase) are not
overridden.

No new tests needed beyond what phase 1a covers for `FilterMounts`.

---

## Phase 5 — VS Code workspace injection

### 5a. `sandbox/vscode.go` — new file

```go
// InjectVSCodeWorkspace writes VS Code workspace files into the workdir copy
// based on devcontainer.json customizations. Called only when vscode-tunnel
// is active and the workdir mode supports writes (:copy or :overlay).
func InjectVSCodeWorkspace(workdirCopyPath string, dc *DevcontainerConfig) error
```

Implementation:
- Skip if `dc.Customizations.VSCode.Extensions` is empty and
  `dc.Customizations.VSCode.Settings` is nil
- Create `.vscode/` dir if absent
- **extensions.json:** if file exists, unmarshal JSON, merge `recommendations`
  arrays (dedup, preserve existing); if absent, create with `{"recommendations": [...]}`.
  Marshal with `json.MarshalIndent`.
- **settings.json:** if file exists, unmarshal into `map[string]any`, merge
  devcontainer settings (existing keys win — preserve project's checked-in settings);
  if absent, create with devcontainer settings only. Marshal with `json.MarshalIndent`.

**Call site** in `sandbox/create_prepare.go`, after the workdir copy step:
```go
if state.archetype == ArchetypeDevcontainer &&
    opts.VscodeTunnel &&
    state.workdirMode != "rw" &&
    state.devcontainer != nil {
    if err := InjectVSCodeWorkspace(workdirCopyPath, state.devcontainer); err != nil {
        // log warning but don't fail sandbox creation
    }
}
```

**Tests (`sandbox/vscode_test.go`):**
- Write from scratch (both files absent)
- Merge extensions (dedup)
- Merge settings (existing keys win)
- Empty extensions + empty settings → no files written
- `:rw` mode skipped (test via call condition, not function)

---

## Phase 6 — `yoloai sandbox <name> vscode` command

### `internal/cli/sandbox_vscode.go` — new file

```go
func newSandboxVscodeCmd() *cobra.Command
```

Handler:
1. Resolve sandbox name via `resolveName`
2. Load meta via `sandbox.LoadMeta`
3. Determine if backend supports container attach:
   - Docker, Podman: yes
   - Tart, Seatbelt, Containerd: no → print:
     ```
     Container attach is not supported for the <backend> backend.
     Use --vscode-tunnel when creating the sandbox instead.
     ```
4. For Docker/Podman: build the attach URI:
   - `containerName = sandbox.InstanceName(meta.Name)`
   - JSON payload: `{"containerName":"<containerName>"}`
   - Hex-encode the JSON bytes as lowercase hex
   - URI: `vscode-remote://attached-container+<hex>/<workdir_mount_path>`
5. If `code` is on PATH (`exec.LookPath("code")`):
   - Exec: `code --folder-uri "<uri>"`
6. If not:
   - Print:
     ```
     Install the VS Code CLI and run:
     code --folder-uri "<uri>"
     ```

Register in `internal/cli/sandbox_cmd.go` by appending to the `sandbox` command:
```go
sandboxCmd.AddCommand(newSandboxVscodeCmd())
```

Also register as `yoloai vscode <name>` top-level alias in `commands.go`
`registerCommands()`, following the same pattern as `yoloai ls`, `yoloai log`,
`yoloai exec`.

**No tests required** for this command (it's UI-only with no logic to unit-test
beyond what integration tests would cover).

---

## Phase 7 — ARCHITECTURE.md updates

Update `docs/contributors/architecture/README.md`:

**File Index — `sandbox/`:** add entries for `devcontainer.go`, `yoloaiyaml.go`,
`archetype.go`, `vscode.go`.

**File Index — `internal/cli/`:** add entry for `sandbox_vscode.go`.

**Command → Code Map:** add row for `yoloai sandbox <name> vscode`.

**Data Flow — Sandbox Creation:** add a bullet after "resolve profile chain" noting
archetype resolution, expansion, and transparency output step.

---

## Commit sequence

```
Phase 1: Data: devcontainer/yoloaiyaml/archetype parsers and model fields
Phase 2: Archetype resolution, expansion, and transparency output
Phase 3: Lifecycle command execution (Go config + Python runtime)
Phase 4: Mounts passthrough from devcontainer.json and .yoloai.yaml
Phase 5: VS Code workspace injection
Phase 6: yoloai sandbox <name> vscode command
Phase 7: ARCHITECTURE.md updates
```

Each commit message should follow the existing project convention
(`Feature:` / `Feat:` prefix with brief description).

> **Superseded:** this `Feature:`/`Feat:` prefix convention is no longer current. The
> live commit-message contract is `AGENTS.md` ("Preparing a PR", rule 4): subject line
> is `type(scope): summary` (`feat`, `fix`, `docs`, `test`, `refactor`, `build`, `ci`,
> `chore`, `perf`), imperative, no trailing period. This plan document is preserved
> as-written for history; do not follow its commit-message example.

---

## Key cross-references

- Design spec: `docs/contributors/design/environments.md`
- `LifecycleCmd` parallel execution: design doc "Resolved Design Decisions" §postStartCommand
- devcontainer.json field table: design doc "Field Mapping"
- Mount stripping rules: design doc "Mounts Passthrough"
- Transparency format: design doc "Transparency Rule"
- `runtime-config.json` existing structure: `runtime/monitor/sandbox-setup.py:read_config()`
- `SandboxState` pattern for one-time tracking: `sandbox/agent_files.go` and
  `sandbox/sandbox_state.go` (existing `AgentFilesInitialized` field — follow same pattern)
- Port handling: `sandbox/create.go:parsePortBindings()` and `runtime.PortMapping`
- dockerd storage driver: `runtime/docker/resources/Dockerfile` lines 131-135
  (fuse-overlayfs already configured — use same driver when starting dockerd)
- `sandbox.Confirm()` for requires: prompt: `sandbox/confirm.go`
- Tart platform detection: `runtime/tart/platform.go:IsAppleSilicon()`

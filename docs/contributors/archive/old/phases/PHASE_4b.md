# Phase 4b: Sandbox Creation (`yoloai new`)

## Goal

Working `yoloai new` command that creates a sandbox with full-copy workdir, starts the Docker container, delivers the prompt, and prints context-aware creation output.

## Prerequisites

- Phase 4a complete (Manager, safety checks, error types, EnsureSetup)
- Docker daemon running
- Base image built (`yoloai build` or auto-built by EnsureSetup)

## Files to Create

| File | Description |
|------|-------------|
| `internal/sandbox/create.go` | `CreateOptions`, `Create` method, all helper functions |
| `internal/sandbox/create_test.go` | Unit tests for helper functions (model alias, agent command, config.json, port parsing) |
| `internal/sandbox/confirm.go` | Reusable confirmation prompt helper |

## Files to Modify

| File | Change |
|------|--------|
| `internal/sandbox/manager.go` | Add `Destroy` stub method (needed by `--replace`) |
| `internal/cli/commands.go` | Wire `yoloai new` with all flags and passthrough args |

## Types and Signatures

### `internal/sandbox/create.go`

```go
package sandbox

// CreateOptions holds all parameters for sandbox creation.
type CreateOptions struct {
	Name        string
	WorkdirArg  string   // raw workdir argument (path with optional :copy/:rw/:force suffixes)
	Agent       string   // agent name (e.g., "claude", "test")
	Model       string   // model name or alias (e.g., "sonnet", "claude-sonnet-4-latest")
	Prompt      string   // prompt text (from --prompt)
	PromptFile  string   // prompt file path (from --prompt-file)
	NetworkNone bool     // --network-none flag
	Ports       []string // --port flags (e.g., ["3000:3000"])
	Replace     bool     // --replace flag
	NoStart     bool     // --no-start flag
	Yes         bool     // --yes flag (skip confirmations)
	Passthrough []string // args after -- passed to agent
	Version     string   // yoloai version for meta.json
}

// Create creates and optionally starts a new sandbox.
func (m *Manager) Create(ctx context.Context, opts CreateOptions) error
```

**`Create` decomposition** — sequential helper calls:

```go
// prepareSandboxState handles steps 1-14: validation, safety checks,
// directory creation, workdir copy, git baseline, meta/config writing.
func (m *Manager) prepareSandboxState(ctx context.Context, opts CreateOptions) (*sandboxState, error)

// createAndStartContainer handles steps 16-18: Docker container
// creation, start, and credential cleanup.
func (m *Manager) createAndStartContainer(ctx context.Context, state *sandboxState) error

// printCreationOutput prints the context-aware summary (step 19).
func (m *Manager) printCreationOutput(state *sandboxState)
```

**`sandboxState`** — internal struct holding resolved state computed during preparation, passed between helpers:

```go
type sandboxState struct {
	name        string
	sandboxDir  string           // ~/.yoloai/sandboxes/<name>/
	workdir     *DirArg          // parsed workdir
	workCopyDir string           // ~/.yoloai/sandboxes/<name>/work/<encoded>/
	agent       *agent.Definition
	model       string           // resolved model (alias expanded)
	hasPrompt   bool
	networkMode string           // "" or "none"
	ports       []string
	meta        *Meta
	configJSON  []byte           // serialized /yoloai/config.json content
	keyFile     string           // path to temp API key file (empty if no keys needed)
}
```

### Helper functions (unexported, in `create.go`)

```go
// resolveModel expands a model alias using the agent definition.
// Returns the alias target if found, otherwise the input unchanged.
func resolveModel(agentDef *agent.Definition, model string) string

// buildAgentCommand constructs the full agent command string for
// config.json. Includes built-in flags (--dangerously-skip-permissions,
// --model) and passthrough args.
func buildAgentCommand(agentDef *agent.Definition, model string, prompt string, passthrough []string) string

// containerConfig is the serializable form of /yoloai/config.json
// written to the sandbox dir and bind-mounted into the container.
type containerConfig struct {
	HostUID        int    `json:"host_uid"`
	HostGID        int    `json:"host_gid"`
	AgentCommand   string `json:"agent_command"`
	StartupDelay   int    `json:"startup_delay"`
	SubmitSequence string `json:"submit_sequence"`
}

// buildContainerConfig creates the config.json content.
func buildContainerConfig(agentDef *agent.Definition, agentCommand string) ([]byte, error)

// readPrompt reads the prompt from --prompt, --prompt-file, or stdin.
// Returns the prompt text and an error if both are provided.
func readPrompt(prompt, promptFile string) (string, error)

// parsePortBindings converts ["host:container", ...] to Docker
// PortBindings and ExposedPorts.
func parsePortBindings(ports []string) (nat.PortMap, nat.PortSet, error)

// gitBaseline records or creates a git baseline for the work copy.
// If .git/ exists: returns HEAD SHA. Otherwise: git init + add + commit,
// returns the new commit SHA.
func gitBaseline(workDir string) (string, error)

// createKeyFile writes the API key(s) to a temp file and returns the
// path. Returns empty string if no keys needed.
func createKeyFile(agentDef *agent.Definition) (string, error)
```

### Confirmation prompt in `internal/sandbox/confirm.go`

```go
package sandbox

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// Confirm prints a prompt and reads y/N from the reader.
// Returns true if the user answered "y" or "yes" (case-insensitive).
func Confirm(prompt string, input io.Reader, output io.Writer) bool
```

### CLI wiring in `internal/cli/commands.go`

Replace the `newNewCmd` stub with the real implementation. The Cobra command:

- Positional args: `<name> [<workdir>]` — but with `--` passthrough, use `cobra.ArbitraryArgs` and manual parsing via `cmd.ArgsLenAtDash()`.
- Flags: `--prompt`/`-p`, `--prompt-file`/`-f`, `--model`/`-m`, `--agent`, `--network-none`, `--port` (repeatable), `--replace`, `--no-start`, `--yes`/`-y`.
- Creates Docker client, Manager, and calls `Manager.Create`.

## Detailed Create Workflow

### Step 1: EnsureSetup
Call `m.EnsureSetup(ctx)` — creates dirs, seeds resources, builds image if needed.

### Step 2: Parse workdir
Call `ParseDirArg(opts.WorkdirArg)`. If no mode specified, default to `"copy"`. Verify the path exists on disk (`os.Stat`).

### Step 3: Validate
- Name must be non-empty.
- Agent must be valid: `agent.GetAgent(opts.Agent)` must return non-nil.
- Sandbox must not already exist (check `os.Stat(Dir(name))`), unless `--replace`.
- `--prompt` and `--prompt-file` are mutually exclusive.
- Required API keys: for each key in `agentDef.APIKeyEnvVars`, check `os.Getenv`. Error with `ErrMissingAPIKey` if missing, naming the specific variable.

### Step 4: Safety checks
- `IsDangerousDir(workdir.Path)`: error unless `:force` was set (warning to `m.output` instead).
- `CheckPathOverlap([]string{workdir.Path})`: MVP has only one dir, so this is a no-op for now. Included for forward compatibility with aux dirs.

### Step 5: --replace
If `opts.Replace` and sandbox exists, call `m.Destroy(ctx, name, true)` (force destroy, skipping confirmation).

### Step 6: Dirty repo warning
Call `CheckDirtyRepo(workdir.Path)`. If dirty and `!opts.Yes`:
```
WARNING: /path/to/dir has uncommitted changes (3 files modified, 1 untracked)
These changes will be visible to the agent and could be modified or lost.
Continue? [y/N]
```
Read from `os.Stdin` via `Confirm`. If declined, return `nil` (user cancelled, not an error).

### Step 7: Create directory structure
```
~/.yoloai/sandboxes/<name>/
~/.yoloai/sandboxes/<name>/work/
~/.yoloai/sandboxes/<name>/agent-state/
```
All with `0750` permissions.

### Step 8: Copy workdir
For `:copy` mode: `cp -rp <source> <dest>` via `os/exec`. Destination is `work/<caret-encoded-path>/`.

For `:rw` mode: no copy needed (bind-mount at runtime). Still create the work dir for consistency.

### Step 9: Git baseline
Call `gitBaseline(workCopyDir)`:
- If `.git/` exists in the copy: `git -C <dir> rev-parse HEAD` → returns SHA.
- If no `.git/`: `git init`, `git add -A`, `git commit -m "yoloai baseline"`, `git rev-parse HEAD` → returns SHA.
- For `:rw` mode: record HEAD SHA of the original if it's a git repo, or skip (no baseline for non-git `:rw` dirs).

### Step 10-11: Write state files
- `meta.json` via `SaveMeta`.
- `prompt.txt` — written from `readPrompt` result if non-empty.
- `log.txt` — create empty file.
- `config.json` — serialized `containerConfig`.

### Step 12: Resolve model alias
`resolveModel(agentDef, opts.Model)` — looks up in `agentDef.ModelAliases`, returns mapped value or pass-through.

### Step 13: Build agent command
`buildAgentCommand(agentDef, resolvedModel, prompt, passthrough)`:

For **interactive mode** (Claude):
- Start with `agentDef.InteractiveCmd` (e.g., `"claude --dangerously-skip-permissions"`)
- Append `--model <resolved>` if model is non-empty and agent has ModelFlag
- Append passthrough args verbatim

For **headless mode with prompt** (test agent):
- Start with `agentDef.HeadlessCmd` with `PROMPT` replaced by the actual prompt text (shell-escaped)
- Append passthrough args

For **interactive mode without prompt** (test agent fallback):
- Use `agentDef.InteractiveCmd`
- Append passthrough args

Decision logic: if `agentDef.PromptMode == PromptModeHeadless` and prompt is non-empty, use headless cmd. Otherwise use interactive cmd.

### Step 14: Create API key temp file
`createKeyFile(agentDef)`:
- For each env var in `agentDef.APIKeyEnvVars`, read from host env.
- Write to `os.CreateTemp("", "yoloai-key-*")` with `0600` permissions.
- Format: `KEY_NAME=VALUE\n` per line (but MVP has only one key per agent, so single line).
- Actually, the entrypoint reads individual files from `/run/secrets/`. So create one temp file per key, named after the env var.
- Wait — the entrypoint iterates `/run/secrets/*` and exports `filename=content`. So we need one file per key where filename = env var name and content = value. For the temp file approach, we create a temp **directory** containing files named after each key.

Revised approach:
- Create temp dir via `os.MkdirTemp("", "yoloai-secrets-*")`.
- Write one file per API key: `<tmpdir>/<KEY_NAME>` containing the key value.
- Bind-mount each file as `/run/secrets/<KEY_NAME>` (read-only).
- Return the temp dir path for cleanup.

### Step 15: --no-start
If `opts.NoStart`, skip container creation. Print creation output and return.

### Step 16-17: Create and start container
Docker container configuration:

```go
config := &container.Config{
    Image:      "yoloai-base",
    WorkingDir: workdir.Path, // mirrored host path
}

init := true
hostConfig := &container.HostConfig{
    Init:         &init,
    NetworkMode:  container.NetworkMode(networkMode),
    PortBindings: portBindings,
    Mounts:       mounts,
}
```

**Mounts** (using `mount.Mount` with `Type: mount.TypeBind`):

| Source (host) | Target (container) | ReadOnly |
|---|---|---|
| `work/<encoded>/` | `<workdir.Path>` (mirrored) | false |
| `agent-state/` | `<agentDef.StateDir>` (e.g., `/home/yoloai/.claude/`) | false |
| `log.txt` | `/yoloai/log.txt` | false |
| `prompt.txt` | `/yoloai/prompt.txt` | true (if exists) |
| `config.json` | `/yoloai/config.json` | true |
| `<tmpdir>/<KEY>` | `/run/secrets/<KEY>` | true (per key) |

Skip `agent-state` mount if `agentDef.StateDir` is empty (test agent).
Skip `prompt.txt` mount if no prompt.
Skip secrets mounts if no API keys.

Container name: `yoloai-<name>`.

Call `m.client.ContainerCreate(ctx, config, hostConfig, nil, nil, containerName)`.
Call `m.client.ContainerStart(ctx, containerID, container.StartOptions{})`.

### Step 18: Credential cleanup
After container start, wait briefly (1 second) for the entrypoint to read secrets, then remove the temp secrets directory. Use `defer` for crash-safety, but also do explicit cleanup after the wait.

### Step 19: Creation output
Print to `m.output`:

```
Sandbox <name> created
  Agent:    <agent>
  Workdir:  <host_path> (copy|rw)
[  Network:  none]
[  Ports:    <port1>, <port2>]

Run 'yoloai attach <name>' to interact (Ctrl-b d to detach)
    'yoloai diff <name>' when done
```

- Network line: only shown when `--network-none` is used.
- Ports line: only shown when `--port` is used.
- "when done" line: only shown when prompt is present (agent is working autonomously).
- Without prompt: `Run 'yoloai attach <name>' to start working (Ctrl-b d to detach)`.

## Implementation Steps

1. **Create `internal/sandbox/confirm.go`:**
   - `Confirm(prompt, input, output)` — reads y/N from reader.

2. **Create `internal/sandbox/create.go`:**
   - `CreateOptions` struct.
   - `sandboxState` internal struct.
   - Helper functions: `resolveModel`, `buildAgentCommand`, `buildContainerConfig`, `readPrompt`, `parsePortBindings`, `gitBaseline`, `createKeyFile`.
   - `prepareSandboxState` method.
   - `createAndStartContainer` method.
   - `printCreationOutput` method.
   - `Create` method orchestrating all steps.

3. **Create `internal/sandbox/create_test.go`:**
   - Test `resolveModel` with alias, non-alias, and empty model.
   - Test `buildAgentCommand` for interactive and headless modes.
   - Test `buildContainerConfig` produces valid JSON.
   - Test `readPrompt` with direct text, file, stdin (`-`), and mutual exclusion error.
   - Test `parsePortBindings` with valid and invalid ports.
   - Test `gitBaseline` on git repo and non-git dir.

4. **Add `Destroy` stub to `internal/sandbox/manager.go`:**
   - `func (m *Manager) Destroy(ctx context.Context, name string, force bool) error` — for now, just removes the sandbox directory and stops/removes the container. Full implementation in Phase 7.

5. **Wire `yoloai new` in `internal/cli/commands.go`:**
   - Replace stub `newNewCmd` with real command.
   - Parse flags: `--prompt`/`-p`, `--prompt-file`/`-f`, `--model`/`-m`, `--agent` (default `"claude"`), `--network-none`, `--port` (repeatable), `--replace`, `--no-start`, `--yes`/`-y`.
   - Handle `--` passthrough via `cmd.ArgsLenAtDash()`.
   - Manual positional arg parsing: first positional = name, second = workdir (default `.`).
   - Create Docker client, Manager, populate `CreateOptions`, call `Create`.

6. **Run `go mod tidy`.**

## Tests

### `internal/sandbox/create_test.go`

```go
func TestResolveModel_Alias(t *testing.T)
// "sonnet" with Claude agent → "claude-sonnet-4-latest"

func TestResolveModel_FullName(t *testing.T)
// "claude-sonnet-4-5-20250929" → passed through unchanged

func TestResolveModel_Empty(t *testing.T)
// "" → ""

func TestBuildAgentCommand_InteractiveWithModel(t *testing.T)
// Claude agent, model "opus", no prompt → "claude --dangerously-skip-permissions --model claude-opus-4-latest"

func TestBuildAgentCommand_InteractiveWithPassthrough(t *testing.T)
// Claude, model "sonnet", passthrough ["--max-turns", "5"] →
// "claude --dangerously-skip-permissions --model claude-sonnet-4-latest --max-turns 5"

func TestBuildAgentCommand_HeadlessWithPrompt(t *testing.T)
// Test agent, prompt "echo hello" → "sh -c \"echo hello\""

func TestBuildAgentCommand_InteractiveFallback(t *testing.T)
// Test agent, no prompt → "bash"

func TestBuildContainerConfig_ValidJSON(t *testing.T)
// Verify JSON is valid and fields match expected values

func TestReadPrompt_DirectText(t *testing.T)
// prompt="hello", promptFile="" → "hello"

func TestReadPrompt_File(t *testing.T)
// Write temp file, prompt="", promptFile=path → file content

func TestReadPrompt_MutualExclusion(t *testing.T)
// Both non-empty → error

func TestReadPrompt_StdinDash(t *testing.T)
// prompt="-" → reads from stdin (tested with pipe)

func TestParsePortBindings_Valid(t *testing.T)
// ["3000:3000", "8080:80"] → correct PortMap and PortSet

func TestParsePortBindings_Invalid(t *testing.T)
// ["invalid"] → error

func TestGitBaseline_ExistingRepo(t *testing.T)
// Create git repo, call gitBaseline → returns HEAD SHA

func TestGitBaseline_NonGitDir(t *testing.T)
// Plain dir with files, call gitBaseline → creates repo, returns SHA
```

No integration tests for container creation in this phase — requires Docker and takes too long. The full `yoloai new` flow is verified manually.

## Verification

```bash
# Must compile
go build ./...

# Linter must pass
make lint

# Unit tests pass
make test

# Manual verification (requires Docker):
make build

# Create a test project
mkdir -p /tmp/test-project
echo "hello" > /tmp/test-project/file.txt

# Create sandbox with test agent (no API key needed)
./yoloai new test-sandbox --agent test /tmp/test-project
# Should print creation output

docker ps --filter name=yoloai-test-sandbox
# Should show running container

# Create sandbox with prompt
./yoloai new test-prompt --agent test --prompt "ls -la" /tmp/test-project
# Should create and deliver prompt

# Verify attach works
./yoloai attach test-sandbox
# Should attach to tmux (Ctrl-b d to detach)

# Clean up
docker stop yoloai-test-sandbox yoloai-test-prompt
docker rm yoloai-test-sandbox yoloai-test-prompt
rm -rf ~/.yoloai/sandboxes/test-sandbox ~/.yoloai/sandboxes/test-prompt
```

## Concerns

### 1. Cobra `ArbitraryArgs` with `--` passthrough

Using `cobra.ArbitraryArgs` disables Cobra's positional arg validation. We must do manual validation: ensure at least 1 positional (name), at most 2 positionals before `--` (name + workdir). The `ArgsLenAtDash()` method returns the index in `args` where `--` appeared, or -1. Everything at and after that index is passthrough.

### 2. Shell escaping in headless agent command

The test agent's headless command is `sh -c "PROMPT"`. The prompt text is substituted into the string, which requires shell escaping. For MVP, use `strings.ReplaceAll` on the `PROMPT` placeholder. If the prompt contains double quotes, they need escaping. Use `strconv.Quote` to safely escape the prompt for shell embedding, then strip the outer quotes since the template already has them.

### 3. Credential temp file timing

The temp key file must exist during container start (entrypoint reads `/run/secrets/` immediately). Cleanup after 1 second is heuristic — the entrypoint reads secrets in its first few lines, well before the 1s wait. For extra safety, the file is also cleaned up via `defer` in case of errors. SIGKILL can still leave orphaned temp files — accepted tradeoff per [PLAN.md](../PLAN.md).

### 4. Destroy stub for --replace

Phase 4b needs `Destroy` for the `--replace` flag. The stub only needs to: stop the container (if running), remove the container, and delete the sandbox directory. Full `Destroy` (with smart confirmation, `--all`, etc.) comes in Phase 7.

### 5. `readPrompt` stdin handling

When `--prompt -` or `--prompt-file -` is used, we read from `os.Stdin`. This consumes stdin, so it can only be done once. The CLI wiring must detect `-` and pass it through correctly. For `--prompt-file -`, read all of stdin into the prompt string.

### 6. `cp -rp` portability

`cp -rp` is POSIX-portable (works on Linux and macOS). The `-a` flag is GNU-specific. For very large directories (with `node_modules`), this can be slow — known limitation, overlay strategy (post-MVP) addresses this.

### 7. Meta.json fields

The existing `Meta` struct has `NetworkMode` and `Ports` fields. The `Model` field stores the resolved model (after alias expansion). `WorkdirMeta.Mode` stores `"copy"` or `"rw"`. `WorkdirMeta.MountPath` equals `WorkdirMeta.HostPath` for MVP (mirrored paths, no custom `=<path>`).

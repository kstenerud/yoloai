# Phase 7: Lifecycle (stop/start/destroy/reset)

## Goal

Working `stop`, `start`, `destroy`, and `reset` commands for full sandbox lifecycle management. Stop preserves state for later restart. Start is idempotent — "get it running, however needed." Destroy cleans up with smart confirmation. Reset re-copies the workdir for retrying the same task.

## Prerequisites

- Phase 6 complete (diff/apply)
- Docker daemon running with at least one sandbox created

## Files to Create

| File | Description |
|------|-------------|
| `internal/sandbox/lifecycle.go` | `Stop`, `Start`, `RecreateContainer`, `relaunchAgent`, `Reset` methods on Manager |
| `internal/sandbox/lifecycle_test.go` | Tests for lifecycle logic (stop, start state transitions, destroy confirmation, reset) |
| `internal/cli/stop.go` | `newStopCmd()` replacing stub, `--all` flag, multi-name support |
| `internal/cli/start.go` | `newStartCmd()` replacing stub |
| `internal/cli/destroy.go` | `newDestroyCmd()` replacing stub, `--all` and `--yes` flags, smart confirmation |
| `internal/cli/reset.go` | `newResetCmd()` replacing stub, `--no-prompt` and `--clean` flags |

## Files to Modify

| File | Change |
|------|--------|
| `internal/sandbox/manager.go` | Replace `Destroy` stub with call to full implementation; add `Destroy` method with smart confirmation logic |
| `internal/cli/commands.go` | Remove `newStopCmd`, `newStartCmd`, `newDestroyCmd`, `newResetCmd` stub functions |

## Types and Signatures

### `internal/sandbox/lifecycle.go`

```go
package sandbox

import "context"

// Stop stops a sandbox's container via Docker SDK.
// Returns nil if the container is already stopped or removed.
func (m *Manager) Stop(ctx context.Context, name string) error

// Start ensures a sandbox is running — idempotent.
// - If already running with live agent: no-op.
// - If running but agent exited: relaunch agent in existing tmux session.
// - If stopped: start the container.
// - If removed: recreate the container from meta.json.
func (m *Manager) Start(ctx context.Context, name string) error

// Destroy stops the container, removes it, and deletes the sandbox directory.
// Always destroys unconditionally — confirmation logic is handled by the
// CLI layer via needsConfirmation before calling this method.
func (m *Manager) Destroy(ctx context.Context, name string, force bool) error

// Reset re-copies the workdir from the original host directory and resets
// the git baseline. Stops and restarts the container.
func (m *Manager) Reset(ctx context.Context, opts ResetOptions) error

// ResetOptions holds parameters for the reset command.
type ResetOptions struct {
	Name     string
	Clean    bool // also wipe agent-state directory
	NoPrompt bool // skip re-sending prompt after reset
}
```

**Unexported helpers:**

```go
// recreateContainer creates a new Docker container from meta.json
// (skipping the workdir copy step — state already exists in work/).
// Creates fresh credential temp files.
func (m *Manager) recreateContainer(ctx context.Context, name string, meta *Meta) error

// relaunchAgent relaunches the agent in the existing tmux session
// when the pane is dead but the container is still running.
// Uses `docker exec` to run `tmux respawn-pane` with the agent command.
func (m *Manager) relaunchAgent(ctx context.Context, name string, meta *Meta) error

// needsConfirmation checks if a sandbox requires confirmation before
// destruction. Returns true if the agent is running or unapplied changes
// exist. Returns a reason string for the confirmation prompt.
func (m *Manager) needsConfirmation(ctx context.Context, name string) (bool, string)
```

### `internal/cli/stop.go`

```go
package cli

import "github.com/spf13/cobra"

// newStopCmd returns the stop command.
// Accepts multiple names. --all stops all running sandboxes.
func newStopCmd() *cobra.Command
```

### `internal/cli/start.go`

```go
package cli

import "github.com/spf13/cobra"

// newStartCmd returns the start command.
func newStartCmd() *cobra.Command
```

### `internal/cli/destroy.go`

```go
package cli

import "github.com/spf13/cobra"

// newDestroyCmd returns the destroy command.
// Accepts multiple names. --all destroys all sandboxes.
// --yes skips confirmation.
func newDestroyCmd() *cobra.Command
```

### `internal/cli/reset.go`

```go
package cli

import "github.com/spf13/cobra"

// newResetCmd returns the reset command.
// --no-prompt skips re-sending prompt. --clean also wipes agent-state.
func newResetCmd() *cobra.Command
```

## Design Decisions

### 1. `Start` is idempotent — "get it running, however needed"

The user shouldn't need to diagnose *why* a sandbox isn't running before choosing a command. `Start` inspects the current state and takes the appropriate action: no-op if running, `ContainerStart` if stopped, container recreation if removed, agent relaunch if pane is dead. This eliminates a category of user error.

### 2. Agent relaunch via `tmux respawn-pane`

When the container is running but the agent has exited (tmux pane dead), `relaunchAgent` uses `docker exec` to run `tmux respawn-pane -t main -k` with the agent command from `config.json`. This reuses the existing tmux session rather than creating a new one. The `-k` flag kills the dead pane and respawns it. This is simpler than destroying and recreating the tmux session.

### 3. Container recreation from meta.json

When the container has been removed (e.g., `docker rm`) but the sandbox directory still exists, `recreateContainer` rebuilds the container from `meta.json`. This reuses `buildMounts` and `createSecretsDir` from `create.go`, but skips the workdir copy step (state already exists in `work/`). Fresh credential temp files are created since the originals were cleaned up.

### 4. Smart confirmation for destroy

`Destroy` only prompts when there's something to lose: agent still running, or unapplied changes exist. A stopped sandbox with a clean workdir is destroyed without prompting. This avoids annoying confirmations for sandboxes the user is clearly done with, while protecting against accidental loss. `--yes` overrides everything.

### 5. Multi-name support for stop and destroy

`stop` and `destroy` accept multiple sandbox names (e.g., `yoloai stop s1 s2 s3`) and process them sequentially. Errors are collected — a failure on one sandbox doesn't abort the rest. The `--all` flag operates on all sandboxes (running ones for stop, all for destroy).

### 6. Reset stops then restarts

Reset always stops the container first (if running), performs the workdir re-copy and baseline reset, then starts the container again. This ensures a clean state. The entrypoint re-establishes mounts on container start, so bind mount paths are correctly re-mapped.

### 7. `--clean` wipes agent-state

By default, reset preserves the agent-state directory (e.g., Claude's `~/.claude/`). This means the agent retains its session history on restart. `--clean` deletes and recreates agent-state, giving a full reset of both workspace and agent memory. Use case: the agent has gone off track and needs a completely fresh start.

### 8. Prompt re-delivery after reset

After reset, if `prompt.txt` exists and `--no-prompt` is not set, the agent command from `config.json` already includes the prompt (for headless agents) or the entrypoint re-sends it via tmux (for interactive agents). Since the container is restarted from scratch, the entrypoint handles prompt delivery as it did during initial creation. No special re-send logic needed.

## Detailed Implementation

### `internal/sandbox/lifecycle.go` — Core lifecycle logic

#### `Stop`

```go
func (m *Manager) Stop(ctx context.Context, name string) error
```

1. Check sandbox exists: `os.Stat(Dir(name))`. Return `ErrSandboxNotFound` if missing.
2. `containerName := "yoloai-" + name`.
3. `m.client.ContainerStop(ctx, containerName, container.StopOptions{})`.
4. If error is "not found" or "not running", return `nil` (idempotent).
5. Return any other error.

#### `Start`

```go
func (m *Manager) Start(ctx context.Context, name string) error
```

1. Check sandbox exists: `os.Stat(Dir(name))`. Return `ErrSandboxNotFound` if missing.
2. Load metadata: `LoadMeta(Dir(name))`.
3. Detect status: `DetectStatus(ctx, m.client, "yoloai-"+name)`.
4. Switch on status:
   - `StatusRunning`: Print `"Sandbox <name> is already running"`. Return nil.
   - `StatusDone`, `StatusFailed`: Agent exited but container running. Call `relaunchAgent(ctx, name, meta)`. Print `"Agent relaunched in sandbox <name>"`.
   - `StatusStopped`: `m.client.ContainerStart(ctx, "yoloai-"+name, container.StartOptions{})`. Print `"Sandbox <name> started"`.
   - `StatusRemoved`: Call `recreateContainer(ctx, name, meta)`. Print `"Sandbox <name> recreated and started"`.

#### `recreateContainer`

```go
func (m *Manager) recreateContainer(ctx context.Context, name string, meta *Meta) error
```

1. Look up agent definition: `agent.GetAgent(meta.Agent)`. Error if nil (unknown agent).
2. Resolve the workdir: `ParseDirArg(meta.Workdir.HostPath + ":" + meta.Workdir.Mode)`.
3. Build a `sandboxState` struct from meta fields:
   - `name`, `sandboxDir = Dir(name)`, `workdir`, `workCopyDir = WorkDir(name, meta.Workdir.HostPath)`.
   - `agent = agentDef`, `model = meta.Model`, `hasPrompt = meta.HasPrompt`.
   - `networkMode = meta.NetworkMode`, `ports = meta.Ports`.
4. Read `config.json` from sandbox dir (already exists from creation).
5. Create fresh secrets: `createSecretsDir(agentDef)`.
6. Build mounts: `buildMounts(state, secretsDir)`.
7. Build port bindings: `parsePortBindings(meta.Ports)`.
8. Create container config (same as `createAndStartContainer` but using stored state).
9. `m.client.ContainerCreate(ctx, config, hostConfig, nil, nil, "yoloai-"+name)`.
10. `m.client.ContainerStart(ctx, resp.ID, container.StartOptions{})`.
11. Clean up secrets dir after brief wait.

#### `relaunchAgent`

```go
func (m *Manager) relaunchAgent(ctx context.Context, name string, meta *Meta) error
```

1. Read `config.json` from sandbox dir to get `agent_command`.
2. Parse `agent_command` from the JSON.
3. Container name: `"yoloai-" + name`.
4. Run via `execInContainer`: `tmux respawn-pane -t main -k -c <workdir> <agent_command>`.
   - Actually, `tmux respawn-pane` takes a shell command directly. Use:
     `[]string{"tmux", "respawn-pane", "-t", "main", "-k", agent_command}`.
   - But `respawn-pane` runs the command in a shell, so pass the full command string.
   - However, `execInContainer` takes `[]string` args. Use `sh -c <agent_command>` wrapping:
     `[]string{"tmux", "respawn-pane", "-t", "main", "-k", "sh", "-c", agentCommand}`.
   - Wait — `tmux respawn-pane` syntax: `tmux respawn-pane [-k] [-t target-pane] [shell-command]`. The shell-command is a single string. So:
     `[]string{"tmux", "respawn-pane", "-t", "main", "-k", agentCommand}`.
   - tmux will execute `agentCommand` in the respawned pane.
5. Return any error from exec.

#### `needsConfirmation`

```go
func (m *Manager) needsConfirmation(ctx context.Context, name string) (bool, string)
```

1. Detect status: `DetectStatus(ctx, m.client, "yoloai-"+name)`.
2. If `StatusRunning`, return `(true, "agent is still running")`.
3. Load meta, get work dir: `WorkDir(name, meta.Workdir.HostPath)`.
4. Check changes: `detectChanges(workDir)`.
5. If `"yes"`, return `(true, "unapplied changes exist")`.
6. Return `(false, "")`.

#### `Destroy`

```go
func (m *Manager) Destroy(ctx context.Context, name string, force bool) error
```

1. Check sandbox exists: `os.Stat(Dir(name))`. Return `ErrSandboxNotFound` if missing.
2. If `!force`:
   - `needsConfirmation(ctx, name)` → `(needs, reason)`.
   - If `needs`, return a sentinel error (or the caller handles prompting — see CLI section).
   - Actually, keep `Destroy` simple: it always destroys. The CLI layer handles confirmation. This is consistent with the existing stub which takes a `force` bool.
3. Container name: `"yoloai-" + name`.
4. `m.client.ContainerStop(ctx, containerName, container.StopOptions{})` — ignore not-found/not-running errors.
5. `m.client.ContainerRemove(ctx, containerName, container.RemoveOptions{Force: true})` — ignore not-found errors.
6. `os.RemoveAll(Dir(name))`.
7. Return any error from RemoveAll.

This is the same as the existing stub, but now it's the final implementation. The smart confirmation logic lives in the CLI layer, not in `Destroy` itself.

#### `Reset`

```go
func (m *Manager) Reset(ctx context.Context, opts ResetOptions) error
```

1. Check sandbox exists. Load metadata.
2. If `meta.Workdir.Mode == "rw"`, return error: `"reset is not applicable for :rw directories — changes are already in the original"`.
3. Stop the container (if running): `m.Stop(ctx, opts.Name)`. Ignore already-stopped.
4. Delete work copy: `os.RemoveAll(WorkDir(opts.Name, meta.Workdir.HostPath))`.
5. Verify original host dir still exists: `os.Stat(meta.Workdir.HostPath)`. Error if missing: `"original directory no longer exists: <path>"`.
6. Re-copy: `copyDir(meta.Workdir.HostPath, WorkDir(opts.Name, meta.Workdir.HostPath))`.
7. Re-create git baseline: `gitBaseline(WorkDir(opts.Name, meta.Workdir.HostPath))` → new SHA.
8. Update meta.json: `meta.Workdir.BaselineSHA = newSHA`. `SaveMeta(Dir(opts.Name), meta)`.
9. If `opts.Clean`:
   - `os.RemoveAll(filepath.Join(Dir(opts.Name), "agent-state"))`.
   - `os.MkdirAll(filepath.Join(Dir(opts.Name), "agent-state"), 0750)`.
10. Start the container: `m.Start(ctx, opts.Name)`.

### `internal/cli/stop.go` — Stop command

```go
func newStopCmd() *cobra.Command
```

Cobra command:
- `Use: "stop <name>..."`
- `Short: "Stop sandboxes (preserving state)"`
- `Args: cobra.ArbitraryArgs` (manual validation for `--all` support)
- Flags: `--all` (bool)

RunE:
1. Get `--all` flag.
2. Create Docker client, Manager.
3. If `--all`:
   a. `ListSandboxes(ctx, client)` to get all sandboxes.
   b. Filter to running ones (`StatusRunning`, `StatusDone`, `StatusFailed` — anything with a running container).
   c. Names = filtered sandbox names.
4. If not `--all`:
   a. If no args, return usage error.
   b. Names = `args`.
5. For each name:
   a. `mgr.Stop(ctx, name)`.
   b. If error, print warning to stderr and continue.
   c. If success, print `"Stopped <name>"` to stdout.
6. If any errors occurred, return a summary error.

### `internal/cli/start.go` — Start command

```go
func newStartCmd() *cobra.Command
```

Cobra command:
- `Use: "start <name>"`
- `Short: "Start a stopped sandbox"`
- `Args: cobra.ExactArgs(1)`

RunE:
1. Get sandbox name.
2. Create Docker client, Manager.
3. `mgr.Start(ctx, name)`.
4. Success/status messages printed by `Start` itself.

### `internal/cli/destroy.go` — Destroy command

```go
func newDestroyCmd() *cobra.Command
```

Cobra command:
- `Use: "destroy <name>..."`
- `Short: "Stop and remove sandboxes"`
- `Args: cobra.ArbitraryArgs` (manual validation for `--all` support)
- Flags: `--all` (bool), `--yes`/`-y` (bool)

RunE:
1. Get `--all` and `--yes` flags.
2. Create Docker client, Manager.
3. If `--all`:
   a. `ListSandboxes(ctx, client)` to get all names.
   b. If empty, print `"No sandboxes to destroy"` and return.
   c. Names = all sandbox names.
4. If not `--all`:
   a. If no args, return usage error.
   b. Names = `args`.
5. Smart confirmation (unless `--yes`):
   a. Collect names that need confirmation via `mgr.needsConfirmation`.
   b. If any need confirmation, show prompt listing which sandboxes need it and why:
      ```
      The following sandboxes have active work:
        fix-bug: agent is still running
        explore: unapplied changes exist
      Destroy all listed sandboxes? [y/N]
      ```
   c. Use `Confirm`. If declined, return nil.
6. For each name:
   a. `mgr.Destroy(ctx, name, true)` (force=true, confirmation already handled).
   b. If error, print warning to stderr and continue.
   c. If success, print `"Destroyed <name>"` to stdout.

### `internal/cli/reset.go` — Reset command

```go
func newResetCmd() *cobra.Command
```

Cobra command:
- `Use: "reset <name>"`
- `Short: "Re-copy workdir and reset git baseline"`
- `Args: cobra.ExactArgs(1)`
- Flags: `--no-prompt` (bool), `--clean` (bool)

RunE:
1. Get sandbox name, `--no-prompt`, `--clean` flags.
2. Create Docker client, Manager.
3. `mgr.Reset(ctx, ResetOptions{Name: name, Clean: clean, NoPrompt: noPrompt})`.
4. Print `"Sandbox <name> reset"` to stdout.

### `internal/sandbox/manager.go` — Replace Destroy stub

Replace the existing `Destroy` stub with the final implementation. The method signature changes slightly: `force bool` now controls whether to skip the container stop/remove error checking (it always did force removal, but now the signature is documented).

### `internal/cli/commands.go` — Remove stubs

Remove the following stub functions (they move to their own files):
- `newStopCmd()`
- `newStartCmd()`
- `newDestroyCmd()`
- `newResetCmd()`

The `registerCommands` function, `errNotImplemented` var, and remaining stubs (`newCompletionCmd`) remain unchanged.

## Implementation Steps

1. **Create `internal/sandbox/lifecycle.go`:**
   - `Stop` method.
   - `relaunchAgent` helper (uses `execInContainer`).
   - `recreateContainer` helper (reuses `buildMounts`, `createSecretsDir`, `parsePortBindings`).
   - `Start` method (orchestrates the four state transitions).
   - `needsConfirmation` helper (uses `DetectStatus`, `detectChanges`).
   - `Destroy` method (replaces the stub in manager.go).
   - `Reset` method with `ResetOptions`.

2. **Update `internal/sandbox/manager.go`:**
   - Remove the `Destroy` stub (moved to lifecycle.go).

3. **Create `internal/sandbox/lifecycle_test.go`:**
   - Tests for stop, start state transitions, destroy, reset.

4. **Create `internal/cli/stop.go`:**
   - `newStopCmd` with `--all` flag and multi-name support.

5. **Create `internal/cli/start.go`:**
   - `newStartCmd`.

6. **Create `internal/cli/destroy.go`:**
   - `newDestroyCmd` with `--all`, `--yes` flags, and smart confirmation.

7. **Create `internal/cli/reset.go`:**
   - `newResetCmd` with `--no-prompt` and `--clean` flags.

8. **Modify `internal/cli/commands.go`:**
   - Remove 4 stub functions. Keep `registerCommands`, `errNotImplemented`, `newCompletionCmd`.

9. **Run `go build ./...` and `make lint`.**

## Tests

### `internal/sandbox/lifecycle_test.go`

```go
func TestStop_AlreadyStopped(t *testing.T)
// Mock Docker returns "not running" error → Stop returns nil (idempotent).

func TestStop_ContainerRemoved(t *testing.T)
// Mock Docker returns "not found" error → Stop returns nil (idempotent).

func TestStop_Running(t *testing.T)
// Mock Docker ContainerStop succeeds → Stop returns nil.

func TestStart_AlreadyRunning(t *testing.T)
// Mock DetectStatus returns StatusRunning → Start is no-op, returns nil.

func TestStart_Stopped(t *testing.T)
// Mock DetectStatus returns StatusStopped → ContainerStart called.

func TestStart_Removed(t *testing.T)
// Mock DetectStatus returns StatusRemoved → recreateContainer called
// (ContainerCreate + ContainerStart).

func TestStart_AgentExited(t *testing.T)
// Mock DetectStatus returns StatusDone → relaunchAgent called
// (ContainerExecCreate for tmux respawn-pane).

func TestStart_SandboxNotFound(t *testing.T)
// Non-existent sandbox → ErrSandboxNotFound.

func TestNeedsConfirmation_Running(t *testing.T)
// Mock status = StatusRunning → (true, "agent is still running").

func TestNeedsConfirmation_ChangesExist(t *testing.T)
// Mock status = StatusStopped, work dir has uncommitted changes → (true, "unapplied changes exist").

func TestNeedsConfirmation_NoChanges(t *testing.T)
// Mock status = StatusStopped, work dir is clean → (false, "").

func TestDestroy_RemovesDir(t *testing.T)
// Create sandbox dir, mock Docker stop/remove. Destroy removes the directory.

func TestDestroy_SandboxNotFound(t *testing.T)
// Non-existent sandbox → ErrSandboxNotFound.

func TestReset_RecopiesWorkdir(t *testing.T)
// Create sandbox with modified work copy. Create original dir.
// Reset → work copy matches original, new baseline SHA in meta.json.

func TestReset_Clean(t *testing.T)
// Create sandbox with agent-state content.
// Reset with Clean=true → agent-state dir is empty.

func TestReset_RWMode_Error(t *testing.T)
// Sandbox with rw mode → error.

func TestReset_OriginalMissing(t *testing.T)
// Original host dir deleted → descriptive error.
```

Tests for `Stop`, `Start`, `needsConfirmation`, and `Destroy` use the mock Docker client pattern from `internal/sandbox/inspect_test.go`. Tests for `Reset` use real filesystem operations with `t.TempDir()` and `t.Setenv("HOME", tmpDir)`.

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

# Create test project and sandbox
mkdir -p /tmp/test-lifecycle
echo "hello" > /tmp/test-lifecycle/file.txt
./yoloai new test-lc --agent test /tmp/test-lifecycle

# Test stop
./yoloai stop test-lc
./yoloai show test-lc
# Status should be "stopped"

# Test start (from stopped)
./yoloai start test-lc
./yoloai show test-lc
# Status should be "running"

# Test stop + start idempotency
./yoloai stop test-lc
./yoloai stop test-lc
# Second stop should succeed silently

./yoloai start test-lc
./yoloai start test-lc
# Second start should print "already running"

# Test start after container removal
docker rm -f yoloai-test-lc
./yoloai start test-lc
./yoloai show test-lc
# Should be "running" — container recreated from meta.json

# Test reset
echo "modified" > ~/.yoloai/sandboxes/test-lc/work/$(echo /tmp/test-lifecycle | sed 's|/|^|g')/file.txt
./yoloai diff test-lc
# Should show modification
./yoloai reset test-lc
./yoloai diff test-lc
# Should show no changes (clean after reset)

# Test reset --clean
./yoloai reset test-lc --clean
# Should also wipe agent-state

# Test destroy with smart confirmation
echo "new change" > ~/.yoloai/sandboxes/test-lc/work/$(echo /tmp/test-lifecycle | sed 's|/|^|g')/file.txt
./yoloai destroy test-lc
# Should prompt (unapplied changes)

./yoloai destroy test-lc --yes
# Should destroy without prompting

# Test destroy already-gone sandbox
./yoloai destroy test-lc
# Should return "sandbox not found"

# Test --all flags
./yoloai new test-a --agent test /tmp/test-lifecycle
./yoloai new test-b --agent test /tmp/test-lifecycle
./yoloai stop --all
# Both should be stopped
./yoloai destroy --all --yes
# Both should be destroyed

# Clean up
rm -rf /tmp/test-lifecycle
```

## Concerns

### 1. Container recreation fidelity

`recreateContainer` rebuilds the container from `meta.json` fields. It must produce the same container config as the original `createAndStartContainer`. The critical pieces are: image name (`yoloai-base`), mount paths, network mode, port bindings, and the `init` flag. All of these are either stored in `meta.json` or derivable from it. The `config.json` file (which contains the agent command) is already on disk and bind-mounted.

### 2. `tmux respawn-pane` command construction

`relaunchAgent` passes the agent command to `tmux respawn-pane`. The command string comes from `config.json`'s `agent_command` field, which may contain spaces and shell metacharacters. `tmux respawn-pane -t main -k <command>` treats everything after the flags as the shell command, so the agent command should be passed as a single argument. Use `execInContainer` with args `["tmux", "respawn-pane", "-t", "main", "-k", agentCommand]` — tmux will run this in a shell.

### 3. Secrets lifecycle on container recreation

Credential temp files were cleaned up after the original container started. `recreateContainer` creates fresh temp files via `createSecretsDir`, bind-mounts them, waits for the entrypoint to read them, then cleans up. This follows the same pattern as `createAndStartContainer`.

### 4. `--all` requires listing sandboxes

The `--all` flag for `stop` and `destroy` calls `ListSandboxes` which makes one Docker API call per sandbox. For MVP this is acceptable. The alternative — `ContainerList` with a `yoloai-` prefix filter — would be faster but wouldn't catch removed containers (sandbox dir exists but container is gone).

### 5. Multi-name error collection

`stop` and `destroy` process multiple names sequentially. Errors don't abort the loop — they're collected and reported at the end. This prevents one broken sandbox from blocking cleanup of the rest. The exit code is non-zero if any operation failed.

### 6. Reset with running agent

Reset always stops the container first. This means the agent is terminated mid-work if it's still running. The design spec says this is acceptable — the user explicitly asked for a reset. No confirmation prompt for reset (the user's intent is clear).

### 7. `detectChanges` vs `HasChanges` in smart confirmation

`needsConfirmation` calls `detectChanges` (from `inspect.go:56`) directly on the work directory, rather than using `InspectSandbox`'s `HasChanges` field. This avoids a redundant Docker API call since we already called `DetectStatus` for the running-agent check. `detectChanges` is the same function used by `InspectSandbox` internally.

### 8. Reset prompt re-delivery

After reset, the container is started from scratch — the entrypoint runs, reads `config.json`, launches the agent, and delivers the prompt (if it exists in `prompt.txt`). No special re-send logic is needed because the entrypoint already handles prompt delivery. The `--no-prompt` flag is implemented by temporarily renaming `prompt.txt` before starting and restoring it after (or by skipping the rename if not set). Actually simpler: `--no-prompt` can be implemented by temporarily removing the `prompt.txt` bind mount. But since we're restarting the container from scratch, the simplest approach is: if `--no-prompt`, rename `prompt.txt` to `prompt.txt.bak` before starting, then rename back after. The entrypoint only delivers if `/yoloai/prompt.txt` exists.

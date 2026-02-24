# Phase 5: Inspection and Output

## Goal

Working `attach`, `show`, `list`, `log`, and `exec` commands plus a shared pager utility. These commands inspect sandbox state and produce output — no sandbox mutation.

## Prerequisites

- Phase 4b complete (sandbox creation, running containers)
- Docker daemon running with at least one sandbox created

## Files to Create

| File | Description |
|------|-------------|
| `internal/sandbox/inspect.go` | `SandboxStatus`, `SandboxInfo`, `InspectSandbox`, `ListSandboxes`, `DetectStatus`, `FormatAge` |
| `internal/sandbox/inspect_test.go` | Tests for `FormatAge`, `detectChanges`, `InspectSandbox`, `ListSandboxes` |
| `internal/cli/pager.go` | `RunPager(io.Reader) error` — auto-paging for TTY output |
| `internal/cli/pager_test.go` | Non-TTY copy test |
| `internal/cli/attach.go` | `newAttachCmd()` — replace stub |
| `internal/cli/show.go` | `newShowCmd()` + `loadPromptPreview()` |
| `internal/cli/list.go` | `newListCmd()` with tabwriter formatting |
| `internal/cli/log.go` | `newLogCmd()` |
| `internal/cli/exec.go` | `newExecCmd()` with TTY detection and exit code propagation |

## Files to Modify

| File | Change |
|------|--------|
| `internal/cli/commands.go` | Remove 5 stub functions (`newAttachCmd`, `newShowCmd`, `newListCmd`, `newLogCmd`, `newExecCmd`) |
| `internal/sandbox/errors.go` | Add `ErrContainerNotRunning` sentinel error |

## Types and Signatures

### `internal/sandbox/inspect.go`

```go
package sandbox

import (
	"context"
	"time"

	"github.com/kstenerud/yoloai/internal/docker"
)

// SandboxStatus represents the current state of a sandbox.
type SandboxStatus string

const (
	StatusRunning SandboxStatus = "running" // container running, agent alive in tmux
	StatusDone    SandboxStatus = "done"    // container running, agent exited cleanly (exit 0)
	StatusFailed  SandboxStatus = "failed"  // container running, agent exited with error (non-zero)
	StatusStopped SandboxStatus = "stopped" // container stopped (docker stop)
	StatusRemoved SandboxStatus = "removed" // container removed but sandbox dir exists
)

// SandboxInfo holds the combined metadata and live state for a sandbox.
type SandboxInfo struct {
	Meta        *Meta
	Status      SandboxStatus
	ContainerID string // 12-char short ID, empty if removed
	HasChanges  string // "yes", "no", or "-" (unknown/not applicable)
}

// InspectSandbox loads metadata and queries Docker for a single sandbox.
// Returns ErrSandboxNotFound if the sandbox directory doesn't exist.
func InspectSandbox(ctx context.Context, client docker.Client, name string) (*SandboxInfo, error)

// ListSandboxes scans ~/.yoloai/sandboxes/ and returns info for all sandboxes.
// Skips entries that fail to load (logs warning). Sorted by name.
func ListSandboxes(ctx context.Context, client docker.Client) ([]*SandboxInfo, error)

// DetectStatus queries Docker and tmux to determine sandbox status.
// Uses ContainerInspect for container state, then tmux pane query
// to distinguish running/done/failed.
func DetectStatus(ctx context.Context, client docker.Client, containerName string) (SandboxStatus, string, error)

// FormatAge returns a human-readable duration string (e.g., "2h", "3d", "5m").
func FormatAge(created time.Time) string
```

**Unexported helpers:**

```go
// execInContainer runs a command inside a container and returns stdout.
// Uses Docker SDK ContainerExecCreate/Attach/Inspect with stdcopy.StdCopy
// to demux the multiplexed Docker stream.
func execInContainer(ctx context.Context, client docker.Client, containerID string, cmd []string) (string, error)

// detectChanges checks if the sandbox work directory has uncommitted changes.
// Runs `git status --porcelain` on the host-side work directory.
// Returns "yes" if changes exist, "no" if clean, "-" if not applicable
// (work dir missing, not a git repo, or error).
func detectChanges(workDir string) string
```

### `internal/cli/pager.go`

```go
package cli

import "io"

// RunPager pipes content through $PAGER (or "less -R" fallback) when stdout
// is a TTY. When stdout is not a TTY (piped), copies content directly to
// stdout. Uses golang.org/x/term for TTY detection.
func RunPager(r io.Reader) error
```

### `internal/cli/attach.go`

```go
package cli

import "github.com/spf13/cobra"

// newAttachCmd returns the attach command.
// Uses os/exec → `docker exec -it yoloai-<name> tmux attach -t main`.
func newAttachCmd() *cobra.Command
```

### `internal/cli/show.go`

```go
package cli

import "github.com/spf13/cobra"

// newShowCmd returns the show command.
func newShowCmd() *cobra.Command

// loadPromptPreview reads prompt.txt and returns the first 200 characters.
// Uses []rune conversion for correct UTF-8 truncation. Appends "..." if
// truncated. Returns empty string if file doesn't exist.
func loadPromptPreview(sandboxDir string) string
```

### `internal/cli/list.go`

```go
package cli

import "github.com/spf13/cobra"

// newListCmd returns the list command with "ls" alias.
func newListCmd() *cobra.Command
```

### `internal/cli/log.go`

```go
package cli

import "github.com/spf13/cobra"

// newLogCmd returns the log command.
// Reads log.txt from host filesystem (no Docker client needed).
// Auto-pages via RunPager when stdout is a TTY.
func newLogCmd() *cobra.Command
```

### `internal/cli/exec.go`

```go
package cli

import "github.com/spf13/cobra"

// newExecCmd returns the exec command.
// Uses os/exec → `docker exec` with -i when stdin is pipe/TTY,
// -t when stdin is TTY. Propagates exit code via os.Exit.
func newExecCmd() *cobra.Command
```

## Design Decisions

### 1. `attach` and `exec` use `os/exec` → `docker` CLI (not Docker SDK)

Docker SDK's `HijackedResponse` doesn't handle raw TTY, terminal resize (`SIGWINCH`), or signal forwarding for interactive tmux sessions. This is consistent with PLAN.md's explicit note: "SDK doesn't handle raw TTY well for interactive tmux — justified exception."

### 2. `log` reads host file directly

`log.txt` is bind-mounted from `~/.yoloai/sandboxes/<name>/log.txt`. Always up-to-date on host. No Docker client needed — just `os.Open` and pipe through pager.

### 3. Status detection uses Docker SDK

`DetectStatus` uses:
- `ContainerInspect` for container state (`.State.Running`, `.State.Status`)
- `ContainerExecCreate`/`ContainerExecAttach`/`ContainerExecInspect` with `stdcopy.StdCopy` for tmux pane query

The `stdcopy.StdCopy` demuxing is required because Docker multiplexes stdout/stderr into a single stream with 8-byte headers when `TTY: false` (which is the case for exec commands).

### 4. `exec` exit code propagation via `os.Exit`

Standard Go pattern for CLI tools that proxy exit codes. Bypasses defers but acceptable — only `client.Close()` is affected, and the OS reclaims resources on exit anyway.

### 5. Pager uses `golang.org/x/term`

New direct dependency for TTY detection. `x/sys` is already an indirect dependency (pulled in by Docker SDK). `x/term` depends on `x/sys`.

### 6. `list` has `ls` alias

Convenience alias via Cobra's `Aliases` field. Both `yoloai list` and `yoloai ls` work.

### 7. No `--json`, `--running`, `--stopped` flags

MVP keeps `list` simple per PLAN.md. Filtering and structured output deferred.

### 8. Each command gets its own CLI file

`attach.go`, `show.go`, `list.go`, `log.go`, `exec.go` — keeps `commands.go` focused on registration and the `build`/`new` commands that are already there.

## Detailed Implementation

### `internal/sandbox/errors.go` — Add sentinel error

Add to the existing sentinel errors block:

```go
ErrContainerNotRunning = errors.New("container is not running")
```

Used by `attach` and `exec` when the container isn't in running state.

### `internal/sandbox/inspect.go` — Core inspection logic

#### `FormatAge`

```go
func FormatAge(created time.Time) string {
	d := time.Since(created)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
```

#### `detectChanges`

```go
func detectChanges(workDir string) string
```

1. Check `workDir` exists with `os.Stat`. If not, return `"-"`.
2. Check `workDir/.git` exists. If not, return `"-"`.
3. Run `git -C <workDir> status --porcelain` via `os/exec`.
4. If output is non-empty, return `"yes"`. If empty, return `"no"`. On error, return `"-"`.

#### `execInContainer`

```go
func execInContainer(ctx context.Context, client docker.Client, containerID string, cmd []string) (string, error)
```

1. `client.ContainerExecCreate(ctx, containerID, container.ExecOptions{Cmd: cmd, AttachStdout: true, AttachStderr: true})`
2. `client.ContainerExecAttach(ctx, execID, container.ExecAttachOptions{})`
3. Read response using `stdcopy.StdCopy(stdout, stderr, resp.Reader)` where `stdout` and `stderr` are `bytes.Buffer`.
4. `resp.Close()`
5. `client.ContainerExecInspect(ctx, execID)` — check `ExitCode`.
6. Return `stdout.String()` trimmed, or error if exit code non-zero.

#### `DetectStatus`

```go
func DetectStatus(ctx context.Context, client docker.Client, containerName string) (SandboxStatus, string, error)
```

1. `client.ContainerInspect(ctx, containerName)`.
2. If error is `errdefs.IsNotFound` → return `(StatusRemoved, "", nil)`.
3. If other error → return error.
4. Extract 12-char container ID: `resp.ID[:12]`.
5. If `!resp.State.Running` → return `(StatusStopped, shortID, nil)`.
6. Query tmux pane state: `execInContainer(ctx, client, containerName, []string{"tmux", "list-panes", "-t", "main", "-F", "#{pane_dead} #{pane_dead_status}"})`.
7. Parse output: first field is `"0"` (alive) or `"1"` (dead). Second field (if dead) is the exit status.
8. If pane alive (`"0"`) or tmux query fails → return `(StatusRunning, shortID, nil)`. Tmux query failure defaults to "running" — the container is running, so the most reasonable assumption is the agent is alive. This handles the edge case where tmux crashed.
9. If pane dead with exit status `"0"` → return `(StatusDone, shortID, nil)`.
10. If pane dead with non-zero exit status → return `(StatusFailed, shortID, nil)`.

#### `InspectSandbox`

```go
func InspectSandbox(ctx context.Context, client docker.Client, name string) (*SandboxInfo, error)
```

1. Check `Dir(name)` exists. If not, return `ErrSandboxNotFound`.
2. `LoadMeta(Dir(name))` → get `*Meta`.
3. `DetectStatus(ctx, client, "yoloai-"+name)` → get status and container ID.
4. Determine work dir: `WorkDir(name, meta.Workdir.HostPath)`.
5. `detectChanges(workDir)` → get changes string.
6. Return `&SandboxInfo{Meta: meta, Status: status, ContainerID: containerID, HasChanges: changes}`.

#### `ListSandboxes`

```go
func ListSandboxes(ctx context.Context, client docker.Client) ([]*SandboxInfo, error)
```

1. Get sandboxes dir: `~/.yoloai/sandboxes/`.
2. `os.ReadDir(sandboxesDir)` — get entries.
3. For each entry that `IsDir()`:
   a. Call `InspectSandbox(ctx, client, entry.Name())`.
   b. If error, log warning and skip (don't fail the entire list).
   c. Append to result slice.
4. Sort by name (already sorted by `os.ReadDir`).
5. Return result slice.

### `internal/cli/pager.go` — Auto-paging utility

```go
func RunPager(r io.Reader) error
```

1. Check if stdout is a TTY using `term.IsTerminal(int(os.Stdout.Fd()))` from `golang.org/x/term`.
2. If not a TTY: `io.Copy(os.Stdout, r)` and return.
3. If TTY: determine pager command from `$PAGER` env var. Default to `"less"` if unset.
4. Build pager args: if using `less` (either from default or `$PAGER`), add `-R` flag for ANSI color passthrough.
5. Create `exec.Command(pagerCmd, args...)`.
6. Set `cmd.Stdin = r`, `cmd.Stdout = os.Stdout`, `cmd.Stderr = os.Stderr`.
7. Run `cmd.Run()`. If pager not found, fall back to `io.Copy(os.Stdout, r)`.

### `internal/cli/attach.go` — Attach to tmux session

```go
func newAttachCmd() *cobra.Command
```

Cobra command:
- `Use: "attach <name>"`
- `Short: "Attach to a sandbox's tmux session"`
- `Args: cobra.ExactArgs(1)`

RunE:
1. Get sandbox name from `args[0]`.
2. Create Docker client, get `SandboxInfo` via `InspectSandbox` to verify sandbox exists and is running.
3. If status is not `StatusRunning`, `StatusDone`, or `StatusFailed`: return `ErrContainerNotRunning`. (Attaching to a container with a dead pane is valid — user can see the final output.)
4. Build command: `docker exec -it yoloai-<name> tmux attach -t main`.
5. Create `exec.Command("docker", args...)`.
6. Set `cmd.Stdin = os.Stdin`, `cmd.Stdout = os.Stdout`, `cmd.Stderr = os.Stderr`.
7. Run `cmd.Run()`. Return error if any.

### `internal/cli/show.go` — Show sandbox details

```go
func newShowCmd() *cobra.Command
```

Cobra command:
- `Use: "show <name>"`
- `Short: "Show sandbox configuration and state"`
- `Args: cobra.ExactArgs(1)`

RunE:
1. Get sandbox name from `args[0]`.
2. Create Docker client.
3. `InspectSandbox(ctx, client, name)` → get `*SandboxInfo`.
4. Format and print output to `cmd.OutOrStdout()`:

```
Name:        <name>
Status:      <status>
Agent:       <agent>
Model:       <model>                    (omit if empty)
Prompt:      <first 200 chars>...       (omit if no prompt)
Workdir:     <host_path> (<mode>)
Network:     none                       (omit if default)
Ports:       3000:3000, 8080:80         (omit if empty)
Created:     <RFC3339 time> (<age>)
Baseline:    <SHA>                      (omit if empty)
Container:   <12-char ID>               (omit if removed)
Changes:     <yes/no/->
```

#### `loadPromptPreview`

```go
func loadPromptPreview(sandboxDir string) string
```

1. Read `prompt.txt` from `sandboxDir`.
2. If file doesn't exist, return `""`.
3. Convert content to `[]rune` for correct UTF-8 handling.
4. If len > 200 runes, truncate to 200 and append `"..."`.
5. Replace newlines with spaces for single-line display.
6. Return result.

### `internal/cli/list.go` — List sandboxes

```go
func newListCmd() *cobra.Command
```

Cobra command:
- `Use: "list"`
- `Aliases: []string{"ls"}`
- `Short: "List sandboxes and their status"`
- `Args: cobra.NoArgs`

RunE:
1. Create Docker client.
2. `ListSandboxes(ctx, client)` → get `[]*SandboxInfo`.
3. If empty, print `"No sandboxes found"` and return.
4. Format table using `text/tabwriter`:

```
NAME       STATUS    AGENT    AGE    WORKDIR                          CHANGES
fix-bug    running   claude   2h     /home/user/projects/my-app       yes
explore    done      claude   3d     /home/user/projects/other        no
test-1     stopped   test     5m     /tmp/test-project                -
```

- Use `tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)`.
- Header line first, then one line per sandbox.
- `FormatAge(info.Meta.CreatedAt)` for the AGE column.

### `internal/cli/log.go` — Show session log

```go
func newLogCmd() *cobra.Command
```

Cobra command:
- `Use: "log <name>"`
- `Short: "Show sandbox session log"`
- `Args: cobra.ExactArgs(1)`

RunE:
1. Get sandbox name from `args[0]`.
2. Check sandbox exists: `os.Stat(Dir(name))`. Return `ErrSandboxNotFound` if missing.
3. Open `~/.yoloai/sandboxes/<name>/log.txt`.
4. If file doesn't exist, print `"No log output yet"` and return.
5. Pipe through `RunPager(file)`.
6. Close file.

No Docker client needed — reads host file directly.

### `internal/cli/exec.go` — Run command in sandbox

```go
func newExecCmd() *cobra.Command
```

Cobra command:
- `Use: "exec <name> <command> [args...]"`
- `Short: "Run a command inside a sandbox"`
- `Args: cobra.MinimumNArgs(2)`

RunE:
1. Get sandbox name from `args[0]`, command from `args[1:]`.
2. Create Docker client, get `SandboxInfo` via `InspectSandbox`.
3. If status is not running (container must be running for exec): return `ErrContainerNotRunning`.
4. Detect TTY: `term.IsTerminal(int(os.Stdin.Fd()))`.
5. Build docker exec args:
   - Always: `exec`
   - If stdin is pipe or TTY: add `-i`
   - If stdin is TTY: add `-t`
   - Container name: `yoloai-<name>`
   - Command and args: `args[1:]...`
6. Create `exec.Command("docker", dockerArgs...)`.
7. Set `cmd.Stdin = os.Stdin`, `cmd.Stdout = os.Stdout`, `cmd.Stderr = os.Stderr`.
8. Run `cmd.Run()`.
9. Extract exit code: if `*exec.ExitError`, get `ExitCode()`. Otherwise default to 1 for errors.
10. `os.Exit(exitCode)` — propagate the exit code.

**Note on `os.Exit`:** This bypasses defers in the calling stack. The only defer affected is `client.Close()` — acceptable since the OS reclaims resources on process exit. The `RunE` function should not return after `os.Exit` — but on success (exit 0), return `nil` normally to let Cobra handle cleanup, and only use `os.Exit` for non-zero exit codes.

### `internal/cli/commands.go` — Remove stubs

Remove the following stub functions (they move to their own files):
- `newAttachCmd()`
- `newShowCmd()`
- `newListCmd()`
- `newLogCmd()`
- `newExecCmd()`

The `registerCommands` function and `errNotImplemented` var remain unchanged (other stubs like `newDiffCmd`, `newApplyCmd`, etc. still use `errNotImplemented`).

## Implementation Steps

1. **Add `ErrContainerNotRunning` to `internal/sandbox/errors.go`:**
   - Add to the sentinel errors `var` block.

2. **Create `internal/sandbox/inspect.go`:**
   - `SandboxStatus` type and constants.
   - `SandboxInfo` struct.
   - `FormatAge` function.
   - `detectChanges` helper (unexported).
   - `execInContainer` helper (unexported).
   - `DetectStatus` function.
   - `InspectSandbox` function.
   - `ListSandboxes` function.

3. **Create `internal/sandbox/inspect_test.go`:**
   - Tests for `FormatAge`.
   - Tests for `detectChanges`.
   - Tests for `InspectSandbox` (with mock Docker client).
   - Tests for `ListSandboxes` (with mock Docker client).

4. **Create `internal/cli/pager.go`:**
   - `RunPager` function with TTY detection and pager fallback.

5. **Create `internal/cli/pager_test.go`:**
   - Test non-TTY mode (copies to writer).

6. **Create `internal/cli/attach.go`:**
   - `newAttachCmd` with status check and `docker exec -it`.

7. **Create `internal/cli/show.go`:**
   - `newShowCmd` with formatted output.
   - `loadPromptPreview` helper.

8. **Create `internal/cli/list.go`:**
   - `newListCmd` with `ls` alias and tabwriter formatting.

9. **Create `internal/cli/log.go`:**
   - `newLogCmd` reading host file through pager.

10. **Create `internal/cli/exec.go`:**
    - `newExecCmd` with TTY detection and exit code propagation.

11. **Modify `internal/cli/commands.go`:**
    - Remove 5 stub functions. Keep `registerCommands`, `errNotImplemented`, `newBuildCmd`, `newNewCmd`.

12. **Run `go mod tidy`** — adds `golang.org/x/term` as direct dependency.

## Tests

### `internal/sandbox/inspect_test.go`

```go
func TestFormatAge_Seconds(t *testing.T)
// 30 seconds ago → "30s"

func TestFormatAge_Minutes(t *testing.T)
// 5 minutes ago → "5m"

func TestFormatAge_Hours(t *testing.T)
// 2 hours ago → "2h"

func TestFormatAge_Days(t *testing.T)
// 3 days ago → "3d"

func TestDetectChanges_NoWorkDir(t *testing.T)
// Non-existent dir → "-"

func TestDetectChanges_NotGitRepo(t *testing.T)
// Plain dir without .git → "-"

func TestDetectChanges_CleanRepo(t *testing.T)
// Git repo with all committed → "no"

func TestDetectChanges_DirtyRepo(t *testing.T)
// Git repo with modified file → "yes"

func TestDetectChanges_UntrackedFiles(t *testing.T)
// Git repo with untracked files → "yes"

func TestInspectSandbox_NotFound(t *testing.T)
// Non-existent sandbox → ErrSandboxNotFound

func TestInspectSandbox_Removed(t *testing.T)
// Sandbox dir exists, mock Docker returns not-found → StatusRemoved

func TestListSandboxes_Empty(t *testing.T)
// Empty sandboxes dir → empty slice

func TestListSandboxes_SkipsBroken(t *testing.T)
// Mix of valid and invalid sandbox dirs → only valid ones returned
```

### `internal/cli/pager_test.go`

```go
func TestRunPager_NonTTY(t *testing.T)
// When stdout is not a TTY (test environment), content is copied
// directly to stdout. Verify content passes through unchanged.
```

**Note:** Testing the TTY + pager path requires a pseudo-terminal, which is fragile in CI. The non-TTY path (direct copy) is the testable code path. The TTY path is verified manually.

## Verification

```bash
# Must compile
go build ./...

# Linter must pass
make lint

# Unit tests pass
make test

# Manual verification (requires Docker and an existing sandbox):
make build

# Create a test sandbox first
mkdir -p /tmp/test-project && echo "hello" > /tmp/test-project/file.txt
./yoloai new test-inspect --agent test /tmp/test-project

# Test show
./yoloai show test-inspect
# Should display: Name, Status (running), Agent (test), Workdir, Created, etc.

# Test list
./yoloai list
# Should show table with test-inspect entry
./yoloai ls
# Alias should work identically

# Test log
./yoloai log test-inspect
# Should show log output (may be empty if agent just started)

# Test attach
./yoloai attach test-inspect
# Should attach to tmux session (Ctrl-b d to detach)

# Test exec
./yoloai exec test-inspect echo hello
# Should print "hello"
./yoloai exec test-inspect ls /yoloai/
# Should show config.json, log.txt, etc.

# Test exec exit code propagation
./yoloai exec test-inspect sh -c "exit 42"; echo $?
# Should print "42"

# Clean up
docker stop yoloai-test-inspect
docker rm yoloai-test-inspect
rm -rf ~/.yoloai/sandboxes/test-inspect
```

## Concerns

### 1. Docker stream multiplexing

`execInContainer` must use `stdcopy.StdCopy` (from `github.com/docker/docker/pkg/stdcopy`) to demux Docker's multiplexed stream. When `TTY: false` in exec options, Docker prepends 8-byte headers to each frame indicating stdout vs stderr and payload length. Reading the stream directly without `StdCopy` produces garbled output with binary header bytes mixed in.

### 2. tmux crash edge case

If tmux crashes inside the container, `DetectStatus` falls back to `StatusRunning` when the tmux query fails. This is the safest default — the container is running, so reporting it as running is more accurate than guessing stopped/failed. The user can inspect further with `exec`.

### 3. `os.Exit` in exec

`os.Exit` bypasses defers. Only `client.Close()` is affected. The OS reclaims all resources (file descriptors, sockets) on process exit. To minimize impact: only call `os.Exit` for non-zero exit codes; let zero-exit return normally through Cobra.

### 4. UTF-8 truncation in `loadPromptPreview`

Naive byte-level truncation can split a multi-byte UTF-8 character. Use `[]rune(content)` conversion to operate on codepoint boundaries. The 200-rune limit is approximate — display width varies with character width, but rune-level truncation is safe and correct.

### 5. `errdefs.IsNotFound` in `DetectStatus`

`ContainerInspect` returns a not-found error when the container was removed but the sandbox directory still exists. Use `errdefs.IsNotFound` (from `github.com/containerd/errdefs`, already a dependency) to distinguish "container removed" from API errors. Other errors (connection refused, timeout) should propagate.

### 6. List performance with many sandboxes

`ListSandboxes` makes one `ContainerInspect` call per sandbox — O(N) Docker API calls. For MVP this is acceptable (users are unlikely to have hundreds of sandboxes). If performance becomes an issue, optimize with a single `ContainerList` call filtered by `yoloai-` prefix, then join with sandbox metadata.

### 7. Pager fallback

`less` may not be installed on minimal systems (e.g., Alpine-based containers, though yoloAI runs on the host). Fallback chain: `$PAGER` → `less -R` → direct copy to stdout. Users on minimal systems can set `PAGER=cat` or `PAGER=more`.

### 8. `attach` with `-i` stdin detection

`attach` always passes `-it` to `docker exec` since it's connecting to an interactive tmux session. The TTY detection in `exec` (interactive `-i`, allocate TTY `-t`) doesn't apply to `attach` — tmux requires both.

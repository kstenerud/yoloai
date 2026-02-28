# Phase 6: Diff and Apply

## Goal

Working `diff` and `apply` commands — the core differentiator. `diff` shows what the agent changed inside the sandbox. `apply` lands those changes back to the original host directory with review and confirmation. MVP operates on full-copy only (no overlay). All operations run entirely on the host — no Docker client needed for diff/apply logic.

## Prerequisites

- Phase 5 complete (inspection, pager, status detection)
- At least one sandbox created with `:copy` mode workdir
- Git available on host (required for diff generation and patch application)

## Files to Create

| File | Description |
|------|-------------|
| `internal/sandbox/diff.go` | `DiffOptions`, `DiffResult`, `GenerateDiff`, `GenerateDiffStat`, `stageUntracked`, `loadDiffContext` |
| `internal/sandbox/diff_test.go` | Tests for diff logic (copy mode, rw mode, stat, path filter, untracked files, binary files, empty diff) |
| `internal/sandbox/apply.go` | `GeneratePatch`, `CheckPatch`, `ApplyPatch`, `isGitRepo`, `runGitApply`, `formatApplyError` |
| `internal/sandbox/apply_test.go` | Tests for apply logic (git target, non-git target, path filter, conflict, new/delete files) |
| `internal/cli/diff.go` | `newDiffCmd()` replacing stub, `--stat` flag, agent-running warning via `InspectSandbox` |
| `internal/cli/apply.go` | `newApplyCmd()` replacing stub, `--yes` flag, `--` path parsing, confirmation |

## Files to Modify

| File | Change |
|------|--------|
| `internal/cli/commands.go` | Remove `newDiffCmd` and `newApplyCmd` stub functions |

## Types and Signatures

### `internal/sandbox/diff.go`

```go
package sandbox

// DiffOptions controls diff generation.
type DiffOptions struct {
	Name  string   // sandbox name
	Stat  bool     // true for --stat summary only
	Paths []string // optional path filter (relative to workdir)
}

// DiffResult holds the output of a diff operation.
type DiffResult struct {
	Output  string // diff text or stat summary
	WorkDir string // work directory that was diffed
	Mode    string // "copy" or "rw"
	Empty   bool   // true if no changes detected
}

// GenerateDiff produces a full diff of agent changes for a sandbox.
// For :copy mode: stages untracked files, then runs git diff --binary
// against the baseline SHA stored in meta.json.
// For :rw mode: runs git diff HEAD on the live host directory.
// Returns an informational DiffResult (not error) for :rw non-git dirs.
func GenerateDiff(opts DiffOptions) (*DiffResult, error)

// GenerateDiffStat produces a summary (files changed, insertions,
// deletions) instead of the full diff.
func GenerateDiffStat(opts DiffOptions) (*DiffResult, error)
```

**Unexported helpers:**

```go
// stageUntracked runs `git add -A` in the work directory to capture
// files created by the agent that are not yet tracked.
func stageUntracked(workDir string) error

// loadDiffContext loads the metadata and resolves paths needed for diff.
// Returns the work directory, baseline SHA, and workdir mode.
// For :copy mode, workDir is ~/.yoloai/sandboxes/<name>/work/<encoded>/.
// For :rw mode, workDir is the original host path.
func loadDiffContext(name string) (workDir string, baselineSHA string, mode string, err error)
```

### `internal/sandbox/apply.go`

```go
package sandbox

// GeneratePatch produces a binary patch from the work copy against
// the baseline SHA. Optionally filtered to specific paths.
// Returns the patch bytes and a stat summary string.
func GeneratePatch(name string, paths []string) (patch []byte, stat string, err error)

// CheckPatch verifies the patch applies cleanly to the target directory
// via `git apply --check`. Returns nil if clean, error with context if not.
func CheckPatch(patch []byte, targetDir string, isGit bool) error

// ApplyPatch applies the patch to the target directory.
// For git repos: runs `git apply` from within the repo.
// For non-git dirs: runs `git apply --unsafe-paths --directory=<path>`
// from the work copy dir (which has .git/).
func ApplyPatch(patch []byte, targetDir string, isGit bool) error

// isGitRepo checks if a directory is a git repository by looking for .git/.
func isGitRepo(dir string) bool
```

**Unexported helpers:**

```go
// runGitApply executes `git apply` with the given args, feeding the
// patch via stdin. Returns the combined output on error.
func runGitApply(dir string, patch []byte, args ...string) error

// formatApplyError wraps a cryptic git apply error with human-readable
// context. Parses the error output to identify conflicting files and
// provides actionable guidance.
func formatApplyError(gitErr error, targetDir string) error
```

### `internal/cli/diff.go`

```go
package cli

import "github.com/spf13/cobra"

// newDiffCmd returns the diff command.
// Loads sandbox metadata, optionally warns if agent is still running,
// generates diff output, and pipes through the pager.
func newDiffCmd() *cobra.Command
```

### `internal/cli/apply.go`

```go
package cli

import "github.com/spf13/cobra"

// newApplyCmd returns the apply command.
// Generates patch, shows stat summary, dry-run check, confirmation
// prompt, then applies. Supports `-- <path>...` for path filtering.
func newApplyCmd() *cobra.Command
```

## Design Decisions

### 1. Standalone functions, not Manager methods

`GenerateDiff`, `GeneratePatch`, `ApplyPatch` are package-level functions, not methods on `Manager`. Full-copy diff/apply operates entirely on host-side files in `work/<encoded-path>/` — no Docker client needed. This keeps the API simple and testable. The CLI commands create a Docker client only for the agent-running warning (diff) and not at all for apply.

### 2. `git add -A` before every diff

Agent-created files are untracked in the work copy's git repo. Without staging them first, `git diff` would silently omit new files. Running `git add -A` before every diff ensures all agent changes (modifications, additions, deletions) appear in the output. This modifies the git index in the work copy, which is acceptable — the work copy is a sandbox artifact, not user code.

### 3. `git diff --binary` for both display and patch generation

`--binary` encodes binary file changes (images, compiled assets) in the diff output. Without it, binary changes would show only "Binary files differ" in the diff and be silently dropped from the patch. Using `--binary` for display produces slightly noisier output for binary files, but ensures the diff is complete and the same command generates both the display and the patch.

### 4. Two `git apply` strategies

**Git repo target:** `git -C <targetDir> apply` reads the patch from stdin. This is the standard path — git apply knows how to resolve paths relative to the repo root.

**Non-git target:** `git -C <workDir> apply --unsafe-paths --directory=<targetDir>` runs from the work copy dir (which has `.git/`). `--unsafe-paths` allows writing outside the git repo, `--directory` specifies the target. This requires git on the host but not in the target directory.

### 5. Path filtering via `-- <path>...`

Uses Cobra's `ArgsLenAtDash()` to separate sandbox name from path filters. Paths after `--` are passed to `git diff -- <path>...` for both display and patch generation. Empty paths (bare `--` or no `--`) means "all changes". This is the natural git convention and requires no special handling — an empty path slice produces the same result as no `-- <path>` argument.

### 6. Agent-running warning via `InspectSandbox`

The diff command checks if the agent is still running by calling `InspectSandbox`. If the status is `StatusRunning`, it prints a warning to stderr: `"Note: agent is still running; diff may be incomplete"`. If Docker is unavailable (e.g., Docker daemon not running), the warning is skipped silently — the diff still works because it reads host-side files. Best-effort warning, not a gate.

### 7. `formatApplyError` for human-readable context

`git apply` errors are cryptic (e.g., `error: patch failed: handler.go:42`). `formatApplyError` parses the error output and wraps it with actionable context, e.g.: `"changes to handler.go conflict with your working directory — the patch expected different content at line 42. This typically means the original file was edited after the sandbox was created."` This helps users understand and resolve conflicts without git expertise.

### 8. `:rw` mode diff is relative to HEAD, not creation-time baseline

For `:rw` directories, there's no work copy — the agent writes directly to the host directory. If the host directory is a git repo, `git diff HEAD` shows all uncommitted changes, which includes both agent changes and any pre-existing uncommitted changes. There's no way to separate them. If the host directory is not a git repo, diff prints an informational message (not error) explaining that diff requires git. Apply is not applicable for `:rw` mode since changes are already live.

## Detailed Implementation

### `internal/sandbox/diff.go` — Core diff logic

#### `loadDiffContext`

```go
func loadDiffContext(name string) (workDir string, baselineSHA string, mode string, err error)
```

1. Load metadata: `LoadMeta(Dir(name))`.
2. If `Dir(name)` doesn't exist, return `ErrSandboxNotFound`.
3. Get mode from `meta.Workdir.Mode`.
4. For `"copy"` mode:
   - `workDir = WorkDir(name, meta.Workdir.HostPath)`.
   - `baselineSHA = meta.Workdir.BaselineSHA`.
   - If `baselineSHA` is empty, return error: `"sandbox has no baseline SHA — was it created before diff support?"`.
5. For `"rw"` mode:
   - `workDir = meta.Workdir.HostPath` (the live host directory).
   - `baselineSHA = "HEAD"` (diff against current HEAD, not a stored SHA).
6. Return `workDir, baselineSHA, mode, nil`.

#### `stageUntracked`

```go
func stageUntracked(workDir string) error
```

1. Call `runGitCmd(workDir, "add", "-A")`.
2. Return any error.

Uses `runGitCmd` from `internal/sandbox/create.go:526`.

#### `GenerateDiff`

```go
func GenerateDiff(opts DiffOptions) (*DiffResult, error)
```

1. `loadDiffContext(opts.Name)` → get `workDir`, `baselineSHA`, `mode`.
2. If `mode == "rw"`:
   a. Check `isGitRepo(workDir)`. If not, return `&DiffResult{Output: "Diff not available: " + workDir + " is not a git repository (live-mounted :rw directory)", Mode: "rw", Empty: true}`.
   b. Run `git -C <workDir> diff HEAD` (with optional `-- <paths>...`).
   c. Return result. No `git add -A` for `:rw` — we don't modify the user's repo index.
3. If `mode == "copy"`:
   a. `stageUntracked(workDir)`.
   b. Build args: `[]string{"diff", "--binary", baselineSHA}`.
   c. If `opts.Paths` is non-empty, append `"--"` then each path.
   d. Run `git -C <workDir> <args>` via `exec.Command`, capture stdout.
   e. If output is empty, return `&DiffResult{Empty: true, ...}`.
   f. Return `&DiffResult{Output: output, WorkDir: workDir, Mode: "copy"}`.

#### `GenerateDiffStat`

```go
func GenerateDiffStat(opts DiffOptions) (*DiffResult, error)
```

Same as `GenerateDiff` but uses `git diff --stat <baseline>` instead of `git diff --binary <baseline>`. For `:rw` mode, uses `git diff --stat HEAD`.

### `internal/sandbox/apply.go` — Core apply logic

#### `isGitRepo`

```go
func isGitRepo(dir string) bool
```

1. `os.Stat(filepath.Join(dir, ".git"))`.
2. Return `err == nil`.

#### `GeneratePatch`

```go
func GeneratePatch(name string, paths []string) (patch []byte, stat string, err error)
```

1. `loadDiffContext(name)` → get `workDir`, `baselineSHA`, `mode`.
2. If `mode == "rw"`, return error: `"apply is not needed for :rw directories — changes are already live"`.
3. `stageUntracked(workDir)`.
4. Build patch args: `[]string{"-C", workDir, "diff", "--binary", baselineSHA}`.
5. Build stat args: `[]string{"-C", workDir, "diff", "--stat", baselineSHA}`.
6. If `paths` is non-empty, append `"--"` then each path to both arg lists.
7. Run patch command, capture stdout as `[]byte`.
8. Run stat command, capture stdout as `string`.
9. Return `patch, stat, nil`.

#### `CheckPatch`

```go
func CheckPatch(patch []byte, targetDir string, isGit bool) error
```

1. If `isGit`:
   - `runGitApply(targetDir, patch, "--check")`.
2. If not git:
   - Need a directory with `.git/` to run `git apply`. Use `os.MkdirTemp` to create a temp dir, `git init` in it, then run `git apply --check --unsafe-paths --directory=<targetDir>` from the temp dir. Clean up temp dir after.
   - Actually, simpler: run `git apply --check --unsafe-paths --directory=<targetDir>` from any dir that has git context. We can pass the patch via stdin and use `git apply --check --unsafe-paths --directory=<targetDir>` without needing to be in a git repo since `--unsafe-paths` allows absolute paths. But `git apply` still needs a git repo context when `--unsafe-paths` is used.
   - Create a temp dir with `git init`, run `git -C <tempDir> apply --check --unsafe-paths --directory=<targetDir>` with patch on stdin. Defer cleanup.
3. If error, return `formatApplyError(err, targetDir)`.
4. Return `nil`.

#### `ApplyPatch`

```go
func ApplyPatch(patch []byte, targetDir string, isGit bool) error
```

1. If `isGit`:
   - `runGitApply(targetDir, patch)`.
2. If not git:
   - Same temp-dir-with-git approach as `CheckPatch`.
   - `git -C <tempDir> apply --unsafe-paths --directory=<targetDir>` with patch on stdin.
3. If error, return `formatApplyError(err, targetDir)`.
4. Return `nil`.

#### `runGitApply`

```go
func runGitApply(dir string, patch []byte, args ...string) error
```

1. Build full args: `[]string{"-C", dir, "apply"}` + `args`.
2. Create `exec.Command("git", fullArgs...)`.
3. Set `cmd.Stdin` to `bytes.NewReader(patch)`.
4. `cmd.CombinedOutput()` → capture output.
5. If error, return `fmt.Errorf("%s: %w", strings.TrimSpace(string(output)), err)`.
6. Return `nil`.

#### `formatApplyError`

```go
func formatApplyError(gitErr error, targetDir string) error
```

1. Parse the error message for common patterns:
   - `"error: patch failed: <file>:<line>"` → extract file and line.
   - `"error: <file>: does not exist in working directory"` → file was deleted.
   - `"error: <file>: already exists in working directory"` → new file conflict.
2. Wrap with context:
   - Patch failed: `"changes to <file> conflict with your working directory — the patch expected different content at line <N>. This typically means the original file was edited after the sandbox was created."`.
   - File missing: `"cannot apply deletion to <file> — the file no longer exists in <targetDir>"`.
   - File exists: `"cannot create <file> — it already exists in <targetDir> with different content"`.
3. If pattern doesn't match, return the original error with a generic wrapper: `"git apply failed in <targetDir>: <original error>"`.

### `internal/cli/diff.go` — Diff command

```go
func newDiffCmd() *cobra.Command
```

Cobra command:
- `Use: "diff <name> [-- <path>...]"`
- `Short: "Show changes the agent made"`
- `Args: cobra.ArbitraryArgs`
- Flags: `--stat` (bool, default false)

RunE:
1. Parse args with `cmd.ArgsLenAtDash()`:
   - Positional args before `--`: must be exactly 1 (sandbox name).
   - Args after `--`: path filter.
2. Get sandbox name from positional args.
3. Agent-running warning (best-effort):
   a. Attempt to create Docker client. If error (Docker unavailable), skip warning.
   b. Call `InspectSandbox(ctx, client, name)`. If error, skip warning.
   c. If status is `StatusRunning`, print to stderr: `"Note: agent is still running; diff may be incomplete\n"`.
4. Build `DiffOptions{Name: name, Paths: paths}`.
5. If `--stat`:
   - `GenerateDiffStat(opts)` → result.
   - Print `result.Output` to `cmd.OutOrStdout()`.
6. If not `--stat`:
   - `GenerateDiff(opts)` → result.
   - If `result.Empty`, print `"No changes"` and return.
   - Pipe `result.Output` through `RunPager(strings.NewReader(result.Output))`.

### `internal/cli/apply.go` — Apply command

```go
func newApplyCmd() *cobra.Command
```

Cobra command:
- `Use: "apply <name> [-- <path>...]"`
- `Short: "Apply agent changes back to original directory"`
- `Args: cobra.ArbitraryArgs`
- Flags: `--yes` / `-y` (bool, default false)

RunE:
1. Parse args with `cmd.ArgsLenAtDash()`:
   - Positional args before `--`: must be exactly 1 (sandbox name).
   - Args after `--`: path filter.
2. Get sandbox name.
3. Load metadata: `LoadMeta(Dir(name))` — need `meta.Workdir.HostPath` for the target directory and `meta.Workdir.Mode` for validation.
4. If `meta.Workdir.Mode == "rw"`, return error: `"apply is not needed for :rw directories — changes are already live"`.
5. `GeneratePatch(name, paths)` → `patch, stat, err`.
6. If `len(patch) == 0`, print `"No changes to apply"` and return.
7. Print stat summary to `cmd.OutOrStdout()`.
8. Determine target: `targetDir = meta.Workdir.HostPath`.
9. Determine if target is a git repo: `isGit = isGitRepo(targetDir)`.
10. Dry-run: `CheckPatch(patch, targetDir, isGit)`.
11. If dry-run fails, return the wrapped error (user sees what conflicts).
12. Confirmation (unless `--yes`):
    ```
    Apply these changes to <targetDir>? [y/N]
    ```
    Use `Confirm(prompt, os.Stdin, cmd.ErrOrStderr())`. If declined, return `nil`.
13. `ApplyPatch(patch, targetDir, isGit)`.
14. Print `"Changes applied to <targetDir>"` to `cmd.OutOrStdout()`.

### `internal/cli/commands.go` — Remove stubs

Remove the following stub functions (they move to their own files):
- `newDiffCmd()`
- `newApplyCmd()`

The `registerCommands` function, `errNotImplemented` var, and remaining stubs (`newStopCmd`, `newStartCmd`, `newDestroyCmd`, `newResetCmd`, `newCompletionCmd`) remain unchanged.

## Implementation Steps

1. **Create `internal/sandbox/diff.go`:**
   - `DiffOptions` and `DiffResult` types.
   - `loadDiffContext` helper (uses `LoadMeta`, `Dir`, `WorkDir`).
   - `stageUntracked` helper (uses `runGitCmd`).
   - `GenerateDiff` function.
   - `GenerateDiffStat` function.

2. **Create `internal/sandbox/diff_test.go`:**
   - Tests for copy-mode diff, rw-mode diff, stat, path filter, untracked files, binary files, empty diff.

3. **Create `internal/sandbox/apply.go`:**
   - `isGitRepo` helper.
   - `runGitApply` helper.
   - `formatApplyError` helper.
   - `GeneratePatch` function.
   - `CheckPatch` function.
   - `ApplyPatch` function.

4. **Create `internal/sandbox/apply_test.go`:**
   - Tests for git target, non-git target, path filter, conflict detection, new/delete files.

5. **Create `internal/cli/diff.go`:**
   - `newDiffCmd` with `--stat` flag, agent-running warning, pager integration.

6. **Create `internal/cli/apply.go`:**
   - `newApplyCmd` with `--yes` flag, `--` path parsing, stat summary, dry-run, confirmation.

7. **Modify `internal/cli/commands.go`:**
   - Remove `newDiffCmd` and `newApplyCmd` stubs. Keep `registerCommands`, `errNotImplemented`, remaining stubs.

8. **Run `go build ./...` and `make lint`.**

## Tests

### `internal/sandbox/diff_test.go`

```go
func TestGenerateDiff_CopyMode_ModifiedFile(t *testing.T)
// Create sandbox dir structure with meta.json (copy mode, baseline SHA).
// Init git repo in work dir, commit baseline, modify a file.
// GenerateDiff → output contains the modification diff.

func TestGenerateDiff_CopyMode_UntrackedFile(t *testing.T)
// Same setup, but add a new file without committing.
// GenerateDiff → output contains the new file (git add -A captures it).

func TestGenerateDiff_CopyMode_BinaryFile(t *testing.T)
// Same setup, add a binary file (e.g., bytes with \x00).
// GenerateDiff → output contains binary diff (--binary flag).

func TestGenerateDiff_CopyMode_Empty(t *testing.T)
// Clean work copy, no changes since baseline.
// GenerateDiff → DiffResult.Empty == true.

func TestGenerateDiff_CopyMode_PathFilter(t *testing.T)
// Modify two files, filter to one.
// GenerateDiff with Paths → output contains only the filtered file.

func TestGenerateDiff_RWMode_GitRepo(t *testing.T)
// Create sandbox with rw mode meta, live host dir is a git repo.
// Modify a file. GenerateDiff → shows uncommitted changes.

func TestGenerateDiff_RWMode_NotGitRepo(t *testing.T)
// Create sandbox with rw mode meta, live host dir is not a git repo.
// GenerateDiff → DiffResult.Empty == true, Output contains informational message.

func TestGenerateDiffStat_CopyMode(t *testing.T)
// Modify files in work copy.
// GenerateDiffStat → output contains stat summary (file names, +/- counts).

func TestLoadDiffContext_SandboxNotFound(t *testing.T)
// Non-existent sandbox → ErrSandboxNotFound.

func TestLoadDiffContext_NoBaseline(t *testing.T)
// Sandbox exists with empty BaselineSHA → descriptive error.
```

Test helpers reuse `initGitRepo`, `writeTestFile`, `gitAdd`, `gitCommit`, `runGit` from `internal/sandbox/safety_test.go`. These helpers are in the `sandbox` package test file, so they're available to `diff_test.go` in the same package.

### `internal/sandbox/apply_test.go`

```go
func TestGeneratePatch_CopyMode(t *testing.T)
// Create sandbox work copy, modify file.
// GeneratePatch → returns non-empty patch and stat summary.

func TestGeneratePatch_RWMode_Error(t *testing.T)
// Create sandbox with rw mode meta.
// GeneratePatch → returns descriptive error.

func TestGeneratePatch_PathFilter(t *testing.T)
// Modify two files, filter to one.
// GeneratePatch → patch contains only the filtered file.

func TestGeneratePatch_Empty(t *testing.T)
// No changes in work copy.
// GeneratePatch → empty patch, empty stat.

func TestApplyPatch_GitTarget(t *testing.T)
// Create target git repo with original file.
// Apply patch that modifies the file.
// Verify file content changed in target.

func TestApplyPatch_NonGitTarget(t *testing.T)
// Create target dir (no git) with original file.
// Apply patch. Verify file content changed.

func TestApplyPatch_NewFile(t *testing.T)
// Patch adds a new file. Apply to target.
// Verify new file exists in target.

func TestApplyPatch_DeleteFile(t *testing.T)
// Patch deletes a file. Apply to target with that file.
// Verify file removed from target.

func TestCheckPatch_Conflict(t *testing.T)
// Create target with modified file (different from what patch expects).
// CheckPatch → error with human-readable context.

func TestCheckPatch_Clean(t *testing.T)
// Create target matching original. CheckPatch → nil.

func TestIsGitRepo_True(t *testing.T)
// Dir with .git/ → true.

func TestIsGitRepo_False(t *testing.T)
// Plain dir → false.

func TestFormatApplyError_PatchFailed(t *testing.T)
// Error with "patch failed: handler.go:42" → wrapped with context.

func TestFormatApplyError_Unknown(t *testing.T)
// Unrecognized error → generic wrapper.
```

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

# Create a test project
mkdir -p /tmp/test-diff
echo "original content" > /tmp/test-diff/file.txt
echo "keep this" > /tmp/test-diff/other.txt

# Create sandbox
./yoloai new test-diff --agent test /tmp/test-diff

# Simulate agent changes (modify the work copy directly)
WORK_DIR=~/.yoloai/sandboxes/test-diff/work/$(echo /tmp/test-diff | sed 's|/|^|g')/
echo "modified by agent" > "$WORK_DIR/file.txt"
echo "new file" > "$WORK_DIR/created.txt"

# Test diff
./yoloai diff test-diff
# Should show: file.txt modified, created.txt added

# Test diff --stat
./yoloai diff test-diff --stat
# Should show: 2 files changed, 2 insertions(+), 1 deletion(-)

# Test diff with path filter
./yoloai diff test-diff -- file.txt
# Should show only file.txt changes

# Test apply dry-run
./yoloai apply test-diff
# Should show stat summary, prompt for confirmation

# Test apply with --yes
./yoloai apply test-diff --yes
# Should apply changes without prompting
cat /tmp/test-diff/file.txt
# Should show "modified by agent"
ls /tmp/test-diff/created.txt
# Should exist with "new file" content

# Test apply with path filter
# (reset first, re-create sandbox, re-modify)
./yoloai apply test-diff -- file.txt --yes
# Should apply only file.txt changes

# Test apply conflict
echo "conflicting edit" > /tmp/test-diff/file.txt
./yoloai apply test-diff --yes
# Should show human-readable conflict error

# Clean up
docker stop yoloai-test-diff 2>/dev/null
docker rm yoloai-test-diff 2>/dev/null
rm -rf ~/.yoloai/sandboxes/test-diff /tmp/test-diff
```

## Concerns

### 1. `git apply --unsafe-paths` for non-git targets

Non-git target directories require `--unsafe-paths` with `--directory=<path>` to write outside a git repo context. `git apply` still needs to run inside a directory with `.git/` for its internal index operations. For `CheckPatch` and `ApplyPatch`, we create a temporary git-initialized directory and run `git apply` from there. The work copy dir cannot be reused for this because `git apply` would modify its index.

### 2. Agent-running warning is best-effort

The diff command tries to detect if the agent is running via `InspectSandbox`, which requires a Docker client. If Docker is unavailable (daemon stopped, not installed on this machine, etc.), the warning is silently skipped. The diff itself doesn't need Docker — it reads host-side files. Users who stop the Docker daemon before reviewing diffs still get correct output.

### 3. Binary diff size held in memory

`git diff --binary` output for large binary files can be substantial. Both the diff output and the patch bytes are held in memory as `[]byte`. For MVP this is acceptable — most AI-generated changes are source code, not large binaries. If memory becomes an issue, stream the diff through a temp file.

### 4. `git apply` is not atomic

`git apply` modifies files one at a time. If it fails partway through, some files are already modified. The `--check` dry-run mitigates this by catching conflicts before attempting the real apply. However, a race condition (file modified between check and apply) is theoretically possible. For MVP, the dry-run check is sufficient — users working alone on a branch won't hit this.

### 5. `:rw` diff shows all uncommitted changes

For `:rw` directories, `git diff HEAD` includes pre-existing uncommitted changes — not just agent changes. There's no reliable way to separate them because the agent writes directly to the host directory. The diff output includes a note about this limitation. Users who need clean agent-only diffs should use `:copy` mode.

### 6. Empty `--` paths treated as "apply all"

When the user types `yoloai apply name --` (with `--` but no paths after it), `ArgsLenAtDash()` returns the index, and `args[dashIdx:]` is an empty slice. An empty paths slice is passed through to `git diff` without a `-- <paths>` suffix, producing the full diff. This is the natural behavior — no special-casing needed.

### 7. `stageUntracked` modifies the work copy's git index

`git add -A` stages all untracked and modified files in the work copy. This is safe because the work copy is a sandbox artifact — not the user's original code. However, running `stageUntracked` multiple times is idempotent, and it doesn't create commits (only stages). The baseline commit remains unchanged.

### 8. Reusing `runGitCmd` from create.go

`runGitCmd` (at `internal/sandbox/create.go:526`) runs a git command in a directory and returns only the error (discarding stdout). Diff and apply need to capture stdout. The diff/apply code uses `exec.Command` directly for commands that need output capture, and `runGitCmd` only for fire-and-forget commands like `git add -A`. No new helper is needed — the pattern is consistent with `gitHeadSHA` at `create.go:516` which also uses `exec.Command` directly.

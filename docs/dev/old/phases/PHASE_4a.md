# Phase 4a: Sandbox Infrastructure

## Goal

Error types, safety checks, `SandboxManager` struct, and first-run `EnsureSetup` — all testable independently before the creation workflow (Phase 4b).

## Prerequisites

- Phase 3 complete (base image build, `SeedResources`)
- Docker daemon running (for `EnsureSetup` verification)

## Files to Create

| File | Description |
|------|-------------|
| `internal/sandbox/errors.go` | Sentinel errors and typed error wrappers for exit code mapping |
| `internal/sandbox/safety.go` | Dangerous dir detection, path overlap, dirty repo check |
| `internal/sandbox/safety_test.go` | Unit tests for all safety checks |
| `internal/sandbox/manager.go` | `Manager` struct with `EnsureSetup` |
| `internal/sandbox/manager_test.go` | Unit tests for `EnsureSetup` with mock Docker client |

## Files to Modify

| File | Change |
|------|--------|
| `internal/cli/root.go` | Remove `UsageError`/`ConfigError` definitions, import from sandbox |
| `internal/docker/client.go` | Add `ImageExists` convenience method to `Client` interface |

## Error Types

### `internal/sandbox/errors.go`

```go
package sandbox

import (
	"errors"
	"fmt"
)

// Sentinel errors for sandbox operations.
var (
	ErrSandboxNotFound   = errors.New("sandbox not found")
	ErrSandboxExists     = errors.New("sandbox already exists")
	ErrDockerUnavailable = errors.New("Docker is not available")
	ErrMissingAPIKey     = errors.New("required API key not set")
)

// UsageError indicates bad arguments or missing required args (exit code 2).
type UsageError struct {
	Err error
}

func (e *UsageError) Error() string { return e.Err.Error() }
func (e *UsageError) Unwrap() error { return e.Err }

// NewUsageError wraps a message as a UsageError.
func NewUsageError(format string, args ...any) *UsageError {
	return &UsageError{Err: fmt.Errorf(format, args...)}
}

// ConfigError indicates a configuration problem (exit code 3).
type ConfigError struct {
	Err error
}

func (e *ConfigError) Error() string { return e.Err.Error() }
func (e *ConfigError) Unwrap() error { return e.Err }

// NewConfigError wraps a message as a ConfigError.
func NewConfigError(format string, args ...any) *ConfigError {
	return &ConfigError{Err: fmt.Errorf(format, args...)}
}
```

**Notes:**
- `UsageError` and `ConfigError` move from `internal/cli/root.go` to here. CLI imports sandbox for these types.
- Constructor helpers `NewUsageError`/`NewConfigError` added for convenience — they're used frequently in Phase 4b.
- Sentinel errors use `errors.New` for simple `errors.Is` checks.

### CLI migration in `internal/cli/root.go`

Remove the `UsageError` and `ConfigError` type definitions and their methods. Replace with imports from the sandbox package:

```go
import "github.com/kstenerud/yoloai/internal/sandbox"
```

The error checking in `Execute` changes from:
```go
var usageErr *UsageError
```
to:
```go
var usageErr *sandbox.UsageError
```

Same for `ConfigError`.

## Safety Checks

### `internal/sandbox/safety.go`

```go
package sandbox

// IsDangerousDir checks whether the given absolute path is a dangerous
// mount target. Resolves symlinks before checking. Does not consider
// :force — the caller handles downgrading errors to warnings.
func IsDangerousDir(absPath string) bool

// CheckPathOverlap checks if any two paths in the list have a prefix
// overlap (one is a parent of the other, or they are identical).
// All paths must be absolute. Resolves symlinks before comparing.
// Returns an error describing the first overlap found, or nil.
func CheckPathOverlap(paths []string) error

// CheckDirtyRepo checks if the given path is a git repository with
// uncommitted changes. Returns a human-readable warning string if
// dirty (e.g., "3 files modified, 1 untracked"), empty string if clean
// or not a git repo. Returns error only on unexpected failures
// (e.g., git command failed for non-obvious reasons).
func CheckDirtyRepo(path string) (string, error)
```

**`IsDangerousDir` behavior:**
1. Call `filepath.EvalSymlinks` on `absPath`. If the symlink resolution fails (broken symlink, permission error), use the original path — don't block on unresolvable symlinks.
2. Check the resolved path against a set of dangerous paths:
   - `$HOME` (via `os.UserHomeDir`)
   - `/`
   - Linux system dirs: `/usr`, `/etc`, `/var`, `/boot`, `/bin`, `/sbin`, `/lib`
   - macOS system dirs: `/System`, `/Library`, `/Applications`
3. Return `true` if the resolved path matches any dangerous path exactly.

**`CheckPathOverlap` behavior:**
1. Resolve all paths through `filepath.EvalSymlinks` (same resilience as above).
2. For each pair of resolved paths (i, j where i < j), check if either is a prefix of the other. Prefix check: `strings.HasPrefix(longer, shorter + "/")` or exact match.
3. Return `fmt.Errorf("path overlap: %s contains %s", shorter, longer)` for the first overlap found.
4. O(n²) comparison is fine — typical sandbox has 1-3 directories.

**`CheckDirtyRepo` behavior:**
1. Check if `path/.git` exists. If not, return `("", nil)` — not a git repo, not dirty.
2. Run `git -C <path> status --porcelain`. Parse output.
3. If output is empty, return `("", nil)` — clean repo.
4. Count modified/untracked files from status output. Return a summary string like `"3 files modified, 1 untracked"`.
5. On `git` command failure (e.g., git not installed), return `("", nil)` — don't fail sandbox creation because git status check failed. Log a debug warning.

## Manager

### `internal/sandbox/manager.go`

```go
package sandbox

import (
	"context"
	"io"
	"log/slog"

	"github.com/kstenerud/yoloai/internal/docker"
)

// Manager is the central orchestrator for sandbox operations.
type Manager struct {
	client docker.Client
	logger *slog.Logger
	output io.Writer // user-facing messages (typically os.Stderr)
}

// NewManager creates a Manager with the given Docker client, logger,
// and output writer for user-facing messages.
func NewManager(client docker.Client, logger *slog.Logger, output io.Writer) *Manager

// EnsureSetup performs first-run auto-setup. Idempotent — safe to call
// before every sandbox operation. Called at the start of Create.
func (m *Manager) EnsureSetup(ctx context.Context) error
```

**`EnsureSetup` behavior:**
1. Get home directory via `os.UserHomeDir`.
2. Create `~/.yoloai/` directory structure if absent:
   - `~/.yoloai/sandboxes/`
   - `~/.yoloai/profiles/`
   - `~/.yoloai/cache/`
   All with `0750` permissions via `os.MkdirAll`.
3. Seed `Dockerfile.base` and `entrypoint.sh` via `docker.SeedResources(yoloaiDir)`.
4. Check if `yoloai-base` image exists: `m.client.ImageInspectWithRaw(ctx, "yoloai-base")`.
   - Use `errdefs.IsNotFound(err)` from `github.com/containerd/errdefs` to distinguish "not found" from real errors.
   - If not found: print `"Building base image (first run only, this may take a few minutes)...\n"` to `m.output`, then call `docker.BuildBaseImage(ctx, m.client, yoloaiDir, m.output, m.logger)`.
   - If found: skip build (fast path).
   - If real error: return wrapped error.
5. Detect first run via absence of `~/.yoloai/config.yaml`.
   - If missing: write default `config.yaml`, print shell completion hint.
6. Write default `config.yaml` if missing (step 5 triggers this). Content:

```yaml
# yoloai configuration
# See https://github.com/kstenerud/yoloai for documentation

defaults:
  agent: claude

  mounts:
    - ~/.gitconfig:/home/yoloai/.gitconfig:ro

  ports: []

  resources:
    cpus: 4
    memory: 8g
```

Permissions: `0600`.

7. Shell completion hint (first run only — print to `m.output`):

```
Tip: enable shell completions with 'yoloai completion --help'
```

Keep it one line — don't overwhelm the first-run output.

**Note:** `config.yaml` is written for user reference only. MVP does not read it. Config file parsing is deferred to post-MVP.

### Helper: `imageExists`

Rather than adding a method to the `docker.Client` interface (which would diverge from the Docker SDK contract), use a package-level helper in the sandbox package:

```go
// imageExists checks if a Docker image with the given tag exists.
func imageExists(ctx context.Context, client docker.Client, tag string) (bool, error)
```

Uses `client.ImageInspectWithRaw(ctx, tag)`. Returns `(true, nil)` if found, `(false, nil)` if not found (via `errdefs.IsNotFound`), `(false, err)` for other errors.

This keeps `docker.Client` as a pure mirror of the Docker SDK. No changes to `internal/docker/client.go` needed.

## Implementation Steps

1. **Create `internal/sandbox/errors.go`:**
   - Sentinel errors: `ErrSandboxNotFound`, `ErrSandboxExists`, `ErrDockerUnavailable`, `ErrMissingAPIKey`.
   - Typed errors: `UsageError`, `ConfigError` with `Error()`, `Unwrap()`, and constructor helpers.

2. **Migrate `internal/cli/root.go`:**
   - Remove `UsageError` and `ConfigError` type definitions and their methods.
   - Add `import "github.com/kstenerud/yoloai/internal/sandbox"`.
   - Update `errors.As` checks to use `*sandbox.UsageError` and `*sandbox.ConfigError`.

3. **Create `internal/sandbox/safety.go`:**
   - `IsDangerousDir(absPath string) bool`
   - `CheckPathOverlap(paths []string) error`
   - `CheckDirtyRepo(path string) (string, error)` — uses `os/exec` to run `git`.

4. **Create `internal/sandbox/safety_test.go`:**
   - Tests for `IsDangerousDir`: home dir, root, system dirs, safe dir.
   - Tests for `CheckPathOverlap`: no overlap, parent/child, identical paths, disjoint.
   - Tests for `CheckDirtyRepo`: clean repo, dirty repo, not a git repo.

5. **Create `internal/sandbox/manager.go`:**
   - `Manager` struct, `NewManager` constructor.
   - `imageExists` helper function.
   - `EnsureSetup` method with directory creation, resource seeding, image check, config writing, completion hint.
   - Embed or define default config.yaml content as a constant.

6. **Create `internal/sandbox/manager_test.go`:**
   - Mock Docker client implementing `docker.Client` interface (most methods return `errNotImplemented`; `ImageInspectWithRaw` and `ImageBuild` configurable).
   - Test `EnsureSetup` creates directory structure.
   - Test `EnsureSetup` writes config.yaml on first run.
   - Test `EnsureSetup` skips build when image exists.
   - Test `EnsureSetup` builds image when missing (mock returns not-found, then ImageBuild succeeds).

7. **Run `go mod tidy`** (for `github.com/containerd/errdefs` — already indirect, may need direct import).

## Tests

### `internal/sandbox/safety_test.go`

```go
func TestIsDangerousDir_Home(t *testing.T)
// os.UserHomeDir() → IsDangerousDir returns true

func TestIsDangerousDir_Root(t *testing.T)
// "/" → returns true

func TestIsDangerousDir_SystemDirs(t *testing.T)
// "/usr", "/etc", "/var", "/boot", "/bin", "/sbin", "/lib" → all true
// "/System", "/Library", "/Applications" → all true (cross-platform safety)

func TestIsDangerousDir_SafeDir(t *testing.T)
// "/tmp/myproject" → returns false

func TestCheckPathOverlap_NoOverlap(t *testing.T)
// ["/a", "/b", "/c"] → nil

func TestCheckPathOverlap_ParentChild(t *testing.T)
// ["/a", "/a/b"] → error containing both paths

func TestCheckPathOverlap_Identical(t *testing.T)
// ["/a", "/a"] → error

func TestCheckPathOverlap_DisjointSimilarNames(t *testing.T)
// ["/abc", "/ab"] → nil (not a prefix overlap — /ab is not /abc's parent)

func TestCheckDirtyRepo_CleanRepo(t *testing.T)
// Create temp dir, git init, add file, commit → ("", nil)

func TestCheckDirtyRepo_DirtyRepo(t *testing.T)
// Create temp dir, git init, add+commit, then modify file → non-empty warning

func TestCheckDirtyRepo_NotGitRepo(t *testing.T)
// Plain temp dir → ("", nil)
```

### `internal/sandbox/manager_test.go`

```go
// mockClient implements docker.Client for testing.
// Fields: imageExistsResult bool, imageExistsErr error, buildCalled bool, buildErr error
type mockClient struct { ... }

func TestEnsureSetup_CreatesDirectories(t *testing.T)
// Set HOME to temp dir, mock image exists → directories created

func TestEnsureSetup_WritesConfigOnFirstRun(t *testing.T)
// Set HOME to temp dir, mock image exists → config.yaml written

func TestEnsureSetup_SkipsConfigOnSubsequentRun(t *testing.T)
// Write config.yaml first, call EnsureSetup → config.yaml not overwritten

func TestEnsureSetup_SkipsBuildWhenImageExists(t *testing.T)
// Mock ImageInspectWithRaw returns success → ImageBuild not called

func TestEnsureSetup_BuildsWhenImageMissing(t *testing.T)
// Mock ImageInspectWithRaw returns not-found → ImageBuild called
```

**Testing note:** Tests that need `os.UserHomeDir` to return a custom path should set `$HOME` to a temp dir for the duration of the test. Use `t.Setenv("HOME", tmpDir)` which auto-restores after the test.

## Verification

```bash
# Must compile
go build ./...

# Linter must pass
make lint

# Unit tests pass
make test

# Manual: verify EnsureSetup creates expected directory structure
# (tested automatically in unit tests, but can also verify manually)
```

## Concerns

### 1. UsageError/ConfigError migration

Moving these types from `internal/cli/root.go` to `internal/sandbox/errors.go` is a cross-package change. The risk is low — only `root.go` uses them currently, and the change is mechanical (update import path and type references). But it creates a dependency: `cli` → `sandbox` for error types. This is fine since `cli` will import `sandbox` anyway for `Manager` in Phase 4b.

### 2. CheckDirtyRepo depends on git

`CheckDirtyRepo` uses `os/exec` to run `git`. If git isn't installed on the host, the check silently returns no-dirty (the function is advisory, not blocking). This matches the plan's "git via os/exec" decision. Tests that exercise dirty repo detection need git installed in the test environment.

### 3. Mock Docker client boilerplate

The mock client for manager tests must implement all 14 methods of `docker.Client`. Most methods return `errNotImplemented`. This is verbose but straightforward — subsequent phases reuse the same mock.

### 4. EnsureSetup modifies $HOME/.yoloai/

`EnsureSetup` creates real directories under `$HOME`. Tests override `$HOME` to a temp dir via `t.Setenv`. This is standard practice but means tests must not run in parallel with other tests that depend on `$HOME` (Go's test isolation via `t.Setenv` handles this per-test).

### 5. containerd/errdefs import

`github.com/containerd/errdefs` is already an indirect dependency (pulled in by the Docker SDK). Using it directly for `IsNotFound` requires adding it to the `require` block in `go.mod`. `go mod tidy` handles this automatically.

// ABOUTME: Public re-exports of the typed errors yoloAI returns. Aliases the
// ABOUTME: yoerrors package so embedders match every error with errors.As/Is
// ABOUTME: against yoloai.* alone, never importing yoerrors directly.

package yoloai

import "github.com/kstenerud/yoloai/yoerrors"

// The library returns these typed errors from its public methods. Match them
// with errors.As (or errors.Is for sentinels). They are type aliases of the
// yoerrors package — the canonical definitions live there because internal
// packages construct them without importing the root yoloai package — so
// yoloai.UsageError and yoerrors.UsageError are the same type. Embedders need
// only the yoloai.* names.

// ExitCoder is implemented by every typed error below: ExitCode returns the
// process exit status the CLI maps the error to. Embedders can match this
// interface to recover a uniform status code regardless of the concrete type.
type ExitCoder = yoerrors.ExitCoder

// UsageError indicates bad arguments or a missing required input.
type UsageError = yoerrors.UsageError

// ConfigError indicates a configuration problem.
type ConfigError = yoerrors.ConfigError

// ActiveWorkError indicates a sandbox holds unapplied changes or a running
// agent — returned by Destroy when AbandonUnappliedWork is not set.
type ActiveWorkError = yoerrors.ActiveWorkError

// DirtyWorkdirError is returned by Create (and Run) when the workdir — or an
// aux directory — has uncommitted git changes and the caller has not acked it
// (via SandboxCreateOptions.AllowDirtyWorkdir / DirSpec.AllowDirty, or
// SandboxRunOptions.AllowDirtyWorkdir). Catch it with errors.As to render a prompt and
// retry with the ack set.
type DirtyWorkdirError = yoerrors.DirtyWorkdirError

// DirtyDir names one uncommitted directory inside a DirtyWorkdirError: its host
// Path and a short Status summary (e.g. "3 modified, 1 untracked").
type DirtyDir = yoerrors.DirtyDir

// MigrationRequiredError indicates the on-disk data directory predates the
// current build's layout and must be migrated (yoloai system migrate) first.
type MigrationRequiredError = yoerrors.MigrationRequiredError

// InconsistentDataDirError indicates the data directory is in a half-present
// state the migration gate cannot safely reconcile.
type InconsistentDataDirError = yoerrors.InconsistentDataDirError

// DependencyError indicates required software is not installed or not running.
type DependencyError = yoerrors.DependencyError

// PlatformError indicates the operation is impossible on this OS/arch.
type PlatformError = yoerrors.PlatformError

// AuthError indicates the selected agent's credentials are absent.
type AuthError = yoerrors.AuthError

// PermissionError indicates access was denied by policy.
type PermissionError = yoerrors.PermissionError

// DiskSpaceError indicates an operation failed because the host filesystem ran
// out of space; recoverable once space is freed.
type DiskSpaceError = yoerrors.DiskSpaceError

// ResourceLimitError indicates a host-side resource limit was hit (e.g. the
// macOS concurrent-VM cap); recoverable after freeing the resource.
type ResourceLimitError = yoerrors.ResourceLimitError

// SandboxLockedError indicates a write could not acquire the per-sandbox lock.
// It carries structured fields (HolderAlive, HolderPID, LockPath) so embedders
// can distinguish a live holder from a stale lock and branch accordingly.
type SandboxLockedError = yoerrors.SandboxLockedError

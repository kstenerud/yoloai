// ABOUTME: Sentinel errors and typed error aliases (UsageError, ConfigError, etc.)
// ABOUTME: re-exported from internal/yoerrors so callers import only sandbox/.
package sandbox

import (
	"errors"

	"github.com/kstenerud/yoloai/internal/sandbox/create"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/internal/yoerrors"
)

// Sentinel errors for sandbox operations.
var (
	// ErrSandboxNotFound is forwarded from store so callers that imported
	// sandbox.ErrSandboxNotFound before the store carve continue to work.
	ErrSandboxNotFound = store.ErrSandboxNotFound

	// ErrSandboxExists is the canonical sentinel for "sandbox already exists";
	// its definition lives in the create leaf so the create pipeline can produce
	// it without importing this façade. This alias preserves the public
	// sandbox.ErrSandboxExists symbol and the yoloai.ErrSandboxExists re-export.
	ErrSandboxExists = create.ErrSandboxExists

	// ErrMissingAPIKey is the canonical sentinel for "required API key not set";
	// its definition lives in the create leaf. This alias preserves the public
	// sandbox.ErrMissingAPIKey symbol.
	ErrMissingAPIKey       = create.ErrMissingAPIKey
	ErrContainerNotRunning = errors.New("container is not running")
)

// Typed error aliases. The canonical definitions live in internal/yoerrors;
// these aliases preserve the historical sandbox.XxxError call sites without
// forcing every caller to import yoerrors directly.

// UsageError is an alias for yoerrors.UsageError.
type UsageError = yoerrors.UsageError

// NewUsageError wraps a message as a UsageError.
var NewUsageError = yoerrors.NewUsageError

// ConfigError is an alias for yoerrors.ConfigError.
type ConfigError = yoerrors.ConfigError

// NewConfigError wraps a message as a ConfigError.
var NewConfigError = yoerrors.NewConfigError

// ActiveWorkError is an alias for yoerrors.ActiveWorkError.
type ActiveWorkError = yoerrors.ActiveWorkError

// NewActiveWorkError wraps a message as an ActiveWorkError.
var NewActiveWorkError = yoerrors.NewActiveWorkError

// DirtyWorkdirError is an alias for yoerrors.DirtyWorkdirError.
type DirtyWorkdirError = yoerrors.DirtyWorkdirError

// DirtyDir is an alias for yoerrors.DirtyDir.
type DirtyDir = yoerrors.DirtyDir

// DependencyError is an alias for yoerrors.DependencyError.
type DependencyError = yoerrors.DependencyError

// NewDependencyError wraps a message as a DependencyError.
var NewDependencyError = yoerrors.NewDependencyError

// PlatformError is an alias for yoerrors.PlatformError.
type PlatformError = yoerrors.PlatformError

// NewPlatformError wraps a message as a PlatformError.
var NewPlatformError = yoerrors.NewPlatformError

// AuthError is an alias for yoerrors.AuthError.
type AuthError = yoerrors.AuthError

// NewAuthError wraps a message as an AuthError.
var NewAuthError = yoerrors.NewAuthError

// PermissionError is an alias for yoerrors.PermissionError.
type PermissionError = yoerrors.PermissionError

// NewPermissionError wraps a message as a PermissionError.
var NewPermissionError = yoerrors.NewPermissionError

// SandboxLockedError is an alias for yoerrors.SandboxLockedError. Surfaced
// by AcquireLock when the per-sandbox lock can't be obtained within the
// retry window. Match with errors.As; the typed fields (HolderPID,
// HolderAlive, LockPath) drive recovery UX.
type SandboxLockedError = yoerrors.SandboxLockedError

// DiskSpaceError is an alias for yoerrors.DiskSpaceError. Surfaced
// when an operation fails because the host filesystem ran out of
// space — fatal to the current operation but recoverable via
// `yoloai system prune` (or `--images` to also drop base images) or
// freeing space on the relevant mount.
type DiskSpaceError = yoerrors.DiskSpaceError

// IsDiskSpaceError forwards to yoerrors.IsDiskSpaceError. Use to
// branch on ENOSPC without unwrapping the error chain manually.
var IsDiskSpaceError = yoerrors.IsDiskSpaceError

// AsDiskSpaceError forwards to yoerrors.AsDiskSpaceError. Use at
// call sites that have meaningful operation context to attach to
// the user-facing error message.
var AsDiskSpaceError = yoerrors.AsDiskSpaceError

// NewDiskSpaceError forwards to yoerrors.NewDiskSpaceError. Use when
// the disk-space condition is detected directly (e.g., from statfs)
// rather than caught from a syscall error.
var NewDiskSpaceError = yoerrors.NewDiskSpaceError

// ResourceLimitError is an alias for yoerrors.ResourceLimitError. Surfaced when
// a host-side resource limit is hit (e.g., the macOS concurrent-VM cap).
type ResourceLimitError = yoerrors.ResourceLimitError

// NewResourceLimitError forwards to yoerrors.NewResourceLimitError.
var NewResourceLimitError = yoerrors.NewResourceLimitError

// ExitCoder is an alias for yoerrors.ExitCoder — the interface the CLI's
// top-level handler matches to translate a typed error into a process
// exit code (F16).
type ExitCoder = yoerrors.ExitCoder

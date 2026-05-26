// ABOUTME: Sentinel errors and typed error aliases (UsageError, ConfigError, etc.)
// ABOUTME: re-exported from internal/yoerrors so callers import only sandbox/.
package sandbox

import (
	"errors"

	"github.com/kstenerud/yoloai/internal/yoerrors"
	"github.com/kstenerud/yoloai/sandbox/store"
)

// Sentinel errors for sandbox operations.
var (
	// ErrSandboxNotFound is forwarded from store so callers that imported
	// sandbox.ErrSandboxNotFound before the store carve continue to work.
	ErrSandboxNotFound     = store.ErrSandboxNotFound
	ErrSandboxExists       = errors.New("sandbox already exists")
	ErrMissingAPIKey       = errors.New("required API key not set")
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

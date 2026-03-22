package sandbox

import (
	"errors"

	"github.com/kstenerud/yoloai/config"
)

// Sentinel errors for sandbox operations.
var (
	ErrSandboxNotFound     = errors.New("sandbox not found")
	ErrSandboxExists       = errors.New("sandbox already exists")
	ErrMissingAPIKey       = errors.New("required API key not set")
	ErrContainerNotRunning = errors.New("container is not running")
	ErrNoChanges           = errors.New("no changes to apply")
)

// UsageError is an alias for config.UsageError.
type UsageError = config.UsageError

// NewUsageError wraps a message as a UsageError.
var NewUsageError = config.NewUsageError

// ConfigError is an alias for config.ConfigError.
type ConfigError = config.ConfigError

// NewConfigError wraps a message as a ConfigError.
var NewConfigError = config.NewConfigError

// ActiveWorkError is an alias for config.ActiveWorkError.
type ActiveWorkError = config.ActiveWorkError

// NewActiveWorkError wraps a message as an ActiveWorkError.
var NewActiveWorkError = config.NewActiveWorkError

// DependencyError is an alias for config.DependencyError.
type DependencyError = config.DependencyError

// NewDependencyError wraps a message as a DependencyError.
var NewDependencyError = config.NewDependencyError

// PlatformError is an alias for config.PlatformError.
type PlatformError = config.PlatformError

// NewPlatformError wraps a message as a PlatformError.
var NewPlatformError = config.NewPlatformError

// AuthError is an alias for config.AuthError.
type AuthError = config.AuthError

// NewAuthError wraps a message as an AuthError.
var NewAuthError = config.NewAuthError

// PermissionError is an alias for config.PermissionError.
type PermissionError = config.PermissionError

// NewPermissionError wraps a message as a PermissionError.
var NewPermissionError = config.NewPermissionError

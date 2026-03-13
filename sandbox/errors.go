package sandbox

import (
	"errors"

	"github.com/kstenerud/yoloai/config"
)

// Sentinel errors for sandbox operations.
var (
	ErrSandboxNotFound     = errors.New("sandbox not found")
	ErrSandboxExists       = errors.New("sandbox already exists")
	ErrDockerUnavailable   = errors.New("docker is not available")
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

package sandbox

import (
	"errors"
	"fmt"
)

// Sentinel errors for sandbox operations.
var (
	ErrSandboxNotFound     = errors.New("sandbox not found")
	ErrSandboxExists       = errors.New("sandbox already exists")
	ErrDockerUnavailable   = errors.New("docker is not available")
	ErrMissingAPIKey       = errors.New("required API key not set")
	ErrContainerNotRunning = errors.New("container is not running")
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

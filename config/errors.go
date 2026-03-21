package config

// ABOUTME: Error types for usage and configuration problems.
// ABOUTME: Used by CLI to determine exit codes.

import (
	"fmt"
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

// ActiveWorkError indicates a sandbox has unapplied changes or a running agent (exit code 4).
type ActiveWorkError struct{ Err error }

func (e *ActiveWorkError) Error() string { return e.Err.Error() }
func (e *ActiveWorkError) Unwrap() error { return e.Err }

// NewActiveWorkError wraps a message as an ActiveWorkError.
func NewActiveWorkError(format string, args ...any) *ActiveWorkError {
	return &ActiveWorkError{Err: fmt.Errorf(format, args...)}
}

// DependencyError indicates required software is not installed or not running (exit code 5).
type DependencyError struct{ Err error }

func (e *DependencyError) Error() string { return e.Err.Error() }
func (e *DependencyError) Unwrap() error { return e.Err }

// NewDependencyError wraps a message as a DependencyError.
func NewDependencyError(format string, args ...any) *DependencyError {
	return &DependencyError{Err: fmt.Errorf(format, args...)}
}

// PlatformError indicates the operation is impossible on this OS/arch (exit code 6).
type PlatformError struct{ Err error }

func (e *PlatformError) Error() string { return e.Err.Error() }
func (e *PlatformError) Unwrap() error { return e.Err }

// NewPlatformError wraps a message as a PlatformError.
func NewPlatformError(format string, args ...any) *PlatformError {
	return &PlatformError{Err: fmt.Errorf(format, args...)}
}

// AuthError indicates credentials are completely absent (exit code 7).
type AuthError struct{ Err error }

func (e *AuthError) Error() string { return e.Err.Error() }
func (e *AuthError) Unwrap() error { return e.Err }

// NewAuthError wraps a message as an AuthError.
func NewAuthError(format string, args ...any) *AuthError {
	return &AuthError{Err: fmt.Errorf(format, args...)}
}

// PermissionError indicates access is denied by policy (exit code 8).
type PermissionError struct{ Err error }

func (e *PermissionError) Error() string { return e.Err.Error() }
func (e *PermissionError) Unwrap() error { return e.Err }

// NewPermissionError wraps a message as a PermissionError.
func NewPermissionError(format string, args ...any) *PermissionError {
	return &PermissionError{Err: fmt.Errorf(format, args...)}
}

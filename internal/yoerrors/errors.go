// Package yoerrors provides typed errors used across yoloAI packages to drive
// CLI exit codes. Lives in internal/ rather than config/ because runtime
// backends (Docker, Podman, Seatbelt, Tart) need to surface these errors —
// having them in config/ inverted the dependency direction (runtime → config),
// which W7 of the architecture remediation plan corrects.
package yoerrors

// ABOUTME: Error types for usage and configuration problems.
// ABOUTME: Used by CLI to determine exit codes.

import (
	"errors"
	"fmt"
	"strings"
	"syscall"
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

// DiskSpaceError indicates an operation failed because the host
// filesystem ran out of space. Fatal to the current operation, but
// recoverable — the user can free space and retry (exit code 10).
//
// Op should describe what was being attempted (e.g. "unpack image",
// "create snapshot", "write sandbox state"), used to phrase the
// user-facing message. Err is the original underlying error so
// errors.Unwrap and errors.Is keep working.
type DiskSpaceError struct {
	Op  string
	Err error
}

func (e *DiskSpaceError) Error() string {
	return fmt.Sprintf(
		"no space left on device while %s: %v\n"+
			"Free space and retry:\n"+
			"  yoloai system disk             # show what yoloai is using\n"+
			"  yoloai system prune --cache    # reclaim backend image cache (forces base rebuild)",
		e.Op, e.Err,
	)
}

func (e *DiskSpaceError) Unwrap() error { return e.Err }

// IsDiskSpaceError reports whether err is — or wraps — a disk-space
// exhaustion. Detection layers:
//
//  1. syscall.ENOSPC via errors.Is (Linux kernel, when callers preserve
//     the sentinel — Go stdlib usually does).
//  2. String markers from higher-level libraries (containerd, docker,
//     podman, tar/snapshotters) that wrap ENOSPC without preserving
//     the sentinel through their internal error chains.
//
// The string fallback is necessary because runtime backends often
// surface ENOSPC as a free-form string with no typed wrapper.
func IsDiskSpaceError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.ENOSPC) {
		return true
	}
	s := err.Error()
	for _, marker := range []string{
		"no space left on device",
		"out of disk space",
		"ENOSPC",
	} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

// AsDiskSpaceError wraps err as *DiskSpaceError if IsDiskSpaceError
// matches; otherwise returns err unchanged (including nil). Use at
// call sites where adding `op` context to the user-facing message
// helps — for example, in image-unpack or snapshot-create paths.
func AsDiskSpaceError(op string, err error) error {
	if !IsDiskSpaceError(err) {
		return err
	}
	return &DiskSpaceError{Op: op, Err: err}
}

// NewDiskSpaceError constructs a DiskSpaceError directly. Useful when
// you've already identified the disk-space condition (e.g. statfs
// returned low free bytes) and want to short-circuit before the
// kernel would have surfaced ENOSPC.
func NewDiskSpaceError(op string, err error) *DiskSpaceError {
	return &DiskSpaceError{Op: op, Err: err}
}

// SandboxLockedError indicates a write operation couldn't acquire the
// per-sandbox file lock within the brief retry window because another
// holder is currently using it (exit code 9).
//
// HolderAlive=true means another process is genuinely using the
// sandbox — the user should wait for it to finish or cancel it.
// HolderAlive=false means the lock is stale. Staleness shouldn't
// happen with flock(2) under normal circumstances (the kernel
// auto-releases locks on process exit, including crashes); it
// requires sudden power loss, the filesystem going read-only or
// offline mid-operation, or a kernel-level process wedge. The user
// can clear it with `yoloai sandbox <name> unlock` or by removing
// the lock file directly.
//
// Unlike the other typed errors in this package, SandboxLockedError
// carries structured fields (not just a wrapped error) so embedders
// can branch on HolderAlive and the CLI can format a targeted
// recovery message. errors.As is the matching idiom.
type SandboxLockedError struct {
	Name        string // sandbox name
	HolderPID   int    // PID recorded in the lock file; 0 if unreadable
	HolderAlive bool   // true if HolderPID names a live process
	LockPath    string // host path to the lock file
}

func (e *SandboxLockedError) Error() string {
	if e.HolderAlive {
		return fmt.Sprintf(
			"sandbox %q is in use by another process (PID %d); wait for it to finish or cancel that process before retrying",
			e.Name, e.HolderPID,
		)
	}
	if e.HolderPID == 0 {
		return fmt.Sprintf(
			"sandbox %q has a stale lock (no holder PID recorded). Clear it with: yoloai sandbox %s unlock (or manually: rm %s)",
			e.Name, e.Name, e.LockPath,
		)
	}
	return fmt.Sprintf(
		"sandbox %q has a stale lock (PID %d no longer exists). Clear it with: yoloai sandbox %s unlock (or manually: rm %s)",
		e.Name, e.HolderPID, e.Name, e.LockPath,
	)
}

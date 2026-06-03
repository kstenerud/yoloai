// Package yoerrors provides the typed errors yoloAI surfaces across its
// public and internal packages to drive CLI exit codes. It is a top-level,
// dependency-light package (stdlib only, Docker errdefs style) so external
// consumers — the public yoloai surface, the CLI, and a future daemon that
// embeds the library — can match these errors without importing internal/
// or linking the engine.
package yoerrors

// ABOUTME: Error types for usage and configuration problems.
// ABOUTME: Used by CLI to determine exit codes.

import (
	"errors"
	"fmt"
	"strings"
	"syscall"
)

// Exit codes for the typed errors in this package. The CLI's top-level
// error handler translates a typed error into the matching process
// exit status via the ExitCoder interface. Keeping the code on the
// error type (ExitCode methods below) means the "which code" decision
// lives in one file alongside the type taxonomy, not split across a
// cascade in cli/root.go (F16). Exit code 1 is the unmapped/generic
// failure; 0 is success. Codes are contiguous from 2.
const (
	ExitUsage         = 2
	ExitConfig        = 3
	ExitActiveWork    = 4
	ExitDependency    = 5
	ExitPlatform      = 6
	ExitAuth          = 7
	ExitPermission    = 8
	ExitSandboxLocked = 9
	ExitDiskSpace     = 10
	ExitResourceLimit = 11
	ExitDirtyWorkdir  = 12

	ExitMigrationRequired   = 13
	ExitInconsistentDataDir = 14
)

// ExitCoder is implemented by typed errors that map to a specific
// process exit code. The CLI's top-level handler matches it with
// errors.AsType[ExitCoder] and returns ExitCode(); the embedded error
// constraint lets AsType walk the wrap chain. Adding a new typed error
// with an ExitCode method automatically participates — no edit to the
// CLI's exit-code logic required (F16).
type ExitCoder interface {
	error
	ExitCode() int
}

// UsageError indicates bad arguments or missing required args (exit code 2).
type UsageError struct {
	Err error
}

func (e *UsageError) Error() string { return e.Err.Error() }
func (e *UsageError) Unwrap() error { return e.Err }
func (e *UsageError) ExitCode() int { return ExitUsage }

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
func (e *ConfigError) ExitCode() int { return ExitConfig }

// NewConfigError wraps a message as a ConfigError.
func NewConfigError(format string, args ...any) *ConfigError {
	return &ConfigError{Err: fmt.Errorf(format, args...)}
}

// ActiveWorkError indicates a sandbox has unapplied changes or a running agent (exit code 4).
type ActiveWorkError struct{ Err error }

func (e *ActiveWorkError) Error() string { return e.Err.Error() }
func (e *ActiveWorkError) Unwrap() error { return e.Err }
func (e *ActiveWorkError) ExitCode() int { return ExitActiveWork }

// NewActiveWorkError wraps a message as an ActiveWorkError.
func NewActiveWorkError(format string, args ...any) *ActiveWorkError {
	return &ActiveWorkError{Err: fmt.Errorf(format, args...)}
}

// DirtyDir names a host directory with uncommitted git changes and a short
// human summary of its status (e.g. "3 modified, 1 untracked").
type DirtyDir struct {
	Path   string
	Status string
}

// DirtyWorkdirError indicates one or more host directories slated for the
// sandbox have uncommitted git changes (exit code 12). Create refuses by
// default rather than prompting: the agent would see — and could modify or
// lose — that uncommitted work, and on :copy a later apply conflicts with the
// still-dirty host. The caller consciously overrides per-directory via
// DirSpec.AllowDirty (or SandboxCreateOptions.AllowDirtyWorkdir for the workdir).
type DirtyWorkdirError struct {
	Dirs []DirtyDir
}

func (e *DirtyWorkdirError) Error() string {
	var b strings.Builder
	b.WriteString("uncommitted changes in:")
	for _, d := range e.Dirs {
		fmt.Fprintf(&b, "\n  %s (%s)", d.Path, d.Status)
	}
	return b.String()
}
func (e *DirtyWorkdirError) ExitCode() int { return ExitDirtyWorkdir }

// MigrationRequiredError indicates the on-disk data directory predates the
// current build's layout and must be migrated before yoloai can run (exit
// code 13). The binary fails fast rather than migrating silently; the user
// brings the directory current with an explicit command. Namespace names the
// realm that triggered the verdict ("cli", "library", or "" for a whole-dir
// v0 install) purely for the diagnostic; the recovery is the same regardless.
type MigrationRequiredError struct {
	Namespace string
}

func (e *MigrationRequiredError) Error() string {
	if e.Namespace != "" {
		return fmt.Sprintf("%s data directory is out of date; run 'yoloai system migrate'", e.Namespace)
	}
	return "data directory is out of date; run 'yoloai system migrate'"
}
func (e *MigrationRequiredError) ExitCode() int { return ExitMigrationRequired }

// NewMigrationRequiredError reports that the given realm (or the whole data
// directory, when namespace is "") needs migration.
func NewMigrationRequiredError(namespace string) *MigrationRequiredError {
	return &MigrationRequiredError{Namespace: namespace}
}

// InconsistentDataDirError indicates the data directory is in a state the
// gate cannot reconcile: some realms look fresh while others are already
// populated (exit code 14). This should not happen in normal use — a realm
// went missing from an otherwise-present install — so the message is loud
// and deliberately does NOT point at 'system migrate', which cannot safely
// sort out a half-present directory.
type InconsistentDataDirError struct {
	Err error
}

func (e *InconsistentDataDirError) Error() string { return e.Err.Error() }
func (e *InconsistentDataDirError) Unwrap() error { return e.Err }
func (e *InconsistentDataDirError) ExitCode() int { return ExitInconsistentDataDir }

// NewInconsistentDataDirError wraps a message as an InconsistentDataDirError.
func NewInconsistentDataDirError(format string, args ...any) *InconsistentDataDirError {
	return &InconsistentDataDirError{Err: fmt.Errorf(format, args...)}
}

// DependencyError indicates required software is not installed or not running (exit code 5).
type DependencyError struct{ Err error }

func (e *DependencyError) Error() string { return e.Err.Error() }
func (e *DependencyError) Unwrap() error { return e.Err }
func (e *DependencyError) ExitCode() int { return ExitDependency }

// NewDependencyError wraps a message as a DependencyError.
func NewDependencyError(format string, args ...any) *DependencyError {
	return &DependencyError{Err: fmt.Errorf(format, args...)}
}

// PlatformError indicates the operation is impossible on this OS/arch (exit code 6).
type PlatformError struct{ Err error }

func (e *PlatformError) Error() string { return e.Err.Error() }
func (e *PlatformError) Unwrap() error { return e.Err }
func (e *PlatformError) ExitCode() int { return ExitPlatform }

// NewPlatformError wraps a message as a PlatformError.
func NewPlatformError(format string, args ...any) *PlatformError {
	return &PlatformError{Err: fmt.Errorf(format, args...)}
}

// AuthError indicates credentials are completely absent (exit code 7).
type AuthError struct{ Err error }

func (e *AuthError) Error() string { return e.Err.Error() }
func (e *AuthError) Unwrap() error { return e.Err }
func (e *AuthError) ExitCode() int { return ExitAuth }

// NewAuthError wraps a message as an AuthError.
func NewAuthError(format string, args ...any) *AuthError {
	return &AuthError{Err: fmt.Errorf(format, args...)}
}

// PermissionError indicates access is denied by policy (exit code 8).
type PermissionError struct{ Err error }

func (e *PermissionError) Error() string { return e.Err.Error() }
func (e *PermissionError) Unwrap() error { return e.Err }
func (e *PermissionError) ExitCode() int { return ExitPermission }

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
			"  yoloai system prune            # reclaim cache, no rebuild\n"+
			"  yoloai system prune --images   # also remove base images (forces rebuild)",
		e.Op, e.Err,
	)
}

func (e *DiskSpaceError) Unwrap() error { return e.Err }
func (e *DiskSpaceError) ExitCode() int { return ExitDiskSpace }

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

// ResourceLimitError indicates a host-side resource limit was hit (exit code 11).
// Currently fired when the macOS concurrent-VM cap (enforced by Apple's
// Virtualization.framework) is exceeded. Recoverable: stop a running VM and retry.
type ResourceLimitError struct{ Err error }

func (e *ResourceLimitError) Error() string { return e.Err.Error() }
func (e *ResourceLimitError) Unwrap() error { return e.Err }
func (e *ResourceLimitError) ExitCode() int { return ExitResourceLimit }

// NewResourceLimitError wraps a message as a ResourceLimitError.
func NewResourceLimitError(format string, args ...any) *ResourceLimitError {
	return &ResourceLimitError{Err: fmt.Errorf(format, args...)}
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

func (e *SandboxLockedError) ExitCode() int { return ExitSandboxLocked }

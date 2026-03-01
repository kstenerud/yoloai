// Package runtime defines the pluggable Runtime interface for sandbox backends.
// ABOUTME: Runtime-agnostic types decouple sandbox logic from Docker SDK.
package runtime //nolint:revive // name chosen for clarity; stdlib runtime is not needed alongside this package

import (
	"context"
	"errors"
	"io"
	"log/slog"
)

// Sentinel errors used across all runtime implementations.
var (
	ErrNotFound   = errors.New("instance not found")
	ErrNotRunning = errors.New("instance not running")
)

// PruneItem describes a single orphaned resource found during pruning.
type PruneItem struct {
	Kind string // "container", "vm"
	Name string // instance name
}

// PruneResult summarizes orphaned resources found by a backend.
type PruneResult struct {
	Items []PruneItem
}

// MountSpec describes a bind mount from host into the sandbox instance.
type MountSpec struct {
	Source   string
	Target   string
	ReadOnly bool
}

// PortMapping describes a port forwarding from host to sandbox instance.
type PortMapping struct {
	HostPort     string
	InstancePort string
	Protocol     string // default "tcp"
}

// InstanceConfig holds the parameters for creating a sandbox instance.
type InstanceConfig struct {
	Name        string
	ImageRef    string // image tag (Docker) or base VM name (Tart)
	WorkingDir  string
	Mounts      []MountSpec
	Ports       []PortMapping
	NetworkMode string // "" = default, "none" = no network, "isolated" = allowlist only
	CapAdd      []string
	UseInit     bool
}

// InstanceInfo holds the inspected state of a sandbox instance.
type InstanceInfo struct {
	Running bool
}

// ExecResult holds the output of a non-interactive command execution.
type ExecResult struct {
	Stdout   string
	ExitCode int
}

// Runtime is the sandbox backend interface. Implementations manage the
// lifecycle of sandbox instances (containers, VMs, etc.) and provide
// image/environment management.
type Runtime interface {
	// EnsureImage ensures the base image is ready, seeding resources and
	// building/pulling as needed. Writes progress to output.
	EnsureImage(ctx context.Context, sourceDir string, output io.Writer, logger *slog.Logger, force bool) error

	// ImageExists checks whether the given image reference exists locally.
	ImageExists(ctx context.Context, imageRef string) (bool, error)

	// Create creates a new sandbox instance from the given config.
	Create(ctx context.Context, cfg InstanceConfig) error

	// Start starts a previously created (or stopped) instance.
	Start(ctx context.Context, name string) error

	// Stop stops a running instance. Returns nil if already stopped.
	Stop(ctx context.Context, name string) error

	// Remove removes an instance. Returns nil if already removed.
	Remove(ctx context.Context, name string) error

	// Inspect returns the current state of an instance.
	// Returns ErrNotFound if the instance does not exist.
	Inspect(ctx context.Context, name string) (InstanceInfo, error)

	// Exec runs a command inside a running instance and returns the result.
	Exec(ctx context.Context, name string, cmd []string, user string) (ExecResult, error)

	// InteractiveExec runs a command interactively (with TTY) inside an instance.
	// Stdin/stdout/stderr are connected to the current terminal.
	// If workDir is non-empty, the command runs in that directory.
	InteractiveExec(ctx context.Context, name string, cmd []string, user string, workDir string) error

	// Prune removes orphaned backend resources. knownInstances lists instance
	// names that have valid sandbox directories; anything else named yoloai-*
	// is considered orphaned. When dryRun is true, reports without removing.
	Prune(ctx context.Context, knownInstances []string, dryRun bool, output io.Writer) (PruneResult, error)

	// Close releases any resources held by the runtime.
	Close() error

	// DiagHint returns a backend-specific hint for how to check logs when
	// an instance fails to start or crashes. The hint is included in error
	// messages shown to the user.
	DiagHint(instanceName string) string
}

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
	Kind string // "container", "vm", "image"
	Name string // instance name or short image ID
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

// ResourceLimits holds converted resource constraints for the runtime backend.
type ResourceLimits struct {
	NanoCPUs int64 // CPU limit in Docker NanoCPUs (cpus * 1e9)
	Memory   int64 // Memory limit in bytes
}

// InstanceConfig holds the parameters for creating a sandbox instance.
type InstanceConfig struct {
	Name             string
	ImageRef         string // image tag (Docker) or base VM name (Tart)
	WorkingDir       string
	Mounts           []MountSpec
	Ports            []PortMapping
	NetworkMode      string // "" = default, "none" = no network, "isolated" = allowlist only
	CapAdd           []string
	Devices          []string
	UseInit          bool
	UsernsMode       string // "" = default, "keep-id" = rootless Podman
	Resources        *ResourceLimits
	ContainerRuntime string // OCI runtime name (shimv2 type for containerd, runtime name for Docker/Podman)
	Snapshotter      string // containerd snapshotter name; "" = backend default (overlayfs)
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

// BackendCaps declares what features a runtime backend supports.
// Each backend returns its own capabilities via Capabilities().
type BackendCaps struct {
	NetworkIsolation    bool // supports --network=isolated (iptables domain filtering)
	OverlayDirs         bool // supports :overlay mount mode (overlayfs inside the container)
	CapAdd              bool // supports cap_add, devices, and setup commands
	NeedsHomeSeedConfig bool // entrypoint remaps yoloai's npm install method; false for seatbelt (runs host native agent)
	RewritesCopyWorkdir bool // :copy workdir mount path must be rewritten to the sandbox copy location; true for seatbelt
}

// IsolationValidator is an optional interface implemented by Runtime backends
// that support prerequisite checking for isolation modes. create.go delegates
// to this interface via checkIsolationPrerequisites; backends that don't
// implement it skip validation silently.
type IsolationValidator interface {
	ValidateIsolation(ctx context.Context, isolation string) error
}

// UsernsProvider is an optional interface implemented by backends that need
// a non-default user namespace mode. Podman rootless uses "keep-id" to map
// the container uid to the host user; this also determines the tmux exec user
// (keep-id containers run as the host user, not as "yoloai").
type UsernsProvider interface {
	// UsernsMode returns the user namespace mode for a new container.
	// hasSysAdmin is true when the container will receive CAP_SYS_ADMIN
	// (overlay mounts or recipe cap_add), which requires real root in the
	// container and therefore cannot use keep-id.
	// Returns "" for the default mode.
	UsernsMode(hasSysAdmin bool) string
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

	// Logs returns the last n lines of an instance's log output.
	// Returns empty string if logs are unavailable (e.g. VM backends).
	// Used to capture crash output before container removal.
	Logs(ctx context.Context, name string, tail int) string

	// DiagHint returns a backend-specific hint for how to check logs when
	// an instance fails to start or crashes. The hint is included in error
	// messages shown to the user.
	DiagHint(instanceName string) string

	// Capabilities returns the feature set supported by this backend.
	// Used by sandbox/create.go to gate features and select backend-specific
	// code paths without string-comparing backend names.
	Capabilities() BackendCaps

	// Name returns the backend name (e.g., "docker", "tart", "seatbelt").
	Name() string

	// PreferredTmuxSocket returns the fixed tmux socket path this backend
	// uses, or empty string if the backend uses the uid-based default socket.
	// The value is written into runtime-config.json at sandbox creation time
	// so all exec'd processes (including non-interactive execs) find the same
	// tmux server as the container init process.
	PreferredTmuxSocket() string

	// AttachCommand returns the command to exec interactively to attach to
	// the tmux session in a running instance. tmuxSocket is the fixed socket
	// path from runtime-config.json (empty = use default). rows and cols are
	// the current terminal dimensions (0 = unknown). isolation is the sandbox
	// isolation mode (e.g. "container-enhanced").
	AttachCommand(tmuxSocket string, rows, cols int, isolation string) []string
}

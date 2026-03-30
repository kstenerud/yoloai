// Package runtime defines the pluggable Runtime interface for sandbox backends.
// ABOUTME: Runtime-agnostic types decouple sandbox logic from Docker SDK.
package runtime //nolint:revive // name chosen for clarity; stdlib runtime is not needed alongside this package

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"github.com/kstenerud/yoloai/runtime/caps"
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
	// Universal — all backends.
	Name        string
	WorkingDir  string
	Mounts      []MountSpec
	Ports       []PortMapping
	NetworkMode string // "" = default, "none" = no network, "isolated" = allowlist only
	Resources   *ResourceLimits

	// Container/VM backends (Docker, Podman, containerd, Tart).
	// Ignored by process-based and remote backends.
	ImageRef   string // image tag (Docker) or base VM name (Tart)
	CapAdd     []string
	Devices    []string
	UseInit    bool
	UsernsMode string // "" = default, "keep-id" = rootless Podman

	// containerd-specific. Ignored by all other backends.
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
	NetworkIsolation bool // supports --network=isolated (iptables domain filtering)
	OverlayDirs      bool // supports :overlay mount mode (overlayfs inside the container)
	CapAdd           bool // supports cap_add, devices, and setup commands
	HostFilesystem   bool // true when sandbox state lives on the host (seatbelt, future SSH)
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
	// Setup prepares the backend for launching agents (builds/pulls images,
	// checks prerequisites). sourceDir is the profile directory containing
	// build instructions (Dockerfile etc.); ignored by backends that don't
	// build images. force=true rebuilds even if already ready.
	Setup(ctx context.Context, sourceDir string, output io.Writer, logger *slog.Logger, force bool) error

	// IsReady returns true if the backend is ready to launch agents (image
	// built, prerequisites present, etc.). Each backend determines readiness
	// by its own internal criteria — callers do not pass an image reference.
	IsReady(ctx context.Context) (bool, error)

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

	// GitExec runs a git command for the given instance. The workDir parameter
	// should be a host path (e.g., ~/.yoloai/sandboxes/<name>/work/<encoded>)
	// as provided by the sandbox package helpers (copyGitWorkDir, WorkDir, etc.).
	//
	// Backends are responsible for translating paths to their execution context:
	// - Docker/Podman/Seatbelt/Containerd: git runs on host, use workDir as-is
	// - Tart: git runs inside VM, translates host paths to VM paths automatically
	//
	// Returns stdout on success. This abstraction allows callers to use host paths
	// uniformly while backends handle their specific execution environments.
	GitExec(ctx context.Context, name, workDir string, args ...string) (string, error)

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

	// AgentProvisionedByBackend reports whether the agent binary is provisioned
	// as part of the backend's image/VM build. Returns true for container/VM
	// backends (Docker, containerd, Tart) where the agent is npm-installed in
	// the image; returns false for process-based backends (e.g. seatbelt) that
	// run the host's native agent installation.
	AgentProvisionedByBackend() bool

	// ResolveCopyMount returns the mount path the agent sees for a :copy directory.
	// For container/VM backends, the copy is bind-mounted at the original host path
	// inside the container, so this returns hostPath unchanged.
	// For process-based backends (seatbelt), the agent runs directly on the host
	// and sees the copy at its sandbox location, so this returns the rewritten path.
	ResolveCopyMount(sandboxName, hostPath string) string

	// Name returns the backend name (e.g., "docker", "tart", "seatbelt").
	Name() string

	// TmuxSocket returns the tmux socket path for a sandbox, or empty string
	// if the backend uses the uid-based default socket. sandboxDir is the
	// resolved sandbox directory path. The value is written into
	// runtime-config.json at sandbox creation time so all exec'd processes
	// (including non-interactive execs) find the same tmux server as the
	// container init process.
	TmuxSocket(sandboxDir string) string

	// AttachCommand returns the command to exec interactively to attach to
	// the tmux session in a running instance. tmuxSocket is the fixed socket
	// path from runtime-config.json (empty = use default). rows and cols are
	// the current terminal dimensions (0 = unknown). isolation is the sandbox
	// isolation mode (e.g. "container-enhanced").
	AttachCommand(tmuxSocket string, rows, cols int, isolation string) []string

	// RequiredCapabilities returns the host capabilities needed for the given
	// isolation mode. Returns nil if the backend has no special requirements
	// for this mode.
	RequiredCapabilities(isolation string) []caps.HostCapability

	// SupportedIsolationModes returns the isolation modes this backend can
	// potentially support. Returns nil if the backend has no isolation modes.
	// Used by `system doctor` to discover what to check without the caller
	// enumerating modes externally.
	SupportedIsolationModes() []string

	// BaseModeName returns the human label for this backend's default (no-isolation)
	// mode, shown in `system doctor` output. e.g. "container", "vm", "process".
	BaseModeName() string
}

// WorkDirSetup is implemented by backends that store work directories
// locally inside the VM/container rather than on the host filesystem.
type WorkDirSetup interface {
	// SetupWorkDirInVM returns shell commands to copy from VirtioFS staging
	// to local VM storage and create git baseline. Called during Create/Reset.
	SetupWorkDirInVM(virtiofsStagingPath, vmLocalPath string) []string
}

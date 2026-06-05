// Package runtime defines the pluggable Runtime interface for sandbox backends.
// ABOUTME: Runtime-agnostic types decouple sandbox logic from Docker SDK.
package runtime //nolint:revive // name chosen for clarity; stdlib runtime is not needed alongside this package

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"strings"
	"syscall"
	"time"

	"github.com/kstenerud/yoloai/internal/config"
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
// HostPath is the path on the host filesystem; ContainerPath is the
// path inside the sandbox; ReadOnly controls write access.
//
// The "Path" suffix is deliberate. Go doesn't surface types at the
// call site — `for _, m := range mounts { ... m.HostPath ... }` leaves the
// reader guessing whether m.HostPath is a hostname, an IP, a port encoded
// as int, or (here) a path. `m.HostPath` is self-documenting. Same
// shape as PortMapping's HostPort/ContainerPort: the {Host,Container}
// prefix names the direction, the type-carrying suffix names the kind.
type MountSpec struct {
	HostPath      string
	ContainerPath string
	ReadOnly      bool
}

// PortMapping describes a port forwarding from host to sandbox instance.
// HostPort is the port number on the host; ContainerPort is the port
// number inside the sandbox. The `Port` suffix is deliberate — without
// it, an `int` field named "Host" reads ambiguously (is it a hostname?
// an address?), whereas "HostPort" is self-documenting. Naming aligns
// with MountSpec's Host/Container direction pair (embedders learn one
// direction convention) but keeps the type-carrying suffix.
//
// int ports replace the prior string fields so embedders never
// hand-format/parse "8080:80" tokens (the CLI parses these at the
// flag boundary and constructs PortMapping with typed ints). Q-Y.
type PortMapping struct {
	HostPort      int
	ContainerPort int
	Protocol      string // default "tcp"
}

// ResourceLimits holds converted resource constraints for the runtime backend.
type ResourceLimits struct {
	NanoCPUs int64 // CPU limit in Docker NanoCPUs (cpus * 1e9)
	Memory   int64 // Memory limit in bytes
}

// Instance label keys. Backends that support labels stamp these so an
// embedder can attribute and enumerate instances by owning principal and
// sandbox name. See D62.
const (
	LabelPrincipal = "com.yoloai.principal"
	LabelSandbox   = "com.yoloai.sandbox"
)

// InstanceConfig holds the parameters for creating a sandbox instance.
type InstanceConfig struct {
	// Universal — all backends.
	Name        string
	WorkingDir  string
	Mounts      []MountSpec
	Ports       []PortMapping
	NetworkMode string // "" = default, "none" = no network, "isolated" = allowlist only
	Resources   *ResourceLimits

	// Labels are key/value metadata attached to the instance (e.g.
	// com.yoloai.principal / com.yoloai.sandbox). Backends with native
	// label support (Docker, containerd) apply them directly; backends
	// that persist their config as JSON (Tart, Seatbelt) carry them in
	// that record so an embedder can enumerate instances by owner.
	Labels map[string]string

	// Container/VM backends (Docker, Podman, containerd, Tart).
	// Ignored by process-based and remote backends.
	ImageRef     string // image tag (Docker) or base VM name (Tart)
	CapAdd       []string
	Devices      []string
	UseInit      bool
	UsernsMode   string   // "" = default, "keep-id" = rootless Podman
	Privileged   bool     // run container with all capabilities, seccomp=unconfined, AppArmor=unconfined
	Seccomp      string   // seccomp profile: "" = default, "unconfined" = disable seccomp filter
	CgroupnsMode string   // cgroup namespace: "" = default (private), "host" = share host cgroup namespace
	ContainerEnv []string // environment variables set directly on the container (not via secrets)

	// containerd-specific. Ignored by all other backends.
	ContainerRuntime string // OCI runtime name (shimv2 type for containerd, runtime name for Docker/Podman)
	Snapshotter      string // containerd snapshotter name; "" = backend default (overlayfs)
}

// InstanceInfo holds the inspected state of a sandbox instance.
type InstanceInfo struct {
	Running   bool
	Suspended bool // true if the instance is suspended (state saved to disk, not consuming CPU/RAM)
}

// ExecResult holds the output of a non-interactive command execution.
type ExecResult struct {
	Stdout   string
	ExitCode int
}

// BackendDescriptor bundles the static facts each backend declares.
// Returned by Runtime.Descriptor(). Values are compile-time constants per
// backend (verified by the W11 spike, docs/contributors/design/research/runtime-interface-spike.md).
type BackendDescriptor struct {
	Type                      BackendType     // BackendDocker, BackendPodman, BackendTart, BackendSeatbelt, BackendContainerd
	Description               string          // one-line user-facing summary ("Linux containers; portable …")
	Platforms                 []string        // host OSes this backend can run on; GOOS-style ("linux", "darwin", "windows")
	Architectures             []string        // host architectures this backend supports; GOARCH-style ("amd64", "arm64"). nil/empty = any arch.
	IsolationTargetOnly       bool            // true when the backend is reached only via isolation routing (e.g. --isolation vm), never selected directly as a user default; setup/default pickers should skip it.
	Requires                  string          // human-readable prerequisites ("Docker Engine installed and running")
	InstallHint               string          // install URL or shell command; "" when no install is needed
	BaseModeName              IsolationMode   // typed label for the backend's default (no-isolation) mode
	AgentProvisionedByBackend bool            // true when the backend's image/VM ships the agent binary
	AgentInstallMethod        string          // how the backend installs Claude Code ("npm-global" or "native"); patched into seeded .claude.json so it matches reality. "" when AgentProvisionedByBackend is false.
	SupportedIsolationModes   []IsolationMode // non-default isolation modes this backend can support
	Capabilities              BackendCaps     // feature-set flags

	// SecretsConsumedTimeout caps how long the host waits for the in-sandbox
	// "secrets consumed" marker before removing the ephemeral secrets dir
	// (see internal/sandbox/create_instance.go). Zero means "use the package
	// default" (a few tens of seconds, fine for fast-booting container
	// backends). Slow-booting backends — where the guest can take longer than
	// the default just to reach the entrypoint that reads the secrets — set
	// this higher so the host does not remove the dir before the guest has
	// read it. Without this, the read-before-removal invariant the marker
	// exists to guarantee is violated on slow boots. Tart sets it because a
	// macOS VM boot (plus a possible one-time `xcodebuild -runFirstLaunch`)
	// routinely exceeds the default. See backend-idiosyncrasies.md.
	SecretsConsumedTimeout time.Duration
	// Probe reports whether this backend is usable right now and, on failure,
	// a short user-facing reason ("docker socket not reachable", "tart binary
	// not found", …). Implementations must be fast and side-effect-free — they
	// run on `yoloai info`, setup wizards, and detect-backend dispatch, so
	// stat the socket / LookPath the binary; do not dial. nil is permitted but
	// every shipped backend supplies a real probe.
	//
	// env is the caller's threaded host-env snapshot (the same map fed to the
	// factory). Backends that locate their daemon socket via env vars
	// (DOCKER_HOST, CONTAINER_HOST, XDG_RUNTIME_DIR) read them from this map
	// rather than os.Getenv, so probing stays principal-scoped (§12). Backends
	// that probe by stat/LookPath ignore it.
	Probe func(ctx context.Context, env map[string]string) (available bool, reason string)

	// CleanupHint returns a user-facing command that removes the named image
	// from this backend's local store (e.g. "docker rmi yoloai-myprofile").
	// Returns "" for backends that don't manage container images (tart,
	// seatbelt). The returned string is shown verbatim to the user in
	// post-delete hints; it must be a single shell command, no formatting.
	CleanupHint func(image string) string

	// HostFromContainer is the hostname inside the sandbox that resolves to
	// the host's network stack — "host.docker.internal" for docker and
	// podman, "" for backends without a special hostname (callers substitute
	// generic phrasing like "the host's routable IP").
	HostFromContainer string

	// VersionString reports the backend's CLI / daemon version for
	// diagnostic output (yoloai info, bug reports). Returns "built-in" for
	// backends that ship as part of the OS (seatbelt) and "" when the
	// version cannot be determined. May fork+exec the backend's CLI; called
	// from human-paced commands, not hot paths. nil is treated as "" by
	// callers — every shipped backend supplies a real implementation.
	VersionString func(ctx context.Context) string
}

// BackendCaps declares what features a runtime backend supports.
// Each backend returns its capabilities via BackendDescriptor.Capabilities.
type BackendCaps struct {
	NetworkIsolation bool   // supports --network=isolated (iptables domain filtering)
	OverlayDirs      bool   // supports :overlay mount mode (overlayfs inside the container)
	CapAdd           bool   // supports cap_add, devices, and setup commands
	HostFilesystem   bool   // true when sandbox state lives on the host (seatbelt, future SSH)
	ContainerAttach  bool   // exposes a docker-compatible container surface so VS Code's "Attach to Running Container" works
	VMRuntimeDir     string // path to yoloai state inside the VM; "" means /yoloai (docker default)
}

// Runtime is the sandbox backend interface. Implementations manage the
// lifecycle of sandbox instances (containers, VMs, etc.) and provide
// image/environment management.
type Runtime interface {
	// Setup prepares the backend for launching agents (builds/pulls images,
	// checks prerequisites). sourceDir is the profile directory containing
	// build instructions (Dockerfile etc.); ignored by backends that don't
	// build images. force=true rebuilds even if already ready.
	//
	// layout is the active config.Layout — backends use it to derive
	// host paths (e.g. base-image build lock locations). Q-W.5 threads
	// it through the interface so backends never read ambient HOME.
	Setup(ctx context.Context, layout config.Layout, sourceDir string, output io.Writer, logger *slog.Logger, force bool) error

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

	// InteractiveExec runs a command interactively inside an instance,
	// wiring the supplied IOStreams to the remote stdio. The caller
	// chooses whether to allocate a PTY (io.TTY) and at what size
	// (io.Rows / io.Cols). If workDir is non-empty, the command runs in
	// that directory. A clean non-zero exit of the inner command is
	// reported as an *ExecError carrying the code (use InteractiveExitError
	// to normalize a shelled-out backend's exec.Cmd result to this contract).
	InteractiveExec(ctx context.Context, name string, cmd []string, user string, workDir string, io IOStreams) error

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

	// Descriptor returns a BackendDescriptor with static facts about this backend.
	// Values are compile-time constants and do not change after construction.
	Descriptor() BackendDescriptor

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
	// isolation mode (e.g. IsolationModeContainerEnhanced).
	AttachCommand(tmuxSocket string, rows, cols int, isolation IsolationMode) []string
}

// IsPermissionDenied reports whether err represents a "permission denied"
// failure, checking both typed (fs.ErrPermission / syscall.EACCES) and text-
// match paths.
//
// The text-match fallback exists because the Docker and containerd SDK errors
// — which surface from gRPC transports and HTTP clients — do NOT always wrap
// the underlying syscall error in a form that errors.Is can detect. The
// strings the SDKs emit are protocol-level identifiers, not localized
// human-facing messages, so the text match is robust in practice. W8 of the
// architecture remediation plan documented this as irreducible at a
// chokepoint.
func IsPermissionDenied(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.EACCES) {
		return true
	}
	return strings.Contains(err.Error(), "permission denied")
}

// IsAddressInUse reports whether err represents an EADDRINUSE failure,
// checking both typed (syscall.EADDRINUSE) and text-match paths.
//
// As with IsPermissionDenied, the text fallback exists because containerd's
// shim errors come through TTRPC and don't reliably unwrap to the syscall
// error. The "address in use" / "address already in use" strings are
// protocol-stable identifiers, not localized messages.
func IsAddressInUse(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "address in use") || strings.Contains(msg, "address already in use")
}

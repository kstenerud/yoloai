// Package runtime defines the pluggable Runtime interface for sandbox backends.
// ABOUTME: Runtime-agnostic types decouple sandbox logic from Docker SDK.
package runtime //nolint:revive // name chosen for clarity; stdlib runtime is not needed alongside this package

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime/caps"
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
	Name                      BackendName     // BackendDocker, BackendPodman, BackendTart, BackendSeatbelt, BackendContainerd
	Description               string          // one-line user-facing summary ("Linux containers; portable …")
	Platforms                 []string        // host OSes this backend can run on; GOOS-style ("linux", "darwin", "windows")
	Architectures             []string        // host architectures this backend supports; GOARCH-style ("amd64", "arm64"). nil/empty = any arch.
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
	Probe func(ctx context.Context) (available bool, reason string)

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

// CopyMountResolver is an optional interface implemented by backends that
// rewrite :copy mount paths from host paths to sandbox-local paths. Backends
// that don't implement it (the default) see :copy mounts at the original
// host path inside the container.
type CopyMountResolver interface {
	ResolveCopyMount(sandboxName, hostPath string) string
}

// AppleSimulatorRuntimes is an optional interface implemented by backends
// that manage Apple simulator (iOS/tvOS/watchOS/visionOS) runtime base
// images. Currently only Tart implements it, but the interface lets the
// orchestration layer react to the capability without importing the
// concrete backend package.
//
// runtimeSpecs are user-facing strings like "ios", "ios:26.4", "tvos:latest".
// The returned imageRef is the base-image name the caller should use when
// creating the sandbox. Errors from this method are user-shaped (UsageError
// when the requested base doesn't exist locally and must be created first).
//
// layout is the active config.Layout — implementations use it to derive
// host paths (e.g. base-image build lock locations). Q-W.5 threads it
// through so backends never read ambient HOME.
type AppleSimulatorRuntimes interface {
	PrepareRuntimeBase(ctx context.Context, layout config.Layout, runtimeSpecs []string) (imageRef string, err error)
}

// ResolveCopyMountFor returns the in-sandbox path for a :copy directory.
// Falls back to hostPath when the backend doesn't implement CopyMountResolver.
func ResolveCopyMountFor(rt Runtime, sandboxName, hostPath string) string {
	if p, ok := rt.(CopyMountResolver); ok {
		return p.ResolveCopyMount(sandboxName, hostPath)
	}
	return hostPath
}

// IsolationCapabilityProvider is an optional interface implemented by
// backends that need specific host capabilities (binaries present, kernel
// features, etc.) for non-default isolation modes. Backends that don't
// implement it have no isolation-mode prerequisites.
type IsolationCapabilityProvider interface {
	RequiredCapabilities(isolation IsolationMode) []caps.HostCapability
}

// RequiredCapabilitiesFor returns the host capabilities needed for the given
// isolation mode, or nil when the backend has no requirements for the mode.
func RequiredCapabilitiesFor(rt Runtime, isolation IsolationMode) []caps.HostCapability {
	if p, ok := rt.(IsolationCapabilityProvider); ok {
		return p.RequiredCapabilities(isolation)
	}
	return nil
}

// VMSlot describes one VM currently occupying a host VM slot. Owned is true
// when a live owner process (e.g. `tart run <name>`) still backs the VM;
// an un-owned slot is an orphan whose launcher died, leaving the underlying
// hypervisor process holding the slot. Deleted is true when the VM's disk
// image has been removed out from under the still-running process (the
// signature of a crashed temp VM).
type VMSlot struct {
	PID     int
	VMName  string // resolved VM name; "" when it could not be determined
	Owned   bool
	Deleted bool
}

// VMCensus is a point-in-time accounting of host VM slots against the
// platform's concurrent-VM limit. Backends whose hypervisor caps the number
// of simultaneous VMs (e.g. tart on macOS) report it so doctor can explain
// why a new sandbox can't start.
type VMCensus struct {
	Limit int      // max concurrent VMs the platform allows (e.g. 2 for macOS)
	Slots []VMSlot // one per VM process currently occupying a slot
}

// InUse returns how many slots are currently occupied.
func (c VMCensus) InUse() int { return len(c.Slots) }

// Blocked reports whether the limit is reached — i.e. a new VM cannot start
// until an existing one frees its slot.
func (c VMCensus) Blocked() bool { return c.Limit > 0 && len(c.Slots) >= c.Limit }

// Orphans returns the un-owned slots — leaked VM processes whose launcher
// died. These are the ones a user can reclaim by killing the PID.
func (c VMCensus) Orphans() []VMSlot {
	var out []VMSlot
	for _, s := range c.Slots {
		if !s.Owned {
			out = append(out, s)
		}
	}
	return out
}

// VMCensusReporter is an optional interface implemented by backends that run
// VMs under a host concurrency limit. Implementations report the current slot
// census so callers can detect (and explain) a reached limit.
type VMCensusReporter interface {
	VMCensus(ctx context.Context) (VMCensus, error)
}

// VMCensusFor returns the VM-slot census for the backend, or ok=false when the
// backend does not run under a VM concurrency limit (does not implement
// VMCensusReporter).
func VMCensusFor(ctx context.Context, rt Runtime) (census VMCensus, ok bool, err error) {
	p, ok := rt.(VMCensusReporter)
	if !ok {
		return VMCensus{}, false, nil
	}
	census, err = p.VMCensus(ctx)
	return census, true, err
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
	// that directory.
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

// WorkDirSetup is implemented by backends that store work directories
// locally inside the VM/container rather than on the host filesystem.
type WorkDirSetup interface {
	// SetupWorkDirInVM returns shell commands to copy from VirtioFS staging
	// to local VM storage and create git baseline. Called during Create/Reset.
	SetupWorkDirInVM(virtiofsStagingPath, vmLocalPath string) []string
}

// StdioExecer is an optional interface implemented by backends that can run a
// child process inside a sandbox with stdio piped to caller-provided
// reader/writers. Used by the MCP proxy to bridge an outer agent's stdio to an
// inner MCP server running inside the sandbox. Returns when the child exits.
//
// Backends that don't implement this (e.g. Tart, Seatbelt — which don't
// natively support docker-style "exec -i with stdin pipe") cause the MCP proxy
// to fail with a clear error pointing at the backend.
type StdioExecer interface {
	StdioExec(ctx context.Context, name string, cmd []string, stdin io.Reader, stdout, stderr io.Writer) error
}

// CachePruner is an optional interface for backends that maintain an
// image/snapshot/build cache that accumulates across sandbox runs. The
// `Prune()` method on the core interface only removes orphaned yoloai
// instances; this reclaims the heavier backend-managed storage.
//
// includeImages selects the depth, drawing the line at "does this force a
// rebuild?":
//
//   - false (plain `yoloai system prune`): reclaim only regenerable content
//     that does NOT force a rebuild — BuildKit cache, unused volumes/networks.
//     The base/profile images are kept, so the next `new` reuses them. On
//     backends whose only reclaimable content IS the base image (tart,
//     containerd), this is a no-op.
//   - true (`yoloai system prune --images`): also remove unused images,
//     forcing a base-image rebuild on next sandbox creation.
//
// Removes ALL unused content the backend tracks, not just yoloai's — a
// "machine dedicated to yoloai" operation. Returns the bytes reclaimed on the
// backend's data filesystem (best-effort; 0 when unmeasurable or dry-run).
type CachePruner interface {
	PruneCache(ctx context.Context, includeImages, dryRun bool, output io.Writer) (int64, error)
}

// CacheUsage reports the backend's on-disk cache footprint, split by whether
// reclaiming it forces a base-image rebuild. Returned by DiskUsageReporter.
type CacheUsage struct {
	// CachedBytes is reclaimable by plain `prune` without forcing a rebuild
	// (BuildKit cache, unused volumes). Always >= 0; 0 means "none" (not
	// "unknown").
	CachedBytes int64
	// ImageBytes is reclaimable only by `prune --images`, which forces a base
	// rebuild (base/profile image layers). -1 if unknown.
	ImageBytes int64
	Detail     string // optional human-readable breakdown ("32 images, 304 snapshots")
}

// DiskUsageReporter is an optional interface for backends that can estimate
// how much of their on-disk storage is consumed. Called by `yoloai system
// disk` to surface backend usage to the user.
type DiskUsageReporter interface {
	CacheUsage(ctx context.Context) (CacheUsage, error)
}

// PruneCacheFor calls rt.PruneCache if implemented (returning the bytes
// reclaimed); for backends without a cache it's a no-op returning (0, nil).
func PruneCacheFor(ctx context.Context, rt Runtime, includeImages, dryRun bool, output io.Writer) (int64, error) {
	if p, ok := rt.(CachePruner); ok {
		return p.PruneCache(ctx, includeImages, dryRun, output)
	}
	return 0, nil
}

// CacheUsageFor calls rt.CacheUsage if implemented; otherwise returns a
// CacheUsage with ImageBytes=-1 to signal "unknown".
func CacheUsageFor(ctx context.Context, rt Runtime) (CacheUsage, error) {
	if r, ok := rt.(DiskUsageReporter); ok {
		return r.CacheUsage(ctx)
	}
	return CacheUsage{CachedBytes: 0, ImageBytes: -1}, nil
}

// LogTailer is an optional interface for backends that can return recent
// instance log output (used to capture crash output before container removal).
// Backends without docker-style logs (VM/process backends write to files) don't
// implement it; LogsFor returns "" for them.
type LogTailer interface {
	Logs(ctx context.Context, name string, tail int) string
}

// LogsFor returns the last tail lines of an instance's logs, or "" when the
// backend doesn't implement LogTailer.
func LogsFor(ctx context.Context, rt Runtime, name string, tail int) string {
	if t, ok := rt.(LogTailer); ok {
		return t.Logs(ctx, name, tail)
	}
	return ""
}

// AgentCommandPreparer is an optional interface for backends that wrap an agent
// launch command with backend-specific environment setup (PATH overrides, shell
// wrappers). Backends that need no wrapping don't implement it; PrepareAgentCommandFor
// returns the command unchanged.
type AgentCommandPreparer interface {
	PrepareAgentCommand(cmd string) string
}

// PrepareAgentCommandFor applies the backend's agent-command wrapping, or
// returns cmd unchanged when the backend doesn't implement AgentCommandPreparer.
func PrepareAgentCommandFor(rt Runtime, cmd string) string {
	if p, ok := rt.(AgentCommandPreparer); ok {
		return p.PrepareAgentCommand(cmd)
	}
	return cmd
}

// GitExecer is an optional interface for backends whose git execution context
// differs from "run git on the host" — i.e. backends that run git inside a VM
// and must translate host work paths (Tart). Backends that run git on the host
// (Docker, Podman, Containerd, Seatbelt) don't implement it; GitExecFor runs git
// on the host directly via the package default.
type GitExecer interface {
	GitExec(ctx context.Context, name, workDir string, args ...string) (string, error)
}

// GitExecFor runs a git command for the given instance. workDir is a host path
// (e.g. ~/.yoloai/sandboxes/<name>/work/<encoded>) from the sandbox package
// helpers. Backends implementing GitExecer (Tart) translate it to their
// execution context; otherwise git runs on the host with workDir as-is. Returns
// stdout on success; a *ExecError carrying the exit code on non-zero exit (so
// callers can match, e.g., `git diff --quiet` exit 1 as "diffs present").
func GitExecFor(ctx context.Context, rt Runtime, name, workDir string, args ...string) (string, error) {
	if g, ok := rt.(GitExecer); ok {
		return g.GitExec(ctx, name, workDir, args...)
	}
	return hostGitExec(ctx, workDir, args...)
}

// hostGitExec runs git on the host filesystem rooted at workDir — the default
// for backends that bind-mount host paths. Hooks are disabled (the host's repo
// hooks must not fire for sandbox-internal git). Output is not trimmed (patches
// are whitespace-sensitive).
func hostGitExec(ctx context.Context, workDir string, args ...string) (string, error) {
	cmdArgs := append([]string{"-c", "core.hooksPath=/dev/null", "-C", workDir}, args...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...) //nolint:gosec // G204: workDir from validated sandbox state
	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", &ExecError{ExitCode: exitErr.ExitCode(), Stderr: strings.TrimSpace(string(exitErr.Stderr))}
		}
		return "", fmt.Errorf("git %v: %w", args, err)
	}
	return string(output), nil
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

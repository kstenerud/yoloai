// Package runtime defines the pluggable Runtime interface for sandbox backends.
// ABOUTME: Runtime-agnostic types decouple sandbox logic from Docker SDK.
package runtime

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
// It is the substrate's agent-free provision config — the ProvisionSpec of
// docs/contributors/design/substrate-interface.md. Agent-launch fields (agent
// command, ready pattern, idle config) are NOT here; they live in the
// orchestrator's runtimeconfig.ContainerConfig (the DF33 substrate/agent split).
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
	// Stdout is the command's standard output, trimmed of leading/trailing
	// whitespace, so callers can compare against an expected value without
	// normalizing. Every backend honors this (docker/apple trim in their Exec
	// impl; seatbelt/tart via the shared runtime.RunCmdExec helper; containerd
	// trims in its exec path). The shared conformance suite asserts it.
	Stdout   string
	ExitCode int
}

// ProbeStatus is a backend's availability tier on the current host, ordered so
// callers can compare against a threshold (>= ProbeInstalled / == ProbeRunning).
//
//   - ProbeAbsent    — the backend's tool isn't installed here at all.
//   - ProbeInstalled — the tool is installed but its daemon/service isn't
//     reachable yet (e.g. Docker Desktop installed but stopped, podman machine
//     not started, the apple apiserver not started). The backend may be usable
//     after a start-on-demand step.
//   - ProbeRunning   — ready to use right now.
//
// Backend selection (auto-pick) chooses by ProbeInstalled — the highest-priority
// *installed* backend wins, and point-of-use starts it if it isn't running.
// Backends with no separate daemon (tart, seatbelt) are only ever Absent or
// Running — they have nothing to be "installed but not running".
type ProbeStatus int

const (
	ProbeAbsent ProbeStatus = iota
	ProbeInstalled
	ProbeRunning
)

// String renders the tier for diagnostics.
func (s ProbeStatus) String() string {
	switch s {
	case ProbeRunning:
		return "running"
	case ProbeInstalled:
		return "installed"
	default:
		return "absent"
	}
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
	SupportedIsolationModes   []IsolationMode // non-default isolation modes this backend can support
	Capabilities              BackendCaps     // feature-set flags

	// SecretsConsumedTimeout caps how long the host waits for the in-sandbox
	// "secrets consumed" marker before removing the ephemeral secrets dir
	// (see internal/orchestrator/create_instance.go). Zero means "use the package
	// default" (a few tens of seconds, fine for fast-booting container
	// backends). Slow-booting backends — where the guest can take longer than
	// the default just to reach the entrypoint that reads the secrets — set
	// this higher so the host does not remove the dir before the guest has
	// read it. Without this, the read-before-removal invariant the marker
	// exists to guarantee is violated on slow boots. Tart sets it because a
	// macOS VM boot (plus a possible one-time `xcodebuild -runFirstLaunch`)
	// routinely exceeds the default. See backend-idiosyncrasies.md.
	SecretsConsumedTimeout time.Duration
	// Probe reports the backend's availability tier on this host (Absent /
	// Installed / Running) and, when not Running, a short user-facing reason
	// ("docker installed but daemon not reachable", "tart binary not found", …).
	// Implementations must be fast and side-effect-free — they run on `yoloai
	// info`, setup wizards, and detect-backend dispatch, so LookPath the binary
	// (installed) and stat the socket (running); do not dial. The split lets
	// auto-pick select by installed (§ ProbeStatus) and point-of-use start an
	// installed-but-stopped backend on demand. nil is permitted (treated as
	// Running); every shipped backend supplies a real probe.
	//
	// env is the caller's threaded host-env snapshot (the same map fed to the
	// factory). Backends that locate their daemon socket via env vars
	// (DOCKER_HOST, CONTAINER_HOST, XDG_RUNTIME_DIR) read them from this map
	// rather than os.Getenv, so probing stays principal-scoped (§12). Backends
	// that probe by stat/LookPath ignore it.
	Probe func(ctx context.Context, env map[string]string) (status ProbeStatus, reason string)

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
	NetworkIsolation   bool               // supports --network=isolated (iptables domain filtering)
	CapAdd             bool               // supports cap_add, devices, and setup commands
	HostFilesystem     bool               // true when sandbox state lives on the host (seatbelt, future SSH)
	ContainerAttach    bool               // exposes a docker-compatible container surface so VS Code's "Attach to Running Container" works
	VMRuntimeDir       string             // path to yoloai state inside the VM; "" means /yoloai (docker default)
	FilesystemLocality FilesystemLocality // where tracked work copies live; see the type doc. Zero value = LocalityHostSide.
	KeepAliveModel     KeepAliveModel     // init/keep-alive model; see the type doc. Zero value = KeepAliveContainerInit.
	// GitExecInConfinement makes the copy-mode work-copy git run inside the
	// sandbox (via GitExecer) instead of on the host, for a backend whose work
	// copy IS host-readable (LocalityHostSide) but which still has a container to
	// exec into. It closes the host-code-execution path through an agent-planted
	// .git filter/diff/fsmonitor driver (audit C1). Set by the container backends
	// (docker/podman/containerd); seatbelt leaves it false (no container — host
	// git, documented residual). Orthogonal to and additive with SandboxSide,
	// which already forces in-confinement git; read both via
	// GitRunsInConfinement, never this field directly.
	GitExecInConfinement bool
	// AgentFreeLaunch opts the backend into the D88 bring-up: the box comes up
	// agent-free on a keepalive_only holder (the entrypoint writes .substrate-ready
	// then execs sleep) and the host Launches sandbox-setup.py over it via
	// runtime.ProcessLauncher. Requires the backend to implement ProcessLauncher.
	// Zero value (false) selects the legacy path — the agent is welded into the
	// entrypoint and the host waits for the secrets-consumed marker instead. Only
	// Docker opts in; Podman inherits Launch by embedding Docker but its rootless
	// bring-up is not verified on this path, so it stays on legacy.
	AgentFreeLaunch bool
}

// FilesystemLocality declares where a sandbox's tracked work copies live
// relative to the host, which determines whether host tooling (git, the change
// probe) can read them directly or must dispatch through the backend.
//
// It is ORTHOGONAL to BackendCaps.HostFilesystem, which is about where sandbox
// *state* lives, not the work copy: seatbelt is HostFilesystem=true yet
// LocalityHostSide; tart is HostFilesystem=false and LocalitySandboxSide.
//
// This property is the named replacement for detecting "does this backend run
// git in-VM?" by type-asserting runtime.GitExecer. The property decides whether
// to route through the backend; GitExecer remains the operation that does it.
type FilesystemLocality int

const (
	// LocalityHostSide: work copies are readable on the host filesystem, so
	// git/diff/change-probe run on the host (docker, podman, containerd,
	// seatbelt, apple). The zero value, so an unset backend defaults here.
	LocalityHostSide FilesystemLocality = iota
	// LocalitySandboxSide: work copies live inside the sandbox (e.g. Tart's
	// VM, where VirtioFS corrupts host-side git), so git must run in-VM and a
	// host-side change probe is blind. A backend declaring this MUST implement
	// GitExecer.
	LocalitySandboxSide
)

// LocalityOf returns rt's declared FilesystemLocality, defaulting to
// LocalityHostSide for a nil runtime.
func LocalityOf(rt Backend) FilesystemLocality {
	if rt == nil {
		return LocalityHostSide
	}
	return rt.Descriptor().Capabilities.FilesystemLocality
}

// GitRunsInConfinement reports whether rt runs the copy-mode work-copy git
// inside the sandbox rather than on the host. True for SandboxSide backends
// (the work copy isn't on the host) and for container backends that keep the
// work copy host-readable but still exec git in-container so an agent-controlled
// .git/config can't run filter/diff/fsmonitor drivers on the host (audit C1).
// False for seatbelt (no container — host git, documented residual) and nil.
// A backend for which this is true MUST implement GitExecer. This is the
// dispatch predicate for git.NewSandbox; it decouples git-exec locality from
// FilesystemLocality (which still governs where work copies physically live).
func GitRunsInConfinement(rt Backend) bool {
	if rt == nil {
		return false
	}
	caps := rt.Descriptor().Capabilities
	return caps.FilesystemLocality == LocalitySandboxSide || caps.GitExecInConfinement
}

// GitHardeningArgs returns the `git -c` flags that must precede every git
// invocation yoloai runs against agent-controlled content — on the host or in a
// backend's confinement. A single source so a future hardening flag is added
// once here, not forgotten in one of the executors.
//
//   - core.hooksPath=/dev/null stops an agent-planted .git/hooks script from
//     executing (audit C1).
//   - core.fsmonitor=false stops an agent-planted core.fsmonitor=<command> from
//     executing. fsmonitor is a pure performance optimization (it never affects
//     correctness), so disabling it is always safe, and it closes an RCE vector
//     that applies even to read-only operations like `git status` — which runs
//     the configured fsmonitor command before scanning the work tree.
//
// Note: this does NOT disable filter/textconv drivers, which are attribute-bound
// and must run for diff correctness (Git LFS, git-crypt, …). Those are safe only
// where git runs in-confinement (GitRunsInConfinement); on a host-side backend
// they remain a residual — see confine-host-side-git.md.
func GitHardeningArgs() []string {
	return []string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false"}
}

// KeepAliveModel classifies how each backend keeps its isolated environment
// alive and reaps the processes running inside it. Declared per-backend in
// BackendCaps so callers reason by semantic property, never by backend type.
// See docs/contributors/design/backend-topology.md for the per-backend table.
type KeepAliveModel int

const (
	// KeepAliveContainerInit: a container init must become the neutral PID 1,
	// with the agent session launched on top via a Go-driven Launch call. The
	// container init is the reaper; the agent is NOT PID 1 (DF31 carve target).
	// Backends: docker, podman.
	// See docs/contributors/design/backend-topology.md.
	KeepAliveContainerInit KeepAliveModel = iota

	// KeepAliveGuestOSInit: the guest OS's own init already keeps the
	// environment up and reaps processes; the neutral keep-alive is provided
	// for free by the VM. Launch targets the guest, not a synthetic PID 1.
	// Backends: containerd (Kata microVM), tart (macOS VM), apple (per-container Apple VM).
	// See docs/contributors/design/backend-topology.md.
	KeepAliveGuestOSInit

	// KeepAliveHostKeepAlive: no container or VM — the isolated environment is
	// a host process group (sandbox-exec). Keep-alive is the host process tree;
	// there is no "inside" to Launch into in the container/VM sense.
	// Backend: seatbelt.
	// See docs/contributors/design/backend-topology.md.
	KeepAliveHostKeepAlive
)

// KeepAliveModelOf returns rt's declared KeepAliveModel, defaulting to
// KeepAliveContainerInit for a nil runtime.
func KeepAliveModelOf(rt Backend) KeepAliveModel {
	if rt == nil {
		return KeepAliveContainerInit
	}
	return rt.Descriptor().Capabilities.KeepAliveModel
}

// ProcSpec is the agent-neutral launch input for the future Substrate.Launch
// verb (docs/contributors/design/substrate-interface.md §ProcSpec). It carries
// NO agent fields (DF33) — agent command, ready pattern, and idle config belong
// to the orchestrator layer. ProcSpec is consumed when the Launch verb lands in
// the session-layer carve; it is defined here now so dependent types can
// reference it before the carve lands.
type ProcSpec struct {
	// Argv is the command and its arguments.
	Argv []string
	// Env holds additional environment variables for the process in KEY=VAL
	// form, matching the package's existing ContainerEnv convention.
	Env []string
	// Cwd is the working directory inside the substrate. Empty means the
	// backend's default (typically the image WORKDIR or home dir).
	Cwd string
	// User is the user[:group] to run the process as. Empty means the
	// backend's default user.
	User string
	// TTY requests a pseudo-terminal. A rich reattachable session (tmux) is
	// a higher-level refinement built on top; TTY here is the raw pty flag.
	TTY bool
	// Stdin requests that stdin be left open so the caller can write to it.
	Stdin bool
	// Detached requests a long-lived process that survives the launching
	// client's disconnect. Its stdio is NOT streamed back to the caller
	// (Streams() returns zero/nil readers and writer); the process is expected
	// to redirect its own output to files inside the substrate. Wait still
	// reports exit via backend inspection. Use for a session-runner / daemon
	// that must outlive the caller.
	Detached bool
}

// Backend is the sandbox backend interface. Implementations manage the
// lifecycle of sandbox instances (containers, VMs, etc.) and provide
// image/environment management.
type Backend interface {
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

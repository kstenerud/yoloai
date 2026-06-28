// ABOUTME: Optional Runtime capability interfaces — backend-specific extensions
// ABOUTME: a backend implements only when it departs from the host/container
// ABOUTME: default. Each is reached via a type-assert + `…For` fallback so no
// ABOUTME: concrete backend type leaks. The core Runtime lives in runtime.go.
package runtime

import (
	"context"
	"io"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/runtime/caps"
)

// The Runtime interface (runtime.go) is the contract every backend implements.
// The interfaces below are OPTIONAL: a backend implements one only when its
// behavior departs from the default. Each carries a package-level `…For(rt, …)`
// helper that type-asserts and falls back to the default when the backend does
// not implement it, so call sites never type-assert directly and no concrete
// backend type leaks across the seam. They fall into three groups:
//
//  1. Path & exec translators — backends whose files or git execution live
//     somewhere other than the host path the caller passes (VM-local storage,
//     sandbox-relocated :copy dirs).
//  2. Capability probes & reporters — backends that declare a host requirement
//     or report a backend-managed resource (user namespaces, isolation
//     prerequisites, simulator images, VM-slot census, disk usage).
//  3. Optional operations — extra verbs only some backends can perform (stdio
//     exec, cache prune, log tail, agent-command wrapping).

// ===== 1. Path & exec translators =====

// CopyMountResolver is an optional interface implemented by backends that
// rewrite :copy mount paths from host paths to sandbox-local paths. Backends
// that don't implement it (the default) see :copy mounts at the original
// host path inside the container.
type CopyMountResolver interface {
	ResolveCopyMount(sandboxName, hostPath string) string
}

// ResolveCopyMountFor returns the in-sandbox path for a :copy directory.
// Falls back to hostPath when the backend doesn't implement CopyMountResolver.
func ResolveCopyMountFor(rt Backend, sandboxName, hostPath string) string {
	if p, ok := rt.(CopyMountResolver); ok {
		return p.ResolveCopyMount(sandboxName, hostPath)
	}
	return hostPath
}

// GuestMountResolver is an optional interface implemented by backends that
// expose bind/share mounts at a translated guest path rather than the
// container mount target. Tart, for example, re-roots host directories under
// /Users/admin/host/<host-path> inside the VM. Backends that don't implement
// it (the default) see mounts at the original container path.
//
// Implementations must be idempotent: applying the translation to an
// already-translated guest path must return it unchanged, so the path can be
// stored in metadata and safely re-resolved on restart/reset.
type GuestMountResolver interface {
	// ResolveGuestMountPath translates a container-side mount target to the
	// path where the mount is actually reachable inside the guest.
	ResolveGuestMountPath(containerPath string) string
}

// ResolveGuestMountPathFor returns the guest-visible path for a mount target.
// Falls back to containerPath when the backend doesn't implement
// GuestMountResolver.
func ResolveGuestMountPathFor(rt Backend, containerPath string) string {
	if p, ok := rt.(GuestMountResolver); ok {
		return p.ResolveGuestMountPath(containerPath)
	}
	return containerPath
}

// WorkDirSetup is implemented by backends that store work directories
// locally inside the VM/container rather than on the host filesystem.
type WorkDirSetup interface {
	// SetupWorkDirInVM returns shell commands to copy from VirtioFS staging
	// to local VM storage and create git baseline. Called during Create/Reset.
	SetupWorkDirInVM(virtiofsStagingPath, vmLocalPath string) []string
}

// GitExecer is an optional interface for backends whose git execution context
// differs from "run git on the host" — i.e. backends that run git inside a VM
// and must translate host work paths (Tart). Backends that run git on the host
// (Docker, Podman, Containerd, Seatbelt) don't implement it; the git package's
// sandbox scope (git.NewSandbox) runs git on the host directly via its default.
type GitExecer interface {
	GitExec(ctx context.Context, name, workDir string, args ...string) (string, error)
}

// ===== 2. Capability probes & reporters =====

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

// IsolationCapabilityProvider is an optional interface implemented by
// backends that need specific host capabilities (binaries present, kernel
// features, etc.) for non-default isolation modes. Backends that don't
// implement it have no isolation-mode prerequisites.
type IsolationCapabilityProvider interface {
	RequiredCapabilities(isolation IsolationMode) []caps.HostCapability
}

// RequiredCapabilitiesFor returns the host capabilities needed for the given
// isolation mode, or nil when the backend has no requirements for the mode.
func RequiredCapabilitiesFor(rt Backend, isolation IsolationMode) []caps.HostCapability {
	if p, ok := rt.(IsolationCapabilityProvider); ok {
		return p.RequiredCapabilities(isolation)
	}
	return nil
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
func VMCensusFor(ctx context.Context, rt Backend) (census VMCensus, ok bool, err error) {
	p, ok := rt.(VMCensusReporter)
	if !ok {
		return VMCensus{}, false, nil
	}
	census, err = p.VMCensus(ctx)
	return census, true, err
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
	// StaleBytes is reclaimable by `prune --stale-bases`: superseded base images
	// the current resolved base no longer references (e.g. an old-macOS Tart base
	// left behind when the host OS — and thus the resolved codename — changed).
	// Removing them never forces a rebuild of the current base. 0 = none.
	StaleBytes int64
	Detail     string // optional human-readable breakdown ("32 images, 304 snapshots")
}

// DiskUsageReporter is an optional interface for backends that can estimate
// how much of their on-disk storage is consumed. Called by `yoloai system
// disk` to surface backend usage to the user.
type DiskUsageReporter interface {
	CacheUsage(ctx context.Context) (CacheUsage, error)
}

// CacheUsageFor calls rt.CacheUsage if implemented; otherwise returns a
// CacheUsage with ImageBytes=-1 to signal "unknown".
func CacheUsageFor(ctx context.Context, rt Backend) (CacheUsage, error) {
	if r, ok := rt.(DiskUsageReporter); ok {
		return r.CacheUsage(ctx)
	}
	return CacheUsage{CachedBytes: 0, ImageBytes: -1}, nil
}

// ===== 3. Optional operations =====

// ExitStatus is the result of a launched process exiting. Signaled and Signal
// are populated when the backend can report signal death; docker exec cannot,
// so it always reports Signaled=false.
type ExitStatus struct {
	Code     int
	Signaled bool
	Signal   int
}

// ProcessStreams holds the I/O streams of a launched process. Stdin is non-nil
// only when ProcSpec.Stdin was requested. Stderr is nil for a TTY process (the
// pty merges stdout+stderr into Stdout).
type ProcessStreams struct {
	Stdin  io.WriteCloser
	Stdout io.Reader
	Stderr io.Reader
}

// Process is a handle to a single launched process. Per substrate-interface.md
// §6 it is live only within the launching call's lifetime — there is no
// re-open-by-pid. Signal() is deliberately omitted for now (docker exec has no
// per-exec signal API; the carve kills launched processes via Stop/tmux, not
// per-process signal). Add it when a consumer needs it.
type Process interface {
	// ID returns the backend-assigned identifier for this process (e.g. a docker
	// exec ID).
	ID() string
	// Streams returns the process's I/O streams.
	Streams() ProcessStreams
	// Wait blocks until the process exits or ctx is cancelled. Returns the exit
	// status on clean exit or ctx.Err() on cancellation.
	Wait(ctx context.Context) (ExitStatus, error)
}

// ProcessLauncher is an optional backend interface: start a process inside a
// running instance and return a non-blocking Process handle. This is the
// agent-free Launch verb of substrate-interface.md; today's Exec/InteractiveExec
// are its blocking siblings. Backends that don't implement it cannot yet host
// the carve's neutral-PID-1 + Launch model (broadened in a later step).
type ProcessLauncher interface {
	// Ready reports whether the instance has finished its own provisioning and
	// can accept a launched process — substrate-interface.md §Ready ("up AND able
	// to accept work", distinct from Inspect's Running, which is merely "PID 1 is
	// up"). A process launched before the box is ready is silently killed mid-set
	// up (DF44 readiness race), so callers must wait for Ready before Launch. The
	// backend owns HOW readiness is determined; the caller owns the wait policy
	// (timeout, poll interval).
	Ready(ctx context.Context, name string) (bool, error)
	Launch(ctx context.Context, name string, spec ProcSpec) (Process, error)
}

// LauncherOf returns rt as a ProcessLauncher if the backend implements one.
func LauncherOf(rt Backend) (ProcessLauncher, bool) {
	l, ok := rt.(ProcessLauncher)
	return l, ok
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

// PruneCacheFor calls rt.PruneCache if implemented (returning the bytes
// reclaimed); for backends without a cache it's a no-op returning (0, nil).
func PruneCacheFor(ctx context.Context, rt Backend, includeImages, dryRun bool, output io.Writer) (int64, error) {
	if p, ok := rt.(CachePruner); ok {
		return p.PruneCache(ctx, includeImages, dryRun, output)
	}
	return 0, nil
}

// StaleBasePruner is an optional interface for backends that can accumulate
// superseded base images — bases the current resolved base no longer
// references. Tart implements it: when the host macOS major (and thus the
// resolved Cirrus codename) changes, the previous macos-<codename>-base OCI
// image lingers, matched by neither the orphan VM sweep (it's an image, not a
// yoloai-* VM) nor `prune --images` (which only targets the *current* base).
// This removal is opt-in (`prune --stale-bases`) and never automatic — a
// multi-GB base a user may still want to switch back to is kept until asked.
//
// Returns the removed (or, under dryRun, removable) image refs and the bytes
// reclaimed (best-effort; 0 when unmeasurable or dry-run).
type StaleBasePruner interface {
	PruneStaleBases(ctx context.Context, dryRun bool, output io.Writer) (refs []string, reclaimed int64, err error)
}

// PruneStaleBasesFor calls rt.PruneStaleBases if implemented; for backends
// without superseded bases it's a no-op returning (nil, 0, nil).
func PruneStaleBasesFor(ctx context.Context, rt Backend, dryRun bool, output io.Writer) ([]string, int64, error) {
	if p, ok := rt.(StaleBasePruner); ok {
		return p.PruneStaleBases(ctx, dryRun, output)
	}
	return nil, 0, nil
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
func LogsFor(ctx context.Context, rt Backend, name string, tail int) string {
	if t, ok := rt.(LogTailer); ok {
		return t.Logs(ctx, name, tail)
	}
	return ""
}

// ===== 4. Interactive session =====

// InteractiveSession is implemented by backends that expose an interactive
// terminal session (a tmux "main" session) for attach, prompt delivery, and
// terminal capture. Optional: a headless backend with no interactive session
// simply does not implement it, keeping the core Runtime contract session-free
// (module-split Phase C-minimal).
type InteractiveSession interface {
	// TmuxSocket returns the tmux socket path for a sandbox, or "" if the
	// backend uses the uid-based default socket. sandboxDir is the resolved
	// sandbox directory path. The value is written into runtime-config.json
	// at sandbox creation time so all exec'd processes (including
	// non-interactive execs) find the same tmux server as the container init
	// process.
	TmuxSocket(sandboxDir string) string
	// AttachCommand returns the command to exec interactively to attach to
	// the tmux session in a running instance. tmuxSocket is the fixed socket
	// path from runtime-config.json (empty = use default). rows and cols are
	// the current terminal dimensions (0 = unknown). isolation is the sandbox
	// isolation mode (e.g. IsolationModeContainerEnhanced).
	AttachCommand(tmuxSocket string, rows, cols int, isolation IsolationMode) []string
}

// TmuxSocketFor returns the backend's tmux socket for sandboxDir, or "" when the
// backend has no interactive session.
func TmuxSocketFor(rt Backend, sandboxDir string) string {
	if s, ok := rt.(InteractiveSession); ok {
		return s.TmuxSocket(sandboxDir)
	}
	return ""
}

// AttachCommandFor returns the interactive attach command, or (nil, false) when
// the backend has no interactive session.
func AttachCommandFor(rt Backend, tmuxSocket string, rows, cols int, isolation IsolationMode) ([]string, bool) {
	if s, ok := rt.(InteractiveSession); ok {
		return s.AttachCommand(tmuxSocket, rows, cols, isolation), true
	}
	return nil, false
}

// InjectorReach describes how a host-side service (the credential injector) is
// reached from inside a sandbox. The two hosts can differ — on Docker Desktop the
// agent dials "host.docker.internal" while the proxy binds a macOS host interface
// — which is why both are reported. See
// docs/contributors/design/research/egress-broker-host-reachability.md.
type InjectorReach struct {
	// BindHost is the host interface a host-side proxy should listen on so that
	// only this sandbox's network can reach it — the bridge gateway IP for
	// bridge/NAT backends, "127.0.0.1" for host-process backends. Never
	// "0.0.0.0", which would expose the injector on the host LAN.
	BindHost string
	// DialHost is what the in-sandbox agent uses to reach that listener (the host
	// it places in base_url). For Linux bridge backends it equals BindHost (the
	// gateway IP).
	DialHost string
}

// InjectorReachable is an optional backend interface: report how a sandbox
// reaches a host-side credential injector. Backends that cannot host a
// sandbox-reachable host-side proxy do not implement it, and the credential
// broker falls back to direct delivery for them (no flag-day, D105/D106).
type InjectorReachable interface {
	// InjectorReach reports the bind/dial endpoint for the named instance, which
	// must already exist (the gateway is only knowable post-create).
	InjectorReach(ctx context.Context, instanceName string) (InjectorReach, error)
}

// InjectorReachOf returns rt as an InjectorReachable if the backend implements one.
func InjectorReachOf(rt Backend) (InjectorReachable, bool) {
	r, ok := rt.(InjectorReachable)
	return r, ok
}

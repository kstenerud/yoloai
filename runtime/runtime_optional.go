// ABOUTME: Optional Runtime capability interfaces — backend-specific extensions
// ABOUTME: a backend implements only when it departs from the host/container
// ABOUTME: default. Each is reached via a type-assert + `…For` fallback so no
// ABOUTME: concrete backend type leaks. The core Runtime lives in runtime.go.
package runtime

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"github.com/kstenerud/yoloai/feedback"
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

	// WorkDirStagingPath returns the guest-visible path of the host-staged work
	// copy for hostPath — the source SetupWorkDirInVM copies from.
	//
	// The backend answers this because only the backend knows how the sandbox
	// directory reaches its guest. The caller used to build the path from a
	// literal ("/Volumes/My Shared Files/yoloai/work/…"), which is tart's share
	// layout spelled out in the orchestrator: correct until the layout moved,
	// then silently wrong — a copy-mode workdir would stage from a path that no
	// longer exists, in a package no backend change would ever make you re-read.
	WorkDirStagingPath(hostPath string) string
}

// GitExecer is an optional interface for backends that run the copy-mode
// work-copy git inside the sandbox's confinement rather than on the host. A
// SandboxSide backend (Tart) MUST implement it because the work copy isn't on
// the host at all; Docker, Podman, Containerd and Apple implement it so an
// agent-controlled work-copy .git/config cannot execute filter/diff/fsmonitor
// drivers on the host during diff/apply/status (audit C1), and Seatbelt
// implements it by wrapping git in a sandbox-exec profile, having no container
// to exec into. The set of implementers is exactly GitRunsInConfinement, which
// today is every backend; the git package dispatches through it for those and
// falls back to host git only for a nil runtime.
//
// name is the resolved runtime instance name (container id), the same contract
// as Exec — the git-package boundary resolves it from the principal. user is the
// sandbox's container user (store.ContainerUser), also resolved at that
// boundary; backends with a single guest user (Tart) ignore it.
// workDir is the path git runs against inside the confinement (the dir's
// resolved mount path), already mapped by the caller. Implementations must
// preserve git's exact stdout (patches are whitespace-sensitive — do not trim)
// and return runtime.ErrNotRunning when the instance is not up.
type GitExecer interface {
	GitExec(ctx context.Context, name, user, workDir string, args ...string) (string, error)
}

// ===== 2. Capability probes & reporters =====

// UsernsProvider is an optional interface implemented by backends that need
// a non-default user namespace mode. Podman rootless uses "keep-id" to map
// the container uid to the host user; this also determines the tmux exec user
// (keep-id containers run as the host user, not as "yoloai").
type UsernsProvider interface {
	// UsernsMode returns the user namespace mode for a new container.
	// hasSysAdmin is true when the container will receive CAP_SYS_ADMIN
	// (recipe/profile cap_add), which requires real root in the container
	// and therefore cannot use keep-id.
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

// NetHealthState classifies a VM's guest-network liveness as observed by a
// backend that can probe it (currently only tart, whose shared vmnet session
// can wedge — see docs/contributors/backend-idiosyncrasies.md, "Tart: vmnet
// session wedges on a long-idle VM").
type NetHealthState int

const (
	// NetHealthOK means the VM resolved a working IP address.
	NetHealthOK NetHealthState = iota
	// NetHealthWedged means the guest network is confirmed dead — either the
	// guest has fallen back to a 169.254.x.x link-local address, or `tart ip`
	// returned an address that falls outside every host bridge* subnet (a
	// stale DHCP lease from a superseded vmnet subnet). Either way the vmnet
	// session is dead and only a VM restart recovers it.
	NetHealthWedged
	// NetHealthUnknown means liveness could not be confirmed either way (the
	// probe failed, returned nothing, or the guest reported an address that
	// doesn't match either wedge signature — e.g. still mid-boot/DHCP).
	NetHealthUnknown
)

// VMNetHealth is one running VM's network-liveness probe result.
type VMNetHealth struct {
	SandboxName string // yoloai sandbox name (instance prefix stripped)
	VMName      string // backend-native VM/instance name
	State       NetHealthState
	Detail      string // a full human-readable clause: the resolved IP, or why wedged/unknown
}

// NetLivenessReport is a point-in-time network-liveness probe across every
// running VM a backend manages. Backends whose guest network can silently
// wedge independent of container/VM lifecycle state (e.g. tart's shared
// vmnet session after host sleep) report it so doctor can catch a dead-network
// VM before the user burns time debugging ConnectionRefused inside the agent.
type NetLivenessReport struct {
	VMs []VMNetHealth
}

// Wedged returns the VMs confirmed to have a dead (link-local) guest network.
func (r NetLivenessReport) Wedged() []VMNetHealth {
	var out []VMNetHealth
	for _, v := range r.VMs {
		if v.State == NetHealthWedged {
			out = append(out, v)
		}
	}
	return out
}

// NetLivenessReporter is an optional interface implemented by backends whose
// guest network can silently wedge independent of container/VM lifecycle
// state. Implementations probe each running VM and report its liveness.
type NetLivenessReporter interface {
	NetLiveness(ctx context.Context) (NetLivenessReport, error)
}

// NetLivenessFor returns the network-liveness report for the backend, or
// ok=false when the backend does not implement NetLivenessReporter.
func NetLivenessFor(ctx context.Context, rt Backend) (report NetLivenessReport, ok bool, err error) {
	p, ok := rt.(NetLivenessReporter)
	if !ok {
		return NetLivenessReport{}, false, nil
	}
	report, err = p.NetLiveness(ctx)
	return report, true, err
}

// SandboxNetHealthProber is an optional interface implemented by backends
// that can probe a single sandbox's guest-network liveness on demand, reusing
// the same probe NetLivenessReporter runs fleet-wide for doctor. Callers must
// only invoke this for a sandbox they already know is running (active/idle) —
// the prober does not itself re-check the sandbox's lifecycle state.
type SandboxNetHealthProber interface {
	SandboxNetHealth(ctx context.Context, name string) (VMNetHealth, error)
}

// SandboxNetHealthFor probes one sandbox's guest-network health, or returns
// ok=false when the backend does not implement SandboxNetHealthProber.
func SandboxNetHealthFor(ctx context.Context, rt Backend, name string) (health VMNetHealth, ok bool, err error) {
	p, ok := rt.(SandboxNetHealthProber)
	if !ok {
		return VMNetHealth{}, false, nil
	}
	health, err = p.SandboxNetHealth(ctx, name)
	return health, true, err
}

// GuestFileDigest names one host file under a sandbox directory together with
// the SHA-256 the host just wrote there. The host supplies the digest because
// only the host knows the intended bytes: on a stale share the guest's own
// size and mtime are already correct while its data is not, so anything the
// guest could compute for itself would be computed from the same stale view
// (DF175).
type GuestFileDigest struct {
	HostPath string // absolute host path, inside the sandbox directory
	SHA256   string // lowercase hex of the bytes the host wrote
}

// GuestFileRefresher is implemented by a backend whose guest can serve stale
// file DATA for a path it has already read, after the host rewrites that path.
// Tart implements it: a macOS guest's VirtioFS client keeps cached pages for a
// vnode that is never reclaimed, so a host-side overwrite is invisible — the
// guest reads the OLD bytes with the NEW size and no error at any layer. No
// host-side write pattern avoids it; temp+rename is measurably worse, since it
// leaves the old size cached too and removes the last signal that anything
// changed (DF175).
//
// The backend does the work because only the backend knows how a host path
// under the sandbox directory reaches its guest — the same reason
// WorkDirSetup.WorkDirStagingPath exists rather than the caller spelling out
// tart's share layout.
//
// Implementations verify first and repair only what is stale, so a path that
// was never cached costs nothing but the check. They return the digests still
// mismatched after the repair — empty means everything the caller wrote is now
// readable in the guest. Returning ErrNotRunning is expected and not a
// failure: a stopped guest holds no cache to be stale.
type GuestFileRefresher interface {
	RefreshGuestFiles(ctx context.Context, name string, files []GuestFileDigest) ([]GuestFileDigest, error)
}

// RefreshGuestFilesFor re-reads files in the guest and repairs any the guest
// sees staler than the host, returning the ones that could not be repaired.
// ok=false means the backend needs no such refresh, which is every backend but
// tart — callers treat that as "nothing to do", never as a failure.
func RefreshGuestFilesFor(ctx context.Context, rt Backend, name string, files []GuestFileDigest) (stale []GuestFileDigest, ok bool, err error) {
	r, ok := rt.(GuestFileRefresher)
	if !ok {
		return nil, false, nil
	}
	stale, err = r.RefreshGuestFiles(ctx, name, files)
	return stale, true, err
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

// RecreateAdvisor is an optional interface for backends whose container may
// actually exist under a different host endpoint than the one currently
// connected (e.g. Docker via OrbStack vs Docker Desktop). Before the lifecycle
// layer silently recreates a sandbox whose container is reported missing, it
// asks the backend for an advisory; a non-empty string warns the user that the
// "missing" container may live in a provider they switched away from, so
// recreating here would abandon the original (DF22). Empty string = no advisory.
type RecreateAdvisor interface {
	RecreateAdvisory(ctx context.Context) string
}

// RecreateAdvisoryFor returns the backend's recreate advisory if implemented,
// else "".
func RecreateAdvisoryFor(ctx context.Context, rt Backend) string {
	if a, ok := rt.(RecreateAdvisor); ok {
		return a.RecreateAdvisory(ctx)
	}
	return ""
}

// ===== 3. Optional operations =====

// Renamer is an optional backend interface: rename an existing instance in
// place, preserving its running state and process. Implemented by the backends
// whose rename is a metadata-only operation — docker (docker rename) and tart
// (tart rename), both verified to keep a running instance running with its PID
// intact. The v4->v5 principal-rename migration (D126) uses it to move a
// "yoloai-<name>" instance to "yoloai-cli-<name>" without recreating it, so a
// mid-task agent is never interrupted.
//
// Backends whose instance identity is immutable simply do NOT implement this —
// containerd (the name is both the container id and the snapshot key, daemon-
// enforced) and apple (its CLI has no rename verb). The migration type-asserts
// for Renamer and falls back to recreate-or-refuse for those. seatbelt needs no
// rename at all (its on-disk dir is the bare sandbox name, carrying no prefix),
// so it also does not implement it.
type Renamer interface {
	// Rename changes an instance's name from oldName to newName. Returns
	// ErrNotFound if oldName does not exist. A successful rename leaves a running
	// instance running.
	Rename(ctx context.Context, oldName, newName string) error
}

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

// Image labels yoloAI stamps on the images it builds. Both are set at build
// time and read back to decide staleness, so the answer lives on the artifact
// rather than in a file beside it.
const (
	// ProfileChecksumLabel carries a profile image's own Dockerfile-chain
	// checksum. Set only on profile images.
	ProfileChecksumLabel = "yoloai.profile.checksum"

	// BaseChecksumLabel carries the build-inputs checksum of the yoloai-base an
	// image was built from. Set on the base itself, and **inherited by every
	// image built FROM it** — OCI config labels propagate through FROM unless
	// overridden — so a profile image reports which base it descends from
	// without anything having to record that separately.
	BaseChecksumLabel = "yoloai.base.checksum"
)

// ProfileImageBuilder is an optional interface for backends that can build a
// custom image from a profile's Dockerfile. Image-based backends (docker,
// podman, containerd, apple) implement it; backends with no OCI image concept
// (tart, seatbelt) do not, and profile Dockerfiles are ignored there.
//
// # Staleness travels with the image
//
// The caller computes a checksum, the backend stamps it on the image at build
// time as ProfileChecksumLabel, and reads it back to answer "what is in the
// store?". The caller compares. That split is deliberate: the *scheme* — what
// goes into the checksum, and how a parent's change reaches a child — lives in
// exactly one place, and a backend only transports a string.
//
// This replaced a host-side marker file keyed by backend, which was the wrong
// shape three times over. A file beside the profile cannot know the image was
// deleted (DF154), cannot tell two docker daemons apart without being keyed by
// one (DF152), and was keyed for the base image but not the profile image
// (DF150) — three defects that are all one defect: a record that outlives what
// it describes. A label cannot outlive its image, because it *is* the image.
//
// The label name is shared; the extraction is not, and that is expected. Docker
// reads a flat Config.Labels via the SDK, containerd walks the content store to
// the OCI config blob, and apple parses `container image inspect` JSON where the
// labels sit two array levels deep. Differing accessors behind one contract is
// what this package is for.
//
// buildEnv is the host-environment snapshot the build subprocess draws from (the
// caller's Layout, never the live process env — §12).
//
// A build failure must carry the build tool's own diagnostic on the error, not
// only on the output stream (DF144/DF145) — see `standards/go.md` → Subprocess
// errors.
type ProfileImageBuilder interface {
	// BuildProfileImage builds sourceDir's Dockerfile as tag, stamping checksum
	// as ProfileChecksumLabel.
	BuildProfileImage(ctx context.Context, sourceDir, tag, checksum string, secrets []string, buildEnv config.Layout, progress feedback.ProgressSink, notices feedback.Sink, logger *slog.Logger) error

	// ImageLabels returns tag's labels as they exist in this backend's store.
	//
	// ok is false when the image is absent or cannot be read — "cannot vouch for
	// it", which every caller treats as needing a build. A present image with no
	// labels returns an empty map and ok=true, which is a different fact: the
	// image is there and says nothing about itself. Callers still treat a missing
	// *label* as stale, but the distinction keeps "image gone" and "image says
	// nothing" from collapsing into one unreadable answer.
	//
	// Returns the whole map rather than one named label because more than one
	// question is asked of the same image — its own build checksum, and which
	// base it descends from — and a per-question accessor would mean a round trip
	// each and a new method per label.
	ImageLabels(ctx context.Context, tag string) (labels map[string]string, ok bool)

	// ExpectedBaseChecksum returns the BaseChecksumLabel value this binary's
	// embedded build inputs produce — the same expression Setup evaluates when
	// deciding whether the base image needs rebuilding.
	//
	// It is the right side of a lineage comparison whenever the left side is an
	// image or instance that already exists and the store cannot be trusted to be
	// current. Reading the base *tag*'s label instead is a false negative in the
	// case that matters most: immediately after an upgrade with nothing yet
	// rebuilt, the tag and the sandbox both still carry the old checksum and
	// agree, while the host binary has already moved (DF156). Paths that have
	// just run Setup may read the tag; paths that have not must use this.
	//
	// It is a content hash of the embedded files, not an identity of the binary,
	// so a rebuilt binary whose resources are unchanged returns the same value and
	// invalidates nothing.
	ExpectedBaseChecksum() string
}

// ProfileImageBuilderOf returns rt as a ProfileImageBuilder if the backend
// builds profile images; ok is false for backends with no image concept.
func ProfileImageBuilderOf(rt Backend) (ProfileImageBuilder, bool) {
	b, ok := rt.(ProfileImageBuilder)
	return b, ok
}

// RuntimeScriptDir is where a launch delivers the runtime scripts inside the
// sandbox. It is the path the host process drives by absolute name — the
// entrypoint, the session runner, the status monitor and the firewall installer
// all live here — so it is a host↔guest contract, not an implementation detail.
const RuntimeScriptDir = "/yoloai/bin"

// RuntimeScriptProvider is an optional backend interface: materialise the runtime
// scripts this backend's sandboxes need into a host directory, so the launch path
// can deliver them rather than relying on the copies baked into the image.
//
// Implemented by the backends that bake — the four image backends — which are
// exactly the four the staleness applies to. tart and seatbelt deliberately do
// not implement it: tart already writes these scripts into the sandbox dir on
// every launch (writeVMSetupScripts) and seatbelt runs them as host processes,
// so both already deliver at launch and there is nothing to convert.
//
// Why a capability rather than orchestrator-owned content: four of the eleven
// scripts (the entrypoints, firewall.py, install-firewall.py) are container
// concerns embedded in runtime/docker, so the file set is genuinely backend-tier.
// containerd and apple delegate to that package, as they already do for
// ExpectedBaseChecksum, because they build their base from the same resources.
type RuntimeScriptProvider interface {
	// WriteRuntimeScripts writes the scripts into dir, creating it if needed, with
	// modes that a guest uid differing from the host's can still read and execute.
	// Overwrites in place: dir may hold an older binary's copies.
	WriteRuntimeScripts(dir string) error
}

// RuntimeScriptProviderOf returns rt as a RuntimeScriptProvider if the backend
// bakes runtime scripts into its images and therefore needs them delivered at
// launch instead; ok is false for backends that already deliver them.
func RuntimeScriptProviderOf(rt Backend) (RuntimeScriptProvider, bool) {
	p, ok := rt.(RuntimeScriptProvider)
	return p, ok
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
	PruneCache(ctx context.Context, includeImages, dryRun bool) (CachePruneResult, error)
}

// CachePruneResult reports what a cache prune reclaimed.
//
// BytesReclaimed is the headline and the reason this is not just a
// []PruneItem: most backends can only measure the total, by asking the daemon
// for its disk usage before and after. Items carry whatever individual
// resources the backend can name; Notices carry what is about the operation
// rather than any resource — a subcommand that failed, an estimate that could
// not be taken, a caveat about how the host actually frees the space.
type CachePruneResult struct {
	BytesReclaimed int64
	Items          []PruneItem
	Notices        []feedback.Notice
}

// PruneCacheFor calls rt.PruneCache if implemented; for backends without a
// cache it's a no-op returning a zero result.
func PruneCacheFor(ctx context.Context, rt Backend, includeImages, dryRun bool) (CachePruneResult, error) {
	if p, ok := rt.(CachePruner); ok {
		return p.PruneCache(ctx, includeImages, dryRun)
	}
	return CachePruneResult{}, nil
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
	PruneStaleBases(ctx context.Context, dryRun bool) (StaleBasePruneResult, error)
}

// StaleBasePruneResult reports the superseded bases found, and what became of
// each. Items carry Kind "stale-base"; the refs a caller used to receive as a
// []string are their Names, now with the action and any failure reason
// attached rather than printed past the caller on a writer.
type StaleBasePruneResult struct {
	BytesReclaimed int64
	Items          []PruneItem
	Notices        []feedback.Notice
}

// PruneStaleBasesFor calls rt.PruneStaleBases if implemented; for backends
// without superseded bases it's a no-op returning a zero result.
func PruneStaleBasesFor(ctx context.Context, rt Backend, dryRun bool) (StaleBasePruneResult, error) {
	if p, ok := rt.(StaleBasePruner); ok {
		return p.PruneStaleBases(ctx, dryRun)
	}
	return StaleBasePruneResult{}, nil
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
	// RequiredNetworkMode, when non-empty, is the InstanceConfig.NetworkMode the
	// sandbox must be created with for DialHost to be reachable — e.g. rootless
	// podman needs "slirp4netns:allow_host_loopback=true" so the container can
	// reach the host loopback at DialHost (10.0.2.2). "" means the backend's
	// default network already works (Linux docker/containerd: the bridge gateway).
	RequiredNetworkMode string
}

// ErrInjectorUnsupported is returned by InjectorReach when a backend that
// otherwise implements InjectorReachable cannot currently host a
// sandbox-reachable injector (e.g. rootless podman, whose bridge gateway lives
// in a network namespace the host can't bind — its slirp host-loopback path is
// not yet wired). The broker treats it like a non-reachable backend: auto mode
// falls back to direct delivery; an explicit --broker errors. Distinguished from
// a genuine reach failure (transient inspect error), which stays fatal so the
// real key is never silently delivered when brokering was expected to work.
var ErrInjectorUnsupported = errors.New("injector reachability not supported on this backend")

// InjectorReachable is an optional backend interface: report how a sandbox
// reaches a host-side credential injector. Backends that cannot host a
// sandbox-reachable host-side proxy do not implement it (or return
// ErrInjectorUnsupported), and the credential broker falls back to direct
// delivery for them (no flag-day, D105/D106).
type InjectorReachable interface {
	// InjectorReach reports the bind/dial endpoint for this backend's sandboxes.
	// It is a network-level property (the bridge gateway, a host loopback, …)
	// knowable before any container is created, so brokering can start the
	// injector ahead of the container, independent of the launch path. Returns
	// ErrInjectorUnsupported when the backend can't currently host an injector.
	InjectorReach(ctx context.Context) (InjectorReach, error)
}

// InjectorReachOf returns rt as an InjectorReachable if the backend implements one.
func InjectorReachOf(rt Backend) (InjectorReachable, bool) {
	r, ok := rt.(InjectorReachable)
	return r, ok
}

// NetnsSidecarSpec describes an ephemeral, privileged helper container that
// shares an existing instance's network namespace. It is the primitive behind
// tamper-resistant network isolation: the agent container is denied CAP_NET_ADMIN
// and the firewall is installed from this sidecar instead, so the agent — even as
// root via sudo — cannot flush a firewall installed from outside its reach.
// See docs/contributors/design/plans/tamper-resistant-network-isolation.md.
type NetnsSidecarSpec struct {
	// Target is the instance whose network namespace the sidecar joins
	// (docker/podman --network container:<target>).
	Target string
	// Image is the container image to run; "" means "reuse the target's image"
	// (resolved by the backend), so callers without an image ref handy (e.g. a
	// live network-allowlist patch) don't have to look it up.
	Image string
	// Argv is the full command to run; it overrides the image's entrypoint so the
	// sidecar runs the firewall installer (or an ipset patch), not the agent boot.
	Argv []string
	// Env holds additional KEY=VAL variables for the sidecar process.
	Env []string
	// CapAdd lists Linux capabilities to grant the sidecar (e.g. NET_ADMIN) — the
	// capabilities the agent container is deliberately denied.
	CapAdd []string
	// Mounts are bind mounts for the sidecar, in the same form the launch path
	// gives Create.
	//
	// A sidecar runs from an *image* with none of the target's mounts, so anything
	// the launch path delivers by mount rather than by baking is absent here unless
	// it is repeated. That is not a detail: the firewall installer runs as a
	// sidecar reading /yoloai/bin/install-firewall.py, so once the runtime scripts
	// are delivered at launch (RuntimeScriptProvider, DF156) the sidecar reads
	// whatever the profile image happened to bake — which is the very staleness
	// being removed, in the one place that fails the launch outright rather than
	// drifting quietly.
	Mounts []MountSpec
}

// NetnsSidecarRunner is an optional backend interface: run a short-lived helper
// container that shares a target instance's network namespace, blocking until it
// exits. Implemented by container backends that support netns sharing
// (docker, and podman via embedding). Returns a non-nil error on infrastructure
// failure OR a non-zero sidecar exit, with captured logs in the message — a
// failed firewall install must fail the operation, never run the agent unguarded.
type NetnsSidecarRunner interface {
	RunNetnsSidecar(ctx context.Context, spec NetnsSidecarSpec) error
}

// NetnsSidecarRunnerOf returns rt as a NetnsSidecarRunner if the backend
// implements one.
func NetnsSidecarRunnerOf(rt Backend) (NetnsSidecarRunner, bool) {
	r, ok := rt.(NetnsSidecarRunner)
	return r, ok
}

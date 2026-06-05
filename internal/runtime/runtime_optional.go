// ABOUTME: Optional Runtime capability interfaces — backend-specific extensions
// ABOUTME: a backend implements only when it departs from the host/container
// ABOUTME: default. Each is reached via a type-assert + `…For` fallback so no
// ABOUTME: concrete backend type leaks. The core Runtime lives in runtime.go.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime/caps"
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
func ResolveCopyMountFor(rt Runtime, sandboxName, hostPath string) string {
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
func ResolveGuestMountPathFor(rt Runtime, containerPath string) string {
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

// ===== 2. Capability probes & reporters =====

// UsernsProvider is an optional interface implemented by backends that need
// a non-default user namespace mode. Podman rootless uses "keep-id" to map
// the container uid to the host user; this also determines the tmux exec user
// (keep-id containers run as the host user, not as "yoloai").
type UsernsProvider interface {
	// UsernsMode returns the user namespace mode for a new container.
	// needsRootInContainer is true when the container needs real root inside
	// it — overlay mounts / recipe cap_add (CAP_SYS_ADMIN) or privileged
	// docker-in-docker — and therefore cannot use keep-id.
	// Returns "" for the default mode.
	UsernsMode(needsRootInContainer bool) string
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
func VMCensusFor(ctx context.Context, rt Runtime) (census VMCensus, ok bool, err error) {
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
func CacheUsageFor(ctx context.Context, rt Runtime) (CacheUsage, error) {
	if r, ok := rt.(DiskUsageReporter); ok {
		return r.CacheUsage(ctx)
	}
	return CacheUsage{CachedBytes: 0, ImageBytes: -1}, nil
}

// ===== 3. Optional operations =====

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
func PruneCacheFor(ctx context.Context, rt Runtime, includeImages, dryRun bool, output io.Writer) (int64, error) {
	if p, ok := rt.(CachePruner); ok {
		return p.PruneCache(ctx, includeImages, dryRun, output)
	}
	return 0, nil
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

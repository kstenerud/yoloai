// Package apple implements runtime.Runtime using Apple's `container` CLI.
// ABOUTME: Shells out to `container` for Linux OCI workloads in per-container VMs (macOS 26+).
package apple

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sysexec"
	"github.com/kstenerud/yoloai/yoerrors"
)

// minMacOSMajor is the lowest macOS major version we allow. Apple `container`
// technically runs on macOS 15 with limitations (no container-to-container net,
// no `container network`, IP conflicts), so we gate strictly on 26 (Tahoe) as a
// safe over-gate. See docs/contributors/design/plans/apple-container-backend.md (AC14).
const minMacOSMajor = 26

// containerBin is the CLI we shell out to.
const containerBin = "container"

// installHint is the install URL; a const (not descriptor.InstallHint) so probe
// can reference it without an initialization cycle through descriptor→probe.
const installHint = "https://github.com/apple/container"

// errNotImplemented marks the lifecycle/exec surface still being built out
// (step 2 of the apple-container plan: skeleton first, then fill in).
var errNotImplemented = errors.New("apple backend: not implemented yet")

// descriptor holds the static facts for the apple backend; shared by the
// registry registration and Runtime.Descriptor().
var descriptor = runtime.BackendDescriptor{
	Type:          runtime.BackendApple,
	Description:   "Apple container — Linux OCI in per-container VMs (macOS 26+)",
	Platforms:     []string{"darwin"},
	Architectures: []string{"arm64"},
	Requires:      "Apple container CLI + macOS 26 (Tahoe), Apple Silicon",
	InstallHint:   installHint,
	// Genuine per-container-VM isolation — vm tier, not the container slot.
	BaseModeName: runtime.IsolationModeVM,
	// Agent is installed via the OCI profile Dockerfile, not baked into the backend.
	AgentProvisionedByBackend: false,
	// No host.docker.internal analogue; callers fall back to the routable IP.
	HostFromContainer:       "",
	SupportedIsolationModes: nil,
	Capabilities: runtime.BackendCaps{
		NetworkIsolation: true, // in-guest iptables (own per-VM kernel) — verified
		OverlayDirs:      true, // overlayfs with --cap-add CAP_SYS_ADMIN — verified
		CapAdd:           true,
		HostFilesystem:   false,
		// Literal mount paths (no Tart-style remap) → the /yoloai default works.
		VMRuntimeDir: "",
	},
	Probe:         probe,
	VersionString: versionString,
	CleanupHint:   func(image string) string { return containerBin + " image delete " + image },
}

func init() {
	runtime.Register(func(ctx context.Context, layout config.Layout) (runtime.Runtime, error) {
		return New(ctx, layout)
	}, descriptor)
}

// probe reports the apple backend's availability tier. It is cheap: GOOS/GOARCH
// checks, a LookPath, and a cached `sw_vers` read for the macOS-major gate. It
// does NOT ask the apiserver whether it is running — that liveness check plus a
// start-on-demand happens at point-of-use (Setup), so the cheap probe never
// forks `container system status` on every dispatch. Hence Absent or Installed,
// never Running: an installed backend is "installed" and started when used.
func probe(_ context.Context, _ map[string]string) (runtime.ProbeStatus, string) {
	if !isMacOS() || !isAppleSilicon() {
		return runtime.ProbeAbsent, "apple container requires macOS on Apple Silicon"
	}
	if _, err := exec.LookPath(containerBin); err != nil {
		return runtime.ProbeAbsent, "container CLI not found (install from " + installHint + ")"
	}
	if major, err := macOSMajor(); err == nil && major < minMacOSMajor {
		return runtime.ProbeAbsent, fmt.Sprintf("apple container requires macOS %d or later (found %d)", minMacOSMajor, major)
	}
	return runtime.ProbeInstalled, "container CLI present (apiserver started on demand)"
}

// versionString returns the `container` CLI version for diagnostics. Minimal
// env (PATH only) per DEV §12 — version probes need no secrets.
func versionString(ctx context.Context) string {
	env := sysexec.Curated(nil, []string{"PATH"}, nil)
	out, err := sysexec.CommandContext(ctx, env, containerBin, "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Runtime implements runtime.Runtime by shelling out to the `container` CLI.
type Runtime struct {
	containerBin string        // resolved path to the container binary
	layout       config.Layout // DataDir-rooted path resolver (DEV §12)
	execEnv      []string      // explicit subprocess env; never inherited ambiently
}

// Compile-time check that the skeleton satisfies the interface.
var _ runtime.Runtime = (*Runtime)(nil)

// New constructs the apple Runtime after verifying platform, the CLI, and the
// macOS version gate. The apiserver is not started here — Setup does that on
// demand.
func New(_ context.Context, layout config.Layout) (*Runtime, error) {
	if !isMacOS() || !isAppleSilicon() {
		return nil, yoerrors.NewPlatformError("apple container backend requires macOS on Apple Silicon")
	}
	bin, err := exec.LookPath(containerBin)
	if err != nil {
		return nil, yoerrors.NewDependencyError("container CLI is not installed. Install it from %s", installHint)
	}
	if major, err := macOSMajor(); err == nil && major < minMacOSMajor {
		return nil, yoerrors.NewPlatformError("apple container backend requires macOS %d or later (found %d)", minMacOSMajor, major)
	}

	// TODO(step 6): replace with layout.Env().EnvForAppleContainer() once the
	// curated HostEnv keyset lands. PATH locates the binary+plugins; HOME backs
	// the default CONTAINER_APP_ROOT (~/Library/Application Support/...).
	execEnv := sysexec.Curated(nil, []string{"PATH", "HOME"}, nil)
	return &Runtime{containerBin: bin, layout: layout, execEnv: execEnv}, nil
}

// Descriptor returns the static facts for this backend.
func (r *Runtime) Descriptor() runtime.BackendDescriptor { return descriptor }

// Close releases resources. The CLI is stateless from our side, so this is a no-op.
func (r *Runtime) Close() error { return nil }

// DiagHint points the user at the container logs when an instance misbehaves.
func (r *Runtime) DiagHint(instanceName string) string {
	return fmt.Sprintf("container logs %s   (or: container system logs)", instanceName)
}

// TmuxSocket returns "" so exec'd processes use the uid-based default socket.
func (r *Runtime) TmuxSocket(_ string) string { return "" }

// AttachCommand returns the command to attach to the sandbox's tmux session.
func (r *Runtime) AttachCommand(tmuxSocket string, _ int, _ int, _ runtime.IsolationMode) []string {
	tmux := "tmux"
	if tmuxSocket != "" {
		tmux = "tmux -S " + tmuxSocket
	}
	return []string{containerBin, "exec", "-i", "-t", "INSTANCE", "sh", "-lc", tmux + " attach"}
}

// --- Lifecycle / exec: filled in by the next step-2 increment. ---

func (r *Runtime) Setup(_ context.Context, _ config.Layout, _ string, _ io.Writer, _ *slog.Logger, _ bool) error {
	return errNotImplemented
}

func (r *Runtime) IsReady(_ context.Context) (bool, error) { return false, nil }

func (r *Runtime) Create(_ context.Context, _ runtime.InstanceConfig) error { return errNotImplemented }

func (r *Runtime) Start(_ context.Context, _ string) error { return errNotImplemented }

func (r *Runtime) Stop(_ context.Context, _ string) error { return errNotImplemented }

func (r *Runtime) Remove(_ context.Context, _ string) error { return errNotImplemented }

func (r *Runtime) Inspect(_ context.Context, _ string) (runtime.InstanceInfo, error) {
	return runtime.InstanceInfo{}, errNotImplemented
}

func (r *Runtime) Exec(_ context.Context, _ string, _ []string, _ string) (runtime.ExecResult, error) {
	return runtime.ExecResult{}, errNotImplemented
}

func (r *Runtime) InteractiveExec(_ context.Context, _ string, _ []string, _ string, _ string, _ runtime.IOStreams) error {
	return errNotImplemented
}

func (r *Runtime) Prune(_ context.Context, _ []string, _ bool, _ io.Writer) (runtime.PruneResult, error) {
	return runtime.PruneResult{}, errNotImplemented
}

// --- platform helpers ---

func isMacOS() bool        { return goruntime.GOOS == "darwin" }
func isAppleSilicon() bool { return goruntime.GOOS == "darwin" && goruntime.GOARCH == "arm64" }

var (
	macOSMajorOnce sync.Once
	macOSMajorVal  int
	macOSMajorErr  error
)

// macOSMajor returns the host macOS major version (e.g. "26.1" → 26) via
// `sw_vers -productVersion`, cached for the process so the probe stays cheap
// after the first call. Only ever reached on darwin (callers gate on isMacOS).
func macOSMajor() (int, error) {
	macOSMajorOnce.Do(func() {
		env := sysexec.Curated(nil, []string{"PATH"}, nil)
		out, err := sysexec.Command(env, "sw_vers", "-productVersion").Output()
		if err != nil {
			macOSMajorErr = err
			return
		}
		s := strings.TrimSpace(string(out))
		if i := strings.IndexByte(s, '.'); i >= 0 {
			s = s[:i]
		}
		macOSMajorVal, macOSMajorErr = strconv.Atoi(s)
	})
	return macOSMajorVal, macOSMajorErr
}

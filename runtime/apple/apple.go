// Package apple implements runtime.Backend using Apple's `container` CLI.
// ABOUTME: Shells out to `container` for Linux OCI workloads in per-container VMs (macOS 26+).
package apple

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/sysexec"
	"github.com/kstenerud/yoloai/runtime"
	dockerrt "github.com/kstenerud/yoloai/runtime/docker"
	"github.com/kstenerud/yoloai/runtime/ptybridge"
	"github.com/kstenerud/yoloai/yoerrors"
)

// minMacOSMajor is the lowest macOS major version we allow. Apple `container`
// technically runs on macOS 15 with limitations (no container-to-container net,
// no `container network`, IP conflicts), so we gate strictly on 26 (Tahoe) as a
// safe over-gate. See docs/contributors/design/plans/apple-container-backend.md (AC14).
const minMacOSMajor = 26

// containerBin is the CLI we shell out to.
const containerBin = "container"

// buildErrorTailLines is how many trailing lines of a failed build's output are
// folded into the returned error, so the actionable cause rides on the error
// itself rather than only on the (maybe discarded) stream (DF144/DF145).
const buildErrorTailLines = 20

// installHint is the install URL; a const (not descriptor.InstallHint) so probe
// can reference it without an initialization cycle through descriptor→probe.
const installHint = "https://github.com/apple/container"

// baseImage is the local OCI image yoloai sandboxes run from.
const baseImage = "yoloai-base"

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
		// in-guest iptables (own per-VM kernel) — verified. IPv4 only; vmnet
		// hands the guest a ULA and no ip6tables rules exist (DF104).
		NetworkIsolation:   true,
		CapAdd:             true,
		HostFilesystem:     false,
		FilesystemLocality: runtime.LocalityHostSide,
		KeepAliveModel:     runtime.KeepAliveGuestOSInit,
		// Literal mount paths (no Tart-style remap) → the /yoloai default works.
		VMRuntimeDir: "",
		// Run copy-mode work-copy git inside the per-container VM (audit C1): the
		// work copy is host-readable (LocalityHostSide) but bind-mounted into the
		// guest, so dispatching git through GitExec keeps an agent-planted .git
		// filter/diff/fsmonitor driver executing inside the container, never on
		// the macOS host. Mirrors the container backends.
		GitExecInConfinement: true,
	},
	Probe:         probe,
	VersionString: versionString,
	CleanupHint:   func(image string) string { return containerBin + " image delete " + image },
}

func init() {
	runtime.Register(func(ctx context.Context, layout config.Layout) (runtime.Backend, error) {
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

// Runtime implements runtime.Backend by shelling out to the `container` CLI.
type Runtime struct {
	containerBin string        // resolved path to the container binary
	layout       config.Layout // DataDir-rooted path resolver (DEV §12)
	execEnv      []string      // explicit subprocess env; never inherited ambiently
}

// Compile-time check that the skeleton satisfies the interface.
var _ runtime.Backend = (*Runtime)(nil)
var _ runtime.InteractiveSession = (*Runtime)(nil)
var _ runtime.GitExecer = (*Runtime)(nil)

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

	// Curated host env for every `container` CLI invocation (DEV §12): PATH +
	// HOME + the CONTAINER_* roots/auth/debug knobs, no ambient inheritance.
	execEnv := layout.Env().EnvForAppleContainer()
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

// TmuxSocket pins an explicit, user-independent socket path inside the container
// (matching the docker backend, which shares this image + entrypoint), so every
// exec attaches to the same tmux server regardless of the user it runs as. With
// the uid-default socket, an exec as root would miss a server started as the
// yoloai user.
func (r *Runtime) TmuxSocket(_ string) string { return "/tmp/yoloai-tmux.sock" }

// AttachCommand returns the *in-container* command to attach to the "main" tmux
// session; the caller wraps it with `container exec` (so this must NOT start with
// `container`). Mirrors the docker backend — the guest is the same Linux image,
// and `script` gives tmux a clean PTY + controlling terminal.
func (r *Runtime) AttachCommand(tmuxSocket string, _ int, _ int, _ runtime.IsolationMode) []string {
	tmuxArgs := "exec tmux attach -t main"
	if tmuxSocket != "" {
		tmuxArgs = fmt.Sprintf("exec tmux -S %s attach -t main", tmuxSocket)
	}
	return []string{"/usr/bin/script", "-q", "-e", "-c", tmuxArgs, "/dev/null"}
}

// Create creates (but does not start) a container from the InstanceConfig. The
// apiserver holds the container record, so — unlike Tart — we don't persist an
// instance.json; Start just references the name. The image's ENTRYPOINT runs as
// the workload (we pass no command), matching the docker backend.
func (r *Runtime) Create(ctx context.Context, cfg runtime.InstanceConfig) error {
	// Pre-clear any stale container with this name from a previous failed run.
	_, _ = r.runContainer(ctx, "delete", "--force", cfg.Name)

	args := []string{"create", "--name", cfg.Name}
	if cfg.WorkingDir != "" {
		args = append(args, "-w", cfg.WorkingDir)
	}
	for _, e := range cfg.ContainerEnv {
		args = append(args, "-e", e)
	}
	for k, v := range cfg.Labels {
		args = append(args, "-l", k+"="+v)
	}
	for _, c := range cfg.CapAdd {
		args = append(args, "--cap-add", normalizeCap(c))
	}
	for _, m := range cfg.Mounts {
		// Use -v, not `--mount type=virtiofs`: -v bind-mounts both files and
		// directories, whereas `--mount type=virtiofs` rejects a file source
		// ("path '…' is not a directory"). yoloai mounts individual seed/
		// credential files (e.g. ~/.claude.json), so -v is required. See
		// backend-idiosyncrasies.md.
		spec := m.HostPath + ":" + m.ContainerPath
		if m.ReadOnly {
			spec += ":ro"
		}
		args = append(args, "-v", spec)
	}
	for _, p := range cfg.Ports {
		args = append(args, "-p", fmt.Sprintf("%d:%d", p.HostPort, p.ContainerPort))
	}
	if cfg.UseInit {
		args = append(args, "--init")
	}
	if cfg.Resources != nil {
		if cfg.Resources.Memory > 0 {
			args = append(args, "-m", strconv.FormatInt(cfg.Resources.Memory, 10))
		}
		if cpus := cfg.Resources.NanoCPUs / 1_000_000_000; cpus > 0 {
			args = append(args, "-c", strconv.FormatInt(cpus, 10))
		}
	}
	// NetworkMode "isolated" is enforced by in-guest iptables (entrypoint.py),
	// not a container network, so we leave networking at the per-VM default —
	// same as the docker backend.
	args = append(args, cfg.ImageRef)

	if _, err := r.runContainer(ctx, args...); err != nil {
		return fmt.Errorf("create container: %w", err)
	}
	return nil
}

// Start starts a created/stopped container. Idempotent: an already-running
// container returns nil; a missing one returns ErrNotFound.
func (r *Runtime) Start(ctx context.Context, name string) error {
	if _, err := r.runContainer(ctx, "start", name); err != nil {
		info, ierr := r.Inspect(ctx, name)
		switch {
		case errors.Is(ierr, runtime.ErrNotFound):
			return runtime.ErrNotFound
		case ierr == nil && info.Running:
			return nil // already running
		default:
			return fmt.Errorf("start container: %w", err)
		}
	}
	return nil
}

// Stop stops a running container. Returns nil if already stopped or absent.
func (r *Runtime) Stop(ctx context.Context, name string) error {
	if _, err := r.runContainer(ctx, "stop", name); err != nil {
		if info, ierr := r.Inspect(ctx, name); ierr != nil || !info.Running {
			return nil //nolint:nilerr // best-effort: an absent/already-stopped container is a successful Stop
		}
		return fmt.Errorf("stop container: %w", err)
	}
	return nil
}

// Remove deletes a container (force, so a running one is removed too). Returns
// nil if it's already gone.
func (r *Runtime) Remove(ctx context.Context, name string) error {
	if _, err := r.runContainer(ctx, "delete", "--force", name); err != nil {
		if _, ierr := r.Inspect(ctx, name); errors.Is(ierr, runtime.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("remove container: %w", err)
	}
	return nil
}

// Inspect returns the container's running state. The `container inspect` JSON is
// an array; state lives at [0].status.state (AC6: status is nested, not flat).
func (r *Runtime) Inspect(ctx context.Context, name string) (runtime.InstanceInfo, error) {
	out, err := r.runContainer(ctx, "inspect", name)
	if err != nil {
		return runtime.InstanceInfo{}, runtime.ErrNotFound
	}
	var arr []struct {
		Status struct {
			State string `json:"state"`
		} `json:"status"`
	}
	if err := json.Unmarshal([]byte(out), &arr); err != nil || len(arr) == 0 {
		return runtime.InstanceInfo{}, runtime.ErrNotFound
	}
	// Apple container has no state-to-disk suspend (AC14) → Suspended stays false.
	return runtime.InstanceInfo{Running: arr[0].Status.State == "running"}, nil
}

// Exec runs a command in a running container and returns its captured output
// and exit code.
func (r *Runtime) Exec(ctx context.Context, name string, cmd []string, user string) (runtime.ExecResult, error) {
	if info, err := r.Inspect(ctx, name); err != nil || !info.Running {
		return runtime.ExecResult{}, runtime.ErrNotRunning
	}
	args := []string{"exec"}
	if user != "" {
		args = append(args, "-u", user)
	}
	args = append(args, name)
	args = append(args, cmd...)
	c := sysexec.CommandContext(ctx, r.execEnv, r.containerBin, args...)
	return runtime.RunCmdExec(c)
}

// GitExec runs a git command against the copy-mode work copy INSIDE the
// container (audit C1). The work copy is bind-mounted into the guest at workDir
// (the dir's mirrored mount path, already resolved by the git package), so an
// agent-planted .git filter/diff/fsmonitor driver executes in the container,
// never on the host. Contract mirrors the other container backends: git's
// stdout is returned UNTRIMMED (patches are whitespace-sensitive), a non-zero
// exit becomes a *runtime.ExecError carrying stderr, and a stopped container
// yields runtime.ErrNotRunning. user matches the agent's container user so the
// work copy's ownership checks out. The base image already ships git (the
// container backends run work-copy git in it too), so no extra provisioning.
func (r *Runtime) GitExec(ctx context.Context, name, user, workDir string, args ...string) (string, error) {
	if info, err := r.Inspect(ctx, name); err != nil || !info.Running {
		return "", runtime.ErrNotRunning
	}

	gitArgs := append([]string{"git"}, runtime.GitHardeningArgs()...)
	gitArgs = append(gitArgs, "-C", workDir)
	gitArgs = append(gitArgs, args...)

	execArgs := []string{"exec"}
	if user != "" {
		execArgs = append(execArgs, "-u", user)
	}
	execArgs = append(execArgs, name)
	execArgs = append(execArgs, gitArgs...)

	// RunCmdExecRaw (not RunCmdExec) so git's exact bytes survive — Exec trims,
	// which would corrupt patches.
	c := sysexec.CommandContext(ctx, r.execEnv, r.containerBin, execArgs...)
	res, err := runtime.RunCmdExecRaw(c)
	return res.Stdout, err
}

// InteractiveExec runs a command interactively, bridging the supplied IOStreams
// to the container's stdio (PTY when streams.TTY). Non-zero exits surface as an
// *ExecError via ptybridge.Exec.
func (r *Runtime) InteractiveExec(ctx context.Context, name string, cmd []string, user, workDir string, streams runtime.IOStreams) error {
	args := []string{"exec"}
	if streams.TTY {
		args = append(args, "-i", "-t")
	} else {
		args = append(args, "-i")
	}
	if user != "" {
		args = append(args, "-u", user)
	}
	if workDir != "" {
		args = append(args, "-w", workDir)
	}
	args = append(args, name)
	args = append(args, cmd...)
	c := sysexec.CommandContext(ctx, r.execEnv, r.containerBin, args...)
	// container exec -t forces ONLCR on the host-local bridge slave, mangling the
	// guest app's bare-LF cursor moves; WithRemotePTY strips the injected CR. See
	// ptybridge.WithRemotePTY and backend-idiosyncrasies.md.
	return ptybridge.Exec(c, streams, ptybridge.WithRemotePTY())
}

// runContainer shells out to the `container` CLI, returning trimmed stdout or an
// error that carries the trimmed stderr for diagnosis.
func (r *Runtime) runContainer(ctx context.Context, args ...string) (string, error) {
	cmd := sysexec.CommandContext(ctx, r.execEnv, r.containerBin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("%w: %s", err, msg)
		}
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

// normalizeCap maps yoloai's docker-style cap names ("SYS_ADMIN") to the
// CAP_-prefixed form Apple's `--cap-add` expects ("CAP_SYS_ADMIN"); "ALL" and
// already-prefixed names pass through.
func normalizeCap(c string) string {
	if c == "ALL" || strings.HasPrefix(c, "CAP_") {
		return c
	}
	return "CAP_" + c
}

// Setup starts the apiserver and the builder, then builds yoloai-base from the
// shared base-image build context when it is missing or its inputs changed.
// Idempotent.
func (r *Runtime) Setup(ctx context.Context, layout config.Layout, sourceDir string, output io.Writer, logger *slog.Logger, force bool) error {
	// Start the apiserver and the (separate) builder VM on demand (AC3).
	if _, err := r.runContainer(ctx, "system", "start"); err != nil {
		return fmt.Errorf("start container system: %w", err)
	}
	if _, err := r.runContainer(ctx, "builder", "start"); err != nil {
		return fmt.Errorf("start container builder: %w", err)
	}

	exists := r.imageExists(ctx, baseImage)
	if force || !exists {
		if !exists {
			fmt.Fprintln(output, "Building base image (first run only, this may take a few minutes)...") //nolint:errcheck // best-effort progress
		}
		return r.buildBaseImage(ctx, layout, output, logger)
	}
	if dockerrt.NeedsBuild(layout, "apple") {
		fmt.Fprintln(output, "Base image resources updated, rebuilding...") //nolint:errcheck // best-effort progress
		return r.buildBaseImage(ctx, layout, output, logger)
	}
	return nil
}

// IsReady reports whether the yoloai-base image is present.
func (r *Runtime) IsReady(ctx context.Context) (bool, error) {
	return r.imageExists(ctx, baseImage), nil
}

// imageExists reports whether an image is present in the apple image store.
func (r *Runtime) imageExists(ctx context.Context, ref string) bool {
	_, err := r.runContainer(ctx, "image", "inspect", ref)
	return err == nil
}

// buildBaseImage materializes the shared build context into a temp directory and
// builds yoloai-base via `container build`. The context path is **absolute** — a
// relative `.` silently transfers an empty context and every COPY fails (AC1).
// Build inputs are the same embedded resources the docker backend uses, so
// staleness rides on the shared checksum marker.
func (r *Runtime) buildBaseImage(ctx context.Context, layout config.Layout, output io.Writer, logger *slog.Logger) error {
	dir, err := layout.MkdirTemp("yoloai-apple-build-")
	if err != nil {
		return fmt.Errorf("create build dir: %w", err)
	}
	defer os.RemoveAll(dir) //nolint:errcheck // best-effort temp cleanup

	if err := dockerrt.WriteBuildContextDir(dir); err != nil {
		return fmt.Errorf("write build context: %w", err)
	}
	logger.Debug("building yoloai-base via container build", "context", dir)

	cmd := sysexec.CommandContext(ctx, r.execEnv, r.containerBin, "build", "-t", baseImage, dir)
	// Stream to output as before, but also tee into a tail buffer so a failure's
	// actionable cause rides on the error itself, not only the (maybe discarded)
	// stream — same value on both so os/exec keeps its single-pipe path (DF145).
	tail := sysexec.NewTailBuffer(buildErrorTailLines)
	w := io.MultiWriter(output, tail)
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Run(); err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			return fmt.Errorf("container build exited with code %d%s", exitErr.ExitCode(), tail.ErrorSuffix())
		}
		return fmt.Errorf("container build: %w%s", err, tail.ErrorSuffix())
	}
	dockerrt.RecordBuildChecksum(layout, "apple")
	return nil
}

// Prune sweeps orphaned apple containers — instances this principal created that
// no longer correspond to a known sandbox. Mirrors the docker/containerd sweep:
// list with labels, filter by label equality, skip known, then stop+delete the
// rest. The base image is an OCI image (not a container), so it never appears in
// `container list` and needs no special exclusion.
func (r *Runtime) Prune(ctx context.Context, knownInstances []string, dryRun bool, output io.Writer) (runtime.PruneResult, error) {
	out, err := r.runContainer(ctx, "list", "--all", "--format", "json")
	if err != nil {
		return runtime.PruneResult{}, fmt.Errorf("list containers: %w", err)
	}

	orphans, err := orphanInstances(out, r.layout.Principal, knownInstances)
	if err != nil {
		return runtime.PruneResult{}, err
	}

	var result runtime.PruneResult
	for _, name := range orphans {
		if !dryRun {
			// delete --force handles a running container; stop first is best-effort.
			_, _ = r.runContainer(ctx, "stop", name)
			if _, derr := r.runContainer(ctx, "delete", "--force", name); derr != nil && !errors.Is(derr, runtime.ErrNotFound) {
				fmt.Fprintf(output, "Warning: failed to delete container %s: %v\n", name, derr) //nolint:errcheck // best-effort output
				continue
			}
		}
		result.Items = append(result.Items, runtime.PruneItem{Kind: "container", Name: name})
	}
	return result, nil
}

// containerListEntry is the subset of `container list --format json` this sweep
// reads: the instance name and the labels Create stamps via `-l k=v`. The daemon
// holds the labels, so they outlive the sandbox dir — which is precisely what
// makes label-based identification possible for apple and not for tart, whose
// labels live in the sandbox dir that an orphan by definition no longer has
// (DF124).
type containerListEntry struct {
	ID            string `json:"id"`
	Configuration struct {
		Labels map[string]string `json:"labels"`
	} `json:"configuration"`
}

// orphanInstances parses `container list --all --format json` and returns the
// names this principal created that aren't in known — the orphans to sweep.
// Candidates are identified by the canonical com.yoloai.* labels
// (runtime.IsOrphanCandidate, D62), never the yoloai-* name prefix: a foreign
// container merely named yoloai-* is left alone, and the principal label scopes
// the sweep by equality rather than containment, so another principal's
// instances are never reaped (DF19, DF115). Pure, so the filtering is testable
// without the live CLI. A parse failure is an error rather than an empty sweep —
// prune deletes things, so an unreadable listing must fail closed.
func orphanInstances(listOutput string, principal config.PrincipalSegment, known []string) ([]string, error) {
	var entries []containerListEntry
	if err := json.Unmarshal([]byte(listOutput), &entries); err != nil {
		return nil, fmt.Errorf("parse container list: %w", err)
	}
	knownSet := make(map[string]bool, len(known))
	for _, n := range known {
		knownSet[n] = true
	}
	var out []string
	for _, e := range entries {
		if e.ID == "" || knownSet[e.ID] {
			continue
		}
		if !runtime.IsOrphanCandidate(e.Configuration.Labels, principal) {
			continue
		}
		out = append(out, e.ID)
	}
	return out, nil
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

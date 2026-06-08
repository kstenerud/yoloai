// Package tart implements the runtime.Runtime interface using Tart VMs.
// ABOUTME: Shells out to the tart CLI for macOS VM lifecycle, exec, and image ops.
package tart

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/yoerrors"
)

// getXcodeSelectPath returns the active Xcode developer directory path from xcode-select.
// Returns empty string if xcode-select is not configured or fails.
func getXcodeSelectPath() string {
	cmd := exec.Command("xcode-select", "-p")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

// descriptor holds the static facts for the tart backend; shared by the
// registry registration and the Runtime.Descriptor() method.
var descriptor = runtime.BackendDescriptor{
	Type:                      runtime.BackendTart,
	Description:               "macOS VMs; native macOS env, strong isolation, heavier",
	Platforms:                 []string{"darwin"},
	Architectures:             []string{"arm64"}, // Apple Silicon only
	Requires:                  "Tart CLI installed, Apple Silicon Mac",
	InstallHint:               "brew install cirruslabs/cli/tart",
	BaseModeName:              runtime.IsolationModeVM,
	AgentProvisionedByBackend: true,
	AgentInstallMethod:        "native",
	// Prepend the provisioned tool dirs to PATH so the agent launches from a
	// non-login shell (tart exec bash -c does not source ~/.zprofile). Claude
	// Code is installed natively in ~/.local/bin; node@22 is keg-only at
	// /opt/homebrew/opt/node@22/bin. Mirrors the login PATH composed in the base
	// image's ~/.zprofile (see build.go provisionCommands).
	AgentLaunchPrefix:       `PATH="$HOME/.local/bin:/opt/homebrew/opt/node@22/bin:/opt/homebrew/bin:$PATH" `,
	SupportedIsolationModes: nil,
	Capabilities: runtime.BackendCaps{
		NetworkIsolation: false,
		OverlayDirs:      false,
		CapAdd:           false,
		HostFilesystem:   false,
		// Tart VMs use a VirtioFS share at "/Volumes/My Shared Files/yoloai"
		// (path contains spaces). The setup script creates a symlink
		// /Users/admin/.yoloai → /Volumes/My Shared Files/yoloai so that
		// shell commands inside the VM can reference state without quoting.
		VMRuntimeDir: "/Users/admin/.yoloai",
	},
	// Tart VMs boot in ~60s+, then sandbox-setup.py runs xcode-select and
	// xcodebuild -license accept before signalling secrets consumed. 180s is
	// a generous safety net; the marker normally appears within a few seconds
	// of the setup script starting (see sandbox-setup.py main() ordering).
	// See backend-idiosyncrasies.md for the host/VM deadlock that required
	// moving signal_secrets_consumed() before get_working_dir().
	SecretsConsumedTimeout: 180 * time.Second,
	Probe:                  probe,
	VersionString:          versionString,
}

// versionString returns tart's CLI version string.
func versionString(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "tart", "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// probe reports whether Tart is usable. Tart requires macOS on Apple Silicon
// and the `tart` binary on PATH. We don't run `tart --version` here — that's a
// fork+exec on every dispatch; LookPath suffices for "is it installed".
func probe(_ context.Context, _ map[string]string) (bool, string) {
	if !isMacOS() {
		return false, "tart requires macOS"
	}
	if !isAppleSilicon() {
		return false, "tart requires Apple Silicon"
	}
	if _, err := exec.LookPath("tart"); err != nil {
		return false, "tart binary not found (install with: brew install cirruslabs/cli/tart)"
	}
	return true, ""
}

func init() {
	runtime.Register(func(ctx context.Context, layout config.Layout) (runtime.Runtime, error) {
		return New(ctx, layout)
	}, descriptor)
}

const (
	// pidFileName stores the tart run process ID.
	pidFileName = "tart.pid"

	// vmLogFileName captures tart run stderr for debugging.
	vmLogFileName = "vm.log"

	// tartConfigFileName stores the instance config for Start to use.
	tartConfigFileName = "instance.json"

	// backendDir holds backend-specific files within the sandbox directory.
	backendDir = config.BackendDirName

	// binDir holds executable scripts within the sandbox directory.
	binDir = config.BinDirName

	// tmuxDir holds tmux configuration within the sandbox directory.
	tmuxDir = config.TmuxDirName

	// sharedDirName is the VirtioFS share name used for yoloai state.
	sharedDirName = "yoloai"

	// sharedDirVMPath is where VirtioFS shares appear inside the macOS VM.
	sharedDirVMPath = "/Volumes/My Shared Files"

	// tartVMLimitSubstr is the fixed prefix Tart writes to stderr when Apple's
	// VZError.virtualMachineLimitExceeded fires. Detection is substring-based
	// to tolerate the optional "(other running VMs: ...)" suffix.
	tartVMLimitSubstr = "The number of VMs exceeds the system limit"
)

// Runtime implements runtime.Runtime using the Tart CLI.
type Runtime struct {
	tartBin           string        // path to tart binary
	layout            config.Layout // DataDir-rooted path resolver (Q-W.6)
	homeDir           string        // host home directory (layout.HomeDir); used for ~ expansion
	baseImageOverride string        // custom base image from config (tart.image)
}

// Compile-time check.
var _ runtime.Runtime = (*Runtime)(nil)
var _ runtime.CopyMountResolver = (*Runtime)(nil)

// Descriptor returns a BackendDescriptor with the static facts for this backend.
func (r *Runtime) Descriptor() runtime.BackendDescriptor {
	return descriptor
}

// ResolveCopyMount returns the local VM path where the copy directory will be
// stored. The actual directory is first staged via VirtioFS, then copied to local
// VM storage during SetupWorkDirInVM. The path is always a guest VM path
// (not a host path), so no layout is needed here.
func (r *Runtime) ResolveCopyMount(sandboxName, hostPath string) string {
	encoded := config.EncodePath(hostPath)
	return filepath.Join("/Users/admin/yoloai-work", encoded)
}

// ResolveGuestMountPath translates a container-side mount target to the path
// where the mount is actually reachable inside the VM guest (e.g. host dirs are
// re-rooted under /Users/admin/host/...). Idempotent: already-translated guest
// paths are returned unchanged, so the result is safe to store in metadata and
// re-resolve on restart/reset.
func (r *Runtime) ResolveGuestMountPath(containerPath string) string {
	return remapTargetPath(containerPath)
}

// SetupWorkDirInVM returns shell commands to copy from VirtioFS staging
// to local VM storage and create git baseline. Called during Create/Reset.
func (r *Runtime) SetupWorkDirInVM(virtiofsStagingPath, vmLocalPath string) []string {
	return []string{
		fmt.Sprintf("mkdir -p '%s'", filepath.Dir(vmLocalPath)),
		fmt.Sprintf("rsync -a '%s/' '%s/'", virtiofsStagingPath, vmLocalPath),
		fmt.Sprintf("cd '%s' && git init && git add -A && git commit --allow-empty -m 'baseline'", vmLocalPath),
	}
}

// New creates a Runtime after verifying that tart is installed and the
// platform is supported (macOS with Apple Silicon). layout is used for all
// host-path resolution so the backend never reads ambient HOME.
func New(_ context.Context, layout config.Layout) (*Runtime, error) {
	// Platform checks first — no amount of software installation can fix these.
	if !isMacOS() {
		return nil, yoerrors.NewPlatformError("tart backend requires macOS with Apple Silicon")
	}
	if !isAppleSilicon() {
		return nil, yoerrors.NewPlatformError("tart backend requires Apple Silicon (M1 or later)")
	}

	tartBin, err := exec.LookPath("tart")
	if err != nil {
		return nil, yoerrors.NewDependencyError("tart is not installed. Install it with: brew install cirruslabs/cli/tart")
	}

	// Read config for optional tart.image override
	var baseImageOverride string
	if cfg, err := config.LoadConfig(layout); err == nil && cfg.TartImage != "" {
		baseImageOverride = cfg.TartImage
	}

	return &Runtime{
		tartBin:           tartBin,
		layout:            layout,
		homeDir:           layout.HomeDir,
		baseImageOverride: baseImageOverride,
	}, nil
}

// Create creates a new VM instance by cloning the base image and writing
// the instance config to the sandbox directory.
func (r *Runtime) Create(ctx context.Context, cfg runtime.InstanceConfig) error {
	slog.Debug("tart Create: starting", "name", cfg.Name, "imageRef", cfg.ImageRef)
	// Stop any stale processes and remove leftover VM (idempotent)
	r.stopVM(ctx, cfg.Name)
	if r.vmExists(ctx, cfg.Name) {
		slog.Debug("tart Create: deleting existing VM", "name", cfg.Name)
		if _, err := r.runTart(ctx, "delete", cfg.Name); err != nil {
			return fmt.Errorf("remove existing VM: %w", err)
		}
	}

	// Clone the base image to create an instance-specific VM
	slog.Debug("tart Create: cloning", "image_ref", cfg.ImageRef, "name", cfg.Name)
	if _, err := r.runTart(ctx, "clone", cfg.ImageRef, cfg.Name); err != nil {
		return fmt.Errorf("clone VM: %w", err)
	}
	slog.Debug("tart Create: clone succeeded", "name", cfg.Name)

	// Save instance config so Start can read mounts/network/ports
	sandboxPath := filepath.Join(r.layout.SandboxesDir(), sandboxName(cfg.Name))
	cfgData, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal instance config: %w", err)
	}
	// Ensure backend dir exists
	if err := fileutil.MkdirAll(filepath.Join(sandboxPath, backendDir), 0750); err != nil {
		return fmt.Errorf("create backend dir: %w", err)
	}
	if err := fileutil.WriteFile(filepath.Join(sandboxPath, backendDir, tartConfigFileName), cfgData, 0600); err != nil {
		return fmt.Errorf("write instance config: %w", err)
	}

	// Copy secrets from mount spec into the sandbox secrets dir.
	// VirtioFS only supports directories, not individual files, so the
	// /run/secrets directory mount created by buildMounts is inaccessible
	// inside the VM. We copy them into the sandbox directory, which is shared
	// via the yoloai VirtioFS share, so sandbox-setup.py can read them.
	if err := copySecretsToSandbox(sandboxPath, cfg.Mounts); err != nil {
		return err
	}

	// Add mount_map to runtime-config.json for symlink creation in the guest.
	// VirtioFS mounts appear at /Volumes/My Shared Files/<name>/, but we need
	// them at standard system paths (e.g., /Applications/Xcode.app).
	if err := r.addMountMapToConfig(sandboxPath, cfg.Mounts); err != nil {
		return fmt.Errorf("add mount map to config: %w", err)
	}

	return nil
}

// Start boots the VM in the background and runs the setup script.
// If the VM is currently suspended, tart run resumes it rather than doing a
// fresh boot. The stale tmux session from before suspension is killed so that
// runSetupScript starts a fresh agent — preserving the work directory while
// giving the agent a clean process state.
func (r *Runtime) Start(ctx context.Context, name string) error {
	slog.Debug("tart Start", "name", name)
	sandboxPath := filepath.Join(r.layout.SandboxesDir(), sandboxName(name))

	// Check if already running
	if r.isRunning(ctx, name) {
		return nil
	}

	// Record whether the VM is suspended before tart run resumes it.
	// Used after boot to decide whether to kill the stale tmux session.
	wasSuspended := r.vmState(ctx, name) == "suspended"

	// Load instance config saved by Create
	var cfg runtime.InstanceConfig
	cfgPath := filepath.Join(sandboxPath, backendDir, tartConfigFileName)
	cfgData, err := os.ReadFile(cfgPath) //nolint:gosec // G304: path within sandbox dir
	if err != nil {
		return fmt.Errorf("read instance config: %w", err)
	}
	if err := json.Unmarshal(cfgData, &cfg); err != nil {
		return fmt.Errorf("parse instance config: %w", err)
	}

	// Build tart run arguments. When resuming from a suspended state, the VM
	// snapshot already contains the VirtioFS configuration — passing --dir args
	// again causes VZErrorDomain Code=12 "permission denied" from the
	// Virtualization.framework restore path. Use minimal args for resume.
	var args []string
	if wasSuspended {
		args = []string{"run", "--no-graphics", name}
	} else {
		args = r.buildRunArgs(name, sandboxPath, cfg.Mounts)
	}

	// Open log file for stderr capture
	logPath := filepath.Join(sandboxPath, backendDir, vmLogFileName)
	logFile, err := fileutil.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600) //nolint:gosec // G304: sandboxPath is ~/.yoloai/sandboxes/<name>
	if err != nil {
		return fmt.Errorf("open VM log: %w", err)
	}

	// Start tart run as a background process.
	// Use exec.Command (not CommandContext) because tart run is a long-lived
	// process that must survive after Start returns. CommandContext would kill
	// it when the parent's context is cancelled.
	cmd := exec.Command(r.tartBin, args...) //nolint:gosec // G204: args are constructed from validated config
	cmd.Stderr = logFile
	cmd.Stdout = logFile
	// Detach the process from the parent so it survives
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	if err := cmd.Start(); err != nil {
		logFile.Close() //nolint:errcheck,gosec // best-effort
		return fmt.Errorf("start VM: %w", err)
	}
	slog.Debug("tart run started", "name", name, "pid", cmd.Process.Pid)

	// Write PID file
	pidPath := filepath.Join(sandboxPath, backendDir, pidFileName)
	if err := fileutil.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0600); err != nil {
		// Kill the process we just started if we can't track it
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		logFile.Close() //nolint:errcheck,gosec // best-effort
		return fmt.Errorf("write PID file: %w", err)
	}

	// Monitor the tart run process in the background so we can detect
	// early exits. The channel receives the error (nil on clean exit).
	procDone := make(chan error, 1)
	go func() {
		procDone <- cmd.Wait()
		logFile.Close() //nolint:errcheck,gosec // best-effort
	}()

	// Wait for VM to become accessible, or detect early process exit
	if err := r.waitForBoot(ctx, name, procDone); err != nil {
		// Attempt cleanup
		r.killByPID(sandboxPath)
		// Include log file contents and the command for diagnostics
		detail := fmt.Sprintf("command: %s %s", r.tartBin, strings.Join(args, " "))
		if logData, readErr := os.ReadFile(logPath); readErr == nil && len(logData) > 0 { //nolint:gosec // G304: path within sandbox dir
			if limitErr := checkVMLimitError(string(logData)); limitErr != nil {
				return limitErr
			}
			detail += fmt.Sprintf("\nVM log output:\n%s", strings.TrimSpace(string(logData)))
		}
		return fmt.Errorf("wait for VM boot: %w\n%s", err, detail)
	}
	slog.Debug("tart Start: waitForBoot succeeded", "name", name)

	// Brief delay to let the VM fully stabilize after first successful exec.
	// Tart's guest agent may need a moment to be fully ready for complex commands.
	slog.Debug("tart Start: sleeping 500ms for stabilization", "name", name)
	time.Sleep(500 * time.Millisecond)
	slog.Debug("tart Start: checking if VM is still running", "name", name, "isRunning", r.isRunning(ctx, name))

	// When resuming from suspend, kill the stale tmux session so that the setup
	// script starts a fresh agent. The work directory is preserved by the suspend.
	if wasSuspended {
		slog.Debug("tart Start: killing stale tmux session after suspend resume", "name", name)
		args := execArgs(name, "bash", "-c", "tmux kill-server 2>/dev/null; true")
		_, _ = r.runTart(ctx, args...)
	}

	// Deliver setup script via shared directory and run it
	slog.Debug("tart Start: calling runSetupScript", "name", name)
	if err := r.runSetupScript(ctx, name, sandboxPath, cfg.Mounts); err != nil {
		return fmt.Errorf("run setup script: %w", err)
	}

	return nil
}

// Stop hard-stops the VM. Apple's Virtualization.framework cannot restore VMs
// that had VirtioFS (--dir) mounts from a suspend snapshot (VZErrorDomain Code=12),
// so suspend-on-stop provides no benefit: Start always recreates from staging anyway.
// Using a hard stop keeps Stop fast and avoids a 15-45s penalty per stop call.
func (r *Runtime) Stop(ctx context.Context, name string) error {
	r.stopVM(ctx, name)
	return nil
}

// Remove deletes the VM and cleans up the PID file.
// Uses a hard stop (not suspend) before deleting — suspending before an
// immediate delete would waste time writing RAM state to disk.
func (r *Runtime) Remove(ctx context.Context, name string) error {
	sandboxPath := filepath.Join(r.layout.SandboxesDir(), sandboxName(name))

	// Hard stop first (don't suspend — state is about to be deleted)
	r.stopVM(ctx, name)

	if !r.vmExists(ctx, name) {
		// Clean up stale PID file
		_ = os.Remove(filepath.Join(sandboxPath, backendDir, pidFileName))
		return nil
	}

	if _, err := r.runTart(ctx, "delete", name); err != nil {
		return fmt.Errorf("delete VM: %w", err)
	}

	_ = os.Remove(filepath.Join(sandboxPath, backendDir, pidFileName))
	_ = os.RemoveAll(filepath.Join(sandboxPath, "secrets"))

	return nil
}

// Inspect returns the current state of the VM instance.
func (r *Runtime) Inspect(ctx context.Context, name string) (runtime.InstanceInfo, error) {
	state := r.vmState(ctx, name)
	if state == "" {
		return runtime.InstanceInfo{}, runtime.ErrNotFound
	}
	return runtime.InstanceInfo{
		Running:   state == "running",
		Suspended: state == "suspended",
	}, nil
}

// Exec runs a command inside the VM via tart exec and returns the result.
// The user parameter is ignored — tart exec runs as the VM's logged-in user.
func (r *Runtime) Exec(ctx context.Context, name string, cmd []string, _ string) (runtime.ExecResult, error) {
	if !r.isRunning(ctx, name) {
		return runtime.ExecResult{}, runtime.ErrNotRunning
	}

	args := execArgs(name, cmd...)

	slog.Debug("tart Exec", "vm", name, "args", args)

	c := exec.CommandContext(ctx, r.tartBin, args...) //nolint:gosec // G204: vmName and cmd are from validated sandbox state

	return runtime.RunCmdExec(c)
}

// ExecRaw is like Exec but preserves exact stdout/stderr without trimming
// whitespace. Use this for commands whose output is whitespace-sensitive.
func (r *Runtime) ExecRaw(ctx context.Context, name string, cmd []string, _ string) (runtime.ExecResult, error) {
	if !r.isRunning(ctx, name) {
		return runtime.ExecResult{}, runtime.ErrNotRunning
	}

	args := execArgs(name, cmd...)

	slog.Debug("tart ExecRaw", "vm", name, "args", args)

	c := exec.CommandContext(ctx, r.tartBin, args...) //nolint:gosec // G204: vmName and cmd are from validated sandbox state

	return runtime.RunCmdExecRaw(c)
}

// translateWorkDirToVMPath translates host sandbox work paths to VM paths.
// Host path pattern: ~/.yoloai/sandboxes/<name>/work/<encoded>/
// VM path pattern: /Users/admin/yoloai-work/<encoded>/
// If workDir is already a VM path or not a sandbox work path, returns it unchanged.
func (r *Runtime) translateWorkDirToVMPath(workDir string) string {
	// Already a VM path — no translation needed
	if strings.HasPrefix(workDir, "/Users/admin/yoloai-work/") {
		return workDir
	}

	// Check if this is a host sandbox work path
	// Pattern: ~/.yoloai/sandboxes/<name>/work/<encoded>
	sandboxesDir := r.layout.SandboxesDir()
	// Normalize by resolving ~ if present
	if strings.HasPrefix(workDir, "~/") {
		workDir = filepath.Join(r.homeDir, workDir[2:])
	}

	// Check if path starts with sandboxes dir
	if !strings.HasPrefix(workDir, sandboxesDir+string(filepath.Separator)) {
		return workDir // Not a sandbox work path
	}

	// Extract path components after sandboxes dir
	// Pattern: <sandboxesDir>/<sandboxName>/work/<encodedPath>
	relPath := strings.TrimPrefix(workDir, sandboxesDir+string(filepath.Separator))
	parts := strings.Split(relPath, string(filepath.Separator))

	// Need at least 3 parts: <sandboxName>/work/<encodedPath>
	if len(parts) < 3 || parts[1] != "work" {
		return workDir // Not a work directory
	}

	// Extract the encoded path (everything after "work/")
	encodedPath := filepath.Join(parts[2:]...)

	// Construct VM path
	return filepath.Join("/Users/admin/yoloai-work", encodedPath)
}

// GitExec runs a git command inside the VM (Tart uses VM filesystem).
// workDir may be either a host path (~/.yoloai/sandboxes/<name>/work/<encoded>)
// or a VM path (/Users/admin/yoloai-work/<encoded>). Host paths are translated
// to VM paths automatically.
// name may be a sandbox name or instance name; both are accepted.
func (r *Runtime) GitExec(ctx context.Context, name, workDir string, args ...string) (string, error) {
	// Callers in the sandbox package pass the sandbox name (e.g. "mybox").
	// The Tart VM is named with the instance prefix (e.g. "yoloai-mybox").
	vmName := instancePrefix + strings.TrimPrefix(name, instancePrefix)
	if !r.isRunning(ctx, vmName) {
		return "", runtime.ErrNotRunning
	}

	// Translate host sandbox work paths to VM paths.
	// Callers may pass either host paths (~/.yoloai/sandboxes/<name>/work/<encoded>)
	// or VM paths (/Users/admin/yoloai-work/<encoded>).
	// Other backends (Docker, Containerd) run git on the host, so they use host paths.
	// Tart runs git inside the VM, so it needs VM paths.
	actualWorkDir := r.translateWorkDirToVMPath(workDir)

	// Build git command with -C workDir
	gitArgs := append([]string{"-c", "core.hooksPath=/dev/null", "-C", actualWorkDir}, args...)
	cmd := append([]string{"git"}, gitArgs...)

	// Use ExecRaw to preserve exact git output (patches are whitespace-sensitive)
	result, err := r.ExecRaw(ctx, vmName, cmd, "")
	if err != nil {
		return "", err
	}
	return result.Stdout, nil
}

// InteractiveExec runs a command interactively inside the VM by shelling
// out to `tart exec`. IOStreams determines whether a PTY is allocated and
// where stdio is wired. The user and workDir params are ignored — tart
// exec runs as the VM's logged-in user in its default cwd.
func (r *Runtime) InteractiveExec(ctx context.Context, name string, cmd []string, _ string, _ string, streams runtime.IOStreams) error {
	args := []string{"exec"}
	if streams.TTY {
		// -i attaches stdin, -t allocates the VM-side PTY (like docker exec -it).
		args = append(args, "-i", "-t")
	} else {
		args = append(args, "-i")
	}
	args = append(args, name)
	args = append(args, cmd...)

	c := exec.CommandContext(ctx, r.tartBin, args...) //nolint:gosec // G204: name and cmd are from validated sandbox state
	// PTYBridgeExec wraps the child in a local host PTY. tart already allocates a
	// PTY inside the VM with -t, so this is a double-PTY (local + remote, like
	// `script ssh -t`) — it works and gives uniform raw-mode handling at the CLI
	// boundary, but can only be exercised on a macOS host with Tart installed.
	return runtime.PTYBridgeExec(c, streams)
}

// Close is a no-op for Tart (no persistent client connection).
func (r *Runtime) Close() error {
	return nil
}

// DiagHint returns a Tart-specific hint for checking logs.
func (r *Runtime) DiagHint(instanceName string) string {
	logPath := filepath.Join(r.layout.SandboxesDir(), sandboxName(instanceName), backendDir, vmLogFileName)
	return fmt.Sprintf("check VM log at %s", logPath)
}

// PrepareAgentCommand prepends the backend's constant launch wrap (see
// descriptor.AgentLaunchPrefix) so the agent launches correctly from a
// non-login shell.
func (r *Runtime) PrepareAgentCommand(cmd string) string {
	return descriptor.AgentLaunchPrefix + cmd
}

// TmuxSocket returns the explicit tmux socket path for Tart VMs.
// When tart exec allocates a PTY with -t, the environment changes (TMPDIR)
// prevent tmux from finding its socket at the default location. We must
// specify the socket explicitly with -S. The admin user in Tart VMs has
// UID 501, so the socket is at /private/tmp/tmux-501/default.
// sandboxDir is ignored (socket is inside the VM, not on host).
func (r *Runtime) TmuxSocket(_ string) string { return "/private/tmp/tmux-501/default" }

// AttachCommand returns the command to attach to the tmux session in a tart VM.
// Tart runs commands directly with the caller's terminal; no script wrapper
// needed (and macOS BSD script does not support the GNU -c flag).
func (r *Runtime) AttachCommand(tmuxSocket string, _ int, _ int, _ runtime.IsolationMode) []string {
	cmd := []string{"tmux"}
	if tmuxSocket != "" {
		cmd = append(cmd, "-S", tmuxSocket)
	}
	return append(cmd, "attach", "-t", "main")
}

// instancePrefix is prepended to sandbox names by the sandbox package
// to form instance names. We strip it to recover the sandbox name for
// constructing file-system paths.
const instancePrefix = "yoloai-"

// sandboxName strips the instance prefix to recover the sandbox name.
func sandboxName(instanceName string) string {
	return strings.TrimPrefix(instanceName, instancePrefix)
}

// buildRunArgs constructs the arguments for tart run.
// Only directories outside the sandbox path get their own VirtioFS share;
// everything under sandboxPath is already accessible via the yoloai share.
// System paths (Xcode, iOS Simulators) are auto-detected at every start.
func (r *Runtime) buildRunArgs(vmName, sandboxPath string, mounts []runtime.MountSpec) []string {
	args := []string{"run", "--no-graphics"}

	// Share the sandbox directory into the VM
	args = append(args, "--dir", fmt.Sprintf("%s:%s", sharedDirName, sandboxPath))

	// Build merged mount list: Xcode system paths + user-specified mounts
	// Deduplication: user-specified mounts take precedence over system paths
	mergedMounts := make(map[string]runtime.MountSpec) // key = Source path

	// 1. Add Xcode system paths (checked at every start)
	// NOTE: CoreSimulator/Volumes is NOT mounted because CoreSimulator cannot
	// discover runtimes from VirtioFS mounts. Runtimes must be copied locally.
	var xcodePaths []struct {
		host string
		name string
	}

	// Detect active Xcode via xcode-select (supports multiple Xcodes, custom paths)
	if xcodeDevPath := getXcodeSelectPath(); xcodeDevPath != "" {
		// xcode-select returns: /Applications/Xcode.app/Contents/Developer
		// We need: /Applications/Xcode.app
		xcodePath := filepath.Dir(filepath.Dir(xcodeDevPath))
		mountName := "m-" + filepath.Base(xcodePath)
		xcodePaths = append(xcodePaths, struct{ host, name string }{xcodePath, mountName})

		// Also mount PrivateFrameworks from the same Xcode installation
		privateFrameworks := filepath.Join(filepath.Dir(xcodeDevPath), "PrivateFrameworks")
		if info, err := os.Stat(privateFrameworks); err == nil && info.IsDir() {
			xcodePaths = append(xcodePaths, struct{ host, name string }{privateFrameworks, "m-PrivateFrameworks"})
		}
	}

	for _, p := range xcodePaths {
		if info, err := os.Stat(p.host); err == nil && info.IsDir() {
			mergedMounts[p.host] = runtime.MountSpec{
				HostPath:      p.host,
				ContainerPath: filepath.Join(sharedDirVMPath, p.name),
				ReadOnly:      true,
			}
		}
	}

	// 2. Add user-specified mounts (override system paths if same Source)
	for _, m := range mounts {
		// Skip anything under the sandbox dir (already shared)
		if strings.HasPrefix(m.HostPath, sandboxPath+"/") || m.HostPath == sandboxPath {
			continue
		}
		// Skip files — VirtioFS only supports directories
		if info, err := os.Stat(m.HostPath); err != nil || !info.IsDir() {
			continue
		}
		mergedMounts[m.HostPath] = m // Overwrites system path if duplicate
	}

	// 3. Build --dir arguments from merged list
	for _, m := range mergedMounts {
		dirName := mountDirName(m.HostPath)
		dirSpec := fmt.Sprintf("%s:%s", dirName, m.HostPath)
		if m.ReadOnly {
			dirSpec += ":ro"
		}
		args = append(args, "--dir", dirSpec)
	}

	return append(args, vmName)
}

// mountDirName generates a VirtioFS share name from a host path.
func mountDirName(hostPath string) string {
	// Use the last path component, prefixed to avoid collisions
	return "m-" + filepath.Base(hostPath)
}

// BuildNetworkArgs returns network-related arguments for tart run based on
// the InstanceConfig. Exported for testing.
func BuildNetworkArgs(cfg runtime.InstanceConfig) []string {
	var args []string

	switch {
	case cfg.NetworkMode == "none" && len(cfg.Ports) > 0:
		// Isolated network with specific port forwarding
		args = append(args, "--net-softnet")
		args = append(args, "--net-softnet-block=0.0.0.0/0")
		args = append(args, "--net-softnet-block=::/0")
		for _, p := range cfg.Ports {
			proto := p.Protocol
			if proto == "" {
				proto = "tcp"
			}
			args = append(args, fmt.Sprintf("--net-softnet-allow=%d/%s", p.ContainerPort, proto))
		}
		args = append(args, portForwardArgs(cfg.Ports)...)

	case cfg.NetworkMode == "none":
		// Fully isolated: block all traffic
		args = append(args, "--net-softnet")
		args = append(args, "--net-softnet-block=0.0.0.0/0")
		args = append(args, "--net-softnet-block=::/0")

	case len(cfg.Ports) > 0:
		// Port forwarding with default networking
		args = append(args, "--net-softnet")
		args = append(args, portForwardArgs(cfg.Ports)...)
	}

	return args
}

// portForwardArgs builds --net-softnet-expose flags from port mappings.
func portForwardArgs(ports []runtime.PortMapping) []string {
	if len(ports) == 0 {
		return nil
	}

	var pairs []string
	for _, p := range ports {
		pairs = append(pairs, fmt.Sprintf("%d:%d", p.HostPort, p.ContainerPort))
	}
	return []string{"--net-softnet-expose=" + strings.Join(pairs, ",")}
}

// execArgs builds the arguments for tart exec.
// tart exec syntax: tart exec <vm-name> <command> [args...]
func execArgs(vmName string, cmd ...string) []string {
	args := []string{"exec", vmName}
	return append(args, cmd...)
}

// runTart executes a tart command and returns stdout.
func (r *Runtime) runTart(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, r.tartBin, args...) //nolint:gosec // G204: args are constructed internally
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		stderrStr := strings.TrimSpace(stderr.String())
		return "", mapTartError(err, stderrStr)
	}

	return strings.TrimSpace(stdout.String()), nil
}

// vmState returns the tart state string for the named VM: "running",
// "suspended", "stopped", or "" if the VM does not exist.
func (r *Runtime) vmState(ctx context.Context, vmName string) string {
	out, err := r.runTart(ctx, "list", "--format", "json")
	if err != nil {
		return ""
	}
	var entries []struct {
		Name  string `json:"Name"`
		State string `json:"State"`
	}
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		return ""
	}
	for _, e := range entries {
		if e.Name == vmName {
			return e.State
		}
	}
	return ""
}

// vmExists checks whether a VM with the given name exists in tart's inventory.
func (r *Runtime) vmExists(ctx context.Context, vmName string) bool {
	return r.vmState(ctx, vmName) != ""
}

// isRunning checks if the VM is running by attempting a trivial exec.
func (r *Runtime) isRunning(ctx context.Context, vmName string) bool {
	args := execArgs(vmName, "true")
	cmd := exec.CommandContext(ctx, r.tartBin, args...) //nolint:gosec // G204
	return cmd.Run() == nil
}

// waitForBoot polls until the VM responds to tart exec or the timeout expires.
// Returns immediately on fatal errors (VM not found, bad command syntax) or
// if the tart run process exits early (procDone fires).
func (r *Runtime) waitForBoot(ctx context.Context, vmName string, procDone <-chan error) error {
	deadline := time.Now().Add(bootTimeout)
	var lastErr error

	for {
		if time.Now().After(deadline) {
			if lastErr != nil {
				return fmt.Errorf("vm did not become accessible within %s: %w", bootTimeout, lastErr)
			}
			return fmt.Errorf("vm did not become accessible within %s", bootTimeout)
		}

		// Check if tart run process exited early
		select {
		case procErr := <-procDone:
			if procErr != nil {
				return fmt.Errorf("tart run exited: %w", procErr)
			}
			return fmt.Errorf("tart run exited unexpectedly with no error")
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Try a simple command via tart exec
		args := execArgs(vmName, "true")
		cmd := exec.CommandContext(ctx, r.tartBin, args...) //nolint:gosec // G204
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		err := cmd.Run()
		if err == nil {
			return nil
		}

		stderrStr := strings.TrimSpace(stderr.String())
		lastErr = fmt.Errorf("%w: %s", err, stderrStr)

		// Fail fast on errors that won't resolve by retrying
		if isFatalExecError(stderrStr) {
			return fmt.Errorf("tart exec failed: %w", lastErr)
		}

		// Brief sleep before retry, also watching for process exit
		select {
		case procErr := <-procDone:
			if procErr != nil {
				return fmt.Errorf("tart run exited: %w", procErr)
			}
			return fmt.Errorf("tart run exited unexpectedly with no error")
		case <-ctx.Done():
			return ctx.Err()
		case <-waitTick():
		}
	}
}

// isFatalExecError returns true if the tart exec error indicates a problem
// that won't resolve by retrying (e.g., bad syntax, VM not found).
func isFatalExecError(stderr string) bool {
	lower := strings.ToLower(stderr)
	fatalPatterns := []string{
		"unknown option",
		"executable file not found",
		"does not exist",
		"no such",
		"usage:",
	}
	for _, p := range fatalPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// vmHomeDir is the home directory of the default user in Cirrus Labs base images.
const vmHomeDir = "/Users/admin"

// dockerHomeDir is the home directory used by Docker-based sandboxes.
const dockerHomeDir = "/home/yoloai"

// patchConfigWorkingDir reads runtime-config.json, remaps working_dir for macOS, and writes it back.
func (r *Runtime) patchConfigWorkingDir(sandboxPath string) error {
	cfgPath := filepath.Join(sandboxPath, "runtime-config.json")
	data, err := os.ReadFile(cfgPath) //nolint:gosec // G304: path within sandbox dir
	if err != nil {
		return err
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	if wd, ok := raw["working_dir"].(string); ok {
		remapped := remapTargetPath(wd)
		if remapped != wd {
			raw["working_dir"] = remapped
			out, err := json.MarshalIndent(raw, "", "  ")
			if err != nil {
				return err
			}
			return fileutil.WriteFile(cfgPath, out, 0600)
		}
	}

	return nil
}

// Per-call timeouts for the stop ladder. The graceful step is bounded
// because `tart stop` can hang indefinitely against an unresponsive VM
// (no kata-agent equivalent, but the Virtualization.framework shutdown
// path can block on a wedged guest kernel). The SIGTERM wait is bounded
// so we can escalate to SIGKILL before the user notices the hang.
// var (not const) so TestStopVM_EscalatesToSIGKILL can shrink them to
// milliseconds — the test validates the SIGTERM→SIGKILL escalation
// *logic*, not the production durations, and a real 15s wall-clock wait
// would dominate the unit suite. Same injectable-package-var pattern
// tart/containerd already use for test seams (kataShimName,
// canRunCNIBridgeFunc).
var (
	tartGracefulStopTimeout = 10 * time.Second
	tartSigtermWait         = 5 * time.Second
)

// stopVM attempts to stop a VM using tart stop, then kills any stale
// `tart run` processes for the given VM name. This is the definitive way
// to ensure no lingering processes hold VM slots.
//
// Ladder (mirrors the containerd Kata-shim ladder; same wedge class):
//
//  1. Graceful: `tart stop <name>` via Virtualization.framework, bounded
//     by tartGracefulStopTimeout. Returns silently if it succeeds.
//  2. Direct SIGTERM to every `tart run.*<name>` host PID. Wait
//     tartSigtermWait for the process to die.
//  3. Direct SIGKILL to any survivors. Logged at WARN level so the user
//     sees that yoloai had to force-kill a stuck VM process.
func (r *Runtime) stopVM(ctx context.Context, vmName string) {
	// Step 1: graceful via tart's own shutdown path. Bounded so a
	// wedged VM can't hold us forever.
	stopCtx, stopCancel := context.WithTimeout(ctx, tartGracefulStopTimeout)
	_, _ = r.runTart(stopCtx, "stop", vmName)
	stopCancel()

	// Steps 2+3: walk the `tart run` host processes for this VM.
	pids := pgrepTartRun(ctx, vmName)
	if len(pids) == 0 {
		return
	}

	// Step 2: SIGTERM.
	for _, pid := range pids {
		if proc, err := os.FindProcess(pid); err == nil {
			_ = proc.Signal(syscall.SIGTERM)
		}
	}

	// Wait for the SIGTERM'd processes to actually exit. waitForExit
	// polls /proc-style via syscall.Kill(0) — checking that the
	// process is still signal-deliverable. Bounded by tartSigtermWait.
	survivors := waitForExit(pids, tartSigtermWait)
	if len(survivors) == 0 {
		return
	}

	// Step 3: escalation. SIGTERM didn't take.
	slog.Warn("tart VM process wedged; escalating to SIGKILL",
		"event", "tart.stop.escalation",
		"vm", vmName,
		"survivors", survivors,
		"reason", "SIGTERM via pgrep did not release process within timeout",
		"timeout", tartSigtermWait.String(),
	)
	for _, pid := range survivors {
		if proc, err := os.FindProcess(pid); err == nil {
			_ = proc.Kill() // SIGKILL
		}
	}
}

// pgrepTartRun returns the PIDs of any `tart run …<vmName>` processes
// on the host. Empty slice if pgrep finds nothing or fails.
func pgrepTartRun(ctx context.Context, vmName string) []int {
	pgrepCmd := exec.CommandContext(ctx, "pgrep", "-f", fmt.Sprintf("tart run.*%s", vmName)) //nolint:gosec // G204: vmName from validated sandbox state
	out, err := pgrepCmd.Output()
	if err != nil {
		return nil
	}
	var pids []int
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if pid, err := strconv.Atoi(strings.TrimSpace(line)); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}

// waitForExit polls the given PIDs and returns those still alive after
// the timeout. Uses syscall.Kill(pid, 0) which returns ESRCH for a
// dead process — no need to read /proc, which is cheaper.
func waitForExit(pids []int, timeout time.Duration) []int {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		alive := pids[:0:len(pids)] // reuse backing array
		for _, pid := range pids {
			if err := syscall.Kill(pid, 0); err == nil {
				alive = append(alive, pid)
			}
		}
		if len(alive) == 0 {
			return nil
		}
		pids = alive
		time.Sleep(100 * time.Millisecond)
	}
	return pids
}

// killByPID reads the PID file and kills the process.
func (r *Runtime) killByPID(sandboxPath string) {
	pidPath := filepath.Join(sandboxPath, backendDir, pidFileName)
	data, err := os.ReadFile(pidPath) //nolint:gosec // G304: path is within ~/.yoloai/
	if err != nil {
		return
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}

	_ = proc.Signal(syscall.SIGTERM)
	_ = os.Remove(pidPath)
}

// checkVMLimitError examines vm.log content for the Tart concurrent-VM limit
// error string. Returns a *yoerrors.ResourceLimitError if matched, nil otherwise.
func checkVMLimitError(logContent string) error {
	if !strings.Contains(logContent, tartVMLimitSubstr) {
		return nil
	}
	return yoerrors.NewResourceLimitError(
		"macOS concurrent VM limit reached — only 2 macOS VMs can run simultaneously.\n"+
			"Stop a running sandbox first:\n"+
			"  yoloai sandbox list\n"+
			"  yoloai sandbox stop <name>\n"+
			"VM log: %s",
		strings.TrimSpace(logContent),
	)
}

// mapTartError maps tart CLI errors to runtime sentinel errors.
func mapTartError(err error, stderr string) error {
	lower := strings.ToLower(stderr)
	switch {
	case strings.Contains(lower, "does not exist"),
		strings.Contains(lower, "not found"),
		strings.Contains(lower, "no such"):
		return runtime.ErrNotFound
	case strings.Contains(lower, "not running"),
		strings.Contains(lower, "is stopped"):
		return runtime.ErrNotRunning
	default:
		if stderr != "" {
			return fmt.Errorf("%w: %s", err, stderr)
		}
		return err
	}
}

// isMacOS returns true if running on macOS.
func isMacOS() bool {
	return goos() == "darwin"
}

// isAppleSilicon returns true if running on Apple Silicon.
func isAppleSilicon() bool {
	return goarch() == "arm64" && goos() == "darwin"
}

// addMountMapToConfig adds a mount_map to runtime-config.json that tells
// sandbox-setup.py where to create symlinks from target paths to VirtioFS mount points.
func (r *Runtime) addMountMapToConfig(sandboxPath string, mounts []runtime.MountSpec) error {
	// Build mount map: target path → VirtioFS mount point
	mountMap := make(map[string]string)
	for _, m := range mounts {
		// Skip mounts under sandbox dir (already accessible via yoloai VirtioFS share)
		if strings.HasPrefix(m.HostPath, sandboxPath+"/") || m.HostPath == sandboxPath {
			continue
		}
		// Only add directory mounts (VirtioFS doesn't support files)
		if info, err := os.Stat(m.HostPath); err != nil || !info.IsDir() {
			continue
		}
		// VirtioFS mount appears at /Volumes/My Shared Files/<name>/
		dirName := mountDirName(m.HostPath)
		virtiofsMountPoint := filepath.Join(sharedDirVMPath, dirName)
		mountMap[remapTargetPath(m.ContainerPath)] = virtiofsMountPoint
	}

	if len(mountMap) == 0 {
		return nil // nothing to add
	}

	// Read existing runtime-config.json
	cfgPath := filepath.Join(sandboxPath, "runtime-config.json")
	data, err := os.ReadFile(cfgPath) //nolint:gosec // G304: path within sandbox dir
	if err != nil {
		return err
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	// Add mount_map
	raw["mount_map"] = mountMap

	// Write back
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.WriteFile(cfgPath, out, 0600)
}

// copySecretsToSandbox copies secret files from mount specs into the sandbox secrets directory.
func copySecretsToSandbox(sandboxPath string, mounts []runtime.MountSpec) error {
	secretsDir := filepath.Join(sandboxPath, "secrets")
	if err := fileutil.MkdirAll(secretsDir, 0700); err != nil {
		return fmt.Errorf("create secrets dir: %w", err)
	}
	for _, m := range mounts {
		if m.ContainerPath != "/run/secrets" && !strings.HasPrefix(m.ContainerPath, "/run/secrets/") {
			continue
		}
		if m.ContainerPath == "/run/secrets" {
			if err := copySecretDir(secretsDir, m.HostPath); err != nil {
				return err
			}
			continue
		}
		data, err := os.ReadFile(m.HostPath) //nolint:gosec // G304: source is from validated mount spec
		if err != nil {
			continue
		}
		keyName := filepath.Base(m.ContainerPath)
		if err := fileutil.WriteFile(filepath.Join(secretsDir, keyName), data, 0600); err != nil { //nolint:gosec // G703: secretsDir is an internal sandbox directory
			return fmt.Errorf("copy secret %s: %w", keyName, err)
		}
	}
	return nil
}

// copySecretDir copies all non-directory files from srcDir into destDir.
func copySecretDir(destDir, srcDir string) error {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return nil //nolint:nilerr // intentional: skip if directory read fails
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(srcDir, entry.Name())) //nolint:gosec // G304: source is from validated mount spec
		if err != nil {
			continue
		}
		if err := fileutil.WriteFile(filepath.Join(destDir, entry.Name()), data, 0600); err != nil { //nolint:gosec // G703: destDir is an internal sandbox directory
			return fmt.Errorf("copy secret %s: %w", entry.Name(), err)
		}
	}
	return nil
}

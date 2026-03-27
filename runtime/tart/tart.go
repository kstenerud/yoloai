// Package tart implements the runtime.Runtime interface using Tart VMs.
// ABOUTME: Shells out to the tart CLI for macOS VM lifecycle, exec, and image ops.
package tart

import (
	"bytes"
	"context"
	"crypto/rand"
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

	"github.com/kstenerud/yoloai/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/runtime/caps"
	"github.com/kstenerud/yoloai/runtime/monitor"
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

func init() {
	runtime.Register("tart", func(ctx context.Context) (runtime.Runtime, error) {
		return New(ctx)
	})
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
)

// Runtime implements runtime.Runtime using the Tart CLI.
type Runtime struct {
	tartBin           string // path to tart binary
	sandboxDir        string // ~/.yoloai/sandboxes/ base path
	baseImageOverride string // custom base image from config (tart.image)
}

// Compile-time check.
var _ runtime.Runtime = (*Runtime)(nil)

// Capabilities returns the Tart backend's feature set.
// Tart runs macOS VMs; no container-specific features are supported.
func (r *Runtime) Capabilities() runtime.BackendCaps {
	return runtime.BackendCaps{
		NetworkIsolation: false,
		OverlayDirs:      false,
		CapAdd:           false,
		HostFilesystem:   false,
	}
}

// AgentProvisionedByBackend returns true — Tart VMs use an npm-installed agent.
func (r *Runtime) AgentProvisionedByBackend() bool { return true }

// ResolveCopyMount returns the VirtioFS path where the copy directory is accessible
// inside the VM. Unlike Docker which can bind-mount at arbitrary paths, Tart VirtioFS
// shares appear at /Volumes/My Shared Files/<sharename>/..., so we must return the
// VirtioFS path where the sandbox work directory is accessible.
func (r *Runtime) ResolveCopyMount(sandboxName, hostPath string) string {
	// The copy is under ~/.yoloai/sandboxes/<sandboxName>/work/<encoded-hostPath>
	// and is accessible via the yoloai VirtioFS share at:
	// /Volumes/My Shared Files/yoloai/work/<encoded-hostPath>
	encoded := config.EncodePath(hostPath)
	vmSharedDir := filepath.Join(sharedDirVMPath, sharedDirName)
	return filepath.Join(vmSharedDir, "work", encoded)
}

// New creates a Runtime after verifying that tart is installed and the
// platform is supported (macOS with Apple Silicon).
func New(_ context.Context) (*Runtime, error) {
	// Platform checks first — no amount of software installation can fix these.
	if !isMacOS() {
		return nil, config.NewPlatformError("tart backend requires macOS with Apple Silicon")
	}
	if !isAppleSilicon() {
		return nil, config.NewPlatformError("tart backend requires Apple Silicon (M1 or later)")
	}

	tartBin, err := exec.LookPath("tart")
	if err != nil {
		return nil, config.NewDependencyError("tart is not installed. Install it with: brew install cirruslabs/cli/tart")
	}

	// Read config for optional tart.image override
	var baseImageOverride string
	if cfg, err := config.LoadConfig(); err == nil && cfg.TartImage != "" {
		baseImageOverride = cfg.TartImage
	}

	return &Runtime{
		tartBin:           tartBin,
		sandboxDir:        config.SandboxesDir(),
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
	slog.Debug("tart Create: cloning", "imageRef", cfg.ImageRef, "name", cfg.Name)
	if _, err := r.runTart(ctx, "clone", cfg.ImageRef, cfg.Name); err != nil {
		return fmt.Errorf("clone VM: %w", err)
	}
	slog.Debug("tart Create: clone succeeded", "name", cfg.Name)

	// Save instance config so Start can read mounts/network/ports
	sandboxPath := filepath.Join(r.sandboxDir, sandboxName(cfg.Name))
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
	secretsDir := filepath.Join(sandboxPath, "secrets")
	if err := fileutil.MkdirAll(secretsDir, 0700); err != nil {
		return fmt.Errorf("create secrets dir: %w", err)
	}
	for _, m := range cfg.Mounts {
		// buildMounts creates a directory mount with Target="/run/secrets"
		if m.Target != "/run/secrets" && !strings.HasPrefix(m.Target, "/run/secrets/") {
			continue
		}
		// If it's a directory mount, copy all files from the source directory
		if m.Target == "/run/secrets" {
			entries, err := os.ReadDir(m.Source)
			if err != nil {
				continue // skip if directory read fails
			}
			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}
				data, err := os.ReadFile(filepath.Join(m.Source, entry.Name())) //nolint:gosec // G304: source is from validated mount spec
				if err != nil {
					continue // skip files that can't be read
				}
				if err := fileutil.WriteFile(filepath.Join(secretsDir, entry.Name()), data, 0600); err != nil { //nolint:gosec // G703: secretsDir is an internal sandbox directory
					return fmt.Errorf("copy secret %s: %w", entry.Name(), err)
				}
			}
			continue
		}
		// Handle individual file mounts (legacy, may not be used anymore)
		data, err := os.ReadFile(m.Source) //nolint:gosec // G304: source is from validated mount spec
		if err != nil {
			continue // skip missing secrets (may have been cleaned up)
		}
		keyName := filepath.Base(m.Target)
		if err := fileutil.WriteFile(filepath.Join(secretsDir, keyName), data, 0600); err != nil { //nolint:gosec // G703: secretsDir is an internal sandbox directory
			return fmt.Errorf("copy secret %s: %w", keyName, err)
		}
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
func (r *Runtime) Start(ctx context.Context, name string) error {
	slog.Debug("tart Start", "name", name)
	sandboxPath := filepath.Join(r.sandboxDir, sandboxName(name))

	// Check if already running
	if r.isRunning(ctx, name) {
		return nil
	}

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

	// Build tart run arguments
	args := r.buildRunArgs(name, sandboxPath, cfg.Mounts)

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

	// Deliver setup script via shared directory and run it
	slog.Debug("tart Start: calling runSetupScript", "name", name)
	if err := r.runSetupScript(ctx, name, sandboxPath, cfg.Mounts); err != nil {
		return fmt.Errorf("run setup script: %w", err)
	}

	return nil
}

// Stop stops the VM with a clean shutdown.
func (r *Runtime) Stop(ctx context.Context, name string) error {
	r.stopVM(ctx, name)
	return nil
}

// Remove deletes the VM and cleans up the PID file.
func (r *Runtime) Remove(ctx context.Context, name string) error {
	sandboxPath := filepath.Join(r.sandboxDir, sandboxName(name))

	// Stop first if running
	_ = r.Stop(ctx, name)

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
	if !r.vmExists(ctx, name) {
		return runtime.InstanceInfo{}, runtime.ErrNotFound
	}

	return runtime.InstanceInfo{
		Running: r.isRunning(ctx, name),
	}, nil
}

// Exec runs a command inside the VM via tart exec and returns the result.
// The user parameter is ignored — tart exec runs as the VM's logged-in user.
func (r *Runtime) Exec(ctx context.Context, name string, cmd []string, _ string) (runtime.ExecResult, error) {
	if !r.isRunning(ctx, name) {
		return runtime.ExecResult{}, runtime.ErrNotRunning
	}

	args := execArgs(name, cmd...)

	c := exec.CommandContext(ctx, r.tartBin, args...) //nolint:gosec // G204: vmName and cmd are from validated sandbox state

	return runtime.RunCmdExec(c)
}

// InteractiveExec runs a command interactively inside the VM by shelling
// out to tart exec with stdin/stdout/stderr connected and PTY allocated.
// The user parameter is ignored — tart exec runs as the VM's logged-in user.
func (r *Runtime) InteractiveExec(ctx context.Context, name string, cmd []string, _ string, _ string) error {
	// -i attaches stdin, -t allocates a PTY (like docker exec -it)
	args := []string{"exec", "-i", "-t", name}
	args = append(args, cmd...)

	c := exec.CommandContext(ctx, r.tartBin, args...) //nolint:gosec // G204: name and cmd are from validated sandbox state
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

// Close is a no-op for Tart (no persistent client connection).
func (r *Runtime) Close() error {
	return nil
}

// Logs returns empty string — Tart VM logs are written to files on disk.
// Callers can use DiagHint to find the log path.
func (r *Runtime) Logs(_ context.Context, _ string, _ int) string { return "" }

// DiagHint returns a Tart-specific hint for checking logs.
func (r *Runtime) DiagHint(instanceName string) string {
	logPath := filepath.Join(r.sandboxDir, sandboxName(instanceName), backendDir, vmLogFileName)
	return fmt.Sprintf("check VM log at %s", logPath)
}

// BaseModeName returns "vm" — Tart runs macOS VMs.
func (r *Runtime) BaseModeName() string { return "vm" }

// SupportedIsolationModes returns nil — Tart has no additional isolation modes.
func (r *Runtime) SupportedIsolationModes() []string { return nil }

// RequiredCapabilities returns nil — Tart's prerequisites are enforced in New().
func (r *Runtime) RequiredCapabilities(_ string) []caps.HostCapability { return nil }

// Name returns the backend name.
func (r *Runtime) Name() string { return "tart" }

// TmuxSocket returns empty: tart VMs use the uid-based default socket.
// The tart runtime handles socket injection internally in InteractiveExec.
// sandboxDir is ignored.
func (r *Runtime) TmuxSocket(_ string) string { return "" }

// AttachCommand returns the command to attach to the tmux session in a tart VM.
// Tart runs commands directly with the caller's terminal; no script wrapper
// needed (and macOS BSD script does not support the GNU -c flag).
func (r *Runtime) AttachCommand(tmuxSocket string, _ int, _ int, _ string) []string {
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
				Source:   p.host,
				Target:   filepath.Join(sharedDirVMPath, p.name),
				ReadOnly: true,
			}
		}
	}

	// 2. Add user-specified mounts (override system paths if same Source)
	for _, m := range mounts {
		// Skip anything under the sandbox dir (already shared)
		if strings.HasPrefix(m.Source, sandboxPath+"/") || m.Source == sandboxPath {
			continue
		}
		// Skip files — VirtioFS only supports directories
		if info, err := os.Stat(m.Source); err != nil || !info.IsDir() {
			continue
		}
		mergedMounts[m.Source] = m // Overwrites system path if duplicate
	}

	// 3. Build --dir arguments from merged list
	for _, m := range mergedMounts {
		dirName := mountDirName(m.Source)
		dirSpec := fmt.Sprintf("%s:%s", dirName, m.Source)
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
			args = append(args, fmt.Sprintf("--net-softnet-allow=%s/%s", p.InstancePort, proto))
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
		pairs = append(pairs, fmt.Sprintf("%s:%s", p.HostPort, p.InstancePort))
	}
	return []string{"--net-softnet-expose=" + strings.Join(pairs, ",")}
}

// BuildMountSymlinkCmds returns shell commands to create symlinks from
// expected mount targets to their actual VirtioFS paths. Exported for testing.
func BuildMountSymlinkCmds(mounts []runtime.MountSpec, dirNames map[string]string) []string {
	var cmds []string
	for _, m := range mounts {
		dirName, ok := dirNames[m.Source]
		if !ok {
			continue
		}
		vfsPath := filepath.Join(sharedDirVMPath, dirName)
		if vfsPath == m.Target {
			continue // no symlink needed
		}
		parent := filepath.Dir(m.Target)
		cmds = append(cmds, fmt.Sprintf("sudo mkdir -p %q", parent))
		cmds = append(cmds, fmt.Sprintf("sudo ln -sf %q %q", vfsPath, m.Target))
	}
	return cmds
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

// vmExists checks whether a VM with the given name exists in tart's inventory.
func (r *Runtime) vmExists(ctx context.Context, vmName string) bool {
	out, err := r.runTart(ctx, "list", "--quiet")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == vmName {
			return true
		}
	}
	return false
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

// remapTargetPath translates Docker/Linux-style mount targets to macOS VM paths.
// - /home/yoloai/... → /Users/admin/...
// - /yoloai/... → /Users/admin/.yoloai/... (sandbox control files)
// - /Users/<host-user>/... → /Users/admin/host/... (host-mirrored workdirs)
func remapTargetPath(target string) string {
	if strings.HasPrefix(target, dockerHomeDir+"/") {
		return vmHomeDir + strings.TrimPrefix(target, dockerHomeDir)
	}
	if target == dockerHomeDir {
		return vmHomeDir
	}
	if strings.HasPrefix(target, "/yoloai/") {
		return vmHomeDir + "/.yoloai" + strings.TrimPrefix(target, "/yoloai")
	}
	// Host-mirrored paths (e.g. /Users/karlstenerud/project) — place under admin home
	if strings.HasPrefix(target, "/Users/") && !strings.HasPrefix(target, vmHomeDir) {
		return vmHomeDir + "/host" + target
	}
	return target
}

// runSetupScript creates mount symlinks, writes the embedded setup script
// to the shared directory, and executes it inside the VM.
func (r *Runtime) runSetupScript(ctx context.Context, vmName, sandboxPath string, mounts []runtime.MountSpec) error {
	vmSharedDir := filepath.Join(sharedDirVMPath, sharedDirName)

	// Create symlinks from expected mount targets to VirtioFS paths
	for _, m := range mounts {
		// Skip /run/secrets mounts - these are copied to sandbox/secrets/ during Create
		// and accessible via /Volumes/My Shared Files/yoloai/secrets/ inside the VM.
		// The setup script handles delivering them to the agent.
		if m.Target == "/run/secrets" || strings.HasPrefix(m.Target, "/run/secrets/") {
			continue
		}

		target := remapTargetPath(m.Target)
		slog.Debug("tart setup: processing mount", "source", m.Source, "target", target)

		var vfsPath string
		if strings.HasPrefix(m.Source, sandboxPath+"/") {
			// Source is under the sandbox dir — accessible via the yoloai share
			relPath := strings.TrimPrefix(m.Source, sandboxPath+"/")
			vfsPath = filepath.Join(vmSharedDir, relPath)
			// Check if source exists on host
			if stat, err := os.Stat(m.Source); err != nil {
				slog.Debug("tart setup: mount source does not exist on host!", "source", m.Source, "error", err)
				continue
			} else {
				slog.Debug("tart setup: mount under sandbox", "source", m.Source, "target", target, "relPath", relPath, "vfsPath", vfsPath, "sourceExists", true, "sourceIsDir", stat.IsDir())
			}
		} else if m.Source == sandboxPath {
			vfsPath = vmSharedDir
		} else if info, err := os.Stat(m.Source); err == nil && info.IsDir() {
			// External directory — has its own VirtioFS share
			vfsPath = filepath.Join(sharedDirVMPath, mountDirName(m.Source))
		} else {
			continue // skip files outside sandbox dir (can't share via VirtioFS)
		}

		// Clean trailing slashes — ln treats /foo/ as "inside /foo" not "replace /foo"
		target = strings.TrimRight(target, "/")

		if vfsPath == target {
			continue // no symlink needed
		}
		parent := filepath.Dir(target)

		// First, check if the VirtioFS path exists
		checkCmd := fmt.Sprintf("ls -la '%s' 2>&1 || echo 'PATH_DOES_NOT_EXIST'", filepath.Dir(vfsPath))
		checkArgs := execArgs(vmName, "bash", "-c", checkCmd)
		if out, checkErr := r.runTart(ctx, checkArgs...); checkErr == nil {
			slog.Debug("tart setup: VirtioFS parent directory listing", "path", filepath.Dir(vfsPath), "output", out)
		} else {
			slog.Debug("tart setup: failed to list VirtioFS parent", "path", filepath.Dir(vfsPath), "error", checkErr)
		}

		// Ensure parent directory exists. Use sudo if regular mkdir fails.
		mkdirCmd := fmt.Sprintf("mkdir -p '%s' 2>/dev/null || sudo mkdir -p '%s'", parent, parent)
		if _, mkdirErr := r.runTart(ctx, execArgs(vmName, "bash", "-c", mkdirCmd)...); mkdirErr != nil {
			return fmt.Errorf("create parent directory %s: %w", parent, mkdirErr)
		}

		// Remove existing dir/file before symlinking (ln -sfn won't replace a directory)
		// System paths (like /Library/*) require sudo
		needsSudo := strings.HasPrefix(target, "/Library/") || strings.HasPrefix(target, "/System/")
		var symlinkCmd string
		if needsSudo {
			symlinkCmd = fmt.Sprintf("sudo rm -rf '%s' && sudo ln -sfn '%s' '%s'", target, vfsPath, target)
		} else {
			symlinkCmd = fmt.Sprintf("rm -rf '%s' && ln -sfn '%s' '%s'", target, vfsPath, target)
		}
		args := execArgs(vmName, "bash", "-c", symlinkCmd)
		slog.Debug("tart setup: creating symlink", "vm", vmName, "target", target, "vfsPath", vfsPath, "cmd", symlinkCmd)
		if _, err := r.runTart(ctx, args...); err != nil {
			// Check if VM is still running to provide better error message
			if !r.isRunning(ctx, vmName) {
				return fmt.Errorf("create mount symlink for %s (VM appears to have crashed): %w", target, err)
			}
			return fmt.Errorf("create mount symlink for %s: %w", target, err)
		}
	}

	// Patch runtime-config.json to remap working_dir for macOS VM paths
	if err := r.patchConfigWorkingDir(sandboxPath); err != nil {
		return fmt.Errorf("patch config working dir: %w", err)
	}

	// Write setup script, status monitor, and tmux config to sandbox dir (shared via VirtioFS)
	scriptPath := filepath.Join(sandboxPath, binDir, "sandbox-setup.py")
	if err := fileutil.WriteFile(scriptPath, monitor.SetupScript(), 0644); err != nil { //nolint:gosec // G306: script content, not user input
		return fmt.Errorf("write sandbox-setup.py: %w", err)
	}
	monitorPath := filepath.Join(sandboxPath, binDir, "status-monitor.py")
	if err := fileutil.WriteFile(monitorPath, monitor.Script(), 0644); err != nil { //nolint:gosec // G306: script content, not user input
		return fmt.Errorf("write status monitor: %w", err)
	}
	diagPath := filepath.Join(sandboxPath, binDir, "diagnose-idle.sh")
	if err := fileutil.WriteFile(diagPath, monitor.DiagnoseScript(), 0755); err != nil { //nolint:gosec // G306: script needs exec permission
		return fmt.Errorf("write diagnose script: %w", err)
	}
	tmuxConfPath := filepath.Join(sandboxPath, tmuxDir, "tmux.conf")
	if err := fileutil.WriteFile(tmuxConfPath, embeddedTmuxConf, 0600); err != nil {
		return fmt.Errorf("write tmux.conf: %w", err)
	}

	// Run the setup script in the background inside the VM.
	// Paths must be quoted — VirtioFS mount path contains spaces.
	setupCmd := fmt.Sprintf("nohup python3 '%s/bin/sandbox-setup.py' tart '%s' </dev/null >'%s/setup.log' 2>&1 &",
		vmSharedDir, vmSharedDir, vmSharedDir)
	args := execArgs(vmName, "bash", "-c", setupCmd)
	_, err := r.runTart(ctx, args...)
	if err != nil {
		return fmt.Errorf("exec setup script: %w", err)
	}

	return nil
}

// patchConfigWorkingDir reads runtime-config.json, remaps working_dir for macOS, and writes it back.
func (r *Runtime) patchConfigWorkingDir(sandboxPath string) error {
	cfgPath := filepath.Join(sandboxPath, "runtime-config.json")
	data, err := os.ReadFile(cfgPath) //nolint:gosec // G304: path within sandbox dir
	if err != nil {
		return err
	}

	var raw map[string]interface{}
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

// stopVM attempts to stop a VM using tart stop, then kills any stale
// tart run processes for the given VM name. This is the definitive way
// to ensure no lingering processes hold VM slots.
func (r *Runtime) stopVM(ctx context.Context, vmName string) {
	// Try graceful stop first
	_, _ = r.runTart(ctx, "stop", vmName)

	// Kill any stale "tart run" processes matching this VM name.
	// pgrep -f matches the full command line.
	pgrepCmd := exec.CommandContext(ctx, "pgrep", "-f", fmt.Sprintf("tart run.*%s", vmName)) //nolint:gosec // G204: vmName is from validated sandbox state
	out, err := pgrepCmd.Output()
	if err != nil {
		return // no matching processes
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		pid, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil {
			continue
		}
		if proc, findErr := os.FindProcess(pid); findErr == nil {
			_ = proc.Signal(syscall.SIGTERM)
		}
	}
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

// BaseExists checks if a base VM exists.
func (r *Runtime) BaseExists(ctx context.Context, baseName string) (bool, error) {
	return r.vmExists(ctx, baseName), nil
}

// CreateBase creates a new runtime base image with specified runtimes.
func (r *Runtime) CreateBase(ctx context.Context, baseName string, runtimes []RuntimeVersion) error {
	tempVM := generateTempVMName(baseName)
	defer r.cleanupTempVM(ctx, tempVM) // Always cleanup temp VM

	// Clone yoloai-base to temp VM
	if _, err := r.runTart(ctx, "clone", "yoloai-base", tempVM); err != nil {
		return fmt.Errorf("clone base: %w", err)
	}

	// Start temp VM for runtime installation
	if err := r.startTempVM(ctx, tempVM); err != nil {
		return fmt.Errorf("start temp VM: %w", err)
	}
	defer r.stopVM(ctx, tempVM) // Ensure VM stopped before snapshot

	// Configure Xcode in VM (required for xcodebuild)
	fmt.Printf("Configuring Xcode...\n")
	if err := r.configureXcodeInVM(ctx, tempVM); err != nil {
		return fmt.Errorf("configure Xcode: %w", err)
	}

	// Copy each runtime into the VM
	for _, rt := range runtimes {
		fmt.Printf("Copying %s %s runtime (this may take several minutes)...\n", rt.Platform, rt.Version)
		if err := CopyRuntimeToVM(ctx, tempVM, rt); err != nil {
			return fmt.Errorf("copy %s %s: %w", rt.Platform, rt.Version, err)
		}
	}

	// Stop VM to flush all changes to disk
	r.stopVM(ctx, tempVM)

	// Wait for VM to fully stop
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if !r.isRunning(ctx, tempVM) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Snapshot temp VM as new base
	if err := r.snapshotAsBase(ctx, tempVM, baseName); err != nil {
		return fmt.Errorf("snapshot base: %w", err)
	}

	return nil
}

// generateTempVMName generates a unique temporary VM name.
func generateTempVMName(baseName string) string {
	// Generate 6 random hex chars
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	random := fmt.Sprintf("%x", b)
	return fmt.Sprintf("%s-tmp-%s", baseName, random)
}

// startTempVM starts a temporary VM for runtime installation.
func (r *Runtime) startTempVM(ctx context.Context, vmName string) error {
	// Build arguments for tart run
	args := []string{"run", "--no-graphics"}

	// Mount Xcode (required for xcodebuild to work)
	if xcodeDevPath := getXcodeSelectPath(); xcodeDevPath != "" {
		// xcode-select returns: /Applications/Xcode.app/Contents/Developer
		// We need: /Applications/Xcode.app
		xcodePath := filepath.Dir(filepath.Dir(xcodeDevPath))
		mountName := "m-" + filepath.Base(xcodePath)
		args = append(args, "--dir", fmt.Sprintf("%s:%s:ro", mountName, xcodePath))

		// Also mount PrivateFrameworks from the same Xcode installation
		privateFrameworks := filepath.Join(filepath.Dir(xcodeDevPath), "PrivateFrameworks")
		if info, err := os.Stat(privateFrameworks); err == nil && info.IsDir() {
			args = append(args, "--dir", "m-PrivateFrameworks:"+privateFrameworks+":ro")
		}
	}

	// Mount /Library/Developer/CoreSimulator/Volumes/ (not needed for xcodebuild, but kept for consistency)
	volumesPath := "/Library/Developer/CoreSimulator/Volumes"
	args = append(args, "--dir", "m-Volumes:"+volumesPath+":ro")

	// Mount /tmp
	args = append(args, "--dir", "m-tmp:/tmp:ro")

	// Add VM name
	args = append(args, vmName)

	// Start VM in background
	cmd := exec.CommandContext(ctx, r.tartBin, args...) //nolint:gosec // G204: args are constructed internally
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start VM: %w", err)
	}

	// Wait for boot
	procDone := make(chan error, 1)
	go func() { procDone <- cmd.Wait() }()

	return r.waitForBoot(ctx, vmName, procDone)
}

// configureXcodeInVM sets up Xcode symlinks and configuration inside the VM.
func (r *Runtime) configureXcodeInVM(ctx context.Context, vmName string) error {
	// Get active Xcode path on host
	xcodeDevPath := getXcodeSelectPath()
	if xcodeDevPath == "" {
		return fmt.Errorf("no active Xcode found (run xcode-select on host)")
	}

	// xcode-select returns: /Applications/Xcode.app/Contents/Developer
	// We need: /Applications/Xcode.app
	xcodePath := filepath.Dir(filepath.Dir(xcodeDevPath))
	xcodeName := filepath.Base(xcodePath)

	// VirtioFS mount point inside VM
	vfsMountPoint := filepath.Join(sharedDirVMPath, "m-"+xcodeName)

	// Create symlink from VirtioFS mount to expected location
	symlinkCmd := fmt.Sprintf("sudo rm -rf '%s' && sudo ln -sf '%s' '%s'", xcodePath, vfsMountPoint, xcodePath)
	args := execArgs(vmName, "bash", "-c", symlinkCmd)
	if _, err := r.runTart(ctx, args...); err != nil {
		return fmt.Errorf("create Xcode symlink: %w", err)
	}

	// Also symlink PrivateFrameworks if it's mounted
	privateFrameworks := filepath.Join(filepath.Dir(xcodeDevPath), "PrivateFrameworks")
	if info, err := os.Stat(privateFrameworks); err == nil && info.IsDir() {
		vfsPrivate := filepath.Join(sharedDirVMPath, "m-PrivateFrameworks")
		symlinkPrivateCmd := fmt.Sprintf("sudo rm -rf '%s' && sudo ln -sf '%s' '%s'", privateFrameworks, vfsPrivate, privateFrameworks)
		args = execArgs(vmName, "bash", "-c", symlinkPrivateCmd)
		if _, err := r.runTart(ctx, args...); err != nil {
			// Non-fatal: PrivateFrameworks might not be critical
			slog.Debug("failed to symlink PrivateFrameworks", "error", err)
		}
	}

	// Set active developer directory
	xcodeSelectCmd := fmt.Sprintf("sudo xcode-select -s '%s/Contents/Developer'", xcodePath)
	args = execArgs(vmName, "bash", "-c", xcodeSelectCmd)
	if _, err := r.runTart(ctx, args...); err != nil {
		return fmt.Errorf("run xcode-select: %w", err)
	}

	// Accept Xcode license (non-interactive)
	acceptLicenseCmd := "sudo xcodebuild -license accept"
	args = execArgs(vmName, "bash", "-c", acceptLicenseCmd)
	if _, err := r.runTart(ctx, args...); err != nil {
		// Non-fatal: license might already be accepted or not required
		slog.Debug("xcodebuild -license accept failed (might already be accepted)", "error", err)
	}

	// Run xcodebuild -runFirstLaunch to complete setup
	firstLaunchCmd := "sudo xcodebuild -runFirstLaunch"
	args = execArgs(vmName, "bash", "-c", firstLaunchCmd)
	if _, err := r.runTart(ctx, args...); err != nil {
		// Non-fatal: might not be needed
		slog.Debug("xcodebuild -runFirstLaunch failed (might not be needed)", "error", err)
	}

	return nil
}

// snapshotAsBase creates a new base image by cloning a temp VM.
func (r *Runtime) snapshotAsBase(ctx context.Context, tempVM, baseName string) error {
	// Clone temp VM to new base name
	if _, err := r.runTart(ctx, "clone", tempVM, baseName); err != nil {
		// If clone fails and partial base exists, delete it
		_, _ = r.runTart(ctx, "delete", baseName)
		return fmt.Errorf("clone to base: %w", err)
	}
	return nil
}

// cleanupTempVM removes a temporary VM (best-effort, never fails).
func (r *Runtime) cleanupTempVM(ctx context.Context, vmName string) {
	r.stopVM(ctx, vmName)
	_, _ = r.runTart(ctx, "delete", vmName)
}

// addMountMapToConfig adds a mount_map to runtime-config.json that tells
// sandbox-setup.py where to create symlinks from target paths to VirtioFS mount points.
func (r *Runtime) addMountMapToConfig(sandboxPath string, mounts []runtime.MountSpec) error {
	// Build mount map: target path → VirtioFS mount point
	mountMap := make(map[string]string)
	for _, m := range mounts {
		// Skip mounts under sandbox dir (already accessible via yoloai VirtioFS share)
		if strings.HasPrefix(m.Source, sandboxPath+"/") || m.Source == sandboxPath {
			continue
		}
		// Only add directory mounts (VirtioFS doesn't support files)
		if info, err := os.Stat(m.Source); err != nil || !info.IsDir() {
			continue
		}
		// VirtioFS mount appears at /Volumes/My Shared Files/<name>/
		dirName := mountDirName(m.Source)
		virtiofsMountPoint := filepath.Join(sharedDirVMPath, dirName)
		mountMap[m.Target] = virtiofsMountPoint
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

	var raw map[string]interface{}
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

// Package tart implements the runtime.Runtime interface using Tart VMs.
// ABOUTME: Shells out to the tart CLI for macOS VM lifecycle, exec, and image ops.
package tart

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox"
)

const (
	// pidFileName stores the tart run process ID.
	pidFileName = "tart.pid"

	// vmLogFileName captures tart run stderr for debugging.
	vmLogFileName = "vm.log"

	// sharedDirName is the VirtioFS share name used for yoloai state.
	sharedDirName = "yoloai"

	// sharedDirVMPath is where VirtioFS shares appear inside the macOS VM.
	sharedDirVMPath = "/Volumes/My Shared Files"
)

// Runtime implements runtime.Runtime using the Tart CLI.
type Runtime struct {
	tartBin           string // path to tart binary
	sandboxDir        string // ~/.yoloai/sandboxes/ base path
	baseImageOverride string // custom base image from config (defaults.tart_image)
}

// Compile-time check.
var _ runtime.Runtime = (*Runtime)(nil)

// New creates a Runtime after verifying that tart is installed and the
// platform is supported (macOS with Apple Silicon).
func New(_ context.Context) (*Runtime, error) {
	tartBin, err := exec.LookPath("tart")
	if err != nil {
		return nil, fmt.Errorf("tart is not installed. Install it with: brew install cirruslabs/cli/tart")
	}

	// Verify we're on macOS (tart requires Apple Virtualization.framework)
	if !isMacOS() {
		return nil, fmt.Errorf("tart backend requires macOS with Apple Silicon")
	}

	// Verify Apple Silicon
	if !isAppleSilicon() {
		return nil, fmt.Errorf("tart backend requires Apple Silicon (M1 or later)")
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home directory: %w", err)
	}

	// Read config for optional tart_image override
	var baseImageOverride string
	if cfg, err := sandbox.LoadConfig(); err == nil && cfg.TartImage != "" {
		baseImageOverride = cfg.TartImage
	}

	return &Runtime{
		tartBin:           tartBin,
		sandboxDir:        filepath.Join(homeDir, ".yoloai", "sandboxes"),
		baseImageOverride: baseImageOverride,
	}, nil
}

// Create creates a new VM instance by cloning the base image and writing
// the instance config to the sandbox directory.
func (r *Runtime) Create(ctx context.Context, cfg runtime.InstanceConfig) error {
	// Stop any stale processes and remove leftover VM (idempotent)
	r.stopVM(ctx, cfg.Name)
	if r.vmExists(ctx, cfg.Name) {
		if _, err := r.runTart(ctx, "delete", cfg.Name); err != nil {
			return fmt.Errorf("remove existing VM: %w", err)
		}
	}

	// Clone the base image to create an instance-specific VM
	if _, err := r.runTart(ctx, "clone", cfg.ImageRef, cfg.Name); err != nil {
		return fmt.Errorf("clone VM: %w", err)
	}

	return nil
}

// Start boots the VM in the background and runs the setup script.
func (r *Runtime) Start(ctx context.Context, name string) error {
	sandboxPath := filepath.Join(r.sandboxDir, sandboxName(name))

	// Check if already running
	if r.isRunning(ctx, name) {
		return nil
	}

	// Build tart run arguments
	args := r.buildRunArgs(name, sandboxPath)

	// Open log file for stderr capture
	logPath := filepath.Join(sandboxPath, vmLogFileName)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600) //nolint:gosec // G304: sandboxPath is ~/.yoloai/sandboxes/<name>
	if err != nil {
		return fmt.Errorf("open VM log: %w", err)
	}

	// Start tart run as a background process
	cmd := exec.CommandContext(ctx, r.tartBin, args...) //nolint:gosec // G204: args are constructed from validated config
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

	// Write PID file
	pidPath := filepath.Join(sandboxPath, pidFileName)
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0600); err != nil {
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

	// Deliver setup script via shared directory and run it
	if err := r.runSetupScript(ctx, name, sandboxPath); err != nil {
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
		_ = os.Remove(filepath.Join(sandboxPath, pidFileName))
		return nil
	}

	if _, err := r.runTart(ctx, "delete", name); err != nil {
		return fmt.Errorf("delete VM: %w", err)
	}

	_ = os.Remove(filepath.Join(sandboxPath, pidFileName))

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
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr

	err := c.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok { //nolint:errorlint // ExitError is concrete type
			exitCode = exitErr.ExitCode()
		} else {
			return runtime.ExecResult{}, fmt.Errorf("exec in VM: %w", err)
		}
	}

	result := runtime.ExecResult{
		Stdout:   strings.TrimSpace(stdout.String()),
		ExitCode: exitCode,
	}

	if exitCode != 0 {
		return result, fmt.Errorf("exec exited with code %d: %s", exitCode, strings.TrimSpace(stderr.String()))
	}

	return result, nil
}

// InteractiveExec runs a command interactively inside the VM by shelling
// out to tart exec with stdin/stdout/stderr connected.
// The user parameter is ignored — tart exec runs as the VM's logged-in user.
func (r *Runtime) InteractiveExec(ctx context.Context, name string, cmd []string, _ string) error {
	args := execArgs(name, cmd...)

	c := exec.CommandContext(ctx, r.tartBin, args...) //nolint:gosec // G204: vmName and cmd are from validated sandbox state
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

// Close is a no-op for Tart (no persistent client connection).
func (r *Runtime) Close() error {
	return nil
}

// DiagHint returns a Tart-specific hint for checking logs.
func (r *Runtime) DiagHint(instanceName string) string {
	logPath := filepath.Join(r.sandboxDir, sandboxName(instanceName), vmLogFileName)
	return fmt.Sprintf("check VM log at %s", logPath)
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
func (r *Runtime) buildRunArgs(vmName, sandboxPath string) []string {
	args := []string{"run", "--no-graphics"}

	// Share the sandbox directory into the VM
	args = append(args, "--dir", fmt.Sprintf("%s:%s", sharedDirName, sandboxPath))

	return append(args, vmName)
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

// isRunning checks if the VM is running using tart list and PID cross-check.
func (r *Runtime) isRunning(ctx context.Context, vmName string) bool {
	out, err := r.runTart(ctx, "list", "--quiet", "--state", "running")
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

// runSetupScript writes the embedded setup script to the shared directory
// and executes it inside the VM.
func (r *Runtime) runSetupScript(ctx context.Context, vmName, sandboxPath string) error {
	// Write setup script to sandbox dir (it's shared via VirtioFS)
	scriptPath := filepath.Join(sandboxPath, "setup.sh")
	if err := os.WriteFile(scriptPath, embeddedSetupScript, 0755); err != nil { //nolint:gosec // G306: script needs exec permission
		return fmt.Errorf("write setup script: %w", err)
	}

	// The shared dir appears at /Volumes/My Shared Files/yoloai inside the VM
	vmSharedDir := filepath.Join(sharedDirVMPath, sharedDirName)

	// Run the setup script in the background inside the VM
	setupCmd := fmt.Sprintf("nohup %s/setup.sh %q </dev/null >%s/setup.log 2>&1 &",
		vmSharedDir, vmSharedDir, vmSharedDir)
	args := execArgs(vmName, "bash", "-c", setupCmd)
	_, err := r.runTart(ctx, args...)
	if err != nil {
		return fmt.Errorf("exec setup script: %w", err)
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
	pidPath := filepath.Join(sandboxPath, pidFileName)
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

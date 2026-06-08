// Package seatbelt implements runtime.Runtime using macOS sandbox-exec.
// ABOUTME: Runs agent processes under sandbox-exec SBPL profiles for lightweight isolation.
package seatbelt

import (
	"context"
	"encoding/json"
	"fmt"
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
	"github.com/kstenerud/yoloai/internal/runtime/monitor"
	"github.com/kstenerud/yoloai/yoerrors"
)

// descriptor holds the static facts for the seatbelt backend; shared by the
// registry registration and the Runtime.Descriptor() method.
var descriptor = runtime.BackendDescriptor{
	Type:                      runtime.BackendSeatbelt,
	Description:               "macOS sandbox; near-instant, uses host tools, less isolation",
	Platforms:                 []string{"darwin"},
	Requires:                  "macOS (sandbox-exec is built-in)",
	InstallHint:               "",
	BaseModeName:              runtime.IsolationModeProcess,
	AgentProvisionedByBackend: false,
	// Source the Swift wrapper that auto-adds --disable-sandbox for Swift PM
	// commands inside the macOS sandbox.
	AgentLaunchPrefix:       "source ~/.swift-wrapper.sh && ",
	SupportedIsolationModes: nil,
	Capabilities: runtime.BackendCaps{
		NetworkIsolation: false,
		OverlayDirs:      false,
		CapAdd:           false,
		HostFilesystem:   true,
	},
	Probe:         probe,
	VersionString: func(_ context.Context) string { return "built-in" },
}

// probe reports whether Seatbelt is usable. sandbox-exec ships with every
// macOS install, so a positive macOS check + LookPath suffices.
func probe(_ context.Context, _ map[string]string) (bool, string) {
	if !isMacOS() {
		return false, "seatbelt requires macOS"
	}
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		return false, "sandbox-exec not found on PATH"
	}
	return true, ""
}

func init() {
	// The registry factory derives homeDir from layout via the conventional
	// $HOME/.yoloai DataDir: homeDir = layout.HomeDir.
	// Direct callers (CLI, tests) may call New(ctx, layout, homeDir) explicitly.
	runtime.Register(func(ctx context.Context, layout config.Layout) (runtime.Runtime, error) {
		return New(ctx, layout, layout.HomeDir)
	}, descriptor)
}

const (
	// backendDir holds backend-specific files within the sandbox directory.
	backendDir = config.BackendDirName

	// binDir holds executable scripts within the sandbox directory.
	binDir = config.BinDirName

	// tmuxDir holds tmux config and sockets within the sandbox directory.
	tmuxDir = config.TmuxDirName

	// pidFileName stores the sandbox-exec process ID.
	pidFileName = "pid"

	// processLogFileName captures sandbox-exec stderr for debugging.
	processLogFileName = "stderr.log"

	// seatbeltConfigFileName stores the instance config for Start to use.
	seatbeltConfigFileName = "instance.json"

	// profileFileName is the generated SBPL profile.
	profileFileName = "profile.sb"

	// tmuxSocketName is the per-sandbox tmux socket filename.
	tmuxSocketName = "tmux.sock"

	// symlinkManifestName tracks mount symlinks for cleanup.
	symlinkManifestName = "mount-symlinks.txt"
)

// Runtime implements runtime.Runtime using macOS sandbox-exec.
type Runtime struct {
	sandboxExecBin string        // path to sandbox-exec binary
	layout         config.Layout // DataDir-rooted path resolver (Q-W.6)
	homeDir        string        // user's real $HOME — needed for SBPL profile generation (not layout.DataDir)
}

// Compile-time checks.
var _ runtime.Runtime = (*Runtime)(nil)
var _ runtime.CopyMountResolver = (*Runtime)(nil)

// Descriptor returns a BackendDescriptor with the static facts for this backend.
func (r *Runtime) Descriptor() runtime.BackendDescriptor {
	return descriptor
}

// ResolveCopyMount returns the sandbox copy directory path. Seatbelt runs the
// agent directly on the host, so it must read :copy files from their actual
// sandbox location rather than from a container bind-mount at the original path.
func (r *Runtime) ResolveCopyMount(sbName, hostPath string) string {
	return filepath.Join(r.layout.SandboxesDir(), sbName, "work", config.EncodePath(hostPath))
}

// New creates a Runtime after verifying that we're on macOS and
// sandbox-exec is available. layout is used for all DataDir-rooted path
// resolution. homeDir is the user's real $HOME directory, which the SBPL
// profile generator needs to allow read access to the home tree; it is
// distinct from layout.DataDir (which is $HOME/.yoloai). Passing them as
// separate arguments makes the distinction explicit and avoids re-computing
// $HOME from layout.DataDir via filepath.Dir (fragile if DataDir changes).
func New(_ context.Context, layout config.Layout, homeDir string) (*Runtime, error) {
	if !isMacOS() {
		return nil, yoerrors.NewPlatformError("seatbelt backend requires macOS")
	}

	sandboxExecBin, err := exec.LookPath("sandbox-exec")
	if err != nil {
		return nil, yoerrors.NewDependencyError("sandbox-exec not found: %w", err)
	}

	return &Runtime{
		sandboxExecBin: sandboxExecBin,
		layout:         layout,
		homeDir:        homeDir,
	}, nil
}

// Create saves the instance config, copies secrets into the sandbox
// directory, patches working_dir for :copy mode, generates the SBPL
// profile, and writes the entrypoint script and tmux config.
func (r *Runtime) Create(_ context.Context, cfg runtime.InstanceConfig) error {
	sandboxPath := filepath.Join(r.layout.SandboxesDir(), sandboxName(cfg.Name))

	for _, dir := range []string{backendDir, binDir, tmuxDir} {
		if err := fileutil.MkdirAll(filepath.Join(sandboxPath, dir), 0750); err != nil {
			return fmt.Errorf("create %s dir: %w", dir, err)
		}
	}

	cfgData, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal instance config: %w", err)
	}
	if err := fileutil.WriteFile(filepath.Join(sandboxPath, backendDir, seatbeltConfigFileName), cfgData, 0600); err != nil {
		return fmt.Errorf("write instance config: %w", err)
	}

	if err := copySecretsToSandbox(sandboxPath, cfg.Mounts); err != nil {
		return err
	}

	if err := r.patchConfigWorkingDir(sandboxPath, cfg.Mounts); err != nil {
		return fmt.Errorf("patch config working dir: %w", err)
	}

	profile := GenerateProfile(cfg, sandboxPath, r.homeDir)
	if err := fileutil.WriteFile(filepath.Join(sandboxPath, backendDir, profileFileName), []byte(profile), 0600); err != nil {
		return fmt.Errorf("write SBPL profile: %w", err)
	}

	if err := writeSandboxScripts(sandboxPath); err != nil {
		return err
	}

	symlinks, err := mountSymlinks(cfg.Mounts)
	if err != nil {
		return fmt.Errorf("create mount symlinks: %w", err)
	}
	if len(symlinks) > 0 {
		manifest := strings.Join(symlinks, "\n") + "\n"
		if err := fileutil.WriteFile(filepath.Join(sandboxPath, backendDir, symlinkManifestName), []byte(manifest), 0600); err != nil {
			return fmt.Errorf("write symlink manifest: %w", err)
		}
	}

	return nil
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

// writeSandboxScripts writes the setup, monitor, and tmux config files.
func writeSandboxScripts(sandboxPath string) error {
	setupScriptPath := filepath.Join(sandboxPath, binDir, "sandbox-setup.py")
	if err := fileutil.WriteFile(setupScriptPath, monitor.SetupScript(), 0644); err != nil { //nolint:gosec // G306: script content
		return fmt.Errorf("write sandbox-setup.py: %w", err)
	}
	helpersPath := filepath.Join(sandboxPath, binDir, "setup_helpers.py")
	if err := fileutil.WriteFile(helpersPath, monitor.SetupHelpers(), 0644); err != nil { //nolint:gosec // G306: script content
		return fmt.Errorf("write setup_helpers.py: %w", err)
	}
	tmuxIOPath := filepath.Join(sandboxPath, binDir, "tmux_io.py")
	if err := fileutil.WriteFile(tmuxIOPath, monitor.TmuxIO(), 0644); err != nil { //nolint:gosec // G306: script content
		return fmt.Errorf("write tmux_io.py: %w", err)
	}
	monitorPath := filepath.Join(sandboxPath, binDir, "status-monitor.py")
	if err := fileutil.WriteFile(monitorPath, monitor.Script(), 0644); err != nil { //nolint:gosec // G306: script content
		return fmt.Errorf("write status-monitor.py: %w", err)
	}
	diagPath := filepath.Join(sandboxPath, binDir, "diagnose-idle.sh")
	if err := fileutil.WriteFile(diagPath, monitor.DiagnoseScript(), 0755); err != nil { //nolint:gosec // G306: script needs exec permission
		return fmt.Errorf("write diagnose-idle.sh: %w", err)
	}
	tmuxConfPath := filepath.Join(sandboxPath, tmuxDir, "tmux.conf")
	if err := fileutil.WriteFile(tmuxConfPath, embeddedTmuxConf, 0600); err != nil {
		return fmt.Errorf("write tmux.conf: %w", err)
	}
	return nil
}

// Start launches the sandboxed process in the background and waits for
// the tmux session to become available.
func (r *Runtime) Start(ctx context.Context, name string) error {
	sandboxPath := filepath.Join(r.layout.SandboxesDir(), sandboxName(name))

	// Check if already running
	if r.isRunning(sandboxPath) {
		return nil
	}

	// Load instance config saved by Create
	var cfg runtime.InstanceConfig
	cfgPath := filepath.Join(sandboxPath, backendDir, seatbeltConfigFileName)
	cfgData, err := os.ReadFile(cfgPath) //nolint:gosec // G304: path within sandbox dir
	if err != nil {
		return fmt.Errorf("read instance config: %w", err)
	}
	if err := json.Unmarshal(cfgData, &cfg); err != nil {
		return fmt.Errorf("parse instance config: %w", err)
	}

	// Regenerate derived artifacts (SBPL profile + monitor scripts) from the
	// persisted config on every Start. They are pure functions of cfg and the
	// host environment, not user state, so regenerating here lets a restart on
	// a newer binary self-heal sandboxes created by an older one — e.g. picking
	// up the /private/var SBPL fix or sandbox-setup.py changes after a data-dir
	// migration relocated (but did not rewrite) the frozen Create-time files.
	profile := GenerateProfile(cfg, sandboxPath, r.homeDir)
	if err := fileutil.WriteFile(filepath.Join(sandboxPath, backendDir, profileFileName), []byte(profile), 0600); err != nil {
		return fmt.Errorf("regenerate profile: %w", err)
	}
	if err := writeSandboxScripts(sandboxPath); err != nil {
		return fmt.Errorf("regenerate sandbox scripts: %w", err)
	}

	// Open log file for stderr capture
	logPath := filepath.Join(sandboxPath, backendDir, processLogFileName)
	logFile, err := fileutil.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600) //nolint:gosec // G304: sandboxPath is ~/.yoloai/sandboxes/<name>
	if err != nil {
		return fmt.Errorf("open log: %w", err)
	}

	// Launch sandbox-exec with the SBPL profile running the setup script
	profilePath := filepath.Join(sandboxPath, backendDir, profileFileName)
	setupScriptPath := filepath.Join(sandboxPath, binDir, "sandbox-setup.py")

	cmd := exec.Command(r.sandboxExecBin, "-f", profilePath, "python3", setupScriptPath, "seatbelt", sandboxPath) //nolint:gosec // G204: paths are constructed from validated config
	cmd.Env = r.sandboxEnv()
	cmd.Stderr = logFile
	cmd.Stdout = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	if err := cmd.Start(); err != nil {
		logFile.Close() //nolint:errcheck,gosec // best-effort
		return fmt.Errorf("start sandbox-exec: %w", err)
	}

	// Write PID file. There is a brief race between cmd.Start() and writing
	// the PID file — if the process exits in this window, the PID file may
	// reference a dead process. This is handled by: (1) the waitForTmux loop
	// below detects early process exit via procDone, and (2) killByPID and
	// isRunning gracefully handle stale PID files.
	pidPath := filepath.Join(sandboxPath, backendDir, pidFileName)
	if err := fileutil.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0600); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		logFile.Close() //nolint:errcheck,gosec // best-effort
		return fmt.Errorf("write PID file: %w", err)
	}

	// Monitor the process in the background
	procDone := make(chan error, 1)
	go func() {
		procDone <- cmd.Wait()
		logFile.Close() //nolint:errcheck,gosec // best-effort
	}()

	// Wait for tmux session to appear
	if err := r.waitForTmux(ctx, sandboxPath, procDone); err != nil {
		r.killByPID(sandboxPath)
		detail := fmt.Sprintf("command: %s -f %s python3 %s seatbelt %s", r.sandboxExecBin, profilePath, setupScriptPath, sandboxPath)
		if logData, readErr := os.ReadFile(logPath); readErr == nil && len(logData) > 0 { //nolint:gosec // G304: path within sandbox dir
			detail += fmt.Sprintf("\nlog output:\n%s", strings.TrimSpace(string(logData)))
		}
		return fmt.Errorf("wait for tmux session: %w\n%s", err, detail)
	}

	return nil
}

// Stop kills the sandbox-exec process and the tmux server.
func (r *Runtime) Stop(_ context.Context, name string) error {
	sandboxPath := filepath.Join(r.layout.SandboxesDir(), sandboxName(name))

	// Kill tmux server via socket
	tmuxSock := filepath.Join(sandboxPath, tmuxDir, tmuxSocketName)
	if _, err := os.Stat(tmuxSock); err == nil {
		killCmd := exec.Command("tmux", "-S", tmuxSock, "kill-server") //nolint:gosec // G204: path within sandbox dir
		_ = killCmd.Run()
	}

	// Kill the sandbox-exec process
	r.killByPID(sandboxPath)

	return nil
}

// Remove stops the instance and removes all sandbox state from disk.
func (r *Runtime) Remove(ctx context.Context, name string) error {
	sandboxPath := filepath.Join(r.layout.SandboxesDir(), sandboxName(name))

	_ = r.Stop(ctx, name)

	// Clean up external mount symlinks before removing the sandbox directory,
	// since the symlink manifest lives inside sandboxPath.
	manifestPath := filepath.Join(sandboxPath, backendDir, symlinkManifestName)
	if data, err := os.ReadFile(manifestPath); err == nil { //nolint:gosec // G304: path within sandbox dir
		for linkPath := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
			if linkPath == "" {
				continue
			}
			_ = os.Remove(linkPath) //nolint:gosec // G703: linkPath is derived from internal agent mount config
			parent := filepath.Dir(linkPath)
			_ = os.Remove(parent) //nolint:gosec // G703: parent is filepath.Dir of an internal controlled path
		}
	}

	_ = os.RemoveAll(sandboxPath)
	return nil
}

// Inspect returns the current state of the sandboxed process.
func (r *Runtime) Inspect(_ context.Context, name string) (runtime.InstanceInfo, error) {
	sandboxPath := filepath.Join(r.layout.SandboxesDir(), sandboxName(name))

	// Use the instance config as the existence marker — it's written by Create,
	// while the PID file only exists after Start.
	cfgPath := filepath.Join(sandboxPath, backendDir, seatbeltConfigFileName)
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		return runtime.InstanceInfo{}, runtime.ErrNotFound
	}

	return runtime.InstanceInfo{
		Running: r.isRunning(sandboxPath),
	}, nil
}

// Exec runs a command inside the sandbox. For tmux commands, injects the
// per-sandbox socket. For other commands, runs under sandbox-exec.
func (r *Runtime) Exec(_ context.Context, name string, cmd []string, _ string) (runtime.ExecResult, error) {
	sandboxPath := filepath.Join(r.layout.SandboxesDir(), sandboxName(name))

	if !r.isRunning(sandboxPath) {
		return runtime.ExecResult{}, runtime.ErrNotRunning
	}

	execCmd := r.buildExecCommand(sandboxPath, cmd)

	return runtime.RunCmdExec(execCmd)
}

// InteractiveExec runs a command with the supplied IOStreams. For tmux
// commands, buildExecCommand injects the per-sandbox socket; other commands run
// under sandbox-exec. When streams.TTY is set the child runs under a locally
// allocated PTY (runtime.PTYBridgeExec) rather than inheriting the host stdio —
// the bridge keeps error output from stair-stepping under the CLI's raw mode and
// makes the path safe for non-CLI embedders whose streams aren't real *os.Files.
func (r *Runtime) InteractiveExec(_ context.Context, name string, cmd []string, _ string, _ string, streams runtime.IOStreams) error {
	sandboxPath := filepath.Join(r.layout.SandboxesDir(), sandboxName(name))
	execCmd := r.buildExecCommand(sandboxPath, cmd)
	return runtime.PTYBridgeExec(execCmd, streams)
}

// Close is a no-op for seatbelt (no persistent connection).
func (r *Runtime) Close() error {
	return nil
}

// DiagHint returns a seatbelt-specific hint for checking logs.
func (r *Runtime) DiagHint(instanceName string) string {
	logPath := filepath.Join(r.layout.SandboxesDir(), sandboxName(instanceName), backendDir, processLogFileName)
	return fmt.Sprintf("check log at %s", logPath)
}

// PrepareAgentCommand prepends the backend's constant launch wrap (see
// descriptor.AgentLaunchPrefix) — sourcing the Swift wrapper that auto-adds
// --disable-sandbox for Swift PM commands inside the macOS sandbox.
func (r *Runtime) PrepareAgentCommand(cmd string) string {
	return descriptor.AgentLaunchPrefix + cmd
}

// TmuxSocket returns the per-sandbox tmux socket path for seatbelt. Each
// seatbelt sandbox has its own socket under its sandbox directory, so the
// socket path is derived from sandboxDir.
func (r *Runtime) TmuxSocket(sandboxDir string) string {
	return filepath.Join(sandboxDir, tmuxDir, tmuxSocketName)
}

// AttachCommand returns the command to attach to the tmux session for seatbelt.
// Seatbelt runs commands directly with the caller's terminal; InteractiveExec
// injects the per-sandbox socket path via buildTmuxCommand.
func (r *Runtime) AttachCommand(tmuxSocket string, _ int, _ int, _ runtime.IsolationMode) []string {
	cmd := []string{"tmux"}
	if tmuxSocket != "" {
		cmd = append(cmd, "-S", tmuxSocket)
	}
	return append(cmd, "attach", "-t", "main")
}

// mountSymlinks creates symlinks from Container → Host for mounts where the
// paths differ, allowing the sandboxed process to find directories at the
// expected target path. Returns the list of created symlink paths.
func mountSymlinks(mounts []runtime.MountSpec) ([]string, error) {
	var created []string
	for _, m := range mounts {
		if m.HostPath == "" || m.HostPath == m.ContainerPath {
			continue
		}
		// Skip secrets — they're handled separately
		if strings.HasPrefix(m.ContainerPath, "/run/secrets/") {
			continue
		}
		// Only symlink directories, not individual files
		info, err := os.Stat(m.HostPath)
		if err != nil || !info.IsDir() {
			continue
		}
		// Skip if target already exists on the host (e.g., copy-mode workdir
		// where Target is the original host path that still exists).
		if _, err := os.Lstat(m.ContainerPath); err == nil {
			continue
		}
		// Create parent directory if needed. Silently skip unreachable paths
		// — on macOS, /home is managed by auto_master and may not be writable,
		// and sandbox-exec restrictions can prevent directory creation in
		// certain locations. The entrypoint script handles these cases internally
		// by setting up paths within its sandboxed HOME.
		if err := fileutil.MkdirAll(filepath.Dir(m.ContainerPath), 0750); err != nil { //nolint:gosec // G301: parent dirs for mount symlinks
			continue
		}
		if err := os.Symlink(m.HostPath, m.ContainerPath); err != nil {
			return created, fmt.Errorf("create symlink %s -> %s: %w", m.ContainerPath, m.HostPath, err)
		}
		if err := fileutil.ChownIfSudo(m.ContainerPath); err != nil {
			return created, fmt.Errorf("chown symlink %s: %w", m.ContainerPath, err)
		}
		created = append(created, m.ContainerPath)
	}
	return created, nil
}

// --- Internal helpers ---

const instancePrefix = "yoloai-"

// sandboxName strips the instance prefix to recover the sandbox name.
func sandboxName(instanceName string) string {
	return strings.TrimPrefix(instanceName, instancePrefix)
}

// isRunning checks if the sandbox-exec process is alive.
func (r *Runtime) isRunning(sandboxPath string) bool {
	pidPath := filepath.Join(sandboxPath, backendDir, pidFileName)
	data, err := os.ReadFile(pidPath) //nolint:gosec // G304: path within sandbox dir
	if err != nil {
		return false
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return false
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	// Signal(0) checks if the process exists without actually sending a signal
	return proc.Signal(syscall.Signal(0)) == nil
}

// killByPID reads the PID file, sends SIGTERM, waits for exit, and
// escalates to SIGKILL if the process doesn't die in time. This ensures
// the process is fully gone before returning, preventing race conditions
// when --replace destroys and recreates the sandbox directory.
func (r *Runtime) killByPID(sandboxPath string) {
	pidPath := filepath.Join(sandboxPath, backendDir, pidFileName)
	data, err := os.ReadFile(pidPath) //nolint:gosec // G304: path within sandbox dir
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

	// Send SIGTERM to process group and process
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	_ = proc.Signal(syscall.SIGTERM)

	// Wait for process to exit (poll every 100ms, up to 5s)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if proc.Signal(syscall.Signal(0)) != nil {
			// Process is gone
			_ = os.Remove(pidPath)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Still alive — escalate to SIGKILL
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = proc.Signal(syscall.SIGKILL)
	time.Sleep(500 * time.Millisecond)

	_ = os.Remove(pidPath)
}

// sandboxEnv returns a filtered subset of the caller's threaded environment
// snapshot (layout.Env), passing only safe OS/locale variables. Credentials
// like SSH_AUTH_SOCK, AWS_SECRET_ACCESS_KEY, etc. are excluded. The entrypoint
// injects agent API keys from the secrets directory; users can opt in to
// additional env vars via the config env: section. The library reads
// layout.Env, never os.Environ (§12) — the CLI captures the host env once at
// its boundary and threads it in.
func (r *Runtime) sandboxEnv() []string {
	allowed := map[string]bool{
		"PATH": true, "HOME": true, "USER": true, "LOGNAME": true,
		"SHELL": true, "TERM": true, "TMPDIR": true,
		"LANG": true, "LC_ALL": true, "LC_CTYPE": true,
		"LC_COLLATE": true, "LC_MESSAGES": true, "LC_MONETARY": true,
		"LC_NUMERIC": true, "LC_TIME": true,
	}
	var filtered []string
	for k, v := range r.layout.Env {
		if allowed[k] {
			filtered = append(filtered, k+"="+v)
		}
	}
	return filtered
}

// waitForTmux polls until the tmux session appears via the per-sandbox socket.
func (r *Runtime) waitForTmux(ctx context.Context, sandboxPath string, procDone <-chan error) error {
	tmuxSock := filepath.Join(sandboxPath, tmuxDir, tmuxSocketName)
	deadline := time.Now().Add(30 * time.Second)

	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("tmux session did not appear within 30s")
		}

		// Check if process exited early
		select {
		case procErr := <-procDone:
			if procErr != nil {
				return fmt.Errorf("sandbox-exec exited: %w", procErr)
			}
			return fmt.Errorf("sandbox-exec exited unexpectedly")
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Try to list tmux sessions via the socket
		checkCmd := exec.Command("tmux", "-S", tmuxSock, "has-session", "-t", "main") //nolint:gosec // G204: path within sandbox dir
		if checkCmd.Run() == nil {
			return nil
		}

		select {
		case procErr := <-procDone:
			if procErr != nil {
				return fmt.Errorf("sandbox-exec exited: %w", procErr)
			}
			return fmt.Errorf("sandbox-exec exited unexpectedly")
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// buildExecCommand constructs the exec.Cmd for running a command.
// Tmux commands get the per-sandbox socket injected; other commands
// run under sandbox-exec with the SBPL profile.
func (r *Runtime) buildExecCommand(sandboxPath string, cmd []string) *exec.Cmd {
	if len(cmd) > 0 && cmd[0] == "tmux" {
		return r.buildTmuxCommand(sandboxPath, cmd)
	}

	// Run under sandbox-exec with the SBPL profile
	profilePath := filepath.Join(sandboxPath, backendDir, profileFileName)
	args := []string{"-f", profilePath}
	args = append(args, cmd...)
	c := exec.Command(r.sandboxExecBin, args...) //nolint:gosec // G204: args from validated sandbox state

	// Read working directory from runtime-config.json, which is the source of truth
	// for seatbelt. patchConfigWorkingDir (called during Start) rewrites it
	// to the actual copy location for :copy sandboxes. We don't use the
	// caller-supplied workDir because it comes from environment.json mount_path,
	// which stores the Docker-oriented target path (the original host path),
	// not the seatbelt copy path.
	cfgPath := filepath.Join(sandboxPath, "runtime-config.json")
	if data, err := os.ReadFile(cfgPath); err == nil { //nolint:gosec // G304: path within sandbox dir
		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err == nil {
			if wd, ok := raw["working_dir"].(string); ok && wd != "" {
				c.Dir = wd
			}
		}
	}

	return c
}

// buildTmuxCommand injects the per-sandbox socket into a tmux command.
func (r *Runtime) buildTmuxCommand(sandboxPath string, cmd []string) *exec.Cmd {
	tmuxSock := filepath.Join(sandboxPath, tmuxDir, tmuxSocketName)

	// cmd[0] is "tmux", inject -S <socket> after it
	args := []string{"-S", tmuxSock}
	if len(cmd) > 1 {
		args = append(args, cmd[1:]...)
	}
	return exec.Command("tmux", args...) //nolint:gosec // G204: socket path within sandbox dir
}

// patchConfigWorkingDir rewrites working_dir in runtime-config.json when the
// workdir mount is a copy (source differs from target).
func (r *Runtime) patchConfigWorkingDir(sandboxPath string, mounts []runtime.MountSpec) error {
	// Find the workdir mount: it's the first non-readonly mount whose
	// source is under <sandboxPath>/work/
	workPrefix := filepath.Join(sandboxPath, "work") + "/"
	var copySource string
	for _, m := range mounts {
		if !m.ReadOnly && strings.HasPrefix(m.HostPath, workPrefix) {
			copySource = m.HostPath
			break
		}
	}
	if copySource == "" {
		return nil // not a copy-mode sandbox
	}

	cfgPath := filepath.Join(sandboxPath, "runtime-config.json")
	data, err := os.ReadFile(cfgPath) //nolint:gosec // G304: path within sandbox dir
	if err != nil {
		return err
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	if wd, ok := raw["working_dir"].(string); ok && wd != copySource {
		raw["working_dir"] = copySource
		out, err := json.MarshalIndent(raw, "", "  ")
		if err != nil {
			return err
		}
		return fileutil.WriteFile(cfgPath, out, 0600)
	}

	return nil
}

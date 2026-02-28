// Package seatbelt implements runtime.Runtime using macOS sandbox-exec.
// ABOUTME: Runs agent processes under sandbox-exec SBPL profiles for lightweight isolation.
package seatbelt

import (
	"bytes"
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

	"github.com/kstenerud/yoloai/internal/runtime"
)

const (
	// pidFileName stores the sandbox-exec process ID.
	pidFileName = "seatbelt.pid"

	// processLogFileName captures sandbox-exec stderr for debugging.
	processLogFileName = "seatbelt.log"

	// seatbeltConfigFileName stores the instance config for Start to use.
	seatbeltConfigFileName = "seatbelt-instance.json"

	// profileFileName is the generated SBPL profile.
	profileFileName = "profile.sb"

	// tmuxSocketName is the per-sandbox tmux socket filename.
	tmuxSocketName = "tmux.sock"

	// symlinkManifestName tracks mount symlinks for cleanup.
	symlinkManifestName = "mount-symlinks.txt"
)

// Runtime implements runtime.Runtime using macOS sandbox-exec.
type Runtime struct {
	sandboxExecBin string // path to sandbox-exec binary
	sandboxDir     string // ~/.yoloai/sandboxes/ base path
}

// Compile-time check.
var _ runtime.Runtime = (*Runtime)(nil)

// New creates a Runtime after verifying that we're on macOS and
// sandbox-exec is available.
func New(_ context.Context) (*Runtime, error) {
	if !isMacOS() {
		return nil, fmt.Errorf("seatbelt backend requires macOS")
	}

	sandboxExecBin, err := exec.LookPath("sandbox-exec")
	if err != nil {
		return nil, fmt.Errorf("sandbox-exec not found: %w", err)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home directory: %w", err)
	}

	return &Runtime{
		sandboxExecBin: sandboxExecBin,
		sandboxDir:     filepath.Join(homeDir, ".yoloai", "sandboxes"),
	}, nil
}

// Create saves the instance config, copies secrets into the sandbox
// directory, patches working_dir for :copy mode, generates the SBPL
// profile, and writes the entrypoint script and tmux config.
func (r *Runtime) Create(_ context.Context, cfg runtime.InstanceConfig) error {
	sandboxPath := filepath.Join(r.sandboxDir, sandboxName(cfg.Name))

	// Save instance config so Start can read it
	cfgData, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal instance config: %w", err)
	}
	if err := os.WriteFile(filepath.Join(sandboxPath, seatbeltConfigFileName), cfgData, 0600); err != nil {
		return fmt.Errorf("write instance config: %w", err)
	}

	// Copy secrets from mount spec into sandbox secrets dir.
	// launchContainer creates a temp secrets dir; we copy those files into
	// the sandbox so the entrypoint can read them after the temp dir is removed.
	secretsDir := filepath.Join(sandboxPath, "secrets")
	if err := os.MkdirAll(secretsDir, 0700); err != nil {
		return fmt.Errorf("create secrets dir: %w", err)
	}
	for _, m := range cfg.Mounts {
		if !strings.HasPrefix(m.Target, "/run/secrets/") {
			continue
		}
		data, err := os.ReadFile(m.Source) //nolint:gosec // G304: source is from validated mount spec
		if err != nil {
			continue // skip missing secrets (may have been cleaned up)
		}
		keyName := filepath.Base(m.Target)
		if err := os.WriteFile(filepath.Join(secretsDir, keyName), data, 0600); err != nil {
			return fmt.Errorf("copy secret %s: %w", keyName, err)
		}
	}

	// Patch working_dir in config.json for :copy mode.
	// When the workdir is a copy, the actual files are in
	// <sandboxDir>/work/<encoded>/ but config.json still has the original
	// host path. Patch it to point at the copy.
	if err := r.patchConfigWorkingDir(sandboxPath, cfg.Mounts); err != nil {
		return fmt.Errorf("patch config working dir: %w", err)
	}

	// Generate SBPL profile
	homeDir, _ := os.UserHomeDir()
	profile := GenerateProfile(cfg, sandboxPath, homeDir)
	if err := os.WriteFile(filepath.Join(sandboxPath, profileFileName), []byte(profile), 0600); err != nil {
		return fmt.Errorf("write SBPL profile: %w", err)
	}

	// Write entrypoint and tmux config
	entrypointPath := filepath.Join(sandboxPath, "entrypoint.sh")
	if err := os.WriteFile(entrypointPath, embeddedEntrypoint, 0755); err != nil { //nolint:gosec // G306: script needs exec permission
		return fmt.Errorf("write entrypoint.sh: %w", err)
	}
	tmuxConfPath := filepath.Join(sandboxPath, "tmux.conf")
	if err := os.WriteFile(tmuxConfPath, embeddedTmuxConf, 0600); err != nil {
		return fmt.Errorf("write tmux.conf: %w", err)
	}

	// Create symlinks for mounts where Target != Source so the sandboxed
	// process can find directories at the expected paths.
	symlinks, err := mountSymlinks(cfg.Mounts)
	if err != nil {
		return fmt.Errorf("create mount symlinks: %w", err)
	}
	if len(symlinks) > 0 {
		manifest := strings.Join(symlinks, "\n") + "\n"
		if err := os.WriteFile(filepath.Join(sandboxPath, symlinkManifestName), []byte(manifest), 0600); err != nil {
			return fmt.Errorf("write symlink manifest: %w", err)
		}
	}

	return nil
}

// Start launches the sandboxed process in the background and waits for
// the tmux session to become available.
func (r *Runtime) Start(ctx context.Context, name string) error {
	sandboxPath := filepath.Join(r.sandboxDir, sandboxName(name))

	// Check if already running
	if r.isRunning(sandboxPath) {
		return nil
	}

	// Load instance config saved by Create
	var cfg runtime.InstanceConfig
	cfgPath := filepath.Join(sandboxPath, seatbeltConfigFileName)
	cfgData, err := os.ReadFile(cfgPath) //nolint:gosec // G304: path within sandbox dir
	if err != nil {
		return fmt.Errorf("read instance config: %w", err)
	}
	if err := json.Unmarshal(cfgData, &cfg); err != nil {
		return fmt.Errorf("parse instance config: %w", err)
	}

	// Open log file for stderr capture
	logPath := filepath.Join(sandboxPath, processLogFileName)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600) //nolint:gosec // G304: sandboxPath is ~/.yoloai/sandboxes/<name>
	if err != nil {
		return fmt.Errorf("open log: %w", err)
	}

	// Launch sandbox-exec with the SBPL profile running the entrypoint
	profilePath := filepath.Join(sandboxPath, profileFileName)
	entrypointPath := filepath.Join(sandboxPath, "entrypoint.sh")

	cmd := exec.Command(r.sandboxExecBin, "-f", profilePath, "bash", entrypointPath, sandboxPath) //nolint:gosec // G204: paths are constructed from validated config
	cmd.Stderr = logFile
	cmd.Stdout = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	if err := cmd.Start(); err != nil {
		logFile.Close() //nolint:errcheck,gosec // best-effort
		return fmt.Errorf("start sandbox-exec: %w", err)
	}

	// Write PID file
	pidPath := filepath.Join(sandboxPath, pidFileName)
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0600); err != nil {
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
		detail := fmt.Sprintf("command: %s -f %s bash %s %s", r.sandboxExecBin, profilePath, entrypointPath, sandboxPath)
		if logData, readErr := os.ReadFile(logPath); readErr == nil && len(logData) > 0 { //nolint:gosec // G304: path within sandbox dir
			detail += fmt.Sprintf("\nlog output:\n%s", strings.TrimSpace(string(logData)))
		}
		return fmt.Errorf("wait for tmux session: %w\n%s", err, detail)
	}

	return nil
}

// Stop kills the sandbox-exec process and the tmux server.
func (r *Runtime) Stop(_ context.Context, name string) error {
	sandboxPath := filepath.Join(r.sandboxDir, sandboxName(name))

	// Kill tmux server via socket
	tmuxSock := filepath.Join(sandboxPath, tmuxSocketName)
	if _, err := os.Stat(tmuxSock); err == nil {
		killCmd := exec.Command("tmux", "-S", tmuxSock, "kill-server") //nolint:gosec // G204: path within sandbox dir
		_ = killCmd.Run()
	}

	// Kill the sandbox-exec process
	r.killByPID(sandboxPath)

	return nil
}

// Remove stops the instance and cleans up seatbelt-specific files.
func (r *Runtime) Remove(ctx context.Context, name string) error {
	sandboxPath := filepath.Join(r.sandboxDir, sandboxName(name))

	_ = r.Stop(ctx, name)

	// Clean up mount symlinks
	manifestPath := filepath.Join(sandboxPath, symlinkManifestName)
	if data, err := os.ReadFile(manifestPath); err == nil { //nolint:gosec // G304: path within sandbox dir
		for _, linkPath := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if linkPath == "" {
				continue
			}
			_ = os.Remove(linkPath)
			// Try to remove empty parent dirs we may have created
			parent := filepath.Dir(linkPath)
			_ = os.Remove(parent) // only succeeds if empty
		}
	}

	// Clean up seatbelt-specific files
	for _, f := range []string{
		pidFileName,
		profileFileName,
		seatbeltConfigFileName,
		processLogFileName,
		tmuxSocketName,
		"entrypoint.sh",
		"tmux.conf",
		symlinkManifestName,
	} {
		_ = os.Remove(filepath.Join(sandboxPath, f))
	}

	// Clean up secrets directory
	_ = os.RemoveAll(filepath.Join(sandboxPath, "secrets"))

	return nil
}

// Inspect returns the current state of the sandboxed process.
func (r *Runtime) Inspect(_ context.Context, name string) (runtime.InstanceInfo, error) {
	sandboxPath := filepath.Join(r.sandboxDir, sandboxName(name))

	pidPath := filepath.Join(sandboxPath, pidFileName)
	if _, err := os.Stat(pidPath); os.IsNotExist(err) {
		return runtime.InstanceInfo{}, runtime.ErrNotFound
	}

	return runtime.InstanceInfo{
		Running: r.isRunning(sandboxPath),
	}, nil
}

// Exec runs a command inside the sandbox. For tmux commands, injects the
// per-sandbox socket. For other commands, runs under sandbox-exec.
func (r *Runtime) Exec(_ context.Context, name string, cmd []string, _ string) (runtime.ExecResult, error) {
	sandboxPath := filepath.Join(r.sandboxDir, sandboxName(name))

	if !r.isRunning(sandboxPath) {
		return runtime.ExecResult{}, runtime.ErrNotRunning
	}

	execCmd := r.buildExecCommand(sandboxPath, cmd)

	var stdout, stderr bytes.Buffer
	execCmd.Stdout = &stdout
	execCmd.Stderr = &stderr

	err := execCmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok { //nolint:errorlint // ExitError is concrete type
			exitCode = exitErr.ExitCode()
		} else {
			return runtime.ExecResult{}, fmt.Errorf("exec: %w", err)
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

// InteractiveExec runs a command interactively. For tmux commands, injects
// the per-sandbox socket. For other commands, runs under sandbox-exec.
func (r *Runtime) InteractiveExec(_ context.Context, name string, cmd []string, _ string) error {
	sandboxPath := filepath.Join(r.sandboxDir, sandboxName(name))

	execCmd := r.buildExecCommand(sandboxPath, cmd)
	execCmd.Stdin = os.Stdin
	execCmd.Stdout = os.Stdout
	execCmd.Stderr = os.Stderr

	return execCmd.Run()
}

// Close is a no-op for seatbelt (no persistent connection).
func (r *Runtime) Close() error {
	return nil
}

// DiagHint returns a seatbelt-specific hint for checking logs.
func (r *Runtime) DiagHint(instanceName string) string {
	logPath := filepath.Join(r.sandboxDir, sandboxName(instanceName), processLogFileName)
	return fmt.Sprintf("check log at %s", logPath)
}

// mountSymlinks creates symlinks from Target → Source for mounts where the
// paths differ, allowing the sandboxed process to find directories at the
// expected target path. Returns the list of created symlink paths.
func mountSymlinks(mounts []runtime.MountSpec) ([]string, error) {
	var created []string
	for _, m := range mounts {
		if m.Source == "" || m.Source == m.Target {
			continue
		}
		// Skip secrets — they're handled separately
		if strings.HasPrefix(m.Target, "/run/secrets/") {
			continue
		}
		// Only symlink directories, not individual files
		info, err := os.Stat(m.Source)
		if err != nil || !info.IsDir() {
			continue
		}
		// Skip if target already exists on the host (e.g., copy-mode workdir
		// where Target is the original host path that still exists).
		if _, err := os.Lstat(m.Target); err == nil {
			continue
		}
		// Create parent directory if needed. Skip unreachable paths
		// (e.g., /home/yoloai/.claude/ — macOS restricts /home via
		// auto_master; the entrypoint handles these internally).
		if err := os.MkdirAll(filepath.Dir(m.Target), 0750); err != nil { //nolint:gosec // G301: parent dirs for mount symlinks
			continue
		}
		if err := os.Symlink(m.Source, m.Target); err != nil {
			return created, fmt.Errorf("create symlink %s -> %s: %w", m.Target, m.Source, err)
		}
		created = append(created, m.Target)
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
	pidPath := filepath.Join(sandboxPath, pidFileName)
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

// killByPID reads the PID file and kills the process.
func (r *Runtime) killByPID(sandboxPath string) {
	pidPath := filepath.Join(sandboxPath, pidFileName)
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

	// Kill the entire process group (negative PID)
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	_ = proc.Signal(syscall.SIGTERM)
	_ = os.Remove(pidPath)
}

// waitForTmux polls until the tmux session appears via the per-sandbox socket.
func (r *Runtime) waitForTmux(ctx context.Context, sandboxPath string, procDone <-chan error) error {
	tmuxSock := filepath.Join(sandboxPath, tmuxSocketName)
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
	profilePath := filepath.Join(sandboxPath, profileFileName)
	args := []string{"-f", profilePath}
	args = append(args, cmd...)
	return exec.Command(r.sandboxExecBin, args...) //nolint:gosec // G204: args from validated sandbox state
}

// buildTmuxCommand injects the per-sandbox socket into a tmux command.
func (r *Runtime) buildTmuxCommand(sandboxPath string, cmd []string) *exec.Cmd {
	tmuxSock := filepath.Join(sandboxPath, tmuxSocketName)

	// cmd[0] is "tmux", inject -S <socket> after it
	args := []string{"-S", tmuxSock}
	if len(cmd) > 1 {
		args = append(args, cmd[1:]...)
	}
	return exec.Command("tmux", args...) //nolint:gosec // G204: socket path within sandbox dir
}

// patchConfigWorkingDir rewrites working_dir in config.json when the
// workdir mount is a copy (source differs from target).
func (r *Runtime) patchConfigWorkingDir(sandboxPath string, mounts []runtime.MountSpec) error {
	// Find the workdir mount: it's the first non-readonly mount whose
	// source is under <sandboxPath>/work/
	workPrefix := filepath.Join(sandboxPath, "work") + "/"
	var copySource string
	for _, m := range mounts {
		if !m.ReadOnly && strings.HasPrefix(m.Source, workPrefix) {
			copySource = m.Source
			break
		}
	}
	if copySource == "" {
		return nil // not a copy-mode sandbox
	}

	cfgPath := filepath.Join(sandboxPath, "config.json")
	data, err := os.ReadFile(cfgPath) //nolint:gosec // G304: path within sandbox dir
	if err != nil {
		return err
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	if wd, ok := raw["working_dir"].(string); ok && wd != copySource {
		raw["working_dir"] = copySource
		out, err := json.MarshalIndent(raw, "", "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(cfgPath, out, 0600)
	}

	return nil
}

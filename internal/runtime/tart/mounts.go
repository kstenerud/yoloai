// ABOUTME: Tart VirtioFS mount subsystem — builds the in-VM symlink commands that
// ABOUTME: map shared VirtioFS dirs to their expected guest paths, plus setup scripts.
package tart

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/runtime/monitor"
)

// BuildMountSymlinkCmds returns shell commands to create symlinks from
// expected mount targets to their actual VirtioFS paths. Exported for testing.
func BuildMountSymlinkCmds(mounts []runtime.MountSpec, dirNames map[string]string) []string {
	var cmds []string
	for _, m := range mounts {
		dirName, ok := dirNames[m.HostPath]
		if !ok {
			continue
		}
		vfsPath := filepath.Join(sharedDirVMPath, dirName)
		if vfsPath == m.ContainerPath {
			continue // no symlink needed
		}
		parent := filepath.Dir(m.ContainerPath)
		cmds = append(cmds, fmt.Sprintf("sudo mkdir -p %q", parent))
		cmds = append(cmds, fmt.Sprintf("sudo ln -sf %q %q", vfsPath, m.ContainerPath))
	}
	return cmds
}

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

	// P1: wire the mounts into the guest. Always — a bare runtime instance still
	// needs its mounts reachable at the expected paths.
	if err := r.createVMMountSymlinks(ctx, vmName, sandboxPath, vmSharedDir, mounts); err != nil {
		return err
	}

	// P2: sandbox provisioning (workdir remap + the sandbox-setup.py monitor) runs
	// only when the sandbox layer has provisioned a runtime-config.json. Absent it
	// — a bare runtime Start (direct runtime.Backend use / the conformance suite)
	// — the VM is left booted, mounted, and exec-able with no monitor. This keeps
	// tart's Start a clean P1 like every other backend's, with P2 gated on the
	// sandbox handshake (the config file) rather than fused into Start.
	if _, err := os.Stat(filepath.Join(sandboxPath, "runtime-config.json")); os.IsNotExist(err) {
		return nil
	}

	if err := r.patchConfigWorkingDir(sandboxPath); err != nil {
		return fmt.Errorf("patch config working dir: %w", err)
	}

	if err := writeVMSetupScripts(sandboxPath); err != nil {
		return err
	}

	setupCmd := fmt.Sprintf("nohup python3 '%s/bin/sandbox-setup.py' tart '%s' </dev/null >'%s/setup.log' 2>&1 &",
		vmSharedDir, vmSharedDir, vmSharedDir)
	args := execArgs(vmName, "bash", "-c", setupCmd)
	if _, err := r.runTart(ctx, args...); err != nil {
		return fmt.Errorf("exec setup script: %w", err)
	}

	return nil
}

// createVMMountSymlinks creates symlinks in the VM from expected mount targets to VirtioFS paths.
func (r *Runtime) createVMMountSymlinks(ctx context.Context, vmName, sandboxPath, vmSharedDir string, mounts []runtime.MountSpec) error {
	for _, m := range mounts {
		if m.ContainerPath == "/run/secrets" || strings.HasPrefix(m.ContainerPath, "/run/secrets/") {
			continue
		}

		target := remapTargetPath(m.ContainerPath)
		slog.Debug("tart setup: processing mount", "source", m.HostPath, "target", target)

		if strings.HasPrefix(target, "/Users/admin/yoloai-work/") {
			slog.Debug("tart setup: skipping copy workdir (handled by executeVMWorkDirSetup)", "target", target)
			continue
		}

		vfsPath, ok := resolveMountVFSPath(m.HostPath, sandboxPath, vmSharedDir)
		if !ok {
			continue
		}

		target = strings.TrimRight(target, "/")
		if vfsPath == target {
			continue
		}

		if err := r.createSingleVMSymlink(ctx, vmName, target, vfsPath); err != nil {
			return err
		}
	}
	return nil
}

// resolveMountVFSPath resolves the VirtioFS path for a mount source.
// Returns the vfsPath and true if the mount should be symlinked, or ("", false) to skip.
func resolveMountVFSPath(source, sandboxPath, vmSharedDir string) (string, bool) {
	if after, ok := strings.CutPrefix(source, sandboxPath+"/"); ok {
		relPath := after
		vfsPath := filepath.Join(vmSharedDir, relPath)
		if stat, err := os.Stat(source); err != nil {
			slog.Debug("tart setup: mount source does not exist on host!", "source", source, "err", err)
			return "", false
		} else {
			slog.Debug("tart setup: mount under sandbox", "source", source, "relPath", relPath, "vfsPath", vfsPath, "sourceIsDir", stat.IsDir())
		}
		return vfsPath, true
	}
	if source == sandboxPath {
		return vmSharedDir, true
	}
	if info, err := os.Stat(source); err == nil && info.IsDir() {
		return filepath.Join(sharedDirVMPath, mountDirName(source)), true
	}
	return "", false
}

// createSingleVMSymlink creates a single symlink in the VM from target to vfsPath.
func (r *Runtime) createSingleVMSymlink(ctx context.Context, vmName, target, vfsPath string) error {
	parent := filepath.Dir(target)

	checkCmd := fmt.Sprintf("ls -la '%s' 2>&1 || echo 'PATH_DOES_NOT_EXIST'", filepath.Dir(vfsPath))
	if out, checkErr := r.runTart(ctx, execArgs(vmName, "bash", "-c", checkCmd)...); checkErr == nil {
		slog.Debug("tart setup: VirtioFS parent directory listing", "path", filepath.Dir(vfsPath), "output", out)
	} else {
		slog.Debug("tart setup: failed to list VirtioFS parent", "path", filepath.Dir(vfsPath), "err", checkErr)
	}

	mkdirCmd := fmt.Sprintf("(mkdir -p '%s' 2>/dev/null || sudo mkdir -p '%s' 2>/dev/null || true)", parent, parent)
	if _, mkdirErr := r.runTart(ctx, execArgs(vmName, "bash", "-c", mkdirCmd)...); mkdirErr != nil {
		return fmt.Errorf("create parent directory %s: %w", parent, mkdirErr)
	}

	symlinkCmd := fmt.Sprintf(
		"(rm -rf '%s' && ln -sfn '%s' '%s') 2>/dev/null || (sudo rm -rf '%s' && sudo ln -sfn '%s' '%s')",
		target, vfsPath, target, target, vfsPath, target,
	)
	args := execArgs(vmName, "bash", "-c", symlinkCmd)
	slog.Debug("tart setup: creating symlink", "vm", vmName, "target", target, "vfsPath", vfsPath, "cmd", symlinkCmd)
	if _, err := r.runTart(ctx, args...); err != nil {
		if !r.isRunning(ctx, vmName) {
			return fmt.Errorf("create mount symlink for %s (VM appears to have crashed): %w", target, err)
		}
		return fmt.Errorf("create mount symlink for %s: %w", target, err)
	}
	return nil
}

// writeVMSetupScripts writes setup script, status monitor, and tmux config to the sandbox dir.
func writeVMSetupScripts(sandboxPath string) error {
	scriptPath := filepath.Join(sandboxPath, binDir, "sandbox-setup.py")
	if err := fileutil.WriteFile(scriptPath, monitor.SetupScript(), 0644); err != nil { //nolint:gosec // G306: script content
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
		return fmt.Errorf("write status monitor: %w", err)
	}
	diagPath := filepath.Join(sandboxPath, binDir, "diagnose-idle.sh")
	if err := fileutil.WriteFile(diagPath, monitor.DiagnoseScript(), 0755); err != nil { //nolint:gosec // G306: script needs exec permission
		return fmt.Errorf("write diagnose script: %w", err)
	}
	agentRunPath := filepath.Join(sandboxPath, binDir, "agent-run.sh")
	if err := fileutil.WriteFile(agentRunPath, monitor.AgentRunScript(), 0755); err != nil { //nolint:gosec // G306: wrapper needs exec permission
		return fmt.Errorf("write agent-run.sh: %w", err)
	}
	tmuxConfPath := filepath.Join(sandboxPath, tmuxDir, "tmux.conf")
	if err := fileutil.WriteFile(tmuxConfPath, embeddedTmuxConf, 0600); err != nil {
		return fmt.Errorf("write tmux.conf: %w", err)
	}
	return nil
}

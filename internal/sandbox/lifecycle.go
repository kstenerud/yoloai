package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/kstenerud/yoloai/internal/agent"
)

// ResetOptions holds parameters for the reset command.
type ResetOptions struct {
	Name      string
	Clean     bool // also wipe agent-state directory
	NoPrompt  bool // skip re-sending prompt after reset
	NoRestart bool // keep agent running, reset workspace in-place
}

// Stop stops a sandbox's container via Docker SDK.
// Returns nil if the container is already stopped or removed.
func (m *Manager) Stop(ctx context.Context, name string) error {
	if _, err := RequireSandboxDir(name); err != nil {
		return err
	}

	if err := m.client.ContainerStop(ctx, ContainerName(name), container.StopOptions{}); err != nil {
		if cerrdefs.IsNotFound(err) || isNotRunningErr(err) {
			return nil
		}
		return fmt.Errorf("stop container: %w", err)
	}
	return nil
}

// Start ensures a sandbox is running — idempotent.
func (m *Manager) Start(ctx context.Context, name string) error {
	sandboxDir, err := RequireSandboxDir(name)
	if err != nil {
		return err
	}

	meta, err := LoadMeta(sandboxDir)
	if err != nil {
		return err
	}

	cname := ContainerName(name)
	status, _, err := DetectStatus(ctx, m.client, cname)
	if err != nil {
		return fmt.Errorf("detect status: %w", err)
	}

	switch status {
	case StatusRunning:
		fmt.Fprintf(m.output, "Sandbox %s is already running\n", name) //nolint:errcheck // best-effort output
		return nil

	case StatusDone, StatusFailed:
		if err := m.relaunchAgent(ctx, name, meta); err != nil {
			return err
		}
		fmt.Fprintf(m.output, "Agent relaunched in sandbox %s\n", name) //nolint:errcheck // best-effort output
		return nil

	case StatusStopped:
		if err := m.client.ContainerStart(ctx, cname, container.StartOptions{}); err != nil {
			return fmt.Errorf("start container: %w", err)
		}

		// Verify container stays running (catches immediate crashes)
		time.Sleep(1 * time.Second)
		info, inspectErr := m.client.ContainerInspect(ctx, cname)
		if inspectErr != nil {
			return fmt.Errorf("inspect container after start: %w", inspectErr)
		}
		if !info.State.Running {
			return fmt.Errorf("container exited immediately (exit code %d) — run 'docker logs %s' to see what went wrong", info.State.ExitCode, cname)
		}

		fmt.Fprintf(m.output, "Sandbox %s started\n", name) //nolint:errcheck // best-effort output
		return nil

	case StatusRemoved:
		if err := m.recreateContainer(ctx, name, meta); err != nil {
			return err
		}
		fmt.Fprintf(m.output, "Sandbox %s recreated and started\n", name) //nolint:errcheck // best-effort output
		return nil

	default:
		return fmt.Errorf("unexpected sandbox status: %s", status)
	}
}

// Destroy stops the container, removes it, and deletes the sandbox directory.
// Always destroys unconditionally — confirmation logic is handled by the
// CLI layer via NeedsConfirmation before calling this method.
func (m *Manager) Destroy(ctx context.Context, name string, _ bool) error {
	if _, err := RequireSandboxDir(name); err != nil {
		return err
	}

	cname := ContainerName(name)

	// Stop container (ignore errors — may not be running)
	_ = m.client.ContainerStop(ctx, cname, container.StopOptions{})

	// Remove container (ignore errors — may not exist)
	_ = m.client.ContainerRemove(ctx, cname, container.RemoveOptions{Force: true})

	// Remove sandbox directory
	if err := os.RemoveAll(Dir(name)); err != nil {
		return fmt.Errorf("remove sandbox directory: %w", err)
	}

	return nil
}

// Reset re-copies the workdir from the original host directory and resets
// the git baseline. Stops and restarts the container.
func (m *Manager) Reset(ctx context.Context, opts ResetOptions) error {
	sandboxDir, err := RequireSandboxDir(opts.Name)
	if err != nil {
		return err
	}

	meta, err := LoadMeta(sandboxDir)
	if err != nil {
		return err
	}

	if meta.Workdir.Mode == "rw" {
		return fmt.Errorf("reset is not applicable for :rw directories — changes are already in the original")
	}

	// Check if we can do an in-place reset (--no-restart)
	if opts.NoRestart {
		status, _, err := DetectStatus(ctx, m.client, ContainerName(opts.Name))
		if err != nil || status != StatusRunning {
			fmt.Fprintf(m.output, "Container is not running, falling back to restart\n") //nolint:errcheck // best-effort output
			opts.NoRestart = false
		}
	}

	if opts.NoRestart {
		return m.resetInPlace(ctx, opts, meta, sandboxDir)
	}

	// Stop the container (if running)
	_ = m.Stop(ctx, opts.Name)

	workDir := WorkDir(opts.Name, meta.Workdir.HostPath)

	// Delete work copy
	if err := os.RemoveAll(workDir); err != nil {
		return fmt.Errorf("remove work copy: %w", err)
	}

	// Verify original still exists
	if _, err := os.Stat(meta.Workdir.HostPath); err != nil {
		return fmt.Errorf("original directory no longer exists: %s", meta.Workdir.HostPath)
	}

	// Re-copy
	if err := copyDir(meta.Workdir.HostPath, workDir); err != nil {
		return fmt.Errorf("re-copy workdir: %w", err)
	}

	// Re-create git baseline
	newSHA, err := gitBaseline(workDir)
	if err != nil {
		return fmt.Errorf("re-create git baseline: %w", err)
	}

	// Update meta.json
	meta.Workdir.BaselineSHA = newSHA
	if err := SaveMeta(sandboxDir, meta); err != nil {
		return err
	}

	// Optionally wipe agent-state
	if opts.Clean {
		agentStateDir := filepath.Join(sandboxDir, "agent-state")
		if err := os.RemoveAll(agentStateDir); err != nil {
			return fmt.Errorf("remove agent-state: %w", err)
		}
		if err := os.MkdirAll(agentStateDir, 0750); err != nil {
			return fmt.Errorf("recreate agent-state: %w", err)
		}
	}

	// Handle --no-prompt by temporarily hiding prompt.txt
	promptPath := filepath.Join(sandboxDir, "prompt.txt")
	promptBackup := promptPath + ".bak"
	if opts.NoPrompt {
		if _, err := os.Stat(promptPath); err == nil {
			if renameErr := os.Rename(promptPath, promptBackup); renameErr != nil {
				return fmt.Errorf("hide prompt.txt: %w", renameErr)
			}
			defer os.Rename(promptBackup, promptPath) //nolint:errcheck // best-effort restore
		}
	}

	// Start the container
	return m.Start(ctx, opts.Name)
}

// NeedsConfirmation checks if a sandbox requires confirmation before
// destruction. Returns true if the agent is running or unapplied changes
// exist. Returns a reason string for the confirmation prompt.
func (m *Manager) NeedsConfirmation(ctx context.Context, name string) (bool, string) {
	status, _, err := DetectStatus(ctx, m.client, ContainerName(name))
	if err != nil {
		return false, ""
	}

	if status == StatusRunning {
		return true, "agent is still running"
	}

	meta, err := LoadMeta(Dir(name))
	if err != nil {
		return false, ""
	}

	workDir := WorkDir(name, meta.Workdir.HostPath)
	if detectChanges(workDir) == "yes" {
		return true, "unapplied changes exist"
	}

	return false, ""
}

// recreateContainer creates a new Docker container from meta.json.
func (m *Manager) recreateContainer(ctx context.Context, name string, meta *Meta) error {
	agentDef := agent.GetAgent(meta.Agent)
	if agentDef == nil {
		return fmt.Errorf("unknown agent: %s", meta.Agent)
	}

	sandboxDir := Dir(name)

	// Refresh seed files from host (handles OAuth token refresh between restarts)
	hasAPIKey := hasAnyAPIKey(agentDef)
	if _, err := copySeedFiles(agentDef, sandboxDir, hasAPIKey); err != nil {
		return fmt.Errorf("refresh seed files: %w", err)
	}

	// Read existing config.json
	configData, err := os.ReadFile(filepath.Join(sandboxDir, "config.json")) //nolint:gosec // path is sandbox-controlled
	if err != nil {
		return fmt.Errorf("read config.json: %w", err)
	}

	// Build sandbox state for container launch
	workdir, err := ParseDirArg(meta.Workdir.HostPath + ":" + meta.Workdir.Mode)
	if err != nil {
		return fmt.Errorf("parse workdir: %w", err)
	}

	// Extract tmux_conf from config.json
	var cfgJSON containerConfig
	if err := json.Unmarshal(configData, &cfgJSON); err != nil {
		return fmt.Errorf("parse config.json: %w", err)
	}

	state := &sandboxState{
		name:        name,
		sandboxDir:  sandboxDir,
		workdir:     workdir,
		workCopyDir: WorkDir(name, meta.Workdir.HostPath),
		agent:       agentDef,
		model:       meta.Model,
		hasPrompt:   meta.HasPrompt,
		networkMode: meta.NetworkMode,
		ports:       meta.Ports,
		tmuxConf:    cfgJSON.TmuxConf,
		configJSON:  configData,
	}

	return m.launchContainer(ctx, state)
}

// relaunchAgent relaunches the agent in the existing tmux session.
func (m *Manager) relaunchAgent(ctx context.Context, name string, _ *Meta) error {
	sandboxDir := Dir(name)

	// Read config.json to get agent_command
	configData, err := os.ReadFile(filepath.Join(sandboxDir, "config.json")) //nolint:gosec // path is sandbox-controlled
	if err != nil {
		return fmt.Errorf("read config.json: %w", err)
	}

	var cfg containerConfig
	if err := json.Unmarshal(configData, &cfg); err != nil {
		return fmt.Errorf("parse config.json: %w", err)
	}

	_, err = execInContainer(ctx, m.client, ContainerName(name), []string{
		"tmux", "respawn-pane", "-t", "main", "-k", cfg.AgentCommand,
	})
	if err != nil {
		return fmt.Errorf("relaunch agent: %w", err)
	}

	return nil
}

// resetInPlace resets the workspace while the agent is still running.
// Syncs files from host, recreates git baseline, and notifies the agent via tmux.
func (m *Manager) resetInPlace(ctx context.Context, opts ResetOptions, meta *Meta, sandboxDir string) error {
	workDir := WorkDir(opts.Name, meta.Workdir.HostPath)

	// Re-sync workdir from host (bind-mount makes changes visible in container)
	if err := rsyncDir(meta.Workdir.HostPath, workDir); err != nil {
		return fmt.Errorf("rsync workdir: %w", err)
	}

	// Strip git metadata from the synced copy (host repo's .git dirs)
	if err := removeGitDirs(workDir); err != nil {
		return fmt.Errorf("remove git dirs: %w", err)
	}

	// Re-create git baseline (host-side, visible in container via bind-mount)
	newSHA, err := gitBaseline(workDir)
	if err != nil {
		return fmt.Errorf("re-create git baseline: %w", err)
	}

	// Update meta.json
	meta.Workdir.BaselineSHA = newSHA
	if err := SaveMeta(sandboxDir, meta); err != nil {
		return err
	}

	// Notify agent via tmux
	return m.sendResetNotification(ctx, opts.Name, sandboxDir, opts.NoPrompt, meta.HasPrompt)
}

// rsyncDir syncs contents of src into dst using rsync.
// Trailing slashes ensure rsync copies contents, not the directory itself.
func rsyncDir(src, dst string) error {
	cmd := exec.Command("rsync", "-a", "--delete", src+"/", dst+"/") //nolint:gosec // src and dst are internal paths, not user-controlled
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("rsync: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

const resetNotification = "[yoloai] Workspace has been reset to match the current host directory. " +
	"All previous changes have been reverted and any new upstream changes are now present. " +
	"Re-read files before assuming their contents."

// sendResetNotification delivers a notification (and optionally the prompt)
// to the running agent via tmux load-buffer + paste-buffer + send-keys.
func (m *Manager) sendResetNotification(ctx context.Context, name, sandboxDir string, noPrompt, hasPrompt bool) error {
	// Read config.json for submit_sequence
	configData, err := os.ReadFile(filepath.Join(sandboxDir, "config.json")) //nolint:gosec // path is sandbox-controlled
	if err != nil {
		return fmt.Errorf("read config.json: %w", err)
	}

	var cfg containerConfig
	if err := json.Unmarshal(configData, &cfg); err != nil {
		return fmt.Errorf("parse config.json: %w", err)
	}

	// Build script to deliver notification via tmux.
	// $1 carries the notification text (positional arg avoids shell injection).
	appendPrompt := ":"
	if !noPrompt && hasPrompt {
		appendPrompt = `printf '\n\n' >> /tmp/yoloai-reset.txt; cat /yoloai/prompt.txt >> /tmp/yoloai-reset.txt`
	}

	script := fmt.Sprintf(`printf '%%s' "$1" > /tmp/yoloai-reset.txt
%s
tmux load-buffer /tmp/yoloai-reset.txt
tmux paste-buffer -t main
sleep 0.5
for key in %s; do
    tmux send-keys -t main "$key"
    sleep 0.2
done
rm -f /tmp/yoloai-reset.txt`, appendPrompt, cfg.SubmitSequence)

	_, err = execInContainer(ctx, m.client, ContainerName(name), []string{
		"bash", "-c", script, "_", resetNotification,
	})
	return err
}

// isNotRunningErr checks if an error indicates the container is not running.
// Docker SDK returns different error types for this across versions.
func isNotRunningErr(err error) bool {
	// containerd/errdefs doesn't have a specific "not running" type,
	// but Docker returns it as a generic error. Check common patterns.
	return cerrdefs.IsConflict(err)
}

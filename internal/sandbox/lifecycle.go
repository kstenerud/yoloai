package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/kstenerud/yoloai/internal/agent"
)

// ResetOptions holds parameters for the reset command.
type ResetOptions struct {
	Name     string
	Clean    bool // also wipe agent-state directory
	NoPrompt bool // skip re-sending prompt after reset
}

// Stop stops a sandbox's container via Docker SDK.
// Returns nil if the container is already stopped or removed.
func (m *Manager) Stop(ctx context.Context, name string) error {
	if _, err := os.Stat(Dir(name)); err != nil {
		return ErrSandboxNotFound
	}

	containerName := "yoloai-" + name
	if err := m.client.ContainerStop(ctx, containerName, container.StopOptions{}); err != nil {
		if cerrdefs.IsNotFound(err) || isNotRunningErr(err) {
			return nil
		}
		return fmt.Errorf("stop container: %w", err)
	}
	return nil
}

// Start ensures a sandbox is running — idempotent.
func (m *Manager) Start(ctx context.Context, name string) error {
	if _, err := os.Stat(Dir(name)); err != nil {
		return ErrSandboxNotFound
	}

	meta, err := LoadMeta(Dir(name))
	if err != nil {
		return err
	}

	status, _, err := DetectStatus(ctx, m.client, "yoloai-"+name)
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
		if err := m.client.ContainerStart(ctx, "yoloai-"+name, container.StartOptions{}); err != nil {
			return fmt.Errorf("start container: %w", err)
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
	if _, err := os.Stat(Dir(name)); err != nil {
		return ErrSandboxNotFound
	}

	containerName := "yoloai-" + name

	// Stop container (ignore errors — may not be running)
	_ = m.client.ContainerStop(ctx, containerName, container.StopOptions{})

	// Remove container (ignore errors — may not exist)
	_ = m.client.ContainerRemove(ctx, containerName, container.RemoveOptions{Force: true})

	// Remove sandbox directory
	if err := os.RemoveAll(Dir(name)); err != nil {
		return fmt.Errorf("remove sandbox directory: %w", err)
	}

	return nil
}

// Reset re-copies the workdir from the original host directory and resets
// the git baseline. Stops and restarts the container.
func (m *Manager) Reset(ctx context.Context, opts ResetOptions) error {
	if _, err := os.Stat(Dir(opts.Name)); err != nil {
		return ErrSandboxNotFound
	}

	meta, err := LoadMeta(Dir(opts.Name))
	if err != nil {
		return err
	}

	if meta.Workdir.Mode == "rw" {
		return fmt.Errorf("reset is not applicable for :rw directories — changes are already in the original")
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
	if err := SaveMeta(Dir(opts.Name), meta); err != nil {
		return err
	}

	// Optionally wipe agent-state
	if opts.Clean {
		agentStateDir := filepath.Join(Dir(opts.Name), "agent-state")
		if err := os.RemoveAll(agentStateDir); err != nil {
			return fmt.Errorf("remove agent-state: %w", err)
		}
		if err := os.MkdirAll(agentStateDir, 0750); err != nil {
			return fmt.Errorf("recreate agent-state: %w", err)
		}
	}

	// Handle --no-prompt by temporarily hiding prompt.txt
	promptPath := filepath.Join(Dir(opts.Name), "prompt.txt")
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
	status, _, err := DetectStatus(ctx, m.client, "yoloai-"+name)
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

	// Create fresh secrets
	secretsDir, err := createSecretsDir(agentDef)
	if err != nil {
		return fmt.Errorf("create secrets: %w", err)
	}
	if secretsDir != "" {
		defer os.RemoveAll(secretsDir) //nolint:errcheck // best-effort cleanup
	}

	// Build sandbox state for mount construction
	workdir, err := ParseDirArg(meta.Workdir.HostPath + ":" + meta.Workdir.Mode)
	if err != nil {
		return fmt.Errorf("parse workdir: %w", err)
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
		configJSON:  configData,
	}

	mounts := buildMounts(state, secretsDir)
	portBindings, exposedPorts, err := parsePortBindings(meta.Ports)
	if err != nil {
		return err
	}

	config := &container.Config{
		Image:        "yoloai-base",
		WorkingDir:   meta.Workdir.MountPath,
		ExposedPorts: exposedPorts,
	}

	initFlag := true
	hostConfig := &container.HostConfig{
		Init:         &initFlag,
		NetworkMode:  container.NetworkMode(meta.NetworkMode),
		PortBindings: portBindings,
		Mounts:       mounts,
	}

	containerName := "yoloai-" + name
	resp, err := m.client.ContainerCreate(ctx, config, hostConfig, nil, nil, containerName)
	if err != nil {
		return fmt.Errorf("create container: %w", err)
	}

	if err := m.client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("start container: %w", err)
	}

	// Wait briefly for entrypoint to read secrets before cleanup
	if secretsDir != "" {
		time.Sleep(1 * time.Second)
	}

	return nil
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

	containerName := "yoloai-" + name
	_, err = execInContainer(ctx, m.client, containerName, []string{
		"tmux", "respawn-pane", "-t", "main", "-k", cfg.AgentCommand,
	})
	if err != nil {
		return fmt.Errorf("relaunch agent: %w", err)
	}

	return nil
}

// isNotRunningErr checks if an error indicates the container is not running.
// Docker SDK returns different error types for this across versions.
func isNotRunningErr(err error) bool {
	// containerd/errdefs doesn't have a specific "not running" type,
	// but Docker returns it as a generic error. Check common patterns.
	return cerrdefs.IsConflict(err)
}

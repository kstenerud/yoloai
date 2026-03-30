package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kstenerud/yoloai/agent"
	"github.com/kstenerud/yoloai/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/workspace"
)

const resumePreamble = "You were previously working on the following task and were interrupted. " +
	"The work directory contains your progress so far. Continue where you left off:\n\n"

// ResetOptions holds parameters for the reset command.
type ResetOptions struct {
	Name       string
	Restart    bool // stop and restart container
	ClearState bool // also wipe agent-runtime directory (replaces Clean)
	KeepCache  bool // preserve cache directory
	KeepFiles  bool // preserve files directory
	NoPrompt   bool // skip re-sending prompt after reset
	Debug      bool // enable entrypoint debug logging
}

// Stop stops a sandbox's instance.
// Returns nil if the instance is already stopped or removed.
func (m *Manager) Stop(ctx context.Context, name string) error {
	unlock, err := acquireLock(name)
	if err != nil {
		return err
	}
	defer unlock()
	return m.stop(ctx, name)
}

func (m *Manager) stop(ctx context.Context, name string) error {
	if _, err := RequireSandboxDir(name); err != nil {
		return err
	}
	slog.Info("stopping sandbox", "event", "sandbox.stop", "container", InstanceName(name))
	return m.runtime.Stop(ctx, InstanceName(name))
}

// StartOptions holds parameters for the start command.
type StartOptions struct {
	Resume     bool   // re-feed original prompt with continuation preamble
	Prompt     string // if set, overwrite prompt.txt and send directly (no preamble)
	PromptFile string // if set, read from file, overwrite prompt.txt, send directly
}

// Start ensures a sandbox is running — idempotent.
func (m *Manager) Start(ctx context.Context, name string, opts StartOptions) error {
	unlock, err := acquireLock(name)
	if err != nil {
		return err
	}
	defer unlock()
	return m.start(ctx, name, opts)
}

func (m *Manager) start(ctx context.Context, name string, opts StartOptions) error {
	slog.Info("starting sandbox", "event", "sandbox.start", "sandbox", name) //nolint:gosec // G706: name is validated by ValidateName
	sandboxDir, err := RequireSandboxDir(name)
	if err != nil {
		return err
	}

	meta, err := LoadMeta(sandboxDir)
	if err != nil {
		return err
	}

	cname := InstanceName(name)
	status, err := DetectStatus(ctx, m.runtime, cname, sandboxDir)
	if err != nil {
		return fmt.Errorf("detect status: %w", err)
	}
	slog.Debug("container status", "event", "sandbox.start.status", "sandbox", name, "status", string(status)) //nolint:gosec // G706: name is validated by ValidateName

	// Resolve custom prompt if provided
	customPrompt := opts.Prompt != "" || opts.PromptFile != ""
	if opts.Resume && customPrompt {
		return fmt.Errorf("--resume and --prompt/--prompt-file are mutually exclusive")
	}
	if opts.Resume && !meta.HasPrompt {
		return fmt.Errorf("--resume requires a sandbox created with --prompt")
	}

	var promptText string
	if customPrompt {
		promptText, err = ReadPrompt(opts.Prompt, opts.PromptFile)
		if err != nil {
			return err
		}
		if promptText == "" {
			return fmt.Errorf("--prompt/--prompt-file produced empty text")
		}
		// Overwrite prompt.txt with new prompt; save old content for rollback.
		promptPath := filepath.Join(sandboxDir, "prompt.txt")
		oldPrompt, _ := os.ReadFile(promptPath) //nolint:gosec // G304: promptPath is constructed from a validated sandbox name
		if writeErr := fileutil.WriteFile(promptPath, []byte(promptText), 0600); writeErr != nil {
			return fmt.Errorf("write prompt.txt: %w", writeErr)
		}
		meta.HasPrompt = true
		if saveErr := SaveMeta(sandboxDir, meta); saveErr != nil {
			// Roll back prompt.txt so disk state remains consistent with environment.json.
			if oldPrompt != nil {
				_ = fileutil.WriteFile(promptPath, oldPrompt, 0600)
			} else {
				_ = os.Remove(promptPath)
			}
			return fmt.Errorf("save meta: %w", saveErr)
		}
	}

	switch status {
	case StatusActive, StatusIdle:
		fmt.Fprintf(m.output, "Sandbox %s is already running\n", name) //nolint:errcheck // best-effort output
		return nil

	case StatusDone, StatusFailed:
		slog.Info("relaunching agent", "event", "sandbox.start.agent.relaunch", "sandbox", name) //nolint:gosec // G706: name is validated by ValidateName
		switch {
		case customPrompt:
			if err := m.relaunchAgentWithCustomPrompt(ctx, name, meta, promptText); err != nil {
				return err
			}
		case opts.Resume:
			if err := m.relaunchAgentWithResume(ctx, name, meta); err != nil {
				return err
			}
		default:
			if err := m.relaunchAgent(ctx, name, meta); err != nil {
				return err
			}
		}
		fmt.Fprintf(m.output, "Agent relaunched in sandbox %s\n", name) //nolint:errcheck // best-effort output
		return nil

	case StatusStopped:
		if err := m.runtime.Remove(ctx, cname); err != nil {
			return fmt.Errorf("remove stopped instance: %w", err)
		}
		slog.Info("recreating container", "event", "sandbox.start.container.recreate", "sandbox", name) //nolint:gosec // G706: name is validated by ValidateName
		switch {
		case customPrompt:
			if err := m.prepareCustomPromptFiles(name, meta, promptText); err != nil {
				return err
			}
			defer m.cleanupResumeFiles(name)
		case opts.Resume:
			if err := m.prepareResumeFiles(name, meta); err != nil {
				return err
			}
			defer m.cleanupResumeFiles(name)
		}
		if err := m.recreateContainer(ctx, name, meta, opts.Resume); err != nil {
			return err
		}
		fmt.Fprintf(m.output, "Sandbox %s started\n", name) //nolint:errcheck // best-effort output
		return nil

	case StatusRemoved:
		slog.Info("recreating container", "event", "sandbox.start.container.recreate", "sandbox", name) //nolint:gosec // G706: name is validated by ValidateName
		switch {
		case customPrompt:
			if err := m.prepareCustomPromptFiles(name, meta, promptText); err != nil {
				return err
			}
			defer m.cleanupResumeFiles(name)
		case opts.Resume:
			if err := m.prepareResumeFiles(name, meta); err != nil {
				return err
			}
			defer m.cleanupResumeFiles(name)
		}
		if err := m.recreateContainer(ctx, name, meta, opts.Resume); err != nil {
			return err
		}
		fmt.Fprintf(m.output, "Sandbox %s recreated and started\n", name) //nolint:errcheck // best-effort output
		return nil

	default:
		return fmt.Errorf("unexpected sandbox status: %s", status)
	}
}

// Destroy stops the container, removes it, and deletes the sandbox directory.
// Always succeeds — confirmation logic is handled by the CLI layer via
// NeedsConfirmation before calling this method.
func (m *Manager) Destroy(ctx context.Context, name string) error {
	unlock, err := acquireLock(name)
	if err != nil {
		return err
	}
	defer unlock()
	return m.destroy(ctx, name)
}

func (m *Manager) destroy(ctx context.Context, name string) error {
	slog.Info("destroying sandbox", "event", "sandbox.destroy", "sandbox", name) //nolint:gosec // G706: name is validated by ValidateName
	if _, err := RequireSandboxDir(name); err != nil {
		if errors.Is(err, ErrSandboxNotFound) {
			return nil // nothing to destroy
		}
		return err
	}

	cname := InstanceName(name)

	// Stop instance (ignore errors — may not be running)
	_ = m.runtime.Stop(ctx, cname)

	// Remove instance (ignore errors — may not exist)
	_ = m.runtime.Remove(ctx, cname)

	// Remove sandbox directory. Some files (e.g. Go module cache) are
	// read-only, so make everything writable first.
	if err := forceRemoveAll(Dir(name)); err != nil {
		fmt.Fprintf(m.output, "Warning: could not fully remove sandbox directory: %v\n", err) //nolint:errcheck // best-effort output
	}

	return nil
}

// Reset re-copies the workdir from the original host directory and resets
// the git baseline. By default, resets in-place (agent stays running).
// With --restart, stops and restarts the container.
func (m *Manager) Reset(ctx context.Context, opts ResetOptions) error {
	unlock, err := acquireLock(opts.Name)
	if err != nil {
		return err
	}
	defer unlock()

	slog.Info("resetting sandbox", "event", "sandbox.reset", "sandbox", opts.Name)
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

	// Auto-upgrade to restart: --state implies restart (can't wipe state while agent is running)
	if opts.ClearState {
		opts.Restart = true
	}

	// Auto-upgrade to restart: overlay mode requires container restart
	if meta.Workdir.Mode == "overlay" {
		opts.Restart = true
	}

	// Auto-upgrade to restart: container not running
	if !opts.Restart {
		status, err := DetectStatus(ctx, m.runtime, InstanceName(opts.Name), sandboxDir)
		if err != nil || (status != StatusActive && status != StatusIdle) {
			fmt.Fprintf(m.output, "Container is not running, upgrading to restart\n") //nolint:errcheck // best-effort output
			opts.Restart = true
		}
	}

	if !opts.Restart {
		return m.resetInPlace(ctx, opts, meta, sandboxDir)
	}

	// Stop the container (if running)
	_ = m.stop(ctx, opts.Name)

	// Clear logs so each run starts fresh
	slog.Debug("clearing logs", "event", "sandbox.reset.logs", "sandbox", opts.Name)
	_ = os.RemoveAll(filepath.Join(sandboxDir, LogsDir))

	perms := Perms(meta.Isolation)

	_ = mkdirAllPerm(filepath.Join(sandboxDir, LogsDir), perms.Dir)
	for _, logFile := range []string{SandboxJSONLFile, MonitorJSONLFile, HooksJSONLFile} {
		_ = writeFilePerm(filepath.Join(sandboxDir, logFile), nil, perms.File)
	}

	var newSHA string
	if meta.Workdir.Mode == "overlay" {
		// Clear upper and ovlwork dirs (instant reset), ensure merged and lower exist
		for _, d := range []string{
			OverlayUpperDir(opts.Name, meta.Workdir.HostPath),
			OverlayOvlworkDir(opts.Name, meta.Workdir.HostPath),
			OverlayMergedDir(opts.Name, meta.Workdir.HostPath),
			OverlayLowerDir(opts.Name, meta.Workdir.HostPath),
		} {
			if err := os.RemoveAll(d); err != nil {
				return fmt.Errorf("clear overlay dir %s: %w", d, err)
			}
			if err := fileutil.MkdirAll(d, 0755); err != nil { //nolint:gosec // G301: world-traversable so container yoloai user can access merged/
				return fmt.Errorf("recreate overlay dir %s: %w", d, err)
			}
		}
		// Baseline deferred — container restart recreates it
		newSHA = ""
	} else {
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
		slog.Debug("re-copying workdir", "event", "sandbox.reset.workdir", "sandbox", opts.Name, "host_path", meta.Workdir.HostPath)
		if err := workspace.CopyDir(meta.Workdir.HostPath, workDir); err != nil {
			return fmt.Errorf("re-copy workdir: %w", err)
		}

		// Record baseline — preserve git history if source is a git repo
		if workspace.IsGitRepo(workDir) {
			sha, err := workspace.HeadSHA(workDir)
			if err != nil {
				return fmt.Errorf("read HEAD of re-copied workdir: %w", err)
			}
			newSHA = sha
		} else {
			// Tart VMs require the container to be running to exec setup commands inside the VM.
			// Docker creates baseline on the host before starting the container.
			if _, ok := m.runtime.(runtime.WorkDirSetup); ok {
				// Defer baseline creation — executeVMWorkDirSetup will call it after container start
				newSHA = ""
			} else {
				sha, err := workspace.Baseline(workDir)
				if err != nil {
					return fmt.Errorf("re-create git baseline: %w", err)
				}
				newSHA = sha
			}
		}
	}

	// Reset aux :copy and :overlay dirs
	for i, d := range meta.Directories {
		switch d.Mode {
		case "copy":
			auxWorkDir := WorkDir(opts.Name, d.HostPath)
			if err := os.RemoveAll(auxWorkDir); err != nil {
				return fmt.Errorf("remove aux work copy %s: %w", d.HostPath, err)
			}
			if _, err := os.Stat(d.HostPath); err != nil {
				return fmt.Errorf("original aux directory no longer exists: %s", d.HostPath)
			}
			if err := workspace.CopyDir(d.HostPath, auxWorkDir); err != nil {
				return fmt.Errorf("re-copy aux dir %s: %w", d.HostPath, err)
			}
			if workspace.IsGitRepo(auxWorkDir) {
				auxSHA, auxErr := workspace.HeadSHA(auxWorkDir)
				if auxErr != nil {
					return fmt.Errorf("read HEAD of re-copied aux dir %s: %w", d.HostPath, auxErr)
				}
				meta.Directories[i].BaselineSHA = auxSHA
			} else {
				auxSHA, auxErr := workspace.Baseline(auxWorkDir)
				if auxErr != nil {
					return fmt.Errorf("git baseline for aux dir %s: %w", d.HostPath, auxErr)
				}
				meta.Directories[i].BaselineSHA = auxSHA
			}
		case "overlay":
			for _, dir := range []string{
				OverlayUpperDir(opts.Name, d.HostPath),
				OverlayOvlworkDir(opts.Name, d.HostPath),
				OverlayMergedDir(opts.Name, d.HostPath),
				OverlayLowerDir(opts.Name, d.HostPath),
			} {
				if err := os.RemoveAll(dir); err != nil {
					return fmt.Errorf("clear overlay dir for aux %s: %w", d.HostPath, err)
				}
				if err := fileutil.MkdirAll(dir, 0755); err != nil { //nolint:gosec // G301: world-traversable so container yoloai user can access merged/
					return fmt.Errorf("recreate overlay dir for aux %s: %w", d.HostPath, err)
				}
			}
			meta.Directories[i].BaselineSHA = ""
		}
	}

	// Update meta.json
	meta.Workdir.BaselineSHA = newSHA
	if err := SaveMeta(sandboxDir, meta); err != nil {
		return err
	}

	// Optionally wipe agent-runtime state
	if opts.ClearState {
		agentStateDir := filepath.Join(sandboxDir, AgentRuntimeDir)
		if err := os.RemoveAll(agentStateDir); err != nil {
			return fmt.Errorf("remove %s: %w", AgentRuntimeDir, err)
		}
		if err := mkdirAllPerm(agentStateDir, perms.Dir); err != nil {
			return fmt.Errorf("recreate %s: %w", AgentRuntimeDir, err)
		}
		// Reset agent_files flag so files get re-seeded on next start
		sbState, stateErr := LoadSandboxState(sandboxDir)
		if stateErr == nil {
			sbState.AgentFilesInitialized = false
			_ = SaveSandboxState(sandboxDir, sbState)
		}
	}

	// Clear cache and files directories (unless --keep-X)
	if err := m.clearCacheAndFiles(opts); err != nil {
		return err
	}

	// Patch runtime-config.json with debug flag if requested
	if opts.Debug {
		if err := patchConfigDebug(sandboxDir, true); err != nil {
			return err
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

	slog.Info("reset complete", "event", "sandbox.reset.complete", "sandbox", opts.Name)
	// Start the container
	if err := m.start(ctx, opts.Name, StartOptions{}); err != nil {
		return err
	}

	// Execute VM-side work directory setup if baseline was deferred (Tart VMs)
	// For :copy mode, if BaselineSHA is empty, VM setup is needed
	if meta.Workdir.Mode == "copy" && meta.Workdir.BaselineSHA == "" {
		if err := executeVMWorkDirSetup(ctx, m.runtime, opts.Name, sandboxDir, meta); err != nil {
			return fmt.Errorf("VM work dir setup: %w", err)
		}
	}

	return nil
}

// NeedsConfirmation checks if a sandbox requires confirmation before
// destruction. Returns true if the agent is running or unapplied changes
// exist (uncommitted changes or commits beyond baseline).
// Returns a reason string for the confirmation prompt.
func (m *Manager) NeedsConfirmation(ctx context.Context, name string) (bool, string) {
	status, err := DetectStatus(ctx, m.runtime, InstanceName(name), Dir(name))
	if err != nil {
		return false, ""
	}

	if status == StatusActive || status == StatusIdle {
		return true, "agent is still running"
	}

	meta, err := LoadMeta(Dir(name))
	if err != nil {
		return false, ""
	}

	if meta.Workdir.Mode == "copy" || meta.Workdir.Mode == "overlay" {
		workDir := WorkDir(name, meta.Workdir.HostPath)
		if hasUnappliedWork(workDir, meta.Workdir.BaselineSHA) {
			return true, "unapplied changes exist"
		}
	}

	for _, d := range meta.Directories {
		if d.Mode == "copy" || d.Mode == "overlay" {
			auxWorkDir := WorkDir(name, d.HostPath)
			if hasUnappliedWork(auxWorkDir, d.BaselineSHA) {
				return true, "unapplied changes exist"
			}
		}
	}

	return false, ""
}

// recreateContainer creates a new Docker container from meta.json.
func (m *Manager) recreateContainer(ctx context.Context, name string, meta *Meta, resume bool) error {
	agentDef := agent.GetAgent(meta.Agent)
	if agentDef == nil {
		return fmt.Errorf("unknown agent: %s", meta.Agent)
	}

	sandboxDir := Dir(name)

	// Refresh seed files from host (handles OAuth token refresh between restarts)
	hasAPIKey := hasAnyAPIKey(agentDef, nil)
	if _, err := copySeedFiles(agentDef, sandboxDir, hasAPIKey); err != nil {
		return fmt.Errorf("refresh seed files: %w", err)
	}

	// Re-apply container settings (copySeedFiles overwrites settings.json
	// with the host version, which lacks sandbox-specific settings like
	// skipDangerousModePermissionPrompt)
	if err := ensureContainerSettings(agentDef, sandboxDir, meta.Isolation); err != nil {
		return fmt.Errorf("ensure container settings: %w", err)
	}

	// Copy agent_files if not yet initialized (e.g., sandbox created before
	// agent_files was configured, or after --clean reset)
	sbState, stateErr := LoadSandboxState(sandboxDir)
	if stateErr != nil {
		return fmt.Errorf("load sandbox state: %w", stateErr)
	}
	if !sbState.AgentFilesInitialized && agentDef.StateDir != "" {
		cfg, cfgErr := config.LoadConfig()
		if cfgErr == nil {
			var agentFilesConfig *config.AgentFilesConfig
			if meta.Profile != "" {
				chain, chainErr := config.ResolveProfileChain(meta.Profile)
				if chainErr == nil {
					merged, mergeErr := config.MergeProfileChain(cfg, chain)
					if mergeErr == nil {
						agentFilesConfig = merged.AgentFiles
					}
				}
			}
			if agentFilesConfig == nil {
				agentFilesConfig = cfg.AgentFiles
			}
			if agentFilesConfig != nil {
				if copyErr := copyAgentFiles(agentDef, sandboxDir, agentFilesConfig); copyErr != nil {
					return fmt.Errorf("copy agent files on restart: %w", copyErr)
				}
				sbState.AgentFilesInitialized = true
				if saveErr := SaveSandboxState(sandboxDir, sbState); saveErr != nil {
					return fmt.Errorf("save sandbox state: %w", saveErr)
				}
			}
		}
	}

	// Read existing runtime-config.json
	configData, err := os.ReadFile(filepath.Join(sandboxDir, RuntimeConfigFile)) //nolint:gosec // path is sandbox-controlled
	if err != nil {
		return fmt.Errorf("read runtime-config.json: %w", err)
	}

	// Build sandbox state for container launch
	workdir, err := ParseDirArg(meta.Workdir.HostPath + ":" + meta.Workdir.Mode)
	if err != nil {
		return fmt.Errorf("parse workdir: %w", err)
	}

	// Extract tmux_conf from runtime-config.json
	var cfgJSON containerConfig
	if err := json.Unmarshal(configData, &cfgJSON); err != nil {
		return fmt.Errorf("parse runtime-config.json: %w", err)
	}

	// Rebuild aux dir args from meta
	var auxDirs []*DirArg
	for _, d := range meta.Directories {
		auxDirs = append(auxDirs, &DirArg{
			Path:      d.HostPath,
			MountPath: d.MountPath,
			Mode:      d.Mode,
		})
	}

	// Resolve env: load config, then merge profile chain if profile was used.
	cfg, cfgErr := config.LoadConfig()
	if cfgErr != nil {
		return fmt.Errorf("load config: %w", cfgErr)
	}
	envVars := cfg.Env
	if meta.Profile != "" {
		chain, chainErr := config.ResolveProfileChain(meta.Profile)
		if chainErr == nil {
			merged, mergeErr := config.MergeProfileChain(cfg, chain)
			if mergeErr == nil {
				envVars = merged.Env
			}
		}
	}

	imageRef := meta.ImageRef

	state := &sandboxState{
		name:         name,
		sandboxDir:   sandboxDir,
		workdir:      workdir,
		workCopyDir:  WorkDir(name, meta.Workdir.HostPath),
		auxDirs:      auxDirs,
		agent:        agentDef,
		model:        meta.Model,
		profile:      meta.Profile,
		imageRef:     imageRef,
		env:          envVars,
		hasPrompt:    meta.HasPrompt,
		networkMode:  meta.NetworkMode,
		networkAllow: meta.NetworkAllow,
		ports:        meta.Ports,
		configMounts: meta.Mounts,
		tmuxConf:     cfgJSON.TmuxConf,
		resources:    meta.Resources,
		capAdd:       meta.CapAdd,
		devices:      meta.Devices,
		setup:        meta.Setup,
		isolation:    meta.Isolation,
		configJSON:   configData,
	}

	if resume {
		state.promptSourcePath = filepath.Join(sandboxDir, "resume-prompt.txt")
	}

	if err := m.launchContainer(ctx, state); err != nil {
		return err
	}

	// Execute VM-side work directory setup if baseline was deferred (Tart VMs)
	// For :copy mode, if BaselineSHA is empty, VM setup is needed
	if meta.Workdir.Mode == "copy" && meta.Workdir.BaselineSHA == "" {
		if err := executeVMWorkDirSetup(ctx, m.runtime, name, sandboxDir, meta); err != nil {
			return fmt.Errorf("VM work dir setup: %w", err)
		}
	}

	return nil
}

// tmuxCmd builds a tmux command slice, injecting -S <socket> when the sandbox
// uses a fixed socket path (Docker, containerd, Tart). Without this, tmux
// clients connect to the uid-based default socket which does not exist in
// containers that started tmux with an explicit -S path.
func tmuxCmd(socket string, args ...string) []string {
	cmd := []string{"tmux"}
	if socket != "" {
		cmd = append(cmd, "-S", socket)
	}
	return append(cmd, args...)
}

// tmuxShellPrefix returns a shell snippet that defines a _tmux() function
// wrapping tmux with -S <socket> when the sandbox uses a fixed socket path.
// Shell scripts that run tmux commands should source this prefix and call
// _tmux instead of tmux.
func tmuxShellPrefix(socket string) string {
	if socket != "" {
		return fmt.Sprintf("_tmux() { tmux -S %q \"$@\"; }", socket)
	}
	return "_tmux() { tmux \"$@\"; }"
}

// relaunchAgent relaunches the agent in the existing tmux session.
func (m *Manager) relaunchAgent(ctx context.Context, name string, meta *Meta) error {
	sandboxDir := Dir(name)

	// Read runtime-config.json to get agent_command
	configData, err := os.ReadFile(filepath.Join(sandboxDir, RuntimeConfigFile)) //nolint:gosec // path is sandbox-controlled
	if err != nil {
		return fmt.Errorf("read runtime-config.json: %w", err)
	}

	var cfg containerConfig
	if err := json.Unmarshal(configData, &cfg); err != nil {
		return fmt.Errorf("parse runtime-config.json: %w", err)
	}

	_, err = execInContainer(ctx, m.runtime, name, meta,
		tmuxCmd(cfg.TmuxSocket, "respawn-pane", "-t", "main", "-k", cfg.AgentCommand),
	)
	if err != nil {
		return fmt.Errorf("relaunch agent: %w", err)
	}

	return nil
}

// relaunchAgentWithResume relaunches the agent in interactive mode and sends
// the resume prompt (preamble + original prompt) via tmux.
func (m *Manager) relaunchAgentWithResume(ctx context.Context, name string, meta *Meta) error {
	sandboxDir := Dir(name)

	configData, err := os.ReadFile(filepath.Join(sandboxDir, RuntimeConfigFile)) //nolint:gosec // path is sandbox-controlled
	if err != nil {
		return fmt.Errorf("read runtime-config.json: %w", err)
	}

	var cfg containerConfig
	if err := json.Unmarshal(configData, &cfg); err != nil {
		return fmt.Errorf("parse runtime-config.json: %w", err)
	}

	agentDef := agent.GetAgent(meta.Agent)
	if agentDef == nil {
		return fmt.Errorf("unknown agent: %s", meta.Agent)
	}

	// Resolve agent_args from config/profile
	agentArgs := resolveAgentArgs(meta.Agent, meta.Profile)

	// Build interactive command (no headless prompt baked in)
	interactiveCmd := buildAgentCommand(agentDef, meta.Model, "", agentArgs, cfg.Passthrough)

	// Respawn with interactive command
	_, err = execInContainer(ctx, m.runtime, name, meta,
		tmuxCmd(cfg.TmuxSocket, "respawn-pane", "-t", "main", "-k", interactiveCmd),
	)
	if err != nil {
		return fmt.Errorf("relaunch agent: %w", err)
	}

	// Deliver resume prompt after agent is ready
	return m.sendResumePrompt(ctx, name, sandboxDir, cfg, meta)
}

// sendResumePrompt waits for the agent to be ready and delivers the resume
// prompt (preamble + original prompt) via tmux load-buffer/paste-buffer.
func (m *Manager) sendResumePrompt(ctx context.Context, name, sandboxDir string, cfg containerConfig, meta *Meta) error {
	promptData, err := os.ReadFile(filepath.Join(sandboxDir, "prompt.txt")) //nolint:gosec // path is sandbox-controlled
	if err != nil {
		return fmt.Errorf("read prompt.txt: %w", err)
	}

	resumeText := resumePreamble + string(promptData)

	// Build a wait-for-ready + deliver script.
	// Uses ready_pattern or startup_delay from runtime-config.json, following
	// the same logic as the entrypoint.
	var waitCmd string
	switch {
	case cfg.ReadyPattern != "":
		// Poll tmux capture-pane output for the ready pattern
		waitCmd = fmt.Sprintf(`for i in $(seq 1 60); do
    if _tmux capture-pane -t main -p 2>/dev/null | grep -q '%s'; then
        break
    fi
    sleep 1
done`, cfg.ReadyPattern)
	case cfg.StartupDelay > 0:
		delaySec := cfg.StartupDelay / 1000
		if delaySec < 1 {
			delaySec = 1
		}
		waitCmd = fmt.Sprintf("sleep %d", delaySec)
	default:
		waitCmd = "sleep 3"
	}

	// Write active status to status.json AFTER prompt delivery, not before.
	// This fixes the race where status shows "active" during the readiness wait.
	statusWrite := `printf '{"status":"active","timestamp":%d}' "$(date +%%s)" > "${YOLOAI_DIR:-/yoloai}/agent-status.json"`

	script := fmt.Sprintf(`%s
%s
printf '%%s' "$1" > /tmp/yoloai-resume.txt
_tmux load-buffer /tmp/yoloai-resume.txt
_tmux paste-buffer -t main
sleep 0.5
for key in %s; do
    _tmux send-keys -t main "$key"
    sleep 0.2
done
rm -f /tmp/yoloai-resume.txt
%s`, tmuxShellPrefix(cfg.TmuxSocket), waitCmd, cfg.SubmitSequence, statusWrite)

	_, err = execInContainer(ctx, m.runtime, name, meta, []string{
		"bash", "-c", "nohup bash -c '" + strings.ReplaceAll(script, "'", "'\"'\"'") + "' _ \"$1\" >/dev/null 2>&1 &", "_", resumeText,
	})
	return err
}

// relaunchAgentWithCustomPrompt relaunches the agent in interactive mode and sends
// the custom prompt directly (no resume preamble) via tmux.
func (m *Manager) relaunchAgentWithCustomPrompt(ctx context.Context, name string, meta *Meta, promptText string) error {
	sandboxDir := Dir(name)

	configData, err := os.ReadFile(filepath.Join(sandboxDir, RuntimeConfigFile)) //nolint:gosec // path is sandbox-controlled
	if err != nil {
		return fmt.Errorf("read runtime-config.json: %w", err)
	}

	var cfg containerConfig
	if err := json.Unmarshal(configData, &cfg); err != nil {
		return fmt.Errorf("parse runtime-config.json: %w", err)
	}

	agentDef := agent.GetAgent(meta.Agent)
	if agentDef == nil {
		return fmt.Errorf("unknown agent: %s", meta.Agent)
	}

	agentArgs := resolveAgentArgs(meta.Agent, meta.Profile)
	interactiveCmd := buildAgentCommand(agentDef, meta.Model, "", agentArgs, cfg.Passthrough)

	_, err = execInContainer(ctx, m.runtime, name, meta,
		tmuxCmd(cfg.TmuxSocket, "respawn-pane", "-t", "main", "-k", interactiveCmd),
	)
	if err != nil {
		return fmt.Errorf("relaunch agent: %w", err)
	}

	return m.sendCustomPrompt(ctx, name, sandboxDir, cfg, promptText, meta)
}

// sendCustomPrompt waits for the agent to be ready and delivers the custom
// prompt directly (without resume preamble) via tmux load-buffer/paste-buffer.
func (m *Manager) sendCustomPrompt(ctx context.Context, name, sandboxDir string, cfg containerConfig, promptText string, meta *Meta) error {
	var waitCmd string
	switch {
	case cfg.ReadyPattern != "":
		waitCmd = fmt.Sprintf(`for i in $(seq 1 60); do
    if _tmux capture-pane -t main -p 2>/dev/null | grep -q '%s'; then
        break
    fi
    sleep 1
done`, cfg.ReadyPattern)
	case cfg.StartupDelay > 0:
		delaySec := cfg.StartupDelay / 1000
		if delaySec < 1 {
			delaySec = 1
		}
		waitCmd = fmt.Sprintf("sleep %d", delaySec)
	default:
		waitCmd = "sleep 3"
	}

	// Write active status to status.json AFTER prompt delivery, not before.
	statusWrite := `printf '{"status":"active","timestamp":%d}' "$(date +%%s)" > "${YOLOAI_DIR:-/yoloai}/agent-status.json"`

	script := fmt.Sprintf(`%s
%s
printf '%%s' "$1" > /tmp/yoloai-custom-prompt.txt
_tmux load-buffer /tmp/yoloai-custom-prompt.txt
_tmux paste-buffer -t main
sleep 0.5
for key in %s; do
    _tmux send-keys -t main "$key"
    sleep 0.2
done
rm -f /tmp/yoloai-custom-prompt.txt
%s`, tmuxShellPrefix(cfg.TmuxSocket), waitCmd, cfg.SubmitSequence, statusWrite)

	_, err := execInContainer(ctx, m.runtime, name, meta, []string{
		"bash", "-c", "nohup bash -c '" + strings.ReplaceAll(script, "'", "'\"'\"'") + "' _ \"$1\" >/dev/null 2>&1 &", "_", promptText,
	})
	return err
}

// prepareCustomPromptFiles writes the resume-prompt.txt (custom prompt, no preamble)
// and patches runtime-config.json for interactive command mode.
func (m *Manager) prepareCustomPromptFiles(name string, meta *Meta, promptText string) error {
	sandboxDir := Dir(name)

	// Write resume-prompt.txt (custom prompt, no preamble)
	if err := fileutil.WriteFile(filepath.Join(sandboxDir, "resume-prompt.txt"), []byte(promptText), 0600); err != nil {
		return fmt.Errorf("write resume-prompt.txt: %w", err)
	}

	// Patch runtime-config.json: replace agent_command with interactive version
	configPath := filepath.Join(sandboxDir, RuntimeConfigFile)
	configData, err := os.ReadFile(configPath) //nolint:gosec // path is sandbox-controlled
	if err != nil {
		return fmt.Errorf("read runtime-config.json: %w", err)
	}

	var cfg containerConfig
	if err := json.Unmarshal(configData, &cfg); err != nil {
		return fmt.Errorf("parse runtime-config.json: %w", err)
	}

	agentDef := agent.GetAgent(meta.Agent)
	if agentDef == nil {
		return fmt.Errorf("unknown agent: %s", meta.Agent)
	}

	agentArgs := resolveAgentArgs(meta.Agent, meta.Profile)
	cfg.AgentCommand = buildAgentCommand(agentDef, meta.Model, "", agentArgs, cfg.Passthrough)

	updated, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal runtime-config.json: %w", err)
	}

	if err := fileutil.WriteFile(configPath, updated, 0600); err != nil {
		return fmt.Errorf("write runtime-config.json: %w", err)
	}

	return nil
}

// prepareResumeFiles writes the resume-prompt.txt and patches runtime-config.json
// for resume mode (interactive command).
func (m *Manager) prepareResumeFiles(name string, meta *Meta) error {
	sandboxDir := Dir(name)

	// Read original prompt
	promptData, err := os.ReadFile(filepath.Join(sandboxDir, "prompt.txt")) //nolint:gosec // path is sandbox-controlled
	if err != nil {
		return fmt.Errorf("read prompt.txt: %w", err)
	}

	// Write resume-prompt.txt (preamble + original prompt)
	resumeText := resumePreamble + string(promptData)
	if err := fileutil.WriteFile(filepath.Join(sandboxDir, "resume-prompt.txt"), []byte(resumeText), 0600); err != nil {
		return fmt.Errorf("write resume-prompt.txt: %w", err)
	}

	// Patch runtime-config.json: replace agent_command with interactive version
	configPath := filepath.Join(sandboxDir, RuntimeConfigFile)
	configData, err := os.ReadFile(configPath) //nolint:gosec // path is sandbox-controlled
	if err != nil {
		return fmt.Errorf("read runtime-config.json: %w", err)
	}

	var cfg containerConfig
	if err := json.Unmarshal(configData, &cfg); err != nil {
		return fmt.Errorf("parse runtime-config.json: %w", err)
	}

	agentDef := agent.GetAgent(meta.Agent)
	if agentDef == nil {
		return fmt.Errorf("unknown agent: %s", meta.Agent)
	}

	agentArgs := resolveAgentArgs(meta.Agent, meta.Profile)
	cfg.AgentCommand = buildAgentCommand(agentDef, meta.Model, "", agentArgs, cfg.Passthrough)

	updated, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal runtime-config.json: %w", err)
	}

	if err := fileutil.WriteFile(configPath, updated, 0600); err != nil {
		return fmt.Errorf("write runtime-config.json: %w", err)
	}

	return nil
}

// cleanupResumeFiles removes the temporary resume-prompt.txt file.
func (m *Manager) cleanupResumeFiles(name string) {
	_ = os.Remove(filepath.Join(Dir(name), "resume-prompt.txt"))
}

// resetInPlace resets the workspace while the agent is still running.
// Syncs files from host, recreates git baseline, and notifies the agent via tmux.
func (m *Manager) resetInPlace(ctx context.Context, opts ResetOptions, meta *Meta, sandboxDir string) error {
	// Tart VMs store the work directory inside the VM, not on the host.
	// In-place reset requires direct host access to the work directory.
	if _, ok := m.runtime.(runtime.WorkDirSetup); ok {
		return fmt.Errorf("in-place reset not supported for Tart VMs (work dir is inside VM)")
	}

	workDir := WorkDir(opts.Name, meta.Workdir.HostPath)

	// Re-sync workdir from host (bind-mount makes changes visible in container)
	if err := rsyncDir(meta.Workdir.HostPath, workDir); err != nil {
		return fmt.Errorf("rsync workdir: %w", err)
	}

	// Record baseline — preserve git history if source is a git repo
	var newSHA string
	if workspace.IsGitRepo(workDir) {
		sha, err := workspace.HeadSHA(workDir)
		if err != nil {
			return fmt.Errorf("read HEAD of resynced workdir: %w", err)
		}
		newSHA = sha
	} else {
		sha, err := workspace.Baseline(workDir)
		if err != nil {
			return fmt.Errorf("re-create git baseline: %w", err)
		}
		newSHA = sha
	}

	// Update meta.json
	meta.Workdir.BaselineSHA = newSHA
	if err := SaveMeta(sandboxDir, meta); err != nil {
		return err
	}

	// Clear cache and files directories (unless --keep-X)
	if err := m.clearCacheAndFiles(opts); err != nil {
		return err
	}

	// Notify agent via tmux
	return m.sendResetNotification(ctx, opts.Name, sandboxDir, opts.NoPrompt, meta.HasPrompt, meta)
}

// clearCacheAndFiles clears the cache and files directories unless --keep-X flags are set.
func (m *Manager) clearCacheAndFiles(opts ResetOptions) error {
	// Load metadata to check security mode for permissions
	meta, err := LoadMeta(Dir(opts.Name))
	if err != nil {
		return fmt.Errorf("load metadata: %w", err)
	}

	perms := Perms(meta.Isolation)

	if !opts.KeepCache {
		cacheDir := CacheDir(opts.Name)
		if err := os.RemoveAll(cacheDir); err != nil {
			return fmt.Errorf("remove cache: %w", err)
		}
		if err := mkdirAllPerm(cacheDir, perms.Dir); err != nil {
			return fmt.Errorf("recreate cache: %w", err)
		}
	}
	if !opts.KeepFiles {
		filesDir := FilesDir(opts.Name)
		if err := os.RemoveAll(filesDir); err != nil {
			return fmt.Errorf("remove files: %w", err)
		}
		if err := mkdirAllPerm(filesDir, perms.Dir); err != nil {
			return fmt.Errorf("recreate files: %w", err)
		}
	}
	return nil
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
func (m *Manager) sendResetNotification(ctx context.Context, name, sandboxDir string, noPrompt, hasPrompt bool, meta *Meta) error {
	// Read runtime-config.json for submit_sequence
	configData, err := os.ReadFile(filepath.Join(sandboxDir, RuntimeConfigFile)) //nolint:gosec // path is sandbox-controlled
	if err != nil {
		return fmt.Errorf("read runtime-config.json: %w", err)
	}

	var cfg containerConfig
	if err := json.Unmarshal(configData, &cfg); err != nil {
		return fmt.Errorf("parse runtime-config.json: %w", err)
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

	_, err = execInContainer(ctx, m.runtime, name, meta, []string{
		"bash", "-c", script, "_", resetNotification,
	})
	return err
}

// resolveAgentArgs loads agent_args for the given agent from config and profile.
// Returns empty string if no args are configured.
func resolveAgentArgs(agentName, profileName string) string {
	cfg, err := config.LoadConfig()
	if err != nil {
		return ""
	}
	if profileName != "" {
		chain, chainErr := config.ResolveProfileChain(profileName)
		if chainErr == nil {
			merged, mergeErr := config.MergeProfileChain(cfg, chain)
			if mergeErr == nil {
				return merged.AgentArgs[agentName]
			}
		}
	}
	return cfg.AgentArgs[agentName]
}

// patchConfigDebug reads runtime-config.json, sets the debug field, and writes it back.
func patchConfigDebug(sandboxDir string, debug bool) error {
	configPath := filepath.Join(sandboxDir, RuntimeConfigFile)
	data, err := os.ReadFile(configPath) //nolint:gosec // path is sandbox-controlled
	if err != nil {
		return fmt.Errorf("read runtime-config.json for debug patch: %w", err)
	}

	var cfg containerConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse runtime-config.json for debug patch: %w", err)
	}

	cfg.Debug = debug
	updated, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal runtime-config.json for debug patch: %w", err)
	}

	if err := fileutil.WriteFile(configPath, updated, 0600); err != nil {
		return fmt.Errorf("write runtime-config.json for debug patch: %w", err)
	}
	return nil
}

// PatchConfigAllowedDomains reads runtime-config.json, updates the allowed_domains
// field, and writes it back. Used by network-allow to persist domain changes.
func PatchConfigAllowedDomains(sandboxDir string, domains []string) error {
	configPath := filepath.Join(sandboxDir, RuntimeConfigFile)
	data, err := os.ReadFile(configPath) //nolint:gosec // path is sandbox-controlled
	if err != nil {
		return fmt.Errorf("read runtime-config.json for domain patch: %w", err)
	}

	var cfg containerConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse runtime-config.json for domain patch: %w", err)
	}

	cfg.AllowedDomains = domains
	updated, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal runtime-config.json for domain patch: %w", err)
	}

	if err := fileutil.WriteFile(configPath, updated, 0600); err != nil {
		return fmt.Errorf("write runtime-config.json for domain patch: %w", err)
	}
	return nil
}

// forceRemoveAll removes a directory tree, making read-only entries writable
// first (e.g. Go module cache files are installed read-only).
func forceRemoveAll(path string) error {
	// First pass: ensure all directories are writable so their contents can
	// be removed. We only need to fix directories — os.RemoveAll handles
	// read-only files fine once the parent directory is writable.
	_ = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			// If the directory isn't readable/executable, fix it and retry.
			_ = os.Chmod(p, 0o700) //nolint:errcheck,gosec // best-effort; 0700 needed for directory traversal before removal
			return nil             //nolint:nilerr // returning nil continues the walk after a best-effort chmod
		}
		if d.IsDir() {
			_ = os.Chmod(p, 0o700) //nolint:errcheck,gosec // best-effort; 0700 needed for directory traversal before removal
		}
		return nil
	})
	// Retry removal a few times. On macOS, system services (Spotlight,
	// FSEvents) can momentarily recreate files in the directory between
	// content removal and the final rmdir, causing "directory not empty".
	var err error
	for range 3 {
		err = os.RemoveAll(path)
		if err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return err
}

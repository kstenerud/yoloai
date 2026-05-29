// ABOUTME: Engine.Stop, Start, Reset, and Destroy: the four core lifecycle
// ABOUTME: verbs that drive sandbox containers through their state transitions.
package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox/invocation"
	"github.com/kstenerud/yoloai/internal/sandbox/launch"
	provision "github.com/kstenerud/yoloai/internal/sandbox/provision"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/internal/workspace"
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
func (m *Engine) Stop(ctx context.Context, name string) error {
	unlock, err := store.AcquireLock(m.layout, name)
	if err != nil {
		return err
	}
	defer unlock()
	return m.stop(ctx, name)
}

func (m *Engine) stop(ctx context.Context, name string) error {
	if err := store.RequireSandboxDir(m.layout.SandboxDir(name)); err != nil {
		return err
	}
	slog.Info("stopping sandbox", "event", "sandbox.stop", "container", store.InstanceName(name))
	return m.runtime.Stop(ctx, store.InstanceName(name))
}

// StartOptions holds parameters for the start command.
type StartOptions struct {
	Resume       bool                  // re-feed original prompt with continuation preamble
	Prompt       string                // if set, overwrite prompt.txt and send directly (no preamble)
	PromptFile   string                // if set, read from file, overwrite prompt.txt, send directly
	Isolation    runtime.IsolationMode // if set, override the isolation mode stored in meta.json
	VscodeTunnel bool                  // if true, enable VS Code Remote Tunnel (persisted to meta)
}

// Start ensures a sandbox is running — idempotent.
func (m *Engine) Start(ctx context.Context, name string, opts StartOptions) (*StartResult, error) {
	unlock, err := store.AcquireLock(m.layout, name)
	if err != nil {
		return nil, err
	}
	defer unlock()
	var n notices
	startErr := m.start(ctx, name, opts, &n)
	return &StartResult{Notices: n.list}, startErr
}

// syncLifecycleMarker checks for the Python on-create-done marker file and
// persists the flag to sandbox-state.json if found and not yet recorded.
func syncLifecycleMarker(sandboxDir string) {
	markerPath := filepath.Join(sandboxDir, "lifecycle-on-create-done")
	if _, markerErr := os.Stat(markerPath); markerErr != nil {
		return
	}
	state, stateErr := store.LoadSandboxState(sandboxDir)
	if stateErr == nil && !state.OnCreateCommandsDone {
		state.OnCreateCommandsDone = true
		if saveErr := store.SaveSandboxState(sandboxDir, state); saveErr != nil {
			slog.Warn("lifecycle: could not save sandbox state", "err", saveErr)
		}
	}
}

// applyIsolationOverride applies the isolation mode override from opts to meta
// if it differs from the current value. Validates mode, checks backend support,
// and saves meta. No-op when opts.Isolation is empty or unchanged.
func (m *Engine) applyIsolationOverride(ctx context.Context, opts StartOptions, sandboxDir string, meta *store.Meta, n *notices) error {
	if opts.Isolation == "" || opts.Isolation == meta.Isolation {
		return nil
	}
	if err := config.ValidateIsolationMode(string(opts.Isolation)); err != nil {
		return err
	}
	desc := m.runtime.Descriptor()
	supported := desc.SupportedIsolationModes
	if opts.Isolation != runtime.IsolationModeContainer && len(supported) > 0 {
		ok := slices.Contains(supported, opts.Isolation)
		if !ok {
			return NewUsageError("isolation mode %q is not supported by the %s backend", opts.Isolation, desc.Name)
		}
	}
	if err := checkIsolationPrerequisites(ctx, m.runtime, opts.Isolation); err != nil {
		return err
	}
	meta.Isolation = opts.Isolation
	if err := store.SaveMeta(sandboxDir, meta); err != nil {
		return fmt.Errorf("save meta: %w", err)
	}
	n.infof("Isolation mode updated to %s", opts.Isolation)
	return nil
}

// applyVscodeTunnelOption enables the VS Code Remote Tunnel in meta and
// runtime-config.json when opts.VscodeTunnel is true and not already enabled.
func (m *Engine) applyVscodeTunnelOption(opts StartOptions, sandboxDir, name string, meta *store.Meta, n *notices) error {
	if !opts.VscodeTunnel || meta.VscodeTunnel {
		return nil
	}
	meta.VscodeTunnel = true
	if err := store.SaveMeta(sandboxDir, meta); err != nil {
		return fmt.Errorf("save meta: %w", err)
	}
	if err := patchConfigVscodeTunnel(sandboxDir, name); err != nil {
		return fmt.Errorf("patch runtime-config.json for vscode-tunnel: %w", err)
	}
	n.infof("VS Code tunnel enabled")
	return nil
}

// preparePromptForStart validates prompt options, reads the custom prompt text
// if provided, and persists it to prompt.txt + meta. Returns the prompt text
// and whether a custom prompt is in use.
// homeDir is used to expand leading "~" in the promptFile path.
func preparePromptForStart(opts StartOptions, sandboxDir string, meta *store.Meta, homeDir string, env map[string]string, stdin io.Reader) (promptText string, customPrompt bool, err error) {
	customPrompt = opts.Prompt != "" || opts.PromptFile != ""
	if opts.Resume && customPrompt {
		return "", false, fmt.Errorf("--resume and --prompt/--prompt-file are mutually exclusive")
	}
	if opts.Resume && !meta.HasPrompt {
		return "", false, fmt.Errorf("--resume requires a sandbox created with --prompt")
	}
	if !customPrompt {
		return "", false, nil
	}

	promptText, err = invocation.ReadPrompt(opts.Prompt, opts.PromptFile, homeDir, env, stdin)
	if err != nil {
		return "", false, err
	}
	if promptText == "" {
		return "", false, fmt.Errorf("--prompt/--prompt-file produced empty text")
	}

	// Overwrite prompt.txt with new prompt; save old content for rollback.
	promptPath := filepath.Join(sandboxDir, "prompt.txt")
	oldPrompt, _ := os.ReadFile(promptPath) //nolint:gosec // G304: promptPath is constructed from a validated sandbox name
	if writeErr := fileutil.WriteFile(promptPath, []byte(promptText), 0600); writeErr != nil {
		return "", false, fmt.Errorf("write prompt.txt: %w", writeErr)
	}
	meta.HasPrompt = true
	if saveErr := store.SaveMeta(sandboxDir, meta); saveErr != nil {
		// Roll back prompt.txt so disk state remains consistent with environment.json.
		if oldPrompt != nil {
			_ = fileutil.WriteFile(promptPath, oldPrompt, 0600)
		} else {
			_ = os.Remove(promptPath)
		}
		return "", false, fmt.Errorf("save meta: %w", saveErr)
	}
	return promptText, true, nil
}

// handleTerminalStatus relaunches the agent after it has exited (Done/Failed).
func (m *Engine) handleTerminalStatus(ctx context.Context, name string, meta *store.Meta, opts StartOptions, promptText string, customPrompt bool, n *notices) error {
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
	n.infof("Agent relaunched in sandbox %s", name)
	return nil
}

// handleStoppedOrRemovedStatus recreates the container for a sandbox whose
// container is stopped or removed. removeStopped indicates the container still
// exists and must be removed first. successMsg is printed on success.
func (m *Engine) handleStoppedOrRemovedStatus(ctx context.Context, cname, name string, meta *store.Meta, opts StartOptions, promptText string, customPrompt, removeStopped bool, successMsg string, n *notices) error {
	if removeStopped && !m.runtime.Descriptor().Capabilities.HostFilesystem {
		// Container backends (Docker, Podman, containerd): the sandbox directory
		// lives on the host separately from the container, so Remove only deletes
		// the stopped container and the sandbox directory is preserved.
		//
		// Host-filesystem backends (Seatbelt): the sandbox directory IS the
		// container state. Remove would destroy the work copy, prompt.txt, and
		// other sandbox files. Skip Remove — the process is already dead after
		// Stop, and Create+Start will refresh scripts and credentials in place.
		if err := m.runtime.Remove(ctx, cname); err != nil {
			return fmt.Errorf("remove stopped instance: %w", err)
		}
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
	if err := m.recreateContainer(ctx, name, meta, opts.Resume, n); err != nil {
		return err
	}
	n.infof("%s", successMsg)
	return nil
}

// handleSuspendedResume resumes a suspended VM and starts a fresh agent session.
// Credentials are refreshed, the VM is resumed via runtime.Start (which kills
// the stale tmux session and runs the setup script), and executeVMWorkDirSetup
// is skipped because the work directory is already present from the suspend.
func (m *Engine) handleSuspendedResume(ctx context.Context, cname, name string, meta *store.Meta, opts StartOptions, promptText string, customPrompt bool, n *notices) error {
	slog.Info("resuming suspended sandbox", "event", "sandbox.start.resume", "sandbox", name)
	sandboxDir := m.layout.SandboxDir(name)

	agentDef := agent.GetAgent(string(meta.Agent))
	if agentDef == nil {
		return NewConfigError("unknown agent %q in sandbox state — this sandbox was created with an agent that's not registered in the current yoloai installation; destroy and recreate the sandbox with a registered agent", meta.Agent)
	}

	// Refresh credentials and settings from host (handles token refresh between sessions).
	hasAPIKey := provision.HasAnyAPIKey(agentDef, nil)
	if _, err := provision.CopySeedFiles(agentDef, sandboxDir, hasAPIKey, m.layout.HomeDir); err != nil {
		return fmt.Errorf("refresh seed files: %w", err)
	}
	if err := provision.EnsureContainerSettings(agentDef, sandboxDir, meta.Isolation); err != nil {
		return fmt.Errorf("ensure container settings: %w", err)
	}

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

	// Resume the VM: tart run resumes from suspended state, kills the stale
	// tmux session, and runs the setup script for a fresh agent.
	if err := m.runtime.Start(ctx, cname); err != nil {
		// Apple VZ framework cannot restore VMs that had VirtioFS (--dir) mounts
		// from a suspend snapshot (VZErrorDomain Code=12). Fall back to destroying
		// the suspended VM and recreating from the host staging area.
		slog.Warn("suspended VM resume failed, falling back to recreate", "sandbox", name, "err", err)
		_ = m.runtime.Remove(ctx, cname)
		return m.handleStoppedOrRemovedStatus(ctx, cname, name, meta, opts, promptText, customPrompt, false, fmt.Sprintf("Sandbox %s recreated and started", name), n)
	}

	// Don't call executeVMWorkDirSetup: the work directory is already present
	// inside the VM from before the suspend.

	n.infof("Sandbox %s resumed", name)
	return nil
}

func (m *Engine) start(ctx context.Context, name string, opts StartOptions, n *notices) error {
	slog.Info("starting sandbox", "event", "sandbox.start", "sandbox", name) //nolint:gosec // G706: name is validated by ValidateName
	sandboxDir := m.layout.SandboxDir(name)
	if err := store.RequireSandboxDir(sandboxDir); err != nil {
		return err
	}

	meta, err := store.LoadMeta(sandboxDir)
	if err != nil {
		return err
	}

	// Sync lifecycle on-create-done marker to sandbox state.
	// Python writes a marker file after successful on-create commands; Go
	// reads it on next start and persists the flag to sandbox-state.json so
	// subsequent runtime-config.json writes can set on_create_done: true.
	syncLifecycleMarker(sandboxDir)

	// Apply isolation override before recreating the container.
	if err := m.applyIsolationOverride(ctx, opts, sandboxDir, meta, n); err != nil {
		return err
	}

	// Enable VS Code Remote Tunnel if requested and not already enabled.
	if err := m.applyVscodeTunnelOption(opts, sandboxDir, name, meta, n); err != nil {
		return err
	}

	cname := store.InstanceName(name)
	status, err := DetectStatus(ctx, m.runtime, cname, sandboxDir)
	if err != nil {
		return fmt.Errorf("detect status: %w", err)
	}
	slog.Debug("container status", "event", "sandbox.start.status", "sandbox", name, "status", string(status)) //nolint:gosec // G706: name is validated by ValidateName

	promptText, customPrompt, err := preparePromptForStart(opts, sandboxDir, meta, m.layout.HomeDir, m.layout.Env, m.input)
	if err != nil {
		return err
	}

	switch status {
	case StatusActive, StatusIdle:
		n.infof("Sandbox %s is already running", name)
		return nil

	case StatusDone, StatusFailed:
		return m.handleTerminalStatus(ctx, name, meta, opts, promptText, customPrompt, n)

	case StatusSuspended:
		return m.handleSuspendedResume(ctx, cname, name, meta, opts, promptText, customPrompt, n)

	case StatusStopped:
		return m.handleStoppedOrRemovedStatus(ctx, cname, name, meta, opts, promptText, customPrompt, true, fmt.Sprintf("Sandbox %s started", name), n)

	case StatusRemoved:
		return m.handleStoppedOrRemovedStatus(ctx, cname, name, meta, opts, promptText, customPrompt, false, fmt.Sprintf("Sandbox %s recreated and started", name), n)

	default:
		return fmt.Errorf("unexpected sandbox status: %s", status)
	}
}

// Destroy stops the container, removes it, and deletes the sandbox directory.
// Always succeeds — confirmation logic is handled by the CLI layer via
// NeedsConfirmation before calling this method.
func (m *Engine) Destroy(ctx context.Context, name string) (*DestroyResult, error) {
	unlock, err := store.AcquireLock(m.layout, name)
	if err != nil {
		return nil, err
	}
	defer unlock()
	res, derr := m.destroy(ctx, name)
	// Remove the per-sandbox lock file while we still hold the flock so
	// the <name>.lock file doesn't accumulate after the sandbox dir is
	// gone. Best-effort: a leftover lock file is harmless, not corruption.
	_ = store.RemoveLockFile(m.layout, name)
	return res, derr
}

func (m *Engine) destroy(ctx context.Context, name string) (*DestroyResult, error) {
	slog.Info("destroying sandbox", "event", "sandbox.destroy", "sandbox", name) //nolint:gosec // G706: name is validated by ValidateName
	sandboxDir := m.layout.SandboxDir(name)
	if err := store.RequireSandboxDir(sandboxDir); err != nil {
		if errors.Is(err, ErrSandboxNotFound) {
			return &DestroyResult{}, nil // nothing to destroy
		}
		return nil, err
	}

	cname := store.InstanceName(name)

	// Stop instance (ignore errors — may not be running)
	_ = m.runtime.Stop(ctx, cname)

	// Remove instance (ignore errors — may not exist)
	_ = m.runtime.Remove(ctx, cname)

	var n notices
	// Remove the metadata file first so a partial directory removal still frees
	// the name for reuse: Create keys "already exists" off the metadata, not the
	// directory, so a leftover (e.g. root-owned overlay/VM state we can't delete)
	// won't block re-creating with the same name.
	_ = os.Remove(filepath.Join(sandboxDir, store.EnvironmentFile)) //nolint:errcheck // best-effort; forceRemoveAll removes it too in the common case

	// Remove sandbox directory. Some files (e.g. Go module cache) are
	// read-only, so make everything writable first.
	if err := forceRemoveAll(sandboxDir); err != nil {
		n.warnf("sandbox %s removed, but some files could not be deleted (likely root-owned overlay/VM state from the backend): %v\n  reclaim the leftover disk with: sudo rm -rf %s   (or run 'yoloai system prune')", name, err, sandboxDir)
	}

	return &DestroyResult{Notices: n.list}, nil
}

// resetOverlayDirs clears the overlay dirs (upper/ovlwork/merged/lower) and
// recreates them for a fresh state.
func resetOverlayDirs(sandboxDir, hostPath string) error {
	for _, d := range []string{
		store.OverlayUpperDir(sandboxDir, hostPath),
		store.OverlayOvlworkDir(sandboxDir, hostPath),
		store.OverlayMergedDir(sandboxDir, hostPath),
		store.OverlayLowerDir(sandboxDir, hostPath),
	} {
		if err := os.RemoveAll(d); err != nil {
			return fmt.Errorf("clear overlay dir %s: %w", d, err)
		}
		if err := fileutil.MkdirAll(d, 0755); err != nil { //nolint:gosec // G301: world-traversable so container yoloai user can access merged/
			return fmt.Errorf("recreate overlay dir %s: %w", d, err)
		}
	}
	return nil
}

// resetCopyWorkdir removes the work copy, re-copies from the host path, and
// records the git baseline. Returns the new baseline SHA (empty if deferred).
func (m *Engine) resetCopyWorkdir(sandboxName, sandboxDir string, meta *store.Meta) (string, error) {
	workDir := store.WorkDir(sandboxDir, meta.Workdir.HostPath)

	if err := os.RemoveAll(workDir); err != nil {
		return "", fmt.Errorf("remove work copy: %w", err)
	}
	if _, err := os.Stat(meta.Workdir.HostPath); err != nil {
		return "", fmt.Errorf("original directory no longer exists: %s", meta.Workdir.HostPath)
	}
	slog.Debug("re-copying workdir", "event", "sandbox.reset.workdir", "sandbox", sandboxName, "host_path", meta.Workdir.HostPath)
	if err := workspace.CopyDir(meta.Workdir.HostPath, workDir); err != nil {
		return "", fmt.Errorf("re-copy workdir: %w", err)
	}

	if workspace.IsGitRepo(workDir) {
		sha, err := workspace.HeadSHA(workDir)
		if err != nil {
			return "", fmt.Errorf("read HEAD of re-copied workdir: %w", err)
		}
		return sha, nil
	}
	// Tart VMs require the container to be running to exec setup commands inside the VM.
	// Docker creates baseline on the host before starting the container.
	if _, ok := m.runtime.(runtime.WorkDirSetup); ok {
		// Defer baseline creation — executeVMWorkDirSetup will call it after container start
		return "", nil
	}
	sha, err := workspace.Baseline(workDir)
	if err != nil {
		return "", fmt.Errorf("re-create git baseline: %w", err)
	}
	return sha, nil
}

// resetAuxCopyDir resets a single aux :copy dir and returns the new baseline SHA.
func resetAuxCopyDir(sandboxDir string, d store.DirMeta) (string, error) {
	auxWorkDir := store.WorkDir(sandboxDir, d.HostPath)
	if err := os.RemoveAll(auxWorkDir); err != nil {
		return "", fmt.Errorf("remove aux work copy %s: %w", d.HostPath, err)
	}
	if _, err := os.Stat(d.HostPath); err != nil {
		return "", fmt.Errorf("original aux directory no longer exists: %s", d.HostPath)
	}
	if err := workspace.CopyDir(d.HostPath, auxWorkDir); err != nil {
		return "", fmt.Errorf("re-copy aux dir %s: %w", d.HostPath, err)
	}
	if workspace.IsGitRepo(auxWorkDir) {
		sha, err := workspace.HeadSHA(auxWorkDir)
		if err != nil {
			return "", fmt.Errorf("read HEAD of re-copied aux dir %s: %w", d.HostPath, err)
		}
		return sha, nil
	}
	sha, err := workspace.Baseline(auxWorkDir)
	if err != nil {
		return "", fmt.Errorf("git baseline for aux dir %s: %w", d.HostPath, err)
	}
	return sha, nil
}

// resetAuxDirs resets all aux :copy and :overlay directories in meta,
// updating BaselineSHA in-place.
func resetAuxDirs(sandboxDir string, meta *store.Meta) error {
	for i, d := range meta.Directories {
		switch d.Mode {
		case store.DirModeCopy:
			sha, err := resetAuxCopyDir(sandboxDir, d)
			if err != nil {
				return err
			}
			meta.Directories[i].BaselineSHA = sha
		case store.DirModeOverlay:
			if err := resetOverlayDirs(sandboxDir, d.HostPath); err != nil {
				return fmt.Errorf("aux overlay %s: %w", d.HostPath, err)
			}
			meta.Directories[i].BaselineSHA = ""
		case store.DirModeRW, store.DirModeRO, "":
			// rw and ro aux dirs have no baseline to reset
		}
	}
	return nil
}

// clearAgentState wipes and recreates the agent-runtime directory and resets
// the AgentFilesInitialized flag.
func clearAgentState(sandboxDir string, perms IsolationPerms) error {
	agentStateDir := filepath.Join(sandboxDir, store.AgentRuntimeDir)
	if err := os.RemoveAll(agentStateDir); err != nil {
		return fmt.Errorf("remove %s: %w", store.AgentRuntimeDir, err)
	}
	if err := fileutil.MkdirAllPerm(agentStateDir, perms.Dir); err != nil {
		return fmt.Errorf("recreate %s: %w", store.AgentRuntimeDir, err)
	}
	// Reset agent_files flag so files get re-seeded on next start
	sbState, stateErr := store.LoadSandboxState(sandboxDir)
	if stateErr == nil {
		sbState.AgentFilesInitialized = false
		_ = store.SaveSandboxState(sandboxDir, sbState)
	}
	return nil
}

// Reset re-copies the workdir from the original host directory and resets
// the git baseline. By default, resets in-place (agent stays running).
// With --restart, stops and restarts the container.
func (m *Engine) Reset(ctx context.Context, opts ResetOptions) (*ResetResult, error) {
	unlock, err := store.AcquireLock(m.layout, opts.Name)
	if err != nil {
		return nil, err
	}
	defer unlock()

	slog.Info("resetting sandbox", "event", "sandbox.reset", "sandbox", opts.Name)
	sandboxDir := m.layout.SandboxDir(opts.Name)
	if err := store.RequireSandboxDir(sandboxDir); err != nil {
		return nil, err
	}

	meta, err := store.LoadMeta(sandboxDir)
	if err != nil {
		return nil, err
	}

	if meta.Workdir.Mode == "rw" {
		return nil, fmt.Errorf("reset is not applicable for :rw directories — changes are already in the original")
	}

	var n notices

	// Auto-upgrade to restart: --state implies restart (can't wipe state while agent is running)
	if opts.ClearState {
		opts.Restart = true
	}

	// Auto-upgrade to restart: overlay mode requires container restart
	if meta.Workdir.Mode == "overlay" {
		opts.Restart = true
	}

	// Auto-upgrade to restart: Tart VMs store the work dir inside the VM,
	// so in-place reset (host-side file access) is not possible.
	if _, ok := m.runtime.(runtime.WorkDirSetup); ok {
		opts.Restart = true
	}

	// Auto-upgrade to restart: container not running
	if !opts.Restart {
		status, err := DetectStatus(ctx, m.runtime, store.InstanceName(opts.Name), sandboxDir)
		if err != nil || (status != StatusActive && status != StatusIdle) {
			n.infof("Container is not running, upgrading to restart")
			opts.Restart = true
		}
	}

	if !opts.Restart {
		err := m.resetInPlace(ctx, opts, meta, sandboxDir)
		return &ResetResult{Notices: n.list}, err
	}

	err = m.prepareResetRestart(ctx, opts, sandboxDir, meta, &n)
	return &ResetResult{Notices: n.list}, err
}

// reinitLogs removes and recreates the sandbox log files with appropriate permissions.
func reinitLogs(sandboxDir string, perms IsolationPerms) {
	_ = os.RemoveAll(filepath.Join(sandboxDir, store.LogsDir))
	_ = fileutil.MkdirAllPerm(filepath.Join(sandboxDir, store.LogsDir), perms.Dir)
	for _, logFile := range []string{store.SandboxJSONLFile, store.MonitorJSONLFile, store.HooksJSONLFile} {
		_ = fileutil.WriteFilePerm(filepath.Join(sandboxDir, logFile), nil, perms.File)
	}
}

// resetWorkdir resets the main workdir (overlay or copy) and returns the new baseline SHA.
func (m *Engine) resetWorkdir(sandboxName, sandboxDir string, meta *store.Meta) (string, error) {
	if meta.Workdir.Mode == "overlay" {
		if err := resetOverlayDirs(sandboxDir, meta.Workdir.HostPath); err != nil {
			return "", err
		}
		return "", nil // baseline deferred — container restart recreates it
	}
	return m.resetCopyWorkdir(sandboxName, sandboxDir, meta)
}

// applyPostResetOptions handles optional post-reset actions: wiping agent state,
// clearing cache/files, patching debug config, and temporarily hiding prompt.txt.
// Returns a cleanup func that must be deferred by the caller (restores prompt.txt).
func (m *Engine) applyPostResetOptions(opts ResetOptions, sandboxDir string, perms IsolationPerms) (func(), error) {
	if opts.ClearState {
		if err := clearAgentState(sandboxDir, perms); err != nil {
			return nil, err
		}
	}
	if err := m.clearCacheAndFiles(opts); err != nil {
		return nil, err
	}
	if opts.Debug {
		if err := patchConfigDebug(sandboxDir, true); err != nil {
			return nil, err
		}
	}

	// Handle --no-prompt by temporarily hiding prompt.txt.
	// Return a cleanup function that restores it after container start.
	promptPath := filepath.Join(sandboxDir, "prompt.txt")
	promptBackup := promptPath + ".bak"
	if opts.NoPrompt {
		if _, err := os.Stat(promptPath); err == nil {
			if renameErr := os.Rename(promptPath, promptBackup); renameErr != nil {
				return nil, fmt.Errorf("hide prompt.txt: %w", renameErr)
			}
			return func() { _ = os.Rename(promptBackup, promptPath) }, nil
		}
	}
	return func() {}, nil
}

// prepareResetRestart performs the full stop → wipe → recopy → start flow for
// reset --restart. Extracted from Reset to reduce its cyclomatic complexity.
func (m *Engine) prepareResetRestart(ctx context.Context, opts ResetOptions, sandboxDir string, meta *store.Meta, n *notices) error {
	// Destroy the container so start() sees StatusRemoved and does a clean
	// recreate. Using Remove (not Stop) avoids suspending a VM we're about
	// to rebuild — the suspend state would be stale after the host workdir
	// is re-copied, and handleSuspendedResume would resume the wrong files.
	cname := store.InstanceName(opts.Name)
	_ = m.runtime.Remove(ctx, cname)

	perms := Perms(meta.Isolation)

	// Clear logs so each run starts fresh
	slog.Debug("clearing logs", "event", "sandbox.reset.logs", "sandbox", opts.Name)
	reinitLogs(sandboxDir, perms)

	// Reset main workdir
	newSHA, err := m.resetWorkdir(opts.Name, sandboxDir, meta)
	if err != nil {
		return err
	}

	// Reset aux :copy and :overlay dirs
	if err := resetAuxDirs(sandboxDir, meta); err != nil {
		return err
	}

	// Update meta.json
	meta.Workdir.BaselineSHA = newSHA
	if err := store.SaveMeta(sandboxDir, meta); err != nil {
		return err
	}

	// Apply post-reset options (state, cache/files, debug, no-prompt)
	cleanup, err := m.applyPostResetOptions(opts, sandboxDir, perms)
	if err != nil {
		return err
	}
	defer cleanup()

	slog.Info("reset complete", "event", "sandbox.reset.complete", "sandbox", opts.Name)
	// Start the container; its status notices flow into the reset's notices.
	if err := m.start(ctx, opts.Name, StartOptions{}, n); err != nil {
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
func (m *Engine) NeedsConfirmation(ctx context.Context, name string) (bool, string) {
	sandboxDir := m.layout.SandboxDir(name)
	status, err := DetectStatus(ctx, m.runtime, store.InstanceName(name), sandboxDir)
	if err != nil {
		return false, ""
	}

	if status == StatusActive || status == StatusIdle {
		return true, "agent is still running"
	}

	meta, err := store.LoadMeta(sandboxDir)
	if err != nil {
		// Meta is unreadable (a broken sandbox). Don't assume it's empty —
		// that silently discards recoverable work. Fall back to a
		// filesystem-level probe of work/ so destroy still prompts.
		if state, detail := ProbeWorkData(sandboxDir); state != WorkDataNone {
			if detail == "" {
				detail = "work directory present but metadata is unreadable"
			}
			return true, detail
		}
		return false, ""
	}

	if meta.Workdir.Mode == "copy" || meta.Workdir.Mode == "overlay" {
		workDir := store.WorkDir(sandboxDir, meta.Workdir.HostPath)
		if hasUnappliedWork(workDir, meta.Workdir.BaselineSHA) {
			return true, "unapplied changes exist"
		}
	}

	for _, d := range meta.Directories {
		if d.Mode == "copy" || d.Mode == "overlay" {
			auxWorkDir := store.WorkDir(sandboxDir, d.HostPath)
			if hasUnappliedWork(auxWorkDir, d.BaselineSHA) {
				return true, "unapplied changes exist"
			}
		}
	}

	return false, ""
}

// initializeAgentFilesIfNeeded copies agent_files into the sandbox when they
// have not yet been initialized (e.g., sandbox predates the feature or
// ClearState was used). No-op if already initialized or no StateDir configured.
func initializeAgentFilesIfNeeded(layout config.Layout, agentDef *agent.Definition, sandboxDir string, meta *store.Meta, sbState *store.SandboxState) error {
	if sbState.AgentFilesInitialized || agentDef.StateDir == "" {
		return nil
	}
	cfg, err := config.LoadConfig(layout)
	if err != nil {
		// Preserves pre-refactor behavior: config load failures must not block
		// sandbox start. The agent_files copy is a best-effort convenience.
		return nil //nolint:nilerr // intentional: best-effort, not load-bearing
	}
	agentFilesConfig := resolvedAgentFiles(layout, cfg, meta)
	if agentFilesConfig == nil {
		return nil
	}
	if err := provision.CopyAgentFiles(agentDef, sandboxDir, agentFilesConfig, layout.HomeDir, layout.Env); err != nil {
		return fmt.Errorf("copy agent files on restart: %w", err)
	}
	sbState.AgentFilesInitialized = true
	if err := store.SaveSandboxState(sandboxDir, sbState); err != nil {
		return fmt.Errorf("save sandbox state: %w", err)
	}
	return nil
}

// resolvedAgentFiles returns the effective AgentFiles config after merging the
// profile chain if a profile is set. Returns nil if no AgentFiles are configured.
func resolvedAgentFiles(layout config.Layout, cfg *config.YoloaiConfig, meta *store.Meta) *config.AgentFilesConfig {
	agentFilesConfig := cfg.AgentFiles
	if meta.Profile == "" {
		return agentFilesConfig
	}
	chain, err := config.ResolveProfileChain(layout, meta.Profile)
	if err != nil {
		return agentFilesConfig
	}
	merged, err := config.MergeProfileChain(layout, cfg, chain)
	if err != nil || merged.AgentFiles == nil {
		return agentFilesConfig
	}
	return merged.AgentFiles
}

// resolveEnvForRestart loads the global config env and merges the profile
// chain if a profile is set. Returns the resolved environment map.
func resolveEnvForRestart(layout config.Layout, meta *store.Meta) (map[string]string, error) {
	cfg, err := config.LoadConfig(layout)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	envVars := cfg.Env
	if meta.Profile != "" {
		chain, chainErr := config.ResolveProfileChain(layout, meta.Profile)
		if chainErr == nil {
			merged, mergeErr := config.MergeProfileChain(layout, cfg, chain)
			if mergeErr == nil {
				envVars = merged.Env
			}
		}
	}
	return envVars, nil
}

// recreateContainer creates a new Docker container from meta.json. Incidental
// progress (e.g. a port-availability warning from filterAvailablePorts) is
// surfaced through n as Notices rather than a raw writer, since the restart
// entry points (Start/Reset) return their output as a *Result's Notices (F8).
func (m *Engine) recreateContainer(ctx context.Context, name string, meta *store.Meta, resume bool, n *notices) error {
	agentDef := agent.GetAgent(string(meta.Agent))
	if agentDef == nil {
		return NewConfigError("unknown agent %q in sandbox state — this sandbox was created with an agent that's not registered in the current yoloai installation; destroy and recreate the sandbox with a registered agent", meta.Agent)
	}

	sandboxDir := m.layout.SandboxDir(name)

	// Refresh seed files from host (handles OAuth token refresh between restarts)
	hasAPIKey := provision.HasAnyAPIKey(agentDef, nil)
	if _, err := provision.CopySeedFiles(agentDef, sandboxDir, hasAPIKey, m.layout.HomeDir); err != nil {
		return fmt.Errorf("refresh seed files: %w", err)
	}

	// Re-apply container settings (copySeedFiles overwrites settings.json
	// with the host version, which lacks sandbox-specific settings like
	// skipDangerousModePermissionPrompt)
	if err := provision.EnsureContainerSettings(agentDef, sandboxDir, meta.Isolation); err != nil {
		return fmt.Errorf("ensure container settings: %w", err)
	}

	// Copy agent_files if not yet initialized (e.g., sandbox created before
	// agent_files was configured, or after --clean reset)
	sbState, stateErr := store.LoadSandboxState(sandboxDir)
	if stateErr != nil {
		return fmt.Errorf("load sandbox state: %w", stateErr)
	}
	if err := initializeAgentFilesIfNeeded(m.layout, agentDef, sandboxDir, meta, sbState); err != nil {
		return err
	}

	// Read existing runtime-config.json
	configData, err := os.ReadFile(filepath.Join(sandboxDir, store.RuntimeConfigFile)) //nolint:gosec // path is sandbox-controlled
	if err != nil {
		return fmt.Errorf("read runtime-config.json: %w", err)
	}

	// Build sandbox state for container launch
	workdir, err := ParseDirArg(meta.Workdir.HostPath+":"+string(meta.Workdir.Mode), m.layout.HomeDir, m.layout.Env)
	if err != nil {
		return fmt.Errorf("parse workdir: %w", err)
	}

	// Extract tmux_conf from runtime-config.json
	var cfgJSON containerConfig
	if err := json.Unmarshal(configData, &cfgJSON); err != nil {
		return fmt.Errorf("parse runtime-config.json: %w", err)
	}

	// Rebuild aux dir args from meta
	var auxDirs []*DirSpec
	for _, d := range meta.Directories {
		auxDirs = append(auxDirs, &DirSpec{
			Path:      d.HostPath,
			MountPath: d.MountPath,
			Mode:      DirMode(d.Mode),
		})
	}

	// Resolve env: load config, then merge profile chain if profile was used.
	envVars, err := resolveEnvForRestart(m.layout, meta)
	if err != nil {
		return err
	}

	// Recover sudo-stripped credentials, mirroring Create: under
	// `sudo yoloai restart` (without -E) the API-key/OAuth env vars are absent
	// from os.Environ, so without this the restart would relaunch the agent
	// unauthenticated even though the original `new` worked.
	credOverrides := provision.RecoverSudoCredentials()

	state := &State{
		Name:          name,
		SandboxDir:    sandboxDir,
		Workdir:       workdir,
		WorkCopyDir:   store.WorkDir(sandboxDir, meta.Workdir.HostPath),
		AuxDirs:       auxDirs,
		Agent:         agentDef,
		Model:         meta.Model,
		Profile:       meta.Profile,
		ImageRef:      meta.ImageRef,
		Env:           envVars,
		HasPrompt:     meta.HasPrompt,
		NetworkMode:   meta.NetworkMode,
		NetworkAllow:  meta.NetworkAllow,
		Ports:         meta.Ports,
		ConfigMounts:  meta.Mounts,
		TmuxConf:      cfgJSON.TmuxConf,
		Resources:     meta.Resources,
		CapAdd:        meta.CapAdd,
		Devices:       meta.Devices,
		Setup:         meta.Setup,
		Isolation:     meta.Isolation,
		VscodeTunnel:  meta.VscodeTunnel,
		CredOverrides: credOverrides,
		ConfigJSON:    configData,
		Layout:        m.layout,
		HomeDir:       m.layout.HomeDir,
		Output:        &noticeWriter{n: n, level: NoticeWarn},
	}

	if resume {
		state.PromptSourcePath = filepath.Join(sandboxDir, "resume-prompt.txt")
	}

	if err := launch.LaunchContainer(ctx, m.deps(), state); err != nil {
		return err
	}

	// Execute VM-side work directory setup (Tart VMs).
	// Always re-run when recreating: the old VM was destroyed, so its local
	// work directory no longer exists even if BaselineSHA is already set.
	if meta.Workdir.Mode == "copy" {
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
func (m *Engine) relaunchAgent(ctx context.Context, name string, meta *store.Meta) error {
	sandboxDir := m.layout.SandboxDir(name)

	// Read runtime-config.json to get agent_command
	configData, err := os.ReadFile(filepath.Join(sandboxDir, store.RuntimeConfigFile)) //nolint:gosec // path is sandbox-controlled
	if err != nil {
		return fmt.Errorf("read runtime-config.json: %w", err)
	}

	var cfg containerConfig
	if err := json.Unmarshal(configData, &cfg); err != nil {
		return fmt.Errorf("parse runtime-config.json: %w", err)
	}

	_, err = ExecInContainer(ctx, m.runtime, name, meta, m.layout.HostUID,
		tmuxCmd(cfg.TmuxSocket, "respawn-pane", "-t", "main", "-k", cfg.AgentCommand),
	)
	if err != nil {
		return fmt.Errorf("relaunch agent: %w", err)
	}

	return nil
}

// relaunchAgentWithResume relaunches the agent in interactive mode and sends
// the resume prompt (preamble + original prompt) via tmux.
func (m *Engine) relaunchAgentWithResume(ctx context.Context, name string, meta *store.Meta) error {
	sandboxDir := m.layout.SandboxDir(name)

	configData, err := os.ReadFile(filepath.Join(sandboxDir, store.RuntimeConfigFile)) //nolint:gosec // path is sandbox-controlled
	if err != nil {
		return fmt.Errorf("read runtime-config.json: %w", err)
	}

	var cfg containerConfig
	if err := json.Unmarshal(configData, &cfg); err != nil {
		return fmt.Errorf("parse runtime-config.json: %w", err)
	}

	agentDef := agent.GetAgent(string(meta.Agent))
	if agentDef == nil {
		return NewConfigError("unknown agent %q in sandbox state — this sandbox was created with an agent that's not registered in the current yoloai installation; destroy and recreate the sandbox with a registered agent", meta.Agent)
	}

	// Resolve agent_args from config/profile
	agentArgs := resolveAgentArgs(m.layout, string(meta.Agent), meta.Profile)

	// Build interactive command (no headless prompt baked in)
	interactiveCmd := invocation.BuildAgentCommand(agentDef, meta.Model, "", agentArgs, cfg.Passthrough)

	// Respawn with interactive command
	_, err = ExecInContainer(ctx, m.runtime, name, meta, m.layout.HostUID,
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
func (m *Engine) sendResumePrompt(ctx context.Context, name, sandboxDir string, cfg containerConfig, meta *store.Meta) error {
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
		delaySec := max(cfg.StartupDelay/1000, 1)
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

	_, err = ExecInContainer(ctx, m.runtime, name, meta, m.layout.HostUID, []string{
		"bash", "-c", "nohup bash -c '" + strings.ReplaceAll(script, "'", "'\"'\"'") + "' _ \"$1\" >/dev/null 2>&1 &", "_", resumeText,
	})
	return err
}

// relaunchAgentWithCustomPrompt relaunches the agent in interactive mode and sends
// the custom prompt directly (no resume preamble) via tmux.
func (m *Engine) relaunchAgentWithCustomPrompt(ctx context.Context, name string, meta *store.Meta, promptText string) error {
	sandboxDir := m.layout.SandboxDir(name)

	configData, err := os.ReadFile(filepath.Join(sandboxDir, store.RuntimeConfigFile)) //nolint:gosec // path is sandbox-controlled
	if err != nil {
		return fmt.Errorf("read runtime-config.json: %w", err)
	}

	var cfg containerConfig
	if err := json.Unmarshal(configData, &cfg); err != nil {
		return fmt.Errorf("parse runtime-config.json: %w", err)
	}

	agentDef := agent.GetAgent(string(meta.Agent))
	if agentDef == nil {
		return NewConfigError("unknown agent %q in sandbox state — this sandbox was created with an agent that's not registered in the current yoloai installation; destroy and recreate the sandbox with a registered agent", meta.Agent)
	}

	agentArgs := resolveAgentArgs(m.layout, string(meta.Agent), meta.Profile)
	interactiveCmd := invocation.BuildAgentCommand(agentDef, meta.Model, "", agentArgs, cfg.Passthrough)
	// Prefer the stored launch prefix (W1a single-source-of-truth) when the gate
	// is set; fall back to re-invoking PrepareAgentCommand for sandboxes created
	// before this field existed. W1b retires the fallback one release later.
	if cfg.UseLaunchPrefix {
		interactiveCmd = cfg.AgentLaunchPrefix + interactiveCmd
	} else {
		interactiveCmd = runtime.PrepareAgentCommandFor(m.runtime, interactiveCmd)
	}
	_, err = ExecInContainer(ctx, m.runtime, name, meta, m.layout.HostUID,
		tmuxCmd(cfg.TmuxSocket, "respawn-pane", "-t", "main", "-k", interactiveCmd),
	)
	if err != nil {
		return fmt.Errorf("relaunch agent: %w", err)
	}

	return m.sendCustomPrompt(ctx, name, sandboxDir, cfg, promptText, meta)
}

// sendCustomPrompt waits for the agent to be ready and delivers the custom
// prompt directly (without resume preamble) via tmux load-buffer/paste-buffer.
func (m *Engine) sendCustomPrompt(ctx context.Context, name, sandboxDir string, cfg containerConfig, promptText string, meta *store.Meta) error {
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
		delaySec := max(cfg.StartupDelay/1000, 1)
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

	_, err := ExecInContainer(ctx, m.runtime, name, meta, m.layout.HostUID, []string{
		"bash", "-c", "nohup bash -c '" + strings.ReplaceAll(script, "'", "'\"'\"'") + "' _ \"$1\" >/dev/null 2>&1 &", "_", promptText,
	})
	return err
}

// prepareCustomPromptFiles writes the resume-prompt.txt (custom prompt, no preamble)
// and patches runtime-config.json for interactive command mode.
func (m *Engine) prepareCustomPromptFiles(name string, meta *store.Meta, promptText string) error {
	sandboxDir := m.layout.SandboxDir(name)

	// Write resume-prompt.txt (custom prompt, no preamble)
	if err := fileutil.WriteFile(filepath.Join(sandboxDir, "resume-prompt.txt"), []byte(promptText), 0600); err != nil {
		return fmt.Errorf("write resume-prompt.txt: %w", err)
	}

	// Patch runtime-config.json: replace agent_command with interactive version
	configPath := filepath.Join(sandboxDir, store.RuntimeConfigFile)
	configData, err := os.ReadFile(configPath) //nolint:gosec // path is sandbox-controlled
	if err != nil {
		return fmt.Errorf("read runtime-config.json: %w", err)
	}

	var cfg containerConfig
	if err := json.Unmarshal(configData, &cfg); err != nil {
		return fmt.Errorf("parse runtime-config.json: %w", err)
	}

	agentDef := agent.GetAgent(string(meta.Agent))
	if agentDef == nil {
		return NewConfigError("unknown agent %q in sandbox state — this sandbox was created with an agent that's not registered in the current yoloai installation; destroy and recreate the sandbox with a registered agent", meta.Agent)
	}

	agentArgs := resolveAgentArgs(m.layout, string(meta.Agent), meta.Profile)
	cfg.AgentCommand = invocation.BuildAgentCommand(agentDef, meta.Model, "", agentArgs, cfg.Passthrough)

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
func (m *Engine) prepareResumeFiles(name string, meta *store.Meta) error {
	sandboxDir := m.layout.SandboxDir(name)

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
	configPath := filepath.Join(sandboxDir, store.RuntimeConfigFile)
	configData, err := os.ReadFile(configPath) //nolint:gosec // path is sandbox-controlled
	if err != nil {
		return fmt.Errorf("read runtime-config.json: %w", err)
	}

	var cfg containerConfig
	if err := json.Unmarshal(configData, &cfg); err != nil {
		return fmt.Errorf("parse runtime-config.json: %w", err)
	}

	agentDef := agent.GetAgent(string(meta.Agent))
	if agentDef == nil {
		return NewConfigError("unknown agent %q in sandbox state — this sandbox was created with an agent that's not registered in the current yoloai installation; destroy and recreate the sandbox with a registered agent", meta.Agent)
	}

	agentArgs := resolveAgentArgs(m.layout, string(meta.Agent), meta.Profile)
	cfg.AgentCommand = invocation.BuildAgentCommand(agentDef, meta.Model, "", agentArgs, cfg.Passthrough)

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
func (m *Engine) cleanupResumeFiles(name string) {
	_ = os.Remove(filepath.Join(m.layout.SandboxDir(name), "resume-prompt.txt"))
}

// resetInPlace resets the workspace while the agent is still running.
// Syncs files from host, recreates git baseline, and notifies the agent via tmux.
func (m *Engine) resetInPlace(ctx context.Context, opts ResetOptions, meta *store.Meta, sandboxDir string) error {
	// Tart VMs store the work directory inside the VM, not on the host.
	// In-place reset requires direct host access to the work directory.
	if _, ok := m.runtime.(runtime.WorkDirSetup); ok {
		return fmt.Errorf("in-place reset not supported for Tart VMs (work dir is inside VM)")
	}

	workDir := store.WorkDir(sandboxDir, meta.Workdir.HostPath)

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
	if err := store.SaveMeta(sandboxDir, meta); err != nil {
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
func (m *Engine) clearCacheAndFiles(opts ResetOptions) error {
	sandboxDir := m.layout.SandboxDir(opts.Name)
	// Load metadata to check security mode for permissions
	meta, err := store.LoadMeta(sandboxDir)
	if err != nil {
		return fmt.Errorf("load metadata: %w", err)
	}

	perms := Perms(meta.Isolation)

	if !opts.KeepCache {
		cacheDir := store.CacheDir(sandboxDir)
		if err := os.RemoveAll(cacheDir); err != nil {
			return fmt.Errorf("remove cache: %w", err)
		}
		if err := fileutil.MkdirAllPerm(cacheDir, perms.Dir); err != nil {
			return fmt.Errorf("recreate cache: %w", err)
		}
	}
	if !opts.KeepFiles {
		filesDir := store.FilesDir(sandboxDir)
		if err := os.RemoveAll(filesDir); err != nil {
			return fmt.Errorf("remove files: %w", err)
		}
		if err := fileutil.MkdirAllPerm(filesDir, perms.Dir); err != nil {
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
func (m *Engine) sendResetNotification(ctx context.Context, name, sandboxDir string, noPrompt, hasPrompt bool, meta *store.Meta) error {
	// Read runtime-config.json for submit_sequence
	configData, err := os.ReadFile(filepath.Join(sandboxDir, store.RuntimeConfigFile)) //nolint:gosec // path is sandbox-controlled
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

	_, err = ExecInContainer(ctx, m.runtime, name, meta, m.layout.HostUID, []string{
		"bash", "-c", script, "_", resetNotification,
	})
	return err
}

// resolveAgentArgs loads agent_args for the given agent from config and profile.
// Returns empty string if no args are configured.
func resolveAgentArgs(layout config.Layout, agentName, profileName string) string {
	cfg, err := config.LoadConfig(layout)
	if err != nil {
		return ""
	}
	if profileName != "" {
		chain, chainErr := config.ResolveProfileChain(layout, profileName)
		if chainErr == nil {
			merged, mergeErr := config.MergeProfileChain(layout, cfg, chain)
			if mergeErr == nil {
				return merged.AgentArgs[agentName]
			}
		}
	}
	return cfg.AgentArgs[agentName]
}

// patchConfigVscodeTunnel reads runtime-config.json, enables the vscode_tunnel
// fields, and writes it back. Called when --vscode-tunnel is added to an
// existing sandbox via start/restart.
func patchConfigVscodeTunnel(sandboxDir, sandboxName string) error {
	configPath := filepath.Join(sandboxDir, store.RuntimeConfigFile)
	data, err := os.ReadFile(configPath) //nolint:gosec // path is sandbox-controlled
	if err != nil {
		return fmt.Errorf("read runtime-config.json: %w", err)
	}

	var cfg containerConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse runtime-config.json: %w", err)
	}

	cfg.VscodeTunnel = true
	cfg.VscodeTunnelName = invocation.SanitizeTunnelName(sandboxName)
	updated, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal runtime-config.json: %w", err)
	}

	if err := fileutil.WriteFile(configPath, updated, 0600); err != nil {
		return fmt.Errorf("write runtime-config.json: %w", err)
	}
	return nil
}

// patchConfigDebug reads runtime-config.json, sets the debug field, and writes it back.
func patchConfigDebug(sandboxDir string, debug bool) error {
	configPath := filepath.Join(sandboxDir, store.RuntimeConfigFile)
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
	configPath := filepath.Join(sandboxDir, store.RuntimeConfigFile)
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

// ABOUTME: Reset verb and its supporting helpers — re-copy workdir from the
// ABOUTME: host, clear agent state/cache/logs, and restart or reset in-place
// ABOUTME: depending on runtime and workdir mode.
package lifecycle

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/kstenerud/yoloai/copyflow"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/git"
	"github.com/kstenerud/yoloai/internal/orchestrator/launch"
	"github.com/kstenerud/yoloai/internal/orchestrator/state"
	"github.com/kstenerud/yoloai/internal/orchestrator/status"
	"github.com/kstenerud/yoloai/internal/sysexec"
	"github.com/kstenerud/yoloai/internal/workspace"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/store"
)

// ResetOptions holds parameters for the reset command.
type ResetOptions struct {
	Name       string
	Restart    bool   // stop and restart container
	ClearState bool   // also wipe agent-runtime directory (replaces Clean)
	KeepCache  bool   // preserve cache directory
	KeepFiles  bool   // preserve files directory
	NoPrompt   bool   // skip re-sending prompt after reset
	Prompt     string // if set, overwrite prompt.txt before reset (re-sent on restart)
	Debug      bool   // enable entrypoint debug logging
	// Env is the per-sandbox environment overlay applied when the container is
	// recreated (Restart). Merged over the resolved config+profile env, never
	// persisted — the caller re-supplies it (secrets are the caller's concern).
	Env map[string]string
}

// Reset re-copies the workdir from the original host directory and resets
// the git baseline. By default, resets in-place (agent stays running).
// With --restart, stops and restarts the container.
func Reset(ctx context.Context, d state.Deps, opts ResetOptions) (*ResetResult, error) {
	unlock, err := store.AcquireLock(d.Layout, opts.Name)
	if err != nil {
		return nil, err
	}
	defer unlock()

	slog.Info("resetting sandbox", "event", "sandbox.reset", "sandbox", opts.Name)
	sandboxDir := d.Layout.SandboxDir(opts.Name)
	if err := store.RequireSandboxDir(sandboxDir); err != nil {
		return nil, err
	}

	meta, err := store.LoadEnvironment(sandboxDir)
	if err != nil {
		return nil, err
	}

	if meta.Workdir().Mode == "rw" {
		return nil, fmt.Errorf("reset is not applicable for :rw directories — changes are already in the original")
	}

	if opts.Prompt != "" {
		promptPath := filepath.Join(sandboxDir, "prompt.txt")
		if err := fileutil.WriteFile(promptPath, []byte(opts.Prompt), 0600); err != nil {
			return nil, fmt.Errorf("write prompt.txt: %w", err)
		}
	}

	var n notices

	// Auto-upgrade to restart: --state implies restart (can't wipe state while agent is running)
	if opts.ClearState {
		opts.Restart = true
	}

	// Auto-upgrade to restart: overlay mode requires container restart
	if meta.Workdir().Mode == "overlay" {
		opts.Restart = true
	}

	// Auto-upgrade to restart: a SandboxSide backend (e.g. Tart) keeps the work
	// copy inside the sandbox, so in-place reset (host-side file access) is not
	// possible.
	if runtime.LocalityOf(d.Runtime) == runtime.LocalitySandboxSide {
		opts.Restart = true
	}

	// Auto-upgrade to restart: container not running
	if !opts.Restart {
		st, err := status.DetectStatus(ctx, d.Runtime, store.InstanceName(d.Layout.Principal, opts.Name), sandboxDir)
		if err != nil || (st != status.StatusActive && st != status.StatusIdle) {
			n.infof("Container is not running, upgrading to restart")
			opts.Restart = true
		}
	}

	if !opts.Restart {
		err := resetInPlace(ctx, d, opts, meta, sandboxDir)
		return &ResetResult{Notices: n.list}, err
	}

	err = prepareResetRestart(ctx, d, opts, sandboxDir, meta, &n)
	return &ResetResult{Notices: n.list}, err
}

// NeedsConfirmation checks if a sandbox requires confirmation before
// destruction. Returns true when destruction would lose unapplied work —
// uncommitted changes or commits beyond the baseline — OR when that can't be
// verified (a VM-local backend that is not running), with a reason string for
// the prompt. A running agent is NOT a blocker on its own: a live but clean
// sandbox has nothing to lose, and gating on it forced --abandon-unapplied for
// every routine destroy. The work signal is read via the backend's git context
// (in-VM for Tart), so callers must open the runtime first.
func NeedsConfirmation(ctx context.Context, d state.Deps, name string) (bool, string) {
	hostGit := git.NewHost(d.Layout)
	sandboxGit := git.NewSandbox(d.Layout, d.Runtime, name)
	return unappliedWorkReason(ctx, hostGit, sandboxGit, d.Layout.SandboxDir(name))
}

// unappliedWorkReason reports whether a sandbox holds work that destruction
// would lose — uncommitted/beyond-baseline changes in the workdir or any aux
// directory — independent of whether the agent is running. A WorkUnknown probe
// (a VM-local backend that is not running, so the in-VM working copy can't be
// read) fails safe: it blocks destroy with a reason that points to the cause.
func unappliedWorkReason(ctx context.Context, hostGit, sandboxGit *git.Git, sandboxDir string) (bool, string) {
	meta, err := store.LoadEnvironment(sandboxDir)
	if err != nil {
		// Environment is unreadable (a broken sandbox). Don't assume it's empty —
		// that silently discards recoverable work. Fall back to a
		// filesystem-level probe of work/ so destroy still prompts.
		if workState, detail := status.ProbeWorkData(ctx, hostGit, sandboxDir); workState != status.WorkDataNone {
			if detail == "" {
				detail = "work directory present but metadata is unreadable"
			}
			return true, detail
		}
		return false, ""
	}

	if blocked, reason := dirWorkReason(ctx, sandboxGit, sandboxDir, meta.Workdir().Mode, meta.Workdir().HostPath, meta.Workdir().BaselineSHA); blocked {
		return true, reason
	}
	for _, dirEnv := range meta.AuxDirs() {
		if blocked, reason := dirWorkReason(ctx, sandboxGit, sandboxDir, dirEnv.Mode, dirEnv.HostPath, dirEnv.BaselineSHA); blocked {
			return true, reason
		}
	}

	return false, ""
}

// dirWorkReason probes one copy/overlay directory for unapplied work and maps
// the result to a destroy-blocking reason. Non-copy/overlay modes never block.
func dirWorkReason(ctx context.Context, sandboxGit *git.Git, sandboxDir string, mode store.DirMode, hostPath, baselineSHA string) (bool, string) {
	if mode != "copy" && mode != "overlay" {
		return false, ""
	}
	workDir := store.WorkDir(sandboxDir, hostPath)
	switch copyflow.HasUnappliedWorkVia(ctx, sandboxGit, workDir, baselineSHA) {
	case copyflow.WorkDirty:
		return true, "unapplied changes exist"
	case copyflow.WorkUnknown:
		return true, "sandbox is stopped, so unapplied changes can't be verified (start it to check, or use --abandon-unapplied)"
	case copyflow.WorkClean:
	}
	return false, ""
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
func resetCopyWorkdir(ctx context.Context, d state.Deps, sandboxName, sandboxDir string, meta *store.Environment) (string, error) {
	workDir := store.WorkDir(sandboxDir, meta.Workdir().HostPath)

	if err := os.RemoveAll(workDir); err != nil {
		return "", fmt.Errorf("remove work copy: %w", err)
	}
	if _, err := os.Stat(meta.Workdir().HostPath); err != nil {
		return "", fmt.Errorf("original directory no longer exists: %s", meta.Workdir().HostPath)
	}
	slog.Debug("re-copying workdir", "event", "sandbox.reset.workdir", "sandbox", sandboxName, "host_path", meta.Workdir().HostPath)
	if err := workspace.CopyDir(meta.Workdir().HostPath, workDir); err != nil {
		return "", fmt.Errorf("re-copy workdir: %w", err)
	}

	g := git.NewHost(d.Layout)
	if git.IsGitRepo(workDir) {
		sha, err := g.HeadSHA(ctx, workDir)
		if err != nil {
			return "", fmt.Errorf("read HEAD of re-copied workdir: %w", err)
		}
		return sha, nil
	}
	// SandboxSide backends (e.g. Tart) require the container running to exec setup
	// commands inside the sandbox. HostSide backends baseline on the host before start.
	if runtime.LocalityOf(d.Runtime) == runtime.LocalitySandboxSide {
		// Defer baseline creation — executeVMWorkDirSetup will call it after container start
		return "", nil
	}
	sha, err := g.Baseline(ctx, workDir)
	if err != nil {
		return "", fmt.Errorf("re-create git baseline: %w", err)
	}
	return sha, nil
}

// resetAuxCopyDir resets a single aux :copy dir and returns the new baseline SHA.
func resetAuxCopyDir(ctx context.Context, g *git.Git, sandboxDir string, d store.DirEnvironment) (string, error) {
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
	if git.IsGitRepo(auxWorkDir) {
		sha, err := g.HeadSHA(ctx, auxWorkDir)
		if err != nil {
			return "", fmt.Errorf("read HEAD of re-copied aux dir %s: %w", d.HostPath, err)
		}
		return sha, nil
	}
	sha, err := g.Baseline(ctx, auxWorkDir)
	if err != nil {
		return "", fmt.Errorf("git baseline for aux dir %s: %w", d.HostPath, err)
	}
	return sha, nil
}

// resetAuxDirs resets all aux :copy and :overlay directories in meta,
// updating BaselineSHA in-place.
func resetAuxDirs(ctx context.Context, g *git.Git, sandboxDir string, meta *store.Environment) error {
	for i, d := range meta.AuxDirs() {
		switch d.Mode {
		case store.DirModeCopy:
			sha, err := resetAuxCopyDir(ctx, g, sandboxDir, d)
			if err != nil {
				return err
			}
			meta.Dirs[1+i].BaselineSHA = sha
		case store.DirModeOverlay:
			if err := resetOverlayDirs(sandboxDir, d.HostPath); err != nil {
				return fmt.Errorf("aux overlay %s: %w", d.HostPath, err)
			}
			meta.Dirs[1+i].BaselineSHA = ""
		case store.DirModeRW, store.DirModeRO, "":
			// rw and ro aux dirs have no baseline to reset
		}
	}
	return nil
}

// clearAgentState wipes and recreates the agent-runtime directory and resets
// the AgentFilesInitialized flag.
func clearAgentState(sandboxDir string, perms store.IsolationPerms) error {
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

// reinitLogs removes and recreates the sandbox log files with appropriate permissions.
func reinitLogs(sandboxDir string, perms store.IsolationPerms) {
	_ = os.RemoveAll(filepath.Join(sandboxDir, store.LogsDir))
	_ = fileutil.MkdirAllPerm(filepath.Join(sandboxDir, store.LogsDir), perms.Dir)
	for _, logFile := range []string{store.SandboxJSONLFile, store.MonitorJSONLFile, store.HooksJSONLFile} {
		_ = fileutil.WriteFilePerm(filepath.Join(sandboxDir, logFile), nil, perms.File)
	}
}

// resetWorkdir resets the main workdir (overlay or copy) and returns the new baseline SHA.
func resetWorkdir(ctx context.Context, d state.Deps, sandboxName, sandboxDir string, meta *store.Environment) (string, error) {
	if meta.Workdir().Mode == "overlay" {
		if err := resetOverlayDirs(sandboxDir, meta.Workdir().HostPath); err != nil {
			return "", err
		}
		return "", nil // baseline deferred — container restart recreates it
	}
	return resetCopyWorkdir(ctx, d, sandboxName, sandboxDir, meta)
}

// applyPostResetOptions handles optional post-reset actions: wiping agent state,
// clearing cache/files, patching debug config, and temporarily hiding prompt.txt.
// Returns a cleanup func that must be deferred by the caller (restores prompt.txt).
func applyPostResetOptions(d state.Deps, opts ResetOptions, sandboxDir string, perms store.IsolationPerms) (func(), error) {
	if opts.ClearState {
		if err := clearAgentState(sandboxDir, perms); err != nil {
			return nil, err
		}
	}
	if err := clearCacheAndFiles(d, opts); err != nil {
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
func prepareResetRestart(ctx context.Context, d state.Deps, opts ResetOptions, sandboxDir string, meta *store.Environment, n *notices) error {
	// Destroy the container so start() sees StatusRemoved and does a clean
	// recreate. Using Remove (not Stop) avoids suspending a VM we're about
	// to rebuild — the suspend state would be stale after the host workdir
	// is re-copied, and handleSuspendedResume would resume the wrong files.
	cname := store.InstanceName(d.Layout.Principal, opts.Name)
	_ = d.Runtime.Remove(ctx, cname)

	perms := store.Perms()

	// Clear logs so each run starts fresh
	slog.Debug("clearing logs", "event", "sandbox.reset.logs", "sandbox", opts.Name)
	reinitLogs(sandboxDir, perms)

	// Reset main workdir
	newSHA, err := resetWorkdir(ctx, d, opts.Name, sandboxDir, meta)
	if err != nil {
		return err
	}

	// Reset aux :copy and :overlay dirs
	if err := resetAuxDirs(ctx, git.NewHost(d.Layout), sandboxDir, meta); err != nil {
		return err
	}

	// Update environment.json
	meta.Workdir().BaselineSHA = newSHA
	if err := store.SaveEnvironment(sandboxDir, meta); err != nil {
		return err
	}

	// Apply post-reset options (state, cache/files, debug, no-prompt)
	cleanup, err := applyPostResetOptions(d, opts, sandboxDir, perms)
	if err != nil {
		return err
	}
	defer cleanup()

	slog.Info("reset complete", "event", "sandbox.reset.complete", "sandbox", opts.Name)
	// Start the container; its status notices flow into the reset's notices.
	if err := start(ctx, d, opts.Name, StartOptions{Env: opts.Env}, n); err != nil {
		return err
	}

	// Execute VM-side work directory setup if baseline was deferred (Tart VMs)
	// For :copy mode, if BaselineSHA is empty, VM setup is needed
	if meta.Workdir().Mode == "copy" && meta.Workdir().BaselineSHA == "" {
		if err := launch.ExecuteVMWorkDirSetup(ctx, d.Runtime, opts.Name, sandboxDir, meta); err != nil {
			return fmt.Errorf("VM work dir setup: %w", err)
		}
	}

	return nil
}

// resetInPlace resets the workspace while the agent is still running.
// Syncs files from host, recreates git baseline, and notifies the agent via tmux.
func resetInPlace(ctx context.Context, d state.Deps, opts ResetOptions, meta *store.Environment, sandboxDir string) error {
	// In-place reset requires direct host access to the work copy; a SandboxSide
	// backend (e.g. Tart) keeps it inside the sandbox, so it is unsupported there.
	if runtime.LocalityOf(d.Runtime) == runtime.LocalitySandboxSide {
		return fmt.Errorf("in-place reset not supported when the work copy is inside the sandbox (SandboxSide backend, e.g. Tart)")
	}

	workDir := store.WorkDir(sandboxDir, meta.Workdir().HostPath)
	g := git.NewHost(d.Layout)
	rsyncEnv := d.Layout.Env().EnvForHostTool()

	// Re-sync workdir from host (bind-mount makes changes visible in container)
	if err := rsyncDir(rsyncEnv, meta.Workdir().HostPath, workDir); err != nil {
		return fmt.Errorf("rsync workdir: %w", err)
	}

	// Record baseline — preserve git history if source is a git repo
	var newSHA string
	if git.IsGitRepo(workDir) {
		sha, err := g.HeadSHA(ctx, workDir)
		if err != nil {
			return fmt.Errorf("read HEAD of resynced workdir: %w", err)
		}
		newSHA = sha
	} else {
		sha, err := g.Baseline(ctx, workDir)
		if err != nil {
			return fmt.Errorf("re-create git baseline: %w", err)
		}
		newSHA = sha
	}

	// Update environment.json
	meta.Workdir().BaselineSHA = newSHA
	if err := store.SaveEnvironment(sandboxDir, meta); err != nil {
		return err
	}

	// Clear cache and files directories (unless --keep-X)
	if err := clearCacheAndFiles(d, opts); err != nil {
		return err
	}

	// Notify agent via tmux
	return sendResetNotification(ctx, d, opts.Name, sandboxDir, opts.NoPrompt, meta.HasPrompt, meta)
}

// clearCacheAndFiles clears the cache and files directories unless --keep-X flags are set.
func clearCacheAndFiles(d state.Deps, opts ResetOptions) error {
	sandboxDir := d.Layout.SandboxDir(opts.Name)
	perms := store.Perms()

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
//
// Why rsync and not RemoveAll+CopyDir (as the restart path does): in-place
// reset runs while the agent's container is live with dst bind-mounted in. A
// differential sync (rsync --delete) never leaves a window where the tree has
// vanished out from under the running container; a wipe-then-copy would. rsync
// is therefore a deliberate runtime dependency on container backends (see
// CLAUDE.md "Runtime dependencies"); the one backend where host-side access is
// impossible — Tart, whose workdir lives inside the VM — is excluded by
// resetInPlace before reaching here.
func rsyncDir(env []string, src, dst string) error {
	cmd := sysexec.Command(env, "rsync", "-a", "--delete", src+"/", dst+"/")
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
func sendResetNotification(ctx context.Context, d state.Deps, name, sandboxDir string, noPrompt, hasPrompt bool, meta *store.Environment) error {
	// Read runtime-config.json for submit_sequence
	cfg, err := loadContainerConfig(sandboxDir)
	if err != nil {
		return err
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

	_, err = status.ExecInContainer(ctx, d.Runtime, name, meta, d.Layout.HostUID, []string{
		"bash", "-c", script, "_", resetNotification,
	})
	return err
}

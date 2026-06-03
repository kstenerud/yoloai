// ABOUTME: Container-recreate and agent-relaunch helpers used by both Start
// ABOUTME: and Reset — recreateContainer, tmux wrappers, relaunch variants,
// ABOUTME: and resume/custom-prompt file preparation.
package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox/invocation"
	"github.com/kstenerud/yoloai/internal/sandbox/launch"
	provision "github.com/kstenerud/yoloai/internal/sandbox/provision"
	"github.com/kstenerud/yoloai/internal/sandbox/runtimeconfig"
	"github.com/kstenerud/yoloai/internal/sandbox/state"
	"github.com/kstenerud/yoloai/internal/sandbox/status"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/yoerrors"
)

// initializeAgentFilesIfNeeded copies agent_files into the sandbox when they
// have not yet been initialized (e.g., sandbox predates the feature or
// ClearState was used). No-op if already initialized or no StateDir configured.
func initializeAgentFilesIfNeeded(layout config.Layout, agentDef *agent.Definition, sandboxDir string, meta *store.Environment, sbState *store.SandboxState) error {
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
func resolvedAgentFiles(layout config.Layout, cfg *config.YoloaiConfig, meta *store.Environment) *config.AgentFilesConfig {
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
func resolveEnvForRestart(layout config.Layout, meta *store.Environment) (map[string]string, error) {
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

// recreateContainer creates a new Docker container from environment.json. Incidental
// progress (e.g. a port-availability warning from filterAvailablePorts) is
// surfaced through n as Notices rather than a raw writer, since the restart
// entry points (Start/Reset) return their output as a *Result's Notices (F8).
func recreateContainer(ctx context.Context, d state.Deps, name string, meta *store.Environment, resume bool, n *notices) error {
	agentDef := agent.GetAgent(string(meta.AgentType))
	if agentDef == nil {
		return yoerrors.NewConfigError("unknown agent %q in sandbox state — this sandbox was created with an agent that's not registered in the current yoloai installation; destroy and recreate the sandbox with a registered agent", meta.AgentType)
	}

	sandboxDir := d.Layout.SandboxDir(name)

	// Refresh seed files from host (handles OAuth token refresh between restarts)
	hasAPIKey := provision.HasAnyAPIKey(agentDef, d.Layout.Env)
	if _, err := provision.CopySeedFiles(agentDef, sandboxDir, hasAPIKey, d.Layout.HomeDir, d.Layout.Env); err != nil {
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
	if err := initializeAgentFilesIfNeeded(d.Layout, agentDef, sandboxDir, meta, sbState); err != nil {
		return err
	}

	// Read existing runtime-config.json
	configData, err := os.ReadFile(filepath.Join(sandboxDir, store.RuntimeConfigFile)) //nolint:gosec // path is sandbox-controlled
	if err != nil {
		return fmt.Errorf("read runtime-config.json: %w", err)
	}

	// Build sandbox state for container launch.
	// Workdir values are already resolved from meta (host-path and mode are stored
	// verbatim at create time), so we construct DirSpec directly rather than
	// re-parsing a ":suffix" string through the CLI layer.
	workdir := &state.DirSpec{
		Path:      meta.Workdir.HostPath,
		MountPath: meta.Workdir.MountPath,
		Mode:      store.DirMode(meta.Workdir.Mode),
	}

	// Extract tmux_conf from runtime-config.json
	var cfgJSON runtimeconfig.ContainerConfig
	if err := json.Unmarshal(configData, &cfgJSON); err != nil {
		return fmt.Errorf("parse runtime-config.json: %w", err)
	}

	// Rebuild aux dir args from meta
	var auxDirs []*state.DirSpec
	for _, dirEnv := range meta.Directories {
		auxDirs = append(auxDirs, &state.DirSpec{
			Path:      dirEnv.HostPath,
			MountPath: dirEnv.MountPath,
			Mode:      store.DirMode(dirEnv.Mode),
		})
	}

	// Resolve env: load config, then merge profile chain if profile was used.
	envVars, err := resolveEnvForRestart(d.Layout, meta)
	if err != nil {
		return err
	}

	sbState2 := &state.State{
		Name:         name,
		SandboxDir:   sandboxDir,
		Workdir:      workdir,
		WorkCopyDir:  store.WorkDir(sandboxDir, meta.Workdir.HostPath),
		AuxDirs:      auxDirs,
		Agent:        agentDef,
		Model:        meta.Model,
		Profile:      meta.Profile,
		ImageRef:     meta.ImageRef,
		Env:          envVars,
		HasPrompt:    meta.HasPrompt,
		NetworkMode:  meta.NetworkMode,
		NetworkAllow: meta.NetworkAllow,
		Ports:        meta.Ports,
		ConfigMounts: meta.Mounts,
		TmuxConf:     cfgJSON.TmuxConf,
		Resources:    meta.Resources,
		CapAdd:       meta.CapAdd,
		Devices:      meta.Devices,
		Setup:        meta.Setup,
		Isolation:    meta.Isolation,
		VscodeTunnel: meta.VscodeTunnel,
		ConfigJSON:   configData,
		Layout:       d.Layout,
		HomeDir:      d.Layout.HomeDir,
		Output:       &noticeWriter{n: n, level: NoticeWarn},
	}

	if resume {
		sbState2.PromptSourcePath = filepath.Join(sandboxDir, "resume-prompt.txt")
	}

	if err := launch.LaunchContainer(ctx, d, sbState2); err != nil {
		return err
	}

	// Execute VM-side work directory setup (Tart VMs).
	// Always re-run when recreating: the old VM was destroyed, so its local
	// work directory no longer exists even if BaselineSHA is already set.
	if meta.Workdir.Mode == "copy" {
		if err := launch.ExecuteVMWorkDirSetup(ctx, d.Runtime, name, sandboxDir, meta); err != nil {
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
func relaunchAgent(ctx context.Context, d state.Deps, name string, meta *store.Environment) error {
	sandboxDir := d.Layout.SandboxDir(name)

	// Read runtime-config.json to get agent_command
	configData, err := os.ReadFile(filepath.Join(sandboxDir, store.RuntimeConfigFile)) //nolint:gosec // path is sandbox-controlled
	if err != nil {
		return fmt.Errorf("read runtime-config.json: %w", err)
	}

	var cfg runtimeconfig.ContainerConfig
	if err := json.Unmarshal(configData, &cfg); err != nil {
		return fmt.Errorf("parse runtime-config.json: %w", err)
	}

	_, err = status.ExecInContainer(ctx, d.Runtime, name, meta, d.Layout.HostUID,
		tmuxCmd(cfg.TmuxSocket, "respawn-pane", "-t", "main", "-k", cfg.AgentCommand),
	)
	if err != nil {
		return fmt.Errorf("relaunch agent: %w", err)
	}

	return nil
}

// relaunchAgentWithResume relaunches the agent in interactive mode and sends
// the resume prompt (preamble + original prompt) via tmux.
func relaunchAgentWithResume(ctx context.Context, d state.Deps, name string, meta *store.Environment) error {
	sandboxDir := d.Layout.SandboxDir(name)

	configData, err := os.ReadFile(filepath.Join(sandboxDir, store.RuntimeConfigFile)) //nolint:gosec // path is sandbox-controlled
	if err != nil {
		return fmt.Errorf("read runtime-config.json: %w", err)
	}

	var cfg runtimeconfig.ContainerConfig
	if err := json.Unmarshal(configData, &cfg); err != nil {
		return fmt.Errorf("parse runtime-config.json: %w", err)
	}

	agentDef := agent.GetAgent(string(meta.AgentType))
	if agentDef == nil {
		return yoerrors.NewConfigError("unknown agent %q in sandbox state — this sandbox was created with an agent that's not registered in the current yoloai installation; destroy and recreate the sandbox with a registered agent", meta.AgentType)
	}

	// Resolve agent_args from config/profile
	agentArgs := resolveAgentArgs(d.Layout, string(meta.AgentType), meta.Profile)

	// Build interactive command (no headless prompt baked in)
	interactiveCmd := invocation.BuildAgentCommand(agentDef, meta.Model, "", agentArgs, cfg.Passthrough)

	// Respawn with interactive command
	_, err = status.ExecInContainer(ctx, d.Runtime, name, meta, d.Layout.HostUID,
		tmuxCmd(cfg.TmuxSocket, "respawn-pane", "-t", "main", "-k", interactiveCmd),
	)
	if err != nil {
		return fmt.Errorf("relaunch agent: %w", err)
	}

	// Deliver resume prompt after agent is ready
	return sendResumePrompt(ctx, d, name, sandboxDir, cfg, meta)
}

// sendResumePrompt waits for the agent to be ready and delivers the resume
// prompt (preamble + original prompt) via tmux load-buffer/paste-buffer.
func sendResumePrompt(ctx context.Context, d state.Deps, name, sandboxDir string, cfg runtimeconfig.ContainerConfig, meta *store.Environment) error {
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

	_, err = status.ExecInContainer(ctx, d.Runtime, name, meta, d.Layout.HostUID, []string{
		"bash", "-c", "nohup bash -c '" + strings.ReplaceAll(script, "'", "'\"'\"'") + "' _ \"$1\" >/dev/null 2>&1 &", "_", resumeText,
	})
	return err
}

// relaunchAgentWithCustomPrompt relaunches the agent in interactive mode and sends
// the custom prompt directly (no resume preamble) via tmux.
func relaunchAgentWithCustomPrompt(ctx context.Context, d state.Deps, name string, meta *store.Environment, promptText string) error {
	sandboxDir := d.Layout.SandboxDir(name)

	configData, err := os.ReadFile(filepath.Join(sandboxDir, store.RuntimeConfigFile)) //nolint:gosec // path is sandbox-controlled
	if err != nil {
		return fmt.Errorf("read runtime-config.json: %w", err)
	}

	var cfg runtimeconfig.ContainerConfig
	if err := json.Unmarshal(configData, &cfg); err != nil {
		return fmt.Errorf("parse runtime-config.json: %w", err)
	}

	agentDef := agent.GetAgent(string(meta.AgentType))
	if agentDef == nil {
		return yoerrors.NewConfigError("unknown agent %q in sandbox state — this sandbox was created with an agent that's not registered in the current yoloai installation; destroy and recreate the sandbox with a registered agent", meta.AgentType)
	}

	agentArgs := resolveAgentArgs(d.Layout, string(meta.AgentType), meta.Profile)
	interactiveCmd := invocation.BuildAgentCommand(agentDef, meta.Model, "", agentArgs, cfg.Passthrough)
	// Prefer the stored launch prefix (W1a single-source-of-truth) when the gate
	// is set; fall back to re-invoking PrepareAgentCommand for sandboxes created
	// before this field existed. W1b retires the fallback one release later.
	if cfg.UseLaunchPrefix {
		interactiveCmd = cfg.AgentLaunchPrefix + interactiveCmd
	} else {
		interactiveCmd = runtime.PrepareAgentCommandFor(d.Runtime, interactiveCmd)
	}
	_, err = status.ExecInContainer(ctx, d.Runtime, name, meta, d.Layout.HostUID,
		tmuxCmd(cfg.TmuxSocket, "respawn-pane", "-t", "main", "-k", interactiveCmd),
	)
	if err != nil {
		return fmt.Errorf("relaunch agent: %w", err)
	}

	return sendCustomPrompt(ctx, d, name, sandboxDir, cfg, promptText, meta)
}

// sendCustomPrompt waits for the agent to be ready and delivers the custom
// prompt directly (without resume preamble) via tmux load-buffer/paste-buffer.
func sendCustomPrompt(ctx context.Context, d state.Deps, name, sandboxDir string, cfg runtimeconfig.ContainerConfig, promptText string, meta *store.Environment) error {
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

	_, err := status.ExecInContainer(ctx, d.Runtime, name, meta, d.Layout.HostUID, []string{
		"bash", "-c", "nohup bash -c '" + strings.ReplaceAll(script, "'", "'\"'\"'") + "' _ \"$1\" >/dev/null 2>&1 &", "_", promptText,
	})
	return err
}

// prepareCustomPromptFiles writes the resume-prompt.txt (custom prompt, no preamble)
// and patches runtime-config.json for interactive command mode.
func prepareCustomPromptFiles(d state.Deps, name string, meta *store.Environment, promptText string) error {
	sandboxDir := d.Layout.SandboxDir(name)

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

	var cfg runtimeconfig.ContainerConfig
	if err := json.Unmarshal(configData, &cfg); err != nil {
		return fmt.Errorf("parse runtime-config.json: %w", err)
	}

	agentDef := agent.GetAgent(string(meta.AgentType))
	if agentDef == nil {
		return yoerrors.NewConfigError("unknown agent %q in sandbox state — this sandbox was created with an agent that's not registered in the current yoloai installation; destroy and recreate the sandbox with a registered agent", meta.AgentType)
	}

	agentArgs := resolveAgentArgs(d.Layout, string(meta.AgentType), meta.Profile)
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
func prepareResumeFiles(d state.Deps, name string, meta *store.Environment) error {
	sandboxDir := d.Layout.SandboxDir(name)

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

	var cfg runtimeconfig.ContainerConfig
	if err := json.Unmarshal(configData, &cfg); err != nil {
		return fmt.Errorf("parse runtime-config.json: %w", err)
	}

	agentDef := agent.GetAgent(string(meta.AgentType))
	if agentDef == nil {
		return yoerrors.NewConfigError("unknown agent %q in sandbox state — this sandbox was created with an agent that's not registered in the current yoloai installation; destroy and recreate the sandbox with a registered agent", meta.AgentType)
	}

	agentArgs := resolveAgentArgs(d.Layout, string(meta.AgentType), meta.Profile)
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
func cleanupResumeFiles(d state.Deps, name string) {
	_ = os.Remove(filepath.Join(d.Layout.SandboxDir(name), "resume-prompt.txt"))
}

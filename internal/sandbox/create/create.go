// ABOUTME: Low-level sandbox create helpers: machine-id generation and directory/file
// ABOUTME: write utilities used by the create pipeline.
package create

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/runtime/caps"
	"github.com/kstenerud/yoloai/internal/sandbox/archetype"
	"github.com/kstenerud/yoloai/internal/sandbox/invocation"
	"github.com/kstenerud/yoloai/internal/sandbox/launch"
	"github.com/kstenerud/yoloai/internal/sandbox/patch"
	provision "github.com/kstenerud/yoloai/internal/sandbox/provision"
	"github.com/kstenerud/yoloai/internal/sandbox/runtimeconfig"
	"github.com/kstenerud/yoloai/internal/sandbox/state"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/internal/yoerrors"
)

// Sentinel errors for the create pipeline.
var (
	// ErrSandboxExists is returned when a sandbox with the given name already
	// exists and Replace is false. Aliased in the façade (package sandbox) so
	// the public sandbox.ErrSandboxExists symbol is unchanged.
	ErrSandboxExists = errors.New("sandbox already exists")

	// ErrMissingAPIKey is returned when the selected agent requires an API key
	// but none is configured. Aliased in the façade so sandbox.ErrMissingAPIKey
	// continues to work.
	ErrMissingAPIKey = errors.New("required API key not set")
)

// NetworkMode specifies the sandbox's network access policy.
type NetworkMode string

const (
	NetworkModeDefault  NetworkMode = ""         // full network access
	NetworkModeNone     NetworkMode = "none"     // no network access
	NetworkModeIsolated NetworkMode = "isolated" // allowlist only
)

// DirMode is re-exported from store. The canonical type definition
// lives there because the persisted WorkdirMeta / DirMeta types hold
// Mode values; keeping the alias here means existing in-package
// callers (`Mode: DirModeCopy`, `m.Mode == DirModeRW`) continue to
// work without churn.
//
// :copy and :overlay are workdir-only (Q-U, 2026-05-25); aux
// directories accept only :rw and :ro (with :ro as the default
// when DirSpec.Mode is left zero).
type DirMode = store.DirMode

// Re-exported DirMode constants. Canonical definitions in
// internal/sandbox/store/dirmode.go.
const (
	DirModeCopy    = store.DirModeCopy
	DirModeOverlay = store.DirModeOverlay
	DirModeRW      = store.DirModeRW
	DirModeRO      = store.DirModeRO
)

// DirSpec describes a directory to mount in the sandbox. The canonical
// definition lives in the state leaf package (so create/mounts/lifecycle can
// share it without importing this façade); aliased here to keep the public
// sandbox.DirSpec name stable.
type DirSpec = state.DirSpec

// Options holds all parameters for sandbox creation.
type Options struct {
	Name         string
	Workdir      DirSpec               // primary working directory
	AuxDirs      []DirSpec             // auxiliary directories
	Agent        string                // agent name (e.g., "claude", "test")
	Model        string                // model name or alias (e.g., "sonnet", "claude-sonnet-4-latest")
	Profile      string                // profile name (from --profile flag)
	Prompt       string                // prompt text (from --prompt)
	PromptFile   string                // prompt file path (from --prompt-file)
	Network      NetworkMode           // network access policy
	NetworkAllow []string              // --network-allow flags
	Ports        []string              // --port flags (e.g., ["3000:3000"])
	Replace      bool                  // --replace flag (safe: errors if unapplied work exists)
	Force        bool                  // --force flag (unconditional replace, skips safety check)
	NoStart      bool                  // --no-start flag
	Passthrough  []string              // args after -- passed to agent
	Version      string                // yoloAI version for meta.json
	Debug        bool                  // --debug flag (enable entrypoint debug logging)
	CPUs         string                // --cpus flag (e.g., "4", "2.5")
	Memory       string                // --memory flag (e.g., "8g", "512m")
	Env          map[string]string     // --env flags (KEY=VAL pairs)
	Isolation    runtime.IsolationMode // --isolation flag (e.g., IsolationModeContainerEnhanced, IsolationModeVM)
	Runtimes     []string              // --runtime flags (Apple simulator runtimes, e.g., ["ios", "tvos:26.1"])
	VscodeTunnel bool                  // --vscode-tunnel flag
	Archetype    string                // --archetype flag (empty = auto-detect)

	// Output receives the create pipeline's human-readable progress (profile
	// image build stream, advisory warnings). Per-call so concurrent Creates on
	// the same Engine don't interleave on a shared writer. Nil falls back to
	// the Engine's output writer (the Client's Options.Output). F8.
	Output io.Writer
}

// outputFor resolves a create-pipeline progress writer: the per-call
// Options.Output when set, otherwise io.Discard. Never returns nil, so
// leaf writers can't panic on a nil io.Writer regardless of which create helper
// a caller enters through. The yoloai.Client seeds Options.Output from its
// Options.Output, so a nil here means a direct library caller opted out. F8.
func outputFor(o io.Writer) io.Writer {
	if o != nil {
		return o
	}
	return io.Discard
}

// CheckIsolationPrerequisites validates isolation prerequisites via RequiredCapabilities.
// Returns nil when all checks pass, or a formatted error listing missing prerequisites.
// Used by lifecycle.go when changing the isolation mode of an existing sandbox.
func CheckIsolationPrerequisites(ctx context.Context, rt runtime.Runtime, isolation runtime.IsolationMode) error {
	capList := runtime.RequiredCapabilitiesFor(rt, isolation)
	if len(capList) == 0 {
		return nil // backend has no requirements for this mode
	}
	env := caps.DetectEnvironment()
	results := caps.RunChecks(ctx, capList, env)
	return caps.FormatError(results)
}

// Run creates and optionally starts a new sandbox.
// Returns the sandbox name on success (empty on no-start).
// EnsureSetup is assumed to have already been called by the caller.
func Run(ctx context.Context, d state.Deps, opts Options) (name string, err error) {
	unlock, lockErr := store.AcquireLock(d.Layout, opts.Name)
	if lockErr != nil {
		return "", lockErr
	}
	defer func() {
		// On a failed Create that left no sandbox directory behind, the
		// lock file created at acquire-time is orphaned cruft — remove it
		// while we still hold the flock (safe: the flock is bound to our
		// open fd, not the path). On success, or when a directory remains
		// (e.g. a partially-replaced sandbox), the lock file is the
		// sandbox's legitimate companion and stays.
		if err != nil {
			if _, statErr := os.Stat(d.Layout.SandboxDir(opts.Name)); errors.Is(statErr, fs.ErrNotExist) {
				_ = store.RemoveLockFile(d.Layout, opts.Name)
			}
		}
		unlock()
	}()

	backend := d.Runtime.Descriptor().Name
	slog.Info("creating sandbox", "event", "sandbox.create", "sandbox", opts.Name, "agent", opts.Agent, "backend", backend)
	// When running as root under sudo, API key env vars (e.g. CLAUDE_CODE_OAUTH_TOKEN)
	// are stripped by sudo. Recover them from the parent process's environment into a
	// local map rather than mutating the process environment.
	credOverrides := provision.RecoverSudoCredentials()
	// Validate isolation prerequisites before the potentially expensive image build.
	if opts.Isolation != "" {
		if err := CheckIsolationPrerequisites(ctx, d.Runtime, opts.Isolation); err != nil {
			return "", err
		}
	}

	sandboxState, err := prepareSandboxState(ctx, d, opts, credOverrides)
	if err != nil {
		return "", err
	}

	if opts.NoStart {
		return "", nil
	}

	if err := launch.LaunchContainer(ctx, d, sandboxState); err != nil {
		// Clean up sandbox directory and attempt container removal.
		_ = os.RemoveAll(sandboxState.SandboxDir)
		_ = d.Runtime.Remove(ctx, store.InstanceName(sandboxState.Name))
		return "", err
	}

	// Execute VM-side work directory setup if baseline was deferred
	if sandboxState.Meta.Workdir.Mode == "copy" && sandboxState.Meta.Workdir.BaselineSHA == "" {
		if err := launch.ExecuteVMWorkDirSetup(ctx, d.Runtime, sandboxState.Name, sandboxState.SandboxDir, sandboxState.Meta); err != nil {
			// Clean up on failure
			_ = os.RemoveAll(sandboxState.SandboxDir)
			_ = d.Runtime.Remove(ctx, store.InstanceName(sandboxState.Name))
			return "", fmt.Errorf("execute VM work dir setup: %w", err)
		}
	}

	slog.Info("sandbox created", "event", "sandbox.create.complete", "sandbox", sandboxState.Name)
	return sandboxState.Name, nil
}

// checkUnappliedWork checks if the named sandbox has any unapplied work
// (uncommitted changes or commits beyond the baseline). Returns an error
// if work would be lost.
func checkUnappliedWork(name string, sandboxDir string) error {
	meta, err := store.LoadMeta(sandboxDir)
	if err != nil {
		return nil //nolint:nilerr // can't load meta — nothing to protect
	}

	if meta.Workdir.Mode == "copy" || meta.Workdir.Mode == "overlay" {
		workDir := store.WorkDir(sandboxDir, meta.Workdir.HostPath)
		if patch.HasUnappliedWork(workDir, meta.Workdir.BaselineSHA) {
			return fmt.Errorf("sandbox %q has unapplied changes (use --force to replace anyway, or 'yoloai apply' first)", name)
		}
	}

	for _, d := range meta.Directories {
		if d.Mode == "copy" || d.Mode == "overlay" {
			auxWorkDir := store.WorkDir(sandboxDir, d.HostPath)
			if patch.HasUnappliedWork(auxWorkDir, d.BaselineSHA) {
				return fmt.Errorf("sandbox %q has unapplied changes in %s (use --force to replace anyway, or 'yoloai apply' first)", name, d.HostPath)
			}
		}
	}

	return nil
}

// prepareSandboxState handles validation, safety checks, directory
// creation, workdir copy, git baseline, and meta/config writing.
func prepareSandboxState(ctx context.Context, d state.Deps, opts Options, credOverrides map[string]string) (*state.State, error) {
	agentDef, sandboxDir, ycfg, gcfg, err := validateAndLoadConfig(d, opts)
	if err != nil {
		return nil, err
	}

	// Phase 1: Resolve profile, runtime base, archetype, and mounts.
	pr, resolvedArchetype, devcontainerCfg, dcMounts, dcMountWarnings, mergedMounts, state_onCreateDone, err := resolveProfileAndArchetype(ctx, d, &opts, agentDef, ycfg, gcfg)
	if err != nil {
		return nil, err
	}

	if err := replaceSandboxIfNeeded(ctx, d, opts, sandboxDir); err != nil {
		return nil, err
	}

	workdir, auxDirs, err := parseAndValidateDirs(d, opts, agentDef, pr.env, ycfg.Model, credOverrides)
	if err != nil {
		return nil, err
	}

	// Phase 2: Create directory structure and seed sandbox.
	perms := state.Perms(pr.isolation)
	agentFilesInitialized, err := createAndSeedSandbox(ctx, d, sandboxDir, agentDef, pr, credOverrides, perms, outputFor(opts.Output))
	if err != nil {
		return nil, err
	}

	// Cleanup sandbox directory on failure
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(sandboxDir)
		}
	}()

	workCopyDir, baselineSHA, dirMetas, err := setupAllWorkdirs(d, opts, workdir, auxDirs, resolvedArchetype, devcontainerCfg)
	if err != nil {
		return nil, err
	}

	// Phase 3: Build config, meta, and state files.
	configData, meta, tmuxConf, promptText, err := buildConfigAndMeta(ctx, d, opts, pr, agentDef, workdir, auxDirs, gcfg, dirMetas, baselineSHA, mergedMounts, resolvedArchetype, devcontainerCfg, state_onCreateDone, sandboxDir)
	if err != nil {
		return nil, err
	}

	if err := writeStatFiles(sandboxDir, meta, agentDef, agentFilesInitialized, meta.HasPrompt, promptText, configData, perms); err != nil {
		return nil, err
	}

	success = true
	return buildSandboxStateResult(opts, sandboxDir, workdir, workCopyDir, auxDirs, agentDef, meta, pr, mergedMounts, configData, tmuxConf, resolvedArchetype, pr.archetypeDockerDRequired, devcontainerCfg, dcMounts, dcMountWarnings, credOverrides, d.Layout, d.Layout.HomeDir), nil
}

// resolveProfileAndArchetype resolves profile config, runtime base, archetype, mounts, and lifecycle state.
func resolveProfileAndArchetype(ctx context.Context, d state.Deps, opts *Options, agentDef *agent.Definition, ycfg *config.YoloaiConfig, gcfg *config.GlobalConfig) (*profileResult, archetype.Archetype, *archetype.DevcontainerConfig, []string, []string, []string, bool, error) {
	pr, err := resolveProfileConfig(ctx, d, opts, &agentDef, ycfg, gcfg)
	if err != nil {
		return nil, "", nil, nil, nil, nil, false, err
	}

	if err := resolveRuntimeBase(ctx, d, opts, pr); err != nil {
		return nil, "", nil, nil, nil, nil, false, err
	}

	if err := applyConfigDefaults(opts, ycfg, pr); err != nil {
		return nil, "", nil, nil, nil, nil, false, err
	}

	resolvedArchetype, devcontainerCfg, dcMounts, dcMountWarnings, err := resolveAndApplyArchetype(ctx, d, opts, pr)
	if err != nil {
		return nil, "", nil, nil, nil, nil, false, err
	}

	mergeDcMounts(pr, dcMounts)
	for _, w := range dcMountWarnings {
		fmt.Fprintln(outputFor(opts.Output), w) //nolint:errcheck // best-effort warning
	}

	state_onCreateDone := loadOnCreateDone(d.Layout.SandboxDir(opts.Name))

	mergedMounts, err := validateAndExpandMounts(pr.mounts, d.Layout.HomeDir, d.Layout.Env)
	if err != nil {
		return nil, "", nil, nil, nil, nil, false, err
	}

	return pr, resolvedArchetype, devcontainerCfg, dcMounts, dcMountWarnings, mergedMounts, state_onCreateDone, nil
}

// createAndSeedSandbox creates directory structure and seeds the sandbox with agent files.
func createAndSeedSandbox(ctx context.Context, d state.Deps, sandboxDir string, agentDef *agent.Definition, pr *profileResult, credOverrides map[string]string, perms state.IsolationPerms, output io.Writer) (bool, error) {
	_ = ctx // reserved for future use
	if err := createSandboxDirs(sandboxDir, perms); err != nil {
		return false, err
	}
	return provision.SeedSandbox(d.Runtime, agentDef, sandboxDir, pr.isolation, pr.agentFiles, credOverrides, d.Layout.HomeDir, d.Layout.Env, output)
}

// buildConfigAndMeta builds the container config and sandbox meta structs.
// Returns (configData, meta, tmuxConf, promptText, error).
func buildConfigAndMeta(ctx context.Context, d state.Deps, opts Options, pr *profileResult, agentDef *agent.Definition, workdir *DirSpec, auxDirs []*DirSpec, gcfg *config.GlobalConfig, dirMetas []store.DirMeta, baselineSHA string, mergedMounts []string, resolvedArchetype archetype.Archetype, devcontainerCfg *archetype.DevcontainerConfig, state_onCreateDone bool, sandboxDir string) ([]byte, *store.Meta, string, string, error) {
	_ = ctx // reserved for future use
	promptText, hasPrompt, model, agentCommand, tmuxConf, err := resolveAgentParams(agentDef, opts, pr, gcfg, d.Layout.HomeDir, d.Layout.Env, d.Input)
	if err != nil {
		return nil, nil, "", "", err
	}

	networkMode, networkAllow := buildNetworkConfig(opts, agentDef)
	slog.Debug("building runtime config", "event", "sandbox.create.config", "network_mode", networkMode)

	archetypeDockerDRequired := pr.archetypeDockerDRequired
	lifecycleCfg := buildLifecycleConfig(resolvedArchetype, archetypeDockerDRequired, state_onCreateDone, devcontainerCfg)

	backend := d.Runtime.Descriptor().Name
	configData, err := buildContainerConfig(d.Layout, agentDef, agentCommand, runtime.PrepareAgentCommandFor(d.Runtime, ""), tmuxConf, launch.OverlayOrResolvedMountPath(workdir), opts.Debug, networkMode == "isolated", networkAllow, opts.Passthrough, collectOverlayMounts(workdir, auxDirs), pr.setup, pr.autoCommitInterval, collectCopyDirs(workdir, auxDirs), opts.Name, d.Runtime.TmuxSocket(sandboxDir), pr.isolation, opts.VscodeTunnel, invocation.SanitizeTunnelName(opts.Name), lifecycleCfg)
	if err != nil {
		return nil, nil, "", "", fmt.Errorf("build %s: %w", store.RuntimeConfigFile, err)
	}

	usernsMode := resolveUsernsMode(d.Runtime, workdir, auxDirs, pr.capAdd)
	meta := buildMeta(opts, pr, workdir, baselineSHA, dirMetas, hasPrompt, networkMode, networkAllow, usernsMode, d.Runtime.Descriptor().Capabilities.HostFilesystem, string(resolvedArchetype), backend, model, mergedMounts)

	return configData, meta, tmuxConf, promptText, nil
}

// buildSandboxStateResult constructs the State from all resolved values.
func buildSandboxStateResult(opts Options, sandboxDir string, workdir *DirSpec, workCopyDir string, auxDirs []*DirSpec, agentDef *agent.Definition, meta *store.Meta, pr *profileResult, mergedMounts []string, configData []byte, tmuxConf string, resolvedArchetype archetype.Archetype, archetypeDockerDRequired bool, devcontainerCfg *archetype.DevcontainerConfig, dcMounts []string, dcMountWarnings []string, credOverrides map[string]string, layout config.Layout, homeDir string) *state.State {
	return &state.State{
		Name:                      opts.Name,
		SandboxDir:                sandboxDir,
		Workdir:                   workdir,
		WorkCopyDir:               workCopyDir,
		AuxDirs:                   auxDirs,
		Agent:                     agentDef,
		Model:                     meta.Model,
		Profile:                   pr.name,
		ImageRef:                  pr.imageRef,
		Env:                       pr.env,
		CredOverrides:             credOverrides,
		HasPrompt:                 meta.HasPrompt,
		NetworkMode:               meta.NetworkMode,
		NetworkAllow:              meta.NetworkAllow,
		Ports:                     opts.Ports,
		ConfigMounts:              mergedMounts,
		TmuxConf:                  tmuxConf,
		Resources:                 pr.resources,
		CapAdd:                    pr.capAdd,
		Devices:                   pr.devices,
		Setup:                     pr.setup,
		Isolation:                 pr.isolation,
		IsolationExplicit:         pr.isolationExplicit,
		VscodeTunnel:              opts.VscodeTunnel,
		Meta:                      meta,
		ConfigJSON:                configData,
		Archetype:                 resolvedArchetype,
		DockerdRequired:           archetypeDockerDRequired,
		Devcontainer:              devcontainerCfg,
		DevcontainerMounts:        dcMounts,
		DevcontainerMountWarnings: dcMountWarnings,
		WorkdirMode:               string(workdir.Mode),
		Layout:                    layout,
		HomeDir:                   homeDir,
		Output:                    opts.Output,
	}
}

// validateAndLoadConfig performs initial validation and loads config files.
func validateAndLoadConfig(d state.Deps, opts Options) (*agent.Definition, string, *config.YoloaiConfig, *config.GlobalConfig, error) {
	if err := store.ValidateName(opts.Name); err != nil {
		return nil, "", nil, nil, err
	}

	agentDef := agent.GetAgent(opts.Agent)
	if agentDef == nil {
		return nil, "", nil, nil, yoerrors.NewUsageError("unknown agent: %s", opts.Agent)
	}

	if opts.Force {
		opts.Replace = true
	}

	sandboxDir := d.Layout.SandboxDir(opts.Name)
	if _, err := os.Stat(sandboxDir); err == nil && !opts.Replace {
		if _, metaErr := store.LoadMeta(sandboxDir); metaErr != nil {
			_ = os.RemoveAll(sandboxDir)
		} else {
			return nil, "", nil, nil, fmt.Errorf("sandbox %q already exists (use --replace to recreate): %w", opts.Name, ErrSandboxExists)
		}
	}

	if opts.Prompt != "" && opts.PromptFile != "" {
		return nil, "", nil, nil, yoerrors.NewUsageError("--prompt and --prompt-file are mutually exclusive")
	}

	ycfg, err := config.LoadConfig(d.Layout)
	if err != nil {
		return nil, "", nil, nil, fmt.Errorf("load config: %w", err)
	}
	gcfg, err := config.LoadGlobalConfig(d.Layout)
	if err != nil {
		return nil, "", nil, nil, fmt.Errorf("load global config: %w", err)
	}

	return agentDef, sandboxDir, ycfg, gcfg, nil
}

// resolveRuntimeBase resolves an Apple simulator runtime base image when
// --runtime flags are provided. Dispatches via the AppleSimulatorRuntimes
// optional interface so sandbox/ doesn't import any concrete backend; only
// backends that opt in (currently Tart) handle the request.
func resolveRuntimeBase(ctx context.Context, d state.Deps, opts *Options, pr *profileResult) error {
	if len(opts.Runtimes) == 0 {
		return nil
	}
	asr, ok := d.Runtime.(runtime.AppleSimulatorRuntimes)
	if !ok {
		return yoerrors.NewUsageError("--runtime flag is only supported on backends that manage Apple simulator runtimes (currently: tart)")
	}
	imageRef, err := asr.PrepareRuntimeBase(ctx, d.Layout, opts.Runtimes)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(outputFor(opts.Output), "Using runtime base %s\n", imageRef)
	pr.imageRef = imageRef
	return nil
}

// mergeDcMounts merges devcontainer mounts into pr.mounts (dedup).
func mergeDcMounts(pr *profileResult, dcMounts []string) {
	seen := make(map[string]bool)
	for _, m := range pr.mounts {
		seen[m] = true
	}
	for _, m := range dcMounts {
		if !seen[m] {
			pr.mounts = append(pr.mounts, m)
			seen[m] = true
		}
	}
}

// loadOnCreateDone returns the on-create-done flag from sandbox state, defaulting to false.
func loadOnCreateDone(sandboxDir string) bool {
	existingState, err := store.LoadSandboxState(sandboxDir)
	if err != nil {
		return false
	}
	return existingState.OnCreateCommandsDone
}

// replaceSandboxIfNeeded destroys the existing sandbox if --replace is set.
func replaceSandboxIfNeeded(ctx context.Context, d state.Deps, opts Options, sandboxDir string) error {
	if !opts.Replace {
		return nil
	}
	if _, err := os.Stat(sandboxDir); os.IsNotExist(err) {
		return nil // nothing to replace
	}
	if !opts.Force {
		if err := checkUnappliedWork(opts.Name, sandboxDir); err != nil {
			return err
		}
	}
	if _, err := launch.Teardown(ctx, d, opts.Name); err != nil {
		return fmt.Errorf("replace existing sandbox: %w", err)
	}
	return nil
}

// createSandboxDirs creates the directory structure for a new sandbox.
func createSandboxDirs(sandboxDir string, perms state.IsolationPerms) error {
	for _, dir := range []string{
		sandboxDir,
		filepath.Join(sandboxDir, "home-seed"),
		filepath.Join(sandboxDir, store.BinDir),
		filepath.Join(sandboxDir, store.TmuxDir),
		filepath.Join(sandboxDir, store.BackendDir),
	} {
		if err := fileutil.MkdirAll(dir, 0750); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	}
	for _, dir := range []string{
		filepath.Join(sandboxDir, "work"),
		filepath.Join(sandboxDir, store.AgentRuntimeDir),
		filepath.Join(sandboxDir, "files"),
		filepath.Join(sandboxDir, "cache"),
	} {
		if err := fileutil.MkdirAllPerm(dir, perms.Dir); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	}
	return nil
}

// setupAllWorkdirs sets up the workdir and aux dirs, and resolves copy mount paths.
func setupAllWorkdirs(d state.Deps, opts Options, workdir *DirSpec, auxDirs []*DirSpec, resolvedArchetype archetype.Archetype, devcontainerCfg *archetype.DevcontainerConfig) (string, string, []store.DirMeta, error) {
	slog.Debug("setting up workdir", "event", "sandbox.create.workdir", "mode", string(workdir.Mode))
	sandboxDir := d.Layout.SandboxDir(opts.Name)
	workCopyDir, baselineSHA, err := setupWorkdir(sandboxDir, workdir, d.Runtime)
	if err != nil {
		return "", "", nil, err
	}

	// VS Code workspace injection (devcontainer + vscode-tunnel + copy/overlay).
	if resolvedArchetype == archetype.ArchetypeDevcontainer && opts.VscodeTunnel &&
		workdir.Mode != "rw" && devcontainerCfg != nil {
		if injectErr := archetype.InjectVSCodeWorkspace(workCopyDir, devcontainerCfg); injectErr != nil {
			slog.Warn("vscode workspace injection failed", "err", injectErr) // non-fatal
		}
	}

	slog.Debug("setting up aux dirs", "event", "sandbox.create.aux_dirs", "count", len(auxDirs))
	dirMetas, err := setupAuxDirs(sandboxDir, auxDirs)
	if err != nil {
		return "", "", nil, err
	}

	// For backends that run agents directly on the host (seatbelt), :copy mount paths
	// must point to the sandbox copy location rather than the original host path.
	if workdir.Mode == "copy" && workdir.MountPath == "" {
		workdir.MountPath = runtime.ResolveCopyMountFor(d.Runtime, opts.Name, workdir.Path)
	}
	for _, ad := range auxDirs {
		if ad.Mode == "copy" && ad.MountPath == "" {
			ad.MountPath = runtime.ResolveCopyMountFor(d.Runtime, opts.Name, ad.Path)
		}
	}

	return workCopyDir, baselineSHA, dirMetas, nil
}

// resolveAgentParams resolves prompt, model, agent command, and tmux config.
// homeDir is used to expand leading "~" in the promptFile path.
// env is the environment map for ${VAR} expansion; use layout.Env.
func resolveAgentParams(agentDef *agent.Definition, opts Options, pr *profileResult, gcfg *config.GlobalConfig, homeDir string, env map[string]string, stdin io.Reader) (string, bool, string, string, string, error) {
	promptText, err := invocation.ReadPrompt(opts.Prompt, opts.PromptFile, homeDir, env, stdin)
	if err != nil {
		return "", false, "", "", "", err
	}
	hasPrompt := promptText != ""

	model := invocation.ResolveModel(agentDef, opts.Model, pr.userAliases)
	model = invocation.ApplyModelPrefix(agentDef, model, pr.env)
	if err := invocation.ValidateModel(agentDef, model, opts.Model); err != nil {
		return "", false, "", "", "", err
	}

	agentArgs := pr.agentArgs[opts.Agent]
	agentCommand := invocation.BuildAgentCommand(agentDef, model, promptText, agentArgs, opts.Passthrough)

	tmuxConf := gcfg.TmuxConf
	if tmuxConf == "" {
		tmuxConf = "default"
	}

	return promptText, hasPrompt, model, agentCommand, tmuxConf, nil
}

// buildLifecycleConfig builds the lifecycle config if the archetype requires it.
func buildLifecycleConfig(resolvedArchetype archetype.Archetype, archetypeDockerDRequired bool, onCreateDone bool, devcontainerCfg *archetype.DevcontainerConfig) *runtimeconfig.LifecycleConfig {
	if resolvedArchetype != archetype.ArchetypeDevcontainer && !archetypeDockerDRequired {
		return nil
	}
	lc := &runtimeconfig.LifecycleConfig{
		DockerDRequired: archetypeDockerDRequired,
		OnCreateDone:    onCreateDone,
	}
	if devcontainerCfg != nil {
		if !devcontainerCfg.OnCreateCommand.IsZero() {
			lc.OnCreate = append(lc.OnCreate, lifecycleCmdToJSON(devcontainerCfg.OnCreateCommand))
		}
		if !devcontainerCfg.UpdateContentCommand.IsZero() {
			lc.OnCreate = append(lc.OnCreate, lifecycleCmdToJSON(devcontainerCfg.UpdateContentCommand))
		}
		if !devcontainerCfg.PostCreateCommand.IsZero() {
			lc.OnCreate = append(lc.OnCreate, lifecycleCmdToJSON(devcontainerCfg.PostCreateCommand))
		}
		if !devcontainerCfg.PostStartCommand.IsZero() {
			lc.OnStart = append(lc.OnStart, lifecycleCmdToJSON(devcontainerCfg.PostStartCommand))
		}
	}
	return lc
}

// resolveUsernsMode determines the effective user namespace mode for the runtime.
func resolveUsernsMode(rt runtime.Runtime, workdir *DirSpec, auxDirs []*DirSpec, capAdd []string) string {
	up, ok := rt.(runtime.UsernsProvider)
	if !ok {
		return ""
	}
	hasSysAdmin := workdir.Mode == "overlay"
	for _, ad := range auxDirs {
		if ad.Mode == "overlay" {
			hasSysAdmin = true
			break
		}
	}
	if !hasSysAdmin {
		if slices.Contains(capAdd, "SYS_ADMIN") {
			hasSysAdmin = true
		}
	}
	return up.UsernsMode(hasSysAdmin)
}

// buildMeta constructs the Meta struct for a new sandbox.
func buildMeta(opts Options, pr *profileResult, workdir *DirSpec, baselineSHA string, dirMetas []store.DirMeta, hasPrompt bool, networkMode string, networkAllow []string, usernsMode string, hostFilesystem bool, archetypeStr string, backend runtime.BackendName, model string, mergedMounts []string) *store.Meta {
	return &store.Meta{
		YoloaiVersion: opts.Version,
		Name:          opts.Name,
		CreatedAt:     time.Now(),
		Backend:       backend,
		Profile:       pr.name,
		ImageRef:      pr.imageRef,
		Agent:         agent.AgentName(opts.Agent),
		Model:         model,
		Workdir: store.WorkdirMeta{
			HostPath:     workdir.Path,
			MountPath:    launch.OverlayOrResolvedMountPath(workdir),
			Mode:         workdir.Mode,
			BaselineSHA:  baselineSHA,
			InceptionSHA: baselineSHA,
		},
		Directories:        dirMetas,
		HasPrompt:          hasPrompt,
		NetworkMode:        networkMode,
		NetworkAllow:       networkAllow,
		Ports:              opts.Ports,
		Resources:          pr.resources,
		Mounts:             mergedMounts,
		CapAdd:             pr.capAdd,
		Devices:            pr.devices,
		Setup:              pr.setup,
		AutoCommitInterval: pr.autoCommitInterval,
		Debug:              opts.Debug,
		UsernsMode:         usernsMode,
		Isolation:          pr.isolation,
		HostFilesystem:     hostFilesystem,
		VscodeTunnel:       opts.VscodeTunnel,
		Archetype:          archetypeStr,
	}
}

// writeStatFiles writes all state files for the new sandbox (meta, sandbox-state,
// prompt, logs, agent-status, runtime-config, context).
func writeStatFiles(sandboxDir string, meta *store.Meta, agentDef *agent.Definition, agentFilesInitialized bool, hasPrompt bool, promptText string, configData []byte, perms state.IsolationPerms) error {
	if err := store.SaveMeta(sandboxDir, meta); err != nil {
		return err
	}
	if err := store.SaveSandboxState(sandboxDir, &store.SandboxState{
		AgentFilesInitialized: agentFilesInitialized,
	}); err != nil {
		return err
	}
	if hasPrompt {
		if err := fileutil.WriteFile(filepath.Join(sandboxDir, "prompt.txt"), []byte(promptText), 0600); err != nil {
			return fmt.Errorf("write prompt.txt: %w", err)
		}
	}

	configPerm := os.FileMode(0644) // always 0644 (no secrets, read-only in container)

	if err := fileutil.MkdirAllPerm(filepath.Join(sandboxDir, store.LogsDir), perms.Dir); err != nil {
		return fmt.Errorf("create logs dir: %w", err)
	}
	for _, logFile := range []string{store.SandboxJSONLFile, store.MonitorJSONLFile, store.HooksJSONLFile} {
		p := filepath.Join(sandboxDir, logFile)
		if err := fileutil.WriteFilePerm(p, nil, perms.File); err != nil {
			return fmt.Errorf("create log file %s: %w", logFile, err)
		}
	}
	if err := fileutil.WriteFilePerm(filepath.Join(sandboxDir, store.AgentStatusFile), []byte("{}\n"), perms.File); err != nil {
		return fmt.Errorf("write %s: %w", store.AgentStatusFile, err)
	}
	if err := fileutil.WriteFilePerm(filepath.Join(sandboxDir, store.RuntimeConfigFile), configData, configPerm); err != nil {
		return fmt.Errorf("write %s: %w", store.RuntimeConfigFile, err)
	}
	if err := WriteContextFiles(sandboxDir, meta, agentDef); err != nil {
		return fmt.Errorf("write context files: %w", err)
	}
	return nil
}

// buildContainerConfig creates the config.json content.
// agentLaunchPrefix is the backend-specific wrap prefix that PrepareAgentCommand
// would prepend (e.g. a 'PATH="..." ' prefix for Tart);
// computed once by the caller, stored here as single source of truth for the
// agent-command wrap (W1a of the architecture remediation plan).
func buildContainerConfig(layout config.Layout, agentDef *agent.Definition, agentCommand string, agentLaunchPrefix string, tmuxConf string, workingDir string, debug bool, networkIsolated bool, allowedDomains []string, passthrough []string, overlayMounts []runtimeconfig.OverlayMountConfig, setupCommands []string, autoCommitInterval int, copyDirs []string, sandboxName string, tmuxSocket string, isolation runtime.IsolationMode, vscodeTunnel bool, vscodeTunnelName string, lifecycle *runtimeconfig.LifecycleConfig) ([]byte, error) {
	var stateDirName string
	if agentDef.StateDir != "" {
		stateDirName = filepath.Base(agentDef.StateDir)
	}

	cfg := runtimeconfig.ContainerConfig{
		SchemaVersion:      runtimeconfig.SchemaVersion,
		HostUID:            layout.HostUID,
		HostGID:            layout.HostGID,
		AgentCommand:       agentCommand,
		AgentLaunchPrefix:  agentLaunchPrefix,
		UseLaunchPrefix:    true,
		StartupDelay:       int(agentDef.StartupDelay / time.Millisecond),
		ReadyPattern:       agentDef.Idle.ReadyPattern,
		SubmitSequence:     agentDef.SubmitSequence,
		TmuxConf:           tmuxConf,
		WorkingDir:         workingDir,
		StateDirName:       stateDirName,
		Debug:              debug,
		NetworkIsolated:    networkIsolated,
		AllowedDomains:     allowedDomains,
		Passthrough:        passthrough,
		OverlayMounts:      overlayMounts,
		SetupCommands:      setupCommands,
		AutoCommitInterval: autoCommitInterval,
		CopyDirs:           copyDirs,
		HookIdle:           agentDef.Idle.Hook,
		Idle:               agentDef.Idle,
		Detectors:          invocation.ResolveDetectors(agentDef.Idle),
		SandboxName:        sandboxName,
		TmuxSocket:         tmuxSocket,
		Isolation:          isolation,
		VscodeTunnel:       vscodeTunnel,
		VscodeTunnelName:   vscodeTunnelName,
		Lifecycle:          lifecycle,
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// lifecycleCmdToJSON converts a LifecycleCmd to the runtime-config.json representation.
func lifecycleCmdToJSON(lc archetype.LifecycleCmd) map[string]any {
	if lc.IsZero() {
		return nil
	}
	switch v := lc.Raw().(type) {
	case string:
		return map[string]any{"type": "string", "cmd": v}
	case []string:
		return map[string]any{"type": "array", "cmd": v}
	case map[string]any:
		return map[string]any{"type": "object", "cmd": v}
	default:
		return nil
	}
}

// containsLocalhost returns true if the URL string references localhost or 127.0.0.1.
func containsLocalhost(url string) bool {
	return strings.Contains(url, "localhost") || strings.Contains(url, "127.0.0.1")
}

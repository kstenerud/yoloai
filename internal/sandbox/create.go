// ABOUTME: Low-level sandbox create helpers: mkdirAllPerm, machine-id generation,
// ABOUTME: and directory/file write utilities used by the create pipeline.
package sandbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/runtime/caps"
	"github.com/kstenerud/yoloai/internal/sandbox/archetype"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
)

// mkdirAllPerm creates a directory (and parents) then explicitly chmods it to
// bypass the process umask. Use this when the directory will be bind-mounted
// into a container that may run under a different uid (e.g. gVisor).
func mkdirAllPerm(path string, perm os.FileMode) error {
	if err := fileutil.MkdirAll(path, perm); err != nil {
		return err
	}
	return os.Chmod(path, perm) //nolint:gosec // G302: caller is responsible for choosing the perm
}

// writeFilePerm writes data to a file then explicitly chmods it to bypass the
// process umask. Use this when the file will be bind-mounted into a container
// that may run under a different uid (e.g. gVisor).
func writeFilePerm(path string, data []byte, perm os.FileMode) error {
	if err := fileutil.WriteFile(path, data, perm); err != nil { //nolint:gosec // G703: path is always a trusted sandbox subpath
		return err
	}
	return os.Chmod(path, perm) //nolint:gosec // G302: caller is responsible for choosing the perm
}

// ensureMachineID creates a stable machine-id file at path if it doesn't exist.
// The ID is a random 32-character lowercase hex string (same format as Linux
// /etc/machine-id) followed by a newline. It is bind-mounted read-only at
// /etc/machine-id in the container so that VS Code CLI sees a consistent machine
// fingerprint across container restarts and does not invalidate stored tokens.
func ensureMachineID(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	return writeFilePerm(path, []byte(hex.EncodeToString(b)+"\n"), 0444)
}

// NetworkMode specifies the sandbox's network access policy.
type NetworkMode string

const (
	NetworkModeDefault  NetworkMode = ""         // full network access
	NetworkModeNone     NetworkMode = "none"     // no network access
	NetworkModeIsolated NetworkMode = "isolated" // allowlist only
)

// checkIsolationPrerequisites validates isolation prerequisites via RequiredCapabilities.
// Returns nil when all checks pass, or a formatted error listing missing prerequisites.
func checkIsolationPrerequisites(ctx context.Context, rt runtime.Runtime, isolation runtime.IsolationMode) error {
	capList := runtime.RequiredCapabilitiesFor(rt, isolation)
	if len(capList) == 0 {
		return nil // backend has no requirements for this mode
	}
	env := caps.DetectEnvironment()
	results := caps.RunChecks(ctx, capList, env)
	return caps.FormatError(results)
}

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

// DirSpec describes a directory to mount in the sandbox.
// Use this instead of raw ":copy"/":rw" string syntax.
type DirSpec struct {
	Path               string  // absolute host path; required
	Mode               DirMode // mount mode; required for workdir
	MountPath          string  // custom container mount path; empty = mirror host path
	AllowDirty         bool    // proceed even if this directory has uncommitted git changes
	AllowDangerousPath bool    // mount even if this is a dangerous path (e.g. $HOME); the :force suffix
}

// CreateOptions holds all parameters for sandbox creation.
type CreateOptions struct {
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
	// the same Manager don't interleave on a shared writer. Nil falls back to
	// the Manager's output writer (the Client's Options.Output). F8.
	Output io.Writer
}

// sandboxState holds resolved state computed during preparation.
type sandboxState struct {
	name              string
	sandboxDir        string
	workdir           *DirSpec
	workCopyDir       string
	auxDirs           []*DirSpec
	agent             *agent.Definition
	model             string
	profile           string
	imageRef          string
	env               map[string]string // merged env (base + profile chain)
	credOverrides     map[string]string // sudo-recovered credential defaults (keys absent from os.Environ)
	hasPrompt         bool
	promptSourcePath  string // overrides default prompt.txt path for /yoloai/prompt.txt mount
	networkMode       string
	networkAllow      []string
	ports             []string
	configMounts      []string // extra bind mounts from config/profile (host:container[:ro])
	tmuxConf          string
	resources         *config.ResourceLimits
	capAdd            []string              // Linux capabilities from config/profile
	devices           []string              // host devices from config/profile
	setup             []string              // setup commands from config/profile
	isolation         runtime.IsolationMode // isolation mode from config/profile
	isolationExplicit bool                  // true when isolation was set via --isolation flag
	vscodeTunnel      bool                  // true when VS Code Remote Tunnel is enabled
	meta              *store.Meta
	configJSON        []byte
	// Archetype fields
	archetype                 archetype.Archetype
	dockerdRequired           bool
	devcontainer              *archetype.DevcontainerConfig
	devcontainerMounts        []string
	devcontainerMountWarnings []string
	workdirMode               string        // resolved workdir mode ("copy", "overlay", "rw")
	layout                    config.Layout // Q-W.3: DataDir-rooted Layout propagated from the Manager
	homeDir                   string        // Q-W.6: host home dir (layout.HomeDir); used for ~ expansion
	output                    io.Writer     // create-pipeline progress writer (CreateOptions.Output); F8
}

// overlayMountConfig describes a single overlay mount for config.json.
type overlayMountConfig struct {
	Lower  string `json:"lower"`
	Upper  string `json:"upper"`
	Work   string `json:"work"`
	Merged string `json:"merged"`
}

// lifecycleConfig describes lifecycle command execution for a sandbox.
type lifecycleConfig struct {
	DockerDRequired bool             `json:"dockerd_required"`
	OnCreateDone    bool             `json:"on_create_done"`
	OnCreate        []map[string]any `json:"on_create,omitempty"`
	OnStart         []map[string]any `json:"on_start,omitempty"`
}

// runtimeConfigSchemaVersion is the contract version between Go (writer) and
// Python (reader, via sandbox-setup.py and status-monitor.py) for
// runtime-config.json. Bump when adding a required field, removing a field,
// renaming, or changing the semantics of any field. Additive changes (new
// optional fields with sensible defaults on both sides) do NOT require a bump.
// W2 of the architecture remediation plan.
const runtimeConfigSchemaVersion = 1

// containerConfig is the serializable form of runtime-config.json.
type containerConfig struct {
	SchemaVersion      int                   `json:"schema_version"`
	HostUID            int                   `json:"host_uid"`
	HostGID            int                   `json:"host_gid"`
	AgentCommand       string                `json:"agent_command"`
	AgentLaunchPrefix  string                `json:"agent_launch_prefix"`
	UseLaunchPrefix    bool                  `json:"use_launch_prefix"`
	StartupDelay       int                   `json:"startup_delay"`
	ReadyPattern       string                `json:"ready_pattern"`
	SubmitSequence     string                `json:"submit_sequence"`
	TmuxConf           string                `json:"tmux_conf"`
	WorkingDir         string                `json:"working_dir"`
	StateDirName       string                `json:"state_dir_name"`
	Debug              bool                  `json:"debug,omitempty"`
	NetworkIsolated    bool                  `json:"network_isolated,omitempty"`
	AllowedDomains     []string              `json:"allowed_domains,omitempty"`
	Passthrough        []string              `json:"passthrough,omitempty"`
	OverlayMounts      []overlayMountConfig  `json:"overlay_mounts,omitempty"`
	SetupCommands      []string              `json:"setup_commands,omitempty"`
	AutoCommitInterval int                   `json:"auto_commit_interval,omitempty"`
	CopyDirs           []string              `json:"copy_dirs,omitempty"`
	HookIdle           bool                  `json:"hook_idle,omitempty"`
	Idle               agent.IdleSupport     `json:"idle"`
	Detectors          []string              `json:"detectors,omitempty"`
	SandboxName        string                `json:"sandbox_name"`
	TmuxSocket         string                `json:"tmux_socket,omitempty"`
	Isolation          runtime.IsolationMode `json:"isolation,omitempty"`
	VscodeTunnel       bool                  `json:"vscode_tunnel,omitempty"`
	VscodeTunnelName   string                `json:"vscode_tunnel_name,omitempty"`
	Lifecycle          *lifecycleConfig      `json:"lifecycle,omitempty"`
}

// outputFor resolves a create-pipeline progress writer: the per-call
// CreateOptions.Output when set, otherwise io.Discard. Never returns nil, so
// leaf writers can't panic on a nil io.Writer regardless of which create helper
// a caller enters through. The yoloai.Client seeds CreateOptions.Output from its
// Options.Output, so a nil here means a direct library caller opted out. F8.
func (m *Manager) outputFor(o io.Writer) io.Writer {
	if o != nil {
		return o
	}
	return io.Discard
}

// Create creates and optionally starts a new sandbox.
// Returns the sandbox name on success (empty on no-start).
func (m *Manager) Create(ctx context.Context, opts CreateOptions) (name string, err error) {
	unlock, lockErr := store.AcquireLock(m.layout, opts.Name)
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
			if _, statErr := os.Stat(m.layout.SandboxDir(opts.Name)); errors.Is(statErr, fs.ErrNotExist) {
				_ = store.RemoveLockFile(m.layout, opts.Name)
			}
		}
		unlock()
	}()

	slog.Info("creating sandbox", "event", "sandbox.create", "sandbox", opts.Name, "agent", opts.Agent, "backend", m.backend)
	// When running as root under sudo, API key env vars (e.g. CLAUDE_CODE_OAUTH_TOKEN)
	// are stripped by sudo. Recover them from the parent process's environment into a
	// local map rather than mutating the process environment.
	credOverrides := recoverSudoCredentials()
	// Validate isolation prerequisites before the potentially expensive image build.
	if opts.Isolation != "" {
		if err := checkIsolationPrerequisites(ctx, m.runtime, opts.Isolation); err != nil {
			return "", err
		}
	}
	if err := m.EnsureSetup(ctx, m.outputFor(opts.Output)); err != nil {
		return "", err
	}

	state, err := m.prepareSandboxState(ctx, opts, credOverrides)
	if err != nil {
		return "", err
	}

	if opts.NoStart {
		return "", nil
	}

	if err := m.launchContainer(ctx, state); err != nil {
		// Clean up sandbox directory and attempt container removal.
		_ = os.RemoveAll(state.sandboxDir)
		_ = m.runtime.Remove(ctx, store.InstanceName(state.name))
		return "", err
	}

	// Execute VM-side work directory setup if baseline was deferred
	if state.meta.Workdir.Mode == "copy" && state.meta.Workdir.BaselineSHA == "" {
		if err := executeVMWorkDirSetup(ctx, m.runtime, state.name, state.sandboxDir, state.meta); err != nil {
			// Clean up on failure
			_ = os.RemoveAll(state.sandboxDir)
			_ = m.runtime.Remove(ctx, store.InstanceName(state.name))
			return "", fmt.Errorf("execute VM work dir setup: %w", err)
		}
	}

	slog.Info("sandbox created", "event", "sandbox.create.complete", "sandbox", state.name)
	return state.name, nil
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
		if hasUnappliedWork(workDir, meta.Workdir.BaselineSHA) {
			return fmt.Errorf("sandbox %q has unapplied changes (use --force to replace anyway, or 'yoloai apply' first)", name)
		}
	}

	for _, d := range meta.Directories {
		if d.Mode == "copy" || d.Mode == "overlay" {
			auxWorkDir := store.WorkDir(sandboxDir, d.HostPath)
			if hasUnappliedWork(auxWorkDir, d.BaselineSHA) {
				return fmt.Errorf("sandbox %q has unapplied changes in %s (use --force to replace anyway, or 'yoloai apply' first)", name, d.HostPath)
			}
		}
	}

	return nil
}

// prepareSandboxState handles validation, safety checks, directory
// creation, workdir copy, git baseline, and meta/config writing.
func (m *Manager) prepareSandboxState(ctx context.Context, opts CreateOptions, credOverrides map[string]string) (*sandboxState, error) {
	agentDef, sandboxDir, ycfg, gcfg, err := m.validateAndLoadConfig(opts)
	if err != nil {
		return nil, err
	}

	// Phase 1: Resolve profile, runtime base, archetype, and mounts.
	pr, resolvedArchetype, devcontainerCfg, dcMounts, dcMountWarnings, mergedMounts, state_onCreateDone, err := m.resolveProfileAndArchetype(ctx, &opts, agentDef, ycfg, gcfg)
	if err != nil {
		return nil, err
	}

	if err := m.replaceSandboxIfNeeded(ctx, opts, sandboxDir); err != nil {
		return nil, err
	}

	workdir, auxDirs, err := m.parseAndValidateDirs(opts, agentDef, pr.env, ycfg.Model, credOverrides)
	if err != nil {
		return nil, err
	}

	// Phase 2: Create directory structure and seed sandbox.
	perms := Perms(pr.isolation)
	agentFilesInitialized, err := m.createAndSeedSandbox(ctx, sandboxDir, agentDef, pr, credOverrides, perms, m.outputFor(opts.Output))
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

	workCopyDir, baselineSHA, dirMetas, err := m.setupAllWorkdirs(opts, workdir, auxDirs, resolvedArchetype, devcontainerCfg)
	if err != nil {
		return nil, err
	}

	// Phase 3: Build config, meta, and state files.
	configData, meta, tmuxConf, promptText, err := m.buildConfigAndMeta(ctx, opts, pr, agentDef, workdir, auxDirs, gcfg, dirMetas, baselineSHA, mergedMounts, resolvedArchetype, devcontainerCfg, state_onCreateDone, sandboxDir)
	if err != nil {
		return nil, err
	}

	if err := writeStatFiles(sandboxDir, meta, agentDef, agentFilesInitialized, meta.HasPrompt, promptText, configData, perms); err != nil {
		return nil, err
	}

	success = true
	return buildSandboxStateResult(opts, sandboxDir, workdir, workCopyDir, auxDirs, agentDef, meta, pr, mergedMounts, configData, tmuxConf, resolvedArchetype, pr.archetypeDockerDRequired, devcontainerCfg, dcMounts, dcMountWarnings, credOverrides, m.layout, m.layout.HomeDir), nil
}

// resolveProfileAndArchetype resolves profile config, runtime base, archetype, mounts, and lifecycle state.
func (m *Manager) resolveProfileAndArchetype(ctx context.Context, opts *CreateOptions, agentDef *agent.Definition, ycfg *config.YoloaiConfig, gcfg *config.GlobalConfig) (*profileResult, archetype.Archetype, *archetype.DevcontainerConfig, []string, []string, []string, bool, error) {
	pr, err := m.resolveProfileConfig(ctx, opts, &agentDef, ycfg, gcfg)
	if err != nil {
		return nil, "", nil, nil, nil, nil, false, err
	}

	if err := m.resolveRuntimeBase(ctx, opts, pr); err != nil {
		return nil, "", nil, nil, nil, nil, false, err
	}

	if err := applyConfigDefaults(opts, ycfg, pr); err != nil {
		return nil, "", nil, nil, nil, nil, false, err
	}

	resolvedArchetype, devcontainerCfg, dcMounts, dcMountWarnings, err := m.resolveAndApplyArchetype(ctx, opts, pr)
	if err != nil {
		return nil, "", nil, nil, nil, nil, false, err
	}

	mergeDcMounts(pr, dcMounts)
	for _, w := range dcMountWarnings {
		fmt.Fprintln(m.outputFor(opts.Output), w) //nolint:errcheck // best-effort warning
	}

	state_onCreateDone := loadOnCreateDone(m.layout.SandboxDir(opts.Name))

	mergedMounts, err := validateAndExpandMounts(pr.mounts, m.layout.HomeDir, m.layout.Env)
	if err != nil {
		return nil, "", nil, nil, nil, nil, false, err
	}

	return pr, resolvedArchetype, devcontainerCfg, dcMounts, dcMountWarnings, mergedMounts, state_onCreateDone, nil
}

// createAndSeedSandbox creates directory structure and seeds the sandbox with agent files.
func (m *Manager) createAndSeedSandbox(ctx context.Context, sandboxDir string, agentDef *agent.Definition, pr *profileResult, credOverrides map[string]string, perms IsolationPerms, output io.Writer) (bool, error) {
	_ = ctx // reserved for future use
	if err := createSandboxDirs(sandboxDir, perms); err != nil {
		return false, err
	}
	return m.seedSandbox(agentDef, sandboxDir, pr.isolation, pr.agentFiles, credOverrides, m.layout.HomeDir, output)
}

// buildConfigAndMeta builds the container config and sandbox meta structs.
// Returns (configData, meta, tmuxConf, promptText, error).
func (m *Manager) buildConfigAndMeta(ctx context.Context, opts CreateOptions, pr *profileResult, agentDef *agent.Definition, workdir *DirSpec, auxDirs []*DirSpec, gcfg *config.GlobalConfig, dirMetas []store.DirMeta, baselineSHA string, mergedMounts []string, resolvedArchetype archetype.Archetype, devcontainerCfg *archetype.DevcontainerConfig, state_onCreateDone bool, sandboxDir string) ([]byte, *store.Meta, string, string, error) {
	_ = ctx // reserved for future use
	promptText, hasPrompt, model, agentCommand, tmuxConf, err := resolveAgentParams(agentDef, opts, pr, gcfg, m.layout.HomeDir, m.layout.Env, m.input)
	if err != nil {
		return nil, nil, "", "", err
	}

	networkMode, networkAllow := buildNetworkConfig(opts, agentDef)
	slog.Debug("building runtime config", "event", "sandbox.create.config", "network_mode", networkMode)

	archetypeDockerDRequired := pr.archetypeDockerDRequired
	lifecycleCfg := buildLifecycleConfig(resolvedArchetype, archetypeDockerDRequired, state_onCreateDone, devcontainerCfg)

	configData, err := buildContainerConfig(m.layout, agentDef, agentCommand, runtime.PrepareAgentCommandFor(m.runtime, ""), tmuxConf, overlayOrResolvedMountPath(workdir), opts.Debug, networkMode == "isolated", networkAllow, opts.Passthrough, collectOverlayMounts(workdir, auxDirs), pr.setup, pr.autoCommitInterval, collectCopyDirs(workdir, auxDirs), opts.Name, m.runtime.TmuxSocket(sandboxDir), pr.isolation, opts.VscodeTunnel, sanitizeTunnelName(opts.Name), lifecycleCfg)
	if err != nil {
		return nil, nil, "", "", fmt.Errorf("build %s: %w", store.RuntimeConfigFile, err)
	}

	usernsMode := resolveUsernsMode(m.runtime, workdir, auxDirs, pr.capAdd)
	meta := buildMeta(opts, pr, workdir, baselineSHA, dirMetas, hasPrompt, networkMode, networkAllow, usernsMode, m.runtime.Descriptor().Capabilities.HostFilesystem, string(resolvedArchetype), m.backend, model, mergedMounts)

	return configData, meta, tmuxConf, promptText, nil
}

// buildSandboxStateResult constructs the sandboxState from all resolved values.
func buildSandboxStateResult(opts CreateOptions, sandboxDir string, workdir *DirSpec, workCopyDir string, auxDirs []*DirSpec, agentDef *agent.Definition, meta *store.Meta, pr *profileResult, mergedMounts []string, configData []byte, tmuxConf string, resolvedArchetype archetype.Archetype, archetypeDockerDRequired bool, devcontainerCfg *archetype.DevcontainerConfig, dcMounts []string, dcMountWarnings []string, credOverrides map[string]string, layout config.Layout, homeDir string) *sandboxState {
	return &sandboxState{
		name:                      opts.Name,
		sandboxDir:                sandboxDir,
		workdir:                   workdir,
		workCopyDir:               workCopyDir,
		auxDirs:                   auxDirs,
		agent:                     agentDef,
		model:                     meta.Model,
		profile:                   pr.name,
		imageRef:                  pr.imageRef,
		env:                       pr.env,
		credOverrides:             credOverrides,
		hasPrompt:                 meta.HasPrompt,
		networkMode:               meta.NetworkMode,
		networkAllow:              meta.NetworkAllow,
		ports:                     opts.Ports,
		configMounts:              mergedMounts,
		tmuxConf:                  tmuxConf,
		resources:                 pr.resources,
		capAdd:                    pr.capAdd,
		devices:                   pr.devices,
		setup:                     pr.setup,
		isolation:                 pr.isolation,
		isolationExplicit:         pr.isolationExplicit,
		vscodeTunnel:              opts.VscodeTunnel,
		meta:                      meta,
		configJSON:                configData,
		archetype:                 resolvedArchetype,
		dockerdRequired:           archetypeDockerDRequired,
		devcontainer:              devcontainerCfg,
		devcontainerMounts:        dcMounts,
		devcontainerMountWarnings: dcMountWarnings,
		workdirMode:               string(workdir.Mode),
		layout:                    layout,
		homeDir:                   homeDir,
		output:                    opts.Output,
	}
}

// validateAndLoadConfig performs initial validation and loads config files.
func (m *Manager) validateAndLoadConfig(opts CreateOptions) (*agent.Definition, string, *config.YoloaiConfig, *config.GlobalConfig, error) {
	if err := store.ValidateName(opts.Name); err != nil {
		return nil, "", nil, nil, err
	}

	agentDef := agent.GetAgent(opts.Agent)
	if agentDef == nil {
		return nil, "", nil, nil, NewUsageError("unknown agent: %s", opts.Agent)
	}

	if opts.Force {
		opts.Replace = true
	}

	sandboxDir := m.layout.SandboxDir(opts.Name)
	if _, err := os.Stat(sandboxDir); err == nil && !opts.Replace {
		if _, metaErr := store.LoadMeta(sandboxDir); metaErr != nil {
			_ = os.RemoveAll(sandboxDir)
		} else {
			return nil, "", nil, nil, fmt.Errorf("sandbox %q already exists (use --replace to recreate): %w", opts.Name, ErrSandboxExists)
		}
	}

	if opts.Prompt != "" && opts.PromptFile != "" {
		return nil, "", nil, nil, NewUsageError("--prompt and --prompt-file are mutually exclusive")
	}

	ycfg, err := config.LoadConfig(m.layout)
	if err != nil {
		return nil, "", nil, nil, fmt.Errorf("load config: %w", err)
	}
	gcfg, err := config.LoadGlobalConfig(m.layout)
	if err != nil {
		return nil, "", nil, nil, fmt.Errorf("load global config: %w", err)
	}

	return agentDef, sandboxDir, ycfg, gcfg, nil
}

// resolveRuntimeBase resolves an Apple simulator runtime base image when
// --runtime flags are provided. Dispatches via the AppleSimulatorRuntimes
// optional interface so sandbox/ doesn't import any concrete backend; only
// backends that opt in (currently Tart) handle the request.
func (m *Manager) resolveRuntimeBase(ctx context.Context, opts *CreateOptions, pr *profileResult) error {
	if len(opts.Runtimes) == 0 {
		return nil
	}
	asr, ok := m.runtime.(runtime.AppleSimulatorRuntimes)
	if !ok {
		return NewUsageError("--runtime flag is only supported on backends that manage Apple simulator runtimes (currently: tart)")
	}
	imageRef, err := asr.PrepareRuntimeBase(ctx, m.layout, opts.Runtimes)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(m.outputFor(opts.Output), "Using runtime base %s\n", imageRef)
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
func (m *Manager) replaceSandboxIfNeeded(ctx context.Context, opts CreateOptions, sandboxDir string) error {
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
	if _, err := m.destroy(ctx, opts.Name); err != nil {
		return fmt.Errorf("replace existing sandbox: %w", err)
	}
	return nil
}

// createSandboxDirs creates the directory structure for a new sandbox.
func createSandboxDirs(sandboxDir string, perms IsolationPerms) error {
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
		if err := mkdirAllPerm(dir, perms.Dir); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	}
	return nil
}

// setupAllWorkdirs sets up the workdir and aux dirs, and resolves copy mount paths.
func (m *Manager) setupAllWorkdirs(opts CreateOptions, workdir *DirSpec, auxDirs []*DirSpec, resolvedArchetype archetype.Archetype, devcontainerCfg *archetype.DevcontainerConfig) (string, string, []store.DirMeta, error) {
	slog.Debug("setting up workdir", "event", "sandbox.create.workdir", "mode", string(workdir.Mode))
	sandboxDir := m.layout.SandboxDir(opts.Name)
	workCopyDir, baselineSHA, err := setupWorkdir(sandboxDir, workdir, m.runtime)
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
		workdir.MountPath = runtime.ResolveCopyMountFor(m.runtime, opts.Name, workdir.Path)
	}
	for _, ad := range auxDirs {
		if ad.Mode == "copy" && ad.MountPath == "" {
			ad.MountPath = runtime.ResolveCopyMountFor(m.runtime, opts.Name, ad.Path)
		}
	}

	return workCopyDir, baselineSHA, dirMetas, nil
}

// resolveAgentParams resolves prompt, model, agent command, and tmux config.
// homeDir is used to expand leading "~" in the promptFile path.
// env is the environment map for ${VAR} expansion; use layout.Env.
func resolveAgentParams(agentDef *agent.Definition, opts CreateOptions, pr *profileResult, gcfg *config.GlobalConfig, homeDir string, env map[string]string, stdin io.Reader) (string, bool, string, string, string, error) {
	promptText, err := ReadPrompt(opts.Prompt, opts.PromptFile, homeDir, env, stdin)
	if err != nil {
		return "", false, "", "", "", err
	}
	hasPrompt := promptText != ""

	model := resolveModel(agentDef, opts.Model, pr.userAliases)
	model = applyModelPrefix(agentDef, model, pr.env)
	if err := validateModel(agentDef, model, opts.Model); err != nil {
		return "", false, "", "", "", err
	}

	agentArgs := pr.agentArgs[opts.Agent]
	agentCommand := buildAgentCommand(agentDef, model, promptText, agentArgs, opts.Passthrough)

	tmuxConf := gcfg.TmuxConf
	if tmuxConf == "" {
		tmuxConf = "default"
	}

	return promptText, hasPrompt, model, agentCommand, tmuxConf, nil
}

// buildLifecycleConfig builds the lifecycle config if the archetype requires it.
func buildLifecycleConfig(resolvedArchetype archetype.Archetype, archetypeDockerDRequired bool, onCreateDone bool, devcontainerCfg *archetype.DevcontainerConfig) *lifecycleConfig {
	if resolvedArchetype != archetype.ArchetypeDevcontainer && !archetypeDockerDRequired {
		return nil
	}
	lc := &lifecycleConfig{
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
func buildMeta(opts CreateOptions, pr *profileResult, workdir *DirSpec, baselineSHA string, dirMetas []store.DirMeta, hasPrompt bool, networkMode string, networkAllow []string, usernsMode string, hostFilesystem bool, archetypeStr string, backend runtime.BackendName, model string, mergedMounts []string) *store.Meta {
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
			MountPath:    overlayOrResolvedMountPath(workdir),
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
func writeStatFiles(sandboxDir string, meta *store.Meta, agentDef *agent.Definition, agentFilesInitialized bool, hasPrompt bool, promptText string, configData []byte, perms IsolationPerms) error {
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

	if err := mkdirAllPerm(filepath.Join(sandboxDir, store.LogsDir), perms.Dir); err != nil {
		return fmt.Errorf("create logs dir: %w", err)
	}
	for _, logFile := range []string{store.SandboxJSONLFile, store.MonitorJSONLFile, store.HooksJSONLFile} {
		p := filepath.Join(sandboxDir, logFile)
		if err := writeFilePerm(p, nil, perms.File); err != nil {
			return fmt.Errorf("create log file %s: %w", logFile, err)
		}
	}
	if err := writeFilePerm(filepath.Join(sandboxDir, store.AgentStatusFile), []byte("{}\n"), perms.File); err != nil {
		return fmt.Errorf("write %s: %w", store.AgentStatusFile, err)
	}
	if err := writeFilePerm(filepath.Join(sandboxDir, store.RuntimeConfigFile), configData, configPerm); err != nil {
		return fmt.Errorf("write %s: %w", store.RuntimeConfigFile, err)
	}
	if err := WriteContextFiles(sandboxDir, meta, agentDef); err != nil {
		return fmt.Errorf("write context files: %w", err)
	}
	return nil
}

// launchContainer creates a sandbox instance from sandboxState, starts it,
// and cleans up credential temp files. Used by both initial creation and
// recreation from meta.json.
func (m *Manager) launchContainer(ctx context.Context, state *sandboxState) error {
	slog.Info("launching container", "event", "sandbox.create.container.launch", "sandbox", state.name, "image", state.imageRef)
	// Use pre-merged env from state if available, otherwise load from config.
	envVars := state.env
	if envVars == nil {
		cfg, cfgErr := config.LoadConfig(m.layout)
		if cfgErr != nil {
			return fmt.Errorf("load config: %w", cfgErr)
		}
		envVars = cfg.Env
	}

	secretsDir, err := createSecretsDir(state.agent, envVars, state.isolation, state.credOverrides)
	if err != nil {
		return fmt.Errorf("create secrets: %w", err)
	}
	if secretsDir != "" {
		defer os.RemoveAll(secretsDir) //nolint:errcheck // best-effort cleanup
	}

	mounts := buildMounts(state, secretsDir)

	ports, err := parsePortBindings(state.ports)
	if err != nil {
		return err
	}
	ports = filterAvailablePorts(ports, m.outputFor(state.output))

	return m.buildAndStart(ctx, state, mounts, ports, secretsDir != "")
}

// resolveModel expands a model alias. User-configured aliases (from
// config.yaml model_aliases) take priority over agent built-in aliases.
func resolveModel(agentDef *agent.Definition, model string, userAliases map[string]string) string {
	if model == "" {
		return ""
	}
	if userAliases != nil {
		if resolved, ok := userAliases[model]; ok {
			return resolved
		}
	}
	if agentDef.ModelAliases != nil {
		if resolved, ok := agentDef.ModelAliases[model]; ok {
			return resolved
		}
	}
	return model
}

// applyModelPrefix adds a provider prefix to the model name when needed.
// For example, when using aider with OLLAMA_API_BASE, the model must be
// prefixed with "ollama_chat/" for litellm to route it correctly.
func applyModelPrefix(agentDef *agent.Definition, model string, configEnv map[string]string) string {
	if model == "" || strings.Contains(model, "/") {
		return model
	}
	if agentDef.ModelPrefixes == nil {
		return model
	}
	for envVar, prefix := range agentDef.ModelPrefixes {
		if os.Getenv(envVar) != "" || configEnv[envVar] != "" { //nolint:forbidigo // §12: agent credential/env presence check (declared API-key exception)
			return prefix + model
		}
	}
	return model
}

// validateModel checks agent-specific model format requirements.
// Returns an error if the model format is invalid for the given agent.
func validateModel(agentDef *agent.Definition, resolvedModel string, originalModel string) error {
	// Skip validation if no model specified
	if resolvedModel == "" {
		return nil
	}

	// OpenCode requires provider/model format (e.g., "openai/gpt-4o", "anthropic/claude-sonnet-4-20250514")
	if agentDef.Name == "opencode" {
		if !strings.Contains(resolvedModel, "/") {
			return fmt.Errorf(
				"opencode requires models in provider/model format (e.g., \"openai/gpt-4o\", \"anthropic/claude-sonnet-4-20250514\")\n\n"+
					"You specified: %q\n"+
					"Resolved to: %q\n\n"+
					"To fix this:\n"+
					"  1. Configure providers on your HOST (install opencode, run /connect)\n"+
					"     OR set API key env vars: export OPENAI_API_KEY=sk-...\n"+
					"  2. Use --model with provider prefix: --model openai/gpt-4o\n\n"+
					"Valid examples:\n"+
					"  openai/gpt-4o\n"+
					"  openai/gpt-4o-mini\n"+
					"  anthropic/claude-sonnet-4-20250514\n"+
					"  opencode/gpt-5.1-codex (OpenCode Zen)\n\n"+
					"Note: OpenCode config must be set up on your host machine.\n"+
					"yoloAI will automatically seed it into containers",
				originalModel,
				resolvedModel,
			)
		}
	}

	return nil
}

// buildAgentCommand constructs the full agent command string for config.json.
// Arg priority (left to right, last flag wins): base cmd → model flag → agentArgs → passthrough.
func buildAgentCommand(agentDef *agent.Definition, model string, prompt string, agentArgs string, passthrough []string) string {
	var cmd string

	if agentDef.PromptMode == agent.PromptModeHeadless && prompt != "" {
		escaped := shellEscapeForDoubleQuotes(prompt)
		cmd = strings.ReplaceAll(agentDef.HeadlessCmd, "PROMPT", escaped)
	} else {
		cmd = agentDef.InteractiveCmd
		if model != "" && agentDef.ModelFlag != "" {
			cmd += " " + agentDef.ModelFlag + " " + model
		}
	}

	if agentArgs != "" {
		cmd += " " + agentArgs
	}

	for _, arg := range passthrough {
		cmd += " " + arg
	}

	return cmd
}

// sanitizeTunnelName converts a sandbox name to a valid VS Code tunnel name.
// VS Code tunnel names are limited to 20 characters, lowercase alphanumeric
// and hyphens, with no leading or trailing hyphens.
func sanitizeTunnelName(name string) string {
	name = strings.ToLower(name)
	// Replace underscores and dots with hyphens (sandbox names allow both)
	name = strings.NewReplacer("_", "-", ".", "-").Replace(name)
	// Truncate to 20 chars
	if len(name) > 20 {
		name = name[:20]
	}
	// Strip trailing hyphens introduced by truncation
	name = strings.TrimRight(name, "-")
	// Ensure minimum 3 chars (pad with 'x' if needed)
	for len(name) < 3 {
		name += "x"
	}
	return name
}

// shellEscapeForDoubleQuotes escapes a string for embedding inside
// double quotes in a shell command.
func shellEscapeForDoubleQuotes(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"`", "\\`",
		`$`, `\$`,
	)
	return r.Replace(s)
}

// resolveDetectors computes the ordered detector stack based on the agent's
// idle support capabilities. The returned list is stored in config.json and
// used by the in-container status monitor to determine which detection
// strategies to run (in priority order).
func resolveDetectors(idle agent.IdleSupport) []string {
	var detectors []string

	// Hook detector: highest priority, agent writes status.json directly.
	if idle.Hook {
		detectors = append(detectors, "hook")
	}

	// Wchan detector: high confidence, works for any process-based agent.
	if idle.WchanApplicable {
		detectors = append(detectors, "wchan")
	}

	// Ready pattern: medium confidence, checks tmux pane for prompt text.
	if idle.ReadyPattern != "" {
		detectors = append(detectors, "ready_pattern")
	}

	// Context signal: medium confidence, checks for agent-emitted markers.
	if idle.ContextSignal {
		detectors = append(detectors, "context_signal")
	}

	// Output stability: low confidence fallback, always added when any
	// other detector exists (provides a last-resort signal).
	if len(detectors) > 0 {
		detectors = append(detectors, "output_stability")
	}

	return detectors
}

// buildContainerConfig creates the config.json content.
// agentLaunchPrefix is the backend-specific wrap prefix that PrepareAgentCommand
// would prepend (e.g. a 'PATH="..." ' prefix for Tart);
// computed once by the caller, stored here as single source of truth for the
// agent-command wrap (W1a of the architecture remediation plan).
func buildContainerConfig(layout config.Layout, agentDef *agent.Definition, agentCommand string, agentLaunchPrefix string, tmuxConf string, workingDir string, debug bool, networkIsolated bool, allowedDomains []string, passthrough []string, overlayMounts []overlayMountConfig, setupCommands []string, autoCommitInterval int, copyDirs []string, sandboxName string, tmuxSocket string, isolation runtime.IsolationMode, vscodeTunnel bool, vscodeTunnelName string, lifecycle *lifecycleConfig) ([]byte, error) {
	var stateDirName string
	if agentDef.StateDir != "" {
		stateDirName = filepath.Base(agentDef.StateDir)
	}

	cfg := containerConfig{
		SchemaVersion:      runtimeConfigSchemaVersion,
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
		Detectors:          resolveDetectors(agentDef.Idle),
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

// ReadPrompt reads the prompt from --prompt, --prompt-file, or stdin ("-").
// homeDir is used to expand leading "~" in the promptFile path. stdin is the
// reader the "-" sentinel pulls from — threaded from the Manager's input
// (the CLI wires os.Stdin there; embedders supply their own), so the library
// never reaches for the process's stdin directly (§12).
// env is the environment map for ${VAR} expansion; use layout.Env.
func ReadPrompt(prompt, promptFile, homeDir string, env map[string]string, stdin io.Reader) (string, error) {
	if prompt != "" && promptFile != "" {
		return "", NewUsageError("--prompt and --prompt-file are mutually exclusive")
	}

	if prompt == "-" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("read prompt from stdin: %w", err)
		}
		return strings.TrimSpace(string(data)), nil
	}

	if prompt != "" {
		return prompt, nil
	}

	if promptFile == "-" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("read prompt from stdin: %w", err)
		}
		return strings.TrimSpace(string(data)), nil
	}

	if promptFile != "" {
		promptFile, err := ExpandPath(promptFile, homeDir, env)
		if err != nil {
			return "", fmt.Errorf("expand prompt file path: %w", err)
		}
		data, err := os.ReadFile(promptFile) //nolint:gosec // G304: path is from user-provided --prompt-file flag
		if err != nil {
			return "", fmt.Errorf("read prompt file: %w", err)
		}
		return strings.TrimSpace(string(data)), nil
	}

	return "", nil
}

// parsePortBindings converts ["host:container", ...] to runtime port mappings.
// filterAvailablePorts removes any port mappings where the host port is already
// in use, printing a warning for each skipped entry. Best-effort: a TOCTOU race
// is possible but Docker's own error is the fallback for that case.
func filterAvailablePorts(ports []runtime.PortMapping, output io.Writer) []runtime.PortMapping {
	var available []runtime.PortMapping
	for _, p := range ports {
		l, err := net.Listen("tcp", fmt.Sprintf(":%d", p.HostPort))
		if err != nil {
			fmt.Fprintf(output, "Warning: skipping port %d:%d — host port %d is already in use\n", //nolint:errcheck // best-effort output
				p.HostPort, p.ContainerPort, p.HostPort)
			continue
		}
		_ = l.Close()
		available = append(available, p)
	}
	return available
}

func parsePortBindings(ports []string) ([]runtime.PortMapping, error) {
	if len(ports) == 0 {
		return nil, nil
	}

	var result []runtime.PortMapping
	for _, p := range ports {
		parts := strings.SplitN(p, ":", 2)
		if len(parts) != 2 {
			return nil, NewUsageError("invalid port format %q (expected host:container)", p)
		}
		hostPort, err := strconv.Atoi(parts[0])
		if err != nil {
			return nil, NewUsageError("invalid host port %q in mapping %q: %v", parts[0], p, err)
		}
		containerPort, err := strconv.Atoi(parts[1])
		if err != nil {
			return nil, NewUsageError("invalid container port %q in mapping %q: %v", parts[1], p, err)
		}
		result = append(result, runtime.PortMapping{
			HostPort:      hostPort,
			ContainerPort: containerPort,
			Protocol:      "tcp",
		})
	}

	return result, nil
}

// createSecretsDir creates a temp directory with one file per env var / API key.
// Env vars are written first; API keys overwrite on conflict (take precedence).
// credOverrides contains sudo-recovered credential defaults for keys absent from
// os.Environ; they are used as a fallback so that creation under sudo sees credentials.
// Returns empty string if nothing was written.
func createSecretsDir(agentDef *agent.Definition, envVars map[string]string, security runtime.IsolationMode, credOverrides map[string]string) (string, error) {
	if len(agentDef.APIKeyEnvVars) == 0 && len(agentDef.AuthHintEnvVars) == 0 && len(envVars) == 0 && len(credOverrides) == 0 {
		return "", nil
	}

	tmpDir, err := os.MkdirTemp("", "yoloai-secrets-*")
	if err != nil {
		return "", fmt.Errorf("create secrets temp dir: %w", err)
	}

	// Determine permissions based on security mode.
	// gVisor gofer runs as remapped uid and needs world-readable/executable.
	// Standard Docker can use restrictive permissions.
	// The dir lives in /tmp and is removed within seconds of container startup.
	perms := Perms(security)

	if err := os.Chmod(tmpDir, perms.SecretsDir); err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", fmt.Errorf("chmod secrets dir: %w", err)
	}
	// When running via sudo, chown the dir to the real user so the container
	// process (running as that user via --userns=keep-id) can read it.
	_ = fileutil.ChownIfSudo(tmpDir) //nolint:errcheck // best-effort; individual files are already chowned by writeFilePerm

	wrote := false

	// Write env vars first
	for k, v := range envVars {
		if err := writeFilePerm(filepath.Join(tmpDir, k), []byte(v), perms.SecretsFile); err != nil {
			_ = os.RemoveAll(tmpDir)
			return "", fmt.Errorf("write env %s: %w", k, err)
		}
		wrote = true
	}

	// Write host env vars for API keys and auth hints (overwrites config env on conflict).
	// credOverrides provides sudo-recovered values for keys absent from os.Environ.
	for _, key := range append(agentDef.APIKeyEnvVars, agentDef.AuthHintEnvVars...) {
		value := os.Getenv(key) //nolint:forbidigo // §12: agent API key / auth-hint value (declared exception)
		if value == "" {
			value = credOverrides[key]
		}
		if value == "" {
			continue
		}
		if err := writeFilePerm(filepath.Join(tmpDir, key), []byte(value), perms.SecretsFile); err != nil {
			_ = os.RemoveAll(tmpDir)
			return "", fmt.Errorf("write secret %s: %w", key, err)
		}
		wrote = true
	}

	if !wrote {
		_ = os.RemoveAll(tmpDir)
		return "", nil
	}

	return tmpDir, nil
}

// recoverSudoCredentials returns sudo-recovered credential env vars for keys
// absent from the current process environment. Under `sudo` (without -E) the
// API-key / OAuth env vars are stripped from os.Environ; recovering them from
// the parent sudo process lets both `new` (Create) and `restart`
// (recreateContainer) inject them. Keys present in os.Environ are skipped so a
// real host value always wins.
func recoverSudoCredentials() map[string]string {
	overrides := make(map[string]string)
	for k, v := range sudoParentEnv() {
		if os.Getenv(k) == "" { //nolint:forbidigo // §12: sudo credential recovery — only override keys absent from the live env
			overrides[k] = v
		}
	}
	return overrides
}

// sudoParentEnv returns env vars from the parent sudo process when yoloai is
// run via sudo. sudo strips most env vars before exec'ing the child, but the
// sudo process itself inherits the full user environment. Reading the parent's
// /proc/<ppid>/environ recovers vars like CLAUDE_CODE_OAUTH_TOKEN and
// ANTHROPIC_API_KEY that were stripped. Returns an empty map if not running
// under sudo or if the parent environ cannot be read.
func sudoParentEnv() map[string]string {
	result := make(map[string]string)
	if fileutil.SudoUID() == -1 {
		return result
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", os.Getppid())) //nolint:gosec,forbidigo // G304 + §12: read parent's environ to recover sudo-stripped credentials
	if err != nil {
		return result
	}
	for kv := range strings.SplitSeq(string(data), "\x00") {
		k, v, ok := strings.Cut(kv, "=")
		if ok && k != "" {
			result[k] = v
		}
	}
	return result
}

// buildMounts constructs the bind mounts for the sandbox instance.
func buildMounts(state *sandboxState, secretsDir string) []runtime.MountSpec {
	var mounts []runtime.MountSpec
	mounts = append(mounts, buildWorkdirMounts(state)...)
	mounts = append(mounts, buildAuxDirMounts(state)...)
	mounts = append(mounts, buildAgentMounts(state)...)
	mounts = append(mounts, buildSystemMounts(state)...)
	mounts = append(mounts, buildGitAndTmuxMounts(state)...)
	mounts = append(mounts, buildConfigAndSecretsMounts(state, secretsDir)...)
	return mounts
}

// buildWorkdirMounts returns the mount specs for the sandbox workdir.
func buildWorkdirMounts(state *sandboxState) []runtime.MountSpec {
	switch state.workdir.Mode {
	case "copy":
		return []runtime.MountSpec{{
			HostPath:      state.workCopyDir,
			ContainerPath: state.workdir.ResolvedMountPath(),
		}}
	case "overlay":
		encoded := store.EncodePath(state.workdir.Path)
		// Mount the entire overlay work base dir (upper/ovlwork/merged/lower) as
		// a single bind mount so upper and ovlwork share the same underlying Docker
		// volume — a kernel requirement for overlayfs to work inside a container.
		// The user's workdir is then nested on top as a read-only bind mount at
		// the lower/ subdirectory within the same volume.
		return []runtime.MountSpec{
			{
				HostPath:      store.OverlayWorkBaseDir(state.sandboxDir, state.workdir.Path),
				ContainerPath: "/yoloai/overlay/" + encoded,
			},
			{
				HostPath:      state.workdir.Path,
				ContainerPath: "/yoloai/overlay/" + encoded + "/lower",
				ReadOnly:      true,
			},
		}
	default:
		return []runtime.MountSpec{{
			HostPath:      state.workdir.Path,
			ContainerPath: state.workdir.ResolvedMountPath(),
			ReadOnly:      state.workdir.Mode != "rw",
		}}
	}
}

// buildAuxDirMounts returns the mount specs for all auxiliary directories.
func buildAuxDirMounts(state *sandboxState) []runtime.MountSpec {
	var mounts []runtime.MountSpec
	for _, ad := range state.auxDirs {
		mounts = append(mounts, buildSingleAuxDirMount(state.sandboxDir, ad)...)
	}
	return mounts
}

// buildSingleAuxDirMount returns mount specs for one auxiliary directory.
func buildSingleAuxDirMount(sandboxDir string, ad *DirSpec) []runtime.MountSpec {
	mountTarget := ad.ResolvedMountPath()
	switch ad.Mode {
	case "copy":
		return []runtime.MountSpec{{
			HostPath:      store.WorkDir(sandboxDir, ad.Path),
			ContainerPath: mountTarget,
		}}
	case "overlay":
		encoded := store.EncodePath(ad.Path)
		return []runtime.MountSpec{
			{
				HostPath:      store.OverlayWorkBaseDir(sandboxDir, ad.Path),
				ContainerPath: "/yoloai/overlay/" + encoded,
			},
			{
				HostPath:      ad.Path,
				ContainerPath: "/yoloai/overlay/" + encoded + "/lower",
				ReadOnly:      true,
			},
		}
	case "rw":
		return []runtime.MountSpec{{
			HostPath:      ad.Path,
			ContainerPath: mountTarget,
		}}
	default: // read-only (empty mode or explicit "ro")
		return []runtime.MountSpec{{
			HostPath:      ad.Path,
			ContainerPath: mountTarget,
			ReadOnly:      true,
		}}
	}
}

// buildAgentMounts returns mount specs for the agent runtime dir, VS Code CLI, and home-seed files.
func buildAgentMounts(state *sandboxState) []runtime.MountSpec {
	var mounts []runtime.MountSpec

	// Agent runtime directory (agent's own managed state)
	if state.agent.StateDir != "" {
		mounts = append(mounts, runtime.MountSpec{
			HostPath:      filepath.Join(state.sandboxDir, store.AgentRuntimeDir),
			ContainerPath: state.agent.StateDir,
		})
	}

	// VS Code CLI data dir
	if state.vscodeTunnel {
		mounts = append(mounts, buildVscodeMounts(state)...)
	}

	// Home-seed files and directories (mounted into /home/yoloai/)
	mounts = append(mounts, buildHomeSeedMounts(state)...)

	return mounts
}

// buildVscodeMounts returns mount specs for VS Code tunnel support.
func buildVscodeMounts(state *sandboxState) []runtime.MountSpec {
	var mounts []runtime.MountSpec

	// VS Code CLI data dir — per-sandbox to prevent singleton lock conflicts when
	// multiple sandboxes run tunnels concurrently. Token is seeded from the global
	// dir (~/.yoloai/vscode-cli/) on first use so re-authentication is only needed
	// once across all sandboxes.
	vscodeSandboxCLIDir := filepath.Join(state.sandboxDir, "vscode-cli")
	_ = fileutil.MkdirAll(vscodeSandboxCLIDir, 0750) //nolint:gosec // G301: sandbox dir, private

	// Seed token from global dir if this sandbox hasn't authenticated yet.
	globalTokenPath := filepath.Join(state.layout.VscodeCLIDir(), "token.json")
	sandboxTokenPath := filepath.Join(vscodeSandboxCLIDir, "token.json")
	if _, err := os.Stat(sandboxTokenPath); os.IsNotExist(err) {
		if data, err2 := os.ReadFile(globalTokenPath); err2 == nil { //nolint:gosec // G304: path is sandbox-controlled
			_ = fileutil.WriteFile(sandboxTokenPath, data, 0600)
		}
	}

	mounts = append(mounts, runtime.MountSpec{
		HostPath:      vscodeSandboxCLIDir,
		ContainerPath: "/home/yoloai/.vscode/cli",
	})

	// Stable machine-id — VS Code CLI ties its token to /etc/machine-id; a
	// fresh random ID on each container restart causes re-authentication.
	machineIDPath := filepath.Join(state.sandboxDir, store.MachineIDFile)
	if err := ensureMachineID(machineIDPath); err == nil {
		mounts = append(mounts, runtime.MountSpec{
			HostPath:      machineIDPath,
			ContainerPath: "/etc/machine-id",
			ReadOnly:      true,
		})
	}

	return mounts
}

// buildHomeSeedMounts returns mount specs for home-seed files.
func buildHomeSeedMounts(state *sandboxState) []runtime.MountSpec {
	var mounts []runtime.MountSpec
	mountedDirs := map[string]bool{}
	for _, sf := range state.agent.SeedFiles {
		if !sf.HomeDir {
			continue
		}
		// For nested paths (e.g., ".claude/settings.json"), mount the
		// top-level directory once rather than individual files. This lets
		// agents create new state files at runtime.
		if strings.Contains(sf.TargetPath, "/") {
			topDir, _, _ := strings.Cut(sf.TargetPath, "/")
			if mountedDirs[topDir] {
				continue
			}
			src := filepath.Join(state.sandboxDir, "home-seed", topDir)
			if _, err := os.Stat(src); err != nil {
				continue
			}
			mounts = append(mounts, runtime.MountSpec{
				HostPath:      src,
				ContainerPath: "/home/yoloai/" + topDir,
			})
			mountedDirs[topDir] = true
		} else {
			src := filepath.Join(state.sandboxDir, "home-seed", sf.TargetPath)
			if _, err := os.Stat(src); err != nil {
				continue // skip if not seeded
			}
			mounts = append(mounts, runtime.MountSpec{
				HostPath:      src,
				ContainerPath: "/home/yoloai/" + sf.TargetPath,
			})
		}
	}
	return mounts
}

// buildSystemMounts returns mount specs for logs, status, prompt, config, files, and cache.
func buildSystemMounts(state *sandboxState) []runtime.MountSpec {
	mounts := []runtime.MountSpec{
		// Structured log directory
		{
			HostPath:      filepath.Join(state.sandboxDir, store.LogsDir),
			ContainerPath: "/yoloai/" + store.LogsDir,
		},
		// Agent status file (for in-container status monitor)
		{
			HostPath:      filepath.Join(state.sandboxDir, store.AgentStatusFile),
			ContainerPath: "/yoloai/" + store.AgentStatusFile,
		},
	}

	// Prompt file
	if state.hasPrompt {
		promptSource := filepath.Join(state.sandboxDir, "prompt.txt")
		if state.promptSourcePath != "" {
			promptSource = state.promptSourcePath
		}
		mounts = append(mounts, runtime.MountSpec{
			HostPath:      promptSource,
			ContainerPath: "/yoloai/prompt.txt",
			ReadOnly:      true,
		})
	}

	mounts = append(mounts,
		// Runtime config file
		runtime.MountSpec{
			HostPath:      filepath.Join(state.sandboxDir, store.RuntimeConfigFile),
			ContainerPath: "/yoloai/" + store.RuntimeConfigFile,
			ReadOnly:      true,
		},
		// File exchange directory
		runtime.MountSpec{
			HostPath:      filepath.Join(state.sandboxDir, "files"),
			ContainerPath: "/yoloai/files",
		},
		// Cache directory
		runtime.MountSpec{
			HostPath:      filepath.Join(state.sandboxDir, "cache"),
			ContainerPath: "/yoloai/cache",
		},
	)

	return mounts
}

// buildGitAndTmuxMounts returns mount specs for git identity and tmux configuration.
func buildGitAndTmuxMounts(state *sandboxState) []runtime.MountSpec {
	var mounts []runtime.MountSpec

	// Defaults tmux config
	if state.tmuxConf == "default" || state.tmuxConf == "default+host" {
		defaultsTmuxConf := filepath.Join(state.layout.DefaultsDir(), "tmux.conf")
		if _, err := os.Stat(defaultsTmuxConf); err == nil {
			// Ensure the file is world-readable (0644). It may have been written
			// with 0600 by older yoloai versions. Inside Kata VMs the file is
			// mounted via virtiofs retaining its host uid, but the yoloai user
			// inside the VM (uid 1001) differs from the host user's uid, so
			// a 0600 file causes tmux to fail reading its config and enter
			// copy-mode — preventing send-keys from reaching the shell.
			_ = os.Chmod(defaultsTmuxConf, 0644) //nolint:gosec // G302: tmux.conf contains no secrets
			mounts = append(mounts, runtime.MountSpec{
				HostPath:      defaultsTmuxConf,
				ContainerPath: "/yoloai/tmux/tmux.conf",
				ReadOnly:      true,
			})
		}
	}

	// Host tmux config (when tmux_conf is default+host or host)
	if state.tmuxConf == "default+host" || state.tmuxConf == "host" {
		tmuxConfPath := ExpandTilde("~/.tmux.conf", state.homeDir)
		if _, err := os.Stat(tmuxConfPath); err == nil {
			mounts = append(mounts, runtime.MountSpec{
				HostPath:      tmuxConfPath,
				ContainerPath: "/home/yoloai/.tmux.conf",
				ReadOnly:      true,
			})
		}
	}

	// Git identity: mount ~/.gitconfig and ~/.config/git/ read-only so that
	// git commands inside the container can resolve user.name / user.email.
	// Mirrors the symlink-based approach used by the Seatbelt backend.
	gitconfigPath := ExpandTilde("~/.gitconfig", state.homeDir)
	if _, err := os.Stat(gitconfigPath); err == nil {
		mounts = append(mounts, runtime.MountSpec{
			HostPath:      gitconfigPath,
			ContainerPath: "/home/yoloai/.gitconfig",
			ReadOnly:      true,
		})
	}
	gitConfigDir := ExpandTilde("~/.config/git", state.homeDir)
	if info, err := os.Stat(gitConfigDir); err == nil && info.IsDir() {
		mounts = append(mounts, runtime.MountSpec{
			HostPath:      gitConfigDir,
			ContainerPath: "/home/yoloai/.config/git",
			ReadOnly:      true,
		})
	}

	return mounts
}

// buildConfigAndSecretsMounts returns mount specs for config/profile mounts and secrets.
func buildConfigAndSecretsMounts(state *sandboxState, secretsDir string) []runtime.MountSpec {
	var mounts []runtime.MountSpec

	// Config/profile mounts (host:container[:ro])
	for _, m := range state.configMounts {
		spec, err := parseConfigMount(m, state.homeDir, state.layout.Env)
		if err != nil {
			continue // skip unparseable mounts (validated at creation time)
		}
		mounts = append(mounts, spec)
	}

	// Secrets (env vars + API keys): mount the whole directory so that
	// Podman and Docker both work. Podman fails with per-file bind mounts
	// because its Docker-compatible API tries to mkdir the source path.
	// The entrypoint already iterates /run/secrets as a directory.
	if secretsDir != "" {
		mounts = append(mounts, runtime.MountSpec{
			HostPath:      secretsDir,
			ContainerPath: "/run/secrets",
			ReadOnly:      true,
		})
	}

	return mounts
}

// overlayOrResolvedMountPath returns the container working directory path for a directory.
// For overlay mode, this is the bind-mounted merged path; otherwise the resolved mount path.
func overlayOrResolvedMountPath(d *DirSpec) string {
	if d.Mode == "overlay" {
		return "/yoloai/overlay/" + store.EncodePath(d.Path) + "/merged"
	}
	return d.ResolvedMountPath()
}

// hasAnyAPIKey returns true if any of the agent's required API key env vars are set
// in the process environment or in credOverrides (sudo-recovered credential defaults).
func hasAnyAPIKey(agentDef *agent.Definition, credOverrides map[string]string) bool {
	if len(agentDef.APIKeyEnvVars) == 0 {
		return true // no API key required
	}
	for _, key := range agentDef.APIKeyEnvVars {
		if os.Getenv(key) != "" || credOverrides[key] != "" { //nolint:forbidigo // §12: agent API-key presence check (declared exception)
			return true
		}
	}
	return false
}

// hasAnyAuthFile returns true if any auth-only seed files exist on disk
// or can be read from the macOS Keychain.
// homeDir is used for ~ expansion in seed file host paths.
func hasAnyAuthFile(agentDef *agent.Definition, homeDir string) bool {
	for _, sf := range agentDef.SeedFiles {
		if sf.AuthOnly {
			if _, err := os.Stat(ExpandTilde(sf.HostPath, homeDir)); err == nil {
				return true
			}
			if sf.KeychainService != "" {
				if _, err := keychainReader(sf.KeychainService); err == nil {
					return true
				}
			}
		}
	}
	return false
}

// hasAnyAuthHint returns true if any of the agent's auth hint env vars are set
// in the host environment, in the config env map, or in credOverrides
// (sudo-recovered credential defaults). This allows agents like aider to work
// with local model servers (Ollama, LM Studio) without a cloud API key.
func hasAnyAuthHint(agentDef *agent.Definition, configEnv map[string]string, credOverrides map[string]string) bool {
	for _, key := range agentDef.AuthHintEnvVars {
		if os.Getenv(key) != "" || credOverrides[key] != "" { //nolint:forbidigo // §12: agent auth-hint presence check (declared exception)
			return true
		}
		if configEnv[key] != "" {
			return true
		}
	}
	return false
}

// containsLocalhost returns true if the URL string references localhost or 127.0.0.1.
func containsLocalhost(url string) bool {
	return strings.Contains(url, "localhost") || strings.Contains(url, "127.0.0.1")
}

// describeSeedAuthFiles returns a human-readable description of expected auth file paths.
func describeSeedAuthFiles(agentDef *agent.Definition) string {
	var paths []string
	for _, sf := range agentDef.SeedFiles {
		if sf.AuthOnly {
			paths = append(paths, sf.HostPath)
		}
	}
	return strings.Join(paths, ", ")
}

// copySeedFiles copies seed files from the host into the sandbox.
// Files with AuthOnly=true are skipped when hasAPIKey is true.
// Files with HomeDir=true go to home-seed/ (mounted at /home/yoloai/);
// others go to agent-runtime/ (mounted at StateDir).
// Returns true if any files were copied. Skips files that don't exist on the host.
// homeDir is used for ~ expansion in seed file host paths.
func copySeedFiles(agentDef *agent.Definition, sandboxDir string, hasAPIKey bool, homeDir string) (bool, error) {
	copiedAuth := false
	agentStateDir := filepath.Join(sandboxDir, store.AgentRuntimeDir)
	homeSeedDir := filepath.Join(sandboxDir, "home-seed")

	for _, sf := range agentDef.SeedFiles {
		if shouldSkipSeedFile(sf, hasAPIKey) {
			continue
		}

		data, ok, err := loadSeedFileData(sf, homeDir)
		if err != nil {
			return copiedAuth, err
		}
		if !ok {
			continue
		}

		baseDir := agentStateDir
		if sf.HomeDir {
			baseDir = homeSeedDir
		}
		targetPath := filepath.Join(baseDir, sf.TargetPath)

		if err := fileutil.MkdirAll(filepath.Dir(targetPath), 0750); err != nil {
			return copiedAuth, fmt.Errorf("create dir for %s: %w", sf.TargetPath, err)
		}
		if err := fileutil.WriteFile(targetPath, data, 0600); err != nil { //nolint:gosec // G703: targetPath is constructed from internal agent config, not user input
			return copiedAuth, fmt.Errorf("write %s: %w", targetPath, err)
		}
		if sf.AuthOnly {
			copiedAuth = true
		}
	}

	return copiedAuth, nil
}

// shouldSkipSeedFile returns true if the seed file should be skipped.
func shouldSkipSeedFile(sf agent.SeedFile, hasAPIKey bool) bool {
	if !sf.AuthOnly {
		return false
	}
	if len(sf.OwnerAPIKeys) > 0 {
		// Per-file API key check (used by shell agent): skip if any key is set
		for _, key := range sf.OwnerAPIKeys {
			if os.Getenv(key) != "" { //nolint:forbidigo // §12: agent API-key presence check (declared exception)
				return true
			}
		}
		return false
	}
	return hasAPIKey // auth file not needed when API key is set
}

// loadSeedFileData reads data from the host file or keychain for a seed file.
// Returns (data, true, nil) if found, (nil, false, nil) if not found, or (nil, false, err) on error.
// homeDir is used for ~ expansion in seed file host paths.
func loadSeedFileData(sf agent.SeedFile, homeDir string) ([]byte, bool, error) {
	hostPath := ExpandTilde(sf.HostPath, homeDir)
	if _, err := os.Stat(hostPath); err == nil {
		data, readErr := os.ReadFile(hostPath) //nolint:gosec // G304: path is from agent definition, not user input
		if readErr != nil {
			return nil, false, fmt.Errorf("read %s: %w", hostPath, readErr)
		}
		return data, true, nil
	}
	if sf.KeychainService != "" {
		data, keychainErr := keychainReader(sf.KeychainService)
		if keychainErr == nil {
			return data, true, nil
		}
	}
	return nil, false, nil
}

// ensureContainerSettings merges required container settings into agent-state/settings.json.
// Agent-specific adjustments are driven by each agent's ApplySettings field.
// Shell agents (SeedsAllAgents=true) apply each real agent's settings into
// home-seed subdirectories instead.
func ensureContainerSettings(agentDef *agent.Definition, sandboxDir string, isolation runtime.IsolationMode) error {
	if agentDef.SeedsAllAgents {
		return ensureShellContainerSettings(sandboxDir, isolation)
	}

	if agentDef.StateDir == "" || agentDef.ApplySettings == nil {
		return nil
	}

	// Use restrictive permissions by default, world-writable only for container-enhanced (gVisor)
	perms := Perms(isolation)

	agentStateDir := filepath.Join(sandboxDir, store.AgentRuntimeDir)
	if err := mkdirAllPerm(agentStateDir, perms.Dir); err != nil {
		return fmt.Errorf("create %s dir: %w", store.AgentRuntimeDir, err)
	}
	settingsPath := filepath.Join(agentStateDir, "settings.json")

	settings, err := readJSONMap(settingsPath)
	if err != nil {
		return err
	}
	agentDef.ApplySettings(settings)
	return writeJSONMap(settingsPath, settings)
}

// ensureShellContainerSettings applies each real agent's container settings
// to its home-seed subdirectory (e.g., home-seed/.claude/settings.json).
func ensureShellContainerSettings(sandboxDir string, _ runtime.IsolationMode) error {
	for _, name := range agent.RealAgents() {
		def := agent.GetAgent(name)
		if def.StateDir == "" || def.ApplySettings == nil {
			continue
		}
		dirBase := filepath.Base(def.StateDir)
		dirPath := filepath.Join(sandboxDir, "home-seed", dirBase)
		settingsPath := filepath.Join(dirPath, "settings.json")

		if err := fileutil.MkdirAll(dirPath, 0750); err != nil {
			return fmt.Errorf("create %s dir: %w", dirBase, err)
		}
		settings, err := readJSONMap(settingsPath)
		if err != nil {
			return err
		}
		def.ApplySettings(settings)
		if err := writeJSONMap(settingsPath, settings); err != nil {
			return err
		}
	}
	return nil
}

// ensureHomeSeedConfig patches home-seed/.claude.json so its installMethod
// matches how the backend actually installed Claude Code (installMethod is the
// backend's AgentInstallMethod — "npm-global" for the container backends,
// "native" for Tart). The seeded file comes from the host, which usually says
// "native"; when the backend installs via npm, a mismatch makes Claude Code
// emit spurious warnings about a missing ~/.local/bin/claude and PATH
// misconfiguration. Writing the backend's real method keeps them consistent.
func ensureHomeSeedConfig(agentDef *agent.Definition, sandboxDir, installMethod string) error {
	// Only relevant for agents that seed .claude.json into HomeDir
	var hasHomeSeed bool
	for _, sf := range agentDef.SeedFiles {
		if sf.HomeDir && sf.TargetPath == ".claude.json" {
			hasHomeSeed = true
			break
		}
	}
	if !hasHomeSeed {
		return nil
	}

	configPath := filepath.Join(sandboxDir, "home-seed", ".claude.json")

	config, err := readJSONMap(configPath)
	if err != nil {
		return err
	}

	config["installMethod"] = installMethod

	return writeJSONMap(configPath, config)
}

// readLogTail returns the last n lines of the file at path.
// Returns empty string on any error or if the file is empty.
func readLogTail(path string, n int) string {
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is constructed from sandbox dir
	if err != nil || len(data) == 0 {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// parseResourceLimits converts user-facing string resource limits to
// runtime-level int64 values (NanoCPUs, bytes).
func parseResourceLimits(rl *config.ResourceLimits) (*runtime.ResourceLimits, error) {
	result := &runtime.ResourceLimits{}

	if rl.CPUs != "" {
		cpus, err := strconv.ParseFloat(rl.CPUs, 64)
		if err != nil || cpus <= 0 {
			return nil, fmt.Errorf("invalid cpus value %q: must be a positive number (e.g., 4, 2.5)", rl.CPUs)
		}
		result.NanoCPUs = int64(cpus * 1e9)
	}

	if rl.Memory != "" {
		mem, err := parseMemoryString(rl.Memory)
		if err != nil {
			return nil, err
		}
		result.Memory = mem
	}

	if result.NanoCPUs == 0 && result.Memory == 0 {
		return nil, nil
	}
	return result, nil
}

// parseMemoryString parses a Docker-style memory string (e.g., "512m", "8g")
// into bytes. Supported suffixes: b, k, m, g (case-insensitive).
func parseMemoryString(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}

	s = strings.TrimSpace(s)
	if len(s) == 0 {
		return 0, fmt.Errorf("empty memory value")
	}

	// Check for suffix
	lastChar := strings.ToLower(s[len(s)-1:])
	var multiplier int64 = 1
	numStr := s

	switch lastChar {
	case "b":
		numStr = s[:len(s)-1]
	case "k":
		multiplier = 1024
		numStr = s[:len(s)-1]
	case "m":
		multiplier = 1024 * 1024
		numStr = s[:len(s)-1]
	case "g":
		multiplier = 1024 * 1024 * 1024
		numStr = s[:len(s)-1]
	default:
		// No suffix — treat as bytes
	}

	val, err := strconv.ParseFloat(numStr, 64)
	if err != nil || val <= 0 {
		return 0, fmt.Errorf("invalid memory value %q: must be a positive number with optional suffix (b, k, m, g)", s)
	}

	return int64(val * float64(multiplier)), nil
}

// hasOverlayDirs returns true if the sandbox's workdir uses overlay
// mode. Q-U (2026-05-25) collapsed aux :overlay to the workdir only,
// so this is now a single-field check. Kept as a named predicate for
// callsite readability.
func hasOverlayDirs(state *sandboxState) bool {
	return state.workdir.Mode == "overlay"
}

// parseConfigMount parses a "host:container[:ro]" mount string into a MountSpec.
// The host path is expanded (tilde and ${VAR}).
// homeDir is used to expand leading "~" in the host path.
func parseConfigMount(s, homeDir string, env map[string]string) (runtime.MountSpec, error) {
	parts := strings.SplitN(s, ":", 3)
	if len(parts) < 2 {
		return runtime.MountSpec{}, fmt.Errorf("expected host:container[:ro] format")
	}

	hostPath, err := ExpandPath(parts[0], homeDir, env)
	if err != nil {
		return runtime.MountSpec{}, fmt.Errorf("expand host path: %w", err)
	}

	spec := runtime.MountSpec{
		HostPath:      hostPath,
		ContainerPath: parts[1],
	}

	if len(parts) == 3 {
		if parts[2] == "ro" {
			spec.ReadOnly = true
		} else {
			return runtime.MountSpec{}, fmt.Errorf("unknown mount option %q (expected \"ro\")", parts[2])
		}
	}

	return spec, nil
}

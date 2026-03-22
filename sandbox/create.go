package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kstenerud/yoloai/agent"
	"github.com/kstenerud/yoloai/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/runtime"
)

// mkdirAllPerm creates a directory (and parents) then explicitly chmods it to
// bypass the process umask. Use this when the directory will be bind-mounted
// into a container that may run under a different uid (e.g. gVisor).
func mkdirAllPerm(path string, perm os.FileMode) error {
	if err := os.MkdirAll(path, perm); err != nil {
		return err
	}
	if err := os.Chmod(path, perm); err != nil { //nolint:gosec // G302: caller is responsible for choosing the perm
		return err
	}
	return fileutil.ChownIfSudo(path)
}

// writeFilePerm writes data to a file then explicitly chmods it to bypass the
// process umask. Use this when the file will be bind-mounted into a container
// that may run under a different uid (e.g. gVisor).
func writeFilePerm(path string, data []byte, perm os.FileMode) error {
	if err := os.WriteFile(path, data, perm); err != nil { //nolint:gosec // G703: path is always a trusted sandbox subpath
		return err
	}
	if err := os.Chmod(path, perm); err != nil { //nolint:gosec // G302: caller is responsible for choosing the perm
		return err
	}
	return fileutil.ChownIfSudo(path)
}

// NetworkMode specifies the sandbox's network access policy.
type NetworkMode string

const (
	NetworkModeDefault  NetworkMode = ""         // full network access
	NetworkModeNone     NetworkMode = "none"     // no network access
	NetworkModeIsolated NetworkMode = "isolated" // allowlist only
)

// checkIsolationPrerequisites delegates isolation prerequisite validation to the
// runtime backend via the optional IsolationValidator interface.
// If the backend does not implement IsolationValidator, validation is skipped.
func checkIsolationPrerequisites(ctx context.Context, rt runtime.Runtime, isolation string) error {
	v, ok := rt.(runtime.IsolationValidator)
	if !ok {
		return nil
	}
	return v.ValidateIsolation(ctx, isolation)
}

// DirMode specifies how a directory is mounted in the sandbox.
type DirMode string

const (
	DirModeCopy    DirMode = "copy"    // full copy; changes tracked via git
	DirModeOverlay DirMode = "overlay" // overlayfs; original untouched
	DirModeRW      DirMode = "rw"      // live bind-mount; changes immediate
	DirModeRO      DirMode = ""        // read-only bind-mount (aux dirs only)
)

// DirSpec describes a directory to mount in the sandbox.
// Use this instead of raw ":copy"/":rw" string syntax.
type DirSpec struct {
	Path      string  // absolute host path; required
	Mode      DirMode // mount mode; required for workdir
	MountPath string  // custom container mount path; empty = mirror host path
	Force     bool    // skip dirty-repo safety check
}

// CreateOptions holds all parameters for sandbox creation.
type CreateOptions struct {
	Name         string
	Workdir      DirSpec           // primary working directory
	AuxDirs      []DirSpec         // auxiliary directories
	Agent        string            // agent name (e.g., "claude", "test")
	Model        string            // model name or alias (e.g., "sonnet", "claude-sonnet-4-latest")
	Profile      string            // profile name (from --profile flag)
	Prompt       string            // prompt text (from --prompt)
	PromptFile   string            // prompt file path (from --prompt-file)
	Network      NetworkMode       // network access policy
	NetworkAllow []string          // --network-allow flags
	Ports        []string          // --port flags (e.g., ["3000:3000"])
	Replace      bool              // --replace flag (safe: errors if unapplied work exists)
	Force        bool              // --force flag (unconditional replace, skips safety check)
	NoStart      bool              // --no-start flag
	Yes          bool              // --yes flag (skip confirmations)
	Passthrough  []string          // args after -- passed to agent
	Version      string            // yoloAI version for meta.json
	Attach       bool              // --attach flag (auto-attach after creation)
	Debug        bool              // --debug flag (enable entrypoint debug logging)
	CPUs         string            // --cpus flag (e.g., "4", "2.5")
	Memory       string            // --memory flag (e.g., "8g", "512m")
	Env          map[string]string // --env flags (KEY=VAL pairs)
	Isolation    string            // --isolation flag (e.g., "container-enhanced", "vm")
}

// sandboxState holds resolved state computed during preparation.
type sandboxState struct {
	name              string
	sandboxDir        string
	workdir           *DirArg
	workCopyDir       string
	auxDirs           []*DirArg
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
	capAdd            []string // Linux capabilities from config/profile
	devices           []string // host devices from config/profile
	setup             []string // setup commands from config/profile
	isolation         string   // isolation mode from config/profile
	isolationExplicit bool     // true when isolation was set via --isolation flag
	meta              *Meta
	configJSON        []byte
}

// overlayMountConfig describes a single overlay mount for config.json.
type overlayMountConfig struct {
	Lower  string `json:"lower"`
	Upper  string `json:"upper"`
	Work   string `json:"work"`
	Merged string `json:"merged"`
}

// containerConfig is the serializable form of runtime-config.json.
type containerConfig struct {
	HostUID            int                  `json:"host_uid"`
	HostGID            int                  `json:"host_gid"`
	AgentCommand       string               `json:"agent_command"`
	StartupDelay       int                  `json:"startup_delay"`
	ReadyPattern       string               `json:"ready_pattern"`
	SubmitSequence     string               `json:"submit_sequence"`
	TmuxConf           string               `json:"tmux_conf"`
	WorkingDir         string               `json:"working_dir"`
	StateDirName       string               `json:"state_dir_name"`
	Debug              bool                 `json:"debug,omitempty"`
	NetworkIsolated    bool                 `json:"network_isolated,omitempty"`
	AllowedDomains     []string             `json:"allowed_domains,omitempty"`
	Passthrough        []string             `json:"passthrough,omitempty"`
	OverlayMounts      []overlayMountConfig `json:"overlay_mounts,omitempty"`
	SetupCommands      []string             `json:"setup_commands,omitempty"`
	AutoCommitInterval int                  `json:"auto_commit_interval,omitempty"`
	CopyDirs           []string             `json:"copy_dirs,omitempty"`
	HookIdle           bool                 `json:"hook_idle,omitempty"`
	Idle               agent.IdleSupport    `json:"idle"`
	Detectors          []string             `json:"detectors,omitempty"`
	SandboxName        string               `json:"sandbox_name"`
	TmuxSocket         string               `json:"tmux_socket,omitempty"`
}

// Create creates and optionally starts a new sandbox.
// Returns the sandbox name on success (empty if user cancelled or no-start).
func (m *Manager) Create(ctx context.Context, opts CreateOptions) (string, error) {
	unlock, err := acquireLock(opts.Name)
	if err != nil {
		return "", err
	}
	defer unlock()

	slog.Info("creating sandbox", "event", "sandbox.create", "sandbox", opts.Name, "agent", opts.Agent, "backend", m.backend)
	// When running as root under sudo, API key env vars (e.g. CLAUDE_CODE_OAUTH_TOKEN)
	// are stripped by sudo. Recover them from the parent process's environment into a
	// local map rather than mutating the process environment. Only keys absent from
	// os.Environ are included so host values always take precedence.
	credOverrides := make(map[string]string)
	for k, v := range sudoParentEnv() {
		if os.Getenv(k) == "" {
			credOverrides[k] = v
		}
	}
	// Validate isolation prerequisites before the potentially expensive image build.
	if opts.Isolation != "" {
		if err := checkIsolationPrerequisites(ctx, m.runtime, opts.Isolation); err != nil {
			return "", err
		}
	}
	if err := m.EnsureSetup(ctx); err != nil {
		return "", err
	}

	state, err := m.prepareSandboxState(ctx, opts, credOverrides)
	if err != nil {
		return "", err
	}
	if state == nil {
		return "", nil // user cancelled
	}

	if opts.NoStart {
		m.printCreationOutput(state, false)
		return "", nil
	}

	if err := m.launchContainer(ctx, state); err != nil {
		// Clean up sandbox directory and attempt container removal.
		_ = os.RemoveAll(state.sandboxDir)
		_ = m.runtime.Remove(ctx, InstanceName(state.name))
		return "", err
	}

	slog.Info("sandbox created", "event", "sandbox.create.complete", "sandbox", state.name)
	m.printCreationOutput(state, opts.Attach)
	return state.name, nil
}

// checkUnappliedWork checks if the named sandbox has any unapplied work
// (uncommitted changes or commits beyond the baseline). Returns an error
// if work would be lost.
func checkUnappliedWork(name string) error {
	meta, err := LoadMeta(Dir(name))
	if err != nil {
		return nil //nolint:nilerr // can't load meta — nothing to protect
	}

	if meta.Workdir.Mode == "copy" || meta.Workdir.Mode == "overlay" {
		workDir := WorkDir(name, meta.Workdir.HostPath)
		if hasUnappliedWork(workDir, meta.Workdir.BaselineSHA) {
			return fmt.Errorf("sandbox %q has unapplied changes (use --force to replace anyway, or 'yoloai apply' first)", name)
		}
	}

	for _, d := range meta.Directories {
		if d.Mode == "copy" || d.Mode == "overlay" {
			auxWorkDir := WorkDir(name, d.HostPath)
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
	// Validate
	if err := ValidateName(opts.Name); err != nil {
		return nil, err
	}

	agentDef := agent.GetAgent(opts.Agent)
	if agentDef == nil {
		return nil, NewUsageError("unknown agent: %s", opts.Agent)
	}

	// --force implies --replace
	if opts.Force {
		opts.Replace = true
	}

	sandboxDir := Dir(opts.Name)
	if _, err := os.Stat(sandboxDir); err == nil && !opts.Replace {
		// Directory exists — check if it's a complete sandbox
		if _, metaErr := LoadMeta(sandboxDir); metaErr != nil {
			// Broken sandbox (no valid meta.json) — auto-clean
			_ = os.RemoveAll(sandboxDir)
		} else {
			return nil, fmt.Errorf("sandbox %q already exists (use --replace to recreate): %w", opts.Name, ErrSandboxExists)
		}
	}

	if opts.Prompt != "" && opts.PromptFile != "" {
		return nil, NewUsageError("--prompt and --prompt-file are mutually exclusive")
	}

	// Load config early — needed for auth hint check and later for tmux_conf.
	ycfg, err := config.LoadConfig()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	gcfg, err := config.LoadGlobalConfig()
	if err != nil {
		return nil, fmt.Errorf("load global config: %w", err)
	}

	// Resolve profile config: profile chain, config merging, image building.
	pr, err := m.resolveProfileConfig(ctx, &opts, &agentDef, ycfg, gcfg)
	if err != nil {
		return nil, err
	}

	// Apply config defaults and CLI overrides for resources.
	if err := applyConfigDefaults(&opts, ycfg, pr); err != nil {
		return nil, err
	}

	// Validate and expand config mounts.
	mergedMounts, err := validateAndExpandMounts(pr.mounts)
	if err != nil {
		return nil, err
	}

	// --replace: destroy existing sandbox (--force skips safety check)
	if opts.Replace {
		if _, err := os.Stat(sandboxDir); err == nil {
			if !opts.Force {
				if err := checkUnappliedWork(opts.Name); err != nil {
					return nil, err
				}
			}
			if err := m.destroy(ctx, opts.Name); err != nil {
				return nil, fmt.Errorf("replace existing sandbox: %w", err)
			}
		}
	}

	// Parse and validate directories, auth, safety checks, dirty repo warnings.
	workdir, auxDirs, err := m.parseAndValidateDirs(ctx, opts, agentDef, pr.env, ycfg.Model, credOverrides)
	if err != nil {
		return nil, err
	}
	if workdir == nil {
		return nil, nil // user cancelled
	}

	// Create directory structure.
	// Host-only dirs (sandboxDir, home-seed, bin, tmux, backend) use 0750.
	// Dirs bind-mounted into the container use restricted permissions by default
	// (0750), but gVisor requires 0777 due to user namespace UID remapping where
	// container root is remapped to a non-root host uid.
	for _, dir := range []string{
		sandboxDir,
		filepath.Join(sandboxDir, "home-seed"),
		filepath.Join(sandboxDir, BinDir),
		filepath.Join(sandboxDir, TmuxDir),
		filepath.Join(sandboxDir, BackendDir),
	} {
		if err := fileutil.MkdirAll(dir, 0750); err != nil {
			return nil, fmt.Errorf("create directory %s: %w", dir, err)
		}
	}

	perms := Perms(pr.isolation)

	for _, dir := range []string{
		filepath.Join(sandboxDir, "work"),
		filepath.Join(sandboxDir, AgentRuntimeDir),
		filepath.Join(sandboxDir, "files"),
		filepath.Join(sandboxDir, "cache"),
	} {
		if err := mkdirAllPerm(dir, perms.Dir); err != nil {
			return nil, fmt.Errorf("create directory %s: %w", dir, err)
		}
	}

	// Cleanup sandbox directory on failure
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(sandboxDir)
		}
	}()

	agentFilesInitialized, err := m.seedSandbox(agentDef, sandboxDir, pr.isolation, pr.agentFiles)
	if err != nil {
		return nil, err
	}

	// Copy/overlay workdir and create git baseline.
	slog.Debug("setting up workdir", "event", "sandbox.create.workdir", "mode", string(workdir.Mode))
	workCopyDir, baselineSHA, err := setupWorkdir(opts.Name, workdir)
	if err != nil {
		return nil, err
	}

	// Copy/overlay aux dirs and create baselines.
	slog.Debug("setting up aux dirs", "event", "sandbox.create.aux_dirs", "count", len(auxDirs))
	dirMetas, err := setupAuxDirs(opts.Name, auxDirs)
	if err != nil {
		return nil, err
	}

	// For backends that run agents directly on the host (seatbelt), :copy mount paths
	// must point to the sandbox copy location rather than the original host path.
	// Container/VM backends bind-mount the copy at the original host path inside the
	// container; ResolveCopyMount returns hostPath unchanged for those backends.
	if workdir.Mode == "copy" && workdir.MountPath == "" {
		workdir.MountPath = m.runtime.ResolveCopyMount(opts.Name, workdir.Path)
	}
	for _, ad := range auxDirs {
		if ad.Mode == "copy" && ad.MountPath == "" {
			ad.MountPath = m.runtime.ResolveCopyMount(opts.Name, ad.Path)
		}
	}

	// Read prompt
	promptText, err := ReadPrompt(opts.Prompt, opts.PromptFile)
	if err != nil {
		return nil, err
	}
	hasPrompt := promptText != ""

	// Resolve model alias and apply provider prefix if needed
	model := resolveModel(agentDef, opts.Model, pr.userAliases)
	model = applyModelPrefix(agentDef, model, pr.env)

	// Build agent command
	agentArgs := pr.agentArgs[opts.Agent]
	agentCommand := buildAgentCommand(agentDef, model, promptText, agentArgs, opts.Passthrough)

	// Read tmux_conf from global config
	tmuxConf := gcfg.TmuxConf
	if tmuxConf == "" {
		tmuxConf = "default" // fallback if not set
	}

	// Determine network mode and allowlist.
	networkMode, networkAllow := buildNetworkConfig(opts, agentDef)
	slog.Debug("building runtime config", "event", "sandbox.create.config", "network_mode", networkMode)

	// Build overlay mount configs for config.json.
	overlayMounts := collectOverlayMounts(workdir, auxDirs)

	// Collect mount paths of all :copy directories for auto-commit loop.
	copyDirs := collectCopyDirs(workdir, auxDirs)

	// Build config.json
	configData, err := buildContainerConfig(agentDef, agentCommand, tmuxConf, overlayOrResolvedMountPath(workdir), opts.Debug, networkMode == "isolated", networkAllow, opts.Passthrough, overlayMounts, pr.setup, pr.autoCommitInterval, copyDirs, opts.Name, m.runtime.TmuxSocket(sandboxDir))
	if err != nil {
		return nil, fmt.Errorf("build %s: %w", RuntimeConfigFile, err)
	}

	// Determine effective user namespace mode. Backends that implement
	// UsernsProvider (currently Podman rootless) may request "keep-id" to map
	// the container uid to the host user. keep-id also changes the tmux exec
	// user from "yoloai" to "" (host user) — see inspect.go:tmuxExecUser.
	// CAP_SYS_ADMIN (required for overlay mounts) disqualifies keep-id because
	// overlay needs real root inside the container.
	usernsMode := ""
	if up, ok := m.runtime.(runtime.UsernsProvider); ok {
		hasSysAdmin := workdir.Mode == "overlay"
		for _, ad := range auxDirs {
			if ad.Mode == "overlay" {
				hasSysAdmin = true
				break
			}
		}
		for _, cap := range pr.capAdd {
			if cap == "SYS_ADMIN" {
				hasSysAdmin = true
				break
			}
		}
		usernsMode = up.UsernsMode(hasSysAdmin)
	}

	// Write state files
	meta := &Meta{
		YoloaiVersion: opts.Version,
		Name:          opts.Name,
		CreatedAt:     time.Now(),
		Backend:       m.backend,
		Profile:       pr.name,
		ImageRef:      pr.imageRef,
		Agent:         opts.Agent,
		Model:         model,
		Workdir: WorkdirMeta{
			HostPath:    workdir.Path,
			MountPath:   overlayOrResolvedMountPath(workdir),
			Mode:        workdir.Mode,
			BaselineSHA: baselineSHA,
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
		HostFilesystem:     m.runtime.Capabilities().HostFilesystem,
	}

	if err := SaveMeta(sandboxDir, meta); err != nil {
		return nil, err
	}

	if err := SaveSandboxState(sandboxDir, &SandboxState{
		AgentFilesInitialized: agentFilesInitialized,
	}); err != nil {
		return nil, err
	}

	if hasPrompt {
		if err := fileutil.WriteFile(filepath.Join(sandboxDir, "prompt.txt"), []byte(promptText), 0600); err != nil {
			return nil, fmt.Errorf("write prompt.txt: %w", err)
		}
	}

	// Determine appropriate permissions based on security mode.
	// gVisor user namespaces require world-readable/writable permissions because
	// container uids are remapped (e.g., root→uid 501, yoloai→uid 1000+501).
	// Standard Docker doesn't need this, so we use restrictive permissions.
	configPerm := os.FileMode(0644) // always 0644 (no secrets, read-only in container)

	if err := mkdirAllPerm(filepath.Join(sandboxDir, LogsDir), perms.Dir); err != nil {
		return nil, fmt.Errorf("create logs dir: %w", err)
	}

	// Pre-create log files so any uid inside the container can append to them.
	// Under gVisor, the entrypoint (running as root) creates the file, and
	// sandbox-setup.py (re-execed as yoloai user) gets EPERM without 0666.
	for _, logFile := range []string{SandboxJSONLFile, MonitorJSONLFile, HooksJSONLFile} {
		p := filepath.Join(sandboxDir, logFile)
		if err := writeFilePerm(p, nil, perms.File); err != nil {
			return nil, fmt.Errorf("create log file %s: %w", logFile, err)
		}
	}

	if err := writeFilePerm(filepath.Join(sandboxDir, AgentStatusFile), []byte("{}\n"), perms.File); err != nil {
		return nil, fmt.Errorf("write %s: %w", AgentStatusFile, err)
	}

	if err := writeFilePerm(filepath.Join(sandboxDir, RuntimeConfigFile), configData, configPerm); err != nil {
		return nil, fmt.Errorf("write %s: %w", RuntimeConfigFile, err)
	}

	if err := WriteContextFiles(sandboxDir, meta, agentDef); err != nil {
		return nil, fmt.Errorf("write context files: %w", err)
	}

	success = true
	return &sandboxState{
		name:              opts.Name,
		sandboxDir:        sandboxDir,
		workdir:           workdir,
		workCopyDir:       workCopyDir,
		auxDirs:           auxDirs,
		agent:             agentDef,
		model:             model,
		profile:           pr.name,
		imageRef:          pr.imageRef,
		env:               pr.env,
		credOverrides:     credOverrides,
		hasPrompt:         hasPrompt,
		networkMode:       networkMode,
		networkAllow:      networkAllow,
		ports:             opts.Ports,
		configMounts:      mergedMounts,
		tmuxConf:          tmuxConf,
		resources:         pr.resources,
		capAdd:            pr.capAdd,
		devices:           pr.devices,
		setup:             pr.setup,
		isolation:         pr.isolation,
		isolationExplicit: pr.isolationExplicit,
		meta:              meta,
		configJSON:        configData,
	}, nil
}

// launchContainer creates a sandbox instance from sandboxState, starts it,
// and cleans up credential temp files. Used by both initial creation and
// recreation from meta.json.
func (m *Manager) launchContainer(ctx context.Context, state *sandboxState) error {
	slog.Info("launching container", "event", "sandbox.create.container.launch", "sandbox", state.name, "image", state.imageRef)
	// Use pre-merged env from state if available, otherwise load from config.
	envVars := state.env
	if envVars == nil {
		cfg, cfgErr := config.LoadConfig()
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

	return m.buildAndStart(ctx, state, mounts, ports, secretsDir != "")
}

// printCreationOutput prints the context-aware summary.
// When autoAttach is true, the attach hint is suppressed (we're about to attach).
func (m *Manager) printCreationOutput(state *sandboxState, autoAttach bool) {
	if state == nil {
		return
	}

	fmt.Fprintf(m.output, "Sandbox %s created\n", state.name)   //nolint:errcheck // best-effort output
	fmt.Fprintf(m.output, "  Agent:    %s\n", state.agent.Name) //nolint:errcheck // best-effort output
	if state.profile != "" {
		fmt.Fprintf(m.output, "  Profile:  %s\n", state.profile) //nolint:errcheck // best-effort output
	}
	fmt.Fprintf(m.output, "  Workdir:  %s (%s)\n", state.workdir.Path, state.workdir.Mode) //nolint:errcheck // best-effort output
	for _, ad := range state.auxDirs {
		mode := ad.Mode
		if mode == "" {
			mode = "ro"
		}
		if ad.MountPath != "" {
			fmt.Fprintf(m.output, "  Dir:      %s → %s (%s)\n", ad.Path, ad.MountPath, mode) //nolint:errcheck // best-effort output
		} else {
			fmt.Fprintf(m.output, "  Dir:      %s (%s)\n", ad.Path, mode) //nolint:errcheck // best-effort output
		}
	}
	switch state.networkMode {
	case "none":
		fmt.Fprintln(m.output, "  Network:  none") //nolint:errcheck // best-effort output
	case "isolated":
		fmt.Fprintf(m.output, "  Network:  isolated (%d allowed domains)\n", len(state.networkAllow)) //nolint:errcheck // best-effort output
	}
	if len(state.ports) > 0 {
		fmt.Fprintf(m.output, "  Ports:    %s\n", strings.Join(state.ports, ", ")) //nolint:errcheck // best-effort output
	}
	fmt.Fprintln(m.output) //nolint:errcheck // best-effort output

	if autoAttach {
		return
	}

	if state.hasPrompt {
		fmt.Fprintf(m.output, "Run 'yoloai attach %s' to interact (Ctrl-b d to detach)\n", state.name) //nolint:errcheck // best-effort output
		fmt.Fprintf(m.output, "    'yoloai diff %s' when done\n", state.name)                          //nolint:errcheck // best-effort output
	} else {
		fmt.Fprintf(m.output, "Run 'yoloai attach %s' to start working (Ctrl-b d to detach)\n", state.name) //nolint:errcheck // best-effort output
	}
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
		if os.Getenv(envVar) != "" || configEnv[envVar] != "" {
			return prefix + model
		}
	}
	return model
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

// effectiveUID returns the real host user's UID, accounting for sudo.
// When running as root via sudo, os.Getuid() returns 0, but the Docker
// entrypoint uses host_uid to remap the yoloai user — passing 0 causes
// usermod to fail (uid 0 is already root). sudo sets SUDO_UID in the
// child's environment so we can recover the original user's uid.
func effectiveUID() int {
	if uid := fileutil.SudoUID(); uid != -1 {
		return uid
	}
	return os.Getuid()
}

// effectiveGID returns the real host user's GID, accounting for sudo.
func effectiveGID() int {
	if gid := fileutil.SudoGID(); gid != -1 {
		return gid
	}
	return os.Getgid()
}

// buildContainerConfig creates the config.json content.
func buildContainerConfig(agentDef *agent.Definition, agentCommand string, tmuxConf string, workingDir string, debug bool, networkIsolated bool, allowedDomains []string, passthrough []string, overlayMounts []overlayMountConfig, setupCommands []string, autoCommitInterval int, copyDirs []string, sandboxName string, tmuxSocket string) ([]byte, error) {
	var stateDirName string
	if agentDef.StateDir != "" {
		stateDirName = filepath.Base(agentDef.StateDir)
	}
	cfg := containerConfig{
		HostUID:            effectiveUID(),
		HostGID:            effectiveGID(),
		AgentCommand:       agentCommand,
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
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// ReadPrompt reads the prompt from --prompt, --prompt-file, or stdin ("-").
func ReadPrompt(prompt, promptFile string) (string, error) {
	if prompt != "" && promptFile != "" {
		return "", NewUsageError("--prompt and --prompt-file are mutually exclusive")
	}

	if prompt == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("read prompt from stdin: %w", err)
		}
		return strings.TrimSpace(string(data)), nil
	}

	if prompt != "" {
		return prompt, nil
	}

	if promptFile == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("read prompt from stdin: %w", err)
		}
		return strings.TrimSpace(string(data)), nil
	}

	if promptFile != "" {
		promptFile, err := ExpandPath(promptFile)
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
		result = append(result, runtime.PortMapping{
			HostPort:     parts[0],
			InstancePort: parts[1],
			Protocol:     "tcp",
		})
	}

	return result, nil
}

// createSecretsDir creates a temp directory with one file per env var / API key.
// Env vars are written first; API keys overwrite on conflict (take precedence).
// credOverrides contains sudo-recovered credential defaults for keys absent from
// os.Environ; they are used as a fallback so that creation under sudo sees credentials.
// Returns empty string if nothing was written.
func createSecretsDir(agentDef *agent.Definition, envVars map[string]string, security string, credOverrides map[string]string) (string, error) {
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
		value := os.Getenv(key)
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

// sudoParentEnv returns env vars from the parent sudo process when yoloai is
// run via sudo. sudo strips most env vars before exec'ing the child, but the
// sudo process itself inherits the full user environment. Reading the parent's
// /proc/<ppid>/environ recovers vars like CLAUDE_CODE_OAUTH_TOKEN and
// ANTHROPIC_API_KEY that were stripped. Returns an empty map if not running
// under sudo or if the parent environ cannot be read.
func sudoParentEnv() map[string]string {
	result := make(map[string]string)
	if os.Getuid() != 0 || os.Getenv("SUDO_USER") == "" {
		return result
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", os.Getppid())) //nolint:gosec // G304: reading parent proc environ to recover sudo-stripped env vars
	if err != nil {
		return result
	}
	for _, kv := range strings.Split(string(data), "\x00") {
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

	// Work directory
	switch state.workdir.Mode {
	case "copy":
		mounts = append(mounts, runtime.MountSpec{
			Source: state.workCopyDir,
			Target: state.workdir.ResolvedMountPath(),
		})
	case "overlay":
		encoded := EncodePath(state.workdir.Path)
		// Mount the entire overlay work base dir (upper/ovlwork/merged/lower) as
		// a single bind mount so upper and ovlwork share the same underlying Docker
		// volume — a kernel requirement for overlayfs to work inside a container.
		// The user's workdir is then nested on top as a read-only bind mount at
		// the lower/ subdirectory within the same volume.
		mounts = append(mounts,
			runtime.MountSpec{
				Source: OverlayWorkBaseDir(state.name, state.workdir.Path),
				Target: "/yoloai/overlay/" + encoded,
			},
			runtime.MountSpec{
				Source:   state.workdir.Path,
				Target:   "/yoloai/overlay/" + encoded + "/lower",
				ReadOnly: true,
			},
		)
	default:
		mounts = append(mounts, runtime.MountSpec{
			Source:   state.workdir.Path,
			Target:   state.workdir.ResolvedMountPath(),
			ReadOnly: state.workdir.Mode != "rw",
		})
	}

	// Auxiliary directories
	for _, ad := range state.auxDirs {
		mountTarget := ad.ResolvedMountPath()
		switch ad.Mode {
		case "copy":
			mounts = append(mounts, runtime.MountSpec{
				Source: WorkDir(state.name, ad.Path),
				Target: mountTarget,
			})
		case "overlay":
			encoded := EncodePath(ad.Path)
			mounts = append(mounts,
				runtime.MountSpec{
					Source: OverlayWorkBaseDir(state.name, ad.Path),
					Target: "/yoloai/overlay/" + encoded,
				},
				runtime.MountSpec{
					Source:   ad.Path,
					Target:   "/yoloai/overlay/" + encoded + "/lower",
					ReadOnly: true,
				},
			)
		case "rw":
			mounts = append(mounts, runtime.MountSpec{
				Source: ad.Path,
				Target: mountTarget,
			})
		default: // read-only (empty mode or explicit "ro")
			mounts = append(mounts, runtime.MountSpec{
				Source:   ad.Path,
				Target:   mountTarget,
				ReadOnly: true,
			})
		}
	}

	// Agent runtime directory (agent's own managed state)
	if state.agent.StateDir != "" {
		mounts = append(mounts, runtime.MountSpec{
			Source: filepath.Join(state.sandboxDir, AgentRuntimeDir),
			Target: state.agent.StateDir,
		})
	}

	// Structured log directory (cli.jsonl, sandbox.jsonl, monitor.jsonl, agent-hooks.jsonl, agent.log)
	mounts = append(mounts, runtime.MountSpec{
		Source: filepath.Join(state.sandboxDir, LogsDir),
		Target: "/yoloai/" + LogsDir,
	})

	// Agent status file (for in-container status monitor)
	mounts = append(mounts, runtime.MountSpec{
		Source: filepath.Join(state.sandboxDir, AgentStatusFile),
		Target: "/yoloai/" + AgentStatusFile,
	})

	// Prompt file
	if state.hasPrompt {
		promptSource := filepath.Join(state.sandboxDir, "prompt.txt")
		if state.promptSourcePath != "" {
			promptSource = state.promptSourcePath
		}
		mounts = append(mounts, runtime.MountSpec{
			Source:   promptSource,
			Target:   "/yoloai/prompt.txt",
			ReadOnly: true,
		})
	}

	// Runtime config file
	mounts = append(mounts, runtime.MountSpec{
		Source:   filepath.Join(state.sandboxDir, RuntimeConfigFile),
		Target:   "/yoloai/" + RuntimeConfigFile,
		ReadOnly: true,
	})

	// File exchange directory
	mounts = append(mounts, runtime.MountSpec{
		Source: filepath.Join(state.sandboxDir, "files"),
		Target: "/yoloai/files",
	})

	// Cache directory
	mounts = append(mounts, runtime.MountSpec{
		Source: filepath.Join(state.sandboxDir, "cache"),
		Target: "/yoloai/cache",
	})

	// Home-seed files and directories (mounted into /home/yoloai/)
	mountedDirs := map[string]bool{}
	for _, sf := range state.agent.SeedFiles {
		if !sf.HomeDir {
			continue
		}
		// For nested paths (e.g., ".claude/settings.json"), mount the
		// top-level directory once rather than individual files. This lets
		// agents create new state files at runtime.
		if strings.Contains(sf.TargetPath, "/") {
			topDir := strings.SplitN(sf.TargetPath, "/", 2)[0]
			if mountedDirs[topDir] {
				continue
			}
			src := filepath.Join(state.sandboxDir, "home-seed", topDir)
			if _, err := os.Stat(src); err != nil {
				continue
			}
			mounts = append(mounts, runtime.MountSpec{
				Source: src,
				Target: "/home/yoloai/" + topDir,
			})
			mountedDirs[topDir] = true
		} else {
			src := filepath.Join(state.sandboxDir, "home-seed", sf.TargetPath)
			if _, err := os.Stat(src); err != nil {
				continue // skip if not seeded
			}
			mounts = append(mounts, runtime.MountSpec{
				Source: src,
				Target: "/home/yoloai/" + sf.TargetPath,
			})
		}
	}

	// Defaults tmux config: mount defaults/tmux.conf over the image-baked copy
	// when the file exists and the mode uses the baked-in config.
	// This lets users customize the config by editing ~/.yoloai/defaults/tmux.conf.
	if state.tmuxConf == "default" || state.tmuxConf == "default+host" {
		defaultsTmuxConf := filepath.Join(config.DefaultsDir(), "tmux.conf")
		if _, err := os.Stat(defaultsTmuxConf); err == nil {
			// Ensure the file is world-readable (0644). It may have been written
			// with 0600 by older yoloai versions. Inside Kata VMs the file is
			// mounted via virtiofs retaining its host uid, but the yoloai user
			// inside the VM (uid 1001) differs from the host user's uid, so
			// a 0600 file causes tmux to fail reading its config and enter
			// copy-mode — preventing send-keys from reaching the shell.
			_ = os.Chmod(defaultsTmuxConf, 0644) //nolint:gosec // G302: tmux.conf contains no secrets
			mounts = append(mounts, runtime.MountSpec{
				Source:   defaultsTmuxConf,
				Target:   "/yoloai/tmux/tmux.conf",
				ReadOnly: true,
			})
		}
	}

	// Host tmux config (when tmux_conf is default+host or host)
	if state.tmuxConf == "default+host" || state.tmuxConf == "host" {
		tmuxConfPath := ExpandTilde("~/.tmux.conf")
		if _, err := os.Stat(tmuxConfPath); err == nil {
			mounts = append(mounts, runtime.MountSpec{
				Source:   tmuxConfPath,
				Target:   "/home/yoloai/.tmux.conf",
				ReadOnly: true,
			})
		}
	}

	// Git identity: mount ~/.gitconfig and ~/.config/git/ read-only so that
	// git commands inside the container can resolve user.name / user.email.
	// Mirrors the symlink-based approach used by the Seatbelt backend.
	gitconfigPath := ExpandTilde("~/.gitconfig")
	if _, err := os.Stat(gitconfigPath); err == nil {
		mounts = append(mounts, runtime.MountSpec{
			Source:   gitconfigPath,
			Target:   "/home/yoloai/.gitconfig",
			ReadOnly: true,
		})
	}
	gitConfigDir := ExpandTilde("~/.config/git")
	if info, err := os.Stat(gitConfigDir); err == nil && info.IsDir() {
		mounts = append(mounts, runtime.MountSpec{
			Source:   gitConfigDir,
			Target:   "/home/yoloai/.config/git",
			ReadOnly: true,
		})
	}

	// Config/profile mounts (host:container[:ro])
	for _, m := range state.configMounts {
		spec, err := parseConfigMount(m)
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
			Source:   secretsDir,
			Target:   "/run/secrets",
			ReadOnly: true,
		})
	}

	return mounts
}

// overlayOrResolvedMountPath returns the container working directory path for a directory.
// For overlay mode, this is the bind-mounted merged path; otherwise the resolved mount path.
func overlayOrResolvedMountPath(d *DirArg) string {
	if d.Mode == "overlay" {
		return "/yoloai/overlay/" + EncodePath(d.Path) + "/merged"
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
		if os.Getenv(key) != "" || credOverrides[key] != "" {
			return true
		}
	}
	return false
}

// hasAnyAuthFile returns true if any auth-only seed files exist on disk
// or can be read from the macOS Keychain.
func hasAnyAuthFile(agentDef *agent.Definition) bool {
	for _, sf := range agentDef.SeedFiles {
		if sf.AuthOnly {
			if _, err := os.Stat(ExpandTilde(sf.HostPath)); err == nil {
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
		if os.Getenv(key) != "" || credOverrides[key] != "" {
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
func copySeedFiles(agentDef *agent.Definition, sandboxDir string, hasAPIKey bool) (bool, error) {
	copiedAuth := false
	agentStateDir := filepath.Join(sandboxDir, AgentRuntimeDir)
	homeSeedDir := filepath.Join(sandboxDir, "home-seed")

	for _, sf := range agentDef.SeedFiles {
		if sf.AuthOnly {
			if len(sf.OwnerAPIKeys) > 0 {
				// Per-file API key check (used by shell agent)
				skip := false
				for _, key := range sf.OwnerAPIKeys {
					if os.Getenv(key) != "" {
						skip = true
						break
					}
				}
				if skip {
					continue
				}
			} else if hasAPIKey {
				continue // auth file not needed when API key is set
			}
		}

		hostPath := ExpandTilde(sf.HostPath)

		var data []byte
		if _, err := os.Stat(hostPath); err == nil {
			fileData, readErr := os.ReadFile(hostPath) //nolint:gosec // G304: path is from agent definition, not user input
			if readErr != nil {
				return copiedAuth, fmt.Errorf("read %s: %w", hostPath, readErr)
			}
			data = fileData
		} else if sf.KeychainService != "" {
			keychainData, keychainErr := keychainReader(sf.KeychainService)
			if keychainErr != nil {
				continue // neither file nor keychain available
			}
			data = keychainData
		} else {
			continue // skip missing files
		}

		baseDir := agentStateDir
		if sf.HomeDir {
			baseDir = homeSeedDir
		}
		targetPath := filepath.Join(baseDir, sf.TargetPath)

		// Ensure parent directory exists
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

// ensureContainerSettings merges required container settings into agent-state/settings.json.
// Agent-specific adjustments are driven by each agent's ApplySettings field.
// Shell agents (SeedsAllAgents=true) apply each real agent's settings into
// home-seed subdirectories instead.
func ensureContainerSettings(agentDef *agent.Definition, sandboxDir, isolation string) error {
	if agentDef.SeedsAllAgents {
		return ensureShellContainerSettings(sandboxDir, isolation)
	}

	if agentDef.StateDir == "" || agentDef.ApplySettings == nil {
		return nil
	}

	// Use restrictive permissions by default, world-writable only for container-enhanced (gVisor)
	perms := Perms(isolation)

	agentStateDir := filepath.Join(sandboxDir, AgentRuntimeDir)
	if err := mkdirAllPerm(agentStateDir, perms.Dir); err != nil {
		return fmt.Errorf("create %s dir: %w", AgentRuntimeDir, err)
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
func ensureShellContainerSettings(sandboxDir string, _ string) error {
	for _, name := range agent.RealAgents() {
		def := agent.GetAgent(name)
		if def.StateDir == "" || def.ApplySettings == nil {
			continue
		}
		dirBase := filepath.Base(def.StateDir)
		dirPath := filepath.Join(sandboxDir, "home-seed", dirBase)
		settingsPath := filepath.Join(dirPath, "settings.json")

		if err := os.MkdirAll(dirPath, 0750); err != nil {
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

// ensureHomeSeedConfig patches home-seed/.claude.json to set installMethod to
// "npm-global". The host file typically has "native" since the user's local
// Claude Code uses the native installer, but inside the container we install
// via npm. Without this fix Claude Code shows spurious warnings about missing
// ~/.local/bin/claude and PATH misconfiguration.
func ensureHomeSeedConfig(agentDef *agent.Definition, sandboxDir string) error {
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

	config["installMethod"] = "npm-global"

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

// hasOverlayDirs returns true if any directory in the sandbox state uses overlay mode.
func hasOverlayDirs(state *sandboxState) bool {
	if state.workdir.Mode == "overlay" {
		return true
	}
	for _, ad := range state.auxDirs {
		if ad.Mode == "overlay" {
			return true
		}
	}
	return false
}

// parseConfigMount parses a "host:container[:ro]" mount string into a MountSpec.
// The host path is expanded (tilde and ${VAR}).
func parseConfigMount(s string) (runtime.MountSpec, error) {
	parts := strings.SplitN(s, ":", 3)
	if len(parts) < 2 {
		return runtime.MountSpec{}, fmt.Errorf("expected host:container[:ro] format")
	}

	hostPath, err := ExpandPath(parts[0])
	if err != nil {
		return runtime.MountSpec{}, fmt.Errorf("expand host path: %w", err)
	}

	spec := runtime.MountSpec{
		Source: hostPath,
		Target: parts[1],
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

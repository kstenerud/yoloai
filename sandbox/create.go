package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kstenerud/yoloai/agent"
	"github.com/kstenerud/yoloai/config"
	"github.com/kstenerud/yoloai/runtime"
)

// CreateOptions holds all parameters for sandbox creation.
type CreateOptions struct {
	Name            string
	WorkdirArg      string            // raw workdir argument (path with optional :copy/:rw/:force suffixes)
	Agent           string            // agent name (e.g., "claude", "test")
	Model           string            // model name or alias (e.g., "sonnet", "claude-sonnet-4-latest")
	Profile         string            // profile name (from --profile flag)
	Prompt          string            // prompt text (from --prompt)
	PromptFile      string            // prompt file path (from --prompt-file)
	NetworkNone     bool              // --network-none flag
	NetworkIsolated bool              // --network-isolated flag
	NetworkAllow    []string          // --network-allow flags
	Ports           []string          // --port flags (e.g., ["3000:3000"])
	Replace         bool              // --replace flag (safe: errors if unapplied work exists)
	Force           bool              // --force flag (unconditional replace, skips safety check)
	NoStart         bool              // --no-start flag
	Yes             bool              // --yes flag (skip confirmations)
	AuxDirArgs      []string          // raw -d arguments (path with optional :copy/:rw/:force/=mount suffixes)
	Passthrough     []string          // args after -- passed to agent
	Version         string            // yoloAI version for meta.json
	Attach          bool              // --attach flag (auto-attach after creation)
	Debug           bool              // --debug flag (enable entrypoint debug logging)
	CPUs            string            // --cpus flag (e.g., "4", "2.5")
	Memory          string            // --memory flag (e.g., "8g", "512m")
	Env             map[string]string // --env flags (KEY=VAL pairs)
}

// sandboxState holds resolved state computed during preparation.
type sandboxState struct {
	name             string
	sandboxDir       string
	workdir          *DirArg
	workCopyDir      string
	auxDirs          []*DirArg
	agent            *agent.Definition
	model            string
	profile          string
	imageRef         string
	env              map[string]string // merged env (base + profile chain)
	hasPrompt        bool
	promptSourcePath string // overrides default prompt.txt path for /yoloai/prompt.txt mount
	networkMode      string
	networkAllow     []string
	ports            []string
	configMounts     []string // extra bind mounts from config/profile (host:container[:ro])
	tmuxConf         string
	resources        *config.ResourceLimits
	capAdd           []string // Linux capabilities from config/profile
	devices          []string // host devices from config/profile
	setup            []string // setup commands from config/profile
	meta             *Meta
	configJSON       []byte
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
}

// Create creates and optionally starts a new sandbox.
// Returns the sandbox name on success (empty if user cancelled or no-start).
func (m *Manager) Create(ctx context.Context, opts CreateOptions) (string, error) {
	if err := m.EnsureSetup(ctx); err != nil {
		return "", err
	}

	state, err := m.prepareSandboxState(ctx, opts)
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
		// Clean up sandbox directory and attempt container removal
		_ = os.RemoveAll(state.sandboxDir)
		_ = m.runtime.Remove(ctx, InstanceName(state.name))
		return "", err
	}

	m.printCreationOutput(state, opts.Attach)
	return state.name, nil
}

// checkUnappliedWork checks if the named sandbox has any unapplied work
// (uncommitted changes or commits beyond the baseline). Returns an error
// if work would be lost.
func checkUnappliedWork(name string) error {
	meta, err := LoadMeta(Dir(name))
	if err != nil {
		return nil // can't load meta — nothing to protect
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
func (m *Manager) prepareSandboxState(ctx context.Context, opts CreateOptions) (*sandboxState, error) {
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
	applyConfigDefaults(&opts, ycfg, pr)

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
			if err := m.Destroy(ctx, opts.Name); err != nil {
				return nil, fmt.Errorf("replace existing sandbox: %w", err)
			}
		}
	}

	// Parse and validate directories, auth, safety checks, dirty repo warnings.
	workdir, auxDirs, err := m.parseAndValidateDirs(ctx, opts, agentDef, pr.env, ycfg.Model)
	if err != nil {
		return nil, err
	}
	if workdir == nil {
		return nil, nil // user cancelled
	}

	// Create directory structure
	for _, dir := range []string{
		sandboxDir,
		filepath.Join(sandboxDir, "work"),
		filepath.Join(sandboxDir, AgentRuntimeDir),
		filepath.Join(sandboxDir, "home-seed"),
		filepath.Join(sandboxDir, "files"),
		filepath.Join(sandboxDir, BinDir),
		filepath.Join(sandboxDir, TmuxDir),
		filepath.Join(sandboxDir, BackendDir),
	} {
		if err := os.MkdirAll(dir, 0750); err != nil {
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

	// Copy seed files into agent-state (config, OAuth credentials, etc.)
	hasAPIKey := hasAnyAPIKey(agentDef)
	copiedAuth, err := copySeedFiles(agentDef, sandboxDir, hasAPIKey)
	if err != nil {
		return nil, fmt.Errorf("copy seed files: %w", err)
	}

	// Warn when Claude is using short-lived OAuth credentials instead of a long-lived token.
	// OAuth access tokens expire after ~30 minutes and refresh tokens are single-use,
	// so the host's Claude Code can invalidate the sandbox's copy by refreshing first.
	if agentDef.Name == "claude" && copiedAuth {
		fmt.Fprintln(m.output, "Warning: using OAuth credentials from ~/.claude/.credentials.json")                         //nolint:errcheck // best-effort warning
		fmt.Fprintln(m.output, "  These tokens expire after ~30 minutes and may fail in long-running sessions.")            //nolint:errcheck // best-effort warning
		fmt.Fprintln(m.output, "  For reliable auth, run 'claude setup-token' and export CLAUDE_CODE_OAUTH_TOKEN instead.") //nolint:errcheck // best-effort warning
		fmt.Fprintln(m.output)                                                                                              //nolint:errcheck // best-effort warning
	}

	// Ensure container-required settings (e.g., skip bypass permissions prompt)
	if err := ensureContainerSettings(agentDef, sandboxDir); err != nil {
		return nil, fmt.Errorf("ensure container settings: %w", err)
	}

	// Copy agent_files (user-configured agent config files)
	agentFilesInitialized := false
	if pr.agentFiles != nil && agentDef.StateDir != "" {
		if err := copyAgentFiles(agentDef, sandboxDir, pr.agentFiles); err != nil {
			return nil, fmt.Errorf("copy agent files: %w", err)
		}
		agentFilesInitialized = true
	}

	// Fix install method in seeded .claude.json (host has "native", container uses npm).
	// Skip for seatbelt — it runs the host's native Claude Code, not npm-installed.
	if m.backend != "seatbelt" {
		if err := ensureHomeSeedConfig(agentDef, sandboxDir); err != nil {
			return nil, fmt.Errorf("ensure home seed config: %w", err)
		}
	}

	// Copy/overlay workdir and create git baseline.
	workCopyDir, baselineSHA, err := setupWorkdir(opts.Name, workdir)
	if err != nil {
		return nil, err
	}

	// Copy/overlay aux dirs and create baselines.
	dirMetas, err := setupAuxDirs(opts.Name, auxDirs)
	if err != nil {
		return nil, err
	}

	// For seatbelt, rewrite mount paths for :copy directories to the actual
	// copy location. Docker mounts the copy at the original host path inside
	// the container, but seatbelt runs directly on macOS — the agent sees
	// the copy at its sandbox location, not the original host path.
	if m.backend == "seatbelt" {
		if workdir.Mode == "copy" {
			workdir.MountPath = WorkDir(opts.Name, workdir.Path)
		}
		for _, ad := range auxDirs {
			if ad.Mode == "copy" {
				ad.MountPath = WorkDir(opts.Name, ad.Path)
			}
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

	// Build overlay mount configs for config.json.
	overlayMounts := collectOverlayMounts(workdir, auxDirs)

	// Collect mount paths of all :copy directories for auto-commit loop.
	copyDirs := collectCopyDirs(workdir, auxDirs)

	// Build config.json
	configData, err := buildContainerConfig(agentDef, agentCommand, tmuxConf, workdir.ResolvedMountPath(), opts.Debug, networkMode == "isolated", networkAllow, opts.Passthrough, overlayMounts, pr.setup, pr.autoCommitInterval, copyDirs, opts.Name)
	if err != nil {
		return nil, fmt.Errorf("build %s: %w", RuntimeConfigFile, err)
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
			MountPath:   workdir.ResolvedMountPath(),
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
		if err := os.WriteFile(filepath.Join(sandboxDir, "prompt.txt"), []byte(promptText), 0600); err != nil {
			return nil, fmt.Errorf("write prompt.txt: %w", err)
		}
	}

	if err := os.WriteFile(filepath.Join(sandboxDir, "log.txt"), nil, 0600); err != nil {
		return nil, fmt.Errorf("write log.txt: %w", err)
	}

	if err := os.WriteFile(filepath.Join(sandboxDir, AgentStatusFile), []byte("{}\n"), 0600); err != nil {
		return nil, fmt.Errorf("write %s: %w", AgentStatusFile, err)
	}

	if err := os.WriteFile(filepath.Join(sandboxDir, "monitor.log"), nil, 0600); err != nil {
		return nil, fmt.Errorf("write monitor.log: %w", err)
	}

	if err := os.WriteFile(filepath.Join(sandboxDir, RuntimeConfigFile), configData, 0600); err != nil {
		return nil, fmt.Errorf("write %s: %w", RuntimeConfigFile, err)
	}

	if err := WriteContextFiles(sandboxDir, meta, agentDef); err != nil {
		return nil, fmt.Errorf("write context files: %w", err)
	}

	success = true
	return &sandboxState{
		name:         opts.Name,
		sandboxDir:   sandboxDir,
		workdir:      workdir,
		workCopyDir:  workCopyDir,
		auxDirs:      auxDirs,
		agent:        agentDef,
		model:        model,
		profile:      pr.name,
		imageRef:     pr.imageRef,
		env:          pr.env,
		hasPrompt:    hasPrompt,
		networkMode:  networkMode,
		networkAllow: networkAllow,
		ports:        opts.Ports,
		configMounts: mergedMounts,
		tmuxConf:     tmuxConf,
		resources:    pr.resources,
		capAdd:       pr.capAdd,
		devices:      pr.devices,
		setup:        pr.setup,
		meta:         meta,
		configJSON:   configData,
	}, nil
}

// launchContainer creates a sandbox instance from sandboxState, starts it,
// and cleans up credential temp files. Used by both initial creation and
// recreation from meta.json.
func (m *Manager) launchContainer(ctx context.Context, state *sandboxState) error {
	// Use pre-merged env from state if available, otherwise load from config.
	envVars := state.env
	if envVars == nil {
		cfg, cfgErr := config.LoadConfig()
		if cfgErr != nil {
			return fmt.Errorf("load config: %w", cfgErr)
		}
		envVars = cfg.Env
	}

	secretsDir, err := createSecretsDir(state.agent, envVars)
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

	cname := InstanceName(state.name)

	if state.networkMode == "isolated" && m.backend != "docker" {
		return fmt.Errorf("--network-isolated requires the docker backend (%s does not support domain-based filtering)", m.backend)
	}

	// Map internal network modes to Docker-understood values.
	// "isolated" uses default bridge networking; iptables rules inside the
	// container handle the actual domain-based filtering.
	dockerNetworkMode := state.networkMode
	if dockerNetworkMode == "isolated" {
		dockerNetworkMode = ""
	}

	resolvedImage := state.imageRef
	if resolvedImage == "" {
		resolvedImage = "yoloai-base"
	}

	instanceCfg := runtime.InstanceConfig{
		Name:        cname,
		ImageRef:    resolvedImage,
		WorkingDir:  state.workdir.ResolvedMountPath(),
		Mounts:      mounts,
		Ports:       ports,
		NetworkMode: dockerNetworkMode,
		UseInit:     true,
	}

	// Convert resource limits
	if state.resources != nil {
		rtResources, err := parseResourceLimits(state.resources)
		if err != nil {
			return err
		}
		instanceCfg.Resources = rtResources
	}

	if state.networkMode == "isolated" && m.backend == "docker" {
		instanceCfg.CapAdd = append(instanceCfg.CapAdd, "NET_ADMIN")
	}

	// CAP_SYS_ADMIN required for overlay mounts inside the container
	if hasOverlayDirs(state) {
		if m.backend != "docker" {
			return fmt.Errorf(":overlay mode requires the docker backend (not supported with %s)", m.backend)
		}
		instanceCfg.CapAdd = append(instanceCfg.CapAdd, "SYS_ADMIN")
	}

	// Recipe fields (cap_add, devices, setup) are Docker-only
	if m.backend != "docker" && (len(state.capAdd) > 0 || len(state.devices) > 0 || len(state.setup) > 0) {
		return fmt.Errorf("cap_add, devices, and setup require the docker backend (not supported with %s)", m.backend)
	}
	instanceCfg.CapAdd = append(instanceCfg.CapAdd, state.capAdd...)
	instanceCfg.Devices = state.devices

	if err := m.runtime.Create(ctx, instanceCfg); err != nil {
		return err
	}

	if err := m.runtime.Start(ctx, cname); err != nil {
		return fmt.Errorf("start instance: %w", err)
	}

	// Wait briefly for entrypoint to read secrets before cleanup
	if secretsDir != "" {
		time.Sleep(1 * time.Second)
	}

	// Verify instance is still running (catches immediate crashes)
	time.Sleep(1 * time.Second)
	info, err := m.runtime.Inspect(ctx, cname)
	if err != nil {
		return fmt.Errorf("inspect instance after start: %w", err)
	}
	if !info.Running {
		logPath := filepath.Join(state.sandboxDir, "log.txt")
		if tail := readLogTail(logPath, 20); tail != "" {
			return fmt.Errorf("instance exited immediately:\n%s", tail)
		}
		return fmt.Errorf("instance exited immediately — %s", m.runtime.DiagHint(cname))
	}

	return nil
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

// buildContainerConfig creates the config.json content.
func buildContainerConfig(agentDef *agent.Definition, agentCommand string, tmuxConf string, workingDir string, debug bool, networkIsolated bool, allowedDomains []string, passthrough []string, overlayMounts []overlayMountConfig, setupCommands []string, autoCommitInterval int, copyDirs []string, sandboxName string) ([]byte, error) {
	var stateDirName string
	if agentDef.StateDir != "" {
		stateDirName = filepath.Base(agentDef.StateDir)
	}
	cfg := containerConfig{
		HostUID:            os.Getuid(),
		HostGID:            os.Getgid(),
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
// Returns empty string if nothing was written.
func createSecretsDir(agentDef *agent.Definition, envVars map[string]string) (string, error) {
	if len(agentDef.APIKeyEnvVars) == 0 && len(agentDef.AuthHintEnvVars) == 0 && len(envVars) == 0 {
		return "", nil
	}

	tmpDir, err := os.MkdirTemp("", "yoloai-secrets-*")
	if err != nil {
		return "", fmt.Errorf("create secrets temp dir: %w", err)
	}

	wrote := false

	// Write env vars first
	for k, v := range envVars {
		if err := os.WriteFile(filepath.Join(tmpDir, k), []byte(v), 0600); err != nil {
			_ = os.RemoveAll(tmpDir)
			return "", fmt.Errorf("write env %s: %w", k, err)
		}
		wrote = true
	}

	// Write host env vars for API keys and auth hints (overwrites config env on conflict)
	for _, key := range append(agentDef.APIKeyEnvVars, agentDef.AuthHintEnvVars...) {
		value := os.Getenv(key)
		if value == "" {
			continue
		}
		if err := os.WriteFile(filepath.Join(tmpDir, key), []byte(value), 0600); err != nil { //nolint:gosec // G703: key is from agent definition, not user input
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
		mounts = append(mounts,
			runtime.MountSpec{
				Source:   state.workdir.Path,
				Target:   "/yoloai/overlay/" + encoded + "/lower",
				ReadOnly: true,
			},
			runtime.MountSpec{
				Source: OverlayUpperDir(state.name, state.workdir.Path),
				Target: "/yoloai/overlay/" + encoded + "/upper",
			},
			runtime.MountSpec{
				Source: OverlayOvlworkDir(state.name, state.workdir.Path),
				Target: "/yoloai/overlay/" + encoded + "/ovlwork",
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
					Source:   ad.Path,
					Target:   "/yoloai/overlay/" + encoded + "/lower",
					ReadOnly: true,
				},
				runtime.MountSpec{
					Source: OverlayUpperDir(state.name, ad.Path),
					Target: "/yoloai/overlay/" + encoded + "/upper",
				},
				runtime.MountSpec{
					Source: OverlayOvlworkDir(state.name, ad.Path),
					Target: "/yoloai/overlay/" + encoded + "/ovlwork",
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

	// Log file
	mounts = append(mounts, runtime.MountSpec{
		Source: filepath.Join(state.sandboxDir, "log.txt"),
		Target: "/yoloai/log.txt",
	})

	// Agent status file (for in-container status monitor)
	mounts = append(mounts, runtime.MountSpec{
		Source: filepath.Join(state.sandboxDir, AgentStatusFile),
		Target: "/yoloai/" + AgentStatusFile,
	})

	// Monitor debug log (written by status-monitor.py when --debug is set)
	mounts = append(mounts, runtime.MountSpec{
		Source: filepath.Join(state.sandboxDir, "monitor.log"),
		Target: "/yoloai/monitor.log",
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

	// Config/profile mounts (host:container[:ro])
	for _, m := range state.configMounts {
		spec, err := parseConfigMount(m)
		if err != nil {
			continue // skip unparseable mounts (validated at creation time)
		}
		mounts = append(mounts, spec)
	}

	// Secrets (env vars + API keys)
	if secretsDir != "" {
		entries, _ := os.ReadDir(secretsDir)
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			mounts = append(mounts, runtime.MountSpec{
				Source:   filepath.Join(secretsDir, e.Name()),
				Target:   filepath.Join("/run/secrets", e.Name()),
				ReadOnly: true,
			})
		}
	}

	return mounts
}

// hasAnyAPIKey returns true if any of the agent's required API key env vars are set.
func hasAnyAPIKey(agentDef *agent.Definition) bool {
	if len(agentDef.APIKeyEnvVars) == 0 {
		return true // no API key required
	}
	for _, key := range agentDef.APIKeyEnvVars {
		if os.Getenv(key) != "" {
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
// in the host environment or in the config env map. This allows agents like
// aider to work with local model servers (Ollama, LM Studio) without a cloud API key.
func hasAnyAuthHint(agentDef *agent.Definition, configEnv map[string]string) bool {
	for _, key := range agentDef.AuthHintEnvVars {
		if os.Getenv(key) != "" {
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
		if err := os.MkdirAll(filepath.Dir(targetPath), 0750); err != nil {
			return copiedAuth, fmt.Errorf("create dir for %s: %w", sf.TargetPath, err)
		}

		if err := os.WriteFile(targetPath, data, 0600); err != nil {
			return copiedAuth, fmt.Errorf("write %s: %w", targetPath, err)
		}
		if sf.AuthOnly {
			copiedAuth = true
		}
	}

	return copiedAuth, nil
}

// ensureContainerSettings merges required container settings into agent-state/settings.json.
// Agent-specific adjustments:
//   - Claude Code: skip --dangerously-skip-permissions prompt, disable nested sandbox-exec.
//   - Gemini CLI: disable folder-trust prompt (the container IS the sandbox).
//   - Shell: apply each real agent's settings into home-seed subdirectories.
func ensureContainerSettings(agentDef *agent.Definition, sandboxDir string) error {
	if agentDef.Name == "shell" {
		return ensureShellContainerSettings(sandboxDir)
	}

	if agentDef.StateDir == "" {
		return nil
	}

	agentStateDir := filepath.Join(sandboxDir, AgentRuntimeDir)
	if err := os.MkdirAll(agentStateDir, 0750); err != nil {
		return fmt.Errorf("create %s dir: %w", AgentRuntimeDir, err)
	}
	settingsPath := filepath.Join(agentStateDir, "settings.json")

	switch agentDef.Name {
	case "claude":
		settings, err := readJSONMap(settingsPath)
		if err != nil {
			return err
		}
		settings["skipDangerousModePermissionPrompt"] = true
		// Disable Claude Code's built-in sandbox-exec to prevent nesting failures.
		// sandbox-exec cannot be nested — an inner sandbox-exec inherits the outer
		// profile's restrictions and typically fails.
		settings["sandbox"] = map[string]interface{}{"enabled": false}
		// Ensure Claude Code emits BEL for tmux tab highlighting
		settings["preferredNotifChannel"] = "terminal_bell"
		// Inject hooks for status tracking. Claude Code's own hook system is
		// far more reliable than polling tmux capture-pane for a ready pattern.
		injectIdleHook(settings)
		return writeJSONMap(settingsPath, settings)

	case "gemini":
		settings, err := readJSONMap(settingsPath)
		if err != nil {
			return err
		}
		// Preserve existing security settings (e.g. auth.selectedType) while
		// disabling folder trust — the container is already sandboxed.
		security, _ := settings["security"].(map[string]interface{})
		if security == nil {
			security = map[string]interface{}{}
		}
		security["folderTrust"] = map[string]interface{}{"enabled": false}
		settings["security"] = security
		return writeJSONMap(settingsPath, settings)

	default:
		return nil
	}
}

// ensureShellContainerSettings applies each real agent's container settings
// to its home-seed subdirectory (e.g., home-seed/.claude/settings.json).
func ensureShellContainerSettings(sandboxDir string) error {
	for _, name := range agent.RealAgents() {
		def := agent.GetAgent(name)
		if def.StateDir == "" {
			continue
		}
		dirBase := filepath.Base(def.StateDir)
		settingsPath := filepath.Join(sandboxDir, "home-seed", dirBase, "settings.json")

		switch name {
		case "claude":
			settings, err := readJSONMap(settingsPath)
			if err != nil {
				return err
			}
			settings["skipDangerousModePermissionPrompt"] = true
			settings["sandbox"] = map[string]interface{}{"enabled": false}
			injectIdleHook(settings)
			if err := writeJSONMap(settingsPath, settings); err != nil {
				return err
			}

		case "gemini":
			settings, err := readJSONMap(settingsPath)
			if err != nil {
				return err
			}
			security, _ := settings["security"].(map[string]interface{})
			if security == nil {
				security = map[string]interface{}{}
			}
			security["folderTrust"] = map[string]interface{}{"enabled": false}
			settings["security"] = security
			if err := writeJSONMap(settingsPath, settings); err != nil {
				return err
			}
		}
	}
	return nil
}

// statusIdleCommand writes idle status to agent-status.json when Claude
// finishes a response (Notification hook). Uses $YOLOAI_DIR for portability
// across backends (Docker=/yoloai, seatbelt=sandbox dir).
const statusIdleCommand = `printf '{"status":"idle","exit_code":null,"timestamp":%d}\n' "$(date +%s)" > "${YOLOAI_DIR:-/yoloai}/agent-status.json"`

// statusActiveCommand writes active status to agent-status.json when Claude
// starts working (PreToolUse hook). This ensures the title updates from "> name"
// back to "name" when the user submits a new prompt.
const statusActiveCommand = `printf '{"status":"active","exit_code":null,"timestamp":%d}\n' "$(date +%s)" > "${YOLOAI_DIR:-/yoloai}/agent-status.json"`

// injectIdleHook merges hooks into Claude Code's settings map for status tracking.
// Notification → idle (turn complete), PreToolUse → running (work started).
// Preserves any existing hooks the user may have configured.
func injectIdleHook(settings map[string]any) {
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}

	// Notification hook: mark idle when Claude finishes a response.
	idleHook := map[string]any{
		"type":    "command",
		"command": statusIdleCommand,
	}
	idleGroup := map[string]any{
		"hooks": []any{idleHook},
	}
	existingNotif, _ := hooks["Notification"].([]any)
	hooks["Notification"] = append(existingNotif, idleGroup)

	// PreToolUse hook: mark active when Claude starts using tools.
	activeHook := map[string]any{
		"type":    "command",
		"command": statusActiveCommand,
	}
	activeGroup := map[string]any{
		"hooks": []any{activeHook},
	}
	existingPre, _ := hooks["PreToolUse"].([]any)
	hooks["PreToolUse"] = append(existingPre, activeGroup)

	settings["hooks"] = hooks
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

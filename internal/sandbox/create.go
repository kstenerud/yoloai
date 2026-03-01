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

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/runtime"
)

// CreateOptions holds all parameters for sandbox creation.
type CreateOptions struct {
	Name            string
	WorkdirArg      string   // raw workdir argument (path with optional :copy/:rw/:force suffixes)
	Agent           string   // agent name (e.g., "claude", "test")
	Model           string   // model name or alias (e.g., "sonnet", "claude-sonnet-4-latest")
	Profile         string   // profile name (from --profile flag)
	Prompt          string   // prompt text (from --prompt)
	PromptFile      string   // prompt file path (from --prompt-file)
	NetworkNone     bool     // --network-none flag
	NetworkIsolated bool     // --network-isolated flag
	NetworkAllow    []string // --network-allow flags
	Ports           []string // --port flags (e.g., ["3000:3000"])
	Replace         bool     // --replace flag
	NoStart         bool     // --no-start flag
	Yes             bool     // --yes flag (skip confirmations)
	AuxDirArgs      []string // raw -d arguments (path with optional :copy/:rw/:force/=mount suffixes)
	Passthrough     []string // args after -- passed to agent
	Version         string   // yoloAI version for meta.json
	Attach          bool     // --attach flag (auto-attach after creation)
	Debug           bool     // --debug flag (enable entrypoint debug logging)
	CPUs            string   // --cpus flag (e.g., "4", "2.5")
	Memory          string   // --memory flag (e.g., "8g", "512m")
}

// sandboxState holds resolved state computed during preparation.
type sandboxState struct {
	name         string
	sandboxDir   string
	workdir      *DirArg
	workCopyDir  string
	auxDirs      []*DirArg
	agent        *agent.Definition
	model        string
	profile      string
	imageRef     string
	env          map[string]string // merged env (base + profile chain)
	hasPrompt    bool
	networkMode  string
	networkAllow []string
	ports        []string
	tmuxConf     string
	resources    *ResourceLimits
	meta         *Meta
	configJSON   []byte
}

// containerConfig is the serializable form of /yoloai/config.json.
type containerConfig struct {
	HostUID         int      `json:"host_uid"`
	HostGID         int      `json:"host_gid"`
	AgentCommand    string   `json:"agent_command"`
	StartupDelay    int      `json:"startup_delay"`
	ReadyPattern    string   `json:"ready_pattern"`
	SubmitSequence  string   `json:"submit_sequence"`
	TmuxConf        string   `json:"tmux_conf"`
	WorkingDir      string   `json:"working_dir"`
	StateDirName    string   `json:"state_dir_name"`
	Debug           bool     `json:"debug,omitempty"`
	NetworkIsolated bool     `json:"network_isolated,omitempty"`
	AllowedDomains  []string `json:"allowed_domains,omitempty"`
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
	ycfg, err := LoadConfig()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	// Profile resolution: load profile chain, merge config, resolve image.
	var profileName, imageRef string
	var resources *ResourceLimits
	mergedEnv := ycfg.Env
	if opts.Profile != "" {
		if err := ValidateProfileName(opts.Profile); err != nil {
			return nil, err
		}
		chain, chainErr := ResolveProfileChain(opts.Profile)
		if chainErr != nil {
			return nil, chainErr
		}
		merged, mergeErr := MergeProfileChain(ycfg, chain)
		if mergeErr != nil {
			return nil, fmt.Errorf("merge profile chain: %w", mergeErr)
		}
		if err := ValidateProfileBackend(merged.Backend, m.backend); err != nil {
			return nil, err
		}

		// Apply merged values where CLI didn't override
		if opts.Agent == ycfg.Agent && merged.Agent != "" {
			opts.Agent = merged.Agent
			agentDef = agent.GetAgent(opts.Agent)
			if agentDef == nil {
				return nil, NewUsageError("unknown agent from profile: %s", opts.Agent)
			}
		}
		if opts.Model == "" && merged.Model != "" {
			opts.Model = merged.Model
		}

		mergedEnv = merged.Env

		if merged.Resources != nil {
			r := *merged.Resources
			resources = &r
		}

		// Profile workdir: use if CLI didn't provide one
		if opts.WorkdirArg == "" && merged.Workdir != nil {
			wdPath, wdErr := ExpandPath(merged.Workdir.Path)
			if wdErr != nil {
				return nil, fmt.Errorf("expand profile workdir path: %w", wdErr)
			}
			suffix := ""
			if merged.Workdir.Mode != "" {
				suffix = ":" + merged.Workdir.Mode
			}
			if merged.Workdir.Mount != "" {
				suffix += "=" + merged.Workdir.Mount
			}
			opts.WorkdirArg = wdPath + suffix
		}

		// Profile directories: prepend before CLI aux dirs
		for _, pd := range merged.Directories {
			dirPath, dirErr := ExpandPath(pd.Path)
			if dirErr != nil {
				return nil, fmt.Errorf("expand profile directory path: %w", dirErr)
			}
			arg := dirPath
			if pd.Mode != "" {
				arg += ":" + pd.Mode
			}
			if pd.Mount != "" {
				arg += "=" + pd.Mount
			}
			opts.AuxDirArgs = append([]string{arg}, opts.AuxDirArgs...)
		}

		// Profile ports: additive
		opts.Ports = append(merged.Ports, opts.Ports...)

		// Network: apply merged config as defaults (CLI flags override later)
		if merged.Network != nil && !opts.NetworkNone && !opts.NetworkIsolated {
			if merged.Network.Isolated {
				opts.NetworkIsolated = true
			}
			opts.NetworkAllow = append(merged.Network.Allow, opts.NetworkAllow...)
		}

		// Resolve image ref
		imageRef = ResolveProfileImage(opts.Profile, chain)
		profileName = opts.Profile

		// Build profile image if needed (Docker only)
		if err := EnsureProfileImage(ctx, m.runtime, opts.Profile, m.backend, m.output, m.logger, false); err != nil {
			return nil, fmt.Errorf("build profile image: %w", err)
		}
	}

	// Resources from base config (if profile didn't set them)
	if resources == nil && ycfg.Resources != nil {
		r := *ycfg.Resources
		resources = &r
	}

	// Network from base config (if profile didn't set it and CLI didn't override)
	if opts.Profile == "" && ycfg.Network != nil && !opts.NetworkNone && !opts.NetworkIsolated {
		if ycfg.Network.Isolated {
			opts.NetworkIsolated = true
		}
		opts.NetworkAllow = append(ycfg.Network.Allow, opts.NetworkAllow...)
	}
	// CLI overrides for resources
	if opts.CPUs != "" {
		if resources == nil {
			resources = &ResourceLimits{}
		}
		resources.CPUs = opts.CPUs
	}
	if opts.Memory != "" {
		if resources == nil {
			resources = &ResourceLimits{}
		}
		resources.Memory = opts.Memory
	}

	// Parse workdir (may have been set from profile above)
	if opts.WorkdirArg == "" {
		return nil, NewUsageError("no workdir specified and no default workdir in profile")
	}
	workdir, err := ParseDirArg(opts.WorkdirArg)
	if err != nil {
		return nil, NewUsageError("invalid workdir: %s", err)
	}
	if workdir.Mode == "" {
		workdir.Mode = "copy"
	}

	if _, err := os.Stat(workdir.Path); err != nil {
		return nil, NewUsageError("workdir does not exist: %s", workdir.Path)
	}

	hasAPIKey := hasAnyAPIKey(agentDef)
	hasAuth := hasAnyAuthFile(agentDef)
	hasAuthHint := hasAnyAuthHint(agentDef, mergedEnv)
	if !hasAPIKey && !hasAuth && !hasAuthHint {
		msg := fmt.Sprintf("no authentication found for %s: set %s",
			agentDef.Name, strings.Join(agentDef.APIKeyEnvVars, "/"))
		if authDesc := describeSeedAuthFiles(agentDef); authDesc != "" {
			msg += fmt.Sprintf(" or provide OAuth credentials (%s)", authDesc)
		}
		if len(agentDef.AuthHintEnvVars) > 0 {
			msg += fmt.Sprintf(", or set %s for local models", strings.Join(agentDef.AuthHintEnvVars, "/"))
		}
		return nil, fmt.Errorf("%s: %w", msg, ErrMissingAPIKey)
	}

	// When auth is only via local model server, a model must be specified
	// so the agent knows which model to use.
	if !hasAPIKey && !hasAuth && hasAuthHint && opts.Model == "" && ycfg.Model == "" {
		return nil, NewUsageError("a model is required when using a local model server: use --model or 'yoloai config set model <model>'")
	}

	// Warn if a local model server URL points to localhost but the backend
	// is containerized — localhost inside a container refers to the container
	// itself, not the host machine.
	if m.backend != "seatbelt" {
		for _, key := range agentDef.AuthHintEnvVars {
			for _, val := range []string{os.Getenv(key), mergedEnv[key]} {
				if val != "" && containsLocalhost(val) {
					hint := "use the host's routable IP instead"
					if m.backend == "docker" {
						hint = "use host.docker.internal instead"
					}
					return nil, NewUsageError("%s contains a localhost address (%s) which won't work inside a %s VM — %s",
						key, val, m.backend, hint)
				}
			}
		}
	}

	// Parse auxiliary directories
	var auxDirs []*DirArg
	for _, auxArg := range opts.AuxDirArgs {
		auxDir, auxErr := ParseDirArg(auxArg)
		if auxErr != nil {
			return nil, NewUsageError("invalid directory %q: %s", auxArg, auxErr)
		}
		// Aux dirs default to read-only (empty mode means "ro")
		if _, auxStatErr := os.Stat(auxDir.Path); auxStatErr != nil {
			return nil, NewUsageError("directory does not exist: %s", auxDir.Path)
		}
		auxDirs = append(auxDirs, auxDir)
	}

	// Safety checks — workdir
	if IsDangerousDir(workdir.Path) {
		if workdir.Force {
			fmt.Fprintf(m.output, "WARNING: mounting dangerous directory %s\n", workdir.Path) //nolint:errcheck // best-effort output
		} else {
			return nil, NewUsageError("refusing to mount dangerous directory %s (use :force to override)", workdir.Path)
		}
	}

	// Safety checks — aux dirs
	for _, ad := range auxDirs {
		if IsDangerousDir(ad.Path) {
			if ad.Force {
				fmt.Fprintf(m.output, "WARNING: mounting dangerous directory %s\n", ad.Path) //nolint:errcheck // best-effort output
			} else {
				return nil, NewUsageError("refusing to mount dangerous directory %s (use :force to override)", ad.Path)
			}
		}
	}

	// Collect all host paths for overlap check
	allPaths := []string{workdir.Path}
	for _, ad := range auxDirs {
		allPaths = append(allPaths, ad.Path)
	}
	if err := CheckPathOverlap(allPaths); err != nil {
		return nil, NewUsageError("%s", err)
	}

	// Check for duplicate container mount paths
	mountPaths := map[string]string{workdir.ResolvedMountPath(): workdir.Path}
	for _, ad := range auxDirs {
		mp := ad.ResolvedMountPath()
		if prev, exists := mountPaths[mp]; exists {
			return nil, NewUsageError("duplicate container mount path %s (from %s and %s)", mp, prev, ad.Path)
		}
		mountPaths[mp] = ad.Path
	}

	// --replace: destroy existing sandbox
	if opts.Replace {
		if _, err := os.Stat(sandboxDir); err == nil {
			if err := m.Destroy(ctx, opts.Name); err != nil {
				return nil, fmt.Errorf("replace existing sandbox: %w", err)
			}
		}
	}

	// Dirty repo warnings (workdir + aux :copy/:rw dirs)
	var dirtyWarnings []string
	if msg, checkErr := CheckDirtyRepo(workdir.Path); checkErr != nil {
		return nil, fmt.Errorf("check repo status: %w", checkErr)
	} else if msg != "" {
		dirtyWarnings = append(dirtyWarnings, fmt.Sprintf("%s: %s", workdir.Path, msg))
	}
	for _, ad := range auxDirs {
		if ad.Mode == "copy" || ad.Mode == "rw" {
			if msg, checkErr := CheckDirtyRepo(ad.Path); checkErr != nil {
				return nil, fmt.Errorf("check repo status: %w", checkErr)
			} else if msg != "" {
				dirtyWarnings = append(dirtyWarnings, fmt.Sprintf("%s: %s", ad.Path, msg))
			}
		}
	}
	if len(dirtyWarnings) > 0 && !opts.Yes {
		for _, w := range dirtyWarnings {
			fmt.Fprintf(m.output, "WARNING: %s has uncommitted changes (%s)\n", strings.SplitN(w, ": ", 2)[0], strings.SplitN(w, ": ", 2)[1]) //nolint:errcheck // best-effort output
		}
		fmt.Fprintln(m.output, "These changes will be visible to the agent and could be modified or lost.") //nolint:errcheck // best-effort output
		confirmed, confirmErr := Confirm(ctx, "Continue? [y/N] ", m.input, m.output)
		if confirmErr != nil {
			return nil, confirmErr
		}
		if !confirmed {
			return nil, nil // user cancelled
		}
	}

	// Create directory structure
	workCopyDir := WorkDir(opts.Name, workdir.Path)
	for _, dir := range []string{
		sandboxDir,
		filepath.Join(sandboxDir, "work"),
		filepath.Join(sandboxDir, "agent-state"),
		filepath.Join(sandboxDir, "home-seed"),
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
	if _, err := copySeedFiles(agentDef, sandboxDir, hasAPIKey); err != nil {
		return nil, fmt.Errorf("copy seed files: %w", err)
	}

	// Ensure container-required settings (e.g., skip bypass permissions prompt)
	if err := ensureContainerSettings(agentDef, sandboxDir); err != nil {
		return nil, fmt.Errorf("ensure container settings: %w", err)
	}

	// Fix install method in seeded .claude.json (host has "native", container uses npm).
	// Skip for seatbelt — it runs the host's native Claude Code, not npm-installed.
	if m.backend != "seatbelt" {
		if err := ensureHomeSeedConfig(agentDef, sandboxDir); err != nil {
			return nil, fmt.Errorf("ensure home seed config: %w", err)
		}
	}

	// Copy workdir
	if workdir.Mode == "copy" {
		if err := copyDir(workdir.Path, workCopyDir); err != nil {
			return nil, fmt.Errorf("copy workdir: %w", err)
		}
	} else {
		if err := os.MkdirAll(workCopyDir, 0750); err != nil {
			return nil, fmt.Errorf("create work dir: %w", err)
		}
	}

	// Strip git metadata from copy before creating fresh baseline
	if workdir.Mode == "copy" {
		if err := removeGitDirs(workCopyDir); err != nil {
			return nil, fmt.Errorf("remove git metadata: %w", err)
		}
	}

	// Git baseline
	var baselineSHA string
	if workdir.Mode == "copy" {
		sha, err := gitBaseline(workCopyDir)
		if err != nil {
			return nil, fmt.Errorf("git baseline: %w", err)
		}
		baselineSHA = sha
	} else {
		sha, _ := gitHeadSHA(workdir.Path)
		baselineSHA = sha
	}

	// Copy and baseline aux dirs
	var dirMetas []DirMeta
	for _, ad := range auxDirs {
		mode := ad.Mode
		if mode == "" {
			mode = "ro"
		}

		dm := DirMeta{
			HostPath:  ad.Path,
			MountPath: ad.ResolvedMountPath(),
			Mode:      mode,
		}

		if ad.Mode == "copy" {
			auxWorkDir := WorkDir(opts.Name, ad.Path)
			if err := copyDir(ad.Path, auxWorkDir); err != nil {
				return nil, fmt.Errorf("copy aux dir %s: %w", ad.Path, err)
			}
			if err := removeGitDirs(auxWorkDir); err != nil {
				return nil, fmt.Errorf("remove git metadata in aux dir %s: %w", ad.Path, err)
			}
			sha, err := gitBaseline(auxWorkDir)
			if err != nil {
				return nil, fmt.Errorf("git baseline for aux dir %s: %w", ad.Path, err)
			}
			dm.BaselineSHA = sha
		}

		dirMetas = append(dirMetas, dm)
	}

	// Read prompt
	promptText, err := readPrompt(opts.Prompt, opts.PromptFile)
	if err != nil {
		return nil, err
	}
	hasPrompt := promptText != ""

	// Resolve model alias and apply provider prefix if needed
	model := resolveModel(agentDef, opts.Model)
	model = applyModelPrefix(agentDef, model, mergedEnv)

	// Build agent command
	agentCommand := buildAgentCommand(agentDef, model, promptText, opts.Passthrough)

	// Read tmux_conf from config (loaded earlier for auth check)
	tmuxConf := ycfg.TmuxConf
	if tmuxConf == "" {
		tmuxConf = "default" // fallback if not set
	}

	// Determine network mode and allowlist
	networkMode := ""
	var networkAllow []string
	if opts.NetworkNone {
		networkMode = "none"
	} else if opts.NetworkIsolated {
		networkMode = "isolated"
		networkAllow = append(networkAllow, agentDef.NetworkAllowlist...)
		networkAllow = append(networkAllow, opts.NetworkAllow...)
	}

	// Build config.json
	configData, err := buildContainerConfig(agentDef, agentCommand, tmuxConf, workdir.ResolvedMountPath(), opts.Debug, networkMode == "isolated", networkAllow)
	if err != nil {
		return nil, fmt.Errorf("build config.json: %w", err)
	}

	// Write state files
	meta := &Meta{
		YoloaiVersion: opts.Version,
		Name:          opts.Name,
		CreatedAt:     time.Now(),
		Backend:       m.backend,
		Profile:       profileName,
		ImageRef:      imageRef,
		Agent:         opts.Agent,
		Model:         model,
		Workdir: WorkdirMeta{
			HostPath:    workdir.Path,
			MountPath:   workdir.ResolvedMountPath(),
			Mode:        workdir.Mode,
			BaselineSHA: baselineSHA,
		},
		Directories:  dirMetas,
		HasPrompt:    hasPrompt,
		NetworkMode:  networkMode,
		NetworkAllow: networkAllow,
		Ports:        opts.Ports,
		Resources:    resources,
	}

	if err := SaveMeta(sandboxDir, meta); err != nil {
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

	if err := os.WriteFile(filepath.Join(sandboxDir, "config.json"), configData, 0600); err != nil {
		return nil, fmt.Errorf("write config.json: %w", err)
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
		profile:      profileName,
		imageRef:     imageRef,
		env:          mergedEnv,
		hasPrompt:    hasPrompt,
		networkMode:  networkMode,
		networkAllow: networkAllow,
		ports:        opts.Ports,
		tmuxConf:     tmuxConf,
		resources:    resources,
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
		cfg, cfgErr := LoadConfig()
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
		instanceCfg.CapAdd = []string{"NET_ADMIN"}
	}

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

// resolveModel expands a model alias using the agent definition.
func resolveModel(agentDef *agent.Definition, model string) string {
	if model == "" {
		return ""
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
func buildAgentCommand(agentDef *agent.Definition, model string, prompt string, passthrough []string) string {
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

// buildContainerConfig creates the config.json content.
func buildContainerConfig(agentDef *agent.Definition, agentCommand string, tmuxConf string, workingDir string, debug bool, networkIsolated bool, allowedDomains []string) ([]byte, error) {
	var stateDirName string
	if agentDef.StateDir != "" {
		stateDirName = filepath.Base(agentDef.StateDir)
	}
	cfg := containerConfig{
		HostUID:         os.Getuid(),
		HostGID:         os.Getgid(),
		AgentCommand:    agentCommand,
		StartupDelay:    int(agentDef.StartupDelay / time.Millisecond),
		ReadyPattern:    agentDef.ReadyPattern,
		SubmitSequence:  agentDef.SubmitSequence,
		TmuxConf:        tmuxConf,
		WorkingDir:      workingDir,
		StateDirName:    stateDirName,
		Debug:           debug,
		NetworkIsolated: networkIsolated,
		AllowedDomains:  allowedDomains,
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// readPrompt reads the prompt from --prompt, --prompt-file, or stdin ("-").
func readPrompt(prompt, promptFile string) (string, error) {
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
	if state.workdir.Mode == "copy" {
		mounts = append(mounts, runtime.MountSpec{
			Source: state.workCopyDir,
			Target: state.workdir.ResolvedMountPath(),
		})
	} else {
		mounts = append(mounts, runtime.MountSpec{
			Source:   state.workdir.Path,
			Target:   state.workdir.ResolvedMountPath(),
			ReadOnly: state.workdir.Mode != "rw" && state.workdir.Mode != "copy",
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

	// Agent state directory
	if state.agent.StateDir != "" {
		mounts = append(mounts, runtime.MountSpec{
			Source: filepath.Join(state.sandboxDir, "agent-state"),
			Target: state.agent.StateDir,
		})
	}

	// Log file
	mounts = append(mounts, runtime.MountSpec{
		Source: filepath.Join(state.sandboxDir, "log.txt"),
		Target: "/yoloai/log.txt",
	})

	// Prompt file
	if state.hasPrompt {
		mounts = append(mounts, runtime.MountSpec{
			Source:   filepath.Join(state.sandboxDir, "prompt.txt"),
			Target:   "/yoloai/prompt.txt",
			ReadOnly: true,
		})
	}

	// Config file
	mounts = append(mounts, runtime.MountSpec{
		Source:   filepath.Join(state.sandboxDir, "config.json"),
		Target:   "/yoloai/config.json",
		ReadOnly: true,
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
// others go to agent-state/ (mounted at StateDir).
// Returns true if any files were copied. Skips files that don't exist on the host.
func copySeedFiles(agentDef *agent.Definition, sandboxDir string, hasAPIKey bool) (bool, error) {
	copied := false
	agentStateDir := filepath.Join(sandboxDir, "agent-state")
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
				return copied, fmt.Errorf("read %s: %w", hostPath, readErr)
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
			return copied, fmt.Errorf("create dir for %s: %w", sf.TargetPath, err)
		}

		if err := os.WriteFile(targetPath, data, 0600); err != nil {
			return copied, fmt.Errorf("write %s: %w", targetPath, err)
		}
		copied = true
	}

	return copied, nil
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

	agentStateDir := filepath.Join(sandboxDir, "agent-state")
	if err := os.MkdirAll(agentStateDir, 0750); err != nil {
		return fmt.Errorf("create agent-state dir: %w", err)
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
func parseResourceLimits(rl *ResourceLimits) (*runtime.ResourceLimits, error) {
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

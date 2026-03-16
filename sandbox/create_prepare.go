package sandbox

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/kstenerud/yoloai/agent"
	"github.com/kstenerud/yoloai/config"
	"github.com/kstenerud/yoloai/workspace"
)

// profileResult holds resolved profile configuration after chain resolution
// and config merging.
type profileResult struct {
	name               string
	imageRef           string
	env                map[string]string
	agentArgs          map[string]string
	agentFiles         *config.AgentFilesConfig
	resources          *config.ResourceLimits
	mounts             []string
	capAdd             []string
	devices            []string
	setup              []string
	autoCommitInterval int
	userAliases        map[string]string
}

// resolveProfileConfig resolves the profile chain, merges config, and builds
// the profile image if needed. Returns a profileResult with all merged values.
func (m *Manager) resolveProfileConfig(ctx context.Context, opts *CreateOptions, agentDef **agent.Definition, ycfg *config.YoloaiConfig, gcfg *config.GlobalConfig) (*profileResult, error) {
	pr := &profileResult{
		env:                ycfg.Env,
		agentArgs:          ycfg.AgentArgs,
		agentFiles:         ycfg.AgentFiles,
		autoCommitInterval: ycfg.AutoCommitInterval,
		userAliases:        gcfg.ModelAliases,
	}

	if opts.Profile == "" {
		return pr, nil
	}

	if err := config.ValidateProfileName(opts.Profile); err != nil {
		return nil, err
	}
	chain, err := config.ResolveProfileChain(opts.Profile)
	if err != nil {
		return nil, err
	}
	merged, err := config.MergeProfileChain(ycfg, chain)
	if err != nil {
		return nil, fmt.Errorf("merge profile chain: %w", err)
	}
	if err := config.ValidateProfileBackend(merged.Backend, m.backend); err != nil {
		return nil, err
	}

	// Apply merged values where CLI didn't override
	if opts.Agent == ycfg.Agent && merged.Agent != "" {
		opts.Agent = merged.Agent
		def := agent.GetAgent(opts.Agent)
		if def == nil {
			return nil, NewUsageError("unknown agent from profile: %s", opts.Agent)
		}
		*agentDef = def
	}
	if opts.Model == "" && merged.Model != "" {
		opts.Model = merged.Model
	}

	pr.env = merged.Env
	pr.agentArgs = merged.AgentArgs
	pr.agentFiles = merged.AgentFiles

	if merged.Resources != nil {
		r := *merged.Resources
		pr.resources = &r
	}

	// Profile workdir: use if CLI didn't provide one
	if opts.Workdir.Path == "" && merged.Workdir != nil {
		wdPath, err := ExpandPath(merged.Workdir.Path)
		if err != nil {
			return nil, fmt.Errorf("expand profile workdir path: %w", err)
		}
		opts.Workdir = DirSpec{
			Path:      wdPath,
			Mode:      DirMode(merged.Workdir.Mode),
			MountPath: merged.Workdir.Mount,
		}
	}

	// Profile directories: prepend before CLI aux dirs
	var profileDirs []DirSpec
	for _, pd := range merged.Directories {
		dirPath, err := ExpandPath(pd.Path)
		if err != nil {
			return nil, fmt.Errorf("expand profile directory path: %w", err)
		}
		profileDirs = append(profileDirs, DirSpec{
			Path:      dirPath,
			Mode:      DirMode(pd.Mode),
			MountPath: pd.Mount,
		})
	}
	opts.AuxDirs = append(profileDirs, opts.AuxDirs...)

	// Profile ports: additive
	opts.Ports = append(merged.Ports, opts.Ports...)

	// Network: apply merged config as defaults (CLI flags override later)
	if merged.Network != nil && opts.Network == NetworkModeDefault {
		if merged.Network.Isolated {
			opts.Network = NetworkModeIsolated
		}
		opts.NetworkAllow = append(merged.Network.Allow, opts.NetworkAllow...)
	}

	pr.mounts = merged.Mounts
	pr.capAdd = merged.CapAdd
	pr.devices = merged.Devices
	pr.setup = merged.Setup
	pr.autoCommitInterval = merged.AutoCommitInterval
	pr.name = opts.Profile

	// Resolve image ref
	pr.imageRef = config.ResolveProfileImage(opts.Profile, chain)

	// Build profile image if needed (Docker only)
	if err := EnsureProfileImage(ctx, m.runtime, opts.Profile, m.backend, AutoBuildSecrets(), m.output, m.logger, false); err != nil {
		return nil, fmt.Errorf("build profile image: %w", err)
	}

	return pr, nil
}

// applyConfigDefaults fills in values from base config when the profile didn't
// set them, and applies CLI overrides for resources.
func applyConfigDefaults(opts *CreateOptions, ycfg *config.YoloaiConfig, pr *profileResult) {
	// Resources from base config (if profile didn't set them)
	if pr.resources == nil && ycfg.Resources != nil {
		r := *ycfg.Resources
		pr.resources = &r
	}

	// Mounts from base config (if profile didn't set them)
	if opts.Profile == "" && len(ycfg.Mounts) > 0 {
		pr.mounts = ycfg.Mounts
	}

	// Ports from base config (if profile didn't set them)
	if opts.Profile == "" && len(ycfg.Ports) > 0 {
		opts.Ports = append(ycfg.Ports, opts.Ports...)
	}

	// Recipes from base config (if profile didn't set them)
	if opts.Profile == "" {
		pr.capAdd = ycfg.CapAdd
		pr.devices = ycfg.Devices
		pr.setup = ycfg.Setup
	}

	// Network from base config (if profile didn't set it and CLI didn't override)
	if opts.Profile == "" && ycfg.Network != nil && opts.Network == NetworkModeDefault {
		if ycfg.Network.Isolated {
			opts.Network = NetworkModeIsolated
		}
		opts.NetworkAllow = append(ycfg.Network.Allow, opts.NetworkAllow...)
	}

	// CLI overrides for resources
	if opts.CPUs != "" {
		if pr.resources == nil {
			pr.resources = &config.ResourceLimits{}
		}
		pr.resources.CPUs = opts.CPUs
	}
	if opts.Memory != "" {
		if pr.resources == nil {
			pr.resources = &config.ResourceLimits{}
		}
		pr.resources.Memory = opts.Memory
	}

	// CLI --env overrides config/profile env vars
	if len(opts.Env) > 0 {
		if pr.env == nil {
			pr.env = make(map[string]string)
		}
		for k, v := range opts.Env {
			pr.env[k] = v
		}
	}
}

// parseAndValidateDirs converts DirSpec values to DirArg, runs safety checks,
// overlap detection, and dirty repo warnings. Returns nil workdir if the user cancelled.
// cfgModel is the model from config.yaml (needed for local model server check).
func (m *Manager) parseAndValidateDirs(ctx context.Context, opts CreateOptions, agentDef *agent.Definition, mergedEnv map[string]string, cfgModel string) (*DirArg, []*DirArg, error) {
	// Convert workdir DirSpec to DirArg
	if opts.Workdir.Path == "" {
		return nil, nil, NewUsageError("no workdir specified and no default workdir in profile")
	}
	workdir := dirSpecToDirArg(opts.Workdir)
	if workdir.Mode == "" {
		workdir.Mode = "copy"
	}

	if _, err := os.Stat(workdir.Path); err != nil {
		return nil, nil, NewUsageError("workdir does not exist: %s", workdir.Path)
	}

	// Auth checks
	hasAPIKey := hasAnyAPIKey(agentDef)
	hasAuth := hasAnyAuthFile(agentDef)
	hasAuthHint := hasAnyAuthHint(agentDef, mergedEnv)
	if !hasAPIKey && !hasAuth && !hasAuthHint {
		if agentDef.AuthOptional {
			fmt.Fprintf(m.output, "Warning: no authentication detected for %s (it may use credentials yoloai cannot check)\n", agentDef.Name) //nolint:errcheck // best-effort warning
		} else {
			msg := fmt.Sprintf("no authentication found for %s: set %s",
				agentDef.Name, strings.Join(agentDef.APIKeyEnvVars, "/"))
			if authDesc := describeSeedAuthFiles(agentDef); authDesc != "" {
				msg += fmt.Sprintf(" or provide OAuth credentials (%s)", authDesc)
			}
			if len(agentDef.AuthHintEnvVars) > 0 {
				msg += fmt.Sprintf(", or set %s for local models", strings.Join(agentDef.AuthHintEnvVars, "/"))
			}
			return nil, nil, fmt.Errorf("%s: %w", msg, ErrMissingAPIKey)
		}
	}

	// Local model server requires a model
	if !hasAPIKey && !hasAuth && hasAuthHint && opts.Model == "" && cfgModel == "" {
		return nil, nil, NewUsageError("a model is required when using a local model server: use --model or 'yoloai config set model <model>'")
	}

	// Localhost URL warning for containerized backends
	if m.backend != "seatbelt" {
		for _, key := range agentDef.AuthHintEnvVars {
			for _, val := range []string{os.Getenv(key), mergedEnv[key]} {
				if val != "" && containsLocalhost(val) {
					hint := "use the host's routable IP instead"
					if isContainerBackend(m.backend) {
						hint = "use host.docker.internal instead"
					}
					return nil, nil, NewUsageError("%s contains a localhost address (%s) which won't work inside a %s VM — %s",
						key, val, m.backend, hint)
				}
			}
		}
	}

	// Convert auxiliary DirSpec values to DirArg
	var auxDirs []*DirArg
	for _, auxSpec := range opts.AuxDirs {
		auxDir := dirSpecToDirArg(auxSpec)
		if _, err := os.Stat(auxDir.Path); err != nil {
			return nil, nil, NewUsageError("directory does not exist: %s", auxDir.Path)
		}
		auxDirs = append(auxDirs, auxDir)
	}

	// Safety checks — workdir
	if workspace.IsDangerousDir(workdir.Path) {
		if workdir.Force {
			fmt.Fprintf(m.output, "WARNING: mounting dangerous directory %s\n", workdir.Path) //nolint:errcheck // best-effort output
		} else {
			return nil, nil, NewUsageError("refusing to mount dangerous directory %s (use :force to override)", workdir.Path)
		}
	}

	// Safety checks — aux dirs
	for _, ad := range auxDirs {
		if workspace.IsDangerousDir(ad.Path) {
			if ad.Force {
				fmt.Fprintf(m.output, "WARNING: mounting dangerous directory %s\n", ad.Path) //nolint:errcheck // best-effort output
			} else {
				return nil, nil, NewUsageError("refusing to mount dangerous directory %s (use :force to override)", ad.Path)
			}
		}
	}

	// Overlap checks
	allPaths := []string{workdir.Path}
	for _, ad := range auxDirs {
		allPaths = append(allPaths, ad.Path)
	}
	if err := workspace.CheckPathOverlap(allPaths); err != nil {
		return nil, nil, NewUsageError("%s", err)
	}

	// Duplicate mount path check
	mountPaths := map[string]string{workdir.ResolvedMountPath(): workdir.Path}
	for _, ad := range auxDirs {
		mp := ad.ResolvedMountPath()
		if prev, exists := mountPaths[mp]; exists {
			return nil, nil, NewUsageError("duplicate container mount path %s (from %s and %s)", mp, prev, ad.Path)
		}
		mountPaths[mp] = ad.Path
	}

	// Dirty repo warnings
	var dirtyWarnings []string
	if msg, err := workspace.CheckDirtyRepo(workdir.Path); err != nil {
		return nil, nil, fmt.Errorf("check repo status: %w", err)
	} else if msg != "" {
		dirtyWarnings = append(dirtyWarnings, fmt.Sprintf("%s: %s", workdir.Path, msg))
	}
	for _, ad := range auxDirs {
		if ad.Mode == "copy" || ad.Mode == "overlay" || ad.Mode == "rw" {
			if msg, err := workspace.CheckDirtyRepo(ad.Path); err != nil {
				return nil, nil, fmt.Errorf("check repo status: %w", err)
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
		confirmed, err := Confirm(ctx, "Continue? [y/N] ", m.input, m.output)
		if err != nil {
			return nil, nil, err
		}
		if !confirmed {
			return nil, nil, nil // user cancelled
		}
	}

	return workdir, auxDirs, nil
}

// setupWorkdir copies/overlays the workdir, strips git metadata, and creates
// the git baseline. Returns the work copy directory path and baseline SHA.
func setupWorkdir(sandboxName string, workdir *DirArg) (string, string, error) {
	workCopyDir := WorkDir(sandboxName, workdir.Path)

	switch workdir.Mode {
	case "copy":
		if err := workspace.CopyDir(workdir.Path, workCopyDir); err != nil {
			return "", "", fmt.Errorf("copy workdir: %w", err)
		}
	case "overlay":
		for _, d := range []string{
			OverlayUpperDir(sandboxName, workdir.Path),
			OverlayOvlworkDir(sandboxName, workdir.Path),
		} {
			if err := os.MkdirAll(d, 0750); err != nil {
				return "", "", fmt.Errorf("create overlay dir %s: %w", d, err)
			}
		}
	default:
		if err := os.MkdirAll(workCopyDir, 0750); err != nil {
			return "", "", fmt.Errorf("create work dir: %w", err)
		}
	}

	// Git baseline (overlay defers baseline to container entrypoint)
	var baselineSHA string
	switch workdir.Mode {
	case "copy":
		// Preserve original git history so the agent (and user) can
		// git log, git show, git blame, etc. inside the sandbox.
		// If the source was a git repo with commits, just record HEAD as baseline.
		// For non-git directories or empty repos, create a fresh repo.
		if workspace.IsGitRepo(workCopyDir) {
			sha, err := workspace.HeadSHA(workCopyDir)
			if err != nil {
				// Git repo exists but has no commits (or is broken).
				// Remove .git and create fresh baseline.
				if rmErr := workspace.RemoveGitDirs(workCopyDir); rmErr != nil {
					return "", "", fmt.Errorf("remove invalid git dir: %w", rmErr)
				}
				sha, err = workspace.Baseline(workCopyDir)
				if err != nil {
					return "", "", fmt.Errorf("git baseline after removing invalid repo: %w", err)
				}
				baselineSHA = sha
			} else {
				baselineSHA = sha
			}
		} else {
			sha, err := workspace.Baseline(workCopyDir)
			if err != nil {
				return "", "", fmt.Errorf("git baseline: %w", err)
			}
			baselineSHA = sha
		}
	case "overlay":
		baselineSHA = ""
	default:
		sha, _ := workspace.HeadSHA(workdir.Path)
		baselineSHA = sha
	}

	return workCopyDir, baselineSHA, nil
}

// setupAuxDirs copies/overlays each auxiliary directory and creates baselines.
func setupAuxDirs(sandboxName string, auxDirs []*DirArg) ([]DirMeta, error) {
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

		switch ad.Mode {
		case "copy":
			auxWorkDir := WorkDir(sandboxName, ad.Path)
			if err := workspace.CopyDir(ad.Path, auxWorkDir); err != nil {
				return nil, fmt.Errorf("copy aux dir %s: %w", ad.Path, err)
			}
			if workspace.IsGitRepo(auxWorkDir) {
				sha, err := workspace.HeadSHA(auxWorkDir)
				if err != nil {
					return nil, fmt.Errorf("read HEAD of copied aux dir %s: %w", ad.Path, err)
				}
				dm.BaselineSHA = sha
			} else {
				sha, err := workspace.Baseline(auxWorkDir)
				if err != nil {
					return nil, fmt.Errorf("git baseline for aux dir %s: %w", ad.Path, err)
				}
				dm.BaselineSHA = sha
			}
		case "overlay":
			for _, d := range []string{
				OverlayUpperDir(sandboxName, ad.Path),
				OverlayOvlworkDir(sandboxName, ad.Path),
			} {
				if err := os.MkdirAll(d, 0750); err != nil {
					return nil, fmt.Errorf("create overlay dir for aux %s: %w", ad.Path, err)
				}
			}
		}

		dirMetas = append(dirMetas, dm)
	}
	return dirMetas, nil
}

// buildNetworkConfig determines the network mode and allowlist from options
// and agent definition.
func buildNetworkConfig(opts CreateOptions, agentDef *agent.Definition) (string, []string) {
	switch opts.Network {
	case NetworkModeNone:
		return "none", nil
	case NetworkModeIsolated:
		var allow []string
		allow = append(allow, agentDef.NetworkAllowlist...)
		allow = append(allow, opts.NetworkAllow...)
		return "isolated", allow
	default:
		return "", nil
	}
}

// dirSpecToDirArg converts a DirSpec to a DirArg for internal use.
func dirSpecToDirArg(s DirSpec) *DirArg {
	return &DirArg{
		Path:      s.Path,
		MountPath: s.MountPath,
		Mode:      string(s.Mode),
		Force:     s.Force,
	}
}

// collectOverlayMounts builds overlay mount configs for config.json from
// the workdir and auxiliary directories that use overlay mode.
func collectOverlayMounts(workdir *DirArg, auxDirs []*DirArg) []overlayMountConfig {
	var overlayMounts []overlayMountConfig
	if workdir.Mode == "overlay" {
		encoded := EncodePath(workdir.Path)
		overlayMounts = append(overlayMounts, overlayMountConfig{
			Lower:  "/yoloai/overlay/" + encoded + "/lower",
			Upper:  "/yoloai/overlay/" + encoded + "/upper",
			Work:   "/yoloai/overlay/" + encoded + "/ovlwork",
			Merged: workdir.ResolvedMountPath(),
		})
	}
	for _, ad := range auxDirs {
		if ad.Mode == "overlay" {
			encoded := EncodePath(ad.Path)
			overlayMounts = append(overlayMounts, overlayMountConfig{
				Lower:  "/yoloai/overlay/" + encoded + "/lower",
				Upper:  "/yoloai/overlay/" + encoded + "/upper",
				Work:   "/yoloai/overlay/" + encoded + "/ovlwork",
				Merged: ad.ResolvedMountPath(),
			})
		}
	}
	return overlayMounts
}

// collectCopyDirs returns mount paths of all :copy directories for the
// auto-commit loop in the container entrypoint.
func collectCopyDirs(workdir *DirArg, auxDirs []*DirArg) []string {
	var copyDirs []string
	if workdir.Mode == "copy" {
		copyDirs = append(copyDirs, workdir.ResolvedMountPath())
	}
	for _, ad := range auxDirs {
		if ad.Mode == "copy" {
			copyDirs = append(copyDirs, ad.ResolvedMountPath())
		}
	}
	return copyDirs
}

// validateAndExpandMounts validates and expands config mount paths.
func validateAndExpandMounts(mounts []string) ([]string, error) {
	result := make([]string, len(mounts))
	for i, m := range mounts {
		spec, err := parseConfigMount(m)
		if err != nil {
			return nil, fmt.Errorf("invalid mount %q: %w", m, err)
		}
		result[i] = spec.Source + ":" + spec.Target
		if spec.ReadOnly {
			result[i] += ":ro"
		}
	}
	return result, nil
}

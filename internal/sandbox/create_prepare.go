// ABOUTME: resolveProfileConfig chains profile configs, builds profile images,
// ABOUTME: and merges all settings into a profileResult for sandbox creation.
package sandbox

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox/archetype"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/internal/workspace"
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
	isolation          string
	isolationExplicit  bool // true when isolation was set via --isolation flag (not config/profile default)
	userAliases        map[string]string
	// Archetype-specific resolved fields
	archetypeDockerDRequired bool // true when archetype requires dockerd auto-start
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
		// No profile specified: use base image
		pr.imageRef = "yoloai-base"
		return pr, nil
	}

	if err := config.ValidateProfileName(opts.Profile); err != nil {
		return nil, err
	}
	chain, err := config.ResolveProfileChain(m.layout, opts.Profile)
	if err != nil {
		return nil, err
	}
	merged, err := config.MergeProfileChain(m.layout, ycfg, chain)
	if err != nil {
		return nil, fmt.Errorf("merge profile chain: %w", err)
	}
	if err := config.ValidateProfileBackend(merged.Backend, string(m.backend)); err != nil {
		return nil, err
	}

	homeDir := filepath.Dir(m.layout.DataDir)
	if err := applyMergedProfileToOpts(opts, agentDef, merged, pr, ycfg.Agent, homeDir); err != nil {
		return nil, err
	}

	pr.name = opts.Profile
	pr.imageRef = config.ResolveProfileImage(m.layout, opts.Profile, chain)

	// Build profile image if needed (Docker only)
	if err := EnsureProfileImage(ctx, m.runtime, m.layout, opts.Profile, AutoBuildSecrets(filepath.Dir(m.layout.DataDir)), m.output, m.logger, false); err != nil {
		return nil, fmt.Errorf("build profile image: %w", err)
	}

	return pr, nil
}

// applyMergedProfileToOpts applies merged profile values to opts and pr.
// homeDir is used for ~ expansion in profile workdir and directory paths.
// baseAgent is the agent name from the base config (ycfg.Agent), used to
// detect whether the CLI override has been applied.
func applyMergedProfileToOpts(opts *CreateOptions, agentDef **agent.Definition, merged *config.MergedConfig, pr *profileResult, baseAgent string, homeDir string) error {
	// Apply merged values where CLI didn't override
	if opts.Agent == baseAgent && merged.Agent != "" {
		opts.Agent = merged.Agent
		def := agent.GetAgent(opts.Agent)
		if def == nil {
			return NewUsageError("unknown agent from profile: %s", opts.Agent)
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
		wdPath, err := ExpandPath(merged.Workdir.Path, homeDir)
		if err != nil {
			return fmt.Errorf("expand profile workdir path: %w", err)
		}
		opts.Workdir = DirSpec{
			Path:      wdPath,
			Mode:      DirMode(merged.Workdir.Mode),
			MountPath: merged.Workdir.Mount,
		}
	}

	// Profile directories: prepend before CLI aux dirs
	if err := prependProfileDirs(opts, merged.Directories, homeDir); err != nil {
		return err
	}

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
	pr.isolation = merged.Isolation

	return nil
}

// prependProfileDirs prepends profile directory specs before the CLI aux dirs.
// homeDir is used for ~ expansion in profile directory paths.
func prependProfileDirs(opts *CreateOptions, profileDirs []config.ProfileDir, homeDir string) error {
	var dirs []DirSpec
	for _, pd := range profileDirs {
		dirPath, err := ExpandPath(pd.Path, homeDir)
		if err != nil {
			return fmt.Errorf("expand profile directory path: %w", err)
		}
		dirs = append(dirs, DirSpec{
			Path:      dirPath,
			Mode:      DirMode(pd.Mode),
			MountPath: pd.Mount,
		})
	}
	opts.AuxDirs = append(dirs, opts.AuxDirs...)
	return nil
}

// applyConfigDefaults fills in values from base config when the profile didn't
// set them, and applies CLI overrides for resources.
func applyConfigDefaults(opts *CreateOptions, ycfg *config.YoloaiConfig, pr *profileResult) error {
	if opts.Profile == "" {
		applyBaseConfigDefaults(opts, ycfg, pr)
	}
	applyBaseResourceDefaults(ycfg, pr)
	return applyCLIOverrides(opts, pr)
}

// applyBaseConfigDefaults applies mounts, ports, caps, and network from base
// config when no profile is active.
func applyBaseConfigDefaults(opts *CreateOptions, ycfg *config.YoloaiConfig, pr *profileResult) {
	if len(ycfg.Mounts) > 0 {
		pr.mounts = ycfg.Mounts
	}
	if len(ycfg.Ports) > 0 {
		opts.Ports = append(ycfg.Ports, opts.Ports...)
	}
	pr.capAdd = ycfg.CapAdd
	pr.devices = ycfg.Devices
	pr.setup = ycfg.Setup
	pr.isolation = ycfg.Isolation

	if ycfg.Network != nil && opts.Network == NetworkModeDefault {
		if ycfg.Network.Isolated {
			opts.Network = NetworkModeIsolated
		}
		opts.NetworkAllow = append(ycfg.Network.Allow, opts.NetworkAllow...)
	}
}

// applyBaseResourceDefaults applies resource limits from base config when the
// profile didn't set them.
func applyBaseResourceDefaults(ycfg *config.YoloaiConfig, pr *profileResult) {
	if pr.resources == nil && ycfg.Resources != nil {
		r := *ycfg.Resources
		pr.resources = &r
	}
}

// applyCLIOverrides applies CLI flag overrides for resources, isolation, and env.
func applyCLIOverrides(opts *CreateOptions, pr *profileResult) error {
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

	if opts.Isolation != "" {
		if err := config.ValidateIsolationMode(opts.Isolation); err != nil {
			return err
		}
		pr.isolation = opts.Isolation
		pr.isolationExplicit = true
	}

	if len(opts.Env) > 0 {
		if pr.env == nil {
			pr.env = make(map[string]string)
		}
		maps.Copy(pr.env, opts.Env)
	}

	return nil
}

// parseAndValidateDirs converts DirSpec values to DirSpec, runs safety checks,
// overlap detection, and dirty repo warnings. Returns nil workdir if the user cancelled.
// cfgModel is the model from config.yaml (needed for local model server check).
// credOverrides contains sudo-recovered credential defaults for keys absent from os.Environ.
func (m *Manager) parseAndValidateDirs(ctx context.Context, opts CreateOptions, agentDef *agent.Definition, mergedEnv map[string]string, cfgModel string, credOverrides map[string]string) (*DirSpec, []*DirSpec, error) {
	// Convert workdir DirSpec to DirSpec
	if opts.Workdir.Path == "" {
		return nil, nil, NewUsageError("no workdir specified and no default workdir in profile")
	}
	wd := opts.Workdir
	workdir := &wd
	if workdir.Mode == "" {
		workdir.Mode = "copy"
	}

	if _, err := os.Stat(workdir.Path); err != nil {
		return nil, nil, NewUsageError("workdir does not exist: %s", workdir.Path)
	}

	if err := m.checkAuthAndLocalhostWarnings(agentDef, mergedEnv, cfgModel, opts, credOverrides); err != nil {
		return nil, nil, err
	}

	auxDirs, err := buildAuxDirs(opts.AuxDirs)
	if err != nil {
		return nil, nil, err
	}

	if err := checkDirSafety(workdir, auxDirs, m.output, filepath.Dir(m.layout.DataDir)); err != nil {
		return nil, nil, err
	}

	if err := checkDirOverlaps(workdir, auxDirs); err != nil {
		return nil, nil, err
	}

	cancelled, err := m.checkDirtyRepos(ctx, workdir, auxDirs, opts.Yes)
	if err != nil {
		return nil, nil, err
	}
	if cancelled {
		return nil, nil, nil // user cancelled
	}

	return workdir, auxDirs, nil
}

// checkAuthAndLocalhostWarnings performs auth checks and localhost URL warnings.
func (m *Manager) checkAuthAndLocalhostWarnings(agentDef *agent.Definition, mergedEnv map[string]string, cfgModel string, opts CreateOptions, credOverrides map[string]string) error {
	hasAPIKey := hasAnyAPIKey(agentDef, credOverrides)
	hasAuth := hasAnyAuthFile(agentDef, filepath.Dir(m.layout.DataDir))
	hasAuthHint := hasAnyAuthHint(agentDef, mergedEnv, credOverrides)
	if err := checkAgentAuth(agentDef, hasAPIKey, hasAuth, hasAuthHint, m.output); err != nil {
		return err
	}

	// Local model server requires a model
	if !hasAPIKey && !hasAuth && hasAuthHint && opts.Model == "" && cfgModel == "" {
		return NewUsageError("a model is required when using a local model server: use --model or 'yoloai config set model <model>'")
	}

	return m.checkLocalhostURLs(agentDef, mergedEnv)
}

// checkAgentAuth verifies that the agent has the necessary authentication configured.
func checkAgentAuth(agentDef *agent.Definition, hasAPIKey, hasAuth, hasAuthHint bool, output io.Writer) error {
	if hasAPIKey || hasAuth || hasAuthHint {
		return nil
	}
	if agentDef.AuthOptional {
		fmt.Fprintf(output, "Warning: no authentication detected for %s (it may use credentials yoloai cannot check)\n", agentDef.Name) //nolint:errcheck // best-effort warning
		return nil
	}
	msg := fmt.Sprintf("no authentication found for %s: set %s",
		agentDef.Name, strings.Join(agentDef.APIKeyEnvVars, "/"))
	if authDesc := describeSeedAuthFiles(agentDef); authDesc != "" {
		msg += fmt.Sprintf(" or provide OAuth credentials (%s)", authDesc)
	}
	if len(agentDef.AuthHintEnvVars) > 0 {
		msg += fmt.Sprintf(", or set %s for local models", strings.Join(agentDef.AuthHintEnvVars, "/"))
	}
	return NewAuthError("%s: %w", msg, ErrMissingAPIKey)
}

// checkLocalhostURLs warns if auth hint env vars contain localhost addresses
// that won't work inside a container/VM sandbox.
func (m *Manager) checkLocalhostURLs(agentDef *agent.Definition, mergedEnv map[string]string) error {
	desc := m.runtime.Descriptor()
	if !desc.AgentProvisionedByBackend {
		return nil
	}
	for _, key := range agentDef.AuthHintEnvVars {
		for _, val := range []string{os.Getenv(key), mergedEnv[key]} {
			if val == "" || !containsLocalhost(val) {
				continue
			}
			hint := "use the host's routable IP instead"
			if desc.HostFromContainer != "" {
				hint = "use " + desc.HostFromContainer + " instead"
			}
			return NewUsageError("%s contains a localhost address (%s) which won't work inside a %s sandbox — %s",
				key, val, desc.Name, hint)
		}
	}
	return nil
}

// buildAuxDirs converts auxiliary DirSpec values to DirSpec and checks
// existence. Also enforces Q-U: aux dirs cannot be :copy or :overlay
// (diff/apply is workdir-only). The CLI and MCP boundaries already
// reject these via sandbox.ParseAuxDirArg, but library embedders that
// construct DirSpec values directly need a Create-time guard so the
// failure is loud rather than a silent no-op in setupAuxDir.
func buildAuxDirs(auxSpecs []DirSpec) ([]*DirSpec, error) {
	var auxDirs []*DirSpec
	for _, auxSpec := range auxSpecs {
		auxSpec := auxSpec
		auxDir := &auxSpec
		switch auxDir.Mode {
		case DirModeCopy:
			return nil, NewUsageError(
				"aux directories cannot use :copy (diff/apply is workdir-only).\n"+
					"  - to track changes, make %q the workdir instead\n"+
					"  - to edit it live, use :rw\n"+
					"  - for an isolated copy, run a separate sandbox", auxDir.Path)
		case DirModeOverlay:
			return nil, NewUsageError(
				"aux directories cannot use :overlay (diff/apply is workdir-only).\n"+
					"  - to track changes, make %q the workdir instead\n"+
					"  - to edit it live, use :rw\n"+
					"  - for an isolated copy, run a separate sandbox", auxDir.Path)
		case DirModeRW, DirModeRO, "":
			// rw / ro / unset all permitted on aux dirs.
		}
		if _, err := os.Stat(auxDir.Path); err != nil {
			return nil, NewUsageError("directory does not exist: %s", auxDir.Path)
		}
		auxDirs = append(auxDirs, auxDir)
	}
	return auxDirs, nil
}

// checkDirSafety checks for dangerous directories in workdir and aux dirs.
// homeDir is used to detect if the user's home directory is being mounted.
func checkDirSafety(workdir *DirSpec, auxDirs []*DirSpec, output io.Writer, homeDir string) error {
	if workspace.IsDangerousDir(workdir.Path, homeDir) {
		if workdir.Force {
			fmt.Fprintf(output, "WARNING: mounting dangerous directory %s\n", workdir.Path) //nolint:errcheck // best-effort output
		} else {
			return NewUsageError("refusing to mount dangerous directory %s (use :force to override)", workdir.Path)
		}
	}
	for _, ad := range auxDirs {
		if workspace.IsDangerousDir(ad.Path, homeDir) {
			if ad.Force {
				fmt.Fprintf(output, "WARNING: mounting dangerous directory %s\n", ad.Path) //nolint:errcheck // best-effort output
			} else {
				return NewUsageError("refusing to mount dangerous directory %s (use :force to override)", ad.Path)
			}
		}
	}
	return nil
}

// checkDirOverlaps checks for path overlaps and duplicate mount paths.
func checkDirOverlaps(workdir *DirSpec, auxDirs []*DirSpec) error {
	allPaths := []string{workdir.Path}
	for _, ad := range auxDirs {
		allPaths = append(allPaths, ad.Path)
	}
	if err := workspace.CheckPathOverlap(allPaths); err != nil {
		return NewUsageError("%s", err)
	}

	mountPaths := map[string]string{workdir.ResolvedMountPath(): workdir.Path}
	for _, ad := range auxDirs {
		mp := ad.ResolvedMountPath()
		if prev, exists := mountPaths[mp]; exists {
			return NewUsageError("duplicate container mount path %s (from %s and %s)", mp, prev, ad.Path)
		}
		mountPaths[mp] = ad.Path
	}
	return nil
}

// checkDirtyRepos checks for uncommitted changes in workdir and aux dirs.
// Returns (cancelled, error): cancelled is true if the user declined to continue.
func (m *Manager) checkDirtyRepos(ctx context.Context, workdir *DirSpec, auxDirs []*DirSpec, yes bool) (bool, error) {
	var dirtyWarnings []string
	if msg, err := workspace.CheckDirtyRepo(workdir.Path); err != nil {
		return false, fmt.Errorf("check repo status: %w", err)
	} else if msg != "" {
		dirtyWarnings = append(dirtyWarnings, fmt.Sprintf("%s: %s", workdir.Path, msg))
	}
	for _, ad := range auxDirs {
		if ad.Mode == "copy" || ad.Mode == "overlay" || ad.Mode == "rw" {
			if msg, err := workspace.CheckDirtyRepo(ad.Path); err != nil {
				return false, fmt.Errorf("check repo status: %w", err)
			} else if msg != "" {
				dirtyWarnings = append(dirtyWarnings, fmt.Sprintf("%s: %s", ad.Path, msg))
			}
		}
	}
	if len(dirtyWarnings) > 0 && !yes {
		for _, w := range dirtyWarnings {
			fmt.Fprintf(m.output, "WARNING: %s has uncommitted changes (%s)\n", strings.SplitN(w, ": ", 2)[0], strings.SplitN(w, ": ", 2)[1]) //nolint:errcheck // best-effort output
		}
		fmt.Fprintln(m.output, "These changes will be visible to the agent and could be modified or lost.") //nolint:errcheck // best-effort output
		confirmed, err := Confirm(ctx, "Continue? [y/N] ", m.input, m.output)
		if err != nil {
			return false, err
		}
		if !confirmed {
			return true, nil // user cancelled
		}
	}
	return false, nil
}

// setupWorkdir copies/overlays the workdir, strips git metadata, and creates
// the git baseline. Returns the work copy directory path and baseline SHA.
// For backends implementing WorkDirSetup (e.g., Tart), baseline creation is
// deferred until the VM starts, and this function returns empty SHA.
func setupWorkdir(sandboxDir string, workdir *DirSpec, rt runtime.Runtime) (string, string, error) {
	workCopyDir := store.WorkDir(sandboxDir, workdir.Path)

	if err := setupWorkdirDirs(sandboxDir, workdir, workCopyDir); err != nil {
		return "", "", err
	}

	baselineSHA, err := createWorkdirBaseline(workdir, workCopyDir, rt)
	if err != nil {
		return "", "", err
	}

	return workCopyDir, baselineSHA, nil
}

// setupWorkdirDirs creates the appropriate directory structure for the workdir mode.
func setupWorkdirDirs(sandboxDir string, workdir *DirSpec, workCopyDir string) error {
	switch workdir.Mode {
	case "copy":
		if err := workspace.CopyDir(workdir.Path, workCopyDir); err != nil {
			return fmt.Errorf("copy workdir: %w", err)
		}
	case "overlay":
		for _, d := range []string{
			store.OverlayUpperDir(sandboxDir, workdir.Path),
			store.OverlayOvlworkDir(sandboxDir, workdir.Path),
			store.OverlayMergedDir(sandboxDir, workdir.Path),
			store.OverlayLowerDir(sandboxDir, workdir.Path),
		} {
			if err := fileutil.MkdirAll(d, 0755); err != nil { //nolint:gosec // G301: world-traversable so container yoloai user can access merged/
				return fmt.Errorf("create overlay dir %s: %w", d, err)
			}
		}
	default:
		if err := fileutil.MkdirAll(workCopyDir, 0750); err != nil {
			return fmt.Errorf("create work dir: %w", err)
		}
	}
	return nil
}

// createWorkdirBaseline creates or resolves the git baseline SHA for the workdir.
func createWorkdirBaseline(workdir *DirSpec, workCopyDir string, rt runtime.Runtime) (string, error) {
	switch workdir.Mode {
	case "copy":
		return createCopyBaseline(workCopyDir, rt)
	case "overlay":
		return "", nil
	default:
		sha, _ := workspace.HeadSHA(workdir.Path)
		return sha, nil
	}
}

// createCopyBaseline creates the git baseline for a copy-mode workdir.
// For backends implementing WorkDirSetup (e.g., Tart), baseline creation is
// deferred until the VM starts, and this function returns empty SHA.
func createCopyBaseline(workCopyDir string, rt runtime.Runtime) (string, error) {
	// For backends implementing WorkDirSetup (e.g., Tart), the work directory
	// is copied to VirtioFS staging on the host, then moved to local VM storage
	// and baselined inside the VM after start. For other backends (Docker),
	// baseline is created on the host immediately after copying.
	if _, ok := rt.(runtime.WorkDirSetup); ok {
		// Tart: baseline will be created in VM after container start.
		// Return empty SHA to signal deferred baseline creation.
		slog.Debug("setupWorkdir: runtime implements WorkDirSetup, deferring baseline to VM",
			"backend", rt.Descriptor().Name)
		return "", nil
	}
	slog.Debug("setupWorkdir: runtime does NOT implement WorkDirSetup, creating baseline on host",
		"backend", rt.Descriptor().Name)

	// Docker: preserve original git history so the agent (and user) can
	// git log, git show, git blame, etc. inside the sandbox.
	// If the source was a git repo with commits, just record HEAD as baseline.
	// For non-git directories or empty repos, create a fresh repo.
	if workspace.IsGitRepo(workCopyDir) {
		return createBaselineForGitRepo(workCopyDir)
	}
	sha, err := workspace.Baseline(workCopyDir)
	if err != nil {
		return "", fmt.Errorf("git baseline: %w", err)
	}
	return sha, nil
}

// createBaselineForGitRepo creates a baseline for a directory that is already a git repo.
func createBaselineForGitRepo(workCopyDir string) (string, error) {
	_, err := workspace.HeadSHA(workCopyDir)
	if err != nil {
		// Git repo exists but has no commits (or is broken).
		// Remove .git and create fresh baseline.
		if rmErr := workspace.RemoveGitDirs(workCopyDir); rmErr != nil {
			return "", fmt.Errorf("remove invalid git dir: %w", rmErr)
		}
		sha, baselineErr := workspace.Baseline(workCopyDir)
		if baselineErr != nil {
			return "", fmt.Errorf("git baseline after removing invalid repo: %w", baselineErr)
		}
		return sha, nil
	}
	// Commit any pre-existing dirty changes so agent diffs are clean.
	sha, baselineErr := workspace.BaselineUncommittedChanges(workCopyDir)
	if baselineErr != nil {
		return "", fmt.Errorf("baseline pre-session state: %w", baselineErr)
	}
	return sha, nil
}

// executeVMWorkDirSetup runs VM-side work directory setup for backends that
// implement WorkDirSetup (e.g., Tart). It copies the work directory from
// VirtioFS staging to local VM storage, creates the git baseline inside the VM,
// retrieves the baseline SHA, and updates meta.json with the SHA.
// Returns nil if the runtime does not implement WorkDirSetup (Docker/containerd).
func executeVMWorkDirSetup(ctx context.Context, rt runtime.Runtime, name, sandboxDir string, meta *store.Meta) error {
	setupIntf, ok := rt.(runtime.WorkDirSetup)
	if !ok {
		return nil // Docker/containerd - no VM setup needed
	}

	vfsPath := filepath.Join("/Volumes/My Shared Files/yoloai/work", config.EncodePath(meta.Workdir.HostPath))
	vmLocalPath := runtime.ResolveCopyMountFor(rt, name, meta.Workdir.HostPath)

	cmds := setupIntf.SetupWorkDirInVM(vfsPath, vmLocalPath)
	for _, cmd := range cmds {
		_, err := rt.Exec(ctx, store.InstanceName(name), []string{"bash", "-c", cmd}, "admin")
		if err != nil {
			return fmt.Errorf("setup work dir in VM: %w", err)
		}
	}

	// Retrieve baseline SHA
	result, err := rt.Exec(ctx, store.InstanceName(name),
		[]string{"git", "-C", vmLocalPath, "rev-parse", "HEAD"}, "admin")
	if err != nil {
		return fmt.Errorf("get baseline SHA: %w", err)
	}

	// Update meta.json
	meta.Workdir.BaselineSHA = strings.TrimSpace(result.Stdout)
	if meta.Workdir.InceptionSHA == "" {
		meta.Workdir.InceptionSHA = meta.Workdir.BaselineSHA
	}
	return store.SaveMeta(sandboxDir, meta)
}

// setupAuxDirs copies/overlays each auxiliary directory and creates baselines.
func setupAuxDirs(sandboxDir string, auxDirs []*DirSpec) ([]store.DirMeta, error) {
	var dirMetas []store.DirMeta
	for _, ad := range auxDirs {
		dm, err := setupAuxDir(sandboxDir, ad)
		if err != nil {
			return nil, err
		}
		dirMetas = append(dirMetas, dm)
	}
	return dirMetas, nil
}

// setupAuxDir prepares a single auxiliary directory and returns its
// DirMeta. After Q-U (2026-05-25) aux dirs only support :rw and the
// default :ro, both of which are pure mounts with no host-side
// preparation — the function just normalises mode and packs the meta.
// The CLI / MCP boundary rejects :copy and :overlay via
// sandbox.ParseAuxDirArg, so they can't reach here.
func setupAuxDir(_ string, ad *DirSpec) (store.DirMeta, error) {
	mode := ad.Mode
	if mode == "" {
		mode = DirModeRO
	}
	return store.DirMeta{
		HostPath:  ad.Path,
		MountPath: ad.ResolvedMountPath(),
		Mode:      string(mode),
	}, nil
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

// collectOverlayMounts builds overlay mount configs for config.json
// from the workdir. After Q-U aux dirs no longer support :overlay,
// so this is a workdir-only check — kept as a function (returning a
// slice) so callers don't need to special-case overlay-vs-no-overlay
// at every config.json assembly site.
//
// The auxDirs parameter is intentionally still threaded through but
// unused; removing it would churn every call site, and the field is
// expected to disappear during the Workdir-only API cascade.
func collectOverlayMounts(workdir *DirSpec, _ []*DirSpec) []overlayMountConfig {
	if workdir.Mode != "overlay" {
		return nil
	}
	encoded := store.EncodePath(workdir.Path)
	return []overlayMountConfig{{
		Lower:  "/yoloai/overlay/" + encoded + "/lower",
		Upper:  "/yoloai/overlay/" + encoded + "/upper",
		Work:   "/yoloai/overlay/" + encoded + "/ovlwork",
		Merged: "/yoloai/overlay/" + encoded + "/merged",
	}}
}

// collectCopyDirs returns the mount paths of the workdir if it is
// :copy. After Q-U aux dirs can no longer be :copy, so this is a
// workdir-only check. The function shape (returning a slice) is
// preserved so the entrypoint auto-commit loop config doesn't need
// to special-case the no-copy and copy cases at every assembly site.
func collectCopyDirs(workdir *DirSpec, _ []*DirSpec) []string {
	if workdir.Mode != "copy" {
		return nil
	}
	return []string{workdir.ResolvedMountPath()}
}

// resolveAndApplyArchetype loads .yoloai.yaml, resolves the archetype with priority
// (CLI > .yoloai.yaml > auto-detect), validates platform requirements, handles
// requires: prompts, expands archetype effects on opts and pr, and prints transparency output.
//
// Returns: (resolved archetype, devcontainer config, safe devcontainer mounts, mount warnings, error).
func (m *Manager) resolveAndApplyArchetype(ctx context.Context, opts *CreateOptions, pr *profileResult) (archetype.Archetype, *archetype.DevcontainerConfig, []string, []string, error) {
	workdir := opts.Workdir.Path

	// Step 1: Load .yoloai.yaml
	yamlCfg, _, yamlErr := archetype.LoadYoloAIYaml(workdir, filepath.Dir(m.layout.DataDir))
	if yamlErr != nil {
		return "", nil, nil, nil, fmt.Errorf("load .yoloai.yaml: %w", yamlErr)
	}

	arch, signals, source, err := resolveArchetype(opts, yamlCfg, workdir)
	if err != nil {
		return "", nil, nil, nil, err
	}

	// Step 2: Platform check for apple archetype
	if err := checkAppleArchetype(m.output, arch, opts.Archetype); err != nil {
		return "", nil, nil, nil, err
	}

	// Step 3: requires: validation
	if err := checkRequires(ctx, m.input, m.output, yamlCfg, opts.Yes); err != nil {
		return "", nil, nil, nil, err
	}

	// Step 4: Archetype expansion
	devcontainerCfg, dcMounts, dcMountWarnings, bullets, err := m.expandArchetype(ctx, opts, pr, arch, yamlCfg)
	if err != nil {
		return "", nil, nil, nil, err
	}

	// Step 5: Transparency output
	printArchetypeOutput(m.output, arch, source, signals, bullets)

	return arch, devcontainerCfg, dcMounts, dcMountWarnings, nil
}

// resolveArchetype determines the archetype from CLI, .yoloai.yaml, or auto-detection.
func resolveArchetype(opts *CreateOptions, yamlCfg *archetype.YoloAIProjectConfig, workdir string) (archetype.Archetype, []string, string, error) {
	switch {
	case opts.Archetype != "":
		a, err := archetype.ParseArchetype(opts.Archetype)
		if err != nil {
			return "", nil, "", err
		}
		return a, nil, "--archetype flag", nil
	case yamlCfg != nil && yamlCfg.Archetype != "":
		a, err := archetype.ParseArchetype(yamlCfg.Archetype)
		if err != nil {
			return "", nil, "", err
		}
		return a, nil, ".yoloai.yaml", nil
	default:
		arch, signals := archetype.DetectArchetype(workdir)
		return arch, signals, "auto-detected", nil
	}
}

// checkAppleArchetype validates platform requirements for the apple archetype.
func checkAppleArchetype(output io.Writer, arch archetype.Archetype, cliArchetype string) error {
	if arch != archetype.ArchetypeApple {
		return nil
	}
	isAppleSilicon := goruntime.GOOS == "darwin" && goruntime.GOARCH == "arm64"
	if isAppleSilicon {
		return nil
	}
	if cliArchetype != "" {
		// Explicit --archetype apple on non-macOS → hard error
		return fmt.Errorf(
			"the \"apple\" archetype requires Apple Silicon macOS (Tart backend); " +
				"use --archetype simple for agent-only work on this project")
	}
	// Auto-detected apple on non-macOS → warn but don't fail
	fmt.Fprintf(output, "Warning: This looks like an Apple platform project. The Tart backend requires Apple Silicon macOS.\n") //nolint:errcheck // best-effort warning
	return nil
}

// checkRequires validates the requires: constraints from .yoloai.yaml.
func checkRequires(ctx context.Context, input io.Reader, output io.Writer, yamlCfg *archetype.YoloAIProjectConfig, yes bool) error {
	if yamlCfg == nil || len(yamlCfg.Requires) == 0 {
		return nil
	}
	for tool, constraint := range yamlCfg.Requires {
		fmt.Fprintf(output, "Warning: requires: %s %s — version verification not yet implemented; continuing.\n", tool, constraint) //nolint:errcheck // best-effort warning
	}
	if yes {
		return nil
	}
	confirmed, err := Confirm(ctx, "Continue anyway? [y/N] ", input, output)
	if err != nil {
		return err
	}
	if !confirmed {
		return fmt.Errorf("aborted due to unverified requires: constraints")
	}
	return nil
}

// expandArchetype applies archetype-specific settings to opts and pr.
// Returns (devcontainerCfg, dcMounts, dcMountWarnings, bullets, error).
func (m *Manager) expandArchetype(ctx context.Context, opts *CreateOptions, pr *profileResult, arch archetype.Archetype, yamlCfg *archetype.YoloAIProjectConfig) (*archetype.DevcontainerConfig, []string, []string, []string, error) {
	var bullets []string
	var devcontainerCfg *archetype.DevcontainerConfig
	var dcMounts []string
	var dcMountWarnings []string

	switch arch {
	case archetype.ArchetypeCompose:
		bullets = applyComposeArchetype(opts, pr)
	case archetype.ArchetypeDevcontainer:
		var err error
		devcontainerCfg, dcMounts, dcMountWarnings, bullets, err = m.applyDevcontainerArchetype(ctx, opts, pr)
		if err != nil {
			return nil, nil, nil, nil, err
		}
	case archetype.ArchetypeApple:
		bullets = append(bullets, "backend=tart required (Apple Silicon macOS VM)")
	case archetype.ArchetypeSimple:
		// no-op
	}

	mergeYamlMounts(pr, yamlCfg)
	return devcontainerCfg, dcMounts, dcMountWarnings, bullets, nil
}

// applyComposeArchetype applies compose-specific settings to opts and pr.
func applyComposeArchetype(opts *CreateOptions, pr *profileResult) []string {
	var bullets []string
	if opts.Isolation == "" || opts.Isolation == "container" {
		opts.Isolation = "container-privileged"
		pr.isolation = "container-privileged"
		bullets = append(bullets, "isolation set to container-privileged (Compose requires nested Docker)")
	}
	pr.archetypeDockerDRequired = true
	bullets = append(bullets, "dockerd will auto-start before lifecycle commands")
	return bullets
}

// applyDevcontainerArchetype loads and applies devcontainer.json settings.
func (m *Manager) applyDevcontainerArchetype(ctx context.Context, opts *CreateOptions, pr *profileResult) (*archetype.DevcontainerConfig, []string, []string, []string, error) {
	_ = ctx // reserved for future use
	workdir := opts.Workdir.Path
	var bullets []string

	dcPath := findDevcontainerPath(workdir)
	if dcPath == "" {
		return nil, nil, nil, bullets, nil
	}

	dc, err := archetype.LoadDevcontainer(dcPath)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("load devcontainer.json: %w", err)
	}

	if dc.DockerComposeFilePresent() {
		return nil, nil, nil, nil, fmt.Errorf(
			"docker Compose devcontainers are not supported; " +
				"use a project with devcontainer.json and docker-compose.yaml side by side instead")
	}

	dc.WarnIgnoredFields(m.output)

	bullets = applyDevcontainerRunArgs(dc, pr, bullets, m.output)
	bullets = applyDevcontainerCompose(dc, opts, pr, bullets)
	bullets = applyDevcontainerEnv(dc, pr, bullets)
	bullets = applyDevcontainerPorts(dc, opts, bullets)
	bullets = applyDevcontainerWorkspaceFolder(dc, opts, bullets)

	workdirMountPath := opts.Workdir.MountPath
	if workdirMountPath == "" {
		workdirMountPath = opts.Workdir.Path
	}
	dcMounts, dcMountWarnings := dc.FilterMounts(workdirMountPath, filepath.Dir(m.layout.DataDir))
	if len(dcMounts) > 0 {
		bullets = append(bullets, fmt.Sprintf("%d devcontainer mounts passed through", len(dcMounts)))
	}

	bullets = appendLifecycleBullets(dc, bullets)

	return dc, dcMounts, dcMountWarnings, bullets, nil
}

// findDevcontainerPath returns the path to devcontainer.json, or empty string if not found.
func findDevcontainerPath(workdir string) string {
	for _, candidate := range []string{
		filepath.Join(workdir, ".devcontainer", "devcontainer.json"),
		filepath.Join(workdir, "devcontainer.json"),
	} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

// applyDevcontainerRunArgs applies runArgs (cpus, memory, capAdd) from devcontainer.json.
func applyDevcontainerRunArgs(dc *archetype.DevcontainerConfig, pr *profileResult, bullets []string, output io.Writer) []string {
	cpus, memory, capAdd, unknownWarnings := dc.ParsedRunArgs()
	for _, w := range unknownWarnings {
		fmt.Fprintln(output, w) //nolint:errcheck // best-effort warning
	}
	if cpus != "" && (pr.resources == nil || pr.resources.CPUs == "") {
		if pr.resources == nil {
			pr.resources = &config.ResourceLimits{}
		}
		pr.resources.CPUs = cpus
		bullets = append(bullets, fmt.Sprintf("CPUs set to %s (from runArgs)", cpus))
	}
	if memory != "" && (pr.resources == nil || pr.resources.Memory == "") {
		if pr.resources == nil {
			pr.resources = &config.ResourceLimits{}
		}
		pr.resources.Memory = memory
		bullets = append(bullets, fmt.Sprintf("memory set to %s (from runArgs)", memory))
	}
	pr.capAdd = append(pr.capAdd, capAdd...)
	return bullets
}

// applyDevcontainerCompose checks postStartCommand for compose usage and sets isolation.
func applyDevcontainerCompose(dc *archetype.DevcontainerConfig, opts *CreateOptions, pr *profileResult, bullets []string) []string {
	if !dc.PostStartCommandUsesCompose() {
		return bullets
	}
	if opts.Isolation == "" || opts.Isolation == "container" {
		opts.Isolation = "container-privileged"
		pr.isolation = "container-privileged"
		bullets = append(bullets, "isolation set to container-privileged (postStartCommand uses docker compose)")
	}
	pr.archetypeDockerDRequired = true
	bullets = append(bullets, "dockerd will auto-start before lifecycle commands")
	return bullets
}

// applyDevcontainerEnv merges environment variables from devcontainer.json.
func applyDevcontainerEnv(dc *archetype.DevcontainerConfig, pr *profileResult, bullets []string) []string {
	merged := dc.MergedEnv()
	if len(merged) == 0 {
		return bullets
	}
	if pr.env == nil {
		pr.env = make(map[string]string)
	}
	for k, v := range merged {
		if _, exists := pr.env[k]; !exists {
			pr.env[k] = v
		}
	}
	return append(bullets, fmt.Sprintf("environment variables merged from devcontainer.json (%d keys)", len(merged)))
}

// applyDevcontainerPorts merges port forwards from devcontainer.json.
func applyDevcontainerPorts(dc *archetype.DevcontainerConfig, opts *CreateOptions, bullets []string) []string {
	ports := dc.ExtractPorts()
	if len(ports) == 0 {
		return bullets
	}
	seenPorts := make(map[string]bool)
	for _, p := range opts.Ports {
		seenPorts[p] = true
	}
	for _, p := range ports {
		if !seenPorts[p] {
			opts.Ports = append(opts.Ports, p)
			seenPorts[p] = true
		}
	}
	return append(bullets, fmt.Sprintf("ports %v forwarded", ports))
}

// applyDevcontainerWorkspaceFolder applies workspaceFolder to the workdir mount path.
func applyDevcontainerWorkspaceFolder(dc *archetype.DevcontainerConfig, opts *CreateOptions, bullets []string) []string {
	if dc.WorkspaceFolder == "" {
		return bullets
	}
	opts.Workdir.MountPath = dc.WorkspaceFolder
	return append(bullets, fmt.Sprintf("workdir mount path set to %s (workspaceFolder)", dc.WorkspaceFolder))
}

// appendLifecycleBullets adds lifecycle command summary bullets.
func appendLifecycleBullets(dc *archetype.DevcontainerConfig, bullets []string) []string {
	if !dc.OnCreateCommand.IsZero() {
		bullets = append(bullets, "onCreateCommand will run once at first start")
	}
	if !dc.UpdateContentCommand.IsZero() {
		bullets = append(bullets, "updateContentCommand will run once at first start")
	}
	if !dc.PostCreateCommand.IsZero() {
		bullets = append(bullets, "postCreateCommand will run once at first start")
	}
	if !dc.PostStartCommand.IsZero() {
		bullets = append(bullets, "postStartCommand will run on each start")
	}
	return bullets
}

// mergeYamlMounts adds .yoloai.yaml mounts to pr.mounts (dedup).
func mergeYamlMounts(pr *profileResult, yamlCfg *archetype.YoloAIProjectConfig) {
	if yamlCfg == nil || len(yamlCfg.Mounts) == 0 {
		return
	}
	seenYamlMounts := make(map[string]bool)
	for _, mount := range pr.mounts {
		seenYamlMounts[mount] = true
	}
	for _, mount := range yamlCfg.Mounts {
		if !seenYamlMounts[mount] {
			pr.mounts = append(pr.mounts, mount)
			seenYamlMounts[mount] = true
		}
	}
}

// printArchetypeOutput prints transparency information about the resolved archetype.
func printArchetypeOutput(output io.Writer, arch archetype.Archetype, source string, signals []string, bullets []string) {
	if arch == archetype.ArchetypeSimple && source == "auto-detected" {
		return
	}
	switch {
	case len(signals) > 0:
		for _, sig := range signals {
			fmt.Fprintf(output, "→ Detected %s\n", sig) //nolint:errcheck // best-effort output
		}
	case source == ".yoloai.yaml":
		fmt.Fprintf(output, "→ .yoloai.yaml declares archetype: %s\n", string(arch)) //nolint:errcheck // best-effort output
	case source == "--archetype flag":
		fmt.Fprintf(output, "→ --archetype %s\n", string(arch)) //nolint:errcheck // best-effort output
	}
	if arch != archetype.ArchetypeSimple {
		fmt.Fprintf(output, "  Archetype: %s\n", string(arch)) //nolint:errcheck // best-effort output
		if len(bullets) > 0 {
			fmt.Fprintln(output, "  Because of this:") //nolint:errcheck // best-effort output
			for _, b := range bullets {
				fmt.Fprintf(output, "    · %s\n", b) //nolint:errcheck // best-effort output
			}
		}
		fmt.Fprintf(output, "  To suppress: --archetype simple\n") //nolint:errcheck // best-effort output
	}
}

// validateAndExpandMounts validates and expands config mount paths.
// homeDir is used to expand leading "~" in host paths.
func validateAndExpandMounts(mounts []string, homeDir string) ([]string, error) {
	result := make([]string, len(mounts))
	for i, m := range mounts {
		spec, err := parseConfigMount(m, homeDir)
		if err != nil {
			return nil, fmt.Errorf("invalid mount %q: %w", m, err)
		}
		result[i] = spec.HostPath + ":" + spec.ContainerPath
		if spec.ReadOnly {
			result[i] += ":ro"
		}
	}
	return result, nil
}

// ABOUTME: Dir parse/validate/setup pipeline — parses and validates workdir and
// ABOUTME: aux dirs, performs safety/overlap/dirty-repo checks, sets up host-side
// ABOUTME: directory state, and collects network and mount configs.
package create

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/runtime"
	provision "github.com/kstenerud/yoloai/internal/sandbox/provision"
	"github.com/kstenerud/yoloai/internal/sandbox/runtimeconfig"
	"github.com/kstenerud/yoloai/internal/sandbox/state"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/internal/workspace"
	"github.com/kstenerud/yoloai/yoerrors"
)

// parseAndValidateDirs converts DirSpec values to DirSpec, runs safety checks,
// overlap detection, and dirty repo warnings. Returns nil workdir if the user cancelled.
// cfgModel is the model from config.yaml (needed for local model server check).
func parseAndValidateDirs(d state.Deps, opts Options, agentDef *agent.Definition, mergedEnv map[string]string, cfgModel string) (*DirSpec, []*DirSpec, error) {
	// Convert workdir DirSpec to DirSpec
	if opts.Workdir.Path == "" {
		return nil, nil, yoerrors.NewUsageError("no workdir specified and no default workdir in profile")
	}
	wd := opts.Workdir
	workdir := &wd
	if workdir.Mode == "" {
		workdir.Mode = "copy"
	}

	if _, err := os.Stat(workdir.Path); err != nil {
		return nil, nil, yoerrors.NewUsageError("workdir does not exist: %s", workdir.Path)
	}

	if err := checkAuthAndLocalhostWarnings(d, agentDef, mergedEnv, cfgModel, opts); err != nil {
		return nil, nil, err
	}

	auxDirs, err := buildAuxDirs(opts.AuxDirs)
	if err != nil {
		return nil, nil, err
	}

	if err := checkDirSafety(workdir, auxDirs, outputFor(opts.Output), d.Layout.HomeDir); err != nil {
		return nil, nil, err
	}

	if err := checkDirOverlaps(workdir, auxDirs); err != nil {
		return nil, nil, err
	}

	if err := checkDirtyRepos(workdir, auxDirs); err != nil {
		return nil, nil, err
	}

	return workdir, auxDirs, nil
}

// checkAuthAndLocalhostWarnings performs auth checks and localhost URL warnings.
func checkAuthAndLocalhostWarnings(d state.Deps, agentDef *agent.Definition, mergedEnv map[string]string, cfgModel string, opts Options) error {
	hasAPIKey := provision.HasAnyAPIKey(agentDef, d.Layout.Env)
	hasAuth := provision.HasAnyAuthFile(agentDef, d.Layout.HomeDir)
	hasAuthHint := provision.HasAnyAuthHint(agentDef, mergedEnv, d.Layout.Env)
	if err := checkAgentAuth(agentDef, hasAPIKey, hasAuth, hasAuthHint, outputFor(opts.Output)); err != nil {
		return err
	}

	// Local model server requires a model
	if !hasAPIKey && !hasAuth && hasAuthHint && opts.Model == "" && cfgModel == "" {
		return yoerrors.NewUsageError("a model is required when using a local model server: use --model or 'yoloai config set model <model>'")
	}

	return checkLocalhostURLs(d, agentDef, mergedEnv)
}

// checkAgentAuth verifies that the agent has the necessary authentication configured.
func checkAgentAuth(agentDef *agent.Definition, hasAPIKey, hasAuth, hasAuthHint bool, output io.Writer) error {
	if hasAPIKey || hasAuth || hasAuthHint {
		return nil
	}
	if agentDef.AuthOptional {
		fmt.Fprintf(output, "Warning: no authentication detected for %s (it may use credentials yoloai cannot check)\n", agentDef.Type) //nolint:errcheck // best-effort warning
		return nil
	}
	msg := fmt.Sprintf("no authentication found for %s: set %s",
		agentDef.Type, strings.Join(agentDef.APIKeyEnvVars, "/"))
	if authDesc := provision.DescribeSeedAuthFiles(agentDef); authDesc != "" {
		msg += fmt.Sprintf(" or provide OAuth credentials (%s)", authDesc)
	}
	if len(agentDef.AuthHintEnvVars) > 0 {
		msg += fmt.Sprintf(", or set %s for local models", strings.Join(agentDef.AuthHintEnvVars, "/"))
	}
	return yoerrors.NewAuthError("%s: %w", msg, ErrMissingAPIKey)
}

// checkLocalhostURLs warns if auth hint env vars contain localhost addresses
// that won't work inside a container/VM sandbox.
func checkLocalhostURLs(d state.Deps, agentDef *agent.Definition, mergedEnv map[string]string) error {
	desc := d.Runtime.Descriptor()
	if !desc.AgentProvisionedByBackend {
		return nil
	}
	for _, key := range agentDef.AuthHintEnvVars {
		for _, val := range []string{d.Layout.Env[key], mergedEnv[key]} {
			if val == "" || !containsLocalhost(val) {
				continue
			}
			hint := "use the host's routable IP instead"
			if desc.HostFromContainer != "" {
				hint = "use " + desc.HostFromContainer + " instead"
			}
			return yoerrors.NewUsageError("%s contains a localhost address (%s) which won't work inside a %s sandbox — %s",
				key, val, desc.Type, hint)
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
			return nil, yoerrors.NewUsageError(
				"aux directories cannot use :copy (diff/apply is workdir-only).\n"+
					"  - to track changes, make %q the workdir instead\n"+
					"  - to edit it live, use :rw\n"+
					"  - for an isolated copy, run a separate sandbox", auxDir.Path)
		case DirModeOverlay:
			return nil, yoerrors.NewUsageError(
				"aux directories cannot use :overlay (diff/apply is workdir-only).\n"+
					"  - to track changes, make %q the workdir instead\n"+
					"  - to edit it live, use :rw\n"+
					"  - for an isolated copy, run a separate sandbox", auxDir.Path)
		case DirModeRW, DirModeRO, "":
			// rw / ro / unset all permitted on aux dirs.
		}
		if _, err := os.Stat(auxDir.Path); err != nil {
			return nil, yoerrors.NewUsageError("directory does not exist: %s", auxDir.Path)
		}
		auxDirs = append(auxDirs, auxDir)
	}
	return auxDirs, nil
}

// checkDirSafety checks for dangerous directories in workdir and aux dirs.
// homeDir is used to detect if the user's home directory is being mounted.
func checkDirSafety(workdir *DirSpec, auxDirs []*DirSpec, output io.Writer, homeDir string) error {
	if workspace.IsDangerousDir(workdir.Path, homeDir) {
		if workdir.AllowDangerousPath {
			fmt.Fprintf(output, "WARNING: mounting dangerous directory %s\n", workdir.Path) //nolint:errcheck // best-effort output
		} else {
			return yoerrors.NewUsageError("refusing to mount dangerous directory %s (use :force to override)", workdir.Path)
		}
	}
	for _, ad := range auxDirs {
		if workspace.IsDangerousDir(ad.Path, homeDir) {
			if ad.AllowDangerousPath {
				fmt.Fprintf(output, "WARNING: mounting dangerous directory %s\n", ad.Path) //nolint:errcheck // best-effort output
			} else {
				return yoerrors.NewUsageError("refusing to mount dangerous directory %s (use :force to override)", ad.Path)
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
		return yoerrors.NewUsageError("%s", err)
	}

	mountPaths := map[string]string{workdir.ResolvedMountPath(): workdir.Path}
	for _, ad := range auxDirs {
		mp := ad.ResolvedMountPath()
		if prev, exists := mountPaths[mp]; exists {
			return yoerrors.NewUsageError("duplicate container mount path %s (from %s and %s)", mp, prev, ad.Path)
		}
		mountPaths[mp] = ad.Path
	}
	return nil
}

// checkDirtyRepos refuses creation when the workdir or any diff/apply aux dir
// has uncommitted git changes, unless that directory opted in via AllowDirty.
// It never prompts: a dirty directory the caller has not acked yields a
// *DirtyWorkdirError the caller must consciously override. The CLI catches it,
// prompts, and retries with AllowDirty set.
func checkDirtyRepos(workdir *DirSpec, auxDirs []*DirSpec) error {
	var dirty []yoerrors.DirtyDir
	check := func(d *DirSpec) error {
		if d.AllowDirty {
			return nil
		}
		msg, err := workspace.CheckDirtyRepo(d.Path)
		if err != nil {
			return fmt.Errorf("check repo status: %w", err)
		}
		if msg != "" {
			dirty = append(dirty, yoerrors.DirtyDir{Path: d.Path, Status: msg})
		}
		return nil
	}
	if err := check(workdir); err != nil {
		return err
	}
	for _, ad := range auxDirs {
		if ad.Mode == "copy" || ad.Mode == "overlay" || ad.Mode == "rw" {
			if err := check(ad); err != nil {
				return err
			}
		}
	}
	if len(dirty) > 0 {
		return &yoerrors.DirtyWorkdirError{Dirs: dirty}
	}
	return nil
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
			"backend", rt.Descriptor().Type)
		return "", nil
	}
	slog.Debug("setupWorkdir: runtime does NOT implement WorkDirSetup, creating baseline on host",
		"backend", rt.Descriptor().Type)

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

// setupAuxDirs copies/overlays each auxiliary directory and creates baselines.
func setupAuxDirs(rt runtime.Runtime, auxDirs []*DirSpec) ([]store.DirEnvironment, error) {
	var dirEnvs []store.DirEnvironment
	for _, ad := range auxDirs {
		dm, err := setupAuxDir(rt, ad)
		if err != nil {
			return nil, err
		}
		dirEnvs = append(dirEnvs, dm)
	}
	return dirEnvs, nil
}

// setupAuxDir prepares a single auxiliary directory and returns its
// DirEnvironment. After Q-U (2026-05-25) aux dirs only support :rw and the
// default :ro, both of which are pure mounts with no host-side
// preparation — the function just normalises mode and packs the meta.
// The CLI / MCP boundary rejects :copy and :overlay via
// sandbox.ParseAuxDirArg, so they can't reach here.
//
// MountPath is recorded as the guest-visible path: backends that re-root
// mounts inside the guest (tart maps host dirs under /Users/admin/host/...)
// must advertise where the mount is actually reachable, so the generated
// CLAUDE.md, `info`, and MCP {dir:N} placeholders don't point at a path that
// doesn't exist in the guest. Identity for backends without translation.
func setupAuxDir(rt runtime.Runtime, ad *DirSpec) (store.DirEnvironment, error) {
	mode := ad.Mode
	if mode == "" {
		mode = DirModeRO
	}
	return store.DirEnvironment{
		HostPath:  ad.Path,
		MountPath: runtime.ResolveGuestMountPathFor(rt, ad.ResolvedMountPath()),
		Mode:      mode,
	}, nil
}

// buildNetworkConfig determines the network mode and allowlist from options
// and agent definition.
func buildNetworkConfig(opts Options, agentDef *agent.Definition) (string, []string) {
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
// from the workdir. Aux dirs don't support :overlay, so this is a
// workdir-only check — kept as a function (returning a slice) so callers
// don't need to special-case overlay-vs-no-overlay at every config.json
// assembly site.
//
// The auxDirs parameter is intentionally threaded through but unused;
// removing it would churn every call site.
func collectOverlayMounts(workdir *DirSpec, _ []*DirSpec) []runtimeconfig.OverlayMountConfig {
	if workdir.Mode != "overlay" {
		return nil
	}
	encoded := store.EncodePath(workdir.Path)
	return []runtimeconfig.OverlayMountConfig{{
		Lower:  "/yoloai/overlay/" + encoded + "/lower",
		Upper:  "/yoloai/overlay/" + encoded + "/upper",
		Work:   "/yoloai/overlay/" + encoded + "/ovlwork",
		Merged: "/yoloai/overlay/" + encoded + "/merged",
	}}
}

// collectCopyDirs returns the mount paths of the workdir if it is
// :copy. Aux dirs can't be :copy, so this is a workdir-only check.
// The function shape (returning a slice) is preserved so the entrypoint
// auto-commit loop config doesn't need to special-case the no-copy and
// copy cases at every assembly site.
func collectCopyDirs(workdir *DirSpec, _ []*DirSpec) []string {
	if workdir.Mode != "copy" {
		return nil
	}
	return []string{workdir.ResolvedMountPath()}
}

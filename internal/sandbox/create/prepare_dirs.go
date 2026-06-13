// ABOUTME: Dir parse/validate/setup pipeline — parses and validates workdir and
// ABOUTME: aux dirs, performs safety/overlap/dirty-repo checks, sets up host-side
// ABOUTME: directory state, and collects network and mount configs.
package create

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/git"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox/launch"
	provision "github.com/kstenerud/yoloai/internal/sandbox/provision"
	"github.com/kstenerud/yoloai/internal/sandbox/runtimeconfig"
	"github.com/kstenerud/yoloai/internal/sandbox/state"
	"github.com/kstenerud/yoloai/internal/store"
	"github.com/kstenerud/yoloai/internal/workspace"
	"github.com/kstenerud/yoloai/yoerrors"
)

// parseAndValidateDirs converts DirSpec values to DirSpec, runs safety checks,
// overlap detection, and dirty repo warnings. Returns nil workdir if the user cancelled.
// cfgModel is the model from config.yaml (needed for local model server check).
func parseAndValidateDirs(ctx context.Context, d state.Deps, opts Options, agentDef *agent.Definition, mergedEnv map[string]string, cfgModel string) (*DirSpec, []*DirSpec, error) {
	// Convert workdir DirSpec to DirSpec
	if opts.Workdir.Path == "" {
		return nil, nil, yoerrors.NewUsageError("no workdir specified and no default workdir in profile")
	}
	wd := opts.Workdir
	workdir := &wd

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

	defaultDirModes(workdir, auxDirs)

	if err := checkDirSafety(workdir, auxDirs, outputFor(opts.Output), d.Layout.HomeDir); err != nil {
		return nil, nil, err
	}

	if err := checkDirOverlaps(workdir, auxDirs); err != nil {
		return nil, nil, err
	}

	if err := checkDirtyRepos(ctx, git.NewHost(d.Layout), workdir, auxDirs); err != nil {
		return nil, nil, err
	}

	return workdir, auxDirs, nil
}

// defaultDirModes fills any unset directory mode with its safe default — the
// workdir to :copy (the original is protected via copy/diff/apply) and each aux
// dir to read-only. This is the single place dir modes are defaulted, and it
// lives in the pipeline (not at the public boundary) on purpose: the effective
// workdir/aux set is the product of the profile merge that ran earlier, so a
// profile-supplied dir with no mode is only fully known here. Because the field
// is safety-sensitive and the default is the safe choice, resolving "unset" to
// the safe default does not conflict with resisting defaults — they align.
func defaultDirModes(workdir *DirSpec, auxDirs []*DirSpec) {
	if workdir.Mode == "" {
		workdir.Mode = DirModeCopy
	}
	for _, ad := range auxDirs {
		if ad.Mode == "" {
			ad.Mode = DirModeRO
		}
	}
}

// checkAuthAndLocalhostWarnings performs auth checks and localhost URL warnings.
func checkAuthAndLocalhostWarnings(d state.Deps, agentDef *agent.Definition, mergedEnv map[string]string, cfgModel string, opts Options) error {
	hasAPIKey := provision.HasAnyAPIKey(agentDef, d.Layout)
	hasAuth := provision.HasAnyAuthFile(agentDef, d.Layout.HomeDir)
	hasAuthHint := provision.HasAnyAuthHint(agentDef, mergedEnv, d.Layout)
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
	hostHints := d.Layout.Env().EnvForAgentCredentials(agentDef.AuthHintEnvVars)
	for _, key := range agentDef.AuthHintEnvVars {
		for _, val := range []string{hostHints[key], mergedEnv[key]} {
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
// existence. All modes are permitted: :copy and :overlay enable the
// diff/apply workflow for multiple directories (D81, multi-workdir Phase 2),
// :rw provides live-edit access, and :ro (the default when unset) is
// read-only.
func buildAuxDirs(auxSpecs []DirSpec) ([]*DirSpec, error) {
	var auxDirs []*DirSpec
	for _, auxSpec := range auxSpecs {
		auxDir := &auxSpec
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
func checkDirtyRepos(ctx context.Context, g *git.Git, workdir *DirSpec, auxDirs []*DirSpec) error {
	var dirty []yoerrors.DirtyDir
	check := func(d *DirSpec) error {
		if d.AllowDirty {
			return nil
		}
		msg, err := g.CheckDirtyRepo(ctx, d.Path)
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
func setupWorkdir(ctx context.Context, g *git.Git, sandboxDir string, workdir *DirSpec, rt runtime.Backend) (string, string, error) {
	workCopyDir := store.WorkDir(sandboxDir, workdir.Path)

	if err := setupDirContent(sandboxDir, workdir, workCopyDir); err != nil {
		return "", "", err
	}

	baselineSHA, err := createDirBaseline(ctx, g, workdir, workCopyDir, rt)
	if err != nil {
		return "", "", err
	}

	return workCopyDir, baselineSHA, nil
}

// setupDirContent creates the appropriate host-side directory structure for
// a :copy or :overlay dir. For other modes the workCopyDir is created as a
// plain directory (used only as a placeholder; the actual mount is a live
// bind-mount). sandboxDir is the sandbox root, dir is the DirSpec, and
// workCopyDir is the pre-computed store.WorkDir path for dir.
func setupDirContent(sandboxDir string, dir *DirSpec, workCopyDir string) error {
	switch dir.Mode {
	case "copy":
		if err := workspace.CopyDir(dir.Path, workCopyDir); err != nil {
			return fmt.Errorf("copy dir: %w", err)
		}
	case "overlay":
		for _, d := range []string{
			store.OverlayUpperDir(sandboxDir, dir.Path),
			store.OverlayOvlworkDir(sandboxDir, dir.Path),
			store.OverlayMergedDir(sandboxDir, dir.Path),
			store.OverlayLowerDir(sandboxDir, dir.Path),
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

// createDirBaseline creates or resolves the git baseline SHA for a dir.
func createDirBaseline(ctx context.Context, g *git.Git, dir *DirSpec, workCopyDir string, rt runtime.Backend) (string, error) {
	switch dir.Mode {
	case "copy":
		return createCopyBaseline(ctx, g, workCopyDir, rt)
	case "overlay":
		return "", nil
	default:
		sha, _ := g.HeadSHA(ctx, dir.Path)
		return sha, nil
	}
}

// createCopyBaseline creates the git baseline for a copy-mode workdir.
// For SandboxSide backends (e.g., Tart) the work copy lives inside the sandbox,
// so baseline creation is deferred until the VM starts and this returns empty SHA.
func createCopyBaseline(ctx context.Context, g *git.Git, workCopyDir string, rt runtime.Backend) (string, error) {
	// A SandboxSide backend (e.g. Tart) keeps the work copy inside the sandbox:
	// it is staged via VirtioFS, moved to local VM storage, and baselined inside
	// the VM after start. A HostSide backend (Docker) baselines on the host
	// immediately after copying.
	if runtime.LocalityOf(rt) == runtime.LocalitySandboxSide {
		// Baseline will be created in-sandbox after start; return empty SHA to
		// signal deferral.
		slog.Debug("setupWorkdir: SandboxSide backend, deferring baseline to sandbox",
			"backend", rt.Descriptor().Type)
		return "", nil
	}
	slog.Debug("setupWorkdir: HostSide backend, creating baseline on host",
		"backend", rt.Descriptor().Type)

	// Docker: preserve original git history so the agent (and user) can
	// git log, git show, git blame, etc. inside the sandbox.
	// If the source was a git repo with commits, just record HEAD as baseline.
	// For non-git directories or empty repos, create a fresh repo.
	if git.IsGitRepo(workCopyDir) {
		return createBaselineForGitRepo(ctx, g, workCopyDir)
	}
	sha, err := g.Baseline(ctx, workCopyDir)
	if err != nil {
		return "", fmt.Errorf("git baseline: %w", err)
	}
	return sha, nil
}

// createBaselineForGitRepo creates a baseline for a directory that is already a git repo.
func createBaselineForGitRepo(ctx context.Context, g *git.Git, workCopyDir string) (string, error) {
	_, err := g.HeadSHA(ctx, workCopyDir)
	if err != nil {
		// Git repo exists but has no commits (or is broken).
		// Remove .git and create fresh baseline.
		if rmErr := workspace.RemoveGitDirs(workCopyDir); rmErr != nil {
			return "", fmt.Errorf("remove invalid git dir: %w", rmErr)
		}
		sha, baselineErr := g.Baseline(ctx, workCopyDir)
		if baselineErr != nil {
			return "", fmt.Errorf("git baseline after removing invalid repo: %w", baselineErr)
		}
		return sha, nil
	}
	// Commit any pre-existing dirty changes so agent diffs are clean.
	sha, baselineErr := g.BaselineUncommittedChanges(ctx, workCopyDir)
	if baselineErr != nil {
		return "", fmt.Errorf("baseline pre-session state: %w", baselineErr)
	}
	return sha, nil
}

// setupAuxDirs prepares each auxiliary directory and returns its DirEnvironment
// slice. :copy and :overlay dirs get host-side content setup and a git baseline
// (same pipeline as the workdir). :rw and :ro dirs are pure reference mounts
// with no host-side preparation.
func setupAuxDirs(ctx context.Context, g *git.Git, sandboxDir string, rt runtime.Backend, auxDirs []*DirSpec) ([]store.DirEnvironment, error) {
	var dirEnvs []store.DirEnvironment
	for _, ad := range auxDirs {
		dm, err := setupAuxDir(ctx, g, sandboxDir, rt, ad)
		if err != nil {
			return nil, fmt.Errorf("setup aux dir %s: %w", ad.Path, err)
		}
		dirEnvs = append(dirEnvs, dm)
	}
	return dirEnvs, nil
}

// setupAuxDir prepares a single auxiliary directory and returns its
// DirEnvironment. For :copy and :overlay modes, host-side content is set up
// and a git baseline is created (D81, multi-workdir Phase 2). For :rw and :ro
// modes, the function packs the mount metadata without any host-side work.
//
// MountPath is recorded as the guest-visible path: backends that re-root
// mounts inside the guest (tart maps host dirs under /Users/admin/host/...)
// must advertise where the mount is actually reachable, so the generated
// CLAUDE.md, `info`, and MCP {dir:N} placeholders don't point at a path that
// doesn't exist in the guest. Identity for backends without translation.
func setupAuxDir(ctx context.Context, g *git.Git, sandboxDir string, rt runtime.Backend, ad *DirSpec) (store.DirEnvironment, error) {
	switch ad.Mode {
	case DirModeCopy, DirModeOverlay:
		workCopyDir := store.WorkDir(sandboxDir, ad.Path)
		if err := setupDirContent(sandboxDir, ad, workCopyDir); err != nil {
			return store.DirEnvironment{}, err
		}
		baselineSHA, err := createDirBaseline(ctx, g, ad, workCopyDir, rt)
		if err != nil {
			return store.DirEnvironment{}, err
		}
		return store.DirEnvironment{
			HostPath:     ad.Path,
			MountPath:    launch.OverlayOrResolvedMountPath(ad),
			Mode:         ad.Mode,
			BaselineSHA:  baselineSHA,
			InceptionSHA: baselineSHA,
		}, nil
	default: // rw, ro
		return store.DirEnvironment{
			HostPath:  ad.Path,
			MountPath: runtime.ResolveGuestMountPathFor(rt, ad.ResolvedMountPath()),
			Mode:      ad.Mode,
		}, nil
	}
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

// collectOverlayMounts builds overlay mount configs for config.json from the
// workdir and any :overlay aux dirs (D81, multi-workdir Phase 2). Kept as a
// function returning a slice so callers don't need to special-case
// overlay-vs-no-overlay at every config.json assembly site.
func collectOverlayMounts(workdir *DirSpec, auxDirs []*DirSpec) []runtimeconfig.OverlayMountConfig {
	var mounts []runtimeconfig.OverlayMountConfig
	for _, d := range append([]*DirSpec{workdir}, auxDirs...) {
		if d.Mode != "overlay" {
			continue
		}
		base := "/yoloai/overlay/" + store.EncodePath(d.Path) + "/"
		mounts = append(mounts, runtimeconfig.OverlayMountConfig{
			Lower:  base + "lower",
			Upper:  base + "upper",
			Work:   base + "ovlwork",
			Merged: base + "merged",
		})
	}
	return mounts
}

// collectCopyDirs returns the mount paths of all :copy dirs — workdir and any
// :copy aux dirs (D81, multi-workdir Phase 2). The function shape (returning a
// slice) means callers don't need to special-case the no-copy and copy cases at
// every assembly site.
func collectCopyDirs(workdir *DirSpec, auxDirs []*DirSpec) []string {
	var dirs []string
	for _, d := range append([]*DirSpec{workdir}, auxDirs...) {
		if d.Mode == "copy" {
			dirs = append(dirs, d.ResolvedMountPath())
		}
	}
	return dirs
}

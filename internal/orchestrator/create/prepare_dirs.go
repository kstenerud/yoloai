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
	"github.com/kstenerud/yoloai/internal/envsetup"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/git"
	"github.com/kstenerud/yoloai/internal/netpolicy"
	"github.com/kstenerud/yoloai/internal/orchestrator/envspec"
	"github.com/kstenerud/yoloai/internal/orchestrator/launch"
	"github.com/kstenerud/yoloai/internal/orchestrator/state"
	"github.com/kstenerud/yoloai/internal/orchestrator/workcopy"
	"github.com/kstenerud/yoloai/internal/workspace"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/store"
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
	auth := envsetup.ResolveAuthPresence(envspec.BuildEnvSpec(agentDef), mergedEnv, d.Layout)
	if err := checkAgentAuth(agentDef, auth, outputFor(opts.Output)); err != nil {
		return err
	}

	// Local model server requires a model
	if !auth.APIKey && !auth.AuthFile && auth.AuthHint && opts.Model == "" && cfgModel == "" {
		return yoerrors.NewUsageError("a model is required when using a local model server: use --model or 'yoloai config set model <model>'")
	}

	return checkLocalhostURLs(d, agentDef, mergedEnv)
}

// checkAgentAuth verifies that the agent has the necessary authentication configured.
func checkAgentAuth(agentDef *agent.Definition, auth envsetup.AuthPresence, output io.Writer) error {
	if auth.OK() {
		return nil
	}
	if agentDef.AuthOptional {
		fmt.Fprintf(output, "Warning: no authentication detected for %s (it may use credentials yoloai cannot check)\n", agentDef.Type) //nolint:errcheck // best-effort warning
		return nil
	}
	msg := fmt.Sprintf("no authentication found for %s: set %s",
		agentDef.Type, strings.Join(agentDef.APIKeyEnvVars, "/"))
	if authDesc := envsetup.DescribeSeedAuthFiles(envspec.BuildEnvSpec(agentDef)); authDesc != "" {
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
// existence. All modes are permitted: :copy enables the diff/apply workflow
// for multiple directories (D81, multi-workdir Phase 2), :rw provides live-edit
// access, and :ro (the default when unset) is read-only. (:overlay retired — D109.)
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
		if ad.Mode == "copy" || ad.Mode == "rw" {
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

// setupWorkdir copies the workdir, strips git metadata, and creates
// the git baseline. Returns the work copy directory path and baseline SHA.
// For backends implementing WorkDirSetup (e.g., Tart), baseline creation is
// deferred until the VM starts, and this function returns empty SHA.
func setupWorkdir(ctx context.Context, g *git.Git, sandboxDir string, workdir *DirSpec, rt runtime.Backend) (string, string, error) {
	workCopyDir := store.WorkDir(sandboxDir, workdir.Path)

	if workdir.Mode == DirModeCopy {
		sha, err := materializeCopyDir(ctx, g, workdir, workCopyDir, rt)
		if err != nil {
			return "", "", err
		}
		return workCopyDir, sha, nil
	}

	// :rw / :ro: the real mount is a live bind-mount; this is only a placeholder
	// directory. No work copy, no baseline (DF121).
	if err := fileutil.MkdirAll(workCopyDir, 0750); err != nil {
		return "", "", fmt.Errorf("create work dir: %w", err)
	}
	return workCopyDir, "", nil
}

// materializeCopyDir runs the shared work-copy materialization for a :copy dir
// and surfaces the history warnings create emits. rw/ro dirs never reach here —
// their callers dispatch on mode first. This is the single create-side seam onto
// workcopy.Materialize, so the workdir and each aux :copy dir go through the same
// sequence create and reset share (the archived workdir-materialization plan).
func materializeCopyDir(ctx context.Context, g *git.Git, dir *DirSpec, workCopyDir string, rt runtime.Backend) (string, error) {
	sha, notice, err := workcopy.Materialize(ctx, workcopy.Spec{
		Src:            dir.Path,
		IncludeIgnored: dir.IncludeIgnored,
		StripHistory:   dir.StripHistory,
	}, workCopyDir, workcopy.WipeAndCopy, g, rt)
	if err != nil {
		return "", fmt.Errorf("materialize %s: %w", dir.Path, err)
	}
	if notice.SourceIsGitLink {
		slog.Warn("git history not preserved: this directory keeps its git dir outside itself (linked worktree or submodule); the sandbox starts from a fresh baseline",
			"event", "sandbox.copy.gitlink_history_dropped", "dir", dir.Path)
	}
	return sha, nil
}

// setupAuxDirs prepares each auxiliary directory and returns its DirEnvironment
// slice. :copy dirs get host-side content setup and a git baseline
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
// DirEnvironment. For :copy mode, host-side content is set up and a git
// baseline is created (D81, multi-workdir Phase 2). For :rw and :ro modes,
// the function packs the mount metadata without any host-side work.
//
// MountPath is recorded as the guest-visible path: backends that re-root
// mounts inside the guest (tart maps host dirs under /Users/admin/host/...)
// must advertise where the mount is actually reachable, so the generated
// CLAUDE.md, `info`, and MCP {dir:N} placeholders don't point at a path that
// doesn't exist in the guest. Identity for backends without translation.
func setupAuxDir(ctx context.Context, g *git.Git, sandboxDir string, rt runtime.Backend, ad *DirSpec) (store.DirEnvironment, error) {
	switch ad.Mode {
	case DirModeCopy:
		workCopyDir := store.WorkDir(sandboxDir, ad.Path)
		baselineSHA, err := materializeCopyDir(ctx, g, ad, workCopyDir, rt)
		if err != nil {
			return store.DirEnvironment{}, err
		}
		return store.DirEnvironment{
			HostPath:       ad.Path,
			MountPath:      launch.WorkdirMountPath(ad),
			Mode:           ad.Mode,
			BaselineSHA:    baselineSHA,
			InceptionSHA:   baselineSHA,
			IncludeIgnored: ad.IncludeIgnored,
			StripHistory:   ad.StripHistory,
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
	return netpolicy.Compose(string(opts.Network), agentDef.NetworkAllowlist, opts.NetworkAllow)
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

package orchestrator

// ABOUTME: The v3->v4 overlay-flatten migrator — captures each overlay sandbox's
// ABOUTME: merged tree into a :copy work dir via the crash-safe promotion, stamps last.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/migrate"
	"github.com/kstenerud/yoloai/internal/orchestrator/status"
	"github.com/kstenerud/yoloai/internal/workspace"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/store"
)

// OverlayFlatten is the framework migrator that takes the library realm from
// schema v3 to v4: it converts every :overlay sandbox to :copy by capturing the
// running container's merged overlay tree, then :overlay is retired as a mode
// (Phase 4). It reads overlay sandboxes straight off disk, so a no-overlay
// install never opens a runtime — the common path (and every unit test) needs no
// backend. It stamps v4 LAST, only after the per-sandbox pass is durable.
type OverlayFlatten struct {
	// runtimeFor builds the backend runtime on demand — called only when overlay
	// sandboxes are actually present, so no-overlay installs never open a backend.
	runtimeFor func(ctx context.Context) (runtime.Backend, error)
	rt         runtime.Backend // cached; closed by Cleanup

	layout        config.Layout
	home          string // $YOLOAI_HOME (scratch lives here; same FS as sandboxesRoot)
	sandboxesRoot string // F7-resolved location of the sandboxes dir
	goos          string // host GOOS, for the stopped-overlay recoverability branch
}

// NewOverlayFlatten constructs the v3->v4 migrator. runtimeFor is invoked lazily.
func NewOverlayFlatten(layout config.Layout, home, sandboxesRoot, goos string, runtimeFor func(ctx context.Context) (runtime.Backend, error)) *OverlayFlatten {
	return &OverlayFlatten{runtimeFor: runtimeFor, layout: layout, home: home, sandboxesRoot: sandboxesRoot, goos: goos}
}

func (o *OverlayFlatten) Describe() string { return "v3->v4 overlay flatten" }

// Cleanup closes the runtime if one was opened.
func (o *OverlayFlatten) Cleanup() {
	if o.rt != nil {
		_ = o.rt.Close() //nolint:errcheck // best-effort
		o.rt = nil
	}
}

func (o *OverlayFlatten) backend(ctx context.Context) (runtime.Backend, error) {
	if o.rt == nil {
		rt, err := o.runtimeFor(ctx)
		if err != nil {
			return nil, fmt.Errorf("connect to runtime: %w", err)
		}
		o.rt = rt
	}
	return o.rt, nil
}

// Plan enumerates overlay sandboxes off disk (no runtime) and classifies each by
// its container status. It contacts the backend only when overlay sandboxes are
// actually present.
func (o *OverlayFlatten) Plan(ctx context.Context) (migrate.Plan, error) {
	names, err := o.overlaySandboxNames()
	if err != nil {
		return migrate.Plan{}, err
	}
	ops := make([]migrate.Op, 0, len(names))
	for _, name := range names {
		st, err := o.status(ctx, name)
		if err != nil {
			return migrate.Plan{}, err
		}
		ops = append(ops, classifyOverlay(name, st, o.goos))
	}
	return migrate.Plan{Ops: ops}, nil
}

// Apply flattens each overlay sandbox per the decision, then — only once every
// sandbox is migrated or quarantined — stamps the realm to v4 (AtomicWriteFile's
// fsync barrier makes the stamp physically incapable of preceding the data).
func (o *OverlayFlatten) Apply(ctx context.Context, d migrate.Decision) (migrate.Report, error) {
	var report migrate.Report
	names, err := o.overlaySandboxNames()
	if err != nil {
		return report, err
	}
	for _, name := range names {
		st, err := o.status(ctx, name)
		if err != nil {
			return report, err
		}
		rep, err := o.flattenOne(ctx, name, st, d)
		report.Merge(rep)
		if err != nil {
			return report, err
		}
	}
	if err := fileutil.AtomicWriteFile(o.layout.SchemaVersionPath(),
		[]byte(strconv.Itoa(config.LibrarySchemaVersion)), 0600); err != nil {
		return report, fmt.Errorf("stamp library v%d: %w", config.LibrarySchemaVersion, err)
	}
	return report, nil
}

// classifyOverlay maps a sandbox's status to the operation (and required
// approval) the flatten would perform. Pure, so it is exhaustively unit-tested.
func classifyOverlay(name string, st status.Status, goos string) migrate.Op {
	switch st {
	case status.StatusActive, status.StatusIdle, status.StatusDone, status.StatusFailed:
		return migrate.Op{Description: "flatten overlay sandbox " + name + " to copy mode", Auth: migrate.AuthNone, Sandbox: name}
	case status.StatusStopped:
		if goos == "darwin" {
			return migrate.Op{Description: "abandon stopped overlay sandbox " + name + " (macOS: its uncommitted overlay changes were already lost at stop)", Auth: migrate.AuthAbandonOverlay, Sandbox: name}
		}
		return migrate.Op{Description: "abandon stopped overlay sandbox " + name + " (Linux: recoverable — downgrade + start it first, or proceed to abandon its overlay changes)", Auth: migrate.AuthAbandonOverlay, Sandbox: name}
	case status.StatusRemoved:
		return migrate.Op{Description: "abandon overlay sandbox " + name + " (container removed; overlay changes unrecoverable)", Auth: migrate.AuthAbandonOverlay, Sandbox: name}
	default: // Broken, Unavailable, Suspended
		return migrate.Op{Description: "quarantine sandbox " + name + " (cannot audit — repair it or start the backend, then re-run migrate)", Auth: migrate.AuthConfirm, Sandbox: name}
	}
}

// flattenOne performs the transform for one overlay sandbox, re-validating its
// status. A running sandbox is captured and promoted; a stopped/removed one is
// abandon-flattened onto its pristine lower (authorized by --abandon-stopped-
// overlay); anything unauditable is quarantined.
func (o *OverlayFlatten) flattenOne(ctx context.Context, name string, st status.Status, d migrate.Decision) (migrate.Report, error) {
	switch st {
	case status.StatusActive, status.StatusIdle, status.StatusDone, status.StatusFailed:
		return o.flattenRunning(ctx, name)
	case status.StatusStopped, status.StatusRemoved:
		if !d.AbandonStoppedOverlay {
			return migrate.Report{}, fmt.Errorf("sandbox %q is not running and abandoning its overlay changes was not authorized; re-run with --abandon-stopped-overlay (or start it and re-run)", name)
		}
		return o.flattenAbandon(name)
	default:
		if !d.Yes {
			return migrate.Report{}, fmt.Errorf("sandbox %q cannot be audited and quarantine was not authorized", name)
		}
		return o.quarantine(name)
	}
}

// flattenRunning captures the running container's merged overlay tree and
// promotes the sandbox to :copy via the crash-safe promotion, then stops the
// container (its overlay mount is now stale). The displaced original (the old
// upper) is dropped, not trashed — redundant with the captured copy, and
// secret-bearing.
func (o *OverlayFlatten) flattenRunning(ctx context.Context, name string) (migrate.Report, error) {
	prom := migrate.Promotion{
		Parent:           o.sandboxesRoot,
		Name:             name,
		ScratchDir:       filepath.Join(migrate.ScratchPath(o.home), "build"),
		Build:            func(dst string) error { return o.buildFlattened(ctx, name, dst) },
		WriteReadyMarker: o.writeCopyModeEnv,
		IsReady:          o.isCopyMode,
		DisposeOrig:      migrate.DropDisposer,
	}
	if err := prom.Run(); err != nil {
		return migrate.Report{}, fmt.Errorf("flatten %q: %w", name, err)
	}

	report := migrate.Report{Migrated: []string{name}}
	rt, err := o.backend(ctx)
	if err != nil {
		return report, err
	}
	// Stop the container: its overlay mount now points at swapped-away dirs. A
	// failed stop is non-fatal — the on-disk form is already :copy and a restart
	// mounts copy cleanly — but is reported as a lingering container.
	if err := rt.Stop(ctx, store.InstanceName("", name)); err != nil {
		report.Notes = append(report.Notes, fmt.Sprintf("sandbox %q: could not stop the container after flattening (%v); remove it manually", name, err))
	} else {
		report.Notes = append(report.Notes, fmt.Sprintf("stopped sandbox %q to finalize overlay->copy; restart to resume in copy mode", name))
	}
	return report, nil
}

// buildFlattened captures the merged overlay tree of name's overlay dirs and
// writes the whole rebuilt work/ dir into dst (the promotion scratch), in copy
// mode layout (files directly under work/<enc>, no overlay subdirs). Non-overlay
// work subdirs are carried faithfully.
func (o *OverlayFlatten) buildFlattened(ctx context.Context, name, dst string) error {
	sandboxDir := o.layout.SandboxDir(name)
	env, err := store.LoadEnvironment(sandboxDir)
	if err != nil {
		return fmt.Errorf("load environment for %q: %w", name, err)
	}
	dstWork := filepath.Join(dst, "work")
	if err := fileutil.MkdirAll(dstWork, 0o750); err != nil {
		return err
	}
	for _, dir := range allDirs(env) {
		enc := store.EncodePath(dir.HostPath)
		if dir.Mode != store.DirModeOverlay {
			if err := workspace.CopyPathFaithful(store.WorkDir(sandboxDir, dir.HostPath), filepath.Join(dstWork, enc)); err != nil {
				return fmt.Errorf("carry work dir %s: %w", enc, err)
			}
			continue
		}
		staged, err := o.captureMerged(ctx, name, dir.HostPath)
		if err != nil {
			return err
		}
		if err := workspace.CopyPathFaithful(staged, filepath.Join(dstWork, enc)); err != nil {
			return fmt.Errorf("stage flattened work dir %s: %w", enc, err)
		}
	}
	return nil
}

const captureStageName = "yoloai-flatten-capture"

// captureMerged copies the kernel-assembled merged overlay tree out of the
// running container into a host-visible staging dir under the sandbox's own
// bind-mounted overlay base, and returns that host path. The copy is a plain
// in-container `cp -a` of the merged mount — whiteout/opaque-clean by
// construction (userspace sees a normal POSIX tree), no git/tar/baseline, so it
// captures gitignored + uncommitted state exactly and never touches the DF70
// host-git or C1 filter classes.
//
// NOTE: the container-side overlay paths are validated by the Phase-3b Docker
// integration test; captureContainerPaths is the single point to adjust.
func (o *OverlayFlatten) captureMerged(ctx context.Context, name, hostPath string) (string, error) {
	rt, err := o.backend(ctx)
	if err != nil {
		return "", err
	}
	merged, stage := captureContainerPaths(hostPath)
	script := fmt.Sprintf("set -e; rm -rf %s; mkdir -p %s; cp -a %s/. %s/", stage, stage, merged, stage)
	res, err := rt.Exec(ctx, store.InstanceName("", name), []string{"sh", "-c", script}, "yoloai")
	if err != nil {
		return "", fmt.Errorf("capture merged tree for %q: %w", name, err)
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("capture merged tree for %q: exit %d: %s", name, res.ExitCode, res.Stdout)
	}
	hostStage := filepath.Join(store.WorkDir(o.layout.SandboxDir(name), hostPath), captureStageName)
	if _, err := os.Stat(hostStage); err != nil {
		return "", fmt.Errorf("captured tree not visible host-side at %s: %w", hostStage, err)
	}
	return hostStage, nil
}

// captureContainerPaths returns the in-container merged-mount path and the
// host-visible staging path (both container-side) for an overlay dir. The
// staging dir is a sibling of merged under the bind-mounted overlay base, so it
// lands host-side under WorkDir. Isolated here so the Phase-3b Docker test can
// pin the exact mount layout.
func captureContainerPaths(hostPath string) (merged, stage string) {
	base := "/yoloai/overlay/" + store.EncodePath(hostPath)
	return base + "/merged", base + "/" + captureStageName
}

// flattenAbandon converts a non-running overlay sandbox to :copy by dropping the
// overlay upper and keeping the pristine lower (the original workdir), abandoning
// the agent's overlay changes — authorized by the caller. On macOS the upper was
// already gone; on Linux the displaced upper is dropped, not trashed.
func (o *OverlayFlatten) flattenAbandon(name string) (migrate.Report, error) {
	prom := migrate.Promotion{
		Parent:           o.sandboxesRoot,
		Name:             name,
		ScratchDir:       filepath.Join(migrate.ScratchPath(o.home), "build"),
		Build:            func(dst string) error { return o.buildFromLower(name, dst) },
		WriteReadyMarker: o.writeCopyModeEnv,
		IsReady:          o.isCopyMode,
		DisposeOrig:      migrate.DropDisposer,
	}
	if err := prom.Run(); err != nil {
		return migrate.Report{}, fmt.Errorf("flatten (abandon) %q: %w", name, err)
	}
	return migrate.Report{
		Migrated: []string{name},
		Notes:    []string{fmt.Sprintf("sandbox %q: flattened onto its original workdir; overlay changes abandoned", name)},
	}, nil
}

// buildFromLower rebuilds work/ from each overlay dir's pristine lower (the
// original workdir copy), abandoning the upper. Non-overlay dirs carry verbatim.
func (o *OverlayFlatten) buildFromLower(name, dst string) error {
	sandboxDir := o.layout.SandboxDir(name)
	env, err := store.LoadEnvironment(sandboxDir)
	if err != nil {
		return fmt.Errorf("load environment for %q: %w", name, err)
	}
	dstWork := filepath.Join(dst, "work")
	if err := fileutil.MkdirAll(dstWork, 0o750); err != nil {
		return err
	}
	for _, dir := range allDirs(env) {
		enc := store.EncodePath(dir.HostPath)
		src := store.WorkDir(sandboxDir, dir.HostPath)
		if dir.Mode == store.DirModeOverlay {
			src = store.OverlayLowerDir(sandboxDir, dir.HostPath)
		}
		if err := workspace.CopyPathFaithful(src, filepath.Join(dstWork, enc)); err != nil {
			return fmt.Errorf("build work dir %s from lower: %w", enc, err)
		}
	}
	return nil
}

// quarantine sets an unauditable sandbox aside in trash/, preserving its data.
func (o *OverlayFlatten) quarantine(name string) (migrate.Report, error) {
	disposer := migrate.TrashDisposer(filepath.Join(o.home, "trash"))
	if err := disposer(o.layout.SandboxDir(name)); err != nil {
		return migrate.Report{}, fmt.Errorf("quarantine %q: %w", name, err)
	}
	return migrate.Report{Quarantined: []string{name}}, nil
}

// writeCopyModeEnv is the promotion ready-marker for a flattened sandbox: it
// rewrites the (repopulated) environment.json in newDir with every overlay dir
// flipped to :copy, its overlay baseline cleared. Written durably and last, so
// its presence (no overlay dirs) authorizes promotion.
func (o *OverlayFlatten) writeCopyModeEnv(newDir string) error {
	env, err := store.LoadEnvironment(newDir)
	if err != nil {
		return fmt.Errorf("load staged environment: %w", err)
	}
	for i := range env.Dirs {
		if env.Dirs[i].Mode == store.DirModeOverlay {
			env.Dirs[i].Mode = store.DirModeCopy
			// Overlay stored MountPath as the in-container merged path; copy mode
			// mirrors the host path (docker's ResolveCopyMount is identity, and
			// overlay is docker-only), so a restart mounts the work copy where the
			// agent expects it.
			env.Dirs[i].MountPath = env.Dirs[i].HostPath
			env.Dirs[i].BaselineSHA = ""
		}
	}
	if err := store.SaveEnvironment(newDir, env); err != nil {
		return fmt.Errorf("write copy-mode environment: %w", err)
	}
	return nil
}

// isCopyMode reports whether dir's environment.json has no overlay dirs left —
// the durable "flattened" form the promotion recovery reads. A missing
// environment.json (the new dir before repopulate copies it in) is "not ready".
func (o *OverlayFlatten) isCopyMode(dir string) (bool, error) {
	env, err := store.LoadEnvironment(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	for _, d := range env.Dirs {
		if d.Mode == store.DirModeOverlay {
			return false, nil
		}
	}
	return true, nil
}

// overlaySandboxNames lists sandbox names whose on-disk form still uses :overlay
// for any dir. Reads environment.json directly — no runtime.
func (o *OverlayFlatten) overlaySandboxNames() ([]string, error) {
	entries, err := os.ReadDir(o.sandboxesRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read sandboxes dir: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		env, err := store.LoadEnvironment(filepath.Join(o.sandboxesRoot, e.Name()))
		if err != nil {
			continue // unreadable/foreign dir — the status pass surfaces it if it matters
		}
		if hasOverlayDir(env) {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// status reports name's current container status via the lazy runtime.
func (o *OverlayFlatten) status(ctx context.Context, name string) (status.Status, error) {
	rt, err := o.backend(ctx)
	if err != nil {
		return "", err
	}
	return status.DetectStatus(ctx, rt, store.InstanceName("", name), o.layout.SandboxDir(name))
}

func hasOverlayDir(env *store.Environment) bool {
	for _, d := range allDirs(env) {
		if d.Mode == store.DirModeOverlay {
			return true
		}
	}
	return false
}

// allDirs returns the workdir followed by aux dirs.
func allDirs(env *store.Environment) []store.DirEnvironment {
	dirs := make([]store.DirEnvironment, 0, 1+len(env.AuxDirs()))
	if wd := env.Workdir(); wd != nil {
		dirs = append(dirs, *wd)
	}
	dirs = append(dirs, env.AuxDirs()...)
	return dirs
}

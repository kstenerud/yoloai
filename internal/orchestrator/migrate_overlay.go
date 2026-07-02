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
	// runtimeFor builds a runtime for a specific backend on demand — called only
	// when overlay sandboxes are actually present, and once per distinct backend,
	// so no-overlay installs never open a backend.
	runtimeFor func(ctx context.Context, backend runtime.BackendType) (runtime.Backend, error)
	rts        map[runtime.BackendType]runtime.Backend // cached per backend; closed by Cleanup

	layout        config.Layout
	home          string // $YOLOAI_HOME (scratch lives here; same FS as sandboxesRoot)
	sandboxesRoot string // F7-resolved location of the sandboxes dir
	goos          string // host GOOS, for the stopped-overlay recoverability branch
}

// NewOverlayFlatten constructs the v3->v4 migrator. runtimeFor is invoked lazily,
// once per distinct backend the overlay sandboxes were created with.
func NewOverlayFlatten(layout config.Layout, home, sandboxesRoot, goos string, runtimeFor func(ctx context.Context, backend runtime.BackendType) (runtime.Backend, error)) *OverlayFlatten {
	return &OverlayFlatten{runtimeFor: runtimeFor, rts: map[runtime.BackendType]runtime.Backend{}, layout: layout, home: home, sandboxesRoot: sandboxesRoot, goos: goos}
}

func (o *OverlayFlatten) Describe() string { return "v3->v4 overlay flatten" }

// Cleanup closes every runtime opened during the migration.
func (o *OverlayFlatten) Cleanup() {
	for k, rt := range o.rts {
		_ = rt.Close()
		delete(o.rts, k)
	}
}

// backendFor resolves (and caches) the runtime for the backend that created
// sandbox name, read from its stored environment. Overlay was not docker-only —
// docker, podman, and apple all supported it — so each sandbox is contacted
// through its own backend. A single hardcoded backend would misreport a live
// podman/apple sandbox as removed and risk abandoning its uncommitted work.
func (o *OverlayFlatten) backendFor(ctx context.Context, name string) (runtime.Backend, error) {
	bt, err := o.sandboxBackend(name)
	if err != nil {
		return nil, err
	}
	if rt, ok := o.rts[bt]; ok {
		return rt, nil
	}
	rt, err := o.runtimeFor(ctx, bt)
	if err != nil {
		return nil, fmt.Errorf("connect to %s runtime: %w", bt, err)
	}
	o.rts[bt] = rt
	return rt, nil
}

// sandboxBackend reads the backend that created name from its environment.
// LoadEnvironment defaults a legacy empty backend to docker, so this is never
// empty for a real sandbox.
func (o *OverlayFlatten) sandboxBackend(name string) (runtime.BackendType, error) {
	env, err := store.LoadEnvironment(o.layout.SandboxDir(name))
	if err != nil {
		return "", fmt.Errorf("load environment for %q: %w", name, err)
	}
	return env.BackendType, nil
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
		op := classifyOverlay(name, st, o.goos)
		// A flatten (running-capture or abandon) runs host-side, so it can't
		// proceed if the sandbox's state is owned by host subuids (rootless
		// backend). Surface that in the plan as a hard block. Quarantine
		// (AuthConfirm) is exempt: it only renames the sandbox dir, which survives
		// subuid-owned children.
		if op.Auth != migrate.AuthConfirm {
			if reason := o.hostUnmanageableReason(name); reason != "" {
				op = migrate.Op{Description: reason, Auth: migrate.AuthBlocked, Sandbox: name}
			}
		}
		ops = append(ops, op)
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
	// Single-filesystem precondition (crash-safe-migration decision 4): scratch
	// (under home) and the sandboxes root hold the promotion's rename endpoints,
	// so a cross-FS split would make the scratch->U_^^_new move-in fail with
	// EXDEV mid-flatten. Cheap insurance, re-checked against current on-disk state
	// before any sandbox is touched. Skipped when there is nothing to flatten —
	// the sandboxes dir may not exist and the stamp-only path moves nothing.
	if len(names) > 0 {
		if err := migrate.SameFilesystem(o.home, o.sandboxesRoot); err != nil {
			return report, fmt.Errorf("migration preflight: %w", err)
		}
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

// assertHostOwnedState is the apply-time guard: it refuses (as an error) any
// sandbox the plan would have flagged AuthBlocked, so a flatten never begins on
// state it can't finish. Defense in depth — the plan already surfaces the same
// reason, but the state can drift between plan and apply.
func (o *OverlayFlatten) assertHostOwnedState(name string) error {
	if reason := o.hostUnmanageableReason(name); reason != "" {
		return errors.New(reason)
	}
	return nil
}

// hostUnmanageableReason returns a user-facing explanation when the sandbox's
// host-side state is owned by a user id the invoking user can neither read nor
// remove, or "" when it is manageable. The flatten runs entirely host-side
// (repopulate copies the sandbox's non-work state into the new dir; the disposer
// removes the old dir), so it needs host access to every top-level entry except
// work/ (whose overlay layers are reclaimed to the host user during capture).
// Rootless backends with a userns that maps the container's users to host
// subuids — podman-rootless — write runtime state (agent-status.json, logs/,
// cache/, files/) under a subuid (e.g. 100999) at mode 0600, which the host
// process can't touch. Docker (rootful) maps container users to the host user or
// root, both host-manageable, so it is unaffected. Surfaced in the plan (a hard
// AuthBlocked op) and re-checked at apply, so the migration refuses cleanly with
// actionable guidance instead of failing mid-promotion and stranding a
// half-swapped sandbox.
func (o *OverlayFlatten) hostUnmanageableReason(name string) string {
	if fileutil.ProcessIsRoot() {
		return "" // root manages any ownership
	}
	sandboxDir := o.layout.SandboxDir(name)
	hostUID := fileutil.HostUID()
	entries, err := os.ReadDir(sandboxDir)
	if err != nil {
		return "" // an unreadable sandbox dir surfaces elsewhere; don't block on a stat hiccup
	}
	for _, e := range entries {
		if e.Name() == "work" {
			continue // overlay layers under work/ are reclaimed to the host user during capture
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		uid, ok := fileOwnerUID(info)
		if ok && uid != hostUID && uid != 0 {
			return fmt.Sprintf("sandbox %q can't be migrated in place: its runtime state (%s) is owned by uid %d, not you (uid %d) — a rootless backend (e.g. podman) maps the container's users to host subuids the migration can't read or remove", name, e.Name(), uid, hostUID)
		}
	}
	return ""
}

// promote runs the crash-safe promotion shared by both flatten paths: guard the
// host-owned-state precondition, then build->stage->swap the sandbox into :copy
// via a scratch dir under home. Only the Build step (which entries to write, and
// from where) differs between the running-capture and abandon paths; the ready
// marker, readiness predicate, and orig disposal (drop the secret-bearing upper)
// are identical.
func (o *OverlayFlatten) promote(name string, build func(dst string) error) error {
	if err := o.assertHostOwnedState(name); err != nil {
		return err
	}
	prom := migrate.Promotion{
		Parent:           o.sandboxesRoot,
		Name:             name,
		ScratchDir:       filepath.Join(migrate.ScratchPath(o.home), "build"),
		Build:            build,
		WriteReadyMarker: o.writeCopyModeEnv,
		IsReady:          o.isCopyMode,
		DisposeOrig:      migrate.DropDisposer,
	}
	return prom.Run()
}

// flattenRunning captures the running container's merged overlay tree and
// promotes the sandbox to :copy via the crash-safe promotion, then stops the
// container (its overlay mount is now stale). The displaced original (the old
// upper) is dropped, not trashed — redundant with the captured copy, and
// secret-bearing.
func (o *OverlayFlatten) flattenRunning(ctx context.Context, name string) (migrate.Report, error) {
	if err := o.promote(name, func(dst string) error { return o.buildFlattened(ctx, name, dst) }); err != nil {
		return migrate.Report{}, fmt.Errorf("flatten %q: %w", name, err)
	}

	report := migrate.Report{Migrated: []string{name}}
	rt, err := o.backendFor(ctx, name)
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

// buildWork rebuilds the whole work/ dir into dst (the promotion scratch) in
// copy-mode layout (files directly under work/<enc>, no overlay subdirs). srcFor
// resolves each dir's source tree — the only thing that varies between the
// capture and abandon paths; the enumerate/mkdir/copy scaffolding is shared.
func (o *OverlayFlatten) buildWork(name, dst string, srcFor func(sandboxDir string, dir store.DirEnvironment) (string, error)) error {
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
		src, err := srcFor(sandboxDir, dir)
		if err != nil {
			return err
		}
		if err := workspace.CopyPathFaithful(src, filepath.Join(dstWork, enc)); err != nil {
			return fmt.Errorf("build work dir %s: %w", enc, err)
		}
	}
	return nil
}

// buildFlattened captures each overlay dir's kernel-merged tree (and carries
// non-overlay dirs verbatim) — the running-capture path's source resolver.
func (o *OverlayFlatten) buildFlattened(ctx context.Context, name, dst string) error {
	return o.buildWork(name, dst, func(sandboxDir string, dir store.DirEnvironment) (string, error) {
		if dir.Mode != store.DirModeOverlay {
			return store.WorkDir(sandboxDir, dir.HostPath), nil
		}
		return o.captureMerged(ctx, name, dir.HostPath)
	})
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
	rt, err := o.backendFor(ctx, name)
	if err != nil {
		return "", err
	}
	merged, stage := captureContainerPaths(hostPath)
	// Run as root: the overlay base dir isn't writable by the agent user on every
	// backend (podman-rootless denies the stage mkdir), and root can always read
	// the merged tree. Hand the staged copy to the invoking host user so the
	// host-side CopyPathFaithful can read it regardless of backend uid mapping.
	uid, gid := fileutil.HostUID(), fileutil.HostGID()
	script := fmt.Sprintf("set -e; rm -rf %s; mkdir -p %s; cp -a %s/. %s/; chown -R %d:%d %s; chmod -R u+rwX %s",
		stage, stage, merged, stage, uid, gid, stage, stage)
	res, err := rt.Exec(ctx, store.InstanceName("", name), []string{"sh", "-c", script}, "root")
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
	if err := o.reclaimOverlayLayers(ctx, rt, name, hostPath); err != nil {
		return "", err
	}
	return hostStage, nil
}

// reclaimOverlayLayers makes the writable overlay layers (upper, ovlwork)
// removable host-side by the invoking user, via the running container (root).
// On rootful Docker those layers are root-owned kernel overlayfs dirs; once the
// sandbox is flattened they ride into the displaced original (_^^_orig), which
// DropDisposer must delete host-side as the invoking user — and can't, without
// this. Two barriers are cleared: ownership (chown to the host user) AND
// permissions — the kernel creates the overlayfs workdir as mode 0000, so even
// the owner can't traverse it, defeating os.RemoveAll; chmod u+rwX restores
// owner access (X = +x on dirs only). lower is the user's read-only project
// (never touched) and merged is a container-namespaced mountpoint (empty
// host-side), so only upper/ovlwork are reclaimed. Best-effort (|| true): on
// rootless/userns backends the layers are already user-owned and this is a
// no-op; a genuine problem surfaces at the drop step rather than here.
func (o *OverlayFlatten) reclaimOverlayLayers(ctx context.Context, rt runtime.Backend, name, hostPath string) error {
	base := "/yoloai/overlay/" + store.EncodePath(hostPath)
	script := fmt.Sprintf(
		`for d in %s/upper %s/ovlwork; do chown -R %d:%d "$d"; chmod -R u+rwX "$d"; done 2>/dev/null || true`,
		base, base, fileutil.HostUID(), fileutil.HostGID())
	if _, err := rt.Exec(ctx, store.InstanceName("", name), []string{"sh", "-c", script}, "root"); err != nil {
		return fmt.Errorf("reclaim overlay layer ownership for %q: %w", name, err)
	}
	return nil
}

// captureContainerPaths returns the in-container merged-mount path and the
// host-visible staging path (both container-side) for an overlay dir. The
// staging dir is a sibling of merged under the bind-mounted overlay base, so it
// lands host-side under WorkDir. Docker-validated end to end at commit f5a914e5
// (before the overlay create path was removed, which made a live create-and-
// flatten integration test impossible); the abandon path is unit-tested here.
func captureContainerPaths(hostPath string) (merged, stage string) {
	base := "/yoloai/overlay/" + store.EncodePath(hostPath)
	return base + "/merged", base + "/" + captureStageName
}

// flattenAbandon converts a non-running overlay sandbox to :copy by dropping the
// overlay upper and keeping the pristine lower (the original workdir), abandoning
// the agent's overlay changes — authorized by the caller. On macOS the upper was
// already gone; on Linux the displaced upper is dropped, not trashed.
func (o *OverlayFlatten) flattenAbandon(name string) (migrate.Report, error) {
	if err := o.promote(name, func(dst string) error { return o.buildFromLower(name, dst) }); err != nil {
		return migrate.Report{}, fmt.Errorf("flatten (abandon) %q: %w", name, err)
	}
	return migrate.Report{
		Migrated: []string{name},
		Notes:    []string{fmt.Sprintf("sandbox %q: flattened onto its original workdir; overlay changes abandoned", name)},
	}, nil
}

// buildFromLower rebuilds work/ from each overlay dir's pristine lower (the
// original workdir copy), abandoning the upper — the abandon path's source
// resolver. Non-overlay dirs carry verbatim.
func (o *OverlayFlatten) buildFromLower(name, dst string) error {
	return o.buildWork(name, dst, func(sandboxDir string, dir store.DirEnvironment) (string, error) {
		if dir.Mode == store.DirModeOverlay {
			return store.OverlayLowerDir(sandboxDir, dir.HostPath), nil
		}
		return store.WorkDir(sandboxDir, dir.HostPath), nil
	})
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
			// mirrors the host path. Every overlay-capable backend (docker, podman,
			// apple) resolves :copy mounts at the identity host path — none
			// implement CopyMountResolver — so a restart mounts the work copy where
			// the agent expects it. Clear both git range endpoints: the flattened
			// tree is a fresh capture with no baseline/inception commit yet.
			env.Dirs[i].MountPath = env.Dirs[i].HostPath
			env.Dirs[i].BaselineSHA = ""
			env.Dirs[i].InceptionSHA = ""
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

// status reports name's current container status via its own backend runtime.
func (o *OverlayFlatten) status(ctx context.Context, name string) (status.Status, error) {
	rt, err := o.backendFor(ctx, name)
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

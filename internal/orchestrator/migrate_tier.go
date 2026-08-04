package orchestrator

// ABOUTME: The v5->v6 tier migrator — rebuilds the whole sandboxes tree with each
// ABOUTME: sandbox split into host/ro/rw, verifies it in scratch, promotes once.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/config/pretier"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/migrate"
	"github.com/kstenerud/yoloai/internal/netpolicycfg"
	"github.com/kstenerud/yoloai/internal/orchestrator/agentcfg"
	"github.com/kstenerud/yoloai/internal/orchestrator/status"
	"github.com/kstenerud/yoloai/internal/workspace"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/store"
)

// TierLayout is the framework migrator that takes the library realm from schema
// v5 to v6: every sandbox directory stops being flat and becomes exactly three
// subdirectories — host/, ro/ and rw/ — so a file's guest-access class is the
// directory it sits in (DF136, DF148).
//
// It migrates by duplication (design/plans/migration-by-duplication.md): the
// whole sandboxes tree is rebuilt into scratch with the transform applied,
// verified there, and swapped in with one promotion. The live tree is read-only
// until the swap, so every failure mode above — an unclassifiable entry, a
// record that will not load from the staged tree, a full disk — discards scratch
// and leaves the realm byte-identical. That is the property the design exists
// for: a failed migration must leave a tree some released binary can still read,
// rather than one stranded between two schemas.
//
// It runs LAST in the ladder, which is what lets every migrator below it address
// a flat sandbox through internal/config/pretier (DF164).
type TierLayout struct {
	layout        config.Layout
	home          string // realm DataDir; scratch, lock and trash live here
	sandboxesRoot string

	// runtimeFor builds a runtime for one backend, to answer "is this sandbox
	// running" — the one question this migrator cannot answer from the disk.
	// Called only while planning, once per distinct backend.
	runtimeFor func(ctx context.Context, backend runtime.BackendType) (runtime.Backend, error)
	rts        map[runtime.BackendType]runtime.Backend
}

// NewTierLayout constructs the v5->v6 migrator. runtimeFor is invoked lazily,
// once per distinct backend, and ONLY to detect a running instance — never to
// build or verify the staged tree, which stays strictly host-side file
// inspection so a Linux host can migrate the tart and seatbelt sandboxes it
// cannot itself run.
func NewTierLayout(layout config.Layout, home, sandboxesRoot string, runtimeFor func(ctx context.Context, backend runtime.BackendType) (runtime.Backend, error)) *TierLayout {
	return &TierLayout{
		layout: layout, home: home, sandboxesRoot: sandboxesRoot,
		runtimeFor: runtimeFor,
		rts:        map[runtime.BackendType]runtime.Backend{},
	}
}

// Cleanup closes every runtime opened while planning.
func (t *TierLayout) Cleanup() {
	for k, rt := range t.rts {
		_ = rt.Close()
		delete(t.rts, k)
	}
}

// runningReason returns a refusal when the sandbox has a live backend instance,
// or "" when it does not.
//
// This is the one question the disk cannot answer, and it has to be asked: the
// promotion renames sandboxes/ out from under a running container, whose bind
// mounts keep pointing at the displaced inodes. The agent would go on writing
// into trash/ and lose everything the moment it was cleared — silently, because
// every host-side check still passes.
//
// A backend that cannot be constructed on this host is treated as not running,
// which is sound rather than lenient: a tart VM or a seatbelt process group
// cannot be live on a machine that cannot run tart or seatbelt. That is also
// what keeps a Linux host able to migrate those sandboxes at all.
func (t *TierLayout) runningReason(ctx context.Context, name string) string {
	env, err := store.LoadEnvironmentFrom(pretier.EnvironmentPath(t.layout.SandboxDir(name)))
	if err != nil || env.BackendType == "" {
		return ""
	}
	rt, ok := t.rts[env.BackendType]
	if !ok {
		rt, err = t.runtimeFor(ctx, env.BackendType)
		if err != nil {
			return "" // backend absent here, so nothing of its kind is running here
		}
		t.rts[env.BackendType] = rt
	}
	st, err := status.DetectStatus(ctx, rt, store.InstanceName(env.Principal, name), t.layout.SandboxDir(name))
	if err != nil || !isInstanceUp(st) {
		return ""
	}
	return fmt.Sprintf(
		"sandbox %q is running (%s) — stop it and re-run migrate; tiering replaces the sandboxes "+
			"directory, and a running instance would keep writing into the displaced copy. "+
			"Note `yoloai stop` will not do it from this build: every command except `system migrate` "+
			"refuses an out-of-date data directory, so use the yoloai release you were running before "+
			"the upgrade, or stop the backend instance directly (e.g. `docker stop %s`)",
		name, st, store.InstanceName(env.Principal, name))
}

func (t *TierLayout) Describe() string { return "v5->v6 sandbox directory tiering" }

// treeMarkerName is the promoted tree's own ready marker, holding the schema it
// was built for. The promotion's IsReady must test a marker *inside* the unit it
// swaps, and the realm stamp is a sibling of sandboxes/ rather than a child — so
// the tree carries its own. It is a plain int stamp, not a shape sniff, which is
// what keeps it on the right side of D61.
//
// It also makes crash recovery exact: a run that died between the promoting
// rename and the realm stamp finds a tree already marked ready, does nothing,
// and stamps the realm.
const treeMarkerName = ".tier-version"

// Plan classifies every sandbox and checks every precondition, reading only. A
// migration is supposed to fail here — once the commit sequence starts there is
// a window in which the tree is in a shape no released binary understands, and
// the only defence against a user getting stuck inside it is that nothing
// deterministic is left to go wrong by then.
func (t *TierLayout) Plan(ctx context.Context) (migrate.Plan, error) {
	current, _, err := config.ReadSchemaVersion(t.layout.SchemaVersionPath())
	if err != nil {
		return migrate.Plan{}, fmt.Errorf("read schema stamp: %w", err)
	}
	if current >= config.SchemaTiered {
		return migrate.Plan{}, nil // already tiered
	}
	names, err := t.sandboxNames()
	if err != nil {
		return migrate.Plan{}, err
	}
	if len(names) == 0 {
		// Nothing to move, but the realm still advances — Apply stamps it. An
		// empty plan is honest: no sandbox is touched.
		return migrate.Plan{}, nil
	}
	ops := make([]migrate.Op, 0, len(names)+1)
	for _, name := range names {
		op, err := t.classifySandbox(name)
		if err != nil {
			return migrate.Plan{}, err
		}
		if op.Auth != migrate.AuthBlocked {
			if reason := t.runningReason(ctx, name); reason != "" {
				op = migrate.Op{Description: reason, Auth: migrate.AuthBlocked, Sandbox: name}
			}
		}
		ops = append(ops, op)
	}
	space, report, err := t.spaceOp()
	if err != nil {
		return migrate.Plan{}, err
	}
	if report {
		ops = append(ops, space)
	}
	return migrate.Plan{Ops: ops}, nil
}

// classifySandbox decides what the migration would do to one sandbox, and
// refuses in the plan phase anything it could not finish.
func (t *TierLayout) classifySandbox(name string) (migrate.Op, error) {
	dir := t.layout.SandboxDir(name)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return migrate.Op{}, fmt.Errorf("read sandbox %q: %w", name, err)
	}
	if reason := hostUnmanageableTierReason(name, dir, entries); reason != "" {
		return migrate.Op{Description: reason, Auth: migrate.AuthBlocked, Sandbox: name}, nil
	}
	// A root already holding a tier directory is not the flat v5 shape this
	// migrator converts. No released version produces that state — a build of
	// the tiering branch does, since its host-tier writers create host/ as they
	// go — so it is a refusal rather than a case to handle: such a sandbox is
	// recreated, not migrated.
	for _, e := range entries {
		if config.IsTierName(e.Name()) {
			return migrate.Op{
				Description: fmt.Sprintf("sandbox %q already has a %q directory, so it is not the flat layout this migration converts; it was created by an unreleased build and must be destroyed and recreated", name, e.Name()),
				Auth:        migrate.AuthBlocked, Sandbox: name,
			}, nil
		}
	}
	if unknown := unrecognizedEntries(entries); len(unknown) > 0 {
		// Reported by name, but NOT a confirmation. The mover is total and host/
		// is the fail-safe direction, so there is no decision for the user to
		// make here — and making it a prompt would mean one stray .DS_Store
		// turned every macOS upgrade interactive and aborted every headless one.
		// The cost of the default is a guest-visible file becoming invisible,
		// which surfaces at runtime as a missing file; the plan names it up
		// front so that surprise is at least a documented one.
		return migrate.Op{
			Description: fmt.Sprintf("tier sandbox %s (%d entries; %d unclassified, moved to host/ where the guest cannot reach them: %v)",
				name, len(entries), len(unknown), unknown),
			Auth: migrate.AuthNone, Sandbox: name,
		}, nil
	}
	return migrate.Op{
		Description: fmt.Sprintf("tier sandbox %s (%d entries into host/, ro/ and rw/)", name, len(entries)),
		Auth:        migrate.AuthNone, Sandbox: name,
	}, nil
}

// spaceOp turns the free-space precondition into a plan op: an informational one
// when there is room, a hard block when there is not. Duplicating the tree needs
// 2x its size — the live tree plus the staged copy during build, then the new
// tree plus the displaced original in trash/ afterwards, which is the same peak.
//
// It is reported even when it passes: "this needs 40 GB free and will leave 40
// GB in trash/ until you clear it" is considerably more useful before committing
// than after. Below spaceReportFloor it is not — a couple of small sandboxes
// cost nothing worth a warning — so the informational half is suppressed there.
// The refusal is not: running out of space is a refusal at any size.
func (t *TierLayout) spaceOp() (op migrate.Op, report bool, err error) {
	size, err := migrate.TreeSize(t.sandboxesRoot)
	if err != nil {
		return migrate.Op{}, false, fmt.Errorf("measure sandboxes tree: %w", err)
	}
	free, err := migrate.FreeBytes(t.home)
	if err != nil {
		return migrate.Op{}, false, fmt.Errorf("check free space: %w", err)
	}
	need := size * 2
	if free < need {
		return migrate.Op{
			Description: fmt.Sprintf("not enough free space: tiering duplicates the sandboxes tree, needing %s free (2x %s), but only %s is available — free some space and re-run",
				fileutil.HumanSize(need), fileutil.HumanSize(size), fileutil.HumanSize(free)),
			Auth: migrate.AuthBlocked,
		}, true, nil
	}
	return migrate.Op{
		Description: fmt.Sprintf("duplicate the %s sandboxes tree (needs %s free, have %s); the displaced copy is kept in trash/ until you clear it",
			fileutil.HumanSize(size), fileutil.HumanSize(need), fileutil.HumanSize(free)),
		Auth: migrate.AuthNone,
	}, size >= spaceReportFloor, nil
}

// spaceReportFloor is where duplicating the tree stops being free and starts
// being something to tell the user about, both as a requirement beforehand and
// as an occupied trash/ afterwards.
const spaceReportFloor = 50 * 1024 * 1024

// Apply rebuilds the tree in scratch, verifies it there, promotes it, and stamps
// the realm last (D110). Every precondition is re-derived first: the plan may
// have been collected minutes ago.
func (t *TierLayout) Apply(ctx context.Context, d migrate.Decision) (migrate.Report, error) {
	var report migrate.Report
	plan, err := t.Plan(ctx)
	if err != nil {
		return report, err
	}
	if len(plan.Ops) == 0 {
		// Nothing to move (a fresh or already-tiered realm); still advance the
		// stamp so the ladder completes.
		return report, stampSchemaAdvancing(t.layout, config.SchemaTiered)
	}
	if ok, unmet := migrate.Authorize([]migrate.Plan{plan}, d); !ok {
		return report, fmt.Errorf("tiering was not authorized: %s", unmet[0].Description)
	}
	if err := migrate.SameFilesystem(t.home, t.sandboxesRoot); err != nil {
		return report, fmt.Errorf("migration preflight: %w", err)
	}

	size, err := migrate.TreeSize(t.sandboxesRoot)
	if err != nil {
		return report, err
	}
	prom := migrate.Promotion{
		Parent:           filepath.Dir(t.sandboxesRoot),
		Name:             filepath.Base(t.sandboxesRoot),
		ScratchDir:       filepath.Join(migrate.ScratchPath(t.home), "tier"),
		Build:            t.buildTieredTree,
		WriteReadyMarker: writeTreeMarker,
		IsReady:          treeIsTiered,
		DisposeOrig:      migrate.TrashDisposer(t.layout.TrashDir()),
	}
	if err := prom.Run(); err != nil {
		return report, fmt.Errorf("tier the sandboxes tree: %w", err)
	}
	if err := stampSchemaAdvancing(t.layout, config.SchemaTiered); err != nil {
		return report, err
	}
	report.Migrated = planSandboxNames(plan)
	if size >= spaceReportFloor {
		report.Notes = append(report.Notes, fmt.Sprintf(
			"the pre-tiering sandboxes tree (%s) was moved to %s — delete it once you are satisfied with the upgrade",
			fileutil.HumanSize(size), t.layout.TrashDir()))
	}
	return report, nil
}

// buildTieredTree writes the complete transformed tree into dst, then verifies
// it there.
//
// Complete, not incremental: the promotion's repopulate step carries over
// entries the build did not produce, and because this build produces every one
// of them its structural filter is empty by construction. That is what lets the
// tree ride the existing Promotion with no modification to it at all.
//
// Verification runs at the end of Build rather than between build and swap
// because Promotion.Run is a single call with no seam — and it needs none: Build
// runs before the promoting rename, so an error returned here propagates out
// with the live tree untouched.
func (t *TierLayout) buildTieredTree(dst string) error {
	entries, err := os.ReadDir(t.sandboxesRoot)
	if err != nil {
		return fmt.Errorf("read sandboxes dir: %w", err)
	}
	for _, e := range entries {
		src := filepath.Join(t.sandboxesRoot, e.Name())
		switch {
		case !e.IsDir() || !isSandboxDir(src):
			// Lock files and anything else that is not a sandbox ride across
			// untouched: the unit being promoted is the whole tree, so dropping
			// a sibling here would delete it.
			if err := workspace.CopyPathFaithful(src, filepath.Join(dst, e.Name())); err != nil {
				return fmt.Errorf("carry %s: %w", e.Name(), err)
			}
		default:
			if err := buildTieredSandbox(src, filepath.Join(dst, e.Name())); err != nil {
				return fmt.Errorf("tier sandbox %q: %w", e.Name(), err)
			}
		}
	}
	return t.verifyStagedTree(dst)
}

// buildTieredSandbox writes one sandbox's tiered form into dst: every root entry
// copied under the tier its name classifies into.
func buildTieredSandbox(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, tier := range []config.Tier{config.TierHost, config.TierReadOnly, config.TierReadWrite} {
		if err := fileutil.MkdirAll(config.TierDir(dst, tier), 0o750); err != nil {
			return err
		}
	}
	for _, e := range entries {
		tier, _ := config.TierOfEntry(e.Name())
		dstPath := filepath.Join(config.TierDir(dst, tier), e.Name())
		if err := workspace.CopyPathFaithful(filepath.Join(src, e.Name()), dstPath); err != nil {
			return fmt.Errorf("move %s into %s/: %w", e.Name(), tier, err)
		}
	}
	return nil
}

// verifyStagedTree checks the staged tree before anything is committed: every
// sandbox's four records load from their new tier paths, and every source entry
// is present at the tier path it classified into.
//
// It loads records rather than only counting files because the record
// relocation is the risky half — a count cannot tell a moved environment.json
// from a truncated one. It constructs no runtime and calls nothing that does:
// a Linux host migrates tart and seatbelt sandboxes it cannot run, so a check
// that needed a backend would pass on a developer's Mac and fail every Linux
// upgrade. And every loader it calls is a pure read (standards/go.md, "Readers
// do not mutate"), so verification cannot repair the defect it is looking for.
func (t *TierLayout) verifyStagedTree(dst string) error {
	names, err := t.sandboxNames()
	if err != nil {
		return err
	}
	for _, name := range names {
		staged := filepath.Join(dst, name)
		if err := verifyStagedRecords(name, staged); err != nil {
			return err
		}
		if err := verifyStagedEntries(name, t.layout.SandboxDir(name), staged); err != nil {
			return err
		}
	}
	return nil
}

// verifyStagedRecords loads each per-sandbox record from the staged tree through
// the live builders — which is correct here and nowhere else in a migrator: the
// staged tree IS the current layout, so the current builders are exactly what
// must be able to read it. That is the whole assertion.
func verifyStagedRecords(name, staged string) error {
	if _, err := store.LoadEnvironment(staged); err != nil {
		return fmt.Errorf("verify %q: environment.json unreadable after tiering: %w", name, err)
	}
	if _, err := store.LoadSandboxState(staged); err != nil {
		return fmt.Errorf("verify %q: sandbox-state.json unreadable after tiering: %w", name, err)
	}
	if _, err := agentcfg.Load(staged); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("verify %q: agent.json unreadable after tiering: %w", name, err)
	}
	if _, err := netpolicycfg.Load(staged); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("verify %q: netpolicy.json unreadable after tiering: %w", name, err)
	}
	return nil
}

// verifyStagedEntries checks that every entry of the source sandbox arrived at
// the tier path its name classifies into. The records get the semantic check
// above; bin/, logs/, work/ and friends get this counting one, because nothing
// else would notice them going missing.
func verifyStagedEntries(name, src, staged string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("verify %q: %w", name, err)
	}
	for _, e := range entries {
		tier, _ := config.TierOfEntry(e.Name())
		want := filepath.Join(config.TierDir(staged, tier), e.Name())
		if _, err := os.Lstat(want); err != nil {
			return fmt.Errorf("verify %q: %s did not arrive at %s/%s: %w", name, e.Name(), tier, e.Name(), err)
		}
	}
	return nil
}

// writeTreeMarker stamps the staged tree as tiered, durably, as the final step
// before the promoting rename. Its presence is what authorizes promotion.
func writeTreeMarker(newDir string) error {
	path := filepath.Join(newDir, treeMarkerName)
	if err := fileutil.AtomicWriteFile(path, fmt.Appendf(nil, "%d", config.SchemaTiered), 0600); err != nil {
		return fmt.Errorf("stamp tiered tree: %w", err)
	}
	return nil
}

// treeIsTiered reports whether dir carries the tiered marker at or above this
// migrator's target. It is also the promotion's done-vs-not-started
// discriminator for a lone tree, so a missing marker must read as "not ready"
// rather than as an error.
func treeIsTiered(dir string) (bool, error) {
	data, err := os.ReadFile(filepath.Join(dir, treeMarkerName)) //nolint:gosec // G304: a fixed name under a realm-owned dir
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	var v int
	if _, err := fmt.Sscanf(string(data), "%d", &v); err != nil {
		return false, fmt.Errorf("parse %s: %w", treeMarkerName, err)
	}
	return v >= config.SchemaTiered, nil
}

// sandboxNames lists the sandbox directories in the live tree, in a stable
// order. A dir with no environment.json is not a sandbox — an embedder's
// scratch dir, a stray mkdir — and is carried across untouched rather than
// tiered.
func (t *TierLayout) sandboxNames() ([]string, error) {
	entries, err := os.ReadDir(t.sandboxesRoot)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read sandboxes dir: %w", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && isSandboxDir(filepath.Join(t.sandboxesRoot, e.Name())) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// isSandboxDir reports whether dir holds a sandbox record at the pre-tier (flat)
// path — the layout this migrator's input is written in, addressed through
// pretier for the reason every migrator below v6 does (DF164).
func isSandboxDir(dir string) bool {
	_, err := os.Stat(pretier.EnvironmentPath(dir))
	return err == nil
}

// unrecognizedEntries returns the sorted names nothing classifies.
func unrecognizedEntries(entries []os.DirEntry) []string {
	var unknown []string
	for _, e := range entries {
		if _, ok := config.TierOfEntry(e.Name()); !ok {
			unknown = append(unknown, e.Name())
		}
	}
	sort.Strings(unknown)
	return unknown
}

// hostUnmanageableTierReason returns a user-facing explanation when a sandbox's
// state is owned by a uid the invoking user can neither read nor remove, or ""
// when it is manageable. The whole transform is host-side — every entry is
// copied and the original removed — so unlike the overlay flatten, which exempts
// work/, this one needs access to everything.
func hostUnmanageableTierReason(name, dir string, entries []os.DirEntry) string {
	if fileutil.ProcessIsRoot() {
		return "" // root manages any ownership
	}
	hostUID := fileutil.HostUID()
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		uid, ok := fileOwnerUID(info)
		if ok && uid != hostUID && uid != 0 {
			return fmt.Sprintf("sandbox %q can't be tiered: %s is owned by uid %d, not you (uid %d) — a rootless backend (e.g. podman) maps the container's users to host subuids the migration can't read or move; destroy the sandbox or re-run as its owner (%s)",
				name, e.Name(), uid, hostUID, dir)
		}
	}
	return ""
}

// planSandboxNames returns the sandboxes a plan names, for the report.
func planSandboxNames(p migrate.Plan) []string {
	var names []string
	for _, op := range p.Ops {
		if op.Sandbox != "" {
			names = append(names, op.Sandbox)
		}
	}
	return names
}

package orchestrator

// ABOUTME: The v4->v5 principal-rename migrator — the CLI adopts the "cli"
// ABOUTME: principal, so existing "yoloai-<name>" instances move to "yoloai-cli-<name>".
// ABOUTME: Also re-stamps a principal-authored profile image's ImageRef (DF126).

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/migrate"
	"github.com/kstenerud/yoloai/internal/orchestrator/status"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/store"
)

// PrincipalRename is the framework migrator that takes the library realm from
// schema v4 to v5: the CLI stops eliding its principal and adopts "cli" (D126),
// so every existing sandbox — whose instance was named "yoloai-<name>" under the
// old empty-principal scheme — must be moved to "yoloai-cli-<name>" and have its
// stored environment.json re-stamped with the new principal. Without this, the
// CLI would resolve "yoloai-cli-<name>", find nothing, and orphan every running
// container/VM while its work copy kept a quota slot.
//
// The rename is per-backend (P1/D126): docker, podman and tart rename in place
// (runtime.Renamer) preserving a running instance; seatbelt needs no backend op
// (its on-disk dir is the bare name); containerd and apple cannot rename (the
// name is the immutable container id / there is no rename verb), so a stopped
// instance is recreate-on-next-start (its old container is removed here) and a
// running one is refused until the user stops it. Modeled on OverlayFlatten: it
// reads sandboxes off disk, contacts a backend only when there is work, and
// stamps v5 LAST (guarded) so the stamp is never ahead of the data.
type PrincipalRename struct {
	// runtimeFor builds a runtime for a specific backend on demand — called only
	// when unmigrated sandboxes are present, once per distinct backend.
	runtimeFor func(ctx context.Context, backend runtime.BackendType) (runtime.Backend, error)
	rts        map[runtime.BackendType]runtime.Backend // cached per backend; closed by Cleanup

	layout        config.Layout // the CLI layout; layout.Principal is the target ("cli")
	sandboxesRoot string
}

// NewPrincipalRename constructs the v4->v5 migrator. runtimeFor is invoked
// lazily, once per distinct backend the unmigrated sandboxes were created with.
func NewPrincipalRename(layout config.Layout, sandboxesRoot string, runtimeFor func(ctx context.Context, backend runtime.BackendType) (runtime.Backend, error)) *PrincipalRename {
	return &PrincipalRename{
		runtimeFor:    runtimeFor,
		rts:           map[runtime.BackendType]runtime.Backend{},
		layout:        layout,
		sandboxesRoot: sandboxesRoot,
	}
}

func (p *PrincipalRename) Describe() string { return "v4->v5 principal rename" }

// Cleanup closes every runtime opened during the migration.
func (p *PrincipalRename) Cleanup() {
	for k, rt := range p.rts {
		_ = rt.Close()
		delete(p.rts, k)
	}
}

// Plan enumerates sandboxes whose stored principal is not yet the target and
// classifies each by its backend's rename capability and current status. It
// contacts a backend only when such sandboxes exist.
func (p *PrincipalRename) Plan(ctx context.Context) (migrate.Plan, error) {
	names, err := p.unmigratedSandboxNames()
	if err != nil {
		return migrate.Plan{}, err
	}
	ops := make([]migrate.Op, 0, len(names))
	for _, name := range names {
		rt, err := p.backendFor(ctx, name)
		if err != nil {
			return migrate.Plan{}, err
		}
		st, err := status.DetectStatus(ctx, rt, store.LegacyCLIInstanceName(name), p.layout.SandboxDir(name))
		if err != nil {
			return migrate.Plan{}, err
		}
		ops = append(ops, classifyPrincipalRename(name, p.backendType(name), st, backendHasInstance(rt), isRenamer(rt)))
	}
	return migrate.Plan{Ops: ops}, nil
}

// Apply migrates each unmigrated sandbox — rename, recreate-remove, or restamp-
// only per backend — then stamps v5 LAST (guarded against a downgrade), only
// after every sandbox's backend op and durable re-stamp is done.
func (p *PrincipalRename) Apply(ctx context.Context, d migrate.Decision) (migrate.Report, error) {
	var report migrate.Report
	names, err := p.unmigratedSandboxNames()
	if err != nil {
		return report, err
	}
	for _, name := range names {
		rep, err := p.migrateOne(ctx, name, d)
		report.Merge(rep)
		if err != nil {
			return report, err
		}
	}
	if err := stampSchemaAdvancing(p.layout, config.SchemaPrincipalRenamed); err != nil {
		return report, err
	}
	return report, nil
}

// classifyPrincipalRename maps a sandbox to the operation (and approval) the
// migration would perform, from its backend's capabilities and current status.
// Pure, so it is exhaustively unit-tested.
//
//   - No backend instance (seatbelt): a metadata-only re-stamp — the on-disk dir
//     is the bare name and the host process group is unaffected. AuthNone.
//   - Rename-capable (docker/podman/tart): rename in place, preserving a running
//     instance. AuthNone — unless the status can't be audited, which blocks.
//   - Recreate-only (containerd/apple): a running/suspended instance is refused
//     (recreating it would kill the live agent); a stopped/removed one is
//     recreate-on-next-start, which drops the container's writable layer and so
//     needs an explicit --yes (the host work copy is preserved either way).
func classifyPrincipalRename(name string, bt runtime.BackendType, st status.Status, hasInstance, renameCapable bool) migrate.Op {
	if !hasInstance {
		return migrate.Op{Description: fmt.Sprintf("adopt the cli principal for sandbox %s", name), Auth: migrate.AuthNone, Sandbox: name}
	}
	if renameCapable {
		if isUnauditable(st) {
			return migrate.Op{Description: fmt.Sprintf("cannot audit sandbox %s to rename it (start the backend or repair it, then re-run migrate)", name), Auth: migrate.AuthBlocked, Sandbox: name}
		}
		return migrate.Op{Description: fmt.Sprintf("rename sandbox %s's instance to yoloai-cli-%s", name, name), Auth: migrate.AuthNone, Sandbox: name}
	}
	switch {
	case isUnauditable(st):
		return migrate.Op{Description: fmt.Sprintf("cannot audit sandbox %s (start the %s backend or repair it, then re-run migrate)", name, bt), Auth: migrate.AuthBlocked, Sandbox: name}
	case isInstanceUp(st):
		return migrate.Op{Description: fmt.Sprintf("stop sandbox %s, then re-run migrate — %s cannot rename a running instance and recreating it would kill the running agent", name, bt), Auth: migrate.AuthBlocked, Sandbox: name}
	default: // stopped / removed
		return migrate.Op{Description: fmt.Sprintf("recreate sandbox %s under its new name (%s cannot rename; the container's writable layer is dropped, the work copy is preserved)", name, bt), Auth: migrate.AuthConfirm, Sandbox: name}
	}
}

// migrateOne performs the transform for one sandbox, re-validating its status
// against the (possibly drifted) current state, then re-stamps its stored
// principal LAST — that re-stamp is the idempotency marker, so a crash after the
// backend op but before it leaves the sandbox re-scanned on the next run.
func (p *PrincipalRename) migrateOne(ctx context.Context, name string, d migrate.Decision) (migrate.Report, error) {
	rt, err := p.backendFor(ctx, name)
	if err != nil {
		return migrate.Report{}, err
	}
	oldName := store.LegacyCLIInstanceName(name)
	newName := store.InstanceName(p.layout.Principal, name)

	if backendHasInstance(rt) {
		st, err := status.DetectStatus(ctx, rt, oldName, p.layout.SandboxDir(name))
		if err != nil {
			return migrate.Report{}, err
		}
		if err := p.applyBackendOp(ctx, rt, name, oldName, newName, st, d); err != nil {
			return migrate.Report{}, err
		}
	}

	if err := p.restampPrincipal(name); err != nil {
		return migrate.Report{}, err
	}
	return migrate.Report{Migrated: []string{name}}, nil
}

// applyBackendOp performs the backend half of the migration for a sandbox that
// has a backend instance: rename in place, or (recreate-only backends) remove
// the old instance so the next start recreates it under the new name, or refuse.
func (p *PrincipalRename) applyBackendOp(ctx context.Context, rt runtime.Backend, name, oldName, newName string, st status.Status, d migrate.Decision) error {
	if r, ok := rt.(runtime.Renamer); ok {
		if isUnauditable(st) {
			return fmt.Errorf("sandbox %q cannot be audited to rename its instance; start the backend or repair it, then re-run", name)
		}
		if err := r.Rename(ctx, oldName, newName); err != nil && !errors.Is(err, runtime.ErrNotFound) {
			return fmt.Errorf("rename sandbox %q instance %q -> %q: %w", name, oldName, newName, err)
		}
		// ErrNotFound tolerated: a crash-interrupted run may have already renamed
		// the instance; the re-stamp below finalizes it idempotently.
		return nil
	}

	// Recreate-only backend (containerd/apple).
	if isUnauditable(st) {
		return fmt.Errorf("sandbox %q cannot be audited; start the backend or repair it, then re-run", name)
	}
	if isInstanceUp(st) {
		return fmt.Errorf("sandbox %q is running and its backend cannot rename a running instance; stop it and re-run migrate", name)
	}
	if !d.Yes {
		return fmt.Errorf("sandbox %q must be recreated under its new name (its backend cannot rename) and that was not authorized; re-run with --yes", name)
	}
	// Remove the stale old-named instance so the next start recreates it as
	// newName; Remove is a no-op if it is already gone.
	if err := rt.Remove(ctx, oldName); err != nil {
		return fmt.Errorf("remove old instance %q for sandbox %q: %w", oldName, name, err)
	}
	return nil
}

// restampPrincipal rewrites the sandbox's environment.json with the target
// principal, so every path that resolves an instance from the stored principal
// (git-in-confinement, vscode attach) agrees with the live layout. It also
// re-stamps ImageRef when it names a principal-authored profile image
// (DF126): the pre-v5 sites that wrote it hardcoded the bare
// "yoloai-<profile>" tag the same way they hardcoded the instance-name prefix
// D126 fixed, so a record left unstamped would resolve a tag the rebuilt
// (principal-scoped) image on the launch/restart path (launch.go, restart.go)
// no longer produces. BaseImage is left alone — it is deliberately unscoped
// (DF126 "Scope note").
//
// Written last, and durably: SaveEnvironment goes through AtomicWriteFile, so
// this record reaches stable storage before Apply stamps the realm, and the
// stamp cannot certify a conversion that did not survive. That is D110's truth
// invariant, and it needs both halves — until 2026-07-17 this comment claimed
// "durable" while the write underneath was a plain os.WriteFile with no fsync
// and no atomic rename, which is the whole of what made the gap invisible.
func (p *PrincipalRename) restampPrincipal(name string) error {
	sandboxDir := p.layout.SandboxDir(name)
	env, err := store.LoadEnvironment(sandboxDir)
	if err != nil {
		return fmt.Errorf("load environment for %q: %w", name, err)
	}
	env.Principal = p.layout.Principal
	env.ImageRef = restampedImageRef(p.layout, env.ImageRef)
	if err := store.SaveEnvironment(sandboxDir, env); err != nil {
		return fmt.Errorf("re-stamp principal for %q: %w", name, err)
	}
	return nil
}

// restampedImageRef re-stamps a pre-v5 ImageRef with the target principal
// (DF126). config.BaseImage and an empty ref are returned unchanged — the base
// image is deliberately unscoped and shared by every principal.
//
// The bare CutPrefix below is sound only because of the caller's domain, not
// because of anything this function checks. It runs solely on records whose
// principal is empty (see unmigratedSandboxNames), and only the pre-D126 CLI
// ever wrote those; it always wrote exactly "yoloai-<profile>". So the cut
// yields the profile name and nothing else — including when the profile is
// itself named something like "acme-web".
//
// It is NOT safe on an arbitrary ref, and must not be reused as though it were:
// "yoloai-" is a prefix of every principal's namespace, so an already-scoped
// "yoloai-acme-web" would be cut to "acme-web" and re-stamped to
// "yoloai-cli-acme-web" — silently rewriting one principal's image to another's.
// That is DF115's shape exactly, and D126 removed it structurally everywhere it
// could reach. Restrict the input before calling this, or don't call it.
func restampedImageRef(layout config.Layout, imageRef string) string {
	if imageRef == "" || imageRef == config.BaseImage {
		return imageRef
	}
	profileName, ok := strings.CutPrefix(imageRef, "yoloai-")
	if !ok {
		return imageRef
	}
	return config.ProfileImageTag(layout, profileName)
}

// unmigratedSandboxNames lists sandbox names whose stored principal is not yet
// the target — the migration's unit of work. Reads environment.json directly, no
// runtime. A sandbox already carrying a non-empty principal (the target, or an
// integrator's own) is left untouched.
func (p *PrincipalRename) unmigratedSandboxNames() ([]string, error) {
	entries, err := os.ReadDir(p.sandboxesRoot)
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
		sandboxDir := p.layout.SandboxDir(e.Name())
		if _, err := os.Stat(filepath.Join(sandboxDir, store.EnvironmentFile)); errors.Is(err, fs.ErrNotExist) {
			continue // not a sandbox dir at all — nothing here claims to be one
		}
		env, err := store.LoadEnvironment(sandboxDir)
		if err != nil {
			// A dir that HAS an environment.json we cannot read is never
			// skippable. Skipping it drops it from the unit of work while Apply
			// stamps the realm to v5 regardless, so the sandbox is left
			// unconverted inside a realm that certifies it converted — and the
			// gate then reads LayoutOK and never routes back here, which makes
			// it unreachable forever. Every reason LoadEnvironment fails is a
			// reason to stop: a torn record, one still below metaVersion (a
			// half-finished MigrateAgentConfigs), or one from a newer binary.
			// Aborting is recoverable; a false stamp is not. This matches
			// MigrateAgentConfigs, which has always failed hard here.
			return nil, fmt.Errorf("sandbox %q: %w", e.Name(), err)
		}
		if env.Principal == "" {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// backendType reads the backend that created name from its environment.
func (p *PrincipalRename) backendType(name string) runtime.BackendType {
	env, err := store.LoadEnvironment(p.layout.SandboxDir(name))
	if err != nil {
		return ""
	}
	return env.BackendType
}

// backendFor resolves (and caches) the runtime for the backend that created name.
func (p *PrincipalRename) backendFor(ctx context.Context, name string) (runtime.Backend, error) {
	bt := p.backendType(name)
	if bt == "" {
		return nil, fmt.Errorf("load backend for %q", name)
	}
	if rt, ok := p.rts[bt]; ok {
		return rt, nil
	}
	rt, err := p.runtimeFor(ctx, bt)
	if err != nil {
		return nil, fmt.Errorf("connect to %s runtime: %w", bt, err)
	}
	p.rts[bt] = rt
	return rt, nil
}

// isRenamer reports whether rt can rename an instance in place.
func isRenamer(rt runtime.Backend) bool {
	_, ok := rt.(runtime.Renamer)
	return ok
}

// backendHasInstance reports whether rt manages a persistent per-sandbox backend
// object (container/VM) that carries the instance name. False only for seatbelt,
// whose isolation is a host process group with no container/VM (its on-disk dir
// is the bare sandbox name), so a principal change needs no backend operation.
func backendHasInstance(rt runtime.Backend) bool {
	return runtime.KeepAliveModelOf(rt) != runtime.KeepAliveHostKeepAlive
}

// isInstanceUp reports whether the instance is running or holds live/suspended
// state that a recreate would destroy.
func isInstanceUp(st status.Status) bool {
	switch st {
	case status.StatusActive, status.StatusIdle, status.StatusDone, status.StatusFailed, status.StatusSuspended:
		return true
	default:
		return false
	}
}

// isUnauditable reports whether the sandbox's status cannot be determined, so the
// migration cannot safely act on its backend instance.
func isUnauditable(st status.Status) bool {
	return st == status.StatusBroken || st == status.StatusUnavailable
}

// stampSchemaAdvancing writes target to the realm's schema stamp only if the
// current stamp is lower, so a framework migrator that re-runs (or runs after a
// later migrator already advanced the realm) never lowers it. Crash-safe: the
// write goes through fileutil.AtomicWriteFile's fsync barrier, so the stamp can
// never physically precede the data it certifies. Shared by every framework
// migrator, each passing its own target.
func stampSchemaAdvancing(layout config.Layout, target int) error {
	current, _, err := config.ReadSchemaVersion(layout.SchemaVersionPath())
	if err != nil {
		return fmt.Errorf("read schema stamp: %w", err)
	}
	if current >= target {
		return nil
	}
	if err := fileutil.AtomicWriteFile(layout.SchemaVersionPath(), fmt.Appendf(nil, "%d", target), 0600); err != nil {
		return fmt.Errorf("stamp library v%d: %w", target, err)
	}
	return nil
}

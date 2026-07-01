package yoloai

// ABOUTME: Public plan/apply verbs for the crash-safe framework migrations
// ABOUTME: (v3->v4 overlay flatten): the app renders + confirms, the library runs.

import (
	"context"
	goruntime "runtime"

	"github.com/kstenerud/yoloai/internal/migrate"
	"github.com/kstenerud/yoloai/internal/orchestrator"
	"github.com/kstenerud/yoloai/runtime"
)

// MigrationOp is one operation a pending framework migration would perform,
// surfaced for the app to render and gate on.
type MigrationOp struct {
	// Description is a one-line, user-facing summary.
	Description string `json:"description"`
	// Destructive reports whether the op mutates or discards user data (needs
	// approval).
	Destructive bool `json:"destructive"`
	// AbandonsWork reports whether the op discards uncommitted work, which needs
	// the explicit --abandon-stopped-overlay authorization (a plain "yes" never
	// suffices).
	AbandonsWork bool `json:"abandons_work"`
	// Blocked reports that the op cannot be performed by any approval — a hard
	// precondition the migration can't satisfy (not a policy the user can waive).
	// Description carries the reason and fix; the run refuses while any op is
	// blocked.
	Blocked bool `json:"blocked,omitempty"`
	// Sandbox, when set, is the sandbox the op concerns.
	Sandbox string `json:"sandbox,omitempty"`
}

// MigrationPlan is the read-only description of the pending framework migrations.
type MigrationPlan struct {
	Ops []MigrationOp `json:"ops"`
}

// HasDestructive reports whether any op needs approval.
func (p MigrationPlan) HasDestructive() bool {
	for _, op := range p.Ops {
		if op.Destructive {
			return true
		}
	}
	return false
}

// MigrationDecision is the approval the app grants, derived from flags (and any
// interactive confirmation the app obtained). The library never prompts.
type MigrationDecision struct {
	// Yes authorizes benign destructive ops.
	Yes bool
	// AbandonStoppedOverlay additionally authorizes ops that discard uncommitted
	// work.
	AbandonStoppedOverlay bool
}

// MigrationReport is the outcome of an applied framework migration.
type MigrationReport struct {
	Migrated    []string `json:"migrated,omitempty"`
	Quarantined []string `json:"quarantined,omitempty"`
	Notes       []string `json:"notes,omitempty"`
}

// MigrationPlan collects the pending framework migrations' plan (read-only). It
// opens a runtime only if overlay sandboxes are actually present.
func (s *System) MigrationPlan(ctx context.Context) (MigrationPlan, error) {
	migrators, cleanup := s.frameworkMigrators()
	defer cleanup()
	plans, err := migrate.CollectPlans(ctx, migrators)
	if err != nil {
		return MigrationPlan{}, err
	}
	var out MigrationPlan
	for _, p := range plans {
		for _, op := range p.Ops {
			out.Ops = append(out.Ops, MigrationOp{
				Description:  op.Description,
				Destructive:  op.Destructive(),
				AbandonsWork: op.Auth == migrate.AuthAbandonOverlay,
				Blocked:      op.Blocked(),
				Sandbox:      op.Sandbox,
			})
		}
	}
	return out, nil
}

// ApplyMigration runs the pending framework migrations under the whole-tree lock,
// gated by d, and returns what it did. The app must have obtained d's approval
// (an unauthorized destructive op is refused, not applied).
func (s *System) ApplyMigration(ctx context.Context, d MigrationDecision) (MigrationReport, error) {
	migrators, cleanup := s.frameworkMigrators()
	defer cleanup()
	rep, err := migrate.ApplyAll(ctx, s.layout.DataDir, migrators,
		migrate.Decision{Yes: d.Yes, AbandonStoppedOverlay: d.AbandonStoppedOverlay})
	return MigrationReport{Migrated: rep.Migrated, Quarantined: rep.Quarantined, Notes: rep.Notes}, err
}

// frameworkMigrators builds the library realm's pending framework migrators and
// a cleanup that closes any runtime they opened. The runtime is built lazily
// (only when overlay sandboxes exist), so a no-overlay migrate never opens a
// backend. The scratch dir and whole-tree lock live under the library DataDir
// (same filesystem as its sandboxes). Sandboxes resolve to their post-relocation
// location (SandboxesDir) — by the time the framework applies, MigrateCLI has
// run (F7 case 2).
func (s *System) frameworkMigrators() ([]migrate.Migrator, func()) {
	flatten := orchestrator.NewOverlayFlatten(
		s.layout,
		s.layout.DataDir,        // home: scratch + lock, same FS as sandboxes
		s.layout.SandboxesDir(), // F7-resolved sandboxes root
		goruntime.GOOS,
		func(ctx context.Context, backend runtime.BackendType) (runtime.Backend, error) {
			return runtime.New(ctx, backend, s.layout) // each overlay sandbox's own backend
		},
	)
	return []migrate.Migrator{flatten}, flatten.Cleanup
}

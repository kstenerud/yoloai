package system

// ABOUTME: The plan -> confirm -> apply flow for framework (v3->v4+) migrations:
// ABOUTME: render the plan, gate destructive ops on approval, then apply.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/migrate"
)

// errMigrateAborted is returned when the user declines a destructive migration
// (or a headless run has no approval), so the command exits non-zero without a
// scary stack of wrapped errors.
var errMigrateAborted = errors.New("migration aborted; nothing was changed")

// libraryMigrators returns the framework (plan/apply-driven) migrators pending
// for the library realm — the crash-safe migrations that run AFTER the frozen
// v0->v3 ladder. It lives in the CLI layer (not on the public yoloai.System)
// because it wires internal-only types (migrate.Migrator, the orchestrator
// migrators) that must not leak through the public API. Empty until the first
// such migrator (the v3->v4 overlay flatten) lands, which will construct it here
// with the layout + runtime it needs.
func libraryMigrators(_ context.Context) ([]migrate.Migrator, error) {
	return nil, nil
}

// planApplyOpts carries everything the framework flow needs, injected so the
// flow is testable without a live cobra command or real migrators.
type planApplyOpts struct {
	home      string
	migrators []migrate.Migrator
	// yes authorizes benign destructive ops without an interactive prompt
	// (--yes, or JSON mode).
	yes bool
	// abandonOverlay additionally authorizes ops that discard uncommitted work
	// (--abandon-stopped-overlay).
	abandonOverlay bool
	json           bool
	in             io.Reader
	out            io.Writer
	errw           io.Writer
}

// runPlanApply executes the framework apply flow: collect the plan, render it,
// obtain approval for any destructive operations, and apply. The read-only
// --check/--dry-run preview is a separate path (previewMigration) that never
// reaches here. The library never prompts; this app-side function owns all
// interaction and hands the library a settled Decision.
func runPlanApply(ctx context.Context, opts planApplyOpts) (migrate.Report, error) {
	plans, err := migrate.CollectPlans(ctx, opts.migrators)
	if err != nil {
		return migrate.Report{}, err
	}
	if err := renderPlan(opts, plans); err != nil {
		return migrate.Report{}, err
	}

	dec := migrate.Decision{Yes: opts.yes, AbandonStoppedOverlay: opts.abandonOverlay}
	if ok, unmet := migrate.Authorize(plans, dec); !ok {
		granted, err := resolveApproval(ctx, opts, unmet, &dec)
		if err != nil {
			return migrate.Report{}, err
		}
		if !granted {
			return migrate.Report{}, errMigrateAborted
		}
	}
	return migrate.ApplyAll(ctx, opts.home, opts.migrators, dec)
}

// resolveApproval turns an unmet-approval set into a granted decision or an
// abort. Ops that discard uncommitted work (AuthAbandonOverlay) can NEVER be
// prompted away — they demand the explicit --abandon-stopped-overlay, so --yes
// alone never destroys work. Remaining confirm-level ops are prompted; a
// headless run (no TTY) reads EOF and declines, i.e. defaults to abort.
func resolveApproval(ctx context.Context, opts planApplyOpts, unmet []migrate.Op, dec *migrate.Decision) (bool, error) {
	var needsAbandon []string
	for _, op := range unmet {
		if op.Auth == migrate.AuthAbandonOverlay {
			needsAbandon = append(needsAbandon, op.Description)
		}
	}
	if len(needsAbandon) > 0 {
		return false, fmt.Errorf(
			"this migration would abandon uncommitted work:\n  - %s\nre-run with --abandon-stopped-overlay to authorize",
			strings.Join(needsAbandon, "\n  - "))
	}
	confirmed, err := cliutil.Confirm(ctx, "Proceed with the migration? [y/N] ", opts.in, opts.errw)
	if err != nil {
		return false, err
	}
	if !confirmed {
		return false, nil
	}
	dec.Yes = true
	return true, nil
}

// previewMigration renders the read-only pre-upgrade audit for --check/--dry-run:
// the realm migration status and the framework plan, writing nothing. (Phase 3
// will additionally record the beta widen-guard inventory on the --dry-run path.)
func previewMigration(ctx context.Context, opts planApplyOpts, cliSt, libSt config.LayoutStatus) error {
	plans, err := migrate.CollectPlans(ctx, opts.migrators)
	if err != nil {
		return err
	}
	if opts.json {
		return cliutil.WriteJSON(opts.out, map[string]any{
			"cli_realm":      statusString(cliSt),
			"library_realm":  statusString(libSt),
			"framework_plan": planDTO(plans),
		})
	}
	if _, err := fmt.Fprintf(opts.out, "CLI realm:     %s\nLibrary realm: %s\n", statusString(cliSt), statusString(libSt)); err != nil {
		return err
	}
	return renderPlan(opts, plans)
}

func statusString(s config.LayoutStatus) string {
	switch s {
	case config.LayoutFresh:
		return "fresh (will be created)"
	case config.LayoutMigrate:
		return "needs migration"
	case config.LayoutOK:
		return "up to date"
	default:
		return "unknown"
	}
}

// renderPlan prints the combined plan (JSON or human), destructive ops flagged.
func renderPlan(opts planApplyOpts, plans []migrate.Plan) error {
	if opts.json {
		return cliutil.WriteJSON(opts.out, planDTO(plans))
	}
	if planIsEmpty(plans) {
		_, err := fmt.Fprintln(opts.out, "No pending framework migrations.")
		return err
	}
	for _, p := range plans {
		if _, err := fmt.Fprintf(opts.out, "Migration: %s\n", p.Migrator); err != nil {
			return err
		}
		for _, op := range p.Ops {
			marker := " "
			if op.Destructive() {
				marker = "!"
			}
			if _, err := fmt.Fprintf(opts.out, "  [%s] %s\n", marker, op.Description); err != nil {
				return err
			}
		}
	}
	return nil
}

// renderReport prints what an apply actually did (stopped/quarantined sandboxes).
func renderReport(opts planApplyOpts, r migrate.Report) error {
	if opts.json {
		return nil // the command emits the final JSON status
	}
	for _, n := range r.Notes {
		if _, err := fmt.Fprintln(opts.out, n); err != nil {
			return err
		}
	}
	for _, q := range r.Quarantined {
		if _, err := fmt.Fprintf(opts.out, "Quarantined (set aside, data preserved): %s\n", q); err != nil {
			return err
		}
	}
	return nil
}

func planIsEmpty(plans []migrate.Plan) bool {
	for _, p := range plans {
		if len(p.Ops) > 0 {
			return false
		}
	}
	return true
}

// opDTO / planDTO are the JSON shape (Auth is rendered as booleans, not an
// opaque enum int).
type opDTO struct {
	Description  string `json:"description"`
	Destructive  bool   `json:"destructive"`
	AbandonsWork bool   `json:"abandons_work"`
	Sandbox      string `json:"sandbox,omitempty"`
}

type planEntryDTO struct {
	Migrator string  `json:"migrator"`
	Ops      []opDTO `json:"ops"`
}

func planDTO(plans []migrate.Plan) []planEntryDTO {
	out := make([]planEntryDTO, 0, len(plans))
	for _, p := range plans {
		ops := make([]opDTO, 0, len(p.Ops))
		for _, op := range p.Ops {
			ops = append(ops, opDTO{
				Description:  op.Description,
				Destructive:  op.Destructive(),
				AbandonsWork: op.Auth == migrate.AuthAbandonOverlay,
				Sandbox:      op.Sandbox,
			})
		}
		out = append(out, planEntryDTO{Migrator: p.Migrator, Ops: ops})
	}
	return out
}

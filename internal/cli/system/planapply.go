package system

// ABOUTME: The plan -> confirm -> apply flow for framework (v3->v4+) migrations,
// ABOUTME: driven through the public yoloai verbs; the app renders + confirms.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/internal/cli/cliutil"
	"github.com/kstenerud/yoloai/internal/config"
)

// errMigrateAborted is returned when the user declines a destructive migration
// (or a headless run has no approval), so the command exits non-zero cleanly.
var errMigrateAborted = errors.New("migration aborted; nothing was changed")

// planApplyOpts carries the framework flow's inputs, injected for testability.
type planApplyOpts struct {
	sys            *yoloai.System
	yes            bool // --yes / JSON: authorizes benign destructive ops
	abandonOverlay bool // --abandon-stopped-overlay: authorizes discarding work
	json           bool
	in             io.Reader
	out            io.Writer
	errw           io.Writer
}

// runPlanApply executes the framework apply flow: collect the plan (public
// verb), render it, obtain approval for any destructive ops, and apply. The
// library never prompts; this app-side function owns interaction and hands the
// library a settled decision.
func runPlanApply(ctx context.Context, opts planApplyOpts) (yoloai.MigrationReport, error) {
	plan, err := opts.sys.MigrationPlan(ctx)
	if err != nil {
		return yoloai.MigrationReport{}, err
	}
	if err := renderPlan(opts, plan); err != nil {
		return yoloai.MigrationReport{}, err
	}

	d := yoloai.MigrationDecision{Yes: opts.yes, AbandonStoppedOverlay: opts.abandonOverlay}
	if ok, unmet := authorize(plan, d); !ok {
		granted, err := resolveApproval(ctx, opts, unmet, &d)
		if err != nil {
			return yoloai.MigrationReport{}, err
		}
		if !granted {
			return yoloai.MigrationReport{}, errMigrateAborted
		}
	}
	return opts.sys.ApplyMigration(ctx, d)
}

// authorize reports whether d satisfies every op's required approval, returning
// the unmet ops for the app to surface or prompt on.
func authorize(plan yoloai.MigrationPlan, d yoloai.MigrationDecision) (ok bool, unmet []yoloai.MigrationOp) {
	for _, op := range plan.Ops {
		if !opSatisfied(op, d) {
			unmet = append(unmet, op)
		}
	}
	return len(unmet) == 0, unmet
}

func opSatisfied(op yoloai.MigrationOp, d yoloai.MigrationDecision) bool {
	switch {
	case !op.Destructive:
		return true
	case op.AbandonsWork:
		return d.Yes && d.AbandonStoppedOverlay
	default:
		return d.Yes
	}
}

// resolveApproval turns an unmet-approval set into a granted decision or an
// abort. Ops that discard uncommitted work can NEVER be prompted away — they
// demand the explicit --abandon-stopped-overlay, so --yes alone never destroys
// work. Remaining confirm-level ops are prompted; a headless run reads EOF and
// declines (defaults to abort).
func resolveApproval(ctx context.Context, opts planApplyOpts, unmet []yoloai.MigrationOp, d *yoloai.MigrationDecision) (bool, error) {
	var needsAbandon []string
	for _, op := range unmet {
		if op.AbandonsWork {
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
	d.Yes = true
	return true, nil
}

// previewMigration renders the read-only --check/--dry-run audit: realm status +
// the framework plan, writing nothing.
func previewMigration(ctx context.Context, opts planApplyOpts, cliSt, libSt config.LayoutStatus) error {
	// Audit the framework plan against where library data physically lives now:
	// on an un-relocated flat v0 install the sandboxes are still at TOP/sandboxes,
	// not the namespaced location opts.sys is rooted at. (The apply path relocates
	// first, so its plan read via opts.sys is already correct.)
	planSys, err := cliutil.MigratePreviewSystem()
	if err != nil {
		return err
	}
	plan, err := planSys.MigrationPlan(ctx)
	if err != nil {
		return err
	}
	if opts.json {
		return cliutil.WriteJSON(opts.out, map[string]any{
			"cli_realm":      statusString(cliSt),
			"library_realm":  statusString(libSt),
			"framework_plan": plan.Ops,
		})
	}
	if _, err := fmt.Fprintf(opts.out, "CLI realm:     %s\nLibrary realm: %s\n", statusString(cliSt), statusString(libSt)); err != nil {
		return err
	}
	return renderPlanHuman(opts, plan)
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

// renderPlan prints the plan (JSON or human), destructive ops flagged.
func renderPlan(opts planApplyOpts, plan yoloai.MigrationPlan) error {
	if opts.json {
		return cliutil.WriteJSON(opts.out, plan)
	}
	return renderPlanHuman(opts, plan)
}

func renderPlanHuman(opts planApplyOpts, plan yoloai.MigrationPlan) error {
	if len(plan.Ops) == 0 {
		_, err := fmt.Fprintln(opts.out, "No pending framework migrations.")
		return err
	}
	for _, op := range plan.Ops {
		marker := " "
		if op.Destructive {
			marker = "!"
		}
		if _, err := fmt.Fprintf(opts.out, "  [%s] %s\n", marker, op.Description); err != nil {
			return err
		}
	}
	return nil
}

// renderReport prints what an apply actually did (stopped/quarantined sandboxes).
func renderReport(opts planApplyOpts, r yoloai.MigrationReport) error {
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

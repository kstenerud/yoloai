// ABOUTME: The run driver — collect every migrator's plan, then apply under the
// ABOUTME: whole-tree lock, re-deriving and re-validating so nothing drifts unseen.
package migrate

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrMigrationInProgress means another run holds the whole-tree lock.
var ErrMigrationInProgress = errors.New("a migration is already in progress")

// CollectPlans gathers each migrator's read-only plan, in order. It takes no
// lock and mutates nothing — the app renders the result for --check / --dry-run
// and to drive the confirmation decision.
func CollectPlans(ctx context.Context, migrators []Migrator) ([]Plan, error) {
	plans := make([]Plan, 0, len(migrators))
	for _, m := range migrators {
		plan, err := m.Plan(ctx)
		if err != nil {
			return nil, fmt.Errorf("plan %q: %w", m.Describe(), err)
		}
		plan.Migrator = m.Describe()
		plans = append(plans, plan)
	}
	return plans, nil
}

// ApplyAll runs every migrator's Apply under the exclusive whole-tree lock,
// re-deriving each plan and re-checking authorization first (the
// newly-destructive-refuse guard): a migrator that became destructive since the
// caller's plan — without that destruction being approved — is refused, never
// silently applied. Leftover scratch from a crashed run is tossed before and
// after (the recovery precondition; scratch is disposable, never resumed). The
// caller must have already obtained the Decision's approval (see Authorize).
func ApplyAll(ctx context.Context, home string, migrators []Migrator, d Decision) (Report, error) {
	var report Report

	release, err := AcquireHomeLock(home)
	if err != nil {
		return report, fmt.Errorf("%w: %w", ErrMigrationInProgress, err)
	}
	defer release()

	// Toss any leftover scratch: a crashed build is garbage, rebuilt fresh.
	if err := DisposeScratch(home); err != nil {
		return report, err
	}
	defer func() { _ = DisposeScratch(home) }()

	for _, m := range migrators {
		plan, err := m.Plan(ctx)
		if err != nil {
			return report, fmt.Errorf("re-plan %q: %w", m.Describe(), err)
		}
		plan.Migrator = m.Describe()
		if ok, unmet := Authorize([]Plan{plan}, d); !ok {
			return report, fmt.Errorf(
				"migrator %q now requires approval it was not granted "+
					"(a precondition drifted since planning): %s; re-run `system migrate` to review",
				m.Describe(), summarizeUnmet(unmet))
		}
		rep, err := m.Apply(ctx, d)
		report.Merge(rep)
		if err != nil {
			return report, fmt.Errorf("apply %q: %w", m.Describe(), err)
		}
	}
	return report, nil
}

// summarizeUnmet renders unmet ops for an error message.
func summarizeUnmet(unmet []Op) string {
	seen := make(map[string]struct{})
	var parts []string
	for _, op := range unmet {
		if _, ok := seen[op.Description]; ok {
			continue
		}
		seen[op.Description] = struct{}{}
		parts = append(parts, op.Description)
	}
	return strings.Join(parts, "; ")
}

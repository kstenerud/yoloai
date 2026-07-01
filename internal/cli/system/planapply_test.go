package system

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/internal/migrate"
)

type fakeMig struct {
	name    string
	ops     []migrate.Op
	applied *bool
	report  migrate.Report
}

func (f *fakeMig) Describe() string { return f.name }

func (f *fakeMig) Plan(context.Context) (migrate.Plan, error) {
	return migrate.Plan{Migrator: f.name, Ops: f.ops}, nil
}

func (f *fakeMig) Apply(context.Context, migrate.Decision) (migrate.Report, error) {
	if f.applied != nil {
		*f.applied = true
	}
	return f.report, nil
}

func baseOpts(t *testing.T, m *fakeMig, in string) planApplyOpts {
	t.Helper()
	return planApplyOpts{
		home:      t.TempDir(),
		migrators: []migrate.Migrator{m},
		in:        strings.NewReader(in),
		out:       &bytes.Buffer{},
		errw:      &bytes.Buffer{},
	}
}

func TestRunPlanApply_BenignApplies(t *testing.T) {
	applied := false
	m := &fakeMig{name: "benign", ops: []migrate.Op{{Description: "stamp v4", Auth: migrate.AuthNone}}, applied: &applied}
	opts := baseOpts(t, m, "")
	if _, err := runPlanApply(context.Background(), opts); err != nil {
		t.Fatalf("runPlanApply: %v", err)
	}
	if !applied {
		t.Error("benign migration was not applied")
	}
	if out := opts.out.(*bytes.Buffer).String(); !strings.Contains(out, "stamp v4") {
		t.Errorf("plan not rendered; out = %q", out)
	}
}

func TestRunPlanApply_DestructiveDeclined(t *testing.T) {
	applied := false
	m := &fakeMig{name: "d", ops: []migrate.Op{{Description: "quarantine sbx X", Auth: migrate.AuthConfirm}}, applied: &applied}
	opts := baseOpts(t, m, "n\n")
	_, err := runPlanApply(context.Background(), opts)
	if !errors.Is(err, errMigrateAborted) {
		t.Fatalf("err = %v, want errMigrateAborted", err)
	}
	if applied {
		t.Error("declined migration was applied anyway")
	}
}

func TestRunPlanApply_DestructiveConfirmed(t *testing.T) {
	applied := false
	m := &fakeMig{name: "d", ops: []migrate.Op{{Description: "quarantine sbx X", Auth: migrate.AuthConfirm}}, applied: &applied}
	opts := baseOpts(t, m, "y\n")
	if _, err := runPlanApply(context.Background(), opts); err != nil {
		t.Fatalf("runPlanApply: %v", err)
	}
	if !applied {
		t.Error("confirmed migration was not applied")
	}
}

func TestRunPlanApply_YesBypassesPrompt(t *testing.T) {
	applied := false
	m := &fakeMig{name: "d", ops: []migrate.Op{{Description: "quarantine sbx X", Auth: migrate.AuthConfirm}}, applied: &applied}
	opts := baseOpts(t, m, "") // no input available; --yes must not prompt
	opts.yes = true
	if _, err := runPlanApply(context.Background(), opts); err != nil {
		t.Fatalf("runPlanApply: %v", err)
	}
	if !applied {
		t.Error("--yes did not apply")
	}
}

// --yes ALONE must never authorize abandoning uncommitted work, even with an
// interactive "y" — that needs the explicit --abandon-stopped-overlay.
func TestRunPlanApply_AbandonRefusedWithoutFlag(t *testing.T) {
	applied := false
	m := &fakeMig{name: "flatten", ops: []migrate.Op{{Description: "abandon stopped overlay sbx X", Auth: migrate.AuthAbandonOverlay}}, applied: &applied}
	opts := baseOpts(t, m, "y\n")
	opts.yes = true
	_, err := runPlanApply(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "--abandon-stopped-overlay") {
		t.Fatalf("err = %v, want it to demand --abandon-stopped-overlay", err)
	}
	if applied {
		t.Error("abandon op ran without its flag")
	}
}

func TestRunPlanApply_AbandonProceedsWithFlag(t *testing.T) {
	applied := false
	m := &fakeMig{name: "flatten", ops: []migrate.Op{{Description: "abandon stopped overlay sbx X", Auth: migrate.AuthAbandonOverlay}}, applied: &applied}
	opts := baseOpts(t, m, "")
	opts.yes = true
	opts.abandonOverlay = true
	if _, err := runPlanApply(context.Background(), opts); err != nil {
		t.Fatalf("runPlanApply: %v", err)
	}
	if !applied {
		t.Error("authorized abandon did not apply")
	}
}

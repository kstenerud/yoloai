package migrate

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

type fakeMigrator struct {
	name     string
	plan     Plan
	applied  *bool
	report   Report
	applyErr error
}

func (f *fakeMigrator) Describe() string { return f.name }

func (f *fakeMigrator) Plan(context.Context) (Plan, error) { return f.plan, nil }

func (f *fakeMigrator) Apply(context.Context, Decision) (Report, error) {
	if f.applied != nil {
		*f.applied = true
	}
	return f.report, f.applyErr
}

func TestAuthorize(t *testing.T) {
	for _, tc := range []struct {
		name string
		auth Auth
		dec  Decision
		want bool
	}{
		{"none always ok", AuthNone, Decision{}, true},
		{"confirm needs yes — denied", AuthConfirm, Decision{}, false},
		{"confirm needs yes — granted", AuthConfirm, Decision{Yes: true}, true},
		{"abandon needs both — yes only", AuthAbandonOverlay, Decision{Yes: true}, false},
		{"abandon needs both — abandon only", AuthAbandonOverlay, Decision{AbandonStoppedOverlay: true}, false},
		{"abandon needs both — granted", AuthAbandonOverlay, Decision{Yes: true, AbandonStoppedOverlay: true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plans := []Plan{{Migrator: "m", Ops: []Op{{Description: "op", Auth: tc.auth}}}}
			ok, unmet := Authorize(plans, tc.dec)
			if ok != tc.want {
				t.Errorf("Authorize ok = %v, want %v", ok, tc.want)
			}
			if ok && len(unmet) != 0 {
				t.Errorf("ok run reported unmet ops: %v", unmet)
			}
			if !ok && len(unmet) == 0 {
				t.Error("denied run reported no unmet ops")
			}
		})
	}
}

func TestCollectPlans_StampsMigratorNames(t *testing.T) {
	m := &fakeMigrator{name: "v3->v4 flatten", plan: Plan{Ops: []Op{{Description: "x"}}}}
	plans, err := CollectPlans(context.Background(), []Migrator{m})
	if err != nil {
		t.Fatalf("CollectPlans: %v", err)
	}
	if len(plans) != 1 || plans[0].Migrator != "v3->v4 flatten" {
		t.Errorf("plans = %+v, want one stamped with the migrator name", plans)
	}
}

func TestApplyAll_BenignRunsAndReports(t *testing.T) {
	home := t.TempDir()
	applied := false
	m := &fakeMigrator{
		name:    "benign",
		plan:    Plan{Ops: []Op{{Description: "stamp", Auth: AuthNone}}},
		applied: &applied,
		report:  Report{Migrated: []string{"sbx"}, Notes: []string{"done"}},
	}
	rep, err := ApplyAll(context.Background(), home, []Migrator{m}, Decision{})
	if err != nil {
		t.Fatalf("ApplyAll: %v", err)
	}
	if !applied {
		t.Error("migrator was not applied")
	}
	if len(rep.Migrated) != 1 || rep.Migrated[0] != "sbx" {
		t.Errorf("report.Migrated = %v, want [sbx]", rep.Migrated)
	}
}

func TestApplyAll_RefusesNewlyDestructive(t *testing.T) {
	home := t.TempDir()
	applied := false
	// The re-derived plan is destructive, but the decision grants nothing:
	// applying would destroy unreviewed work, so ApplyAll must refuse.
	m := &fakeMigrator{
		name:    "turned-destructive",
		plan:    Plan{Ops: []Op{{Description: "abandon stopped overlay sandbox X", Auth: AuthAbandonOverlay}}},
		applied: &applied,
	}
	_, err := ApplyAll(context.Background(), home, []Migrator{m}, Decision{})
	if err == nil {
		t.Fatal("expected refusal, got nil")
	}
	if !strings.Contains(err.Error(), "requires approval") {
		t.Errorf("error = %v, want it to mention unmet approval", err)
	}
	if applied {
		t.Error("migrator was applied despite unmet approval")
	}
}

func TestApplyAll_LockContention(t *testing.T) {
	home := t.TempDir()
	release, err := AcquireHomeLock(home)
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	defer release()

	m := &fakeMigrator{name: "blocked", plan: Plan{Ops: []Op{{Description: "x"}}}}
	_, err = ApplyAll(context.Background(), home, []Migrator{m}, Decision{})
	if !errors.Is(err, ErrMigrationInProgress) {
		t.Errorf("error = %v, want ErrMigrationInProgress", err)
	}
}

func TestApplyAll_TossesLeftoverScratch(t *testing.T) {
	home := t.TempDir()
	// Simulate a crashed build's leftover scratch.
	if err := os.MkdirAll(ScratchPath(home), 0o750); err != nil {
		t.Fatalf("seed scratch: %v", err)
	}
	if err := os.WriteFile(ScratchPath(home)+"/junk", []byte("x"), 0o600); err != nil {
		t.Fatalf("seed junk: %v", err)
	}
	m := &fakeMigrator{name: "noop", plan: Plan{Ops: []Op{{Description: "x", Auth: AuthNone}}}}
	if _, err := ApplyAll(context.Background(), home, []Migrator{m}, Decision{}); err != nil {
		t.Fatalf("ApplyAll: %v", err)
	}
	if _, err := os.Stat(ScratchPath(home)); !os.IsNotExist(err) {
		t.Error("leftover scratch was not disposed")
	}
}

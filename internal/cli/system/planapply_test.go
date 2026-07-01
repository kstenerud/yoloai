package system

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai"
)

func TestAuthorize(t *testing.T) {
	for _, tc := range []struct {
		name string
		op   yoloai.MigrationOp
		dec  yoloai.MigrationDecision
		want bool
	}{
		{"benign always ok", yoloai.MigrationOp{Description: "stamp"}, yoloai.MigrationDecision{}, true},
		{"destructive needs yes — denied", yoloai.MigrationOp{Description: "q", Destructive: true}, yoloai.MigrationDecision{}, false},
		{"destructive needs yes — granted", yoloai.MigrationOp{Description: "q", Destructive: true}, yoloai.MigrationDecision{Yes: true}, true},
		{"abandon needs both — yes only", yoloai.MigrationOp{Description: "a", Destructive: true, AbandonsWork: true}, yoloai.MigrationDecision{Yes: true}, false},
		{"abandon needs both — granted", yoloai.MigrationOp{Description: "a", Destructive: true, AbandonsWork: true}, yoloai.MigrationDecision{Yes: true, AbandonStoppedOverlay: true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ok, unmet := authorize(yoloai.MigrationPlan{Ops: []yoloai.MigrationOp{tc.op}}, tc.dec)
			if ok != tc.want {
				t.Errorf("authorize ok = %v, want %v", ok, tc.want)
			}
			if ok != (len(unmet) == 0) {
				t.Errorf("ok=%v but unmet=%v", ok, unmet)
			}
		})
	}
}

// --yes ALONE must never authorize abandoning uncommitted work — that needs the
// explicit flag, and can't be prompted away.
func TestResolveApproval_AbandonRefusedWithoutFlag(t *testing.T) {
	opts := planApplyOpts{in: strings.NewReader("y\n"), out: &bytes.Buffer{}, errw: &bytes.Buffer{}}
	unmet := []yoloai.MigrationOp{{Description: "abandon stopped overlay X", Destructive: true, AbandonsWork: true}}
	d := yoloai.MigrationDecision{Yes: true}
	granted, err := resolveApproval(context.Background(), opts, unmet, &d)
	if err == nil || !strings.Contains(err.Error(), "--abandon-stopped-overlay") {
		t.Fatalf("err = %v, want it to demand --abandon-stopped-overlay", err)
	}
	if granted {
		t.Error("abandon op was granted without its flag")
	}
}

func TestResolveApproval_ConfirmAccepted(t *testing.T) {
	opts := planApplyOpts{in: strings.NewReader("y\n"), out: &bytes.Buffer{}, errw: &bytes.Buffer{}}
	unmet := []yoloai.MigrationOp{{Description: "quarantine X", Destructive: true}}
	d := yoloai.MigrationDecision{}
	granted, err := resolveApproval(context.Background(), opts, unmet, &d)
	if err != nil {
		t.Fatalf("resolveApproval: %v", err)
	}
	if !granted || !d.Yes {
		t.Errorf("confirm accepted but granted=%v d.Yes=%v", granted, d.Yes)
	}
}

func TestResolveApproval_DeclinedOrHeadless(t *testing.T) {
	// Empty input == EOF == headless: defaults to abort.
	opts := planApplyOpts{in: strings.NewReader(""), out: &bytes.Buffer{}, errw: &bytes.Buffer{}}
	unmet := []yoloai.MigrationOp{{Description: "quarantine X", Destructive: true}}
	d := yoloai.MigrationDecision{}
	granted, err := resolveApproval(context.Background(), opts, unmet, &d)
	if err != nil {
		t.Fatalf("resolveApproval: %v", err)
	}
	if granted {
		t.Error("headless run should default to abort, but granted")
	}
}

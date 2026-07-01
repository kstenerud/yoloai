package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/migrate"
	"github.com/kstenerud/yoloai/internal/orchestrator/status"
	"github.com/kstenerud/yoloai/runtime"
)

func TestClassifyOverlay(t *testing.T) {
	for _, tc := range []struct {
		name     string
		st       status.Status
		goos     string
		wantAuth migrate.Auth
	}{
		{"active runs benign", status.StatusActive, "linux", migrate.AuthNone},
		{"idle runs benign", status.StatusIdle, "linux", migrate.AuthNone},
		{"done runs benign", status.StatusDone, "darwin", migrate.AuthNone},
		{"stopped linux needs abandon", status.StatusStopped, "linux", migrate.AuthAbandonOverlay},
		{"stopped macos needs abandon", status.StatusStopped, "darwin", migrate.AuthAbandonOverlay},
		{"removed needs abandon", status.StatusRemoved, "linux", migrate.AuthAbandonOverlay},
		{"broken quarantines", status.StatusBroken, "linux", migrate.AuthConfirm},
		{"unavailable quarantines", status.StatusUnavailable, "linux", migrate.AuthConfirm},
		{"suspended quarantines", status.StatusSuspended, "linux", migrate.AuthConfirm},
	} {
		t.Run(tc.name, func(t *testing.T) {
			op := classifyOverlay("sbx", tc.st, tc.goos)
			if op.Auth != tc.wantAuth {
				t.Errorf("Auth = %v, want %v", op.Auth, tc.wantAuth)
			}
			if op.Sandbox != "sbx" {
				t.Errorf("Sandbox = %q, want sbx", op.Sandbox)
			}
		})
	}
}

// The macOS stopped-overlay message must call out that the changes are already
// lost, so the user isn't misled into thinking abandon is a live choice.
func TestClassifyOverlay_MacStoppedMessagesLoss(t *testing.T) {
	op := classifyOverlay("sbx", status.StatusStopped, "darwin")
	if !strings.Contains(op.Description, "already lost") {
		t.Errorf("macOS stopped message = %q, want it to flag the loss", op.Description)
	}
}

// With no overlay sandboxes, Apply stamps v4 without ever opening a runtime — the
// common no-overlay migrate path needs no backend.
func TestOverlayFlatten_NoOverlayStampsV4WithoutRuntime(t *testing.T) {
	dir := t.TempDir()
	layout := config.NewLayout(dir)

	runtimeOpened := false
	m := NewOverlayFlatten(layout, dir, layout.SandboxesDir(), "linux",
		func(context.Context) (runtime.Backend, error) {
			runtimeOpened = true
			t.Error("runtime must not be opened when there are no overlay sandboxes")
			return nil, nil
		})

	if _, err := m.Apply(context.Background(), migrate.Decision{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if runtimeOpened {
		t.Error("runtime was opened despite no overlay sandboxes")
	}
	v, exists, err := config.ReadSchemaVersion(layout.SchemaVersionPath())
	if err != nil {
		t.Fatalf("read stamp: %v", err)
	}
	if !exists || v != config.LibrarySchemaVersion {
		t.Errorf("stamp = %d (exists=%v), want %d", v, exists, config.LibrarySchemaVersion)
	}
}

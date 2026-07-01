package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/migrate"
	"github.com/kstenerud/yoloai/internal/orchestrator/status"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/store"
)

// TestOverlayFlatten_AbandonFromFixture exercises the promotion + copy-mode
// conversion off disk, no container: it stages a fake stopped overlay sandbox
// (environment.json Mode=overlay + a pristine lower/) and runs the abandon
// flatten, asserting the sandbox becomes :copy carrying the lower's content with
// MountPath reset to the host path. (The running-capture path is Docker-validated
// at commit f5a914e5, before Phase 4 deleted the overlay create path.)
func TestOverlayFlatten_AbandonFromFixture(t *testing.T) {
	dir := t.TempDir()
	layout := config.NewLayout(dir)
	const name, hostPath = "sbx", "/proj"
	sandboxDir := layout.SandboxDir(name)
	enc := store.EncodePath(hostPath)

	// Pristine lower (the original workdir copy) with a file the flatten must keep.
	lower := store.OverlayLowerDir(sandboxDir, hostPath)
	if err := fileutil.MkdirAll(lower, 0o750); err != nil {
		t.Fatalf("mkdir lower: %v", err)
	}
	if err := os.WriteFile(filepath.Join(lower, "keep.txt"), []byte("orig"), 0o600); err != nil {
		t.Fatalf("write lower file: %v", err)
	}
	// A stopped overlay sandbox's on-disk form.
	env := &store.Environment{Name: name, Dirs: []store.DirEnvironment{{
		HostPath:    hostPath,
		MountPath:   "/yoloai/overlay/" + enc + "/merged",
		Mode:        store.DirModeOverlay,
		BaselineSHA: "deadbeef",
	}}}
	if err := store.SaveEnvironment(sandboxDir, env); err != nil {
		t.Fatalf("save env: %v", err)
	}

	m := NewOverlayFlatten(layout, dir, layout.SandboxesDir(), "linux",
		func(context.Context) (runtime.Backend, error) {
			t.Error("abandon flatten must not open a runtime")
			return nil, nil
		})
	if _, err := m.flattenAbandon(name); err != nil {
		t.Fatalf("flattenAbandon: %v", err)
	}

	flat, err := store.LoadEnvironment(sandboxDir)
	if err != nil {
		t.Fatalf("reload env: %v", err)
	}
	if flat.Workdir().Mode != store.DirModeCopy {
		t.Errorf("Mode = %q, want copy", flat.Workdir().Mode)
	}
	if flat.Workdir().MountPath != hostPath {
		t.Errorf("MountPath = %q, want %q", flat.Workdir().MountPath, hostPath)
	}
	got, err := os.ReadFile(filepath.Join(store.WorkDir(sandboxDir, hostPath), "keep.txt")) //nolint:gosec // test path
	if err != nil {
		t.Fatalf("read flattened work file: %v", err)
	}
	if string(got) != "orig" {
		t.Errorf("keep.txt = %q, want orig (lower content carried)", got)
	}
}

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

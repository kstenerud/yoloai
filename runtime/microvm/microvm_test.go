//go:build linux

package microvm

// ABOUTME: Unit tests for the microvm backend's static descriptor and the
// ABOUTME: host-prerequisite capability set (well-formedness, not host probing).

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/runtime"
)

func newTestRuntime(t *testing.T) *Runtime {
	t.Helper()
	r, err := New(context.Background(), config.NewLayout(filepath.Join(t.TempDir(), ".yoloai")))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r
}

func TestDescriptor(t *testing.T) {
	d := newTestRuntime(t).Descriptor()
	if d.Type != runtime.BackendMicroVM {
		t.Errorf("Type = %q, want %q", d.Type, runtime.BackendMicroVM)
	}
	if d.BaseModeName != runtime.IsolationModeMicroVM {
		t.Errorf("BaseModeName = %q, want microvm", d.BaseModeName)
	}
	if !d.IsolationTargetOnly {
		t.Error("IsolationTargetOnly = false, want true (reached only via --isolation microvm)")
	}
	if got := d.Platforms; len(got) != 1 || got[0] != "linux" {
		t.Errorf("Platforms = %v, want [linux]", got)
	}
	if d.Capabilities.KeepAliveModel != runtime.KeepAliveGuestOSInit {
		t.Errorf("KeepAliveModel = %v, want KeepAliveGuestOSInit", d.Capabilities.KeepAliveModel)
	}
	if d.Capabilities.FilesystemLocality != runtime.LocalityHostSide {
		t.Errorf("FilesystemLocality = %v, want LocalityHostSide", d.Capabilities.FilesystemLocality)
	}
	if d.Capabilities.NetworkIsolation {
		t.Error("NetworkIsolation = true, want false (phase 1 ships without network isolation)")
	}
}

func TestRequiredCapabilities(t *testing.T) {
	r := newTestRuntime(t)
	caps := r.RequiredCapabilities(runtime.IsolationModeMicroVM)

	wantIDs := map[string]bool{
		"qemu-microvm": false, "kvm-device": false, "skopeo": false,
		"umoci": false, "virtiofsd": false,
	}
	if len(caps) != len(wantIDs) {
		t.Fatalf("RequiredCapabilities returned %d caps, want %d", len(caps), len(wantIDs))
	}
	for _, c := range caps {
		if _, ok := wantIDs[c.ID]; !ok {
			t.Errorf("unexpected capability ID %q", c.ID)
			continue
		}
		wantIDs[c.ID] = true
		if c.Summary == "" {
			t.Errorf("capability %q has empty Summary", c.ID)
		}
		if c.Check == nil {
			t.Errorf("capability %q has nil Check", c.ID)
		}
		if c.Fix == nil {
			t.Errorf("capability %q has nil Fix", c.ID)
		}
	}
	for id, seen := range wantIDs {
		if !seen {
			t.Errorf("missing expected capability %q", id)
		}
	}
}

// TestRequiredCapabilities_ModeIndependent guards that the capability set does
// not depend on the isolation mode (microvm has a single mode).
func TestRequiredCapabilities_ModeIndependent(t *testing.T) {
	r := newTestRuntime(t)
	if a, b := r.RequiredCapabilities(runtime.IsolationModeMicroVM), r.RequiredCapabilities(""); len(a) != len(b) {
		t.Errorf("cap count differs by mode: %d vs %d", len(a), len(b))
	}
}

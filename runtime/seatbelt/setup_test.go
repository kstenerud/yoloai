// ABOUTME: Per-test seatbelt setup helper, shared by the untagged VM-free
// ABOUTME: basics and the tagged integration tests. Skips off macOS
// ABOUTME: (sandbox-exec is structurally impossible there); on macOS an absent
// ABOUTME: backend FAILS per the mandatory-infra policy (D112) unless carved out.

package seatbelt

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/testutil"
)

// seatbeltSetup skips off macOS, isolates HOME so the test doesn't touch the
// real ~/.yoloai/, and returns a *Runtime ready for use. On macOS a failed New
// is the D112 gate: FAIL (absence is a misconfiguration), skip only under the
// YOLOAI_TEST_UNCONTROLLED_BACKENDS carve-out. Cleanup of the sandbox
// directory is handled by t.TempDir().
func seatbeltSetup(t *testing.T) (*Runtime, context.Context) {
	t.Helper()
	if !isMacOS() {
		t.Skip("seatbelt requires macOS; structurally impossible on this platform")
	}
	home := testutil.IsolatedHome(t)
	ctx := context.Background()

	rt, err := New(ctx, config.NewLayout(filepath.Join(home, ".yoloai")).WithPrincipal(config.CLIPrincipal), home)
	if err != nil {
		testutil.RequireBackend(t, "seatbelt", err.Error())
	}

	return rt, ctx
}

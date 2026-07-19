// ABOUTME: Per-test tart setup helper, shared by the untagged VM-free basics
// ABOUTME: and any tagged integration test. Skips off macOS (tart is
// ABOUTME: structurally impossible there); on macOS an absent backend FAILS
// ABOUTME: per the mandatory-infra policy (D112) unless carved out.

package tart

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/testutil"
)

// tartSetup skips off macOS, isolates HOME so the test doesn't touch the real
// ~/.yoloai/, and returns a *Runtime ready for use. On macOS a failed New is
// the D112 gate: FAIL (absence is a misconfiguration), skip only under the
// YOLOAI_TEST_UNCONTROLLED_BACKENDS carve-out.
func tartSetup(t *testing.T) (*Runtime, context.Context) {
	t.Helper()
	if !isMacOS() {
		t.Skip("tart requires macOS; structurally impossible on this platform")
	}
	home := testutil.IsolatedHome(t)
	ctx := context.Background()

	rt, err := New(ctx, config.NewLayout(filepath.Join(home, ".yoloai")).WithPrincipal(config.CLIPrincipal))
	if err != nil {
		testutil.RequireBackend(t, "tart", err.Error())
	}

	return rt, ctx
}

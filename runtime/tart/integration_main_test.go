//go:build integration

// ABOUTME: TestMain for tart integration tests. Verifies macOS + Apple Silicon +
// ABOUTME: tart CLI before any integration test runs; otherwise skips cleanly.

package tart

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/testutil"
)

// TestMain verifies tart is available before running integration tests.
// On non-macOS / non-AppleSilicon / no-tart platforms the suite skips
// cleanly (exit 0). Matches the Docker/Podman/Seatbelt pattern so the CI
// matrix can run this target everywhere and only execute where it makes
// sense.
func TestMain(m *testing.M) {
	// TestMain runs before any t.TempDir; use a real temp dir for the
	// availability probe. Tart's New only checks tool presence so the
	// path isn't actually used here.
	tmp, _ := os.MkdirTemp("", "tart-probe-*")
	defer os.RemoveAll(tmp) //nolint:errcheck // best-effort cleanup
	rt, err := New(context.Background(), config.NewLayout(filepath.Join(tmp, ".yoloai")).WithPrincipal(config.CLIPrincipal))
	if err != nil {
		// Tart is macOS + Apple Silicon only. On any non-macOS host it is
		// structurally impossible, not merely absent — outside the mandatory-infra
		// policy (D112), so it skips cleanly like the containerd non-Linux stub.
		// Only on darwin is absence a failure, subject to the carve-out env.
		if runtime.GOOS != "darwin" {
			fmt.Fprintf(os.Stderr, "tart backend not applicable on %s — skipping integration tests\n", runtime.GOOS)
			os.Exit(0)
		}
		os.Exit(testutil.BackendAbsent("tart", err.Error()))
	}
	_ = rt
	os.Exit(m.Run())
}

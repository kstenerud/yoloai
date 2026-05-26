//go:build integration

// ABOUTME: TestMain for tart integration tests. Verifies macOS + Apple Silicon +
// ABOUTME: tart CLI before any integration test runs; otherwise skips cleanly.

package tart

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/kstenerud/yoloai/config"
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
	rt, err := New(context.Background(), config.NewLayout(filepath.Join(tmp, ".yoloai")))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Tart unavailable, skipping integration tests: %v\n", err)
		os.Exit(0)
	}
	_ = rt
	os.Exit(m.Run())
}

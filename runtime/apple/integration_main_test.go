//go:build integration

// ABOUTME: TestMain for apple integration tests. Verifies macOS 26 + Apple
// ABOUTME: Silicon + the `container` CLI before any integration test runs;
// ABOUTME: otherwise skips cleanly (exit 0), matching the docker/podman/
// ABOUTME: seatbelt/tart pattern so the CI matrix can run this target everywhere
// ABOUTME: and only execute where the backend is actually available.

package apple

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

// TestMain probes apple-backend availability once. New() enforces the full gate
// (macOS major >= minMacOSMajor, Apple Silicon, container CLI installed); if any
// is missing the suite skips cleanly so a Linux or older-macOS host runs the
// same `integration-apple` target without failing. The apiserver is started
// on demand by the per-test setup, not here.
func TestMain(m *testing.M) {
	os.Exit(testMain(m))
}

// testMain holds the real TestMain body in a function that RETURNS its exit
// code, so the deferred temp-dir cleanup actually runs — os.Exit (called only
// by the thin TestMain wrapper) skips defers.
func testMain(m *testing.M) int {
	tmp, _ := os.MkdirTemp("", "apple-probe-*")
	defer func() { _ = os.RemoveAll(tmp) }()
	rt, err := New(context.Background(), config.NewLayout(filepath.Join(tmp, ".yoloai")).WithPrincipal(config.CLIPrincipal))
	if err != nil {
		// The apple `container` backend is macOS-only (macOS 26 + Apple Silicon).
		// On any non-macOS host it is structurally impossible, not merely absent —
		// so it falls outside the mandatory-infra policy (D112) and skips cleanly,
		// mirroring the containerd non-Linux stub. Only where the platform can host
		// it (darwin) is absence a failure, subject to the carve-out env.
		if runtime.GOOS != "darwin" {
			fmt.Fprintf(os.Stderr, "apple backend not applicable on %s — skipping integration tests\n", runtime.GOOS)
			return 0
		}
		return testutil.BackendAbsent("apple", err.Error())
	}
	_ = rt
	return m.Run()
}

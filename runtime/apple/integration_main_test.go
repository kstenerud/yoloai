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
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
)

// TestMain probes apple-backend availability once. New() enforces the full gate
// (macOS major >= minMacOSMajor, Apple Silicon, container CLI installed); if any
// is missing the suite skips cleanly so a Linux or older-macOS host runs the
// same `integration-apple` target without failing. The apiserver is started
// on demand by the per-test setup, not here.
func TestMain(m *testing.M) {
	tmp, _ := os.MkdirTemp("", "apple-probe-*")
	defer os.RemoveAll(tmp) //nolint:errcheck // best-effort cleanup
	rt, err := New(context.Background(), config.NewLayout(filepath.Join(tmp, ".yoloai")))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Apple container backend unavailable, skipping integration tests: %v\n", err)
		os.Exit(0)
	}
	_ = rt
	os.Exit(m.Run())
}

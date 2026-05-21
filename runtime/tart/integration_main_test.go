//go:build integration

// ABOUTME: TestMain for tart integration tests. Verifies macOS + Apple Silicon +
// ABOUTME: tart CLI before any integration test runs; otherwise skips cleanly.

package tart

import (
	"context"
	"fmt"
	"os"
	"testing"
)

// TestMain verifies tart is available before running integration tests.
// On non-macOS / non-AppleSilicon / no-tart platforms the suite skips
// cleanly (exit 0). Matches the Docker/Podman/Seatbelt pattern so the CI
// matrix can run this target everywhere and only execute where it makes
// sense.
func TestMain(m *testing.M) {
	rt, err := New(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Tart unavailable, skipping integration tests: %v\n", err)
		os.Exit(0)
	}
	_ = rt
	os.Exit(m.Run())
}

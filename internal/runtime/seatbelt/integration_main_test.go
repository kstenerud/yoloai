//go:build integration

// ABOUTME: TestMain for seatbelt integration tests. Verifies macOS + sandbox-exec
// ABOUTME: availability before any integration test runs; otherwise skips cleanly.

package seatbelt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
)

// TestMain verifies macOS + sandbox-exec are available before running
// integration tests. On other platforms the tests skip silently (matches
// the Docker/Podman pattern); on macOS without sandbox-exec the tests skip
// with a diagnostic line.
func TestMain(m *testing.M) {
	tmp, _ := os.MkdirTemp("", "seatbelt-probe-*")
	defer os.RemoveAll(tmp) //nolint:errcheck // best-effort cleanup
	rt, err := New(context.Background(), config.NewLayout(filepath.Join(tmp, ".yoloai")), tmp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Seatbelt unavailable, skipping integration tests: %v\n", err)
		os.Exit(0)
	}
	_ = rt
	os.Exit(m.Run())
}

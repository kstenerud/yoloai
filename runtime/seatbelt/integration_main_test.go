//go:build integration

// ABOUTME: TestMain for seatbelt integration tests. Verifies macOS + sandbox-exec
// ABOUTME: availability before any integration test runs; otherwise skips cleanly.

package seatbelt

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

// TestMain verifies macOS + sandbox-exec are available before running
// integration tests. On other platforms the tests skip silently (matches
// the Docker/Podman pattern); on macOS without sandbox-exec the tests skip
// with a diagnostic line.
func TestMain(m *testing.M) {
	tmp, _ := os.MkdirTemp("", "seatbelt-probe-*")
	defer os.RemoveAll(tmp) //nolint:errcheck // best-effort cleanup
	rt, err := New(context.Background(), config.NewLayout(filepath.Join(tmp, ".yoloai")), tmp)
	if err != nil {
		// Seatbelt (sandbox-exec) is macOS-only. On any non-macOS host it is
		// structurally impossible, not merely absent — outside the mandatory-infra
		// policy (D112), so it skips cleanly like the containerd non-Linux stub.
		// Only on darwin is absence a failure, subject to the carve-out env.
		if runtime.GOOS != "darwin" {
			fmt.Fprintf(os.Stderr, "seatbelt backend not applicable on %s — skipping integration tests\n", runtime.GOOS)
			os.Exit(0)
		}
		os.Exit(testutil.BackendAbsent("seatbelt", err.Error()))
	}
	_ = rt
	os.Exit(m.Run())
}

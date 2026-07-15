//go:build integration

// ABOUTME: TestMain for podman package-internal integration tests: connects to
// ABOUTME: a real Podman once and verifies the base image exists before any
// ABOUTME: test runs, so failures point at setup, not a flaky per-test dial.
package podman

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/testutil"
)

// TestMain connects to Podman once and verifies the base image exists before
// any integration tests run. Individual tests still call podmanSetup(t) to get
// a per-test *Runtime with cleanup registered.
func TestMain(m *testing.M) {
	ctx := context.Background()

	rt, err := New(ctx, config.Layout{}.WithEnv(testutil.GetCuratedHostEnv(testutil.IntegrationHostEnvVars)))
	if err != nil {
		os.Exit(testutil.BackendAbsent("podman", err.Error()))
	}
	defer rt.Close() //nolint:errcheck // best-effort close in test main

	exists, err := rt.IsReady(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "IsReady check failed: %v\n", err)
		os.Exit(1)
	}
	if !exists {
		fmt.Fprintf(os.Stderr, "yoloai-base image not found — run 'make build && ./yoloai system setup' first\n")
		os.Exit(1)
	}

	os.Exit(m.Run())
}

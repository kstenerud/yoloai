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
	os.Exit(testMain(m))
}

// testMain holds the real TestMain body in a function that RETURNS its exit
// code, so the deferred Runtime close actually runs — os.Exit (called only by
// the thin TestMain wrapper) skips defers.
func testMain(m *testing.M) int {
	ctx := context.Background()

	rt, err := New(ctx, config.Layout{}.WithEnv(testutil.GetCuratedHostEnv(testutil.IntegrationHostEnvVars)))
	if err != nil {
		return testutil.BackendAbsent("podman", err.Error())
	}
	defer func() { _ = rt.Close() }()

	exists, err := rt.IsReady(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "IsReady check failed: %v\n", err)
		return 1
	}
	if !exists {
		fmt.Fprintf(os.Stderr, "yoloai-base image not found — run 'make build && ./yoloai system setup' first\n")
		return 1
	}

	return m.Run()
}

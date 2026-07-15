//go:build integration

// ABOUTME: TestMain for the docker package's integration tests: connects to a
// ABOUTME: real Docker daemon once and requires the yoloai-base image be ready
// ABOUTME: before any test runs, so failures point at setup, not a flaky test.
package docker

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/testutil"
)

// TestMain connects to Docker once and verifies the base image exists before
// any integration tests run. Individual tests still call dockerSetup(t) to get
// a per-test *Runtime with cleanup registered.
func TestMain(m *testing.M) {
	ctx := context.Background()
	step := testutil.TestMainBreadcrumb("docker")

	var rt *Runtime
	var dockerErr error
	step("connecting to docker", func() {
		rt, dockerErr = New(ctx, config.Layout{}.WithEnv(testutil.GetCuratedHostEnv(testutil.IntegrationHostEnvVars)))
	})
	if dockerErr != nil {
		os.Exit(testutil.BackendAbsent("docker", dockerErr.Error()))
	}
	defer rt.Close() //nolint:errcheck // best-effort close in test main

	var exists bool
	var readyErr error
	step("verifying base image exists", func() {
		exists, readyErr = rt.IsReady(ctx)
	})
	if readyErr != nil {
		fmt.Fprintf(os.Stderr, "IsReady check failed: %v\n", readyErr)
		os.Exit(1)
	}
	if !exists {
		fmt.Fprintf(os.Stderr, "yoloai-base image not found — run 'make build && ./yoloai system setup' first\n")
		os.Exit(1)
	}

	os.Exit(m.Run())
}

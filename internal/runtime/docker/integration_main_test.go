//go:build integration

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
		rt, dockerErr = New(ctx, config.Layout{Env: testutil.HostEnv()})
	})
	if dockerErr != nil {
		fmt.Fprintf(os.Stderr, "Docker unavailable, skipping integration tests: %v\n", dockerErr)
		os.Exit(0)
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

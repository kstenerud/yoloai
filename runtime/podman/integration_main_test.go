//go:build integration

package podman

import (
	"context"
	"fmt"
	"os"
	"testing"
)

// TestMain connects to Podman once and verifies the base image exists before
// any integration tests run. Individual tests still call podmanSetup(t) to get
// a per-test *Runtime with cleanup registered.
func TestMain(m *testing.M) {
	ctx := context.Background()

	rt, err := New(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Podman unavailable, skipping integration tests: %v\n", err)
		os.Exit(0)
	}
	defer rt.Close() //nolint:errcheck // best-effort close in test main

	exists, err := rt.IsReady(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "IsReady check failed: %v\n", err)
		os.Exit(1)
	}
	if !exists {
		fmt.Fprintf(os.Stderr, "yoloai-base image not found — run 'make build && ./yoloai setup' first\n")
		os.Exit(1)
	}

	os.Exit(m.Run())
}

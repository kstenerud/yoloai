//go:build integration && !linux

// ABOUTME: Stub TestMain for non-Linux hosts. The containerd backend is
// ABOUTME: Linux-only, so every other file here is compiled out; without this
// ABOUTME: stub `go test -tags=integration ./.../containerd/` would error with
// ABOUTME: "build constraints exclude all Go files". It exits 0 so the shared
// ABOUTME: integration matrix runs this target everywhere and only executes
// ABOUTME: where the backend exists — matching docker/podman/apple/seatbelt/tart.

package containerdrt

import (
	"fmt"
	"os"
	"testing"
)

// TestMain skips containerd tests on non-Linux platforms, since the containerd
// backend uses Linux-specific syscalls and APIs.
func TestMain(m *testing.M) {
	fmt.Fprintf(os.Stderr, "containerd backend unavailable (Linux-only), skipping integration tests\n")
	os.Exit(0)
}

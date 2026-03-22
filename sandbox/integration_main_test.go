//go:build integration

package sandbox

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	dockerrt "github.com/kstenerud/yoloai/runtime/docker"
)

// TestMain builds the base Docker image once before any integration tests run.
// Individual tests still call integrationSetup(t) which uses IsolatedHome(t)
// for per-test sandbox isolation; subsequent Setup calls hit the cache and
// return in milliseconds.
func TestMain(m *testing.M) {
	ctx := context.Background()

	tmpHome, err := os.MkdirTemp("", "yoloai-setup-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create temp home: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpHome)
	os.Setenv("HOME", tmpHome) //nolint:errcheck // best-effort env set in test main

	rt, err := dockerrt.New(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Docker unavailable, skipping integration tests: %v\n", err)
		os.Exit(0)
	}
	defer rt.Close() //nolint:errcheck // best-effort close in test main

	mgr := NewManager(rt, slog.Default(), strings.NewReader(""), io.Discard)
	if err := mgr.EnsureSetup(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "EnsureSetup failed: %v\n", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

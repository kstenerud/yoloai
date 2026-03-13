//go:build integration

package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"testing"
)

// TestMain runs EnsureSetup once (via a throwaway sandbox creation) before any
// integration tests run, so the base Docker image is ready. Individual tests
// still call cliSetup(t) for per-test HOME isolation; subsequent EnsureSetup
// calls inside cliSetup hit the image cache and return in milliseconds.
func TestMain(m *testing.M) {
	tmpHome, err := os.MkdirTemp("", "yoloai-cli-setup-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create temp home: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpHome)
	os.Setenv("HOME", tmpHome) //nolint:errcheck // best-effort env set in test main

	// Bootstrap: create a throwaway sandbox to trigger EnsureSetup (image build).
	root := newRootCmd("test", "test", "test")
	root.SetArgs([]string{"new", "--agent", "test", "--no-start", "cli-main-setup", tmpHome})
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	if err := root.ExecuteContext(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap EnsureSetup failed: %v\n", err)
		os.Exit(1)
	}

	// Clean up the bootstrap sandbox (best-effort).
	root = newRootCmd("test", "test", "test")
	root.SetArgs([]string{"destroy", "--yes", "cli-main-setup"})
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	_ = root.ExecuteContext(context.Background())

	os.Exit(m.Run())
}

//go:build integration

package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/kstenerud/yoloai/internal/testutil"
)

// writeTestBackendConfig pins the CLI's container-backend selection to the
// integration backend named by YOLOAI_TEST_BACKEND (default "docker"). Without
// this, autodetect prefers Docker whenever its socket exists, which would
// mismatch the runtime that test code constructs via
// testutil.NewIntegrationRuntime on a host where both Docker and Podman are
// installed (e.g. the ubuntu-24.04 GitHub runner).
func writeTestBackendConfig(home string) error {
	backend := testutil.IntegrationBackendName()
	if backend == "" || backend == "docker" {
		// Autodetect already prefers docker; nothing to pin.
		return nil
	}
	cfgDir := filepath.Join(home, ".yoloai")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		return fmt.Errorf("mkdir %s: %w", cfgDir, err)
	}
	return os.WriteFile(
		filepath.Join(cfgDir, "config.yaml"),
		[]byte(fmt.Sprintf("container_backend: %s\n", backend)),
		0600,
	)
}

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

	if err := writeTestBackendConfig(tmpHome); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write test backend config: %v\n", err)
		os.Exit(1)
	}

	// Bootstrap: create a throwaway sandbox to trigger EnsureSetup (image build).
	// Use a project subdirectory — tmpHome itself triggers the "dangerous directory" safety check.
	projectDir := filepath.Join(tmpHome, "project")
	if err := os.MkdirAll(projectDir, 0o750); err != nil {
		fmt.Fprintf(os.Stderr, "failed to create project dir: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write project file: %v\n", err)
		os.Exit(1)
	}

	root := newRootCmd("test", "test", "test")
	root.SetArgs([]string{"new", "--agent", "test", "--no-start", "cli-main-setup", projectDir})
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

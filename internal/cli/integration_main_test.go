//go:build integration

package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
	dockerrt "github.com/kstenerud/yoloai/internal/runtime/docker"
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
	// container_backend is a defaults-level key — it lives in defaults/config.yaml,
	// not the global config.yaml. Writing to the wrong file silently has no effect.
	defaultsDir := filepath.Join(home, ".yoloai", "defaults")
	if err := os.MkdirAll(defaultsDir, 0700); err != nil {
		return fmt.Errorf("mkdir %s: %w", defaultsDir, err)
	}
	return os.WriteFile(
		filepath.Join(defaultsDir, "config.yaml"),
		[]byte(fmt.Sprintf("container_backend: %s\n", backend)),
		0600,
	)
}

// pinPodmanSocket discovers the Podman Machine socket path using the real HOME
// and sets CONTAINER_HOST so discoverSocket() finds it after HOME is overridden.
// On macOS, "podman machine inspect" reads machine state from the real HOME; once
// we override HOME for test isolation, the subprocess fails and socket discovery
// falls through to "no podman socket found".
func pinPodmanSocket() {
	if testutil.IntegrationBackendName() != "podman" {
		return
	}
	out, err := exec.Command("podman", "machine", "inspect", "--format", "{{.ConnectionInfo.PodmanSocket.Path}}").Output() //nolint:gosec // trusted binary path
	if err != nil {
		return
	}
	sock := strings.TrimSpace(string(out))
	if sock == "" || sock == "<no value>" {
		return
	}
	os.Setenv("CONTAINER_HOST", "unix://"+sock) //nolint:errcheck // best-effort env pin in test setup
}

// TestMain runs EnsureSetup once (via a throwaway sandbox creation) before any
// integration tests run, so the base Docker image is ready. Individual tests
// still call cliSetup(t) for per-test HOME isolation; subsequent EnsureSetup
// calls inside cliSetup hit the image cache and return in milliseconds.
func TestMain(m *testing.M) {
	// Pin CONTAINER_HOST before overriding HOME — podman machine inspect reads
	// machine state from the real HOME directory.
	pinPodmanSocket()

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

	// Pre-seed the build-inputs checksum in the per-test HOME. `make integration`
	// builds the base image (via `make base-image`) immediately before this test
	// runs, so the docker daemon already has yoloai-base:latest with bytes that
	// match the current embedded build inputs. Without this seed, the bootstrap
	// invocation below reads the checksum from the fresh tmp HOME, finds nothing,
	// and triggers a redundant rebuild — which races with the daemon's
	// delete-then-create on the tag and intermittently fails with
	// "AlreadyExists after deleting the existing one".
	// See backend-idiosyncrasies.md "Docker daemon races on AlreadyExists when
	// rebuilding an existing tag with identical content".
	if testutil.IntegrationBackendName() == "" || testutil.IntegrationBackendName() == "docker" {
		integLayout := config.NewLayout(filepath.Join(tmpHome, ".yoloai"))
		if err := os.MkdirAll(integLayout.CacheDir(), 0750); err != nil {
			fmt.Fprintf(os.Stderr, "failed to create cache dir: %v\n", err)
			os.Exit(1)
		}
		dockerrt.RecordBuildChecksum(integLayout, "")
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

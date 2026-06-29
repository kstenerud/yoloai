//go:build integration

package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/sysexec"
	"github.com/kstenerud/yoloai/internal/testutil"
	dockerrt "github.com/kstenerud/yoloai/runtime/docker"
)

// writeTestBackendConfig pins the CLI's container-backend selection to the
// integration backend named by YOLOAI_TEST_BACKEND (default "docker"). Without
// this, autodetect prefers Docker whenever its socket exists, which would
// mismatch the runtime that test code constructs via
// testutil.NewIntegrationRuntime on a host where both Docker and Podman are
// installed (e.g. the ubuntu-24.04 GitHub runner).
func writeTestBackendConfig(home string) error {
	backend := testutil.IntegrationBackendType()
	if backend == "" || backend == "docker" {
		// Autodetect already prefers docker; nothing to pin.
		return nil
	}
	// container_backend is a defaults-level key — it lives in defaults/config.yaml,
	// not the global config.yaml. Writing to the wrong file silently has no effect.
	defaultsDir := filepath.Join(home, ".yoloai", "library", "defaults")
	if err := os.MkdirAll(defaultsDir, 0700); err != nil {
		return fmt.Errorf("mkdir %s: %w", defaultsDir, err)
	}
	return os.WriteFile(
		filepath.Join(defaultsDir, "config.yaml"),
		[]byte(fmt.Sprintf("container_backend: %s\n", backend)),
		0600,
	)
}

// stampRealms writes the current plain-int version stamps for both the library
// and cli realms under home/.yoloai, creating the cli realm dir. The integration
// harnesses seed TOP/library (build checksum and/or backend config) before
// invoking commands through the startup gate (D61); the gate then runs its
// read-only realm checks rather than create-fresh. Without both realms stamped
// it would flag an inconsistent data dir (library present, cli uninitialized).
// Stamping presents a consistent, current install so the gate proceeds — it no
// longer auto-migrates.
func stampRealms(home string) error {
	top := filepath.Join(home, ".yoloai")
	if err := config.WriteSchemaVersion(
		config.SchemaVersionPathFor(filepath.Join(top, "library")),
		config.LibrarySchemaVersion); err != nil {
		return fmt.Errorf("stamp library realm: %w", err)
	}
	cliDir := filepath.Join(top, "cli")
	if err := os.MkdirAll(cliDir, 0750); err != nil {
		return fmt.Errorf("create cli realm dir: %w", err)
	}
	if err := config.WriteSchemaVersion(
		config.SchemaVersionPathFor(cliDir), cliutil.CLISchemaVersion); err != nil {
		return fmt.Errorf("stamp cli realm: %w", err)
	}
	return nil
}

// pinPodmanSocket discovers the Podman Machine socket path using the real HOME
// and sets CONTAINER_HOST so discoverSocket() finds it after HOME is overridden.
// On macOS, "podman machine inspect" reads machine state from the real HOME; once
// we override HOME for test isolation, the subprocess fails and socket discovery
// falls through to "no podman socket found".
//
// TMPDIR is load-bearing in the probe env: macOS podman derives the machine API
// socket path from $TMPDIR ($TMPDIR/podman/...). Without it, inspect reports the
// /tmp fallback path, which doesn't exist, and we'd pin a dead CONTAINER_HOST —
// the Docker SDK ping then fails with "daemon is not responding". Mirrors the
// production allowlist (config.daemonEnvAllowlist).
func pinPodmanSocket() {
	if testutil.IntegrationBackendType() != "podman" {
		return
	}
	// Minimal curated env (real HOME, since this runs before per-test HOME
	// override; PATH to find podman; TMPDIR for the macOS socket path) — never
	// the full ambient env (DEV §12).
	probeEnv := sysexec.Curated(testutil.GetCuratedHostEnv([]string{"PATH", "HOME", "TMPDIR"}), []string{"PATH", "HOME", "TMPDIR"}, nil)
	out, err := sysexec.Command(probeEnv, "podman", "machine", "inspect", "--format", "{{.ConnectionInfo.PodmanSocket.Path}}").Output()
	if err != nil {
		return
	}
	sock := strings.TrimSpace(string(out))
	if sock == "" || sock == "<no value>" {
		return
	}
	os.Setenv("CONTAINER_HOST", "unix://"+sock) //nolint:errcheck // best-effort env pin in test setup
}

// integrationBackendKey maps the active integration backend type (from
// YOLOAI_TEST_BACKEND; "" means the default docker) to the per-image-store
// checksum key used by RecordBuildChecksum (DF56). The key equals the runtime's
// binaryName — docker and podman each name their own store — so a pre-seed under
// one backend doesn't satisfy (or get satisfied by) the other.
func integrationBackendKey(backendType string) string {
	if backendType == "" {
		return "docker"
	}
	return backendType
}

// TestMain runs EnsureSetup once (via a throwaway sandbox creation) before any
// integration tests run, so the base Docker image is ready. Individual tests
// still call cliSetup(t) for per-test HOME isolation; subsequent EnsureSetup
// calls inside cliSetup hit the image cache and return in milliseconds.
func TestMain(m *testing.M) {
	step := testutil.TestMainBreadcrumb("cli")

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

	// Pre-seed the build-inputs checksum in the per-test HOME.
	// `make integration`/`make integration-podman` builds the base image
	// immediately before this test runs, so the daemon (or podman service
	// storage) already has yoloai-base:latest with bytes that match the
	// current embedded build inputs. Without this seed, the bootstrap
	// invocation below reads the checksum from the fresh tmp HOME, finds
	// nothing, and triggers a redundant rebuild. On docker that rebuild
	// races with the daemon's delete-then-create on the tag and
	// intermittently fails with "AlreadyExists after deleting the existing
	// one" (see backend-idiosyncrasies.md "Docker daemon races on
	// AlreadyExists when rebuilding an existing tag with identical
	// content"). On podman it's worse: buildBaseImage shells out to
	// `podman build` under the overridden HOME, whose rootless storage
	// graphroot follows $HOME — a fresh empty store — forcing a full cold
	// rebuild (re-pull + every RUN) that blows the package timeout. The
	// image already exists in the service storage that imageExists queries
	// via the socket, so seeding the checksum lets Setup skip the build.
	if bt := testutil.IntegrationBackendType(); bt == "" || bt == "docker" || bt == "podman" {
		integLayout := config.NewLayoutFor(filepath.Join(tmpHome, ".yoloai", "library"), tmpHome)
		if err := os.MkdirAll(integLayout.CacheDir(), 0750); err != nil {
			fmt.Fprintf(os.Stderr, "failed to create cache dir: %v\n", err)
			os.Exit(1)
		}
		// DF56: the checksum is keyed per image store. The CLI subset runs under
		// both docker and podman (YOLOAI_TEST_BACKEND), so seed the ACTIVE backend's
		// key — a fixed "docker" key would mismatch under podman and force a full
		// rebuild mid-test. The backend type matches the runtime's binaryName.
		dockerrt.RecordBuildChecksum(integLayout, integrationBackendKey(bt))
	}

	// The seeding above (backend config and/or build checksum) populates
	// TOP/library, so the startup gate (D61) sees a non-empty top dir and runs
	// its read-only realm checks instead of create-fresh. Stamp both realms so
	// the gate reads a consistent, current install and proceeds.
	if err := stampRealms(tmpHome); err != nil {
		fmt.Fprintf(os.Stderr, "failed to stamp realms: %v\n", err)
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

	var bootstrapErr error
	step("bootstrapping cli sandbox (triggers EnsureSetup)", func() {
		root := NewRootCmd("test", "test", "test")
		root.SetArgs([]string{"new", "--agent", "test", "--no-start", "cli-main-setup", projectDir})
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		bootstrapErr = root.ExecuteContext(context.Background())
	})
	if bootstrapErr != nil {
		fmt.Fprintf(os.Stderr, "bootstrap EnsureSetup failed: %v\n", bootstrapErr)
		os.Exit(1)
	}

	// Clean up the bootstrap sandbox (best-effort).
	root := NewRootCmd("test", "test", "test")
	root.SetArgs([]string{"destroy", "--abandon-unapplied", "cli-main-setup"})
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	_ = root.ExecuteContext(context.Background())

	os.Exit(m.Run())
}

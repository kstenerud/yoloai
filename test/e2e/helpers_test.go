//go:build e2e

// Package e2e_test contains end-to-end tests that compile the yoloai binary
// and exercise it as a subprocess. These tests require Docker to be running.
package e2e_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"
	"github.com/kstenerud/yoloai/internal/config"
	dockerrt "github.com/kstenerud/yoloai/internal/runtime/docker"
	"github.com/stretchr/testify/require"
)

// yoloaiBin is the path to the compiled binary, set once in TestMain.
var yoloaiBin string

// TestMain compiles the yoloai binary once before all E2E tests run.
func TestMain(m *testing.M) {
	// Locate module root by walking up from this test file.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		panic("runtime.Caller failed")
	}
	modRoot := findModuleRoot(filepath.Dir(thisFile))
	if modRoot == "" {
		panic("could not find go.mod")
	}

	tmp, err := os.MkdirTemp("", "yoloai-e2e-*")
	if err != nil {
		panic("MkdirTemp: " + err.Error())
	}
	defer os.RemoveAll(tmp)

	yoloaiBin = filepath.Join(tmp, "yoloai")
	build := exec.Command("go", "build", "-o", yoloaiBin, "./cmd/yoloai") //nolint:gosec // G204: known command
	build.Dir = modRoot
	if out, err := build.CombinedOutput(); err != nil {
		panic("build failed: " + string(out))
	}

	os.Exit(m.Run())
}

// findModuleRoot walks up from dir until it finds a go.mod file.
func findModuleRoot(dir string) string {
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// runYoloai runs the compiled yoloai binary with the given arguments.
// Returns stdout, stderr, and the exit code.
func runYoloai(t *testing.T, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(yoloaiBin, args...) //nolint:gosec // G204: test helper, path set in TestMain
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	exitCode = 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
	}
	return outBuf.String(), errBuf.String(), exitCode
}

// e2eSetup sets HOME to a fresh temp dir and bootstraps EnsureSetup via a
// throwaway sandbox. Returns the project directory path.
func e2eSetup(t *testing.T) string {
	t.Helper()
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// Pre-seed the build-inputs checksum in the per-test HOME. Same
	// rationale as the integration tests (see
	// internal/sandbox/integration_helpers_test.go and
	// backend-idiosyncrasies.md "Docker daemon races on AlreadyExists
	// when rebuilding an existing tag with identical content"). Without
	// this, the bootstrap `yoloai new` subprocess re-builds the base
	// image against the daemon's existing one and intermittently hangs
	// the Docker SDK HTTP transport on the delete-then-recreate race.
	// The subprocess inherits HOME from this process via t.Setenv, so
	// writing the checksum here is visible to the binary we'll launch.
	// D60 bifurcates the data dir: the library realm roots at
	// TOP/library and the CLI realm at TOP/cli. The binary's gate (D61,
	// no auto-migrate) requires both realms to present a consistent,
	// current install — otherwise it would route to "run yoloai system
	// migrate" or flag an inconsistent data dir. Seed library state under
	// TOP/library and stamp both realms at their current versions so the
	// gate reads both OK and proceeds.
	top := filepath.Join(tmpHome, ".yoloai")
	layout := config.NewLayout(filepath.Join(top, "library"))
	require.NoError(t, os.MkdirAll(layout.CacheDir(), 0750))
	dockerrt.RecordBuildChecksum(layout, "")

	// Pin the container backend to docker. Otherwise `new` resolves the
	// backend by auto-detect, which on a host with both docker and podman
	// installed can silently pick podman — running the "docker" e2e suite
	// against the wrong daemon. The Makefile builds the base image with
	// --backend docker, so the suite must exercise docker too.
	require.NoError(t, os.MkdirAll(layout.DefaultsDir(), 0750))
	require.NoError(t, os.WriteFile(layout.DefaultsConfigPath(),
		[]byte("container_backend: docker\n"), 0600))

	// Stamp both realms current so the startup gate proceeds.
	require.NoError(t, config.WriteSchemaVersion(
		config.SchemaVersionPathFor(filepath.Join(top, "library")),
		config.LibrarySchemaVersion))
	cliDir := filepath.Join(top, "cli")
	require.NoError(t, os.MkdirAll(cliDir, 0750))
	require.NoError(t, config.WriteSchemaVersion(
		config.SchemaVersionPathFor(cliDir), cliutil.CLISchemaVersion))

	projectDir := filepath.Join(t.TempDir(), "project")
	require.NoError(t, os.MkdirAll(projectDir, 0750))
	require.NoError(t, os.WriteFile(
		filepath.Join(projectDir, "main.go"),
		[]byte("package main\n\nfunc main() {}\n"),
		0600,
	))

	// Initialize git so yoloai doesn't warn about uncommitted changes.
	gitInit := func(args ...string) {
		cmd := exec.Command("git", args...) //nolint:gosec // G204: test helper
		cmd.Dir = projectDir
		_ = cmd.Run()
	}
	gitInit("init")
	gitInit("config", "user.email", "test@test.com")
	gitInit("config", "user.name", "Test")
	gitInit("add", ".")
	gitInit("commit", "-m", "initial")

	// Bootstrap: trigger EnsureSetup (builds Docker image if not present).
	_, _, code := runYoloai(t, "new", "--agent", "test", "--no-start", "e2e-setup", projectDir)
	if code != 0 {
		t.Fatal("e2eSetup: EnsureSetup bootstrap failed")
	}
	runYoloai(t, "destroy", "--abandon-unapplied", "e2e-setup") //nolint:errcheck // best-effort cleanup

	return projectDir
}

// destroySandbox is a cleanup helper that destroys a sandbox, ignoring errors.
func destroySandbox(t *testing.T, name string) {
	t.Helper()
	runYoloai(t, "destroy", "--abandon-unapplied", name) //nolint:errcheck // best-effort cleanup
}

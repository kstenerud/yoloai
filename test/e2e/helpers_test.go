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
	t.Setenv("HOME", t.TempDir())

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
	runYoloai(t, "destroy", "--yes", "e2e-setup") //nolint:errcheck // best-effort cleanup

	return projectDir
}

// destroySandbox is a cleanup helper that destroys a sandbox, ignoring errors.
func destroySandbox(t *testing.T, name string) {
	t.Helper()
	runYoloai(t, "destroy", "--yes", name) //nolint:errcheck // best-effort cleanup
}

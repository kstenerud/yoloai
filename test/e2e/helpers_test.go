//go:build e2e

// ABOUTME: Shared e2e infrastructure: TestMain compiles the yoloai binary once,
// ABOUTME: curated-env helpers for running it and `go build` (DEV §12), and
// ABOUTME: e2eSetup, which bootstraps a per-test HOME past the migration gate.

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
	"github.com/kstenerud/yoloai/internal/sysexec"
	"github.com/kstenerud/yoloai/internal/testutil"
	dockerrt "github.com/kstenerud/yoloai/runtime/docker"
	"github.com/stretchr/testify/require"
)

// yoloaiBin is the path to the compiled binary, set once in TestMain.
var yoloaiBin string

// sutEnv is the curated environment handed to the yoloai binary under test:
// PATH/HOME/TMPDIR + daemon-discovery + TLS/locale/proxy, snapshotted from the
// process env at call time (e2eSetup points HOME at a temp dir via t.Setenv, so
// a fresh snapshot here captures it). Never the full ambient env — the SUT gets
// only what it needs and curates its own subprocesses downstream (DEV §12).
func sutEnv() []string {
	vars := []string{
		"PATH", "HOME", "TMPDIR",
		"DOCKER_HOST", "DOCKER_CERT_PATH", "DOCKER_TLS_VERIFY", "DOCKER_API_VERSION", "DOCKER_CONFIG",
		"CONTAINER_HOST", "XDG_RUNTIME_DIR",
		"SSL_CERT_FILE", "SSL_CERT_DIR",
		"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy",
		"LANG", "LC_ALL", "LC_CTYPE",
	}
	return sysexec.Curated(testutil.GetCuratedHostEnv(vars), vars, nil)
}

// goBuildEnv is the curated environment for the `go build` of the SUT: PATH/HOME
// plus the Go toolchain vars a build legitimately needs (module cache, proxy,
// flags, cross-compile target), never the full ambient env (DEV §12).
func goBuildEnv() []string {
	vars := []string{
		// SUDO_UID lets `go build`'s VCS stamping run git in this repo under
		// `sudo make e2e`: git runs as root against a work tree owned by the
		// invoking user, and needs SUDO_UID to accept it instead of failing its
		// dubious-ownership guard ("error obtaining VCS status: exit status 128").
		// Absent off-sudo, so it is a no-op there. Mirrors sysexec.GitEnv.
		"PATH", "HOME", "TMPDIR", "SUDO_UID",
		"GOPATH", "GOCACHE", "GOMODCACHE", "GOROOT", "GOBIN",
		"GOFLAGS", "GOPROXY", "GOPRIVATE", "GONOSUMCHECK", "GONOSUMDB", "GOSUMDB", "GOINSECURE",
		"GOTOOLCHAIN", "GO111MODULE", "GOOS", "GOARCH", "GOARM", "GOAMD64", "GOEXPERIMENT",
		"CGO_ENABLED", "CC", "CXX",
	}
	return sysexec.Curated(testutil.GetCuratedHostEnv(vars), vars, nil)
}

// TestMain compiles the yoloai binary once before all E2E tests run.
func TestMain(m *testing.M) { os.Exit(runE2EMain(m)) }

// runE2EMain holds the real TestMain body in a function that RETURNS its exit
// code, so the deferred temp-dir cleanup actually runs — os.Exit (called only by
// the thin TestMain wrapper) skips defers, which previously leaked the e2e temp
// dir on every run. (Setup panics already run the defer via stack unwinding.)
func runE2EMain(m *testing.M) int {
	// Reclaim e2e temp dirs leaked by a PRIOR run killed before its defer ran
	// (SIGKILL/-timeout). The live run cleans its own via the defer below.
	testutil.SweepStaleTestHomes("yoloai-e2e-")

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
	defer os.RemoveAll(tmp) //nolint:errcheck // best-effort cleanup

	yoloaiBin = filepath.Join(tmp, "yoloai")
	build := sysexec.Command(goBuildEnv(), "go", "build", "-o", yoloaiBin, "./cmd/yoloai")
	build.Dir = modRoot
	if out, err := build.CombinedOutput(); err != nil {
		panic("build failed: " + string(out))
	}

	return m.Run()
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
	cmd := sysexec.Command(sutEnv(), yoloaiBin, args...)
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
	// internal/orchestrator/integration_helpers_test.go and
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
	// DF56: the checksum is keyed per image store; docker's Setup checks the
	// "docker" key, so the pre-seed must use it (an empty/mismatched key makes the
	// bootstrap `yoloai new` rebuild the base image mid-test).
	dockerrt.RecordBuildChecksum(layout, "docker")

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
		cmd := sysexec.Command(testutil.GitEnv(), "git", args...)
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

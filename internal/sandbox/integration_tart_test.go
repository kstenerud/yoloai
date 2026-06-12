//go:build integration

// ABOUTME: Integration tests for the Tart VM backend — full lifecycle, aux dirs,
// ABOUTME: git corruption resistance, and VM-local storage verification.
// ABOUTME: Guarded by YOLOAI_TEST_TART=1 because they require Apple Silicon + tart.

package sandbox_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/git"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/runtime/tart"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/kstenerud/yoloai/internal/sandbox/patch"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tartIntegrationSetup creates a Tart-based test environment.
// Returns nil if Tart is not available (caller should skip test).
// These tests are currently experimental and disabled by default.
// Set YOLOAI_TEST_TART=1 to enable them.
func tartIntegrationSetup(t *testing.T) (*sandbox.Engine, context.Context) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Tart integration test in short mode")
	}

	// Gated behind YOLOAI_TEST_TART=1 because each test clones+boots a multi-GB
	// macOS VM. (The old "workdir symlink fails for temp dirs" note was DF27 —
	// verified stale; the :copy path works. Runtime-level coverage now lives in
	// internal/runtime/tart TestTartConformance.)
	if os.Getenv("YOLOAI_TEST_TART") != "1" {
		t.Skip("skipping Tart integration test (set YOLOAI_TEST_TART=1 to enable)")
	}

	ctx := context.Background()

	home := testutil.IsolatedHome(t)
	layout := config.NewLayout(filepath.Join(home, ".yoloai"))

	rt, err := tart.New(ctx, layout)
	if err != nil {
		t.Skipf("Tart not available: %v", err)
		return nil, nil
	}
	t.Cleanup(func() { rt.Close() }) //nolint:errcheck // test cleanup

	mgr := sandbox.NewEngineWithRuntime(rt, slog.Default(), strings.NewReader(""), sandbox.WithLayout(layout))
	require.NoError(t, mgr.EnsureSetup(ctx, io.Discard))

	return mgr, ctx
}

// TestIntegrationTart_FullLifecycle tests the complete create → modify → diff → apply → reset cycle
// with Tart VM and VM-local work directories.
func TestIntegrationTart_FullLifecycle(t *testing.T) {
	mgr, ctx := tartIntegrationSetup(t)
	if mgr == nil {
		return // skipped
	}

	projectDir := testutil.GoProject(t)

	// Create sandbox (starts VM)
	sandboxName := "tart-lifecycle"
	_, err := createSandbox(ctx, mgr, sandbox.CreateOptions{
		Name:    sandboxName,
		Workdir: sandbox.DirSpec{Path: projectDir},
		Agent:   "test",
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(ctx, mgr, sandboxName) }) //nolint:errcheck // test cleanup

	// Wait for VM to become active
	testutil.WaitForActive(ctx, t, mgr.Runtime(), store.InstanceName("", sandboxName), 90*time.Second)

	// Verify sandbox directory structure
	sandboxDir := mgr.Layout().SandboxDir(sandboxName)
	assert.DirExists(t, sandboxDir)

	meta, err := store.LoadEnvironment(sandboxDir)
	require.NoError(t, err)
	assert.Equal(t, sandboxName, meta.Name)
	assert.Equal(t, agent.AgentTest, meta.AgentType)
	assert.Equal(t, runtime.BackendTart, meta.BackendType)
	assert.Equal(t, store.DirModeCopy, meta.Workdir().Mode)
	assert.NotEmpty(t, meta.Workdir().BaselineSHA, "baseline SHA should be set after VM work dir setup")

	// Verify work directory path is VM-local (not VirtioFS)
	vmLocalPath := runtime.ResolveCopyMountFor(mgr.Runtime(), sandboxName, projectDir)
	assert.Contains(t, vmLocalPath, "/Users/admin/yoloai-work/", "work dir should be on VM local storage")
	assert.Equal(t, vmLocalPath, meta.Workdir().MountPath)

	// Verify VM is running
	status, err := sandbox.DetectStatus(ctx, mgr.Runtime(), store.InstanceName("", sandboxName), mgr.Layout().SandboxDir(sandboxName))
	require.NoError(t, err)
	assert.Equal(t, sandbox.StatusActive, status)

	// Exec inside running VM to verify it's functional
	result, err := mgr.Runtime().Exec(ctx, store.InstanceName("", sandboxName), []string{"echo", "vm-test"}, "admin")
	require.NoError(t, err)
	assert.Equal(t, "vm-test", result.Stdout)
	assert.Equal(t, 0, result.ExitCode)

	// Verify git is functional inside VM work directory
	result, err = mgr.Runtime().Exec(ctx, store.InstanceName("", sandboxName),
		[]string{"git", "-C", vmLocalPath, "status", "--short"}, "admin")
	require.NoError(t, err)
	assert.Empty(t, result.Stdout, "work dir should be clean after setup")
	assert.Equal(t, 0, result.ExitCode)

	// Modify a file inside the VM (simulating agent work)
	modifyCmd := []string{"bash", "-c",
		"cd " + vmLocalPath + " && echo 'package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"modified\") }' > main.go"}
	result, err = mgr.Runtime().Exec(ctx, store.InstanceName("", sandboxName), modifyCmd, "admin")
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)

	// Verify git detects the change
	result, err = mgr.Runtime().Exec(ctx, store.InstanceName("", sandboxName),
		[]string{"git", "-C", vmLocalPath, "status", "--short"}, "admin")
	require.NoError(t, err)
	assert.Contains(t, result.Stdout, "main.go", "git should detect modified file")

	// Generate diff (should use VM-exec path for Tart)
	diffResult, err := patch.GenerateDiff(ctx, patch.DiffOptions{Name: sandboxName, Layout: mgr.Layout(), Runtime: mgr.Runtime()})
	require.NoError(t, err)
	assert.NotEmpty(t, diffResult, "diff should not be empty after modification")
	assert.Contains(t, diffResult, "fmt.Println", "diff should contain modification")

	// Generate patch and apply to a target directory (while VM is still running)
	patchBytes, stat, err := patch.GeneratePatch(ctx, mgr.Layout(), mgr.Runtime(), sandboxName, "", nil, true)
	require.NoError(t, err)
	assert.NotEmpty(t, patchBytes)
	assert.Contains(t, stat, "main.go")

	targetDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(targetDir, "main.go"),
		[]byte("package main\n\nfunc main() {}\n"),
		0600,
	))

	require.NoError(t, git.NewHostWithEnv(testutil.GitEnv()).ApplyPatch(context.Background(), patchBytes, targetDir, false))

	applied, err := os.ReadFile(filepath.Join(targetDir, "main.go")) //nolint:gosec // G304: test file path
	require.NoError(t, err)
	assert.Contains(t, string(applied), "fmt.Println")

	// Stop VM: suspends the VM, freeing the quota slot.
	require.NoError(t, stopSandbox(ctx, mgr, sandboxName))

	status, err = sandbox.DetectStatus(ctx, mgr.Runtime(), store.InstanceName("", sandboxName), mgr.Layout().SandboxDir(sandboxName))
	require.NoError(t, err)
	assert.Equal(t, sandbox.StatusSuspended, status)

	// Restart: attempts to resume from suspend, but Apple VZ framework cannot restore
	// VMs with VirtioFS (--dir) mounts from a snapshot (VZErrorDomain Code=12), so
	// lifecycle falls back to destroy + recreate from staging. VM is fresh on start.
	_, startErr := startSandbox(ctx, mgr, sandboxName, sandbox.StartOptions{})
	require.NoError(t, startErr)
	testutil.WaitForActive(ctx, t, mgr.Runtime(), store.InstanceName("", sandboxName), 90*time.Second)

	status, err = sandbox.DetectStatus(ctx, mgr.Runtime(), store.InstanceName("", sandboxName), mgr.Layout().SandboxDir(sandboxName))
	require.NoError(t, err)
	assert.True(t, status == sandbox.StatusIdle || status == sandbox.StatusActive,
		"VM should be running after start, got %s", status)

	// Reload vmLocalPath for the recreated VM (same path, but VM is fresh)
	result, err = mgr.Runtime().Exec(ctx, store.InstanceName("", sandboxName),
		[]string{"git", "-C", vmLocalPath, "status", "--short"}, "admin")
	require.NoError(t, err)
	assert.Empty(t, result.Stdout, "work dir should be clean after recreate from staging")

	// Reset should restore clean state
	_, resetErr := resetSandbox(ctx, mgr, sandbox.ResetOptions{Name: sandboxName})
	require.NoError(t, resetErr)

	// Wait for VM to be active again after reset
	testutil.WaitForActive(ctx, t, mgr.Runtime(), store.InstanceName("", sandboxName), 90*time.Second)

	// Verify work directory is clean after reset
	result, err = mgr.Runtime().Exec(ctx, store.InstanceName("", sandboxName),
		[]string{"git", "-C", vmLocalPath, "status", "--short"}, "admin")
	require.NoError(t, err)
	assert.Empty(t, result.Stdout, "work dir should be clean after reset")

	// Destroy
	_, destroyErr := destroySandbox(ctx, mgr, sandboxName)
	require.NoError(t, destroyErr)
	assert.NoDirExists(t, sandboxDir)

	// VM should be gone
	status, err = sandbox.DetectStatus(ctx, mgr.Runtime(), store.InstanceName("", sandboxName), mgr.Layout().SandboxDir(sandboxName))
	require.NoError(t, err)
	assert.Equal(t, sandbox.StatusRemoved, status)
}

// TestIntegrationTart_MultipleAuxDirs verifies Tart with multiple aux
// directories. Aux :copy is no longer supported (Q-U, 2026-05-25), so
// this exercises the still-supported :rw mode: two writable aux dirs
// mounted into the VM, each accessible from inside and writable.
// Diff/apply remains workdir-only.
func TestIntegrationTart_MultipleAuxDirs(t *testing.T) {
	mgr, ctx := tartIntegrationSetup(t)
	if mgr == nil {
		return // skipped
	}

	projectDir := testutil.GoProject(t)
	auxDir1 := testutil.AuxDir(t, "libs")
	auxDir2 := testutil.AuxDir(t, "data")

	sandboxName := "tart-multiaux"
	_, err := createSandbox(ctx, mgr, sandbox.CreateOptions{
		Name:    sandboxName,
		Workdir: sandbox.DirSpec{Path: projectDir},
		Agent:   "test",
		AuxDirs: []sandbox.DirSpec{
			{Path: auxDir1, Mode: sandbox.DirModeRW},
			{Path: auxDir2, Mode: sandbox.DirModeRW},
		},
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(ctx, mgr, sandboxName) }) //nolint:errcheck // test cleanup

	// Wait for VM to become active
	testutil.WaitForActive(ctx, t, mgr.Runtime(), store.InstanceName("", sandboxName), 90*time.Second)

	meta, err := store.LoadEnvironment(mgr.Layout().SandboxDir(sandboxName))
	require.NoError(t, err)
	require.Len(t, meta.AuxDirs(), 2, "should have two aux directories")

	for i, dir := range meta.AuxDirs() {
		assert.Equal(t, store.DirModeRW, dir.Mode)
		// :rw is a live bind-mount; there's no baseline to capture.
		assert.Empty(t, dir.BaselineSHA, "aux dir %d should have no baseline (rw)", i)

		// Verify aux directory is accessible in VM
		result, err := mgr.Runtime().Exec(ctx, store.InstanceName("", sandboxName),
			[]string{"test", "-f", filepath.Join(dir.MountPath, "data.txt")}, "admin")
		require.NoError(t, err)
		assert.Equal(t, 0, result.ExitCode, "aux dir %d should be accessible in VM", i)
	}

	// Modify both aux dirs from inside the VM — :rw means writes land
	// on the host directly, so this also exercises the bind-mount.
	for i, dir := range meta.AuxDirs() {
		modifyCmd := []string{"bash", "-c",
			"echo 'modified' >> " + filepath.Join(dir.MountPath, "data.txt")}
		result, err := mgr.Runtime().Exec(ctx, store.InstanceName("", sandboxName), modifyCmd, "admin")
		require.NoError(t, err)
		assert.Equal(t, 0, result.ExitCode, "should modify aux dir %d", i)
	}
}

// TestIntegrationTart_GitCorruption runs repeated git operations to ensure no corruption.
func TestIntegrationTart_GitCorruption(t *testing.T) {
	mgr, ctx := tartIntegrationSetup(t)
	if mgr == nil {
		return // skipped
	}

	projectDir := testutil.GoProject(t)

	sandboxName := "tart-gitcorruption"
	_, err := createSandbox(ctx, mgr, sandbox.CreateOptions{
		Name:    sandboxName,
		Workdir: sandbox.DirSpec{Path: projectDir},
		Agent:   "test",
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(ctx, mgr, sandboxName) }) //nolint:errcheck // test cleanup

	testutil.WaitForActive(ctx, t, mgr.Runtime(), store.InstanceName("", sandboxName), 90*time.Second)

	meta, err := store.LoadEnvironment(mgr.Layout().SandboxDir(sandboxName))
	require.NoError(t, err)
	vmLocalPath := meta.Workdir().MountPath

	// Run git status/diff multiple times to detect corruption
	for i := 0; i < 10; i++ {
		// git status
		result, err := mgr.Runtime().Exec(ctx, store.InstanceName("", sandboxName),
			[]string{"git", "-C", vmLocalPath, "status"}, "admin")
		require.NoError(t, err, "git status iteration %d", i)
		assert.Equal(t, 0, result.ExitCode, "git status should succeed iteration %d", i)
		assert.NotContains(t, result.Stdout, "corrupt", "git should not detect corruption iteration %d", i)

		// git diff
		result, err = mgr.Runtime().Exec(ctx, store.InstanceName("", sandboxName),
			[]string{"git", "-C", vmLocalPath, "diff"}, "admin")
		require.NoError(t, err, "git diff iteration %d", i)
		assert.Equal(t, 0, result.ExitCode, "git diff should succeed iteration %d", i)
	}

	// Reset and verify git still works
	_, resetErr := resetSandbox(ctx, mgr, sandbox.ResetOptions{Name: sandboxName})
	require.NoError(t, resetErr)
	testutil.WaitForActive(ctx, t, mgr.Runtime(), store.InstanceName("", sandboxName), 90*time.Second)

	// Verify git operations work after reset
	result, err := mgr.Runtime().Exec(ctx, store.InstanceName("", sandboxName),
		[]string{"git", "-C", vmLocalPath, "status"}, "admin")
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.NotContains(t, result.Stdout, "corrupt")

	// Run diff/apply cycle after reset
	modifyCmd := []string{"bash", "-c",
		"cd " + vmLocalPath + " && echo 'new content' > test.txt && git add test.txt"}
	result, err = mgr.Runtime().Exec(ctx, store.InstanceName("", sandboxName), modifyCmd, "admin")
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)

	diffResult, err := patch.GenerateDiff(ctx, patch.DiffOptions{Name: sandboxName, Layout: mgr.Layout(), Runtime: mgr.Runtime()})
	require.NoError(t, err)
	assert.NotEmpty(t, diffResult)
	assert.Contains(t, diffResult, "test.txt")
}

// TestIntegrationTart_VMLocalStorageVerification verifies work directory is on local VM storage, not VirtioFS.
func TestIntegrationTart_VMLocalStorageVerification(t *testing.T) {
	mgr, ctx := tartIntegrationSetup(t)
	if mgr == nil {
		return // skipped
	}

	projectDir := testutil.GoProject(t)

	sandboxName := "tart-vmlocal"
	_, err := createSandbox(ctx, mgr, sandbox.CreateOptions{
		Name:    sandboxName,
		Workdir: sandbox.DirSpec{Path: projectDir},
		Agent:   "test",
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(ctx, mgr, sandboxName) }) //nolint:errcheck // test cleanup

	meta, err := store.LoadEnvironment(mgr.Layout().SandboxDir(sandboxName))
	require.NoError(t, err)

	// Verify mount path is VM-local, not VirtioFS
	assert.Contains(t, meta.Workdir().MountPath, "/Users/admin/yoloai-work/",
		"Tart work dir should be on VM local storage")
	assert.NotContains(t, meta.Workdir().MountPath, "/Volumes/My Shared Files",
		"Tart work dir should not be on VirtioFS")

	// Start VM and verify directory exists on local storage
	_, startErr := startSandbox(ctx, mgr, sandboxName, sandbox.StartOptions{})
	require.NoError(t, startErr)
	testutil.WaitForActive(ctx, t, mgr.Runtime(), store.InstanceName("", sandboxName), 90*time.Second)

	// Reload meta — Start() populates BaselineSHA (VM work dir setup runs inside VM)
	meta, err = store.LoadEnvironment(mgr.Layout().SandboxDir(sandboxName))
	require.NoError(t, err)

	// Check that work directory is a real directory (not a symlink to VirtioFS)
	result, err := mgr.Runtime().Exec(ctx, store.InstanceName("", sandboxName),
		[]string{"test", "-d", meta.Workdir().MountPath}, "admin")
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode, "work dir should exist on VM")

	// Verify it's not a symlink. test -L exits 1 when path is not a symlink,
	// so err is an *ExecError here — use the exit code directly.
	result, _ = mgr.Runtime().Exec(ctx, store.InstanceName("", sandboxName),
		[]string{"test", "-L", meta.Workdir().MountPath}, "admin")
	assert.NotEqual(t, 0, result.ExitCode, "work dir should not be a symlink")

	// Verify baseline SHA was created
	assert.NotEmpty(t, meta.Workdir().BaselineSHA, "baseline SHA should be set after VM setup")

	// Verify the baseline commit exists in git history
	result, err = mgr.Runtime().Exec(ctx, store.InstanceName("", sandboxName),
		[]string{"git", "-C", meta.Workdir().MountPath, "log", "--oneline"}, "admin")
	require.NoError(t, err)
	assert.Contains(t, result.Stdout, "baseline", "git history should contain baseline commit")
}

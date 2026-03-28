//go:build integration

package sandbox

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kstenerud/yoloai/internal/testutil"
	tartrt "github.com/kstenerud/yoloai/runtime/tart"
	"github.com/kstenerud/yoloai/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tartIntegrationSetup creates a Tart-based test environment.
// Returns nil if Tart is not available (caller should skip test).
// These tests are currently experimental and disabled by default.
// Set YOLOAI_TEST_TART=1 to enable them.
func tartIntegrationSetup(t *testing.T) (*Manager, context.Context) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Tart integration test in short mode")
	}

	// Skip Tart tests by default - they're experimental
	// Known issue: Tart workdir symlink creation fails for temp directories
	// TODO: Fix Tart runtime to skip symlink creation for :copy workdirs
	if os.Getenv("YOLOAI_TEST_TART") != "1" {
		t.Skip("skipping Tart integration test (set YOLOAI_TEST_TART=1 to enable)")
	}

	ctx := context.Background()

	testutil.IsolatedHome(t)

	rt, err := tartrt.New(ctx)
	if err != nil {
		t.Skipf("Tart not available: %v", err)
		return nil, nil
	}
	t.Cleanup(func() { rt.Close() }) //nolint:errcheck // test cleanup

	mgr := NewManager(rt, slog.Default(), strings.NewReader(""), io.Discard)
	require.NoError(t, mgr.EnsureSetup(ctx))

	return mgr, ctx
}

// TestIntegrationTart_FullLifecycle tests the complete create → modify → diff → apply → reset cycle
// with Tart VM and VM-local work directories.
func TestIntegrationTart_FullLifecycle(t *testing.T) {
	mgr, ctx := tartIntegrationSetup(t)
	if mgr == nil {
		return // skipped
	}

	projectDir := createProjectDir(t)

	// Create sandbox (starts VM)
	sandboxName := "tart-lifecycle"
	_, err := mgr.Create(ctx, CreateOptions{
		Name:    sandboxName,
		Workdir: DirSpec{Path: projectDir},
		Agent:   "test",
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Destroy(ctx, sandboxName) }) //nolint:errcheck // test cleanup

	// Wait for VM to become active
	testutil.WaitForActive(ctx, t, mgr.runtime, InstanceName(sandboxName), 30*time.Second)

	// Verify sandbox directory structure
	sandboxDir := Dir(sandboxName)
	assert.DirExists(t, sandboxDir)

	meta, err := LoadMeta(sandboxDir)
	require.NoError(t, err)
	assert.Equal(t, sandboxName, meta.Name)
	assert.Equal(t, "test", meta.Agent)
	assert.Equal(t, "tart", meta.Backend)
	assert.Equal(t, "copy", meta.Workdir.Mode)
	assert.NotEmpty(t, meta.Workdir.BaselineSHA, "baseline SHA should be set after VM work dir setup")

	// Verify work directory path is VM-local (not VirtioFS)
	vmLocalPath := mgr.runtime.ResolveCopyMount(sandboxName, projectDir)
	assert.Contains(t, vmLocalPath, "/Users/admin/yoloai-work/", "work dir should be on VM local storage")
	assert.Equal(t, vmLocalPath, meta.Workdir.MountPath)

	// Verify VM is running
	status, err := DetectStatus(ctx, mgr.runtime, InstanceName(sandboxName), Dir(sandboxName))
	require.NoError(t, err)
	assert.Equal(t, StatusActive, status)

	// Exec inside running VM to verify it's functional
	result, err := mgr.runtime.Exec(ctx, InstanceName(sandboxName), []string{"echo", "vm-test"}, "admin")
	require.NoError(t, err)
	assert.Equal(t, "vm-test", result.Stdout)
	assert.Equal(t, 0, result.ExitCode)

	// Verify git is functional inside VM work directory
	result, err = mgr.runtime.Exec(ctx, InstanceName(sandboxName),
		[]string{"git", "-C", vmLocalPath, "status", "--short"}, "admin")
	require.NoError(t, err)
	assert.Empty(t, result.Stdout, "work dir should be clean after setup")
	assert.Equal(t, 0, result.ExitCode)

	// Modify a file inside the VM (simulating agent work)
	modifyCmd := []string{"bash", "-c",
		"cd " + vmLocalPath + " && echo 'package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"modified\") }' > main.go"}
	result, err = mgr.runtime.Exec(ctx, InstanceName(sandboxName), modifyCmd, "admin")
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)

	// Verify git detects the change
	result, err = mgr.runtime.Exec(ctx, InstanceName(sandboxName),
		[]string{"git", "-C", vmLocalPath, "status", "--short"}, "admin")
	require.NoError(t, err)
	assert.Contains(t, result.Stdout, "main.go", "git should detect modified file")

	// Generate diff (should use VM-exec path for Tart)
	diffResult, err := GenerateDiff(ctx, DiffOptions{Name: sandboxName})
	require.NoError(t, err)
	assert.False(t, diffResult.Empty, "diff should not be empty after modification")
	assert.Contains(t, diffResult.Output, "fmt.Println", "diff should contain modification")

	// Stop VM and verify
	require.NoError(t, mgr.Stop(ctx, sandboxName))

	status, err = DetectStatus(ctx, mgr.runtime, InstanceName(sandboxName), Dir(sandboxName))
	require.NoError(t, err)
	assert.Equal(t, StatusStopped, status)

	// Restart VM and verify
	require.NoError(t, mgr.Start(ctx, sandboxName, StartOptions{}))
	testutil.WaitForActive(ctx, t, mgr.runtime, InstanceName(sandboxName), 30*time.Second)

	status, err = DetectStatus(ctx, mgr.runtime, InstanceName(sandboxName), Dir(sandboxName))
	require.NoError(t, err)
	assert.Equal(t, StatusActive, status)

	// Verify change persists after restart (VM local storage)
	result, err = mgr.runtime.Exec(ctx, InstanceName(sandboxName),
		[]string{"git", "-C", vmLocalPath, "status", "--short"}, "admin")
	require.NoError(t, err)
	assert.Contains(t, result.Stdout, "main.go", "changes should persist in VM local storage")

	// Generate patch and apply to a target directory
	patch, stat, err := GeneratePatch(sandboxName, nil)
	require.NoError(t, err)
	assert.NotEmpty(t, patch)
	assert.Contains(t, stat, "main.go")

	targetDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(targetDir, "main.go"),
		[]byte("package main\n\nfunc main() {}\n"),
		0600,
	))

	require.NoError(t, workspace.ApplyPatch(patch, targetDir, false))

	applied, err := os.ReadFile(filepath.Join(targetDir, "main.go")) //nolint:gosec // G304: test file path
	require.NoError(t, err)
	assert.Contains(t, string(applied), "fmt.Println")

	// Reset should restore clean state
	require.NoError(t, mgr.Reset(ctx, ResetOptions{Name: sandboxName}))

	// Wait for VM to be active again after reset
	testutil.WaitForActive(ctx, t, mgr.runtime, InstanceName(sandboxName), 30*time.Second)

	// Verify work directory is clean after reset
	result, err = mgr.runtime.Exec(ctx, InstanceName(sandboxName),
		[]string{"git", "-C", vmLocalPath, "status", "--short"}, "admin")
	require.NoError(t, err)
	assert.Empty(t, result.Stdout, "work dir should be clean after reset")

	// Destroy
	require.NoError(t, mgr.Destroy(ctx, sandboxName))
	assert.NoDirExists(t, sandboxDir)

	// VM should be gone
	status, err = DetectStatus(ctx, mgr.runtime, InstanceName(sandboxName), Dir(sandboxName))
	require.NoError(t, err)
	assert.Equal(t, StatusRemoved, status)
}

// TestIntegrationTart_MultipleAuxDirs tests Tart with multiple :copy auxiliary directories.
func TestIntegrationTart_MultipleAuxDirs(t *testing.T) {
	mgr, ctx := tartIntegrationSetup(t)
	if mgr == nil {
		return // skipped
	}

	projectDir := createProjectDir(t)
	auxDir1 := createAuxDir(t, "libs")
	auxDir2 := createAuxDir(t, "data")

	sandboxName := "tart-multiaux"
	_, err := mgr.Create(ctx, CreateOptions{
		Name:    sandboxName,
		Workdir: DirSpec{Path: projectDir},
		Agent:   "test",
		AuxDirs: []DirSpec{
			{Path: auxDir1, Mode: DirModeCopy},
			{Path: auxDir2, Mode: DirModeCopy},
		},
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Destroy(ctx, sandboxName) }) //nolint:errcheck // test cleanup

	// Wait for VM to become active
	testutil.WaitForActive(ctx, t, mgr.runtime, InstanceName(sandboxName), 30*time.Second)

	meta, err := LoadMeta(Dir(sandboxName))
	require.NoError(t, err)
	require.Len(t, meta.Directories, 2, "should have two aux directories")

	// Verify both aux directories are set up
	for i, dir := range meta.Directories {
		assert.Equal(t, "copy", dir.Mode)
		assert.NotEmpty(t, dir.BaselineSHA, "aux dir %d should have baseline SHA", i)

		// Verify aux directory is accessible in VM
		result, err := mgr.runtime.Exec(ctx, InstanceName(sandboxName),
			[]string{"test", "-f", filepath.Join(dir.MountPath, "data.txt")}, "admin")
		require.NoError(t, err)
		assert.Equal(t, 0, result.ExitCode, "aux dir %d should be accessible in VM", i)
	}

	// Modify both aux directories
	for i, dir := range meta.Directories {
		modifyCmd := []string{"bash", "-c",
			"echo 'modified' >> " + filepath.Join(dir.MountPath, "data.txt")}
		result, err := mgr.runtime.Exec(ctx, InstanceName(sandboxName), modifyCmd, "admin")
		require.NoError(t, err)
		assert.Equal(t, 0, result.ExitCode, "should modify aux dir %d", i)
	}

	// Generate diff (should include changes from all directories)
	diffResult, err := GenerateDiff(ctx, DiffOptions{Name: sandboxName})
	require.NoError(t, err)
	assert.False(t, diffResult.Empty, "diff should detect changes in aux directories")
}

// TestIntegrationTart_GitCorruption runs repeated git operations to ensure no corruption.
func TestIntegrationTart_GitCorruption(t *testing.T) {
	mgr, ctx := tartIntegrationSetup(t)
	if mgr == nil {
		return // skipped
	}

	projectDir := createProjectDir(t)

	sandboxName := "tart-gitcorruption"
	_, err := mgr.Create(ctx, CreateOptions{
		Name:    sandboxName,
		Workdir: DirSpec{Path: projectDir},
		Agent:   "test",
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Destroy(ctx, sandboxName) }) //nolint:errcheck // test cleanup

	testutil.WaitForActive(ctx, t, mgr.runtime, InstanceName(sandboxName), 30*time.Second)

	meta, err := LoadMeta(Dir(sandboxName))
	require.NoError(t, err)
	vmLocalPath := meta.Workdir.MountPath

	// Run git status/diff multiple times to detect corruption
	for i := 0; i < 10; i++ {
		// git status
		result, err := mgr.runtime.Exec(ctx, InstanceName(sandboxName),
			[]string{"git", "-C", vmLocalPath, "status"}, "admin")
		require.NoError(t, err, "git status iteration %d", i)
		assert.Equal(t, 0, result.ExitCode, "git status should succeed iteration %d", i)
		assert.NotContains(t, result.Stdout, "corrupt", "git should not detect corruption iteration %d", i)

		// git diff
		result, err = mgr.runtime.Exec(ctx, InstanceName(sandboxName),
			[]string{"git", "-C", vmLocalPath, "diff"}, "admin")
		require.NoError(t, err, "git diff iteration %d", i)
		assert.Equal(t, 0, result.ExitCode, "git diff should succeed iteration %d", i)
	}

	// Reset and verify git still works
	require.NoError(t, mgr.Reset(ctx, ResetOptions{Name: sandboxName}))
	testutil.WaitForActive(ctx, t, mgr.runtime, InstanceName(sandboxName), 30*time.Second)

	// Verify git operations work after reset
	result, err := mgr.runtime.Exec(ctx, InstanceName(sandboxName),
		[]string{"git", "-C", vmLocalPath, "status"}, "admin")
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.NotContains(t, result.Stdout, "corrupt")

	// Run diff/apply cycle after reset
	modifyCmd := []string{"bash", "-c",
		"cd " + vmLocalPath + " && echo 'new content' > test.txt && git add test.txt"}
	result, err = mgr.runtime.Exec(ctx, InstanceName(sandboxName), modifyCmd, "admin")
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)

	diffResult, err := GenerateDiff(ctx, DiffOptions{Name: sandboxName})
	require.NoError(t, err)
	assert.False(t, diffResult.Empty)
	assert.Contains(t, diffResult.Output, "test.txt")
}

// TestIntegrationTart_VMLocalStorageVerification verifies work directory is on local VM storage, not VirtioFS.
func TestIntegrationTart_VMLocalStorageVerification(t *testing.T) {
	mgr, ctx := tartIntegrationSetup(t)
	if mgr == nil {
		return // skipped
	}

	projectDir := createProjectDir(t)

	sandboxName := "tart-vmlocal"
	_, err := mgr.Create(ctx, CreateOptions{
		Name:    sandboxName,
		Workdir: DirSpec{Path: projectDir},
		Agent:   "test",
		NoStart: true, // Create but don't start
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Destroy(ctx, sandboxName) }) //nolint:errcheck // test cleanup

	meta, err := LoadMeta(Dir(sandboxName))
	require.NoError(t, err)

	// Verify mount path is VM-local, not VirtioFS
	assert.Contains(t, meta.Workdir.MountPath, "/Users/admin/yoloai-work/",
		"Tart work dir should be on VM local storage")
	assert.NotContains(t, meta.Workdir.MountPath, "/Volumes/My Shared Files",
		"Tart work dir should not be on VirtioFS")

	// Start VM and verify directory exists on local storage
	require.NoError(t, mgr.Start(ctx, sandboxName, StartOptions{}))
	testutil.WaitForActive(ctx, t, mgr.runtime, InstanceName(sandboxName), 30*time.Second)

	// Check that work directory is a real directory (not a symlink to VirtioFS)
	result, err := mgr.runtime.Exec(ctx, InstanceName(sandboxName),
		[]string{"test", "-d", meta.Workdir.MountPath}, "admin")
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode, "work dir should exist on VM")

	// Verify it's not a symlink
	result, err = mgr.runtime.Exec(ctx, InstanceName(sandboxName),
		[]string{"test", "-L", meta.Workdir.MountPath}, "admin")
	require.NoError(t, err)
	assert.NotEqual(t, 0, result.ExitCode, "work dir should not be a symlink")

	// Verify baseline SHA was created
	assert.NotEmpty(t, meta.Workdir.BaselineSHA, "baseline SHA should be set after VM setup")

	// Verify the baseline commit exists in git history
	result, err = mgr.runtime.Exec(ctx, InstanceName(sandboxName),
		[]string{"git", "-C", meta.Workdir.MountPath, "log", "--oneline"}, "admin")
	require.NoError(t, err)
	assert.Contains(t, result.Stdout, "baseline", "git history should contain baseline commit")
}

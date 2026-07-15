//go:build integration

// ABOUTME: Integration tests for the Tart VM backend — full lifecycle, aux dirs,
// ABOUTME: git corruption resistance, and VM-local storage verification.
// ABOUTME: Guarded by YOLOAI_TEST_TART_VM=1 (the same gate as the tart conformance
// ABOUTME: suite) because they require Apple Silicon + tart and clone a multi-GB VM.

package orchestrator_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kstenerud/yoloai/copyflow"
	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/git"
	"github.com/kstenerud/yoloai/internal/orchestrator"
	"github.com/kstenerud/yoloai/internal/orchestrator/agentcfg"
	"github.com/kstenerud/yoloai/internal/testutil"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/runtime/tart"
	"github.com/kstenerud/yoloai/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tartIntegrationSetup creates a Tart-based test environment.
// Returns nil if Tart is not available (caller should skip test).
// Set YOLOAI_TEST_TART_VM=1 to enable them; `make releasetest` sets it.
func tartIntegrationSetup(t *testing.T) (*orchestrator.Engine, context.Context) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Tart integration test in short mode")
	}

	// Gated behind YOLOAI_TEST_TART_VM=1 — the SAME variable as the tart conformance
	// suite in runtime/tart, because each test clones+boots a multi-GB macOS VM.
	// It used to be YOLOAI_TEST_TART (no _VM), which nothing set:
	// releasetest exported only YOLOAI_TEST_TART_VM, so this whole lifecycle tier silently
	// self-skipped on the one platform that can run it, while the identically-named-looking
	// conformance suite ran and made the tier look covered. D112's own plan quoted this
	// line in its keep-list of scope gates and never noticed the two names differed.
	// (The old "workdir symlink fails for temp dirs" note was DF27 —
	// verified stale; the :copy path works. Runtime-level coverage now lives in
	// runtime/tart TestTartConformance.)
	if os.Getenv("YOLOAI_TEST_TART_VM") != "1" {
		t.Skip("skipping Tart integration test (set YOLOAI_TEST_TART_VM=1 to enable)")
	}

	ctx := context.Background()

	// Isolate yoloai's state by path and this test's VMs by principal, but share
	// the real ~/.tart store — see testutil.TartStoreLayout. Using IsolatedHome
	// here instead points TART_HOME at an empty store, and each test then spends
	// ~35 minutes re-downloading the ~30 GB base image before it does any work
	// (DF19).
	layout := testutil.TartStoreLayout(t)

	rt, err := tart.New(ctx, layout)
	if err != nil {
		t.Skipf("Tart not available: %v", err)
		return nil, nil
	}
	t.Cleanup(func() { rt.Close() }) //nolint:errcheck // test cleanup

	// Reap before provisioning, never after: an earlier run that died on a
	// timeout skipped its t.Cleanup and left a VM standing under this exact
	// principal, which Start would otherwise adopt as "already running" — running
	// the test against a VM it never built (DF110). Scoped to this test's
	// t000NNNN namespace, so the developer's real VMs are untouchable.
	testutil.ReapLeakedInstances(ctx, t, rt)

	mgr := orchestrator.NewEngineWithRuntime(rt, slog.Default(), strings.NewReader(""), orchestrator.WithLayout(layout))

	// Pre-seed the provision checksum, exactly as the docker tier does in
	// integration_main_test.go, and for exactly the same reason. It is the seed —
	// not the pre-built base — that makes the EnsureSetup below cheap: needsBuild
	// reads its checksum from layout.CacheDir(), which lives under the isolated
	// temp DataDir, so the record is never there and it reports stale no matter
	// how fresh ~/.tart is. Unseeded, every test re-clones and re-provisions a
	// ~29 GB VM in the developer's real ~/.tart. `make integration` builds the
	// base first (tart-base-image), which is what makes "trust it" true. The seed
	// cannot mask an absent base: needsBuild checks existence before the checksum.
	if err := os.MkdirAll(layout.CacheDir(), 0750); err != nil { //nolint:forbidigo // test-edge dir create; fileutil's sudo chown is irrelevant here
		t.Fatalf("create cache dir: %v", err)
	}
	rt.RecordBuildChecksum(layout.ProfileDir("base"))

	// Setup's output goes to the test log, not io.Discard. It is the only thing
	// that reports a base-image pull ("This is a one-time download (~30 GB)"),
	// and discarding it is what hid DF19 here for as long as it did: the suite
	// looked like it was hanging when it was actually downloading.
	require.NoError(t, mgr.EnsureSetup(ctx, testutil.LogWriter(t)))

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

	sandboxName := "tart-lifecycle"
	_, err := createSandbox(ctx, mgr, orchestrator.CreateOptions{
		Name:    sandboxName,
		Workdir: orchestrator.DirSpec{Path: projectDir},
		Agent:   "test",
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(ctx, mgr, sandboxName) }) //nolint:errcheck // test cleanup

	// Create only provisions; it does not boot the VM. This used to go straight
	// to WaitForActive on a VM nothing had started, so it could only ever time
	// out — invisible until DF94 wired the tier on. Start also populates the
	// workdir BaselineSHA asserted below, by running the work-dir setup inside
	// the VM.
	_, err = startSandbox(ctx, mgr, sandboxName, orchestrator.StartOptions{})
	require.NoError(t, err)
	testutil.WaitForActive(ctx, t, mgr.Runtime(), store.InstanceName(mgr.Layout().Principal, sandboxName), 90*time.Second)

	// Verify sandbox directory structure
	sandboxDir := mgr.Layout().SandboxDir(sandboxName)
	assert.DirExists(t, sandboxDir)

	meta, err := store.LoadEnvironment(sandboxDir)
	require.NoError(t, err)
	assert.Equal(t, sandboxName, meta.Name)
	acfg, err := agentcfg.Load(sandboxDir)
	require.NoError(t, err)
	assert.Equal(t, string(agent.AgentTest), acfg.AgentType)
	assert.Equal(t, runtime.BackendTart, meta.BackendType)
	assert.Equal(t, store.DirModeCopy, meta.Workdir().Mode)
	assert.NotEmpty(t, meta.Workdir().BaselineSHA, "baseline SHA should be set after VM work dir setup")

	// Verify work directory path is VM-local (not VirtioFS)
	vmLocalPath := runtime.ResolveCopyMountFor(mgr.Runtime(), sandboxName, projectDir)
	assert.Contains(t, vmLocalPath, "/Users/admin/yoloai-work/", "work dir should be on VM local storage")
	assert.Equal(t, vmLocalPath, meta.Workdir().MountPath)

	// Verify VM is running
	status, err := orchestrator.DetectStatus(ctx, mgr.Runtime(), store.InstanceName(mgr.Layout().Principal, sandboxName), mgr.Layout().SandboxDir(sandboxName))
	require.NoError(t, err)
	assert.Equal(t, orchestrator.StatusActive, status)

	// Exec inside running VM to verify it's functional
	result, err := mgr.Runtime().Exec(ctx, store.InstanceName(mgr.Layout().Principal, sandboxName), []string{"echo", "vm-test"}, "admin")
	require.NoError(t, err)
	assert.Equal(t, "vm-test", result.Stdout)
	assert.Equal(t, 0, result.ExitCode)

	// Verify git is functional inside VM work directory
	result, err = mgr.Runtime().Exec(ctx, store.InstanceName(mgr.Layout().Principal, sandboxName),
		[]string{"git", "-C", vmLocalPath, "status", "--short"}, "admin")
	require.NoError(t, err)
	assert.Empty(t, result.Stdout, "work dir should be clean after setup")
	assert.Equal(t, 0, result.ExitCode)

	// Modify a file inside the VM (simulating agent work)
	modifyCmd := []string{"bash", "-c",
		"cd " + vmLocalPath + " && echo 'package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"modified\") }' > main.go"}
	result, err = mgr.Runtime().Exec(ctx, store.InstanceName(mgr.Layout().Principal, sandboxName), modifyCmd, "admin")
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)

	// Verify git detects the change
	result, err = mgr.Runtime().Exec(ctx, store.InstanceName(mgr.Layout().Principal, sandboxName),
		[]string{"git", "-C", vmLocalPath, "status", "--short"}, "admin")
	require.NoError(t, err)
	assert.Contains(t, result.Stdout, "main.go", "git should detect modified file")

	// Generate diff (should use VM-exec path for Tart)
	diffResult, err := copyflow.GenerateDiff(ctx, copyflow.DiffOptions{Name: sandboxName, Layout: mgr.Layout(), Runtime: mgr.Runtime()})
	require.NoError(t, err)
	assert.NotEmpty(t, diffResult, "diff should not be empty after modification")
	assert.Contains(t, diffResult, "fmt.Println", "diff should contain modification")

	// Generate patch and apply to a target directory (while VM is still running)
	patchBytes, stat, err := copyflow.GeneratePatch(ctx, mgr.Layout(), mgr.Runtime(), sandboxName, "", nil, true)
	require.NoError(t, err)
	assert.NotEmpty(t, patchBytes)
	assert.Contains(t, stat, "main.go")

	targetDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(targetDir, "main.go"),
		[]byte("package main\n\nfunc main() {}\n"),
		0600,
	))

	require.NoError(t, git.NewTestHostWithEnv(testutil.GitEnv()).ApplyPatch(context.Background(), patchBytes, targetDir, false))

	applied, err := os.ReadFile(filepath.Join(targetDir, "main.go")) //nolint:gosec // G304: test file path
	require.NoError(t, err)
	assert.Contains(t, string(applied), "fmt.Println")

	// Stop hard-stops the VM; it does not suspend, so the status is "stopped".
	// This asserted StatusSuspended — a state tart cannot produce. Apple's
	// Virtualization.framework cannot restore a VM that had VirtioFS (--dir)
	// mounts from a suspend snapshot (VZErrorDomain Code=12), and every tart
	// sandbox has the yoloai share, so suspend-on-stop buys nothing and costs
	// 15-45s per call; tart.Runtime.Stop documents the choice. StatusSuspended
	// remains reachable only for a VM suspended out-of-band.
	require.NoError(t, stopSandbox(ctx, mgr, sandboxName))

	status, err = orchestrator.DetectStatus(ctx, mgr.Runtime(), store.InstanceName(mgr.Layout().Principal, sandboxName), mgr.Layout().SandboxDir(sandboxName))
	require.NoError(t, err)
	assert.Equal(t, orchestrator.StatusStopped, status)

	// Restart recreates the VM from staging rather than resuming (see above), so
	// the work dir comes back clean — which is what the check below pins.
	_, startErr := startSandbox(ctx, mgr, sandboxName, orchestrator.StartOptions{})
	require.NoError(t, startErr)
	testutil.WaitForActive(ctx, t, mgr.Runtime(), store.InstanceName(mgr.Layout().Principal, sandboxName), 90*time.Second)

	status, err = orchestrator.DetectStatus(ctx, mgr.Runtime(), store.InstanceName(mgr.Layout().Principal, sandboxName), mgr.Layout().SandboxDir(sandboxName))
	require.NoError(t, err)
	assert.True(t, status == orchestrator.StatusIdle || status == orchestrator.StatusActive,
		"VM should be running after start, got %s", status)

	// Reload vmLocalPath for the recreated VM (same path, but VM is fresh)
	result, err = mgr.Runtime().Exec(ctx, store.InstanceName(mgr.Layout().Principal, sandboxName),
		[]string{"git", "-C", vmLocalPath, "status", "--short"}, "admin")
	require.NoError(t, err)
	assert.Empty(t, result.Stdout, "work dir should be clean after recreate from staging")

	// Reset should restore clean state
	_, resetErr := resetSandbox(ctx, mgr, orchestrator.ResetOptions{Name: sandboxName})
	require.NoError(t, resetErr)

	// Wait for VM to be active again after reset
	testutil.WaitForActive(ctx, t, mgr.Runtime(), store.InstanceName(mgr.Layout().Principal, sandboxName), 90*time.Second)

	// Verify work directory is clean after reset
	result, err = mgr.Runtime().Exec(ctx, store.InstanceName(mgr.Layout().Principal, sandboxName),
		[]string{"git", "-C", vmLocalPath, "status", "--short"}, "admin")
	require.NoError(t, err)
	assert.Empty(t, result.Stdout, "work dir should be clean after reset")

	// Destroy
	_, destroyErr := destroySandbox(ctx, mgr, sandboxName)
	require.NoError(t, destroyErr)
	assert.NoDirExists(t, sandboxDir)

	// VM should be gone
	status, err = orchestrator.DetectStatus(ctx, mgr.Runtime(), store.InstanceName(mgr.Layout().Principal, sandboxName), mgr.Layout().SandboxDir(sandboxName))
	require.NoError(t, err)
	assert.Equal(t, orchestrator.StatusRemoved, status)
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
	_, err := createSandbox(ctx, mgr, orchestrator.CreateOptions{
		Name:    sandboxName,
		Workdir: orchestrator.DirSpec{Path: projectDir},
		Agent:   "test",
		AuxDirs: []orchestrator.DirSpec{
			{Path: auxDir1, Mode: orchestrator.DirModeRW},
			{Path: auxDir2, Mode: orchestrator.DirModeRW},
		},
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(ctx, mgr, sandboxName) }) //nolint:errcheck // test cleanup

	// Create provisions but does not boot; the aux mounts are only observable
	// from inside a running VM.
	_, err = startSandbox(ctx, mgr, sandboxName, orchestrator.StartOptions{})
	require.NoError(t, err)
	testutil.WaitForActive(ctx, t, mgr.Runtime(), store.InstanceName(mgr.Layout().Principal, sandboxName), 90*time.Second)

	meta, err := store.LoadEnvironment(mgr.Layout().SandboxDir(sandboxName))
	require.NoError(t, err)
	require.Len(t, meta.AuxDirs(), 2, "should have two aux directories")

	for i, dir := range meta.AuxDirs() {
		assert.Equal(t, store.DirModeRW, dir.Mode)
		// :rw is a live bind-mount; there's no baseline to capture.
		assert.Empty(t, dir.BaselineSHA, "aux dir %d should have no baseline (rw)", i)

		// Verify aux directory is accessible in VM
		result, err := mgr.Runtime().Exec(ctx, store.InstanceName(mgr.Layout().Principal, sandboxName),
			[]string{"test", "-f", filepath.Join(dir.MountPath, "data.txt")}, "admin")
		require.NoError(t, err)
		assert.Equal(t, 0, result.ExitCode, "aux dir %d should be accessible in VM", i)
	}

	// Modify both aux dirs from inside the VM — :rw means writes land
	// on the host directly, so this also exercises the bind-mount.
	for i, dir := range meta.AuxDirs() {
		modifyCmd := []string{"bash", "-c",
			"echo 'modified' >> " + filepath.Join(dir.MountPath, "data.txt")}
		result, err := mgr.Runtime().Exec(ctx, store.InstanceName(mgr.Layout().Principal, sandboxName), modifyCmd, "admin")
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
	_, err := createSandbox(ctx, mgr, orchestrator.CreateOptions{
		Name:    sandboxName,
		Workdir: orchestrator.DirSpec{Path: projectDir},
		Agent:   "test",
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(ctx, mgr, sandboxName) }) //nolint:errcheck // test cleanup

	// Create provisions but does not boot; the git operations below all run
	// inside the VM.
	_, err = startSandbox(ctx, mgr, sandboxName, orchestrator.StartOptions{})
	require.NoError(t, err)
	testutil.WaitForActive(ctx, t, mgr.Runtime(), store.InstanceName(mgr.Layout().Principal, sandboxName), 90*time.Second)

	meta, err := store.LoadEnvironment(mgr.Layout().SandboxDir(sandboxName))
	require.NoError(t, err)
	vmLocalPath := meta.Workdir().MountPath

	// Run git status/diff multiple times to detect corruption
	for i := 0; i < 10; i++ {
		// git status
		result, err := mgr.Runtime().Exec(ctx, store.InstanceName(mgr.Layout().Principal, sandboxName),
			[]string{"git", "-C", vmLocalPath, "status"}, "admin")
		require.NoError(t, err, "git status iteration %d", i)
		assert.Equal(t, 0, result.ExitCode, "git status should succeed iteration %d", i)
		assert.NotContains(t, result.Stdout, "corrupt", "git should not detect corruption iteration %d", i)

		// git diff
		result, err = mgr.Runtime().Exec(ctx, store.InstanceName(mgr.Layout().Principal, sandboxName),
			[]string{"git", "-C", vmLocalPath, "diff"}, "admin")
		require.NoError(t, err, "git diff iteration %d", i)
		assert.Equal(t, 0, result.ExitCode, "git diff should succeed iteration %d", i)
	}

	// Reset and verify git still works
	_, resetErr := resetSandbox(ctx, mgr, orchestrator.ResetOptions{Name: sandboxName})
	require.NoError(t, resetErr)
	testutil.WaitForActive(ctx, t, mgr.Runtime(), store.InstanceName(mgr.Layout().Principal, sandboxName), 90*time.Second)

	// Verify git operations work after reset
	result, err := mgr.Runtime().Exec(ctx, store.InstanceName(mgr.Layout().Principal, sandboxName),
		[]string{"git", "-C", vmLocalPath, "status"}, "admin")
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.NotContains(t, result.Stdout, "corrupt")

	// Run diff/apply cycle after reset
	modifyCmd := []string{"bash", "-c",
		"cd " + vmLocalPath + " && echo 'new content' > test.txt && git add test.txt"}
	result, err = mgr.Runtime().Exec(ctx, store.InstanceName(mgr.Layout().Principal, sandboxName), modifyCmd, "admin")
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)

	diffResult, err := copyflow.GenerateDiff(ctx, copyflow.DiffOptions{Name: sandboxName, Layout: mgr.Layout(), Runtime: mgr.Runtime()})
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
	_, err := createSandbox(ctx, mgr, orchestrator.CreateOptions{
		Name:    sandboxName,
		Workdir: orchestrator.DirSpec{Path: projectDir},
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
	_, startErr := startSandbox(ctx, mgr, sandboxName, orchestrator.StartOptions{})
	require.NoError(t, startErr)
	testutil.WaitForActive(ctx, t, mgr.Runtime(), store.InstanceName(mgr.Layout().Principal, sandboxName), 90*time.Second)

	// Reload meta — Start() populates BaselineSHA (VM work dir setup runs inside VM)
	meta, err = store.LoadEnvironment(mgr.Layout().SandboxDir(sandboxName))
	require.NoError(t, err)

	// Check that work directory is a real directory (not a symlink to VirtioFS)
	result, err := mgr.Runtime().Exec(ctx, store.InstanceName(mgr.Layout().Principal, sandboxName),
		[]string{"test", "-d", meta.Workdir().MountPath}, "admin")
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode, "work dir should exist on VM")

	// Verify it's not a symlink. test -L exits 1 when path is not a symlink,
	// so err is an *ExecError here — use the exit code directly.
	result, _ = mgr.Runtime().Exec(ctx, store.InstanceName(mgr.Layout().Principal, sandboxName),
		[]string{"test", "-L", meta.Workdir().MountPath}, "admin")
	assert.NotEqual(t, 0, result.ExitCode, "work dir should not be a symlink")

	// Verify baseline SHA was created
	assert.NotEmpty(t, meta.Workdir().BaselineSHA, "baseline SHA should be set after VM setup")

	// Verify the baseline commit exists in git history
	result, err = mgr.Runtime().Exec(ctx, store.InstanceName(mgr.Layout().Principal, sandboxName),
		[]string{"git", "-C", meta.Workdir().MountPath, "log", "--oneline"}, "admin")
	require.NoError(t, err)
	assert.Contains(t, result.Stdout, "baseline", "git history should contain baseline commit")
}

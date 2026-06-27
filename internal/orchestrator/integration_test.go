//go:build integration

package orchestrator_test

import (
	"context"
	"encoding/json"
	"fmt"
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
	"github.com/kstenerud/yoloai/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIntegration_FullLifecycle(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	// Create sandbox (starts container)
	sandboxName := "integ-test"
	_, err := createSandbox(ctx, mgr, orchestrator.CreateOptions{
		Name:    sandboxName,
		Workdir: orchestrator.DirSpec{Path: projectDir},
		Agent:   "test",
		Version: "test",
	})
	require.NoError(t, err)

	_, err = startSandbox(ctx, mgr, sandboxName, orchestrator.StartOptions{})
	require.NoError(t, err)

	// Wait for container to become active
	testutil.WaitForActive(ctx, t, mgr.Runtime(), store.InstanceName("", sandboxName), 15*time.Second)

	// Verify directory structure
	sandboxDir := mgr.Layout().SandboxDir(sandboxName)
	assert.DirExists(t, sandboxDir)

	meta, err := store.LoadEnvironment(sandboxDir)
	require.NoError(t, err)
	assert.Equal(t, sandboxName, meta.Name)
	acfg, err := agentcfg.Load(sandboxDir)
	require.NoError(t, err)
	assert.Equal(t, string(agent.AgentTest), acfg.AgentType)
	assert.Equal(t, store.DirModeCopy, meta.Workdir().Mode)
	assert.NotEmpty(t, meta.Workdir().BaselineSHA)

	workDir := store.WorkDir(mgr.Layout().SandboxDir(sandboxName), meta.Workdir().HostPath)
	assert.FileExists(t, filepath.Join(workDir, "main.go"))

	// Verify container is running
	status, err := orchestrator.DetectStatus(ctx, mgr.Runtime(), store.InstanceName("", sandboxName), mgr.Layout().SandboxDir(sandboxName))
	require.NoError(t, err)
	assert.Equal(t, orchestrator.StatusActive, status)

	// Exec inside running container
	result, err := mgr.Runtime().Exec(ctx, store.InstanceName("", sandboxName), []string{"echo", "lifecycle-test"}, "yoloai")
	require.NoError(t, err)
	assert.Equal(t, "lifecycle-test", result.Stdout)
	assert.Equal(t, 0, result.ExitCode)

	// Modify work copy and verify diff
	require.NoError(t, os.WriteFile(
		filepath.Join(workDir, "main.go"),
		[]byte("package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"hello\") }\n"),
		0600,
	))

	diffResult, err := copyflow.GenerateDiff(ctx, copyflow.DiffOptions{Name: sandboxName, Layout: mgr.Layout(), Runtime: mgr.Runtime()})
	require.NoError(t, err)
	assert.NotEmpty(t, diffResult)
	assert.Contains(t, diffResult, "fmt")

	// Stop container and verify
	require.NoError(t, stopSandbox(ctx, mgr, sandboxName))

	status, err = orchestrator.DetectStatus(ctx, mgr.Runtime(), store.InstanceName("", sandboxName), mgr.Layout().SandboxDir(sandboxName))
	require.NoError(t, err)
	assert.Equal(t, orchestrator.StatusStopped, status)

	// Restart container and verify
	_, startErr := startSandbox(ctx, mgr, sandboxName, orchestrator.StartOptions{})
	require.NoError(t, startErr)
	testutil.WaitForActive(ctx, t, mgr.Runtime(), store.InstanceName("", sandboxName), 15*time.Second)

	status, err = orchestrator.DetectStatus(ctx, mgr.Runtime(), store.InstanceName("", sandboxName), mgr.Layout().SandboxDir(sandboxName))
	require.NoError(t, err)
	assert.Equal(t, orchestrator.StatusActive, status)

	// Exec again after restart
	result, err = mgr.Runtime().Exec(ctx, store.InstanceName("", sandboxName), []string{"echo", "after-restart"}, "yoloai")
	require.NoError(t, err)
	assert.Equal(t, "after-restart", result.Stdout)
	assert.Equal(t, 0, result.ExitCode)

	// Generate patch and apply to a target directory
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

	// Destroy
	_, destroyErr := destroySandbox(ctx, mgr, sandboxName)
	require.NoError(t, destroyErr)
	assert.NoDirExists(t, sandboxDir)

	// Container should be gone
	status, err = orchestrator.DetectStatus(ctx, mgr.Runtime(), store.InstanceName("", sandboxName), mgr.Layout().SandboxDir(sandboxName))
	require.NoError(t, err)
	assert.Equal(t, orchestrator.StatusRemoved, status)
}

func TestIntegration_CreateNoStart(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := createSandbox(ctx, mgr, orchestrator.CreateOptions{
		Name:    "nostart",
		Workdir: orchestrator.DirSpec{Path: projectDir},
		Agent:   "test",
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(ctx, mgr, "nostart") }) //nolint:errcheck // test cleanup

	sandboxDir := mgr.Layout().SandboxDir("nostart")
	assert.DirExists(t, sandboxDir)

	meta, err := store.LoadEnvironment(sandboxDir)
	require.NoError(t, err)
	assert.Equal(t, "nostart", meta.Name)
	acfg, err := agentcfg.Load(sandboxDir)
	require.NoError(t, err)
	assert.Equal(t, string(agent.AgentTest), acfg.AgentType)
	assert.Equal(t, store.DirModeCopy, meta.Workdir().Mode)
	assert.NotEmpty(t, meta.Workdir().BaselineSHA)

	// Verify work copy contains our file
	workDir := store.WorkDir(mgr.Layout().SandboxDir("nostart"), meta.Workdir().HostPath)
	assert.FileExists(t, filepath.Join(workDir, "main.go"))

	// Verify standard subdirs
	assert.DirExists(t, filepath.Join(sandboxDir, store.AgentRuntimeDir))
	assert.FileExists(t, filepath.Join(sandboxDir, store.EnvironmentFile))
	assert.FileExists(t, filepath.Join(sandboxDir, store.RuntimeConfigFile))
}

func TestIntegration_CopyMode(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := createSandbox(ctx, mgr, orchestrator.CreateOptions{
		Name:    "copymode",
		Workdir: orchestrator.DirSpec{Path: projectDir, Mode: orchestrator.DirModeCopy},
		Agent:   "test",
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(ctx, mgr, "copymode") }) //nolint:errcheck // test cleanup

	meta, err := store.LoadEnvironment(mgr.Layout().SandboxDir("copymode"))
	require.NoError(t, err)
	assert.Equal(t, store.DirModeCopy, meta.Workdir().Mode)

	workDir := store.WorkDir(mgr.Layout().SandboxDir("copymode"), meta.Workdir().HostPath)

	// Modify work copy
	require.NoError(t, os.WriteFile(
		filepath.Join(workDir, "main.go"),
		[]byte("package main\n\n// modified\nfunc main() {}\n"),
		0600,
	))

	// Original should be unchanged
	original, err := os.ReadFile(filepath.Join(projectDir, "main.go")) //nolint:gosec // test path
	require.NoError(t, err)
	assert.NotContains(t, string(original), "modified")
}

func TestIntegration_RWMode(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := createSandbox(ctx, mgr, orchestrator.CreateOptions{
		Name:    "rwmode",
		Workdir: orchestrator.DirSpec{Path: projectDir, Mode: orchestrator.DirModeRW},
		Agent:   "test",
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(ctx, mgr, "rwmode") }) //nolint:errcheck // test cleanup

	meta, err := store.LoadEnvironment(mgr.Layout().SandboxDir("rwmode"))
	require.NoError(t, err)
	assert.Equal(t, store.DirModeRW, meta.Workdir().Mode)
}

// D81 (multi-workdir Phase 2): aux :copy is now accepted. The library creates
// a host-side copy and records a baseline SHA in environment.json, just as it
// does for the workdir.
func TestIntegration_AuxDirCopy_AcceptedByLibrary(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)
	auxDir := createAuxDir(t, "libs")

	_, err := createSandbox(ctx, mgr, orchestrator.CreateOptions{
		Name:    "auxcopy-accepted",
		Workdir: orchestrator.DirSpec{Path: projectDir},
		Agent:   "test",
		AuxDirs: []orchestrator.DirSpec{{Path: auxDir, Mode: orchestrator.DirModeCopy}},
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(ctx, mgr, "auxcopy-accepted") }) //nolint:errcheck // test cleanup

	meta, err := store.LoadEnvironment(mgr.Layout().SandboxDir("auxcopy-accepted"))
	require.NoError(t, err)
	require.Len(t, meta.AuxDirs(), 1)
	assert.Equal(t, store.DirModeCopy, meta.AuxDirs()[0].Mode)
	assert.NotEmpty(t, meta.AuxDirs()[0].BaselineSHA, "aux :copy dir must have a baseline SHA")
}

// Aux :rw is the still-supported writable aux mode after Q-U. The
// kernel-side mount semantics are exercised by TestIntegration_*
// elsewhere; this test just regress-guards that Create accepts the
// mode and writes it through to meta.
func TestIntegration_AuxDirRW(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)
	auxDir := createAuxDir(t, "writable-lib")

	_, err := createSandbox(ctx, mgr, orchestrator.CreateOptions{
		Name:    "auxrw",
		Workdir: orchestrator.DirSpec{Path: projectDir},
		Agent:   "test",
		AuxDirs: []orchestrator.DirSpec{{Path: auxDir, Mode: orchestrator.DirModeRW}},
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(ctx, mgr, "auxrw") }) //nolint:errcheck // test cleanup

	meta, err := store.LoadEnvironment(mgr.Layout().SandboxDir("auxrw"))
	require.NoError(t, err)
	require.Len(t, meta.AuxDirs(), 1)
	assert.Equal(t, store.DirModeRW, meta.AuxDirs()[0].Mode)
}

func TestIntegration_AuxDirRO(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)
	auxDir := createAuxDir(t, "readonly-lib")

	_, err := createSandbox(ctx, mgr, orchestrator.CreateOptions{
		Name:    "auxro",
		Workdir: orchestrator.DirSpec{Path: projectDir},
		Agent:   "test",
		AuxDirs: []orchestrator.DirSpec{{Path: auxDir}},
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(ctx, mgr, "auxro") }) //nolint:errcheck // test cleanup

	meta, err := store.LoadEnvironment(mgr.Layout().SandboxDir("auxro"))
	require.NoError(t, err)
	require.Len(t, meta.AuxDirs(), 1)
	assert.Equal(t, store.DirModeRO, meta.AuxDirs()[0].Mode)
}

func TestIntegration_Replace(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	// Create first sandbox
	_, err := createSandbox(ctx, mgr, orchestrator.CreateOptions{
		Name:    "replaceme",
		Workdir: orchestrator.DirSpec{Path: projectDir},
		Agent:   "test",
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(ctx, mgr, "replaceme") }) //nolint:errcheck // test cleanup

	// Replace with new sandbox
	_, err = createSandbox(ctx, mgr, orchestrator.CreateOptions{
		Name:    "replaceme",
		Workdir: orchestrator.DirSpec{Path: projectDir},
		Agent:   "test",
		Replace: true,
		Version: "test",
	})
	require.NoError(t, err)

	// Should still exist with valid meta
	meta, err := store.LoadEnvironment(mgr.Layout().SandboxDir("replaceme"))
	require.NoError(t, err)
	assert.Equal(t, "replaceme", meta.Name)
}

func TestIntegration_Reset(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	// Create and start the sandbox (Reset requires a restart cycle)
	_, err := createSandbox(ctx, mgr, orchestrator.CreateOptions{
		Name:    "resettest",
		Workdir: orchestrator.DirSpec{Path: projectDir},
		Agent:   "test",
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(ctx, mgr, "resettest") }) //nolint:errcheck // test cleanup

	_, err = startSandbox(ctx, mgr, "resettest", orchestrator.StartOptions{})
	require.NoError(t, err)

	// Wait for container to become active
	testutil.WaitForActive(ctx, t, mgr.Runtime(), store.InstanceName("", "resettest"), 15*time.Second)

	meta, err := store.LoadEnvironment(mgr.Layout().SandboxDir("resettest"))
	require.NoError(t, err)
	workDir := store.WorkDir(mgr.Layout().SandboxDir("resettest"), meta.Workdir().HostPath)

	// Modify work copy
	require.NoError(t, os.WriteFile(
		filepath.Join(workDir, "new_file.txt"),
		[]byte("agent wrote this\n"),
		0600,
	))

	// Reset
	_, resetErr := resetSandbox(ctx, mgr, orchestrator.ResetOptions{Name: "resettest"})
	require.NoError(t, resetErr)

	// Reset is synchronous (stop+restore+start completes before returning), so
	// just wait for the container to be active again.
	testutil.WaitForActive(ctx, t, mgr.Runtime(), store.InstanceName("", "resettest"), 15*time.Second)

	// new_file.txt should be gone after reset
	assert.NoFileExists(t, filepath.Join(workDir, "new_file.txt"))

	// Original file should be restored
	assert.FileExists(t, filepath.Join(workDir, "main.go"))
}

func TestIntegration_Exec(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	// Create and start the sandbox
	_, err := createSandbox(ctx, mgr, orchestrator.CreateOptions{
		Name:    "exectest",
		Workdir: orchestrator.DirSpec{Path: projectDir},
		Agent:   "test",
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(ctx, mgr, "exectest") }) //nolint:errcheck // test cleanup

	_, err = startSandbox(ctx, mgr, "exectest", orchestrator.StartOptions{})
	require.NoError(t, err)

	// Wait for container to become active
	testutil.WaitForActive(ctx, t, mgr.Runtime(), store.InstanceName("", "exectest"), 15*time.Second)

	// Exec a command
	result, err := mgr.Runtime().Exec(ctx, store.InstanceName("", "exectest"), []string{"echo", "integration-test"}, "yoloai")
	require.NoError(t, err)
	assert.Equal(t, "integration-test", result.Stdout)
	assert.Equal(t, 0, result.ExitCode)
}

func TestIntegration_DiffClean(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := createSandbox(ctx, mgr, orchestrator.CreateOptions{
		Name:    "diffclean",
		Workdir: orchestrator.DirSpec{Path: projectDir},
		Agent:   "test",
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(ctx, mgr, "diffclean") }) //nolint:errcheck // test cleanup

	diffResult, err := copyflow.GenerateDiff(ctx, copyflow.DiffOptions{Name: "diffclean", Layout: mgr.Layout(), Runtime: mgr.Runtime()})
	require.NoError(t, err)
	assert.Empty(t, diffResult)
}

func TestIntegration_DiffWithChanges(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := createSandbox(ctx, mgr, orchestrator.CreateOptions{
		Name:    "diffchanges",
		Workdir: orchestrator.DirSpec{Path: projectDir},
		Agent:   "test",
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(ctx, mgr, "diffchanges") }) //nolint:errcheck // test cleanup

	meta, err := store.LoadEnvironment(mgr.Layout().SandboxDir("diffchanges"))
	require.NoError(t, err)
	workDir := store.WorkDir(mgr.Layout().SandboxDir("diffchanges"), meta.Workdir().HostPath)

	require.NoError(t, os.WriteFile(
		filepath.Join(workDir, "main.go"),
		[]byte("package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"changed\") }\n"),
		0600,
	))

	diffResult, err := copyflow.GenerateDiff(ctx, copyflow.DiffOptions{Name: "diffchanges", Layout: mgr.Layout(), Runtime: mgr.Runtime()})
	require.NoError(t, err)
	assert.NotEmpty(t, diffResult)
	assert.Contains(t, diffResult, "fmt")
}

func TestIntegration_ApplyPatch(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := createSandbox(ctx, mgr, orchestrator.CreateOptions{
		Name:    "applypatch",
		Workdir: orchestrator.DirSpec{Path: projectDir},
		Agent:   "test",
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(ctx, mgr, "applypatch") }) //nolint:errcheck // test cleanup

	meta, err := store.LoadEnvironment(mgr.Layout().SandboxDir("applypatch"))
	require.NoError(t, err)
	workDir := store.WorkDir(mgr.Layout().SandboxDir("applypatch"), meta.Workdir().HostPath)

	// Make a change
	require.NoError(t, os.WriteFile(
		filepath.Join(workDir, "main.go"),
		[]byte("package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"patched\") }\n"),
		0600,
	))

	// Generate patch
	patchBytes, stat, err := copyflow.GeneratePatch(ctx, mgr.Layout(), mgr.Runtime(), "applypatch", "", nil, true)
	require.NoError(t, err)
	assert.NotEmpty(t, patchBytes)
	assert.Contains(t, stat, "main.go")

	// Apply to a fresh copy of the original
	targetDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(targetDir, "main.go"),
		[]byte("package main\n\nfunc main() {}\n"),
		0600,
	))

	require.NoError(t, git.NewTestHostWithEnv(testutil.GitEnv()).ApplyPatch(context.Background(), patchBytes, targetDir, false))

	applied, err := os.ReadFile(filepath.Join(targetDir, "main.go")) //nolint:gosec // test path
	require.NoError(t, err)
	assert.Contains(t, string(applied), "patched")
}

func TestIntegration_Prompt(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := createSandbox(ctx, mgr, orchestrator.CreateOptions{
		Name:    "prompttest",
		Workdir: orchestrator.DirSpec{Path: projectDir},
		Agent:   "test",
		Prompt:  "echo hello world",
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(ctx, mgr, "prompttest") }) //nolint:errcheck // test cleanup

	sandboxDir := mgr.Layout().SandboxDir("prompttest")
	meta, err := store.LoadEnvironment(sandboxDir)
	require.NoError(t, err)
	assert.True(t, meta.HasPrompt)

	// Verify prompt.txt was written
	prompt, err := os.ReadFile(filepath.Join(sandboxDir, "prompt.txt")) //nolint:gosec // test path
	require.NoError(t, err)
	assert.Equal(t, "echo hello world", string(prompt))
}

func TestIntegration_ResourceLimits(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := createSandbox(ctx, mgr, orchestrator.CreateOptions{
		Name:    "reslimits",
		Workdir: orchestrator.DirSpec{Path: projectDir},
		Agent:   "test",
		CPUs:    "2",
		Memory:  "512m",
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(ctx, mgr, "reslimits") }) //nolint:errcheck // test cleanup

	meta, err := store.LoadEnvironment(mgr.Layout().SandboxDir("reslimits"))
	require.NoError(t, err)
	require.NotNil(t, meta.Resources)
	assert.Equal(t, "2", meta.Resources.CPUs)
	assert.Equal(t, "512m", meta.Resources.Memory)
}

func TestIntegration_PortForwarding(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := createSandbox(ctx, mgr, orchestrator.CreateOptions{
		Name:    "portfwd",
		Workdir: orchestrator.DirSpec{Path: projectDir},
		Agent:   "test",
		Ports:   []string{"3000:3000"},
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(ctx, mgr, "portfwd") }) //nolint:errcheck // test cleanup

	meta, err := store.LoadEnvironment(mgr.Layout().SandboxDir("portfwd"))
	require.NoError(t, err)
	assert.Contains(t, meta.Ports, "3000:3000")
}

func TestIntegration_MultiSandbox(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	for _, name := range []string{"multi-a", "multi-b"} {
		_, err := createSandbox(ctx, mgr, orchestrator.CreateOptions{
			Name:    name,
			Workdir: orchestrator.DirSpec{Path: projectDir},
			Agent:   "test",
			Version: "test",
		})
		require.NoError(t, err)
	}
	t.Cleanup(func() {
		destroySandbox(ctx, mgr, "multi-a") //nolint:errcheck // test cleanup
		destroySandbox(ctx, mgr, "multi-b") //nolint:errcheck // test cleanup
	})

	// Both should exist
	assert.DirExists(t, mgr.Layout().SandboxDir("multi-a"))
	assert.DirExists(t, mgr.Layout().SandboxDir("multi-b"))

	// Both should be in the listing
	infos, err := orchestrator.ListSandboxes(ctx, mgr.Layout(), mgr.Runtime())
	require.NoError(t, err)

	names := make(map[string]bool)
	for _, info := range infos {
		names[info.Environment.Name] = true
	}
	assert.True(t, names["multi-a"], "multi-a should be listed")
	assert.True(t, names["multi-b"], "multi-b should be listed")
}

func TestIntegration_DestroyCleanup(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := createSandbox(ctx, mgr, orchestrator.CreateOptions{
		Name:    "destroyme",
		Workdir: orchestrator.DirSpec{Path: projectDir},
		Agent:   "test",
		Version: "test",
	})
	require.NoError(t, err)

	sandboxDir := mgr.Layout().SandboxDir("destroyme")
	assert.DirExists(t, sandboxDir)

	_, destroyErr := destroySandbox(ctx, mgr, "destroyme")
	require.NoError(t, destroyErr)
	assert.NoDirExists(t, sandboxDir)

	// Container should be removed
	status, err := orchestrator.DetectStatus(ctx, mgr.Runtime(), store.InstanceName("", "destroyme"), mgr.Layout().SandboxDir("destroyme"))
	require.NoError(t, err)
	assert.Equal(t, orchestrator.StatusRemoved, status)
}

// TestIntegration_NetworkIsolation verifies that network-isolated sandboxes block
// outbound traffic. The isolation is enforced by iptables rules applied in
// entrypoint.py; this test confirms those rules are actually in effect.
func TestIntegration_NetworkIsolation(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := createSandbox(ctx, mgr, orchestrator.CreateOptions{
		Name:    "netisolated",
		Workdir: orchestrator.DirSpec{Path: projectDir},
		Agent:   "test",
		Network: orchestrator.NetworkModeIsolated,
		// No NetworkAllow entries — all outbound should be blocked.
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(ctx, mgr, "netisolated") }) //nolint:errcheck // test cleanup

	// Verify runtime-config.json has network_isolated: true so the test
	// can't pass vacuously (e.g., if the config field were never written).
	rcData, err := os.ReadFile(filepath.Join(mgr.Layout().SandboxDir("netisolated"), store.RuntimeConfigFile)) //nolint:gosec // test path
	require.NoError(t, err)
	var rc map[string]any
	require.NoError(t, json.Unmarshal(rcData, &rc))
	assert.Equal(t, true, rc["network_isolated"], "runtime-config.json must have network_isolated: true")

	_, err = startSandbox(ctx, mgr, "netisolated", orchestrator.StartOptions{})
	require.NoError(t, err)

	testutil.WaitForActive(ctx, t, mgr.Runtime(), store.InstanceName("", "netisolated"), 15*time.Second)

	// The entrypoint applies iptables rules before starting the agent, but
	// WaitForActive only confirms the container process is running, not that
	// the Python setup has completed. Give it a moment.
	time.Sleep(2 * time.Second)

	// curl to a well-known external IP should be blocked by the iptables REJECT rule.
	// Exec returns a non-nil error when the command exits non-zero, so we discard
	// the error and check ExitCode directly.
	external, _ := mgr.Runtime().Exec(ctx, store.InstanceName("", "netisolated"),
		[]string{"curl", "-sf", "--max-time", "5", "https://1.1.1.1"}, "yoloai")
	assert.NotEqual(t, 0, external.ExitCode,
		"curl to external IP should be blocked by network isolation")

	// Loopback must still be reachable — isolation must not block intra-container traffic.
	// Nothing listens on 127.0.0.1:80, so curl gets "connection refused" (exit 7),
	// which is distinct from an iptables timeout (exit 28). The point is that loopback
	// traffic is not blocked by our rules.
	lo, _ := mgr.Runtime().Exec(ctx, store.InstanceName("", "netisolated"),
		[]string{"curl", "-sf", "--max-time", "5", "http://127.0.0.1"}, "yoloai")
	assert.NotEqual(t, 28, lo.ExitCode, "loopback should not time out (iptables must allow lo)")
}

// TestIntegration_ReadOnlyMountVerified verifies that a read-only aux directory
// mount is actually enforced inside the container, not just recorded in environment.json.
// TestIntegration_AuxDirRO only checks the meta; this test proves the kernel
// enforces the mount flag.
func TestIntegration_ReadOnlyMountVerified(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)
	auxDir := createAuxDir(t, "readonly-verify")

	_, err := createSandbox(ctx, mgr, orchestrator.CreateOptions{
		Name:    "romount",
		Workdir: orchestrator.DirSpec{Path: projectDir},
		Agent:   "test",
		AuxDirs: []orchestrator.DirSpec{{Path: auxDir}}, // default mode = read-only
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(ctx, mgr, "romount") }) //nolint:errcheck // test cleanup

	_, err = startSandbox(ctx, mgr, "romount", orchestrator.StartOptions{})
	require.NoError(t, err)

	testutil.WaitForActive(ctx, t, mgr.Runtime(), store.InstanceName("", "romount"), 15*time.Second)

	// Attempt to write to the read-only aux dir from inside the container.
	// The mount target mirrors the host path by default.
	result, _ := mgr.Runtime().Exec(ctx, store.InstanceName("", "romount"),
		[]string{"sh", "-c", "echo pwned > " + auxDir + "/attack.txt"}, "yoloai")
	assert.NotEqual(t, 0, result.ExitCode,
		"write to read-only aux dir mount should fail inside the container")

	// Verify the host-side aux dir is unmodified.
	assert.NoFileExists(t, filepath.Join(auxDir, "attack.txt"))
}

// TestIntegration_CredentialInjection verifies the /run/secrets credential
// lifecycle: secrets are readable inside the agent's process tree, and the
// host-side temp directory is cleaned up after container start.
//
// Docker exec does NOT inherit the entrypoint's environment (it starts a new
// process chain), so we verify credentials by having the test agent's prompt
// write the env var to a file, then reading that file via exec.
func TestIntegration_CredentialInjection(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	// Snapshot pre-existing yoloai-secrets-* dirs so the cleanup assertion
	// below flags only one THIS run leaked — not an orphan a previously
	// killed run (e.g. a timed-out Tart smoke) left in the shared system
	// temp dir. The defer in launch.LaunchContainer cleans up on normal return; an
	// abnormally terminated run elsewhere can leave a dir we didn't create.
	secretsBefore := existingSecretsDirs(t)

	meta, err := createSandbox(ctx, mgr, orchestrator.CreateOptions{
		Name:    "credinject",
		Workdir: orchestrator.DirSpec{Path: projectDir},
		Agent:   "test",
		Prompt:  "printenv TEST_CREDENTIAL > /tmp/cred-check; sleep 5",
		Version: "test",
	})
	require.NoError(t, err)
	_ = meta
	t.Cleanup(func() { destroySandbox(ctx, mgr, "credinject") }) //nolint:errcheck // test cleanup

	// The per-sandbox env overlay is injected at launch (recreate), not persisted
	// from create — the caller re-supplies it on each start.
	_, err = startSandbox(ctx, mgr, "credinject", orchestrator.StartOptions{
		Env: map[string]string{"TEST_CREDENTIAL": "secret-value-xyz"},
	})
	require.NoError(t, err)

	testutil.WaitForActive(ctx, t, mgr.Runtime(), store.InstanceName("", "credinject"), 15*time.Second)

	// Poll until the prompt has written the credential file. The test agent
	// runs sh -c "PROMPT" via tmux; on slow CI runners a fixed sleep was
	// insufficient and caused flaky failures.
	var credOutput string
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		r, execErr := mgr.Runtime().Exec(ctx, store.InstanceName("", "credinject"),
			[]string{"cat", "/tmp/cred-check"}, "yoloai")
		if execErr == nil && r.ExitCode == 0 && r.Stdout != "" {
			credOutput = r.Stdout
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	require.NotEmpty(t, credOutput, "credential file was never written by prompt within 15s")
	assert.Equal(t, "secret-value-xyz", credOutput,
		"credential should be injected via /run/secrets and available in agent env")

	// The host-side temp secrets dir (yoloai-secrets-*) should have been
	// removed by the defer in launch.LaunchContainer. Assert no *new* one survived;
	// orphans from other/prior runs in the shared temp dir are out of scope.
	for name := range existingSecretsDirs(t) {
		if _, preexisting := secretsBefore[name]; !preexisting {
			assert.Failf(t, "secrets temp dir leaked",
				"host-side secrets temp dir from this run should be cleaned up: %s", name)
		}
	}
}

// existingSecretsDirs returns the set of yoloai-secrets-* directory names
// currently present in the system temp dir.
func existingSecretsDirs(t *testing.T) map[string]struct{} {
	t.Helper()
	entries, err := os.ReadDir(os.TempDir())
	require.NoError(t, err)
	out := make(map[string]struct{})
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "yoloai-secrets-") {
			out[e.Name()] = struct{}{}
		}
	}
	return out
}

// TestIntegration_AgentStubWorkflow tests the full agent-does-work → diff → apply pipeline.
// It uses the "test" agent (bash), starts the container, execs a command inside
// to simulate agent output, then verifies diff detects the change and apply lands
// the file in the original project directory.
func TestIntegration_AgentStubWorkflow(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := createSandbox(ctx, mgr, orchestrator.CreateOptions{
		Name:    "stubworkflow",
		Workdir: orchestrator.DirSpec{Path: projectDir},
		Agent:   "test",
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(ctx, mgr, "stubworkflow") }) //nolint:errcheck // test cleanup

	_, err = startSandbox(ctx, mgr, "stubworkflow", orchestrator.StartOptions{})
	require.NoError(t, err)

	testutil.WaitForActive(ctx, t, mgr.Runtime(), store.InstanceName("", "stubworkflow"), 15*time.Second)

	// Simulate agent output: create a new file inside the container.
	// Exec runs in the container's WorkingDir (= project bind-mount), so the
	// file appears in the work copy on the host side via the bind-mount.
	result, err := mgr.Runtime().Exec(ctx, store.InstanceName("", "stubworkflow"), []string{"touch", "agent-output.txt"}, "yoloai")
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)

	// Verify the file is visible in the work copy on the host
	meta, err := store.LoadEnvironment(mgr.Layout().SandboxDir("stubworkflow"))
	require.NoError(t, err)
	workDir := store.WorkDir(mgr.Layout().SandboxDir("stubworkflow"), meta.Workdir().HostPath)
	assert.FileExists(t, filepath.Join(workDir, "agent-output.txt"))

	// Diff should detect the new file
	diffResult, err := copyflow.GenerateDiff(ctx, copyflow.DiffOptions{Name: "stubworkflow", Layout: mgr.Layout(), Runtime: mgr.Runtime()})
	require.NoError(t, err)
	assert.NotEmpty(t, diffResult, "diff should not be empty after agent created a file")
	assert.Contains(t, diffResult, "agent-output.txt")

	// Generate patch and apply to a fresh copy of the original project
	patchBytes, stat, err := copyflow.GeneratePatch(ctx, mgr.Layout(), mgr.Runtime(), "stubworkflow", "", nil, true)
	require.NoError(t, err)
	assert.NotEmpty(t, patchBytes)
	assert.Contains(t, stat, "agent-output.txt")

	targetDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(targetDir, "main.go"),
		[]byte("package main\n\nfunc main() {}\n"),
		0600,
	))
	require.NoError(t, git.NewTestHostWithEnv(testutil.GitEnv()).ApplyPatch(context.Background(), patchBytes, targetDir, false))
	assert.FileExists(t, filepath.Join(targetDir, "agent-output.txt"))
}

func TestIntegration_Clone(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := createSandbox(ctx, mgr, orchestrator.CreateOptions{
		Name:    "clone-a",
		Workdir: orchestrator.DirSpec{Path: projectDir},
		Agent:   "test",
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		destroySandbox(ctx, mgr, "clone-a") //nolint:errcheck // test cleanup
		destroySandbox(ctx, mgr, "clone-b") //nolint:errcheck // test cleanup
	})

	// Seed a change in A's work copy
	meta, err := store.LoadEnvironment(mgr.Layout().SandboxDir("clone-a"))
	require.NoError(t, err)
	workDir := store.WorkDir(mgr.Layout().SandboxDir("clone-a"), meta.Workdir().HostPath)
	require.NoError(t, os.WriteFile(
		filepath.Join(workDir, "main.go"),
		[]byte("package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"clone-test\") }\n"),
		0600,
	))

	// Clone A → B
	require.NoError(t, mgr.Clone(ctx, orchestrator.CloneOptions{Source: "clone-a", Dest: "clone-b"}))

	// Diff on clone should show the seeded change
	diffResult, err := copyflow.GenerateDiff(ctx, copyflow.DiffOptions{Name: "clone-b", Layout: mgr.Layout(), Runtime: mgr.Runtime()})
	require.NoError(t, err)
	assert.NotEmpty(t, diffResult, "cloned sandbox should have changes")
	assert.Contains(t, diffResult, "clone-test")
}

func TestIntegration_Overlay(t *testing.T) {
	mgr, ctx := integrationSetup(t)

	// Use a project dir WITHOUT git. The entrypoint's chown -R on the overlay
	// merged dir makes overlayfs directories opaque, hiding the lower layer's
	// .git objects. Removing .git via whiteout + re-creating is unreliable
	// across kernel versions. Starting without .git avoids the problem entirely
	// and matches the real-world smoke test fixture (no pre-existing git repo).
	projectDir := testutil.GoProjectNoGit(t)

	_, err := createSandbox(ctx, mgr, orchestrator.CreateOptions{
		Name:    "overlay-integ",
		Workdir: orchestrator.DirSpec{Path: projectDir, Mode: orchestrator.DirModeOverlay},
		Agent:   "test",
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		// The overlayfs workdir (ovlwork/) contains root-owned kernel files that
		// cannot be removed by the test process. Clean them up via exec as root
		// inside the still-running container before destroying the sandbox.
		ovlEncoded := store.EncodePath(projectDir)
		mgr.Runtime().Exec(ctx, store.InstanceName("", "overlay-integ"), //nolint:errcheck // best-effort
			[]string{"rm", "-rf", "/yoloai/overlay/" + ovlEncoded + "/ovlwork"}, "root")
		destroySandbox(ctx, mgr, "overlay-integ") //nolint:errcheck // test cleanup
	})

	// The overlay mount happens at container launch (Start), not at provision
	// time, so the "overlay unsupported" skip is detected here rather than on create.
	if _, startErr := startSandbox(ctx, mgr, "overlay-integ", orchestrator.StartOptions{}); startErr != nil {
		if strings.Contains(startErr.Error(), "overlay") || strings.Contains(startErr.Error(), "mount") ||
			strings.Contains(startErr.Error(), "CAP_SYS_ADMIN") || strings.Contains(startErr.Error(), "permission") {
			t.Skip("overlay not supported: " + startErr.Error())
		}
		require.NoError(t, startErr) // fail on unexpected errors
	}

	testutil.WaitForActive(ctx, t, mgr.Runtime(), store.InstanceName("", "overlay-integ"), 15*time.Second)

	// For overlay mode, MountPath is /yoloai/overlay/<encoded>/merged — not the host path.
	meta, err := store.LoadEnvironment(mgr.Layout().SandboxDir("overlay-integ"))
	require.NoError(t, err)
	containerPath := meta.Workdir().MountPath

	// Create a git baseline inside the container. No .git exists in the lower
	// layer, so git init creates a fresh repo in the upper layer with no
	// overlayfs whiteout complications.
	//
	// Poll because the overlay mount is done by the entrypoint; on slow systems
	// WaitForActive may return before the mount is visible to exec calls.
	initCmd := fmt.Sprintf(
		"cd %s && git init -b main && git config user.email test@test && git config user.name test && git add -A && git commit -q -m baseline",
		containerPath,
	)
	var baselineSHA string
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		initResult, initErr := mgr.Runtime().Exec(ctx, store.InstanceName("", "overlay-integ"),
			[]string{"sh", "-c", initCmd}, "yoloai")
		if initErr == nil && initResult.ExitCode == 0 {
			shaResult, shaErr := mgr.Runtime().Exec(ctx, store.InstanceName("", "overlay-integ"),
				[]string{"git", "-C", containerPath, "rev-parse", "HEAD"}, "yoloai")
			if shaErr == nil && len(strings.TrimSpace(shaResult.Stdout)) == 40 {
				baselineSHA = strings.TrimSpace(shaResult.Stdout)
				break
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	require.NotEmpty(t, baselineSHA, "git init + commit inside overlay should produce a valid SHA within 15s")
	require.NoError(t, copyflow.UpdateOverlayBaseline(mgr.Layout(), "overlay-integ", projectDir, baselineSHA))

	// Write a file inside the container (overlay captures it in upper layer)
	writeResult, err := mgr.Runtime().Exec(ctx, store.InstanceName("", "overlay-integ"),
		[]string{"sh", "-c", fmt.Sprintf("echo overlay-test > %s/output.txt", containerPath)}, "yoloai")
	require.NoError(t, err)
	assert.Equal(t, 0, writeResult.ExitCode)

	// Diff: must use GenerateOverlayDiff (GenerateDiff returns
	// ErrOverlayRequiresRuntime for overlay; overlay needs container
	// exec).
	overlayDiff, err := copyflow.GenerateOverlayDiff(ctx, mgr.Runtime(), copyflow.DiffOptions{Name: "overlay-integ", Layout: mgr.Layout()})
	require.NoError(t, err)
	assert.NotEmpty(t, overlayDiff, "overlay should have changes after exec write")
	assert.Contains(t, overlayDiff, "output.txt")

	// Apply via the library orchestrator copyflow.ApplyOverlay (captures the
	// upper-layer diff, applies it to the host, advances the overlay baseline) —
	// the same path Workdir().Apply(ApplyModeNoCommit) takes for overlay.
	result, err := copyflow.ApplyOverlay(ctx, mgr.Layout(), mgr.Runtime(), "overlay-integ", copyflow.ApplyOverlayOptions{})
	require.NoError(t, err)
	require.NotNil(t, result, "overlay apply should report a result when there are changes")
	assert.True(t, result.UncommittedApplied)
	assert.Contains(t, result.Stat, "output.txt")

	applied, err := os.ReadFile(filepath.Join(projectDir, "output.txt")) //nolint:gosec // test path
	require.NoError(t, err)
	assert.Contains(t, string(applied), "overlay-test")

	// DryRun reports the same stat without re-applying.
	preview, err := copyflow.ApplyOverlay(ctx, mgr.Layout(), mgr.Runtime(), "overlay-integ", copyflow.ApplyOverlayOptions{DryRun: true})
	require.NoError(t, err)
	require.NotNil(t, preview)
	assert.Contains(t, preview.Stat, "output.txt")
}

//go:build integration

package sandbox

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kstenerud/yoloai/internal/testutil"
	"github.com/kstenerud/yoloai/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIntegration_FullLifecycle(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	// Create sandbox (starts container)
	sandboxName := "integ-test"
	_, err := mgr.Create(ctx, CreateOptions{
		Name:    sandboxName,
		Workdir: DirSpec{Path: projectDir},
		Agent:   "test",
		Version: "test",
	})
	require.NoError(t, err)

	// Wait for container to become active
	testutil.WaitForActive(ctx, t, mgr.runtime, InstanceName(sandboxName), 15*time.Second)

	// Verify directory structure
	sandboxDir := Dir(sandboxName)
	assert.DirExists(t, sandboxDir)

	meta, err := LoadMeta(sandboxDir)
	require.NoError(t, err)
	assert.Equal(t, sandboxName, meta.Name)
	assert.Equal(t, "test", meta.Agent)
	assert.Equal(t, "copy", meta.Workdir.Mode)
	assert.NotEmpty(t, meta.Workdir.BaselineSHA)

	workDir := WorkDir(sandboxName, meta.Workdir.HostPath)
	assert.FileExists(t, filepath.Join(workDir, "main.go"))

	// Verify container is running
	status, err := DetectStatus(ctx, mgr.runtime, InstanceName(sandboxName), Dir(sandboxName))
	require.NoError(t, err)
	assert.Equal(t, StatusActive, status)

	// Exec inside running container
	result, err := mgr.runtime.Exec(ctx, InstanceName(sandboxName), []string{"echo", "lifecycle-test"}, "yoloai")
	require.NoError(t, err)
	assert.Equal(t, "lifecycle-test", result.Stdout)
	assert.Equal(t, 0, result.ExitCode)

	// Modify work copy and verify diff
	require.NoError(t, os.WriteFile(
		filepath.Join(workDir, "main.go"),
		[]byte("package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"hello\") }\n"),
		0600,
	))

	diffResult, err := GenerateDiff(ctx, DiffOptions{Name: sandboxName})
	require.NoError(t, err)
	assert.False(t, diffResult.Empty)
	assert.Contains(t, diffResult.Output, "fmt")

	// Stop container and verify
	require.NoError(t, mgr.Stop(ctx, sandboxName))

	status, err = DetectStatus(ctx, mgr.runtime, InstanceName(sandboxName), Dir(sandboxName))
	require.NoError(t, err)
	assert.Equal(t, StatusStopped, status)

	// Restart container and verify
	require.NoError(t, mgr.Start(ctx, sandboxName, StartOptions{}))
	testutil.WaitForActive(ctx, t, mgr.runtime, InstanceName(sandboxName), 15*time.Second)

	status, err = DetectStatus(ctx, mgr.runtime, InstanceName(sandboxName), Dir(sandboxName))
	require.NoError(t, err)
	assert.Equal(t, StatusActive, status)

	// Exec again after restart
	result, err = mgr.runtime.Exec(ctx, InstanceName(sandboxName), []string{"echo", "after-restart"}, "yoloai")
	require.NoError(t, err)
	assert.Equal(t, "after-restart", result.Stdout)
	assert.Equal(t, 0, result.ExitCode)

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

	// Destroy
	require.NoError(t, mgr.Destroy(ctx, sandboxName))
	assert.NoDirExists(t, sandboxDir)

	// Container should be gone
	status, err = DetectStatus(ctx, mgr.runtime, InstanceName(sandboxName), Dir(sandboxName))
	require.NoError(t, err)
	assert.Equal(t, StatusRemoved, status)
}

func TestIntegration_CreateNoStart(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := mgr.Create(ctx, CreateOptions{
		Name:    "nostart",
		Workdir: DirSpec{Path: projectDir},
		Agent:   "test",
		NoStart: true,
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Destroy(ctx, "nostart") }) //nolint:errcheck // test cleanup

	sandboxDir := Dir("nostart")
	assert.DirExists(t, sandboxDir)

	meta, err := LoadMeta(sandboxDir)
	require.NoError(t, err)
	assert.Equal(t, "nostart", meta.Name)
	assert.Equal(t, "test", meta.Agent)
	assert.Equal(t, "copy", meta.Workdir.Mode)
	assert.NotEmpty(t, meta.Workdir.BaselineSHA)

	// Verify work copy contains our file
	workDir := WorkDir("nostart", meta.Workdir.HostPath)
	assert.FileExists(t, filepath.Join(workDir, "main.go"))

	// Verify standard subdirs
	assert.DirExists(t, filepath.Join(sandboxDir, AgentRuntimeDir))
	assert.FileExists(t, filepath.Join(sandboxDir, EnvironmentFile))
	assert.FileExists(t, filepath.Join(sandboxDir, RuntimeConfigFile))
}

func TestIntegration_CopyMode(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := mgr.Create(ctx, CreateOptions{
		Name:    "copymode",
		Workdir: DirSpec{Path: projectDir, Mode: DirModeCopy},
		Agent:   "test",
		NoStart: true,
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Destroy(ctx, "copymode") }) //nolint:errcheck // test cleanup

	meta, err := LoadMeta(Dir("copymode"))
	require.NoError(t, err)
	assert.Equal(t, "copy", meta.Workdir.Mode)

	workDir := WorkDir("copymode", meta.Workdir.HostPath)

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

	_, err := mgr.Create(ctx, CreateOptions{
		Name:    "rwmode",
		Workdir: DirSpec{Path: projectDir, Mode: DirModeRW},
		Agent:   "test",
		NoStart: true,
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Destroy(ctx, "rwmode") }) //nolint:errcheck // test cleanup

	meta, err := LoadMeta(Dir("rwmode"))
	require.NoError(t, err)
	assert.Equal(t, "rw", meta.Workdir.Mode)
}

func TestIntegration_AuxDirCopy(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)
	auxDir := createAuxDir(t, "libs")

	_, err := mgr.Create(ctx, CreateOptions{
		Name:    "auxcopy",
		Workdir: DirSpec{Path: projectDir},
		Agent:   "test",
		NoStart: true,
		AuxDirs: []DirSpec{{Path: auxDir, Mode: DirModeCopy}},
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Destroy(ctx, "auxcopy") }) //nolint:errcheck // test cleanup

	meta, err := LoadMeta(Dir("auxcopy"))
	require.NoError(t, err)
	require.Len(t, meta.Directories, 1)
	assert.Equal(t, "copy", meta.Directories[0].Mode)
	assert.NotEmpty(t, meta.Directories[0].BaselineSHA)

	// Verify aux work copy has the file
	auxWorkDir := WorkDir("auxcopy", meta.Directories[0].HostPath)
	assert.FileExists(t, filepath.Join(auxWorkDir, "data.txt"))
}

func TestIntegration_AuxDirRO(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)
	auxDir := createAuxDir(t, "readonly-lib")

	_, err := mgr.Create(ctx, CreateOptions{
		Name:    "auxro",
		Workdir: DirSpec{Path: projectDir},
		Agent:   "test",
		NoStart: true,
		AuxDirs: []DirSpec{{Path: auxDir}},
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Destroy(ctx, "auxro") }) //nolint:errcheck // test cleanup

	meta, err := LoadMeta(Dir("auxro"))
	require.NoError(t, err)
	require.Len(t, meta.Directories, 1)
	assert.Equal(t, "ro", meta.Directories[0].Mode)
}

func TestIntegration_Replace(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	// Create first sandbox
	_, err := mgr.Create(ctx, CreateOptions{
		Name:    "replaceme",
		Workdir: DirSpec{Path: projectDir},
		Agent:   "test",
		NoStart: true,
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Destroy(ctx, "replaceme") }) //nolint:errcheck // test cleanup

	// Replace with new sandbox
	_, err = mgr.Create(ctx, CreateOptions{
		Name:    "replaceme",
		Workdir: DirSpec{Path: projectDir},
		Agent:   "test",
		NoStart: true,
		Replace: true,
		Version: "test",
	})
	require.NoError(t, err)

	// Should still exist with valid meta
	meta, err := LoadMeta(Dir("replaceme"))
	require.NoError(t, err)
	assert.Equal(t, "replaceme", meta.Name)
}

func TestIntegration_Reset(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	// Create and start the sandbox (Reset requires a restart cycle)
	_, err := mgr.Create(ctx, CreateOptions{
		Name:    "resettest",
		Workdir: DirSpec{Path: projectDir},
		Agent:   "test",
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Destroy(ctx, "resettest") }) //nolint:errcheck // test cleanup

	// Wait for container to become active
	testutil.WaitForActive(ctx, t, mgr.runtime, InstanceName("resettest"), 15*time.Second)

	meta, err := LoadMeta(Dir("resettest"))
	require.NoError(t, err)
	workDir := WorkDir("resettest", meta.Workdir.HostPath)

	// Modify work copy
	require.NoError(t, os.WriteFile(
		filepath.Join(workDir, "new_file.txt"),
		[]byte("agent wrote this\n"),
		0600,
	))

	// Reset
	require.NoError(t, mgr.Reset(ctx, ResetOptions{Name: "resettest"}))

	// Reset is synchronous (stop+restore+start completes before returning), so
	// just wait for the container to be active again.
	testutil.WaitForActive(ctx, t, mgr.runtime, InstanceName("resettest"), 15*time.Second)

	// new_file.txt should be gone after reset
	assert.NoFileExists(t, filepath.Join(workDir, "new_file.txt"))

	// Original file should be restored
	assert.FileExists(t, filepath.Join(workDir, "main.go"))
}

func TestIntegration_Exec(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	// Create and start the sandbox
	_, err := mgr.Create(ctx, CreateOptions{
		Name:    "exectest",
		Workdir: DirSpec{Path: projectDir},
		Agent:   "test",
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Destroy(ctx, "exectest") }) //nolint:errcheck // test cleanup

	// Wait for container to become active
	testutil.WaitForActive(ctx, t, mgr.runtime, InstanceName("exectest"), 15*time.Second)

	// Exec a command
	result, err := mgr.runtime.Exec(ctx, InstanceName("exectest"), []string{"echo", "integration-test"}, "yoloai")
	require.NoError(t, err)
	assert.Equal(t, "integration-test", result.Stdout)
	assert.Equal(t, 0, result.ExitCode)
}

func TestIntegration_DiffClean(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := mgr.Create(ctx, CreateOptions{
		Name:    "diffclean",
		Workdir: DirSpec{Path: projectDir},
		Agent:   "test",
		NoStart: true,
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Destroy(ctx, "diffclean") }) //nolint:errcheck // test cleanup

	diffResult, err := GenerateDiff(ctx, DiffOptions{Name: "diffclean"})
	require.NoError(t, err)
	assert.True(t, diffResult.Empty)
}

func TestIntegration_DiffWithChanges(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := mgr.Create(ctx, CreateOptions{
		Name:    "diffchanges",
		Workdir: DirSpec{Path: projectDir},
		Agent:   "test",
		NoStart: true,
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Destroy(ctx, "diffchanges") }) //nolint:errcheck // test cleanup

	meta, err := LoadMeta(Dir("diffchanges"))
	require.NoError(t, err)
	workDir := WorkDir("diffchanges", meta.Workdir.HostPath)

	require.NoError(t, os.WriteFile(
		filepath.Join(workDir, "main.go"),
		[]byte("package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"changed\") }\n"),
		0600,
	))

	diffResult, err := GenerateDiff(ctx, DiffOptions{Name: "diffchanges"})
	require.NoError(t, err)
	assert.False(t, diffResult.Empty)
	assert.Contains(t, diffResult.Output, "fmt")
}

func TestIntegration_ApplyPatch(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := mgr.Create(ctx, CreateOptions{
		Name:    "applypatch",
		Workdir: DirSpec{Path: projectDir},
		Agent:   "test",
		NoStart: true,
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Destroy(ctx, "applypatch") }) //nolint:errcheck // test cleanup

	meta, err := LoadMeta(Dir("applypatch"))
	require.NoError(t, err)
	workDir := WorkDir("applypatch", meta.Workdir.HostPath)

	// Make a change
	require.NoError(t, os.WriteFile(
		filepath.Join(workDir, "main.go"),
		[]byte("package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"patched\") }\n"),
		0600,
	))

	// Generate patch
	patch, stat, err := GeneratePatch("applypatch", nil)
	require.NoError(t, err)
	assert.NotEmpty(t, patch)
	assert.Contains(t, stat, "main.go")

	// Apply to a fresh copy of the original
	targetDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(targetDir, "main.go"),
		[]byte("package main\n\nfunc main() {}\n"),
		0600,
	))

	require.NoError(t, workspace.ApplyPatch(patch, targetDir, false))

	applied, err := os.ReadFile(filepath.Join(targetDir, "main.go")) //nolint:gosec // test path
	require.NoError(t, err)
	assert.Contains(t, string(applied), "patched")
}

func TestIntegration_Prompt(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := mgr.Create(ctx, CreateOptions{
		Name:    "prompttest",
		Workdir: DirSpec{Path: projectDir},
		Agent:   "test",
		Prompt:  "echo hello world",
		NoStart: true,
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Destroy(ctx, "prompttest") }) //nolint:errcheck // test cleanup

	sandboxDir := Dir("prompttest")
	meta, err := LoadMeta(sandboxDir)
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

	_, err := mgr.Create(ctx, CreateOptions{
		Name:    "reslimits",
		Workdir: DirSpec{Path: projectDir},
		Agent:   "test",
		NoStart: true,
		CPUs:    "2",
		Memory:  "512m",
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Destroy(ctx, "reslimits") }) //nolint:errcheck // test cleanup

	meta, err := LoadMeta(Dir("reslimits"))
	require.NoError(t, err)
	require.NotNil(t, meta.Resources)
	assert.Equal(t, "2", meta.Resources.CPUs)
	assert.Equal(t, "512m", meta.Resources.Memory)
}

func TestIntegration_PortForwarding(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := mgr.Create(ctx, CreateOptions{
		Name:    "portfwd",
		Workdir: DirSpec{Path: projectDir},
		Agent:   "test",
		NoStart: true,
		Ports:   []string{"3000:3000"},
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Destroy(ctx, "portfwd") }) //nolint:errcheck // test cleanup

	meta, err := LoadMeta(Dir("portfwd"))
	require.NoError(t, err)
	assert.Contains(t, meta.Ports, "3000:3000")
}

func TestIntegration_MultiSandbox(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	for _, name := range []string{"multi-a", "multi-b"} {
		_, err := mgr.Create(ctx, CreateOptions{
			Name:    name,
			Workdir: DirSpec{Path: projectDir},
			Agent:   "test",
			NoStart: true,
			Version: "test",
		})
		require.NoError(t, err)
	}
	t.Cleanup(func() {
		mgr.Destroy(ctx, "multi-a") //nolint:errcheck // test cleanup
		mgr.Destroy(ctx, "multi-b") //nolint:errcheck // test cleanup
	})

	// Both should exist
	assert.DirExists(t, Dir("multi-a"))
	assert.DirExists(t, Dir("multi-b"))

	// Both should be in the listing
	infos, err := ListSandboxes(ctx, mgr.runtime)
	require.NoError(t, err)

	names := make(map[string]bool)
	for _, info := range infos {
		names[info.Meta.Name] = true
	}
	assert.True(t, names["multi-a"], "multi-a should be listed")
	assert.True(t, names["multi-b"], "multi-b should be listed")
}

func TestIntegration_DestroyCleanup(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := mgr.Create(ctx, CreateOptions{
		Name:    "destroyme",
		Workdir: DirSpec{Path: projectDir},
		Agent:   "test",
		NoStart: true,
		Version: "test",
	})
	require.NoError(t, err)

	sandboxDir := Dir("destroyme")
	assert.DirExists(t, sandboxDir)

	require.NoError(t, mgr.Destroy(ctx, "destroyme"))
	assert.NoDirExists(t, sandboxDir)

	// Container should be removed
	status, err := DetectStatus(ctx, mgr.runtime, InstanceName("destroyme"), Dir("destroyme"))
	require.NoError(t, err)
	assert.Equal(t, StatusRemoved, status)
}

// TestIntegration_AgentStubWorkflow tests the full agent-does-work → diff → apply pipeline.
// It uses the "test" agent (bash), starts the container, execs a command inside
// to simulate agent output, then verifies diff detects the change and apply lands
// the file in the original project directory.
func TestIntegration_AgentStubWorkflow(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := mgr.Create(ctx, CreateOptions{
		Name:    "stubworkflow",
		Workdir: DirSpec{Path: projectDir},
		Agent:   "test",
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Destroy(ctx, "stubworkflow") }) //nolint:errcheck // test cleanup

	testutil.WaitForActive(ctx, t, mgr.runtime, InstanceName("stubworkflow"), 15*time.Second)

	// Simulate agent output: create a new file inside the container.
	// Exec runs in the container's WorkingDir (= project bind-mount), so the
	// file appears in the work copy on the host side via the bind-mount.
	result, err := mgr.runtime.Exec(ctx, InstanceName("stubworkflow"), []string{"touch", "agent-output.txt"}, "yoloai")
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)

	// Verify the file is visible in the work copy on the host
	meta, err := LoadMeta(Dir("stubworkflow"))
	require.NoError(t, err)
	workDir := WorkDir("stubworkflow", meta.Workdir.HostPath)
	assert.FileExists(t, filepath.Join(workDir, "agent-output.txt"))

	// Diff should detect the new file
	diffResult, err := GenerateDiff(ctx, DiffOptions{Name: "stubworkflow"})
	require.NoError(t, err)
	assert.False(t, diffResult.Empty, "diff should not be empty after agent created a file")
	assert.Contains(t, diffResult.Output, "agent-output.txt")

	// Generate patch and apply to a fresh copy of the original project
	patch, stat, err := GeneratePatch("stubworkflow", nil)
	require.NoError(t, err)
	assert.NotEmpty(t, patch)
	assert.Contains(t, stat, "agent-output.txt")

	targetDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(targetDir, "main.go"),
		[]byte("package main\n\nfunc main() {}\n"),
		0600,
	))
	require.NoError(t, workspace.ApplyPatch(patch, targetDir, false))
	assert.FileExists(t, filepath.Join(targetDir, "agent-output.txt"))
}

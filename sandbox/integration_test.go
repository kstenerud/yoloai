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

	dockerrt "github.com/kstenerud/yoloai/runtime/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIntegration_FullLifecycle(t *testing.T) {
	ctx := context.Background()

	// Use a dedicated HOME to avoid polluting the real one
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// Create a temp project directory with known content
	projectDir := filepath.Join(tmpHome, "project")
	require.NoError(t, os.MkdirAll(projectDir, 0750))
	require.NoError(t, os.WriteFile(
		filepath.Join(projectDir, "main.go"),
		[]byte("package main\n\nfunc main() {}\n"),
		0600,
	))

	// Connect to Docker
	rt, err := dockerrt.New(ctx)
	require.NoError(t, err, "Docker must be running for integration tests")
	defer rt.Close() //nolint:errcheck // test cleanup

	mgr := NewManager(rt, "docker", slog.Default(), strings.NewReader(""), io.Discard)

	// Step 1: EnsureSetup (builds base image if needed)
	require.NoError(t, mgr.EnsureSetup(ctx))

	// Step 2: Create sandbox with --no-start (no API keys needed)
	sandboxName := "integ-test"
	_, err = mgr.Create(ctx, CreateOptions{
		Name:       sandboxName,
		WorkdirArg: projectDir,
		Agent:      "claude",
		NoStart:    true,
		Version:    "test",
	})
	require.NoError(t, err)

	// Step 3: Verify directory structure
	sandboxDir := Dir(sandboxName)
	assert.DirExists(t, sandboxDir)

	meta, err := LoadMeta(sandboxDir)
	require.NoError(t, err)
	assert.Equal(t, sandboxName, meta.Name)
	assert.Equal(t, "claude", meta.Agent)
	assert.Equal(t, "copy", meta.Workdir.Mode)
	assert.NotEmpty(t, meta.Workdir.BaselineSHA)

	workDir := WorkDir(sandboxName, meta.Workdir.HostPath)
	assert.FileExists(t, filepath.Join(workDir, "main.go"))

	// Step 4: Start the container
	require.NoError(t, mgr.Start(ctx, sandboxName, false))

	// Give the container a moment to start
	time.Sleep(2 * time.Second)

	status, containerID, err := DetectStatus(ctx, rt, "yoloai-"+sandboxName)
	require.NoError(t, err)
	assert.NotEmpty(t, containerID)
	// Status should be running, done, or failed (agent may fail without API key)
	assert.Contains(t, []Status{StatusRunning, StatusDone, StatusFailed}, status)

	// Step 5: Stop the container
	require.NoError(t, mgr.Stop(ctx, sandboxName))

	// Give Docker a moment to stop
	time.Sleep(1 * time.Second)

	status, _, err = DetectStatus(ctx, rt, "yoloai-"+sandboxName)
	require.NoError(t, err)
	assert.Equal(t, StatusStopped, status)

	// Step 6: Modify work copy and verify diff
	require.NoError(t, os.WriteFile(
		filepath.Join(workDir, "main.go"),
		[]byte("package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"hello\") }\n"),
		0600,
	))

	diffResult, err := GenerateDiff(DiffOptions{Name: sandboxName})
	require.NoError(t, err)
	assert.False(t, diffResult.Empty)
	assert.Contains(t, diffResult.Output, "fmt")

	// Step 7: Generate patch and apply to a target directory
	patch, stat, err := GeneratePatch(sandboxName, nil)
	require.NoError(t, err)
	assert.NotEmpty(t, patch)
	assert.Contains(t, stat, "main.go")

	targetDir := filepath.Join(tmpHome, "target")
	require.NoError(t, os.MkdirAll(targetDir, 0750))
	require.NoError(t, os.WriteFile(
		filepath.Join(targetDir, "main.go"),
		[]byte("package main\n\nfunc main() {}\n"),
		0600,
	))

	require.NoError(t, ApplyPatch(patch, targetDir, false))

	applied, err := os.ReadFile(filepath.Join(targetDir, "main.go")) //nolint:gosec // G304: test file path
	require.NoError(t, err)
	assert.Contains(t, string(applied), "fmt.Println")

	// Step 8: Destroy
	require.NoError(t, mgr.Destroy(ctx, sandboxName))
	assert.NoDirExists(t, sandboxDir)

	// Container should be gone
	status, _, err = DetectStatus(ctx, rt, "yoloai-"+sandboxName)
	require.NoError(t, err)
	assert.Equal(t, StatusRemoved, status)
}

func TestIntegration_CreateNoStart(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := mgr.Create(ctx, CreateOptions{
		Name:       "nostart",
		WorkdirArg: projectDir,
		Agent:      "test",
		NoStart:    true,
		Version:    "test",
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
	assert.DirExists(t, filepath.Join(sandboxDir, "agent-state"))
	assert.FileExists(t, filepath.Join(sandboxDir, "meta.json"))
	assert.FileExists(t, filepath.Join(sandboxDir, "config.json"))
}

func TestIntegration_CopyMode(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := mgr.Create(ctx, CreateOptions{
		Name:       "copymode",
		WorkdirArg: projectDir + ":copy",
		Agent:      "test",
		NoStart:    true,
		Version:    "test",
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
		Name:       "rwmode",
		WorkdirArg: projectDir + ":rw",
		Agent:      "test",
		NoStart:    true,
		Version:    "test",
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
		Name:       "auxcopy",
		WorkdirArg: projectDir,
		Agent:      "test",
		NoStart:    true,
		AuxDirArgs: []string{auxDir + ":copy"},
		Version:    "test",
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
		Name:       "auxro",
		WorkdirArg: projectDir,
		Agent:      "test",
		NoStart:    true,
		AuxDirArgs: []string{auxDir},
		Version:    "test",
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
		Name:       "replaceme",
		WorkdirArg: projectDir,
		Agent:      "test",
		NoStart:    true,
		Version:    "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Destroy(ctx, "replaceme") }) //nolint:errcheck // test cleanup

	// Replace with new sandbox
	_, err = mgr.Create(ctx, CreateOptions{
		Name:       "replaceme",
		WorkdirArg: projectDir,
		Agent:      "test",
		NoStart:    true,
		Replace:    true,
		Version:    "test",
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

	_, err := mgr.Create(ctx, CreateOptions{
		Name:       "resettest",
		WorkdirArg: projectDir,
		Agent:      "test",
		NoStart:    true,
		Version:    "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Destroy(ctx, "resettest") }) //nolint:errcheck // test cleanup

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

	// new_file.txt should be gone after reset
	assert.NoFileExists(t, filepath.Join(workDir, "new_file.txt"))

	// Original file should be restored
	assert.FileExists(t, filepath.Join(workDir, "main.go"))
}

func TestIntegration_Exec(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := mgr.Create(ctx, CreateOptions{
		Name:       "exectest",
		WorkdirArg: projectDir,
		Agent:      "test",
		NoStart:    true,
		Version:    "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Destroy(ctx, "exectest") }) //nolint:errcheck // test cleanup

	// Start the container
	require.NoError(t, mgr.Start(ctx, "exectest", false))
	time.Sleep(1 * time.Second)

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
		Name:       "diffclean",
		WorkdirArg: projectDir,
		Agent:      "test",
		NoStart:    true,
		Version:    "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { mgr.Destroy(ctx, "diffclean") }) //nolint:errcheck // test cleanup

	diffResult, err := GenerateDiff(DiffOptions{Name: "diffclean"})
	require.NoError(t, err)
	assert.True(t, diffResult.Empty)
}

func TestIntegration_DiffWithChanges(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := mgr.Create(ctx, CreateOptions{
		Name:       "diffchanges",
		WorkdirArg: projectDir,
		Agent:      "test",
		NoStart:    true,
		Version:    "test",
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

	diffResult, err := GenerateDiff(DiffOptions{Name: "diffchanges"})
	require.NoError(t, err)
	assert.False(t, diffResult.Empty)
	assert.Contains(t, diffResult.Output, "fmt")
}

func TestIntegration_ApplyPatch(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := mgr.Create(ctx, CreateOptions{
		Name:       "applypatch",
		WorkdirArg: projectDir,
		Agent:      "test",
		NoStart:    true,
		Version:    "test",
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

	require.NoError(t, ApplyPatch(patch, targetDir, false))

	applied, err := os.ReadFile(filepath.Join(targetDir, "main.go")) //nolint:gosec // test path
	require.NoError(t, err)
	assert.Contains(t, string(applied), "patched")
}

func TestIntegration_Prompt(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	projectDir := createProjectDir(t)

	_, err := mgr.Create(ctx, CreateOptions{
		Name:       "prompttest",
		WorkdirArg: projectDir,
		Agent:      "test",
		Prompt:     "echo hello world",
		NoStart:    true,
		Version:    "test",
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
		Name:       "reslimits",
		WorkdirArg: projectDir,
		Agent:      "test",
		NoStart:    true,
		CPUs:       "2",
		Memory:     "512m",
		Version:    "test",
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
		Name:       "portfwd",
		WorkdirArg: projectDir,
		Agent:      "test",
		NoStart:    true,
		Ports:      []string{"3000:3000"},
		Version:    "test",
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
			Name:       name,
			WorkdirArg: projectDir,
			Agent:      "test",
			NoStart:    true,
			Version:    "test",
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
		Name:       "destroyme",
		WorkdirArg: projectDir,
		Agent:      "test",
		NoStart:    true,
		Version:    "test",
	})
	require.NoError(t, err)

	sandboxDir := Dir("destroyme")
	assert.DirExists(t, sandboxDir)

	require.NoError(t, mgr.Destroy(ctx, "destroyme"))
	assert.NoDirExists(t, sandboxDir)

	// Container should be removed
	status, _, err := DetectStatus(ctx, mgr.runtime, InstanceName("destroyme"))
	require.NoError(t, err)
	assert.Equal(t, StatusRemoved, status)
}

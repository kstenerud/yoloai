//go:build integration

package sandbox

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kstenerud/yoloai/internal/docker"
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
	client, err := docker.NewClient(ctx)
	require.NoError(t, err, "Docker must be running for integration tests")
	defer client.Close() //nolint:errcheck // test cleanup

	mgr := NewManager(client, slog.Default(), io.Discard)

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
	require.NoError(t, mgr.Start(ctx, sandboxName))

	// Give the container a moment to start
	time.Sleep(2 * time.Second)

	status, containerID, err := DetectStatus(ctx, client, "yoloai-"+sandboxName)
	require.NoError(t, err)
	assert.NotEmpty(t, containerID)
	// Status should be running, done, or failed (agent may fail without API key)
	assert.Contains(t, []Status{StatusRunning, StatusDone, StatusFailed}, status)

	// Step 5: Stop the container
	require.NoError(t, mgr.Stop(ctx, sandboxName))

	// Give Docker a moment to stop
	time.Sleep(1 * time.Second)

	status, _, err = DetectStatus(ctx, client, "yoloai-"+sandboxName)
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
	require.NoError(t, mgr.Destroy(ctx, sandboxName, true))
	assert.NoDirExists(t, sandboxDir)

	// Container should be gone
	status, _, err = DetectStatus(ctx, client, "yoloai-"+sandboxName)
	require.NoError(t, err)
	assert.Equal(t, StatusRemoved, status)
}

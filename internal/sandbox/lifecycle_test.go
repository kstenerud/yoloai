package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lifecycleMockClient extends mockClient for lifecycle tests.
type lifecycleMockClient struct {
	mockClient
	containerStopFn    func(ctx context.Context, id string, opts container.StopOptions) error
	containerStartFn   func(ctx context.Context, id string, opts container.StartOptions) error
	containerRemoveFn  func(ctx context.Context, id string, opts container.RemoveOptions) error
	containerInspectFn func(ctx context.Context, id string) (container.InspectResponse, error)
}

func (m *lifecycleMockClient) ContainerStop(ctx context.Context, id string, opts container.StopOptions) error {
	if m.containerStopFn != nil {
		return m.containerStopFn(ctx, id, opts)
	}
	return nil
}

func (m *lifecycleMockClient) ContainerStart(ctx context.Context, id string, opts container.StartOptions) error {
	if m.containerStartFn != nil {
		return m.containerStartFn(ctx, id, opts)
	}
	return nil
}

func (m *lifecycleMockClient) ContainerRemove(ctx context.Context, id string, opts container.RemoveOptions) error {
	if m.containerRemoveFn != nil {
		return m.containerRemoveFn(ctx, id, opts)
	}
	return nil
}

func (m *lifecycleMockClient) ContainerInspect(ctx context.Context, id string) (container.InspectResponse, error) {
	if m.containerInspectFn != nil {
		return m.containerInspectFn(ctx, id)
	}
	return container.InspectResponse{}, errMockNotImplemented
}

// newLifecycleMgr creates a Manager with the given mock client and a discard output.
func newLifecycleMgr(client *lifecycleMockClient) *Manager {
	return NewManager(client, slog.Default(), strings.NewReader(""), io.Discard)
}

// createTestSandbox creates a sandbox directory with meta.json for lifecycle tests.
func createTestSandbox(t *testing.T, tmpDir, name, hostPath, mode string) {
	t.Helper()
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	meta := &Meta{
		Name:      name,
		Agent:     "claude",
		CreatedAt: time.Now(),
		Workdir: WorkdirMeta{
			HostPath:  hostPath,
			MountPath: hostPath,
			Mode:      mode,
		},
	}
	require.NoError(t, SaveMeta(sandboxDir, meta))
}

// Stop tests

func TestStop_Running(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createTestSandbox(t, tmpDir, "test-stop", "/tmp/project", "copy")

	stopCalled := false
	mock := &lifecycleMockClient{
		containerStopFn: func(_ context.Context, _ string, _ container.StopOptions) error {
			stopCalled = true
			return nil
		},
	}

	mgr := newLifecycleMgr(mock)
	err := mgr.Stop(context.Background(), "test-stop")
	require.NoError(t, err)
	assert.True(t, stopCalled)
}

func TestStop_AlreadyStopped(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createTestSandbox(t, tmpDir, "test-stop-already", "/tmp/project", "copy")

	mock := &lifecycleMockClient{
		containerStopFn: func(_ context.Context, _ string, _ container.StopOptions) error {
			return fmt.Errorf("conflict: %w", cerrdefs.ErrConflict)
		},
	}

	mgr := newLifecycleMgr(mock)
	err := mgr.Stop(context.Background(), "test-stop-already")
	assert.NoError(t, err) // idempotent
}

func TestStop_ContainerRemoved(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createTestSandbox(t, tmpDir, "test-stop-removed", "/tmp/project", "copy")

	mock := &lifecycleMockClient{
		containerStopFn: func(_ context.Context, _ string, _ container.StopOptions) error {
			return fmt.Errorf("not found: %w", cerrdefs.ErrNotFound)
		},
	}

	mgr := newLifecycleMgr(mock)
	err := mgr.Stop(context.Background(), "test-stop-removed")
	assert.NoError(t, err) // idempotent
}

func TestStop_SandboxNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	mock := &lifecycleMockClient{}
	mgr := newLifecycleMgr(mock)
	err := mgr.Stop(context.Background(), "nonexistent")
	assert.ErrorIs(t, err, ErrSandboxNotFound)
}

// Start tests

func TestStart_AlreadyRunning(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createTestSandbox(t, tmpDir, "test-start-running", "/tmp/project", "copy")

	mock := &lifecycleMockClient{
		containerInspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{
				ContainerJSONBase: &container.ContainerJSONBase{
					ID:    "abc123def456",
					State: &container.State{Running: true},
				},
			}, nil
		},
	}
	// Mock exec for tmux pane detection — return "running" (pane alive)
	mock.mockClient = mockClient{}

	var output bytes.Buffer
	mgr := NewManager(mock, slog.Default(), strings.NewReader(""), &output)

	// DetectStatus will call ContainerInspect (running=true),
	// then try execInContainer for tmux. Since our mock returns errMockNotImplemented
	// for exec, DetectStatus defaults to StatusRunning.
	err := mgr.Start(context.Background(), "test-start-running")
	require.NoError(t, err)
	assert.Contains(t, output.String(), "already running")
}

func TestStart_Stopped(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createTestSandbox(t, tmpDir, "test-start-stopped", "/tmp/project", "copy")

	startCalled := false
	mock := &lifecycleMockClient{
		containerInspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			running := startCalled // after start, report as running
			return container.InspectResponse{
				ContainerJSONBase: &container.ContainerJSONBase{
					ID:    "abc123def456",
					State: &container.State{Running: running},
				},
			}, nil
		},
		containerStartFn: func(_ context.Context, _ string, _ container.StartOptions) error {
			startCalled = true
			return nil
		},
	}

	var output bytes.Buffer
	mgr := NewManager(mock, slog.Default(), strings.NewReader(""), &output)

	err := mgr.Start(context.Background(), "test-start-stopped")
	require.NoError(t, err)
	assert.True(t, startCalled)
	assert.Contains(t, output.String(), "started")
}

func TestStart_SandboxNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	mock := &lifecycleMockClient{}
	mgr := newLifecycleMgr(mock)
	err := mgr.Start(context.Background(), "nonexistent")
	assert.ErrorIs(t, err, ErrSandboxNotFound)
}

func TestStart_Removed(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createTestSandbox(t, tmpDir, "test-start-removed", "/tmp/project", "copy")

	mock := &lifecycleMockClient{
		containerInspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{}, fmt.Errorf("not found: %w", cerrdefs.ErrNotFound)
		},
	}

	mgr := newLifecycleMgr(mock)

	// recreateContainer will fail because there's no config.json,
	// but we're testing that Start routes to recreateContainer for StatusRemoved.
	err := mgr.Start(context.Background(), "test-start-removed")
	assert.Error(t, err)
	// Should be a recreateContainer error (config.json missing), not a routing error
	assert.Contains(t, err.Error(), "config.json")
}

// NeedsConfirmation tests

func TestNeedsConfirmation_Running(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createTestSandbox(t, tmpDir, "test-confirm-running", "/tmp/project", "copy")

	mock := &lifecycleMockClient{
		containerInspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{
				ContainerJSONBase: &container.ContainerJSONBase{
					ID:    "abc123def456",
					State: &container.State{Running: true},
				},
			}, nil
		},
	}

	mgr := newLifecycleMgr(mock)
	needs, reason := mgr.NeedsConfirmation(context.Background(), "test-confirm-running")
	assert.True(t, needs)
	assert.Equal(t, "agent is still running", reason)
}

func TestNeedsConfirmation_ChangesExist(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Create sandbox with a work directory that has changes
	name := "test-confirm-changes"
	hostPath := "/tmp/project"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	workDir := filepath.Join(sandboxDir, "work", EncodePath(hostPath))
	require.NoError(t, os.MkdirAll(workDir, 0750))

	initGitRepo(t, workDir)
	writeTestFile(t, workDir, "file.txt", "original")
	gitAdd(t, workDir, ".")
	gitCommit(t, workDir, "initial")

	meta := &Meta{
		Name:      name,
		Agent:     "claude",
		CreatedAt: time.Now(),
		Workdir: WorkdirMeta{
			HostPath:  hostPath,
			MountPath: hostPath,
			Mode:      "copy",
		},
	}
	require.NoError(t, SaveMeta(sandboxDir, meta))

	// Make changes in work dir
	writeTestFile(t, workDir, "file.txt", "modified")

	mock := &lifecycleMockClient{
		containerInspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			// Container not running
			return container.InspectResponse{
				ContainerJSONBase: &container.ContainerJSONBase{
					ID:    "abc123def456",
					State: &container.State{Running: false},
				},
			}, nil
		},
	}

	mgr := newLifecycleMgr(mock)
	needs, reason := mgr.NeedsConfirmation(context.Background(), "test-confirm-changes")
	assert.True(t, needs)
	assert.Equal(t, "unapplied changes exist", reason)
}

func TestNeedsConfirmation_NoChanges(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Create sandbox with clean work directory
	name := "test-confirm-clean"
	hostPath := "/tmp/project"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	workDir := filepath.Join(sandboxDir, "work", EncodePath(hostPath))
	require.NoError(t, os.MkdirAll(workDir, 0750))

	initGitRepo(t, workDir)
	writeTestFile(t, workDir, "file.txt", "content")
	gitAdd(t, workDir, ".")
	gitCommit(t, workDir, "initial")

	meta := &Meta{
		Name:      name,
		Agent:     "claude",
		CreatedAt: time.Now(),
		Workdir: WorkdirMeta{
			HostPath:  hostPath,
			MountPath: hostPath,
			Mode:      "copy",
		},
	}
	require.NoError(t, SaveMeta(sandboxDir, meta))

	mock := &lifecycleMockClient{
		containerInspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{
				ContainerJSONBase: &container.ContainerJSONBase{
					ID:    "abc123def456",
					State: &container.State{Running: false},
				},
			}, nil
		},
	}

	mgr := newLifecycleMgr(mock)
	needs, reason := mgr.NeedsConfirmation(context.Background(), "test-confirm-clean")
	assert.False(t, needs)
	assert.Empty(t, reason)
}

// Destroy tests

func TestDestroy_RemovesDir(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createTestSandbox(t, tmpDir, "test-destroy", "/tmp/project", "copy")

	mock := &lifecycleMockClient{}
	mgr := newLifecycleMgr(mock)

	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", "test-destroy")
	assert.DirExists(t, sandboxDir)

	err := mgr.Destroy(context.Background(), "test-destroy", true)
	require.NoError(t, err)
	assert.NoDirExists(t, sandboxDir)
}

func TestDestroy_SandboxNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	mock := &lifecycleMockClient{}
	mgr := newLifecycleMgr(mock)
	err := mgr.Destroy(context.Background(), "nonexistent", true)
	assert.ErrorIs(t, err, ErrSandboxNotFound)
}

// Reset tests

func TestReset_RecopiesWorkdir(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Create original source directory
	origDir := filepath.Join(tmpDir, "original")
	require.NoError(t, os.MkdirAll(origDir, 0750))
	writeTestFile(t, origDir, "file.txt", "original content\n")

	// Create sandbox with work copy
	name := "test-reset"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	workDir := filepath.Join(sandboxDir, "work", EncodePath(origDir))
	require.NoError(t, os.MkdirAll(workDir, 0750))

	// Copy original to work dir and create baseline
	writeTestFile(t, workDir, "file.txt", "original content\n")
	initGitRepo(t, workDir)
	gitAdd(t, workDir, ".")
	gitCommit(t, workDir, "yoloai baseline")
	sha := gitHEAD(t, workDir)

	meta := &Meta{
		Name:      name,
		Agent:     "claude",
		CreatedAt: time.Now(),
		Workdir: WorkdirMeta{
			HostPath:    origDir,
			MountPath:   origDir,
			Mode:        "copy",
			BaselineSHA: sha,
		},
	}
	require.NoError(t, SaveMeta(sandboxDir, meta))

	// Modify work copy
	writeTestFile(t, workDir, "file.txt", "modified by agent\n")

	// Reset needs Start to work, which needs Docker. Stop will also
	// try Docker. Since we just want to test the re-copy logic,
	// mock everything to succeed/no-op.
	mock := &lifecycleMockClient{
		// Stop: already stopped
		containerStopFn: func(_ context.Context, _ string, _ container.StopOptions) error {
			return fmt.Errorf("not found: %w", cerrdefs.ErrNotFound)
		},
		// Start: container removed, recreate will fail (no config.json), but
		// we still check the re-copy happened
		containerInspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{}, fmt.Errorf("not found: %w", cerrdefs.ErrNotFound)
		},
	}

	mgr := newLifecycleMgr(mock)

	// Reset will re-copy and re-baseline, then fail at Start (recreateContainer
	// needs config.json). That's OK — we verify the re-copy happened.
	_ = mgr.Reset(context.Background(), ResetOptions{Name: name})

	// Verify work copy was re-copied from original
	content, err := os.ReadFile(filepath.Join(workDir, "file.txt")) //nolint:gosec // G304: test file path
	require.NoError(t, err)
	assert.Equal(t, "original content\n", string(content))

	// Verify new baseline SHA in meta
	updatedMeta, err := LoadMeta(sandboxDir)
	require.NoError(t, err)
	assert.NotEmpty(t, updatedMeta.Workdir.BaselineSHA)
	assert.NotEqual(t, sha, updatedMeta.Workdir.BaselineSHA) // new baseline
}

func TestReset_Clean(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	origDir := filepath.Join(tmpDir, "original")
	require.NoError(t, os.MkdirAll(origDir, 0750))
	writeTestFile(t, origDir, "file.txt", "content\n")

	name := "test-reset-clean"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	workDir := filepath.Join(sandboxDir, "work", EncodePath(origDir))
	agentStateDir := filepath.Join(sandboxDir, "agent-state")
	require.NoError(t, os.MkdirAll(workDir, 0750))
	require.NoError(t, os.MkdirAll(agentStateDir, 0750))

	// Add content to agent-state
	writeTestFile(t, agentStateDir, "session.json", `{"key":"value"}`)

	writeTestFile(t, workDir, "file.txt", "content\n")
	initGitRepo(t, workDir)
	gitAdd(t, workDir, ".")
	gitCommit(t, workDir, "yoloai baseline")
	sha := gitHEAD(t, workDir)

	meta := &Meta{
		Name:      name,
		Agent:     "claude",
		CreatedAt: time.Now(),
		Workdir: WorkdirMeta{
			HostPath:    origDir,
			MountPath:   origDir,
			Mode:        "copy",
			BaselineSHA: sha,
		},
	}
	require.NoError(t, SaveMeta(sandboxDir, meta))

	mock := &lifecycleMockClient{
		containerStopFn: func(_ context.Context, _ string, _ container.StopOptions) error {
			return fmt.Errorf("not found: %w", cerrdefs.ErrNotFound)
		},
		containerInspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{}, fmt.Errorf("not found: %w", cerrdefs.ErrNotFound)
		},
	}

	mgr := newLifecycleMgr(mock)
	_ = mgr.Reset(context.Background(), ResetOptions{Name: name, Clean: true})

	// agent-state dir should exist but be empty
	assert.DirExists(t, agentStateDir)
	entries, err := os.ReadDir(agentStateDir)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestReset_RWMode_Error(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	hostDir := filepath.Join(tmpDir, "rw-dir")
	require.NoError(t, os.MkdirAll(hostDir, 0750))
	createRWSandbox(t, tmpDir, "test-reset-rw", hostDir)

	mock := &lifecycleMockClient{}
	mgr := newLifecycleMgr(mock)

	err := mgr.Reset(context.Background(), ResetOptions{Name: "test-reset-rw"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), ":rw directories")
}

func TestReset_OriginalMissing(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Create original dir, then delete it
	origDir := filepath.Join(tmpDir, "vanished")
	require.NoError(t, os.MkdirAll(origDir, 0750))
	writeTestFile(t, origDir, "file.txt", "content\n")

	name := "test-reset-missing"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	workDir := filepath.Join(sandboxDir, "work", EncodePath(origDir))
	require.NoError(t, os.MkdirAll(workDir, 0750))

	writeTestFile(t, workDir, "file.txt", "content\n")
	initGitRepo(t, workDir)
	gitAdd(t, workDir, ".")
	gitCommit(t, workDir, "yoloai baseline")
	sha := gitHEAD(t, workDir)

	meta := &Meta{
		Name:      name,
		Agent:     "claude",
		CreatedAt: time.Now(),
		Workdir: WorkdirMeta{
			HostPath:    origDir,
			MountPath:   origDir,
			Mode:        "copy",
			BaselineSHA: sha,
		},
	}
	require.NoError(t, SaveMeta(sandboxDir, meta))

	// Delete the original
	require.NoError(t, os.RemoveAll(origDir))

	mock := &lifecycleMockClient{
		containerStopFn: func(_ context.Context, _ string, _ container.StopOptions) error {
			return fmt.Errorf("not found: %w", cerrdefs.ErrNotFound)
		},
	}

	mgr := newLifecycleMgr(mock)
	err := mgr.Reset(context.Background(), ResetOptions{Name: name})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "original directory no longer exists")
}

// --no-restart tests

func TestReset_NoRestart_SyncsWorkdir(t *testing.T) {
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("rsync not installed")
	}

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Create original source directory
	origDir := filepath.Join(tmpDir, "original")
	require.NoError(t, os.MkdirAll(origDir, 0750))
	writeTestFile(t, origDir, "file.txt", "original content\n")

	// Create sandbox with work copy
	name := "test-reset-norestart"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	workDir := filepath.Join(sandboxDir, "work", EncodePath(origDir))
	require.NoError(t, os.MkdirAll(workDir, 0750))

	// Set up work copy with baseline
	writeTestFile(t, workDir, "file.txt", "original content\n")
	initGitRepo(t, workDir)
	gitAdd(t, workDir, ".")
	gitCommit(t, workDir, "yoloai baseline")
	sha := gitHEAD(t, workDir)

	meta := &Meta{
		Name:      name,
		Agent:     "claude",
		HasPrompt: true,
		CreatedAt: time.Now(),
		Workdir: WorkdirMeta{
			HostPath:    origDir,
			MountPath:   origDir,
			Mode:        "copy",
			BaselineSHA: sha,
		},
	}
	require.NoError(t, SaveMeta(sandboxDir, meta))

	// Simulate agent changes in work copy
	writeTestFile(t, workDir, "file.txt", "modified by agent\n")
	writeTestFile(t, workDir, "agent-new.txt", "agent created this\n")

	// Simulate upstream changes in original
	writeTestFile(t, origDir, "file.txt", "updated upstream\n")
	writeTestFile(t, origDir, "upstream-new.txt", "new upstream file\n")

	// Mock: container is running
	mock := &lifecycleMockClient{
		containerInspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{
				ContainerJSONBase: &container.ContainerJSONBase{
					ID:    "abc123def456",
					State: &container.State{Running: true},
				},
			}, nil
		},
	}

	mgr := newLifecycleMgr(mock)

	// Reset with --no-restart. sendResetNotification will fail (no config.json
	// and exec mock not wired), but workspace sync and baseline should succeed.
	_ = mgr.Reset(context.Background(), ResetOptions{Name: name, NoRestart: true})

	// Verify work copy was synced from updated original
	content, err := os.ReadFile(filepath.Join(workDir, "file.txt")) //nolint:gosec // test
	require.NoError(t, err)
	assert.Equal(t, "updated upstream\n", string(content))

	// Verify new upstream file was synced
	content, err = os.ReadFile(filepath.Join(workDir, "upstream-new.txt")) //nolint:gosec // test
	require.NoError(t, err)
	assert.Equal(t, "new upstream file\n", string(content))

	// Verify agent-created file was removed (rsync --delete)
	assert.NoFileExists(t, filepath.Join(workDir, "agent-new.txt"))

	// Verify new baseline SHA in meta
	updatedMeta, err := LoadMeta(sandboxDir)
	require.NoError(t, err)
	assert.NotEmpty(t, updatedMeta.Workdir.BaselineSHA)
	assert.NotEqual(t, sha, updatedMeta.Workdir.BaselineSHA)
}

func TestReset_NoRestart_FallsBackWhenNotRunning(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Create original source directory
	origDir := filepath.Join(tmpDir, "original")
	require.NoError(t, os.MkdirAll(origDir, 0750))
	writeTestFile(t, origDir, "file.txt", "original content\n")

	// Create sandbox with work copy
	name := "test-reset-norestart-fallback"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	workDir := filepath.Join(sandboxDir, "work", EncodePath(origDir))
	require.NoError(t, os.MkdirAll(workDir, 0750))

	writeTestFile(t, workDir, "file.txt", "original content\n")
	initGitRepo(t, workDir)
	gitAdd(t, workDir, ".")
	gitCommit(t, workDir, "yoloai baseline")
	sha := gitHEAD(t, workDir)

	meta := &Meta{
		Name:      name,
		Agent:     "claude",
		CreatedAt: time.Now(),
		Workdir: WorkdirMeta{
			HostPath:    origDir,
			MountPath:   origDir,
			Mode:        "copy",
			BaselineSHA: sha,
		},
	}
	require.NoError(t, SaveMeta(sandboxDir, meta))

	// Modify work copy
	writeTestFile(t, workDir, "file.txt", "modified by agent\n")

	// Mock: container not found (removed)
	mock := &lifecycleMockClient{
		containerStopFn: func(_ context.Context, _ string, _ container.StopOptions) error {
			return fmt.Errorf("not found: %w", cerrdefs.ErrNotFound)
		},
		containerInspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{}, fmt.Errorf("not found: %w", cerrdefs.ErrNotFound)
		},
	}

	var output bytes.Buffer
	mgr := NewManager(mock, slog.Default(), strings.NewReader(""), &output)

	// Reset with --no-restart; container not running → falls back to default path.
	// Default path will fail at Start (no config.json), but re-copy should happen.
	_ = mgr.Reset(context.Background(), ResetOptions{Name: name, NoRestart: true})

	// Verify fallback message was printed
	assert.Contains(t, output.String(), "Container is not running, falling back to restart")

	// Verify work copy was re-copied from original (default reset behavior)
	content, err := os.ReadFile(filepath.Join(workDir, "file.txt")) //nolint:gosec // test
	require.NoError(t, err)
	assert.Equal(t, "original content\n", string(content))

	// Verify new baseline SHA in meta
	updatedMeta, err := LoadMeta(sandboxDir)
	require.NoError(t, err)
	assert.NotEmpty(t, updatedMeta.Workdir.BaselineSHA)
	assert.NotEqual(t, sha, updatedMeta.Workdir.BaselineSHA)
}

package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/runtime"
)

// lifecycleMockRuntime extends mockRuntime for lifecycle tests.
type lifecycleMockRuntime struct {
	mockRuntime
	stopFn    func(ctx context.Context, name string) error
	startFn   func(ctx context.Context, name string) error
	removeFn  func(ctx context.Context, name string) error
	inspectFn func(ctx context.Context, name string) (runtime.InstanceInfo, error)
	execFn    func(ctx context.Context, name string, cmd []string, user string) (runtime.ExecResult, error)
}

func (m *lifecycleMockRuntime) Stop(ctx context.Context, name string) error {
	if m.stopFn != nil {
		return m.stopFn(ctx, name)
	}
	return nil
}

func (m *lifecycleMockRuntime) Start(ctx context.Context, name string) error {
	if m.startFn != nil {
		return m.startFn(ctx, name)
	}
	return nil
}

func (m *lifecycleMockRuntime) Remove(ctx context.Context, name string) error {
	if m.removeFn != nil {
		return m.removeFn(ctx, name)
	}
	return nil
}

func (m *lifecycleMockRuntime) Inspect(ctx context.Context, name string) (runtime.InstanceInfo, error) {
	if m.inspectFn != nil {
		return m.inspectFn(ctx, name)
	}
	return runtime.InstanceInfo{}, errMockNotImplemented
}

func (m *lifecycleMockRuntime) Exec(ctx context.Context, name string, cmd []string, user string) (runtime.ExecResult, error) {
	if m.execFn != nil {
		return m.execFn(ctx, name, cmd, user)
	}
	return m.mockRuntime.Exec(ctx, name, cmd, user)
}

// newLifecycleMgr creates a Manager with the given mock runtime and a discard output.
func newLifecycleMgr(rt *lifecycleMockRuntime) *Manager {
	return NewManager(rt, slog.Default(), strings.NewReader(""), io.Discard)
}

// createTestSandbox creates a sandbox directory with environment.json for lifecycle tests.
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
	mock := &lifecycleMockRuntime{
		stopFn: func(_ context.Context, _ string) error {
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

	// Runtime.Stop returns nil when already stopped (contract guarantee).
	mock := &lifecycleMockRuntime{
		stopFn: func(_ context.Context, _ string) error {
			return nil
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

	// Runtime.Stop returns nil when instance not found (contract guarantee).
	mock := &lifecycleMockRuntime{
		stopFn: func(_ context.Context, _ string) error {
			return nil
		},
	}

	mgr := newLifecycleMgr(mock)
	err := mgr.Stop(context.Background(), "test-stop-removed")
	assert.NoError(t, err) // idempotent
}

func TestStop_SandboxNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	mock := &lifecycleMockRuntime{}
	mgr := newLifecycleMgr(mock)
	err := mgr.Stop(context.Background(), "nonexistent")
	assert.ErrorIs(t, err, ErrSandboxNotFound)
}

// Start tests

func TestStart_AlreadyRunning(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createTestSandbox(t, tmpDir, "test-start-running", "/tmp/project", "copy")

	mock := &lifecycleMockRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{Running: true}, nil
		},
	}

	var output bytes.Buffer
	mgr := NewManager(mock, slog.Default(), strings.NewReader(""), &output)

	// DetectStatus will call Inspect (running=true),
	// then try Exec for tmux. Since our mock returns errMockNotImplemented
	// for exec, DetectStatus defaults to StatusActive.
	err := mgr.Start(context.Background(), "test-start-running", StartOpts{})
	require.NoError(t, err)
	assert.Contains(t, output.String(), "already running")
}

func TestStart_Stopped(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createTestSandbox(t, tmpDir, "test-start-stopped", "/tmp/project", "copy")

	removeCalled := false
	mock := &lifecycleMockRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{Running: false}, nil
		},
		removeFn: func(_ context.Context, _ string) error {
			removeCalled = true
			return nil
		},
	}

	mgr := newLifecycleMgr(mock)

	// After remove, Start routes to recreateContainer which fails
	// (no runtime-config.json) — same pattern as TestStart_Removed.
	err := mgr.Start(context.Background(), "test-start-stopped", StartOpts{})
	assert.Error(t, err)
	assert.True(t, removeCalled, "should remove stopped container before recreating")
	assert.Contains(t, err.Error(), RuntimeConfigFile)
}

func TestStart_SandboxNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	mock := &lifecycleMockRuntime{}
	mgr := newLifecycleMgr(mock)
	err := mgr.Start(context.Background(), "nonexistent", StartOpts{})
	assert.ErrorIs(t, err, ErrSandboxNotFound)
}

func TestStart_Removed(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createTestSandbox(t, tmpDir, "test-start-removed", "/tmp/project", "copy")

	mock := &lifecycleMockRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{}, fmt.Errorf("not found: %w", runtime.ErrNotFound)
		},
	}

	mgr := newLifecycleMgr(mock)

	// recreateContainer will fail because there's no runtime-config.json,
	// but we're testing that Start routes to recreateContainer for StatusRemoved.
	err := mgr.Start(context.Background(), "test-start-removed", StartOpts{})
	assert.Error(t, err)
	// Should be a recreateContainer error (runtime-config.json missing), not a routing error
	assert.Contains(t, err.Error(), RuntimeConfigFile)
}

func TestStart_Resume_RequiresPrompt(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Create sandbox WITHOUT HasPrompt
	createTestSandbox(t, tmpDir, "test-resume-noprompt", "/tmp/project", "copy")

	mock := &lifecycleMockRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{Running: false}, nil
		},
	}

	mgr := newLifecycleMgr(mock)
	err := mgr.Start(context.Background(), "test-resume-noprompt", StartOpts{Resume: true})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "--resume requires a sandbox created with --prompt")
}

func TestStart_Resume_DoneStatus(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	name := "test-resume-done"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	// Create meta with HasPrompt=true
	meta := &Meta{
		Name:      name,
		Agent:     "claude",
		HasPrompt: true,
		CreatedAt: time.Now(),
		Workdir: WorkdirMeta{
			HostPath:  "/tmp/project",
			MountPath: "/tmp/project",
			Mode:      "copy",
		},
	}
	require.NoError(t, SaveMeta(sandboxDir, meta))

	// Write prompt.txt
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, "prompt.txt"), []byte("Write hello world"), 0600))

	// Write runtime-config.json
	cfg := containerConfig{
		AgentCommand:   "claude --dangerously-skip-permissions --print \"Write hello world\"",
		SubmitSequence: "Enter",
		ReadyPattern:   "> $",
	}
	cfgData, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, RuntimeConfigFile), cfgData, 0600))

	// Write agent-status.json indicating done (exit code 0)
	statusData := fmt.Sprintf(`{"status":"done","exit_code":0,"timestamp":%d}`, time.Now().Unix())
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, AgentStatusFile), []byte(statusData), 0600))

	// Track exec calls
	var execCalls [][]string
	mock := &lifecycleMockRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{Running: true}, nil
		},
	}
	// Override Exec to capture calls
	mock.execFn = func(_ context.Context, _ string, cmd []string, _ string) (runtime.ExecResult, error) {
		execCalls = append(execCalls, cmd)
		// respawn-pane will succeed
		if len(cmd) > 0 && cmd[0] == "tmux" && len(cmd) > 1 && cmd[1] == "respawn-pane" {
			return runtime.ExecResult{}, nil
		}
		// Other tmux commands (wait for ready, etc.) may fail but that's OK
		return runtime.ExecResult{ExitCode: 1}, fmt.Errorf("mock error")
	}

	mgr := newLifecycleMgr(mock)
	_ = mgr.Start(context.Background(), name, StartOpts{Resume: true})
	// The sendResumePrompt exec might fail but the respawn should have happened
	// We just check that the respawn used interactive command (no headless prompt)

	// Find the respawn-pane exec call
	foundRespawn := false
	for _, call := range execCalls {
		if len(call) >= 5 && call[0] == "tmux" && call[1] == "respawn-pane" {
			foundRespawn = true
			// The command should be the interactive version (no "PROMPT" substitution)
			agentCmd := call[5]
			assert.NotContains(t, agentCmd, "Write hello world", "resume should use interactive command, not headless")
			assert.Contains(t, agentCmd, "claude", "command should contain agent name")
		}
	}
	assert.True(t, foundRespawn, "should have called tmux respawn-pane")
}

func TestStart_Resume_StoppedStatus(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	name := "test-resume-stopped"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	meta := &Meta{
		Name:      name,
		Agent:     "claude",
		HasPrompt: true,
		CreatedAt: time.Now(),
		Workdir: WorkdirMeta{
			HostPath:  "/tmp/project",
			MountPath: "/tmp/project",
			Mode:      "copy",
		},
	}
	require.NoError(t, SaveMeta(sandboxDir, meta))

	// Write prompt.txt
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, "prompt.txt"), []byte("Write hello world"), 0600))

	// Write runtime-config.json with headless command
	cfg := containerConfig{
		AgentCommand:   "claude --dangerously-skip-permissions --print \"Write hello world\"",
		SubmitSequence: "Enter",
		ReadyPattern:   "> $",
	}
	cfgData, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, RuntimeConfigFile), cfgData, 0600))

	mock := &lifecycleMockRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			// Container exists but stopped
			return runtime.InstanceInfo{Running: false}, nil
		},
		removeFn: func(_ context.Context, _ string) error {
			return nil
		},
	}

	mgr := newLifecycleMgr(mock)

	// Start with resume will call prepareResumeFiles then recreateContainer.
	// recreateContainer will fail (no work dir, no secrets etc.) but we can check
	// that resume-prompt.txt was created and runtime-config.json was patched.
	_ = mgr.Start(context.Background(), name, StartOpts{Resume: true})

	// Verify runtime-config.json was patched to interactive command
	updatedCfgData, err := os.ReadFile(filepath.Join(sandboxDir, RuntimeConfigFile)) //nolint:gosec // test file in controlled temp dir
	require.NoError(t, err)
	var updatedCfg containerConfig
	require.NoError(t, json.Unmarshal(updatedCfgData, &updatedCfg))
	assert.NotContains(t, updatedCfg.AgentCommand, "Write hello world",
		"runtime-config.json should have interactive command after resume prep")
	assert.Contains(t, updatedCfg.AgentCommand, "claude",
		"runtime-config.json should still reference the agent")

	// resume-prompt.txt is cleaned up by defer, so it may not exist anymore.
	// But we can verify the runtime-config.json patch was permanent (design spec says
	// interactive command is correct for future starts).
}

// NeedsConfirmation tests

func TestNeedsConfirmation_Running(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createTestSandbox(t, tmpDir, "test-confirm-running", "/tmp/project", "copy")

	mock := &lifecycleMockRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{Running: true}, nil
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

	mock := &lifecycleMockRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			// Container not running
			return runtime.InstanceInfo{Running: false}, nil
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

	mock := &lifecycleMockRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{Running: false}, nil
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

	mock := &lifecycleMockRuntime{}
	mgr := newLifecycleMgr(mock)

	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", "test-destroy")
	assert.DirExists(t, sandboxDir)

	err := mgr.Destroy(context.Background(), "test-destroy")
	require.NoError(t, err)
	assert.NoDirExists(t, sandboxDir)
}

func TestDestroy_SandboxNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	mock := &lifecycleMockRuntime{}
	mgr := newLifecycleMgr(mock)
	err := mgr.Destroy(context.Background(), "nonexistent")
	assert.NoError(t, err)
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

	// Create cache and files dirs with content (should be cleared by default)
	cacheDir := filepath.Join(sandboxDir, "cache")
	filesDir := filepath.Join(sandboxDir, "files")
	require.NoError(t, os.MkdirAll(cacheDir, 0750))
	require.NoError(t, os.MkdirAll(filesDir, 0750))
	writeTestFile(t, cacheDir, "cached.txt", "cached data\n")
	writeTestFile(t, filesDir, "shared.txt", "shared data\n")

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

	// Container not running → auto-upgrades to restart.
	// Stop/Start will eventually fail (no runtime-config.json), but
	// we still check the re-copy happened.
	mock := &lifecycleMockRuntime{
		stopFn: func(_ context.Context, _ string) error {
			return nil
		},
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{}, fmt.Errorf("not found: %w", runtime.ErrNotFound)
		},
	}

	mgr := newLifecycleMgr(mock)

	// Reset will re-copy and re-baseline, then fail at Start (recreateContainer
	// needs runtime-config.json). That's OK — we verify the re-copy happened.
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

	// Verify cache and files were cleared (default behavior)
	assert.NoFileExists(t, filepath.Join(cacheDir, "cached.txt"))
	assert.NoFileExists(t, filepath.Join(filesDir, "shared.txt"))
	assert.DirExists(t, cacheDir)
	assert.DirExists(t, filesDir)
}

func TestReset_State(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	origDir := filepath.Join(tmpDir, "original")
	require.NoError(t, os.MkdirAll(origDir, 0750))
	writeTestFile(t, origDir, "file.txt", "content\n")

	name := "test-reset-state"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	workDir := filepath.Join(sandboxDir, "work", EncodePath(origDir))
	agentStateDir := filepath.Join(sandboxDir, AgentRuntimeDir)
	require.NoError(t, os.MkdirAll(workDir, 0750))
	require.NoError(t, os.MkdirAll(agentStateDir, 0750))

	// Add content to agent-runtime
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

	mock := &lifecycleMockRuntime{
		stopFn: func(_ context.Context, _ string) error {
			return nil
		},
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{}, fmt.Errorf("not found: %w", runtime.ErrNotFound)
		},
	}

	mgr := newLifecycleMgr(mock)
	// --state implies --restart
	_ = mgr.Reset(context.Background(), ResetOptions{Name: name, ClearState: true})

	// agent-runtime dir should exist with only settings.json (re-applied by
	// ensureContainerSettings after clean wipe)
	assert.DirExists(t, agentStateDir)
	entries, err := os.ReadDir(agentStateDir)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	assert.Equal(t, []string{"settings.json"}, names)
}

func TestReset_RWMode_Error(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	hostDir := filepath.Join(tmpDir, "rw-dir")
	require.NoError(t, os.MkdirAll(hostDir, 0750))
	createRWSandbox(t, tmpDir, "test-reset-rw", hostDir)

	mock := &lifecycleMockRuntime{}
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

	mock := &lifecycleMockRuntime{
		stopFn: func(_ context.Context, _ string) error {
			return nil
		},
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{}, fmt.Errorf("not found: %w", runtime.ErrNotFound)
		},
	}

	mgr := newLifecycleMgr(mock)
	// Container not running → auto-upgrades to restart
	err := mgr.Reset(context.Background(), ResetOptions{Name: name})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "original directory no longer exists")
}

// In-place reset tests (default behavior when container is running)

func TestReset_InPlace_SyncsWorkdir(t *testing.T) {
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
	name := "test-reset-inplace"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	workDir := filepath.Join(sandboxDir, "work", EncodePath(origDir))
	require.NoError(t, os.MkdirAll(workDir, 0750))

	// Create cache and files dirs with content
	cacheDir := filepath.Join(sandboxDir, "cache")
	filesDir := filepath.Join(sandboxDir, "files")
	require.NoError(t, os.MkdirAll(cacheDir, 0750))
	require.NoError(t, os.MkdirAll(filesDir, 0750))
	writeTestFile(t, cacheDir, "cached.txt", "cached data\n")
	writeTestFile(t, filesDir, "shared.txt", "shared data\n")

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
	mock := &lifecycleMockRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{Running: true}, nil
		},
	}

	mgr := newLifecycleMgr(mock)

	// Default reset (in-place). sendResetNotification will fail (no runtime-config.json
	// and exec mock not wired), but workspace sync and baseline should succeed.
	_ = mgr.Reset(context.Background(), ResetOptions{Name: name})

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

	// Verify cache and files were cleared (default behavior)
	assert.NoFileExists(t, filepath.Join(cacheDir, "cached.txt"))
	assert.NoFileExists(t, filepath.Join(filesDir, "shared.txt"))
	// Directories should still exist (recreated)
	assert.DirExists(t, cacheDir)
	assert.DirExists(t, filesDir)
}

func TestReset_InPlace_KeepCache(t *testing.T) {
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("rsync not installed")
	}

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	origDir := filepath.Join(tmpDir, "original")
	require.NoError(t, os.MkdirAll(origDir, 0750))
	writeTestFile(t, origDir, "file.txt", "content\n")

	name := "test-reset-keep-cache"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	workDir := filepath.Join(sandboxDir, "work", EncodePath(origDir))
	require.NoError(t, os.MkdirAll(workDir, 0750))

	// Create cache and files dirs with content
	cacheDir := filepath.Join(sandboxDir, "cache")
	filesDir := filepath.Join(sandboxDir, "files")
	require.NoError(t, os.MkdirAll(cacheDir, 0750))
	require.NoError(t, os.MkdirAll(filesDir, 0750))
	writeTestFile(t, cacheDir, "cached.txt", "cached data\n")
	writeTestFile(t, filesDir, "shared.txt", "shared data\n")

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

	mock := &lifecycleMockRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{Running: true}, nil
		},
	}

	mgr := newLifecycleMgr(mock)
	_ = mgr.Reset(context.Background(), ResetOptions{Name: name, KeepCache: true})

	// Cache should be preserved
	assert.FileExists(t, filepath.Join(cacheDir, "cached.txt"))
	// Files dir should be cleared
	assert.NoFileExists(t, filepath.Join(filesDir, "shared.txt"))
	assert.DirExists(t, filesDir)
}

func TestReset_InPlace_KeepFiles(t *testing.T) {
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("rsync not installed")
	}

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	origDir := filepath.Join(tmpDir, "original")
	require.NoError(t, os.MkdirAll(origDir, 0750))
	writeTestFile(t, origDir, "file.txt", "content\n")

	name := "test-reset-keep-files"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	workDir := filepath.Join(sandboxDir, "work", EncodePath(origDir))
	require.NoError(t, os.MkdirAll(workDir, 0750))

	// Create cache and files dirs with content
	cacheDir := filepath.Join(sandboxDir, "cache")
	filesDir := filepath.Join(sandboxDir, "files")
	require.NoError(t, os.MkdirAll(cacheDir, 0750))
	require.NoError(t, os.MkdirAll(filesDir, 0750))
	writeTestFile(t, cacheDir, "cached.txt", "cached data\n")
	writeTestFile(t, filesDir, "shared.txt", "shared data\n")

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

	mock := &lifecycleMockRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{Running: true}, nil
		},
	}

	mgr := newLifecycleMgr(mock)
	_ = mgr.Reset(context.Background(), ResetOptions{Name: name, KeepFiles: true})

	// Cache should be cleared
	assert.NoFileExists(t, filepath.Join(cacheDir, "cached.txt"))
	assert.DirExists(t, cacheDir)
	// Files dir should be preserved
	assert.FileExists(t, filepath.Join(filesDir, "shared.txt"))
}

func TestReset_UpgradesToRestartWhenNotRunning(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Create original source directory
	origDir := filepath.Join(tmpDir, "original")
	require.NoError(t, os.MkdirAll(origDir, 0750))
	writeTestFile(t, origDir, "file.txt", "original content\n")

	// Create sandbox with work copy
	name := "test-reset-upgrade-restart"
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
	mock := &lifecycleMockRuntime{
		stopFn: func(_ context.Context, _ string) error {
			return nil
		},
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{}, fmt.Errorf("not found: %w", runtime.ErrNotFound)
		},
	}

	var output bytes.Buffer
	mgr := NewManager(mock, slog.Default(), strings.NewReader(""), &output)

	// Default reset; container not running → auto-upgrades to restart.
	// Restart path will fail at Start (no runtime-config.json), but re-copy should happen.
	_ = mgr.Reset(context.Background(), ResetOptions{Name: name})

	// Verify upgrade message was printed
	assert.Contains(t, output.String(), "Container is not running, upgrading to restart")

	// Verify work copy was re-copied from original (restart behavior)
	content, err := os.ReadFile(filepath.Join(workDir, "file.txt")) //nolint:gosec // test
	require.NoError(t, err)
	assert.Equal(t, "original content\n", string(content))

	// Verify new baseline SHA in meta
	updatedMeta, err := LoadMeta(sandboxDir)
	require.NoError(t, err)
	assert.NotEmpty(t, updatedMeta.Workdir.BaselineSHA)
	assert.NotEqual(t, sha, updatedMeta.Workdir.BaselineSHA)
}

// patchConfigDebug tests

func TestPatchConfigDebug_SetTrue(t *testing.T) {
	sandboxDir := t.TempDir()
	cfg := containerConfig{AgentCommand: "claude", WorkingDir: "/project"}
	cfgData, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, RuntimeConfigFile), cfgData, 0600))

	require.NoError(t, patchConfigDebug(sandboxDir, true))

	data, err := os.ReadFile(filepath.Join(sandboxDir, RuntimeConfigFile)) //nolint:gosec // test
	require.NoError(t, err)
	var result containerConfig
	require.NoError(t, json.Unmarshal(data, &result))
	assert.True(t, result.Debug)
}

func TestPatchConfigDebug_SetFalse(t *testing.T) {
	sandboxDir := t.TempDir()
	cfg := containerConfig{AgentCommand: "claude", Debug: true}
	cfgData, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, RuntimeConfigFile), cfgData, 0600))

	require.NoError(t, patchConfigDebug(sandboxDir, false))

	data, err := os.ReadFile(filepath.Join(sandboxDir, RuntimeConfigFile)) //nolint:gosec // test
	require.NoError(t, err)
	var result containerConfig
	require.NoError(t, json.Unmarshal(data, &result))
	assert.False(t, result.Debug)
}

func TestPatchConfigDebug_MissingConfig(t *testing.T) {
	sandboxDir := t.TempDir()
	err := patchConfigDebug(sandboxDir, true)
	assert.Error(t, err)
}

func TestPatchConfigDebug_PreservesOtherFields(t *testing.T) {
	sandboxDir := t.TempDir()
	cfg := containerConfig{AgentCommand: "claude --print", WorkingDir: "/home/user/project"}
	cfgData, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, RuntimeConfigFile), cfgData, 0600))

	require.NoError(t, patchConfigDebug(sandboxDir, true))

	data, err := os.ReadFile(filepath.Join(sandboxDir, RuntimeConfigFile)) //nolint:gosec // test
	require.NoError(t, err)
	var result containerConfig
	require.NoError(t, json.Unmarshal(data, &result))
	assert.Equal(t, "claude --print", result.AgentCommand)
	assert.Equal(t, "/home/user/project", result.WorkingDir)
	assert.True(t, result.Debug)
}

// PatchConfigAllowedDomains tests

func TestPatchConfigAllowedDomains_SetDomains(t *testing.T) {
	sandboxDir := t.TempDir()
	cfg := containerConfig{AgentCommand: "claude"}
	cfgData, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, RuntimeConfigFile), cfgData, 0600))

	require.NoError(t, PatchConfigAllowedDomains(sandboxDir, []string{"api.anthropic.com", "sentry.io"}))

	data, err := os.ReadFile(filepath.Join(sandboxDir, RuntimeConfigFile)) //nolint:gosec // test
	require.NoError(t, err)
	var result containerConfig
	require.NoError(t, json.Unmarshal(data, &result))
	assert.Equal(t, []string{"api.anthropic.com", "sentry.io"}, result.AllowedDomains)
}

func TestPatchConfigAllowedDomains_ReplacesExisting(t *testing.T) {
	sandboxDir := t.TempDir()
	cfg := containerConfig{AgentCommand: "claude", AllowedDomains: []string{"old.com"}}
	cfgData, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, RuntimeConfigFile), cfgData, 0600))

	require.NoError(t, PatchConfigAllowedDomains(sandboxDir, []string{"new.com"}))

	data, err := os.ReadFile(filepath.Join(sandboxDir, RuntimeConfigFile)) //nolint:gosec // test
	require.NoError(t, err)
	var result containerConfig
	require.NoError(t, json.Unmarshal(data, &result))
	assert.Equal(t, []string{"new.com"}, result.AllowedDomains)
}

func TestPatchConfigAllowedDomains_EmptyListClears(t *testing.T) {
	sandboxDir := t.TempDir()
	cfg := containerConfig{AgentCommand: "claude", AllowedDomains: []string{"api.com"}}
	cfgData, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, RuntimeConfigFile), cfgData, 0600))

	require.NoError(t, PatchConfigAllowedDomains(sandboxDir, []string{}))

	data, err := os.ReadFile(filepath.Join(sandboxDir, RuntimeConfigFile)) //nolint:gosec // test
	require.NoError(t, err)
	var result containerConfig
	require.NoError(t, json.Unmarshal(data, &result))
	assert.Empty(t, result.AllowedDomains)
}

func TestPatchConfigAllowedDomains_MissingConfig(t *testing.T) {
	sandboxDir := t.TempDir()
	err := PatchConfigAllowedDomains(sandboxDir, []string{"api.com"})
	assert.Error(t, err)
}

func TestDestroy_BrokenSandbox(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Create sandbox dir without environment.json (broken sandbox)
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", "broken")
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	mock := &lifecycleMockRuntime{}
	mgr := newLifecycleMgr(mock)

	err := mgr.Destroy(context.Background(), "broken")
	require.NoError(t, err)
	assert.NoDirExists(t, sandboxDir)
}

func TestDestroy_ReadOnlyFiles(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	name := "test-destroy-readonly"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	// Create read-only nested structure (like Go module cache)
	readonlyDir := filepath.Join(sandboxDir, "work", "modcache", "pkg")
	require.NoError(t, os.MkdirAll(readonlyDir, 0750))
	writeTestFile(t, readonlyDir, "mod.go", "package mod")
	// Make directory read-only
	require.NoError(t, os.Chmod(readonlyDir, 0o555))               //nolint:gosec // intentionally read-only for test
	require.NoError(t, os.Chmod(filepath.Dir(readonlyDir), 0o555)) //nolint:gosec // intentionally read-only for test

	meta := &Meta{
		Name:      name,
		Agent:     "claude",
		CreatedAt: time.Now(),
		Workdir:   WorkdirMeta{HostPath: "/tmp/project", Mode: "copy"},
	}
	require.NoError(t, SaveMeta(sandboxDir, meta))

	mock := &lifecycleMockRuntime{}
	mgr := newLifecycleMgr(mock)

	err := mgr.Destroy(context.Background(), name)
	require.NoError(t, err)
	assert.NoDirExists(t, sandboxDir)
}

// NeedsConfirmation edge cases

func TestNeedsConfirmation_InspectError(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createTestSandbox(t, tmpDir, "test-confirm-err", "/tmp/project", "copy")

	mock := &lifecycleMockRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{}, fmt.Errorf("runtime unavailable")
		},
	}

	mgr := newLifecycleMgr(mock)
	needs, reason := mgr.NeedsConfirmation(context.Background(), "test-confirm-err")
	// When inspect fails, DetectStatus returns an error → NeedsConfirmation returns false
	assert.False(t, needs)
	assert.Empty(t, reason)
}

// forceRemoveAll tests

func TestForceRemoveAll_ReadOnlyNested(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "a", "b", "c")
	require.NoError(t, os.MkdirAll(nested, 0750))
	writeTestFile(t, nested, "file.txt", "content")

	// Make everything read-only
	require.NoError(t, os.Chmod(nested, 0o555))                             //nolint:gosec // intentionally read-only for test
	require.NoError(t, os.Chmod(filepath.Dir(nested), 0o555))               //nolint:gosec // intentionally read-only for test
	require.NoError(t, os.Chmod(filepath.Dir(filepath.Dir(nested)), 0o555)) //nolint:gosec // intentionally read-only for test

	err := forceRemoveAll(dir)
	require.NoError(t, err)
	assert.NoDirExists(t, dir)
}

func TestForceRemoveAll_NonExistentPath(t *testing.T) {
	err := forceRemoveAll("/tmp/nonexistent-path-" + t.Name())
	assert.NoError(t, err)
}

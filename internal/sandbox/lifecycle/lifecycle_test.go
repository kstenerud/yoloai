package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/git"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox/runtimeconfig"
	"github.com/kstenerud/yoloai/internal/sandbox/state"
	"github.com/kstenerud/yoloai/internal/store"
	"github.com/kstenerud/yoloai/internal/testutil"
)

// newLifecycleDeps builds a state.Deps backed by the given mock runtime and
// a layout rooted at tmpDir/.yoloai — mirrors what the Engine would build.
func newLifecycleDeps(rt runtime.Backend, tmpDir string) state.Deps {
	layout := config.NewLayout(filepath.Join(tmpDir, ".yoloai"))
	return state.Deps{Runtime: rt, Layout: layout, Input: strings.NewReader("")}
}

// createTestSandbox creates a sandbox directory with environment.json for lifecycle tests.
func createTestSandbox(t *testing.T, tmpDir, name, hostPath string, mode store.DirMode) {
	t.Helper()
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	meta := &store.Environment{
		Name:      name,
		AgentType: "claude",
		CreatedAt: time.Now(),
		Dirs: []store.DirEnvironment{{
			HostPath:  hostPath,
			MountPath: hostPath,
			Mode:      mode,
		}},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))
}

// createRWSandbox creates a minimal :rw mode sandbox directory structure for tests.
func createRWSandbox(t *testing.T, tmpDir, name, hostPath string) {
	t.Helper()
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))
	meta := &store.Environment{
		Name:      name,
		AgentType: "test",
		CreatedAt: time.Now(),
		Dirs: []store.DirEnvironment{{
			HostPath:  hostPath,
			MountPath: hostPath,
			Mode:      "rw",
		}},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))
}

// gitHEAD returns the HEAD commit SHA for the git repo at dir.
func gitHEAD(t *testing.T, dir string) string {
	t.Helper()
	sha, err := git.NewHostWithEnv(testutil.GitEnv()).HeadSHA(context.Background(), dir)
	require.NoError(t, err)
	return sha
}

// noticeText joins a result's notice messages for substring assertions.
func noticeText(ns []Notice) string {
	var b strings.Builder
	for _, n := range ns {
		b.WriteString(n.Message)
		b.WriteString("\n")
	}
	return b.String()
}

// Stop tests

func TestStop_Running(t *testing.T) {
	tmpDir := t.TempDir()

	createTestSandbox(t, tmpDir, "test-stop", "/tmp/project", "copy")

	stopCalled := false
	mock := &lifecycleMockRuntime{
		stopFn: func(_ context.Context, _ string) error {
			stopCalled = true
			return nil
		},
	}

	d := newLifecycleDeps(mock, tmpDir)
	err := Stop(context.Background(), d, "test-stop")
	require.NoError(t, err)
	assert.True(t, stopCalled)
}

func TestStop_AlreadyStopped(t *testing.T) {
	tmpDir := t.TempDir()

	createTestSandbox(t, tmpDir, "test-stop-already", "/tmp/project", "copy")

	// Runtime.Stop returns nil when already stopped (contract guarantee).
	mock := &lifecycleMockRuntime{
		stopFn: func(_ context.Context, _ string) error {
			return nil
		},
	}

	d := newLifecycleDeps(mock, tmpDir)
	err := Stop(context.Background(), d, "test-stop-already")
	assert.NoError(t, err) // idempotent
}

func TestStop_ContainerRemoved(t *testing.T) {
	tmpDir := t.TempDir()

	createTestSandbox(t, tmpDir, "test-stop-removed", "/tmp/project", "copy")

	// Runtime.Stop returns nil when instance not found (contract guarantee).
	mock := &lifecycleMockRuntime{
		stopFn: func(_ context.Context, _ string) error {
			return nil
		},
	}

	d := newLifecycleDeps(mock, tmpDir)
	err := Stop(context.Background(), d, "test-stop-removed")
	assert.NoError(t, err) // idempotent
}

func TestStop_SandboxNotFound(t *testing.T) {
	tmpDir := t.TempDir()

	mock := &lifecycleMockRuntime{}
	d := newLifecycleDeps(mock, tmpDir)
	err := Stop(context.Background(), d, "nonexistent")
	assert.ErrorIs(t, err, store.ErrSandboxNotFound)
}

// Start tests

func TestStart_AlreadyRunning(t *testing.T) {
	tmpDir := t.TempDir()

	createTestSandbox(t, tmpDir, "test-start-running", "/tmp/project", "copy")

	mock := &lifecycleMockRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{Running: true}, nil
		},
	}

	d := newLifecycleDeps(mock, tmpDir)

	// DetectStatus will call Inspect (running=true),
	// then try Exec for tmux. Since our mock returns errMockNotImplemented
	// for exec, DetectStatus defaults to StatusActive.
	res, err := Start(context.Background(), d, "test-start-running", StartOptions{})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Contains(t, noticeText(res.Notices), "already running",
		"already-running status is now returned as a notice, not written to output")
}

func TestStart_Stopped(t *testing.T) {
	tmpDir := t.TempDir()

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

	d := newLifecycleDeps(mock, tmpDir)

	// After remove, Start routes to recreateContainer which fails
	// (no runtime-config.json) — same pattern as TestStart_Removed.
	_, err := Start(context.Background(), d, "test-start-stopped", StartOptions{})
	assert.Error(t, err)
	assert.True(t, removeCalled, "should remove stopped container before recreating")
	assert.Contains(t, err.Error(), store.RuntimeConfigFile)
}

func TestStart_SandboxNotFound(t *testing.T) {
	tmpDir := t.TempDir()

	mock := &lifecycleMockRuntime{}
	d := newLifecycleDeps(mock, tmpDir)
	_, err := Start(context.Background(), d, "nonexistent", StartOptions{})
	assert.ErrorIs(t, err, store.ErrSandboxNotFound)
}

func TestStart_Removed(t *testing.T) {
	tmpDir := t.TempDir()

	createTestSandbox(t, tmpDir, "test-start-removed", "/tmp/project", "copy")

	mock := &lifecycleMockRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{}, fmt.Errorf("not found: %w", runtime.ErrNotFound)
		},
	}

	d := newLifecycleDeps(mock, tmpDir)

	// recreateContainer will fail because there's no runtime-config.json,
	// but we're testing that Start routes to recreateContainer for StatusRemoved.
	_, err := Start(context.Background(), d, "test-start-removed", StartOptions{})
	assert.Error(t, err)
	// Should be a recreateContainer error (runtime-config.json missing), not a routing error
	assert.Contains(t, err.Error(), store.RuntimeConfigFile)
}

func TestStart_Resume_RequiresPrompt(t *testing.T) {
	tmpDir := t.TempDir()

	// Create sandbox WITHOUT HasPrompt
	createTestSandbox(t, tmpDir, "test-resume-noprompt", "/tmp/project", "copy")

	mock := &lifecycleMockRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{Running: false}, nil
		},
	}

	d := newLifecycleDeps(mock, tmpDir)
	_, err := Start(context.Background(), d, "test-resume-noprompt", StartOptions{Resume: true})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "--resume requires a sandbox created with --prompt")
}

func TestStart_Resume_DoneStatus(t *testing.T) {
	tmpDir := t.TempDir()

	name := "test-resume-done"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	// Create meta with HasPrompt=true
	meta := &store.Environment{
		Name:      name,
		AgentType: "claude",
		HasPrompt: true,
		CreatedAt: time.Now(),
		Dirs: []store.DirEnvironment{{
			HostPath:  "/tmp/project",
			MountPath: "/tmp/project",
			Mode:      "copy",
		}},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))

	// Write prompt.txt
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, "prompt.txt"), []byte("Write hello world"), 0600))

	// Write runtime-config.json
	cfg := runtimeconfig.ContainerConfig{
		AgentCommand:   "claude --dangerously-skip-permissions --print \"Write hello world\"",
		SubmitSequence: "Enter",
		ReadyPattern:   "> $",
	}
	cfgData, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, store.RuntimeConfigFile), cfgData, 0600))

	// Write agent-status.json indicating done (exit code 0)
	statusData := fmt.Sprintf(`{"status":"done","exit_code":0,"timestamp":%d}`, time.Now().Unix())
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, store.AgentStatusFile), []byte(statusData), 0600))

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

	d := newLifecycleDeps(mock, tmpDir)
	_, _ = Start(context.Background(), d, name, StartOptions{Resume: true})
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

	name := "test-resume-stopped"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	meta := &store.Environment{
		Name:      name,
		AgentType: "claude",
		HasPrompt: true,
		CreatedAt: time.Now(),
		Dirs: []store.DirEnvironment{{
			HostPath:  "/tmp/project",
			MountPath: "/tmp/project",
			Mode:      "copy",
		}},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))

	// Write prompt.txt
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, "prompt.txt"), []byte("Write hello world"), 0600))

	// Write runtime-config.json with headless command
	cfg := runtimeconfig.ContainerConfig{
		AgentCommand:   "claude --dangerously-skip-permissions --print \"Write hello world\"",
		SubmitSequence: "Enter",
		ReadyPattern:   "> $",
	}
	cfgData, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, store.RuntimeConfigFile), cfgData, 0600))

	mock := &lifecycleMockRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			// Container exists but stopped
			return runtime.InstanceInfo{Running: false}, nil
		},
		removeFn: func(_ context.Context, _ string) error {
			return nil
		},
	}

	d := newLifecycleDeps(mock, tmpDir)

	// Start with resume will call prepareResumeFiles then recreateContainer.
	// recreateContainer will fail (no work dir, no secrets etc.) but we can check
	// that resume-prompt.txt was created and runtime-config.json was patched.
	_, _ = Start(context.Background(), d, name, StartOptions{Resume: true})

	// Verify runtime-config.json was patched to interactive command
	updatedCfgData, err := os.ReadFile(filepath.Join(sandboxDir, store.RuntimeConfigFile)) //nolint:gosec // test file in controlled temp dir
	require.NoError(t, err)
	var updatedCfg runtimeconfig.ContainerConfig
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

// A running agent with a clean workdir is NOT a destroy blocker: there is no
// unapplied work to lose, so NeedsConfirmation must return false even when the
// container is live.
func TestNeedsConfirmation_RunningButClean(t *testing.T) {
	tmpDir := t.TempDir()

	name := "test-confirm-running"
	hostPath := "/tmp/project"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	workDir := filepath.Join(sandboxDir, "work", store.EncodePath(hostPath))
	require.NoError(t, os.MkdirAll(workDir, 0750))

	testutil.InitGitRepo(t, workDir)
	testutil.WriteFile(t, workDir, "file.txt", "content")
	testutil.GitAdd(t, workDir, ".")
	testutil.GitCommit(t, workDir, "initial")

	meta := &store.Environment{
		Name:      name,
		AgentType: "claude",
		CreatedAt: time.Now(),
		Dirs: []store.DirEnvironment{{
			HostPath:  hostPath,
			MountPath: hostPath,
			Mode:      "copy",
		}},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))

	mock := &lifecycleMockRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{Running: true}, nil
		},
	}

	d := newLifecycleDeps(mock, tmpDir)
	needs, reason := NeedsConfirmation(context.Background(), d, name)
	assert.False(t, needs)
	assert.Empty(t, reason)
}

// A VM-local backend (Tart) that is not running can't be probed: the working
// copy lives in the VM, not on the host seed. GitExec reports ErrNotRunning, and
// the gate must fail safe — block destroy rather than read a stale host copy.
func TestNeedsConfirmation_StoppedVMFailsafe(t *testing.T) {
	tmpDir := t.TempDir()

	name := "test-confirm-stopped"
	hostPath := "/tmp/project"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	workDir := filepath.Join(sandboxDir, "work", store.EncodePath(hostPath))
	require.NoError(t, os.MkdirAll(workDir, 0750))

	// Host seed is a clean repo (what the host can see) — but the agent's real
	// edits would be inside the stopped VM, invisible here.
	testutil.InitGitRepo(t, workDir)
	testutil.WriteFile(t, workDir, "file.txt", "content")
	testutil.GitAdd(t, workDir, ".")
	testutil.GitCommit(t, workDir, "initial")

	meta := &store.Environment{
		Name:      name,
		AgentType: "claude",
		CreatedAt: time.Now(),
		Dirs: []store.DirEnvironment{{
			HostPath:  hostPath,
			MountPath: hostPath,
			Mode:      "copy",
		}},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))

	mock := &lifecycleMockRuntime{
		locality: runtime.LocalitySandboxSide, // VM: git runs in-sandbox; host probe is blind
		gitExecFn: func(_ context.Context, _, _ string, _ ...string) (string, error) {
			return "", runtime.ErrNotRunning
		},
	}

	d := newLifecycleDeps(mock, tmpDir)
	needs, reason := NeedsConfirmation(context.Background(), d, name)
	assert.True(t, needs)
	assert.Contains(t, reason, "stopped")
}

func TestNeedsConfirmation_ChangesExist(t *testing.T) {
	tmpDir := t.TempDir()

	// Create sandbox with a work directory that has changes
	name := "test-confirm-changes"
	hostPath := "/tmp/project"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	workDir := filepath.Join(sandboxDir, "work", store.EncodePath(hostPath))
	require.NoError(t, os.MkdirAll(workDir, 0750))

	testutil.InitGitRepo(t, workDir)
	testutil.WriteFile(t, workDir, "file.txt", "original")
	testutil.GitAdd(t, workDir, ".")
	testutil.GitCommit(t, workDir, "initial")

	meta := &store.Environment{
		Name:      name,
		AgentType: "claude",
		CreatedAt: time.Now(),
		Dirs: []store.DirEnvironment{{
			HostPath:  hostPath,
			MountPath: hostPath,
			Mode:      "copy",
		}},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))

	// Make changes in work dir
	testutil.WriteFile(t, workDir, "file.txt", "modified")

	mock := &lifecycleMockRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			// Container not running
			return runtime.InstanceInfo{Running: false}, nil
		},
	}

	d := newLifecycleDeps(mock, tmpDir)
	needs, reason := NeedsConfirmation(context.Background(), d, "test-confirm-changes")
	assert.True(t, needs)
	assert.Equal(t, "unapplied changes exist", reason)
}

func TestNeedsConfirmation_NoChanges(t *testing.T) {
	tmpDir := t.TempDir()

	// Create sandbox with clean work directory
	name := "test-confirm-clean"
	hostPath := "/tmp/project"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	workDir := filepath.Join(sandboxDir, "work", store.EncodePath(hostPath))
	require.NoError(t, os.MkdirAll(workDir, 0750))

	testutil.InitGitRepo(t, workDir)
	testutil.WriteFile(t, workDir, "file.txt", "content")
	testutil.GitAdd(t, workDir, ".")
	testutil.GitCommit(t, workDir, "initial")

	meta := &store.Environment{
		Name:      name,
		AgentType: "claude",
		CreatedAt: time.Now(),
		Dirs: []store.DirEnvironment{{
			HostPath:  hostPath,
			MountPath: hostPath,
			Mode:      "copy",
		}},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))

	mock := &lifecycleMockRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{Running: false}, nil
		},
	}

	d := newLifecycleDeps(mock, tmpDir)
	needs, reason := NeedsConfirmation(context.Background(), d, "test-confirm-clean")
	assert.False(t, needs)
	assert.Empty(t, reason)
}

// Destroy tests

func TestDestroy_RemovesDir(t *testing.T) {
	tmpDir := t.TempDir()

	createTestSandbox(t, tmpDir, "test-destroy", "/tmp/project", "copy")

	mock := &lifecycleMockRuntime{}
	d := newLifecycleDeps(mock, tmpDir)

	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", "test-destroy")
	assert.DirExists(t, sandboxDir)

	_, err := Destroy(context.Background(), d, "test-destroy")
	require.NoError(t, err)
	assert.NoDirExists(t, sandboxDir)
}

func TestDestroy_RemovesLockFile(t *testing.T) {
	tmpDir := t.TempDir()

	createTestSandbox(t, tmpDir, "test-destroy-lock", "/tmp/project", "copy")

	mock := &lifecycleMockRuntime{}
	d := newLifecycleDeps(mock, tmpDir)

	lockPath := d.Layout.SandboxLockPath("test-destroy-lock")

	_, err := Destroy(context.Background(), d, "test-destroy-lock")
	require.NoError(t, err)
	assert.NoFileExists(t, lockPath, "destroy should remove the per-sandbox lock file")
}

func TestDestroy_SandboxNotFound(t *testing.T) {
	tmpDir := t.TempDir()

	mock := &lifecycleMockRuntime{}
	d := newLifecycleDeps(mock, tmpDir)
	_, err := Destroy(context.Background(), d, "nonexistent")
	assert.NoError(t, err)
}

// Reset tests

func TestReset_RecopiesWorkdir(t *testing.T) {
	tmpDir := t.TempDir()

	// Create original source directory
	origDir := filepath.Join(tmpDir, "original")
	require.NoError(t, os.MkdirAll(origDir, 0750))
	testutil.WriteFile(t, origDir, "file.txt", "original content\n")

	// Create sandbox with work copy
	name := "test-reset"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	workDir := filepath.Join(sandboxDir, "work", store.EncodePath(origDir))
	require.NoError(t, os.MkdirAll(workDir, 0750))

	// Create cache and files dirs with content (should be cleared by default)
	cacheDir := filepath.Join(sandboxDir, "cache")
	filesDir := filepath.Join(sandboxDir, "files")
	require.NoError(t, os.MkdirAll(cacheDir, 0750))
	require.NoError(t, os.MkdirAll(filesDir, 0750))
	testutil.WriteFile(t, cacheDir, "cached.txt", "cached data\n")
	testutil.WriteFile(t, filesDir, "shared.txt", "shared data\n")

	// Copy original to work dir and create baseline
	testutil.WriteFile(t, workDir, "file.txt", "original content\n")
	testutil.InitGitRepo(t, workDir)
	testutil.GitAdd(t, workDir, ".")
	testutil.GitCommit(t, workDir, "yoloai baseline")
	sha := gitHEAD(t, workDir)

	meta := &store.Environment{
		Name:      name,
		AgentType: "claude",
		CreatedAt: time.Now(),
		Dirs: []store.DirEnvironment{{
			HostPath:    origDir,
			MountPath:   origDir,
			Mode:        "copy",
			BaselineSHA: sha,
		}},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))

	// Modify work copy
	testutil.WriteFile(t, workDir, "file.txt", "modified by agent\n")

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

	d := newLifecycleDeps(mock, tmpDir)

	// Reset will re-copy and re-baseline, then fail at Start (recreateContainer
	// needs runtime-config.json). The re-copy must happen AND the downstream
	// Start failure must propagate — Reset never swallows it.
	_, resetErr := Reset(context.Background(), d, ResetOptions{Name: name})
	require.Error(t, resetErr, "Reset must surface the Start failure, not swallow it")

	// Verify work copy was re-copied from original
	content, err := os.ReadFile(filepath.Join(workDir, "file.txt")) //nolint:gosec // G304: test file path
	require.NoError(t, err)
	assert.Equal(t, "original content\n", string(content))

	// Verify new baseline SHA in meta
	updatedMeta, err := store.LoadEnvironment(sandboxDir)
	require.NoError(t, err)
	assert.NotEmpty(t, updatedMeta.Workdir().BaselineSHA)
	assert.NotEqual(t, sha, updatedMeta.Workdir().BaselineSHA) // new baseline

	// Verify cache and files were cleared (default behavior)
	assert.NoFileExists(t, filepath.Join(cacheDir, "cached.txt"))
	assert.NoFileExists(t, filepath.Join(filesDir, "shared.txt"))
	assert.DirExists(t, cacheDir)
	assert.DirExists(t, filesDir)
}

func TestReset_PromptOverwrite(t *testing.T) {
	tmpDir := t.TempDir()

	origDir := filepath.Join(tmpDir, "original")
	require.NoError(t, os.MkdirAll(origDir, 0750))

	name := "test-reset-prompt"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, "prompt.txt"), []byte("old prompt"), 0600))

	meta := &store.Environment{
		Name:      name,
		AgentType: "claude",
		CreatedAt: time.Now(),
		Dirs: []store.DirEnvironment{{
			HostPath:  origDir,
			MountPath: origDir,
			Mode:      "copy",
		}},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))

	mock := &lifecycleMockRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{}, fmt.Errorf("not found: %w", runtime.ErrNotFound)
		},
	}
	d := newLifecycleDeps(mock, tmpDir)

	// Reset overwrites prompt.txt before doing anything else; the later restart
	// fails (no runtime-config.json) but the prompt write already happened. The
	// restart failure must propagate.
	_, resetErr := Reset(context.Background(), d, ResetOptions{Name: name, Prompt: "new prompt"})
	require.Error(t, resetErr, "Reset must surface the restart failure, not swallow it")

	got, err := os.ReadFile(filepath.Join(sandboxDir, "prompt.txt")) //nolint:gosec // test path
	require.NoError(t, err)
	assert.Equal(t, "new prompt", string(got))
}

func TestReset_State(t *testing.T) {
	tmpDir := t.TempDir()

	origDir := filepath.Join(tmpDir, "original")
	require.NoError(t, os.MkdirAll(origDir, 0750))
	testutil.WriteFile(t, origDir, "file.txt", "content\n")

	name := "test-reset-state"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	workDir := filepath.Join(sandboxDir, "work", store.EncodePath(origDir))
	agentStateDir := filepath.Join(sandboxDir, store.AgentRuntimeDir)
	require.NoError(t, os.MkdirAll(workDir, 0750))
	require.NoError(t, os.MkdirAll(agentStateDir, 0750))

	// Add content to agent-runtime
	testutil.WriteFile(t, agentStateDir, "session.json", `{"key":"value"}`)

	testutil.WriteFile(t, workDir, "file.txt", "content\n")
	testutil.InitGitRepo(t, workDir)
	testutil.GitAdd(t, workDir, ".")
	testutil.GitCommit(t, workDir, "yoloai baseline")
	sha := gitHEAD(t, workDir)

	meta := &store.Environment{
		Name:      name,
		AgentType: "claude",
		CreatedAt: time.Now(),
		Dirs: []store.DirEnvironment{{
			HostPath:    origDir,
			MountPath:   origDir,
			Mode:        "copy",
			BaselineSHA: sha,
		}},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))

	mock := &lifecycleMockRuntime{
		stopFn: func(_ context.Context, _ string) error {
			return nil
		},
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{}, fmt.Errorf("not found: %w", runtime.ErrNotFound)
		},
	}

	d := newLifecycleDeps(mock, tmpDir)
	// --state implies --restart; the restart fails (no runtime-config.json) but
	// the state wipe already happened. The failure must propagate.
	_, resetErr := Reset(context.Background(), d, ResetOptions{Name: name, ClearState: true})
	require.Error(t, resetErr, "Reset must surface the restart failure, not swallow it")

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

	hostDir := filepath.Join(tmpDir, "rw-dir")
	require.NoError(t, os.MkdirAll(hostDir, 0750))
	createRWSandbox(t, tmpDir, "test-reset-rw", hostDir)

	mock := &lifecycleMockRuntime{}
	d := newLifecycleDeps(mock, tmpDir)

	_, err := Reset(context.Background(), d, ResetOptions{Name: "test-reset-rw"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), ":rw directories")
}

func TestReset_OriginalMissing(t *testing.T) {
	tmpDir := t.TempDir()

	// Create original dir, then delete it
	origDir := filepath.Join(tmpDir, "vanished")
	require.NoError(t, os.MkdirAll(origDir, 0750))
	testutil.WriteFile(t, origDir, "file.txt", "content\n")

	name := "test-reset-missing"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	workDir := filepath.Join(sandboxDir, "work", store.EncodePath(origDir))
	require.NoError(t, os.MkdirAll(workDir, 0750))

	testutil.WriteFile(t, workDir, "file.txt", "content\n")
	testutil.InitGitRepo(t, workDir)
	testutil.GitAdd(t, workDir, ".")
	testutil.GitCommit(t, workDir, "yoloai baseline")
	sha := gitHEAD(t, workDir)

	meta := &store.Environment{
		Name:      name,
		AgentType: "claude",
		CreatedAt: time.Now(),
		Dirs: []store.DirEnvironment{{
			HostPath:    origDir,
			MountPath:   origDir,
			Mode:        "copy",
			BaselineSHA: sha,
		}},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))

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

	d := newLifecycleDeps(mock, tmpDir)
	// Container not running → auto-upgrades to restart
	_, err := Reset(context.Background(), d, ResetOptions{Name: name})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "original directory no longer exists")
}

// In-place reset tests (default behavior when container is running)

func TestReset_InPlace_SyncsWorkdir(t *testing.T) {
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("rsync not installed")
	}

	tmpDir := t.TempDir()

	// Create original source directory
	origDir := filepath.Join(tmpDir, "original")
	require.NoError(t, os.MkdirAll(origDir, 0750))
	testutil.WriteFile(t, origDir, "file.txt", "original content\n")

	// Create sandbox with work copy
	name := "test-reset-inplace"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	workDir := filepath.Join(sandboxDir, "work", store.EncodePath(origDir))
	require.NoError(t, os.MkdirAll(workDir, 0750))

	// Create cache and files dirs with content
	cacheDir := filepath.Join(sandboxDir, "cache")
	filesDir := filepath.Join(sandboxDir, "files")
	require.NoError(t, os.MkdirAll(cacheDir, 0750))
	require.NoError(t, os.MkdirAll(filesDir, 0750))
	testutil.WriteFile(t, cacheDir, "cached.txt", "cached data\n")
	testutil.WriteFile(t, filesDir, "shared.txt", "shared data\n")

	// Set up work copy with baseline
	testutil.WriteFile(t, workDir, "file.txt", "original content\n")
	testutil.InitGitRepo(t, workDir)
	testutil.GitAdd(t, workDir, ".")
	testutil.GitCommit(t, workDir, "yoloai baseline")
	sha := gitHEAD(t, workDir)

	meta := &store.Environment{
		Name:      name,
		AgentType: "claude",
		HasPrompt: true,
		CreatedAt: time.Now(),
		Dirs: []store.DirEnvironment{{
			HostPath:    origDir,
			MountPath:   origDir,
			Mode:        "copy",
			BaselineSHA: sha,
		}},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))

	// Simulate agent changes in work copy
	testutil.WriteFile(t, workDir, "file.txt", "modified by agent\n")
	testutil.WriteFile(t, workDir, "agent-new.txt", "agent created this\n")

	// Simulate upstream changes in original
	testutil.WriteFile(t, origDir, "file.txt", "updated upstream\n")
	testutil.WriteFile(t, origDir, "upstream-new.txt", "new upstream file\n")

	// Mock: container is running
	mock := &lifecycleMockRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{Running: true}, nil
		},
	}

	d := newLifecycleDeps(mock, tmpDir)

	// Default reset (in-place). sendResetNotification will fail (no runtime-config.json
	// and exec mock not wired), but workspace sync and baseline should succeed.
	// The notification failure must propagate.
	_, resetErr := Reset(context.Background(), d, ResetOptions{Name: name})
	require.Error(t, resetErr, "Reset must surface the notification failure, not swallow it")

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
	updatedMeta, err := store.LoadEnvironment(sandboxDir)
	require.NoError(t, err)
	assert.NotEmpty(t, updatedMeta.Workdir().BaselineSHA)
	assert.NotEqual(t, sha, updatedMeta.Workdir().BaselineSHA)

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

	origDir := filepath.Join(tmpDir, "original")
	require.NoError(t, os.MkdirAll(origDir, 0750))
	testutil.WriteFile(t, origDir, "file.txt", "content\n")

	name := "test-reset-keep-cache"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	workDir := filepath.Join(sandboxDir, "work", store.EncodePath(origDir))
	require.NoError(t, os.MkdirAll(workDir, 0750))

	// Create cache and files dirs with content
	cacheDir := filepath.Join(sandboxDir, "cache")
	filesDir := filepath.Join(sandboxDir, "files")
	require.NoError(t, os.MkdirAll(cacheDir, 0750))
	require.NoError(t, os.MkdirAll(filesDir, 0750))
	testutil.WriteFile(t, cacheDir, "cached.txt", "cached data\n")
	testutil.WriteFile(t, filesDir, "shared.txt", "shared data\n")

	testutil.WriteFile(t, workDir, "file.txt", "content\n")
	testutil.InitGitRepo(t, workDir)
	testutil.GitAdd(t, workDir, ".")
	testutil.GitCommit(t, workDir, "yoloai baseline")
	sha := gitHEAD(t, workDir)

	meta := &store.Environment{
		Name:      name,
		AgentType: "claude",
		CreatedAt: time.Now(),
		Dirs: []store.DirEnvironment{{
			HostPath:    origDir,
			MountPath:   origDir,
			Mode:        "copy",
			BaselineSHA: sha,
		}},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))

	mock := &lifecycleMockRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{Running: true}, nil
		},
	}

	d := newLifecycleDeps(mock, tmpDir)
	// In-place reset; sendResetNotification fails (no runtime-config.json) but
	// the cache/files handling already ran. The failure must propagate.
	_, resetErr := Reset(context.Background(), d, ResetOptions{Name: name, KeepCache: true})
	require.Error(t, resetErr, "Reset must surface the notification failure, not swallow it")

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

	origDir := filepath.Join(tmpDir, "original")
	require.NoError(t, os.MkdirAll(origDir, 0750))
	testutil.WriteFile(t, origDir, "file.txt", "content\n")

	name := "test-reset-keep-files"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	workDir := filepath.Join(sandboxDir, "work", store.EncodePath(origDir))
	require.NoError(t, os.MkdirAll(workDir, 0750))

	// Create cache and files dirs with content
	cacheDir := filepath.Join(sandboxDir, "cache")
	filesDir := filepath.Join(sandboxDir, "files")
	require.NoError(t, os.MkdirAll(cacheDir, 0750))
	require.NoError(t, os.MkdirAll(filesDir, 0750))
	testutil.WriteFile(t, cacheDir, "cached.txt", "cached data\n")
	testutil.WriteFile(t, filesDir, "shared.txt", "shared data\n")

	testutil.WriteFile(t, workDir, "file.txt", "content\n")
	testutil.InitGitRepo(t, workDir)
	testutil.GitAdd(t, workDir, ".")
	testutil.GitCommit(t, workDir, "yoloai baseline")
	sha := gitHEAD(t, workDir)

	meta := &store.Environment{
		Name:      name,
		AgentType: "claude",
		CreatedAt: time.Now(),
		Dirs: []store.DirEnvironment{{
			HostPath:    origDir,
			MountPath:   origDir,
			Mode:        "copy",
			BaselineSHA: sha,
		}},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))

	mock := &lifecycleMockRuntime{
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{Running: true}, nil
		},
	}

	d := newLifecycleDeps(mock, tmpDir)
	// In-place reset; sendResetNotification fails (no runtime-config.json) but
	// the cache/files handling already ran. The failure must propagate.
	_, resetErr := Reset(context.Background(), d, ResetOptions{Name: name, KeepFiles: true})
	require.Error(t, resetErr, "Reset must surface the notification failure, not swallow it")

	// Cache should be cleared
	assert.NoFileExists(t, filepath.Join(cacheDir, "cached.txt"))
	assert.DirExists(t, cacheDir)
	// Files dir should be preserved
	assert.FileExists(t, filepath.Join(filesDir, "shared.txt"))
}

func TestReset_UpgradesToRestartWhenNotRunning(t *testing.T) {
	tmpDir := t.TempDir()

	// Create original source directory
	origDir := filepath.Join(tmpDir, "original")
	require.NoError(t, os.MkdirAll(origDir, 0750))
	testutil.WriteFile(t, origDir, "file.txt", "original content\n")

	// Create sandbox with work copy
	name := "test-reset-upgrade-restart"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	workDir := filepath.Join(sandboxDir, "work", store.EncodePath(origDir))
	require.NoError(t, os.MkdirAll(workDir, 0750))

	testutil.WriteFile(t, workDir, "file.txt", "original content\n")
	testutil.InitGitRepo(t, workDir)
	testutil.GitAdd(t, workDir, ".")
	testutil.GitCommit(t, workDir, "yoloai baseline")
	sha := gitHEAD(t, workDir)

	meta := &store.Environment{
		Name:      name,
		AgentType: "claude",
		CreatedAt: time.Now(),
		Dirs: []store.DirEnvironment{{
			HostPath:    origDir,
			MountPath:   origDir,
			Mode:        "copy",
			BaselineSHA: sha,
		}},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))

	// Modify work copy
	testutil.WriteFile(t, workDir, "file.txt", "modified by agent\n")

	// Mock: container not found (removed)
	mock := &lifecycleMockRuntime{
		stopFn: func(_ context.Context, _ string) error {
			return nil
		},
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{}, fmt.Errorf("not found: %w", runtime.ErrNotFound)
		},
	}

	d := newLifecycleDeps(mock, tmpDir)

	// Default reset; container not running → auto-upgrades to restart.
	// Restart path will fail at Start (no runtime-config.json), but re-copy should
	// happen and the upgrade notice is returned even on the later error.
	res, _ := Reset(context.Background(), d, ResetOptions{Name: name})

	// Verify the upgrade notice was emitted (now returned, not written to output).
	require.NotNil(t, res)
	assert.Contains(t, noticeText(res.Notices), "Container is not running, upgrading to restart")

	// Verify work copy was re-copied from original (restart behavior)
	content, err := os.ReadFile(filepath.Join(workDir, "file.txt")) //nolint:gosec // test
	require.NoError(t, err)
	assert.Equal(t, "original content\n", string(content))

	// Verify new baseline SHA in meta
	updatedMeta, err := store.LoadEnvironment(sandboxDir)
	require.NoError(t, err)
	assert.NotEmpty(t, updatedMeta.Workdir().BaselineSHA)
	assert.NotEqual(t, sha, updatedMeta.Workdir().BaselineSHA)
}

// patchConfigDebug tests

func TestPatchConfigDebug_SetTrue(t *testing.T) {
	sandboxDir := t.TempDir()
	cfg := runtimeconfig.ContainerConfig{AgentCommand: "claude", WorkingDir: "/project"}
	cfgData, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, store.RuntimeConfigFile), cfgData, 0600))

	require.NoError(t, patchConfigDebug(sandboxDir, true))

	data, err := os.ReadFile(filepath.Join(sandboxDir, store.RuntimeConfigFile)) //nolint:gosec // test
	require.NoError(t, err)
	var result runtimeconfig.ContainerConfig
	require.NoError(t, json.Unmarshal(data, &result))
	assert.True(t, result.Debug)
}

func TestPatchConfigDebug_SetFalse(t *testing.T) {
	sandboxDir := t.TempDir()
	cfg := runtimeconfig.ContainerConfig{AgentCommand: "claude", Debug: true}
	cfgData, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, store.RuntimeConfigFile), cfgData, 0600))

	require.NoError(t, patchConfigDebug(sandboxDir, false))

	data, err := os.ReadFile(filepath.Join(sandboxDir, store.RuntimeConfigFile)) //nolint:gosec // test
	require.NoError(t, err)
	var result runtimeconfig.ContainerConfig
	require.NoError(t, json.Unmarshal(data, &result))
	assert.False(t, result.Debug)
}

func TestPatchConfigDebug_MissingConfig(t *testing.T) {
	sandboxDir := t.TempDir()
	err := patchConfigDebug(sandboxDir, true)
	require.ErrorIs(t, err, fs.ErrNotExist,
		"a missing runtime-config.json must surface as fs.ErrNotExist, distinct from a parse failure")
}

func TestPatchConfigDebug_PreservesOtherFields(t *testing.T) {
	sandboxDir := t.TempDir()
	cfg := runtimeconfig.ContainerConfig{AgentCommand: "claude --print", WorkingDir: "/home/user/project"}
	cfgData, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, store.RuntimeConfigFile), cfgData, 0600))

	require.NoError(t, patchConfigDebug(sandboxDir, true))

	data, err := os.ReadFile(filepath.Join(sandboxDir, store.RuntimeConfigFile)) //nolint:gosec // test
	require.NoError(t, err)
	var result runtimeconfig.ContainerConfig
	require.NoError(t, json.Unmarshal(data, &result))
	assert.Equal(t, "claude --print", result.AgentCommand)
	assert.Equal(t, "/home/user/project", result.WorkingDir)
	assert.True(t, result.Debug)
}

// PatchConfigAllowedDomains tests

func TestPatchConfigAllowedDomains_SetDomains(t *testing.T) {
	sandboxDir := t.TempDir()
	cfg := runtimeconfig.ContainerConfig{AgentCommand: "claude"}
	cfgData, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, store.RuntimeConfigFile), cfgData, 0600))

	require.NoError(t, PatchConfigAllowedDomains(sandboxDir, []string{"api.anthropic.com", "sentry.io"}))

	data, err := os.ReadFile(filepath.Join(sandboxDir, store.RuntimeConfigFile)) //nolint:gosec // test
	require.NoError(t, err)
	var result runtimeconfig.ContainerConfig
	require.NoError(t, json.Unmarshal(data, &result))
	assert.Equal(t, []string{"api.anthropic.com", "sentry.io"}, result.AllowedDomains)
}

func TestPatchConfigAllowedDomains_ReplacesExisting(t *testing.T) {
	sandboxDir := t.TempDir()
	cfg := runtimeconfig.ContainerConfig{AgentCommand: "claude", AllowedDomains: []string{"old.com"}}
	cfgData, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, store.RuntimeConfigFile), cfgData, 0600))

	require.NoError(t, PatchConfigAllowedDomains(sandboxDir, []string{"new.com"}))

	data, err := os.ReadFile(filepath.Join(sandboxDir, store.RuntimeConfigFile)) //nolint:gosec // test
	require.NoError(t, err)
	var result runtimeconfig.ContainerConfig
	require.NoError(t, json.Unmarshal(data, &result))
	assert.Equal(t, []string{"new.com"}, result.AllowedDomains)
}

func TestPatchConfigAllowedDomains_EmptyListClears(t *testing.T) {
	sandboxDir := t.TempDir()
	cfg := runtimeconfig.ContainerConfig{AgentCommand: "claude", AllowedDomains: []string{"api.com"}}
	cfgData, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, store.RuntimeConfigFile), cfgData, 0600))

	require.NoError(t, PatchConfigAllowedDomains(sandboxDir, []string{}))

	data, err := os.ReadFile(filepath.Join(sandboxDir, store.RuntimeConfigFile)) //nolint:gosec // test
	require.NoError(t, err)
	var result runtimeconfig.ContainerConfig
	require.NoError(t, json.Unmarshal(data, &result))
	assert.Empty(t, result.AllowedDomains)
}

func TestPatchConfigAllowedDomains_MissingConfig(t *testing.T) {
	sandboxDir := t.TempDir()
	err := PatchConfigAllowedDomains(sandboxDir, []string{"api.com"})
	require.ErrorIs(t, err, fs.ErrNotExist,
		"a missing runtime-config.json must surface as fs.ErrNotExist, distinct from a parse failure")
}

func TestDestroy_BrokenSandbox(t *testing.T) {
	tmpDir := t.TempDir()

	// Create sandbox dir without environment.json (broken sandbox)
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", "broken")
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	mock := &lifecycleMockRuntime{}
	d := newLifecycleDeps(mock, tmpDir)

	_, err := Destroy(context.Background(), d, "broken")
	require.NoError(t, err)
	assert.NoDirExists(t, sandboxDir)
}

func TestDestroy_ReadOnlyFiles(t *testing.T) {
	tmpDir := t.TempDir()

	name := "test-destroy-readonly"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	// Create read-only nested structure (like Go module cache)
	readonlyDir := filepath.Join(sandboxDir, "work", "modcache", "pkg")
	require.NoError(t, os.MkdirAll(readonlyDir, 0750))
	testutil.WriteFile(t, readonlyDir, "mod.go", "package mod")
	// Make directory read-only
	require.NoError(t, os.Chmod(readonlyDir, 0o555))               //nolint:gosec // intentionally read-only for test
	require.NoError(t, os.Chmod(filepath.Dir(readonlyDir), 0o555)) //nolint:gosec // intentionally read-only for test

	meta := &store.Environment{
		Name:      name,
		AgentType: "claude",
		CreatedAt: time.Now(),
		Dirs:      []store.DirEnvironment{{HostPath: "/tmp/project", Mode: "copy"}},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))

	mock := &lifecycleMockRuntime{}
	d := newLifecycleDeps(mock, tmpDir)

	_, err := Destroy(context.Background(), d, name)
	require.NoError(t, err)
	assert.NoDirExists(t, sandboxDir)
}

// NeedsConfirmation edge cases

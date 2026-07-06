// ABOUTME: Tests for forceRemoveAll — verifies read-only nested trees are made
// ABOUTME: writable and removed, and that a missing path is a no-op.
package launch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/orchestrator/state"
	"github.com/kstenerud/yoloai/internal/sysexec"
)

// TestTeardown_ReapsInjectorBeforeDeletingDir guards DF71: Teardown must kill the
// recorded credential injector before it deletes the sandbox dir — otherwise the
// detached process is orphaned with its PID record (injector.json) gone, so no
// later Stop can ever find it. Uses a live stand-in process (sleep) as the
// "injector".
func TestTeardown_ReapsInjectorBeforeDeletingDir(t *testing.T) {
	layout := config.NewLayout(t.TempDir())
	d := state.Deps{Runtime: &fakeRuntime{}, Layout: layout}

	dir := layout.SandboxDir("box")
	require.NoError(t, fileutil.MkdirAll(dir, 0o755))

	// A live "injector": a blocking child with a reaper goroutine so a SIGTERM'd
	// child is reaped rather than lingering as a zombie (mirroring init reaping
	// the detached injector in production).
	cmd := sysexec.Command([]string{}, "sleep", "300")
	require.NoError(t, cmd.Start())
	go func() { _ = cmd.Wait() }()
	pid := cmd.Process.Pid
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	rec := fmt.Sprintf(`{"pid":%d,"addr":"127.0.0.1:1"}`, pid)
	require.NoError(t, fileutil.WriteFile(filepath.Join(dir, "injector.json"), []byte(rec), 0o600))

	_, err := Teardown(context.Background(), d, "box")
	require.NoError(t, err)

	assert.NoDirExists(t, dir, "teardown removes the sandbox dir")
	// The injector process must be dead — killed before the dir (and its record)
	// were deleted.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && syscall.Kill(pid, 0) == nil {
		time.Sleep(20 * time.Millisecond)
	}
	assert.Error(t, syscall.Kill(pid, 0), "injector process was reaped by teardown")
}

func TestForceRemoveAll_ReadOnlyNested(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "a", "b", "c")
	require.NoError(t, os.MkdirAll(nested, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(nested, "file.txt"), []byte("content"), 0600))

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

// ABOUTME: Tests that rollbackPartialLaunch reaps the container + injector on a
// ABOUTME: failed launch, and does so on a live (detached) context even when the
// ABOUTME: caller's context was already cancelled (the Ctrl-C case).
package launch

import (
	"context"
	"fmt"
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
	"github.com/kstenerud/yoloai/runtime"
)

// recordingRuntime records Stop/Remove and whether the context it received was
// already cancelled. The embedded nil Backend panics on any other method, proving
// rollback touches only Stop+Remove.
type recordingRuntime struct {
	runtime.Backend
	stopped, removed []string
	ctxLiveAtStop    bool
}

func (r *recordingRuntime) Stop(ctx context.Context, name string) error {
	r.stopped = append(r.stopped, name)
	r.ctxLiveAtStop = ctx.Err() == nil
	return nil
}

func (r *recordingRuntime) Remove(ctx context.Context, name string) error {
	r.removed = append(r.removed, name)
	return nil
}

func TestRollbackPartialLaunch_ReapsContainerAndInjectorOnCancelledCtx(t *testing.T) {
	dir := t.TempDir()

	// A live "injector" recorded under the sandbox dir.
	cmd := sysexec.Command([]string{}, "sleep", "300")
	require.NoError(t, cmd.Start())
	go func() { _ = cmd.Wait() }()
	pid := cmd.Process.Pid
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	rec := fmt.Sprintf(`{"pid":%d,"addr":"127.0.0.1:1"}`, pid)
	require.NoError(t, fileutil.WriteFile(filepath.Join(dir, "injector.json"), []byte(rec), 0o600))

	st := &state.State{Name: "box", SandboxDir: dir, Layout: config.NewLayout(t.TempDir()).WithPrincipal(config.CLIPrincipal)}
	rt := &recordingRuntime{}

	// The caller's context is ALREADY cancelled — the Ctrl-C case. Rollback must
	// still reach the backend by detaching the cancellation.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rollbackPartialLaunch(ctx, rt, st)

	cname := "yoloai-cli-box" // InstanceName for the CLI principal
	assert.Equal(t, []string{cname}, rt.stopped, "stops the instance")
	assert.Equal(t, []string{cname}, rt.removed, "removes the instance (triggers netns teardown)")
	assert.True(t, rt.ctxLiveAtStop, "cleanup runs on a detached (non-cancelled) context")

	// The injector must be reaped.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && syscall.Kill(pid, 0) == nil {
		time.Sleep(20 * time.Millisecond)
	}
	assert.Error(t, syscall.Kill(pid, 0), "injector process was reaped")
}

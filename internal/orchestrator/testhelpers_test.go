package orchestrator

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/orchestrator/agentcfg"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/store"
	"github.com/kstenerud/yoloai/internal/testutil"
)

// lifecycleMockRuntime extends mockRuntime for lifecycle-related tests in
// package sandbox (clone_test.go, terminal_test.go). Mirrors the struct that
// lived in the old lifecycle_test.go before it moved to sandbox/lifecycle/.
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

// newLifecycleMgr creates an Engine with the given mock runtime rooted at
// tmpDir/.yoloai. Used by terminal_test.go and any other sandbox tests that
// need an engine before lifecycle methods moved to the lifecycle/ sub-package.
func newLifecycleMgr(rt *lifecycleMockRuntime, tmpDir string) *Engine {
	layout := config.NewLayout(filepath.Join(tmpDir, ".yoloai"))
	return NewEngineWithRuntime(rt, slog.Default(), strings.NewReader(""), WithLayout(layout))
}

// createTestSandbox creates a sandbox directory with environment.json for tests.
func createTestSandbox(t *testing.T, tmpDir, name, hostPath string, mode store.DirMode) {
	t.Helper()
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	meta := &store.Environment{
		Name:      name,
		CreatedAt: time.Now(),
		Dirs: []store.DirEnvironment{{
			HostPath:  hostPath,
			MountPath: hostPath,
			Mode:      mode,
		}},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))
	// agent/model are the inside-process config, kept in the sibling agent.json
	// (Q104). Write it so agent-resolving paths see a configured agent.
	require.NoError(t, agentcfg.Save(sandboxDir, &agentcfg.AgentConfig{AgentType: "claude"}))
}

func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	testutil.WriteFile(t, dir, name, content)
}

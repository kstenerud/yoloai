// ABOUTME: Minimal runtime.Runtime stub for lifecycle-package white-box tests.
// ABOUTME: Mirrors the zero-value behavior of sandbox.mockRuntime without
// ABOUTME: importing the façade (which would create an import cycle).
package lifecycle

import (
	"context"
	"io"
	"log/slog"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"
)

// Compile-time check.
var _ runtime.Runtime = (*lifecycleMockRuntime)(nil)

var errMockNotImplemented = &mockNotImplError{}

type mockNotImplError struct{}

func (e *mockNotImplError) Error() string { return "mock: not implemented" }

// lifecycleMockRuntime is a configurable runtime.Runtime for lifecycle-package tests.
// Hooks that are nil fall back to nil-safe defaults (Stop/Start/Remove return nil,
// Inspect returns errMockNotImplemented, Exec returns errMockNotImplemented).
type lifecycleMockRuntime struct {
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
	return runtime.ExecResult{}, errMockNotImplemented
}

func (m *lifecycleMockRuntime) Setup(_ context.Context, _ config.Layout, _ string, _ io.Writer, _ *slog.Logger, _ bool) error {
	return nil
}
func (m *lifecycleMockRuntime) IsReady(_ context.Context) (bool, error) { return false, nil }
func (m *lifecycleMockRuntime) Create(_ context.Context, _ runtime.InstanceConfig) error {
	return errMockNotImplemented
}
func (m *lifecycleMockRuntime) GitExec(_ context.Context, _ string, _ string, _ ...string) (string, error) {
	return "", errMockNotImplemented
}
func (m *lifecycleMockRuntime) InteractiveExec(_ context.Context, _ string, _ []string, _ string, _ string, _ runtime.IOStreams) error {
	return errMockNotImplemented
}
func (m *lifecycleMockRuntime) Close() error { return nil }
func (m *lifecycleMockRuntime) Prune(_ context.Context, _ []string, _ bool, _ io.Writer) (runtime.PruneResult, error) {
	return runtime.PruneResult{}, errMockNotImplemented
}
func (m *lifecycleMockRuntime) Logs(_ context.Context, _ string, _ int) string { return "" }
func (m *lifecycleMockRuntime) DiagHint(name string) string {
	return "check logs for " + name
}
func (m *lifecycleMockRuntime) TmuxSocket(_ string) string { return "" }
func (m *lifecycleMockRuntime) AttachCommand(_ string, _, _ int, _ runtime.IsolationMode) []string {
	return nil
}
func (m *lifecycleMockRuntime) PrepareAgentCommand(cmd string) string { return cmd }
func (m *lifecycleMockRuntime) Descriptor() runtime.BackendDescriptor {
	return runtime.BackendDescriptor{
		Type:         "mock",
		BaseModeName: runtime.IsolationModeContainer,
		Capabilities: runtime.BackendCaps{
			NetworkIsolation: true,
			OverlayDirs:      true,
			CapAdd:           true,
		},
	}
}

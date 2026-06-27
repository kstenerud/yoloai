// ABOUTME: Minimal runtime.Backend stub for status-package tests, with
// ABOUTME: configurable Inspect/Exec hooks for status-detection scenarios.
package status

import (
	"context"
	"io"
	"log/slog"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/runtime"
)

// Compile-time check.
var _ runtime.Backend = (*fakeRuntime)(nil)

// fakeRuntime is a minimal runtime.Backend for status-package tests. Inspect
// and Exec dispatch to optional hooks; all other operations return
// "not implemented".
type fakeRuntime struct {
	inspectFn func(ctx context.Context, name string) (runtime.InstanceInfo, error)
	execFn    func(ctx context.Context, name string, cmd []string, user string) (runtime.ExecResult, error)
}

func (f *fakeRuntime) Inspect(ctx context.Context, name string) (runtime.InstanceInfo, error) {
	if f.inspectFn != nil {
		return f.inspectFn(ctx, name)
	}
	return runtime.InstanceInfo{}, errFakeNotImplemented
}

func (f *fakeRuntime) Exec(ctx context.Context, name string, cmd []string, user string) (runtime.ExecResult, error) {
	if f.execFn != nil {
		return f.execFn(ctx, name, cmd, user)
	}
	return runtime.ExecResult{}, errFakeNotImplemented
}

func (f *fakeRuntime) Setup(_ context.Context, _ config.Layout, _ string, _ io.Writer, _ *slog.Logger, _ bool) error {
	return nil
}
func (f *fakeRuntime) IsReady(_ context.Context) (bool, error) { return false, nil }
func (f *fakeRuntime) Create(_ context.Context, _ runtime.InstanceConfig) error {
	return errFakeNotImplemented
}
func (f *fakeRuntime) Start(_ context.Context, _ string) error  { return errFakeNotImplemented }
func (f *fakeRuntime) Stop(_ context.Context, _ string) error   { return errFakeNotImplemented }
func (f *fakeRuntime) Remove(_ context.Context, _ string) error { return errFakeNotImplemented }
func (f *fakeRuntime) GitExec(_ context.Context, _ string, _ string, _ ...string) (string, error) {
	return "", errFakeNotImplemented
}
func (f *fakeRuntime) InteractiveExec(_ context.Context, _ string, _ []string, _ string, _ string, _ runtime.IOStreams) error {
	return errFakeNotImplemented
}
func (f *fakeRuntime) Close() error { return nil }
func (f *fakeRuntime) Prune(_ context.Context, _ []string, _ bool, _ io.Writer) (runtime.PruneResult, error) {
	return runtime.PruneResult{}, errFakeNotImplemented
}
func (f *fakeRuntime) Logs(_ context.Context, _ string, _ int) string { return "" }
func (f *fakeRuntime) DiagHint(name string) string                    { return "check logs for " + name }
func (f *fakeRuntime) TmuxSocket(_ string) string                     { return "" }
func (f *fakeRuntime) AttachCommand(_ string, _, _ int, _ runtime.IsolationMode) []string {
	return nil
}
func (f *fakeRuntime) Descriptor() runtime.BackendDescriptor {
	return runtime.BackendDescriptor{
		Type:         "mock",
		BaseModeName: runtime.IsolationModeContainer,
		Capabilities: runtime.BackendCaps{
			NetworkIsolation:   true,
			OverlayDirs:        true,
			CapAdd:             true,
			FilesystemLocality: runtime.LocalitySandboxSide,
		},
	}
}

var errFakeNotImplemented = &fakeNotImplError{}

type fakeNotImplError struct{}

func (e *fakeNotImplError) Error() string { return "fake runtime: not implemented" }

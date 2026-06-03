// ABOUTME: Minimal runtime.Runtime stub for create-package white-box tests.
// ABOUTME: Mirrors the zero-value behavior of sandbox.mockRuntime without
// ABOUTME: importing the façade (which would create an import cycle).
package create

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"
)

// Compile-time check.
var _ runtime.Runtime = (*fakeRuntime)(nil)

// fakeRuntime is a minimal runtime.Runtime for create-package tests.
// All container operations return "not implemented" (matching sandbox.mockRuntime).
type fakeRuntime struct{}

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
func (f *fakeRuntime) Inspect(_ context.Context, _ string) (runtime.InstanceInfo, error) {
	return runtime.InstanceInfo{}, errFakeNotImplemented
}
func (f *fakeRuntime) Exec(_ context.Context, _ string, _ []string, _ string) (runtime.ExecResult, error) {
	return runtime.ExecResult{}, errFakeNotImplemented
}
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
func (f *fakeRuntime) PrepareAgentCommand(cmd string) string { return cmd }
func (f *fakeRuntime) Descriptor() runtime.BackendDescriptor {
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

// fakeGuestMountRuntime is a fakeRuntime that also implements
// runtime.GuestMountResolver, re-rooting host dirs under /guest (mirroring how
// tart maps them under /Users/admin/host/...).
type fakeGuestMountRuntime struct {
	fakeRuntime
}

var _ runtime.GuestMountResolver = (*fakeGuestMountRuntime)(nil)

func (f *fakeGuestMountRuntime) ResolveGuestMountPath(containerPath string) string {
	return "/guest" + containerPath
}

var errFakeNotImplemented = &fakeNotImplError{}

type fakeNotImplError struct{}

func (e *fakeNotImplError) Error() string { return "fake runtime: not implemented" }

// layoutForTmpDir builds a Layout rooted at tmpDir/.yoloai for tests
// that exercise functions which use a Layout. Mirrors what the
// CLI does at startup so tests don't depend on ambient HOME.
func layoutForTmpDir(tmpDir string) config.Layout {
	l := config.NewLayout(filepath.Join(tmpDir, ".yoloai"))
	// Mirror the CLI boundary: capture the process env as the Layout's host-env
	// snapshot so credential checks (which read Layout.Env, not os.Getenv) see
	// any keys the test set via t.Setenv.
	env := make(map[string]string)
	for _, e := range os.Environ() {
		if k, v, ok := strings.Cut(e, "="); ok {
			env[k] = v
		}
	}
	l.Env = env
	return l
}

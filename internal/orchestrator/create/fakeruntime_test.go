// ABOUTME: Minimal runtime.Backend stub for create-package white-box tests.
// ABOUTME: Mirrors the zero-value behavior of sandbox.mockRuntime without
// ABOUTME: importing the façade (which would create an import cycle).
package create

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/testutil"
)

// Compile-time check.
var _ runtime.Backend = (*fakeRuntime)(nil)

// fakeRuntime is a minimal runtime.Backend for create-package tests.
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
func (f *fakeRuntime) Descriptor() runtime.BackendDescriptor {
	return runtime.BackendDescriptor{
		Type:         "mock",
		BaseModeName: runtime.IsolationModeContainer,
		Capabilities: runtime.BackendCaps{
			NetworkIsolation:   true,
			OverlayDirs:        true,
			CapAdd:             true,
			FilesystemLocality: runtime.LocalityHostSide,
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
	// Mirror the CLI boundary: thread the host env so credential checks (which
	// read the Layout, not os.Getenv) see the keys these tests inject via
	// t.Setenv. The allowlist is every registered agent's API-key/auth-hint vars
	// — the only env these prepare-flow tests set — never the full ambient env.
	l = l.WithEnv(testutil.GetCuratedHostEnv(agentCredentialEnvVars()))
	return l
}

// agentCredentialEnvVars collects the union of every registered agent's API-key
// and auth-hint env var names. layoutForTmpDir allowlists these so a test that
// sets a credential via t.Setenv has it surface through the Layout's curated
// snapshot, without admitting any unrelated ambient var.
func agentCredentialEnvVars() []string {
	var keys []string
	for _, name := range agent.AllAgentTypes() {
		def := agent.GetAgent(name)
		if def == nil {
			continue
		}
		keys = append(keys, def.APIKeyEnvVars...)
		keys = append(keys, def.AuthHintEnvVars...)
	}
	return keys
}

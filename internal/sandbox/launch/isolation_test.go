package launch

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/runtime/caps"
)

// Compile-time check.
var _ runtime.Runtime = (*fakeRuntime)(nil)

// fakeRuntime is a minimal runtime.Runtime for launch-package tests. It does
// not implement the optional CapabilityRequirer interface, so
// RequiredCapabilitiesFor returns nil for it.
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
	return runtime.BackendDescriptor{Type: "mock", BaseModeName: runtime.IsolationModeContainer}
}

var errFakeNotImplemented = fmt.Errorf("fake runtime: not implemented")

// capsRuntime wraps fakeRuntime and overrides RequiredCapabilities for testing.
type capsRuntime struct {
	fakeRuntime
	capList []caps.HostCapability
}

func (c *capsRuntime) RequiredCapabilities(_ runtime.IsolationMode) []caps.HostCapability {
	return c.capList
}

func TestCheckIsolationPrerequisites_NoCaps(t *testing.T) {
	// fakeRuntime doesn't implement CapabilityRequirer — should be a no-op.
	rt := &fakeRuntime{}
	err := CheckIsolationPrerequisites(context.Background(), rt, "container-enhanced")
	assert.NoError(t, err)
}

func TestCheckIsolationPrerequisites_AllCapsPass(t *testing.T) {
	rt := &capsRuntime{
		capList: []caps.HostCapability{
			{ID: "a", Summary: "Cap A", Check: func(_ context.Context) error { return nil }},
		},
	}
	err := CheckIsolationPrerequisites(context.Background(), rt, "vm")
	assert.NoError(t, err)
}

func TestCheckIsolationPrerequisites_CapFails(t *testing.T) {
	rt := &capsRuntime{
		capList: []caps.HostCapability{
			{ID: "kata-shim", Summary: "kata shim", Check: func(_ context.Context) error { return fmt.Errorf("kata shim not found") }},
		},
	}
	err := CheckIsolationPrerequisites(context.Background(), rt, "vm")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kata shim")
}

func TestCheckIsolationPrerequisites_IsolationModeForwarded(t *testing.T) {
	rt := &capsRuntime{}
	// The base capsRuntime returns nil caps. Verify CheckIsolationPrerequisites
	// doesn't panic for any mode.
	for _, mode := range []runtime.IsolationMode{"container", "container-enhanced", "vm", "vm-enhanced", ""} {
		err := CheckIsolationPrerequisites(context.Background(), rt, mode)
		assert.NoError(t, err, "mode %q should not fail with nil caps", mode)
	}
}

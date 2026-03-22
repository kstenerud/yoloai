package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/config"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/runtime/caps"
	dockerrt "github.com/kstenerud/yoloai/runtime/docker"
)

var errMockNotImplemented = fmt.Errorf("mock: not implemented")

// mockRuntime implements runtime.Runtime for testing.
type mockRuntime struct {
	isReadyResult bool  // returned by IsReady
	isReadyErr    error // error returned by IsReady
	setupCalled   bool  // whether Setup was invoked
	setupErr      error // error returned by Setup
}

// Compile-time check.
var _ runtime.Runtime = (*mockRuntime)(nil)

func (m *mockRuntime) Setup(_ context.Context, _ string, _ io.Writer, _ *slog.Logger, _ bool) error {
	m.setupCalled = true
	return m.setupErr
}

func (m *mockRuntime) IsReady(_ context.Context) (bool, error) {
	return m.isReadyResult, m.isReadyErr
}

func (m *mockRuntime) Create(_ context.Context, _ runtime.InstanceConfig) error {
	return errMockNotImplemented
}

func (m *mockRuntime) Start(_ context.Context, _ string) error {
	return errMockNotImplemented
}

func (m *mockRuntime) Stop(_ context.Context, _ string) error {
	return errMockNotImplemented
}

func (m *mockRuntime) Remove(_ context.Context, _ string) error {
	return errMockNotImplemented
}

func (m *mockRuntime) Inspect(_ context.Context, _ string) (runtime.InstanceInfo, error) {
	return runtime.InstanceInfo{}, errMockNotImplemented
}

func (m *mockRuntime) Exec(_ context.Context, _ string, _ []string, _ string) (runtime.ExecResult, error) {
	return runtime.ExecResult{}, errMockNotImplemented
}

func (m *mockRuntime) InteractiveExec(_ context.Context, _ string, _ []string, _ string, _ string) error {
	return errMockNotImplemented
}

func (m *mockRuntime) Close() error {
	return nil
}

func (m *mockRuntime) Prune(_ context.Context, _ []string, _ bool, _ io.Writer) (runtime.PruneResult, error) {
	return runtime.PruneResult{}, errMockNotImplemented
}

func (m *mockRuntime) Logs(_ context.Context, _ string, _ int) string { return "" }
func (m *mockRuntime) DiagHint(instanceName string) string {
	return "check logs for " + instanceName
}

func (m *mockRuntime) Name() string                                        { return "docker" }
func (m *mockRuntime) TmuxSocket(_ string) string                          { return "" }
func (m *mockRuntime) AttachCommand(_ string, _, _ int, _ string) []string { return nil }
func (m *mockRuntime) AgentProvisionedByBackend() bool                     { return true }
func (m *mockRuntime) ResolveCopyMount(_, hostPath string) string          { return hostPath }
func (m *mockRuntime) BaseModeName() string                                { return "container" }
func (m *mockRuntime) SupportedIsolationModes() []string                   { return nil }
func (m *mockRuntime) RequiredCapabilities(_ string) []caps.HostCapability { return nil }
func (m *mockRuntime) Capabilities() runtime.BackendCaps {
	return runtime.BackendCaps{
		NetworkIsolation: true,
		OverlayDirs:      true,
		CapAdd:           true,
	}
}

func TestEnsureSetup_CreatesDirectories(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	mock := &mockRuntime{} // image exists (no error)
	mgr := NewManager(mock, slog.Default(), strings.NewReader(""), io.Discard)

	err := mgr.EnsureSetup(context.Background())
	require.NoError(t, err)

	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	for _, sub := range []string{"sandboxes", "profiles", "cache"} {
		info, err := os.Stat(filepath.Join(yoloaiDir, sub))
		require.NoError(t, err, "%s should exist", sub)
		assert.True(t, info.IsDir())
	}
}

func TestEnsureSetup_WritesConfigOnFirstRun(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	mock := &mockRuntime{}
	var output bytes.Buffer
	mgr := NewManager(mock, slog.Default(), strings.NewReader(""), &output)

	err := mgr.EnsureSetup(context.Background())
	require.NoError(t, err)

	configPath := filepath.Join(tmpDir, ".yoloai", "defaults", "config.yaml")
	content, err := os.ReadFile(configPath) //nolint:gosec // G304: test code with temp dir
	require.NoError(t, err)
	assert.Contains(t, string(content), "agent")

	// Check completion hint was printed
	assert.Contains(t, output.String(), "completion")
}

func TestEnsureSetup_WritesStateOnFirstRun(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	mock := &mockRuntime{}
	mgr := NewManager(mock, slog.Default(), strings.NewReader(""), io.Discard)

	err := mgr.EnsureSetup(context.Background())
	require.NoError(t, err)

	statePath := filepath.Join(tmpDir, ".yoloai", "state.yaml")
	_, err = os.Stat(statePath)
	require.NoError(t, err, "state.yaml should exist")
}

func TestEnsureSetup_SkipsConfigOnSubsequentRun(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Pre-create defaults/config.yaml and state.yaml
	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	defaultsDir := filepath.Join(yoloaiDir, "defaults")
	require.NoError(t, os.MkdirAll(defaultsDir, 0750))
	customContent := []byte("# custom config\n# agent: claude\n")
	require.NoError(t, os.WriteFile(filepath.Join(defaultsDir, "config.yaml"), customContent, 0600))
	require.NoError(t, config.SaveState(&config.State{SetupComplete: true}))

	mock := &mockRuntime{}
	var output bytes.Buffer
	mgr := NewManager(mock, slog.Default(), strings.NewReader(""), &output)

	err := mgr.EnsureSetup(context.Background())
	require.NoError(t, err)

	// Config should be preserved
	content, err := os.ReadFile(filepath.Join(defaultsDir, "config.yaml")) //nolint:gosec // G304: test code with temp dir
	require.NoError(t, err)
	assert.Equal(t, customContent, content)

	// Completion hint should NOT be printed
	assert.NotContains(t, output.String(), "completion")
}

func TestEnsureSetup_AlwaysCallsSetup(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Simulate a prior successful build by recording the current checksum in defaults/
	defaultsDir := filepath.Join(tmpDir, ".yoloai", "defaults")
	require.NoError(t, os.MkdirAll(defaultsDir, 0750))
	dockerrt.RecordBuildChecksum(defaultsDir)

	mock := &mockRuntime{} // Setup returns nil (success)
	mgr := NewManager(mock, slog.Default(), strings.NewReader(""), io.Discard)

	err := mgr.EnsureSetup(context.Background())
	require.NoError(t, err)
	assert.True(t, mock.setupCalled, "Setup should always be called")
}

func TestEnsureSetup_RebuildWhenChecksumStale(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Simulate a stale build: write a different checksum to defaults/
	defaultsDir := filepath.Join(tmpDir, ".yoloai", "defaults")
	require.NoError(t, os.MkdirAll(defaultsDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(defaultsDir, ".last-build-checksum"), []byte("stale-checksum"), 0600))

	mock := &mockRuntime{} // Setup returns nil (success)
	var output bytes.Buffer
	mgr := NewManager(mock, slog.Default(), strings.NewReader(""), &output)

	err := mgr.EnsureSetup(context.Background())
	require.NoError(t, err)
	assert.True(t, mock.setupCalled, "Setup should be called when checksum is stale")
}

func TestEnsureSetup_BuildsWhenImageMissing(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	mock := &mockRuntime{} // Setup returns nil (success)
	var output bytes.Buffer
	mgr := NewManager(mock, slog.Default(), strings.NewReader(""), &output)

	err := mgr.EnsureSetup(context.Background())
	require.NoError(t, err)
	assert.True(t, mock.setupCalled, "Setup should be called when image is missing")
}

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

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"
	dockerrt "github.com/kstenerud/yoloai/internal/runtime/docker"
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

func (m *mockRuntime) Setup(_ context.Context, _ config.Layout, _ string, _ io.Writer, _ *slog.Logger, _ bool) error {
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

func (m *mockRuntime) GitExec(_ context.Context, _ string, _ string, _ ...string) (string, error) {
	return "", errMockNotImplemented
}

func (m *mockRuntime) InteractiveExec(_ context.Context, _ string, _ []string, _ string, _ string, _ runtime.IOStreams) error {
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

func (m *mockRuntime) TmuxSocket(_ string) string { return "" }
func (m *mockRuntime) AttachCommand(_ string, _, _ int, _ runtime.IsolationMode) []string {
	return nil
}
func (m *mockRuntime) PrepareAgentCommand(cmd string) string { return cmd }

func (m *mockRuntime) Descriptor() runtime.BackendDescriptor {
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

func TestEnsureSetup_CreatesDirectories(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	mock := &mockRuntime{} // image exists (no error)
	layout := config.NewLayout(filepath.Join(tmpDir, ".yoloai"))
	mgr := NewEngine(mock, slog.Default(), strings.NewReader(""), WithLayout(layout))

	err := mgr.EnsureSetup(context.Background(), io.Discard)
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
	layout := config.NewLayout(filepath.Join(tmpDir, ".yoloai"))
	mgr := NewEngine(mock, slog.Default(), strings.NewReader(""), WithLayout(layout))

	err := mgr.EnsureSetup(context.Background(), io.Discard)
	require.NoError(t, err)

	configPath := filepath.Join(tmpDir, ".yoloai", "defaults", "config.yaml")
	content, err := os.ReadFile(configPath) //nolint:gosec // G304: test code with temp dir
	require.NoError(t, err)
	assert.Contains(t, string(content), "agent")
}

// TestEnsureSetup_DoesNotStampSchemaVersion guards the no-silent-migration
// invariant: stamping/migrating the schema version is the startup gate's
// (fresh-create) or the explicit `system migrate` command's job, never a side
// effect of EnsureSetup. A regression that re-adds an auto-stamp here would
// reintroduce the silent auto-migration this design removed.
func TestEnsureSetup_DoesNotStampSchemaVersion(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	mock := &mockRuntime{}
	layout := config.NewLayout(filepath.Join(tmpDir, ".yoloai"))
	mgr := NewEngine(mock, slog.Default(), strings.NewReader(""), WithLayout(layout))

	err := mgr.EnsureSetup(context.Background(), io.Discard)
	require.NoError(t, err)

	_, exists, err := config.ReadSchemaVersion(layout.SchemaVersionPath())
	require.NoError(t, err)
	assert.False(t, exists, "EnsureSetup must not stamp .schema-version (gate/migrate owns that)")
}

func TestEnsureSetup_PreservesConfigOnSubsequentRun(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Pre-create defaults/config.yaml simulating an existing install.
	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	defaultsDir := filepath.Join(yoloaiDir, "defaults")
	require.NoError(t, os.MkdirAll(defaultsDir, 0750))
	customContent := []byte("# custom config\n# agent: claude\n")
	require.NoError(t, os.WriteFile(filepath.Join(defaultsDir, "config.yaml"), customContent, 0600))
	layout := config.NewLayout(yoloaiDir)

	mock := &mockRuntime{}
	mgr := NewEngine(mock, slog.Default(), strings.NewReader(""), WithLayout(layout))

	err := mgr.EnsureSetup(context.Background(), io.Discard)
	require.NoError(t, err)

	// Existing config should be preserved, not stomped.
	content, err := os.ReadFile(filepath.Join(defaultsDir, "config.yaml")) //nolint:gosec // G304: test code with temp dir
	require.NoError(t, err)
	assert.Equal(t, customContent, content)
}

func TestEnsureSetup_AlwaysCallsSetup(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Simulate a prior successful build by recording the current checksum.
	layout := config.NewLayout(filepath.Join(tmpDir, ".yoloai"))
	require.NoError(t, os.MkdirAll(layout.CacheDir(), 0750))
	dockerrt.RecordBuildChecksum(layout, "")
	mock := &mockRuntime{}
	mgr := NewEngine(mock, slog.Default(), strings.NewReader(""), WithLayout(layout))

	err := mgr.EnsureSetup(context.Background(), io.Discard)
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
	layout := config.NewLayout(filepath.Join(tmpDir, ".yoloai"))
	mgr := NewEngine(mock, slog.Default(), strings.NewReader(""), WithLayout(layout))

	err := mgr.EnsureSetup(context.Background(), &output)
	require.NoError(t, err)
	assert.True(t, mock.setupCalled, "Setup should be called when checksum is stale")
}

func TestEnsureSetup_BuildsWhenImageMissing(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	mock := &mockRuntime{} // Setup returns nil (success)
	var output bytes.Buffer
	layout := config.NewLayout(filepath.Join(tmpDir, ".yoloai"))
	mgr := NewEngine(mock, slog.Default(), strings.NewReader(""), WithLayout(layout))

	err := mgr.EnsureSetup(context.Background(), &output)
	require.NoError(t, err)
	assert.True(t, mock.setupCalled, "Setup should be called when image is missing")
}

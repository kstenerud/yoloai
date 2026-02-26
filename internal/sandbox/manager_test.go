package sandbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/runtime"
	dockerrt "github.com/kstenerud/yoloai/internal/runtime/docker"
)

var errMockNotImplemented = fmt.Errorf("mock: not implemented")

// mockRuntime implements runtime.Runtime for testing.
type mockRuntime struct {
	imageExistsResult bool  // returned by ImageExists
	imageExistsErr    error // error returned by ImageExists
	ensureImageCalled bool  // whether EnsureImage was invoked
	ensureImageErr    error // error returned by EnsureImage
}

// Compile-time check.
var _ runtime.Runtime = (*mockRuntime)(nil)

func (m *mockRuntime) EnsureImage(_ context.Context, _ string, _ io.Writer, _ *slog.Logger, _ bool) error {
	m.ensureImageCalled = true
	return m.ensureImageErr
}

func (m *mockRuntime) ImageExists(_ context.Context, _ string) (bool, error) {
	return m.imageExistsResult, m.imageExistsErr
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

func (m *mockRuntime) InteractiveExec(_ context.Context, _ string, _ []string, _ string) error {
	return errMockNotImplemented
}

func (m *mockRuntime) Close() error {
	return nil
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

	configPath := filepath.Join(tmpDir, ".yoloai", "config.yaml")
	content, err := os.ReadFile(configPath) //nolint:gosec // G304: test code with temp dir
	require.NoError(t, err)
	assert.Contains(t, string(content), "agent: claude")

	// Check completion hint was printed
	assert.Contains(t, output.String(), "completion")
}

func TestEnsureSetup_SkipsConfigOnSubsequentRun(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Pre-create config.yaml with custom content (valid YAML, setup already done)
	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))
	customContent := []byte("# custom config\nsetup_complete: true\ndefaults:\n  agent: claude\n")
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "config.yaml"), customContent, 0600))

	mock := &mockRuntime{}
	var output bytes.Buffer
	mgr := NewManager(mock, slog.Default(), strings.NewReader(""), &output)

	err := mgr.EnsureSetup(context.Background())
	require.NoError(t, err)

	// Config should be preserved (setup_complete is true, so no modification)
	content, err := os.ReadFile(filepath.Join(yoloaiDir, "config.yaml")) //nolint:gosec // G304: test code with temp dir
	require.NoError(t, err)
	assert.Equal(t, customContent, content)

	// Completion hint should NOT be printed
	assert.NotContains(t, output.String(), "completion")
}

func TestEnsureSetup_SkipsBuildWhenImageExists(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Pre-seed resources and simulate a prior successful build
	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	_, err := dockerrt.SeedResources(yoloaiDir)
	require.NoError(t, err)
	dockerrt.RecordBuildChecksum(yoloaiDir)

	mock := &mockRuntime{} // EnsureImage returns nil (success)
	mgr := NewManager(mock, slog.Default(), strings.NewReader(""), io.Discard)

	err = mgr.EnsureSetup(context.Background())
	require.NoError(t, err)
	assert.True(t, mock.ensureImageCalled, "EnsureImage should be called")
}

func TestEnsureSetup_RebuildWhenResourcesChanged(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// First seed to establish checksum manifest
	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	_, err := dockerrt.SeedResources(yoloaiDir)
	require.NoError(t, err)

	// Simulate a binary upgrade: write stale content with matching checksums
	// (so SeedResources sees "unmodified by user" but "differs from embedded")
	staleContent := []byte("# old version")
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "entrypoint.sh"), staleContent, 0600))
	// Update checksum to match stale content (not user-modified, just old version)
	checksumPath := filepath.Join(yoloaiDir, ".resource-checksums")
	checksumData, err := os.ReadFile(checksumPath) //nolint:gosec // G304: test code
	require.NoError(t, err)
	var checksums map[string]string
	require.NoError(t, json.Unmarshal(checksumData, &checksums))
	checksums["entrypoint.sh"] = fmt.Sprintf("%x", sha256.Sum256(staleContent))
	updated, err := json.Marshal(checksums)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(checksumPath, updated, 0600))

	mock := &mockRuntime{} // EnsureImage returns nil (success)
	var output bytes.Buffer
	mgr := NewManager(mock, slog.Default(), strings.NewReader(""), &output)

	err = mgr.EnsureSetup(context.Background())
	require.NoError(t, err)
	assert.True(t, mock.ensureImageCalled, "EnsureImage should be called when resources changed")
}

func TestEnsureSetup_BuildsWhenImageMissing(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	mock := &mockRuntime{} // EnsureImage returns nil (success)
	var output bytes.Buffer
	mgr := NewManager(mock, slog.Default(), strings.NewReader(""), &output)

	err := mgr.EnsureSetup(context.Background())
	require.NoError(t, err)
	assert.True(t, mock.ensureImageCalled, "EnsureImage should be called when image is missing")
}

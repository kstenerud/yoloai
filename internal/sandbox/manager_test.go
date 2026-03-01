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

func (m *mockRuntime) InteractiveExec(_ context.Context, _ string, _ []string, _ string, _ string) error {
	return errMockNotImplemented
}

func (m *mockRuntime) Close() error {
	return nil
}

func (m *mockRuntime) Prune(_ context.Context, _ []string, _ bool, _ io.Writer) (runtime.PruneResult, error) {
	return runtime.PruneResult{}, errMockNotImplemented
}

func (m *mockRuntime) DiagHint(instanceName string) string {
	return "check logs for " + instanceName
}

func TestEnsureSetup_CreatesDirectories(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	mock := &mockRuntime{} // image exists (no error)
	mgr := NewManager(mock, "docker", slog.Default(), strings.NewReader(""), io.Discard)

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
	mgr := NewManager(mock, "docker", slog.Default(), strings.NewReader(""), &output)

	err := mgr.EnsureSetup(context.Background())
	require.NoError(t, err)

	configPath := filepath.Join(tmpDir, ".yoloai", "profiles", "base", "config.yaml")
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
	mgr := NewManager(mock, "docker", slog.Default(), strings.NewReader(""), io.Discard)

	err := mgr.EnsureSetup(context.Background())
	require.NoError(t, err)

	statePath := filepath.Join(tmpDir, ".yoloai", "state.yaml")
	_, err = os.Stat(statePath)
	require.NoError(t, err, "state.yaml should exist")
}

func TestEnsureSetup_SkipsConfigOnSubsequentRun(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Pre-create config.yaml and state.yaml
	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	baseDir := filepath.Join(yoloaiDir, "profiles", "base")
	require.NoError(t, os.MkdirAll(baseDir, 0750))
	customContent := []byte("# custom config\nagent: claude\n")
	require.NoError(t, os.WriteFile(filepath.Join(baseDir, "config.yaml"), customContent, 0600))
	require.NoError(t, SaveState(&State{SetupComplete: true}))

	mock := &mockRuntime{}
	var output bytes.Buffer
	mgr := NewManager(mock, "docker", slog.Default(), strings.NewReader(""), &output)

	err := mgr.EnsureSetup(context.Background())
	require.NoError(t, err)

	// Config should be preserved
	content, err := os.ReadFile(filepath.Join(baseDir, "config.yaml")) //nolint:gosec // G304: test code with temp dir
	require.NoError(t, err)
	assert.Equal(t, customContent, content)

	// Completion hint should NOT be printed
	assert.NotContains(t, output.String(), "completion")
}

func TestEnsureSetup_SkipsBuildWhenImageExists(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Pre-seed resources to profiles/base/ and simulate a prior successful build
	baseDir := filepath.Join(tmpDir, ".yoloai", "profiles", "base")
	_, err := dockerrt.SeedResources(baseDir)
	require.NoError(t, err)
	dockerrt.RecordBuildChecksum(baseDir)

	mock := &mockRuntime{} // EnsureImage returns nil (success)
	mgr := NewManager(mock, "docker", slog.Default(), strings.NewReader(""), io.Discard)

	err = mgr.EnsureSetup(context.Background())
	require.NoError(t, err)
	assert.True(t, mock.ensureImageCalled, "EnsureImage should be called")
}

func TestEnsureSetup_RebuildWhenResourcesChanged(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// First seed to establish checksum manifest
	baseDir := filepath.Join(tmpDir, ".yoloai", "profiles", "base")
	_, err := dockerrt.SeedResources(baseDir)
	require.NoError(t, err)

	// Simulate a binary upgrade: write stale content with matching checksums
	staleContent := []byte("# old version")
	require.NoError(t, os.WriteFile(filepath.Join(baseDir, "entrypoint.sh"), staleContent, 0600))
	// Update checksum to match stale content
	checksumPath := filepath.Join(baseDir, ".resource-checksums")
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
	mgr := NewManager(mock, "docker", slog.Default(), strings.NewReader(""), &output)

	err = mgr.EnsureSetup(context.Background())
	require.NoError(t, err)
	assert.True(t, mock.ensureImageCalled, "EnsureImage should be called when resources changed")
}

func TestEnsureSetup_BuildsWhenImageMissing(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	mock := &mockRuntime{} // EnsureImage returns nil (success)
	var output bytes.Buffer
	mgr := NewManager(mock, "docker", slog.Default(), strings.NewReader(""), &output)

	err := mgr.EnsureSetup(context.Background())
	require.NoError(t, err)
	assert.True(t, mock.ensureImageCalled, "EnsureImage should be called when image is missing")
}

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
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/docker"
)

var errMockNotImplemented = fmt.Errorf("mock: not implemented")

// mockClient implements docker.Client for testing.
type mockClient struct {
	imageExistsErr error  // error returned by ImageInspectWithRaw
	buildCalled    bool   // whether ImageBuild was invoked
	buildErr       error  // error returned by ImageBuild
}

// Compile-time check.
var _ docker.Client = (*mockClient)(nil)

func (m *mockClient) ImageBuild(_ context.Context, _ io.Reader, _ build.ImageBuildOptions) (build.ImageBuildResponse, error) {
	m.buildCalled = true
	if m.buildErr != nil {
		return build.ImageBuildResponse{}, m.buildErr
	}
	body := io.NopCloser(bytes.NewReader([]byte("{\"stream\":\"done\\n\"}\n")))
	return build.ImageBuildResponse{Body: body}, nil
}

func (m *mockClient) ImageInspectWithRaw(_ context.Context, _ string) (image.InspectResponse, []byte, error) {
	if m.imageExistsErr != nil {
		return image.InspectResponse{}, nil, m.imageExistsErr
	}
	return image.InspectResponse{}, nil, nil
}

func (m *mockClient) ContainerCreate(_ context.Context, _ *container.Config, _ *container.HostConfig, _ *network.NetworkingConfig, _ *ocispec.Platform, _ string) (container.CreateResponse, error) {
	return container.CreateResponse{}, errMockNotImplemented
}

func (m *mockClient) ContainerStart(_ context.Context, _ string, _ container.StartOptions) error {
	return errMockNotImplemented
}

func (m *mockClient) ContainerStop(_ context.Context, _ string, _ container.StopOptions) error {
	return errMockNotImplemented
}

func (m *mockClient) ContainerRemove(_ context.Context, _ string, _ container.RemoveOptions) error {
	return errMockNotImplemented
}

func (m *mockClient) ContainerList(_ context.Context, _ container.ListOptions) ([]container.Summary, error) {
	return nil, errMockNotImplemented
}

func (m *mockClient) ContainerInspect(_ context.Context, _ string) (container.InspectResponse, error) {
	return container.InspectResponse{}, errMockNotImplemented
}

func (m *mockClient) ContainerExecCreate(_ context.Context, _ string, _ container.ExecOptions) (container.ExecCreateResponse, error) {
	return container.ExecCreateResponse{}, errMockNotImplemented
}

func (m *mockClient) ContainerExecAttach(_ context.Context, _ string, _ container.ExecAttachOptions) (types.HijackedResponse, error) {
	return types.HijackedResponse{}, errMockNotImplemented
}

func (m *mockClient) ContainerExecInspect(_ context.Context, _ string) (container.ExecInspect, error) {
	return container.ExecInspect{}, errMockNotImplemented
}

func (m *mockClient) Ping(_ context.Context) (types.Ping, error) {
	return types.Ping{}, errMockNotImplemented
}

func (m *mockClient) Close() error {
	return nil
}

func TestEnsureSetup_CreatesDirectories(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	mock := &mockClient{} // image exists (no error)
	mgr := NewManager(mock, slog.Default(), io.Discard)

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

	mock := &mockClient{}
	var output bytes.Buffer
	mgr := NewManager(mock, slog.Default(), &output)

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

	// Pre-create config.yaml with custom content
	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))
	customContent := []byte("# custom config\n")
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "config.yaml"), customContent, 0600))

	mock := &mockClient{}
	var output bytes.Buffer
	mgr := NewManager(mock, slog.Default(), &output)

	err := mgr.EnsureSetup(context.Background())
	require.NoError(t, err)

	// Config should be preserved
	content, err := os.ReadFile(filepath.Join(yoloaiDir, "config.yaml")) //nolint:gosec // G304: test code with temp dir
	require.NoError(t, err)
	assert.Equal(t, customContent, content)

	// Completion hint should NOT be printed
	assert.NotContains(t, output.String(), "completion")
}

func TestEnsureSetup_SkipsBuildWhenImageExists(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Pre-seed resources so SeedResources reports no changes
	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	_, err := docker.SeedResources(yoloaiDir)
	require.NoError(t, err)

	mock := &mockClient{} // imageExistsErr is nil → image exists
	mgr := NewManager(mock, slog.Default(), io.Discard)

	err = mgr.EnsureSetup(context.Background())
	require.NoError(t, err)
	assert.False(t, mock.buildCalled, "ImageBuild should not be called when image exists and resources unchanged")
}

func TestEnsureSetup_RebuildWhenResourcesChanged(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// First seed to establish checksum manifest
	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	_, err := docker.SeedResources(yoloaiDir)
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

	mock := &mockClient{} // image exists
	var output bytes.Buffer
	mgr := NewManager(mock, slog.Default(), &output)

	err = mgr.EnsureSetup(context.Background())
	require.NoError(t, err)
	assert.True(t, mock.buildCalled, "ImageBuild should be called when resources changed")
	assert.Contains(t, output.String(), "resources updated")
}

func TestEnsureSetup_BuildsWhenImageMissing(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	mock := &mockClient{
		imageExistsErr: fmt.Errorf("not found: %w", cerrdefs.ErrNotFound),
	}
	var output bytes.Buffer
	mgr := NewManager(mock, slog.Default(), &output)

	err := mgr.EnsureSetup(context.Background())
	require.NoError(t, err)
	assert.True(t, mock.buildCalled, "ImageBuild should be called when image is missing")
	assert.Contains(t, output.String(), "Building base image")
}

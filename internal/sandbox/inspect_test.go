package sandbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// inspectMockClient extends mockClient to support ContainerInspect for inspect tests.
type inspectMockClient struct {
	mockClient
	containerInspectFn func(ctx context.Context, id string) (container.InspectResponse, error)
}

func (m *inspectMockClient) ContainerInspect(ctx context.Context, id string) (container.InspectResponse, error) {
	if m.containerInspectFn != nil {
		return m.containerInspectFn(ctx, id)
	}
	return container.InspectResponse{}, errMockNotImplemented
}

// FormatAge tests

func TestFormatAge_Seconds(t *testing.T) {
	created := time.Now().Add(-30 * time.Second)
	assert.Equal(t, "30s", FormatAge(created))
}

func TestFormatAge_Minutes(t *testing.T) {
	created := time.Now().Add(-5 * time.Minute)
	assert.Equal(t, "5m", FormatAge(created))
}

func TestFormatAge_Hours(t *testing.T) {
	created := time.Now().Add(-2 * time.Hour)
	assert.Equal(t, "2h", FormatAge(created))
}

func TestFormatAge_Days(t *testing.T) {
	created := time.Now().Add(-3 * 24 * time.Hour)
	assert.Equal(t, "3d", FormatAge(created))
}

// detectChanges tests

func TestDetectChanges_NoWorkDir(t *testing.T) {
	assert.Equal(t, "-", detectChanges("/nonexistent/path"))
}

func TestDetectChanges_NotGitRepo(t *testing.T) {
	dir := t.TempDir()
	assert.Equal(t, "-", detectChanges(dir))
}

func TestDetectChanges_CleanRepo(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	assert.Equal(t, "no", detectChanges(dir))
}

func TestDetectChanges_DirtyRepo(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	writeTestFile(t, dir, "file.txt", "modified")
	assert.Equal(t, "yes", detectChanges(dir))
}

func TestDetectChanges_UntrackedFiles(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	writeTestFile(t, dir, "new.txt", "untracked")
	assert.Equal(t, "yes", detectChanges(dir))
}

// InspectSandbox tests

func TestInspectSandbox_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	mock := &inspectMockClient{}
	_, err := InspectSandbox(context.Background(), mock, "nonexistent")
	assert.ErrorIs(t, err, ErrSandboxNotFound)
}

func TestInspectSandbox_Removed(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Create sandbox dir with meta.json
	name := "test-removed"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))
	meta := &Meta{
		Name:  name,
		Agent: "claude",
		Workdir: WorkdirMeta{
			HostPath: "/tmp/test",
			Mode:     "copy",
		},
		CreatedAt: time.Now(),
	}
	require.NoError(t, SaveMeta(sandboxDir, meta))

	mock := &inspectMockClient{
		containerInspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{}, fmt.Errorf("not found: %w", cerrdefs.ErrNotFound)
		},
	}

	info, err := InspectSandbox(context.Background(), mock, name)
	require.NoError(t, err)
	assert.Equal(t, StatusRemoved, info.Status)
	assert.Empty(t, info.ContainerID)
}

// ListSandboxes tests

func TestListSandboxes_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Create sandboxes dir but leave it empty
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".yoloai", "sandboxes"), 0750))

	mock := &inspectMockClient{}
	result, err := ListSandboxes(context.Background(), mock)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestListSandboxes_SkipsBroken(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	sandboxesDir := filepath.Join(tmpDir, ".yoloai", "sandboxes")
	require.NoError(t, os.MkdirAll(sandboxesDir, 0750))

	// Create a valid sandbox
	validDir := filepath.Join(sandboxesDir, "valid")
	require.NoError(t, os.MkdirAll(validDir, 0750))
	meta := &Meta{
		Name:  "valid",
		Agent: "claude",
		Workdir: WorkdirMeta{
			HostPath: "/tmp/test",
			Mode:     "copy",
		},
		CreatedAt: time.Now(),
	}
	require.NoError(t, SaveMeta(validDir, meta))

	// Create a broken sandbox (dir exists but no meta.json)
	brokenDir := filepath.Join(sandboxesDir, "broken")
	require.NoError(t, os.MkdirAll(brokenDir, 0750))

	mock := &inspectMockClient{
		containerInspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{}, fmt.Errorf("not found: %w", cerrdefs.ErrNotFound)
		},
	}

	result, err := ListSandboxes(context.Background(), mock)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "valid", result[0].Meta.Name)
}

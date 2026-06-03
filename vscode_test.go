// ABOUTME: Tests for Sandbox.VscodeAttach — supported/unsupported backend
// ABOUTME: resolution and the missing-sandbox error.
package yoloai

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/sandbox/store"
)

// vscodeClient builds a backend-less Client; when meta is non-nil it also
// materializes the sandbox dir + environment.json so c.Sandbox resolves it.
func vscodeClient(t *testing.T, meta *store.Environment) *Client {
	t.Helper()
	dir := t.TempDir()
	c, err := NewWithOptions(context.Background(), Options{DataDir: dir, HomeDir: dir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	if meta != nil {
		sbDir := c.layout.SandboxDir(meta.Name)
		require.NoError(t, os.MkdirAll(sbDir, 0750))
		require.NoError(t, store.SaveEnvironment(sbDir, meta))
	}
	return c
}

func TestVscodeAttach_Supported(t *testing.T) {
	c := vscodeClient(t, &store.Environment{
		Name:    "box",
		Agent:   "test",
		Backend: BackendDocker,
		Workdir: store.WorkdirEnvironment{HostPath: "/proj", MountPath: "/proj", Mode: store.DirModeCopy},
	})
	sb, err := c.Sandbox("box")
	require.NoError(t, err)

	attach, err := sb.VscodeAttach()
	require.NoError(t, err)
	assert.True(t, attach.Supported)
	assert.Equal(t, store.InstanceName("", "box"), attach.ContainerName)
	assert.Equal(t, "/proj", attach.WorkdirPath)
	assert.True(t, strings.HasPrefix(attach.FolderURI, "vscode-remote://attached-container+"))
	assert.True(t, strings.HasSuffix(attach.FolderURI, "/proj"))
}

func TestVscodeAttach_Unsupported(t *testing.T) {
	c := vscodeClient(t, &store.Environment{
		Name:    "box",
		Agent:   "test",
		Backend: BackendSeatbelt,
		Workdir: store.WorkdirEnvironment{HostPath: "/proj", MountPath: "/proj", Mode: store.DirModeCopy},
	})
	sb, err := c.Sandbox("box")
	require.NoError(t, err)

	attach, err := sb.VscodeAttach()
	require.NoError(t, err)
	assert.False(t, attach.Supported)
	assert.Empty(t, attach.FolderURI)
	assert.Equal(t, BackendSeatbelt, attach.Backend)
}

func TestVscodeAttach_NotFound(t *testing.T) {
	c := vscodeClient(t, nil)
	_, err := c.Sandbox("ghost")
	require.ErrorIs(t, err, ErrSandboxNotFound)
}

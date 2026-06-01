// ABOUTME: Tests for SystemClient.VscodeAttach — supported/unsupported backend
// ABOUTME: resolution and the missing-sandbox error.
package yoloai

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
)

func vscodeSystemClient(t *testing.T, meta *store.Environment) *SystemClient {
	t.Helper()
	tmpDir := t.TempDir()
	layout := config.NewLayout(filepath.Join(tmpDir, ".yoloai"))
	if meta != nil {
		require.NoError(t, os.MkdirAll(layout.SandboxDir(meta.Name), 0750))
		require.NoError(t, store.SaveEnvironment(layout.SandboxDir(meta.Name), meta))
	}
	return &SystemClient{layout: layout}
}

func TestVscodeAttach_Supported(t *testing.T) {
	sc := vscodeSystemClient(t, &store.Environment{
		Name:    "box",
		Agent:   "test",
		Backend: BackendDocker,
		Workdir: store.WorkdirEnvironment{HostPath: "/proj", MountPath: "/proj", Mode: store.DirModeCopy},
	})

	attach, err := sc.VscodeAttach("box")
	require.NoError(t, err)
	assert.True(t, attach.Supported)
	assert.Equal(t, store.InstanceName("box"), attach.ContainerName)
	assert.Equal(t, "/proj", attach.WorkdirPath)
	assert.True(t, strings.HasPrefix(attach.FolderURI, "vscode-remote://attached-container+"))
	assert.True(t, strings.HasSuffix(attach.FolderURI, "/proj"))
}

func TestVscodeAttach_Unsupported(t *testing.T) {
	sc := vscodeSystemClient(t, &store.Environment{
		Name:    "box",
		Agent:   "test",
		Backend: BackendSeatbelt,
		Workdir: store.WorkdirEnvironment{HostPath: "/proj", MountPath: "/proj", Mode: store.DirModeCopy},
	})

	attach, err := sc.VscodeAttach("box")
	require.NoError(t, err)
	assert.False(t, attach.Supported)
	assert.Empty(t, attach.FolderURI)
	assert.Equal(t, BackendSeatbelt, attach.Backend)
}

func TestVscodeAttach_NotFound(t *testing.T) {
	sc := vscodeSystemClient(t, nil)
	_, err := sc.VscodeAttach("ghost")
	require.ErrorIs(t, err, sandbox.ErrSandboxNotFound)
}

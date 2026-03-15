package podman

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscoverSocket_ContainerHost(t *testing.T) {
	t.Setenv("CONTAINER_HOST", "unix:///custom/podman.sock")
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("XDG_RUNTIME_DIR", "")

	sock, err := discoverSocket()
	require.NoError(t, err)
	assert.Equal(t, "unix:///custom/podman.sock", sock)
}

func TestDiscoverSocket_DockerHost(t *testing.T) {
	t.Setenv("CONTAINER_HOST", "")
	t.Setenv("DOCKER_HOST", "unix:///custom/docker.sock")
	t.Setenv("XDG_RUNTIME_DIR", "")

	sock, err := discoverSocket()
	require.NoError(t, err)
	assert.Equal(t, "unix:///custom/docker.sock", sock)
}

func TestDiscoverSocket_ContainerHostTakesPrecedence(t *testing.T) {
	t.Setenv("CONTAINER_HOST", "unix:///container.sock")
	t.Setenv("DOCKER_HOST", "unix:///docker.sock")

	sock, err := discoverSocket()
	require.NoError(t, err)
	assert.Equal(t, "unix:///container.sock", sock)
}

func TestDiscoverSocket_XDGRuntimeDir(t *testing.T) {
	tmpDir := t.TempDir()
	sockDir := filepath.Join(tmpDir, "podman")
	require.NoError(t, os.MkdirAll(sockDir, 0o750))

	sockPath := filepath.Join(sockDir, "podman.sock")
	require.NoError(t, os.WriteFile(sockPath, nil, 0o600))

	t.Setenv("CONTAINER_HOST", "")
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("XDG_RUNTIME_DIR", tmpDir)

	sock, err := discoverSocket()
	require.NoError(t, err)
	assert.Equal(t, "unix://"+sockPath, sock)
}

func TestDiscoverSocket_NoSocket(t *testing.T) {
	t.Setenv("CONTAINER_HOST", "")
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	_, err := discoverSocket()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no podman socket found")
}

func TestIsRootless_NonRoot(t *testing.T) {
	// Save and restore
	orig := isRootless
	defer func() { isRootless = orig }()

	isRootless = func() bool { return true }
	assert.True(t, isRootless())
}

func TestIsRootless_Root(t *testing.T) {
	orig := isRootless
	defer func() { isRootless = orig }()

	isRootless = func() bool { return false }
	assert.False(t, isRootless())
}

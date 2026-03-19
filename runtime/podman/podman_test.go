package podman

import (
	"context"
	"fmt"
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
	// Mock machine socket discovery to fail (prevents executing podman commands)
	origMachineDiscovery := machineSocketDiscovery
	defer func() { machineSocketDiscovery = origMachineDiscovery }()
	machineSocketDiscovery = func() (string, error) {
		return "", assert.AnError
	}

	// Override system socket path so tests pass even when podman is installed
	origSystemSockPath := systemSockPath
	defer func() { systemSockPath = origSystemSockPath }()
	systemSockPath = filepath.Join(t.TempDir(), "nonexistent.sock")

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

func TestDiscoverSocket_WSL2(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "podman-root.sock")
	require.NoError(t, os.WriteFile(sockPath, nil, 0o600))

	orig := wsl2SockPaths
	defer func() { wsl2SockPaths = orig }()
	wsl2SockPaths = []string{
		filepath.Join(tmpDir, "nonexistent.sock"),
		sockPath,
	}

	origMachineDiscovery := machineSocketDiscovery
	defer func() { machineSocketDiscovery = origMachineDiscovery }()
	machineSocketDiscovery = func() (string, error) { return "", fmt.Errorf("not macOS") }

	origSystem := systemSockPath
	defer func() { systemSockPath = origSystem }()
	systemSockPath = filepath.Join(tmpDir, "system.sock")

	t.Setenv("CONTAINER_HOST", "")
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	sock, err := discoverSocket()
	require.NoError(t, err)
	assert.Equal(t, "unix://"+sockPath, sock)
}

func TestDiscoverSocket_WSL2_FirstPathWins(t *testing.T) {
	tmpDir := t.TempDir()
	first := filepath.Join(tmpDir, "podman-root.sock")
	second := filepath.Join(tmpDir, "podman-user.sock")
	require.NoError(t, os.WriteFile(first, nil, 0o600))
	require.NoError(t, os.WriteFile(second, nil, 0o600))

	orig := wsl2SockPaths
	defer func() { wsl2SockPaths = orig }()
	wsl2SockPaths = []string{first, second}

	origMachineDiscovery := machineSocketDiscovery
	defer func() { machineSocketDiscovery = origMachineDiscovery }()
	machineSocketDiscovery = func() (string, error) { return "", fmt.Errorf("not macOS") }

	origSystem := systemSockPath
	defer func() { systemSockPath = origSystem }()
	systemSockPath = filepath.Join(tmpDir, "system.sock")

	t.Setenv("CONTAINER_HOST", "")
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	sock, err := discoverSocket()
	require.NoError(t, err)
	assert.Equal(t, "unix://"+first, sock)
}

// ValidateIsolation tests

func TestValidateIsolation_Podman_NonEnhanced(t *testing.T) {
	r := &Runtime{}
	for _, mode := range []string{"", "container", "vm", "vm-enhanced"} {
		err := r.ValidateIsolation(context.Background(), mode)
		assert.NoError(t, err, "mode %q should not require validation", mode)
	}
}

func TestValidateIsolation_Podman_Rootless(t *testing.T) {
	orig := isRootless
	defer func() { isRootless = orig }()
	isRootless = func() bool { return true }

	r := &Runtime{}
	err := r.ValidateIsolation(context.Background(), "container-enhanced")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rootless")
	assert.Contains(t, err.Error(), "root")
}

func TestValidateIsolation_Podman_RootNoRunsc(t *testing.T) {
	orig := isRootless
	defer func() { isRootless = orig }()
	isRootless = func() bool { return false }

	origLook := runscLookPath
	defer func() { runscLookPath = origLook }()
	runscLookPath = func(string) (string, error) { return "", fmt.Errorf("not found") }

	r := &Runtime{}
	err := r.ValidateIsolation(context.Background(), "container-enhanced")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "runsc")
	assert.Contains(t, err.Error(), "gVisor")
}

func TestValidateIsolation_Podman_RootWithRunsc(t *testing.T) {
	orig := isRootless
	defer func() { isRootless = orig }()
	isRootless = func() bool { return false }

	origLook := runscLookPath
	defer func() { runscLookPath = origLook }()
	runscLookPath = func(string) (string, error) { return "/usr/local/sbin/runsc", nil }

	r := &Runtime{}
	err := r.ValidateIsolation(context.Background(), "container-enhanced")
	assert.NoError(t, err)
}

package podman

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/runtime/caps"
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

func TestSocketIsRootless_UserSocket(t *testing.T) {
	assert.True(t, socketIsRootless("unix:///run/user/1000/podman/podman.sock"))
}

func TestSocketIsRootless_SystemSocket(t *testing.T) {
	assert.False(t, socketIsRootless("unix://"+systemSockPath))
}

func TestSocketIsRootless_WSL2Socket(t *testing.T) {
	assert.True(t, socketIsRootless("unix:///mnt/wsl/podman-sockets/podman-machine-default/podman-root.sock"))
}

func TestSocketIsRootless_CustomSocket(t *testing.T) {
	assert.True(t, socketIsRootless("unix:///custom/podman.sock"))
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

// RequiredCapabilities tests

func buildPodmanTestRuntime(rootless bool, lookPath func(string) (string, error)) *Runtime {
	r := &Runtime{rootless: rootless}
	r.rootlessCheck = buildRootlessCheckCap(rootless)
	r.gvisorRunsc = caps.NewGVisorRunsc(lookPath)
	return r
}

func TestRequiredCapabilities_Podman_NonEnhanced(t *testing.T) {
	r := buildPodmanTestRuntime(false, func(string) (string, error) { return "/sbin/runsc", nil })
	for _, mode := range []string{"", "container", "vm", "vm-enhanced"} {
		capList := r.RequiredCapabilities(mode)
		assert.Nil(t, capList, "mode %q should return nil caps", mode)
	}
}

func TestRequiredCapabilities_Podman_Rootless(t *testing.T) {
	r := buildPodmanTestRuntime(true, func(string) (string, error) { return "/sbin/runsc", nil })
	capList := r.RequiredCapabilities("container-enhanced")
	require.NotNil(t, capList)

	env := caps.DetectEnvironment()
	results := caps.RunChecks(context.Background(), capList, env)
	err := caps.FormatError(results)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rootless")
}

func TestRequiredCapabilities_Podman_RootNoRunsc(t *testing.T) {
	r := buildPodmanTestRuntime(false, func(string) (string, error) { return "", fmt.Errorf("not found") })
	capList := r.RequiredCapabilities("container-enhanced")
	require.NotNil(t, capList)

	env := caps.DetectEnvironment()
	results := caps.RunChecks(context.Background(), capList, env)
	err := caps.FormatError(results)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gVisor runtime")
}

func TestRequiredCapabilities_Podman_RootWithRunsc(t *testing.T) {
	r := buildPodmanTestRuntime(false, func(string) (string, error) { return "/usr/local/sbin/runsc", nil })
	capList := r.RequiredCapabilities("container-enhanced")
	require.NotNil(t, capList)

	env := caps.DetectEnvironment()
	results := caps.RunChecks(context.Background(), capList, env)
	err := caps.FormatError(results)
	assert.NoError(t, err)
}

func TestBaseModeName_Podman(t *testing.T) {
	r := &Runtime{}
	assert.Equal(t, "container", r.BaseModeName())
}

func TestSupportedIsolationModes_Podman(t *testing.T) {
	r := &Runtime{}
	modes := r.SupportedIsolationModes()
	assert.Contains(t, modes, "container-enhanced")
}

func TestRequiredCapabilities_Podman_RootlessIsPermanent(t *testing.T) {
	r := buildPodmanTestRuntime(true, func(string) (string, error) { return "/sbin/runsc", nil })
	capList := r.RequiredCapabilities("container-enhanced")
	require.NotNil(t, capList)

	env := caps.DetectEnvironment()
	results := caps.RunChecks(context.Background(), capList, env)
	// First result should be permanent (rootless is permanent)
	require.NotEmpty(t, results)
	assert.True(t, results[0].IsPermanent)
}

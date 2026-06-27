package podman

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/image"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/runtime/caps"
)

const gib = int64(1024 * 1024 * 1024)

// Podman's docker-compat /system/df reports LayersSize=0, so the inherited
// docker image-byte computation would read 0. podmanImageBytes must instead
// dedup from per-image Size/SharedSize. Summing Size would multiply-count the
// shared base (here 3 stages sharing a 5 GiB base → 15 GiB); the correct
// dedup is Σ(unique) + shared-once.
func TestPodmanImageBytes_DedupsSharedBase(t *testing.T) {
	du := types.DiskUsage{
		LayersSize: 0, // the Podman bug this function works around
		Images: []*image.Summary{
			{Size: 5 * gib, SharedSize: 5 * gib},     // base, all shared
			{Size: 5*gib + 100, SharedSize: 5 * gib}, // +100 unique
			{Size: 5*gib + 200, SharedSize: 5 * gib}, // +200 unique
		},
	}
	// unique = 0 + 100 + 200 = 300; maxShared = 5 GiB.
	assert.Equal(t, 5*gib+300, podmanImageBytes(du))
}

func TestPodmanImageBytes_Empty(t *testing.T) {
	assert.Equal(t, int64(0), podmanImageBytes(types.DiskUsage{}))
}

func TestPodmanImageBytes_SkipsNilEntries(t *testing.T) {
	du := types.DiskUsage{Images: []*image.Summary{
		nil,
		{Size: gib, SharedSize: 0}, // fully unique, unshared
	}}
	assert.Equal(t, gib, podmanImageBytes(du))
}

func TestDiscoverSocket_ContainerHost(t *testing.T) {
	sock, err := discoverSocket(map[string]string{"CONTAINER_HOST": "unix:///custom/podman.sock"})
	require.NoError(t, err)
	assert.Equal(t, "unix:///custom/podman.sock", sock)
}

func TestDiscoverSocket_DockerHost(t *testing.T) {
	sock, err := discoverSocket(map[string]string{"DOCKER_HOST": "unix:///custom/docker.sock"})
	require.NoError(t, err)
	assert.Equal(t, "unix:///custom/docker.sock", sock)
}

func TestDiscoverSocket_ContainerHostTakesPrecedence(t *testing.T) {
	sock, err := discoverSocket(map[string]string{
		"CONTAINER_HOST": "unix:///container.sock",
		"DOCKER_HOST":    "unix:///docker.sock",
	})
	require.NoError(t, err)
	assert.Equal(t, "unix:///container.sock", sock)
}

func TestDiscoverSocket_XDGRuntimeDir(t *testing.T) {
	tmpDir := t.TempDir()
	sockDir := filepath.Join(tmpDir, "podman")
	require.NoError(t, os.MkdirAll(sockDir, 0o750))

	sockPath := filepath.Join(sockDir, "podman.sock")
	require.NoError(t, os.WriteFile(sockPath, nil, 0o600))

	sock, err := discoverSocket(map[string]string{"XDG_RUNTIME_DIR": tmpDir})
	require.NoError(t, err)
	assert.Equal(t, "unix://"+sockPath, sock)
}

func TestDiscoverSocket_NoSocket(t *testing.T) {
	// Mock machine socket discovery to fail (prevents executing podman commands)
	origMachineDiscovery := machineSocketDiscovery
	defer func() { machineSocketDiscovery = origMachineDiscovery }()
	machineSocketDiscovery = func(_ map[string]string) (string, error) {
		return "", assert.AnError
	}

	// Override system socket path so tests pass even when podman is installed
	origSystemSockPath := systemSockPath
	defer func() { systemSockPath = origSystemSockPath }()
	systemSockPath = filepath.Join(t.TempDir(), "nonexistent.sock")

	_, err := discoverSocket(map[string]string{"XDG_RUNTIME_DIR": t.TempDir()})
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
	machineSocketDiscovery = func(_ map[string]string) (string, error) { return "", fmt.Errorf("not macOS") }

	origSystem := systemSockPath
	defer func() { systemSockPath = origSystem }()
	systemSockPath = filepath.Join(tmpDir, "system.sock")

	sock, err := discoverSocket(map[string]string{"XDG_RUNTIME_DIR": t.TempDir()})
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
	machineSocketDiscovery = func(_ map[string]string) (string, error) { return "", fmt.Errorf("not macOS") }

	origSystem := systemSockPath
	defer func() { systemSockPath = origSystem }()
	systemSockPath = filepath.Join(tmpDir, "system.sock")

	sock, err := discoverSocket(map[string]string{"XDG_RUNTIME_DIR": t.TempDir()})
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
	for _, mode := range []runtime.IsolationMode{"", "container", "vm", "vm-enhanced"} {
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

func TestDescriptor_Podman(t *testing.T) {
	r := &Runtime{}
	d := r.Descriptor()
	assert.Equal(t, runtime.BackendType("podman"), d.Type)
	assert.Equal(t, runtime.IsolationModeContainer, d.BaseModeName)
	assert.Contains(t, d.SupportedIsolationModes, runtime.IsolationModeContainerEnhanced)
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

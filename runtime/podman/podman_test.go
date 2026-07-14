package podman

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
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

// mockNoNativeSockets points every native podman-socket probe (system socket,
// WSL2 paths, macOS machine) at a nonexistent/failing source so discoverSocket
// falls through to its env-var fallbacks. Returns a restore func. Callers that
// also want the XDG rootless probe to miss should pass an XDG_RUNTIME_DIR with no
// podman.sock under it.
func mockNoNativeSockets(t *testing.T) func() {
	t.Helper()
	origMachine := machineSocketDiscovery
	origSystem := systemSockPath
	origWSL := wsl2SockPaths
	machineSocketDiscovery = func(_ map[string]string) (string, error) { return "", assert.AnError }
	systemSockPath = filepath.Join(t.TempDir(), "nonexistent.sock")
	wsl2SockPaths = nil
	return func() {
		machineSocketDiscovery = origMachine
		systemSockPath = origSystem
		wsl2SockPaths = origWSL
	}
}

func TestDiscoverSocket_ContainerHost(t *testing.T) {
	sock, err := discoverSocket(map[string]string{"CONTAINER_HOST": "unix:///custom/podman.sock"})
	require.NoError(t, err)
	assert.Equal(t, "unix:///custom/podman.sock", sock)
}

func TestDiscoverSocket_DockerHost_FallbackWhenNoNativeSocket(t *testing.T) {
	// $DOCKER_HOST is honored only as a last resort, when no native podman socket
	// exists. Mock all native paths away so the fallback is reached.
	defer mockNoNativeSockets(t)()
	sock, err := discoverSocket(map[string]string{
		"DOCKER_HOST":     "unix:///custom/docker.sock",
		"XDG_RUNTIME_DIR": t.TempDir(),
	})
	require.NoError(t, err)
	assert.Equal(t, "unix:///custom/docker.sock", sock)
}

// TestDiscoverSocket_NativeSocketBeatsDockerHost is the regression guard for the
// mixed-host footgun: with a real podman socket present AND $DOCKER_HOST pointing
// at the docker daemon, the native podman socket must win — otherwise the podman
// backend silently talks to docker (where slirp4netns etc. don't exist).
func TestDiscoverSocket_NativeSocketBeatsDockerHost(t *testing.T) {
	tmpDir := t.TempDir()
	sockDir := filepath.Join(tmpDir, "podman")
	require.NoError(t, os.MkdirAll(sockDir, 0o750))
	sockPath := filepath.Join(sockDir, "podman.sock")
	require.NoError(t, os.WriteFile(sockPath, nil, 0o600))

	sock, err := discoverSocket(map[string]string{
		"XDG_RUNTIME_DIR": tmpDir,
		"DOCKER_HOST":     "unix:///var/run/docker.sock",
	})
	require.NoError(t, err)
	assert.Equal(t, "unix://"+sockPath, sock, "native podman socket must win over DOCKER_HOST")
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
	for _, mode := range []runtime.IsolationMode{"vm", "vm-enhanced"} {
		capList := r.RequiredCapabilities(mode)
		assert.Nil(t, capList, "mode %q should return nil caps", mode)
	}
}

// TestRequiredCapabilities_Podman_BaseMode_CrunFloor locks the base ("" and
// "container") modes to the crun version-floor check. It is Advisory: a
// failure never blocks, just surfaces via doctor and the launch-time warning.
func TestRequiredCapabilities_Podman_BaseMode_CrunFloor(t *testing.T) {
	r := buildPodmanTestRuntime(false, func(string) (string, error) { return "/sbin/runsc", nil })
	r.crunVersionFloor = buildCrunVersionFloorCap()
	for _, mode := range []runtime.IsolationMode{runtime.IsolationModeDefault, runtime.IsolationModeContainer, runtime.IsolationModeContainerPrivileged} {
		capList := r.RequiredCapabilities(mode)
		require.Len(t, capList, 1, "mode %q", mode)
		assert.Equal(t, "crun-version-floor", capList[0].ID)
		assert.True(t, capList[0].Advisory)
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

// TestInjectorReach_Darwin covers the macOS branch: podman runs in a podman-machine
// VM whose host hops (slirp 10.0.2.2 / gvproxy) don't carry the real agent's traffic
// reliably, so podman reports unsupported on macOS and brokering degrades to direct
// delivery. Only asserts on darwin (where the branch is taken).
func TestInjectorReach_Darwin(t *testing.T) {
	if goruntime.GOOS != "darwin" {
		t.Skip("macOS podman reach only applies on darwin")
	}
	_, err := (&Runtime{rootless: true}).InjectorReach(context.Background())
	assert.ErrorIs(t, err, runtime.ErrInjectorUnsupported, "podman doesn't broker on macOS; direct delivery")
}

func TestInjectorReach_Rootless(t *testing.T) {
	if goruntime.GOOS == "darwin" {
		t.Skip("on darwin the podman-machine branch overrides the Linux rootless slirp path")
	}
	r := &Runtime{rootless: true}
	reach, err := r.InjectorReach(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1", reach.BindHost, "injector binds host loopback")
	assert.Equal(t, "10.0.2.2", reach.DialHost, "agent dials the slirp host alias")
	assert.Equal(t, "slirp4netns:allow_host_loopback=true", reach.RequiredNetworkMode)
}

func TestInjectorReach_RootfulUnsupported(t *testing.T) {
	if goruntime.GOOS == "darwin" {
		t.Skip("on darwin podman brokers via host.containers.internal regardless of rootless")
	}
	r := &Runtime{rootless: false}
	_, err := r.InjectorReach(context.Background())
	assert.ErrorIs(t, err, runtime.ErrInjectorUnsupported, "rootful podman brokering not wired yet")
}

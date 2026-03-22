package docker

import (
	"context"
	"fmt"
	"testing"

	"github.com/docker/docker/api/types/mount"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/runtime/caps"
)

func TestConvertMounts_Empty(t *testing.T) {
	result := ConvertMounts(nil)
	assert.Nil(t, result)

	result = ConvertMounts([]runtime.MountSpec{})
	assert.Nil(t, result)
}

func TestConvertMounts_SingleRO(t *testing.T) {
	specs := []runtime.MountSpec{
		{Source: "/host/path", Target: "/container/path", ReadOnly: true},
	}
	result := ConvertMounts(specs)
	require.Len(t, result, 1)
	assert.Equal(t, mount.TypeBind, result[0].Type)
	assert.Equal(t, "/host/path", result[0].Source)
	assert.Equal(t, "/container/path", result[0].Target)
	assert.True(t, result[0].ReadOnly)
}

func TestConvertMounts_MultipleWithRW(t *testing.T) {
	specs := []runtime.MountSpec{
		{Source: "/src1", Target: "/dst1", ReadOnly: true},
		{Source: "/src2", Target: "/dst2", ReadOnly: false},
		{Source: "/src3", Target: "/dst3", ReadOnly: true},
	}
	result := ConvertMounts(specs)
	require.Len(t, result, 3)
	assert.True(t, result[0].ReadOnly)
	assert.False(t, result[1].ReadOnly)
	assert.True(t, result[2].ReadOnly)
}

func TestConvertPorts_Empty(t *testing.T) {
	portMap, portSet := ConvertPorts(nil)
	assert.Nil(t, portMap)
	assert.Nil(t, portSet)

	portMap, portSet = ConvertPorts([]runtime.PortMapping{})
	assert.Nil(t, portMap)
	assert.Nil(t, portSet)
}

func TestConvertPorts_SingleMapping(t *testing.T) {
	ports := []runtime.PortMapping{
		{HostPort: "8080", InstancePort: "80", Protocol: "tcp"},
	}
	portMap, portSet := ConvertPorts(ports)
	require.Len(t, portMap, 1)
	require.Len(t, portSet, 1)

	// Check that the port binding is correct
	for port, bindings := range portMap {
		assert.Equal(t, "80", port.Port())
		assert.Equal(t, "tcp", port.Proto())
		require.Len(t, bindings, 1)
		assert.Equal(t, "8080", bindings[0].HostPort)
	}
}

func TestConvertPorts_DefaultProtocol(t *testing.T) {
	ports := []runtime.PortMapping{
		{HostPort: "3000", InstancePort: "3000"},
	}
	portMap, portSet := ConvertPorts(ports)
	require.Len(t, portMap, 1)
	require.Len(t, portSet, 1)

	for port := range portSet {
		assert.Equal(t, "tcp", port.Proto())
	}
}

func TestConvertPorts_Multiple(t *testing.T) {
	ports := []runtime.PortMapping{
		{HostPort: "8080", InstancePort: "80"},
		{HostPort: "8443", InstancePort: "443"},
	}
	portMap, portSet := ConvertPorts(ports)
	require.Len(t, portMap, 2)
	require.Len(t, portSet, 2)
}

// RequiredCapabilities tests

func TestRequiredCapabilities_Docker_NonEnhanced(t *testing.T) {
	r := &Runtime{binaryName: "docker"}
	for _, mode := range []string{"", "container", "vm", "vm-enhanced"} {
		capList := r.RequiredCapabilities(mode)
		assert.Nil(t, capList, "mode %q should return nil caps", mode)
	}
}

func buildDockerTestRuntime(binaryName string) *Runtime {
	r := &Runtime{binaryName: binaryName}
	r.gvisorRunsc = caps.NewGVisorRunsc(func(string) (string, error) {
		return "/usr/local/sbin/runsc", nil // pass by default
	})
	r.gvisorRegistered = buildGVisorRegisteredCap(binaryName)
	return r
}

func TestRequiredCapabilities_Docker_RunscPresent(t *testing.T) {
	orig := dockerInfoOutput
	defer func() { dockerInfoOutput = orig }()
	dockerInfoOutput = func(_ context.Context, _ string) ([]byte, error) {
		return []byte("runc\nrunsc\nio.containerd.runc.v2\n"), nil
	}

	r := buildDockerTestRuntime("docker")
	capList := r.RequiredCapabilities("container-enhanced")
	require.NotNil(t, capList)

	env := caps.DetectEnvironment()
	results := caps.RunChecks(context.Background(), capList, env)
	err := caps.FormatError(results)
	assert.NoError(t, err)
}

func TestRequiredCapabilities_Docker_RunscMissing(t *testing.T) {
	orig := dockerInfoOutput
	defer func() { dockerInfoOutput = orig }()
	dockerInfoOutput = func(_ context.Context, _ string) ([]byte, error) {
		return []byte("runc\nio.containerd.runc.v2\n"), nil
	}

	r := buildDockerTestRuntime("docker")
	capList := r.RequiredCapabilities("container-enhanced")
	require.NotNil(t, capList)

	env := caps.DetectEnvironment()
	results := caps.RunChecks(context.Background(), capList, env)
	err := caps.FormatError(results)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gVisor registered")
}

func TestRequiredCapabilities_Docker_InfoFails(t *testing.T) {
	orig := dockerInfoOutput
	defer func() { dockerInfoOutput = orig }()
	dockerInfoOutput = func(_ context.Context, _ string) ([]byte, error) {
		return nil, fmt.Errorf("docker daemon not responding")
	}

	r := buildDockerTestRuntime("docker")
	capList := r.RequiredCapabilities("container-enhanced")
	require.NotNil(t, capList)

	env := caps.DetectEnvironment()
	results := caps.RunChecks(context.Background(), capList, env)
	err := caps.FormatError(results)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "check runtimes")
}

func TestBaseModeName_Docker(t *testing.T) {
	r := &Runtime{}
	assert.Equal(t, "container", r.BaseModeName())
}

func TestSupportedIsolationModes_Docker(t *testing.T) {
	r := &Runtime{}
	modes := r.SupportedIsolationModes()
	assert.Contains(t, modes, "container-enhanced")
}

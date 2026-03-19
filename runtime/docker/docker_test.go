package docker

import (
	"context"
	"fmt"
	"testing"

	"github.com/docker/docker/api/types/mount"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/runtime"
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

// ValidateIsolation tests

func TestValidateIsolation_NonEnhanced(t *testing.T) {
	r := &Runtime{binaryName: "docker"}
	for _, mode := range []string{"", "container", "vm", "vm-enhanced"} {
		err := r.ValidateIsolation(context.Background(), mode)
		assert.NoError(t, err, "mode %q should not require validation", mode)
	}
}

func TestValidateIsolation_RunscPresent(t *testing.T) {
	orig := dockerInfoOutput
	defer func() { dockerInfoOutput = orig }()
	dockerInfoOutput = func(_ context.Context, _ string) ([]byte, error) {
		return []byte("runc\nrunsc\nio.containerd.runc.v2\n"), nil
	}

	r := &Runtime{binaryName: "docker"}
	err := r.ValidateIsolation(context.Background(), "container-enhanced")
	assert.NoError(t, err)
}

func TestValidateIsolation_RunscMissing(t *testing.T) {
	orig := dockerInfoOutput
	defer func() { dockerInfoOutput = orig }()
	dockerInfoOutput = func(_ context.Context, _ string) ([]byte, error) {
		return []byte("runc\nio.containerd.runc.v2\n"), nil
	}

	r := &Runtime{binaryName: "docker"}
	err := r.ValidateIsolation(context.Background(), "container-enhanced")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "runsc")
	assert.Contains(t, err.Error(), "gVisor")
}

func TestValidateIsolation_DockerInfoFails(t *testing.T) {
	orig := dockerInfoOutput
	defer func() { dockerInfoOutput = orig }()
	dockerInfoOutput = func(_ context.Context, _ string) ([]byte, error) {
		return nil, fmt.Errorf("docker daemon not responding")
	}

	r := &Runtime{binaryName: "docker"}
	err := r.ValidateIsolation(context.Background(), "container-enhanced")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "check runtimes")
}

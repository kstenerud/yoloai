// ABOUTME: Unit tests for Tart runtime — arg building, error mapping, helpers.
package tart

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kstenerud/yoloai/internal/runtime"
)

func TestSandboxName(t *testing.T) {
	assert.Equal(t, "mysandbox", sandboxName("yoloai-mysandbox"))
	assert.Equal(t, "test-box", sandboxName("yoloai-test-box"))
	assert.Equal(t, "plain", sandboxName("plain")) // no prefix — returns as-is
}

func TestExecArgs(t *testing.T) {
	args := execArgs("yoloai-test", "bash", "-c", "echo hello")
	assert.Equal(t, []string{
		"exec", "yoloai-test", "bash", "-c", "echo hello",
	}, args)
}

func TestBuildRunArgs(t *testing.T) {
	r := &Runtime{tartBin: "/usr/local/bin/tart"}
	mounts := []runtime.MountSpec{
		{Source: "/Users/karl/project", Target: "/Users/karl/project"},
	}
	args := r.buildRunArgs("yoloai-test", "/home/user/.yoloai/sandboxes/test", mounts)

	assert.Contains(t, args, "run")
	assert.Contains(t, args, "--no-graphics")
	assert.Contains(t, args, "--dir")
	assert.Contains(t, args, "yoloai:/home/user/.yoloai/sandboxes/test")
	assert.Contains(t, args, "mount0:/Users/karl/project")
	// VM name must be last argument
	assert.Equal(t, "yoloai-test", args[len(args)-1])
}

func TestBuildNetworkArgs_None(t *testing.T) {
	cfg := runtime.InstanceConfig{NetworkMode: "none"}
	args := BuildNetworkArgs(cfg)

	assert.Contains(t, args, "--net-softnet")
	assert.Contains(t, args, "--net-softnet-block=0.0.0.0/0")
	assert.Contains(t, args, "--net-softnet-block=::/0")
}

func TestBuildNetworkArgs_Default(t *testing.T) {
	cfg := runtime.InstanceConfig{}
	args := BuildNetworkArgs(cfg)
	assert.Empty(t, args)
}

func TestBuildNetworkArgs_PortForwarding(t *testing.T) {
	cfg := runtime.InstanceConfig{
		Ports: []runtime.PortMapping{
			{HostPort: "8080", InstancePort: "80"},
			{HostPort: "8443", InstancePort: "443", Protocol: "tcp"},
		},
	}
	args := BuildNetworkArgs(cfg)

	assert.Contains(t, args, "--net-softnet")
	assert.Contains(t, args, "--net-softnet-expose=8080:80,8443:443")
}

func TestBuildNetworkArgs_IsolatedWithPorts(t *testing.T) {
	cfg := runtime.InstanceConfig{
		NetworkMode: "none",
		Ports: []runtime.PortMapping{
			{HostPort: "3000", InstancePort: "3000"},
		},
	}
	args := BuildNetworkArgs(cfg)

	assert.Contains(t, args, "--net-softnet")
	assert.Contains(t, args, "--net-softnet-block=0.0.0.0/0")
	assert.Contains(t, args, "--net-softnet-block=::/0")
	assert.Contains(t, args, "--net-softnet-allow=3000/tcp")
	assert.Contains(t, args, "--net-softnet-expose=3000:3000")
}

func TestBuildMountSymlinkCmds(t *testing.T) {
	mounts := []runtime.MountSpec{
		{Source: "/Users/karl/project", Target: "/Users/karl/project"},
		{Source: "/Users/karl/.yoloai/sandboxes/test/agent-state", Target: "/home/admin/.claude/"},
	}
	dirNames := map[string]string{
		"/Users/karl/project":                                  "workdir",
		"/Users/karl/.yoloai/sandboxes/test/agent-state": "agent-state",
	}

	cmds := BuildMountSymlinkCmds(mounts, dirNames)

	// Should create symlinks for paths that differ from VirtioFS mount points
	assert.NotEmpty(t, cmds)
	// Check that mkdir and ln commands are generated
	foundMkdir := false
	foundLn := false
	for _, cmd := range cmds {
		if contains(cmd, "mkdir") {
			foundMkdir = true
		}
		if contains(cmd, "ln -sf") {
			foundLn = true
		}
	}
	assert.True(t, foundMkdir, "should generate mkdir commands")
	assert.True(t, foundLn, "should generate symlink commands")
}

func TestBuildMountSymlinkCmds_NoSymlinkNeeded(t *testing.T) {
	mounts := []runtime.MountSpec{
		{Source: "/Users/karl/project", Target: "/Volumes/My Shared Files/workdir"},
	}
	dirNames := map[string]string{
		"/Users/karl/project": "workdir",
	}

	cmds := BuildMountSymlinkCmds(mounts, dirNames)
	assert.Empty(t, cmds)
}

func TestMapTartError_NotFound(t *testing.T) {
	tests := []struct {
		stderr string
	}{
		{"VM 'yoloai-test' does not exist"},
		{"error: not found"},
		{"no such VM"},
	}

	for _, tt := range tests {
		err := mapTartError(assert.AnError, tt.stderr)
		assert.ErrorIs(t, err, runtime.ErrNotFound, "stderr: %s", tt.stderr)
	}
}

func TestMapTartError_NotRunning(t *testing.T) {
	tests := []struct {
		stderr string
	}{
		{"VM is not running"},
		{"error: VM is stopped"},
	}

	for _, tt := range tests {
		err := mapTartError(assert.AnError, tt.stderr)
		assert.ErrorIs(t, err, runtime.ErrNotRunning, "stderr: %s", tt.stderr)
	}
}

func TestMapTartError_Unknown(t *testing.T) {
	err := mapTartError(assert.AnError, "some other error")
	assert.NotErrorIs(t, err, runtime.ErrNotFound)
	assert.NotErrorIs(t, err, runtime.ErrNotRunning)
	assert.Contains(t, err.Error(), "some other error")
}

func TestMapTartError_EmptyStderr(t *testing.T) {
	err := mapTartError(assert.AnError, "")
	assert.Equal(t, assert.AnError, err)
}

func TestPortForwardArgs(t *testing.T) {
	ports := []runtime.PortMapping{
		{HostPort: "8080", InstancePort: "80"},
	}
	args := portForwardArgs(ports)
	assert.Equal(t, []string{"--net-softnet-expose=8080:80"}, args)
}

func TestPortForwardArgs_Empty(t *testing.T) {
	args := portForwardArgs(nil)
	assert.Nil(t, args)
}

func TestPortForwardArgs_MultipleWithProtocol(t *testing.T) {
	ports := []runtime.PortMapping{
		{HostPort: "8080", InstancePort: "80", Protocol: "tcp"},
		{HostPort: "5353", InstancePort: "53", Protocol: "udp"},
	}
	args := portForwardArgs(ports)
	assert.Equal(t, []string{"--net-softnet-expose=8080:80,5353:53"}, args)
}

func TestResolveBaseImage_Default(t *testing.T) {
	r := &Runtime{}
	assert.Equal(t, defaultBaseImage, r.resolveBaseImage(""))
}

func TestResolveBaseImage_Override(t *testing.T) {
	r := &Runtime{baseImageOverride: "my-custom-vm"}
	assert.Equal(t, "my-custom-vm", r.resolveBaseImage(""))
}

func TestPlatformDetection(t *testing.T) {
	// Save and restore
	origGOOS := goos
	origGOARCH := goarch
	defer func() {
		goos = origGOOS
		goarch = origGOARCH
	}()

	goos = func() string { return "darwin" }
	goarch = func() string { return "arm64" }
	assert.True(t, isMacOS())
	assert.True(t, isAppleSilicon())

	goos = func() string { return "linux" }
	assert.False(t, isMacOS())
	assert.False(t, isAppleSilicon())

	goos = func() string { return "darwin" }
	goarch = func() string { return "amd64" }
	assert.True(t, isMacOS())
	assert.False(t, isAppleSilicon())
}

func TestIsFatalExecError(t *testing.T) {
	fatal := []string{
		"Unknown option '--user'",
		"executable file not found in $PATH",
		"VM 'yoloai-test' does not exist",
		"no such file or directory",
		"Usage: tart exec <vm-name>",
	}
	for _, s := range fatal {
		assert.True(t, isFatalExecError(s), "should be fatal: %s", s)
	}

	notFatal := []string{
		"connection refused",
		"VM agent is not running",
		"timeout waiting for response",
		"",
	}
	for _, s := range notFatal {
		assert.False(t, isFatalExecError(s), "should NOT be fatal: %s", s)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ABOUTME: Unit tests for Tart runtime — arg building, error mapping, helpers.
package tart

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/config"
	"github.com/kstenerud/yoloai/runtime"
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
	sandboxPath := t.TempDir()

	// Create an external dir that actually exists (for os.Stat check)
	extDir := t.TempDir()

	mounts := []runtime.MountSpec{
		// External dir — should get its own --dir share
		{Source: extDir, Target: "/Users/karl/project"},
		// Sandbox-internal dir — should be skipped (already in yoloai share)
		{Source: sandboxPath + "/agent-runtime", Target: "/home/yoloai/.claude/"},
		// File mount — should be skipped (VirtioFS only supports dirs)
		{Source: sandboxPath + "/runtime-config.json", Target: "/yoloai/runtime-config.json"},
	}
	args := r.buildRunArgs("yoloai-test", sandboxPath, mounts)

	assert.Contains(t, args, "run")
	assert.Contains(t, args, "--no-graphics")
	assert.Contains(t, args, "--dir")
	assert.Contains(t, args, "yoloai:"+sandboxPath)
	// External dir should have its own share
	assert.Contains(t, args, "m-"+filepath.Base(extDir)+":"+extDir)
	// Sandbox-internal and file mounts should NOT appear
	for _, a := range args {
		assert.NotContains(t, a, "agent-runtime")
		assert.NotContains(t, a, "runtime-config.json")
	}
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
		{Source: "/Users/karl/.yoloai/sandboxes/test/agent-runtime", Target: "/home/admin/.claude/"},
	}
	dirNames := map[string]string{
		"/Users/karl/project":                              "workdir",
		"/Users/karl/.yoloai/sandboxes/test/agent-runtime": "agent-runtime",
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

func TestRemapTargetPath(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{
			name:   "docker home subpath",
			input:  "/home/yoloai/.claude",
			expect: "/Users/admin/.claude",
		},
		{
			name:   "docker home exact",
			input:  "/home/yoloai",
			expect: "/Users/admin",
		},
		{
			name:   "yoloai prefix",
			input:  "/yoloai/runtime-config.json",
			expect: "/Users/admin/.yoloai/runtime-config.json",
		},
		{
			name:   "other user under /Users",
			input:  "/Users/karl/project",
			expect: "/Users/admin/host/Users/karl/project",
		},
		{
			name:   "already under vmHomeDir",
			input:  "/Users/admin/something",
			expect: "/Users/admin/something",
		},
		{
			name:   "no matching prefix",
			input:  "/tmp/foo",
			expect: "/tmp/foo",
		},
		{
			name:   "docker home subpath with trailing content",
			input:  "/home/yoloai/deep/nested/dir",
			expect: "/Users/admin/deep/nested/dir",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := remapTargetPath(tt.input)
			assert.Equal(t, tt.expect, got)
		})
	}
}

// patchConfigWorkingDir tests

func TestPatchConfigWorkingDir_RemapsDockerPath(t *testing.T) {
	sandboxDir := t.TempDir()
	cfg := map[string]interface{}{
		"agent_command": "claude",
		"working_dir":   "/home/yoloai/project",
	}
	cfgData, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, "runtime-config.json"), cfgData, 0600))

	r := &Runtime{}
	require.NoError(t, r.patchConfigWorkingDir(sandboxDir))

	data, err := os.ReadFile(filepath.Join(sandboxDir, "runtime-config.json")) //nolint:gosec // test
	require.NoError(t, err)
	var result map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &result))
	assert.Equal(t, "/Users/admin/project", result["working_dir"])
}

func TestPatchConfigWorkingDir_NoopWhenNoRemap(t *testing.T) {
	sandboxDir := t.TempDir()
	cfg := map[string]interface{}{
		"agent_command": "claude",
		"working_dir":   "/tmp/foo",
	}
	cfgData, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, "runtime-config.json"), cfgData, 0600))

	r := &Runtime{}
	require.NoError(t, r.patchConfigWorkingDir(sandboxDir))

	data, err := os.ReadFile(filepath.Join(sandboxDir, "runtime-config.json")) //nolint:gosec // test
	require.NoError(t, err)
	var result map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &result))
	assert.Equal(t, "/tmp/foo", result["working_dir"])
}

func TestPatchConfigWorkingDir_MissingConfig(t *testing.T) {
	sandboxDir := t.TempDir()
	r := &Runtime{}
	err := r.patchConfigWorkingDir(sandboxDir)
	assert.Error(t, err)
}

func TestPatchConfigWorkingDir_NoWorkingDirKey(t *testing.T) {
	sandboxDir := t.TempDir()
	cfg := map[string]interface{}{
		"agent_command": "claude",
	}
	cfgData, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, "runtime-config.json"), cfgData, 0600))

	r := &Runtime{}
	require.NoError(t, r.patchConfigWorkingDir(sandboxDir))

	// File should remain unchanged
	data, err := os.ReadFile(filepath.Join(sandboxDir, "runtime-config.json")) //nolint:gosec // test
	require.NoError(t, err)
	var result map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &result))
	assert.Nil(t, result["working_dir"])
}

func TestMountDirName(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{
			name:   "typical path",
			input:  "/Users/karl/project",
			expect: "m-project",
		},
		{
			name:   "deep nested path",
			input:  "/some/deep/nested/path",
			expect: "m-path",
		},
		{
			name:   "single component",
			input:  "/single",
			expect: "m-single",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mountDirName(tt.input)
			assert.Equal(t, tt.expect, got)
		})
	}
}

// TestSetupWorkDirInVM tests the command generation for VM work directory setup.
func TestSetupWorkDirInVM(t *testing.T) {
	r := &Runtime{}

	testCases := []struct {
		name                string
		virtiofsStagingPath string
		vmLocalPath         string
		expectMkdir         string
		expectRsync         string
		expectGit           string
	}{
		{
			name:                "simple path",
			virtiofsStagingPath: "/Volumes/My Shared Files/yoloai/work/encoded",
			vmLocalPath:         "/Users/admin/yoloai-work/encoded",
			expectMkdir:         "mkdir -p '/Users/admin/yoloai-work'",
			expectRsync:         "rsync -a '/Volumes/My Shared Files/yoloai/work/encoded/' '/Users/admin/yoloai-work/encoded/'",
			expectGit:           "cd '/Users/admin/yoloai-work/encoded' && git init && git add -A && git commit --allow-empty -m 'baseline'",
		},
		{
			name:                "path with special characters",
			virtiofsStagingPath: "/Volumes/My Shared Files/yoloai/work/project-name",
			vmLocalPath:         "/Users/admin/yoloai-work/project-name",
			expectMkdir:         "mkdir -p '/Users/admin/yoloai-work'",
			expectRsync:         "rsync -a '/Volumes/My Shared Files/yoloai/work/project-name/' '/Users/admin/yoloai-work/project-name/'",
			expectGit:           "cd '/Users/admin/yoloai-work/project-name' && git init && git add -A && git commit --allow-empty -m 'baseline'",
		},
		{
			name:                "deeply nested path",
			virtiofsStagingPath: "/Volumes/My Shared Files/yoloai/work/deep/nested/dir",
			vmLocalPath:         "/Users/admin/yoloai-work/deep/nested/dir",
			expectMkdir:         "mkdir -p '/Users/admin/yoloai-work/deep/nested'",
			expectRsync:         "rsync -a '/Volumes/My Shared Files/yoloai/work/deep/nested/dir/' '/Users/admin/yoloai-work/deep/nested/dir/'",
			expectGit:           "cd '/Users/admin/yoloai-work/deep/nested/dir' && git init && git add -A && git commit --allow-empty -m 'baseline'",
		},
		{
			name:                "encoded path with special chars",
			virtiofsStagingPath: "/Volumes/My Shared Files/yoloai/work/%2FUsers%2Fkarl%2Fproject",
			vmLocalPath:         "/Users/admin/yoloai-work/%2FUsers%2Fkarl%2Fproject",
			expectMkdir:         "mkdir -p '/Users/admin/yoloai-work'",
			expectRsync:         "rsync -a '/Volumes/My Shared Files/yoloai/work/%2FUsers%2Fkarl%2Fproject/' '/Users/admin/yoloai-work/%2FUsers%2Fkarl%2Fproject/'",
			expectGit:           "cd '/Users/admin/yoloai-work/%2FUsers%2Fkarl%2Fproject' && git init && git add -A && git commit --allow-empty -m 'baseline'",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmds := r.SetupWorkDirInVM(tc.virtiofsStagingPath, tc.vmLocalPath)

			// Should return exactly 3 commands: mkdir, rsync, git
			require.Len(t, cmds, 3, "should return 3 commands")

			// Verify mkdir command
			assert.Equal(t, tc.expectMkdir, cmds[0], "mkdir command should create parent directory")

			// Verify rsync command
			assert.Equal(t, tc.expectRsync, cmds[1], "rsync command should copy files with trailing slashes")

			// Verify git baseline command
			assert.Equal(t, tc.expectGit, cmds[2], "git command should initialize repo and create baseline commit")

			// Verify all paths are properly quoted (protect against spaces)
			for i, cmd := range cmds {
				assert.Contains(t, cmd, "'", "command %d should contain single quotes for path quoting", i)
			}
		})
	}
}

// TestSetupWorkDirInVM_TrailingSlashes verifies rsync gets trailing slashes.
func TestSetupWorkDirInVM_TrailingSlashes(t *testing.T) {
	r := &Runtime{}
	cmds := r.SetupWorkDirInVM("/source", "/dest")

	rsyncCmd := cmds[1]
	// rsync behavior: trailing slash on source means "copy contents", not "copy dir itself"
	assert.Contains(t, rsyncCmd, "'/source/'", "source should have trailing slash")
	assert.Contains(t, rsyncCmd, "'/dest/'", "dest should have trailing slash")
}

// TestResolveCopyMount tests path encoding for copy mode directories.
func TestResolveCopyMount(t *testing.T) {
	r := &Runtime{}

	testCases := []struct {
		name        string
		sandboxName string
		hostPath    string
		expectPath  string
	}{
		{
			name:        "simple path",
			sandboxName: "test-sandbox",
			hostPath:    "/Users/karl/project",
			expectPath:  "/Users/admin/yoloai-work/^sUsers^skarl^sproject", // ^s = / in caret encoding
		},
		{
			name:        "path with spaces",
			sandboxName: "test-sandbox",
			hostPath:    "/Users/karl/my project",
			expectPath:  "/Users/admin/yoloai-work/^sUsers^skarl^smy^_project", // ^_ = space in caret encoding
		},
		{
			name:        "nested path",
			sandboxName: "test-sandbox",
			hostPath:    "/Users/karl/dev/work/project",
			expectPath:  "/Users/admin/yoloai-work/^sUsers^skarl^sdev^swork^sproject",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := r.ResolveCopyMount(tc.sandboxName, tc.hostPath)
			assert.Equal(t, tc.expectPath, result)
			// Verify it's under /Users/admin/yoloai-work/
			assert.Contains(t, result, "/Users/admin/yoloai-work/", "should be under VM work directory")
		})
	}
}

// TestTranslateWorkDirToVMPath tests host-to-VM path translation for GitExec.
func TestTranslateWorkDirToVMPath(t *testing.T) {
	r := &Runtime{
		sandboxDir: config.SandboxesDir(),
	}

	testCases := []struct {
		name       string
		input      string
		expectPath string
	}{
		{
			name:       "host sandbox work path",
			input:      filepath.Join(config.SandboxesDir(), "mybox/work/^sUsers^skarl^sproject"),
			expectPath: "/Users/admin/yoloai-work/^sUsers^skarl^sproject",
		},
		{
			name:       "nested encoded path",
			input:      filepath.Join(config.SandboxesDir(), "test/work/^svar^sfolders^sh8^sp75r4zq95d59q622q33pngzr0000gn^sT^syoloai-smoke-dvrjw0tc^sproject-workflow-tart"),
			expectPath: "/Users/admin/yoloai-work/^svar^sfolders^sh8^sp75r4zq95d59q622q33pngzr0000gn^sT^syoloai-smoke-dvrjw0tc^sproject-workflow-tart",
		},
		{
			name:       "already VM path",
			input:      "/Users/admin/yoloai-work/^sUsers^skarl^sproject",
			expectPath: "/Users/admin/yoloai-work/^sUsers^skarl^sproject",
		},
		{
			name:       "non-sandbox path",
			input:      "/some/other/path",
			expectPath: "/some/other/path",
		},
		{
			name:       "sandbox but not work dir",
			input:      filepath.Join(config.SandboxesDir(), "mybox/files"),
			expectPath: filepath.Join(config.SandboxesDir(), "mybox/files"),
		},
		{
			name:       "deeply nested work subdir",
			input:      filepath.Join(config.SandboxesDir(), "mybox/work/^sUsers^skarl^sproject/subdir/nested"),
			expectPath: "/Users/admin/yoloai-work/^sUsers^skarl^sproject/subdir/nested",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := r.translateWorkDirToVMPath(tc.input)
			assert.Equal(t, tc.expectPath, result)
		})
	}
}

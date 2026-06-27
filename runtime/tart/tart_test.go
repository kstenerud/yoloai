// ABOUTME: Unit tests for Tart runtime — arg building, error mapping, helpers.
package tart

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/yoerrors"
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
	r := &Runtime{tartBin: "/usr/local/bin/tart", execEnv: []string{"PATH=/usr/bin:/bin"}}
	sandboxPath := t.TempDir()

	// Create an external dir that actually exists (for os.Stat check)
	extDir := t.TempDir()

	mounts := []runtime.MountSpec{
		// External dir — should get its own --dir share
		{HostPath: extDir, ContainerPath: "/Users/karl/project"},
		// Sandbox-internal dir — should be skipped (already in yoloai share)
		{HostPath: sandboxPath + "/agent-runtime", ContainerPath: "/home/yoloai/.claude/"},
		// File mount — should be skipped (VirtioFS only supports dirs)
		{HostPath: sandboxPath + "/runtime-config.json", ContainerPath: "/yoloai/runtime-config.json"},
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

// fakeFailingTart writes a stub `tart` that always exits 1 with the given
// stderr, regardless of subcommand — to exercise runTart's error mapping.
func fakeFailingTart(t *testing.T, stderr string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tart")
	script := "#!/bin/sh\nprintf '%s\\n' " + shellSingleQuote(stderr) + " >&2\nexit 1\n"
	require.NoError(t, os.WriteFile(path, []byte(script), 0700)) //nolint:gosec // test fixture needs exec bit
	return path
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// DF30: `tart exec` stderr is the inner guest command's, so a "no such"/"not
// found" message there must NOT be mapped to a VM-level sentinel — only
// VM-level operations (delete/list/clone) get that treatment.
func TestRunTart_ExecFailureNotMappedToSentinel(t *testing.T) {
	const innerErr = "ln: /mnt/test: No such file or directory"
	r := &Runtime{tartBin: fakeFailingTart(t, innerErr), execEnv: []string{"PATH=/usr/bin:/bin"}}

	_, err := r.runTart(context.Background(), execArgs("yoloai-test", "bash", "-c", "ln -sfn a /mnt/test")...)
	require.Error(t, err)
	assert.NotErrorIs(t, err, runtime.ErrNotFound,
		"exec inner-command stderr must not be read as a VM-level sentinel")
	assert.NotErrorIs(t, err, runtime.ErrNotRunning)
	assert.Contains(t, err.Error(), innerErr, "the real failure must be surfaced, not swallowed")
}

func TestRunTart_VMLevelFailureStillMapsToSentinel(t *testing.T) {
	// Same "no such" stderr, but a non-exec (VM-level) command — must still map.
	r := &Runtime{tartBin: fakeFailingTart(t, "no such VM"), execEnv: []string{"PATH=/usr/bin:/bin"}}

	_, err := r.runTart(context.Background(), "delete", "yoloai-test")
	assert.ErrorIs(t, err, runtime.ErrNotFound,
		"VM-level command stderr must still map to runtime.ErrNotFound")
}

func TestCheckVMLimitError_Detected(t *testing.T) {
	cases := []string{
		"The number of VMs exceeds the system limit",
		"The number of VMs exceeds the system limit (other running VMs: vm1, vm2)",
		"some prefix\nThe number of VMs exceeds the system limit\nsome suffix",
	}
	for _, logContent := range cases {
		end := len(logContent)
		if end > 50 {
			end = 50
		}
		t.Run(logContent[:end], func(t *testing.T) {
			err := checkVMLimitError(logContent)
			require.NotNil(t, err)
			var limitErr *yoerrors.ResourceLimitError
			assert.True(t, errors.As(err, &limitErr), "expected *ResourceLimitError")
		})
	}
}

func TestCheckVMLimitError_NotDetected(t *testing.T) {
	cases := []string{
		"",
		"some other error",
		"VM booted successfully",
		"tart: error: VM not found",
	}
	for _, logContent := range cases {
		assert.Nil(t, checkVMLimitError(logContent), "should not detect limit error in: %q", logContent)
	}
}

func TestResolveBaseImage_HostMatched(t *testing.T) {
	cases := []struct {
		name  string
		major int
		want  string
	}{
		{"sonoma", 14, "ghcr.io/cirruslabs/macos-sonoma-base:latest"},
		{"sequoia", 15, "ghcr.io/cirruslabs/macos-sequoia-base:latest"},
		{"tahoe", 26, "ghcr.io/cirruslabs/macos-tahoe-base:latest"},
		{"unmapped future falls back to newest known", 27, defaultBaseImage},
		{"unmapped old falls back to newest known", 12, defaultBaseImage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &Runtime{hostMajor: func() (int, error) { return tc.major, nil }}
			assert.Equal(t, tc.want, r.resolveBaseImage(""))
		})
	}
}

func TestResolveBaseImage_HostUndetected(t *testing.T) {
	// sw_vers failure → newest-known fallback, never an empty/garbage ref.
	r := &Runtime{hostMajor: func() (int, error) { return 0, assert.AnError }}
	assert.Equal(t, defaultBaseImage, r.resolveBaseImage(""))
}

func TestResolveBaseImage_Override(t *testing.T) {
	// Override wins even when the host would resolve to a different codename.
	r := &Runtime{
		baseImageOverride: "my-custom-vm",
		hostMajor:         func() (int, error) { return 15, nil },
	}
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

// TestResolveGuestMountPath verifies the GuestMountResolver wraps remapTargetPath
// and is idempotent on already-translated guest paths (safe for restart/reset).
func TestResolveGuestMountPath(t *testing.T) {
	r := &Runtime{}

	hostMirror := "/Users/karl/work/embrace"
	guest := r.ResolveGuestMountPath(hostMirror)
	assert.Equal(t, "/Users/admin/host/Users/karl/work/embrace", guest)

	// Re-resolving the guest path (as happens on restart, where the stored
	// MountPath is fed back as the mount target) must be a no-op.
	assert.Equal(t, guest, r.ResolveGuestMountPath(guest))
}

// patchConfigWorkingDir tests

func TestPatchConfigWorkingDir_RemapsDockerPath(t *testing.T) {
	sandboxDir := t.TempDir()
	cfg := map[string]any{
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
	var result map[string]any
	require.NoError(t, json.Unmarshal(data, &result))
	assert.Equal(t, "/Users/admin/project", result["working_dir"])
}

func TestPatchConfigWorkingDir_NoopWhenNoRemap(t *testing.T) {
	sandboxDir := t.TempDir()
	cfg := map[string]any{
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
	var result map[string]any
	require.NoError(t, json.Unmarshal(data, &result))
	assert.Equal(t, "/tmp/foo", result["working_dir"])
}

func TestPatchConfigWorkingDir_MissingConfig(t *testing.T) {
	sandboxDir := t.TempDir()
	r := &Runtime{}
	err := r.patchConfigWorkingDir(sandboxDir)
	require.NoError(t, err,
		"a missing runtime-config.json is a bare runtime instance (no sandbox monitor "+
			"provisioned) — there is nothing to patch, so Start proceeds with a booted, "+
			"exec-able VM rather than failing")
}

func TestPatchConfigWorkingDir_NoWorkingDirKey(t *testing.T) {
	sandboxDir := t.TempDir()
	cfg := map[string]any{
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
	var result map[string]any
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
	// Use a fixed DataDir so test cases can reference stable sandbox paths.
	const testDataDir = "/home/testuser/.yoloai"
	layout := config.NewLayout(testDataDir)
	r := &Runtime{layout: layout}
	sandboxesDir := layout.SandboxesDir()

	testCases := []struct {
		name       string
		input      string
		expectPath string
	}{
		{
			name:       "host sandbox work path",
			input:      filepath.Join(sandboxesDir, "mybox/work/^sUsers^skarl^sproject"),
			expectPath: "/Users/admin/yoloai-work/^sUsers^skarl^sproject",
		},
		{
			name:       "nested encoded path",
			input:      filepath.Join(sandboxesDir, "test/work/^svar^sfolders^sh8^sp75r4zq95d59q622q33pngzr0000gn^sT^syoloai-smoke-dvrjw0tc^sproject-workflow-tart"),
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
			input:      filepath.Join(sandboxesDir, "mybox/files"),
			expectPath: filepath.Join(sandboxesDir, "mybox/files"),
		},
		{
			name:       "deeply nested work subdir",
			input:      filepath.Join(sandboxesDir, "mybox/work/^sUsers^skarl^sproject/subdir/nested"),
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

func TestAttachCommand_ForcesUTF8(t *testing.T) {
	r := &Runtime{}
	cmd := r.AttachCommand("/private/tmp/tmux-501/default", 24, 80, "vm")
	require.NotEmpty(t, cmd)
	// -u is load-bearing: tart exec gives the client a C locale, so without it
	// tmux flags the client utf8=0 and repaints non-ASCII glyphs as '_'.
	assert.Equal(t, "tmux", cmd[0])
	assert.Contains(t, cmd, "-u")
	joined := strings.Join(cmd, " ")
	assert.Contains(t, joined, "-S /private/tmp/tmux-501/default")
	assert.Contains(t, joined, "attach -t main")
}

func TestAttachCommand_ForcesUTF8_NoSocket(t *testing.T) {
	r := &Runtime{}
	cmd := r.AttachCommand("", 24, 80, "vm")
	require.NotEmpty(t, cmd)
	assert.Contains(t, cmd, "-u")
	joined := strings.Join(cmd, " ")
	assert.NotContains(t, joined, "-S")
	assert.Contains(t, joined, "attach -t main")
}

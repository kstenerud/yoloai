package caps

// ABOUTME: Tests for DetectEnvironment with injected file paths for testability.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// injectPaths sets the injectable path vars to temp files and returns a restore function.
func injectPaths(t *testing.T) (tmpDir string, restore func()) {
	t.Helper()
	tmpDir = t.TempDir()

	origProc := procVersionPath
	origDocker := dockerEnvPath
	origCgroup := cgroupPath
	origGroup := groupFilePath

	procVersionPath = filepath.Join(tmpDir, "proc_version")
	dockerEnvPath = filepath.Join(tmpDir, "dockerenv")
	cgroupPath = filepath.Join(tmpDir, "cgroup")
	groupFilePath = filepath.Join(tmpDir, "group")

	return tmpDir, func() {
		procVersionPath = origProc
		dockerEnvPath = origDocker
		cgroupPath = origCgroup
		groupFilePath = origGroup
	}
}

func TestDetectIsWSL2_True(t *testing.T) {
	tmpDir, restore := injectPaths(t)
	defer restore()

	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "proc_version"),
		[]byte("Linux version 5.15.0-microsoft-standard-WSL2"), 0o600))

	env := DetectEnvironment()
	assert.True(t, env.IsWSL2)
}

func TestDetectIsWSL2_False(t *testing.T) {
	tmpDir, restore := injectPaths(t)
	defer restore()

	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "proc_version"),
		[]byte("Linux version 6.8.0-106-generic"), 0o600))

	env := DetectEnvironment()
	assert.False(t, env.IsWSL2)
}

func TestDetectIsWSL2_MissingFile(t *testing.T) {
	_, restore := injectPaths(t)
	defer restore()
	// Don't create proc_version file.
	env := DetectEnvironment()
	assert.False(t, env.IsWSL2)
}

func TestDetectInContainer_DockerEnvFile(t *testing.T) {
	tmpDir, restore := injectPaths(t)
	defer restore()

	// Create /.dockerenv equivalent.
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "dockerenv"), nil, 0o600))

	env := DetectEnvironment()
	assert.True(t, env.InContainer)
}

func TestDetectInContainer_CgroupDocker(t *testing.T) {
	tmpDir, restore := injectPaths(t)
	defer restore()

	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "cgroup"),
		[]byte("12:devices:/docker/abcdef123456"), 0o600))

	env := DetectEnvironment()
	assert.True(t, env.InContainer)
}

func TestDetectInContainer_CgroupKubepods(t *testing.T) {
	tmpDir, restore := injectPaths(t)
	defer restore()

	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "cgroup"),
		[]byte("11:memory:/kubepods/pod1234/container5678"), 0o600))

	env := DetectEnvironment()
	assert.True(t, env.InContainer)
}

func TestDetectInContainer_NotInContainer(t *testing.T) {
	tmpDir, restore := injectPaths(t)
	defer restore()

	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "cgroup"),
		[]byte("12:devices:/user.slice"), 0o600))

	env := DetectEnvironment()
	assert.False(t, env.InContainer)
}

func TestDetectKVMGroup_InGroup(t *testing.T) {
	tmpDir, restore := injectPaths(t)
	defer restore()

	// Write group file with the current USER in kvm group.
	username := os.Getenv("USER")
	if username == "" {
		t.Skip("USER not set")
	}
	groupContent := "kvm:x:136:" + username + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "group"), []byte(groupContent), 0o600)) //nolint:gosec // G703: test code with temp dir

	env := DetectEnvironment()
	assert.True(t, env.KVMGroup)
}

func TestDetectKVMGroup_NotInGroup(t *testing.T) {
	tmpDir, restore := injectPaths(t)
	defer restore()

	groupContent := "kvm:x:136:someotheruser\n"
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "group"), []byte(groupContent), 0o600)) //nolint:gosec // G703: test code with temp dir

	env := DetectEnvironment()
	assert.False(t, env.KVMGroup)
}

func TestDetectKVMGroup_NoKVMLine(t *testing.T) {
	tmpDir, restore := injectPaths(t)
	defer restore()

	groupContent := "docker:x:999:someuser\naudio:x:29:someuser\n"
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "group"), []byte(groupContent), 0o600)) //nolint:gosec // G703: test code with temp dir

	env := DetectEnvironment()
	assert.False(t, env.KVMGroup)
}

func TestDetectKVMGroup_MissingFile(t *testing.T) {
	_, restore := injectPaths(t)
	defer restore()
	// Don't create group file.
	env := DetectEnvironment()
	assert.False(t, env.KVMGroup)
}

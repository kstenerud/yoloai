package docker

import (
	"archive/tar"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateBuildContext(t *testing.T) {
	reader, err := createBuildContext()
	require.NoError(t, err)

	// Read the tar and verify embedded files are present
	tr := tar.NewReader(reader)
	found := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		found[hdr.Name] = true
	}

	assert.Contains(t, found, "Dockerfile")
	assert.Contains(t, found, "entrypoint.sh")
	assert.Contains(t, found, "entrypoint.py")
	assert.Contains(t, found, "sandbox-setup.py")
	assert.Contains(t, found, "setup_helpers.py")
	assert.Contains(t, found, "tmux_io.py")
	assert.Contains(t, found, "status-monitor.py")
	assert.Contains(t, found, "diagnose-idle.sh")
	assert.Contains(t, found, "tmux.conf")
	assert.Len(t, found, 9)
}

func TestCreateProfileBuildContext(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM yoloai-base"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "setup.sh"), []byte("apt install -y go"), 0600))
	// Internal files should be excluded
	require.NoError(t, os.WriteFile(filepath.Join(dir, lastBuildFile), []byte("abc"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("agent: claude"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "profile.yaml"), []byte("extends: base"), 0600))

	reader, err := createProfileBuildContext(dir)
	require.NoError(t, err)

	tr := tar.NewReader(reader)
	found := map[string]string{}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		data, err := io.ReadAll(tr)
		require.NoError(t, err)
		found[hdr.Name] = string(data)
	}

	assert.Contains(t, found, "Dockerfile")
	assert.Contains(t, found, "setup.sh")
	assert.Contains(t, found, "profile.yaml") // profile.yaml is NOT excluded (only config.yaml is)
	assert.NotContains(t, found, lastBuildFile)
	assert.NotContains(t, found, "config.yaml")
}

func TestNeedsBuild_NoChecksum(t *testing.T) {
	tmp := t.TempDir()
	layout := config.NewLayout(filepath.Join(tmp, ".yoloai"))
	assert.True(t, NeedsBuild(layout, ""))
}

func TestNeedsBuild_AfterRecord(t *testing.T) {
	tmp := t.TempDir()
	layout := config.NewLayout(filepath.Join(tmp, ".yoloai"))
	// Ensure cache dir exists (normally created by EnsureSetup).
	require.NoError(t, os.MkdirAll(filepath.Join(tmp, ".yoloai", "cache"), 0750))
	RecordBuildChecksum(layout, "")
	assert.False(t, NeedsBuild(layout, ""))
}

func TestBuildInputsChecksum_Deterministic(t *testing.T) {
	sum1 := buildInputsChecksum()
	sum2 := buildInputsChecksum()
	assert.Equal(t, sum1, sum2)
	assert.NotEmpty(t, sum1)
	assert.True(t, len(sum1) == 64, "expected SHA-256 hex string (64 chars), got %d", len(sum1))
}

// profileBuildChecksum tests

func TestProfileBuildChecksum_ValidDockerfile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM yoloai-base\nRUN apt install -y go"), 0600))

	sum := profileBuildChecksum(dir)
	assert.NotEmpty(t, sum)
	assert.Len(t, sum, 64, "expected SHA-256 hex string (64 chars)")
}

func TestProfileBuildChecksum_Deterministic(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM yoloai-base"), 0600))

	sum1 := profileBuildChecksum(dir)
	sum2 := profileBuildChecksum(dir)
	assert.Equal(t, sum1, sum2)
	assert.NotEmpty(t, sum1)
}

func TestProfileBuildChecksum_MissingDockerfile(t *testing.T) {
	dir := t.TempDir()
	sum := profileBuildChecksum(dir)
	assert.Empty(t, sum)
}

func TestCuratedBuildEnv_FiltersToAllowlistAndForcesBuildKit(t *testing.T) {
	snapshot := map[string]string{
		"DOCKER_HOST":         "tcp://10.0.0.1:2375",
		"HTTP_PROXY":          "http://proxy:8080",
		"HOME":                "/home/principal",
		"ANTHROPIC_API_KEY":   "sk-secret-should-not-leak",
		"SOME_OTHER_SECRET":   "nope",
		"XDG_RUNTIME_DIR":     "/run/user/1000",
		"DOCKER_CONFIG_EMPTY": "",
	}

	env := CuratedBuildEnv(config.MapEnv(snapshot))

	assert.Contains(t, env, "DOCKER_HOST=tcp://10.0.0.1:2375")
	assert.Contains(t, env, "HTTP_PROXY=http://proxy:8080")
	assert.Contains(t, env, "HOME=/home/principal")
	assert.Contains(t, env, "XDG_RUNTIME_DIR=/run/user/1000")
	assert.Contains(t, env, "DOCKER_BUILDKIT=1")

	for _, e := range env {
		assert.NotContains(t, e, "ANTHROPIC_API_KEY", "non-allowlisted credential must not reach the build child")
		assert.NotContains(t, e, "SOME_OTHER_SECRET", "non-allowlisted key must not reach the build child")
	}
}

func TestCuratedBuildEnv_NilSnapshotStillForcesBuildKit(t *testing.T) {
	env := CuratedBuildEnv(config.MapEnv(nil))
	assert.Equal(t, []string{"DOCKER_BUILDKIT=1"}, env)
}

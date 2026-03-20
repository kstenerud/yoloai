package docker

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

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
	assert.Contains(t, found, "status-monitor.py")
	assert.Contains(t, found, "diagnose-idle.sh")
	assert.Contains(t, found, "tmux.conf")
	assert.Len(t, found, 7)
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

func TestStreamBuildOutput_ValidMessages(t *testing.T) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	require.NoError(t, encoder.Encode(buildMessage{Stream: "Step 1/3\n"}))
	require.NoError(t, encoder.Encode(buildMessage{Stream: "Step 2/3\n"}))
	require.NoError(t, encoder.Encode(buildMessage{Stream: "Step 3/3\n"}))

	var output bytes.Buffer
	err := streamBuildOutput(&buf, &output)
	assert.NoError(t, err)
	assert.Contains(t, output.String(), "Step 1/3")
	assert.Contains(t, output.String(), "Step 3/3")
}

func TestStreamBuildOutput_ErrorMessage(t *testing.T) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	require.NoError(t, encoder.Encode(buildMessage{Stream: "Step 1/2\n"}))
	require.NoError(t, encoder.Encode(buildMessage{Error: "build failed: missing dependency"}))

	var output bytes.Buffer
	err := streamBuildOutput(&buf, &output)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "missing dependency")
}

func TestStreamBuildOutput_EmptyStream(t *testing.T) {
	var buf bytes.Buffer
	var output bytes.Buffer
	err := streamBuildOutput(&buf, &output)
	assert.NoError(t, err)
	assert.Empty(t, output.String())
}

func TestNeedsBuild_NoChecksum(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	assert.True(t, NeedsBuild(""))
}

func TestNeedsBuild_AfterRecord(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	// Ensure cache dir exists (normally created by EnsureSetup).
	require.NoError(t, os.MkdirAll(filepath.Join(tmp, ".yoloai", "cache"), 0750))
	RecordBuildChecksum("")
	assert.False(t, NeedsBuild(""))
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

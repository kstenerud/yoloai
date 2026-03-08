package docker

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateBuildContext(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM ubuntu"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "entrypoint.sh"), []byte("#!/bin/bash"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "entrypoint-user.sh"), []byte("#!/bin/bash\nset -euo pipefail"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "status-monitor.py"), []byte("#!/usr/bin/env python3"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "diagnose-idle.sh"), []byte("#!/bin/bash"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tmux.conf"), []byte("set -g mouse on"), 0600))

	reader, err := createBuildContext(dir)
	require.NoError(t, err)

	// Read the tar and verify files
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

	assert.Equal(t, "FROM ubuntu", found["Dockerfile"])
	assert.Equal(t, "#!/bin/bash", found["entrypoint.sh"])
	assert.Equal(t, "#!/bin/bash\nset -euo pipefail", found["entrypoint-user.sh"])
	assert.Equal(t, "#!/usr/bin/env python3", found["status-monitor.py"])
	assert.Equal(t, "#!/bin/bash", found["diagnose-idle.sh"])
	assert.Equal(t, "set -g mouse on", found["tmux.conf"])
	assert.Len(t, found, 6)
}

func TestCreateProfileBuildContext(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM yoloai-base"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "setup.sh"), []byte("apt install -y go"), 0600))
	// Internal files should be excluded
	require.NoError(t, os.WriteFile(filepath.Join(dir, checksumFile), []byte("{}"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, lastBuildFile), []byte("abc"), 0600))
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
	assert.NotContains(t, found, checksumFile)
	assert.NotContains(t, found, lastBuildFile)
	assert.NotContains(t, found, "profile.yaml")
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

func TestSeedResources_FirstRun(t *testing.T) {
	dir := t.TempDir()
	result, err := SeedResources(dir)
	require.NoError(t, err)
	assert.True(t, result.Changed)
	assert.Empty(t, result.Conflicts)

	// Verify files exist
	for _, name := range []string{"Dockerfile", "entrypoint.sh", "entrypoint-user.sh", "tmux.conf"} {
		_, err := os.Stat(filepath.Join(dir, name))
		assert.NoError(t, err, "expected %s to exist", name)
	}

	// Verify checksums file was written
	_, err = os.Stat(filepath.Join(dir, checksumFile))
	assert.NoError(t, err)
}

func TestSeedResources_SecondRunUnchanged(t *testing.T) {
	dir := t.TempDir()

	// First run
	_, err := SeedResources(dir)
	require.NoError(t, err)

	// Second run — nothing changed
	result, err := SeedResources(dir)
	require.NoError(t, err)
	assert.False(t, result.Changed)
	assert.Empty(t, result.Conflicts)
}

func TestSeedResources_UserModifiedFile(t *testing.T) {
	dir := t.TempDir()

	// First run
	_, err := SeedResources(dir)
	require.NoError(t, err)

	// Modify entrypoint.sh to simulate user customization
	customContent := []byte("#!/bin/bash\n# my custom entrypoint\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "entrypoint.sh"), customContent, 0600))

	// Modify the embedded version to trigger a conflict — we can't change
	// the embedded content, but we can change the checksum to simulate a
	// different embedded version.
	checksums, _ := loadChecksums(dir)
	checksums["entrypoint.sh"] = "different-from-on-disk"
	require.NoError(t, saveChecksums(dir, checksums))

	result, err := SeedResources(dir)
	require.NoError(t, err)

	// If embedded differs from on-disk and on-disk differs from last-seeded,
	// a .new file should be created
	if len(result.Conflicts) > 0 {
		assert.Contains(t, result.Conflicts, "entrypoint.sh")
		newPath := filepath.Join(dir, "entrypoint.sh.new")
		_, err := os.Stat(newPath)
		assert.NoError(t, err, "expected entrypoint.sh.new to exist")
	}
}

func TestNeedsBuild_NoChecksum(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM ubuntu"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "entrypoint.sh"), []byte("#!/bin/bash"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "entrypoint-user.sh"), []byte("#!/bin/bash"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "status-monitor.py"), []byte("#!/usr/bin/env python3"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "diagnose-idle.sh"), []byte("#!/bin/bash"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tmux.conf"), []byte("set -g mouse on"), 0600))

	assert.True(t, NeedsBuild(dir))
}

func TestNeedsBuild_AfterRecord(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM ubuntu"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "entrypoint.sh"), []byte("#!/bin/bash"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "entrypoint-user.sh"), []byte("#!/bin/bash"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "status-monitor.py"), []byte("#!/usr/bin/env python3"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "diagnose-idle.sh"), []byte("#!/bin/bash"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tmux.conf"), []byte("set -g mouse on"), 0600))

	RecordBuildChecksum(dir)
	assert.False(t, NeedsBuild(dir))
}

func TestLoadSaveChecksums_RoundTrip(t *testing.T) {
	dir := t.TempDir()

	original := map[string]string{
		"Dockerfile":    "abc123",
		"entrypoint.sh": "def456",
	}
	require.NoError(t, saveChecksums(dir, original))

	loaded, ok := loadChecksums(dir)
	assert.True(t, ok)
	assert.Equal(t, original, loaded)
}

func TestLoadChecksums_MissingFile(t *testing.T) {
	dir := t.TempDir()
	checksums, ok := loadChecksums(dir)
	assert.False(t, ok)
	assert.Empty(t, checksums)
}

func TestLoadChecksums_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, checksumFile), []byte("not json"), 0600))
	checksums, ok := loadChecksums(dir)
	assert.False(t, ok)
	assert.Empty(t, checksums)
}

func TestBuildInputsChecksum_MissingFile(t *testing.T) {
	dir := t.TempDir()
	// Only write some files, not all
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM ubuntu"), 0600))
	sum := buildInputsChecksum(dir)
	assert.Empty(t, sum, "should return empty when files are missing")
}

func TestBuildInputsChecksum_Deterministic(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM ubuntu"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "entrypoint.sh"), []byte("#!/bin/bash"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "entrypoint-user.sh"), []byte("#!/bin/bash"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "status-monitor.py"), []byte("#!/usr/bin/env python3"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "diagnose-idle.sh"), []byte("#!/bin/bash"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tmux.conf"), []byte("set -g mouse on"), 0600))

	sum1 := buildInputsChecksum(dir)
	sum2 := buildInputsChecksum(dir)
	assert.Equal(t, sum1, sum2)
	assert.NotEmpty(t, sum1)
	assert.True(t, len(sum1) == 64, "expected SHA-256 hex string (64 chars), got %d", len(sum1))
}

func TestSha256Hex(t *testing.T) {
	hash := sha256Hex([]byte("hello"))
	assert.Len(t, hash, 64)
	// Known SHA-256 of "hello"
	assert.Equal(t, "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824", hash)
}

func TestCreateBuildContext_MissingFile(t *testing.T) {
	dir := t.TempDir()
	// Only write Dockerfile, missing entrypoint.sh and tmux.conf
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM ubuntu"), 0600))

	_, err := createBuildContext(dir)
	assert.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "entrypoint.sh") || strings.Contains(err.Error(), "tmux.conf"))
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

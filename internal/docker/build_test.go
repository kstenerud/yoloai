package docker

import (
	"archive/tar"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSeedResources_CreatesFiles(t *testing.T) {
	dir := t.TempDir()
	targetDir := filepath.Join(dir, "yoloai")

	err := SeedResources(targetDir)
	require.NoError(t, err)

	dockerfile, err := os.ReadFile(filepath.Join(targetDir, "Dockerfile.base")) //nolint:gosec // G304: test code with temp dir
	require.NoError(t, err)
	assert.Equal(t, embeddedDockerfile, dockerfile)

	entrypoint, err := os.ReadFile(filepath.Join(targetDir, "entrypoint.sh")) //nolint:gosec // G304: test code with temp dir
	require.NoError(t, err)
	assert.Equal(t, embeddedEntrypoint, entrypoint)
}

func TestSeedResources_DoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()

	customContent := []byte("# custom dockerfile\n")
	err := os.WriteFile(filepath.Join(dir, "Dockerfile.base"), customContent, 0600)
	require.NoError(t, err)

	err = SeedResources(dir)
	require.NoError(t, err)

	// Existing file should be preserved
	content, err := os.ReadFile(filepath.Join(dir, "Dockerfile.base")) //nolint:gosec // G304: test code with temp dir
	require.NoError(t, err)
	assert.Equal(t, customContent, content, "existing file should not be overwritten")

	// Missing file should be created
	entrypoint, err := os.ReadFile(filepath.Join(dir, "entrypoint.sh")) //nolint:gosec // G304: test code with temp dir
	require.NoError(t, err)
	assert.Equal(t, embeddedEntrypoint, entrypoint)
}

func TestCreateBuildContext_ValidTar(t *testing.T) {
	dir := t.TempDir()

	dockerfileContent := []byte("FROM debian:slim\n")
	entrypointContent := []byte("#!/bin/bash\necho hello\n")

	err := os.WriteFile(filepath.Join(dir, "Dockerfile.base"), dockerfileContent, 0600)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(dir, "entrypoint.sh"), entrypointContent, 0600)
	require.NoError(t, err)

	reader, err := createBuildContext(dir)
	require.NoError(t, err)

	// Read tar entries
	tr := tar.NewReader(reader)
	found := make(map[string][]byte)

	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)

		content, err := io.ReadAll(tr)
		require.NoError(t, err)
		found[header.Name] = content
	}

	// Verify Dockerfile (renamed from Dockerfile.base)
	assert.Contains(t, found, "Dockerfile", "tar should contain Dockerfile (not Dockerfile.base)")
	assert.Equal(t, dockerfileContent, found["Dockerfile"])

	// Verify entrypoint.sh
	assert.Contains(t, found, "entrypoint.sh")
	assert.Equal(t, entrypointContent, found["entrypoint.sh"])

	// Should not contain Dockerfile.base
	assert.NotContains(t, found, "Dockerfile.base")
}

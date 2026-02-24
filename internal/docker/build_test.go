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

	result, err := SeedResources(targetDir)
	require.NoError(t, err)
	assert.True(t, result.Changed, "should report changed when creating new files")
	assert.Empty(t, result.Conflicts)

	dockerfile, err := os.ReadFile(filepath.Join(targetDir, "Dockerfile.base")) //nolint:gosec // G304: test code with temp dir
	require.NoError(t, err)
	assert.Equal(t, embeddedDockerfile, dockerfile)

	entrypoint, err := os.ReadFile(filepath.Join(targetDir, "entrypoint.sh")) //nolint:gosec // G304: test code with temp dir
	require.NoError(t, err)
	assert.Equal(t, embeddedEntrypoint, entrypoint)
}

func TestSeedResources_NoChangeWhenCurrent(t *testing.T) {
	dir := t.TempDir()

	// First seed — creates files and checksum manifest
	result, err := SeedResources(dir)
	require.NoError(t, err)
	assert.True(t, result.Changed)

	// Second seed — files and embedded versions match, no change
	result, err = SeedResources(dir)
	require.NoError(t, err)
	assert.False(t, result.Changed, "should not report changed when files match embedded version")
	assert.Empty(t, result.Conflicts)
}

func TestSeedResources_OverwritesUnmodifiedFiles(t *testing.T) {
	dir := t.TempDir()

	// First seed — creates files and records checksums
	result, err := SeedResources(dir)
	require.NoError(t, err)
	assert.True(t, result.Changed)

	// Simulate a binary upgrade by writing different content directly
	// but keeping the checksum manifest pointing to the old content.
	// This mimics the case where the on-disk file matches the last-seeded
	// checksum (user hasn't touched it) but the embedded version changed.
	//
	// We can't truly change the embedded content in a test, so instead
	// we modify the file to differ from embedded AND update the checksum
	// to match the modified file — simulating "last seeded was this old version."
	staleContent := []byte("# old seeded version\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "entrypoint.sh"), staleContent, 0600))
	checksums := loadChecksums(dir)
	checksums["entrypoint.sh"] = sha256Hex(staleContent) // checksum matches on-disk
	require.NoError(t, saveChecksums(dir, checksums))

	// Now seed again — on-disk matches last-seeded checksum (not user-modified)
	// but differs from embedded → should overwrite
	result, err = SeedResources(dir)
	require.NoError(t, err)
	assert.True(t, result.Changed, "should overwrite unmodified file when embedded version changed")
	assert.Empty(t, result.Conflicts)

	// File should now match embedded version
	entrypoint, err := os.ReadFile(filepath.Join(dir, "entrypoint.sh")) //nolint:gosec // G304: test code with temp dir
	require.NoError(t, err)
	assert.Equal(t, embeddedEntrypoint, entrypoint)
}

func TestSeedResources_ConflictOnUserCustomization(t *testing.T) {
	dir := t.TempDir()

	// First seed — creates files and records checksums
	_, err := SeedResources(dir)
	require.NoError(t, err)

	// User customizes entrypoint.sh
	customContent := []byte("#!/bin/bash\n# my custom entrypoint\necho custom\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "entrypoint.sh"), customContent, 0600))

	// Simulate embedded version change by tampering with the checksum
	// to differ from both on-disk and the real embedded content.
	checksums := loadChecksums(dir)
	checksums["entrypoint.sh"] = sha256Hex([]byte("# original seeded version"))
	require.NoError(t, saveChecksums(dir, checksums))

	// Seed again — on-disk differs from last-seeded checksum (user modified)
	// and embedded version also differs → conflict
	result, err := SeedResources(dir)
	require.NoError(t, err)
	assert.False(t, result.Changed, "should not report changed when conflict detected")
	assert.Contains(t, result.Conflicts, "entrypoint.sh")

	// User's file should be preserved
	content, err := os.ReadFile(filepath.Join(dir, "entrypoint.sh")) //nolint:gosec // G304: test code with temp dir
	require.NoError(t, err)
	assert.Equal(t, customContent, content, "user's file should not be overwritten")

	// .new file should contain embedded version
	newContent, err := os.ReadFile(filepath.Join(dir, "entrypoint.sh.new")) //nolint:gosec // G304: test code with temp dir
	require.NoError(t, err)
	assert.Equal(t, embeddedEntrypoint, newContent)
}

func TestSeedResources_NoManifestAssumesUserCustomization(t *testing.T) {
	dir := t.TempDir()

	// Write a file that differs from embedded, with no checksum manifest
	// (simulates upgrading from a binary that didn't track checksums)
	customContent := []byte("#!/bin/bash\n# user's old custom version\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "entrypoint.sh"), customContent, 0600))

	result, err := SeedResources(dir)
	require.NoError(t, err)

	// Dockerfile.base was missing → created (changed=true)
	assert.True(t, result.Changed)
	// entrypoint.sh differs from embedded with no manifest → conflict
	assert.Contains(t, result.Conflicts, "entrypoint.sh")

	// User's file preserved
	content, err := os.ReadFile(filepath.Join(dir, "entrypoint.sh")) //nolint:gosec // G304: test code with temp dir
	require.NoError(t, err)
	assert.Equal(t, customContent, content)

	// .new file written
	assert.FileExists(t, filepath.Join(dir, "entrypoint.sh.new"))
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

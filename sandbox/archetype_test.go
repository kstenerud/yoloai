// ABOUTME: Tests for ParseArchetype, ValidArchetypes, and DetectArchetype auto-detection.
// ABOUTME: Covers detection priority: devcontainer > compose > apple > simple.

package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseArchetype_Valid(t *testing.T) {
	cases := []string{"simple", "compose", "devcontainer", "apple"}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			a, err := ParseArchetype(c)
			require.NoError(t, err)
			assert.Equal(t, Archetype(c), a)
		})
	}
}

func TestParseArchetype_Invalid(t *testing.T) {
	_, err := ParseArchetype("unknown")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown archetype")
}

func TestValidArchetypes_Sorted(t *testing.T) {
	names := ValidArchetypes()
	assert.NotEmpty(t, names)
	for i := 1; i < len(names); i++ {
		assert.LessOrEqual(t, names[i-1], names[i], "ValidArchetypes should be sorted")
	}
}

func TestValidArchetypes_ContainsAll(t *testing.T) {
	names := ValidArchetypes()
	assert.Contains(t, names, "simple")
	assert.Contains(t, names, "compose")
	assert.Contains(t, names, "devcontainer")
	assert.Contains(t, names, "apple")
}

func TestDetectArchetype_Simple(t *testing.T) {
	dir := t.TempDir()
	arch, signals := DetectArchetype(dir)
	assert.Equal(t, ArchetypeSimple, arch)
	assert.NotEmpty(t, signals)
}

func TestDetectArchetype_DevcontainerSubdir(t *testing.T) {
	dir := t.TempDir()
	dcDir := filepath.Join(dir, ".devcontainer")
	require.NoError(t, os.MkdirAll(dcDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(`{}`), 0600))

	arch, signals := DetectArchetype(dir)
	assert.Equal(t, ArchetypeDevcontainer, arch)
	assert.NotEmpty(t, signals)
}

func TestDetectArchetype_DevcontainerRoot(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "devcontainer.json"), []byte(`{}`), 0600))

	arch, signals := DetectArchetype(dir)
	assert.Equal(t, ArchetypeDevcontainer, arch)
	assert.NotEmpty(t, signals)
}

func TestDetectArchetype_ComposeYaml(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docker-compose.yaml"), []byte("services: {}"), 0600))

	arch, signals := DetectArchetype(dir)
	assert.Equal(t, ArchetypeCompose, arch)
	assert.NotEmpty(t, signals)
}

func TestDetectArchetype_ComposeYml(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte("services: {}"), 0600))

	arch, signals := DetectArchetype(dir)
	assert.Equal(t, ArchetypeCompose, arch)
	assert.NotEmpty(t, signals)
}

func TestDetectArchetype_DevcontainerWinsOverCompose(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docker-compose.yaml"), []byte("services: {}"), 0600))
	dcDir := filepath.Join(dir, ".devcontainer")
	require.NoError(t, os.MkdirAll(dcDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(`{}`), 0600))

	arch, _ := DetectArchetype(dir)
	assert.Equal(t, ArchetypeDevcontainer, arch)
}

func TestDetectArchetype_AppleXcodeProj(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "MyApp.xcodeproj"), 0750))

	arch, signals := DetectArchetype(dir)
	assert.Equal(t, ArchetypeApple, arch)
	assert.NotEmpty(t, signals)
}

func TestDetectArchetype_AppleXcworkspace(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "MyApp.xcworkspace"), 0750))

	arch, signals := DetectArchetype(dir)
	assert.Equal(t, ArchetypeApple, arch)
	assert.NotEmpty(t, signals)
}

func TestDetectArchetype_ApplePackageSwift(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Package.swift"), []byte(""), 0600))

	arch, signals := DetectArchetype(dir)
	assert.Equal(t, ArchetypeApple, arch)
	assert.NotEmpty(t, signals)
}

func TestDetectArchetype_ComposeWinsOverApple(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docker-compose.yaml"), []byte("services: {}"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Package.swift"), []byte(""), 0600))

	arch, _ := DetectArchetype(dir)
	assert.Equal(t, ArchetypeCompose, arch)
}

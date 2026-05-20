// ABOUTME: Tests for LoadYoloAIYaml: missing file, valid file, unknown archetype, tilde expansion.
// ABOUTME: Covers all return paths including not-found, parse success, and validation errors.

package archetype

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadYoloAIYaml_Missing(t *testing.T) {
	dir := t.TempDir()
	cfg, found, err := LoadYoloAIYaml(dir)
	require.NoError(t, err)
	assert.False(t, found)
	assert.Nil(t, cfg)
}

func TestLoadYoloAIYaml_ValidAllFields(t *testing.T) {
	dir := t.TempDir()
	content := `archetype: devcontainer
mounts:
  - /host/path:/container/path:ro
requires:
  yoloai: ">=1.0"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".yoloai.yaml"), []byte(content), 0600))

	cfg, found, err := LoadYoloAIYaml(dir)
	require.NoError(t, err)
	assert.True(t, found)
	require.NotNil(t, cfg)
	assert.Equal(t, "devcontainer", cfg.Archetype)
	assert.Equal(t, []string{"/host/path:/container/path:ro"}, cfg.Mounts)
	assert.Equal(t, map[string]string{"yoloai": ">=1.0"}, cfg.Requires)
}

func TestLoadYoloAIYaml_UnknownArchetype(t *testing.T) {
	dir := t.TempDir()
	content := "archetype: invalid-archetype\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".yoloai.yaml"), []byte(content), 0600))

	cfg, found, err := LoadYoloAIYaml(dir)
	require.Error(t, err)
	assert.False(t, found)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "unknown archetype")
}

func TestLoadYoloAIYaml_TildeExpansion(t *testing.T) {
	dir := t.TempDir()
	homeDir, err := os.UserHomeDir()
	require.NoError(t, err)

	content := "mounts:\n  - ~/mydata:/container/mydata:ro\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".yoloai.yaml"), []byte(content), 0600))

	cfg, found, err := LoadYoloAIYaml(dir)
	require.NoError(t, err)
	assert.True(t, found)
	require.Len(t, cfg.Mounts, 1)
	assert.True(t, strings.HasPrefix(cfg.Mounts[0], homeDir),
		"expected tilde expanded to home dir in %q", cfg.Mounts[0])
}

func TestLoadYoloAIYaml_NoArchetype(t *testing.T) {
	dir := t.TempDir()
	content := "mounts:\n  - /data:/data:ro\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".yoloai.yaml"), []byte(content), 0600))

	cfg, found, err := LoadYoloAIYaml(dir)
	require.NoError(t, err)
	assert.True(t, found)
	require.NotNil(t, cfg)
	assert.Empty(t, cfg.Archetype)
}

func TestLoadYoloAIYaml_AllValidArchetypes(t *testing.T) {
	for _, arch := range ValidArchetypes() {
		t.Run(arch, func(t *testing.T) {
			dir := t.TempDir()
			content := "archetype: " + arch + "\n"
			require.NoError(t, os.WriteFile(filepath.Join(dir, ".yoloai.yaml"), []byte(content), 0600))

			cfg, found, err := LoadYoloAIYaml(dir)
			require.NoError(t, err)
			assert.True(t, found)
			require.NotNil(t, cfg)
			assert.Equal(t, arch, cfg.Archetype)
		})
	}
}

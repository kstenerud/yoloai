// ABOUTME: Tests for InjectVSCodeWorkspace, mergeExtensionsJSON, and mergeSettingsJSON.
// ABOUTME: Covers nil/empty skip, dedup of extensions, and existing-key-wins for settings.

package archetype

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInjectVSCodeWorkspace_NilDC(t *testing.T) {
	dir := t.TempDir()
	err := InjectVSCodeWorkspace(dir, nil)
	require.NoError(t, err)
	// No .vscode dir should be created
	_, statErr := os.Stat(filepath.Join(dir, ".vscode"))
	assert.True(t, os.IsNotExist(statErr))
}

func TestInjectVSCodeWorkspace_EmptyCustomizations(t *testing.T) {
	dir := t.TempDir()
	dc := &DevcontainerConfig{}
	err := InjectVSCodeWorkspace(dir, dc)
	require.NoError(t, err)
	_, statErr := os.Stat(filepath.Join(dir, ".vscode"))
	assert.True(t, os.IsNotExist(statErr))
}

func TestInjectVSCodeWorkspace_WritesExtensions(t *testing.T) {
	dir := t.TempDir()
	dc := &DevcontainerConfig{}
	dc.Customizations.VSCode.Extensions = []string{"ms-python.python", "golang.go"}
	require.NoError(t, InjectVSCodeWorkspace(dir, dc))

	data, err := os.ReadFile(filepath.Join(dir, ".vscode", "extensions.json")) //nolint:gosec // G304: test-controlled temp dir
	require.NoError(t, err)
	var result struct {
		Recommendations []string `json:"recommendations"`
	}
	require.NoError(t, json.Unmarshal(data, &result))
	assert.ElementsMatch(t, []string{"ms-python.python", "golang.go"}, result.Recommendations)
}

func TestInjectVSCodeWorkspace_WritesSettings(t *testing.T) {
	dir := t.TempDir()
	dc := &DevcontainerConfig{}
	dc.Customizations.VSCode.Settings = map[string]any{"editor.tabSize": float64(4)}
	require.NoError(t, InjectVSCodeWorkspace(dir, dc))

	data, err := os.ReadFile(filepath.Join(dir, ".vscode", "settings.json")) //nolint:gosec // G304: test-controlled temp dir
	require.NoError(t, err)
	var result map[string]any
	require.NoError(t, json.Unmarshal(data, &result))
	assert.Equal(t, float64(4), result["editor.tabSize"])
}

func TestMergeExtensionsJSON_Dedup(t *testing.T) {
	dir := t.TempDir()
	// Write existing file with one extension
	existing := `{"recommendations": ["ms-python.python"]}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "extensions.json"), []byte(existing), 0600))

	// Merge: one duplicate, one new
	require.NoError(t, mergeExtensionsJSON(dir, []string{"ms-python.python", "golang.go"}))

	data, err := os.ReadFile(filepath.Join(dir, "extensions.json")) //nolint:gosec // G304: test-controlled temp dir
	require.NoError(t, err)
	var result struct {
		Recommendations []string `json:"recommendations"`
	}
	require.NoError(t, json.Unmarshal(data, &result))
	// Should have exactly 2 (no duplicate)
	assert.Len(t, result.Recommendations, 2)
	assert.Contains(t, result.Recommendations, "ms-python.python")
	assert.Contains(t, result.Recommendations, "golang.go")
}

func TestMergeExtensionsJSON_ExistingPreserved(t *testing.T) {
	dir := t.TempDir()
	existing := `{"recommendations": ["project.ext"]}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "extensions.json"), []byte(existing), 0600))

	require.NoError(t, mergeExtensionsJSON(dir, []string{"new.ext"}))

	data, err := os.ReadFile(filepath.Join(dir, "extensions.json")) //nolint:gosec // G304: test-controlled temp dir
	require.NoError(t, err)
	var result struct {
		Recommendations []string `json:"recommendations"`
	}
	require.NoError(t, json.Unmarshal(data, &result))
	assert.Contains(t, result.Recommendations, "project.ext")
	assert.Contains(t, result.Recommendations, "new.ext")
}

func TestMergeExtensionsJSON_InvalidExistingSkipsInjection(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "extensions.json"), []byte("not json"), 0600))

	// Should not error — just skip injection
	require.NoError(t, mergeExtensionsJSON(dir, []string{"ms-python.python"}))

	// File should remain unchanged (unparseable JSON left as-is)
	data, err := os.ReadFile(filepath.Join(dir, "extensions.json")) //nolint:gosec // G304: test-controlled temp dir
	require.NoError(t, err)
	assert.Equal(t, "not json", string(data))
}

func TestMergeSettingsJSON_ExistingKeysWin(t *testing.T) {
	dir := t.TempDir()
	existing := `{"editor.tabSize": 2, "editor.formatOnSave": false}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "settings.json"), []byte(existing), 0600))

	// devcontainer wants different tabSize and adds a new key
	newSettings := map[string]any{
		"editor.tabSize": float64(4),
		"python.linting": true,
	}
	require.NoError(t, mergeSettingsJSON(dir, newSettings))

	data, err := os.ReadFile(filepath.Join(dir, "settings.json")) //nolint:gosec // G304: test-controlled temp dir
	require.NoError(t, err)
	var result map[string]any
	require.NoError(t, json.Unmarshal(data, &result))

	// Existing key wins — tabSize stays 2
	assert.Equal(t, float64(2), result["editor.tabSize"])
	// Existing key preserved
	assert.Equal(t, false, result["editor.formatOnSave"])
	// New key from devcontainer added
	assert.Equal(t, true, result["python.linting"])
}

func TestMergeSettingsJSON_NoExistingFile(t *testing.T) {
	dir := t.TempDir()
	newSettings := map[string]any{"editor.tabSize": float64(4)}
	require.NoError(t, mergeSettingsJSON(dir, newSettings))

	data, err := os.ReadFile(filepath.Join(dir, "settings.json")) //nolint:gosec // G304: test-controlled temp dir
	require.NoError(t, err)
	var result map[string]any
	require.NoError(t, json.Unmarshal(data, &result))
	assert.Equal(t, float64(4), result["editor.tabSize"])
}

// ABOUTME: Tests agent.json Save/Load roundtrip and the zero-value default when
// ABOUTME: the file is missing.
package agentcfg_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/orchestrator/agentcfg"
)

func TestAgentConfig_Roundtrip(t *testing.T) {
	dir := t.TempDir()

	cfg := &agentcfg.AgentConfig{AgentType: "claude", Model: "opus"}
	require.NoError(t, agentcfg.Save(dir, cfg))

	loaded, err := agentcfg.Load(dir)
	require.NoError(t, err)
	assert.Equal(t, "claude", loaded.AgentType)
	assert.Equal(t, "opus", loaded.Model)
	assert.Equal(t, 1, loaded.Version)
}

func TestAgentConfig_MissingFile(t *testing.T) {
	dir := t.TempDir()

	loaded, err := agentcfg.Load(dir)
	require.NoError(t, err)
	assert.Equal(t, "", loaded.AgentType, "missing agent.json should return zero value")
}

// TestSaveTo_WritesExactlyWhereTold: the path-taking writer resolves nothing —
// it writes where told and creates no host/ tier, which is what lets a pre-v6
// migrator write a flat-era record (DF164).
func TestSaveTo_WritesExactlyWhereTold(t *testing.T) {
	dir := t.TempDir()
	flat := filepath.Join(dir, "agent.json") // literal: a pre-tier record

	require.NoError(t, agentcfg.SaveTo(flat, &agentcfg.AgentConfig{AgentType: "claude", Model: "opus"}))

	require.FileExists(t, flat)
	_, err := os.Stat(filepath.Join(dir, "host"))
	assert.True(t, os.IsNotExist(err), "a path-taking writer must not create the host/ tier")

	data, err := os.ReadFile(flat) //nolint:gosec // G304: trusted t.TempDir() subpath
	require.NoError(t, err)
	var got agentcfg.AgentConfig
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, "claude", got.AgentType)
	assert.Equal(t, "opus", got.Model)
}

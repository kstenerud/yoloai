// ABOUTME: Tests agent.json Save/Load roundtrip and the zero-value default when
// ABOUTME: the file is missing.
package agentcfg_test

import (
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

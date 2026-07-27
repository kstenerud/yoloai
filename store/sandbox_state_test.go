// ABOUTME: SandboxState (sandbox-state.json) save/load round-trip, the
// ABOUTME: zero-value default when the file is absent, and error surfacing on
// ABOUTME: corrupt JSON.
package store

import (
	"os"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSandboxState_Roundtrip(t *testing.T) {
	dir := t.TempDir()

	state := &SandboxState{AgentFilesInitialized: true}
	require.NoError(t, SaveSandboxState(dir, state))

	loaded, err := LoadSandboxState(dir)
	require.NoError(t, err)
	assert.True(t, loaded.AgentFilesInitialized)
}

func TestSandboxState_MissingFile(t *testing.T) {
	dir := t.TempDir()

	loaded, err := LoadSandboxState(dir)
	require.NoError(t, err)
	assert.False(t, loaded.AgentFilesInitialized, "missing sandbox-state.json should return zero value")
}

func TestSandboxState_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(config.HostTierDir(dir), 0750))
	require.NoError(t, os.WriteFile(SandboxStateFilePath(dir), []byte("{invalid"), 0600))

	_, err := LoadSandboxState(dir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse "+SandboxStateFile)
}

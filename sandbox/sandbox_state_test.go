package sandbox

import (
	"os"
	"path/filepath"
	"testing"

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

func TestSandboxState_FalseValue(t *testing.T) {
	dir := t.TempDir()

	state := &SandboxState{AgentFilesInitialized: false}
	require.NoError(t, SaveSandboxState(dir, state))

	loaded, err := LoadSandboxState(dir)
	require.NoError(t, err)
	assert.False(t, loaded.AgentFilesInitialized)
}

func TestSandboxState_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, SandboxStateFile), []byte("{invalid"), 0600))

	_, err := LoadSandboxState(dir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse "+SandboxStateFile)
}

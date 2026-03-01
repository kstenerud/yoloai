package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatePath(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	p, err := StatePath()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(tmpDir, ".yoloai", "state.yaml"), p)
}

func TestLoadState_Missing(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	state, err := LoadState()
	require.NoError(t, err)
	assert.False(t, state.SetupComplete)
}

func TestLoadState_Exists(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "state.yaml"), []byte("setup_complete: true\n"), 0600))

	state, err := LoadState()
	require.NoError(t, err)
	assert.True(t, state.SetupComplete)
}

func TestSaveState(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))

	require.NoError(t, SaveState(&State{SetupComplete: true}))

	state, err := LoadState()
	require.NoError(t, err)
	assert.True(t, state.SetupComplete)
}

func TestSaveState_Roundtrip(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))

	// Save false, load, verify
	require.NoError(t, SaveState(&State{SetupComplete: false}))
	state, err := LoadState()
	require.NoError(t, err)
	assert.False(t, state.SetupComplete)

	// Save true, load, verify
	require.NoError(t, SaveState(&State{SetupComplete: true}))
	state, err = LoadState()
	require.NoError(t, err)
	assert.True(t, state.SetupComplete)
}

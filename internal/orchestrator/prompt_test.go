// ABOUTME: Tests for ReadStoredPrompt — missing vs empty vs present prompt.txt.
package orchestrator

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/store"
)

func promptLayout(t *testing.T) (config.Layout, string) {
	t.Helper()
	tmp := t.TempDir()
	layout := config.NewLayout(filepath.Join(tmp, ".yoloai"))
	name := "box"
	createTestSandbox(t, tmp, name, filepath.Join(tmp, "host"), store.DirModeCopy)
	return layout, name
}

func TestReadStoredPrompt_Missing(t *testing.T) {
	layout, name := promptLayout(t)
	text, configured, err := ReadStoredPrompt(layout, name)
	require.NoError(t, err)
	assert.False(t, configured)
	assert.Empty(t, text)
}

func TestReadStoredPrompt_Empty(t *testing.T) {
	layout, name := promptLayout(t)
	require.NoError(t, os.WriteFile(store.PromptFilePath(layout.SandboxDir(name)), []byte(""), 0600))

	text, configured, err := ReadStoredPrompt(layout, name)
	require.NoError(t, err)
	assert.True(t, configured)
	assert.Empty(t, text)
}

func TestReadStoredPrompt_Present(t *testing.T) {
	layout, name := promptLayout(t)
	require.NoError(t, os.WriteFile(store.PromptFilePath(layout.SandboxDir(name)), []byte("do the thing"), 0600))

	text, configured, err := ReadStoredPrompt(layout, name)
	require.NoError(t, err)
	assert.True(t, configured)
	assert.Equal(t, "do the thing", text)
}

func TestReadStoredPrompt_NoSandbox(t *testing.T) {
	tmp := t.TempDir()
	layout := config.NewLayout(filepath.Join(tmp, ".yoloai"))
	_, _, err := ReadStoredPrompt(layout, "ghost")
	require.ErrorIs(t, err, store.ErrSandboxNotFound,
		"a missing sandbox must surface ErrSandboxNotFound, distinct from the missing-prompt no-op")
}

// ABOUTME: Tests for the runtime-free SystemClient file-exchange / cache path
// ABOUTME: verbs.

package yoloai

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSystemClient_ExchangePaths(t *testing.T) {
	dir := t.TempDir()
	sc, err := NewSystemClient(SystemOptions{DataDir: dir})
	require.NoError(t, err)

	state := sc.layout.SandboxDir("box")
	assert.Equal(t, filepath.Join(state, "files"), sc.FilesDir("box"))
	assert.Equal(t, filepath.Join(state, "cache"), sc.CacheDir("box"))
}

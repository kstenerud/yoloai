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
	assert.Equal(t, filepath.Join(state, "runtime-config.json"), sc.RuntimeConfigPath("box"))
	assert.Equal(t, filepath.Join(state, "environment.json"), sc.EnvironmentPath("box"))
}

func TestSystemClient_LogPaths(t *testing.T) {
	dir := t.TempDir()
	sc, err := NewSystemClient(SystemOptions{DataDir: dir})
	require.NoError(t, err)

	state := sc.layout.SandboxDir("box")
	logs := sc.LogPaths("box")
	assert.Equal(t, filepath.Join(state, "logs", "cli.jsonl"), logs.CLI)
	assert.Equal(t, filepath.Join(state, "logs", "sandbox.jsonl"), logs.Sandbox)
	assert.Equal(t, filepath.Join(state, "logs", "monitor.jsonl"), logs.Monitor)
	assert.Equal(t, filepath.Join(state, "logs", "agent-hooks.jsonl"), logs.Hooks)
	assert.Equal(t, filepath.Join(state, "agent-status.json"), logs.AgentStatus)
}

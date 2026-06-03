// ABOUTME: Tests for the runtime-free per-sandbox file-exchange / cache path
// ABOUTME: verbs on *Sandbox.

package yoloai

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSandbox_ExchangePaths(t *testing.T) {
	dir := t.TempDir()
	c, err := NewWithOptions(context.Background(), Options{DataDir: dir, HomeDir: dir})
	require.NoError(t, err)
	defer c.Close() //nolint:errcheck

	state := c.layout.SandboxDir("box")
	require.NoError(t, os.MkdirAll(state, 0750))
	sb, err := c.Sandbox("box")
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(state, "files"), sb.Files().Path())
	assert.Equal(t, filepath.Join(state, "cache"), sb.CacheDir())
	assert.Equal(t, filepath.Join(state, "runtime-config.json"), sb.RuntimeConfigPath())
	assert.Equal(t, filepath.Join(state, "environment.json"), sb.EnvironmentPath())
}

func TestSandbox_LogPaths(t *testing.T) {
	dir := t.TempDir()
	c, err := NewWithOptions(context.Background(), Options{DataDir: dir, HomeDir: dir})
	require.NoError(t, err)
	defer c.Close() //nolint:errcheck

	state := c.layout.SandboxDir("box")
	require.NoError(t, os.MkdirAll(state, 0750))
	sb, err := c.Sandbox("box")
	require.NoError(t, err)

	logs := sb.LogPaths()
	assert.Equal(t, filepath.Join(state, "logs", "cli.jsonl"), logs.CLI)
	assert.Equal(t, filepath.Join(state, "logs", "sandbox.jsonl"), logs.Sandbox)
	assert.Equal(t, filepath.Join(state, "logs", "monitor.jsonl"), logs.Monitor)
	assert.Equal(t, filepath.Join(state, "logs", "agent-hooks.jsonl"), logs.Hooks)
	assert.Equal(t, filepath.Join(state, "agent-status.json"), logs.AgentStatus)
}

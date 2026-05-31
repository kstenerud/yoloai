// ABOUTME: Tests for ReadAgentLog — full read, tail-N, and missing-file no-op.
package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
)

func agentLogLayout(t *testing.T) (config.Layout, string) {
	t.Helper()
	tmp := t.TempDir()
	layout := config.NewLayout(filepath.Join(tmp, ".yoloai"))
	name := "box"
	createTestSandbox(t, tmp, name, filepath.Join(tmp, "host"), store.DirModeCopy)
	return layout, name
}

func writeAgentLog(t *testing.T, layout config.Layout, name, content string) {
	t.Helper()
	path := store.AgentLogPath(layout.SandboxDir(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))
}

func TestReadAgentLog_MissingFile(t *testing.T) {
	layout, name := agentLogLayout(t)
	out, err := ReadAgentLog(layout, name, 0)
	require.NoError(t, err)
	assert.Empty(t, out)
}

func TestReadAgentLog_Full(t *testing.T) {
	layout, name := agentLogLayout(t)
	writeAgentLog(t, layout, name, "line1\nline2\nline3\n")

	out, err := ReadAgentLog(layout, name, 0)
	require.NoError(t, err)
	assert.Equal(t, "line1\nline2\nline3\n", out)
}

func TestReadAgentLog_Tail(t *testing.T) {
	layout, name := agentLogLayout(t)
	writeAgentLog(t, layout, name, "line1\nline2\nline3\nline4\n")

	out, err := ReadAgentLog(layout, name, 2)
	require.NoError(t, err)
	assert.Equal(t, "line3\nline4", out)
}

func TestReadAgentLog_TailExceedsLength(t *testing.T) {
	layout, name := agentLogLayout(t)
	writeAgentLog(t, layout, name, "only\none\n")

	out, err := ReadAgentLog(layout, name, 10)
	require.NoError(t, err)
	assert.Equal(t, "only\none", out)
}

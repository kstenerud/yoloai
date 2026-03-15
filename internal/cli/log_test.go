package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kstenerud/yoloai/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupLogTest creates a sandbox dir and returns the name and sandbox dir path.
func setupLogTest(t *testing.T, name string) string {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, "logs"), 0750))

	meta := &sandbox.Meta{
		Name:      name,
		Agent:     "claude",
		CreatedAt: time.Now(),
		Workdir:   sandbox.WorkdirMeta{HostPath: "/tmp/test", Mode: "copy"},
	}
	require.NoError(t, sandbox.SaveMeta(sandboxDir, meta))
	return sandboxDir
}

func TestRunLog_FileExists(t *testing.T) {
	sandboxDir := setupLogTest(t, "logtest")
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, "logs", "agent.log"), []byte("hello world\n"), 0600))

	cmd := newLogAliasCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"logtest"})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, buf.String(), "hello world")
}

func TestRunLog_FileMissing(t *testing.T) {
	setupLogTest(t, "logtest-missing")

	cmd := newLogAliasCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"logtest-missing"})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, buf.String(), "No log output yet")
}

func TestRunLog_RawPreservesANSI(t *testing.T) {
	sandboxDir := setupLogTest(t, "logtest-raw")
	ansiContent := "\x1b[31mred text\x1b[0m\n"
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, "logs", "agent.log"), []byte(ansiContent), 0600))

	cmd := newLogAliasCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"logtest-raw", "--raw"})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, buf.String(), "\x1b[31m")
}

func TestRunLog_NoRawStripsANSI(t *testing.T) {
	sandboxDir := setupLogTest(t, "logtest-strip")
	ansiContent := "\x1b[31mred text\x1b[0m\n"
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, "logs", "agent.log"), []byte(ansiContent), 0600))

	cmd := newLogAliasCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"logtest-strip"})
	require.NoError(t, cmd.Execute())

	assert.NotContains(t, buf.String(), "\x1b[31m")
	assert.Contains(t, buf.String(), "red text")
}

func TestRunLog_JSONWithContent(t *testing.T) {
	sandboxDir := setupLogTest(t, "logtest-json")
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, "logs", "agent.log"), []byte("log content"), 0600))

	cmd := newLogAliasCmd()
	cmd.PersistentFlags().Bool("json", false, "")
	require.NoError(t, cmd.PersistentFlags().Set("json", "true"))
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"logtest-json"})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, buf.String(), `"content"`)
	assert.Contains(t, buf.String(), "log content")
}

func TestRunLog_JSONMissingLog(t *testing.T) {
	setupLogTest(t, "logtest-json-empty")

	cmd := newLogAliasCmd()
	cmd.PersistentFlags().Bool("json", false, "")
	require.NoError(t, cmd.PersistentFlags().Set("json", "true"))
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"logtest-json-empty"})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, buf.String(), `"content"`)
	assert.Contains(t, buf.String(), `""`)
}

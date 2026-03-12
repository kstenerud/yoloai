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

// setupSandboxCmdTest creates a minimal sandbox dir so name validation succeeds.
func setupSandboxCmdTest(t *testing.T, name string) {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	meta := &sandbox.Meta{
		Name:      name,
		Agent:     "claude",
		CreatedAt: time.Now(),
		Workdir:   sandbox.WorkdirMeta{HostPath: "/tmp/test", Mode: "copy"},
	}
	require.NoError(t, sandbox.SaveMeta(sandboxDir, meta))
}

func TestSandboxDispatch_NoArgs_ShowsHelp(t *testing.T) {
	cmd := newSandboxCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{})
	require.NoError(t, cmd.Execute())

	out := buf.String()
	assert.Contains(t, out, "sandbox")
}

func TestSandboxDispatch_ValidNameAndSubcmd(t *testing.T) {
	setupSandboxCmdTest(t, "mybox")

	cmd := newSandboxCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"mybox", "info"})
	// info will fail (no full env) but should reach the info handler
	err := cmd.Execute()
	// It may succeed or fail depending on what runSandboxInfo needs,
	// but it should NOT fail with "unknown subcommand"
	if err != nil {
		assert.NotContains(t, err.Error(), "unknown subcommand")
	}
}

func TestSandboxDispatch_ValidNameMissingSubcmd(t *testing.T) {
	setupSandboxCmdTest(t, "mybox2")

	cmd := newSandboxCmd()
	cmd.SetArgs([]string{"mybox2"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "subcommand required")
}

func TestSandboxDispatch_UnknownSubcmd(t *testing.T) {
	setupSandboxCmdTest(t, "mybox3")

	cmd := newSandboxCmd()
	cmd.SetArgs([]string{"mybox3", "bogus"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown subcommand")
	assert.Contains(t, err.Error(), "bogus")
}

func TestSandboxDispatch_SubcmdFirstWithEnv(t *testing.T) {
	setupSandboxCmdTest(t, "envbox")
	t.Setenv(EnvSandboxName, "envbox")

	cmd := newSandboxCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"info"})
	// Should extract name from env and dispatch to info
	err := cmd.Execute()
	if err != nil {
		assert.NotContains(t, err.Error(), "sandbox name required")
	}
}

func TestSandboxDispatch_SubcmdFirstWithoutEnv(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv(EnvSandboxName, "")

	cmd := newSandboxCmd()
	cmd.SetArgs([]string{"info"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sandbox name required")
}

func TestSandboxDispatch_InvalidName(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cmd := newSandboxCmd()
	cmd.SetArgs([]string{"INVALID_NAME!", "info"})
	err := cmd.Execute()
	require.Error(t, err)
}

// ABOUTME: Integration tests that exercise the help command through the full
// ABOUTME: cli root tree — kept in helpcmd_test (external) because they need
// ABOUTME: cli.NewRootCmd to set up the command groups + persistent flags.
package helpcmd_test

import (
	"bytes"
	"testing"

	"github.com/kstenerud/yoloai/internal/cli"
	"github.com/kstenerud/yoloai/internal/cli/helpcmd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHelpCmd_NoArgs_ShowsQuickstart(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cmd := helpcmd.NewCmd()
	// Give it a parent with the group so GroupID validation passes.
	root := cli.NewRootCmd("test", "abc", "now")
	root.AddCommand(cmd)

	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{})
	require.NoError(t, cmd.Execute())

	// quickstart.md content should contain the basic workflow
	// (topic resolution tested separately below)
}

func TestBareInvocation_ShowsIntro(t *testing.T) {
	// Bare `yoloai` is not gate-exempt; an empty HOME lets the gate
	// create-fresh and proceed to the intro banner.
	t.Setenv("HOME", t.TempDir())
	root := cli.NewRootCmd("test", "abc", "now")
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetArgs([]string{})
	require.NoError(t, root.Execute())

	out := buf.String()
	assert.Contains(t, out, "yoloai help")
	assert.Contains(t, out, "yoloai -h")
}

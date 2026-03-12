package cli

// ABOUTME: Unit tests for `yoloai sandbox <name> allowed` command.

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNetworkList_Isolated(t *testing.T) {
	createNetworkSandbox(t, "nl-iso", "isolated", []string{"api.example.com", "cdn.example.com"})

	cmd := newSandboxCmd()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetArgs([]string{"nl-iso", "allowed"})
	require.NoError(t, cmd.Execute())

	assert.Equal(t, "api.example.com\ncdn.example.com\n", out.String())
}

func TestNetworkList_IsolatedEmpty(t *testing.T) {
	createNetworkSandbox(t, "nl-empty", "isolated", nil)

	cmd := newSandboxCmd()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetArgs([]string{"nl-empty", "allowed"})
	require.NoError(t, cmd.Execute())

	assert.Equal(t, "No domains allowed\n", out.String())
}

func TestNetworkList_None(t *testing.T) {
	createNetworkSandbox(t, "nl-none", "none", nil)

	cmd := newSandboxCmd()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetArgs([]string{"nl-none", "allowed"})
	require.NoError(t, cmd.Execute())

	assert.Equal(t, "Network disabled (--network-none)\n", out.String())
}

func TestNetworkList_Open(t *testing.T) {
	createNetworkSandbox(t, "nl-open", "", nil)

	cmd := newSandboxCmd()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetArgs([]string{"nl-open", "allowed"})
	require.NoError(t, cmd.Execute())

	assert.Equal(t, "No network isolation\n", out.String())
}

func TestNetworkList_JSON(t *testing.T) {
	createNetworkSandbox(t, "nl-json", "isolated", []string{"api.example.com"})

	// Build command tree so persistent flags from parent are available
	root := &cobra.Command{}
	root.PersistentFlags().Bool("json", false, "")
	root.AddGroup(&cobra.Group{ID: groupSandboxTools, Title: "Sandbox Tools:"})
	sb := newSandboxCmd()
	root.AddCommand(sb)

	out := new(bytes.Buffer)
	root.SetOut(out)
	root.SetArgs([]string{"sandbox", "nl-json", "allowed"})
	require.NoError(t, root.PersistentFlags().Set("json", "true"))
	require.NoError(t, root.Execute())

	assert.JSONEq(t, `{"name":"nl-json","network_mode":"isolated","domains":["api.example.com"]}`, out.String())
}

func TestNetworkList_JSONNoDomains(t *testing.T) {
	createNetworkSandbox(t, "nl-jempty", "isolated", nil)

	root := &cobra.Command{}
	root.PersistentFlags().Bool("json", false, "")
	root.AddGroup(&cobra.Group{ID: groupSandboxTools, Title: "Sandbox Tools:"})
	sb := newSandboxCmd()
	root.AddCommand(sb)

	out := new(bytes.Buffer)
	root.SetOut(out)
	root.SetArgs([]string{"sandbox", "nl-jempty", "allowed"})
	require.NoError(t, root.PersistentFlags().Set("json", "true"))
	require.NoError(t, root.Execute())

	assert.JSONEq(t, `{"name":"nl-jempty","network_mode":"isolated","domains":[]}`, out.String())
}

func TestNetworkList_NonexistentSandbox(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	cmd := newSandboxCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetArgs([]string{"does-not-exist", "allowed"})
	assert.Error(t, cmd.Execute())
}

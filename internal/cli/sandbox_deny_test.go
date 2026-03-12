package cli

// ABOUTME: Unit tests for `yoloai sandbox <name> deny` command.
// ABOUTME: Tests domain validation, filtering, and error cases.

import (
	"bytes"
	"testing"

	"github.com/kstenerud/yoloai/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNetworkRemove_SingleDomain(t *testing.T) {
	sandboxDir := createNetworkSandbox(t, "nr-single", "isolated", []string{"keep.com", "drop.com"})

	cmd := newSandboxCmd()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetArgs([]string{"nr-single", "deny", "drop.com"})
	require.NoError(t, cmd.Execute())

	meta, err := sandbox.LoadMeta(sandboxDir)
	require.NoError(t, err)
	assert.Equal(t, []string{"keep.com"}, meta.NetworkAllow)
	assert.Contains(t, out.String(), "drop.com")
	assert.Contains(t, out.String(), "will take effect on next start")
}

func TestNetworkRemove_MultipleDomains(t *testing.T) {
	sandboxDir := createNetworkSandbox(t, "nr-multi", "isolated", []string{"a.com", "b.com", "c.com", "d.com"})

	cmd := newSandboxCmd()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetArgs([]string{"nr-multi", "deny", "b.com", "d.com"})
	require.NoError(t, cmd.Execute())

	meta, err := sandbox.LoadMeta(sandboxDir)
	require.NoError(t, err)
	assert.Equal(t, []string{"a.com", "c.com"}, meta.NetworkAllow)
}

func TestNetworkRemove_AllDomains(t *testing.T) {
	sandboxDir := createNetworkSandbox(t, "nr-all", "isolated", []string{"only.com"})

	cmd := newSandboxCmd()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetArgs([]string{"nr-all", "deny", "only.com"})
	require.NoError(t, cmd.Execute())

	meta, err := sandbox.LoadMeta(sandboxDir)
	require.NoError(t, err)
	assert.Nil(t, meta.NetworkAllow)
}

func TestNetworkRemove_DomainNotInList(t *testing.T) {
	createNetworkSandbox(t, "nr-missing", "isolated", []string{"exists.com"})

	cmd := newSandboxCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetArgs([]string{"nr-missing", "deny", "nope.com"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nope.com")
	assert.Contains(t, err.Error(), "not in the allowlist")
}

func TestNetworkRemove_NoDomainArg(t *testing.T) {
	createNetworkSandbox(t, "nr-noarg", "isolated", []string{"x.com"})

	cmd := newSandboxCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetArgs([]string{"nr-noarg", "deny"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one domain")
}

func TestNetworkRemove_NotIsolated(t *testing.T) {
	createNetworkSandbox(t, "nr-open", "", nil)

	cmd := newSandboxCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetArgs([]string{"nr-open", "deny", "x.com"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not using network isolation")
}

func TestNetworkRemove_NetworkNone(t *testing.T) {
	createNetworkSandbox(t, "nr-none", "none", nil)

	cmd := newSandboxCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetArgs([]string{"nr-none", "deny", "x.com"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--network-none")
}

func TestNetworkRemove_PreservesOrder(t *testing.T) {
	sandboxDir := createNetworkSandbox(t, "nr-order", "isolated", []string{"first.com", "middle.com", "last.com"})

	cmd := newSandboxCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetArgs([]string{"nr-order", "deny", "middle.com"})
	require.NoError(t, cmd.Execute())

	meta, err := sandbox.LoadMeta(sandboxDir)
	require.NoError(t, err)
	assert.Equal(t, []string{"first.com", "last.com"}, meta.NetworkAllow)
}

package cli

// ABOUTME: Unit tests for `yoloai sandbox network remove` command.
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

	cmd := newSandboxNetworkRemoveCmd()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetArgs([]string{"nr-single", "drop.com"})
	require.NoError(t, cmd.Execute())

	meta, err := sandbox.LoadMeta(sandboxDir)
	require.NoError(t, err)
	assert.Equal(t, []string{"keep.com"}, meta.NetworkAllow)
	assert.Contains(t, out.String(), "drop.com")
	assert.Contains(t, out.String(), "will take effect on next start")
}

func TestNetworkRemove_MultipleDomains(t *testing.T) {
	sandboxDir := createNetworkSandbox(t, "nr-multi", "isolated", []string{"a.com", "b.com", "c.com", "d.com"})

	cmd := newSandboxNetworkRemoveCmd()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetArgs([]string{"nr-multi", "b.com", "d.com"})
	require.NoError(t, cmd.Execute())

	meta, err := sandbox.LoadMeta(sandboxDir)
	require.NoError(t, err)
	assert.Equal(t, []string{"a.com", "c.com"}, meta.NetworkAllow)
}

func TestNetworkRemove_AllDomains(t *testing.T) {
	sandboxDir := createNetworkSandbox(t, "nr-all", "isolated", []string{"only.com"})

	cmd := newSandboxNetworkRemoveCmd()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetArgs([]string{"nr-all", "only.com"})
	require.NoError(t, cmd.Execute())

	meta, err := sandbox.LoadMeta(sandboxDir)
	require.NoError(t, err)
	assert.Nil(t, meta.NetworkAllow)
}

func TestNetworkRemove_DomainNotInList(t *testing.T) {
	createNetworkSandbox(t, "nr-missing", "isolated", []string{"exists.com"})

	cmd := newSandboxNetworkRemoveCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetArgs([]string{"nr-missing", "nope.com"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nope.com")
	assert.Contains(t, err.Error(), "not in the allowlist")
}

func TestNetworkRemove_NoDomainArg(t *testing.T) {
	createNetworkSandbox(t, "nr-noarg", "isolated", []string{"x.com"})

	cmd := newSandboxNetworkRemoveCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetArgs([]string{"nr-noarg"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one domain")
}

func TestNetworkRemove_NotIsolated(t *testing.T) {
	createNetworkSandbox(t, "nr-open", "", nil)

	cmd := newSandboxNetworkRemoveCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetArgs([]string{"nr-open", "x.com"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not using network isolation")
}

func TestNetworkRemove_NetworkNone(t *testing.T) {
	createNetworkSandbox(t, "nr-none", "none", nil)

	cmd := newSandboxNetworkRemoveCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetArgs([]string{"nr-none", "x.com"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--network-none")
}

func TestNetworkRemove_PreservesOrder(t *testing.T) {
	sandboxDir := createNetworkSandbox(t, "nr-order", "isolated", []string{"first.com", "middle.com", "last.com"})

	cmd := newSandboxNetworkRemoveCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetArgs([]string{"nr-order", "middle.com"})
	require.NoError(t, cmd.Execute())

	meta, err := sandbox.LoadMeta(sandboxDir)
	require.NoError(t, err)
	assert.Equal(t, []string{"first.com", "last.com"}, meta.NetworkAllow)
}

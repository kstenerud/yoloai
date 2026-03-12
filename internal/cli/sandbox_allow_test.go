package cli

// ABOUTME: Unit tests for `yoloai sandbox <name> allow` command.
// ABOUTME: Tests domain deduplication, persistence, and error cases.

import (
	"bytes"
	"testing"

	"github.com/kstenerud/yoloai/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNetworkAdd_NewDomains(t *testing.T) {
	sandboxDir := createNetworkSandbox(t, "na-new", "isolated", []string{"existing.com"})

	cmd := newSandboxCmd()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetArgs([]string{"na-new", "allow", "added.com"})
	require.NoError(t, cmd.Execute())

	// Verify persisted
	meta, err := sandbox.LoadMeta(sandboxDir)
	require.NoError(t, err)
	assert.Equal(t, []string{"existing.com", "added.com"}, meta.NetworkAllow)

	// Output says "will take effect on next start" since Docker isn't running
	assert.Contains(t, out.String(), "added.com")
	assert.Contains(t, out.String(), "will take effect on next start")
}

func TestNetworkAdd_DeduplicateExisting(t *testing.T) {
	createNetworkSandbox(t, "na-dedup", "isolated", []string{"already.com"})

	cmd := newSandboxCmd()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetArgs([]string{"na-dedup", "allow", "already.com"})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, out.String(), "All domains already allowed")
}

func TestNetworkAdd_DeduplicateWithinInput(t *testing.T) {
	sandboxDir := createNetworkSandbox(t, "na-inputdup", "isolated", nil)

	cmd := newSandboxCmd()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetArgs([]string{"na-inputdup", "allow", "dup.com", "dup.com", "unique.com"})
	require.NoError(t, cmd.Execute())

	meta, err := sandbox.LoadMeta(sandboxDir)
	require.NoError(t, err)
	assert.Equal(t, []string{"dup.com", "unique.com"}, meta.NetworkAllow)
}

func TestNetworkAdd_MultipleDomains(t *testing.T) {
	sandboxDir := createNetworkSandbox(t, "na-multi", "isolated", []string{"keep.com"})

	cmd := newSandboxCmd()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetArgs([]string{"na-multi", "allow", "a.com", "b.com", "c.com"})
	require.NoError(t, cmd.Execute())

	meta, err := sandbox.LoadMeta(sandboxDir)
	require.NoError(t, err)
	assert.Equal(t, []string{"keep.com", "a.com", "b.com", "c.com"}, meta.NetworkAllow)
}

func TestNetworkAdd_NoDomainArg(t *testing.T) {
	createNetworkSandbox(t, "na-nodom", "isolated", nil)

	cmd := newSandboxCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetArgs([]string{"na-nodom", "allow"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one domain")
}

func TestNetworkAdd_NotIsolated(t *testing.T) {
	createNetworkSandbox(t, "na-open", "", nil)

	cmd := newSandboxCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetArgs([]string{"na-open", "allow", "x.com"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not using network isolation")
}

func TestNetworkAdd_NetworkNone(t *testing.T) {
	createNetworkSandbox(t, "na-none", "none", nil)

	cmd := newSandboxCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetArgs([]string{"na-none", "allow", "x.com"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--network-none")
}

// ABOUTME: Tests for the pure, VM-free pieces of the Tart mount/setup subsystem —
// ABOUTME: the guest-path mapping and the in-guest command construction.
package tart

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
)

// TestResolveMountVFSPath_MapsTierPathsToTheirShare covers the translation the
// tiering changed. A mount source under the sandbox dir is now tiered, so the
// path relative to the sandbox dir starts with the tier name — which is also the
// share name — and the guest path is that relative path under the shares root.
//
// The failure this guards is silent: mapping onto the read-write share instead
// of the shares root yields ".../rw/rw/logs" (or ".../rw/ro/bin"), a path that
// exists nowhere. Nothing errors; the guest just cannot find its logs.
func TestResolveMountVFSPath_MapsTierPathsToTheirShare(t *testing.T) {
	sandboxPath := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxPath, "rw", "logs"), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxPath, "ro", "bin"), 0o750))

	got, ok := resolveMountVFSPath(filepath.Join(sandboxPath, "rw", "logs"), sandboxPath)
	require.True(t, ok)
	assert.Equal(t, "/Volumes/My Shared Files/rw/logs", got)

	got, ok = resolveMountVFSPath(filepath.Join(sandboxPath, "ro", "bin"), sandboxPath)
	require.True(t, ok)
	assert.Equal(t, "/Volumes/My Shared Files/ro/bin", got)
}

// TestResolveMountVFSPath_SandboxRootIsTheFlatView pins what the guest gets for
// the sandbox dir itself: one flat root, not the shares root, which holds the
// tiers side by side and is not a sandbox layout any guest script understands.
func TestResolveMountVFSPath_SandboxRootIsTheFlatView(t *testing.T) {
	sandboxPath := t.TempDir()

	got, ok := resolveMountVFSPath(sandboxPath, sandboxPath)

	require.True(t, ok)
	assert.Equal(t, "/Volumes/My Shared Files/rw", got)
}

// TestResolveMountVFSPath_RefusesTheHostTier keeps the mapping honest about a
// path it must never produce: host/ has no share, so any guest path for it
// would dangle. Nothing should ask — no MountSpec names a host-tier path — and
// this makes the day one does a refusal rather than a broken symlink.
func TestResolveMountVFSPath_RefusesTheHostTier(t *testing.T) {
	sandboxPath := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxPath, "host"), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(sandboxPath, "host", "environment.json"), []byte("{}"), 0o600))

	_, ok := resolveMountVFSPath(filepath.Join(sandboxPath, "host", "environment.json"), sandboxPath)

	assert.False(t, ok, "the host tier has no share and must not resolve to a guest path")
}

// TestSetupScriptCommand_RunsFromTheFlatView pins the single root the guest
// scripts join every one of their paths from.
func TestSetupScriptCommand_RunsFromTheFlatView(t *testing.T) {
	cmd := setupScriptCommand(vmGuestViewDir())

	// bin/ resolves through the view into the read-only tier...
	assert.Contains(t, cmd, "'/Volumes/My Shared Files/rw/bin/sandbox-setup.py'")
	// ...the backend argument is the same flat root, not a tier...
	assert.Contains(t, cmd, "tart '/Volumes/My Shared Files/rw'")
	// ...and setup.log lands where the guest is allowed to write.
	assert.Contains(t, cmd, ">'/Volumes/My Shared Files/rw/setup.log'")
	assert.NotContains(t, cmd, "/Volumes/My Shared Files/ro/",
		"nothing the guest writes may be addressed through the read-only share")
}

func TestHostnameSetCommand_SetsAllThreeMacOSNames(t *testing.T) {
	cmd := hostnameSetCommand("my-sandbox")
	// All three macOS hostname facets must be set: HostName (what `hostname` and
	// shells read), LocalHostName (Bonjour/.local), and ComputerName (UI label).
	assert.Contains(t, cmd, "scutil --set HostName 'my-sandbox'")
	assert.Contains(t, cmd, "scutil --set LocalHostName 'my-sandbox'")
	assert.Contains(t, cmd, "scutil --set ComputerName 'my-sandbox'")
	// Chained with && so a failure short-circuits and runTart surfaces it.
	assert.Equal(t, 2, strings.Count(cmd, "&&"))
}

func TestHostnameSetCommand_AcceptsSanitizedLabel(t *testing.T) {
	// The orchestrator feeds a config.SanitizeHostname'd value, which contains no
	// shell metacharacters, so single-quoting is sufficient and the label is a
	// valid LocalHostName (a DNS label). Guard that assumption end to end.
	label := config.SanitizeHostname("My_Feature.Branch")
	assert.Equal(t, "my-feature-branch", label)
	cmd := hostnameSetCommand(label)
	assert.NotContains(t, label, "'")
	assert.Contains(t, cmd, "'my-feature-branch'")
}

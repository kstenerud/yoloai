// ABOUTME: Covers the flat guest view assembled over the tiers — what it
// ABOUTME: surfaces, what it refuses to clobber, and what it cleans up.
package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeROEntry puts a file in the read-only tier of a fresh sandbox dir.
func writeROEntry(t *testing.T, sandboxDir, name, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(ReadOnlyTierDir(sandboxDir), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(ReadOnlyTierDir(sandboxDir), name), []byte(content), 0o600))
}

// TestAssembleGuestView_SurfacesReadOnlyEntriesFlat is the property the guest
// depends on: one root, holding both tiers' entries, with the tier segment
// invisible. The in-sandbox scripts join from a single directory, so a
// read-only entry reachable only at ro/<name> is a file they cannot find.
func TestAssembleGuestView_SurfacesReadOnlyEntriesFlat(t *testing.T) {
	sandboxDir := t.TempDir()
	writeROEntry(t, sandboxDir, "runtime-config.json", `{"real":true}`)
	require.NoError(t, os.MkdirAll(filepath.Join(ReadOnlyTierDir(sandboxDir), "bin"), 0o750))

	require.NoError(t, AssembleGuestView(sandboxDir))

	view := GuestViewDir(sandboxDir)
	got, err := os.ReadFile(filepath.Join(view, "runtime-config.json")) //nolint:gosec // G304: test code
	require.NoError(t, err)
	assert.Equal(t, `{"real":true}`, string(got), "the view must resolve to the read-only original")
	// os.Stat, not assert.DirExists: the latter Lstats, so it would see the
	// link rather than what the guest reaches by following it.
	info, err := os.Stat(filepath.Join(view, "bin"))
	require.NoError(t, err)
	assert.True(t, info.IsDir(), "directory entries are surfaced too")
}

// TestAssembleGuestView_LinksAreRelative pins the link *body*, not just that it
// resolves. An absolute target resolves fine on the host and breaks in a tart
// guest, where the tiers are two VirtioFS shares under a different root — the
// failure would be invisible to every host-side test.
func TestAssembleGuestView_LinksAreRelative(t *testing.T) {
	sandboxDir := t.TempDir()
	writeROEntry(t, sandboxDir, "prompt.txt", "hi")

	require.NoError(t, AssembleGuestView(sandboxDir))

	target, err := os.Readlink(filepath.Join(GuestViewDir(sandboxDir), "prompt.txt"))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("..", "ro", "prompt.txt"), target)
}

// TestAssembleGuestView_IsIdempotent covers the launch path, which re-runs it
// every start.
func TestAssembleGuestView_IsIdempotent(t *testing.T) {
	sandboxDir := t.TempDir()
	writeROEntry(t, sandboxDir, "machine-id", "abc")

	require.NoError(t, AssembleGuestView(sandboxDir))
	require.NoError(t, AssembleGuestView(sandboxDir))

	entries, err := os.ReadDir(GuestViewDir(sandboxDir))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "machine-id", entries[0].Name())
}

// TestAssembleGuestView_KeepsReadWriteContent guards the direction that would
// destroy user data: the view lives *in* the read-write tier, so assembling it
// must not disturb what the guest has written there.
func TestAssembleGuestView_KeepsReadWriteContent(t *testing.T) {
	sandboxDir := t.TempDir()
	writeROEntry(t, sandboxDir, "prompt.txt", "hi")
	require.NoError(t, os.MkdirAll(filepath.Join(ReadWriteTierDir(sandboxDir), "logs"), 0o750))
	agentLog := filepath.Join(ReadWriteTierDir(sandboxDir), "logs", "agent.log")
	require.NoError(t, os.WriteFile(agentLog, []byte("work"), 0o600))

	require.NoError(t, AssembleGuestView(sandboxDir))

	got, err := os.ReadFile(agentLog) //nolint:gosec // G304: test code
	require.NoError(t, err)
	assert.Equal(t, "work", string(got))
}

// TestAssembleGuestView_LeavesAGuestFileInPlace covers the guest having
// replaced its own view entry. Removing it would delete guest-owned data on the
// next launch; the guest has only cost itself the read-only original.
func TestAssembleGuestView_LeavesAGuestFileInPlace(t *testing.T) {
	sandboxDir := t.TempDir()
	writeROEntry(t, sandboxDir, "runtime-config.json", `{"real":true}`)
	require.NoError(t, os.MkdirAll(GuestViewDir(sandboxDir), 0o750))
	shadow := filepath.Join(GuestViewDir(sandboxDir), "runtime-config.json")
	require.NoError(t, os.WriteFile(shadow, []byte(`{"forged":true}`), 0o600))

	require.NoError(t, AssembleGuestView(sandboxDir))

	got, err := os.ReadFile(shadow) //nolint:gosec // G304: test code
	require.NoError(t, err)
	assert.Equal(t, `{"forged":true}`, string(got), "a guest-owned file is reported, not deleted")

	// And the host is unaffected, which is the half that matters: it addresses
	// the tier directly and never reads through the view.
	original, err := os.ReadFile(RuntimeConfigPath(sandboxDir))
	require.NoError(t, err)
	assert.Equal(t, `{"real":true}`, string(original))
}

// TestAssembleGuestView_PrunesLinksWhoseOriginalIsGone covers secrets/, which
// is staged for one launch and deleted once the guest has consumed it.
func TestAssembleGuestView_PrunesLinksWhoseOriginalIsGone(t *testing.T) {
	sandboxDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(ReadOnlyTierDir(sandboxDir), "secrets"), 0o750))
	require.NoError(t, AssembleGuestView(sandboxDir))
	require.NoError(t, func() error { _, err := os.Lstat(filepath.Join(GuestViewDir(sandboxDir), "secrets")); return err }())

	require.NoError(t, os.RemoveAll(filepath.Join(ReadOnlyTierDir(sandboxDir), "secrets")))
	require.NoError(t, AssembleGuestView(sandboxDir))

	_, err := os.Lstat(filepath.Join(GuestViewDir(sandboxDir), "secrets"))
	assert.True(t, os.IsNotExist(err), "a link to a removed read-only entry is pruned")
}

// TestAssembleGuestView_KeepsAGuestOwnedSymlink bounds the pruning: only links
// of this package's own shape are removed.
func TestAssembleGuestView_KeepsAGuestOwnedSymlink(t *testing.T) {
	sandboxDir := t.TempDir()
	require.NoError(t, os.MkdirAll(GuestViewDir(sandboxDir), 0o750))
	guestLink := filepath.Join(GuestViewDir(sandboxDir), "scratch")
	require.NoError(t, os.Symlink("/tmp/elsewhere", guestLink))

	require.NoError(t, AssembleGuestView(sandboxDir))

	target, err := os.Readlink(guestLink)
	require.NoError(t, err)
	assert.Equal(t, "/tmp/elsewhere", target)
}

// TestAssembleGuestView_CreatesTiers covers a bare runtime instance, which has
// no sandbox layer to have created the directories. tart shares both tiers on
// the `tart run` command line, and a --dir naming a path that does not exist
// fails the boot outright.
func TestAssembleGuestView_CreatesTiers(t *testing.T) {
	sandboxDir := t.TempDir()

	require.NoError(t, AssembleGuestView(sandboxDir))

	assert.DirExists(t, ReadOnlyTierDir(sandboxDir))
	assert.DirExists(t, ReadWriteTierDir(sandboxDir))
}

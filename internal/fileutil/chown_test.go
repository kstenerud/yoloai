// ABOUTME: Tests the no-op-when-not-sudo contract of the chown helpers that
// ABOUTME: callers rely on to invoke them unconditionally on every code path.
package fileutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// When not running under sudo, the chown helpers must short-circuit to nil
// before touching the filesystem. Callers invoke them unconditionally on the
// common (non-sudo) path, so a missing or partially-built tree must not error.
// This guards against a refactor that walks first and checks sudo second.
func TestChownHelpers_NoOpWhenNotSudo(t *testing.T) {
	if SudoUID() != -1 {
		t.Skip("running under sudo; this test pins the non-sudo no-op path")
	}

	require.NoError(t, ChownIfSudo(filepath.Join(t.TempDir(), "does-not-exist")))
	require.NoError(t, ChownRecursiveIfSudo(filepath.Join(t.TempDir(), "does-not-exist")))

	// A populated tree (dir + file + symlink) must also be left untouched.
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sub"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(root, "sub", "f"), []byte("x"), 0o600))
	require.NoError(t, os.Symlink("f", filepath.Join(root, "sub", "link")))
	require.NoError(t, ChownRecursiveIfSudo(root))
}

// MkdirAll must fix ownership of every level it creates, not just the leaf, so
// missingAncestors has to name each directory the create will conjure. The
// chown itself needs root and cannot run here; this pins the half that decides
// what gets chowned, which is the half that was wrong.
//
// The bug it guards: sandbox tiering made `rw/` and `ro/` intermediates of
// deeper MkdirAll calls rather than explicit creates. Under sudo they stayed
// root-owned, and a rootless podman daemon — running as the invoking user —
// could not traverse them, so it reported every bind source underneath as
// missing and failed trying to mkdir it. Seven smoke tiers, podman only (DF186).
func TestMissingAncestors_NamesEveryLevelTheCreateWillConjure(t *testing.T) {
	top := t.TempDir() // exists already, so it must never be named

	deep := filepath.Join(top, "sandbox", "rw", "logs")
	require.Equal(t, []string{
		filepath.Join(top, "sandbox"),
		filepath.Join(top, "sandbox", "rw"),
	}, missingAncestors(deep), "both conjured levels, outermost first, and not the leaf")

	// An existing parent is never named — chowning it would re-own a directory
	// this call did not create.
	require.Empty(t, missingAncestors(filepath.Join(top, "leaf")),
		"nothing to chown above a parent that already exists")

	// The walk stops at the first existing ancestor rather than running to /.
	require.NotContains(t, missingAncestors(deep), filepath.Dir(top),
		"an existing ancestor and everything above it must be left alone")
}

// And the levels named are exactly the ones that appear on disk afterwards —
// the property the chown loop depends on.
func TestMissingAncestors_MatchesWhatMkdirAllCreates(t *testing.T) {
	top := t.TempDir()
	target := filepath.Join(top, "a", "b", "c")

	named := missingAncestors(target)
	require.NoError(t, MkdirAll(target, 0o750))

	for _, dir := range named {
		st, err := os.Stat(dir)
		require.NoError(t, err, "missingAncestors named %s, which MkdirAll did not create", dir)
		require.True(t, st.IsDir())
	}
	require.Len(t, named, 2, "a/ and a/b/, with c/ being the leaf the caller chowns")
}

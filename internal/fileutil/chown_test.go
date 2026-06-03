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

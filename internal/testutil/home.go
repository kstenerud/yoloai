// ABOUTME: IsolatedHome sets HOME to a temp dir for the test, preventing config
// ABOUTME: reads from the real user's home directory during unit tests.
package testutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// IsolatedHome sets HOME to a new temp directory for the duration of the test.
// Returns the temp directory path.
func IsolatedHome(t *testing.T) string {
	t.Helper()
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	return tmpHome
}

// CLIConfigDir creates the ~/.yoloai/defaults/ directory under an isolated
// HOME and returns its absolute path. Used by CLI config tests that need
// a writable defaults dir without touching the real user home.
func CLIConfigDir(t *testing.T) string {
	t.Helper()
	tmpHome := IsolatedHome(t)
	dir := filepath.Join(tmpHome, ".yoloai", "defaults")
	require.NoError(t, os.MkdirAll(dir, 0750))
	return dir
}

package testutil

import "testing"

// IsolatedHome sets HOME to a new temp directory for the duration of the test.
// Returns the temp directory path.
func IsolatedHome(t *testing.T) string {
	t.Helper()
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	return tmpHome
}

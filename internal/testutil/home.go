// ABOUTME: IsolatedHome sets HOME to a temp dir for the test, preventing config
// ABOUTME: reads from the real user's home directory during unit tests.
package testutil

import (
	"testing"
)

// IsolatedHome sets HOME to a new temp directory for the duration of the test.
// Returns the temp directory path.
//
// It also clears SUDO_USER, because under `sudo` the CLI's resolveHome()
// (internal/cli/cliutil/layout.go) deliberately ignores $HOME and looks the
// invoking user's home up in /etc/passwd via SUDO_USER — correct for real
// `sudo yoloai` (it should touch the user's ~/.yoloai, not /root), but it would
// silently override this HOME override and route the test back to the real home.
// Clearing SUDO_USER makes the $HOME override authoritative so the test is
// hermetic whether or not the suite itself was launched under sudo (e.g. the
// root-requiring VM-backend integration runs). SUDO_UID/SUDO_GID are left intact
// on purpose: they feed the container uid-remap (fileutil.HostUID), not home
// resolution, and a container-creating test under sudo wants the real user's uid.
func IsolatedHome(t *testing.T) string {
	t.Helper()
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("SUDO_USER", "")
	return tmpHome
}

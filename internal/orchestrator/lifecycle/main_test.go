// ABOUTME: Package-wide test setup: disable the real macOS Keychain so lifecycle
// ABOUTME: tests (restart/reset re-seed credentials) are hermetic on a logged-in Mac.
package lifecycle

import (
	"errors"
	"os"
	"testing"

	"github.com/kstenerud/yoloai/internal/envsetup"
)

// TestMain defaults the Keychain reader to a no-op for the whole package. Restart
// and reset re-seed the agent's credential files via envsetup.CopySeedFiles, which
// on macOS materializes credentials from the Keychain. On a developer Mac logged
// in to an agent that would seed real .credentials.json files into the test's
// sandbox dir, making assertions about the re-seeded state host-dependent.
// Disabling the reader makes darwin match Linux (where it is already a no-op).
func TestMain(m *testing.M) {
	envsetup.KeychainReader = func(string) ([]byte, error) {
		return nil, errors.New("keychain disabled in tests")
	}
	os.Exit(m.Run())
}

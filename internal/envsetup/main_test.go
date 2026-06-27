// ABOUTME: Package-wide test setup: disable the real macOS Keychain so auth-
// ABOUTME: detection tests are hermetic on a developer Mac logged in to an agent.
package envsetup

import (
	"errors"
	"os"
	"testing"
)

// TestMain defaults KeychainReader to a no-op for the whole package. On a
// developer Mac that is logged in to an agent (e.g. Claude), the real Keychain
// reader would return live credentials, so a test asserting "no auth present"
// would spuriously see auth and fail. On Linux KeychainReader is already a no-op
// (keychain_other.go); this just makes darwin match. Tests that exercise the
// Keychain path override KeychainReader locally and restore it.
func TestMain(m *testing.M) {
	KeychainReader = func(string) ([]byte, error) {
		return nil, errors.New("keychain disabled in tests")
	}
	os.Exit(m.Run())
}

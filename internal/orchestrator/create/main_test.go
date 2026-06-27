// ABOUTME: Package-wide test setup: disable the real macOS Keychain so the
// ABOUTME: create pipeline's auth-gate tests are hermetic on a logged-in Mac.
package create

import (
	"errors"
	"os"
	"testing"

	"github.com/kstenerud/yoloai/internal/envsetup"
)

// TestMain defaults the Keychain reader to a no-op for the whole package. The
// create pipeline's auth gate (checkAgentAuth / agentHasUsableAuth) consults
// envsetup.HasAnyAuthFile, which on macOS reads the Keychain. On a developer Mac
// logged in to an agent that would report auth present, so tests expecting
// "missing auth" (e.g. TestPrepareSandboxState_MissingAPIKey, TestAgentHasUsableAuth)
// would take the wrong branch and fail. Disabling the reader makes darwin match
// Linux (where it is already a no-op). Keychain-path tests override it locally.
func TestMain(m *testing.M) {
	envsetup.KeychainReader = func(string) ([]byte, error) {
		return nil, errors.New("keychain disabled in tests")
	}
	os.Exit(m.Run())
}

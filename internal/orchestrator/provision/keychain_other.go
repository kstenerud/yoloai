// ABOUTME: Stub keychain reader for non-macOS platforms (always returns error).

//go:build !darwin

package provision

import "fmt"

// KeychainReader is a no-op on non-darwin platforms.
var KeychainReader = readKeychainPassword

func readKeychainPassword(_ string) ([]byte, error) {
	return nil, fmt.Errorf("keychain not available on this platform")
}

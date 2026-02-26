// ABOUTME: Stub keychain reader for non-macOS platforms (always returns error).

//go:build !darwin

package sandbox

import "fmt"

// keychainReader is a no-op on non-darwin platforms.
var keychainReader = readKeychainPassword

func readKeychainPassword(service string) ([]byte, error) {
	return nil, fmt.Errorf("keychain not available on this platform")
}

// ABOUTME: macOS Keychain reader for credential fallback when host file is missing.

//go:build darwin

package sandbox

import (
	"fmt"
	"os/exec"
	"strings"
)

// keychainReader reads a generic password from the macOS Keychain by service name.
// Overridden in tests to avoid real Keychain calls.
var keychainReader = readKeychainPassword

func readKeychainPassword(service string) ([]byte, error) {
	out, err := exec.Command("security", "find-generic-password", "-s", service, "-w").Output() //nolint:gosec // service name comes from agent definition, not user input
	if err != nil {
		return nil, fmt.Errorf("keychain lookup for %q: %w", service, err)
	}
	return []byte(strings.TrimRight(string(out), "\n")), nil
}

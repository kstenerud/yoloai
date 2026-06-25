// ABOUTME: macOS Keychain reader for credential fallback when host file is missing.

//go:build darwin

package envsetup

import (
	"fmt"
	"strings"

	"github.com/kstenerud/yoloai/internal/sysexec"
)

// KeychainReader reads a generic password from the macOS Keychain by service name.
// Overridden in tests to avoid real Keychain calls.
var KeychainReader = readKeychainPassword

// keychainEnv is the minimal env for the `security` subprocess. macOS's
// security(1) lives at /usr/bin and needs only PATH; no layout is available at
// this call site (provision is layout-free), so we curate a hardcoded minimum.
var keychainEnv = []string{"PATH=/usr/bin:/bin:/usr/local/bin"}

func readKeychainPassword(service string) ([]byte, error) {
	out, err := sysexec.Command(keychainEnv, "security", "find-generic-password", "-s", service, "-w").Output()
	if err != nil {
		return nil, fmt.Errorf("keychain lookup for %q: %w", service, err)
	}
	return []byte(strings.TrimRight(string(out), "\n")), nil
}

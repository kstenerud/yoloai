// ABOUTME: Unit test for the DF145 fix in readKeychainPassword: a failed
// ABOUTME: `security` lookup must surface the tool's own stderr diagnostic,
// ABOUTME: not a bare "exit status 44".

//go:build darwin

package envsetup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadKeychainPassword_ErrorCarriesSecurityStderr(t *testing.T) {
	const cause = "security: SecKeychainSearchCopyNext: The specified item could not be found in the keychain."
	dir := t.TempDir()
	script := "#!/bin/sh\necho '" + cause + "' >&2\nexit 44\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "security"), []byte(script), 0700)) //nolint:gosec // test fixture needs exec bit

	origEnv := keychainEnv
	keychainEnv = []string{"PATH=" + dir}
	t.Cleanup(func() { keychainEnv = origEnv })

	_, err := readKeychainPassword("yoloai-test-service")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `keychain lookup for "yoloai-test-service"`)
	assert.Contains(t, err.Error(), cause,
		"security(1)'s own diagnostic must ride on the error (DF145); .Output() captured it")
}

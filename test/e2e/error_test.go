//go:build e2e

package e2e_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestE2E_ExitCodeDestroyNonExistent verifies exit code 1 when destroying a
// sandbox that does not exist.
func TestE2E_ExitCodeDestroyNonExistent(t *testing.T) {
	_ = e2eSetup(t)

	_, _, code := runYoloai(t, "destroy", "--yes", "nonexistent-sandbox-xyz")
	assert.Equal(t, 1, code, "destroy of nonexistent sandbox should exit 1")
}

// TestE2E_ExitCodeDuplicateNew verifies exit code 1 when creating a duplicate sandbox.
func TestE2E_ExitCodeDuplicateNew(t *testing.T) {
	projectDir := e2eSetup(t)

	_, _, code := runYoloai(t, "new", "--agent", "test", "--no-start", "e2e-exitdup", projectDir)
	require.Equal(t, 0, code)
	t.Cleanup(func() { destroySandbox(t, "e2e-exitdup") })

	_, _, code = runYoloai(t, "new", "--agent", "test", "--no-start", "e2e-exitdup", projectDir)
	assert.Equal(t, 1, code, "duplicate new should exit 1")
}

// TestE2E_ErrorMessageDestroyNonExistent verifies that destroying a nonexistent
// sandbox produces a human-readable error message on stderr.
func TestE2E_ErrorMessageDestroyNonExistent(t *testing.T) {
	_ = e2eSetup(t)

	_, stderr, _ := runYoloai(t, "destroy", "--yes", "no-such-sandbox")
	assert.Contains(t, stderr, "no-such-sandbox", "error message should mention the sandbox name")
}

// TestE2E_HelpExitZero verifies that --help exits with code 0.
func TestE2E_HelpExitZero(t *testing.T) {
	_, _, code := runYoloai(t, "--help")
	assert.Equal(t, 0, code)
}

// TestE2E_VersionExitZero verifies that --version exits with code 0.
func TestE2E_VersionExitZero(t *testing.T) {
	stdout, _, code := runYoloai(t, "--version")
	assert.Equal(t, 0, code)
	assert.NotEmpty(t, stdout)
}

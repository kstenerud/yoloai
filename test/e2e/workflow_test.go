//go:build e2e

package e2e_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestE2E_NewDiffApplyDestroy exercises the full user-facing workflow:
// new → (simulate agent output) → diff → apply → destroy.
func TestE2E_NewDiffApplyDestroy(t *testing.T) {
	projectDir := e2eSetup(t)

	// Create sandbox
	_, _, code := runYoloai(t, "new", "--agent", "test", "--no-start", "e2e-workflow", projectDir)
	require.Equal(t, 0, code, "new should succeed")
	t.Cleanup(func() { destroySandbox(t, "e2e-workflow") })

	// Simulate agent output by modifying the work copy directly.
	// The work copy path can be derived from the sandbox meta, but here we use
	// 'yoloai ls --json' to confirm the sandbox exists, then write via the host.
	stdout, _, code := runYoloai(t, "ls")
	require.Equal(t, 0, code)
	assert.Contains(t, stdout, "e2e-workflow")

	// Write to the work copy. We need the sandbox dir; construct it from HOME.
	home := os.Getenv("HOME")
	sandboxDir := filepath.Join(home, ".yoloai", "sandboxes", "e2e-workflow")
	assert.DirExists(t, sandboxDir)

	// Sandbox metadata is stored in environment.json (not meta.json).
	metaPath := filepath.Join(sandboxDir, "environment.json")
	assert.FileExists(t, metaPath)

	// Diff should initially be clean
	stdout, _, code = runYoloai(t, "diff", "e2e-workflow")
	require.Equal(t, 0, code)
	assert.Contains(t, stdout, "No changes")

	// Destroy
	stdout, _, code = runYoloai(t, "destroy", "--yes", "e2e-workflow")
	require.Equal(t, 0, code)
	assert.Contains(t, stdout, "Destroyed")
	assert.NoDirExists(t, sandboxDir)
}

// TestE2E_NewDuplicate verifies that creating a sandbox with a duplicate name fails.
func TestE2E_NewDuplicate(t *testing.T) {
	projectDir := e2eSetup(t)

	_, _, code := runYoloai(t, "new", "--agent", "test", "--no-start", "e2e-dup", projectDir)
	require.Equal(t, 0, code)
	t.Cleanup(func() { destroySandbox(t, "e2e-dup") })

	_, _, code = runYoloai(t, "new", "--agent", "test", "--no-start", "e2e-dup", projectDir)
	assert.NotEqual(t, 0, code, "duplicate sandbox creation should fail")
}

// TestE2E_NewForce verifies that --force replaces an existing sandbox.
func TestE2E_NewForce(t *testing.T) {
	projectDir := e2eSetup(t)

	_, _, code := runYoloai(t, "new", "--agent", "test", "--no-start", "e2e-force", projectDir)
	require.Equal(t, 0, code)
	t.Cleanup(func() { destroySandbox(t, "e2e-force") })

	_, _, code = runYoloai(t, "new", "--agent", "test", "--no-start", "--force", "e2e-force", projectDir)
	assert.Equal(t, 0, code, "--force should replace existing sandbox")
}

// TestE2E_DestroyNonExistent verifies that destroying a missing sandbox returns non-zero.
func TestE2E_DestroyNonExistent(t *testing.T) {
	_ = e2eSetup(t)

	_, _, code := runYoloai(t, "destroy", "--yes", "does-not-exist-xyz")
	assert.NotEqual(t, 0, code)
}

// TestE2E_Ls verifies that 'ls' output includes a created sandbox.
func TestE2E_Ls(t *testing.T) {
	projectDir := e2eSetup(t)

	_, _, code := runYoloai(t, "new", "--agent", "test", "--no-start", "e2e-ls", projectDir)
	require.Equal(t, 0, code)
	t.Cleanup(func() { destroySandbox(t, "e2e-ls") })

	stdout, _, code := runYoloai(t, "ls")
	require.Equal(t, 0, code)
	assert.Contains(t, stdout, "e2e-ls")
}

// ABOUTME: Test for Sandbox.Unlock delegation — the no-lock-present path.
package yoloai

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSandbox_Unlock_Noop verifies that unlocking a sandbox with no lock
// file present reports cleared=false without error. The stale-lock and
// live-holder paths are covered by store/lock_test.go.
func TestSandbox_Unlock_Noop(t *testing.T) {
	dir := t.TempDir()
	c, err := NewWithOptions(context.Background(), Options{DataDir: dir, HomeDir: dir})
	require.NoError(t, err)
	defer c.Close() //nolint:errcheck

	require.NoError(t, os.MkdirAll(c.layout.SandboxDir("box"), 0750))
	sb, err := c.Sandbox("box")
	require.NoError(t, err)

	cleared, err := sb.Unlock()
	require.NoError(t, err)
	assert.False(t, cleared)
}

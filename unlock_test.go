// ABOUTME: Test for SystemClient.Unlock delegation — the no-lock-present path.
package yoloai

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSystemClient_Unlock_Noop verifies that unlocking a sandbox with no lock
// file present reports cleared=false without error. The stale-lock and
// live-holder paths are covered by store/lock_test.go.
func TestSystemClient_Unlock_Noop(t *testing.T) {
	sc, err := NewSystemClient(SystemOptions{DataDir: t.TempDir()})
	require.NoError(t, err)

	cleared, err := sc.Unlock("ghost")
	require.NoError(t, err)
	assert.False(t, cleared)
}

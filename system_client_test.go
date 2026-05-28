// ABOUTME: Tests for SystemClient cross-backend introspection (Info / Backends).

package yoloai

import (
	"context"
	"testing"

	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSystemClient_Info verifies paths are derived from the layout and that the
// backend probe returns exactly one status per registered backend (names in
// registration order; unavailable backends carry a reason).
func TestSystemClient_Info(t *testing.T) {
	c := newTestClient(t)

	info, err := c.Info(context.Background())
	require.NoError(t, err)
	require.NotNil(t, info)

	assert.Equal(t, c.layout.YoloaiDir(), info.DataDir)
	assert.Equal(t, c.layout.SandboxesDir(), info.SandboxesDir)
	assert.Equal(t, c.layout.GlobalConfigPath(), info.GlobalConfig)
	assert.Equal(t, c.layout.DefaultsConfigPath(), info.DefaultsConfig)

	descs := runtime.Descriptors()
	require.Len(t, info.Backends, len(descs), "one BackendStatus per registered backend")
	for i, b := range info.Backends {
		assert.Equal(t, descs[i].Name, b.Name, "backend statuses preserve registration order")
		if !b.Available {
			assert.NotEmpty(t, b.Note, "an unavailable backend must explain why")
		}
	}
}

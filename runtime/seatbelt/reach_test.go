package seatbelt

// ABOUTME: Unit test for seatbelt's InjectorReach — host loopback for both fields.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInjectorReach(t *testing.T) {
	reach, err := (&Runtime{}).InjectorReach(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1", reach.BindHost, "host-process backend binds loopback")
	assert.Equal(t, "127.0.0.1", reach.DialHost, "agent shares the host network stack")
	assert.Empty(t, reach.RequiredNetworkMode, "no special network mode needed")
}

//go:build integration

package docker

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClient_ConnectsToDocker(t *testing.T) {
	ctx := context.Background()
	client, err := NewClient(ctx)
	require.NoError(t, err, "Docker must be running for integration tests")
	defer client.Close()

	ping, err := client.Ping(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, ping.APIVersion)
}

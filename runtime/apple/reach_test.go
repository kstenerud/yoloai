package apple

// ABOUTME: Unit tests for parseNetworkGateway — the IPv4-gateway extraction behind
// ABOUTME: apple's vmnet InjectorReach.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseNetworkGateway(t *testing.T) {
	t.Run("default vmnet network", func(t *testing.T) {
		const out = `[{"id":"default","status":{"ipv4Gateway":"192.168.64.1","ipv4Subnet":"192.168.64.0/24"}}]`
		gw, err := parseNetworkGateway(out)
		require.NoError(t, err)
		assert.Equal(t, "192.168.64.1", gw)
	})

	t.Run("empty array", func(t *testing.T) {
		_, err := parseNetworkGateway(`[]`)
		assert.Error(t, err)
	})

	t.Run("no gateway in status", func(t *testing.T) {
		_, err := parseNetworkGateway(`[{"id":"default","status":{"ipv4Subnet":"192.168.64.0/24"}}]`)
		assert.Error(t, err)
	})

	t.Run("malformed JSON", func(t *testing.T) {
		_, err := parseNetworkGateway(`not json`)
		assert.Error(t, err)
	})
}

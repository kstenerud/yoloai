// ABOUTME: Unit tests for firstGateway — the IPAM-config gateway extraction
// ABOUTME: behind docker's network-level InjectorReach.
package docker

import (
	"testing"

	"github.com/docker/docker/api/types/network"
	"github.com/stretchr/testify/assert"
)

func TestFirstGateway(t *testing.T) {
	t.Run("no config", func(t *testing.T) {
		assert.Equal(t, "", firstGateway(nil))
	})

	t.Run("default bridge gateway", func(t *testing.T) {
		cfgs := []network.IPAMConfig{{Subnet: "172.17.0.0/16", Gateway: "172.17.0.1"}}
		assert.Equal(t, "172.17.0.1", firstGateway(cfgs))
	})

	t.Run("first non-empty wins (skips a gatewayless entry)", func(t *testing.T) {
		cfgs := []network.IPAMConfig{{Subnet: "::/0"}, {Gateway: "172.17.0.1"}}
		assert.Equal(t, "172.17.0.1", firstGateway(cfgs))
	})

	t.Run("no gateway anywhere", func(t *testing.T) {
		cfgs := []network.IPAMConfig{{Subnet: "172.17.0.0/16"}}
		assert.Equal(t, "", firstGateway(cfgs))
	})
}

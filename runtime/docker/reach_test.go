// ABOUTME: Unit tests for containerGateway — the default-network-then-fallback
// ABOUTME: gateway extraction behind docker's InjectorReach.
package docker

import (
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/stretchr/testify/assert"
)

func TestContainerGateway(t *testing.T) {
	t.Run("nil settings", func(t *testing.T) {
		assert.Equal(t, "", containerGateway(nil))
	})

	t.Run("default bridge gateway", func(t *testing.T) {
		ns := &container.NetworkSettings{
			Networks: map[string]*network.EndpointSettings{
				"bridge": {Gateway: "172.17.0.1"},
			},
		}
		assert.Equal(t, "172.17.0.1", containerGateway(ns))
	})

	t.Run("no gateway anywhere", func(t *testing.T) {
		ns := &container.NetworkSettings{
			Networks: map[string]*network.EndpointSettings{"none": {}},
		}
		assert.Equal(t, "", containerGateway(ns))
	})
}

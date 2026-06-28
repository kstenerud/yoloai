// ABOUTME: Unit tests for firstGateway — the IPAM-config gateway extraction
// ABOUTME: behind docker's network-level InjectorReach.
package docker

import (
	goruntime "runtime"
	"testing"

	"github.com/docker/docker/api/types/network"
	"github.com/stretchr/testify/assert"
)

// TestIsDesktopClassEngine covers the engine-flavor branch behind InjectorReach:
// a Desktop-class engine binds host loopback + dials the alias, a native Linux
// Engine binds the bridge gateway. The verdict is platform-aware (macOS is always
// a VM), so the assertion folds in GOOS to stay correct on both CI hosts.
func TestIsDesktopClassEngine(t *testing.T) {
	darwinHost := goruntime.GOOS == "darwin"

	t.Run("no provider sockets detected", func(t *testing.T) {
		// Native engine on Linux (gateway-for-both); always a VM on macOS.
		r := &Runtime{}
		assert.Equal(t, darwinHost, r.isDesktopClassEngine())
	})

	t.Run("a Desktop-class provider is detected", func(t *testing.T) {
		// OrbStack/Docker Desktop etc. → desktop-class on every platform.
		r := &Runtime{providerNames: []string{"OrbStack"}}
		assert.True(t, r.isDesktopClassEngine())
	})
}

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

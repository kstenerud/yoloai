//go:build integration

// ABOUTME: Real-backend test that apple's InjectorReach returns a host-bindable
// ABOUTME: vmnet gateway — exactly what the credential broker binds before launch.

package apple

import (
	"errors"
	"net"
	"testing"

	"github.com/kstenerud/yoloai/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInjectorReach_Bindable validates the per-backend credential-broker contract
// on the live apple `container` backend: the reported BindHost must be a real host
// interface the injector can listen on BEFORE any sandbox is created (the broker
// binds it in brokerCredentials, pre-create). It mirrors the broker's own bind
// step (broker.SidecarHost.Ensure → net.Listen on BindHost) and confirms the
// 2026-06-28 Mac-spike finding that the shared vmnet gateway (e.g. 192.168.64.1)
// is persistent and host-bindable. Guest-side reachability of DialHost (the same
// gateway, the guest's default route) is covered by the manual spike recorded in
// egress-broker-host-reachability.md.
func TestInjectorReach_Bindable(t *testing.T) {
	rt, ctx := appleSetup(t)

	reach, err := rt.InjectorReach(ctx)
	if errors.Is(err, runtime.ErrInjectorUnsupported) {
		t.Skip("apple vmnet bridge not up (container system stopped) — injector unsupported, brokering degrades to direct delivery")
	}
	require.NoError(t, err)

	assert.NotEmpty(t, reach.BindHost, "apple must report a vmnet gateway to bind")
	assert.Equal(t, reach.BindHost, reach.DialHost, "apple is gateway-for-both")
	assert.Empty(t, reach.RequiredNetworkMode, "apple needs no special network mode")

	// The broker binds BindHost before the VM exists; prove that listen succeeds.
	ln, err := net.Listen("tcp", net.JoinHostPort(reach.BindHost, "0"))
	require.NoError(t, err, "the injector must be able to bind the vmnet gateway %s", reach.BindHost)
	require.NoError(t, ln.Close())
}

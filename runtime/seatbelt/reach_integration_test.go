//go:build integration

// ABOUTME: Real-backend test that seatbelt's InjectorReach returns a host-bindable
// ABOUTME: loopback endpoint — exactly what the credential broker binds before launch.

package seatbelt

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInjectorReach_Bindable validates the per-backend credential-broker contract
// on the live macOS seatbelt backend: the agent is a host process on the host
// network stack, so the injector binds 127.0.0.1 and the agent reaches it on the
// same loopback. It mirrors the broker's own bind step (broker.SidecarHost.Ensure
// → net.Listen on BindHost). Agent-side reachability of a 127.0.0.1 listener from
// inside an SBPL sandbox is covered by the manual spike recorded in
// egress-broker-host-reachability.md (the profile allows "(allow network*)").
func TestInjectorReach_Bindable(t *testing.T) {
	rt, ctx := seatbeltSetup(t)

	reach, err := rt.InjectorReach(ctx)
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1", reach.BindHost)
	assert.Equal(t, "127.0.0.1", reach.DialHost)
	assert.Empty(t, reach.RequiredNetworkMode)

	ln, err := net.Listen("tcp", net.JoinHostPort(reach.BindHost, "0"))
	require.NoError(t, err, "the injector must be able to bind %s", reach.BindHost)
	require.NoError(t, ln.Close())
}

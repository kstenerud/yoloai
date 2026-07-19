//go:build integration

// ABOUTME: Runs the shared docker-API conformance suite against a real Podman
// ABOUTME: daemon (Podman's docker-compat socket, W5) and pins the backend
// ABOUTME: identity that distinguishes it from the embedded docker runtime.
package podman_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/testutil"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/runtime/podman"
	"github.com/kstenerud/yoloai/runtime/runtimetest"
)

// TestPodmanConformance runs the shared docker-API conformance suite against a
// real Podman daemon. Podman embeds the Docker runtime and speaks the same
// docker-compatible socket, so it exercises the identical table as docker (W5).
func TestPodmanConformance(t *testing.T) {
	runtimetest.RunConformance(t, func(t *testing.T) (runtimetest.DockerCompatRuntime, context.Context) {
		ctx := context.Background()
		rt, err := podman.New(ctx, config.Layout{}.WithEnv(testutil.GetCuratedHostEnv(testutil.IntegrationHostEnvVars)))
		require.NoError(t, err, "Podman must be running with socket activated for integration tests")
		t.Cleanup(func() { _ = rt.Close() })
		return rt, ctx
	})
}

// TestPodman_Descriptor pins the backend identity — the one fact that
// distinguishes podman from the embedded docker runtime.
func TestPodman_Descriptor(t *testing.T) {
	ctx := context.Background()
	rt, err := podman.New(ctx, config.Layout{}.WithEnv(testutil.GetCuratedHostEnv(testutil.IntegrationHostEnvVars)))
	require.NoError(t, err, "Podman must be running for integration tests")
	t.Cleanup(func() { _ = rt.Close() })

	assert.Equal(t, runtime.BackendPodman, rt.Descriptor().Type)
}

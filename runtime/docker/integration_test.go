//go:build integration

// ABOUTME: Runs the shared docker/podman-API conformance suite (runtimetest)
// ABOUTME: against a real Docker daemon so both backends share one behavior
// ABOUTME: table.
package docker_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/testutil"
	"github.com/kstenerud/yoloai/runtime/docker"
	"github.com/kstenerud/yoloai/runtime/runtimetest"
)

// TestDockerConformance runs the shared docker-API conformance suite against a
// real Docker daemon. The behavioral assertions live in runtimetest so docker
// and podman exercise one identical table (W5).
func TestDockerConformance(t *testing.T) {
	runtimetest.RunConformance(t, func(t *testing.T) (runtimetest.DockerCompatRuntime, context.Context) {
		ctx := context.Background()
		rt, err := docker.New(ctx, config.Layout{}.WithEnv(testutil.GetCuratedHostEnv(testutil.IntegrationHostEnvVars)))
		require.NoError(t, err, "Docker must be running for integration tests")
		t.Cleanup(func() { rt.Close() }) //nolint:errcheck // test cleanup
		return rt, ctx
	})
}

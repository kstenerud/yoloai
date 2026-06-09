//go:build integration

package docker_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime/docker"
	"github.com/kstenerud/yoloai/internal/runtime/runtimetest"
)

// TestDockerConformance runs the shared docker-API conformance suite against a
// real Docker daemon. The behavioral assertions live in runtimetest so docker
// and podman exercise one identical table (W5).
func TestDockerConformance(t *testing.T) {
	runtimetest.RunConformance(t, func(t *testing.T) (runtimetest.DockerCompatRuntime, context.Context) {
		ctx := context.Background()
		rt, err := docker.New(ctx, config.Layout{Env: runtimetest.EnvFromOS()})
		require.NoError(t, err, "Docker must be running for integration tests")
		t.Cleanup(func() { rt.Close() }) //nolint:errcheck // test cleanup
		return rt, ctx
	})
}

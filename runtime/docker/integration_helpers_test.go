//go:build integration

package docker

import (
	"context"
	"testing"

	"github.com/kstenerud/yoloai/runtime"
	"github.com/stretchr/testify/require"
)

// dockerSetup connects to Docker, ensures the base image exists,
// and returns a *Runtime. Uses t.Cleanup for Close().
func dockerSetup(t *testing.T) (*Runtime, context.Context) {
	t.Helper()
	ctx := context.Background()

	rt, err := New(ctx)
	require.NoError(t, err, "Docker must be running for integration tests")
	t.Cleanup(func() { rt.Close() }) //nolint:errcheck // test cleanup

	exists, err := rt.ImageExists(ctx, "yoloai-base")
	require.NoError(t, err)
	require.True(t, exists, "yoloai-base image must exist — run 'make build && ./yoloai setup' first")

	return rt, ctx
}

// createTestContainer creates a minimal container for testing.
// Returns the container name. Uses t.Cleanup for removal.
func createTestContainer(t *testing.T, rt *Runtime, ctx context.Context, cfg runtime.InstanceConfig) string {
	t.Helper()

	if cfg.Name == "" {
		cfg.Name = "yoloai-test-" + t.Name()
	}
	if cfg.ImageRef == "" {
		cfg.ImageRef = "yoloai-base"
	}

	require.NoError(t, rt.Create(ctx, cfg))
	t.Cleanup(func() {
		_ = rt.Stop(ctx, cfg.Name)
		_ = rt.Remove(ctx, cfg.Name)
	})

	return cfg.Name
}

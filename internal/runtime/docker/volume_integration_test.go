//go:build integration

package docker

import (
	"context"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/testutil"
)

// TestDindVolumeLifecycle verifies that a privileged sandbox gets a managed,
// labeled /var/lib/docker volume on Create and that Remove reclaims it — the
// backing for docker-in-docker's overlay2 driver (see ensureDindVolumeMount).
func TestDindVolumeLifecycle(t *testing.T) {
	ctx := context.Background()
	rt, err := New(ctx, config.Layout{}.WithEnv(testutil.GetCuratedHostEnv(testutil.IntegrationHostEnvVars)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })

	const name = "yoloai-dindvol-it"
	volName := dockerLibVolumeName(name)
	_ = rt.Remove(ctx, name) // clear any leftover from a prior failed run

	cfg := runtime.InstanceConfig{
		Name:       name,
		ImageRef:   "yoloai-base",
		WorkingDir: "/",
		Privileged: true,
		Labels:     map[string]string{"com.yoloai.sandbox": "dindvol-it"},
	}
	require.NoError(t, rt.Create(ctx, cfg))
	t.Cleanup(func() { _ = rt.Remove(ctx, name) })

	v, err := rt.client.VolumeInspect(ctx, volName)
	require.NoError(t, err, "managed /var/lib/docker volume should exist after Create")
	assert.Equal(t, "true", v.Labels[managedLabel], "volume must carry the managed label for prune/cleanup")
	assert.Equal(t, "dindvol-it", v.Labels["com.yoloai.sandbox"], "instance labels should be copied onto the volume")

	require.NoError(t, rt.Remove(ctx, name))
	_, err = rt.client.VolumeInspect(ctx, volName)
	assert.True(t, cerrdefs.IsNotFound(err), "volume should be gone after Remove, got %v", err)
}

// TestDindVolumeNotCreatedForNonPrivileged guards that the volume is privileged-
// only: a plain sandbox must not leave a stray /var/lib/docker volume behind.
func TestDindVolumeNotCreatedForNonPrivileged(t *testing.T) {
	ctx := context.Background()
	rt, err := New(ctx, config.Layout{}.WithEnv(testutil.GetCuratedHostEnv(testutil.IntegrationHostEnvVars)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })

	const name = "yoloai-nodindvol-it"
	_ = rt.Remove(ctx, name)

	cfg := runtime.InstanceConfig{Name: name, ImageRef: "yoloai-base", WorkingDir: "/"}
	require.NoError(t, rt.Create(ctx, cfg))
	t.Cleanup(func() { _ = rt.Remove(ctx, name) })

	_, err = rt.client.VolumeInspect(ctx, dockerLibVolumeName(name))
	assert.True(t, cerrdefs.IsNotFound(err), "no volume should be created for a non-privileged sandbox")
}

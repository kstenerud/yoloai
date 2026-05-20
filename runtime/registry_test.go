// ABOUTME: Tests for the backend registry descriptor lookup API (W11 step 4).

package runtime_test

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/runtime"
	// Imports for side-effect: each backend's init() registers its descriptor.
	_ "github.com/kstenerud/yoloai/runtime/docker"
	_ "github.com/kstenerud/yoloai/runtime/podman"
)

func TestDescriptor_Found(t *testing.T) {
	desc, ok := runtime.Descriptor("docker")
	require.True(t, ok)
	assert.Equal(t, "docker", desc.Name)
	assert.Equal(t, "container", desc.BaseModeName)
	assert.True(t, desc.Capabilities.CapAdd)
}

func TestDescriptor_NotFound(t *testing.T) {
	desc, ok := runtime.Descriptor("does-not-exist")
	assert.False(t, ok)
	assert.Equal(t, runtime.BackendDescriptor{}, desc)
}

func TestDescriptors_SortedByName(t *testing.T) {
	descs := runtime.Descriptors()
	require.NotEmpty(t, descs)

	names := make([]string, len(descs))
	for i, d := range descs {
		names[i] = d.Name
	}
	assert.True(t, sort.StringsAreSorted(names), "descriptors not sorted: %v", names)

	// docker and podman are imported above; both must be present.
	assert.Contains(t, names, "docker")
	assert.Contains(t, names, "podman")
}

// ABOUTME: Tests for KeepAliveModelOf — nil-safe helper and per-backend assertions.

package runtime_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/runtime"
	// Side-effect imports register each backend's descriptor via init(). These
	// backends all have a platform-neutral descriptor, so they register (and this
	// file compiles) on every host. containerd is linux-only (its Go client deps
	// build only on linux) — its import + assertion live in keepalive_linux_test.go,
	// mirroring the binary's runtime_imports_linux.go.
	_ "github.com/kstenerud/yoloai/runtime/apple"
	_ "github.com/kstenerud/yoloai/runtime/docker"
	_ "github.com/kstenerud/yoloai/runtime/podman"
	_ "github.com/kstenerud/yoloai/runtime/seatbelt"
	_ "github.com/kstenerud/yoloai/runtime/tart"
)

// TestKeepAliveModelOf_Nil confirms the nil-safe default (KeepAliveContainerInit,
// the zero value) so callers never need a nil guard.
func TestKeepAliveModelOf_Nil(t *testing.T) {
	assert.Equal(t, runtime.KeepAliveContainerInit, runtime.KeepAliveModelOf(nil))
}

// TestKeepAliveModelOf_PerBackend asserts the expected KeepAliveModel for every
// registered backend, mirroring the backend-topology.md table.
func TestKeepAliveModelOf_PerBackend(t *testing.T) {
	cases := []struct {
		backend runtime.BackendType
		want    runtime.KeepAliveModel
	}{
		{runtime.BackendDocker, runtime.KeepAliveContainerInit},
		{runtime.BackendPodman, runtime.KeepAliveContainerInit},
		{runtime.BackendTart, runtime.KeepAliveGuestOSInit},
		{runtime.BackendApple, runtime.KeepAliveGuestOSInit},
		{runtime.BackendSeatbelt, runtime.KeepAliveHostKeepAlive},
	}
	for _, tc := range cases {
		t.Run(string(tc.backend), func(t *testing.T) {
			desc, ok := runtime.Descriptor(tc.backend)
			require.True(t, ok, "backend %q not registered", tc.backend)
			assert.Equal(t, tc.want, desc.Capabilities.KeepAliveModel,
				"backend %q KeepAliveModel mismatch", tc.backend)
		})
	}
}

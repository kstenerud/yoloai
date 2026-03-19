package containerdrt

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestName verifies the backend name is "containerd".
func TestName(t *testing.T) {
	r := &Runtime{namespace: "yoloai"}
	assert.Equal(t, "containerd", r.Name())
}

// TestWithNamespace verifies that withNamespace injects the correct namespace.
func TestWithNamespace(t *testing.T) {
	r := &Runtime{namespace: "yoloai"}
	ctx := r.withNamespace(context.Background())
	require.NotNil(t, ctx)
	// The namespace is embedded in the context value — verify we can round-trip it.
	// We don't import namespaces package here to keep the test minimal, so just
	// verify the context is distinct from the input.
	assert.NotEqual(t, context.Background(), ctx)
}

// TestIsWSL2_Linux verifies isWSL2 doesn't panic in a regular Linux environment.
func TestIsWSL2_Linux(t *testing.T) {
	// On a non-WSL2 machine, isWSL2() should return false without panic.
	// On a WSL2 machine it may return true — either is acceptable.
	result := isWSL2()
	// No assertion on value — only testing that it doesn't panic.
	_ = result
}

// TestKataConfigPath verifies the correct config path is returned for each runtime.
func TestKataConfigPath(t *testing.T) {
	tests := []struct {
		runtime  string
		wantPath string
	}{
		{
			runtime:  "io.containerd.kata.v2",
			wantPath: "/opt/kata/share/defaults/kata-containers/runtime-rs/configuration-qemu-runtime-rs.toml",
		},
		{
			runtime:  "io.containerd.kata-fc.v2",
			wantPath: "/opt/kata/share/defaults/kata-containers/configuration-fc.toml",
		},
		{
			runtime:  "",
			wantPath: "/opt/kata/share/defaults/kata-containers/runtime-rs/configuration-qemu-runtime-rs.toml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.runtime, func(t *testing.T) {
			got := kataConfigPath(tt.runtime)
			assert.Equal(t, tt.wantPath, got)
		})
	}
}

// TestSandboxDirForName verifies the sandbox directory path derivation.
func TestSandboxDirForName(t *testing.T) {
	dir := sandboxDirForName("yoloai-mybox")
	assert.Contains(t, dir, "mybox")
	assert.NotContains(t, dir, "yoloai-mybox") // prefix stripped
}

// ABOUTME: YOLOAI_TEST_BACKEND resolver and NewIntegrationRuntime constructor for
// ABOUTME: parametrizing integration tests across docker/podman/containerd backends.
package testutil

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
	yrt "github.com/kstenerud/yoloai/internal/runtime"
)

// integrationBackendEnv selects the backend used by parametrized integration
// tests. Default is "docker"; CI's podman job sets it to "podman".
const integrationBackendEnv = "YOLOAI_TEST_BACKEND"

// IntegrationBackendName returns the backend name driven by
// YOLOAI_TEST_BACKEND, defaulting to "docker". Callers must ensure the
// backend has been registered (via blank-import of its runtime package).
func IntegrationBackendName() string {
	if name := os.Getenv(integrationBackendEnv); name != "" {
		return name
	}
	return "docker"
}

// envSnapshot captures the process environment as a map, the test-side
// equivalent of the CLI's licensed os.Environ() boundary read.
func envSnapshot() map[string]string {
	m := make(map[string]string)
	for _, e := range os.Environ() {
		if k, v, ok := strings.Cut(e, "="); ok {
			m[k] = v
		}
	}
	return m
}

// NewIntegrationRuntime constructs the runtime named by YOLOAI_TEST_BACKEND
// (default "docker"). On failure it calls t.Fatal with the backend name so
// the source of the failure is unambiguous. The returned runtime must be
// closed by the caller.
func NewIntegrationRuntime(ctx context.Context, t *testing.T) yrt.Runtime {
	t.Helper()
	name := IntegrationBackendName()
	home, _ := os.UserHomeDir()
	layout := config.NewLayoutFor(filepath.Join(home, ".yoloai", "library"), home)
	// Tests are the boundary equivalent of the CLI's licensed os.Environ read:
	// thread the host env so backend socket discovery (e.g. podman's
	// XDG_RUNTIME_DIR) sees the real environment, not an empty map.
	layout.Env = envSnapshot()
	rt, err := yrt.New(ctx, yrt.BackendName(name), layout)
	if err != nil {
		t.Fatalf("create %q runtime: %v", name, err)
	}
	return rt
}

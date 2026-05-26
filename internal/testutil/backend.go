// ABOUTME: YOLOAI_TEST_BACKEND resolver and NewIntegrationRuntime constructor for
// ABOUTME: parametrizing integration tests across docker/podman/containerd backends.
package testutil

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kstenerud/yoloai/config"
	yrt "github.com/kstenerud/yoloai/runtime"
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

// NewIntegrationRuntime constructs the runtime named by YOLOAI_TEST_BACKEND
// (default "docker"). On failure it calls t.Fatal with the backend name so
// the source of the failure is unambiguous. The returned runtime must be
// closed by the caller.
func NewIntegrationRuntime(ctx context.Context, t *testing.T) yrt.Runtime {
	t.Helper()
	name := IntegrationBackendName()
	home, _ := os.UserHomeDir()
	layout := config.NewLayout(filepath.Join(home, ".yoloai"))
	rt, err := yrt.New(ctx, name, layout)
	if err != nil {
		t.Fatalf("create %q runtime: %v", name, err)
	}
	return rt
}

//go:build integration

package orchestrator_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/orchestrator"
	dockerrt "github.com/kstenerud/yoloai/internal/runtime/docker"
	"github.com/kstenerud/yoloai/internal/testutil"
)

// TestMain builds the base Docker image once before any integration tests run.
// Individual tests still call integrationSetup(t) which uses IsolatedHome(t)
// for per-test sandbox isolation; subsequent Setup calls hit the cache and
// return in milliseconds.
func TestMain(m *testing.M) {
	ctx := context.Background()
	step := testutil.TestMainBreadcrumb("sandbox")

	tmpHome, err := os.MkdirTemp("", "yoloai-setup-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create temp home: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpHome)
	os.Setenv("HOME", tmpHome) //nolint:errcheck // best-effort env set in test main

	var rt *dockerrt.Runtime
	var dockerErr error
	step("connecting to docker", func() {
		rt, dockerErr = dockerrt.New(ctx, config.Layout{}.WithEnv(testutil.GetCuratedHostEnv(testutil.IntegrationHostEnvVars)))
	})
	if dockerErr != nil {
		fmt.Fprintf(os.Stderr, "Docker unavailable, skipping integration tests: %v\n", dockerErr)
		os.Exit(0)
	}
	defer rt.Close() //nolint:errcheck // best-effort close in test main

	// Pre-seed the build-inputs checksum in the per-test HOME. `make integration`
	// builds the base image (via `make base-image`) immediately before this test
	// runs, so the docker daemon already has yoloai-base:latest with bytes that
	// match the current embedded build inputs. Without this seed, EnsureSetup
	// reads the checksum from the fresh tmp HOME, finds nothing, and triggers a
	// redundant rebuild — which races with the daemon's delete-then-create on
	// the tag and surfaces as "AlreadyExists after deleting the existing one".
	// See backend-idiosyncrasies.md "Docker daemon races on AlreadyExists when
	// rebuilding an existing tag with identical content".
	integLayout := config.NewLayout(filepath.Join(tmpHome, ".yoloai"))
	if err := os.MkdirAll(integLayout.CacheDir(), 0750); err != nil {
		fmt.Fprintf(os.Stderr, "failed to create cache dir: %v\n", err)
		os.Exit(1)
	}
	dockerrt.RecordBuildChecksum(integLayout, "")

	mgr := orchestrator.NewEngineWithRuntime(rt, slog.Default(), strings.NewReader(""), orchestrator.WithLayout(integLayout))
	var setupErr error
	step("ensuring base image is ready", func() {
		setupErr = mgr.EnsureSetup(ctx, io.Discard)
	})
	if setupErr != nil {
		fmt.Fprintf(os.Stderr, "EnsureSetup failed: %v\n", setupErr)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

//go:build integration

// ABOUTME: TestMain for orchestrator integration tests: dispatches the
// ABOUTME: credential-broker injector subprocess, builds a temp HOME plus
// ABOUTME: Docker runtime, pre-seeds the build checksum, and runs EnsureSetup
// ABOUTME: once before the suite.
package orchestrator_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/kstenerud/yoloai/internal/broker"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/orchestrator"
	"github.com/kstenerud/yoloai/internal/testutil"
	dockerrt "github.com/kstenerud/yoloai/runtime/docker"
)

// TestMain builds the base Docker image once before any integration tests run.
// Individual tests still call integrationSetup(t) which uses IsolatedHome(t)
// for per-test sandbox isolation; subsequent Setup calls hit the cache and
// return in milliseconds.
func TestMain(m *testing.M) {
	// The credential broker spawns its injector as `<this-binary> __inject` (it
	// uses os.Executable()). In an integration test that binary IS this test
	// binary, so it must dispatch __inject exactly as cmd/yoloai's main does —
	// run the sidecar and exit before any test bootstrap. This is what lets the
	// real launch path start a working injector during the broker integration test.
	if len(os.Args) >= 2 && os.Args[1] == broker.InjectVerb {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		if err := broker.RunSidecar(ctx, os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err) //nolint:errcheck // best-effort diagnostic
			os.Exit(1)
		}
		os.Exit(0)
	}

	os.Exit(runIntegrationMain(m))
}

// runIntegrationMain holds the real TestMain body in a function that RETURNS its
// exit code, so the deferred temp-HOME cleanup actually runs — os.Exit (called
// only by the thin TestMain wrapper) skips defers, which previously leaked the
// bootstrap HOME on every run. Returns the code to pass to os.Exit.
func runIntegrationMain(m *testing.M) int {
	// Reclaim bootstrap HOMEs leaked by a PRIOR run killed before its defer ran
	// (SIGKILL/-timeout). The live run cleans its own HOME via the defer below.
	testutil.SweepStaleTestHomes("yoloai-setup-")

	ctx := context.Background()
	step := testutil.TestMainBreadcrumb("sandbox")

	tmpHome, err := os.MkdirTemp("", "yoloai-setup-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create temp home: %v\n", err)
		return 1
	}
	defer os.RemoveAll(tmpHome) //nolint:errcheck // best-effort cleanup
	os.Setenv("HOME", tmpHome)  //nolint:errcheck // best-effort env set in test main

	var rt *dockerrt.Runtime
	var dockerErr error
	step("connecting to docker", func() {
		rt, dockerErr = dockerrt.New(ctx, config.Layout{}.WithEnv(testutil.GetCuratedHostEnv(testutil.IntegrationHostEnvVars)))
	})
	if dockerErr != nil {
		return testutil.BackendAbsent("docker", dockerErr.Error())
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
		return 1
	}
	dockerrt.RecordBuildChecksum(integLayout, "docker")

	mgr := orchestrator.NewEngineWithRuntime(rt, slog.Default(), strings.NewReader(""), orchestrator.WithLayout(integLayout))
	var setupErr error
	step("ensuring base image is ready", func() {
		setupErr = mgr.EnsureSetup(ctx, io.Discard)
	})
	if setupErr != nil {
		fmt.Fprintf(os.Stderr, "EnsureSetup failed: %v\n", setupErr)
		return 1
	}

	return m.Run()
}

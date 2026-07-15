//go:build integration

// ABOUTME: TestMain for orchestrator integration tests: dispatches the
// ABOUTME: credential-broker injector subprocess and builds the temp bootstrap
// ABOUTME: HOME. Backend warm-up belongs to each backend's own setup helper.
package orchestrator_test

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"testing"

	"github.com/kstenerud/yoloai/internal/broker"
	"github.com/kstenerud/yoloai/internal/testutil"
)

// TestMain prepares only what every test in this package needs, whatever
// backend it drives: the injector dispatch and a temp bootstrap HOME.
//
// It deliberately does NOT touch a backend. This package is the only
// multi-backend test package in the repo — docker, podman, seatbelt, apple and
// tart tests all live here — and it used to connect to docker and build the
// docker base image here, unconditionally, before any test ran. That made
// docker a hard prerequisite for tests that have nothing to do with it: the
// seatbelt containment test, which needs no daemon at all, could not run
// without Docker Desktop up. Worse, it shaped the build graph around itself —
// the seatbelt and apple orchestrator tests could not live under their own
// Makefile targets, so they were left behind gate variables nothing set, and
// two security tests went unrun (DF99).
//
// Every single-backend package's TestMain probes its own backend, which is
// correct because the package IS that backend (runtime/docker, runtime/tart,
// runtime/seatbelt, runtime/apple). The generalisation for a package holding
// several is that each backend warms itself, once, from its own setup helper:
// see warmDockerBase in integration_helpers_test.go. A backend whose tests
// don't run is then never touched.
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

	tmpHome, err := os.MkdirTemp("", "yoloai-setup-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create temp home: %v\n", err)
		return 1
	}
	defer os.RemoveAll(tmpHome) //nolint:errcheck // best-effort cleanup
	os.Setenv("HOME", tmpHome)  //nolint:errcheck // best-effort env set in test main

	return m.Run()
}

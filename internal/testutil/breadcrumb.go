// ABOUTME: TestMainBreadcrumb emits progress lines from integration-test TestMain
// ABOUTME: so the silent startup gap (Docker dial, image probe) is visible to operators.
package testutil

import (
	"fmt"
	"io"
	"os"
	"time"
)

// TestMainBreadcrumb prints `integration[pkg]: <label> (elapsed)` lines to
// stderr around blocking steps in TestMain. With `go test -v`, no output
// reaches the terminal until the first `=== RUN`, so a slow daemon (cold
// Docker socket, busy host, post-build cache settling) shows up as an
// opaque gap between `make[1]: Leaving directory` and the first test.
// Surface it. Returns a "step" function: pass it a label and the work,
// and it prints start + duration around the call.
//
//	step := TestMainBreadcrumb("sandbox")
//	step("connecting to docker", func() { rt, _ = dockerrt.New(ctx) })
//	step("verifying base image", func() { err = mgr.EnsureSetup(ctx) })
//
// The pkg label distinguishes which package's TestMain is talking when
// `go test ./pkgA ./pkgB ./pkgC` is invoked.
func TestMainBreadcrumb(pkg string) func(label string, fn func()) {
	return breadcrumbWriter(pkg, os.Stderr)
}

// breadcrumbWriter is the testable form of TestMainBreadcrumb.
func breadcrumbWriter(pkg string, w io.Writer) func(label string, fn func()) {
	return func(label string, fn func()) {
		fmt.Fprintf(w, "integration[%s]: %s...\n", pkg, label) //nolint:errcheck // best-effort progress
		start := time.Now()
		fn()
		fmt.Fprintf(w, "integration[%s]: %s done (%v)\n", pkg, label, time.Since(start).Round(time.Millisecond)) //nolint:errcheck
	}
}

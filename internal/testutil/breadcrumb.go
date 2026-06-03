// ABOUTME: TestMainBreadcrumb emits progress lines from integration-test TestMain
// ABOUTME: so the silent startup gap (Docker dial, image probe) is visible to operators.
package testutil

import (
	"fmt"
	"io"
	"os"
	"time"
)

// TestMainBreadcrumb prints `integration[pkg]: <label> (elapsed)` lines
// around blocking steps in TestMain. With `go test -v`, no output reaches
// the terminal until the first `=== RUN`, so a slow daemon (cold Docker
// socket, busy host, post-build cache settling) shows up as an opaque
// gap between `make[1]: Leaving directory` and the first test. Surface
// it. Returns a "step" function: pass it a label and the work, and it
// prints start + duration around the call.
//
//	step := TestMainBreadcrumb("sandbox")
//	step("connecting to docker", func() { rt, _ = dockerrt.New(ctx, env) })
//	step("verifying base image", func() { err = mgr.EnsureSetup(ctx) })
//
// The pkg label distinguishes which package's TestMain is talking when
// `go test ./pkgA ./pkgB ./pkgC` is invoked.
//
// When invoked under multi-package `go test ./a ./b ./c`, cmd/go captures
// each child test binary's stdout AND stderr into per-package buffers and
// emits them in order, so plain Fprintln(os.Stderr, ...) calls from one
// package can sit invisible behind another's stream for many seconds —
// long enough to look like a hang. The breadcrumb output therefore goes
// to /dev/tty when available: that's the controlling terminal device,
// which the child opens directly and bypasses cmd/go's stdout/stderr
// capture pipes entirely. Falls back to os.Stderr when /dev/tty isn't
// reachable (CI runners, nohup, piped output, Windows).
func TestMainBreadcrumb(pkg string) func(label string, fn func()) {
	if tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0); err == nil {
		return breadcrumbWriter(pkg, tty)
	}
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

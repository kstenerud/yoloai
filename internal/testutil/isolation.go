// ABOUTME: The single source of test namespacing — a process-unique
// ABOUTME: PrincipalSegment so a test's prune/sweep can never touch real state.

package testutil

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
	yrt "github.com/kstenerud/yoloai/runtime"
)

var testPrincipalCounter atomic.Uint64

// UniqueTestPrincipal returns a process-unique principal segment ("tNNNNNNN",
// trimmed to config.MaxPrincipalLength). Scoping a test's config.Layout to it
// (via Layout.WithPrincipal or ClientCreateOptions.Principal) means the test's
// instance names are "yoloai-<principal>-*" and every backend's prune sweep —
// which is principal-scoped — can only match that namespace. It can never touch
// the developer's real "yoloai-<name>" / "yoloai-base" resources (DEV §12, DF19).
//
// This is THE shared isolation primitive for both the system Client tests and
// the integration-runtime tests, so there is one place that mints test
// namespaces rather than a per-package counter.
func UniqueTestPrincipal(t *testing.T) config.PrincipalSegment {
	t.Helper()
	n := testPrincipalCounter.Add(1)
	raw := fmt.Sprintf("t%07d", n)
	raw = raw[len(raw)-config.MaxPrincipalLength:] // keep within the segment length cap
	p, err := config.ParsePrincipalSegment(raw)
	if err != nil {
		t.Fatalf("build unique test principal %q: %v", raw, err)
	}
	return p
}

// ReapLeakedInstances destroys any instance already standing under this test's
// principal, so the test provisions its own rather than adopting a leftover.
//
// Call it after the runtime is built and BEFORE the test creates anything: the
// sweep protects nothing, so running it later reaps the test's own instances.
//
// Why this is needed at all. UniqueTestPrincipal is process-unique, not
// run-unique — every binary counts from t0000001, so a given test recomputes the
// SAME instance name on every run. That determinism is deliberate and worth
// keeping: prune is principal-scoped, so debris stays findable and reapable,
// whereas a random principal would orphan it forever under a name nobody can
// reconstruct (DF19). The cost is that a run which dies without running
// t.Cleanup — a test timeout panics, and a panic skips cleanups (DF94) — leaks an
// instance that the next run's DetectStatus reports as StatusActive.
// lifecycle.Start then takes its "already running" no-op (start.go), work-dir
// setup never runs, and the test proceeds against an instance it never
// provisioned. That produced a confusing failure when the leftover happened to
// lack the work dir, but the same adoption could equally report a false PASS —
// which is the reason to reap rather than to hope (DF110).
//
// Safe by construction: Prune is scoped to InstancePrefix(principal), i.e. this
// test's own yoloai-t000NNNN- namespace, so it cannot reach a developer's real
// yoloai-<name> or yoloai-base resources (D62/DF19).
func ReapLeakedInstances(ctx context.Context, t *testing.T, rt yrt.Backend) {
	t.Helper()
	// nil knownInstances: nothing under a test principal is legitimately alive
	// before the test starts, so every match is debris from an earlier run.
	res, err := rt.Prune(ctx, nil, false, LogWriter(t))
	if err != nil {
		t.Fatalf("reap leaked instances under test principal: %v", err)
	}
	for _, item := range res.Items {
		t.Logf("reaped leaked %s %q from an earlier run (DF110) — this run provisions its own", item.Kind, item.Name)
	}
}

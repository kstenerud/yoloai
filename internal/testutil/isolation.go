// ABOUTME: The single source of test namespacing — a process-unique
// ABOUTME: PrincipalSegment so a test's prune/sweep can never touch real state.

package testutil

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
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

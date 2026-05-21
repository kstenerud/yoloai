//go:build integration

// ABOUTME: Per-test tart setup helper. Isolates HOME via testutil and constructs
// ABOUTME: a Runtime; returns the runtime and a context.

package tart

import (
	"context"
	"testing"

	"github.com/kstenerud/yoloai/internal/testutil"
	"github.com/stretchr/testify/require"
)

// tartSetup verifies macOS + Apple Silicon + tart CLI are available,
// isolates HOME so the test doesn't touch the real ~/.yoloai/, and
// returns a *Runtime ready for use.
func tartSetup(t *testing.T) (*Runtime, context.Context) {
	t.Helper()
	testutil.IsolatedHome(t)
	ctx := context.Background()

	rt, err := New(ctx)
	require.NoError(t, err, "tart backend must be available on this platform")

	return rt, ctx
}

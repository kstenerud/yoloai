//go:build integration

// ABOUTME: Per-test seatbelt setup helper. Isolates HOME via testutil and
// ABOUTME: constructs a Runtime; returns the runtime and a context.

package seatbelt

import (
	"context"
	"testing"

	"github.com/kstenerud/yoloai/internal/testutil"
	"github.com/stretchr/testify/require"
)

// seatbeltSetup verifies macOS + sandbox-exec are available, isolates HOME
// so the test doesn't touch the real ~/.yoloai/, and returns a *Runtime
// ready for use. Cleanup of the sandbox directory is handled by t.TempDir().
func seatbeltSetup(t *testing.T) (*Runtime, context.Context) {
	t.Helper()
	testutil.IsolatedHome(t)
	ctx := context.Background()

	rt, err := New(ctx)
	require.NoError(t, err, "seatbelt backend must be available on this platform")

	return rt, ctx
}

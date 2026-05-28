// ABOUTME: Unit tests for the Workdir sub-handle — chiefly that Apply requires
// ABOUTME: an explicit Mode (no silent default) per §4 / comply-or-complain.

package yoloai

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/sandbox"
)

// TestWorkdir_Apply_RequiresMode verifies the consequential apply mode has no
// free default: an unset Mode is a *UsageError (§4), not a silently-chosen
// behavior that could change out from under callers. The check precedes any
// backend use, so a zero-value Client suffices.
func TestWorkdir_Apply_RequiresMode(t *testing.T) {
	_, err := (&Client{}).Sandbox("box").Workdir().Apply(context.Background(), ApplyOptions{})
	require.Error(t, err)
	var ue *sandbox.UsageError
	require.ErrorAs(t, err, &ue, "unset apply mode must be a *UsageError")
}

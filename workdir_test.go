// ABOUTME: Unit tests for the Workdir sub-handle — chiefly that Apply requires
// ABOUTME: an explicit Mode (no silent default) per §4 / comply-or-complain.

package yoloai

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
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

// TestWorkdir_Apply_OverlayRefusesCommits verifies comply-or-complain (D29): an
// :overlay workdir has no commit history, so ApplyModeCommits is refused with a
// *UsageError rather than silently doing something else. The refusal precedes
// any backend use, so a nil runtime suffices.
func TestWorkdir_Apply_OverlayRefusesCommits(t *testing.T) {
	tmpDir := t.TempDir()
	layout := config.NewLayout(filepath.Join(tmpDir, ".yoloai"))
	require.NoError(t, os.MkdirAll(layout.SandboxDir("box"), 0750))
	meta := &store.Meta{
		Name:    "box",
		Agent:   "test",
		Workdir: store.WorkdirMeta{HostPath: "/x", MountPath: "/x", Mode: store.DirModeOverlay, BaselineSHA: "abc"},
	}
	require.NoError(t, store.SaveMeta(layout.SandboxDir("box"), meta))

	c := &Client{layout: layout}
	_, err := c.Sandbox("box").Workdir().Apply(context.Background(), ApplyOptions{Mode: ApplyModeCommits})
	require.Error(t, err)
	var ue *sandbox.UsageError
	require.ErrorAs(t, err, &ue, "ApplyModeCommits on an overlay workdir must be a *UsageError")
}

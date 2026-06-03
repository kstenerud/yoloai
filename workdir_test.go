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
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/yoerrors"
)

// newSandboxHandle builds a validated *Sandbox over a temp layout with a
// sandbox dir holding the given meta. Returns the handle for Workdir tests.
func newSandboxHandle(t *testing.T, meta *store.Environment) *Sandbox {
	t.Helper()
	tmpDir := t.TempDir()
	layout := config.NewLayout(filepath.Join(tmpDir, ".yoloai"))
	require.NoError(t, os.MkdirAll(layout.SandboxDir(meta.Name), 0750))
	require.NoError(t, store.SaveEnvironment(layout.SandboxDir(meta.Name), meta))
	sb, err := (&Client{layout: layout}).Sandbox(meta.Name)
	require.NoError(t, err)
	return sb
}

// TestWorkdir_Apply_RequiresMode verifies the consequential apply mode has no
// free default: an unset Mode is a *UsageError (§4), not a silently-chosen
// behavior that could change out from under callers.
func TestWorkdir_Apply_RequiresMode(t *testing.T) {
	sb := newSandboxHandle(t, &store.Environment{
		Name:      "box",
		AgentType: "test",
		Workdir:   store.WorkdirEnvironment{HostPath: "/x", MountPath: "/x", Mode: store.DirModeCopy, BaselineSHA: "abc"},
	})
	_, err := sb.Workdir().Apply(context.Background(), WorkdirApplyOptions{})
	require.Error(t, err)
	var ue *yoerrors.UsageError
	require.ErrorAs(t, err, &ue, "unset apply mode must be a *UsageError")
}

// TestWorkdir_Apply_OverlayRefusesCommits verifies comply-or-complain (D29): an
// :overlay workdir has no commit history, so ApplyModeCommits is refused with a
// *UsageError rather than silently doing something else.
func TestWorkdir_Apply_OverlayRefusesCommits(t *testing.T) {
	sb := newSandboxHandle(t, &store.Environment{
		Name:      "box",
		AgentType: "test",
		Workdir:   store.WorkdirEnvironment{HostPath: "/x", MountPath: "/x", Mode: store.DirModeOverlay, BaselineSHA: "abc"},
	})
	_, err := sb.Workdir().Apply(context.Background(), WorkdirApplyOptions{Mode: ApplyModeCommits})
	require.Error(t, err)
	var ue *yoerrors.UsageError
	require.ErrorAs(t, err, &ue, "ApplyModeCommits on an overlay workdir must be a *UsageError")
}

// TestClient_Sandbox_NotFoundHandle verifies the handle constructor itself
// refuses an unknown name (F22) — the error surfaces here, not lazily inside a
// later operation.
func TestClient_Sandbox_NotFoundHandle(t *testing.T) {
	tmpDir := t.TempDir()
	c := &Client{layout: config.NewLayout(filepath.Join(tmpDir, ".yoloai"))}
	_, err := c.Sandbox("ghost")
	require.ErrorIs(t, err, ErrSandboxNotFound)
}

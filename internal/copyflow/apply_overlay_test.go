// ABOUTME: Tests for ApplyOverlay — the :overlay net-diff apply orchestrator.
// ABOUTME: The container-backed happy path is covered by integration tests; here
// ABOUTME: we cover the cheap guard (non-overlay sandboxes are a no-op).

package copyflow

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestApplyOverlay_NonOverlayNoop returns (nil, nil) for a copy-mode sandbox
// without touching the runtime — the refusal precedes any container exec, so rt
// is nil here.
func TestApplyOverlay_NonOverlayNoop(t *testing.T) {
	tmpDir := t.TempDir()
	name := "apply-overlay-copy"
	layout := testLayout(tmpDir)
	require.NoError(t, os.MkdirAll(layout.SandboxDir(name), 0750))
	meta := &store.Environment{
		Name:      name,
		AgentType: "test",
		Dirs:      []store.DirEnvironment{{HostPath: filepath.Join(tmpDir, "p"), MountPath: filepath.Join(tmpDir, "p"), Mode: store.DirModeCopy, BaselineSHA: "abc"}},
	}
	require.NoError(t, store.SaveEnvironment(layout.SandboxDir(name), meta))

	result, err := ApplyOverlay(context.Background(), layout, nil, name, ApplyOverlayOptions{})
	require.NoError(t, err)
	assert.Nil(t, result, "ApplyOverlay on a non-overlay sandbox should be a no-op")
}

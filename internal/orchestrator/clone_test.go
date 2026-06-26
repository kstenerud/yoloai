package orchestrator

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/orchestrator/agentcfg"
	"github.com/kstenerud/yoloai/internal/store"
	"github.com/kstenerud/yoloai/yoerrors"
)

func newCloneMgr(tmpDir string) *Engine {
	mock := &lifecycleMockRuntime{}
	layout := config.NewLayout(filepath.Join(tmpDir, ".yoloai"))
	return NewEngineWithRuntime(mock, slog.Default(), strings.NewReader(""), WithLayout(layout))
}

func createCloneSource(t *testing.T, tmpDir, name string) {
	t.Helper()
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, "work"), 0750))

	meta := &store.Environment{
		Name:        name,
		BackendType: "docker",
		CreatedAt:   time.Now().Add(-time.Hour), // created an hour ago
		Dirs: []store.DirEnvironment{{
			HostPath:    "/tmp/project",
			MountPath:   "/tmp/project",
			Mode:        "copy",
			BaselineSHA: "abc123",
		}},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))
	require.NoError(t, agentcfg.Save(sandboxDir, &agentcfg.AgentConfig{AgentType: "claude"}))

	// Add some content to clone
	writeTestFile(t, sandboxDir, "log.txt", "session log content")
}

func TestClone_Success(t *testing.T) {
	tmpDir := t.TempDir()

	createCloneSource(t, tmpDir, "source")
	mgr := newCloneMgr(tmpDir)

	err := mgr.Clone(context.Background(), CloneOptions{Source: "source", Dest: "dest"})
	require.NoError(t, err)

	// Verify destination exists
	dstDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", "dest")
	assert.DirExists(t, dstDir)

	// Verify meta was updated
	meta, err := store.LoadEnvironment(dstDir)
	require.NoError(t, err)
	assert.Equal(t, "dest", meta.Name)
	dstAgent, err := agentcfg.Load(dstDir)
	require.NoError(t, err)
	assert.Equal(t, string(agent.AgentClaude), dstAgent.AgentType)
	assert.Equal(t, "abc123", meta.Workdir().BaselineSHA)
	// CreatedAt should be refreshed (newer than source)
	assert.True(t, meta.CreatedAt.After(time.Now().Add(-time.Minute)))
}

func TestClone_InvalidDestName(t *testing.T) {
	tmpDir := t.TempDir()

	createCloneSource(t, tmpDir, "source2")
	mgr := newCloneMgr(tmpDir)

	err := mgr.Clone(context.Background(), CloneOptions{Source: "source2", Dest: "INVALID!"})
	var ue *yoerrors.UsageError
	require.ErrorAs(t, err, &ue, "an invalid dest name must be a *UsageError, not a generic failure")
	assert.Contains(t, ue.Error(), "INVALID!", "the error should name the rejected dest")
}

func TestClone_SourceNotFound(t *testing.T) {
	tmpDir := t.TempDir()

	mgr := newCloneMgr(tmpDir)
	err := mgr.Clone(context.Background(), CloneOptions{Source: "nonexistent", Dest: "dest2"})
	require.ErrorIs(t, err, store.ErrSandboxNotFound,
		"a missing source must surface ErrSandboxNotFound, distinct from a dest-name rejection")
	assert.ErrorIs(t, err, ErrSandboxNotFound)
}

func TestClone_DestAlreadyExists(t *testing.T) {
	tmpDir := t.TempDir()

	createCloneSource(t, tmpDir, "src3")
	// Create destination dir
	dstDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", "dst3")
	require.NoError(t, os.MkdirAll(dstDir, 0750))

	mgr := newCloneMgr(tmpDir)
	err := mgr.Clone(context.Background(), CloneOptions{Source: "src3", Dest: "dst3"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestClone_MetaNameAndTimestamp(t *testing.T) {
	tmpDir := t.TempDir()

	createCloneSource(t, tmpDir, "src4")
	mgr := newCloneMgr(tmpDir)

	before := time.Now()
	err := mgr.Clone(context.Background(), CloneOptions{Source: "src4", Dest: "dst4"})
	require.NoError(t, err)

	dstDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", "dst4")
	meta, err := store.LoadEnvironment(dstDir)
	require.NoError(t, err)
	assert.Equal(t, "dst4", meta.Name)
	assert.False(t, meta.CreatedAt.Before(before))
}

func TestClone_CleansUpOnMetaLoadFailure(t *testing.T) {
	tmpDir := t.TempDir()

	// Create source without valid environment.json (just a dir)
	srcDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", "badsrc")
	require.NoError(t, os.MkdirAll(srcDir, 0750))
	// Write invalid JSON as environment.json
	writeTestFile(t, srcDir, store.EnvironmentFile, "not valid json{{{")

	mgr := newCloneMgr(tmpDir)
	err := mgr.Clone(context.Background(), CloneOptions{Source: "badsrc", Dest: "baddst"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "load cloned meta")

	// Destination should be cleaned up
	dstDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", "baddst")
	assert.NoDirExists(t, dstDir)
}

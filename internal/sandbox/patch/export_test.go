// ABOUTME: Tests for Export — writing the sandbox's changes as patch files to a
// ABOUTME: directory (the apply --patches flow) without applying them.

package patch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/yoerrors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var exportThreeCommits = []struct {
	subject  string
	filename string
	content  string
}{
	{"add A", "a.txt", "a\n"},
	{"add B", "b.txt", "b\n"},
	{"add C", "c.txt", "c\n"},
}

// TestExport_CopyAllCommits writes one .patch per beyond-baseline commit to Dir
// and reports them, without an uncommitted.diff.
func TestExport_CopyAllCommits(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	name := "export-all"
	createCopySandboxWithCommits(t, tmpDir, name, "/tmp/project", exportThreeCommits)
	rt := getTestRuntime(t)

	dir := filepath.Join(tmpDir, "out")
	result, err := Export(context.Background(), testLayout(tmpDir), rt, name, ExportOptions{Dir: dir})
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, dir, result.Dir)
	assert.False(t, result.UncommittedExported)
	require.Len(t, result.Files, 3)
	for _, f := range result.Files {
		assert.True(t, strings.HasSuffix(f, ".patch"), "expected .patch file, got %s", f)
		assert.FileExists(t, f)
	}
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 3)
}

// TestExport_CopyWithRefs exports only the named commits.
func TestExport_CopyWithRefs(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	name := "export-refs"
	createCopySandboxWithCommits(t, tmpDir, name, "/tmp/project", exportThreeCommits)
	rt := getTestRuntime(t)

	commits, err := ListCommitsBeyondBaseline(context.Background(), testLayout(tmpDir), rt, name)
	require.NoError(t, err)
	require.Len(t, commits, 3)

	dir := filepath.Join(tmpDir, "out")
	result, err := Export(context.Background(), testLayout(tmpDir), rt, name, ExportOptions{
		Dir:  dir,
		Refs: []string{commits[0].SHA[:7], commits[1].SHA[:7]},
	})
	require.NoError(t, err)
	require.Len(t, result.Files, 2)
}

// TestExport_IncludeUncommitted additionally writes uncommitted.diff.
func TestExport_IncludeUncommitted(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	name := "export-uncommitted"
	workDir := createCopySandboxWithCommits(t, tmpDir, name, "/tmp/project", exportThreeCommits)
	writeTestFile(t, workDir, "scratch.txt", "uncommitted edit\n")
	rt := getTestRuntime(t)

	dir := filepath.Join(tmpDir, "out")
	result, err := Export(context.Background(), testLayout(tmpDir), rt, name, ExportOptions{
		Dir:                dir,
		IncludeUncommitted: true,
	})
	require.NoError(t, err)
	assert.True(t, result.UncommittedExported)
	assert.FileExists(t, filepath.Join(dir, "uncommitted.diff"))
	// 3 commit patches + 1 uncommitted.diff
	assert.Len(t, result.Files, 4)
}

// TestExport_RWRefused refuses :rw directories (changes are already live).
func TestExport_RWRefused(t *testing.T) {
	tmpDir := t.TempDir()
	name := "export-rw"
	layout := testLayout(tmpDir)
	require.NoError(t, os.MkdirAll(layout.SandboxDir(name), 0750))
	meta := &store.Environment{
		Name:      name,
		AgentType: "test",
		Workdir:   store.WorkdirEnvironment{HostPath: filepath.Join(tmpDir, "p"), MountPath: filepath.Join(tmpDir, "p"), Mode: store.DirModeRW},
	}
	require.NoError(t, store.SaveEnvironment(layout.SandboxDir(name), meta))

	_, err := Export(context.Background(), layout, nil, name, ExportOptions{Dir: filepath.Join(tmpDir, "out")})
	require.Error(t, err)
	var ue *yoerrors.UsageError
	require.ErrorAs(t, err, &ue, ":rw export must yield a *UsageError")
}

// TestExport_OverlayRefsRefused refuses Refs on an overlay sandbox — overlay
// changes have no commit history to select from. The refusal precedes any
// runtime call, so rt is nil here.
func TestExport_OverlayRefsRefused(t *testing.T) {
	tmpDir := t.TempDir()
	name := "export-overlay-refs"
	layout := testLayout(tmpDir)
	require.NoError(t, os.MkdirAll(layout.SandboxDir(name), 0750))
	meta := &store.Environment{
		Name:      name,
		AgentType: "test",
		Workdir:   store.WorkdirEnvironment{HostPath: filepath.Join(tmpDir, "p"), MountPath: filepath.Join(tmpDir, "p"), Mode: store.DirModeOverlay, BaselineSHA: "abc"},
	}
	require.NoError(t, store.SaveEnvironment(layout.SandboxDir(name), meta))

	_, err := Export(context.Background(), layout, nil, name, ExportOptions{Dir: filepath.Join(tmpDir, "out"), Refs: []string{"abc"}})
	require.Error(t, err)
	var ue *yoerrors.UsageError
	require.ErrorAs(t, err, &ue, "overlay + Refs export must yield a *UsageError")
}

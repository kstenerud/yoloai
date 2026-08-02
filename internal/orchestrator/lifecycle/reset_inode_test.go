// ABOUTME: Reset clears bind-mounted dirs while the sandbox may still be
// ABOUTME: running, so it must empty them in place — replacing the directory
// ABOUTME: leaves the agent writing into an unlinked inode (DF149).
package lifecycle

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/orchestrator/agentcfg"
	"github.com/kstenerud/yoloai/internal/testutil"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/store"
)

// resetInodeSandbox builds the minimum sandbox Reset needs to reach the
// clearing step: a git-baselined work copy, an environment record, and an agent
// config. Returns the sandbox dir.
func resetInodeSandbox(t *testing.T, tmpDir, name string) string {
	t.Helper()

	origDir := filepath.Join(tmpDir, "original")
	require.NoError(t, os.MkdirAll(origDir, 0750))
	testutil.WriteFile(t, origDir, "file.txt", "original content\n")

	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	workDir := filepath.Join(store.WorkBasePath(sandboxDir), store.EncodePath(origDir))
	require.NoError(t, os.MkdirAll(workDir, 0750))
	testutil.WriteFile(t, workDir, "file.txt", "original content\n")
	testutil.InitGitRepo(t, workDir)
	testutil.GitAdd(t, workDir, ".")
	testutil.GitCommit(t, workDir, "yoloai baseline")

	meta := &store.Environment{
		Name:      name,
		Principal: config.CLIPrincipal,
		CreatedAt: time.Now(),
		Dirs: []store.DirEnvironment{{
			HostPath:    origDir,
			MountPath:   origDir,
			Mode:        "copy",
			BaselineSHA: gitHEAD(t, workDir),
		}},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))
	require.NoError(t, agentcfg.Save(sandboxDir, &agentcfg.AgentConfig{AgentType: "claude"}))

	return sandboxDir
}

// runResetForInodes drives Reset far enough to clear the sandbox dirs. Reset
// fails downstream at Start (no runtime-config.json), which the existing reset
// tests rely on too — the clearing has already happened by then.
func runResetForInodes(t *testing.T, tmpDir string, opts ResetOptions) {
	t.Helper()

	mock := &lifecycleMockRuntime{
		stopFn: func(_ context.Context, _ string) error { return nil },
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{}, fmt.Errorf("not found: %w", runtime.ErrNotFound)
		},
	}
	_, err := Reset(context.Background(), newLifecycleDeps(mock, tmpDir), opts)
	require.Error(t, err, "Reset surfaces the downstream Start failure; clearing ran before it")
}

// DF149: files/ and cache/ are bind-mounted into a running sandbox, and reset
// cleared them with RemoveAll+MkdirAll. The guest's mount kept resolving to the
// deleted inode, so the agent's writes failed with a misleading ENOENT and the
// user's drops into files/ never became visible. A contents-only assertion
// cannot catch this — emptying and replacing look identical from the host.
func TestReset_PreservesFilesAndCacheInodes(t *testing.T) {
	tmpDir := t.TempDir()
	name := "test-reset-inodes"
	sandboxDir := resetInodeSandbox(t, tmpDir, name)

	cacheDir := store.CacheDir(sandboxDir)
	filesDir := store.FilesDir(sandboxDir)
	require.NoError(t, os.MkdirAll(cacheDir, 0750))
	require.NoError(t, os.MkdirAll(filesDir, 0750))
	testutil.WriteFile(t, cacheDir, "cached.txt", "cached data\n")
	testutil.WriteFile(t, filesDir, "shared.txt", "shared data\n")

	cacheBefore, err := os.Stat(cacheDir)
	require.NoError(t, err)
	filesBefore, err := os.Stat(filesDir)
	require.NoError(t, err)

	runResetForInodes(t, tmpDir, ResetOptions{Name: name})

	cacheAfter, err := os.Stat(cacheDir)
	require.NoError(t, err)
	filesAfter, err := os.Stat(filesDir)
	require.NoError(t, err)

	assert.True(t, os.SameFile(cacheBefore, cacheAfter),
		"the sandbox's bind-mount resolves to this inode; replacing it strands the agent")
	assert.True(t, os.SameFile(filesBefore, filesAfter),
		"the sandbox's bind-mount resolves to this inode; replacing it strands the agent")

	// The clearing itself must still work — the fix must not trade one for the other.
	assert.NoFileExists(t, filepath.Join(cacheDir, "cached.txt"))
	assert.NoFileExists(t, filepath.Join(filesDir, "shared.txt"))
}

// agent-runtime/ is bind-mounted too and --clear-state wipes it on a live
// sandbox, so it has the same exposure the reported files/cache bug had.
func TestReset_ClearState_PreservesAgentRuntimeInode(t *testing.T) {
	tmpDir := t.TempDir()
	name := "test-reset-agentstate"
	sandboxDir := resetInodeSandbox(t, tmpDir, name)

	agentStateDir := store.AgentRuntimePath(sandboxDir)
	require.NoError(t, os.MkdirAll(agentStateDir, 0750))
	testutil.WriteFile(t, agentStateDir, "state.json", "{}\n")

	before, err := os.Stat(agentStateDir)
	require.NoError(t, err)

	runResetForInodes(t, tmpDir, ResetOptions{Name: name, ClearState: true})

	after, err := os.Stat(agentStateDir)
	require.NoError(t, err)
	assert.True(t, os.SameFile(before, after),
		"the sandbox's bind-mount resolves to this inode; replacing it strands the agent")
	assert.NoFileExists(t, filepath.Join(agentStateDir, "state.json"))
}

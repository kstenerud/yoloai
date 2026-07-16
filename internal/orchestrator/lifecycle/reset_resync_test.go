// ABOUTME: In-place reset must reproduce the copy create made, not approximate
// ABOUTME: it: it re-copies through CopyProjectDir and prunes to that file set,
// ABOUTME: so it cannot re-import a gitignored secret (DF117), merge a .git
// ABOUTME: (DF118), or re-acquire a worktree's gitlink (DF116).
package lifecycle

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/git"
	"github.com/kstenerud/yoloai/internal/orchestrator/agentcfg"
	"github.com/kstenerud/yoloai/internal/testutil"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/store"
)

// confinedBackend models a real backend: every one of them confines work-copy
// git, which is what lets a :copy dir keep its history (DF119).
func confinedBackend() *lifecycleMockRuntime {
	return &lifecycleMockRuntime{gitExecInConfinement: true}
}

func resync(t *testing.T, dir store.DirEnvironment, workDir string) (string, error) {
	t.Helper()
	return resyncWorkCopy(context.Background(), git.NewTestHostWithEnv(testutil.GitEnv()), dir, workDir, confinedBackend())
}

// makeWorktreeRepo builds a real repo with a linked worktree checked out on
// branch `feature`. Real git, not a hand-written `gitdir:` string: the point is
// what git itself puts in that file.
func makeWorktreeRepo(t *testing.T, root string) (mainRepo, worktree string) {
	t.Helper()
	mainRepo = filepath.Join(root, "main")
	worktree = filepath.Join(root, "wt")
	require.NoError(t, os.MkdirAll(mainRepo, 0o750))
	testutil.InitGitRepo(t, mainRepo)
	testutil.WriteFile(t, mainRepo, "file.txt", "v1\n")
	testutil.GitAdd(t, mainRepo, ".")
	testutil.GitCommit(t, mainRepo, "initial")
	testutil.RunGit(t, mainRepo, "worktree", "add", "-q", worktree, "-b", "feature")
	return mainRepo, worktree
}

// newWorkCopy stands in for what create leaves behind: the source's files and a
// baseline repo of the work copy's own.
func newWorkCopy(t *testing.T, content string) string {
	t.Helper()
	workDir := filepath.Join(t.TempDir(), "work")
	require.NoError(t, os.MkdirAll(workDir, 0o750))
	testutil.WriteFile(t, workDir, "file.txt", content)
	testutil.InitGitRepo(t, workDir)
	testutil.GitAdd(t, workDir, ".")
	testutil.GitCommit(t, workDir, "yoloai baseline")
	return workDir
}

// secretRepo builds a source repo that gitignores a secret and a build artifact
// — the shape :copy exists to protect.
func secretRepo(t *testing.T) string {
	src := filepath.Join(t.TempDir(), "orig")
	require.NoError(t, os.MkdirAll(src, 0o750))
	testutil.InitGitRepo(t, src)
	testutil.WriteFile(t, src, ".gitignore", ".env\nnode_modules/\n")
	testutil.WriteFile(t, src, "app.js", "v1\n")
	testutil.WriteFile(t, src, ".env", "AWS_SECRET_KEY=hunter2")
	require.NoError(t, os.MkdirAll(filepath.Join(src, "node_modules"), 0o750))
	testutil.WriteFile(t, src, filepath.Join("node_modules", "dep.js"), "artifact")
	testutil.GitAdd(t, src, ".")
	testutil.GitCommit(t, src, "initial")
	return src
}

// DF117, the defect itself: a reset must not hand the agent the .gitignore'd
// secret that create was careful to leave out.
func TestResyncWorkCopy_CopyDoesNotImportIgnoredSecrets(t *testing.T) {
	src := secretRepo(t)
	workDir := newWorkCopy(t, "v1\n")

	_, err := resync(t, store.DirEnvironment{HostPath: src, Mode: "copy"}, workDir)
	require.NoError(t, err)

	assert.False(t, exists(filepath.Join(workDir, ".env")),
		"a gitignored secret must never reach the sandbox — the whole purpose of :copy")
	assert.False(t, exists(filepath.Join(workDir, "node_modules")), "nor a build artifact tree")
	assert.True(t, exists(filepath.Join(workDir, "app.js")), "the project itself still arrives")
}

// The leak was not hypothetical: a work copy that already holds a secret from an
// older, laxer reset must be cleaned by this one, not left as it found it.
func TestResyncWorkCopy_RemovesAPreviouslyLeakedSecret(t *testing.T) {
	src := secretRepo(t)
	workDir := newWorkCopy(t, "v1\n")
	testutil.WriteFile(t, workDir, ".env", "AWS_SECRET_KEY=leaked-by-an-older-reset")

	_, err := resync(t, store.DirEnvironment{HostPath: src, Mode: "copy"}, workDir)
	require.NoError(t, err)

	assert.False(t, exists(filepath.Join(workDir, ".env")), "reset must clean up the old leak")
}

// :copy-all opts out of gitignore honoring, and must keep doing so.
func TestResyncWorkCopy_CopyAllStillImportsIgnoredFiles(t *testing.T) {
	src := secretRepo(t)
	workDir := newWorkCopy(t, "v1\n")

	_, err := resync(t, store.DirEnvironment{HostPath: src, Mode: "copy", IncludeIgnored: true}, workDir)
	require.NoError(t, err)

	assert.True(t, exists(filepath.Join(workDir, ".env")), ":copy-all's purpose is including ignored files")
	assert.False(t, exists(filepath.Join(workDir, "node_modules")), "but artifacts stay out, as at create")
}

// copy-strict strips history because the history may hold unrotated secrets. A
// reset that hands it back defeats the flag (DF117).
func TestResyncWorkCopy_CopyStrictDoesNotImportHistory(t *testing.T) {
	src := secretRepo(t)
	workDir := newWorkCopy(t, "v1\n")

	sha, err := resync(t, store.DirEnvironment{HostPath: src, Mode: "copy", StripHistory: true}, workDir)
	require.NoError(t, err)

	assert.Len(t, sha, 40, "the copy still gets a baseline of its own")
	assert.Equal(t, "yoloai baseline", testutil.RunGitOutput(t, workDir, "log", "-1", "--format=%s"),
		"a fresh baseline, not the source's history")
	assert.NotEqual(t, gitHEAD(t, src), sha)
}

// Reset discards the agent's work: that is what it is for.
func TestResyncWorkCopy_DiscardsAgentChangesAndPicksUpUpstream(t *testing.T) {
	src := secretRepo(t)
	workDir := newWorkCopy(t, "v1\n")
	testutil.WriteFile(t, workDir, "agent-new.txt", "the agent made this")
	testutil.WriteFile(t, workDir, "app.js", "mangled by the agent\n")
	testutil.WriteFile(t, src, "app.js", "v2 upstream\n")
	testutil.GitAdd(t, src, ".")
	testutil.GitCommit(t, src, "upstream change")

	_, err := resync(t, store.DirEnvironment{HostPath: src, Mode: "copy"}, workDir)
	require.NoError(t, err)

	assert.False(t, exists(filepath.Join(workDir, "agent-new.txt")), "the agent's additions go")
	body, err := os.ReadFile(filepath.Join(workDir, "app.js")) //nolint:gosec // G304: test temp dir
	require.NoError(t, err)
	assert.Equal(t, "v2 upstream\n", string(body), "and upstream arrives")
}

// The work copy's directory is bind-mounted into a live container, so the sync
// must overwrite in place. Replacing the directory would leave the agent looking
// at a deleted inode for the rest of the session.
func TestResyncWorkCopy_PreservesWorkDirInode(t *testing.T) {
	src := secretRepo(t)
	workDir := newWorkCopy(t, "v1\n")

	before, err := os.Stat(workDir)
	require.NoError(t, err)

	_, err = resync(t, store.DirEnvironment{HostPath: src, Mode: "copy"}, workDir)
	require.NoError(t, err)

	after, err := os.Stat(workDir)
	require.NoError(t, err)
	assert.True(t, os.SameFile(before, after),
		"the container's bind-mount resolves to this inode; replacing it strands the agent")
}

// DF116: rsync used to mirror a worktree's gitlink into the copy, where it
// resolved back to the user's real repo. The copy must own its baseline.
func TestResyncWorkCopy_FromWorktree_KeepsBaselineLocal(t *testing.T) {
	_, wt := makeWorktreeRepo(t, t.TempDir())
	workDir := newWorkCopy(t, "v1\n")

	testutil.WriteFile(t, wt, "file.txt", "v2 upstream\n")
	hostHead := gitHEAD(t, wt)
	hostStatus := testutil.RunGitOutput(t, wt, "status", "--porcelain")
	require.NotEmpty(t, hostStatus, "fixture must be dirty or the read-through would have nothing to prove")

	sha, err := resync(t, store.DirEnvironment{HostPath: wt, Mode: "copy"}, workDir)
	require.NoError(t, err)

	assert.Len(t, sha, 40)
	assert.NotEqual(t, hostHead, sha, "baseline must not be the user's HEAD, read through a gitlink")
	info, err := os.Lstat(filepath.Join(workDir, ".git"))
	require.NoError(t, err, "the work copy still needs a repo")
	assert.True(t, info.IsDir(), "and it must be its own, not a pointer at the user's")

	body, err := os.ReadFile(filepath.Join(workDir, "file.txt")) //nolint:gosec // G304: test temp dir
	require.NoError(t, err)
	assert.Equal(t, "v2 upstream\n", string(body), "severing must not cost the resync")

	assert.Equal(t, hostHead, gitHEAD(t, wt), "the user's branch must not have moved")
	assert.Equal(t, hostStatus, testutil.RunGitOutput(t, wt, "status", "--porcelain"))
}

// DF118: .git is replaced as a unit. Copying it entry by entry over an existing
// repo unions the two and can leave a ref naming an object that is gone.
func TestResyncWorkCopy_ReplacesGitRatherThanMergingIt(t *testing.T) {
	src := secretRepo(t)
	workDir := newWorkCopy(t, "v1\n")

	// The work copy's baseline repo is unrelated to the source's: exactly the
	// case where a merge leaves a dangling ref.
	staleSHA := gitHEAD(t, workDir)

	sha, err := resync(t, store.DirEnvironment{HostPath: src, Mode: "copy"}, workDir)
	require.NoError(t, err)

	assert.Equal(t, gitHEAD(t, src), sha, "the copy adopts the source's history wholesale")
	assert.NotEqual(t, staleSHA, sha)
	assert.Equal(t, "initial", testutil.RunGitOutput(t, workDir, "log", "-1", "--format=%s"),
		"and the repo is readable — a merged .git would say 'bad object HEAD' here")
}

// A non-repo source has no history to adopt, so the copy gets a fresh baseline.
func TestResyncWorkCopy_FromPlainDir_CreatesFreshBaseline(t *testing.T) {
	src := filepath.Join(t.TempDir(), "orig")
	require.NoError(t, os.MkdirAll(src, 0o750))
	testutil.WriteFile(t, src, "file.txt", "v1\n")
	workDir := newWorkCopy(t, "stale\n")

	sha, err := resync(t, store.DirEnvironment{HostPath: src, Mode: "copy"}, workDir)
	require.NoError(t, err)
	assert.Len(t, sha, 40)
	assert.Equal(t, sha, gitHEAD(t, workDir))
}

func exists(path string) bool { _, err := os.Lstat(path); return err == nil }

// The same defect through the front door: Reset() itself, on a running
// container, taking the in-place path. resyncWorkCopy is tested directly above;
// this proves the wiring reaches it with the dir's real mode, since reading
// IncludeIgnored/StripHistory off the wrong place is exactly how a fix like this
// gets undone.
func TestReset_InPlace_DoesNotImportIgnoredSecrets(t *testing.T) {
	tmpDir := t.TempDir()
	src := secretRepo(t)

	name := "test-reset-secrets"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	workDir := filepath.Join(sandboxDir, "work", store.EncodePath(src))
	require.NoError(t, os.MkdirAll(workDir, 0o750))

	// The work copy as create leaves it for :copy — the secret is not here.
	testutil.WriteFile(t, workDir, "app.js", "v1\n")
	testutil.InitGitRepo(t, workDir)
	testutil.GitAdd(t, workDir, ".")
	testutil.GitCommit(t, workDir, "yoloai baseline")
	require.False(t, exists(filepath.Join(workDir, ".env")), "fixture: create left the secret out")

	meta := &store.Environment{
		Name:      name,
		CreatedAt: time.Now(),
		Dirs: []store.DirEnvironment{{
			HostPath:    src,
			MountPath:   src,
			Mode:        "copy",
			BaselineSHA: gitHEAD(t, workDir),
		}},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))
	require.NoError(t, agentcfg.Save(sandboxDir, &agentcfg.AgentConfig{AgentType: "claude"}))

	mock := &lifecycleMockRuntime{
		gitExecInConfinement: true,
		inspectFn: func(_ context.Context, _ string) (runtime.InstanceInfo, error) {
			return runtime.InstanceInfo{Running: true}, nil // running: take the in-place path
		},
	}

	// The notification fails (no runtime-config.json), which is fine and asserted
	// elsewhere; the workspace sync still runs and is what this is about.
	_, _ = Reset(context.Background(), newLifecycleDeps(mock, tmpDir), ResetOptions{Name: name})

	assert.False(t, exists(filepath.Join(workDir, ".env")),
		"reset must not hand the agent the secret :copy kept out at create")
	assert.False(t, exists(filepath.Join(workDir, "node_modules")))
	assert.True(t, exists(filepath.Join(workDir, "app.js")), "and the project still arrives")
}

// DF120: reset must baseline the way create does. A source with uncommitted work
// is copied dirty, and a baseline of HEAD would make the user's own edits read as
// the agent's from the moment the reset finished.
func TestResyncWorkCopy_DirtySource_BaselinesLikeCreate(t *testing.T) {
	src := filepath.Join(t.TempDir(), "orig")
	require.NoError(t, os.MkdirAll(src, 0o750))
	testutil.InitGitRepo(t, src)
	testutil.WriteFile(t, src, "app.js", "v1\n")
	testutil.GitAdd(t, src, ".")
	testutil.GitCommit(t, src, "initial")
	srcHead := gitHEAD(t, src)
	testutil.WriteFile(t, src, "app.js", "v1\nthe user's work in progress\n")

	workDir := newWorkCopy(t, "v1\n")
	sha, err := resync(t, store.DirEnvironment{HostPath: src, Mode: "copy"}, workDir)
	require.NoError(t, err)

	assert.NotEqual(t, srcHead, sha, "the baseline must move past HEAD to cover the dirty state")
	assert.Empty(t, testutil.RunGitOutput(t, workDir, "status", "--porcelain"),
		"the agent starts clean, so a diff shows only the agent's work")
	assert.Equal(t, "yoloai: pre-session state", testutil.RunGitOutput(t, workDir, "log", "-1", "--format=%s"))

	// The property that actually matters: a diff right after reset is empty.
	testutil.RunGit(t, workDir, "add", "-A")
	assert.Empty(t, testutil.RunGitOutput(t, workDir, "diff", sha),
		"the agent has done nothing, so yoloai diff must report nothing")
}

// DF122: a SandboxSide backend baselines inside the VM after start, and the empty
// SHA is the signal that triggers it. Baselining here would return one and
// silently suppress the VM's work-dir setup.
func TestResetCopyWorkdir_SandboxSide_DefersBaseline(t *testing.T) {
	src := filepath.Join(t.TempDir(), "orig")
	require.NoError(t, os.MkdirAll(src, 0o750))
	testutil.InitGitRepo(t, src)
	testutil.WriteFile(t, src, "app.js", "v1\n")
	testutil.GitAdd(t, src, ".")
	testutil.GitCommit(t, src, "initial")

	tmpDir := t.TempDir()
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", "vmbox")
	meta := &store.Environment{
		Name:      "vmbox",
		CreatedAt: time.Now(),
		Dirs:      []store.DirEnvironment{{HostPath: src, MountPath: src, Mode: "copy"}},
	}
	// SandboxSide, and confined git so the work copy keeps a .git — which is
	// exactly the case that used to return a SHA and skip the VM setup.
	mock := &lifecycleMockRuntime{locality: runtime.LocalitySandboxSide, gitExecInConfinement: true}

	sha, err := resetCopyWorkdir(context.Background(), newLifecycleDeps(mock, tmpDir), "vmbox", sandboxDir, meta)
	require.NoError(t, err)
	assert.Empty(t, sha, "an empty SHA is what tells reset to run the VM work-dir setup")
}

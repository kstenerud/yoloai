// ABOUTME: CopyProjectDir's git-aware filtering: honors git's tracked and
// ABOUTME: untracked file list by default, --include-ignored copies everything,
// ABOUTME: falls back to a full copy outside a repo, and PreserveGit keeps or
// ABOUTME: drops .git history.
package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/internal/sysexec"
	"github.com/kstenerud/yoloai/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func exists(path string) bool { _, err := os.Lstat(path); return err == nil }

// listFn returns a fixed project-file list, standing in for git.ListProjectFiles.
func listFn(files ...string) func() ([]string, bool, error) {
	return func() ([]string, bool, error) { return files, true, nil }
}

func TestCopyProjectDir_HonorsListedFilesOnly(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "a.txt"), "a")
	write(t, filepath.Join(src, "sub", "b.txt"), "b")
	write(t, filepath.Join(src, "secret.env"), "TOKEN") // present on disk but NOT in the list

	dst := filepath.Join(t.TempDir(), "out")
	require.NoError(t, CopyProjectDir(src, dst, false, false, listFn("a.txt", "sub/b.txt")))

	assert.True(t, exists(filepath.Join(dst, "a.txt")))
	assert.True(t, exists(filepath.Join(dst, "sub", "b.txt")), "nested listed file copied")
	assert.False(t, exists(filepath.Join(dst, "secret.env")), "unlisted (ignored) file excluded")
}

func TestCopyProjectDir_IncludeIgnoredCopiesEverything(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "data.txt"), "d")
	write(t, filepath.Join(src, "secret.env"), "TOKEN")

	// includeIgnored=true must copy all and must NOT consult the enumerator.
	enum := func() ([]string, bool, error) {
		t.Fatal("enumerator must not be called for :copy-all")
		return nil, false, nil
	}
	dst := filepath.Join(t.TempDir(), "out")
	require.NoError(t, CopyProjectDir(src, dst, true, false, enum))

	assert.True(t, exists(filepath.Join(dst, "data.txt")))
	assert.True(t, exists(filepath.Join(dst, "secret.env")), ":copy-all includes gitignored files")
}

func TestCopyProjectDir_NonRepoFallsBackToFullCopy(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "config.env"), "x")
	write(t, filepath.Join(src, "data.txt"), "y")

	notRepo := func() ([]string, bool, error) { return nil, false, nil }
	dst := filepath.Join(t.TempDir(), "out")
	require.NoError(t, CopyProjectDir(src, dst, false, false, notRepo))

	assert.True(t, exists(filepath.Join(dst, "config.env")), "non-repo copies everything")
	assert.True(t, exists(filepath.Join(dst, "data.txt")))
}

func TestCopyProjectDir_SkipsDeletedAndPreservesSymlink(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "real.txt"), "r")
	require.NoError(t, os.Symlink("real.txt", filepath.Join(src, "link")))

	// "gone.txt" is listed (tracked) but not on disk (deleted) — must be skipped.
	dst := filepath.Join(t.TempDir(), "out")
	require.NoError(t, CopyProjectDir(src, dst, false, false, listFn("real.txt", "link", "gone.txt")))

	assert.True(t, exists(filepath.Join(dst, "real.txt")))
	assert.False(t, exists(filepath.Join(dst, "gone.txt")), "deleted-but-listed file skipped")
	fi, err := os.Lstat(filepath.Join(dst, "link"))
	require.NoError(t, err)
	assert.True(t, fi.Mode()&os.ModeSymlink != 0, "symlink recreated as a symlink")
}

func TestCopyProjectDir_EnumeratorErrorPropagates(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "a.txt"), "a")
	boom := func() ([]string, bool, error) { return nil, false, errors.New("git exploded") }
	err := CopyProjectDir(src, filepath.Join(t.TempDir(), "out"), false, false, boom)
	require.Error(t, err, "a genuine enumeration error must not silently full-copy")
}

// runGit runs git in dir, failing the test on error, and returns trimmed stdout.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := sysexec.Command(testutil.GitEnv(), "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

// makeRepo initializes a real git repo in dir with one commit tracking a.txt and
// .gitignore (which ignores ignored.log). Returns nothing; the repo has history.
func makeRepo(t *testing.T, dir string) {
	t.Helper()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "t@t")
	runGit(t, dir, "config", "user.name", "t")
	write(t, filepath.Join(dir, "a.txt"), "a")
	write(t, filepath.Join(dir, ".gitignore"), "ignored.log\n")
	write(t, filepath.Join(dir, "ignored.log"), "SECRET")
	runGit(t, dir, "add", "a.txt", ".gitignore")
	runGit(t, dir, "commit", "-qm", "initial commit")
}

func TestCopyProjectDir_PreserveGitKeepsHistory(t *testing.T) {
	src := t.TempDir()
	makeRepo(t, src)

	dst := filepath.Join(t.TempDir(), "out")
	require.NoError(t, CopyProjectDir(src, dst, false, true, listFn("a.txt", ".gitignore")))

	assert.True(t, exists(filepath.Join(dst, ".git")), "preserveGit clones the source .git")
	assert.True(t, exists(filepath.Join(dst, "a.txt")))
	assert.False(t, exists(filepath.Join(dst, "ignored.log")), "gitignored file still excluded (orthogonal to history)")
	// History survived: the source commit is reachable in the work copy.
	assert.Contains(t, runGit(t, dst, "log", "--oneline"), "initial commit")
	// The tracked working tree matches HEAD (gitignored files are untracked, so
	// their absence does not dirty the repo).
	assert.Empty(t, runGit(t, dst, "status", "--porcelain"), "work copy is clean vs HEAD")
}

func TestCopyProjectDir_StripHistoryDropsGitDir(t *testing.T) {
	src := t.TempDir()
	makeRepo(t, src)

	dst := filepath.Join(t.TempDir(), "out")
	require.NoError(t, CopyProjectDir(src, dst, false, false, listFn("a.txt", ".gitignore")))

	assert.False(t, exists(filepath.Join(dst, ".git")), "preserveGit=false leaves no .git (fresh-baseline path)")
	assert.True(t, exists(filepath.Join(dst, "a.txt")))
	assert.False(t, exists(filepath.Join(dst, "ignored.log")), "gitignored file still excluded")
}

func TestPreserveGit(t *testing.T) {
	cases := []struct {
		name                        string
		stripHistory, confined      bool
		wantPreserve, wantDowngrade bool
	}{
		{"default on confined backend preserves", false, true, true, false},
		{"default on unconfined backend strips + downgrades", false, false, false, true},
		{"opt-out on confined backend strips, no downgrade", true, true, false, false},
		{"opt-out on unconfined backend strips, no downgrade", true, false, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			preserve, downgraded := PreserveGit(c.stripHistory, c.confined)
			assert.Equal(t, c.wantPreserve, preserve)
			assert.Equal(t, c.wantDowngrade, downgraded)
		})
	}
}

package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

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
	require.NoError(t, CopyProjectDir(src, dst, false, listFn("a.txt", "sub/b.txt")))

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
	require.NoError(t, CopyProjectDir(src, dst, true, enum))

	assert.True(t, exists(filepath.Join(dst, "data.txt")))
	assert.True(t, exists(filepath.Join(dst, "secret.env")), ":copy-all includes gitignored files")
}

func TestCopyProjectDir_NonRepoFallsBackToFullCopy(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "config.env"), "x")
	write(t, filepath.Join(src, "data.txt"), "y")

	notRepo := func() ([]string, bool, error) { return nil, false, nil }
	dst := filepath.Join(t.TempDir(), "out")
	require.NoError(t, CopyProjectDir(src, dst, false, notRepo))

	assert.True(t, exists(filepath.Join(dst, "config.env")), "non-repo copies everything")
	assert.True(t, exists(filepath.Join(dst, "data.txt")))
}

func TestCopyProjectDir_SkipsDeletedAndPreservesSymlink(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "real.txt"), "r")
	require.NoError(t, os.Symlink("real.txt", filepath.Join(src, "link")))

	// "gone.txt" is listed (tracked) but not on disk (deleted) — must be skipped.
	dst := filepath.Join(t.TempDir(), "out")
	require.NoError(t, CopyProjectDir(src, dst, false, listFn("real.txt", "link", "gone.txt")))

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
	err := CopyProjectDir(src, filepath.Join(t.TempDir(), "out"), false, boom)
	require.Error(t, err, "a genuine enumeration error must not silently full-copy")
}

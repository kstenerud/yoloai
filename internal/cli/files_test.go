package cli

// ABOUTME: Tests for the files exchange CLI commands (put, get, ls, rm, path).

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupFilesTest creates a fake sandbox directory with a files/ subdirectory.
// Returns the sandbox name and the files directory path.
func setupFilesTest(t *testing.T) (string, string) {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	name := "testbox"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	filesDir := filepath.Join(sandboxDir, "files")
	require.NoError(t, os.MkdirAll(filesDir, 0750))

	return name, filesDir
}

// --- put ---

func TestFilesPut_SingleFile(t *testing.T) {
	name, filesDir := setupFilesTest(t)

	src := filepath.Join(t.TempDir(), "hello.txt")
	require.NoError(t, os.WriteFile(src, []byte("hello"), 0600))

	cmd := newFilesPutCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{name, src})
	require.NoError(t, cmd.Execute())

	assert.Equal(t, "hello.txt\n", buf.String())

	got, err := os.ReadFile(filepath.Join(filesDir, "hello.txt")) //nolint:gosec // test helper
	require.NoError(t, err)
	assert.Equal(t, "hello", string(got))
}

func TestFilesPut_MultipleFiles(t *testing.T) {
	name, filesDir := setupFilesTest(t)

	srcDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "a.txt"), []byte("a"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "b.txt"), []byte("b"), 0600))

	cmd := newFilesPutCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{name, filepath.Join(srcDir, "a.txt"), filepath.Join(srcDir, "b.txt")})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, buf.String(), "a.txt")
	assert.Contains(t, buf.String(), "b.txt")
	assert.FileExists(t, filepath.Join(filesDir, "a.txt"))
	assert.FileExists(t, filepath.Join(filesDir, "b.txt"))
}

func TestFilesPut_Directory(t *testing.T) {
	name, filesDir := setupFilesTest(t)

	srcDir := filepath.Join(t.TempDir(), "mydir")
	require.NoError(t, os.MkdirAll(srcDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "inner.txt"), []byte("inner"), 0600))

	cmd := newFilesPutCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{name, srcDir})
	require.NoError(t, cmd.Execute())

	assert.Equal(t, "mydir\n", buf.String())

	got, err := os.ReadFile(filepath.Join(filesDir, "mydir", "inner.txt")) //nolint:gosec // test helper
	require.NoError(t, err)
	assert.Equal(t, "inner", string(got))
}

func TestFilesPut_OverwriteFails(t *testing.T) {
	name, filesDir := setupFilesTest(t)

	require.NoError(t, os.WriteFile(filepath.Join(filesDir, "exists.txt"), []byte("old"), 0600))

	src := filepath.Join(t.TempDir(), "exists.txt")
	require.NoError(t, os.WriteFile(src, []byte("new"), 0600))

	cmd := newFilesPutCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetArgs([]string{name, src})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestFilesPut_OverwriteWithForce(t *testing.T) {
	name, filesDir := setupFilesTest(t)

	require.NoError(t, os.WriteFile(filepath.Join(filesDir, "exists.txt"), []byte("old"), 0600))

	src := filepath.Join(t.TempDir(), "exists.txt")
	require.NoError(t, os.WriteFile(src, []byte("new"), 0600))

	cmd := newFilesPutCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetArgs([]string{name, "--force", src})
	require.NoError(t, cmd.Execute())

	got, err := os.ReadFile(filepath.Join(filesDir, "exists.txt")) //nolint:gosec // test helper
	require.NoError(t, err)
	assert.Equal(t, "new", string(got))
}

func TestFilesPut_MissingSource(t *testing.T) {
	name, _ := setupFilesTest(t)

	cmd := newFilesPutCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetArgs([]string{name, "/nonexistent/file.txt"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "source")
}

// --- get ---

func TestFilesGet_ToCwd(t *testing.T) {
	name, filesDir := setupFilesTest(t)
	require.NoError(t, os.WriteFile(filepath.Join(filesDir, "report.txt"), []byte("data"), 0600))

	dstDir := t.TempDir()

	cmd := newFilesGetCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{name, "report.txt", dstDir})
	require.NoError(t, cmd.Execute())

	got, err := os.ReadFile(filepath.Join(dstDir, "report.txt")) //nolint:gosec // test helper
	require.NoError(t, err)
	assert.Equal(t, "data", string(got))
}

func TestFilesGet_ToExplicitDst(t *testing.T) {
	name, filesDir := setupFilesTest(t)
	require.NoError(t, os.WriteFile(filepath.Join(filesDir, "report.txt"), []byte("data"), 0600))

	dstFile := filepath.Join(t.TempDir(), "output.txt")

	cmd := newFilesGetCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{name, "report.txt", dstFile})
	require.NoError(t, cmd.Execute())

	got, err := os.ReadFile(dstFile) //nolint:gosec // test helper
	require.NoError(t, err)
	assert.Equal(t, "data", string(got))
	assert.Contains(t, buf.String(), "output.txt")
}

func TestFilesGet_OverwriteFails(t *testing.T) {
	name, filesDir := setupFilesTest(t)
	require.NoError(t, os.WriteFile(filepath.Join(filesDir, "report.txt"), []byte("data"), 0600))

	dstFile := filepath.Join(t.TempDir(), "report.txt")
	require.NoError(t, os.WriteFile(dstFile, []byte("old"), 0600))

	cmd := newFilesGetCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetArgs([]string{name, "report.txt", dstFile})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestFilesGet_OverwriteWithForce(t *testing.T) {
	name, filesDir := setupFilesTest(t)
	require.NoError(t, os.WriteFile(filepath.Join(filesDir, "report.txt"), []byte("new"), 0600))

	dstFile := filepath.Join(t.TempDir(), "report.txt")
	require.NoError(t, os.WriteFile(dstFile, []byte("old"), 0600))

	cmd := newFilesGetCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetArgs([]string{name, "--force", "report.txt", dstFile})
	require.NoError(t, cmd.Execute())

	got, err := os.ReadFile(dstFile) //nolint:gosec // test helper
	require.NoError(t, err)
	assert.Equal(t, "new", string(got))
}

func TestFilesGet_MissingFile(t *testing.T) {
	name, _ := setupFilesTest(t)

	cmd := newFilesGetCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetArgs([]string{name, "nope.txt", t.TempDir()})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestFilesGet_PathTraversalBlocked(t *testing.T) {
	name, _ := setupFilesTest(t)

	cmd := newFilesGetCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetArgs([]string{name, "../../../etc/passwd", t.TempDir()})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes exchange directory")
}

// --- ls ---

func TestFilesLs_ImplicitGlob(t *testing.T) {
	name, filesDir := setupFilesTest(t)
	require.NoError(t, os.WriteFile(filepath.Join(filesDir, "a.txt"), []byte(""), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(filesDir, "b.txt"), []byte(""), 0600))

	cmd := newFilesLsCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{name})
	require.NoError(t, cmd.Execute())

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	assert.Equal(t, []string{"a.txt", "b.txt"}, lines)
}

func TestFilesLs_WithGlob(t *testing.T) {
	name, filesDir := setupFilesTest(t)
	require.NoError(t, os.WriteFile(filepath.Join(filesDir, "foo.log"), []byte(""), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(filesDir, "bar.txt"), []byte(""), 0600))

	cmd := newFilesLsCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{name, "*.log"})
	require.NoError(t, cmd.Execute())

	assert.Equal(t, "foo.log\n", buf.String())
}

func TestFilesLs_DotfilesIncluded(t *testing.T) {
	name, filesDir := setupFilesTest(t)
	require.NoError(t, os.WriteFile(filepath.Join(filesDir, ".hidden"), []byte(""), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(filesDir, "visible"), []byte(""), 0600))

	cmd := newFilesLsCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{name})
	require.NoError(t, cmd.Execute())

	out := buf.String()
	assert.Contains(t, out, ".hidden")
	assert.Contains(t, out, "visible")
}

func TestFilesLs_EmptyDir(t *testing.T) {
	name, _ := setupFilesTest(t)

	cmd := newFilesLsCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{name})
	require.NoError(t, cmd.Execute())

	assert.Empty(t, buf.String())
}

// --- rm ---

func TestFilesRm_MatchingFiles(t *testing.T) {
	name, filesDir := setupFilesTest(t)
	require.NoError(t, os.WriteFile(filepath.Join(filesDir, "a.log"), []byte(""), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(filesDir, "b.log"), []byte(""), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(filesDir, "keep.txt"), []byte(""), 0600))

	cmd := newFilesRmCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{name, "*.log"})
	require.NoError(t, cmd.Execute())

	out := buf.String()
	assert.Contains(t, out, "a.log")
	assert.Contains(t, out, "b.log")
	assert.NoFileExists(t, filepath.Join(filesDir, "a.log"))
	assert.NoFileExists(t, filepath.Join(filesDir, "b.log"))
	assert.FileExists(t, filepath.Join(filesDir, "keep.txt"))
}

func TestFilesRm_Directory(t *testing.T) {
	name, filesDir := setupFilesTest(t)
	subDir := filepath.Join(filesDir, "subdir")
	require.NoError(t, os.MkdirAll(subDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(subDir, "inner.txt"), []byte(""), 0600))

	cmd := newFilesRmCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{name, "subdir"})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, buf.String(), "subdir")
	assert.NoDirExists(t, subDir)
}

func TestFilesRm_NoMatches(t *testing.T) {
	name, _ := setupFilesTest(t)

	cmd := newFilesRmCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetArgs([]string{name, "*.nope"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no files match")
}

func TestFilesRm_ReadOnlyFile(t *testing.T) {
	name, filesDir := setupFilesTest(t)
	f := filepath.Join(filesDir, "readonly.txt")
	require.NoError(t, os.WriteFile(f, []byte("locked"), 0444)) //nolint:gosec // intentionally read-only for test

	cmd := newFilesRmCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{name, "readonly.txt"})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, buf.String(), "readonly.txt")
	assert.NoFileExists(t, f)
}

// --- path ---

func TestFilesPath(t *testing.T) {
	name, filesDir := setupFilesTest(t)

	cmd := newFilesPathCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{name})
	require.NoError(t, cmd.Execute())

	assert.Equal(t, filesDir+"\n", buf.String())
}

// --- validateExchangePath ---

func TestValidateExchangePath_Valid(t *testing.T) {
	assert.NoError(t, validateExchangePath("/a/b/files", "/a/b/files/foo.txt"))
	assert.NoError(t, validateExchangePath("/a/b/files", "/a/b/files"))
}

func TestValidateExchangePath_Traversal(t *testing.T) {
	assert.Error(t, validateExchangePath("/a/b/files", "/a/b/files/../secret"))
	assert.Error(t, validateExchangePath("/a/b/files", "/etc/passwd"))
}

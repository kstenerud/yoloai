package testutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// GoProject creates a temp directory containing a minimal Go project (main.go)
// with a git repository initialized and an initial commit. Returns the project dir.
func GoProject(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "project")
	require.NoError(t, os.MkdirAll(dir, 0750))
	WriteFile(t, dir, "main.go", "package main\n\nfunc main() {}\n")
	InitGitRepo(t, dir)
	GitAdd(t, dir, ".")
	GitCommit(t, dir, "initial commit")
	return dir
}

// AuxDir creates a temp directory named name containing a single data.txt file.
// Returns the directory path.
func AuxDir(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.MkdirAll(dir, 0750))
	WriteFile(t, dir, "data.txt", "aux data for "+name+"\n")
	return dir
}

// MultiFileProject creates a temp directory with several files and a committed
// git repository. Useful for tests that need a more realistic project layout.
func MultiFileProject(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "project")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "internal", "util"), 0750))

	WriteFile(t, dir, "main.go", "package main\n\nfunc main() {}\n")
	WriteFile(t, dir, "go.mod", "module example.com/project\n\ngo 1.22\n")
	WriteFile(t, dir, "README.md", "# Project\n")
	WriteFile(t, filepath.Join(dir, "internal", "util"), "util.go",
		"package util\n\n// Noop does nothing.\nfunc Noop() {}\n")

	InitGitRepo(t, dir)
	GitAdd(t, dir, ".")
	GitCommit(t, dir, "initial commit")
	return dir
}

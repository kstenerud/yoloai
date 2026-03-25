package workspace

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// CopyDir tests

func TestCopyDir_Basic(t *testing.T) {
	src := t.TempDir()
	writeTestFile(t, src, "file.txt", "hello")
	require.NoError(t, os.MkdirAll(filepath.Join(src, "sub"), 0750))
	writeTestFile(t, src, "sub/nested.txt", "world")

	dst := filepath.Join(t.TempDir(), "copy")
	require.NoError(t, CopyDir(src, dst))

	content, err := os.ReadFile(filepath.Join(dst, "file.txt")) //nolint:gosec
	require.NoError(t, err)
	assert.Equal(t, "hello", string(content))

	content, err = os.ReadFile(filepath.Join(dst, "sub", "nested.txt")) //nolint:gosec
	require.NoError(t, err)
	assert.Equal(t, "world", string(content))
}

func TestCopyDir_SourceMissing(t *testing.T) {
	err := CopyDir("/nonexistent/path", filepath.Join(t.TempDir(), "dst"))
	assert.Error(t, err)
}

func TestCopyDir_BrokenSymlink(t *testing.T) {
	src := t.TempDir()
	require.NoError(t, os.Symlink("/nonexistent/target", filepath.Join(src, "broken")))

	dst := filepath.Join(t.TempDir(), "copy")
	require.NoError(t, CopyDir(src, dst))

	link, err := os.Readlink(filepath.Join(dst, "broken"))
	require.NoError(t, err)
	assert.Equal(t, "/nonexistent/target", link)
}

func TestCopyDir_ValidSymlink(t *testing.T) {
	src := t.TempDir()
	writeTestFile(t, src, "real.txt", "content")
	require.NoError(t, os.Symlink("real.txt", filepath.Join(src, "link.txt")))

	dst := filepath.Join(t.TempDir(), "copy")
	require.NoError(t, CopyDir(src, dst))

	// Must be a symlink, not a regular file copy.
	fi, err := os.Lstat(filepath.Join(dst, "link.txt"))
	require.NoError(t, err)
	assert.NotZero(t, fi.Mode()&os.ModeSymlink, "should be a symlink")

	link, err := os.Readlink(filepath.Join(dst, "link.txt"))
	require.NoError(t, err)
	assert.Equal(t, "real.txt", link)
}

func TestCopyDir_RelativeSymlink(t *testing.T) {
	src := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(src, "sub"), 0750))
	writeTestFile(t, src, "sub/target.txt", "data")
	require.NoError(t, os.Symlink("sub/target.txt", filepath.Join(src, "rel")))

	dst := filepath.Join(t.TempDir(), "copy")
	require.NoError(t, CopyDir(src, dst))

	link, err := os.Readlink(filepath.Join(dst, "rel"))
	require.NoError(t, err)
	assert.Equal(t, "sub/target.txt", link)
}

func TestCopyDir_SymlinkToDirectory(t *testing.T) {
	src := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(src, "realdir"), 0750))
	writeTestFile(t, src, "realdir/file.txt", "inside")
	require.NoError(t, os.Symlink("realdir", filepath.Join(src, "linkdir")))

	dst := filepath.Join(t.TempDir(), "copy")
	require.NoError(t, CopyDir(src, dst))

	// linkdir should be a symlink, not a real directory.
	fi, err := os.Lstat(filepath.Join(dst, "linkdir"))
	require.NoError(t, err)
	assert.NotZero(t, fi.Mode()&os.ModeSymlink, "should be a symlink")

	link, err := os.Readlink(filepath.Join(dst, "linkdir"))
	require.NoError(t, err)
	assert.Equal(t, "realdir", link)
}

func TestCopyDir_PreservesPermissions(t *testing.T) {
	src := t.TempDir()
	f := filepath.Join(src, "exec.sh")
	require.NoError(t, os.WriteFile(f, []byte("#!/bin/sh"), 0755)) //nolint:gosec

	dst := filepath.Join(t.TempDir(), "copy")
	require.NoError(t, CopyDir(src, dst))

	fi, err := os.Stat(filepath.Join(dst, "exec.sh"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0755), fi.Mode().Perm())
}

func TestCopyDir_PreservesModTime(t *testing.T) {
	src := t.TempDir()
	writeTestFile(t, src, "file.txt", "hello")
	past := time.Date(2020, 6, 15, 12, 0, 0, 0, time.UTC)
	require.NoError(t, os.Chtimes(filepath.Join(src, "file.txt"), past, past))

	dst := filepath.Join(t.TempDir(), "copy")
	require.NoError(t, CopyDir(src, dst))

	fi, err := os.Stat(filepath.Join(dst, "file.txt"))
	require.NoError(t, err)
	assert.True(t, fi.ModTime().Equal(past), "mod time should be preserved, got %v", fi.ModTime())
}

func TestCopyDir_EmptyDirectory(t *testing.T) {
	src := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(src, "empty"), 0750))

	dst := filepath.Join(t.TempDir(), "copy")
	require.NoError(t, CopyDir(src, dst))

	fi, err := os.Stat(filepath.Join(dst, "empty"))
	require.NoError(t, err)
	assert.True(t, fi.IsDir(), "empty dir should be preserved")
}

func TestCopyDir_SourceNotDirectory(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file.txt")
	require.NoError(t, os.WriteFile(f, []byte("data"), 0600))

	err := CopyDir(f, filepath.Join(t.TempDir(), "dst"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not a directory")
}

func TestCopyDir_CloneIsolation(t *testing.T) {
	src := t.TempDir()
	writeTestFile(t, src, "file.txt", "original")
	require.NoError(t, os.MkdirAll(filepath.Join(src, "sub"), 0750))
	writeTestFile(t, src, "sub/nested.txt", "nested-original")

	dst := filepath.Join(t.TempDir(), "copy")
	require.NoError(t, CopyDir(src, dst))

	// Modify files in the copy.
	require.NoError(t, os.WriteFile(filepath.Join(dst, "file.txt"), []byte("modified"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dst, "sub", "nested.txt"), []byte("nested-modified"), 0600))

	// Original files must be unchanged (validates isolation for both
	// clonefile and regular copy paths).
	content, err := os.ReadFile(filepath.Join(src, "file.txt")) //nolint:gosec
	require.NoError(t, err)
	assert.Equal(t, "original", string(content))

	content, err = os.ReadFile(filepath.Join(src, "sub", "nested.txt")) //nolint:gosec
	require.NoError(t, err)
	assert.Equal(t, "nested-original", string(content))
}

func TestCopyDir_SkipsBugreportFiles(t *testing.T) {
	src := t.TempDir()
	writeTestFile(t, src, "file.txt", "hello")
	writeTestFile(t, src, "yoloai-bugreport-20260316-102123.534.md", "bugreport content")
	writeTestFile(t, src, "yoloai-bugreport-20260316-103627.211.md.tmp", "temp bugreport")
	writeTestFile(t, src, "yoloai-bugreport-20260315-140000.000.md", "another bugreport")
	require.NoError(t, os.MkdirAll(filepath.Join(src, "sub"), 0750))
	writeTestFile(t, src, "sub/yoloai-bugreport-20260314-120000.000.md", "nested bugreport")
	writeTestFile(t, src, "sub/yoloai-bugreport-20260314-120001.000.md.tmp", "nested temp bugreport")
	writeTestFile(t, src, "sub/nested.txt", "world")

	dst := filepath.Join(t.TempDir(), "copy")
	require.NoError(t, CopyDir(src, dst))

	// Normal files should be copied
	content, err := os.ReadFile(filepath.Join(dst, "file.txt")) //nolint:gosec
	require.NoError(t, err)
	assert.Equal(t, "hello", string(content))

	content, err = os.ReadFile(filepath.Join(dst, "sub", "nested.txt")) //nolint:gosec
	require.NoError(t, err)
	assert.Equal(t, "world", string(content))

	// Bugreport files (.md and .md.tmp) should NOT be copied
	_, err = os.ReadFile(filepath.Join(dst, "yoloai-bugreport-20260316-102123.534.md")) //nolint:gosec
	assert.Error(t, err, "bugreport .md file should not be copied")

	_, err = os.ReadFile(filepath.Join(dst, "yoloai-bugreport-20260316-103627.211.md.tmp")) //nolint:gosec
	assert.Error(t, err, "bugreport .md.tmp file should not be copied")

	_, err = os.ReadFile(filepath.Join(dst, "yoloai-bugreport-20260315-140000.000.md")) //nolint:gosec
	assert.Error(t, err, "bugreport .md file should not be copied")

	_, err = os.ReadFile(filepath.Join(dst, "sub", "yoloai-bugreport-20260314-120000.000.md")) //nolint:gosec
	assert.Error(t, err, "nested bugreport .md file should not be copied")

	_, err = os.ReadFile(filepath.Join(dst, "sub", "yoloai-bugreport-20260314-120001.000.md.tmp")) //nolint:gosec
	assert.Error(t, err, "nested bugreport .md.tmp file should not be copied")
}

func TestCopyDir_SkipsBuildArtifacts(t *testing.T) {
	src := t.TempDir()
	writeTestFile(t, src, "main.swift", "print(\"hello\")")
	writeTestFile(t, src, "package.json", "{}")

	// Swift Package Manager artifacts
	require.NoError(t, os.MkdirAll(filepath.Join(src, ".build", "x86_64-apple-macosx", "debug"), 0750))
	writeTestFile(t, src, ".build/x86_64-apple-macosx/debug/module.pcm", "PCH with hardcoded paths")

	// Xcode derived data
	require.NoError(t, os.MkdirAll(filepath.Join(src, "DerivedData", "MyApp", "Build"), 0750))
	writeTestFile(t, src, "DerivedData/MyApp/Build/cache.db", "build cache")

	// Node.js modules
	require.NoError(t, os.MkdirAll(filepath.Join(src, "node_modules", "lodash"), 0750))
	writeTestFile(t, src, "node_modules/lodash/index.js", "module.exports = {}")

	// Python cache
	require.NoError(t, os.MkdirAll(filepath.Join(src, "__pycache__"), 0750))
	writeTestFile(t, src, "__pycache__/module.cpython-39.pyc", "bytecode")

	// Xcode user data (nested pattern)
	require.NoError(t, os.MkdirAll(filepath.Join(src, "MyApp.xcworkspace", "xcuserdata", "user.xcuserdatad"), 0750))
	writeTestFile(t, src, "MyApp.xcworkspace/xcuserdata/user.xcuserdatad/UserInterfaceState.xcuserstate", "UI state")
	require.NoError(t, os.MkdirAll(filepath.Join(src, "MyApp.xcodeproj", "xcuserdata", "user.xcuserdatad"), 0750))
	writeTestFile(t, src, "MyApp.xcodeproj/xcuserdata/user.xcuserdatad/WorkspaceSettings.xcsettings", "settings")

	// Nested artifacts (should also be excluded)
	require.NoError(t, os.MkdirAll(filepath.Join(src, "subproject", "node_modules", "express"), 0750))
	writeTestFile(t, src, "subproject/node_modules/express/index.js", "express")
	require.NoError(t, os.MkdirAll(filepath.Join(src, "subproject", ".build", "debug"), 0750))
	writeTestFile(t, src, "subproject/.build/debug/app", "binary")

	dst := filepath.Join(t.TempDir(), "copy")
	require.NoError(t, CopyDir(src, dst))

	// Normal files should be copied
	content, err := os.ReadFile(filepath.Join(dst, "main.swift")) //nolint:gosec
	require.NoError(t, err)
	assert.Equal(t, "print(\"hello\")", string(content))

	content, err = os.ReadFile(filepath.Join(dst, "package.json")) //nolint:gosec
	require.NoError(t, err)
	assert.Equal(t, "{}", string(content))

	// Build artifacts should NOT be copied
	_, err = os.Stat(filepath.Join(dst, ".build"))
	assert.True(t, os.IsNotExist(err), ".build directory should not be copied")

	_, err = os.Stat(filepath.Join(dst, "DerivedData"))
	assert.True(t, os.IsNotExist(err), "DerivedData directory should not be copied")

	_, err = os.Stat(filepath.Join(dst, "node_modules"))
	assert.True(t, os.IsNotExist(err), "node_modules directory should not be copied")

	_, err = os.Stat(filepath.Join(dst, "__pycache__"))
	assert.True(t, os.IsNotExist(err), "__pycache__ directory should not be copied")

	_, err = os.Stat(filepath.Join(dst, "MyApp.xcworkspace", "xcuserdata"))
	assert.True(t, os.IsNotExist(err), "xcworkspace xcuserdata should not be copied")

	_, err = os.Stat(filepath.Join(dst, "MyApp.xcodeproj", "xcuserdata"))
	assert.True(t, os.IsNotExist(err), "xcodeproj xcuserdata should not be copied")

	// Nested artifacts should not be copied
	_, err = os.Stat(filepath.Join(dst, "subproject", "node_modules"))
	assert.True(t, os.IsNotExist(err), "nested node_modules should not be copied")

	_, err = os.Stat(filepath.Join(dst, "subproject", ".build"))
	assert.True(t, os.IsNotExist(err), "nested .build should not be copied")
}

func TestIsBuildArtifact_Directories(t *testing.T) {
	tests := []struct {
		path     string
		isDir    bool
		expected bool
		desc     string
	}{
		{".build", true, true, "Swift .build directory"},
		{".build/debug", true, true, "Swift .build subdirectory"},
		{"src/.build", true, true, "nested .build"},
		{"DerivedData", true, true, "Xcode DerivedData"},
		{"DerivedData/MyApp/Build", true, true, "DerivedData subdirectory"},
		{"node_modules", true, true, "Node.js node_modules"},
		{"node_modules/lodash", true, true, "node_modules package"},
		{"src/node_modules", true, true, "nested node_modules"},
		{"__pycache__", true, true, "Python cache"},
		{"src/__pycache__", true, true, "nested __pycache__"},
		{"src", true, false, "normal directory"},
		{"build", true, false, "lowercase build (too generic)"},
		{"Build", true, false, "capital Build (too generic)"},
		{"target", true, false, "target directory (too generic)"},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			result := isBuildArtifact(tt.path, tt.isDir)
			assert.Equal(t, tt.expected, result, "path: %s", tt.path)
		})
	}
}

func TestIsBuildArtifact_NestedPatterns(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
		desc     string
	}{
		{"MyApp.xcworkspace/xcuserdata", true, "xcworkspace xcuserdata"},
		{"MyApp.xcworkspace/xcuserdata/user.xcuserdatad", true, "xcworkspace xcuserdata subdirectory"},
		{"MyApp.xcworkspace/xcuserdata/user.xcuserdatad/UserInterfaceState.xcuserstate", true, "file inside xcuserdata (excluded)"},
		{"MyApp.xcodeproj/xcuserdata", true, "xcodeproj xcuserdata"},
		{"MyApp.xcodeproj/xcuserdata/user.xcuserdatad", true, "xcodeproj xcuserdata subdirectory"},
		{"sub/MyApp.xcworkspace/xcuserdata", true, "nested xcworkspace xcuserdata"},
		{"sub/MyApp.xcodeproj/xcuserdata", true, "nested xcodeproj xcuserdata"},
		{"MyApp.xcworkspace/project.pbxproj", false, "xcworkspace other file"},
		{"MyApp.xcodeproj/project.pbxproj", false, "xcodeproj other file"},
		{"xcuserdata", false, "xcuserdata without workspace/project"},
		{"random.xcworkspace/other", false, "xcworkspace without xcuserdata"},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			result := isBuildArtifact(tt.path, true)
			assert.Equal(t, tt.expected, result, "path: %s", tt.path)
		})
	}
}

func TestIsBuildArtifact_Files(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
		desc     string
	}{
		{"main.swift", false, "normal Swift file"},
		{"package.json", false, "normal package file"},
		{"src/file.txt", false, "normal nested file"},
		{".build/debug/module.pcm", true, "file inside .build"},
		{"node_modules/lodash/index.js", true, "file inside node_modules"},
		{"__pycache__/module.pyc", true, "file inside __pycache__"},
		{"DerivedData/cache.db", true, "file inside DerivedData"},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			result := isBuildArtifact(tt.path, false)
			assert.Equal(t, tt.expected, result, "path: %s", tt.path)
		})
	}
}

// RemoveGitDirs tests

func TestRemoveGitDirs_RemovesGitDirectory(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	require.NoError(t, os.MkdirAll(gitDir, 0750))
	writeTestFile(t, dir, ".git/HEAD", "ref: refs/heads/main")

	require.NoError(t, RemoveGitDirs(dir))

	_, err := os.Stat(gitDir)
	assert.True(t, os.IsNotExist(err), ".git directory should be removed")
}

func TestRemoveGitDirs_RemovesNestedGit(t *testing.T) {
	dir := t.TempDir()
	subGitDir := filepath.Join(dir, "sub", ".git")
	require.NoError(t, os.MkdirAll(subGitDir, 0750))
	writeTestFile(t, dir, "sub/.git/HEAD", "ref: refs/heads/main")

	require.NoError(t, RemoveGitDirs(dir))

	_, err := os.Stat(subGitDir)
	assert.True(t, os.IsNotExist(err), "nested .git directory should be removed")
}

func TestRemoveGitDirs_RemovesGitFile(t *testing.T) {
	dir := t.TempDir()
	// Worktree-style .git file (not a directory)
	writeTestFile(t, dir, ".git", "gitdir: /some/other/path")

	require.NoError(t, RemoveGitDirs(dir))

	_, err := os.Stat(filepath.Join(dir, ".git"))
	assert.True(t, os.IsNotExist(err), ".git file should be removed")
}

func TestRemoveGitDirs_PreservesOtherFiles(t *testing.T) {
	dir := t.TempDir()
	// Create .git dir and non-git files
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0750))
	writeTestFile(t, dir, ".git/HEAD", "ref: refs/heads/main")
	writeTestFile(t, dir, "file.txt", "keep me")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0750))
	writeTestFile(t, dir, "sub/other.txt", "keep me too")

	require.NoError(t, RemoveGitDirs(dir))

	// .git should be gone
	_, err := os.Stat(filepath.Join(dir, ".git"))
	assert.True(t, os.IsNotExist(err), ".git directory should be removed")

	// Other files should be preserved
	content, err := os.ReadFile(filepath.Join(dir, "file.txt")) //nolint:gosec
	require.NoError(t, err)
	assert.Equal(t, "keep me", string(content))

	content, err = os.ReadFile(filepath.Join(dir, "sub", "other.txt")) //nolint:gosec
	require.NoError(t, err)
	assert.Equal(t, "keep me too", string(content))
}

func TestRemoveGitDirs_NoopWhenNoGit(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "file.txt", "hello")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0750))
	writeTestFile(t, dir, "sub/other.txt", "world")

	err := RemoveGitDirs(dir)
	assert.NoError(t, err)

	// All files should still be present
	content, err := os.ReadFile(filepath.Join(dir, "file.txt")) //nolint:gosec
	require.NoError(t, err)
	assert.Equal(t, "hello", string(content))

	content, err = os.ReadFile(filepath.Join(dir, "sub", "other.txt")) //nolint:gosec
	require.NoError(t, err)
	assert.Equal(t, "world", string(content))
}

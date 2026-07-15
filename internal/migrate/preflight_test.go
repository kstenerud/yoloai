// ABOUTME: Tests SameFilesystem: same-device detection for same/trivial path
// ABOUTME: counts and a missing-path error.
package migrate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSameFilesystem_SamePaths(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := SameFilesystem(dir, sub); err != nil {
		t.Errorf("SameFilesystem on one FS: %v", err)
	}
}

func TestSameFilesystem_TrivialLengths(t *testing.T) {
	if err := SameFilesystem(); err != nil {
		t.Errorf("no paths: %v", err)
	}
	if err := SameFilesystem(t.TempDir()); err != nil {
		t.Errorf("single path: %v", err)
	}
}

func TestSameFilesystem_MissingPath(t *testing.T) {
	dir := t.TempDir()
	if err := SameFilesystem(dir, filepath.Join(dir, "does-not-exist")); err == nil {
		t.Error("expected error for missing path, got nil")
	}
}

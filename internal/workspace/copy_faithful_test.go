package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyPathFaithful_File(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	if err := os.WriteFile(src, []byte("payload"), 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}
	dst := filepath.Join(dir, "dst.txt")
	if err := CopyPathFaithful(src, dst); err != nil {
		t.Fatalf("CopyPathFaithful: %v", err)
	}
	got, err := os.ReadFile(dst) //nolint:gosec // test path
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(got) != "payload" {
		t.Errorf("content = %q, want %q", got, "payload")
	}
}

func TestCopyPathFaithful_Symlink(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "link")
	if err := os.Symlink("target/path", src); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	dst := filepath.Join(dir, "copied-link")
	if err := CopyPathFaithful(src, dst); err != nil {
		t.Fatalf("CopyPathFaithful: %v", err)
	}
	info, err := os.Lstat(dst)
	if err != nil {
		t.Fatalf("lstat dst: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("dst is not a symlink")
	}
	target, err := os.Readlink(dst)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if target != "target/path" {
		t.Errorf("link target = %q, want %q", target, "target/path")
	}
}

// The defining difference from CopyDir: nothing is filtered. A tree containing
// build artifacts, a bugreport, and a .git dir must be reproduced verbatim.
func TestCopyPathFaithful_PreservesEverything(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "tree")
	mustMkdir(t, filepath.Join(src, "node_modules"))
	mustMkdir(t, filepath.Join(src, ".git"))
	mustWrite(t, filepath.Join(src, "node_modules", "dep.js"), "x")
	mustWrite(t, filepath.Join(src, ".git", "HEAD"), "ref: refs/heads/main")
	mustWrite(t, filepath.Join(src, "yoloai-bugreport-123.md"), "report")
	mustWrite(t, filepath.Join(src, "keep.txt"), "keep")

	dst := filepath.Join(dir, "copy")
	if err := CopyPathFaithful(src, dst); err != nil {
		t.Fatalf("CopyPathFaithful: %v", err)
	}
	for _, rel := range []string{
		"node_modules/dep.js",
		".git/HEAD",
		"yoloai-bugreport-123.md",
		"keep.txt",
	} {
		if _, err := os.Stat(filepath.Join(dst, rel)); err != nil {
			t.Errorf("missing %s in faithful copy: %v", rel, err)
		}
	}
}

func TestCopyPathFaithful_PreservesPermissions(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "exec.sh")
	if err := os.WriteFile(src, []byte("#!/bin/sh\n"), 0o755); err != nil { //nolint:gosec // G306: deliberately testing exec-bit preservation
		t.Fatalf("write: %v", err)
	}
	dst := filepath.Join(dir, "exec-copy.sh")
	if err := CopyPathFaithful(src, dst); err != nil {
		t.Fatalf("CopyPathFaithful: %v", err)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("perm = %o, want 0755", info.Mode().Perm())
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

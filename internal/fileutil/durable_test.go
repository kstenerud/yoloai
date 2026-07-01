package fileutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicWriteFile_WritesContentAndPerm(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.txt")
	if err := AtomicWriteFile(path, []byte("hello"), 0o640); err != nil {
		t.Fatalf("AtomicWriteFile: %v", err)
	}

	got, err := os.ReadFile(path) //nolint:gosec // test path
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("content = %q, want %q", got, "hello")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// Chmod is explicit, so the umask must not have masked bits off.
	if info.Mode().Perm() != 0o640 {
		t.Errorf("perm = %o, want %o", info.Mode().Perm(), 0o640)
	}
}

func TestAtomicWriteFile_OverwritesAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.txt")
	if err := AtomicWriteFile(path, []byte("first"), 0o644); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := AtomicWriteFile(path, []byte("second-longer"), 0o644); err != nil {
		t.Fatalf("second write: %v", err)
	}

	got, err := os.ReadFile(path) //nolint:gosec // test path
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "second-longer" {
		t.Errorf("content = %q, want %q", got, "second-longer")
	}
}

func TestAtomicWriteFile_NoLeftoverTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.txt")
	if err := AtomicWriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("AtomicWriteFile: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "data.txt" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("dir entries = %v, want just [data.txt] (temp file leaked)", names)
	}
}

func TestAtomicWriteJSON_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obj.json")
	type payload struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	if err := AtomicWriteJSON(path, payload{Name: "sb", Count: 3}, 0o600); err != nil {
		t.Fatalf("AtomicWriteJSON: %v", err)
	}
	got, err := os.ReadFile(path) //nolint:gosec // test path
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	want := "{\n  \"name\": \"sb\",\n  \"count\": 3\n}"
	if string(got) != want {
		t.Errorf("json = %q, want %q", got, want)
	}
}

func TestFsyncDir_OK(t *testing.T) {
	if err := FsyncDir(t.TempDir()); err != nil {
		t.Errorf("FsyncDir on existing dir: %v", err)
	}
}

func TestFsyncDir_Missing(t *testing.T) {
	if err := FsyncDir(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("FsyncDir on missing dir: want error, got nil")
	}
}

func TestFsyncTree_OK(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0o600); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sub, "b.txt"), []byte("b"), 0o600); err != nil {
		t.Fatalf("write b: %v", err)
	}
	// A symlink must be skipped, not followed/fsynced.
	if err := os.Symlink("a.txt", filepath.Join(root, "link")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := FsyncTree(root); err != nil {
		t.Errorf("FsyncTree: %v", err)
	}
}

func TestFsyncTree_Missing(t *testing.T) {
	if err := FsyncTree(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("FsyncTree on missing root: want error, got nil")
	}
}

// ABOUTME: Tests for OwnerUID and ScanWrongOwner — the ownership-audit
// primitives behind `yoloai doctor`'s root-owned-leftover detection.

//go:build !windows

package fileutil

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestOwnerUID_ReportsCurrentUser(t *testing.T) {
	f := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(f)
	if err != nil {
		t.Fatal(err)
	}
	uid, ok := OwnerUID(info)
	if !ok {
		t.Fatal("OwnerUID reported unavailable on a unix build")
	}
	if uid != os.Getuid() {
		t.Fatalf("owner uid = %d, want current uid %d", uid, os.Getuid())
	}
}

func TestScanWrongOwner_CleanTreeHasNoConcern(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "a", "b"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a", "b", "f"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Every entry is owned by the running user; scanning for that uid finds nothing.
	scan, err := ScanWrongOwner(context.Background(), root, os.Getuid(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if scan.Count != 0 {
		t.Fatalf("clean tree reported %d wrong-owned entries: %v", scan.Count, scan.Sample)
	}
}

func TestScanWrongOwner_FlagsEntriesNotOwnedByWantUID(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "mine"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// We can't chown to root without privilege, so invert the test: scan for a
	// uid nobody owns (current uid + 1), so every entry counts as wrong-owned.
	// This exercises the same predicate the doctor uses (owner != wantUID).
	scan, err := ScanWrongOwner(context.Background(), root, os.Getuid()+1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if scan.Count == 0 {
		t.Fatal("expected the current-user files to count as wrong-owned for a foreign wantUID")
	}
	if len(scan.Sample) == 0 {
		t.Fatal("expected at least one sampled offending path")
	}
}

func TestScanWrongOwner_SkipsWrongOwnedSubtreeAndCapsSample(t *testing.T) {
	root := t.TempDir()
	// A directory plus files under it. Scanning for a foreign uid, the top dir is
	// wrong-owned, so it must be counted ONCE and its children not descended.
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a", "b", "c"} {
		if err := os.WriteFile(filepath.Join(sub, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// wantUID foreign → root itself is wrong-owned and skipped, so Count is 1
	// (the root), not 1 dir + 3 files.
	scan, err := ScanWrongOwner(context.Background(), root, os.Getuid()+1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if scan.Count != 1 {
		t.Fatalf("Count = %d, want 1 (wrong-owned root skipped as a single problem)", scan.Count)
	}
	if len(scan.Sample) != 1 {
		t.Fatalf("Sample len = %d, want it capped at 1", len(scan.Sample))
	}
}

func TestScanWrongOwner_MissingRootIsNotAnError(t *testing.T) {
	scan, err := ScanWrongOwner(context.Background(), filepath.Join(t.TempDir(), "nope"), os.Getuid(), 10)
	if err != nil {
		t.Fatalf("missing root should be benign, got %v", err)
	}
	if scan.Count != 0 {
		t.Fatalf("missing root should yield no concern, got %d", scan.Count)
	}
}

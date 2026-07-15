// ABOUTME: Tests the crash-safe Promotion state machine
// ABOUTME: (build/orig/new/repopulate/marker/swap/dispose): happy path,
// ABOUTME: trash-vs-drop disposal, resuming after an injected crash at every
// ABOUTME: boundary, discarding partial builds, promoting a
// ABOUTME: ready-but-incomplete build, and halting on unreachable state.
package migrate

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/internal/fileutil"
)

// A realm-like fake: Build rebuilds data.txt whole; keep.txt and the
// .schema_version marker are repopulated from the original; WriteReadyMarker
// stamps the version last. This exercises the whole promoter without any
// overlay/sandbox specifics.
func fakeMigration(parent, name, scratch string, dispose DisposeFunc) Promotion {
	return Promotion{
		Parent:     parent,
		Name:       name,
		ScratchDir: scratch,
		Build: func(dst string) error {
			return os.WriteFile(filepath.Join(dst, "data.txt"), []byte("new"), 0o600)
		},
		WriteReadyMarker: func(newDir string) error {
			return fileutil.AtomicWriteFile(filepath.Join(newDir, ".schema_version"), []byte("2"), 0o600)
		},
		IsReady: func(dir string) (bool, error) {
			b, err := os.ReadFile(filepath.Join(dir, ".schema_version")) //nolint:gosec // test path
			if errors.Is(err, fs.ErrNotExist) {
				return false, nil
			}
			if err != nil {
				return false, err
			}
			return strings.TrimSpace(string(b)) == "2", nil
		},
		DisposeOrig: dispose,
	}
}

// setupUnit creates a home with an initial (pre-migration) unit and a fresh
// scratch dir, returning the promotion ready to Run.
func setupUnit(t *testing.T, dispose DisposeFunc) (home string, p Promotion) {
	t.Helper()
	home = t.TempDir()
	name := "myunit"
	unit := filepath.Join(home, name)
	if err := os.MkdirAll(unit, 0o750); err != nil {
		t.Fatalf("mkdir unit: %v", err)
	}
	writeFile(t, filepath.Join(unit, "data.txt"), "old")
	writeFile(t, filepath.Join(unit, "keep.txt"), "keep")
	writeFile(t, filepath.Join(unit, ".schema_version"), "1")
	scratch := ScratchPath(home)
	if err := os.MkdirAll(scratch, 0o750); err != nil {
		t.Fatalf("create scratch: %v", err)
	}
	return home, fakeMigration(home, name, scratch, dispose)
}

// assertMigrated verifies the committed final state: data rebuilt, unchanged
// data carried over, marker stamped, and no sentinels/temps left behind.
func assertMigrated(t *testing.T, p Promotion) {
	t.Helper()
	if got := readFile(t, filepath.Join(p.canonical(), "data.txt")); got != "new" {
		t.Errorf("data.txt = %q, want %q", got, "new")
	}
	if got := readFile(t, filepath.Join(p.canonical(), "keep.txt")); got != "keep" {
		t.Errorf("keep.txt = %q, want %q (repopulate lost it)", got, "keep")
	}
	if got := readFile(t, filepath.Join(p.canonical(), ".schema_version")); got != "2" {
		t.Errorf(".schema_version = %q, want %q", got, "2")
	}
	if exists(p.orig()) {
		t.Error("U_^^_orig still present after commit")
	}
	if exists(p.newer()) {
		t.Error("U_^^_new still present after commit")
	}
	if exists(filepath.Join(p.canonical(), buildCompleteName)) {
		t.Error("build-complete sentinel leaked into the committed unit")
	}
	entries, err := os.ReadDir(p.canonical())
	if err != nil {
		t.Fatalf("read committed unit: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), repopTempPrefix) {
			t.Errorf("repopulate temp leaked: %s", e.Name())
		}
	}
}

func TestPromotion_HappyPath(t *testing.T) {
	t.Cleanup(func() { crashAfter = nil })
	_, p := setupUnit(t, DropDisposer)
	if err := p.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertMigrated(t, p)
}

func TestPromotion_TrashDisposerKeepsOrig(t *testing.T) {
	t.Cleanup(func() { crashAfter = nil })
	home, p := setupUnit(t, nil)
	trash := filepath.Join(home, "trash")
	p.DisposeOrig = TrashDisposer(trash)
	if err := p.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertMigrated(t, p)
	// The prior generation's complete data survives in trash for manual revert.
	if got := readFile(t, filepath.Join(trash, "myunit"+suffixOrig, "data.txt")); got != "old" {
		t.Errorf("trashed data.txt = %q, want %q", got, "old")
	}
}

// The core guarantee: a crash injected at EVERY rename/write boundary must,
// after a plain re-run, converge to the correct committed state with no data
// lost.
func TestPromotion_ResumesFromEveryCrashBoundary(t *testing.T) {
	forwardBoundaries := []string{"build", "orig", "new", "repopulate", "marker", "pre-promote-swap", "promote", "dispose"}
	for _, boundary := range forwardBoundaries {
		t.Run(boundary, func(t *testing.T) {
			t.Cleanup(func() { crashAfter = nil })
			_, p := setupUnit(t, DropDisposer)

			// First run: die right after the named boundary.
			crashAfter = failAt(boundary)
			err := p.Run()
			if err == nil {
				t.Fatalf("expected injected crash at %q, got nil", boundary)
			}
			if !errors.Is(err, errInjected) {
				t.Fatalf("crash at %q surfaced unexpected error: %v", boundary, err)
			}

			// Recovery run: no injection, must converge.
			crashAfter = nil
			if err := p.Run(); err != nil {
				t.Fatalf("recovery run after crash at %q: %v", boundary, err)
			}
			assertMigrated(t, p)
		})
	}
}

// A build that never reached its build-complete sentinel (a partial move-in)
// must be rolled back — discarded and the original restored — never promoted.
func TestPromotion_DiscardsPartialBuild(t *testing.T) {
	t.Cleanup(func() { crashAfter = nil })
	_, p := setupUnit(t, DropDisposer)

	// Hand-craft the {orig, new-without-build-complete} crash state: U renamed
	// to orig, a partial new dir staged but its sentinel never written.
	if err := os.Rename(p.canonical(), p.orig()); err != nil {
		t.Fatalf("stage orig: %v", err)
	}
	if err := os.MkdirAll(p.newer(), 0o750); err != nil {
		t.Fatalf("stage partial new: %v", err)
	}
	writeFile(t, filepath.Join(p.newer(), "data.txt"), "halfnew")

	if err := p.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The partial "halfnew" was discarded; the migration re-ran cleanly from the
	// intact original, so the committed data is the legitimate build.
	assertMigrated(t, p)
}

func TestPromotion_RestoresFromOrigOnly(t *testing.T) {
	t.Cleanup(func() { crashAfter = nil })
	_, p := setupUnit(t, DropDisposer)
	// {orig} alone: crashed between U->orig and the built move-in.
	if err := os.Rename(p.canonical(), p.orig()); err != nil {
		t.Fatalf("stage orig: %v", err)
	}
	if err := p.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertMigrated(t, p)
}

// Regression for the H1 recovery bug: a crash in promoteReady between removing
// the build-complete sentinel and the swap leaves {orig, new} where new is
// READY but lacks build-complete. That new dir is a finished build and must be
// promoted, not misread as a partial build and discarded. An orphaned
// marker-write temp in new must also be swept, not carried into the commit.
func TestPromotion_PromotesReadyBuildMissingBuildComplete(t *testing.T) {
	t.Cleanup(func() { crashAfter = nil })
	_, p := setupUnit(t, DropDisposer)

	// Stage the exact post-crash state: U -> orig, and a ready new (marker
	// written, repopulated) with the build-complete sentinel already removed.
	if err := os.Rename(p.canonical(), p.orig()); err != nil {
		t.Fatalf("stage orig: %v", err)
	}
	if err := os.MkdirAll(p.newer(), 0o750); err != nil {
		t.Fatalf("stage new: %v", err)
	}
	writeFile(t, filepath.Join(p.newer(), "data.txt"), "promoted-new")
	writeFile(t, filepath.Join(p.newer(), "keep.txt"), "keep")
	writeFile(t, filepath.Join(p.newer(), ".schema_version"), "2")
	// An orphaned AtomicWriteFile temp from an interrupted marker write
	// (".<base>.tmp-*", so "..schema_version.tmp-*").
	writeFile(t, filepath.Join(p.newer(), "..schema_version.tmp-abcd"), "2")

	if err := p.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Promoted the finished build verbatim — NOT discarded and rebuilt, which
	// would yield Build's "new" rather than the staged "promoted-new".
	if got := readFile(t, filepath.Join(p.canonical(), "data.txt")); got != "promoted-new" {
		t.Errorf("data.txt = %q, want %q (ready build was discarded, not promoted)", got, "promoted-new")
	}
	if got := readFile(t, filepath.Join(p.canonical(), ".schema_version")); got != "2" {
		t.Errorf(".schema_version = %q, want %q", got, "2")
	}
	// The orphaned marker temp was swept before the swap.
	for _, e := range mustReadDir(t, p.canonical()) {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("orphaned write temp leaked into commit: %s", e.Name())
		}
	}
	if exists(p.orig()) || exists(p.newer()) {
		t.Error("sentinel dir left after commit")
	}
}

// The recovery-only boundaries (discard-new, restore) must themselves be
// crash-resumable: a second crash while rolling back a partial build, or while
// restoring the original, still converges on a plain re-run.
func TestPromotion_ResumesFromRecoveryBoundaries(t *testing.T) {
	t.Run("discard-new", func(t *testing.T) {
		t.Cleanup(func() { crashAfter = nil })
		_, p := setupUnit(t, DropDisposer)
		// {orig, partial new (no build-complete, not ready)} -> discardPartialAndRestore.
		if err := os.Rename(p.canonical(), p.orig()); err != nil {
			t.Fatalf("stage orig: %v", err)
		}
		if err := os.MkdirAll(p.newer(), 0o750); err != nil {
			t.Fatalf("stage partial new: %v", err)
		}
		writeFile(t, filepath.Join(p.newer(), "data.txt"), "halfnew")

		crashAfter = failAt("discard-new")
		if err := p.Run(); !errors.Is(err, errInjected) {
			t.Fatalf("expected injected crash at discard-new, got %v", err)
		}
		crashAfter = nil
		if err := p.Run(); err != nil {
			t.Fatalf("recovery run after discard-new crash: %v", err)
		}
		assertMigrated(t, p)
	})
	t.Run("restore", func(t *testing.T) {
		t.Cleanup(func() { crashAfter = nil })
		_, p := setupUnit(t, DropDisposer)
		// {orig} alone -> restoreFromOrig.
		if err := os.Rename(p.canonical(), p.orig()); err != nil {
			t.Fatalf("stage orig: %v", err)
		}
		crashAfter = failAt("restore")
		if err := p.Run(); !errors.Is(err, errInjected) {
			t.Fatalf("expected injected crash at restore, got %v", err)
		}
		crashAfter = nil
		if err := p.Run(); err != nil {
			t.Fatalf("recovery run after restore crash: %v", err)
		}
		assertMigrated(t, p)
	})
}

func TestPromotion_HaltsOnCorruptState(t *testing.T) {
	t.Cleanup(func() { crashAfter = nil })
	for _, tc := range []struct {
		name  string
		stage func(p Promotion)
	}{
		{"U and new coexist", func(p Promotion) {
			// U left in place while a new build also exists — the invariant says
			// they never coexist.
			_ = os.MkdirAll(p.newer(), 0o750)
		}},
		{"new alone", func(p Promotion) {
			_ = os.RemoveAll(p.canonical())
			_ = os.MkdirAll(p.newer(), 0o750)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, p := setupUnit(t, DropDisposer)
			tc.stage(p)
			err := p.Run()
			if err == nil {
				t.Fatal("expected halt on corrupt state, got nil")
			}
			if !strings.Contains(err.Error(), "unreachable") {
				t.Errorf("error = %v, want it to mention an unreachable state", err)
			}
		})
	}
}

var errInjected = errors.New("injected crash")

func failAt(target string) func(string) error {
	return func(step string) error {
		if step == target {
			return errInjected
		}
		return nil
	}
}

func mustReadDir(t *testing.T, dir string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	return entries
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // test path
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.TrimSpace(string(b))
}

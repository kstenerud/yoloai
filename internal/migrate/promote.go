// ABOUTME: The resumable atomic-rename promotion primitive — build-new ->
// ABOUTME: repopulate -> swap, with an exhaustive crash-recovery classifier.
package migrate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/workspace"
)

// The reserved sentinel token. The "^^" bytes (0x5E) are illegal in any real
// realm or sandbox name — verified F-7: sandbox names go through
// config.ParseSandboxName (the containerd grammar, no "^"), realm/top-level unit
// names are fixed "^"-free constants — so these suffixes never collide with live
// data. Only the live dir uses them.
const (
	sentinelToken = "_^^_"
	suffixOrig    = "_^^_orig"
	suffixNew     = "_^^_new"

	// buildCompleteName marks a fully-built, contents-fsynced new dir. Written
	// last in scratch (after FsyncTree), so a new dir lacking it is a partial
	// build/move-in and must be discarded, never promoted.
	buildCompleteName = ".yoloai-build-complete"
	// repopTempPrefix names in-progress per-entry repopulate copies, so a crash
	// mid-copy leaves only a temp (cleaned on resume), never a half-copied entry
	// under its real name that the structural filter would then skip.
	repopTempPrefix = ".yoloai-repop-"

	maxResolveSteps = 16
)

// DisposeFunc decides what happens to the displaced original (U_^^_orig) after
// a unit commits. TrashDisposer preserves it for a manual revert (the default);
// DropDisposer deletes it (the overlay flatten, whose orig is redundant with
// the new copy and secret-bearing).
type DisposeFunc func(origPath string) error

// Promotion promotes a freshly-built unit into place atomically and durably,
// leaving the displaced original as a one-generation backup, and is resumable
// across a crash at any rename boundary. It is realm-agnostic: the caller
// injects what "changed" means (Build), what the durable ready marker is
// (WriteReadyMarker / IsReady), and how to dispose the original (DisposeOrig).
//
// A "unit" U is a directory (a sandbox, or a realm subtree) inside Parent. The
// invariant that makes recovery a pure classifier: U and U_^^_new NEVER coexist
// — U becomes U_^^_orig before the new build is placed — so the complete data
// always lives under exactly one of {U, U_^^_orig}.
type Promotion struct {
	// Parent is the live dir that contains the unit and its sentinels.
	Parent string
	// Name is the unit's canonical directory name (e.g. "library", "mysbx").
	Name string
	// ScratchDir is an empty dir on the SAME filesystem as Parent, into which
	// Build writes and which is then moved in as U_^^_new. Disposable.
	ScratchDir string
	// Build populates dst with the unit's *changed* top-level entries, each
	// rebuilt whole. Unchanged entries are repopulated from the displaced
	// original by the promoter and must NOT be produced here. dst is emptied
	// and recreated before each call.
	Build func(dst string) error
	// WriteReadyMarker writes the realm-specific ready marker durably into the
	// built new dir, as the final step before the promoting rename (e.g. a
	// realm unit writes .schema_version; a per-sandbox unit flips Mode->copy in
	// environment.json). Its presence authorizes promotion.
	WriteReadyMarker func(newDir string) error
	// IsReady reports whether dir carries a durable ready marker. It is also the
	// done-vs-not-started discriminator for a lone U (a migrated U is "ready").
	IsReady func(dir string) (bool, error)
	// DisposeOrig disposes U_^^_orig after commit.
	DisposeOrig DisposeFunc
}

func (p Promotion) canonical() string { return filepath.Join(p.Parent, p.Name) }
func (p Promotion) orig() string      { return filepath.Join(p.Parent, p.Name+suffixOrig) }
func (p Promotion) newer() string     { return filepath.Join(p.Parent, p.Name+suffixNew) }

type promotionState int

const (
	stateInitial        promotionState = iota // {U}, not ready -> build & promote
	stateDone                                 // {U}, ready     -> nothing to do
	stateOrigOnly                             // {orig}         -> restore U, retry
	stateOrigNew                              // {orig,new}     -> discard/resume/promote
	stateDisposePending                       // {U,orig}       -> dispose orig
	stateCorrupt                              // any other set  -> halt
)

// Run drives the unit to its committed final state, whether starting fresh
// ({U} not ready) or resuming after a crash (a sentinel present). It re-reads
// the live-dir names on each iteration and takes exactly the recovery action
// that state calls for — never blindly "completing forward": a build that never
// reached its build-complete sentinel is rolled back, not published.
func (p Promotion) Run() error {
	for range maxResolveSteps {
		st, err := p.classify()
		if err != nil {
			return err
		}
		switch st {
		case stateDone:
			return nil
		case stateInitial:
			err = p.buildAndStage()
		case stateOrigOnly:
			err = p.restoreFromOrig()
		case stateOrigNew:
			err = p.resolveOrigNew()
		case stateDisposePending:
			err = p.dispose()
		case stateCorrupt:
			return fmt.Errorf("migration promotion for %q is in an unreachable on-disk state "+
				"(an unexpected %q sentinel or a live unit beside its new build); halting to avoid data loss",
				p.Name, sentinelToken)
		}
		if err != nil {
			return err
		}
	}
	return fmt.Errorf("migration promotion for %q did not converge after %d steps", p.Name, maxResolveSteps)
}

// classify maps the on-disk name set of {U, U_^^_orig, U_^^_new} to a state,
// exhaustively over all 8 subsets. The unreachable subsets ({new} alone, any
// containing both U and new, and {}) collapse to stateCorrupt.
func (p Promotion) classify() (promotionState, error) {
	u := exists(p.canonical())
	o := exists(p.orig())
	n := exists(p.newer())
	switch {
	case u && !o && !n:
		return p.classifyLoneUnit()
	case !u && o && !n:
		return stateOrigOnly, nil
	case !u && o && n:
		return stateOrigNew, nil
	case u && o && !n:
		return stateDisposePending, nil
	default:
		// {U,new}, {new}, {U,orig,new}, {} — all violate "U and U_^^_new never
		// coexist" or "the unit always exists".
		return stateCorrupt, nil
	}
}

// classifyLoneUnit disambiguates a lone canonical U: a migrated unit satisfies
// the ready predicate (stateDone); an un-migrated one does not (stateInitial).
func (p Promotion) classifyLoneUnit() (promotionState, error) {
	ready, err := p.IsReady(p.canonical())
	if err != nil {
		return 0, fmt.Errorf("read ready marker of %q: %w", p.Name, err)
	}
	if ready {
		return stateDone, nil
	}
	return stateInitial, nil
}

// buildAndStage runs promotion steps 1-3 from a clean {U}: build the changed
// entries in scratch (fsync contents, then the build-complete sentinel last),
// rename U -> U_^^_orig, then move the built dir in as U_^^_new. It then
// returns; Run re-classifies to stateOrigNew and finishes via resolveOrigNew,
// so the build path and the crash-recovery path share one implementation.
func (p Promotion) buildAndStage() error {
	// Step 1: build in scratch. Start from a clean scratch each time.
	if err := os.RemoveAll(p.ScratchDir); err != nil {
		return fmt.Errorf("clear scratch: %w", err)
	}
	if err := fileutil.MkdirAll(p.ScratchDir, 0o750); err != nil {
		return fmt.Errorf("create scratch: %w", err)
	}
	if err := p.Build(p.ScratchDir); err != nil {
		return fmt.Errorf("build %q: %w", p.Name, err)
	}
	if err := fileutil.FsyncTree(p.ScratchDir); err != nil {
		return fmt.Errorf("fsync built %q: %w", p.Name, err)
	}
	if err := writeBuildComplete(p.ScratchDir); err != nil {
		return err
	}
	if err := checkpoint("build"); err != nil {
		return err
	}
	// Step 2: U -> U_^^_orig.
	if err := os.Rename(p.canonical(), p.orig()); err != nil {
		return fmt.Errorf("rename %q to orig: %w", p.Name, err)
	}
	if err := fileutil.FsyncDir(p.Parent); err != nil {
		return err
	}
	if err := checkpoint("orig"); err != nil {
		return err
	}
	// Step 3: built scratch -> U_^^_new.
	if err := os.Rename(p.ScratchDir, p.newer()); err != nil {
		return fmt.Errorf("move built %q into place: %w", p.Name, err)
	}
	if err := fileutil.FsyncDir(p.Parent); err != nil {
		return err
	}
	return checkpoint("new")
}

// resolveOrigNew handles the {U_^^_orig, U_^^_new} state — the heart of both the
// forward path and recovery. It discards a partial build, resumes an
// interrupted repopulate/marker, or promotes a ready build.
func (p Promotion) resolveOrigNew() error {
	complete, err := hasBuildComplete(p.newer())
	if err != nil {
		return err
	}
	if !complete {
		return p.discardPartialAndRestore()
	}
	ready, err := p.IsReady(p.newer())
	if err != nil {
		return fmt.Errorf("read ready marker of new %q: %w", p.Name, err)
	}
	if !ready {
		return p.repopulateAndMark()
	}
	return p.promoteReady()
}

// discardPartialAndRestore rolls back a new dir that never reached its
// build-complete sentinel: discard U_^^_new FIRST, then restore U from the
// still-intact orig. The order is load-bearing — removing new before restoring
// U means a crash here cannot manufacture the illegal {U, U_^^_new} set.
func (p Promotion) discardPartialAndRestore() error {
	if err := os.RemoveAll(p.newer()); err != nil {
		return fmt.Errorf("discard partial new %q: %w", p.Name, err)
	}
	if err := fileutil.FsyncDir(p.Parent); err != nil {
		return err
	}
	if err := checkpoint("discard-new"); err != nil {
		return err
	}
	return p.restoreFromOrig()
}

// repopulateAndMark runs steps 4-5: repopulate the unchanged entries from the
// intact orig, then write the ready marker last. Both are idempotent, so a
// crash re-runs them cleanly.
func (p Promotion) repopulateAndMark() error {
	if err := repopulate(p.orig(), p.newer()); err != nil {
		return err
	}
	if err := fileutil.FsyncDir(p.newer()); err != nil {
		return err
	}
	if err := checkpoint("repopulate"); err != nil {
		return err
	}
	if err := p.WriteReadyMarker(p.newer()); err != nil {
		return fmt.Errorf("write ready marker %q: %w", p.Name, err)
	}
	return checkpoint("marker")
}

// promoteReady runs step 6: drop the now-redundant build-complete sentinel so
// the committed unit stays clean, then swap U_^^_new -> U.
func (p Promotion) promoteReady() error {
	if err := removeBuildComplete(p.newer()); err != nil {
		return err
	}
	if err := fileutil.FsyncDir(p.newer()); err != nil {
		return err
	}
	if err := os.Rename(p.newer(), p.canonical()); err != nil {
		return fmt.Errorf("promote %q: %w", p.Name, err)
	}
	if err := fileutil.FsyncDir(p.Parent); err != nil {
		return err
	}
	return checkpoint("promote")
}

// restoreFromOrig renames U_^^_orig back to U (the complete old unit), returning
// to stateInitial so the build re-runs.
func (p Promotion) restoreFromOrig() error {
	if err := os.Rename(p.orig(), p.canonical()); err != nil {
		return fmt.Errorf("restore %q from orig: %w", p.Name, err)
	}
	if err := fileutil.FsyncDir(p.Parent); err != nil {
		return err
	}
	return checkpoint("restore")
}

// dispose finishes step 7: dispose the displaced original after commit.
func (p Promotion) dispose() error {
	if err := p.DisposeOrig(p.orig()); err != nil {
		return fmt.Errorf("dispose orig %q: %w", p.Name, err)
	}
	if err := fileutil.FsyncDir(p.Parent); err != nil {
		return err
	}
	return checkpoint("dispose")
}

// repopulate duplicates into newer every top-level entry present in orig but
// absent from newer — the structural filter entries(orig) \ entries(newer),
// exactly the entries the migrator did not rebuild whole. orig stays intact, so
// a partial/lost newer item is always re-derivable. Each entry is copied to a
// temp name then renamed into place, so an interrupted copy leaves only a temp
// (cleaned on the next call), never a half-copied entry under its real name.
func repopulate(orig, newer string) error {
	if err := cleanRepopTemps(newer); err != nil {
		return err
	}
	origEntries, err := os.ReadDir(orig)
	if err != nil {
		return fmt.Errorf("read orig for repopulate: %w", err)
	}
	present, err := dirNameSet(newer)
	if err != nil {
		return err
	}
	for _, e := range origEntries {
		name := e.Name()
		if _, ok := present[name]; ok {
			continue // rebuilt whole in new, or already repopulated
		}
		tmp := filepath.Join(newer, repopTempPrefix+name)
		if err := os.RemoveAll(tmp); err != nil {
			return fmt.Errorf("clear repopulate temp for %s: %w", name, err)
		}
		if err := workspace.CopyPathFaithful(filepath.Join(orig, name), tmp); err != nil {
			return fmt.Errorf("repopulate %s: %w", name, err)
		}
		if err := fileutil.FsyncTree(tmp); err != nil {
			return fmt.Errorf("fsync repopulated %s: %w", name, err)
		}
		if err := os.Rename(tmp, filepath.Join(newer, name)); err != nil {
			return fmt.Errorf("commit repopulated %s: %w", name, err)
		}
	}
	return nil
}

// TrashDisposer returns a DisposeFunc that moves the displaced original into
// trashDir under its own base name (uniquified on collision), preserving the
// prior schema's data for a manual, LLM-assisted revert (decision 3). trashDir
// must be on the same filesystem as the original.
func TrashDisposer(trashDir string) DisposeFunc {
	return func(origPath string) error {
		if err := fileutil.MkdirAll(trashDir, 0o750); err != nil {
			return fmt.Errorf("create trash dir: %w", err)
		}
		dest := filepath.Join(trashDir, filepath.Base(origPath))
		for i := 1; exists(dest); i++ {
			dest = filepath.Join(trashDir, fmt.Sprintf("%s.%d", filepath.Base(origPath), i))
		}
		if err := os.Rename(origPath, dest); err != nil {
			return fmt.Errorf("move orig to trash: %w", err)
		}
		return fileutil.FsyncDir(trashDir)
	}
}

// DropDisposer deletes the displaced original outright. Used when the orig is
// redundant with the committed new copy (the overlay flatten's old upper) and
// keeping it would stash a secret-bearing delta in trash/.
func DropDisposer(origPath string) error {
	if err := os.RemoveAll(origPath); err != nil {
		return fmt.Errorf("drop orig: %w", err)
	}
	return nil
}

func writeBuildComplete(dir string) error {
	if err := fileutil.AtomicWriteFile(filepath.Join(dir, buildCompleteName), []byte("ok\n"), 0o600); err != nil {
		return fmt.Errorf("write build-complete sentinel: %w", err)
	}
	return nil
}

func hasBuildComplete(dir string) (bool, error) {
	_, err := os.Stat(filepath.Join(dir, buildCompleteName))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("stat build-complete sentinel: %w", err)
}

func removeBuildComplete(dir string) error {
	if err := os.Remove(filepath.Join(dir, buildCompleteName)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove build-complete sentinel: %w", err)
	}
	return nil
}

func cleanRepopTemps(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read dir for temp cleanup: %w", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), repopTempPrefix) {
			if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
				return fmt.Errorf("remove stale repopulate temp %s: %w", e.Name(), err)
			}
		}
	}
	return nil
}

func dirNameSet(dir string) (map[string]struct{}, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read dir %s: %w", dir, err)
	}
	set := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		set[e.Name()] = struct{}{}
	}
	return set, nil
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// checkpoint is a crash-injection seam. In production crashAfter is nil and this
// is a no-op; crash-injection tests set it to simulate process death right
// after a named rename/write boundary.
func checkpoint(step string) error {
	if crashAfter != nil {
		return crashAfter(step)
	}
	return nil
}

var crashAfter func(step string) error

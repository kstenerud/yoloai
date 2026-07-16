// ABOUTME: Materialize is the one owner of "produce a :copy work copy and its
// ABOUTME: diff baseline", so create and reset stop each re-deriving the
// ABOUTME: mode × backend-locality matrix and drifting apart (the DF116/117/118/
// ABOUTME: 120/121 cluster). See the archived workdir-materialization plan.
package workcopy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/git"
	"github.com/kstenerud/yoloai/internal/orchestrator/baseline"
	"github.com/kstenerud/yoloai/internal/workspace"
	"github.com/kstenerud/yoloai/runtime"
)

// Strategy is how the destination is brought to match the source. It is the one
// axis that genuinely differs between callers, and it is a physical distinction,
// not a preference: a live bind-mounted work copy cannot be removed and rebuilt.
type Strategy int

const (
	// WipeAndCopy rebuilds dst from scratch: remove it, then copy. For create's
	// not-yet-existing destination and reset --restart's stale one — both cases
	// where nothing is watching dst. After the copy dst holds exactly the source
	// file set, so no prune is needed.
	WipeAndCopy Strategy = iota
	// InPlaceAndPrune overwrites dst without replacing the directory, then prunes
	// what the source no longer has. For in-place reset, whose dst is bind-mounted
	// into a live container: RemoveAll would strand the container on a deleted
	// inode. Only .git is removed-and-replaced, as a unit, never merged (DF118).
	InPlaceAndPrune
)

// Spec is the subset of a directory that materialization depends on, normalized
// so both create's DirSpec and reset's DirEnvironment adapt to it with a couple
// of fields. Mode is not here on purpose: callers dispatch copy-vs-rw/ro before
// reaching this, so everything here is a :copy directory.
type Spec struct {
	Src            string // absolute host path of the source directory
	IncludeIgnored bool   // :copy-all — copy gitignored files too
	StripHistory   bool   // :copy-strict — fresh baseline instead of preserving .git
}

// HistoryNotice reports why the source's git history did not come along, if it
// did not. It is returned rather than logged so each caller decides whether and
// how to surface it — create warns, reset stays quiet. A zero value means history
// was preserved (or there was none to begin with).
type HistoryNotice struct {
	// SourceIsGitLink: the source is a linked worktree or submodule, whose git
	// dir lives outside the copied tree, so no copy of it carries history.
	SourceIsGitLink bool
}

// Materialize builds the sandbox work copy for a :copy directory at dst from
// spec.Src and establishes its diff baseline, returning that baseline's SHA and
// any reason history was not preserved.
//
// An empty SHA means the baseline was deferred: a SandboxSide backend (Tart)
// stages the copy on the host and baselines inside the VM after start, and the
// empty value is the signal the VM-setup step keys off.
//
// strategy chooses how dst is brought to match the source (see Strategy); the
// rest — history detection, the copy, the SandboxSide deferral, the baseline —
// is identical either way, which is what makes this one owner rather than two.
func Materialize(ctx context.Context, spec Spec, dst string, strategy Strategy, g *git.Git, backend runtime.Backend) (string, HistoryNotice, error) {
	// Preserve the source's .git unless the user asked to strip it (:copy-strict).
	// This is always safe because every backend runs work-copy git in confinement
	// (enforced by the runtime conformance suite), so an agent-writable .git on a
	// host-side work copy is only ever operated on by confined git.
	preserveGit := !spec.StripHistory
	notice := HistoryNotice{SourceIsGitLink: workspace.IsGitLink(spec.Src)}

	// One enumeration serves both the copy and the prune; they must agree, or the
	// prune could delete a file the copy just wrote (the host could change under a
	// second `git ls-files`).
	listProjectFiles := memoizeProjectFiles(ctx, g, spec.Src)

	if err := bringDestinationInLine(ctx, spec, dst, strategy, preserveGit, listProjectFiles); err != nil {
		return "", notice, err
	}

	if runtime.LocalityOf(backend) == runtime.LocalitySandboxSide {
		return "", notice, nil
	}
	sha, err := baseline.WorkCopy(ctx, g, dst)
	if err != nil {
		return "", notice, err
	}
	return sha, notice, nil
}

func bringDestinationInLine(ctx context.Context, spec Spec, dst string, strategy Strategy, preserveGit bool, listProjectFiles func() ([]string, bool, error)) error {
	switch strategy {
	case WipeAndCopy:
		if err := os.RemoveAll(dst); err != nil {
			return fmt.Errorf("clear work copy %s: %w", dst, err)
		}
		if err := workspace.CopyProjectDir(spec.Src, dst, spec.IncludeIgnored, preserveGit, listProjectFiles); err != nil {
			return fmt.Errorf("copy %s: %w", spec.Src, err)
		}
		return nil

	case InPlaceAndPrune:
		files, err := workspace.ProjectFileSet(spec.Src, spec.IncludeIgnored, listProjectFiles)
		if err != nil {
			return fmt.Errorf("enumerate project files of %s: %w", spec.Src, err)
		}
		// Replace .git as a unit rather than let CopyProjectDir merge it over the
		// existing repo — a merge can leave a ref pointing at a deleted object
		// (DF118). The live-mounted dst itself is never removed.
		if err := os.RemoveAll(filepath.Join(dst, ".git")); err != nil {
			return fmt.Errorf("remove work copy .git: %w", err)
		}
		if err := workspace.CopyProjectDir(spec.Src, dst, spec.IncludeIgnored, preserveGit, listProjectFiles); err != nil {
			return fmt.Errorf("copy %s: %w", spec.Src, err)
		}
		// Copying refreshes what the source still has; pruning removes what the
		// agent added and what the source dropped — a leaked secret included (DF117).
		if err := workspace.PruneToFileSet(dst, files); err != nil {
			return fmt.Errorf("prune work copy: %w", err)
		}
		return nil

	default:
		return fmt.Errorf("unknown materialization strategy %d", strategy)
	}
}

// memoizeProjectFiles returns a git.ListProjectFiles closure that runs at most
// once, so a strategy that needs the list twice (enumerate then copy) cannot get
// two different answers.
func memoizeProjectFiles(ctx context.Context, g *git.Git, src string) func() ([]string, bool, error) {
	var (
		done    bool
		files   []string
		isRepo  bool
		listErr error
	)
	return func() ([]string, bool, error) {
		if !done {
			files, isRepo, listErr = g.ListProjectFiles(ctx, src)
			done = true
		}
		return files, isRepo, listErr
	}
}

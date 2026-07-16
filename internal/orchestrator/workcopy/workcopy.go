// ABOUTME: Materialize is the one owner of "produce a :copy work copy and its
// ABOUTME: diff baseline", so create and reset stop each re-deriving the
// ABOUTME: mode × backend-locality matrix and drifting apart (the DF116/117/118/
// ABOUTME: 120/121 cluster). See design/plans/workdir-materialization.md.
package workcopy

import (
	"context"
	"fmt"
	"os"

	"github.com/kstenerud/yoloai/internal/git"
	"github.com/kstenerud/yoloai/internal/orchestrator/baseline"
	"github.com/kstenerud/yoloai/internal/workspace"
	"github.com/kstenerud/yoloai/runtime"
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
// how to surface it — create warns, reset may stay quiet. Both fields false
// means history was preserved (or there was none to begin with).
type HistoryNotice struct {
	// HistoryDowngraded: the backend does not confine work-copy git, so history
	// was stripped to a fresh baseline to avoid widening the host-side git RCE
	// surface. Distinct from the user asking for :copy-strict, which is silent.
	HistoryDowngraded bool
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
// This is the WipeAndCopy strategy: the destination is rebuilt from scratch, so
// after CopyProjectDir it holds exactly the source's file set and needs no prune.
// It serves both create (a fresh, absent dst) and reset --restart (a stale dst
// that RemoveAll clears) — the same operation, which is the point. The in-place
// reset's copy-over-a-live-mount strategy is a separate entry (a later stage of
// the plan) because it cannot RemoveAll the directory the container is watching.
func Materialize(ctx context.Context, spec Spec, dst string, g *git.Git, backend runtime.Backend) (string, HistoryNotice, error) {
	preserveGit, downgraded := workspace.PreserveGit(spec.StripHistory, runtime.GitRunsInConfinement(backend))
	notice := HistoryNotice{
		HistoryDowngraded: downgraded,
		SourceIsGitLink:   workspace.IsGitLink(spec.Src),
	}

	// Rebuild dst from scratch. RemoveAll is a no-op on create's not-yet-existing
	// destination and clears a restart's stale one; either way the copy that
	// follows writes exactly the source's files, so no prune is needed here.
	if err := os.RemoveAll(dst); err != nil {
		return "", notice, fmt.Errorf("clear work copy %s: %w", dst, err)
	}
	if err := workspace.CopyProjectDir(spec.Src, dst, spec.IncludeIgnored, preserveGit, func() ([]string, bool, error) {
		return g.ListProjectFiles(ctx, spec.Src)
	}); err != nil {
		return "", notice, fmt.Errorf("copy %s: %w", spec.Src, err)
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

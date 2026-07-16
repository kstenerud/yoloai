// ABOUTME: The one place a copy-mode work copy's diff baseline is established,
// ABOUTME: shared by create and reset so they cannot disagree about what the
// ABOUTME: agent's starting point is — which they did, and which made a diff
// ABOUTME: report the user's own uncommitted work as the agent's (DF120).
package baseline

import (
	"context"
	"fmt"

	"github.com/kstenerud/yoloai/internal/git"
	"github.com/kstenerud/yoloai/internal/workspace"
)

// WorkCopy establishes the diff baseline for the copy-mode work copy at
// workCopyDir and returns its SHA. Every later diff is read against it, so it
// has to mean "the state the agent was handed" — not "the state the source
// happened to be committed at".
//
// That distinction is the whole point of committing the work copy's dirty
// entries rather than taking HEAD. A copy of a source with uncommitted work
// arrives dirty, and a baseline of HEAD would make every one of those edits
// look like the agent's the moment the sandbox opened. Committing them first
// means a diff shows what the agent did and nothing else. The commit lands in
// the **work copy's** repo, never the source's — since DF116 a work copy cannot
// hold a .git that points anywhere else, which is what makes that safe to say.
//
// create and reset both call this, and must: they diverged here once, create
// committing and all three reset paths taking HEAD, and the result was a diff
// that misreported the user's own work in progress as the agent's while apply
// then refused the patch. One caller drifting from the other is exactly the
// failure this function exists to make impossible, so resist giving it a mode
// or a flag — a second behaviour is a second chance to disagree.
func WorkCopy(ctx context.Context, g *git.Git, workCopyDir string) (string, error) {
	if !git.IsGitRepo(workCopyDir) {
		sha, err := g.Baseline(ctx, workCopyDir)
		if err != nil {
			return "", fmt.Errorf("git baseline: %w", err)
		}
		return sha, nil
	}
	if _, err := g.HeadSHA(ctx, workCopyDir); err != nil {
		// A repo with no commits, or a broken one: there is no history to keep,
		// so start over rather than baseline against something unreadable.
		if rmErr := workspace.RemoveGitDirs(workCopyDir); rmErr != nil {
			return "", fmt.Errorf("remove unusable git dir: %w", rmErr)
		}
		sha, baselineErr := g.Baseline(ctx, workCopyDir)
		if baselineErr != nil {
			return "", fmt.Errorf("git baseline after removing unusable repo: %w", baselineErr)
		}
		return sha, nil
	}
	sha, err := g.BaselineUncommittedChanges(ctx, workCopyDir)
	if err != nil {
		return "", fmt.Errorf("baseline pre-session state: %w", err)
	}
	return sha, nil
}

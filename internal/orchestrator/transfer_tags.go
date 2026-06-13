// ABOUTME: Host-side tag transfer — re-creates agent-created sandbox tags on the
// ABOUTME: original host repo after apply, plus the apply-target git-repo check.
package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/git"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/store"
)

// TagOutcome is the result of transferring one tag to the host. Applied is true
// when the tag was created; otherwise exactly one of Unmatched (the tag's
// sandbox commit didn't map to a host commit) or Err (git tag failed) explains
// the skip.
type TagOutcome struct {
	Name      string
	Applied   bool
	Unmatched bool
	Err       string
}

// TransferTagsResult collects the per-tag outcomes plus applied/skipped counts.
type TransferTagsResult struct {
	Outcomes []TagOutcome
	Applied  int
	Skipped  int
}

// TargetIsGitRepo reports whether the sandbox's original host work directory is
// a git repository — the apply target. Drives the CLI's non-git fallback and
// the selective-apply precondition.
func TargetIsGitRepo(layout config.Layout, name string, dirHostPath string) (bool, error) {
	meta, err := store.LoadEnvironment(layout.SandboxDir(name))
	if err != nil {
		return false, err
	}
	dir := meta.Dir(dirHostPath)
	if dir == nil {
		return false, nil
	}
	return git.IsGitRepo(dir.HostPath), nil
}

// TransferTags re-creates the given sandbox tags on the host target repo,
// mapping each tag's sandbox commit SHA to the host SHA it landed on. shaMap
// (lowercase sandbox SHA → host SHA) comes from a prior series apply; when it's
// empty the map is built by matching commits (author/timestamp/subject) between
// the sandbox work copy and the host target — the no-commits-applied path.
// Returns per-tag outcomes plus counts; an empty tag list is a no-op.
//
// The sandbox work copy is read through the backend (Tart-correct via rt); the
// host target repo, where the tags are created, always uses host git.
func TransferTags(ctx context.Context, layout config.Layout, rt runtime.Backend, name string, dirHostPath string, tags []TagInfo, shaMap map[string]string) (*TransferTagsResult, error) {
	res := &TransferTagsResult{}
	if len(tags) == 0 {
		return res, nil
	}

	sandboxDir := layout.SandboxDir(name)
	meta, err := store.LoadEnvironment(sandboxDir)
	if err != nil {
		return nil, err
	}
	dir := meta.Dir(dirHostPath)
	if dir == nil {
		return nil, fmt.Errorf("directory %q not found in sandbox %q", dirHostPath, name)
	}
	targetDir := dir.HostPath

	if len(shaMap) == 0 {
		workDir := store.WorkDir(sandboxDir, dir.HostPath)
		sandboxSHAs := make([]string, len(tags))
		for i, t := range tags {
			sandboxSHAs[i] = t.SHA
		}
		sandboxGit := git.NewSandbox(layout, rt, name)
		hostGit := git.NewHost(layout)
		shaMap, err = sandboxGit.BuildSHAMapByMatching(ctx, hostGit, targetDir, workDir, sandboxSHAs)
		if err != nil {
			return nil, fmt.Errorf("build SHA map: %w", err)
		}
	}

	res.Outcomes = make([]TagOutcome, 0, len(tags))
	for _, tag := range tags {
		hostSHA, ok := shaMap[strings.ToLower(tag.SHA)]
		if !ok {
			res.Outcomes = append(res.Outcomes, TagOutcome{Name: tag.Name, Unmatched: true})
			res.Skipped++
			continue
		}
		hostGit := git.NewHost(layout)
		if createErr := hostGit.CreateTag(ctx, targetDir, tag.Name, hostSHA, tag.Message); createErr != nil {
			res.Outcomes = append(res.Outcomes, TagOutcome{Name: tag.Name, Err: createErr.Error()})
			res.Skipped++
			continue
		}
		res.Outcomes = append(res.Outcomes, TagOutcome{Name: tag.Name, Applied: true})
		res.Applied++
	}
	return res, nil
}

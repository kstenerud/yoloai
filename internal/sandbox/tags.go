// ABOUTME: Git tag helpers for sandboxes: ListTagsBeyondBaseline, ListUnappliedTags,
// ABOUTME: and listAllTags used by the diff/apply pipeline to surface agent-created tags.
package sandbox

import (
	"context"
	"fmt"
	"strings"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/internal/workspace"
)

// sandboxGitRunner returns a workspace.GitRunner that runs git against the
// sandbox work copy via the backend: on the host for Docker/Podman/Seatbelt (or
// a nil runtime), and inside the VM for Tart (runtime.GitExecFor translates the
// host work path to its VM path). This is what makes the tag pipeline correct
// for Tart VM work copies (see DF12).
func sandboxGitRunner(ctx context.Context, rt runtime.Runtime, name, workDir string) workspace.GitRunner {
	return func(args ...string) (string, error) {
		return runtime.GitExecFor(ctx, rt, name, workDir, args...)
	}
}

// hostGitRunner returns a workspace.GitRunner that runs git directly on a host
// path — used for the host target repo, which lives on the host even when the
// sandbox runs in a VM. Hooks are disabled, matching the rest of the pipeline.
func hostGitRunner(dir string) workspace.GitRunner {
	return func(args ...string) (string, error) {
		out, err := workspace.NewGitCmd(dir, args...).Output()
		return string(out), err
	}
}

// loadDiffContext returns the work directory, baseline SHA, and workdir mode for
// a sandbox. Used by tags.go to locate commits relative to the baseline.
func loadDiffContext(layout config.Layout, name string) (workDir string, baselineSHA string, mode store.DirMode, err error) {
	sandboxDir := layout.SandboxDir(name)
	if dirErr := store.RequireSandboxDir(sandboxDir); dirErr != nil {
		return "", "", "", dirErr
	}

	meta, loadErr := store.LoadEnvironment(sandboxDir)
	if loadErr != nil {
		return "", "", "", loadErr
	}

	mode = meta.Workdir.Mode

	switch mode {
	case store.DirModeCopy:
		mountPath := meta.Workdir.MountPath
		if mountPath != "" && mountPath != meta.Workdir.HostPath {
			workDir = mountPath
		} else {
			workDir = store.WorkDir(sandboxDir, meta.Workdir.HostPath)
		}
		baselineSHA = meta.Workdir.BaselineSHA
		if baselineSHA == "" {
			return "", "", "", fmt.Errorf("sandbox has no baseline SHA — was it created before diff support?")
		}
	case store.DirModeOverlay:
		workDir = meta.Workdir.MountPath
		if workDir == "" {
			workDir = meta.Workdir.HostPath
		}
		baselineSHA = meta.Workdir.BaselineSHA
	case store.DirModeRW:
		workDir = meta.Workdir.HostPath
		baselineSHA = "HEAD"
	case store.DirModeRO:
		return "", "", "", fmt.Errorf("workdir cannot be read-only (mode %s)", mode)
	default:
		return "", "", "", fmt.Errorf("unsupported workdir mode: %s", mode)
	}

	return workDir, baselineSHA, mode, nil
}

// TagInfo holds information about a git tag in the sandbox.
type TagInfo struct {
	Name    string `json:"name"`
	SHA     string `json:"sha"`     // commit SHA the tag points to (dereferenced)
	Message string `json:"message"` // empty for lightweight tags
}

// ListTagsBeyondBaseline returns tags whose target commit is beyond the baseline.
// Returns nil for :rw and :overlay sandboxes (not supported). Reads the sandbox
// work copy through the backend, so it is correct for Tart VM work copies.
func ListTagsBeyondBaseline(ctx context.Context, layout config.Layout, rt runtime.Runtime, name string) ([]TagInfo, error) {
	workDir, baselineSHA, mode, err := loadDiffContext(layout, name)
	if err != nil {
		return nil, err
	}

	if mode != "copy" {
		return nil, nil
	}

	git := sandboxGitRunner(ctx, rt, name, workDir)

	// Collect commit SHAs beyond baseline
	revOut, err := git("rev-list", baselineSHA+"..HEAD")
	if err != nil {
		return nil, fmt.Errorf("git rev-list: %w", err)
	}

	beyondSet := make(map[string]bool)
	for sha := range strings.FieldsSeq(revOut) {
		beyondSet[strings.ToLower(sha)] = true
	}

	if len(beyondSet) == 0 {
		return nil, nil
	}

	tags, err := listAllTags(git)
	if err != nil {
		return nil, err
	}

	var result []TagInfo
	for _, t := range tags {
		if beyondSet[strings.ToLower(t.SHA)] {
			result = append(result, t)
		}
	}

	return result, nil
}

// ListUnappliedTags returns tags that exist in the sandbox but not on the host.
// This is useful for showing hints about tags that haven't been transferred yet,
// even if their commits have already been applied. The sandbox side is read
// through the backend (Tart-correct); the host target repo uses host git.
func ListUnappliedTags(ctx context.Context, layout config.Layout, rt runtime.Runtime, name string) ([]TagInfo, error) {
	sandboxDir := layout.SandboxDir(name)
	meta, err := store.LoadEnvironment(sandboxDir)
	if err != nil {
		return nil, err
	}

	if meta.Workdir.Mode != "copy" {
		return nil, nil
	}

	workDir := store.WorkDir(sandboxDir, meta.Workdir.HostPath)
	targetDir := meta.Workdir.HostPath

	// Check if target is a git repo
	if !workspace.IsGitRepo(targetDir) {
		return nil, nil
	}

	// List all tags in sandbox
	sandboxTags, err := listAllTags(sandboxGitRunner(ctx, rt, name, workDir))
	if err != nil {
		return nil, err
	}

	if len(sandboxTags) == 0 {
		return nil, nil
	}

	// List all tags on host
	hostTagNames := make(map[string]bool)
	hostTags, err := listAllTags(hostGitRunner(targetDir))
	if err == nil { // best-effort; ignore errors
		for _, t := range hostTags {
			hostTagNames[t.Name] = true
		}
	}

	// Return tags that exist in sandbox but not on host
	var unapplied []TagInfo
	for _, t := range sandboxTags {
		if !hostTagNames[t.Name] {
			unapplied = append(unapplied, t)
		}
	}

	return unapplied, nil
}

// getTagMessage returns the full message for an annotated tag via the runner.
// Returns empty string for lightweight tags or if the message can't be read.
func getTagMessage(git workspace.GitRunner, tagName string) string {
	out, err := git("for-each-ref", "--format=%(contents)", "refs/tags/"+tagName)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// listAllTags returns all tags in the repository the runner targets.
// Tag messages are NOT populated (Message field is empty); use getTagMessage
// to fetch the full message for a specific tag when needed.
func listAllTags(git workspace.GitRunner) ([]TagInfo, error) {
	// Use only single-line fields to keep parsing reliable.
	// Multi-line tag messages are fetched separately via getTagMessage.
	const tagFmt = "%(refname:short)\x01%(objecttype)\x01%(*objectname)\x01%(objectname)"
	tagOut, err := git("for-each-ref", "--format="+tagFmt, "refs/tags")
	if err != nil {
		return nil, fmt.Errorf("git for-each-ref: %w", err)
	}

	raw := strings.TrimRight(tagOut, "\n")
	if raw == "" {
		return nil, nil
	}

	var tags []TagInfo
	for line := range strings.SplitSeq(raw, "\n") {
		parts := strings.SplitN(line, "\x01", 4)
		if len(parts) < 4 {
			continue
		}
		tagName := parts[0]
		objType := parts[1]
		derefSHA := parts[2]
		objSHA := parts[3]

		var commitSHA string
		switch objType {
		case "tag": // annotated tag: deref to the commit
			commitSHA = derefSHA
		case "commit": // lightweight tag: points directly to commit
			commitSHA = objSHA
		default:
			continue // blobs, trees — ignore
		}

		tags = append(tags, TagInfo{Name: tagName, SHA: commitSHA})
	}

	return tags, nil
}

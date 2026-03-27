package sandbox

import (
	"fmt"
	"strings"

	"github.com/kstenerud/yoloai/workspace"
)

// TagInfo holds information about a git tag in the sandbox.
type TagInfo struct {
	Name    string `json:"name"`
	SHA     string `json:"sha"`     // commit SHA the tag points to (dereferenced)
	Message string `json:"message"` // empty for lightweight tags
}

// ListTagsBeyondBaseline returns tags whose target commit is beyond the baseline.
// Returns nil for :rw and :overlay sandboxes (not supported).
func ListTagsBeyondBaseline(name string) ([]TagInfo, error) {
	workDir, baselineSHA, mode, err := loadDiffContext(name)
	if err != nil {
		return nil, err
	}

	if mode != "copy" {
		return nil, nil
	}

	// Collect commit SHAs beyond baseline
	revCmd := workspace.NewGitCmd(workDir, "rev-list", baselineSHA+"..HEAD")
	revOut, err := revCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git rev-list: %w", err)
	}

	beyondSet := make(map[string]bool)
	for _, sha := range strings.Fields(string(revOut)) {
		beyondSet[strings.ToLower(sha)] = true
	}

	if len(beyondSet) == 0 {
		return nil, nil
	}

	tags, err := listAllTags(workDir)
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
// even if their commits have already been applied.
func ListUnappliedTags(name string) ([]TagInfo, error) {
	meta, err := LoadMeta(Dir(name))
	if err != nil {
		return nil, err
	}

	if meta.Workdir.Mode != "copy" {
		return nil, nil
	}

	workDir := WorkDir(name, meta.Workdir.HostPath)
	targetDir := meta.Workdir.HostPath

	// Check if target is a git repo
	if !workspace.IsGitRepo(targetDir) {
		return nil, nil
	}

	// List all tags in sandbox
	sandboxTags, err := listAllTags(workDir)
	if err != nil {
		return nil, err
	}

	if len(sandboxTags) == 0 {
		return nil, nil
	}

	// List all tags on host
	hostTagNames := make(map[string]bool)
	hostTags, err := listAllTags(targetDir)
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

// GetTagMessage returns the full message for an annotated tag.
// Returns empty string for lightweight tags or if the message can't be read.
func GetTagMessage(gitDir, tagName string) string {
	cmd := workspace.NewGitCmd(gitDir, "for-each-ref", "--format=%(contents)", "refs/tags/"+tagName)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// listAllTags returns all tags in a git repository.
// Tag messages are NOT populated (Message field is empty); use GetTagMessage
// to fetch the full message for a specific tag when needed.
func listAllTags(gitDir string) ([]TagInfo, error) {
	// Use only single-line fields to keep parsing reliable.
	// Multi-line tag messages are fetched separately via GetTagMessage.
	const tagFmt = "%(refname:short)\x01%(objecttype)\x01%(*objectname)\x01%(objectname)"
	tagCmd := workspace.NewGitCmd(gitDir, "for-each-ref", "--format="+tagFmt, "refs/tags")
	tagOut, err := tagCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git for-each-ref: %w", err)
	}

	raw := strings.TrimRight(string(tagOut), "\n")
	if raw == "" {
		return nil, nil
	}

	var tags []TagInfo
	for _, line := range strings.Split(raw, "\n") {
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

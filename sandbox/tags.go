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

	// List tags with type, dereferenced SHA, obj SHA, and full message.
	// Fields are separated by \x01; entries by \n.
	const tagFmt = "%(refname:short)\x01%(objecttype)\x01%(*objectname)\x01%(objectname)\x01%(contents)"
	tagCmd := workspace.NewGitCmd(workDir, "for-each-ref", "--format="+tagFmt, "refs/tags")
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
		parts := strings.SplitN(line, "\x01", 5)
		if len(parts) < 5 {
			continue
		}
		tagName := parts[0]
		objType := parts[1]
		derefSHA := parts[2]
		objSHA := parts[3]
		message := strings.TrimSpace(parts[4])

		var commitSHA, tagMsg string
		switch objType {
		case "tag": // annotated tag: deref to the commit
			commitSHA = derefSHA
			tagMsg = message
		case "commit": // lightweight tag: points directly to commit
			commitSHA = objSHA
		default:
			continue // blobs, trees — ignore
		}

		if !beyondSet[strings.ToLower(commitSHA)] {
			continue
		}

		tags = append(tags, TagInfo{Name: tagName, SHA: commitSHA, Message: tagMsg})
	}

	return tags, nil
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

// listAllTags returns all tags in a git repository.
func listAllTags(gitDir string) ([]TagInfo, error) {
	const tagFmt = "%(refname:short)\x01%(objecttype)\x01%(*objectname)\x01%(objectname)\x01%(contents)"
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
		parts := strings.SplitN(line, "\x01", 5)
		if len(parts) < 5 {
			continue
		}
		tagName := parts[0]
		objType := parts[1]
		derefSHA := parts[2]
		objSHA := parts[3]
		message := strings.TrimSpace(parts[4])

		var commitSHA, tagMsg string
		switch objType {
		case "tag": // annotated tag
			commitSHA = derefSHA
			tagMsg = message
		case "commit": // lightweight tag
			commitSHA = objSHA
		default:
			continue
		}

		tags = append(tags, TagInfo{Name: tagName, SHA: commitSHA, Message: tagMsg})
	}

	return tags, nil
}

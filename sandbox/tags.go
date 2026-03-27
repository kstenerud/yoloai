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

	// List tags with type, dereferenced SHA, obj SHA, subject, and body.
	// Fields are separated by \x01; records by %x00 (null byte in format string).
	// Using subject+body separately to handle multi-line messages correctly.
	const tagFmt = "%(refname:short)\x01%(objecttype)\x01%(*objectname)\x01%(objectname)\x01%(contents:subject)\x01%(contents:body)%x00"
	tagCmd := workspace.NewGitCmd(workDir, "for-each-ref", "--format="+tagFmt, "refs/tags")
	tagOut, err := tagCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git for-each-ref: %w", err)
	}

	raw := strings.TrimRight(string(tagOut), "\x00")
	if raw == "" {
		return nil, nil
	}

	var tags []TagInfo
	for _, record := range strings.Split(raw, "\x00") {
		if record == "" {
			continue
		}
		parts := strings.SplitN(record, "\x01", 6)
		if len(parts) < 6 {
			continue
		}
		tagName := parts[0]
		objType := parts[1]
		derefSHA := parts[2]
		objSHA := parts[3]
		subject := strings.TrimSpace(parts[4])
		body := strings.TrimSpace(parts[5])

		var commitSHA, tagMsg string
		switch objType {
		case "tag": // annotated tag: deref to the commit
			commitSHA = derefSHA
			// Combine subject and body with blank line separator (git convention)
			if body != "" {
				tagMsg = subject + "\n\n" + body
			} else {
				tagMsg = subject
			}
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
	const tagFmt = "%(refname:short)\x01%(objecttype)\x01%(*objectname)\x01%(objectname)\x01%(contents:subject)\x01%(contents:body)%x00"
	tagCmd := workspace.NewGitCmd(gitDir, "for-each-ref", "--format="+tagFmt, "refs/tags")
	tagOut, err := tagCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git for-each-ref: %w", err)
	}

	raw := strings.TrimRight(string(tagOut), "\x00")
	if raw == "" {
		return nil, nil
	}

	var tags []TagInfo
	for _, record := range strings.Split(raw, "\x00") {
		if record == "" {
			continue
		}
		parts := strings.SplitN(record, "\x01", 6)
		if len(parts) < 6 {
			continue
		}
		tagName := parts[0]
		objType := parts[1]
		derefSHA := parts[2]
		objSHA := parts[3]
		subject := strings.TrimSpace(parts[4])
		body := strings.TrimSpace(parts[5])

		var commitSHA, tagMsg string
		switch objType {
		case "tag": // annotated tag
			commitSHA = derefSHA
			// Combine subject and body with blank line separator (git convention)
			if body != "" {
				tagMsg = subject + "\n\n" + body
			} else {
				tagMsg = subject
			}
		case "commit": // lightweight tag
			commitSHA = objSHA
		default:
			continue
		}

		tags = append(tags, TagInfo{Name: tagName, SHA: commitSHA, Message: tagMsg})
	}

	return tags, nil
}

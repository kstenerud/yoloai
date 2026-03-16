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

	// List tags with type, dereferenced SHA, obj SHA, and subject.
	// Fields are separated by \x01; entries by \n.
	const tagFmt = "%(refname:short)\x01%(objecttype)\x01%(*objectname)\x01%(objectname)\x01%(contents:subject)"
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

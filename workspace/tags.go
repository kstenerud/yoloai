package workspace

import (
	"fmt"
	"strings"
)

// CreateTag creates a git tag on the given SHA in the target directory.
// If message is non-empty, an annotated tag is created; otherwise lightweight.
// Returns an error if the tag already exists or git tag fails.
func CreateTag(dir, name, sha, message string) error {
	var args []string
	if message != "" {
		args = []string{"tag", "-a", name, sha, "-m", message}
	} else {
		args = []string{"tag", name, sha}
	}
	cmd := NewGitCmd(dir, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if strings.Contains(msg, "already exists") {
			return fmt.Errorf("tag %q already exists", name)
		}
		return fmt.Errorf("git tag %s: %s: %w", name, msg, err)
	}
	return nil
}

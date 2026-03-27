package workspace

import (
	"fmt"
	"strings"
)

// CommitExists checks if a commit SHA exists in the git repository.
func CommitExists(dir, sha string) bool {
	cmd := NewGitCmd(dir, "cat-file", "-e", sha)
	err := cmd.Run()
	return err == nil
}

// commitMeta holds commit metadata for matching.
type commitMeta struct {
	SHA       string
	Author    string
	Timestamp string
	Subject   string
}

// getCommitMeta retrieves commit metadata for a given SHA.
func getCommitMeta(dir, sha string) (*commitMeta, error) {
	// Format: %H (hash) %an (author name) %at (timestamp) %s (subject)
	cmd := NewGitCmd(dir, "show", "-s", "--format=%H%x00%an%x00%at%x00%s", sha)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git show: %w", err)
	}
	parts := strings.Split(strings.TrimSpace(string(output)), "\x00")
	if len(parts) < 4 {
		return nil, fmt.Errorf("unexpected git show output format")
	}
	return &commitMeta{
		SHA:       parts[0],
		Author:    parts[1],
		Timestamp: parts[2],
		Subject:   parts[3],
	}, nil
}

// BuildSHAMapByMatching builds a sandbox→host SHA map by matching commits.
// Matches commits by author, timestamp, and subject line.
func BuildSHAMapByMatching(sandboxDir, hostDir string, sandboxSHAs []string) (map[string]string, error) {
	shaMap := make(map[string]string)

	// Get all commits from host (last 1000 should be enough)
	cmd := NewGitCmd(hostDir, "log", "--format=%H%x00%an%x00%at%x00%s", "-1000")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git log on host: %w", err)
	}

	// Build index of host commits by (author, timestamp, subject)
	type commitKey struct {
		Author    string
		Timestamp string
		Subject   string
	}
	hostCommits := make(map[commitKey]string)
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\x00")
		if len(parts) < 4 {
			continue
		}
		key := commitKey{
			Author:    parts[1],
			Timestamp: parts[2],
			Subject:   parts[3],
		}
		hostCommits[key] = parts[0] // SHA
	}

	// Match each sandbox SHA to a host SHA
	for _, sandboxSHA := range sandboxSHAs {
		meta, err := getCommitMeta(sandboxDir, sandboxSHA)
		if err != nil {
			continue // skip if can't get info
		}
		key := commitKey{
			Author:    meta.Author,
			Timestamp: meta.Timestamp,
			Subject:   meta.Subject,
		}
		if hostSHA, found := hostCommits[key]; found {
			shaMap[strings.ToLower(sandboxSHA)] = hostSHA
		}
	}

	return shaMap, nil
}

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

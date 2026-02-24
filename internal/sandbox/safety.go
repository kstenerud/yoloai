package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// dangerousDirs is the set of paths that should not be mounted into sandboxes.
var dangerousDirs = map[string]bool{
	"/":             true,
	"/usr":          true,
	"/etc":          true,
	"/var":          true,
	"/boot":         true,
	"/bin":          true,
	"/sbin":         true,
	"/lib":          true,
	"/System":       true,
	"/Library":      true,
	"/Applications": true,
}

// IsDangerousDir checks whether the given absolute path is a dangerous
// mount target. Resolves symlinks before checking. Does not consider
// :force — the caller handles downgrading errors to warnings.
func IsDangerousDir(absPath string) bool {
	resolved, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		resolved = absPath
	}

	// Check both original and resolved paths (e.g., /bin → /usr/bin).
	if dangerousDirs[absPath] || dangerousDirs[resolved] {
		return true
	}

	home, err := os.UserHomeDir()
	if err == nil && (resolved == home || absPath == home) {
		return true
	}

	return false
}

// CheckPathOverlap checks if any two paths in the list have a prefix
// overlap (one is a parent of the other, or they are identical).
// All paths must be absolute. Resolves symlinks before comparing.
// Returns an error describing the first overlap found, or nil.
func CheckPathOverlap(paths []string) error {
	resolved := make([]string, len(paths))
	for i, p := range paths {
		r, err := filepath.EvalSymlinks(p)
		if err != nil {
			r = p
		}
		resolved[i] = r
	}

	for i := 0; i < len(resolved); i++ {
		for j := i + 1; j < len(resolved); j++ {
			if pathsOverlap(resolved[i], resolved[j]) {
				shorter, longer := resolved[i], resolved[j]
				if len(longer) < len(shorter) {
					shorter, longer = longer, shorter
				}
				return fmt.Errorf("path overlap: %s contains %s", shorter, longer)
			}
		}
	}

	return nil
}

// pathsOverlap checks if either path is a prefix of the other.
func pathsOverlap(a, b string) bool {
	if a == b {
		return true
	}
	return strings.HasPrefix(b, a+"/") || strings.HasPrefix(a, b+"/")
}

// CheckDirtyRepo checks if the given path is a git repository with
// uncommitted changes. Returns a human-readable warning string if
// dirty, empty string if clean or not a git repo.
func CheckDirtyRepo(path string) (string, error) {
	gitDir := filepath.Join(path, ".git")
	if _, err := os.Stat(gitDir); err != nil {
		return "", nil // not a git repo
	}

	cmd := exec.Command("git", "-C", path, "status", "--porcelain") //nolint:gosec // G204: args are not user-controlled
	output, err := cmd.Output()
	if err != nil {
		return "", nil // git command failed, don't block sandbox creation
	}

	if len(output) == 0 {
		return "", nil // clean repo
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	modified := 0
	untracked := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "??") {
			untracked++
		} else {
			modified++
		}
	}

	var parts []string
	if modified > 0 {
		parts = append(parts, fmt.Sprintf("%d files modified", modified))
	}
	if untracked > 0 {
		parts = append(parts, fmt.Sprintf("%d untracked", untracked))
	}

	return strings.Join(parts, ", "), nil
}

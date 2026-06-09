// ABOUTME: IsDangerousDir, CheckPathOverlap guard sandbox creation from mounting
// ABOUTME: system paths. CheckDirtyRepo has moved to internal/git; the free-function
// ABOUTME: wrapper is in workspace/git.go.
package workspace

import (
	"fmt"
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
// :force -- the caller handles downgrading errors to warnings.
// homeDir is the user's home directory; callers derive it from layout.HomeDir.
func IsDangerousDir(absPath, homeDir string) bool {
	resolved, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		resolved = absPath
	}

	// Check both original and resolved paths (e.g., /bin -> /usr/bin).
	if dangerousDirs[absPath] || dangerousDirs[resolved] {
		return true
	}

	if homeDir != "" && (resolved == homeDir || absPath == homeDir) {
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

	for i := range resolved {
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

package sandbox

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// copyDir copies a directory tree using cp -rp.
func copyDir(src, dst string) error {
	cmd := exec.Command("cp", "-rp", src, dst) //nolint:gosec // G204: paths are validated sandbox paths
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("cp -rp: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

// removeGitDirs recursively removes all .git entries (files and directories)
// from root. This strips git metadata from a copied working tree so that
// hooks, LFS filters, submodule links, and worktree links don't interfere
// with yoloAI's internal git operations.
func removeGitDirs(root string) error {
	var toRemove []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Name() == ".git" {
			toRemove = append(toRemove, path)
			if d.IsDir() {
				return filepath.SkipDir
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk for .git entries: %w", err)
	}

	// Remove in reverse order so nested entries are removed before parents.
	for i := len(toRemove) - 1; i >= 0; i-- {
		if err := os.RemoveAll(toRemove[i]); err != nil {
			return fmt.Errorf("remove %s: %w", toRemove[i], err)
		}
	}
	return nil
}

// expandTilde replaces a leading ~ with the user's home directory.
func expandTilde(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
}

// readJSONMap reads a JSON file into a map, returning an empty map if the file doesn't exist.
func readJSONMap(path string) (map[string]any, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is sandbox-controlled
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]any), nil
		}
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// writeJSONMap marshals a map and writes it as indented JSON to the given path.
func writeJSONMap(path string, m map[string]any) error {
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0600)
}

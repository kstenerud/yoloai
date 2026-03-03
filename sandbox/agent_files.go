package sandbox

// ABOUTME: Copies user-configured agent files into sandbox agent-state directory.
// ABOUTME: Supports string form (base directory) and list form (explicit paths).

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kstenerud/yoloai/agent"
	"github.com/kstenerud/yoloai/config"
	"github.com/kstenerud/yoloai/workspace"
)

// copyAgentFiles copies user-configured agent files into the sandbox's
// agent-state directory. Supports two forms:
//   - String form (BaseDir): copies the agent's state subdir from the base
//     directory, applying AgentFilesExclude patterns.
//   - List form (Files): copies each listed file/directory into agent-state.
//
// Files that already exist in agent-state are never overwritten (SeedFiles win).
// Returns nil if agentFiles is nil or the agent has no StateDir.
func copyAgentFiles(agentDef *agent.Definition, sandboxDir string, agentFiles *config.AgentFilesConfig) error {
	if agentFiles == nil {
		return nil
	}

	if agentFiles.IsStringForm() {
		return copyAgentFilesFromBaseDir(agentDef, sandboxDir, agentFiles.BaseDir)
	}

	return copyAgentFilesList(sandboxDir, agentFiles.Files)
}

// copyAgentFilesFromBaseDir copies the agent's state directory from a base
// directory. For example, if baseDir is "/home/user" and the agent is Claude,
// it copies from /home/user/.claude/ into agent-state/.
// Applies AgentFilesExclude patterns and skips files that already exist.
func copyAgentFilesFromBaseDir(agentDef *agent.Definition, sandboxDir, baseDir string) error {
	relPath := agentDef.StateRelPath()
	if relPath == "" {
		return nil // agent has no state dir (aider, test, shell)
	}

	srcDir := filepath.Join(baseDir, relPath)
	if _, err := os.Stat(srcDir); os.IsNotExist(err) {
		return nil // source doesn't exist, skip silently
	}

	agentStateDir := filepath.Join(sandboxDir, "agent-state")

	return filepath.WalkDir(srcDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Get path relative to srcDir
		rel, relErr := filepath.Rel(srcDir, path)
		if relErr != nil {
			return relErr
		}

		// Skip the root itself
		if rel == "." {
			return nil
		}

		// Check exclusion patterns
		if shouldExclude(rel, d.IsDir(), agentDef.AgentFilesExclude) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		dst := filepath.Join(agentStateDir, rel)

		if d.IsDir() {
			return os.MkdirAll(dst, 0750)
		}

		// Don't overwrite files that already exist (SeedFiles win)
		if _, err := os.Stat(dst); err == nil {
			return nil
		}

		return copyFilePreserve(path, dst)
	})
}

// copyAgentFilesList copies explicit file/directory paths into agent-state.
// Each entry is copied to agent-state/basename. Missing entries are skipped.
// Existing files are not overwritten.
func copyAgentFilesList(sandboxDir string, files []string) error {
	agentStateDir := filepath.Join(sandboxDir, "agent-state")

	for _, src := range files {
		info, err := os.Stat(src)
		if os.IsNotExist(err) {
			continue // skip silently
		}
		if err != nil {
			return fmt.Errorf("stat %s: %w", src, err)
		}

		dst := filepath.Join(agentStateDir, filepath.Base(src))

		if info.IsDir() {
			// Don't overwrite if destination already exists
			if _, err := os.Stat(dst); err == nil {
				continue
			}
			if err := workspace.CopyDir(src, dst); err != nil {
				return fmt.Errorf("copy directory %s: %w", src, err)
			}
		} else {
			// Don't overwrite existing files
			if _, err := os.Stat(dst); err == nil {
				continue
			}
			if err := copyFilePreserve(src, dst); err != nil {
				return fmt.Errorf("copy file %s: %w", src, err)
			}
		}
	}

	return nil
}

// shouldExclude checks if a relative path matches any exclusion pattern.
// Patterns ending in "/" match directory names. Other patterns use
// filepath.Match against the basename.
func shouldExclude(rel string, isDir bool, patterns []string) bool {
	for _, pattern := range patterns {
		if strings.HasSuffix(pattern, "/") {
			// Directory pattern: match against any path component
			dirName := strings.TrimSuffix(pattern, "/")
			parts := strings.Split(rel, string(filepath.Separator))
			for _, part := range parts {
				if part == dirName {
					return true
				}
			}
		} else {
			// File pattern: match against basename
			base := filepath.Base(rel)
			if matched, _ := filepath.Match(pattern, base); matched {
				return true
			}
		}
	}
	return false
}

// copyFilePreserve copies a single file with mode 0600.
func copyFilePreserve(src, dst string) error {
	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(dst), 0750); err != nil {
		return err
	}

	in, err := os.Open(src) //nolint:gosec // path is from user config
	if err != nil {
		return err
	}
	defer in.Close() //nolint:errcheck // best-effort close on read-only file

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600) //nolint:gosec // dst is sandbox-controlled path
	if err != nil {
		return err
	}
	defer out.Close() //nolint:errcheck // explicit close below returns the error

	if _, err := io.Copy(out, in); err != nil {
		return err
	}

	return out.Close()
}

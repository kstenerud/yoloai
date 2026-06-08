// ABOUTME: Host-side file-exchange operations on a sandbox's files/ directory —
// ABOUTME: list, import, export, remove, with path-traversal containment.
package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
)

// FilesDir returns the host path of the sandbox's file-exchange directory.
// It does not create the directory or check that the sandbox exists.
func FilesDir(layout config.Layout, name string) string {
	return store.FilesDir(layout.SandboxDir(name))
}

// ListExchangeFiles expands the glob patterns against the exchange directory and
// returns deduplicated, sorted relative paths. An empty match set is not an
// error (callers that require a match check the length themselves).
func ListExchangeFiles(layout config.Layout, name string, patterns []string) ([]string, error) {
	return collectExchangeGlobs(FilesDir(layout, name), patterns)
}

// ImportFile copies a single host file or directory into the exchange directory,
// creating the exchange directory if needed. Returns the base name placed.
// Without force, an existing entry of the same name is an error.
func ImportFile(ctx context.Context, layout config.Layout, name, hostPath string, force bool) (string, error) {
	filesDir := FilesDir(layout, name)
	if err := fileutil.MkdirAll(filesDir, 0750); err != nil {
		return "", fmt.Errorf("create files directory: %w", err)
	}

	absSrc, err := filepath.Abs(hostPath)
	if err != nil {
		return "", fmt.Errorf("resolve path %s: %w", hostPath, err)
	}
	info, err := os.Stat(absSrc)
	if err != nil {
		return "", fmt.Errorf("source %s: %w", hostPath, err)
	}

	dst := filepath.Join(filesDir, info.Name())
	if !force {
		if _, statErr := os.Stat(dst); statErr == nil { //nolint:gosec // G703: path is under sandbox files dir
			return "", fmt.Errorf("target already exists: %s (use --overwrite to replace it)", info.Name())
		}
	}
	if err := copyTree(ctx, absSrc, dst); err != nil {
		return "", fmt.Errorf("copy %s: %w", hostPath, err)
	}
	return info.Name(), nil
}

// ExportFile copies one exchange entry (rel, relative to the exchange dir) to dst
// on the host. Without force, an existing dst is an error. rel is validated to
// stay within the exchange directory.
func ExportFile(ctx context.Context, layout config.Layout, name, rel, dst string, force bool) error {
	filesDir := FilesDir(layout, name)
	srcPath := filepath.Join(filesDir, rel)
	if err := validateExchangePath(filesDir, srcPath); err != nil {
		return err
	}
	if !force {
		if _, err := os.Stat(dst); err == nil { //nolint:gosec // G703: dst is a user-specified destination
			return fmt.Errorf("destination already exists: %s (use --overwrite to replace it)", dst)
		}
	}
	if err := copyTree(ctx, srcPath, dst); err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	return nil
}

// RemoveExchangeFile removes one exchange entry (rel, relative to the exchange
// dir). rel is validated to stay within the exchange directory.
func RemoveExchangeFile(layout config.Layout, name, rel string) error {
	filesDir := FilesDir(layout, name)
	target := filepath.Join(filesDir, rel)
	if err := validateExchangePath(filesDir, target); err != nil {
		return err
	}
	if err := os.RemoveAll(target); err != nil { //nolint:gosec // G703: path is under sandbox files dir
		return fmt.Errorf("remove %s: %w", rel, err)
	}
	return nil
}

// copyTree copies src to dst preserving mode and recursing into directories,
// honoring ctx cancellation. Uses cp -rp (matching prior CLI behavior) since the
// exchange directory may hold directory trees.
func copyTree(ctx context.Context, src, dst string) error {
	cmd := exec.CommandContext(ctx, "cp", "-rp", src, dst) //nolint:gosec // G204: paths are validated
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return nil
}

// validateExchangePath ensures a resolved path stays within the files directory.
// Prevents path traversal via patterns like "../../../etc/passwd".
func validateExchangePath(filesDir, resolved string) error {
	cleanFiles := filepath.Clean(filesDir)
	cleanResolved := filepath.Clean(resolved)
	if !strings.HasPrefix(cleanResolved, cleanFiles+string(filepath.Separator)) && cleanResolved != cleanFiles {
		return fmt.Errorf("path escapes exchange directory: %s", resolved)
	}
	return nil
}

// collectExchangeGlobs expands multiple glob patterns against the exchange
// directory. Returns deduplicated, sorted relative paths. Returns an empty slice
// (not an error) when nothing matches.
func collectExchangeGlobs(filesDir string, patterns []string) ([]string, error) {
	seen := make(map[string]bool)
	names := make([]string, 0)

	for _, pat := range patterns {
		fullPattern := filepath.Join(filesDir, pat)
		if err := validateExchangePath(filesDir, fullPattern); err != nil {
			return nil, err
		}

		matches, err := filepath.Glob(fullPattern)
		if err != nil {
			return nil, fmt.Errorf("invalid glob pattern: %w", err)
		}

		for _, m := range matches {
			rel, err := filepath.Rel(filesDir, m)
			if err != nil {
				continue
			}
			if !seen[rel] {
				seen[rel] = true
				names = append(names, rel)
			}
		}
	}

	sort.Strings(names)
	return names, nil
}

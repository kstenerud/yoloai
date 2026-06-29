// ABOUTME: Host-side file-exchange operations on a sandbox's files/ directory —
// ABOUTME: list, import, export, remove, with path-traversal + symlink containment.
package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/sysexec"
	"github.com/kstenerud/yoloai/store"
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

	dst, err := resolveExchangePath(filesDir, info.Name())
	if err != nil {
		return "", err
	}
	if !force {
		if _, statErr := os.Stat(dst); statErr == nil { //nolint:gosec // G703: path is under sandbox files dir
			return "", fmt.Errorf("target already exists: %s (use --overwrite to replace it)", info.Name())
		}
	}
	cpEnv := layout.Env().EnvForHostTool()
	if err := copyTree(ctx, cpEnv, absSrc, dst); err != nil {
		return "", fmt.Errorf("copy %s: %w", hostPath, err)
	}
	return info.Name(), nil
}

// ExportFile copies one exchange entry (rel, relative to the exchange dir) to dst
// on the host. Without force, an existing dst is an error. rel is validated to
// stay within the exchange directory.
func ExportFile(ctx context.Context, layout config.Layout, name, rel, dst string, force bool) error {
	filesDir := FilesDir(layout, name)
	srcPath, err := resolveExchangePath(filesDir, rel)
	if err != nil {
		return err
	}
	if !force {
		if _, err := os.Stat(dst); err == nil { //nolint:gosec // G703: dst is a user-specified destination
			return fmt.Errorf("destination already exists: %s (use --overwrite to replace it)", dst)
		}
	}
	cpEnv := layout.Env().EnvForHostTool()
	if err := copyTree(ctx, cpEnv, srcPath, dst); err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	return nil
}

// RemoveExchangeFile removes one exchange entry (rel, relative to the exchange
// dir). rel is validated to stay within the exchange directory.
func RemoveExchangeFile(layout config.Layout, name, rel string) error {
	filesDir := FilesDir(layout, name)
	target, err := resolveExchangePath(filesDir, rel)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(target); err != nil { //nolint:gosec // G703: path is under sandbox files dir
		return fmt.Errorf("remove %s: %w", rel, err)
	}
	return nil
}

// ReadExchangeFile returns the bytes of one exchange entry (rel, relative to the
// exchange dir). rel is validated to stay within the exchange directory, so a
// content-oriented consumer never needs its own path-traversal guard.
func ReadExchangeFile(layout config.Layout, name, rel string) ([]byte, error) {
	filesDir := FilesDir(layout, name)
	target, err := resolveExchangePath(filesDir, rel)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(target) //nolint:gosec // G304: target validated by resolveExchangePath
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("file %q not found in exchange directory", rel)
		}
		return nil, fmt.Errorf("read %s: %w", rel, err)
	}
	return data, nil
}

// WriteExchangeFile writes data to one exchange entry (rel, relative to the
// exchange dir), creating the exchange directory and any parent directories as
// needed. rel is validated to stay within the exchange directory. Files are
// written 0600 (owner-only), matching the prior file-exchange write path.
func WriteExchangeFile(layout config.Layout, name, rel string, data []byte) error {
	filesDir := FilesDir(layout, name)
	target, err := resolveExchangePath(filesDir, rel)
	if err != nil {
		return err
	}
	if err := fileutil.MkdirAll(filepath.Dir(target), 0750); err != nil {
		return fmt.Errorf("create files directory: %w", err)
	}
	if err := fileutil.WriteFile(target, data, 0600); err != nil {
		return fmt.Errorf("write %s: %w", rel, err)
	}
	return nil
}

// copyTree copies src to dst preserving mode and recursing into directories,
// honoring ctx cancellation. Uses cp -rp (matching prior CLI behavior) since the
// exchange directory may hold directory trees.
func copyTree(ctx context.Context, env []string, src, dst string) error {
	cmd := sysexec.CommandContext(ctx, env, "cp", "-rp", src, dst)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return nil
}

// resolveExchangePath is the single containment entry point for every host-side
// exchange operation that acts on a caller-supplied relative path. It joins rel
// onto filesDir and verifies the result is safely contained two ways: lexically
// within the exchange dir (validateExchangePath) AND not reached through any
// symlink planted inside it (assertNoSymlinkInExchange). Returns the validated
// absolute target path.
func resolveExchangePath(filesDir, rel string) (string, error) {
	target := filepath.Join(filesDir, rel)
	if err := validateExchangePath(filesDir, target); err != nil {
		return "", err
	}
	if err := assertNoSymlinkInExchange(filesDir, target); err != nil {
		return "", err
	}
	return target, nil
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

// assertNoSymlinkInExchange verifies that no path component between filesDir
// (exclusive) and target (inclusive) is a symlink. The exchange directory is
// bind-mounted read-write into the container, so the untrusted agent inside can
// plant a symlink — e.g. answer.json -> ~/.ssh/id_rsa, or a sub/ -> /home dir
// symlink — that a host-side read/write/remove/export would otherwise follow out
// of the sandbox (host-file exfil or overwrite). Symlinks inside the exchange
// dir are never a supported use, so we refuse to traverse them (matching the
// symlink-rejection stance in internal/workspace/copy.go). Components that do
// not exist yet (a write creating new entries) are fine — there is nothing to
// follow. Callers must pass a target already lexically contained in filesDir.
//
// This blocks the plant-and-wait attack completely; a residual TOCTOU race
// (swapping a real file for a symlink between this check and the open) would
// require openat2(RESOLVE_NO_SYMLINKS), which is Linux-only and not portable to
// the macOS host backends.
func assertNoSymlinkInExchange(filesDir, target string) error {
	rel, err := filepath.Rel(filesDir, target)
	if err != nil {
		return fmt.Errorf("path escapes exchange directory: %s", target)
	}
	if rel == "." {
		return nil // the exchange dir itself
	}
	cur := filepath.Clean(filesDir)
	for part := range strings.SplitSeq(rel, string(filepath.Separator)) {
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if err != nil {
			if os.IsNotExist(err) {
				return nil // remaining components don't exist yet; nothing to follow
			}
			return fmt.Errorf("inspect exchange path %s: %w", rel, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("exchange path component is a symlink (refused): %s", rel)
		}
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

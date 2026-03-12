package cli

// ABOUTME: CLI commands for bidirectional file exchange between host and sandbox.
// ABOUTME: Implements put, get, ls, rm, and path subcommands for the files dir.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kstenerud/yoloai/sandbox"
	"github.com/spf13/cobra"
)

func newFilesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "files",
		Short:   "Exchange files with a sandbox",
		GroupID: groupWorkflow,
	}

	cmd.AddCommand(
		newFilesPutCmd(),
		newFilesGetCmd(),
		newFilesLsCmd(),
		newFilesRmCmd(),
		newFilesPathCmd(),
	)

	return cmd
}

func newFilesPutCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "put <sandbox> <file/glob>...",
		Short: "Copy files into sandbox exchange directory",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, rest, err := resolveName(cmd, args)
			if err != nil {
				return err
			}
			if len(rest) == 0 {
				return sandbox.NewUsageError("at least one file is required")
			}

			if _, err := sandbox.RequireSandboxDir(name); err != nil {
				return err
			}
			filesDir := sandbox.FilesDir(name)
			force, _ := cmd.Flags().GetBool("force")

			expanded, err := expandHostGlobs(rest)
			if err != nil {
				return err
			}

			for _, src := range expanded {
				absSrc, err := filepath.Abs(src)
				if err != nil {
					return fmt.Errorf("resolve path %s: %w", src, err)
				}

				info, err := os.Stat(absSrc)
				if err != nil {
					return fmt.Errorf("source %s: %w", src, err)
				}

				dst := filepath.Join(filesDir, info.Name())
				if !force {
					if _, err := os.Stat(dst); err == nil {
						return fmt.Errorf("target already exists: %s (use --force to overwrite)", info.Name())
					}
				}

				cpCmd := exec.Command("cp", "-rp", absSrc, dst) //nolint:gosec // G204: paths are validated
				if out, err := cpCmd.CombinedOutput(); err != nil {
					return fmt.Errorf("copy %s: %s", src, strings.TrimSpace(string(out)))
				}

				fmt.Fprintln(cmd.OutOrStdout(), info.Name()) //nolint:errcheck // best-effort output
			}
			return nil
		},
	}
	cmd.Flags().Bool("force", false, "Overwrite existing files")
	return cmd
}

func newFilesGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <sandbox> <file/glob>... [-o dir]",
		Short: "Copy files from sandbox exchange directory",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, rest, err := resolveName(cmd, args)
			if err != nil {
				return err
			}
			if len(rest) == 0 {
				return sandbox.NewUsageError("file name is required")
			}

			if _, err := sandbox.RequireSandboxDir(name); err != nil {
				return err
			}
			filesDir := sandbox.FilesDir(name)

			files, err := expandExchangeGlobs(filesDir, rest)
			if err != nil {
				return err
			}

			dst, _ := cmd.Flags().GetString("output")
			force, _ := cmd.Flags().GetBool("force")

			absDst, err := filepath.Abs(dst)
			if err != nil {
				return fmt.Errorf("resolve destination: %w", err)
			}

			// Multiple files require destination to be a directory
			if len(files) > 1 {
				info, err := os.Stat(absDst)
				if err != nil {
					return fmt.Errorf("destination directory does not exist: %s", absDst)
				}
				if !info.IsDir() {
					return fmt.Errorf("destination must be a directory when getting multiple files: %s", absDst)
				}
			}

			for _, rel := range files {
				srcPath := filepath.Join(filesDir, rel)

				// Compute final destination for this file
				fileDst := absDst
				if info, err := os.Stat(fileDst); err == nil && info.IsDir() {
					fileDst = filepath.Join(fileDst, filepath.Base(rel))
				}

				if !force {
					if _, err := os.Stat(fileDst); err == nil {
						return fmt.Errorf("destination already exists: %s (use --force to overwrite)", fileDst)
					}
				}

				cpCmd := exec.Command("cp", "-rp", srcPath, fileDst) //nolint:gosec // G204: paths are validated
				if out, err := cpCmd.CombinedOutput(); err != nil {
					return fmt.Errorf("copy: %s", strings.TrimSpace(string(out)))
				}

				fmt.Fprintln(cmd.OutOrStdout(), fileDst) //nolint:errcheck // best-effort output
			}
			return nil
		},
	}
	cmd.Flags().Bool("force", false, "Overwrite existing destination files")
	cmd.Flags().StringP("output", "o", ".", "Destination directory (or file for single get)")
	return cmd
}

func newFilesLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls <sandbox> [glob]...",
		Short: "List files in sandbox exchange directory",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, rest, err := resolveName(cmd, args)
			if err != nil {
				return err
			}

			if _, err := sandbox.RequireSandboxDir(name); err != nil {
				return err
			}
			filesDir := sandbox.FilesDir(name)

			patterns := rest
			if len(patterns) == 0 {
				patterns = []string{"*"}
			}

			names, err := collectExchangeGlobs(filesDir, patterns)
			if err != nil {
				return err
			}

			for _, n := range names {
				fmt.Fprintln(cmd.OutOrStdout(), n) //nolint:errcheck // best-effort output
			}
			return nil
		},
	}
}

func newFilesRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <sandbox> <glob>...",
		Short: "Remove files from sandbox exchange directory",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, rest, err := resolveName(cmd, args)
			if err != nil {
				return err
			}
			if len(rest) == 0 {
				return sandbox.NewUsageError("glob pattern is required")
			}

			if _, err := sandbox.RequireSandboxDir(name); err != nil {
				return err
			}
			filesDir := sandbox.FilesDir(name)

			matches, err := expandExchangeGlobs(filesDir, rest)
			if err != nil {
				return err
			}

			for _, rel := range matches {
				m := filepath.Join(filesDir, rel)
				if err := os.RemoveAll(m); err != nil {
					return fmt.Errorf("remove %s: %w", rel, err)
				}
				fmt.Fprintln(cmd.OutOrStdout(), rel) //nolint:errcheck // best-effort output
			}
			return nil
		},
	}
}

func newFilesPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path <sandbox>",
		Short: "Print host path to sandbox exchange directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _, err := resolveName(cmd, args)
			if err != nil {
				return err
			}

			if _, err := sandbox.RequireSandboxDir(name); err != nil {
				return err
			}

			fmt.Fprintln(cmd.OutOrStdout(), sandbox.FilesDir(name)) //nolint:errcheck // best-effort output
			return nil
		},
	}
}

// validateExchangePath ensures a resolved path stays within the files directory.
// Prevents path traversal attacks via patterns like "../../../etc/passwd".
func validateExchangePath(filesDir, resolved string) error {
	cleanFiles := filepath.Clean(filesDir)
	cleanResolved := filepath.Clean(resolved)
	if !strings.HasPrefix(cleanResolved, cleanFiles+string(filepath.Separator)) && cleanResolved != cleanFiles {
		return fmt.Errorf("path escapes exchange directory: %s", resolved)
	}
	return nil
}

// hasGlobMeta reports whether s contains glob metacharacters.
func hasGlobMeta(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

// collectExchangeGlobs expands multiple glob patterns against the exchange
// directory. Returns deduplicated, sorted relative paths. Returns an empty
// slice (not an error) when nothing matches.
func collectExchangeGlobs(filesDir string, patterns []string) ([]string, error) {
	seen := make(map[string]bool)
	var names []string

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

// expandExchangeGlobs wraps collectExchangeGlobs and returns an error if no
// files match any of the patterns.
func expandExchangeGlobs(filesDir string, patterns []string) ([]string, error) {
	names, err := collectExchangeGlobs(filesDir, patterns)
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no files match pattern: %s", strings.Join(patterns, " "))
	}
	return names, nil
}

// expandHostGlobs expands arguments that may be literal paths or glob patterns
// on the host filesystem. For each arg, tries os.Stat first (literal path);
// if that fails and the arg has glob metacharacters, expands with filepath.Glob.
// Returns deduplicated results in argument order.
func expandHostGlobs(args []string) ([]string, error) {
	seen := make(map[string]bool)
	var result []string

	for _, arg := range args {
		if _, err := os.Stat(arg); err == nil {
			// Literal path exists — use it directly
			if !seen[arg] {
				seen[arg] = true
				result = append(result, arg)
			}
			continue
		}

		if hasGlobMeta(arg) {
			matches, err := filepath.Glob(arg)
			if err != nil {
				return nil, fmt.Errorf("invalid glob pattern %s: %w", arg, err)
			}
			if len(matches) == 0 {
				return nil, fmt.Errorf("no files match pattern: %s", arg)
			}
			for _, m := range matches {
				if !seen[m] {
					seen[m] = true
					result = append(result, m)
				}
			}
			continue
		}

		// Not a glob, doesn't exist — pass through for later error handling
		if !seen[arg] {
			seen[arg] = true
			result = append(result, arg)
		}
	}

	return result, nil
}

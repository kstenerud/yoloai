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

	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

func newFilesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "files",
		Short:   "Exchange files with a sandbox",
		GroupID: groupAdmin,
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
		Use:   "put <sandbox> <file>...",
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

			for _, src := range rest {
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
		Use:   "get <sandbox> <file> [dst]",
		Short: "Copy a file from sandbox exchange directory",
		Args:  cobra.RangeArgs(2, 3),
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

			srcPath := filepath.Join(filesDir, rest[0])
			if err := validateExchangePath(filesDir, srcPath); err != nil {
				return err
			}

			if _, err := os.Stat(srcPath); err != nil {
				return fmt.Errorf("file not found in exchange directory: %s", rest[0])
			}

			dst := "."
			if len(rest) >= 2 {
				dst = rest[1]
			}

			absDst, err := filepath.Abs(dst)
			if err != nil {
				return fmt.Errorf("resolve destination: %w", err)
			}

			// If dst is a directory, place file inside it
			if info, err := os.Stat(absDst); err == nil && info.IsDir() {
				absDst = filepath.Join(absDst, filepath.Base(rest[0]))
			}

			force, _ := cmd.Flags().GetBool("force")
			if !force {
				if _, err := os.Stat(absDst); err == nil {
					return fmt.Errorf("destination already exists: %s (use --force to overwrite)", absDst)
				}
			}

			cpCmd := exec.Command("cp", "-rp", srcPath, absDst) //nolint:gosec // G204: paths are validated
			if out, err := cpCmd.CombinedOutput(); err != nil {
				return fmt.Errorf("copy: %s", strings.TrimSpace(string(out)))
			}

			fmt.Fprintln(cmd.OutOrStdout(), absDst) //nolint:errcheck // best-effort output
			return nil
		},
	}
	cmd.Flags().Bool("force", false, "Overwrite existing destination file")
	return cmd
}

func newFilesLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls <sandbox> [glob]",
		Short: "List files in sandbox exchange directory",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, rest, err := resolveName(cmd, args)
			if err != nil {
				return err
			}

			if _, err := sandbox.RequireSandboxDir(name); err != nil {
				return err
			}
			filesDir := sandbox.FilesDir(name)

			glob := "*"
			if len(rest) >= 1 {
				glob = rest[0]
			}

			pattern := filepath.Join(filesDir, glob)
			if err := validateExchangePath(filesDir, pattern); err != nil {
				return err
			}

			matches, err := filepath.Glob(pattern)
			if err != nil {
				return fmt.Errorf("invalid glob pattern: %w", err)
			}

			names := make([]string, 0, len(matches))
			for _, m := range matches {
				rel, err := filepath.Rel(filesDir, m)
				if err != nil {
					continue
				}
				names = append(names, rel)
			}
			sort.Strings(names)

			for _, n := range names {
				fmt.Fprintln(cmd.OutOrStdout(), n) //nolint:errcheck // best-effort output
			}
			return nil
		},
	}
}

func newFilesRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <sandbox> <glob>",
		Short: "Remove files from sandbox exchange directory",
		Args:  cobra.ExactArgs(2),
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

			pattern := filepath.Join(filesDir, rest[0])
			if err := validateExchangePath(filesDir, pattern); err != nil {
				return err
			}

			matches, err := filepath.Glob(pattern)
			if err != nil {
				return fmt.Errorf("invalid glob pattern: %w", err)
			}

			if len(matches) == 0 {
				return fmt.Errorf("no files match pattern: %s", rest[0])
			}

			for _, m := range matches {
				rel, err := filepath.Rel(filesDir, m)
				if err != nil {
					continue
				}
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

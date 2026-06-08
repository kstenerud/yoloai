package workflow

// ABOUTME: CLI commands for bidirectional file exchange between host and sandbox.
// ABOUTME: Implements put, get, ls, rm, and path subcommands for the files dir.
// ABOUTME: Uses name-first dispatch: `yoloai files <sandbox> <subcommand> [args...]`.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	yoloai "github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/internal/cli/cliutil"
	"github.com/kstenerud/yoloai/yoerrors"
	"github.com/spf13/cobra"
)

// filesSubcmds is the set of known files subcommands.
var filesSubcmds = map[string]bool{
	"put": true, "get": true, "ls": true, "rm": true, "path": true,
}

func NewFilesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "files <sandbox> <subcommand> [args...]",
		Short: "Exchange files with a sandbox",
		Long: `Exchange files with a sandbox.

Subcommands:
  put <file/glob>...         Copy files into sandbox exchange directory
  get <file/glob>... [-o dir] Copy files from sandbox exchange directory
  ls [glob]...               List files in sandbox exchange directory
  rm <glob>...               Remove files from sandbox exchange directory
  path                       Print host path to sandbox exchange directory`,
		GroupID:            cliutil.GroupWorkflow,
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: false,
		RunE:               filesDispatch,
	}
	cmd.Flags().Bool("overwrite", false, "Overwrite existing files")
	cmd.Flags().StringP("output", "o", ".", "Destination directory (or file for single get)")
	return cmd
}

func filesDispatch(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}

	var name, subcmd string
	var rest []string

	if filesSubcmds[args[0]] {
		// args[0] is a subcommand — name must come from YOLOAI_SANDBOX
		envName := cliutil.SandboxNameFromEnv()
		if envName == "" {
			return yoerrors.NewUsageError("sandbox name required before subcommand (or set YOLOAI_SANDBOX)")
		}
		if err := cliutil.ValidateName(envName); err != nil {
			return err
		}
		name = envName
		subcmd = args[0]
		rest = args[1:]
	} else {
		if err := cliutil.ValidateName(args[0]); err != nil {
			return err
		}
		name = args[0]
		if len(args) < 2 {
			return yoerrors.NewUsageError("subcommand required: put, get, ls, rm, path")
		}
		subcmd = args[1]
		rest = args[2:]
	}

	c, err := cliutil.Client(cmd)
	if err != nil {
		return err
	}
	defer c.Close() //nolint:errcheck // best-effort cleanup
	sb, err := c.Sandbox(name)
	if err != nil {
		return cliutil.SandboxErrorHint(name, err)
	}
	files := sb.Files()

	switch subcmd {
	case "put":
		return runFilesPut(cmd, files, rest)
	case "get":
		return runFilesGet(cmd, files, rest)
	case "ls":
		return runFilesLs(cmd, files, rest)
	case "rm":
		return runFilesRm(cmd, files, rest)
	case "path":
		return runFilesPath(cmd, files)
	default:
		return yoerrors.NewUsageError("unknown subcommand %q: valid subcommands are put, get, ls, rm, path", subcmd)
	}
}

func runFilesPut(cmd *cobra.Command, files *yoloai.Files, args []string) error {
	if len(args) == 0 {
		return yoerrors.NewUsageError("at least one file is required")
	}
	overwrite, _ := cmd.Flags().GetBool("overwrite")

	expanded, err := expandHostGlobs(args)
	if err != nil {
		return err
	}

	for _, src := range expanded {
		placed, err := files.Import(cmd.Context(), src, overwrite)
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), placed) //nolint:errcheck // best-effort output
	}
	return nil
}

func runFilesGet(cmd *cobra.Command, files *yoloai.Files, args []string) error {
	if len(args) == 0 {
		return yoerrors.NewUsageError("file name is required")
	}

	matches, err := files.List(args)
	if err != nil {
		return err
	}
	if len(matches) == 0 {
		return fmt.Errorf("no files match pattern: %s", strings.Join(args, " "))
	}

	dst, _ := cmd.Flags().GetString("output")
	overwrite, _ := cmd.Flags().GetBool("overwrite")

	absDst, err := filepath.Abs(dst)
	if err != nil {
		return fmt.Errorf("resolve destination: %w", err)
	}

	// Multiple files require destination to be a directory
	if len(matches) > 1 {
		info, err := os.Stat(absDst)
		if err != nil {
			return fmt.Errorf("destination directory does not exist: %s", absDst)
		}
		if !info.IsDir() {
			return fmt.Errorf("destination must be a directory when getting multiple files: %s", absDst)
		}
	}

	for _, rel := range matches {
		// Compute final destination for this file
		fileDst := absDst
		if info, statErr := os.Stat(fileDst); statErr == nil && info.IsDir() {
			fileDst = filepath.Join(fileDst, filepath.Base(rel))
		}
		if err := files.Export(cmd.Context(), rel, fileDst, overwrite); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), fileDst) //nolint:errcheck // best-effort output
	}
	return nil
}

func runFilesLs(cmd *cobra.Command, files *yoloai.Files, args []string) error {
	patterns := args
	if len(patterns) == 0 {
		patterns = []string{"*"}
	}

	names, err := files.List(patterns)
	if err != nil {
		return err
	}

	for _, n := range names {
		fmt.Fprintln(cmd.OutOrStdout(), n) //nolint:errcheck // best-effort output
	}
	return nil
}

func runFilesRm(cmd *cobra.Command, files *yoloai.Files, args []string) error {
	if len(args) == 0 {
		return yoerrors.NewUsageError("glob pattern is required")
	}

	matches, err := files.List(args)
	if err != nil {
		return err
	}
	if len(matches) == 0 {
		return fmt.Errorf("no files match pattern: %s", strings.Join(args, " "))
	}

	for _, rel := range matches {
		if err := files.Remove(rel); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), rel) //nolint:errcheck // best-effort output
	}
	return nil
}

func runFilesPath(cmd *cobra.Command, files *yoloai.Files) error {
	fmt.Fprintln(cmd.OutOrStdout(), files.Path()) //nolint:errcheck // best-effort output
	return nil
}

// hasGlobMeta reports whether s contains glob metacharacters.
func hasGlobMeta(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

// expandHostGlobs expands arguments that may be literal paths or glob patterns
// on the host filesystem. For each arg, tries os.Stat first (literal path);
// if that fails and the arg has glob metacharacters, expands with filepath.Glob.
// Returns deduplicated results in argument order.
func expandHostGlobs(args []string) ([]string, error) {
	seen := make(map[string]bool)
	var result []string

	for _, arg := range args {
		expanded, err := expandHostGlob(arg)
		if err != nil {
			return nil, err
		}
		for _, p := range expanded {
			if !seen[p] {
				seen[p] = true
				result = append(result, p)
			}
		}
	}

	return result, nil
}

// expandHostGlob expands a single argument into one or more paths.
// Returns the literal path if it exists, glob matches if it contains metacharacters,
// or the original arg (pass-through for later error handling).
func expandHostGlob(arg string) ([]string, error) {
	if _, err := os.Stat(arg); err == nil { //nolint:gosec // G703: path is CLI-supplied by the user
		return []string{arg}, nil
	}
	if hasGlobMeta(arg) {
		matches, err := filepath.Glob(arg)
		if err != nil {
			return nil, fmt.Errorf("invalid glob pattern %s: %w", arg, err)
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("no files match pattern: %s", arg)
		}
		return matches, nil
	}
	// Not a glob, doesn't exist — pass through for later error handling
	return []string{arg}, nil
}

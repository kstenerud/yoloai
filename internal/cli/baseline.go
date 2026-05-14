// ABOUTME: Implements the `yoloai baseline` command with `advance` and `set`
// ABOUTME: subcommands for manually correcting stuck sandbox baselines after
// ABOUTME: stash-pop conflicts or non-contiguous selective applies.
package cli

import (
	"fmt"
	"strings"

	"github.com/kstenerud/yoloai/sandbox"
	"github.com/kstenerud/yoloai/workspace"
	"github.com/spf13/cobra"
)

func newBaselineCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "baseline <subcommand>",
		Short:   "Manage sandbox baseline SHA",
		GroupID: groupWorkflow,
	}
	cmd.AddCommand(newBaselineAdvanceCmd(), newBaselineSetCmd())
	return cmd
}

// newBaselineAdvanceCmd implements `yoloai baseline advance <name>`.
// It moves the baseline to the current HEAD of the sandbox work copy.
func newBaselineAdvanceCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "advance <name>",
		Short: "Move baseline to current HEAD of the sandbox work copy",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			meta, workDir, err := loadBaselineContext(name)
			if err != nil {
				return err
			}
			_ = meta // mode already validated inside loadBaselineContext

			sha, err := workspace.HeadSHA(workDir)
			if err != nil {
				return fmt.Errorf("resolve HEAD: %w", err)
			}
			return advanceBaselineAndPrint(cmd, name, sha, workDir)
		},
	}
}

// newBaselineSetCmd implements `yoloai baseline set <name> <sha>`.
// It moves the baseline to the given commit (short SHA accepted).
func newBaselineSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <name> <sha>",
		Short: "Move baseline to a specific commit SHA",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, shortSHA := args[0], args[1]
			_, workDir, err := loadBaselineContext(name)
			if err != nil {
				return err
			}

			// Resolve short SHA to full SHA via git.
			out, err := workspace.NewGitCmd(workDir, "rev-parse", shortSHA).Output()
			if err != nil {
				return fmt.Errorf("resolve sha %q: %w", shortSHA, err)
			}
			sha := strings.TrimSpace(string(out))

			return advanceBaselineAndPrint(cmd, name, sha, workDir)
		},
	}
}

// loadBaselineContext loads the sandbox metadata, validates that the workdir
// mode supports baseline management (:copy only), and returns the work copy
// path on the host. Returns a clear user-facing error for :rw and :overlay.
func loadBaselineContext(name string) (*sandbox.Meta, string, error) {
	meta, err := sandbox.LoadMeta(sandbox.Dir(name))
	if err != nil {
		return nil, "", sandboxErrorHint(name, err)
	}

	switch meta.Workdir.Mode {
	case "rw":
		return nil, "", sandbox.NewUsageError("baseline is not tracked for :rw directories")
	case "overlay":
		return nil, "", sandbox.NewUsageError("use git commands inside the container to manage overlay baselines")
	}

	workDir := sandbox.WorkDir(name, meta.Workdir.HostPath)
	return meta, workDir, nil
}

// advanceBaselineAndPrint updates the baseline to sha and prints a
// confirmation line: "Baseline advanced to <short-sha> (<subject>)".
func advanceBaselineAndPrint(cmd *cobra.Command, name, sha, workDir string) error {
	if err := sandbox.AdvanceBaselineTo(name, sha); err != nil {
		return fmt.Errorf("update baseline: %w", err)
	}

	// Fetch the commit subject for the confirmation message.
	out, err := workspace.NewGitCmd(workDir, "log", "--format=%s", "-1", sha).Output()
	if err != nil {
		// Non-fatal: print without subject.
		_, werr := fmt.Fprintf(cmd.OutOrStdout(), "Baseline advanced to %.8s\n", sha)
		return werr
	}
	subject := strings.TrimSpace(string(out))
	shortSHA := sha
	if len(shortSHA) > 8 {
		shortSHA = shortSHA[:8]
	}
	_, werr := fmt.Fprintf(cmd.OutOrStdout(), "Baseline advanced to %s (%s)\n", shortSHA, subject)
	return werr
}

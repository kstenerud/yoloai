package cli

import (
	"fmt"
	"os"

	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

func newApplyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "apply <name> [<path>...]",
		Short:   "Apply agent changes back to original directory",
		GroupID: groupWorkflow,
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, paths, err := resolveName(cmd, args)
			if err != nil {
				return err
			}

			yes, _ := cmd.Flags().GetBool("yes")

			// Load metadata for target directory and mode validation
			meta, err := sandbox.LoadMeta(sandbox.Dir(name))
			if err != nil {
				return err
			}
			if meta.Workdir.Mode == "rw" {
				return fmt.Errorf("apply is not needed for :rw directories — changes are already live")
			}

			// Generate patch
			patch, stat, err := sandbox.GeneratePatch(name, paths)
			if err != nil {
				return err
			}
			if len(patch) == 0 {
				_, err = fmt.Fprintln(cmd.OutOrStdout(), "No changes to apply")
				return err
			}

			// Show stat summary
			fmt.Fprintln(cmd.OutOrStdout(), stat) //nolint:errcheck // best-effort output

			targetDir := meta.Workdir.HostPath
			isGit := sandbox.IsGitRepo(targetDir)

			// Dry-run check
			if err := sandbox.CheckPatch(patch, targetDir, isGit); err != nil {
				return err
			}

			// Confirmation
			if !yes {
				prompt := fmt.Sprintf("Apply these changes to %s? [y/N] ", targetDir)
				if !sandbox.Confirm(prompt, os.Stdin, cmd.ErrOrStderr()) {
					return nil
				}
			}

			// Apply
			if err := sandbox.ApplyPatch(patch, targetDir, isGit); err != nil {
				return err
			}

			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Changes applied to %s\n", targetDir)
			return err
		},
	}

	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")

	return cmd
}

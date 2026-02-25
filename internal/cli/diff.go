package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/kstenerud/yoloai/internal/docker"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

func newDiffCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "diff <name> [<path>...]",
		Short:   "Show changes the agent made",
		GroupID: groupWorkflow,
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, paths, err := resolveName(cmd, args)
			if err != nil {
				return err
			}

			stat, _ := cmd.Flags().GetBool("stat")

			// Best-effort agent-running warning
			agentRunningWarning(cmd, name)

			opts := sandbox.DiffOptions{
				Name:  name,
				Paths: paths,
			}

			if stat {
				result, err := sandbox.GenerateDiffStat(opts)
				if err != nil {
					return err
				}
				if result.Empty {
					_, err = fmt.Fprintln(cmd.OutOrStdout(), "No changes")
					return err
				}
				_, err = fmt.Fprintln(cmd.OutOrStdout(), result.Output)
				return err
			}

			result, err := sandbox.GenerateDiff(opts)
			if err != nil {
				return err
			}
			if result.Empty {
				_, err = fmt.Fprintln(cmd.OutOrStdout(), "No changes")
				return err
			}

			return RunPager(strings.NewReader(result.Output + "\n"))
		},
	}

	cmd.Flags().Bool("stat", false, "Show summary (files changed, insertions, deletions)")

	return cmd
}

// agentRunningWarning prints a warning to stderr if the agent is still running.
// Silently skips if Docker is unavailable or inspection fails.
func agentRunningWarning(cmd *cobra.Command, name string) {
	_ = withClient(cmd, func(ctx context.Context, client docker.Client) error {
		info, err := sandbox.InspectSandbox(ctx, client, name)
		if err != nil {
			return nil // silently skip
		}

		if info.Status == sandbox.StatusRunning {
			fmt.Fprintln(cmd.ErrOrStderr(), "Note: agent is still running; diff may be incomplete") //nolint:errcheck // best-effort warning
		}
		return nil
	})
}

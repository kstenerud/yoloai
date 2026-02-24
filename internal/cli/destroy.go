package cli

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/kstenerud/yoloai/internal/docker"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

func newDestroyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "destroy <name>...",
		Short: "Stop and remove sandboxes",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			all, _ := cmd.Flags().GetBool("all")
			yes, _ := cmd.Flags().GetBool("yes")

			ctx := cmd.Context()
			client, err := docker.NewClient(ctx)
			if err != nil {
				return err
			}
			defer client.Close() //nolint:errcheck // best-effort cleanup

			mgr := sandbox.NewManager(client, slog.Default(), cmd.ErrOrStderr())

			var names []string
			if all {
				infos, err := sandbox.ListSandboxes(ctx, client)
				if err != nil {
					return err
				}
				if len(infos) == 0 {
					_, err = fmt.Fprintln(cmd.OutOrStdout(), "No sandboxes to destroy")
					return err
				}
				for _, info := range infos {
					names = append(names, info.Meta.Name)
				}
			} else {
				if len(args) == 0 {
					return sandbox.NewUsageError("at least one sandbox name is required (or use --all)")
				}
				names = args
			}

			// Smart confirmation (unless --yes)
			if !yes {
				var warnings []string
				for _, name := range names {
					needs, reason := mgr.NeedsConfirmation(ctx, name)
					if needs {
						warnings = append(warnings, fmt.Sprintf("  %s: %s", name, reason))
					}
				}
				if len(warnings) > 0 {
					fmt.Fprintln(cmd.ErrOrStderr(), "The following sandboxes have active work:") //nolint:errcheck // best-effort output
					for _, w := range warnings {
						fmt.Fprintln(cmd.ErrOrStderr(), w) //nolint:errcheck // best-effort output
					}
					if !sandbox.Confirm("Destroy all listed sandboxes? [y/N] ", os.Stdin, cmd.ErrOrStderr()) {
						return nil
					}
				}
			}

			var errs []error
			for _, name := range names {
				if err := mgr.Destroy(ctx, name, true); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: destroy %s: %v\n", name, err) //nolint:errcheck // best-effort output
					errs = append(errs, err)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "Destroyed %s\n", name) //nolint:errcheck // best-effort output
				}
			}

			if len(errs) > 0 {
				return fmt.Errorf("failed to destroy %d sandbox(es)", len(errs))
			}
			return nil
		},
	}

	cmd.Flags().Bool("all", false, "Destroy all sandboxes")
	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")

	return cmd
}

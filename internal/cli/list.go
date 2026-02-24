package cli

import (
	"fmt"
	"log/slog"
	"text/tabwriter"

	"github.com/kstenerud/yoloai/internal/docker"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List sandboxes and their status",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			client, err := docker.NewClient(ctx)
			if err != nil {
				return err
			}
			defer client.Close() //nolint:errcheck // best-effort cleanup

			infos, err := sandbox.ListSandboxes(ctx, client)
			if err != nil {
				return err
			}

			if len(infos) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No sandboxes found") //nolint:errcheck // best-effort output
				return nil
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "NAME\tSTATUS\tAGENT\tAGE\tWORKDIR\tCHANGES") //nolint:errcheck // best-effort output
			for _, info := range infos {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", //nolint:errcheck // best-effort output
					info.Meta.Name,
					info.Status,
					info.Meta.Agent,
					sandbox.FormatAge(info.Meta.CreatedAt),
					info.Meta.Workdir.HostPath,
					info.HasChanges,
				)
			}
			if err := w.Flush(); err != nil {
				return err
			}

			slog.Debug("list complete", "count", len(infos))
			return nil
		},
	}
}

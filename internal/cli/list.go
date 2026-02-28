package cli

// ABOUTME: Sandbox listing logic shared by `yoloai sandbox list` and the
// ABOUTME: top-level `yoloai ls` shortcut.

import (
	"context"
	"fmt"
	"log/slog"
	"text/tabwriter"

	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

func newSandboxListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List sandboxes and their status",
		Args:  cobra.NoArgs,
		RunE:  runList,
	}
}

// runList is the shared implementation for `sandbox list` and the `ls` alias.
func runList(cmd *cobra.Command, _ []string) error {
	backend := resolveBackendFromConfig()
	return withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
		infos, err := sandbox.ListSandboxes(ctx, rt)
		if err != nil {
			return err
		}

		if len(infos) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No sandboxes found") //nolint:errcheck
			return nil
		}

		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "NAME\tSTATUS\tAGENT\tAGE\tSIZE\tWORKDIR\tCHANGES") //nolint:errcheck
		for _, info := range infos {
			if info.Status == sandbox.StatusBroken {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", //nolint:errcheck
					info.Meta.Name,
					info.Status,
					"-",
					"-",
					info.DiskUsage,
					"-",
					"-",
				)
				continue
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", //nolint:errcheck
				info.Meta.Name,
				info.Status,
				info.Meta.Agent,
				sandbox.FormatAge(info.Meta.CreatedAt),
				info.DiskUsage,
				info.Meta.Workdir.HostPath,
				info.HasChanges,
			)
		}
		if err := w.Flush(); err != nil {
			return err
		}

		slog.Debug("list complete", "count", len(infos)) //nolint:gosec
		return nil
	})
}

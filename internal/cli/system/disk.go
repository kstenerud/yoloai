package system

// ABOUTME: `yoloai system disk` reports backend cache usage so users can spot
// when it's time to run `yoloai system prune --cache`.

import (
	"fmt"
	"text/tabwriter"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	"github.com/kstenerud/yoloai"
	"github.com/spf13/cobra"
)

func newSystemDiskCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "disk",
		Short: "Report on-disk usage for yoloai and its backends",
		Long: `Report on-disk usage for yoloai and its registered backends.

Surfaces how much space each container backend is consuming so you can spot
when it's time to run 'yoloai system prune --cache'. Backend cache sizes
include all images / snapshots / volumes the backend tracks — not just
yoloai's — because the backend doesn't tag content by who created it.`,
		Args: cobra.NoArgs,
		RunE: runSystemDisk,
	}
}

func runSystemDisk(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()

	du, err := cliutil.NewSystemClient().DiskUsage(ctx)
	if err != nil {
		return err
	}

	if cliutil.JSONEnabled(cmd) {
		return cliutil.WriteJSON(out, formatDiskJSON(du, cliutil.Layout().SandboxesDir()))
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SOURCE\tSIZE\tDETAIL")                                                                  //nolint:errcheck
	fmt.Fprintf(w, "sandboxes\t%s\t%s\n", cliutil.HumanBytes(du.Sandboxes), cliutil.Layout().SandboxesDir()) //nolint:errcheck
	for _, b := range du.PerBackend {
		switch {
		case b.Err != nil:
			fmt.Fprintf(w, "%s\t-\t%v\n", b.Name, b.Err) //nolint:errcheck
		case b.Bytes < 0:
			fmt.Fprintf(w, "%s\t?\t%s\n", b.Name, b.Detail) //nolint:errcheck
		default:
			fmt.Fprintf(w, "%s\t%s\t%s\n", b.Name, cliutil.HumanBytes(b.Bytes), b.Detail) //nolint:errcheck
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Fprintln(out)                                                                          //nolint:errcheck
	fmt.Fprintln(out, "Reclaim with: yoloai system prune --cache (forces base image rebuild)") //nolint:errcheck
	return nil
}

// formatDiskJSON renders a DiskUsage into the existing JSON shape so
// the public CLI contract is unchanged.
func formatDiskJSON(du *yoloai.DiskUsage, sandboxesDir string) map[string]any {
	entries := []map[string]any{
		{"source": "sandboxes", "bytes": du.Sandboxes, "detail": sandboxesDir},
	}
	for _, b := range du.PerBackend {
		entry := map[string]any{"source": b.Name}
		if b.Err != nil {
			entry["error"] = b.Err.Error()
		} else {
			entry["bytes"] = b.Bytes
			entry["detail"] = b.Detail
		}
		entries = append(entries, entry)
	}
	return map[string]any{"entries": entries}
}

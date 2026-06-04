package system

// ABOUTME: `yoloai system disk` reports backend cache usage so users can spot
// when it's time to run `yoloai system prune` (or `--images`).

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
when it's time to prune. The CACHE column is reclaimable by 'yoloai system
prune' with no rebuild (build cache, volumes); the IMAGES column is reclaimable
only by 'yoloai system prune --images', which forces a base rebuild. Sizes
include all content the backend tracks — not just yoloai's — because the
backend doesn't tag content by who created it.`,
		Args: cobra.NoArgs,
		RunE: runSystemDisk,
	}
}

func runSystemDisk(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()

	du, err := cliutil.System().DiskUsage(ctx)
	if err != nil {
		return err
	}

	if cliutil.JSONEnabled(cmd) {
		return cliutil.WriteJSON(out, formatDiskJSON(du, cliutil.Layout().SandboxesDir()))
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SOURCE\tCACHE\tIMAGES\tDETAIL")                                                            //nolint:errcheck
	fmt.Fprintf(w, "sandboxes\t-\t%s\t%s\n", cliutil.HumanBytes(du.Sandboxes), cliutil.Layout().SandboxesDir()) //nolint:errcheck
	for _, b := range du.PerBackend {
		if b.Err != nil {
			fmt.Fprintf(w, "%s\t-\t-\t%v\n", b.Type, b.Err) //nolint:errcheck
			continue
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", b.Type, cliutil.HumanBytes(b.CachedBytes), imageBytesCell(b.ImageBytes), b.Detail) //nolint:errcheck
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Fprintln(out)                                                                      //nolint:errcheck
	fmt.Fprintln(out, "Reclaim cached data (no rebuild):    yoloai system prune")          //nolint:errcheck
	fmt.Fprintln(out, "Reclaim images (forces rebuild):     yoloai system prune --images") //nolint:errcheck
	return nil
}

// imageBytesCell renders the IMAGES column, showing "?" for the
// unknown sentinel (backends like containerd/tart that can't size images
// cheaply) rather than a misleading "0 B" or "-1 B".
func imageBytesCell(n int64) string {
	if n < 0 {
		return "?"
	}
	return cliutil.HumanBytes(n)
}

// formatDiskJSON renders a DiskUsage into the existing JSON shape so
// the public CLI contract is unchanged.
func formatDiskJSON(du *yoloai.DiskUsage, sandboxesDir string) map[string]any {
	entries := []map[string]any{
		{"source": "sandboxes", "bytes": du.Sandboxes, "detail": sandboxesDir},
	}
	for _, b := range du.PerBackend {
		entry := map[string]any{"source": b.Type}
		if b.Err != nil {
			entry["error"] = b.Err.Error()
		} else {
			entry["cached_bytes"] = b.CachedBytes
			entry["image_bytes"] = b.ImageBytes
			entry["detail"] = b.Detail
		}
		entries = append(entries, entry)
	}
	return map[string]any{"entries": entries}
}

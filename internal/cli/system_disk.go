package cli

// ABOUTME: `yoloai system disk` reports backend cache usage so users can spot
// when it's time to run `yoloai system prune --cache`.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/kstenerud/yoloai/runtime"
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

	if jsonEnabled(cmd) {
		return writeJSON(out, collectDiskJSON(ctx))
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SOURCE\tSIZE\tDETAIL") //nolint:errcheck

	sandboxesSize := dirSize(cliLayout().SandboxesDir())
	fmt.Fprintf(w, "sandboxes\t%s\t%s\n", humanBytes(sandboxesSize), cliLayout().SandboxesDir()) //nolint:errcheck

	for _, desc := range runtime.Descriptors() {
		available, _ := checkBackend(ctx, desc.Name)
		if !available {
			continue
		}
		usage, err := backendUsage(ctx, desc.Name)
		switch {
		case err != nil:
			fmt.Fprintf(w, "%s\t-\t%v\n", desc.Name, err) //nolint:errcheck
		case usage.BytesUsed < 0:
			fmt.Fprintf(w, "%s\t?\t%s\n", desc.Name, usage.Detail) //nolint:errcheck
		default:
			fmt.Fprintf(w, "%s\t%s\t%s\n", desc.Name, humanBytes(usage.BytesUsed), usage.Detail) //nolint:errcheck
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Fprintln(out)                                                                          //nolint:errcheck
	fmt.Fprintln(out, "Reclaim with: yoloai system prune --cache (forces base image rebuild)") //nolint:errcheck
	return nil
}

func backendUsage(ctx context.Context, backend string) (runtime.CacheUsage, error) {
	var usage runtime.CacheUsage
	err := withRuntime(ctx, backend, func(ctx context.Context, rt runtime.Runtime) error {
		var inner error
		usage, inner = runtime.CacheUsageFor(ctx, rt)
		return inner
	})
	return usage, err
}

func collectDiskJSON(ctx context.Context) map[string]any {
	sandboxesDir := cliLayout().SandboxesDir()
	entries := []map[string]any{
		{"source": "sandboxes", "bytes": dirSize(sandboxesDir), "detail": sandboxesDir},
	}
	for _, desc := range runtime.Descriptors() {
		available, _ := checkBackend(ctx, desc.Name)
		if !available {
			continue
		}
		entry := map[string]any{"source": desc.Name}
		usage, err := backendUsage(ctx, desc.Name)
		if err != nil {
			entry["error"] = err.Error()
		} else {
			entry["bytes"] = usage.BytesUsed
			entry["detail"] = usage.Detail
		}
		entries = append(entries, entry)
	}
	return map[string]any{"entries": entries}
}

// dirSize sums the size of every regular file under dir. Returns 0 on error
// (typical: dir doesn't exist yet on first run).
func dirSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // dirSize is best-effort; skip unreadable entries
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

// humanBytes formats a byte count with binary (1024-based) units.
// Mirrors the docker/podman convention used elsewhere in the CLI.
func humanBytes(n int64) string {
	const (
		kib = 1024
		mib = 1024 * kib
		gib = 1024 * mib
	)
	switch {
	case n >= gib:
		return fmt.Sprintf("%.2f GiB", float64(n)/float64(gib))
	case n >= mib:
		return fmt.Sprintf("%.1f MiB", float64(n)/float64(mib))
	case n >= kib:
		return fmt.Sprintf("%.1f KiB", float64(n)/float64(kib))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

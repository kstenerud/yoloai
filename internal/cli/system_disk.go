package cli

// ABOUTME: `yoloai system disk` reports backend cache usage so users can spot
// when it's time to run `yoloai system prune --cache`.

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
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

// lowDiskWarnThresholdBytes is the free-space level below which we
// print a courtesy warning before commands that allocate significant
// disk (new, clone, system build). 2 GiB is generous enough to cover
// a typical workdir copy plus overlay churn for one sandbox without
// being so conservative it fires on healthy systems.
//
// Below this, the operation may still succeed — the warning is
// advisory, not blocking. Users with workdirs > 2 GiB will see the
// warning even on plenty-of-space machines; that's acceptable
// because the operation is the same shape (could still fail).
const lowDiskWarnThresholdBytes int64 = 2 * 1024 * 1024 * 1024

// freeBytesAt returns bytes free on the filesystem backing path. If
// path doesn't exist yet (typical on first run, before ~/.yoloai/ is
// created), walks up to the nearest existing ancestor. Loop terminates
// at "/" since filepath.Dir("/") == "/" — checking that path == parent
// after Dir() catches the fixed point.
//
// Returns (-1, err) only if no ancestor up to and including "/"
// exists or Statfs fails.
func freeBytesAt(path string) (int64, error) {
	for {
		if _, err := os.Stat(path); err == nil {
			var stat syscall.Statfs_t
			if err := syscall.Statfs(path, &stat); err != nil {
				return -1, err
			}
			// Bavail is unprivileged-user-visible free blocks.
			// Bsize is the optimal transfer block size (== fs block size
			// for ext4/xfs/btrfs/zfs); use that, not Frsize, since they
			// match for the filesystems we care about and Bsize is the
			// portable choice.
			return int64(stat.Bavail) * int64(stat.Bsize), nil //nolint:gosec // G115: ext4/xfs filesystem sizes fit in int64
		}
		parent := filepath.Dir(path)
		if parent == path {
			// Fixed point: "/" doesn't stat AND has no ancestor.
			// Effectively impossible on a healthy Linux system.
			return -1, fmt.Errorf("no existing ancestor for path")
		}
		path = parent
	}
}

// warnIfLowDisk prints a one-line warning to stderr if free space on
// the filesystem backing path is below lowDiskWarnThresholdBytes.
// Stat errors are swallowed silently — this is a courtesy check, not
// a precondition, and shouldn't break commands when /proc/mounts is
// momentarily unreadable or similar.
//
// Call from any command that's about to allocate significant disk:
// sandbox creation (new, clone), image builds (system build).
func warnIfLowDisk(stderr io.Writer, path string) {
	free, err := freeBytesAt(path)
	if err != nil || free < 0 {
		return
	}
	emitLowDiskWarning(stderr, path, free, lowDiskWarnThresholdBytes)
}

// emitLowDiskWarning is the pure helper testable without filesystem
// access: given an already-determined free-bytes value, it writes
// the warning to stderr iff free is below threshold. Returns true
// if a warning was emitted (lets tests assert on side-effect).
func emitLowDiskWarning(stderr io.Writer, path string, free, threshold int64) bool {
	if free >= threshold {
		return false
	}
	fmt.Fprintf(stderr, //nolint:errcheck // best-effort stderr write
		"Warning: only %s free on %s — operation may run out of disk space.\n"+
			"  yoloai system disk             # see what's using space\n"+
			"  yoloai system prune --cache    # reclaim backend image cache\n",
		humanBytes(free), path,
	)
	return true
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

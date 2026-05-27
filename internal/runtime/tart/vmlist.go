// ABOUTME: Typed helpers around the `tart` CLI's list/delete operations for
// ABOUTME: callers that manage base VMs (e.g. yoloai system tart commands).

package tart

import (
	"context"
	"fmt"
	"strings"
)

// VMListEntry is a single row returned by `tart list` (default format).
// Size is in bytes; 0 means the size field was missing or unparseable.
type VMListEntry struct {
	Name string
	Size int64
}

// ListVMs returns every VM tart knows about (including base images) with
// the size reported by `tart list`. Equivalent to invoking `tart list` and
// parsing the Name + Size columns.
func (r *Runtime) ListVMs(ctx context.Context) ([]VMListEntry, error) {
	out, err := r.runTart(ctx, "list")
	if err != nil {
		return nil, err
	}
	return parseVMList(out), nil
}

// DeleteVM removes a VM from tart's inventory.
func (r *Runtime) DeleteVM(ctx context.Context, name string) error {
	_, err := r.runTart(ctx, "delete", name)
	return err
}

// parseVMList parses the default `tart list` output. Each line carries the
// VM name as the first field and a human-readable size (e.g. "20GB") as the
// second. Unparseable lines are skipped.
func parseVMList(output string) []VMListEntry {
	var entries []VMListEntry
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		entries = append(entries, VMListEntry{
			Name: fields[0],
			Size: parseSizeBytes(fields[1]),
		})
	}
	return entries
}

// parseSizeBytes parses size strings like "20GB" or "36.5GB" into bytes.
// Returns 0 on parse failure rather than erroring — the size column is
// informational, not required for correctness.
func parseSizeBytes(sizeStr string) int64 {
	trimmed := strings.TrimSuffix(strings.TrimSpace(sizeStr), "GB")
	var gb float64
	if _, err := fmt.Sscanf(trimmed, "%f", &gb); err != nil {
		return 0
	}
	return int64(gb * 1024 * 1024 * 1024)
}

// ABOUTME: Typed helpers around the `tart` CLI's list/delete operations for
// ABOUTME: callers that manage base VMs (e.g. yoloai system tart commands).

package tart

import "context"

// VMListEntry is a single VM/image tart tracks. Size is the on-disk footprint
// in bytes (coarse — tart reports whole GB); 0 when unavailable.
type VMListEntry struct {
	Name string
	Size int64
}

// ListVMs returns every VM tart knows about (including base images) with the
// size reported by `tart list`. It reuses the JSON list parser (listEntries)
// rather than scraping the human-readable columns, which carry a leading
// Source column and drift between tart versions — the old column scrape read
// "local"/"OCI" as the VM name, so name-prefix lookups (system tart list)
// always came up empty.
func (r *Runtime) ListVMs(ctx context.Context) ([]VMListEntry, error) {
	entries, err := r.listEntries(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]VMListEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, VMListEntry{Name: e.Name, Size: e.Size * bytesPerGB})
	}
	return out, nil
}

// DeleteVM removes a VM from tart's inventory.
func (r *Runtime) DeleteVM(ctx context.Context, name string) error {
	_, err := r.runTart(ctx, "delete", name)
	return err
}

// ABOUTME: SystemClient — admin sub-client off Client. Hosts cross-backend
// ABOUTME: operations (disk usage, prune, build, check) that are scoped to the
// ABOUTME: host rather than to a specific sandbox.
package yoloai

import (
	"context"
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/config"
	"github.com/kstenerud/yoloai/runtime"
)

// SystemClient scopes `yoloai system …` operations. Constructed via
// Client.System() (for embedders that already have a Client) or
// directly via NewSystemClient (for the CLI and embedders that
// only need admin ops). Never errors at construction.
//
// Decoupled from a specific backend on purpose: cross-backend
// methods (DiskUsage, Prune, Build with AllBackends) iterate every
// registered backend that's available in the current environment
// and spin up an ephemeral runtime per backend. Single-backend
// methods (Check, single-backend Build) take a BackendName parameter.
//
// Safe for concurrent use by multiple goroutines. Read-only methods
// (DiskUsage, Check) run in parallel. Write methods (Build, Prune)
// acquire backend-internal locks where applicable.
type SystemClient struct {
	layout config.Layout
}

// NewSystemClient constructs a SystemClient from a layout. Used by
// the CLI's system_* commands (which don't have a backend-specific
// Client) and by embedders that need only admin operations.
func NewSystemClient(layout config.Layout) *SystemClient {
	return &SystemClient{layout: layout}
}

// System returns the admin sub-client for system-level operations.
// Always non-nil; never errors. See SystemClient for the surface.
func (c *Client) System() *SystemClient {
	return &SystemClient{layout: c.layout}
}

// DiskUsage reports total on-disk usage by yoloai and each available
// backend. Walks the sandboxes directory for yoloai's own footprint
// and queries each backend's CacheUsage. Backends that fail to report
// surface their error in the per-backend entry rather than aborting
// the whole call.
type DiskUsage struct {
	// Sandboxes is the total byte count under DataDir/sandboxes/.
	Sandboxes int64
	// PerBackend has one entry per backend that was probed available.
	// Order matches runtime.Descriptors() (registration order).
	PerBackend []BackendDiskUsage
}

// BackendDiskUsage is one row of DiskUsage's per-backend section.
// When Err is non-nil, Bytes is 0 and Detail carries any partial
// progress info from the backend.
type BackendDiskUsage struct {
	Name   string
	Bytes  int64
	Detail string
	Err    error
}

// DiskUsage returns a per-backend disk-usage snapshot plus yoloai's
// own sandboxes-directory size. Unavailable backends are skipped.
func (s *SystemClient) DiskUsage(ctx context.Context) (*DiskUsage, error) {
	du := &DiskUsage{
		Sandboxes: dirSize(s.layout.SandboxesDir()),
	}
	for _, desc := range runtime.Descriptors() {
		rt, err := newRuntime(ctx, desc.Name, s.layout)
		if err != nil {
			// Backend not available in this environment — skip silently.
			// The CLI's `yoloai system disk` does the same filtering via
			// checkBackend before calling per-backend code.
			continue
		}
		usage, usageErr := runtime.CacheUsageFor(ctx, rt)
		_ = rt.Close()
		du.PerBackend = append(du.PerBackend, BackendDiskUsage{
			Name:   desc.Name,
			Bytes:  usage.BytesUsed,
			Detail: usage.Detail,
			Err:    usageErr,
		})
	}
	return du, nil
}

// dirSize sums every regular file under dir. Returns 0 on any
// error — best-effort, matches the CLI's existing semantics.
func dirSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // best-effort walk
		}
		if d.IsDir() {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

// ABOUTME: Teardown is the symmetric inverse of LaunchContainer: stop + remove
// ABOUTME: the container and force-delete the sandbox directory. Shared by create
// ABOUTME: (replace path) and the façade Destroy so neither owns the primitive.
package launch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/kstenerud/yoloai/internal/orchestrator/state"
	"github.com/kstenerud/yoloai/store"
)

// Teardown stops and removes the named sandbox's container and deletes its
// on-host directory. It is best-effort about the container (a stopped or
// already-removed instance is fine) and returns any advisory warnings (e.g. a
// directory that could not be fully removed because of root-owned backend
// state) as plain strings for the caller to render. A missing sandbox is not
// an error — there is simply nothing to tear down.
func Teardown(ctx context.Context, d state.Deps, name string) (warnings []string, err error) {
	slog.Info("destroying sandbox", "event", "sandbox.destroy", "sandbox", name)
	sandboxDir := d.Layout.SandboxDir(name)
	if rerr := store.RequireSandboxDir(sandboxDir); rerr != nil {
		if errors.Is(rerr, store.ErrSandboxNotFound) {
			return nil, nil // nothing to destroy
		}
		return nil, rerr
	}

	cname := store.InstanceName(d.Layout.Principal, name)

	// Stop instance (ignore errors — may not be running)
	_ = d.Runtime.Stop(ctx, cname)

	// Remove instance (ignore errors — may not exist)
	_ = d.Runtime.Remove(ctx, cname)

	// Remove the metadata file first so a partial directory removal still frees
	// the name for reuse: Create keys "already exists" off the metadata, not the
	// directory, so a leftover (e.g. root-owned overlay/VM state we can't delete)
	// won't block re-creating with the same name.
	_ = os.Remove(filepath.Join(sandboxDir, store.EnvironmentFile))

	// Remove sandbox directory. Some files (e.g. Go module cache) are
	// read-only, so make everything writable first.
	if rerr := forceRemoveAll(sandboxDir); rerr != nil {
		warnings = append(warnings, fmt.Sprintf("sandbox %s removed, but some files could not be deleted (likely root-owned overlay/VM state from the backend): %v\n  reclaim the leftover disk with: sudo rm -rf %s   (or run 'yoloai system prune')", name, rerr, sandboxDir))
	}

	return warnings, nil
}

// forceRemoveAll removes a directory tree, making read-only entries writable
// first and retrying briefly to absorb macOS system services (Spotlight,
// FSEvents) momentarily recreating files mid-removal.
func forceRemoveAll(path string) error {
	// First pass: ensure all directories are writable so their contents can
	// be removed. We only need to fix directories — os.RemoveAll handles
	// read-only files fine once the parent directory is writable.
	_ = filepath.WalkDir(path, func(p string, dirEntry os.DirEntry, err error) error {
		if err != nil {
			// If the directory isn't readable/executable, fix it and retry.
			_ = os.Chmod(p, 0o700) //nolint:gosec // best-effort; 0700 needed for directory traversal before removal
			return nil             //nolint:nilerr // returning nil continues the walk after a best-effort chmod
		}
		if dirEntry.IsDir() {
			_ = os.Chmod(p, 0o700) //nolint:gosec // best-effort; 0700 needed for directory traversal before removal
		}
		return nil
	})
	// Retry removal a few times. On macOS, system services (Spotlight,
	// FSEvents) can momentarily recreate files in the directory between
	// content removal and the final rmdir, causing "directory not empty".
	var err error
	for range 3 {
		err = os.RemoveAll(path)
		if err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return err
}

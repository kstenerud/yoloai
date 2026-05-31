// ABOUTME: Files is the host-side file-exchange handle for a sandbox's files/
// ABOUTME: directory — listing, importing, exporting, and removing entries.
package yoloai

import (
	"context"

	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
)

// Files is a name-scoped handle for a sandbox's file-exchange directory
// (~/.yoloai/sandboxes/<name>/files). It is obtained from the runtime-free
// SystemClient because file exchange is pure host filesystem work — it needs no
// container backend, so callers can shuttle files even when the sandbox isn't
// running.
type Files struct {
	sc   *SystemClient
	name string
}

// Files returns a file-exchange handle for the named sandbox, validating that
// the sandbox exists (ErrSandboxNotFound otherwise).
func (s *SystemClient) Files(name string) (*Files, error) {
	if err := store.RequireSandboxDir(s.layout.SandboxDir(name)); err != nil {
		return nil, err
	}
	return &Files{sc: s, name: name}, nil
}

// Path returns the host path of the exchange directory. The directory may not
// exist yet (Import creates it on demand).
func (f *Files) Path() string {
	return sandbox.FilesDir(f.sc.layout, f.name)
}

// List expands the glob patterns against the exchange directory and returns
// deduplicated, sorted relative paths. An empty match set is not an error.
func (f *Files) List(patterns []string) ([]string, error) {
	return sandbox.ListExchangeFiles(f.sc.layout, f.name, patterns)
}

// Import copies a host file or directory into the exchange directory (creating
// it if needed) and returns the base name placed. Without force, an existing
// entry of the same name is an error.
func (f *Files) Import(ctx context.Context, hostPath string, force bool) (string, error) {
	return sandbox.ImportFile(ctx, f.sc.layout, f.name, hostPath, force)
}

// Export copies one exchange entry (rel) to dst on the host. Without force, an
// existing dst is an error. rel is validated to stay within the exchange dir.
func (f *Files) Export(ctx context.Context, rel, dst string, force bool) error {
	return sandbox.ExportFile(ctx, f.sc.layout, f.name, rel, dst, force)
}

// Remove deletes one exchange entry (rel). rel is validated to stay within the
// exchange dir.
func (f *Files) Remove(rel string) error {
	return sandbox.RemoveExchangeFile(f.sc.layout, f.name, rel)
}

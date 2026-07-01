// ABOUTME: Files is the host-side file-exchange handle for a sandbox's files/
// ABOUTME: directory — listing, importing, exporting, and removing entries.
package yoloai

import (
	"context"

	"github.com/kstenerud/yoloai/internal/orchestrator"
)

// Files is a name-scoped handle for a sandbox's file-exchange directory
// (~/.yoloai/sandboxes/<name>/files). File exchange is pure host filesystem
// work — it needs no container backend, so callers can shuttle files even when
// the sandbox isn't running.
type Files struct {
	engine *orchestrator.Engine
	name   string
}

// Path returns the host path of the exchange directory. The directory may not
// exist yet (Import creates it on demand).
func (f *Files) Path() string {
	return f.engine.SandboxFiles(f.name)
}

// List expands the glob patterns against the exchange directory and returns
// deduplicated, sorted relative paths. An empty match set is not an error.
func (f *Files) List(patterns []string) ([]string, error) {
	return f.engine.ListFiles(f.name, patterns)
}

// Import copies a host file or directory into the exchange directory (creating
// it if needed) and returns the base name placed. Without force, an existing
// entry of the same name is an error.
func (f *Files) Import(ctx context.Context, hostPath string, force bool) (string, error) {
	return f.engine.ImportFile(ctx, f.name, hostPath, force)
}

// Export copies one exchange entry (rel) to dst on the host. Without force, an
// existing dst is an error. rel is validated to stay within the exchange dir.
func (f *Files) Export(ctx context.Context, rel, dst string, force bool) error {
	return f.engine.ExportFile(ctx, f.name, rel, dst, force)
}

// Remove deletes one exchange entry (rel). rel is validated to stay within the
// exchange dir.
func (f *Files) Remove(rel string) error {
	return f.engine.RemoveFile(f.name, rel)
}

// ReadFile returns the bytes of one exchange entry (rel, relative to the
// exchange dir). This is the content-oriented counterpart to Export (which
// copies to a host path): use it when the caller wants the file's bytes in
// memory rather than a host-side copy. rel is validated to stay within the
// exchange dir; paths that escape it (via "..", absolute components, etc.) are
// rejected.
func (f *Files) ReadFile(rel string) ([]byte, error) {
	return f.engine.ReadFile(f.name, rel)
}

// WriteFile writes data to one exchange entry (rel, relative to the exchange
// dir), creating the exchange directory and any parent dirs as needed. This is
// the content-oriented counterpart to Import (which copies an existing host
// file): use it when the caller already holds the content in memory. rel is
// validated to stay within the exchange dir (same containment as ReadFile).
func (f *Files) WriteFile(rel string, data []byte) error {
	return f.engine.WriteFile(f.name, rel, data)
}

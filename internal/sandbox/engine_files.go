// ABOUTME: Engine-level file-exchange verbs — list/import/export/remove on a
// ABOUTME: sandbox's files/ dir, so the Files sub-handle never threads layout.

package sandbox

import "context"

// ListFiles expands the glob patterns against the sandbox's exchange directory
// and returns deduplicated, sorted relative paths.
func (e *Engine) ListFiles(name string, patterns []string) ([]string, error) {
	return ListExchangeFiles(e.layout, name, patterns)
}

// ImportFile copies a host file or directory into the exchange directory and
// returns the base name placed.
func (e *Engine) ImportFile(ctx context.Context, name, hostPath string, force bool) (string, error) {
	return ImportFile(ctx, e.layout, name, hostPath, force)
}

// ExportFile copies one exchange entry (rel) to dst on the host.
func (e *Engine) ExportFile(ctx context.Context, name, rel, dst string, force bool) error {
	return ExportFile(ctx, e.layout, name, rel, dst, force)
}

// RemoveFile deletes one exchange entry (rel).
func (e *Engine) RemoveFile(name, rel string) error {
	return RemoveExchangeFile(e.layout, name, rel)
}

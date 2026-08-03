// ABOUTME: Engine-level file-exchange verbs — list/import/export/remove on a
// ABOUTME: sandbox's files/ dir, so the Files sub-handle never threads layout.

package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/store"
)

// ListFiles expands the glob patterns against the sandbox's exchange directory
// and returns deduplicated, sorted relative paths.
func (e *Engine) ListFiles(name string, patterns []string) ([]string, error) {
	return ListExchangeFiles(e.layout, name, patterns)
}

// ImportFile copies a host file or directory into the exchange directory and
// returns the base name placed. When the import replaced an existing entry, the
// guest's view of it is verified — and repaired if stale — before returning, so
// the command never reports success over content the sandbox cannot read.
func (e *Engine) ImportFile(ctx context.Context, name, hostPath string, force bool) (string, error) {
	res, err := ImportFile(ctx, e.layout, name, hostPath, force)
	if err != nil {
		return "", err
	}
	if res.Replaced {
		if err := e.refreshGuestView(ctx, name, res.Path); err != nil {
			return "", err
		}
	}
	return res.Name, nil
}

// refreshGuestView makes a running sandbox's view of the just-written tree match
// the host's, for the backends whose guest can serve stale data for a path it has
// already read (DF175 — tart's VirtioFS shares).
//
// It is a no-op for every other backend, and for a sandbox that is not running:
// a stopped guest holds no cache, and a guest that has never read the path is
// never served stale content. When it does run and cannot make the guest agree,
// that is an error rather than a warning — the alternative is a `files put` that
// exits 0 having delivered bytes the agent will never see.
func (e *Engine) refreshGuestView(ctx context.Context, name, hostPath string) error {
	// The backend comes from the sandbox's own environment.json, not from the
	// Engine: `files` is documented as pure host filesystem work and its Client
	// is built backend-less, so e.Runtime() is nil on exactly the path that
	// needs this. Resolving per-sandbox is also what a cross-backend operation
	// already does (depsForSandbox, DF138).
	meta, err := store.LoadEnvironment(e.layout.SandboxDir(name))
	if err != nil || meta.BackendType == "" {
		return nil // unreadable or legacy metadata: nothing to verify against
	}
	desc, known := runtime.Descriptor(meta.BackendType)
	if !known || !desc.Capabilities.HostWritesNeedGuestRefresh {
		return nil // the common path: every backend but tart delivers host writes live
	}

	rt := e.Runtime()
	if rt == nil || meta.BackendType != e.backend {
		opened, openErr := runtime.New(ctx, meta.BackendType, e.layout)
		if openErr != nil {
			return fmt.Errorf("connect to %s backend to verify delivery for %q: %w", meta.BackendType, name, openErr)
		}
		defer opened.Close() //nolint:errcheck // best-effort close of a per-call connection
		rt = opened
	}

	files, err := digestTree(hostPath)
	if err != nil {
		return fmt.Errorf("hash %s: %w", hostPath, err)
	}
	if len(files) == 0 {
		return nil
	}

	stale, _, err := runtime.RefreshGuestFilesFor(ctx, rt, store.InstanceName(e.layout.Principal, name), files)
	if errors.Is(err, runtime.ErrNotRunning) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(stale) > 0 {
		return fmt.Errorf(
			"%s was written to the exchange directory, but the sandbox still reads stale content for %s "+
				"and it could not be refreshed; the sandbox would act on content that never existed. "+
				"Restart the sandbox (yoloai stop %s && yoloai start %s) to clear it",
			filepath.Base(hostPath), staleList(stale), name, name)
	}
	return nil
}

// digestTree hashes every regular file at or under hostPath. Symlinks are
// recorded by neither name nor target: the guest resolves them itself, and the
// file they point at is hashed on its own if it is inside the tree.
func digestTree(hostPath string) ([]runtime.GuestFileDigest, error) {
	var files []runtime.GuestFileDigest
	err := filepath.WalkDir(hostPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		sum, err := sha256File(path)
		if err != nil {
			return err
		}
		files = append(files, runtime.GuestFileDigest{HostPath: path, SHA256: sum})
		return nil
	})
	return files, err
}

func sha256File(path string) (string, error) {
	fh, err := os.Open(path) //nolint:gosec // G304: path comes from walking a resolved exchange entry
	if err != nil {
		return "", err
	}
	defer fh.Close() //nolint:errcheck // read-only
	h := sha256.New()
	if _, err := io.Copy(h, fh); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// staleList renders the unrepairable paths for an error message, capped so a
// directory import that went wholly stale does not print hundreds of lines.
func staleList(stale []runtime.GuestFileDigest) string {
	const maxNamed = 3
	names := make([]string, 0, maxNamed)
	for _, f := range stale[:min(len(stale), maxNamed)] {
		names = append(names, filepath.Base(f.HostPath))
	}
	out := strings.Join(names, ", ")
	if extra := len(stale) - len(names); extra > 0 {
		out = fmt.Sprintf("%s and %d more", out, extra)
	}
	return out
}

// ExportFile copies one exchange entry (rel) to dst on the host.
func (e *Engine) ExportFile(ctx context.Context, name, rel, dst string, force bool) error {
	return ExportFile(ctx, e.layout, name, rel, dst, force)
}

// RemoveFile deletes one exchange entry (rel).
func (e *Engine) RemoveFile(name, rel string) error {
	return RemoveExchangeFile(e.layout, name, rel)
}

// ReadFile returns the bytes of one exchange entry (rel).
func (e *Engine) ReadFile(name, rel string) ([]byte, error) {
	return ReadExchangeFile(e.layout, name, rel)
}

// WriteFile writes data to one exchange entry (rel), creating parent dirs.
func (e *Engine) WriteFile(name, rel string, data []byte) error {
	return WriteExchangeFile(e.layout, name, rel, data)
}

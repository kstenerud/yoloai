// ABOUTME: Engine.Clone deep-copies an existing sandbox state directory to a
// ABOUTME: new name, preserving agent state and workdir while resetting identity.
package sandbox

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/internal/workspace"
)

// CloneOptions configures a sandbox clone operation. Overwriting a pre-existing
// destination is the orchestration layer's job (yoloai.Client.Clone tears the
// old one down first); Engine.Clone itself refuses an existing destination.
type CloneOptions struct {
	Source string // existing sandbox name
	Dest   string // new sandbox name
}

// Clone creates a new stopped sandbox by deep-copying an existing one's
// state directory. The clone gets a fresh name and creation timestamp;
// everything else (agent, model, profile, workdir, config, work copies,
// agent state, prompt) is preserved.
func (m *Engine) Clone(ctx context.Context, opts CloneOptions) error {
	_ = ctx // reserved for future use

	if err := store.ValidateName(opts.Dest); err != nil {
		return err
	}

	unlock, err := store.AcquireMultiLock(m.layout, opts.Source, opts.Dest)
	if err != nil {
		return err
	}
	defer unlock()

	srcDir := m.layout.SandboxDir(opts.Source)
	if err := store.RequireSandboxDir(srcDir); err != nil {
		return fmt.Errorf("source sandbox %q: %w", opts.Source, err)
	}

	dstDir := m.layout.SandboxDir(opts.Dest)
	if _, err := os.Stat(dstDir); err == nil {
		return fmt.Errorf("destination sandbox %q already exists", opts.Dest)
	}

	m.logger.Debug("cloning sandbox", "source", opts.Source, "dest", opts.Dest)

	if err := workspace.CopyDir(srcDir, dstDir); err != nil {
		return fmt.Errorf("copy sandbox directory: %w", err)
	}

	meta, err := store.LoadEnvironment(dstDir)
	if err != nil {
		os.RemoveAll(dstDir) //nolint:errcheck,gosec // best-effort cleanup
		return fmt.Errorf("load cloned meta: %w", err)
	}

	meta.Name = opts.Dest
	meta.CreatedAt = time.Now()

	if err := store.SaveEnvironment(dstDir, meta); err != nil {
		os.RemoveAll(dstDir) //nolint:errcheck,gosec // best-effort cleanup
		return fmt.Errorf("update cloned meta: %w", err)
	}

	m.logger.Info("cloned sandbox", "source", opts.Source, "dest", opts.Dest)
	return nil
}

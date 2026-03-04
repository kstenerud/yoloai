package sandbox

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/kstenerud/yoloai/workspace"
)

// CloneOptions configures a sandbox clone operation.
type CloneOptions struct {
	Source string // existing sandbox name
	Dest   string // new sandbox name
	Force  bool   // destroy destination if it already exists
}

// Clone creates a new stopped sandbox by deep-copying an existing one's
// state directory. The clone gets a fresh name and creation timestamp;
// everything else (agent, model, profile, workdir, config, work copies,
// agent state, prompt) is preserved.
func (m *Manager) Clone(ctx context.Context, opts CloneOptions) error {
	_ = ctx // reserved for future use

	if err := ValidateName(opts.Dest); err != nil {
		return err
	}

	srcDir, err := RequireSandboxDir(opts.Source)
	if err != nil {
		return fmt.Errorf("source sandbox %q: %w", opts.Source, err)
	}

	dstDir := Dir(opts.Dest)
	if _, err := os.Stat(dstDir); err == nil {
		return fmt.Errorf("destination sandbox %q already exists", opts.Dest)
	}

	m.logger.Debug("cloning sandbox", "source", opts.Source, "dest", opts.Dest)

	if err := workspace.CopyDir(srcDir, dstDir); err != nil {
		return fmt.Errorf("copy sandbox directory: %w", err)
	}

	meta, err := LoadMeta(dstDir)
	if err != nil {
		os.RemoveAll(dstDir) //nolint:errcheck,gosec // best-effort cleanup
		return fmt.Errorf("load cloned meta: %w", err)
	}

	meta.Name = opts.Dest
	meta.CreatedAt = time.Now()

	if err := SaveMeta(dstDir, meta); err != nil {
		os.RemoveAll(dstDir) //nolint:errcheck,gosec // best-effort cleanup
		return fmt.Errorf("update cloned meta: %w", err)
	}

	m.logger.Info("cloned sandbox", "source", opts.Source, "dest", opts.Dest)
	return nil
}

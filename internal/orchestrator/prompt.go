// ABOUTME: Host-side read of a sandbox's configured prompt text (prompt.txt),
// ABOUTME: distinguishing "no prompt configured" from an empty prompt.
package orchestrator

import (
	"fmt"
	"os"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/store"
)

// ReadStoredPrompt returns the prompt text persisted for a sandbox. The bool
// reports whether a prompt was configured at all: a missing prompt.txt yields
// ("", false, nil), while a present-but-empty file yields ("", true, nil). This
// is a host-filesystem read and does not require a running backend.
func ReadStoredPrompt(layout config.Layout, name string) (string, bool, error) {
	sandboxDir := layout.SandboxDir(name)
	if err := store.RequireSandboxDir(sandboxDir); err != nil {
		return "", false, err
	}
	data, err := os.ReadFile(store.PromptFilePath(sandboxDir)) //nolint:gosec // path is store-owned
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read prompt: %w", err)
	}
	return string(data), true, nil
}

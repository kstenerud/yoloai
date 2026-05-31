// ABOUTME: SystemClient.Prompt — backend-free read of a sandbox's configured
// ABOUTME: prompt text (prompt.txt), reporting whether a prompt was set at all.
package yoloai

import "github.com/kstenerud/yoloai/internal/sandbox"

// Prompt returns the prompt text persisted for a sandbox. The bool reports
// whether a prompt was configured: a sandbox with no prompt yields
// ("", false, nil); a present-but-empty prompt yields ("", true, nil). This is
// a host-filesystem read and does not require a running backend.
func (s *SystemClient) Prompt(name string) (string, bool, error) {
	return sandbox.ReadStoredPrompt(s.layout, name)
}

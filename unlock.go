// ABOUTME: SystemClient.Unlock — clear a stale per-sandbox lock file, refusing
// ABOUTME: when the recorded holder process is still alive.
package yoloai

import "github.com/kstenerud/yoloai/internal/sandbox/store"

// Unlock force-clears a stale lock file for a sandbox. It returns whether a
// lock was actually cleared (false means there was no lock file present) and
// surfaces a *UsageError when the recorded holder process is still alive. This
// is a host-filesystem operation and does not require a running backend.
func (s *SystemClient) Unlock(name string) (cleared bool, err error) {
	return store.ForceUnlock(s.layout, name)
}

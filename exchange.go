// ABOUTME: Runtime-free host paths for a sandbox's file-exchange and cache
// ABOUTME: directories — derivable and readable without the backend running.

package yoloai

import "github.com/kstenerud/yoloai/internal/sandbox/store"

// FilesDir returns the host path of the sandbox's file-exchange directory
// (<state>/files), where agent-produced files surface for the host to read.
// This is pure path computation: the sandbox need not exist or be running, and
// no backend is contacted.
func (s *SystemClient) FilesDir(name string) string {
	return store.FilesDir(s.layout.SandboxDir(name))
}

// CacheDir returns the host path of the sandbox's cache directory
// (<state>/cache). Like FilesDir, it is pure path computation with no backend
// contact and no existence check.
func (s *SystemClient) CacheDir(name string) string {
	return store.CacheDir(s.layout.SandboxDir(name))
}

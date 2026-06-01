// ABOUTME: Runtime-free host paths for a sandbox's file-exchange and cache
// ABOUTME: directories — derivable and readable without the backend running.

package yoloai

import (
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/sandbox/store"
)

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

// RuntimeConfigPath returns the host path of the sandbox's runtime-config.json
// (<state>/runtime-config.json), the entrypoint/infrastructure config the
// backend reads at launch. Pure path computation: no backend contact, no
// existence check.
func (s *SystemClient) RuntimeConfigPath(name string) string {
	return store.RuntimeConfigFilePath(s.layout.SandboxDir(name))
}

// EnvironmentPath returns the host path of the sandbox's environment.json
// (<state>/environment.json), the captured creation-time metadata. Pure path
// computation; the file need not exist.
func (s *SystemClient) EnvironmentPath(name string) string {
	return filepath.Join(s.layout.SandboxDir(name), store.EnvironmentFile)
}

// LogPaths holds the host paths of a sandbox's diagnostic JSONL streams and the
// agent-status snapshot — the files the CLI tails and the bug-report bundle
// collects. Pure path computation; the files need not exist.
type LogPaths struct {
	CLI         string // <state>/logs/cli.jsonl
	Sandbox     string // <state>/logs/sandbox.jsonl
	Monitor     string // <state>/logs/monitor.jsonl
	Hooks       string // <state>/logs/agent-hooks.jsonl
	AgentStatus string // <state>/agent-status.json
}

// LogPaths returns the diagnostic file paths for the named sandbox. No backend
// is contacted and no existence check is performed.
func (s *SystemClient) LogPaths(name string) LogPaths {
	dir := s.layout.SandboxDir(name)
	return LogPaths{
		CLI:         store.CLIJSONLPath(dir),
		Sandbox:     store.SandboxJSONLPath(dir),
		Monitor:     store.MonitorJSONLPath(dir),
		Hooks:       store.HooksJSONLPath(dir),
		AgentStatus: store.AgentStatusFilePath(dir),
	}
}

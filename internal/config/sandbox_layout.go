// ABOUTME: Sandbox-directory layout helpers shared by store/paths.go and the
// ABOUTME: runtime backends — the single source of per-sandbox subpath truth.
package config

import "path/filepath"

// RuntimeConfigFileName is the sandbox's Go<->guest runtime config file.
const RuntimeConfigFileName = "runtime-config.json"

// RuntimeConfigPath returns the path to runtime-config.json within a sandbox
// directory. Centralized here (not in store) so the runtime backends — which
// do not import store — resolve the same path as the orchestrator.
func RuntimeConfigPath(sandboxDir string) string {
	return filepath.Join(sandboxDir, RuntimeConfigFileName)
}

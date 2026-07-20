// ABOUTME: Sandbox-directory layout helpers shared by store/paths.go and the
// ABOUTME: runtime backends — the single source of per-sandbox subpath truth.
package config

import "path/filepath"

// Sandbox access tiers. A sandbox directory contains exactly these three
// subdirectories and nothing else at its root; every per-sandbox file lives
// inside one of them. The tier a file sits in *is* its guest-access class, so
// classification is structural — a new file cannot be added without choosing a
// tier, and the mount/share wiring exposes tiers, never individual files. See
// docs/contributors/design/plans/sandbox-share-tiering.md.
const (
	// HostTierName holds host-only state the guest must never reach
	// (environment.json, sandbox-state.json, agent.json, netpolicy.json,
	// backend/). Never shared into any sandbox.
	HostTierName = "host"
	// ReadOnlyTierName holds files the guest reads but must not write
	// (runtime-config.json, bin/ scripts, prompt.txt, machine-id, home-seed/,
	// secrets/). Shared read-only.
	ReadOnlyTierName = "ro"
	// ReadWriteTierName holds files the guest reads and writes (logs/,
	// agent-runtime/, agent-status.json, files/, cache/, home/, work/, tmux/,
	// setup.log, vscode-cli/). Shared read-write.
	ReadWriteTierName = "rw"
)

// HostTierDir returns the host-only tier directory within a sandbox directory.
func HostTierDir(sandboxDir string) string { return filepath.Join(sandboxDir, HostTierName) }

// ReadOnlyTierDir returns the guest-read-only tier directory within a sandbox.
func ReadOnlyTierDir(sandboxDir string) string { return filepath.Join(sandboxDir, ReadOnlyTierName) }

// ReadWriteTierDir returns the guest-read-write tier directory within a sandbox.
func ReadWriteTierDir(sandboxDir string) string {
	return filepath.Join(sandboxDir, ReadWriteTierName)
}

// RuntimeConfigFileName is the sandbox's Go<->guest runtime config file.
const RuntimeConfigFileName = "runtime-config.json"

// RuntimeConfigPath returns the path to runtime-config.json within a sandbox
// directory. Centralized here (not in store) so the runtime backends — which
// do not import store — resolve the same path as the orchestrator.
func RuntimeConfigPath(sandboxDir string) string {
	return filepath.Join(sandboxDir, RuntimeConfigFileName)
}

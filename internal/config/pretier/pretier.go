// ABOUTME: The frozen pre-tier sandbox layout (schema <= v5): flat literal paths
// ABOUTME: that every migrator below v6 reads and writes, so a tier move can't move them.
package pretier

import "path/filepath"

// This package is a photograph, not an abstraction. Every path below is spelled
// with string literals rather than by calling internal/config's builders, and
// that is the entire point: a migrator addresses the layout of the era it is
// migrating *from*, which by definition is not the layout the current build
// uses. Routing a migrator through the live builders is DF164 — it silently
// starts looking for a flat sandbox's files in the tiered locations, finds
// nothing, and reports "nothing to migrate".
//
// So: nothing here may be rewritten in terms of config.EnvironmentPath and
// friends, however tempting the de-duplication looks. If the live layout moves
// again, this file must NOT follow it.
//
// Correspondingly, tests that exercise a pre-v6 migrator seed their fixtures
// with their own literals, not with these helpers — a fixture built by the same
// builder the code reads with cannot fail when the layout moves, which is how
// DF164 stayed green through the break.

// SandboxRecord names the flat, pre-tier per-sandbox record files. Schema v5 and
// below wrote all four at the sandbox-dir root; v6 sorts them into host/.
const (
	EnvironmentFileName  = "environment.json"
	SandboxStateFileName = "sandbox-state.json"
	AgentConfigFileName  = "agent.json"
	NetpolicyFileName    = "netpolicy.json"
	RuntimeConfigName    = "runtime-config.json"
	WorkDirName          = "work"
)

// EnvironmentPath returns the pre-tier path to a sandbox's environment.json.
func EnvironmentPath(sandboxDir string) string {
	return filepath.Join(sandboxDir, EnvironmentFileName)
}

// SandboxStatePath returns the pre-tier path to a sandbox's sandbox-state.json.
func SandboxStatePath(sandboxDir string) string {
	return filepath.Join(sandboxDir, SandboxStateFileName)
}

// AgentConfigPath returns the pre-tier path to a sandbox's agent.json.
func AgentConfigPath(sandboxDir string) string {
	return filepath.Join(sandboxDir, AgentConfigFileName)
}

// NetpolicyPath returns the pre-tier path to a sandbox's netpolicy.json.
func NetpolicyPath(sandboxDir string) string {
	return filepath.Join(sandboxDir, NetpolicyFileName)
}

// RuntimeConfigPath returns the pre-tier path to a sandbox's
// runtime-config.json.
func RuntimeConfigPath(sandboxDir string) string {
	return filepath.Join(sandboxDir, RuntimeConfigName)
}

// WorkDir returns the pre-tier path to a sandbox's work/ directory — the one
// tier member a pre-v6 migrator walks (the overlay flatten enumerates it).
func WorkDir(sandboxDir string) string {
	return filepath.Join(sandboxDir, WorkDirName)
}

// WorkDirFor returns the pre-tier work directory of a single mount, given its
// caret-encoded path segment (store.EncodePath). The encoding is passed in
// rather than computed here for two reasons: it keeps this package on the
// standard library alone, and an encoding is not a layout — it did not move
// with the tiers and has no pre-tier form to freeze.
func WorkDirFor(sandboxDir, encodedPath string) string {
	return filepath.Join(WorkDir(sandboxDir), encodedPath)
}

// OverlayLowerName is the subdirectory, under one mount's work directory, where
// a pre-v4 :overlay sandbox bind-mounted the user's original workdir read-only.
const OverlayLowerName = "lower"

// OverlayLowerFor returns that pristine lower, which the v3->v4 flatten migrator
// rebuilds an abandoned overlay sandbox from. Doubly frozen: the tier move put
// work/ somewhere else, and :overlay itself was retired at v4 (D109), so no live
// layout has anywhere to point.
func OverlayLowerFor(sandboxDir, encodedPath string) string {
	return filepath.Join(WorkDirFor(sandboxDir, encodedPath), OverlayLowerName)
}

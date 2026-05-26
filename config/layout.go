// ABOUTME: Layout — a DataDir-rooted struct exposing every yoloai data path
// ABOUTME: as a method. Replaces package-level $HOME-derived helpers (§12).

package config

import "path/filepath"

// Layout names every yoloai data path rooted at a given DataDir.
// Threading a Layout through library functions — instead of relying on
// the package-level helpers (YoloaiDir, SandboxesDir, ...) which read
// $HOME implicitly via HomeDir() — is Q-W's no-ambient-configuration
// discipline (development-principles.md §12) applied to paths.
//
// Embedders construct a Layout once via NewLayout and pass it down;
// library code uses Layout methods, not the package-level helpers.
// The legacy package-level helpers continue to work during the W-L8b
// migration but read HomeDir() and so violate §12 — they will be
// removed (or restricted) once threading is complete in Q-W.4/.5.
type Layout struct {
	// DataDir is the root yoloai data directory; all per-Layout paths
	// derive from this. The CLI sets this to $HOME/.yoloai/ at startup
	// (its single licensed os.UserHomeDir() call); HTTP servers,
	// daemons, multi-tenant processes, and tests pass an explicit
	// path.
	//
	// Empty DataDir is not rejected at Layout construction — the
	// resulting paths will be unrooted (relative to "") — but
	// downstream callers (yoloai.NewWithOptions) reject empty
	// DataDir with *UsageError per Q-W.
	DataDir string
}

// NewLayout constructs a Layout rooted at dataDir.
func NewLayout(dataDir string) Layout {
	return Layout{DataDir: dataDir}
}

// YoloaiDir returns the root data directory (an alias for DataDir,
// kept for parity with the package-level helper's name during the
// W-L8b migration).
func (l Layout) YoloaiDir() string { return l.DataDir }

// SandboxesDir returns DataDir/sandboxes/.
func (l Layout) SandboxesDir() string {
	return filepath.Join(l.DataDir, "sandboxes")
}

// ProfilesDir returns DataDir/profiles/.
func (l Layout) ProfilesDir() string {
	return filepath.Join(l.DataDir, "profiles")
}

// CacheDir returns DataDir/cache/.
func (l Layout) CacheDir() string {
	return filepath.Join(l.DataDir, "cache")
}

// ExtensionsDir returns DataDir/extensions/.
func (l Layout) ExtensionsDir() string {
	return filepath.Join(l.DataDir, "extensions")
}

// DefaultsDir returns DataDir/defaults/.
func (l Layout) DefaultsDir() string {
	return filepath.Join(l.DataDir, "defaults")
}

// DefaultsConfigPath returns DataDir/defaults/config.yaml.
func (l Layout) DefaultsConfigPath() string {
	return filepath.Join(l.DefaultsDir(), "config.yaml")
}

// TartBaseMetadataDir returns the directory for Tart runtime base
// metadata under this layout.
func (l Layout) TartBaseMetadataDir() string {
	return filepath.Join(l.DataDir, "tart-base-metadata")
}

// TartBaseLocksDir returns the directory for Tart runtime base locks
// under this layout.
func (l Layout) TartBaseLocksDir() string {
	return filepath.Join(l.DataDir, "tart-base-locks")
}

// DockerBaseLocksDir returns the directory for Docker base-image
// build locks under this layout.
func (l Layout) DockerBaseLocksDir() string {
	return filepath.Join(l.DataDir, "docker-base-locks")
}

// VscodeCLIDir returns DataDir/vscode-cli/, the global VS Code CLI
// token seed store. It is NOT mounted directly into containers;
// each sandbox gets its own per-sandbox vscode-cli directory
// (seeded from this location on first use) to prevent VS Code CLI's
// singleton lock from blocking concurrent tunnels.
func (l Layout) VscodeCLIDir() string {
	return filepath.Join(l.DataDir, "vscode-cli")
}

// SandboxDir returns the per-sandbox state directory:
// DataDir/sandboxes/<name>/. Equivalent to store.Dir(name) under the
// legacy package-level helpers; the migration target for the 42+
// store.Dir call sites (Q-W.4b).
func (l Layout) SandboxDir(name string) string {
	return filepath.Join(l.SandboxesDir(), name)
}

// SandboxLockPath returns the per-sandbox advisory lockfile path:
// DataDir/sandboxes/<name>.lock. The lockfile lives next to the
// sandbox dir (not inside it) so it works before the sandbox
// directory is created — e.g. during "yoloai new".
func (l Layout) SandboxLockPath(name string) string {
	return filepath.Join(l.SandboxesDir(), name+".lock")
}

// TartBaseLockPath returns the lockfile path for serializing Tart
// base VM builds: DataDir/tart-base-locks/<baseName>.lock.
func (l Layout) TartBaseLockPath(baseName string) string {
	return filepath.Join(l.TartBaseLocksDir(), baseName+".lock")
}

// DockerBaseLockPath returns the lockfile path for serializing
// Docker base image builds: DataDir/docker-base-locks/<baseName>.lock.
func (l Layout) DockerBaseLockPath(baseName string) string {
	return filepath.Join(l.DockerBaseLocksDir(), baseName+".lock")
}

// GlobalConfigPath returns DataDir/config.yaml — the user-level
// yoloai configuration file. Migration target for the package-level
// GlobalConfigPath() helper.
func (l Layout) GlobalConfigPath() string {
	return filepath.Join(l.DataDir, "config.yaml")
}

// StatePath returns DataDir/state.yaml — the operational state file
// (setup_complete, etc.). Migration target for the package-level
// StatePath() helper.
func (l Layout) StatePath() string {
	return filepath.Join(l.DataDir, "state.yaml")
}

// ProfileDir returns DataDir/profiles/<name>/. Migration target for
// the package-level ProfileDirPath(name) helper.
func (l Layout) ProfileDir(name string) string {
	return filepath.Join(l.ProfilesDir(), name)
}

// CniDir returns DataDir/cni/ — the containerd backend's per-data-dir
// CNI configuration directory.
func (l Layout) CniDir() string {
	return filepath.Join(l.DataDir, "cni")
}

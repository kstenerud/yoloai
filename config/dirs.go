package config

// ABOUTME: Centralized directory helpers and shared subdirectory name constants.
// ABOUTME: All code that constructs ~/.yoloai/ paths should use these helpers.

import "path/filepath"

// Top-level directory helpers. These use HomeDir() which panics on failure,
// so they return string directly (no error).

// YoloaiDir returns the path to ~/.yoloai/.
func YoloaiDir() string {
	return filepath.Join(HomeDir(), ".yoloai")
}

// SandboxesDir returns the path to ~/.yoloai/sandboxes/.
func SandboxesDir() string {
	return filepath.Join(YoloaiDir(), "sandboxes")
}

// ProfilesDir returns the path to ~/.yoloai/profiles/.
func ProfilesDir() string {
	return filepath.Join(YoloaiDir(), "profiles")
}

// CacheDir returns the path to ~/.yoloai/cache/.
func CacheDir() string {
	return filepath.Join(YoloaiDir(), "cache")
}

// ExtensionsDir returns the path to ~/.yoloai/extensions/.
func ExtensionsDir() string {
	return filepath.Join(YoloaiDir(), "extensions")
}

// DefaultsDir returns the path to ~/.yoloai/defaults/.
func DefaultsDir() string {
	return filepath.Join(YoloaiDir(), "defaults")
}

// DefaultsConfigPath returns the path to ~/.yoloai/defaults/config.yaml.
func DefaultsConfigPath() string {
	return filepath.Join(DefaultsDir(), "config.yaml")
}

// TartBaseMetadataDir returns the directory for Tart runtime base metadata.
func TartBaseMetadataDir() string {
	return filepath.Join(YoloaiDir(), "tart-base-metadata")
}

// TartBaseLocksDir returns the directory for Tart runtime base locks.
func TartBaseLocksDir() string {
	return filepath.Join(YoloaiDir(), "tart-base-locks")
}

// VscodeCLIDir returns ~/.yoloai/vscode-cli/, the global VS Code CLI token seed
// store. It is NOT mounted directly into containers; instead, each sandbox gets
// its own per-sandbox vscode-cli directory (seeded from this location on first
// use) to prevent VS Code CLI's singleton lock from blocking concurrent tunnels.
func VscodeCLIDir() string {
	return filepath.Join(YoloaiDir(), "vscode-cli")
}

// Shared sandbox subdirectory name constants. Used by sandbox/paths.go and
// runtime backends to avoid duplicating these literal strings.
const (
	BackendDirName      = "backend"
	BinDirName          = "bin"
	TmuxDirName         = "tmux"
	AgentRuntimeDirName = "agent-runtime"
)

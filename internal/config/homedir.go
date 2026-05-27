// ABOUTME: Package config — homedir.go is now empty; HomeDir() was removed in
// ABOUTME: Q-W.6. The single licensed os.UserHomeDir() call lives in
// ABOUTME: internal/cli/layout_bridge.go (homeBasedDataDir). No package-level
// ABOUTME: function reads ambient HOME; callers receive an explicit config.Layout.
package config

package config

// ABOUTME: Migration helpers for upgrading from old yoloai directory layouts.

import (
	"os"
)

// CheckDefaultsDir verifies that ~/.yoloai/defaults/ exists. If it doesn't,
// returns a descriptive error telling the user how to resolve it.
// Only called when setup_complete is true (i.e., this is an upgrade, not a fresh install).
func CheckDefaultsDir() error {
	if _, err := os.Stat(DefaultsDir()); err == nil {
		return nil // exists, nothing to do
	}
	msg := "~/.yoloai/defaults/ not found\n\n" +
		"This directory was added in a recent update. To fix:\n\n" +
		"  Option 1 — Re-run setup (creates a fresh defaults/config.yaml):\n" +
		"    yoloai system setup\n\n" +
		"  Option 2 — Copy your existing settings manually:\n" +
		"    mkdir -p ~/.yoloai/defaults\n" +
		"    cp ~/.yoloai/profiles/base/config.yaml ~/.yoloai/defaults/config.yaml\n" +
		"    # If you have a custom tmux.conf:\n" +
		"    cp ~/.yoloai/profiles/base/tmux.conf ~/.yoloai/defaults/tmux.conf\n" +
		"  Then remove any 'profile:' line from config.yaml (that key no longer exists).\n\n" +
		"  Note: after migration, 'base' will appear as a regular profile in 'yoloai profile list'.\n" +
		"  You may want to remove it: yoloai profile delete base\n"
	return NewConfigError("%s", msg)
}

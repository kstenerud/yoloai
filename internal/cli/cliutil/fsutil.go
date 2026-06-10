// ABOUTME: Filesystem helpers for CLI commands: tilde/env path expansion of flag
// ABOUTME: values and recursive directory-size measurement for display.
package cliutil

import (
	"io/fs"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/config"
)

// ExpandPath expands a leading ~ and ${VAR} references in a CLI flag path.
// homeDir and env are supplied explicitly (the CLI captures them once into
// Layout; the library never reads $HOME — see development-principles.md §12).
func ExpandPath(path, homeDir string, env config.EnvLookup) (string, error) {
	return config.ExpandPath(path, homeDir, env)
}

// DirSize recursively sums the size of all files under path. Used by info and
// bug-report rendering (always paired with FormatSize); the domain measures
// per-sandbox disk usage separately via the status leaf.
func DirSize(path string) (int64, error) {
	var size int64
	err := filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			size += info.Size()
		}
		return nil
	})
	return size, err
}

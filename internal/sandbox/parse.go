package sandbox

import (
	"fmt"
	"path/filepath"
	"strings"
)

// DirArg holds the parsed components of a directory argument.
type DirArg struct {
	Path  string // resolved absolute path
	Mode  string // "copy", "rw", or "" (caller applies default)
	Force bool   // :force was specified
}

// knownSuffixes are the recognized directory argument suffixes.
var knownSuffixes = map[string]bool{
	"copy":  true,
	"rw":    true,
	"force": true,
}

// ParseDirArg parses a directory argument with optional suffixes.
// Suffixes (:copy, :rw, :force) can be combined in any order.
// Default mode (no :copy or :rw) is determined by the caller
// (workdir defaults to "copy", aux dirs default to "ro").
func ParseDirArg(arg string) (*DirArg, error) {
	result := &DirArg{}

	// Parse suffixes from the right.
	remaining := arg
	for {
		idx := strings.LastIndex(remaining, ":")
		if idx < 0 {
			break
		}

		suffix := remaining[idx+1:]
		if !knownSuffixes[suffix] {
			break
		}

		switch suffix {
		case "copy":
			if result.Mode == "rw" {
				return nil, fmt.Errorf("cannot combine :copy and :rw on %q", arg)
			}
			result.Mode = "copy"
		case "rw":
			if result.Mode == "copy" {
				return nil, fmt.Errorf("cannot combine :copy and :rw on %q", arg)
			}
			result.Mode = "rw"
		case "force":
			result.Force = true
		}

		remaining = remaining[:idx]
	}

	absPath, err := filepath.Abs(remaining)
	if err != nil {
		return nil, fmt.Errorf("resolve path %q: %w", remaining, err)
	}
	result.Path = absPath

	return result, nil
}

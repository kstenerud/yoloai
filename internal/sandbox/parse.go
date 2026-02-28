package sandbox

import (
	"fmt"
	"path/filepath"
	"strings"
)

// DirArg holds the parsed components of a directory argument.
type DirArg struct {
	Path      string // resolved absolute host path
	MountPath string // container mount path ("" = mirror host path)
	Mode      string // "copy", "rw", or "" (caller applies default)
	Force     bool   // :force was specified
}

// ResolvedMountPath returns the container mount path. If MountPath is
// set, it is returned; otherwise Path (mirroring the host path).
func (d *DirArg) ResolvedMountPath() string {
	if d.MountPath != "" {
		return d.MountPath
	}
	return d.Path
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

	// Strip =<mount-path> first (before suffix parsing), since suffixes
	// like :rw appear between the host path and the = sign.
	// Format: <host-path>[:suffix...]=[<mount-path>]
	remaining := arg
	var mountPart string
	if idx := strings.LastIndex(remaining, "="); idx > 0 {
		mountPart = remaining[idx+1:]
		remaining = remaining[:idx]
	}

	// Parse suffixes from the right.
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

	remaining, err := ExpandPath(remaining)
	if err != nil {
		return nil, fmt.Errorf("expand path %q: %w", arg, err)
	}
	absPath, err := filepath.Abs(remaining)
	if err != nil {
		return nil, fmt.Errorf("resolve path %q: %w", remaining, err)
	}
	result.Path = absPath

	if mountPart != "" {
		mountPart, err = ExpandPath(mountPart)
		if err != nil {
			return nil, fmt.Errorf("expand mount path %q: %w", arg, err)
		}
		absMountPath, err := filepath.Abs(mountPart)
		if err != nil {
			return nil, fmt.Errorf("resolve mount path %q: %w", mountPart, err)
		}
		result.MountPath = absMountPath
	}

	return result, nil
}

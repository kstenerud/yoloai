// ABOUTME: ParseDirArg parses "path[:suffix...]=[mount]" directory arguments,
// ABOUTME: producing DirSpec values consumed by CreateOptions workdir/aux fields.
package sandbox

import (
	"fmt"
	"path/filepath"
	"strings"
)

// knownSuffixes are the recognized directory argument suffixes.
var knownSuffixes = map[string]bool{
	"copy":    true,
	"overlay": true,
	"rw":      true,
	"force":   true,
}

// applyDirSuffix applies a single recognized suffix token to result.
// Returns an error if the suffix conflicts with an already-set mode.
func applyDirSuffix(result *DirSpec, suffix, arg string) error {
	switch suffix {
	case "copy":
		if result.Mode == DirModeRW || result.Mode == DirModeOverlay {
			return fmt.Errorf("cannot combine :copy and :%s on %q", result.Mode, arg)
		}
		result.Mode = DirModeCopy
	case "overlay":
		if result.Mode == DirModeCopy || result.Mode == DirModeRW {
			return fmt.Errorf("cannot combine :overlay and :%s on %q", result.Mode, arg)
		}
		result.Mode = DirModeOverlay
	case "rw":
		if result.Mode == DirModeCopy || result.Mode == DirModeOverlay {
			return fmt.Errorf("cannot combine :rw and :%s on %q", result.Mode, arg)
		}
		result.Mode = DirModeRW
	case "force":
		result.AllowDangerousPath = true
	}
	return nil
}

// ParseAuxDirArg parses an auxiliary (`-d`) directory argument and
// rejects modes the diff/apply workflow doesn't support on aux dirs.
//
// Q-U (resolved 2026-05-25): the diff/apply workflow operates on the
// workdir only — aux dirs are reference mounts (`:rw` for live edits,
// default `:ro` for read-only). Aux `:copy` and `:overlay` are no
// longer supported. Returns *UsageError pointing at the workarounds:
// make the dir the workdir, mount as `:rw`, or run a separate sandbox.
// env is the environment map for ${VAR} expansion; use layout.Env.
func ParseAuxDirArg(arg, homeDir string, env map[string]string) (*DirSpec, error) {
	d, err := ParseDirArg(arg, homeDir, env)
	if err != nil {
		return nil, err
	}
	switch d.Mode {
	case DirModeCopy:
		return nil, NewUsageError(
			"aux directories cannot use :copy (diff/apply is workdir-only).\n"+
				"  - to track changes, make %q the workdir instead\n"+
				"  - to edit it live, use :rw\n"+
				"  - for an isolated copy, run a separate sandbox", arg)
	case DirModeOverlay:
		return nil, NewUsageError(
			"aux directories cannot use :overlay (diff/apply is workdir-only).\n"+
				"  - to track changes, make %q the workdir instead\n"+
				"  - to edit it live, use :rw\n"+
				"  - for an isolated copy, run a separate sandbox", arg)
	case DirModeRW, DirModeRO, "":
		// rw / ro / unset all permitted on aux dirs; caller applies the
		// "" → ro default downstream.
	}
	return d, nil
}

// ParseDirArg parses a directory argument with optional suffixes.
// Suffixes (:copy, :rw, :force) can be combined in any order.
// Default mode (no :copy or :rw) is determined by the caller
// (workdir defaults to "copy", aux dirs default to "ro").
// homeDir is used for ~ expansion; callers derive it from layout.HomeDir.
// env is the environment map for ${VAR} expansion; use layout.Env.
//
// Use ParseAuxDirArg for the `-d` flag — it adds the workdir-only
// validation enforced by Q-U.
func ParseDirArg(arg, homeDir string, env map[string]string) (*DirSpec, error) {
	result := &DirSpec{}

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

		if err := applyDirSuffix(result, suffix, arg); err != nil {
			return nil, err
		}

		remaining = remaining[:idx]
	}

	remaining, err := ExpandPath(remaining, homeDir, env)
	if err != nil {
		return nil, fmt.Errorf("expand path %q: %w", arg, err)
	}
	absPath, err := filepath.Abs(remaining)
	if err != nil {
		return nil, fmt.Errorf("resolve path %q: %w", remaining, err)
	}
	result.Path = absPath

	if mountPart != "" {
		mountPart, err = ExpandPath(mountPart, homeDir, env)
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

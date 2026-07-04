// ABOUTME: ParseDirArg parses "path[:suffix...]=[mount]" directory arguments from
// ABOUTME: CLI flag strings, producing yoloai.DirSpec values for SandboxCreateOptions.
package cliutil

import (
	"fmt"
	"path/filepath"
	"strings"

	yoloai "github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/internal/config"
)

// knownSuffixes are the recognized directory argument suffixes. :overlay was
// retired (D109); an existing overlay sandbox is auto-converted to :copy by
// `yoloai system migrate`, and the suffix is no longer creatable.
var knownSuffixes = map[string]bool{
	"copy":        true,
	"copy-all":    true,
	"copy-strict": true,
	"rw":          true,
	"force":       true,
}

// applyDirSuffix applies a single recognized suffix token to result.
// Returns an error if the suffix conflicts with an already-set mode.
func applyDirSuffix(result *yoloai.DirSpec, suffix, arg string) error {
	switch suffix {
	case "copy":
		if result.Mode == yoloai.DirModeRW {
			return fmt.Errorf("cannot combine :copy and :%s on %q", result.Mode, arg)
		}
		result.Mode = yoloai.DirModeCopy
	case "copy-all":
		// Like :copy but copies gitignored files too (opt-out of the default
		// .gitignore-honoring). Same diff/apply workflow — it's :copy with one knob.
		if result.Mode == yoloai.DirModeRW {
			return fmt.Errorf("cannot combine :copy-all and :%s on %q", result.Mode, arg)
		}
		if result.StripHistory {
			return fmt.Errorf("cannot combine :copy-all and :copy-strict on %q", arg)
		}
		result.Mode = yoloai.DirModeCopy
		result.IncludeIgnored = true
	case "copy-strict":
		// Like :copy but strips the source .git (fresh baseline, no history)
		// instead of preserving it — for repos with secrets in history that
		// haven't been rotated. Same diff/apply workflow — it's :copy with one knob.
		if result.Mode == yoloai.DirModeRW {
			return fmt.Errorf("cannot combine :copy-strict and :%s on %q", result.Mode, arg)
		}
		if result.IncludeIgnored {
			return fmt.Errorf("cannot combine :copy-strict and :copy-all on %q", arg)
		}
		result.Mode = yoloai.DirModeCopy
		result.StripHistory = true
	case "rw":
		if result.Mode == yoloai.DirModeCopy {
			return fmt.Errorf("cannot combine :rw and :%s on %q", result.Mode, arg)
		}
		result.Mode = yoloai.DirModeRW
	case "force":
		result.AllowDangerousPath = true
	}
	return nil
}

// ParseAuxDirArg parses an auxiliary (`-d`) directory argument.
//
// All modes are permitted on aux dirs: `:copy` enables the diff/apply workflow
// for multiple directories (D81, multi-workdir Phase 2), `:rw` provides live-edit
// access, and the default `:ro` is read-only. (`:overlay` was retired — D109.)
// env is the curated interpolation map for ${VAR} expansion; pass
// layout.Env().EnvForConfigInterpolation().
func ParseAuxDirArg(arg, homeDir string, env map[string]string) (*yoloai.DirSpec, error) {
	d, err := ParseDirArg(arg, homeDir, env)
	if err != nil {
		return nil, err
	}
	// All modes accepted; caller applies the "" → ro default downstream.
	return d, nil
}

// ParseDirArg parses a directory argument with optional suffixes.
// Suffixes (:copy, :rw, :force) can be combined in any order.
// Default mode (no :copy or :rw) is determined by the caller
// (workdir defaults to "copy", aux dirs default to "ro").
// homeDir is used for ~ expansion; callers derive it from layout.HomeDir.
// env is the curated interpolation map for ${VAR} expansion; pass
// layout.Env().EnvForConfigInterpolation().
//
// Use ParseAuxDirArg for the `-d` flag — it adds the workdir-only
// validation enforced by Q-U.
func ParseDirArg(arg, homeDir string, env map[string]string) (*yoloai.DirSpec, error) {
	result := &yoloai.DirSpec{}

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

	remaining, err := config.ExpandPath(remaining, homeDir, env)
	if err != nil {
		return nil, fmt.Errorf("expand path %q: %w", arg, err)
	}
	absPath, err := filepath.Abs(remaining)
	if err != nil {
		return nil, fmt.Errorf("resolve path %q: %w", remaining, err)
	}
	result.Path = absPath

	if mountPart != "" {
		mountPart, err = config.ExpandPath(mountPart, homeDir, env)
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

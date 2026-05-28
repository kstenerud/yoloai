package config

// ABOUTME: Path expansion utilities: tilde expansion and ${VAR} env var expansion.

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ExpandPath composes tilde expansion with braced env var expansion.
// Tilde is expanded first, then ${VAR} references are resolved.
// Bare $VAR is treated as literal text. Unset variables and unclosed
// ${ produce an error.
//
// homeDir is the user's home directory for ~ expansion; callers derive it
// from layout.HomeDir (the conventional $HOME/.yoloai DataDir)
// or pass an explicit home for testing.
// env is the environment map used for ${VAR} expansion; use layout.Env.
// A nil env means any ${VAR} reference is an unset-variable error.
func ExpandPath(path, homeDir string, env map[string]string) (string, error) {
	path = ExpandTilde(path, homeDir)
	return expandEnvBraced(path, env)
}

// expandEnvBraced expands ${VAR} references in s using the provided env map.
// Bare $VAR (without braces) is left as-is. Returns an error for
// unset variables or unclosed ${.
// A nil env map means any ${VAR} reference returns an "not set" error.
func expandEnvBraced(s string, env map[string]string) (string, error) {
	var b strings.Builder
	b.Grow(len(s))

	i := 0
	for i < len(s) {
		// Look for "${".
		idx := strings.Index(s[i:], "${")
		if idx < 0 {
			b.WriteString(s[i:])
			break
		}
		b.WriteString(s[i : i+idx])
		i += idx + 2 // skip past "${"

		// Find closing "}".
		end := strings.IndexByte(s[i:], '}')
		if end < 0 {
			return "", fmt.Errorf("unclosed ${ in %q", s)
		}
		varName := s[i : i+end]
		i += end + 1 // skip past "}"

		val, ok := env[varName]
		if !ok {
			return "", fmt.Errorf("environment variable %q is not set", varName)
		}
		b.WriteString(val)
	}

	return b.String(), nil
}

// ExpandTilde replaces a leading ~ with the user's home directory.
// homeDir is the caller-supplied home directory; callers derive it from
// layout.HomeDir (the conventional $HOME/.yoloai DataDir)
// or pass an explicit home for testing.
func ExpandTilde(path, homeDir string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	return filepath.Join(homeDir, path[1:])
}

package config

// ABOUTME: Path expansion utilities: tilde expansion and ${VAR} env var expansion.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ExpandPath composes tilde expansion with braced env var expansion.
// Tilde is expanded first, then ${VAR} references are resolved.
// Bare $VAR is treated as literal text. Unset variables and unclosed
// ${ produce an error.
func ExpandPath(path string) (string, error) {
	path = ExpandTilde(path)
	return expandEnvBraced(path)
}

// expandEnvBraced expands ${VAR} references in s using os.LookupEnv.
// Bare $VAR (without braces) is left as-is. Returns an error for
// unset variables or unclosed ${.
func expandEnvBraced(s string) (string, error) {
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

		val, ok := os.LookupEnv(varName)
		if !ok {
			return "", fmt.Errorf("environment variable %q is not set", varName)
		}
		b.WriteString(val)
	}

	return b.String(), nil
}

// ExpandTilde replaces a leading ~ with the user's home directory.
func ExpandTilde(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
}

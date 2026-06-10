// ABOUTME: ExpandPath/ExpandTilde wrappers delegating to config package for
// ABOUTME: tilde and environment variable expansion in sandbox path resolution.
package sandbox

import (
	"github.com/kstenerud/yoloai/internal/config"
)

// ExpandPath composes tilde expansion with braced env var expansion.
// homeDir is used for ~ expansion; derive from layout.HomeDir.
// env is the EnvLookup for ${VAR} expansion; pass a Layout or MapEnv.
func ExpandPath(p, homeDir string, env config.EnvLookup) (string, error) {
	return config.ExpandPath(p, homeDir, env)
}

// ExpandTilde replaces a leading ~ with the user's home directory.
// homeDir is used for ~ expansion; derive from layout.HomeDir.
func ExpandTilde(p, homeDir string) string { return config.ExpandTilde(p, homeDir) }

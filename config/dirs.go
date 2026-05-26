package config

// ABOUTME: Shared sandbox subdirectory name constants used by runtime backends
// ABOUTME: and sandbox/paths.go to avoid duplicating literal strings.

// Shared sandbox subdirectory name constants. Used by sandbox/paths.go and
// runtime backends to avoid duplicating these literal strings.
const (
	BackendDirName      = "backend"
	BinDirName          = "bin"
	TmuxDirName         = "tmux"
	AgentRuntimeDirName = "agent-runtime"
)

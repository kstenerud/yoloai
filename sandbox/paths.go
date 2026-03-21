// Package sandbox implements sandbox lifecycle operations.
package sandbox

import (
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/config"
)

// Centralized sandbox file and directory names. All code that reads/writes
// these files should reference these constants rather than using literal strings.
const (
	// EnvironmentFile stores sandbox metadata captured at creation time (was "meta.json").
	EnvironmentFile = "environment.json"

	// SandboxStateFile stores per-sandbox persistent flags (was "state.json").
	SandboxStateFile = "sandbox-state.json"

	// RuntimeConfigFile stores entrypoint/infrastructure config (was "config.json").
	RuntimeConfigFile = "runtime-config.json"

	// AgentStatusFile stores live agent liveness status (was "status.json").
	AgentStatusFile = "agent-status.json"

	// AgentRuntimeDir stores agent-managed state (was "agent-state").
	AgentRuntimeDir = config.AgentRuntimeDirName

	// BinDir holds executable scripts (entrypoint, monitor, diagnose).
	BinDir = config.BinDirName

	// TmuxDir holds tmux configuration and sockets.
	TmuxDir = config.TmuxDirName

	// BackendDir holds backend-specific files (seatbelt profile, pid, logs).
	BackendDir = config.BackendDirName

	// LogsDir holds per-sandbox structured log files.
	LogsDir = "logs"

	// CLIJSONLFile is the relative path to the CLI structured log within the sandbox dir.
	CLIJSONLFile = "logs/cli.jsonl"

	// SandboxJSONLFile is the relative path to the container entrypoint structured log.
	SandboxJSONLFile = "logs/sandbox.jsonl"

	// MonitorJSONLFile is the relative path to the status monitor structured log.
	MonitorJSONLFile = "logs/monitor.jsonl"

	// HooksJSONLFile is the relative path to the agent hooks structured log.
	HooksJSONLFile = "logs/agent-hooks.jsonl"

	// AgentLogFile is the relative path to the raw agent terminal output log.
	AgentLogFile = "logs/agent.log"
)

// EncodePath encodes a host path using the caret encoding spec for use as a
// filesystem-safe directory name. Delegates to config.EncodePath.
func EncodePath(hostPath string) string {
	return config.EncodePath(hostPath)
}

// DecodePath reverses caret encoding back to the original path.
// Delegates to config.DecodePath.
func DecodePath(encoded string) (string, error) {
	return config.DecodePath(encoded)
}

// ValidateName checks that a sandbox name is safe for use in filesystem paths
// and Docker container names.
func ValidateName(name string) error {
	if name == "" {
		return NewUsageError("sandbox name is required")
	}
	if len(name) > config.MaxNameLength {
		return NewUsageError("invalid sandbox name: must be at most %d characters (got %d)", config.MaxNameLength, len(name))
	}
	if name[0] == '/' || name[0] == '\\' {
		return NewUsageError("invalid sandbox name %q: looks like a path (did you swap the arguments?)", name)
	}
	if !config.ValidNameRe.MatchString(name) {
		return NewUsageError("invalid sandbox name %q: must start with a letter or digit and contain only letters, digits, underscores, dots, or hyphens", name)
	}
	return nil
}

// InstanceName returns the runtime instance name for a sandbox.
func InstanceName(name string) string {
	return "yoloai-" + name
}

// Dir returns the host-side state directory for a sandbox.
//
//	~/.yoloai/sandboxes/<name>/
func Dir(name string) string {
	return filepath.Join(config.SandboxesDir(), name)
}

// Per-sandbox subdirectory helpers.

// BackendPath returns the backend-specific directory for a sandbox.
func BackendPath(name string) string {
	return filepath.Join(Dir(name), BackendDir)
}

// BinPath returns the executable scripts directory for a sandbox.
func BinPath(name string) string {
	return filepath.Join(Dir(name), BinDir)
}

// TmuxPath returns the tmux configuration directory for a sandbox.
func TmuxPath(name string) string {
	return filepath.Join(Dir(name), TmuxDir)
}

// AgentRuntimePath returns the agent-managed state directory for a sandbox.
func AgentRuntimePath(name string) string {
	return filepath.Join(Dir(name), AgentRuntimeDir)
}

// HomeSeedPath returns the home-seed directory for a sandbox.
func HomeSeedPath(name string) string {
	return filepath.Join(Dir(name), "home-seed")
}

// Per-sandbox file helpers.

// RuntimeConfigFilePath returns the path to runtime-config.json for a sandbox.
func RuntimeConfigFilePath(name string) string {
	return filepath.Join(Dir(name), RuntimeConfigFile)
}

// AgentStatusFilePath returns the path to agent-status.json for a sandbox.
func AgentStatusFilePath(name string) string {
	return filepath.Join(Dir(name), AgentStatusFile)
}

// LogsPath returns the logs/ directory for a sandbox.
func LogsPath(name string) string {
	return filepath.Join(Dir(name), LogsDir)
}

// CLIJSONLPath returns the path to logs/cli.jsonl for a sandbox.
func CLIJSONLPath(name string) string {
	return filepath.Join(Dir(name), CLIJSONLFile)
}

// SandboxJSONLPath returns the path to logs/sandbox.jsonl for a sandbox.
func SandboxJSONLPath(name string) string {
	return filepath.Join(Dir(name), SandboxJSONLFile)
}

// MonitorJSONLPath returns the path to logs/monitor.jsonl for a sandbox.
func MonitorJSONLPath(name string) string {
	return filepath.Join(Dir(name), MonitorJSONLFile)
}

// HooksJSONLPath returns the path to logs/agent-hooks.jsonl for a sandbox.
func HooksJSONLPath(name string) string {
	return filepath.Join(Dir(name), HooksJSONLFile)
}

// AgentLogPath returns the path to logs/agent.log for a sandbox.
func AgentLogPath(name string) string {
	return filepath.Join(Dir(name), AgentLogFile)
}

// PromptFilePath returns the path to prompt.txt for a sandbox.
func PromptFilePath(name string) string {
	return filepath.Join(Dir(name), "prompt.txt")
}

// RequireSandboxDir returns the sandbox directory path after verifying it exists.
func RequireSandboxDir(name string) (string, error) {
	dir := Dir(name)
	if _, err := os.Stat(dir); err != nil {
		return "", ErrSandboxNotFound
	}
	return dir, nil
}

// WorkDir returns the host-side work directory for a specific
// copy-mode mount within a sandbox.
//
//	~/.yoloai/sandboxes/<name>/work/<caret-encoded-path>/
func WorkDir(name string, hostPath string) string {
	return filepath.Join(Dir(name), "work", EncodePath(hostPath))
}

// OverlayUpperDir returns the upper layer directory for an overlay mount.
//
//	~/.yoloai/sandboxes/<name>/work/<caret-encoded-path>/upper/
func OverlayUpperDir(name string, hostPath string) string {
	return filepath.Join(Dir(name), "work", EncodePath(hostPath), "upper")
}

// OverlayOvlworkDir returns the overlayfs workdir for an overlay mount.
// Named "ovlwork" to avoid collision with the sandbox work/ directory.
//
//	~/.yoloai/sandboxes/<name>/work/<caret-encoded-path>/ovlwork/
func OverlayOvlworkDir(name string, hostPath string) string {
	return filepath.Join(Dir(name), "work", EncodePath(hostPath), "ovlwork")
}

// FilesDir returns the host-side file exchange directory for a sandbox.
//
//	~/.yoloai/sandboxes/<name>/files/
func FilesDir(name string) string {
	return filepath.Join(Dir(name), "files")
}

// CacheDir returns the host-side cache directory for a sandbox.
//
//	~/.yoloai/sandboxes/<name>/cache/
func CacheDir(name string) string {
	return filepath.Join(Dir(name), "cache")
}

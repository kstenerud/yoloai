// ABOUTME: Per-sandbox subdirectory path helpers and on-disk file name constants.
// ABOUTME: All path-construction takes a sandboxDir (from config.Layout.SandboxDir).
// Package store manages on-disk sandbox state: directory paths, the
// per-sandbox Meta record, and the SandboxState completion flags. All
// other sandbox/ subpackages consume types from here; this package
// imports only the standard library, config, and internal helpers.
//
// **Layout discipline (Q-W.4b).** None of the helpers in this file
// reach back to config.SandboxesDir() or config.YoloaiDir(); they
// derive subpaths from a sandboxDir argument supplied by the caller.
// The caller obtains the sandboxDir from a config.Layout
// (layout.SandboxDir(name)). This satisfies the "all layout info in
// one authoritative source" rule from §12: Layout is the only thing
// that knows where the sandbox root lives; store is the only thing
// that knows the per-sandbox subdirectory structure.
package store

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/config"
	"github.com/kstenerud/yoloai/internal/yoerrors"
)

// ErrSandboxNotFound is returned when a sandbox directory does not exist.
var ErrSandboxNotFound = errors.New("sandbox not found")

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

	// MachineIDFile stores a stable machine-id for the sandbox. Bind-mounted at
	// /etc/machine-id to prevent VS Code CLI from seeing a new machine on every
	// container restart (which would invalidate stored tunnel auth tokens).
	MachineIDFile = "machine-id"

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
		return yoerrors.NewUsageError("sandbox name is required")
	}
	if len(name) > config.MaxNameLength {
		return yoerrors.NewUsageError("invalid sandbox name: must be at most %d characters (got %d)", config.MaxNameLength, len(name))
	}
	if name[0] == '/' || name[0] == '\\' {
		return yoerrors.NewUsageError("invalid sandbox name %q: looks like a path (did you swap the arguments?)", name)
	}
	if !config.ValidNameRe.MatchString(name) {
		return yoerrors.NewUsageError("invalid sandbox name %q: must start with a letter or digit and contain only letters, digits, underscores, dots, or hyphens", name)
	}
	return nil
}

// InstanceName returns the runtime instance name for a sandbox.
func InstanceName(name string) string {
	return "yoloai-" + name
}

// Per-sandbox subdirectory helpers. Each takes a sandboxDir (typically
// obtained via layout.SandboxDir(name)) and returns the subpath.

// BackendPath returns the backend-specific directory within a sandbox.
func BackendPath(sandboxDir string) string {
	return filepath.Join(sandboxDir, BackendDir)
}

// BinPath returns the executable scripts directory within a sandbox.
func BinPath(sandboxDir string) string {
	return filepath.Join(sandboxDir, BinDir)
}

// TmuxPath returns the tmux configuration directory within a sandbox.
func TmuxPath(sandboxDir string) string {
	return filepath.Join(sandboxDir, TmuxDir)
}

// AgentRuntimePath returns the agent-managed state directory within a sandbox.
func AgentRuntimePath(sandboxDir string) string {
	return filepath.Join(sandboxDir, AgentRuntimeDir)
}

// HomeSeedPath returns the home-seed directory within a sandbox.
func HomeSeedPath(sandboxDir string) string {
	return filepath.Join(sandboxDir, "home-seed")
}

// Per-sandbox file helpers.

// RuntimeConfigFilePath returns the path to runtime-config.json within a sandbox.
func RuntimeConfigFilePath(sandboxDir string) string {
	return filepath.Join(sandboxDir, RuntimeConfigFile)
}

// AgentStatusFilePath returns the path to agent-status.json within a sandbox.
func AgentStatusFilePath(sandboxDir string) string {
	return filepath.Join(sandboxDir, AgentStatusFile)
}

// LogsPath returns the logs/ directory within a sandbox.
func LogsPath(sandboxDir string) string {
	return filepath.Join(sandboxDir, LogsDir)
}

// CLIJSONLPath returns the path to logs/cli.jsonl within a sandbox.
func CLIJSONLPath(sandboxDir string) string {
	return filepath.Join(sandboxDir, CLIJSONLFile)
}

// SandboxJSONLPath returns the path to logs/sandbox.jsonl within a sandbox.
func SandboxJSONLPath(sandboxDir string) string {
	return filepath.Join(sandboxDir, SandboxJSONLFile)
}

// MonitorJSONLPath returns the path to logs/monitor.jsonl within a sandbox.
func MonitorJSONLPath(sandboxDir string) string {
	return filepath.Join(sandboxDir, MonitorJSONLFile)
}

// HooksJSONLPath returns the path to logs/agent-hooks.jsonl within a sandbox.
func HooksJSONLPath(sandboxDir string) string {
	return filepath.Join(sandboxDir, HooksJSONLFile)
}

// AgentLogPath returns the path to logs/agent.log within a sandbox.
func AgentLogPath(sandboxDir string) string {
	return filepath.Join(sandboxDir, AgentLogFile)
}

// PromptFilePath returns the path to prompt.txt within a sandbox.
func PromptFilePath(sandboxDir string) string {
	return filepath.Join(sandboxDir, "prompt.txt")
}

// MachineIDPath returns the path to the stable machine-id file within a sandbox.
func MachineIDPath(sandboxDir string) string {
	return filepath.Join(sandboxDir, MachineIDFile)
}

// RequireSandboxDir verifies that the given sandbox directory exists on
// disk. Returns ErrSandboxNotFound when the directory is missing.
// Other stat errors propagate (returned as-is).
func RequireSandboxDir(sandboxDir string) error {
	if _, err := os.Stat(sandboxDir); err != nil {
		if os.IsNotExist(err) {
			return ErrSandboxNotFound
		}
		return err
	}
	return nil
}

// WorkDir returns the host-side work directory for a specific
// copy-mode mount within a sandbox.
//
//	<sandboxDir>/work/<caret-encoded-path>/
func WorkDir(sandboxDir string, hostPath string) string {
	return filepath.Join(sandboxDir, "work", EncodePath(hostPath))
}

// OverlayWorkBaseDir returns the parent directory for all overlay layer
// directories (upper, ovlwork, merged, lower). This entire directory is
// bind-mounted as a single Docker volume so that upper and ovlwork share
// the same underlying mount — a requirement for overlayfs to work inside
// a Docker container.
//
//	<sandboxDir>/work/<caret-encoded-path>/
func OverlayWorkBaseDir(sandboxDir string, hostPath string) string {
	return filepath.Join(sandboxDir, "work", EncodePath(hostPath))
}

// OverlayUpperDir returns the upper layer directory for an overlay mount.
//
//	<sandboxDir>/work/<caret-encoded-path>/upper/
func OverlayUpperDir(sandboxDir string, hostPath string) string {
	return filepath.Join(sandboxDir, "work", EncodePath(hostPath), "upper")
}

// OverlayOvlworkDir returns the overlayfs workdir for an overlay mount.
// Named "ovlwork" to avoid collision with the sandbox work/ directory.
//
//	<sandboxDir>/work/<caret-encoded-path>/ovlwork/
func OverlayOvlworkDir(sandboxDir string, hostPath string) string {
	return filepath.Join(sandboxDir, "work", EncodePath(hostPath), "ovlwork")
}

// OverlayLowerDir returns the mount-point directory inside OverlayWorkBaseDir
// where the user's read-only workdir is bind-mounted (nested inside the
// parent volume so all overlay dirs share the same Docker bind mount).
//
//	<sandboxDir>/work/<caret-encoded-path>/lower/
func OverlayLowerDir(sandboxDir string, hostPath string) string {
	return filepath.Join(sandboxDir, "work", EncodePath(hostPath), "lower")
}

// OverlayMergedDir returns the directory inside OverlayWorkBaseDir that
// serves as the overlayfs merge target (the unified view of lower+upper).
//
//	<sandboxDir>/work/<caret-encoded-path>/merged/
func OverlayMergedDir(sandboxDir string, hostPath string) string {
	return filepath.Join(sandboxDir, "work", EncodePath(hostPath), "merged")
}

// FilesDir returns the host-side file exchange directory within a sandbox.
//
//	<sandboxDir>/files/
func FilesDir(sandboxDir string) string {
	return filepath.Join(sandboxDir, "files")
}

// CacheDir returns the host-side cache directory within a sandbox.
//
//	<sandboxDir>/cache/
func CacheDir(sandboxDir string) string {
	return filepath.Join(sandboxDir, "cache")
}

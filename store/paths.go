// ABOUTME: Per-sandbox subdirectory path helpers and on-disk file name constants.
// ABOUTME: All path-construction takes a sandboxDir (from config.Layout.SandboxDir).
// Package store manages on-disk sandbox state: directory paths, the
// per-sandbox Environment record, and the SandboxState completion flags. All
// other sandbox/ subpackages consume types from here; this package
// imports only the standard library, config, and internal helpers.
//
// **Layout discipline (Q-W).** None of the helpers in this file read
// ambient $HOME; they derive subpaths from a sandboxDir argument
// supplied by the caller, which obtains it from a config.Layout
// (layout.SandboxDir(name)). This satisfies the "all layout info in
// one authoritative source" rule from §12: Layout is the only thing
// that knows where the sandbox root lives; store is the only thing
// that knows the per-sandbox subdirectory structure.
package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
)

// ErrSandboxNotFound is returned when a sandbox directory does not exist.
var ErrSandboxNotFound = errors.New("sandbox not found")

// Centralized sandbox file and directory names. All code that reads/writes
// these files should reference these constants rather than using literal strings.
const (
	// EnvironmentFile stores sandbox metadata captured at creation time.
	EnvironmentFile = "environment.json"

	// SandboxStateFile stores per-sandbox persistent flags.
	SandboxStateFile = "sandbox-state.json"

	// RuntimeConfigFile stores entrypoint/infrastructure config.
	RuntimeConfigFile = "runtime-config.json"

	// AgentStatusFile stores live agent liveness status.
	AgentStatusFile = "agent-status.json"

	// AgentRuntimeDir stores agent-managed state.
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

	// SecretsConsumedMarker is a host-visible marker the in-sandbox
	// entrypoint writes after it has read /run/secrets into the agent's
	// environment. The host waits for this marker before removing the
	// ephemeral secrets temp dir, so a slow-booting backend (Kata VM via
	// containerd) can't have the dir yanked out from under it before the
	// guest reads it.
	//
	// It lives UNDER logs/ deliberately: the container gets individual
	// bind mounts for /yoloai subdirs (logs, files, cache, ...) but NOT
	// for the /yoloai root, so a file written at the root is invisible to
	// the host. logs/ is bind-mounted and propagates guest→host promptly
	// (same path the entrypoint's sandbox.jsonl uses). The Python writers
	// (entrypoint.py, sandbox-setup.py) hard-code the same relative path;
	// keep them in sync.
	SecretsConsumedMarker = "logs/.secrets-consumed" //nolint:gosec // G101: a marker filename, not a credential

	// SubstrateReadyMarker is a host-visible marker the in-sandbox entrypoint
	// writes once root provisioning (UID remap, network isolation, overlay
	// mounts, setup commands) is complete and immediately before it execs the
	// neutral keep-alive holder — i.e. the box is ready to accept a launched
	// session-runner. The host waits for this before ProcessLauncher.Launch:
	// a runner started DURING root setup is silently killed (the readiness race
	// found in the S3 carve smoke, DF44). Only the keepalive_only bring-up
	// writes it. Lives under logs/ for the same bind-mount reason as
	// SecretsConsumedMarker; entrypoint.py hard-codes the same relative path.
	SubstrateReadyMarker = "logs/.substrate-ready"
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
// and runtime instance names. It delegates to config.ParseSandboxName so the
// containerd identifier grammar is enforced in exactly one place (DF16/DF15).
func ValidateName(name string) error {
	_, err := config.ParseSandboxName(name)
	return err
}

// InstanceName returns the runtime instance name (container id) for a sandbox
// owned by the given principal. The default (empty) principal elides, yielding
// the historical "yoloai-<name>"; a non-empty principal namespaces the id as
// "yoloai-<principal>-<name>" so two principals' same-named sandboxes never
// collide on the runtime backend. Delegates to config.InstancePrefix so the
// prefix logic lives in exactly one place (DF19). See D62.
func InstanceName(principal config.PrincipalSegment, name string) string {
	return config.InstancePrefix(principal) + name
}

// Per-sandbox subdirectory helpers. Each takes a sandboxDir (typically
// obtained via layout.SandboxDir(name)) and returns the subpath.

// BackendPath returns the backend-specific directory within a sandbox.
func BackendPath(sandboxDir string) string {
	return filepath.Join(sandboxDir, BackendDir)
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

// QuarantineSandbox moves a sandbox directory into the trash dir so it
// can be recovered later with a plain `mv`. Used by prune for sandboxes
// it cannot safely classify (unreadable/corrupt metadata) but where no
// recoverable work was detected — quarantining instead of deleting keeps
// repair reversible. Returns the destination path under the trash dir.
//
// When a trash entry with the same name already exists, a nanosecond
// timestamp suffix is appended so a repeated quarantine never clobbers an
// earlier one.
func QuarantineSandbox(layout config.Layout, name string) (string, error) {
	src := layout.SandboxDir(name)
	if err := fileutil.MkdirAll(layout.TrashDir(), 0o700); err != nil {
		return "", fmt.Errorf("create trash dir: %w", err)
	}
	dest := filepath.Join(layout.TrashDir(), name)
	if _, err := os.Stat(dest); err == nil {
		dest = fmt.Sprintf("%s.%d", dest, time.Now().UnixNano())
	}
	if err := os.Rename(src, dest); err != nil {
		return "", fmt.Errorf("quarantine sandbox %q to trash: %w", name, err)
	}
	return dest, nil
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

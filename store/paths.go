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
	EnvironmentFile = config.EnvironmentFileName

	// SandboxStateFile stores per-sandbox persistent flags.
	SandboxStateFile = config.SandboxStateFileName

	// RuntimeConfigFile stores entrypoint/infrastructure config.
	RuntimeConfigFile = config.RuntimeConfigFileName

	// AgentStatusFile stores live agent liveness status.
	AgentStatusFile = config.AgentStatusFileName

	// AgentRuntimeDir stores agent-managed state.
	AgentRuntimeDir = config.AgentRuntimeDirName

	// BinDir holds executable scripts (entrypoint, monitor, diagnose).
	BinDir = config.BinDirName

	// TmuxDir holds tmux configuration and sockets.
	TmuxDir = config.TmuxDirName

	// BackendDir holds backend-specific files (seatbelt profile, pid, logs).
	BackendDir = config.BackendDirName

	// LogsDir holds per-sandbox structured log files.
	LogsDir = config.LogsDirName

	// MachineIDFile stores a stable machine-id for the sandbox. Bind-mounted at
	// /etc/machine-id to prevent VS Code CLI from seeing a new machine on every
	// container restart (which would invalidate stored tunnel auth tokens).
	MachineIDFile = config.MachineIDFileName

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

// LegacyCLIInstanceName returns the pre-D126 instance name for a CLI sandbox —
// "yoloai-<name>", the empty-principal form the CLI produced before it adopted
// the "cli" principal. It exists ONLY for migrations that operate on instances
// created under the old naming: the v3->v4 overlay flatten and the v4->v5
// principal rename, both of which must address a still-"yoloai-<name>" instance.
//
// This is the one legitimate use of the bare "yoloai-" prefix that D126/DF98
// otherwise make unwritable: it names historical data in a frozen, one-way
// migration, never a live path derived from the current principal. New code must
// use InstanceName(principal, name) — which cannot emit a bare "yoloai-".
func LegacyCLIInstanceName(name string) string {
	return "yoloai-" + name
}

// Per-sandbox subdirectory helpers. Each takes a sandboxDir (typically
// obtained via layout.SandboxDir(name)) and returns the subpath.
//
// These delegate to internal/config, which owns the layout: the runtime
// backends and internal/broker need the same subpaths but cannot import store,
// so a builder that lived only here would be re-derived by hand over there.
// Add new per-sandbox paths in config/sandbox_layout.go and re-export here —
// never as a filepath.Join at a call site, which is what the tiering is
// removing (each path's tier must have exactly one place to change).

// HostTierPath returns the host-only tier directory within a sandbox. Nothing
// under it is ever shared into a guest on any backend.
func HostTierPath(sandboxDir string) string {
	return config.HostTierDir(sandboxDir)
}

// ReadOnlyTierPath returns the guest-readable tier directory within a sandbox.
func ReadOnlyTierPath(sandboxDir string) string {
	return config.ReadOnlyTierDir(sandboxDir)
}

// ReadWriteTierPath returns the guest-writable tier directory within a sandbox.
func ReadWriteTierPath(sandboxDir string) string {
	return config.ReadWriteTierDir(sandboxDir)
}

// BackendPath returns the backend-specific directory within a sandbox.
func BackendPath(sandboxDir string) string {
	return config.BackendPath(sandboxDir)
}

// HomeSeedPath returns the home-seed directory within a sandbox.
func HomeSeedPath(sandboxDir string) string {
	return config.HomeSeedPath(sandboxDir)
}

// BinPath returns the guest-exec'd script directory within a sandbox.
func BinPath(sandboxDir string) string {
	return config.BinPath(sandboxDir)
}

// TmuxPath returns the tmux config/socket directory within a sandbox.
func TmuxPath(sandboxDir string) string {
	return config.TmuxPath(sandboxDir)
}

// AgentRuntimePath returns the agent-managed state directory within a sandbox.
func AgentRuntimePath(sandboxDir string) string {
	return config.AgentRuntimePath(sandboxDir)
}

// AgentRuntimeFilePath returns the path to a relative entry inside a sandbox's
// agent-runtime directory.
func AgentRuntimeFilePath(sandboxDir, relPath string) string {
	return config.AgentRuntimeFilePath(sandboxDir, relPath)
}

// VSCodeCLIPath returns the VS Code CLI state directory within a sandbox.
func VSCodeCLIPath(sandboxDir string) string {
	return config.VSCodeCLIPath(sandboxDir)
}

// WorkBasePath returns the base directory holding a sandbox's work copies.
func WorkBasePath(sandboxDir string) string {
	return config.WorkBasePath(sandboxDir)
}

// Per-sandbox file helpers.

// EnvironmentFilePath returns the path to environment.json within a sandbox.
func EnvironmentFilePath(sandboxDir string) string {
	return config.EnvironmentPath(sandboxDir)
}

// SandboxStateFilePath returns the path to sandbox-state.json within a sandbox.
func SandboxStateFilePath(sandboxDir string) string {
	return config.SandboxStatePath(sandboxDir)
}

// RuntimeConfigFilePath returns the path to runtime-config.json within a sandbox.
func RuntimeConfigFilePath(sandboxDir string) string {
	return config.RuntimeConfigPath(sandboxDir)
}

// AgentStatusFilePath returns the path to agent-status.json within a sandbox.
func AgentStatusFilePath(sandboxDir string) string {
	return config.AgentStatusPath(sandboxDir)
}

// NetworkDiagFilePath returns the path to network-diag.txt within a sandbox.
func NetworkDiagFilePath(sandboxDir string) string {
	return config.NetworkDiagPath(sandboxDir)
}

// CreateDoneMarkerPath returns the path to the on-create-completed marker.
func CreateDoneMarkerPath(sandboxDir string) string {
	return config.CreateDoneMarkerPath(sandboxDir)
}

// LogsPath returns the logs/ directory within a sandbox.
func LogsPath(sandboxDir string) string {
	return config.LogsPath(sandboxDir)
}

// The *JSONLFile constants above are sandbox-root-relative ("logs/x.jsonl")
// because the guest and the Python writers spell them that way. Host-side
// callers must use these helpers instead of joining the constant onto a
// sandboxDir: the two agree today, but logs/ is a tier-owned directory and only
// the helper follows it.

// CLIJSONLPath returns the path to logs/cli.jsonl within a sandbox.
func CLIJSONLPath(sandboxDir string) string {
	return config.LogFilePath(sandboxDir, "cli.jsonl")
}

// SandboxJSONLPath returns the path to logs/sandbox.jsonl within a sandbox.
func SandboxJSONLPath(sandboxDir string) string {
	return config.LogFilePath(sandboxDir, "sandbox.jsonl")
}

// MonitorJSONLPath returns the path to logs/monitor.jsonl within a sandbox.
func MonitorJSONLPath(sandboxDir string) string {
	return config.LogFilePath(sandboxDir, "monitor.jsonl")
}

// HooksJSONLPath returns the path to logs/agent-hooks.jsonl within a sandbox.
func HooksJSONLPath(sandboxDir string) string {
	return config.LogFilePath(sandboxDir, "agent-hooks.jsonl")
}

// AgentLogPath returns the path to logs/agent.log within a sandbox.
func AgentLogPath(sandboxDir string) string {
	return config.LogFilePath(sandboxDir, "agent.log")
}

// GuestLogFilePaths returns the host paths of the structured log files the
// in-guest writers append to. Create pre-creates them so they exist with the
// right permissions before the guest opens them, and reset re-creates them
// after clearing logs/; both need the same set, in the same place.
func GuestLogFilePaths(sandboxDir string) []string {
	return []string{
		SandboxJSONLPath(sandboxDir),
		MonitorJSONLPath(sandboxDir),
		HooksJSONLPath(sandboxDir),
	}
}

// SecretsConsumedMarkerPath returns the path to the secrets-consumed marker.
func SecretsConsumedMarkerPath(sandboxDir string) string {
	return config.LogFilePath(sandboxDir, ".secrets-consumed")
}

// SubstrateReadyMarkerPath returns the path to the substrate-ready marker.
func SubstrateReadyMarkerPath(sandboxDir string) string {
	return config.LogFilePath(sandboxDir, ".substrate-ready")
}

// ContextFilePath returns the path to context.md within a sandbox.
func ContextFilePath(sandboxDir string) string {
	return config.ContextPath(sandboxDir)
}

// PromptFilePath returns the path to prompt.txt within a sandbox.
func PromptFilePath(sandboxDir string) string {
	return config.PromptPath(sandboxDir)
}

// ResumePromptFilePath returns the path to resume-prompt.txt within a sandbox.
func ResumePromptFilePath(sandboxDir string) string {
	return config.ResumePromptPath(sandboxDir)
}

// MachineIDPath returns the path to the stable machine-id file within a sandbox.
func MachineIDPath(sandboxDir string) string {
	return config.MachineIDPath(sandboxDir)
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
	return filepath.Join(config.WorkBasePath(sandboxDir), EncodePath(hostPath))
}

// FilesDir returns the host-side file exchange directory within a sandbox.
//
//	<sandboxDir>/files/
func FilesDir(sandboxDir string) string {
	return config.FilesPath(sandboxDir)
}

// CacheDir returns the host-side cache directory within a sandbox.
//
//	<sandboxDir>/cache/
func CacheDir(sandboxDir string) string {
	return config.CachePath(sandboxDir)
}

// ABOUTME: Sandbox-directory layout helpers shared by store/paths.go and the
// ABOUTME: runtime backends — the single source of per-sandbox subpath truth.
package config

import (
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/fileutil"
)

// Sandbox access tiers. A sandbox directory contains exactly these three
// subdirectories and nothing else at its root; every per-sandbox file lives
// inside one of them. The tier a file sits in *is* its guest-access class, so
// classification is structural — a new file cannot be added without choosing a
// tier, and the mount/share wiring exposes tiers, never individual files. See
// docs/contributors/design/plans/sandbox-share-tiering.md.
const (
	// HostTierName holds host-only state the guest must never reach
	// (environment.json, sandbox-state.json, agent.json, netpolicy.json,
	// backend/). Never shared into any sandbox.
	HostTierName = "host"
	// ReadOnlyTierName holds files the guest reads but must not write
	// (runtime-config.json, bin/ scripts, prompt.txt, machine-id, home-seed/,
	// secrets/). Shared read-only.
	ReadOnlyTierName = "ro"
	// ReadWriteTierName holds files the guest reads and writes (logs/,
	// agent-runtime/, agent-status.json, files/, cache/, home/, work/, tmux/,
	// setup.log, vscode-cli/). Shared read-write.
	ReadWriteTierName = "rw"
)

// HostTierDir returns the host-only tier directory within a sandbox directory.
func HostTierDir(sandboxDir string) string { return filepath.Join(sandboxDir, HostTierName) }

// ReadOnlyTierDir returns the guest-read-only tier directory within a sandbox.
func ReadOnlyTierDir(sandboxDir string) string { return filepath.Join(sandboxDir, ReadOnlyTierName) }

// ReadWriteTierDir returns the guest-read-write tier directory within a sandbox.
func ReadWriteTierDir(sandboxDir string) string {
	return filepath.Join(sandboxDir, ReadWriteTierName)
}

// Per-sandbox entry names. Every file and directory inside a sandbox dir is
// named exactly once, here. The path builders below are the only supported way
// to turn a sandboxDir into a subpath: an ad-hoc filepath.Join at a call site
// re-derives the layout, and the tier a path belongs to then has no single
// place to change. store/paths.go re-exports these for its own callers; the
// builders live here rather than there because the runtime backends and
// internal/broker cannot import store.
const (
	EnvironmentFileName  = "environment.json"
	SandboxStateFileName = "sandbox-state.json"
	AgentConfigFileName  = "agent.json"
	NetpolicyFileName    = "netpolicy.json"
	NetworkDiagFileName  = "network-diag.txt"
	// The injector's record, log and placeholder token are host-side only —
	// the token comment in internal/broker is explicit that it is never
	// bind-mounted, which is what keeps a co-resident container from learning
	// another sandbox's token.
	InjectorRecordFileName = "injector.json"
	InjectorLogFileName    = "injector.log"
	InjectorTokenFileName  = "injector-token"

	RuntimeConfigFileName = "runtime-config.json"
	PromptFileName        = "prompt.txt"
	ResumePromptFileName  = "resume-prompt.txt"
	MachineIDFileName     = "machine-id"
	HomeSeedDirName       = "home-seed"

	AgentStatusFileName = "agent-status.json"
	LogsDirName         = "logs"
	FilesDirName        = "files"
	CacheDirName        = "cache"
	WorkDirName         = "work"
	VSCodeCLIDirName    = "vscode-cli"
	ContextFileName     = "context.md"
	// SecretsDirName holds credentials staged for the guest to read once at
	// startup, then removed. It was a bare literal at three call sites until
	// 2026-07-30; naming it is what lets its tier be changed in one place.
	SecretsDirName = "secrets"
	// ContainerLogFileName is the guest-written container log the containerd
	// backend bind-mounts and tails.
	ContainerLogFileName = "log.txt"
	// CreateDoneMarkerName records that on-create setup commands completed, so
	// a restart does not re-run them.
	CreateDoneMarkerName = "lifecycle-on-create-done"
)

// EnsureHostTier creates the host-only tier directory inside an existing
// sandbox directory, so a writer of host-tier state does not depend on whoever
// built the sandbox having created it. It deliberately refuses to create the
// sandbox dir itself: a writer that conjured one would turn "no such sandbox"
// into a silently created empty one, which is how a typo becomes a new sandbox.
func EnsureHostTier(sandboxDir string) error {
	if _, err := os.Stat(sandboxDir); err != nil {
		return err
	}
	return fileutil.MkdirAll(HostTierDir(sandboxDir), 0o750)
}

// Host-tier paths. Guest-invisible sandbox bookkeeping: these resolve inside
// host/, which is never shared into any sandbox on any backend. That placement
// is what closes DF136 — the agent cannot forge a record it cannot reach.

// EnvironmentPath returns the path to environment.json within a sandbox.
func EnvironmentPath(sandboxDir string) string {
	return filepath.Join(HostTierDir(sandboxDir), EnvironmentFileName)
}

// SandboxStatePath returns the path to sandbox-state.json within a sandbox.
func SandboxStatePath(sandboxDir string) string {
	return filepath.Join(HostTierDir(sandboxDir), SandboxStateFileName)
}

// AgentConfigPath returns the path to agent.json within a sandbox.
func AgentConfigPath(sandboxDir string) string {
	return filepath.Join(HostTierDir(sandboxDir), AgentConfigFileName)
}

// NetpolicyPath returns the path to netpolicy.json within a sandbox.
func NetpolicyPath(sandboxDir string) string {
	return filepath.Join(HostTierDir(sandboxDir), NetpolicyFileName)
}

// NetworkDiagPath returns the path to network-diag.txt within a sandbox.
func NetworkDiagPath(sandboxDir string) string {
	return filepath.Join(sandboxDir, NetworkDiagFileName)
}

// BackendPath returns the backend-specific state directory within a sandbox
// (SBPL profile, pids, VM/CNI state).
func BackendPath(sandboxDir string) string {
	return filepath.Join(sandboxDir, BackendDirName)
}

// InjectorRecordPath returns the path to the injector's pid/addr record.
func InjectorRecordPath(sandboxDir string) string {
	return filepath.Join(sandboxDir, InjectorRecordFileName)
}

// InjectorLogPath returns the path to the injector's host-side log.
func InjectorLogPath(sandboxDir string) string {
	return filepath.Join(sandboxDir, InjectorLogFileName)
}

// InjectorTokenPath returns the path to the sandbox's injector placeholder
// token.
func InjectorTokenPath(sandboxDir string) string {
	return filepath.Join(sandboxDir, InjectorTokenFileName)
}

// Read-only-tier paths. The guest reads these; only the host writes them.

// RuntimeConfigPath returns the path to runtime-config.json within a sandbox.
func RuntimeConfigPath(sandboxDir string) string {
	return filepath.Join(sandboxDir, RuntimeConfigFileName)
}

// BinPath returns the guest-exec'd script directory within a sandbox.
func BinPath(sandboxDir string) string {
	return filepath.Join(sandboxDir, BinDirName)
}

// PromptPath returns the path to prompt.txt within a sandbox.
func PromptPath(sandboxDir string) string {
	return filepath.Join(sandboxDir, PromptFileName)
}

// ResumePromptPath returns the path to resume-prompt.txt within a sandbox.
func ResumePromptPath(sandboxDir string) string {
	return filepath.Join(sandboxDir, ResumePromptFileName)
}

// MachineIDPath returns the path to the sandbox's stable machine-id file.
func MachineIDPath(sandboxDir string) string {
	return filepath.Join(sandboxDir, MachineIDFileName)
}

// HomeSeedPath returns the home-seed directory within a sandbox.
func HomeSeedPath(sandboxDir string) string {
	return filepath.Join(sandboxDir, HomeSeedDirName)
}

// Read-write-tier paths. The guest reads and writes these.

// LogsPath returns the logs/ directory within a sandbox.
func LogsPath(sandboxDir string) string {
	return filepath.Join(sandboxDir, LogsDirName)
}

// LogFilePath returns the path to a named file under a sandbox's logs/ dir.
// Callers pass a bare file name; the logs-relative constants in store are
// spelled "logs/x.jsonl" for the guest's benefit and resolve to the same place.
func LogFilePath(sandboxDir, name string) string {
	return filepath.Join(sandboxDir, LogsDirName, name)
}

// AgentStatusPath returns the path to agent-status.json within a sandbox.
func AgentStatusPath(sandboxDir string) string {
	return filepath.Join(sandboxDir, AgentStatusFileName)
}

// AgentRuntimePath returns the agent-managed state directory within a sandbox.
func AgentRuntimePath(sandboxDir string) string {
	return filepath.Join(sandboxDir, AgentRuntimeDirName)
}

// AgentRuntimeFilePath returns the path to a relative entry inside a sandbox's
// agent-runtime directory.
func AgentRuntimeFilePath(sandboxDir, relPath string) string {
	return filepath.Join(sandboxDir, AgentRuntimeDirName, relPath)
}

// FilesPath returns the host<->guest file exchange directory within a sandbox.
func FilesPath(sandboxDir string) string {
	return filepath.Join(sandboxDir, FilesDirName)
}

// CachePath returns the cache directory within a sandbox.
func CachePath(sandboxDir string) string {
	return filepath.Join(sandboxDir, CacheDirName)
}

// WorkBasePath returns the base directory holding a sandbox's per-mount work
// copies. Individual work copies are addressed via store.WorkDir.
func WorkBasePath(sandboxDir string) string {
	return filepath.Join(sandboxDir, WorkDirName)
}

// TmuxPath returns the tmux config/socket directory within a sandbox.
func TmuxPath(sandboxDir string) string {
	return filepath.Join(sandboxDir, TmuxDirName)
}

// SecretsPath returns the ephemeral credential staging directory within a
// sandbox. Guest-read once at startup and then removed by the backend.
func SecretsPath(sandboxDir string) string {
	return filepath.Join(sandboxDir, SecretsDirName)
}

// VSCodeCLIPath returns the VS Code CLI state directory within a sandbox.
func VSCodeCLIPath(sandboxDir string) string {
	return filepath.Join(sandboxDir, VSCodeCLIDirName)
}

// ContainerLogPath returns the path to the guest-written container log.
func ContainerLogPath(sandboxDir string) string {
	return filepath.Join(sandboxDir, ContainerLogFileName)
}

// ContextPath returns the path to context.md within a sandbox.
func ContextPath(sandboxDir string) string {
	return filepath.Join(sandboxDir, ContextFileName)
}

// CreateDoneMarkerPath returns the path to the on-create-completed marker.
func CreateDoneMarkerPath(sandboxDir string) string {
	return filepath.Join(sandboxDir, CreateDoneMarkerName)
}

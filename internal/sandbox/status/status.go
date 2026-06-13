// ABOUTME: Status/AgentStatus types and InspectSandbox/ListSandboxes/DetectStatus:
// ABOUTME: the read-only view of a sandbox's live state consumed by CLI commands.
package status

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/git"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox/patch"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
)

// Status represents the current state of a sandbox.
type Status string

// Status constants for sandbox lifecycle states.
const (
	StatusActive      Status = "active"      // container running, agent actively working
	StatusIdle        Status = "idle"        // container running, agent alive, bell flag set (finished processing)
	StatusDone        Status = "done"        // container running, agent exited cleanly (exit 0)
	StatusFailed      Status = "failed"      // container running, agent exited with error (non-zero)
	StatusStopped     Status = "stopped"     // container stopped (docker stop)
	StatusSuspended   Status = "suspended"   // VM suspended (state on disk, quota slot free; Tart only)
	StatusRemoved     Status = "removed"     // container removed but sandbox dir exists
	StatusBroken      Status = "broken"      // sandbox dir exists but environment.json missing/invalid
	StatusUnavailable Status = "unavailable" // backend not running (container state unknown)
)

// AgentStatus represents the agent's activity state within a running sandbox.
type AgentStatus string

const (
	AgentStatusUnknown AgentStatus = ""       // status not yet determined
	AgentStatusActive  AgentStatus = "active" // agent is actively working
	AgentStatusIdle    AgentStatus = "idle"   // agent is idle, awaiting input
	AgentStatusDone    AgentStatus = "done"   // agent has completed its task
	AgentStatusFailed  AgentStatus = "failed" // agent exited with an error
)

// Info holds the combined metadata and live state for a sandbox.
type Info struct {
	Environment *store.Environment `json:"environment"`
	Status      Status             `json:"status"`
	AgentStatus AgentStatus        `json:"agent_status,omitempty"` // agent activity status (may be empty)
	HasChanges  string             `json:"has_changes"`            // "yes", "no", "unknown" (stopped VM-local backend), or "-" (not applicable)
	// DiskUsageBytes is the total size of the sandbox directory in bytes, or
	// -1 when it could not be measured. Rendering to a human-readable string
	// is the CLI's responsibility (see cliutil.FormatSize).
	DiskUsageBytes int64 `json:"disk_usage_bytes"`
}

// DirSize recursively calculates the total size of all files under path.
func DirSize(path string) (int64, error) {
	var size int64
	err := filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			size += info.Size()
		}
		return nil
	})
	return size, err
}

// WorkDataState classifies what a sandbox directory holds, determined by
// filesystem inspection alone — no environment.json required. This is the
// recoverability signal used when meta is unreadable (broken sandboxes), so
// destroy/prune can reason about a sandbox they cannot otherwise load.
type WorkDataState int

const (
	// WorkDataNone: no work/ payload — nothing the user could lose.
	WorkDataNone WorkDataState = iota
	// WorkDataPresent: detected uncommitted changes (copy: dirty git tree;
	// overlay: a non-empty host-side upper layer).
	WorkDataPresent
	// WorkDataAmbiguous: a work/ payload exists but its state can't be
	// confirmed without meta (e.g. a clean copy tree whose baseline is
	// unknown, or a partially-populated work dir). Callers treat this as
	// "might hold data" and preserve it.
	WorkDataAmbiguous
)

// ProbeWorkData inspects a sandbox directory for recoverable user data
// without loading its metadata. It walks work/* and classifies each
// payload: copy dirs are probed with `git status`; overlay dirs are probed
// by checking the host-side upper layer. Returns the strongest signal found
// (Present > Ambiguous > None) and a human-readable detail for the first
// payload that carries data.
// g is a host-scoped git runner derived from the caller's layout (DEV §12).
func ProbeWorkData(ctx context.Context, g *git.Git, sandboxDir string) (WorkDataState, string) {
	entries, err := os.ReadDir(filepath.Join(sandboxDir, "work"))
	if err != nil {
		return WorkDataNone, ""
	}

	result := WorkDataNone
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		workEntry := filepath.Join(sandboxDir, "work", entry.Name())

		// Copy mode: the work dir is the git repo itself.
		if _, statErr := os.Stat(filepath.Join(workEntry, ".git")); statErr == nil {
			if patch.DetectChanges(ctx, g, workEntry) == "yes" {
				return WorkDataPresent, "uncommitted changes in copied work dir"
			}
			// Clean tree, but without the baseline SHA from meta we can't
			// rule out commits the user hasn't applied — preserve it.
			result = max(result, WorkDataAmbiguous)
			continue
		}

		// Overlay mode: changes persist host-side in the upper layer
		// regardless of container state.
		upper := filepath.Join(workEntry, "upper")
		if dirHasEntries(upper) {
			return WorkDataPresent, "changes captured in overlay upper layer"
		}

		// A work payload we can't otherwise classify (no .git, no upper):
		// treat its mere presence as something to preserve.
		if dirHasEntries(workEntry) {
			result = max(result, WorkDataAmbiguous)
		}
	}
	return result, ""
}

// dirHasEntries reports whether dir exists and contains at least one entry.
func dirHasEntries(dir string) bool {
	f, err := os.Open(dir) //nolint:gosec // G304: dir is a sandbox-controlled path
	if err != nil {
		return false
	}
	defer f.Close() //nolint:errcheck // read-only handle
	names, err := f.Readdirnames(1)
	return err == nil && len(names) > 0
}

// ContainerUser is re-exported from store so existing callers in this
// package continue to compile. The body lives in store/environment.go now so
// patch/ can reach it without importing the sandbox parent (F6).
func ContainerUser(meta *store.Environment, hostUID int) string {
	return store.ContainerUser(meta, hostUID)
}

// ExecInContainer runs a command inside a sandbox instance and returns stdout.
// hostUID is layout.HostUID at the boundary (F31); it precedes cmd so
// multi-line cmd literals at call sites stay readable.
func ExecInContainer(ctx context.Context, rt runtime.Backend, sandboxName string, meta *store.Environment, hostUID int, cmd []string) (string, error) {
	result, err := rt.Exec(ctx, store.InstanceName(meta.Principal, sandboxName), cmd, ContainerUser(meta, hostUID))
	if err != nil {
		return "", err
	}
	return result.Stdout, nil
}

// statusFileStaleness is the maximum age of a status.json timestamp before
// falling back to exec-based detection.
const statusFileStaleness = 10 * time.Second

// agentStatusSchemaVersion is the contract version for agent-status.json. Must
// equal the AGENT_STATUS_SCHEMA_VERSION constant in sandbox-setup.py, the
// literal in status-monitor.py, and the literals in agent.go's shell hook
// commands. The cross-language fence in schema_version_test.go (F7) asserts
// this agreement at every `go test ./...`. W2 of the architecture remediation
// plan.
const agentStatusSchemaVersion = 1

// statusJSON is the structure written by the in-container status monitor.
// Designed for extensibility — new fields can be added without breaking
// readers. The schema_version field is omitempty so files written by older
// yoloai versions (before W2) parse with SchemaVersion=0; the reader tolerates
// 0 and otherwise enforces a match.
type statusJSON struct {
	SchemaVersion int    `json:"schema_version,omitempty"`
	Status        string `json:"status"`              // "active", "idle", "done"
	ExitCode      *int   `json:"exit_code,omitempty"` // set when status is "done"
	Timestamp     int64  `json:"timestamp"`           // unix seconds
}

// DetectStatus queries the runtime and agent-status.json to determine sandbox status.
// sandboxDir is the host-side sandbox directory.
func DetectStatus(ctx context.Context, rt runtime.Backend, containerName string, sandboxDir string) (Status, error) {
	info, err := rt.Inspect(ctx, containerName)
	if err != nil {
		if errors.Is(err, runtime.ErrNotFound) {
			return StatusRemoved, nil
		}
		return "", fmt.Errorf("inspect container: %w", err)
	}

	if !info.Running {
		if info.Suspended {
			return StatusSuspended, nil
		}
		return StatusStopped, nil
	}

	// Try agent-status.json (fast path — no exec)
	if sandboxDir != "" {
		statusPath := filepath.Join(sandboxDir, store.AgentStatusFile)
		data, readErr := os.ReadFile(statusPath) //nolint:gosec // path is sandbox-controlled
		if readErr == nil && len(data) > 0 {
			if status, ok := parseStatusJSON(data); ok {
				return status, nil
			}
		}
	}

	// If status file is missing or stale, assume active (container is running)
	slog.Debug("detecting sandbox status", "event", "sandbox.inspect.status", "container", containerName, "result", string(StatusActive))
	return StatusActive, nil
}

// parseStatusJSON parses the status.json content and returns the status.
// Returns false if the content is invalid or stale (except for terminal "done" state).
func parseStatusJSON(data []byte) (Status, bool) {
	var s statusJSON
	if err := json.Unmarshal(data, &s); err != nil {
		return "", false
	}

	// schema_version=0 means the file was written before W2 (no version
	// field). Any non-zero value must match the expected version; mismatch
	// signals coordinated Python/Go drift and we treat the file as unusable.
	if s.SchemaVersion != 0 && s.SchemaVersion != agentStatusSchemaVersion {
		slog.Warn("agent-status.json schema_version mismatch — file ignored",
			"event", "sandbox.inspect.schema_mismatch",
			"got", s.SchemaVersion,
			"expected", agentStatusSchemaVersion)
		return "", false
	}

	if s.Status == "" || s.Timestamp == 0 {
		return "", false
	}

	switch s.Status {
	case "active":
		age := time.Since(time.Unix(s.Timestamp, 0))
		if age > statusFileStaleness {
			return "", false // stale — fall back to exec
		}
		return StatusActive, true

	case "idle":
		// Idle is a persistent state written once (by hook or monitor) and
		// cleared only by new prompt delivery or agent exit. No staleness
		// check — the status remains valid until explicitly changed.
		return StatusIdle, true

	case "done":
		// "done" is a terminal state — trust it even if stale
		exitCode := 1
		if s.ExitCode != nil {
			exitCode = *s.ExitCode
		}
		if exitCode == 0 {
			return StatusDone, true
		}
		return StatusFailed, true

	default:
		return "", false
	}
}

// InspectSandbox loads metadata and queries the runtime for a single sandbox.
func InspectSandbox(ctx context.Context, layout config.Layout, rt runtime.Backend, name string) (*Info, error) {
	sandboxDir := layout.SandboxDir(name)
	if err := store.RequireSandboxDir(sandboxDir); err != nil {
		return nil, err
	}

	meta, err := store.LoadEnvironment(sandboxDir)
	if err != nil {
		return nil, fmt.Errorf("load metadata: %w", err)
	}

	status, err := DetectStatus(ctx, rt, store.InstanceName(layout.Principal, name), sandboxDir)
	if err != nil {
		return nil, err
	}

	diskUsageBytes := int64(-1)
	if size, err := DirSize(sandboxDir); err == nil {
		diskUsageBytes = size
	}

	return &Info{
		Environment:    meta,
		Status:         status,
		HasChanges:     detectWorkdirChanges(ctx, git.NewSandbox(layout, rt, name), sandboxDir, meta),
		DiskUsageBytes: diskUsageBytes,
	}, nil
}

// detectWorkdirChanges returns "yes", "no", "unknown", or "-" for a sandbox's
// workdir and aux dirs. "unknown" means the working copy lives in a VM-local
// backend (Tart) that is not running, so the probe can't reach it — the change
// state genuinely can't be read from the host (see patch.HasUnappliedWorkVia).
func detectWorkdirChanges(ctx context.Context, g *git.Git, sandboxDir string, meta *store.Environment) string {
	if meta.Workdir().Mode != "copy" && meta.Workdir().Mode != "overlay" {
		return "-"
	}
	workDir := store.WorkDir(sandboxDir, meta.Workdir().HostPath)
	switch patch.HasUnappliedWorkVia(ctx, g, workDir, meta.Workdir().BaselineSHA) {
	case patch.WorkDirty:
		return "yes"
	case patch.WorkUnknown:
		return "unknown"
	case patch.WorkClean:
	}
	// workdir has no unapplied work — check aux dirs before reporting "no"
	for _, d := range meta.AuxDirs() {
		if d.Mode == "copy" || d.Mode == "overlay" {
			auxWorkDir := store.WorkDir(sandboxDir, d.HostPath)
			switch patch.HasUnappliedWorkVia(ctx, g, auxWorkDir, d.BaselineSHA) {
			case patch.WorkDirty:
				return "yes"
			case patch.WorkUnknown:
				return "unknown"
			case patch.WorkClean:
			}
		}
	}
	return "no"
}

// InspectSandboxWithBackend loads metadata and optionally queries the runtime.
// If rt is nil, returns basic info (from environment.json and filesystem) with StatusUnavailable.
// If rt is available, performs full inspection including container state.
func InspectSandboxWithBackend(ctx context.Context, layout config.Layout, rt runtime.Backend, name string) (*Info, error) {
	sandboxDir := layout.SandboxDir(name)
	if err := store.RequireSandboxDir(sandboxDir); err != nil {
		return nil, err
	}

	meta, err := store.LoadEnvironment(sandboxDir)
	if err != nil {
		return nil, fmt.Errorf("load metadata: %w", err)
	}

	diskUsageBytes := int64(-1)
	if size, err := DirSize(sandboxDir); err == nil {
		diskUsageBytes = size
	}

	// If runtime is nil, return basic info with unavailable status
	if rt == nil {
		return &Info{
			Environment:    meta,
			Status:         StatusUnavailable,
			HasChanges:     "-",
			DiskUsageBytes: diskUsageBytes,
		}, nil
	}

	// Runtime available - perform full inspection
	status, err := DetectStatus(ctx, rt, store.InstanceName(layout.Principal, name), sandboxDir)
	if err != nil {
		return nil, err
	}

	return &Info{
		Environment:    meta,
		Status:         status,
		HasChanges:     detectWorkdirChanges(ctx, git.NewSandbox(layout, rt, name), sandboxDir, meta),
		DiskUsageBytes: diskUsageBytes,
	}, nil
}

// ListSandboxes scans ~/.yoloai/sandboxes/ and returns info for all sandboxes.
func ListSandboxes(ctx context.Context, layout config.Layout, rt runtime.Backend) ([]*Info, error) {
	sandboxesDir := layout.SandboxesDir()

	entries, err := os.ReadDir(sandboxesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read sandboxes directory: %w", err)
	}

	var result []*Info
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := InspectSandbox(ctx, layout, rt, entry.Name())
		if err != nil {
			// Include broken sandboxes with minimal info
			result = append(result, &Info{
				Environment:    &store.Environment{Name: entry.Name()},
				Status:         StatusBroken,
				HasChanges:     "-",
				DiskUsageBytes: -1,
			})
			continue
		}
		result = append(result, info)
	}

	return result, nil
}

// ListSandboxesMultiBackend scans sandboxes and inspects them using their respective backends.
// Takes a newRuntimeFunc parameter for creating runtimes (enables testing).
// Returns (infos, unavailableBackends, error).
// Sandboxes whose backends are unavailable get StatusUnavailable.
func ListSandboxesMultiBackend(ctx context.Context, layout config.Layout, newRuntimeFunc func(context.Context, runtime.BackendType) (runtime.Backend, error)) ([]*Info, []string, error) {
	sandboxesDir := layout.SandboxesDir()

	entries, err := os.ReadDir(sandboxesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("read sandboxes directory: %w", err)
	}

	backendSandboxes := groupSandboxesByBackend(entries, sandboxesDir)

	var result []*Info
	var unavailableBackends []string
	unavailableSet := make(map[runtime.BackendType]bool)

	for backend, names := range backendSandboxes {
		if backend == "" {
			result = append(result, brokenInfos(names)...)
			continue
		}
		infos, unavail := inspectBackendGroup(ctx, layout, newRuntimeFunc, backend, names, unavailableSet)
		result = append(result, infos...)
		unavailableBackends = append(unavailableBackends, unavail...)
	}

	return result, unavailableBackends, nil
}

// groupSandboxesByBackend maps backend name → sandbox names from the sandbox directory entries.
// Broken sandboxes (unreadable meta) are keyed to "".
func groupSandboxesByBackend(entries []os.DirEntry, sandboxesDir string) map[runtime.BackendType][]string {
	byBackend := make(map[runtime.BackendType][]string)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		sandboxDir := filepath.Join(sandboxesDir, entry.Name())
		meta, err := store.LoadEnvironment(sandboxDir)
		if err != nil {
			byBackend[""] = append(byBackend[""], entry.Name())
			continue
		}
		// BackendType is guaranteed non-empty for any healthy meta (the v0→v1
		// migration backfills legacy Docker sandboxes; see store.migrate). An
		// empty value here is genuinely broken metadata and keys to "", which
		// the caller routes to brokenInfos.
		byBackend[meta.BackendType] = append(byBackend[meta.BackendType], entry.Name())
	}
	return byBackend
}

// brokenInfos returns a StatusBroken Info entry for each sandbox name.
func brokenInfos(names []string) []*Info {
	infos := make([]*Info, len(names))
	for i, name := range names {
		infos[i] = &Info{
			Environment:    &store.Environment{Name: name},
			Status:         StatusBroken,
			HasChanges:     "-",
			DiskUsageBytes: -1,
		}
	}
	return infos
}

// inspectBackendGroup inspects all sandboxes for a single backend, returning
// their Info entries and any newly discovered unavailable backend names.
func inspectBackendGroup(ctx context.Context, layout config.Layout, newRuntimeFunc func(context.Context, runtime.BackendType) (runtime.Backend, error), backend runtime.BackendType, names []string, unavailableSet map[runtime.BackendType]bool) ([]*Info, []string) {
	var unavailableBackends []string
	rt, err := newRuntimeFunc(ctx, backend)
	var effectiveRT runtime.Backend
	if err == nil {
		effectiveRT = rt
		defer rt.Close() //nolint:errcheck,gosec // best-effort cleanup
	} else if !unavailableSet[backend] {
		unavailableBackends = append(unavailableBackends, string(backend))
		unavailableSet[backend] = true
	}

	var result []*Info
	for _, name := range names {
		info, inspectErr := InspectSandboxWithBackend(ctx, layout, effectiveRT, name)
		if inspectErr != nil {
			result = append(result, &Info{
				Environment:    &store.Environment{Name: name},
				Status:         StatusBroken,
				HasChanges:     "-",
				DiskUsageBytes: -1,
			})
			continue
		}
		result = append(result, info)
	}
	return result, unavailableBackends
}

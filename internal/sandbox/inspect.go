// ABOUTME: Status/AgentStatus types and InspectSandbox/ListSandboxes/DetectStatus:
// ABOUTME: the read-only view of a sandbox's live state consumed by CLI commands.
package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox/state"
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
	StatusBroken      Status = "broken"      // sandbox dir exists but meta.json missing/invalid
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
	Meta        *store.Meta `json:"meta"`
	Status      Status      `json:"status"`
	AgentStatus AgentStatus `json:"agent_status,omitempty"` // agent activity status (may be empty)
	HasChanges  string      `json:"has_changes"`            // "yes", "no", or "-" (unknown/not applicable)
	DiskUsage   string      `json:"disk_usage"`             // human-readable size, e.g. "42.0MB"
}

// FormatAge returns a human-readable duration string (e.g., "2h", "3d", "5m").
func FormatAge(created time.Time) string {
	d := time.Since(created)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
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

// FormatSize returns a human-readable size string (e.g., "1.2GB", "340KB").
func FormatSize(bytes int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)
	switch {
	case bytes >= gb:
		return fmt.Sprintf("%.1fGB", float64(bytes)/float64(gb))
	case bytes >= mb:
		return fmt.Sprintf("%.1fMB", float64(bytes)/float64(mb))
	case bytes >= kb:
		return fmt.Sprintf("%dKB", bytes/kb)
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}

// detectChanges checks if the sandbox work directory has uncommitted changes.
// Returns "yes" if changes exist, "no" if clean, "-" if not applicable.
func detectChanges(workDir string) string {
	if _, err := os.Stat(workDir); err != nil {
		return "-"
	}
	if _, err := os.Stat(filepath.Join(workDir, ".git")); err != nil {
		return "-"
	}
	cmd := exec.Command("git", "-C", workDir, "status", "--porcelain") //nolint:gosec // G204: workDir is sandbox-controlled path
	output, err := cmd.Output()
	if err != nil {
		return "-"
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(output)), "\n") {
		if len(line) < 3 {
			continue
		}
		name := filepath.Base(line[3:])
		if strings.HasPrefix(name, "yoloai-bugreport-") &&
			(strings.HasSuffix(name, ".md") || strings.HasSuffix(name, ".md.tmp")) {
			continue
		}
		return "yes"
	}
	return "no"
}

// hasUnappliedWork checks if a work directory has any unapplied work:
// uncommitted changes OR commits beyond the baseline SHA.
// Returns true if work exists that would be lost on destruction.
func hasUnappliedWork(workDir, baselineSHA string) bool {
	if detectChanges(workDir) == "yes" {
		return true
	}
	if baselineSHA == "" {
		return false
	}
	// Check for commits beyond the baseline
	cmd := exec.Command("git", "-C", workDir, "rev-list", "--count", baselineSHA+"..HEAD") //nolint:gosec // G204: workDir and baselineSHA are sandbox-controlled
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(output)) != "0"
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
func ProbeWorkData(sandboxDir string) (WorkDataState, string) {
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
			if detectChanges(workEntry) == "yes" {
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
// package continue to compile. The body lives in store/meta.go now so
// patch/ can reach it without importing the sandbox parent (F6).
func ContainerUser(meta *store.Meta, hostUID int) string {
	return store.ContainerUser(meta, hostUID)
}

// IsolationPerms is re-exported from state so existing façade callers keep
// compiling after F5.2b moved the body to the state leaf.
type IsolationPerms = state.IsolationPerms

// Perms is re-exported from state. See state.Perms.
var Perms = state.Perms

// ExecInContainer runs a command inside a sandbox instance and returns stdout.
// hostUID is layout.HostUID at the boundary (F31); it precedes cmd so
// multi-line cmd literals at call sites stay readable.
func ExecInContainer(ctx context.Context, rt runtime.Runtime, sandboxName string, meta *store.Meta, hostUID int, cmd []string) (string, error) {
	result, err := rt.Exec(ctx, store.InstanceName(sandboxName), cmd, ContainerUser(meta, hostUID))
	if err != nil {
		return "", err
	}
	return result.Stdout, nil
}

// statusFileStaleness is the maximum age of a status.json timestamp before
// falling back to exec-based detection.
const statusFileStaleness = 10 * time.Second

// agentStatusSchemaVersion is the contract version for agent-status.json. Must
// equal the AGENT_STATUS_SCHEMA_VERSION constants in sandbox-setup.py and
// status-monitor.py, and the literal in agent.go's shell hook commands. W2 of
// the architecture remediation plan.
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
func DetectStatus(ctx context.Context, rt runtime.Runtime, containerName string, sandboxDir string) (Status, error) {
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
func InspectSandbox(ctx context.Context, layout config.Layout, rt runtime.Runtime, name string) (*Info, error) {
	sandboxDir := layout.SandboxDir(name)
	if err := store.RequireSandboxDir(sandboxDir); err != nil {
		return nil, err
	}

	meta, err := store.LoadMeta(sandboxDir)
	if err != nil {
		return nil, fmt.Errorf("load metadata: %w", err)
	}

	status, err := DetectStatus(ctx, rt, store.InstanceName(name), sandboxDir)
	if err != nil {
		return nil, err
	}

	changes := "-"
	if meta.Workdir.Mode == "copy" || meta.Workdir.Mode == "overlay" {
		workDir := store.WorkDir(sandboxDir, meta.Workdir.HostPath)
		if hasUnappliedWork(workDir, meta.Workdir.BaselineSHA) {
			changes = "yes"
		} else if detectChanges(workDir) != "-" {
			changes = "no"
		}
	}

	// Also check aux :copy/:overlay dirs for changes
	if changes == "no" {
		for _, d := range meta.Directories {
			if d.Mode == "copy" || d.Mode == "overlay" {
				auxWorkDir := store.WorkDir(sandboxDir, d.HostPath)
				if hasUnappliedWork(auxWorkDir, d.BaselineSHA) {
					changes = "yes"
					break
				}
			}
		}
	}

	diskUsage := "-"
	if size, err := DirSize(sandboxDir); err == nil {
		diskUsage = FormatSize(size)
	}

	return &Info{
		Meta:       meta,
		Status:     status,
		HasChanges: changes,
		DiskUsage:  diskUsage,
	}, nil
}

// detectWorkdirChanges returns "yes", "no", or "-" for a sandbox's workdir and aux dirs.
func detectWorkdirChanges(sandboxDir string, meta *store.Meta) string {
	if meta.Workdir.Mode != "copy" && meta.Workdir.Mode != "overlay" {
		return "-"
	}
	workDir := store.WorkDir(sandboxDir, meta.Workdir.HostPath)
	if hasUnappliedWork(workDir, meta.Workdir.BaselineSHA) {
		return "yes"
	}
	if detectChanges(workDir) == "-" {
		return "-"
	}
	// workdir has no unapplied work — check aux dirs before reporting "no"
	for _, d := range meta.Directories {
		if d.Mode == "copy" || d.Mode == "overlay" {
			auxWorkDir := store.WorkDir(sandboxDir, d.HostPath)
			if hasUnappliedWork(auxWorkDir, d.BaselineSHA) {
				return "yes"
			}
		}
	}
	return "no"
}

// InspectSandboxWithBackend loads metadata and optionally queries the runtime.
// If rt is nil, returns basic info (from meta.json and filesystem) with StatusUnavailable.
// If rt is available, performs full inspection including container state.
func InspectSandboxWithBackend(ctx context.Context, layout config.Layout, rt runtime.Runtime, name string) (*Info, error) {
	sandboxDir := layout.SandboxDir(name)
	if err := store.RequireSandboxDir(sandboxDir); err != nil {
		return nil, err
	}

	meta, err := store.LoadMeta(sandboxDir)
	if err != nil {
		return nil, fmt.Errorf("load metadata: %w", err)
	}

	diskUsage := "-"
	if size, err := DirSize(sandboxDir); err == nil {
		diskUsage = FormatSize(size)
	}

	// If runtime is nil, return basic info with unavailable status
	if rt == nil {
		return &Info{
			Meta:       meta,
			Status:     StatusUnavailable,
			HasChanges: "-",
			DiskUsage:  diskUsage,
		}, nil
	}

	// Runtime available - perform full inspection
	status, err := DetectStatus(ctx, rt, store.InstanceName(name), sandboxDir)
	if err != nil {
		return nil, err
	}

	return &Info{
		Meta:       meta,
		Status:     status,
		HasChanges: detectWorkdirChanges(sandboxDir, meta),
		DiskUsage:  diskUsage,
	}, nil
}

// ListSandboxes scans ~/.yoloai/sandboxes/ and returns info for all sandboxes.
func ListSandboxes(ctx context.Context, layout config.Layout, rt runtime.Runtime) ([]*Info, error) {
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
				Meta:       &store.Meta{Name: entry.Name()},
				Status:     StatusBroken,
				HasChanges: "-",
				DiskUsage:  "-",
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
func ListSandboxesMultiBackend(ctx context.Context, layout config.Layout, newRuntimeFunc func(context.Context, runtime.BackendName) (runtime.Runtime, error)) ([]*Info, []string, error) {
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
	unavailableSet := make(map[runtime.BackendName]bool)

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
func groupSandboxesByBackend(entries []os.DirEntry, sandboxesDir string) map[runtime.BackendName][]string {
	byBackend := make(map[runtime.BackendName][]string)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		sandboxDir := filepath.Join(sandboxesDir, entry.Name())
		meta, err := store.LoadMeta(sandboxDir)
		if err != nil {
			byBackend[""] = append(byBackend[""], entry.Name())
			continue
		}
		backend := meta.Backend
		if backend == "" {
			backend = "docker"
		}
		byBackend[backend] = append(byBackend[backend], entry.Name())
	}
	return byBackend
}

// brokenInfos returns a StatusBroken Info entry for each sandbox name.
func brokenInfos(names []string) []*Info {
	infos := make([]*Info, len(names))
	for i, name := range names {
		infos[i] = &Info{
			Meta:       &store.Meta{Name: name},
			Status:     StatusBroken,
			HasChanges: "-",
			DiskUsage:  "-",
		}
	}
	return infos
}

// inspectBackendGroup inspects all sandboxes for a single backend, returning
// their Info entries and any newly discovered unavailable backend names.
func inspectBackendGroup(ctx context.Context, layout config.Layout, newRuntimeFunc func(context.Context, runtime.BackendName) (runtime.Runtime, error), backend runtime.BackendName, names []string, unavailableSet map[runtime.BackendName]bool) ([]*Info, []string) {
	var unavailableBackends []string
	rt, err := newRuntimeFunc(ctx, backend)
	var effectiveRT runtime.Runtime
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
				Meta:       &store.Meta{Name: name},
				Status:     StatusBroken,
				HasChanges: "-",
				DiskUsage:  "-",
			})
			continue
		}
		result = append(result, info)
	}
	return result, unavailableBackends
}

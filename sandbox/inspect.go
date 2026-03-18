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

	"github.com/kstenerud/yoloai/runtime"
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
	Meta        *Meta       `json:"meta"`
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
	if len(strings.TrimSpace(string(output))) > 0 {
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

// ContainerUser returns the appropriate user string for docker exec operations
// in the given sandbox. Under gVisor, docker exec resolves usernames from the
// OCI image manifest (the placeholder UID used at build time), not the
// container's live /etc/passwd (updated by the entrypoint's uid-remap step).
// Use the numeric host UID instead to match the remapped container user.
func ContainerUser(meta *Meta) string {
	if meta == nil {
		return "yoloai"
	}
	if meta.UsernsMode == "keep-id" {
		return ""
	}
	if meta.Security == "gvisor" {
		return fmt.Sprintf("%d", os.Getuid())
	}
	return "yoloai"
}

// SecurityPerms holds filesystem permission values that vary by security mode.
// Under gVisor, the entrypoint remaps the container's yoloai user UID to the
// host user's UID at runtime, but files created before the remap (e.g. by the
// Go host process) are owned by the original host UID. Both UIDs need access,
// so permissions must be world-accessible.
type SecurityPerms struct {
	Dir         os.FileMode // container-owned directories (work, cache, logs, agent-state)
	File        os.FileMode // container-owned files (logs, status)
	SecretsDir  os.FileMode // ephemeral secrets dir (removed after container mount)
	SecretsFile os.FileMode // individual secret files (removed after container mount)
}

// Perms returns the filesystem permissions appropriate for the given security
// mode. Use this whenever creating host-side files or directories that the
// container process will write to.
func Perms(security string) SecurityPerms {
	if security == "gvisor" {
		return SecurityPerms{
			Dir:         0777, //nolint:gosec // G301: world-writable needed for gVisor user-namespace UID remapping
			File:        0666, //nolint:gosec // G306: world-writable needed for gVisor user-namespace UID remapping
			SecretsDir:  0755, //nolint:gosec // G302: world-executable for gVisor UID remapping; removed within seconds
			SecretsFile: 0644, //nolint:gosec // G306: world-readable for gVisor UID remapping; removed within seconds
		}
	}
	return SecurityPerms{
		Dir:         0750,
		File:        0600,
		SecretsDir:  0700,
		SecretsFile: 0600,
	}
}

// execInContainer runs a command inside a sandbox instance and returns stdout.
func execInContainer(ctx context.Context, rt runtime.Runtime, sandboxName string, meta *Meta, cmd []string) (string, error) {
	result, err := rt.Exec(ctx, InstanceName(sandboxName), cmd, ContainerUser(meta))
	if err != nil {
		return "", err
	}
	return result.Stdout, nil
}

// statusFileStaleness is the maximum age of a status.json timestamp before
// falling back to exec-based detection.
const statusFileStaleness = 10 * time.Second

// statusJSON is the structure written by the in-container status monitor.
// Designed for extensibility — new fields can be added without breaking readers.
type statusJSON struct {
	Status    string `json:"status"`              // "active", "idle", "done"
	ExitCode  *int   `json:"exit_code,omitempty"` // set when status is "done"
	Timestamp int64  `json:"timestamp"`           // unix seconds
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
		return StatusStopped, nil
	}

	// Try agent-status.json (fast path — no exec)
	if sandboxDir != "" {
		statusPath := filepath.Join(sandboxDir, AgentStatusFile)
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
func InspectSandbox(ctx context.Context, rt runtime.Runtime, name string) (*Info, error) {
	sandboxDir, err := RequireSandboxDir(name)
	if err != nil {
		return nil, err
	}

	meta, err := LoadMeta(sandboxDir)
	if err != nil {
		return nil, fmt.Errorf("load metadata: %w", err)
	}

	status, err := DetectStatus(ctx, rt, InstanceName(name), sandboxDir)
	if err != nil {
		return nil, err
	}

	changes := "-"
	if meta.Workdir.Mode == "copy" || meta.Workdir.Mode == "overlay" {
		workDir := WorkDir(name, meta.Workdir.HostPath)
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
				auxWorkDir := WorkDir(name, d.HostPath)
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

// InspectSandboxWithBackend loads metadata and optionally queries the runtime.
// If rt is nil, returns basic info (from meta.json and filesystem) with StatusUnavailable.
// If rt is available, performs full inspection including container state.
func InspectSandboxWithBackend(ctx context.Context, rt runtime.Runtime, name string) (*Info, error) {
	sandboxDir, err := RequireSandboxDir(name)
	if err != nil {
		return nil, err
	}

	meta, err := LoadMeta(sandboxDir)
	if err != nil {
		return nil, fmt.Errorf("load metadata: %w", err)
	}

	// If runtime is nil, return basic info with unavailable status
	if rt == nil {
		diskUsage := "-"
		if size, err := DirSize(sandboxDir); err == nil {
			diskUsage = FormatSize(size)
		}
		return &Info{
			Meta:       meta,
			Status:     StatusUnavailable,
			HasChanges: "-",
			DiskUsage:  diskUsage,
		}, nil
	}

	// Runtime available - perform full inspection
	status, err := DetectStatus(ctx, rt, InstanceName(name), sandboxDir)
	if err != nil {
		return nil, err
	}

	changes := "-"
	if meta.Workdir.Mode == "copy" || meta.Workdir.Mode == "overlay" {
		workDir := WorkDir(name, meta.Workdir.HostPath)
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
				auxWorkDir := WorkDir(name, d.HostPath)
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

// ListSandboxes scans ~/.yoloai/sandboxes/ and returns info for all sandboxes.
func ListSandboxes(ctx context.Context, rt runtime.Runtime) ([]*Info, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home directory: %w", err)
	}
	sandboxesDir := filepath.Join(home, ".yoloai", "sandboxes")

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
		info, err := InspectSandbox(ctx, rt, entry.Name())
		if err != nil {
			// Include broken sandboxes with minimal info
			result = append(result, &Info{
				Meta:       &Meta{Name: entry.Name()},
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
func ListSandboxesMultiBackend(ctx context.Context, newRuntimeFunc func(context.Context, string) (runtime.Runtime, error)) ([]*Info, []string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil, fmt.Errorf("get home directory: %w", err)
	}
	sandboxesDir := filepath.Join(home, ".yoloai", "sandboxes")

	entries, err := os.ReadDir(sandboxesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("read sandboxes directory: %w", err)
	}

	// Group sandboxes by backend
	backendSandboxes := make(map[string][]string)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		sandboxDir := filepath.Join(sandboxesDir, entry.Name())
		meta, err := LoadMeta(sandboxDir)
		if err != nil {
			// Broken sandbox - will be added with StatusBroken
			backendSandboxes[""] = append(backendSandboxes[""], entry.Name())
			continue
		}
		backend := meta.Backend
		if backend == "" {
			backend = "docker" // fallback for old sandboxes
		}
		backendSandboxes[backend] = append(backendSandboxes[backend], entry.Name())
	}

	var result []*Info
	var unavailableBackends []string
	unavailableSet := make(map[string]bool)

	// Process each backend group
	for backend, names := range backendSandboxes {
		if backend == "" {
			// Broken sandboxes
			for _, name := range names {
				result = append(result, &Info{
					Meta:       &Meta{Name: name},
					Status:     StatusBroken,
					HasChanges: "-",
					DiskUsage:  "-",
				})
			}
			continue
		}

		// Try to create runtime for this backend
		rt, err := newRuntimeFunc(ctx, backend)
		var runtimeAvailable bool
		if err == nil {
			runtimeAvailable = true
			defer rt.Close() //nolint:errcheck,gosec // best-effort cleanup
		} else if !unavailableSet[backend] {
			// Backend unavailable - track it
			unavailableBackends = append(unavailableBackends, backend)
			unavailableSet[backend] = true
		}

		// Inspect each sandbox in this backend group
		for _, name := range names {
			var info *Info
			if runtimeAvailable {
				info, err = InspectSandboxWithBackend(ctx, rt, name)
			} else {
				info, err = InspectSandboxWithBackend(ctx, nil, name)
			}

			if err != nil {
				// Broken sandbox
				result = append(result, &Info{
					Meta:       &Meta{Name: name},
					Status:     StatusBroken,
					HasChanges: "-",
					DiskUsage:  "-",
				})
				continue
			}
			result = append(result, info)
		}
	}

	return result, unavailableBackends, nil
}

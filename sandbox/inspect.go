package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
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
	StatusActive  Status = "active"  // container running, agent actively working
	StatusIdle    Status = "idle"    // container running, agent alive, bell flag set (finished processing)
	StatusDone    Status = "done"    // container running, agent exited cleanly (exit 0)
	StatusFailed  Status = "failed"  // container running, agent exited with error (non-zero)
	StatusStopped Status = "stopped" // container stopped (docker stop)
	StatusRemoved Status = "removed" // container removed but sandbox dir exists
	StatusBroken  Status = "broken"  // sandbox dir exists but meta.json missing/invalid
)

// DefaultIdleThreshold is retained for config/API compatibility but unused.
//
// Deprecated: idle detection uses the in-container status monitor.
const DefaultIdleThreshold = 30

// Info holds the combined metadata and live state for a sandbox.
type Info struct {
	Meta       *Meta  `json:"meta"`
	Status     Status `json:"status"`
	HasChanges string `json:"has_changes"` // "yes", "no", or "-" (unknown/not applicable)
	DiskUsage  string `json:"disk_usage"`  // human-readable size, e.g. "42.0MB"
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

// execInContainer runs a command inside a sandbox instance and returns stdout.
func execInContainer(ctx context.Context, rt runtime.Runtime, containerID string, cmd []string) (string, error) {
	result, err := rt.Exec(ctx, containerID, cmd, "yoloai")
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

// resetStatusToActive writes an "active" status to the sandbox's status.json.
// Called from the host side when delivering a new prompt to reset idle→active.
func resetStatusToActive(sandboxDir string) {
	s := statusJSON{
		Status:    "active",
		Timestamp: time.Now().Unix(),
	}
	data, err := json.Marshal(s)
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(sandboxDir, "status.json"), data, 0644) //nolint:gosec // status file is sandbox-controlled
}

// DetectStatus queries the runtime and status.json to determine sandbox status.
// sandboxDir is the host-side sandbox directory; if empty, only exec fallback is used.
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

	// Try status.json first (fast path — no exec)
	if sandboxDir != "" {
		data, readErr := os.ReadFile(filepath.Join(sandboxDir, "status.json")) //nolint:gosec // path is sandbox-controlled
		if readErr == nil && len(data) > 0 {
			if status, ok := parseStatusJSON(data); ok {
				return status, nil
			}
		}
	}

	// Fall back to exec-based detection (old sandboxes without status monitor)
	return detectStatusViaExec(ctx, rt, containerName)
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
	case "active", "running":
		// Accept both "active" (current) and "running" (old sandboxes) for
		// backward compatibility with status.json written by older versions.
		age := time.Since(time.Unix(s.Timestamp, 0))
		if age > statusFileStaleness {
			return "", false // stale — fall back to exec
		}
		return StatusActive, true

	case "idle":
		// Idle is a persistent state written once (by hook or monitor) and
		// cleared only by resetStatusToActive or agent exit. No staleness
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

// detectStatusViaExec falls back to exec-based tmux queries for old sandboxes
// without the in-container status monitor.
func detectStatusViaExec(ctx context.Context, rt runtime.Runtime, containerName string) (Status, error) {
	output, err := execInContainer(ctx, rt, containerName, []string{
		"tmux", "list-panes", "-t", "main", "-F", "#{pane_dead}|#{pane_dead_status}",
	})
	if err != nil {
		return StatusActive, nil
	}

	parts := strings.SplitN(strings.TrimSpace(output), "|", 2)
	if len(parts) < 2 {
		return StatusActive, nil
	}

	if parts[0] == "0" {
		return StatusActive, nil
	}

	// Pane is dead
	if parts[1] == "0" {
		return StatusDone, nil
	}
	return StatusFailed, nil
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

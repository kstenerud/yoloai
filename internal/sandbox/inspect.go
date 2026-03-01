package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kstenerud/yoloai/internal/runtime"
)

// Status represents the current state of a sandbox.
type Status string

// Status constants for sandbox lifecycle states.
const (
	StatusRunning Status = "running" // container running, agent alive in tmux
	StatusDone    Status = "done"    // container running, agent exited cleanly (exit 0)
	StatusFailed  Status = "failed"  // container running, agent exited with error (non-zero)
	StatusStopped Status = "stopped" // container stopped (docker stop)
	StatusRemoved Status = "removed" // container removed but sandbox dir exists
	StatusBroken  Status = "broken"  // sandbox dir exists but meta.json missing/invalid
)

// Info holds the combined metadata and live state for a sandbox.
type Info struct {
	Meta        *Meta  `json:"meta"`
	Status      Status `json:"status"`
	ContainerID string `json:"container_id,omitempty"` // 12-char short ID, empty if removed
	HasChanges  string `json:"has_changes"`            // "yes", "no", or "-" (unknown/not applicable)
	DiskUsage   string `json:"disk_usage"`             // human-readable size, e.g. "42.0MB"
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

// execInContainer runs a command inside a sandbox instance and returns stdout.
func execInContainer(ctx context.Context, rt runtime.Runtime, containerID string, cmd []string) (string, error) {
	result, err := rt.Exec(ctx, containerID, cmd, "yoloai")
	if err != nil {
		return "", err
	}
	return result.Stdout, nil
}

// DetectStatus queries the runtime and tmux to determine sandbox status.
func DetectStatus(ctx context.Context, rt runtime.Runtime, containerName string) (Status, string, error) {
	info, err := rt.Inspect(ctx, containerName)
	if err != nil {
		if errors.Is(err, runtime.ErrNotFound) {
			return StatusRemoved, "", nil
		}
		return "", "", fmt.Errorf("inspect container: %w", err)
	}

	if !info.Running {
		return StatusStopped, "", nil
	}

	// Query tmux pane state
	output, err := execInContainer(ctx, rt, containerName, []string{
		"tmux", "list-panes", "-t", "main", "-F", "#{pane_dead} #{pane_dead_status}",
	})
	if err != nil {
		// tmux query failed — default to running (safest assumption)
		return StatusRunning, "", nil
	}

	fields := strings.Fields(output)
	if len(fields) < 1 || fields[0] == "0" {
		return StatusRunning, "", nil
	}

	// Pane is dead
	if len(fields) >= 2 && fields[1] == "0" {
		return StatusDone, "", nil
	}
	return StatusFailed, "", nil
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

	status, containerID, err := DetectStatus(ctx, rt, InstanceName(name))
	if err != nil {
		return nil, err
	}

	workDir := WorkDir(name, meta.Workdir.HostPath)
	changes := detectChanges(workDir)

	// Also check aux :copy dirs for changes
	if changes == "no" {
		for _, d := range meta.Directories {
			if d.Mode == "copy" {
				auxWorkDir := WorkDir(name, d.HostPath)
				if detectChanges(auxWorkDir) == "yes" {
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
		Meta:        meta,
		Status:      status,
		ContainerID: containerID,
		HasChanges:  changes,
		DiskUsage:   diskUsage,
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

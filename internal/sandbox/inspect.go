package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/kstenerud/yoloai/internal/docker"
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
)

// Info holds the combined metadata and live state for a sandbox.
type Info struct {
	Meta        *Meta
	Status      Status
	ContainerID string // 12-char short ID, empty if removed
	HasChanges  string // "yes", "no", or "-" (unknown/not applicable)
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

// execInContainer runs a command inside a container and returns stdout.
func execInContainer(ctx context.Context, client docker.Client, containerID string, cmd []string) (string, error) {
	execResp, err := client.ContainerExecCreate(ctx, containerID, container.ExecOptions{
		Cmd:          cmd,
		User:         "yoloai",
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return "", fmt.Errorf("exec create: %w", err)
	}

	resp, err := client.ContainerExecAttach(ctx, execResp.ID, container.ExecAttachOptions{})
	if err != nil {
		return "", fmt.Errorf("exec attach: %w", err)
	}
	defer resp.Close()

	var stdout, stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, resp.Reader); err != nil {
		return "", fmt.Errorf("exec read: %w", err)
	}

	inspectResp, err := client.ContainerExecInspect(ctx, execResp.ID)
	if err != nil {
		return "", fmt.Errorf("exec inspect: %w", err)
	}
	if inspectResp.ExitCode != 0 {
		return "", fmt.Errorf("exec exited with code %d: %s", inspectResp.ExitCode, strings.TrimSpace(stderr.String()))
	}

	return strings.TrimSpace(stdout.String()), nil
}

// DetectStatus queries Docker and tmux to determine sandbox status.
func DetectStatus(ctx context.Context, client docker.Client, containerName string) (Status, string, error) {
	info, err := client.ContainerInspect(ctx, containerName)
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return StatusRemoved, "", nil
		}
		return "", "", fmt.Errorf("inspect container: %w", err)
	}

	shortID := info.ID
	if len(shortID) > 12 {
		shortID = shortID[:12]
	}

	if !info.State.Running {
		return StatusStopped, shortID, nil
	}

	// Query tmux pane state
	output, err := execInContainer(ctx, client, containerName, []string{
		"tmux", "list-panes", "-t", "main", "-F", "#{pane_dead} #{pane_dead_status}",
	})
	if err != nil {
		// tmux query failed — default to running (safest assumption)
		return StatusRunning, shortID, nil
	}

	fields := strings.Fields(output)
	if len(fields) < 1 || fields[0] == "0" {
		return StatusRunning, shortID, nil
	}

	// Pane is dead
	if len(fields) >= 2 && fields[1] == "0" {
		return StatusDone, shortID, nil
	}
	return StatusFailed, shortID, nil
}

// InspectSandbox loads metadata and queries Docker for a single sandbox.
func InspectSandbox(ctx context.Context, client docker.Client, name string) (*Info, error) {
	sandboxDir, err := RequireSandboxDir(name)
	if err != nil {
		return nil, err
	}

	meta, err := LoadMeta(sandboxDir)
	if err != nil {
		return nil, fmt.Errorf("load metadata: %w", err)
	}

	status, containerID, err := DetectStatus(ctx, client, ContainerName(name))
	if err != nil {
		return nil, err
	}

	workDir := WorkDir(name, meta.Workdir.HostPath)
	changes := detectChanges(workDir)

	return &Info{
		Meta:        meta,
		Status:      status,
		ContainerID: containerID,
		HasChanges:  changes,
	}, nil
}

// ListSandboxes scans ~/.yoloai/sandboxes/ and returns info for all sandboxes.
func ListSandboxes(ctx context.Context, client docker.Client) ([]*Info, error) {
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
		info, err := InspectSandbox(ctx, client, entry.Name())
		if err != nil {
			continue // skip broken sandboxes
		}
		result = append(result, info)
	}

	return result, nil
}

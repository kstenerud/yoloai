package containerdrt

// ABOUTME: Logs and diagnostic hints for the containerd backend.
// Logs are read from the bind-mounted log.txt in the sandbox directory
// (the containerd log API is not used — logs go to the file via the agent).

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Logs returns the last n lines of a container's log output.
// Reads from the sandbox's log.txt bind-mounted file, not from containerd's
// log API. Returns empty string if the log file does not exist.
func (r *Runtime) Logs(_ context.Context, name string, tail int) string {
	sandboxDir := sandboxDirForName(name)
	logPath := filepath.Join(sandboxDir, "log.txt")

	data, err := os.ReadFile(logPath) //nolint:gosec // G304: path is always a trusted sandbox subpath
	if err != nil {
		return ""
	}

	content := strings.TrimSpace(string(data))
	if content == "" {
		return ""
	}

	lines := strings.Split(content, "\n")
	if tail > 0 && len(lines) > tail {
		lines = lines[len(lines)-tail:]
	}

	return strings.Join(lines, "\n")
}

// DiagHint returns backend-specific diagnostic instructions.
func (r *Runtime) DiagHint(instanceName string) string {
	return fmt.Sprintf(
		"check containerd task status: ctr -n yoloai tasks ls\n"+
			"  check containerd logs: journalctl -u containerd\n"+
			"  check container logs: cat %s/log.txt",
		sandboxDirForName(instanceName),
	)
}

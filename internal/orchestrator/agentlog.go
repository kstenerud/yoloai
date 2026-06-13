// ABOUTME: Host-side read of a sandbox's raw agent terminal output
// ABOUTME: (logs/agent.log) — full or tail-N, ANSI bytes left intact.
package orchestrator

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/store"
)

// ReadAgentLog returns the raw agent terminal output for a sandbox.
// tailLines <= 0 returns the full file; otherwise the last tailLines lines.
// A missing log file is not an error — it returns ("", nil). ANSI escape
// sequences are left intact; presentation-layer stripping is the caller's job.
func ReadAgentLog(layout config.Layout, name string, tailLines int) (string, error) {
	path := store.AgentLogPath(layout.SandboxDir(name))
	f, err := os.Open(path) //nolint:gosec // G304: path is store.AgentLogPath(name) — yoloAI-owned
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("open agent log: %w", err)
	}
	defer f.Close() //nolint:errcheck // read-only file

	if tailLines <= 0 {
		data, err := io.ReadAll(f)
		if err != nil {
			return "", fmt.Errorf("read agent log: %w", err)
		}
		return string(data), nil
	}

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scan agent log: %w", err)
	}
	if len(lines) > tailLines {
		lines = lines[len(lines)-tailLines:]
	}
	return strings.Join(lines, "\n"), nil
}

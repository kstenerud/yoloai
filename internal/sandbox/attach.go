// ABOUTME: Library-side attach-readiness helpers. Polls sandbox.jsonl / tmux
// ABOUTME: has-session to know when a started sandbox is ready for tmux attach.

package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
)

// ReadTmuxSocket returns the tmux socket path recorded in the sandbox's
// runtime-config.json, or "" if the backend uses tmux's default per-user
// socket. Exposed for embedders that need to read the same socket as
// `yoloai sandbox <name> exec -- tmux -S <socket> ...`.
func ReadTmuxSocket(layout config.Layout, sandboxName string) string {
	data, err := os.ReadFile(store.RuntimeConfigFilePath(layout.SandboxDir(sandboxName))) //nolint:gosec // G304: path is layout-scoped sandbox dir
	if err != nil {
		return ""
	}
	var cfg struct {
		TmuxSocket string `json:"tmux_socket"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ""
	}
	return cfg.TmuxSocket
}

// WaitForAttachReady polls until the sandbox's agent has launched (or
// failed to launch) and the tmux session is reachable. Returns early on
// context cancel or when the container stops running. The polling
// strategy mirrors what the CLI's attach command needs:
//
//  1. Primary: read sandbox.jsonl on the host for the "sandbox.agent_launch"
//     or "sandbox.agent_not_found" event. This is the most reliable signal
//     because it's written by the agent-launch sequence AFTER lifecycle
//     commands finish.
//  2. Fallback: `tmux has-session -t main` via runtime.Exec. Only reached
//     when sandbox.jsonl is unreadable (very old container images).
//
// Moved from internal/cli/attach.go to this package as W-L8d's Sandbox.Attach
// preparation — Client.Attach needs the same orchestration that the CLI used
// to do directly. Embedders calling Client.Attach get readiness polling
// transparently.
func WaitForAttachReady(
	ctx context.Context,
	rt runtime.Runtime,
	layout config.Layout,
	sandboxName, user string,
	timeout time.Duration,
) error {
	containerName := store.InstanceName(sandboxName)
	jsonlPath := store.SandboxJSONLPath(layout.SandboxDir(sandboxName))
	tmuxSocket := ReadTmuxSocket(layout, sandboxName)
	deadline := time.Now().Add(timeout)
	var lastExecErr error
	for time.Now().Before(deadline) {
		ready, err := pollAttachReady(ctx, rt, containerName, jsonlPath, tmuxSocket, user)
		if err != nil {
			return err
		}
		if ready {
			return nil
		}
		lastExecErr = err
		if err := sleepOrCancel(ctx, 500*time.Millisecond); err != nil {
			return err
		}
	}
	slog.Debug("WaitForAttachReady: timed out", "event", "sandbox.wait_tmux.timeout", //nolint:gosec // G706: slog uses structured logging
		"container", containerName, "last_exec_err", lastExecErr)
	if logs := rt.Logs(ctx, containerName, 50); logs != "" {
		return fmt.Errorf("tmux session not ready after %s\n\nContainer logs:\n%s", timeout, logs)
	}
	return fmt.Errorf("tmux session not ready after %s", timeout)
}

// pollAttachReady performs one readiness check cycle. Returns (true, nil)
// when ready, (false, nil) to keep polling, or (false, err) on a hard
// error (container not running, context cancelled).
func pollAttachReady(
	ctx context.Context,
	rt runtime.Runtime,
	containerName, jsonlPath, tmuxSocket, user string,
) (bool, error) {
	if ctx.Err() != nil {
		return false, ctx.Err()
	}

	info, err := rt.Inspect(ctx, containerName)
	if err != nil || !info.Running {
		return false, fmt.Errorf("container %s is not running", containerName)
	}

	// Primary: sandbox.jsonl agent-launch event.
	if data, readErr := os.ReadFile(jsonlPath); readErr == nil { //nolint:gosec // G304: path from layout-scoped sandbox dir
		if bytes.Contains(data, []byte(`"sandbox.agent_launch"`)) ||
			bytes.Contains(data, []byte(`"sandbox.agent_not_found"`)) {
			return true, nil
		}
		// Log exists but agent not yet launched — keep polling.
		return false, nil
	}

	// Fallback: tmux has-session via exec.
	tmuxArgs := buildTmuxHasSessionArgs(tmuxSocket)
	_, execErr := rt.Exec(ctx, containerName, tmuxArgs, user)
	if execErr == nil {
		return true, nil
	}
	slog.Debug("WaitForAttachReady: exec check failed", "event", "sandbox.wait_tmux.exec_fail", //nolint:gosec // G706: slog uses structured logging
		"container", containerName, "err", execErr)
	return false, nil
}

// buildTmuxHasSessionArgs constructs the tmux has-session argument list.
func buildTmuxHasSessionArgs(tmuxSocket string) []string {
	args := []string{"tmux"}
	if tmuxSocket != "" {
		args = append(args, "-S", tmuxSocket)
	}
	return append(args, "has-session", "-t", "main")
}

// sleepOrCancel waits for the given duration or returns ctx.Err() if cancelled.
func sleepOrCancel(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

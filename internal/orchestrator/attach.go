// ABOUTME: Library-side attach-readiness helpers. Polls sandbox.jsonl / tmux
// ABOUTME: has-session to know when a started sandbox is ready for tmux attach.

package orchestrator

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/store"
)

// attachReadyTimeout bounds how long Attach waits for the agent's tmux session
// to come up before giving up. Human-scale: a started agent normally launches
// its session within seconds; 5 minutes covers a cold image pull/build.
const attachReadyTimeout = 300 * time.Second

// Attach connects io to the sandbox's tmux session and blocks until the user
// detaches (Ctrl-B d) or the agent exits. It owns the full interactive-attach
// orchestration — status gate, container/user resolution, attach-readiness
// poll, and the runtime attach exec — so the public Agent.Attach reduces to a
// TTY check plus this one call (mirroring CaptureTerminal/SendInput). The
// sandbox must be running (Active/Idle/Done/Failed); stopped sandboxes return
// ErrContainerNotRunning.
func (e *Engine) Attach(ctx context.Context, name string, io runtime.IOStreams) error {
	if err := e.ensure(ctx); err != nil {
		return err
	}
	info, err := e.Inspect(ctx, name)
	if err != nil {
		return err
	}
	if err := attachStatusOK(info.Status, name); err != nil {
		return err
	}
	user := ContainerUser(info.Environment, e.layout.HostUID)
	if err := WaitForAttachReady(ctx, e.runtime, e.layout, name, user, attachReadyTimeout); err != nil {
		return fmt.Errorf("waiting for tmux session: %w", err)
	}
	socket := runtime.TmuxSocketFor(e.runtime, e.layout.SandboxDir(name))
	cmd, ok := runtime.AttachCommandFor(e.runtime, socket, io.Rows, io.Cols, info.Environment.Isolation)
	if !ok {
		return fmt.Errorf("backend %s does not support interactive attach", e.runtime.Descriptor().Type)
	}
	return e.runtime.InteractiveExec(ctx, store.InstanceName(e.layout.Principal, name), cmd, user, "", io)
}

// attachStatusOK returns nil if the sandbox status permits attach, otherwise a
// typed error suitable for the CLI exit-code mapping.
func attachStatusOK(status Status, name string) error {
	switch status {
	case StatusActive, StatusIdle, StatusDone, StatusFailed:
		return nil
	default:
		// StatusStopped, StatusRemoved, StatusBroken, StatusUnavailable
		return fmt.Errorf("sandbox %q: %w", name, ErrContainerNotRunning)
	}
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
// Client.Attach needs the same readiness orchestration the CLI performs, so
// embedders calling Client.Attach get the polling transparently.
func WaitForAttachReady(
	ctx context.Context,
	rt runtime.Backend,
	layout config.Layout,
	sandboxName, user string,
	timeout time.Duration,
) error {
	containerName := store.InstanceName(layout.Principal, sandboxName)
	jsonlPath := store.SandboxJSONLPath(layout.SandboxDir(sandboxName))
	tmuxSocket := runtime.TmuxSocketFor(rt, layout.SandboxDir(sandboxName))
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
	slog.Debug("WaitForAttachReady: timed out", "event", "sandbox.wait_tmux.timeout",
		"container", containerName, "last_exec_err", lastExecErr)
	if logs := runtime.LogsFor(ctx, rt, containerName, 50); logs != "" {
		return fmt.Errorf("tmux session not ready after %s\n\nContainer logs:\n%s", timeout, logs)
	}
	return fmt.Errorf("tmux session not ready after %s", timeout)
}

// pollAttachReady performs one readiness check cycle. Returns (true, nil)
// when ready, (false, nil) to keep polling, or (false, err) on a hard
// error (container not running, context cancelled).
func pollAttachReady(
	ctx context.Context,
	rt runtime.Backend,
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
	slog.Debug("WaitForAttachReady: exec check failed", "event", "sandbox.wait_tmux.exec_fail",
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

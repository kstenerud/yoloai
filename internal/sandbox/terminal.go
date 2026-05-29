package sandbox

// ABOUTME: Non-interactive tmux capture-pane wrapper — grabs the rendered
// ABOUTME: agent terminal for diagnostics (bug reports, smoke-test preserves).

import (
	"context"
	"errors"
	"fmt"

	"github.com/kstenerud/yoloai/internal/sandbox/store"
)

// CaptureTerminal returns the rendered agent terminal — what `yoloai
// attach <name>` would show right now — by running `tmux capture-pane`
// inside the named sandbox via the runtime's non-interactive Exec
// surface. Returns the plain text (printable characters only) and the
// ANSI variant (with terminal-control escape sequences preserved); the
// ANSI form is useful when the failure cue is colour / cursor movement
// (e.g. a model's "thinking…" spinner that stops moving), the plain
// form when reading the agent's actual conversation.
//
// scrollback is the number of history lines to capture above the
// current viewport (tmux's -S flag). Pass 0 for just the visible
// pane; -1 for "every available line"; positive values for that many
// lines back (e.g. 200 ≈ 4-5 screens). DF3 settled on -200 as the
// useful diagnostic depth.
//
// Best-effort: when the sandbox isn't running, returns
// ErrContainerNotRunning so callers can preserve "no capture" without
// surfacing it as a hard failure. Other runtime errors are surfaced
// verbatim with the command's stderr/exit-code wrapped.
//
// DF3: this is the yoloai-level primitive that the bug-report writer
// and the smoke test's `_capture_terminal_snapshot` both call, so the
// "what does the agent's screen actually look like right now" capability
// stays in one place instead of being re-implemented per backend in
// scripts/smoke_test.py.
func (m *Engine) CaptureTerminal(ctx context.Context, name string, scrollback int) (plain, ansi []byte, err error) {
	info, err := m.Inspect(ctx, name)
	if err != nil {
		return nil, nil, err
	}
	if info.Status != StatusActive && info.Status != StatusIdle {
		return nil, nil, fmt.Errorf("sandbox %q: %w", name, ErrContainerNotRunning)
	}

	containerName := store.InstanceName(name)
	socket := m.runtime.TmuxSocket(m.layout.SandboxDir(name))
	user := ContainerUser(info.Meta, m.layout.HostUID)

	plain, err = m.capturePane(ctx, containerName, socket, user, scrollback, false)
	if err != nil {
		return nil, nil, fmt.Errorf("capture-pane plain: %w", err)
	}
	ansi, err = m.capturePane(ctx, containerName, socket, user, scrollback, true)
	if err != nil {
		// The plain capture worked — return what we have rather than failing
		// the whole call. Callers see ansi==nil and degrade gracefully.
		return plain, nil, fmt.Errorf("capture-pane ansi (plain succeeded): %w", err)
	}
	return plain, ansi, nil
}

// capturePane runs a single tmux capture-pane invocation via runtime.Exec
// and returns the captured bytes. The tmux socket is supplied explicitly
// when the backend's TmuxSocket() returns a non-empty path; for backends
// that auto-inject the socket internally (seatbelt), passing the path
// is still safe because tmux accepts repeated -S with last-one-wins
// semantics and both paths resolve to the same socket.
func (m *Engine) capturePane(ctx context.Context, containerName, socket, user string, scrollback int, ansi bool) ([]byte, error) {
	args := []string{"tmux"}
	if socket != "" {
		args = append(args, "-S", socket)
	}
	args = append(args, "capture-pane", "-p", "-t", "main")
	if scrollback != 0 {
		args = append(args, "-S", fmt.Sprintf("%d", -scrollback))
	}
	if ansi {
		args = append(args, "-e")
	}

	result, err := m.runtime.Exec(ctx, containerName, args, user)
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("tmux capture-pane exit %d", result.ExitCode)
	}
	return []byte(result.Stdout), nil
}

// ErrTerminalUnavailable is returned by CaptureTerminal when the sandbox
// is in a state from which tmux output cannot be captured (stopped,
// removed, never started). Callers preserving diagnostics should treat
// this as "best-effort skip" rather than a fatal error.
var ErrTerminalUnavailable = errors.New("terminal unavailable")
